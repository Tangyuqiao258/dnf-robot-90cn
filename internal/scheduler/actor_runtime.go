package scheduler

import (
	"fmt"
	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotaction "robot/internal/capability/robotaction"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	robottemplate "robot/internal/capability/robottemplate"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
	"runtime/debug"
	"strings"
	"time"
)

type RobotRuntime struct {
	manager  *RobotManager
	uidLocks *lockhub.RefHub
}

var _ actormodel.RobotRuntime = (*RobotRuntime)(nil)

func NewRobotRuntime(manager *RobotManager) *RobotRuntime {
	return &RobotRuntime{manager: manager, uidLocks: lockhub.NewRefHub()}
}

func (r *RobotRuntime) Config() robotconfig.RuntimeConfig {
	return r.manager.loadRobotConfig()
}

func (r *RobotRuntime) Status(uid int) (robotcap.RuntimeStatus, bool) {
	return r.manager.runtimeStatus(uid)
}

func (r *RobotRuntime) PartyActive(uid int) bool {
	if provider, ok := r.manager.actions.(interface{ PartyActive(int) bool }); ok {
		return provider.PartyActive(uid)
	}
	return false
}

func (r *RobotRuntime) IsActive(uid int) bool {
	st, ok := r.Status(uid)
	if !ok {
		return false
	}
	return robotcap.ActiveRuntimeStatus(st)
}

func (r *RobotRuntime) FinishStoreState(uid, cid int, reason string) {
	r.manager.finishStoreState(uid, cid, reason)
}

func (r *RobotRuntime) AddAutoOnline(success, failed int) {
	r.manager.addAutoOnline(success, failed)
}

func (r *RobotRuntime) AddOnlineAttempt(success bool) {
	r.manager.addAutoOnlineAttempt(success)
}

// TryAcquireOnlineAttempt and ReleaseOnlineAttempt expose the scheduler-owned
// online attempt gate to the actor model. The actor reaches the runtime through
// this type, so the methods must live here and not only on RobotManager.
func (r *RobotRuntime) TryAcquireOnlineAttempt() bool {
	return r.manager.TryAcquireOnlineAttempt()
}

func (r *RobotRuntime) ReleaseOnlineAttempt() {
	r.manager.ReleaseOnlineAttempt()
}

func (r *RobotRuntime) AutoActionsEnabled(rc robotconfig.RuntimeConfig) bool {
	return r.manager.autoActionsEnabled(rc)
}

func (r *RobotRuntime) RandomShoutMessage(randIntn func(int) int) string {
	tpl := r.manager.loadShoutTemplates()
	if len(tpl.Messages) == 0 {
		return ""
	}
	idx := 0
	if randIntn != nil {
		idx = randIntn(len(tpl.Messages))
	}
	return robottemplate.SafeShoutMessage(tpl.Messages[idx])
}

func (r *RobotRuntime) OnlineNoConfirm(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		res, err := r.manager.sessionService().Online(robotcap.CommandRequest{UIDs: []int{uid}}, false, r.Config())
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) Logout(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		res, err := r.manager.sessionService().LogoutUID(uid)
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) ForceClose(uid int) bool {
	if r == nil || r.manager == nil || r.manager.sessions == nil {
		return false
	}
	return r.manager.sessions.ForceClose(uid)
}

func (r *RobotRuntime) Move(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityTownMove); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		res, err := r.manager.moveService().Move(robotcap.CommandRequest{UIDs: []int{uid}}, r.Config())
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) Shout(uid int, world bool) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if world {
			if err := r.manager.requireBackendCapability(shared.CapabilityWorldShout); err != nil {
				return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
			}
		} else if err := r.manager.requireBroadcastShout(); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		res, err := r.manager.shoutService().ShoutOne(robotcap.CommandRequest{UIDs: []int{uid}}, world)
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) Store(uid int) robotcap.ActionResult {
	if !r.manager.itemStoreSupported() {
		// Expert-job-only adapters route the manual store command to the
		// disassembler machine or enchanter stall instead of the unavailable
		// private item stall.
		return r.run(uid, func() robotcap.ActionResult {
			st, ok := r.Status(uid)
			if !ok || !robotcap.ActiveRuntimeStatus(st) || st.PartyActive || r.PartyActive(uid) {
				return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
			}
			return r.autoExpertJobStore(uid, st, nil)
		})
	}
	return r.run(uid, func() robotcap.ActionResult {
		res, err := r.manager.storeWorkflow().Store(robotcap.CommandRequest{UIDs: []int{uid}})
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) AutoMove(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityTownMove); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		rc := r.Config()
		maps := r.manager.loadMapCatalog()
		target, hasTarget := r.manager.currentFollowTarget(uid, rc, maps)
		// A party owns its member's movement, so a member with nowhere to
		// follow still stands still. Having a leader position to walk to is the
		// one case where that rule must not block the move.
		if !hasTarget && (st.PartyActive || r.PartyActive(uid)) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		info := robotcap.Info{UID: st.UID, CID: st.CID, GuildID: st.GuildID, Village: st.Village, Area: st.Area, X: st.X, Y: st.Y}
		var err error
		if hasTarget {
			err = r.manager.moveService().AutoMove(info, rc, maps, &target)
		} else {
			err = r.manager.moveService().AutoMove(info, rc, maps, nil)
		}
		if err != nil {
			r.manager.addAutoMove(0, 1)
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		r.manager.addAutoMove(1, 0)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateMoved}
	})
}

func (r *RobotRuntime) AutoShout(uid int, world bool, msg string) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if world {
			if err := r.manager.requireBackendCapability(shared.CapabilityWorldShout); err != nil {
				world = false
			}
		}
		if !world {
			if err := r.manager.requireBroadcastShout(); err != nil {
				return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
			}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			r.manager.addAutoShoutChannel(world, 0, 1)
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		tpl := r.manager.loadShoutTemplates()
		if msg == "" && len(tpl.Messages) > 0 {
			msg = robottemplate.SafeShoutMessage(tpl.Messages[0])
		}
		if err := r.manager.shoutService().AutoShout(uid, msg, world); err != nil {
			r.manager.addAutoShoutChannel(world, 0, 1)
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		r.manager.addAutoShoutChannel(world, 1, 0)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent}
	})
}

func (r *RobotRuntime) AutoStore(uid int, shouldStop func() bool) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityStore); err != nil {
			robotLogf("[STORE_CAPABILITY_BLOCKED] uid=%d err=%v\n", uid, err)
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		if shouldStop != nil && shouldStop() {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled}
		}
		expert, releaseStoreType := r.manager.beginAdaptiveStoreType()
		defer releaseStoreType()
		if expert {
			robotLogf("[STORE_EXPERT_ATTEMPT] uid=%d cid=%d\n", uid, st.CID)
			return r.autoExpertJobStore(uid, st, shouldStop)
		}
		return r.autoItemStore(st, shouldStop)
	})
}

func (r *RobotRuntime) autoItemStore(st robotcap.RuntimeStatus, shouldStop func() bool) robotcap.ActionResult {
	switch r.manager.storeWorkflow().AutoUntilSuccess(st, r.Config(), shouldStop) {
	case storecap.AutoAttemptSuccess:
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: true, State: robotcap.ActionStateStore}
	case storecap.AutoAttemptBusy:
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateStoreBusy}
	case storecap.AutoAttemptCancelled:
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled}
	}
	if shouldStop != nil && shouldStop() {
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled}
	}
	return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateStoreFailed}
}

// claimStorePoint prefers a point in the robot's current area so store
// attempts avoid a server-side area transition under load, then falls back to
// the globally balanced pool.
func (r *RobotRuntime) claimStorePoint(points *storecap.PointCoordinator, uid int, rc robotconfig.RuntimeConfig, info robotcap.Info, allowed func(storecap.Position) bool) (storecap.Position, bool) {
	preferVillage, preferArea := info.Village, info.Area
	if status, ok := r.manager.runtimeStatus(uid); ok {
		preferVillage, preferArea = status.Village, status.Area
	}
	if pos, ok := points.ClaimForStoreInAreaWhere(uid, rc.AutoStoreDurationSec, preferVillage, preferArea, allowed); ok {
		return pos, true
	}
	return points.ClaimForStoreWhere(uid, rc.AutoStoreDurationSec, allowed)
}

func (r *RobotRuntime) autoExpertJobStore(uid int, st robotcap.RuntimeStatus, shouldStop func() bool) robotcap.ActionResult {
	rc := r.Config()
	kind := r.manager.expertJobStoreKindForUID(uid, rc)
	info := robotcap.Info{UID: uid, CID: st.CID, Village: st.Village, Area: st.Area, X: st.X, Y: st.Y, Port: r.manager.cfg.RobotGamePort}
	if robots, err := r.manager.selectRobots(robotcap.CommandRequest{UIDs: []int{uid}}); err == nil && len(robots) > 0 {
		info = robots[0]
		info.Port = r.manager.cfg.RobotGamePort
	}
	if !r.manager.beginStoreBusy(uid) {
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateStoreBusy}
	}
	// The proven baseline shared the normal store concurrency window. A later
	// independent cap of four made disjoint preparation visibly starve.
	releaseSlot, ok := r.manager.acquireAutoStoreSlot(rc)
	if !ok {
		r.manager.endStoreBusy(uid)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateStoreBusy}
	}
	defer func() {
		releaseSlot()
		r.manager.endStoreBusy(uid)
	}()

	points := r.manager.storePoints()
	tries := rc.AutoStoreMaxPositionTries
	if tries <= 0 {
		tries = 10
	}
	var failureState storecap.AttemptFailureState
	reuseSession := false
	setAreaStalls := 0
	allowedPosition := func(pos storecap.Position) bool { return shared.GenericAreaAllowed(info.GuildID, pos.Village) }
	// Adapters whose server cannot confirm cross-town transitions restrict the
	// point pool to the robot's current town.
	if placement, ok := r.manager.storePolicy.(shared.BackendStorePlacementPolicy); ok && placement.StoreSameTownOnly() {
		sameTown := info.Village
		base := allowedPosition
		allowedPosition = func(pos storecap.Position) bool { return pos.Village == sameTown && base(pos) }
	}
	for try := 1; try <= tries; try++ {
		if shouldStop != nil && shouldStop() {
			points.DiscardAttemptFailure(uid, &failureState)
			points.Flush()
			r.cleanupStoreSession(info, rc, "cancelled")
			return robotcap.ActionResult{UID: uid, CID: info.CID, OK: false, State: robotcap.ActionStateCancelled}
		}
		pos, ok := r.claimStorePoint(points, uid, rc, info, allowedPosition)
		if !ok {
			break
		}
		info.Village, info.Area, info.X, info.Y = pos.Village, pos.Area, pos.X, pos.Y
		var reason string
		if reuseSession {
			ok, reason = r.tryExpertStorePositionInCurrentSession(info, kind, shouldStop)
		} else {
			ok, reason = r.tryExpertStorePosition(info, rc, kind, shouldStop)
		}
		if ok {
			points.CommitAttemptFailure(uid, &failureState)
			points.Report(uid, pos, true, storecap.StoreReasonDisjointAck)
			r.manager.addAutoStore(1, 0, 0)
			robotLogf("[STORE_SUCCESS_POINT] uid=%d kind=%s point=%s village=%d area=%d x=%d y=%d try=%d\n", uid, kind.Name(), pos.PointID, pos.Village, pos.Area, pos.X, pos.Y, try)
			return robotcap.ActionResult{UID: uid, CID: info.CID, OK: true, State: robotcap.ActionStateStore}
		}
		if reason == "cancelled" {
			points.DiscardAttemptFailure(uid, &failureState)
			points.Discard(uid, pos)
			points.Flush()
			r.cleanupStoreSession(info, rc, reason)
			return robotcap.ActionResult{UID: uid, CID: info.CID, OK: false, State: robotcap.ActionStateCancelled}
		}
		if reason == "" {
			reason = expertJobFailedReason(kind)
		}
		robotLogf("[STORE_TRY_FAILED] uid=%d cid=%d kind=%s try=%d/%d point=%s reason=%s\n",
			uid, info.CID, kind.Name(), try, tries, pos.PointID, reason)
		if points.ReportAttemptFailure(uid, &failureState, pos, reason) {
			robotLogf("[STORE_SESSION_POINT_FAILURE] uid=%d cid=%d kind=%s try=%d/%d point=%s reason=%s\n",
				uid, info.CID, kind.Name(), try, tries, pos.PointID, reason)
			break
		}
		if reason == "set_area_failed" {
			setAreaStalls++
			if setAreaStalls >= disjointSetAreaStallLimit {
				robotLogf("[STORE_SET_AREA_STALLED] uid=%d cid=%d kind=%s try=%d/%d\n", uid, info.CID, kind.Name(), try, tries)
				break
			}
		}
		reuseSession = r.manager.expertJobStoreReasonRetryable(kind, reason)
		if !reuseSession {
			break
		}
	}
	points.CommitAttemptFailure(uid, &failureState)
	points.Flush()
	r.cleanupStoreSession(info, rc, expertJobFailedReason(kind))
	r.manager.addAutoStore(0, 1, 0)
	return robotcap.ActionResult{UID: uid, CID: info.CID, OK: false, State: robotcap.ActionStateStoreFailed}
}

// AutoAuction runs one auction round for an already-online robot: it tops the
// character's own listings up to the configured count and buys player listings
// that sit under the price model. The backend owns the whole round, so this
// only gates it: the capability must be enabled, the session must be running
// and outside a stall, and the automatic schedule must be on.
func (r *RobotRuntime) AutoAuction(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityMarket); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		if st.RobotType == 2 || st.RobotType == 3 || st.StoreDisplayAck {
			// Never disturb an active stall; the action timer retries later.
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "store_active"}
		}
		rc := r.Config()
		if !rc.AuctionEnabled {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "disabled"}
		}
		runner, ok := r.manager.actions.(interface {
			AuctionRound(int, robotconfig.RuntimeConfig) (int, int, error)
		})
		if !ok {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "auction_unsupported"}
		}
		listed, bought, err := runner.AuctionRound(uid, rc)
		if err != nil {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		return robotcap.ActionResult{
			UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent,
			Message: fmt.Sprintf("listed=%d bought=%d", listed, bought),
		}
	})
}

// AutoMail runs one mailbox round for an already-online robot: it claims what
// the server is holding and deletes the letters that leaves empty. Auction
// settlement arrives this way, so without this round a robot never receives the
// stack it bought or the gold it sold for.
//
// The gates mirror AutoAuction: the capability must be enabled, the session must
// be running and outside a stall, and the automatic schedule must be on. The
// round takes no policy of its own -- it claims whatever is addressed to the
// character and deletes only what it drained itself -- so the config only
// decides whether it runs.
func (r *RobotRuntime) AutoMail(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityMailNotification); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		if st.RobotType == 2 || st.RobotType == 3 || st.StoreDisplayAck {
			// Never disturb an active stall; the action timer retries later.
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "store_active"}
		}
		rc := r.Config()
		if !rc.MailEnabled {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "disabled"}
		}
		runner, ok := r.manager.actions.(interface {
			MailRound(int) (int, int, error)
		})
		if !ok {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "mail_unsupported"}
		}
		claimed, deleted, err := runner.MailRound(uid)
		if err != nil {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		return robotcap.ActionResult{
			UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent,
			Message: fmt.Sprintf("claimed=%d deleted=%d", claimed, deleted),
		}
	})
}

// AutoEmote sets one robot's mood/emotion index. The stored index is a
// persistent character trait that other players read off the actor row rather
// than a live broadcast, so the action is one op254 round trip and nothing
// else: there is no round to drain and no opinion about which index is right,
// which is why the config only bounds the range and paces the timer.
//
// The gates mirror AutoMail, including the party check. A party owns its
// member's state, and the server answers a mood change with a scene-object
// rebuild of the acting session, which is not a refresh a follower needs while
// it is being driven.
func (r *RobotRuntime) AutoEmote(uid int, emotionIndex uint16) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityEmote); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		if st.RobotType == 2 || st.RobotType == 3 || st.StoreDisplayAck {
			// Never disturb an active stall; the action timer retries later.
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "store_active"}
		}
		rc := r.Config()
		if !rc.AutoEmote {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "disabled"}
		}
		emoter, ok := r.manager.actions.(interface {
			ChangeEmotion(int, uint16) (uint16, error)
		})
		if !ok {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "emote_unsupported"}
		}
		stored, err := emoter.ChangeEmotion(uid, emotionIndex)
		if err != nil {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		return robotcap.ActionResult{
			UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent,
			Message: fmt.Sprintf("emotion=%d", stored),
		}
	})
}

// AutoServerNotice runs one fleet-paced server-notice action: it prepares the
// trigger stock in an offline window when the live probe fails, re-logs the
// robot through the adaptive online gate, then sends the trigger packet and
// records the observed broadcast.
func (r *RobotRuntime) AutoServerNotice(uid int, shouldStop func() bool) robotcap.ActionResult {
	return r.runServerNotice(uid, shouldStop, true)
}

// ForceServerNotice runs one operator-requested notice action without the
// fleet pacing gate and without requiring the automatic schedule switch. It
// still prepares missing stock through the offline cycle and honors the
// adaptive online gate.
func (r *RobotRuntime) ForceServerNotice(uid int) robotcap.ActionResult {
	return r.runServerNotice(uid, nil, false)
}

func (r *RobotRuntime) runServerNotice(uid int, shouldStop func() bool, paced bool) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityServerNotice); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		if paced && (st.RobotType == 2 || st.RobotType == 3 || st.StoreDisplayAck) {
			// Never disturb an active stall; the action timer retries later.
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "store_active"}
		}
		if shouldStop != nil && shouldStop() {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled}
		}
		rc := r.Config()
		if paced && rc.ServerNoticeVillage > 0 && st.Village != rc.ServerNoticeVillage {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "area_filtered"}
		}
		if paced && rc.ServerNoticeArea >= 0 && st.Area != rc.ServerNoticeArea {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "area_filtered"}
		}
		if paced && !rc.AutoServerNotice {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "disabled"}
		}
		// The runtime status does not carry the character id; the state
		// directory owns the UID/CID mapping used by every adapter write.
		cid := st.CID
		if robots, err := r.manager.selectRobots(robotcap.CommandRequest{UIDs: []int{uid}}); err == nil && len(robots) > 0 {
			cid = robots[0].CID
		}
		if cid <= 0 {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: "cid_missing"}
		}
		runtime := r.manager.serverNoticeRuntime()
		writer := r.manager.serverNoticeStockWriter()
		if runtime == nil || writer == nil {
			r.manager.addServerNotice(0, 1, 0)
			return robotcap.ActionResult{UID: uid, CID: cid, OK: false, State: robotcap.ActionStateFailed, Message: "port_unavailable"}
		}
		if paced && !r.manager.claimServerNoticeSlot(rc, st.Village, st.Area) {
			return robotcap.ActionResult{UID: uid, CID: cid, OK: false, State: robotcap.ActionStateCancelled, Message: "not_due"}
		}
		kind := r.manager.serverNoticeKind(rc)
		ready, err := writer.ServerNoticeStockReady(cid, kind)
		if err != nil || !ready {
			if ok, reason := r.prepareServerNoticeStock(uid, cid, rc, kind, shouldStop); !ok {
				r.manager.addServerNotice(0, 1, 0)
				if reason == "cancelled" {
					return robotcap.ActionResult{UID: uid, CID: cid, OK: false, State: robotcap.ActionStateCancelled}
				}
				robotLogf("[SERVER_NOTICE_PREPARE_FAILED] uid=%d cid=%d kind=%s reason=%s err=%v\n", uid, cid, kind.Name(), reason, err)
				return robotcap.ActionResult{UID: uid, CID: cid, OK: false, State: robotcap.ActionStateStorePrepareFailed, Message: reason}
			}
		}
		result, err := runtime.TriggerServerNotice(shared.ServerNoticeTriggerRequest{UID: uid, CID: cid, Kind: kind})
		if err == nil && !result.Accepted && strings.HasPrefix(result.Reason, "rejected") {
			// The server rolled the action back (its SQLite commit competed
			// with the login storm). The consumed stock is unchanged, so one
			// short retry is safe and recovers most transient rejections.
			robotLogf("[SERVER_NOTICE_RETRY] uid=%d cid=%d kind=%s reason=%s\n", uid, cid, kind.Name(), result.Reason)
			if storecap.SleepWithStop(3*time.Second, shouldStop) {
				return robotcap.ActionResult{UID: uid, CID: cid, OK: false, State: robotcap.ActionStateCancelled}
			}
			result, err = runtime.TriggerServerNotice(shared.ServerNoticeTriggerRequest{UID: uid, CID: cid, Kind: kind})
		}
		if err != nil {
			r.manager.addServerNotice(0, 1, 0)
			robotLogf("[SERVER_NOTICE_FAILED] uid=%d cid=%d kind=%s reason=%s err=%v\n", uid, cid, kind.Name(), result.Reason, err)
			return robotcap.ActionResult{UID: uid, CID: cid, OK: false, State: robotcap.ActionStateFailed, Message: firstNonEmpty(result.Reason, err.Error())}
		}
		if result.Broadcast {
			r.manager.addServerNotice(1, 0, 1)
		} else if result.Accepted {
			r.manager.addServerNotice(1, 0, 0)
		} else {
			r.manager.addServerNotice(0, 1, 0)
		}
		robotLogf("[SERVER_NOTICE] kind=%s uid=%d cid=%d sent=%t accepted=%t broadcast=%t item=0x%X level=%d reason=%s\n",
			result.Kind.Name(), result.UID, result.CID, result.Sent, result.Accepted, result.Broadcast, result.ItemID, result.Level, result.Reason)
		if !result.Sent || !result.Accepted {
			return robotcap.ActionResult{UID: uid, CID: cid, OK: false, State: robotcap.ActionStateFailed, Message: firstNonEmpty(result.Reason, "rejected")}
		}
		r.manager.invalidateRuntimeStatusCache()
		return robotcap.ActionResult{UID: uid, CID: cid, OK: true, State: robotcap.ActionStateNoticed}
	})
}

// prepareServerNoticeStock runs the offline stock write and re-login cycle.
// The stock only loads at character select, so it must run between logout and
// the next login; the adaptive online gate keeps the reconnect inside the same
// budget as every other login.
func (r *RobotRuntime) prepareServerNoticeStock(uid, cid int, rc robotconfig.RuntimeConfig, kind shared.ServerNoticeKind, shouldStop func() bool) (bool, string) {
	writer := r.manager.serverNoticeStockWriter()
	if writer == nil {
		return false, "port_unavailable"
	}
	closed := false
	closeDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(closeDeadline) {
		if r.ForceClose(uid) {
			closed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !closed {
		return false, "logout_failed"
	}
	r.manager.markSessionLogout(uid, time.Now())
	if err := r.manager.invalidateClosedCharacterCache(uid); err != nil {
		return false, "cache_invalidation_failed"
	}
	cancelled, err := r.manager.waitAccountOffline(uid, shouldStop)
	if err != nil {
		return false, "offline_failed"
	}
	if cancelled {
		return false, "cancelled"
	}
	if err := writer.EnsureServerNoticeStock(cid, kind); err != nil {
		robotLogf("[SERVER_NOTICE_STOCK_ERROR] uid=%d cid=%d kind=%s err=%v\n", uid, cid, kind.Name(), err)
		return false, "stock_write_failed"
	}
	if !r.manager.acquireOnlineAttemptWait(storeOnlineGateTimeout, shouldStop) {
		return false, "online_gate_busy"
	}
	online, err := r.manager.sessionService().Online(robotcap.CommandRequest{UIDs: []int{uid}}, true, rc)
	r.manager.ReleaseOnlineAttempt()
	if err != nil || online.Confirmed != 1 {
		robotLogf("[SERVER_NOTICE_ONLINE_ERROR] uid=%d cid=%d kind=%s confirmed=%d failed=%d err=%v\n",
			uid, cid, kind.Name(), online.Confirmed, online.Failed, err)
		return false, "online_failed"
	}
	r.manager.invalidateRuntimeStatusCache()
	// The inventory projection lands with the select-character ack; a short
	// settle window keeps the trigger behind that load, mirroring the stall
	// workflow.
	if storecap.SleepWithStop(time.Second, shouldStop) {
		return false, "cancelled"
	}
	return true, ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// expertJobFailedReason is the generic stop reason for one stall kind.
func expertJobFailedReason(kind shared.ExpertJobStoreKind) string {
	if kind == shared.ExpertJobStoreEnchant {
		return "enchant_failed"
	}
	return "disjoint_failed"
}

func (r *RobotRuntime) tryExpertStorePosition(info robotcap.Info, rc robotconfig.RuntimeConfig, kind shared.ExpertJobStoreKind, shouldStop func() bool) (bool, string) {
	// A prepared expert with a durable stall opens the next store on the live
	// session. The offline profession cycle below only runs when the probe
	// fails, the stall needs its endurance refreshed, or the live area cannot
	// host a point: the next login lets the spawn repair relocate the robot
	// instead of forcing a cross-area transition under load.
	if points := r.manager.storePoints(); points != nil && points.HasArea(info.Village, info.Area) {
		if writer := r.manager.expertJobWriter(); writer != nil {
			if ready, err := expertJobProfessionReady(writer, kind, info.CID); err == nil && ready {
				return r.tryExpertStorePositionInCurrentSession(info, kind, shouldStop)
			}
		}
	}
	// The first coordinate establishes the account session. After the
	// transactional profession and position writes, NoCache is the reload
	// boundary before CREATE_EXPERT_JOB_STORE. Coordinate-only retries stay on
	// this session.
	closed := false
	closeDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(closeDeadline) {
		if r.ForceClose(info.UID) {
			closed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !closed {
		robotLogf("[STORE_LOGOUT_ERROR] uid=%d cid=%d kind=%s reason=direct_close_timeout\n", info.UID, info.CID, kind.Name())
		return false, "logout_failed"
	}
	r.manager.markSessionLogout(info.UID, time.Now())
	if err := r.manager.invalidateClosedCharacterCache(info.UID); err != nil {
		robotLogf("[STORE_CACHE_INVALIDATION_ERROR] uid=%d cid=%d kind=%s phase=logout err=%v\n", info.UID, info.CID, kind.Name(), err)
		return false, "cache_invalidation_failed"
	}
	cancelled, err := r.manager.waitAccountOffline(info.UID, shouldStop)
	if err != nil {
		robotLogf("[STORE_OFFLINE_ERROR] uid=%d cid=%d kind=%s err=%v\n", info.UID, info.CID, kind.Name(), err)
		return false, "offline_failed"
	}
	if cancelled {
		return false, "cancelled"
	}
	// Expert-job persistence is adapter-owned: the profession rows load at
	// character select, so the write must happen between logout and the next
	// login. A backend without this port keeps the capability disabled.
	writer := r.manager.expertJobWriter()
	if writer == nil {
		robotLogf("[STORE_PERSISTENCE_UNAVAILABLE] uid=%d cid=%d kind=%s\n", info.UID, info.CID, kind.Name())
		return false, "profession_failed"
	}
	if err := ensureExpertJobProfession(writer, kind, info.CID); err != nil {
		robotLogf("[STORE_PROFESSION_ERROR] uid=%d cid=%d kind=%s err=%v\n", info.UID, info.CID, kind.Name(), err)
		return false, "profession_failed"
	}
	// Share the scheduler's adaptive login budget: store reconnects must not
	// bypass the breaker or the in-flight cap that protects the server.
	if !r.manager.acquireOnlineAttemptWait(storeOnlineGateTimeout, shouldStop) {
		robotLogf("[STORE_ONLINE_GATE_BUSY] uid=%d cid=%d kind=%s\n", info.UID, info.CID, kind.Name())
		return false, "online_gate_busy"
	}
	online, err := r.manager.sessionService().Online(robotcap.CommandRequest{UIDs: []int{info.UID}}, true, rc)
	r.manager.ReleaseOnlineAttempt()
	if err != nil || online.Confirmed != 1 {
		robotLogf("[STORE_ONLINE_ERROR] uid=%d kind=%s confirmed=%d failed=%d err=%v\n", info.UID, kind.Name(), online.Confirmed, online.Failed, err)
		return false, "online_failed"
	}
	r.manager.invalidateRuntimeStatusCache()
	return r.tryExpertStorePositionInCurrentSession(info, kind, shouldStop)
}

// expertJobProfessionReady probes one stall profession in the adapter's
// persistence boundary.
func expertJobProfessionReady(writer BackendExpertJobProfessionWriter, kind shared.ExpertJobStoreKind, cid int) (bool, error) {
	if kind == shared.ExpertJobStoreEnchant {
		return writer.EnchantProfessionReady(cid)
	}
	return writer.DisjointProfessionReady(cid)
}

// ensureExpertJobProfession writes one stall profession in the adapter's
// persistence boundary.
func ensureExpertJobProfession(writer BackendExpertJobProfessionWriter, kind shared.ExpertJobStoreKind, cid int) error {
	if kind == shared.ExpertJobStoreEnchant {
		return writer.EnsureEnchantProfession(cid)
	}
	return writer.EnsureDisjointProfession(cid)
}

func (r *RobotRuntime) tryExpertStorePositionInCurrentSession(info robotcap.Info, kind shared.ExpertJobStoreKind, shouldStop func() bool) (bool, string) {
	st, ok := r.manager.runtimeStatus(info.UID)
	if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 {
		return false, "runtime_stopped"
	}
	if points := r.manager.storePoints(); points == nil || !points.HasArea(info.Village, info.Area) {
		return false, "set_area_failed"
	}
	runtime := r.manager.storeSessionRuntime()
	if runtime == nil {
		return false, "set_area_failed"
	}
	if !runtime.SetAreaFrom(info.UID, info.Village, info.Area, info.X, info.Y, st.Village, st.Area) {
		return false, "set_area_failed"
	}
	if storecap.SleepWithStop(1800*time.Millisecond, shouldStop) {
		return false, "cancelled"
	}
	if !runtime.StartExpertJobStore(info.UID, kind, r.manager.expertJobStoreCost(kind)) {
		return false, "start_failed"
	}
	r.manager.invalidateRuntimeStatusCache()
	robotLogf("[STORE_SENT] uid=%d cid=%d kind=%s from=%d/%d to=%d/%d/%d/%d\n",
		info.UID, info.CID, kind.Name(), st.Village, st.Area, info.Village, info.Area, info.X, info.Y)
	return r.waitExpertStoreResult(info, kind, shouldStop, false)
}

// expertStoreSent/directAck/active/lastError read the per-kind status fields.
func expertStoreSent(st robotcap.RuntimeStatus, kind shared.ExpertJobStoreKind) bool {
	if kind == shared.ExpertJobStoreEnchant {
		return st.EnchantCreateSent
	}
	return st.DisjointCreateSent
}

func expertStoreDirectAck(st robotcap.RuntimeStatus, kind shared.ExpertJobStoreKind) bool {
	if kind == shared.ExpertJobStoreEnchant {
		return st.EnchantDirectAck
	}
	return st.DisjointDirectAck
}

func expertStoreActive(st robotcap.RuntimeStatus, kind shared.ExpertJobStoreKind) bool {
	if kind == shared.ExpertJobStoreEnchant {
		return st.EnchantActive
	}
	return st.DisjointActive
}

func expertStoreLastError(st robotcap.RuntimeStatus, kind shared.ExpertJobStoreKind) byte {
	if kind == shared.ExpertJobStoreEnchant {
		return st.LastEnchantError
	}
	return st.LastDisjointError
}

func (r *RobotRuntime) waitExpertStoreResult(info robotcap.Info, kind shared.ExpertJobStoreKind, shouldStop func() bool, allowCompatibilitySend bool) (bool, string) {
	deadline := time.Now().Add(20 * time.Second)
	sawRunning := false
	runningSince := time.Time{}
	compatibilitySendTried := !allowCompatibilitySend
	for time.Now().Before(deadline) {
		if shouldStop != nil && shouldStop() {
			return false, "cancelled"
		}
		if st, ok := r.manager.runtimeStatus(info.UID); ok {
			if st.DisconnectReason != 0 {
				robotLogf("[STORE_RUNTIME_STOPPED] uid=%d kind=%s state=%s disconnect=%d type=%d sent=%t direct_ack=%t active=%t last_error=%d\n",
					info.UID, kind.Name(), st.StateName, st.DisconnectReason, st.RobotType, expertStoreSent(st, kind), expertStoreDirectAck(st, kind), expertStoreActive(st, kind), expertStoreLastError(st, kind))
				return false, "runtime_stopped"
			}
			if st.StateName != robotcap.RuntimeStateRunning {
				// The store open is intentionally asynchronous. init/login
				// before the first StateRun is normal startup, not a failed
				// attempt.
				if sawRunning || expertStoreSent(st, kind) || st.RobotType == 3 {
					robotLogf("[STORE_RUNTIME_STOPPED] uid=%d kind=%s state=%s disconnect=%d type=%d sent=%t direct_ack=%t active=%t last_error=%d\n",
						info.UID, kind.Name(), st.StateName, st.DisconnectReason, st.RobotType, expertStoreSent(st, kind), expertStoreDirectAck(st, kind), expertStoreActive(st, kind), expertStoreLastError(st, kind))
					return false, "runtime_stopped"
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			sawRunning = true
			if runningSince.IsZero() {
				runningSince = time.Now()
			}
			if st.RobotType == 3 && expertStoreActive(st, kind) {
				return true, ""
			}
			if lastError := expertStoreLastError(st, kind); lastError != 0 {
				robotLogf("[STORE_ACK_ERROR] uid=%d kind=%s type=%d sent=%t direct_ack=%t active=%t last_error=%d pos=%d/%d/%d/%d\n",
					info.UID, kind.Name(), st.RobotType, expertStoreSent(st, kind), expertStoreDirectAck(st, kind), expertStoreActive(st, kind), lastError, st.Village, st.Area, st.X, st.Y)
				reason, _ := r.manager.expertJobStoreFailure(kind, lastError)
				return false, reason
			}
			if !compatibilitySendTried && !expertStoreSent(st, kind) && st.RobotType != 3 && time.Since(runningSince) >= 500*time.Millisecond {
				compatibilitySendTried = true
				runtime := r.manager.storeSessionRuntime()
				if runtime != nil && runtime.StartExpertJobStore(info.UID, kind, r.manager.expertJobStoreCost(kind)) {
					robotLogf("[STORE_LOGIN_FALLBACK_SENT] uid=%d cid=%d kind=%s pos=%d/%d/%d/%d\n",
						info.UID, info.CID, kind.Name(), st.Village, st.Area, st.X, st.Y)
				} else {
					robotLogf("[STORE_LOGIN_FALLBACK_FAILED] uid=%d cid=%d kind=%s state=%s type=%d sent=%t\n",
						info.UID, info.CID, kind.Name(), st.StateName, st.RobotType, expertStoreSent(st, kind))
					return false, "start_failed"
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st, ok := r.manager.runtimeStatus(info.UID); ok {
		robotLogf("[STORE_ACK_TIMEOUT] uid=%d kind=%s state=%s disconnect=%d type=%d sent=%t direct_ack=%t active=%t last_error=%d pos=%d/%d/%d/%d target=%d/%d/%d/%d\n",
			info.UID, kind.Name(), st.StateName, st.DisconnectReason, st.RobotType, expertStoreSent(st, kind), expertStoreDirectAck(st, kind), expertStoreActive(st, kind), expertStoreLastError(st, kind),
			st.Village, st.Area, st.X, st.Y, info.Village, info.Area, info.X, info.Y)
	} else {
		robotLogf("[STORE_ACK_TIMEOUT] uid=%d kind=%s runtime_status=missing target=%d/%d/%d/%d\n", info.UID, kind.Name(), info.Village, info.Area, info.X, info.Y)
	}
	return false, "ack_timeout"
}

func (r *RobotRuntime) ExpireStore(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		st, ok := r.Status(uid)
		if !ok {
			return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateOffline}
		}
		rc := r.Config()
		info := robotcap.Info{UID: uid, CID: st.CID, Village: st.Village, Area: st.Area, X: st.X, Y: st.Y, Port: r.manager.cfg.RobotGamePort}
		if robots, err := r.manager.selectRobots(robotcap.CommandRequest{UIDs: []int{uid}}); err == nil && len(robots) > 0 {
			info = robots[0]
			info.Port = r.manager.cfg.RobotGamePort
		}
		recovered := r.cleanupStoreSession(info, rc, "store_expired")
		r.manager.addAutoStore(0, 0, 1)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: recovered, State: robotcap.ActionStateStoreExpired}
	})
}

// cleanupStoreSession releases both character and account caches before
// removing temporary stall rows/permissions, then returns the role as a normal
// online robot. This prevents the final server snapshot from restoring stale
// inventory or the private-store entitlement (the visible pack-animal state).
func (r *RobotRuntime) cleanupStoreSession(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) bool {
	if err := r.manager.offlineStoreSession(info.UID); err != nil {
		robotLogf("[STORE_CLEANUP_OFFLINE_ERROR] uid=%d cid=%d reason=%s err=%v\n", info.UID, info.CID, reason, err)
		return false
	}
	r.manager.finishStoreState(info.UID, info.CID, reason)
	_, recovered := r.manager.restoreAutoNormalOnline(info, rc, reason)
	return recovered
}

func (r *RobotRuntime) run(uid int, fn func() robotcap.ActionResult) (result robotcap.ActionResult) {
	lock := r.uidLocks.Acquire(uid)
	defer r.uidLocks.Release(uid, lock)
	defer func() {
		if rec := recover(); rec != nil {
			robotLogf("[RobotRuntime] panic uid=%d err=%v stack=%s\n", uid, rec, debug.Stack())
			result = robotcap.ActionResult{
				UID:     uid,
				OK:      false,
				State:   robotcap.ActionStateFailed,
				Message: "runtime panic",
			}
		}
	}()
	return fn()
}

// currentFollowTarget resolves the point AutoMove should walk toward. A party
// leader the session is tracking live takes precedence over the configured
// follow account: the leader position is exact and already in hand, while the
// account path is a database lookup that only yields a home village.
func (m *RobotManager) currentFollowTarget(uid int, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) (robotaction.FollowTarget, bool) {
	if target, ok := m.partyLeaderFollowTarget(uid); ok {
		return target, true
	}
	return m.accountFollowTarget(rc, maps)
}

// partyLeaderFollowTarget reads the leader position the backend session tracked
// from the town movement broadcast. The target is deliberately not aged out: a
// leader standing still broadcasts nothing, so a freshness deadline would turn
// "the leader is idle" into "wander off". It also means a party that dissolves
// without closing the session leaves the follower parked near the leader's last
// known spot rather than roaming, which is the safe direction to fail.
func (m *RobotManager) partyLeaderFollowTarget(uid int) (robotaction.FollowTarget, bool) {
	provider, ok := m.actions.(interface {
		PartyFollowTarget(int) (shared.PartyFollowTarget, bool)
	})
	if !ok {
		return robotaction.FollowTarget{}, false
	}
	target, ok := provider.PartyFollowTarget(uid)
	if !ok || target.CharacterID <= 0 {
		return robotaction.FollowTarget{}, false
	}
	return robotaction.FollowTarget{Village: target.Village, Area: target.Area, X: target.X, Y: target.Y}, true
}

func (m *RobotManager) accountFollowTarget(rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) (robotaction.FollowTarget, bool) {
	account := strings.TrimSpace(rc.FollowAccount)
	if account == "" || rc.SpawnFixed {
		return robotaction.FollowTarget{}, false
	}

	lookup, ok := m.loadFollowAccount(account)
	if !ok {
		return robotaction.FollowTarget{}, false
	}
	if !lookup.villageOK {
		return robotaction.FollowTarget{}, false
	}
	info := robotcap.Info{Village: lookup.village, Area: rc.SpawnArea, X: m.randBetween(rc.SpawnXMin, rc.SpawnXMax), Y: m.randBetween(rc.SpawnYMin, rc.SpawnYMax), Level: rc.LevelMax}
	robotspawn.ApplyVillageLocation(spawnEnv{manager: m}, &info, info.Village, rc, maps)
	return robotaction.FollowTarget{Village: info.Village, Area: info.Area, X: info.X, Y: info.Y}, true
}

func firstActionResult(uid int, res robotcap.CommandResult, err error) robotcap.ActionResult {
	if err != nil {
		return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
	}
	for _, robot := range res.Robots {
		if robot.UID == uid {
			return robot
		}
	}
	return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateMissing, Message: "no action result"}
}
