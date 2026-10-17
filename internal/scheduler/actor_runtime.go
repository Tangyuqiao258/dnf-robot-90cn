package scheduler

import (
	"fmt"
	"math"
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
	"sort"
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
		if runs, ok := r.manager.actions.(interface {
			DungeonState(int) (bool, bool, time.Time)
		}); ok {
			// A character inside a run has no town move to make: the run owns it
			// until the exit duty takes it back. Party members follow their leader
			// through this same round, and a fleet party stands in a run for as
			// long as its dwell, so without this guard every follower would spend
			// that whole time asking the server for town moves from a dungeon.
			if active, _, _ := runs.DungeonState(uid); active {
				return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "in_dungeon"}
			}
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

// storePointAllowed builds the filter one store round claims its point under:
// the world's generic-area rule, plus the map's own crowd rule for the kind the
// round is about to open.
//
// The crowd rule used to be consulted only after a point was claimed, and a map
// that refused ended the whole round -- which is what the store failure counter
// was mostly made of. A map that cannot host the kind is not a candidate at all,
// so it is dropped before the claim rather than after it. The verdict is
// memoized per map: one check per round, not one per candidate point.
func (r *RobotRuntime) storePointAllowed(info robotcap.Info, kind shared.ExpertJobStoreKind) func(storecap.Position) bool {
	allowed := make(map[shared.MapAreaKey]bool, 8)
	return func(pos storecap.Position) bool {
		if !shared.GenericAreaAllowed(info.GuildID, pos.Village) {
			return false
		}
		key := shared.MapAreaKey{Village: pos.Village, Area: pos.Area}
		verdict, seen := allowed[key]
		if !seen {
			verdict = r.manager.crowdStallAllows(pos.Village, pos.Area, kind)
			allowed[key] = verdict
		}
		return verdict
	}
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
	// attempted records whether any coordinate reached the wire. A round that
	// never got one has no store result to report at all: it is a placement
	// outcome, and it is counted and returned as one below.
	attempted := false
	allowedPosition := r.storePointAllowed(info, kind)
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
			// The pool ran dry for this town: every coordinate was taken, or the
			// crowd rules refused every map that holds one. Naming it matters,
			// because this ending used to be silent -- the round fell through to
			// the generic cleanup reason, so a fleet with nowhere left to stand
			// was indistinguishable in the log from a disassembler that kept
			// breaking, and the two need opposite remedies.
			robotLogf("[STORE_NO_POINT] uid=%d cid=%d kind=%s village=%d area=%d area_has_points=%t try=%d/%d\n",
				uid, info.CID, kind.Name(), info.Village, info.Area, points.HasArea(info.Village, info.Area), try, tries)
			break
		}
		info.Village, info.Area, info.X, info.Y = pos.Village, pos.Area, pos.X, pos.Y
		// The trade belongs to the character, the market to the map: a robot
		// keeps the expert job it was raised with, and a map hosts only the
		// stalls its crowd rule allows, one apiece where the rule says so. The
		// pool filter above already drops every map the rule forbids, so a
		// refusal here means another robot took the map's last slot between the
		// claim and this check. The claim is released rather than reported,
		// since the point itself is sound -- the map, not the coordinate, is
		// what refused.
		if !r.manager.crowdStallAllows(pos.Village, pos.Area, kind) {
			robotLogf("[STORE_MAP_STALL_REFUSED] uid=%d cid=%d kind=%s point=%s village=%d area=%d\n",
				uid, info.CID, kind.Name(), pos.PointID, pos.Village, pos.Area)
			points.Discard(uid, pos)
			points.Flush()
			break
		}
		attempted = true
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
	if !attempted {
		// Nothing reached the wire, so there is no store result to report. The
		// round is closed out as a placement failure: the pool had no coordinate
		// this town could use. Reporting it as a store failure would misdescribe
		// the market as a broken machine and, through the store failure window,
		// arm a rule meant to read the server's health.
		r.cleanupStoreSession(info, rc, "no_store_point")
		r.manager.addAutoStoreNoPoint(1)
		return robotcap.ActionResult{UID: uid, CID: info.CID, OK: false, State: robotcap.ActionStateStoreNoPoint}
	}
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

// AutoDungeonEnter runs one robot through a whole dungeon round trip: it walks
// the selector, commits a run, waits for the scene to load, and then abandons
// the run so the character is back in town before the next action.
//
// The exit is not a courtesy. No backend adapter here drives a character inside
// a run -- moving between rooms needs the current room cleared, which needs
// combat -- so a robot that stayed would sit in the dungeon while every other
// automatic action assumed it was in town. Entering and leaving is the whole
// round trip this build can honestly claim, which is also why the switch that
// turns it on is opt-in rather than part of the social defaults.
//
// The gates mirror AutoEmote, including the party check. A party owns its
// member's run, and following a leader into a dungeon is a separate feature
// this action must not stand in for.
func (r *RobotRuntime) AutoDungeonEnter(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityDungeonEnter); err != nil {
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
		if !rc.AutoDungeonEnter {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "disabled"}
		}
		// The id and the difficulty come from the server's own PVF, so a config
		// pointing outside the wire range is refused here rather than truncated
		// into a request for a different dungeon.
		if rc.AutoDungeonEnterID <= 0 || rc.AutoDungeonEnterID > math.MaxUint32 {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled,
				Message: fmt.Sprintf("dungeon id out of range: %d", rc.AutoDungeonEnterID)}
		}
		if rc.AutoDungeonEnterDifficulty < 0 || rc.AutoDungeonEnterDifficulty > math.MaxUint8 {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled,
				Message: fmt.Sprintf("dungeon difficulty out of range: %d", rc.AutoDungeonEnterDifficulty)}
		}
		dungeons, ok := r.manager.actions.(interface {
			EnterDungeon(int, uint32, byte) error
			LeaveDungeon(int) error
		})
		if !ok {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "dungeon_enter_unsupported"}
		}
		dungeonID := uint32(rc.AutoDungeonEnterID)
		difficulty := byte(rc.AutoDungeonEnterDifficulty)
		if err := dungeons.EnterDungeon(uid, dungeonID, difficulty); err != nil {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		if err := dungeons.LeaveDungeon(uid); err != nil {
			// The entry itself succeeded, so the character really is inside a run
			// the exit did not get it out of. Reporting that as a failed entry
			// would hide the state the operator has to know about.
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed,
				Message: fmt.Sprintf("entered dungeon=%d difficulty=%d but the exit failed: %v", dungeonID, difficulty, err)}
		}
		return robotcap.ActionResult{
			UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent,
			Message: fmt.Sprintf("dungeon=%d difficulty=%d", dungeonID, difficulty),
		}
	})
}

const (
	// partyFormationGrace is how long a party of robots may stand in town with no
	// run before the fleet treats it as abandoned and leaves it. It has to outlast
	// the assembly window -- the invite wait plus the entry -- or a party would be
	// torn down while its leader is still gathering it.
	partyFormationGrace = 90 * time.Second
	// partyInvitePoll paces the wait for invitations to be answered. The server
	// pushes the roster as each member joins, so this polls state that is already
	// local rather than anything on the wire.
	partyInvitePoll = 500 * time.Millisecond
)

// partyTransport is the part of the backend action surface the party-run
// actions use, named rather than written inline so the invite wait can take it
// as a parameter.
type partyTransport interface {
	InviteParty(int, int) error
	PartyMembers(int) ([]int, bool)
	PartySince(int) (time.Time, bool)
	LeaveParty(int) error
	DungeonState(int) (bool, bool, time.Time)
	EnterDungeon(int, uint32, byte) error
	LeaveDungeon(int) error
	ClearDungeon(int) error
}

// AutoPartyDungeonEnter forms a party out of the robots standing on this robot's
// own map and walks the whole party into one dungeon run.
//
// The server does the half that would otherwise need fan-out: the leader's op16
// preflights every member and commits a run for each of them, and the followers
// are pulled in by the server rather than entering anything themselves. So
// nothing here enters a dungeon on a member's behalf, and nothing here has to
// tell a member where to go.
//
// The exit is the part the fleet owes in exchange, and it is not a courtesy:
// there is no party-wide dungeon exit on the server, so every member has to
// leave its own run. That belongs to AutoPartyDungeonExit, for the leader as
// much as for the followers, which is why this action forms the party and
// starts the run but ends neither of them itself.
//
// The gates mirror AutoDungeonEnter. A robot already in a party is refused: the
// party owns its member, and a second formation would fight the first.
func (r *RobotRuntime) AutoPartyDungeonEnter(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityParty); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		if err := r.manager.requireBackendCapability(shared.CapabilityDungeonEnter); err != nil {
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
		if !rc.AutoPartyDungeon {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "disabled"}
		}
		if rc.AutoDungeonEnterID <= 0 || rc.AutoDungeonEnterID > math.MaxUint32 {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled,
				Message: fmt.Sprintf("dungeon id out of range: %d", rc.AutoDungeonEnterID)}
		}
		if rc.AutoDungeonEnterDifficulty < 0 || rc.AutoDungeonEnterDifficulty > math.MaxUint8 {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled,
				Message: fmt.Sprintf("dungeon difficulty out of range: %d", rc.AutoDungeonEnterDifficulty)}
		}
		party, ok := r.manager.actions.(partyTransport)
		if !ok {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "party_dungeon_unsupported"}
		}
		if active, _, _ := party.DungeonState(uid); active {
			// A robot already inside a run cannot form another one: the run owns
			// the character until the exit duty takes it back to town.
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "already_in_dungeon"}
		}
		size := rc.AutoPartyDungeonSize
		if size < 2 {
			size = 2
		}
		invited := r.partyCandidates(uid, st.CID, st.Village, st.Area, size-1)
		if len(invited) == 0 {
			// Nothing to gather here. The next tick looks again, and by then the
			// robots on this map have moved on, which is the whole point of retrying
			// rather than widening the search to another map.
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "no_candidates"}
		}
		sent := 0
		for _, target := range invited {
			// A refused invitation is the other robot's business -- it may have just
			// been invited by somebody else, or be walking out of the area -- and one
			// refusal does not spoil the invitations that were sent.
			if err := party.InviteParty(uid, target); err == nil {
				sent++
			}
		}
		if sent == 0 {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: "party_invite_failed"}
		}
		members := r.awaitPartyMembers(party, uid, size, time.Duration(rc.AutoPartyDungeonInviteTimeoutSec)*time.Second)
		if members < 2 {
			// Nobody answered. Leaving costs one packet and clears the party the
			// server may be holding for this character alone, so the next formation
			// starts from nothing rather than from a party of one.
			_ = party.LeaveParty(uid)
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "party_not_formed"}
		}
		dungeonID := uint32(rc.AutoDungeonEnterID)
		difficulty := byte(rc.AutoDungeonEnterDifficulty)
		if err := party.EnterDungeon(uid, dungeonID, difficulty); err != nil {
			// The party is formed and the run was refused. Every member is standing
			// in town inside a party with nothing to do, so the leader leaves it and
			// lets the members find their own way out through AutoPartyDungeonExit,
			// which ends a party that never got anywhere.
			_ = party.LeaveParty(uid)
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed,
				Message: fmt.Sprintf("party=%d dungeon=%d entry failed: %v", members, dungeonID, err)}
		}
		if rc.AutoDungeonClear {
			// The run is live and the server has already pulled every member
			// into it. Clearing walks the maze as the leader; the followers ride
			// along on the server's own projections, so nothing here drives them.
			if err := party.ClearDungeon(uid); err != nil {
				// The entry succeeded, so the party really is inside a run the
				// clear could not finish. Reporting that as a failed entry would
				// hide the state the operator has to know about; the exit duty
				// still takes the party out afterwards.
				return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed,
					Message: fmt.Sprintf("party=%d dungeon=%d entered but the clear failed: %v", members, dungeonID, err)}
			}
			return robotcap.ActionResult{
				UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent,
				Message: fmt.Sprintf("party=%d dungeon=%d difficulty=%d cleared", members, dungeonID, difficulty),
			}
		}
		return robotcap.ActionResult{
			UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent,
			Message: fmt.Sprintf("party=%d dungeon=%d difficulty=%d", members, dungeonID, difficulty),
		}
	})
}

// awaitPartyMembers waits until the party has reached the wanted size or the
// window closes, and reports the membership it ended on.
func (r *RobotRuntime) awaitPartyMembers(party partyTransport, uid, wanted int, window time.Duration) int {
	deadline := time.Now().Add(window)
	for {
		members, _ := party.PartyMembers(uid)
		if len(members) >= wanted || !time.Now().Before(deadline) {
			return len(members)
		}
		time.Sleep(partyInvitePoll)
	}
}

// partyCandidates picks the robots this formation may invite: the fleet robots
// standing on the leader's own map, online, in no party and running no stall.
//
// The map is not a preference. The server forwards an invitation only to a
// target in the inviter's own town area and drops it without a word otherwise,
// so an invitation to anywhere else is a packet that was never going to be read.
// The order is by uid, which keeps a retry on the same map choosing the same
// robots rather than a fresh coin toss.
func (r *RobotRuntime) partyCandidates(uid, selfCharacterID, village, area, limit int) []int {
	if limit <= 0 {
		return nil
	}
	status := r.manager.runtimeStatusMapCopy()
	type candidate struct {
		uid int
		cid int
	}
	ready := make([]candidate, 0, len(status))
	for other, st := range status {
		if other == uid || st.CID <= 0 || st.CID == selfCharacterID {
			continue
		}
		if !robotcap.ActiveRuntimeStatus(st) || st.PartyActive {
			continue
		}
		if st.RobotType == 2 || st.RobotType == 3 || st.StoreDisplayAck {
			continue
		}
		if st.Village != village || st.Area != area {
			continue
		}
		ready = append(ready, candidate{uid: other, cid: st.CID})
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].uid < ready[j].uid })
	if len(ready) > limit {
		ready = ready[:limit]
	}
	targets := make([]int, 0, len(ready))
	for _, item := range ready {
		targets = append(targets, item.cid)
	}
	return targets
}

// AutoPartyDungeonExit is the exit duty of a party run, and it belongs to the
// leader as much as to the followers.
//
// It does two things, and both exist because the server leaves them to the
// client. A member of a party run has to leave its own run -- there is no
// party-wide dungeon exit, and a character left inside one never walks back to
// town on its own. And a party that never got as far as a run has to be ended by
// somebody: the fleet forms parties of its own, so it has to be able to end
// them, or the members stand frozen in town for as long as the party exists.
//
// The party is only ever ended, and its run only ever left, when every member is
// one of this fleet's online robots. A party holding a character the fleet does
// not run is somebody else's, and the robots in it were asked to be there: the
// run is that character's too, so a robot that walked out of it would leave them
// standing inside alone.
func (r *RobotRuntime) AutoPartyDungeonExit(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		rc := r.Config()
		if !rc.AutoPartyDungeon {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "disabled"}
		}
		party, ok := r.manager.actions.(partyTransport)
		if !ok {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "party_dungeon_unsupported"}
		}
		// The party decides both halves of the duty, so it is read before either
		// of them is touched.
		members, known := party.PartyMembers(uid)
		leftRun := false
		if active, selfStarted, since := party.DungeonState(uid); active {
			// The hold is measured from the run, not from this tick, so a robot the
			// server dragged in is not thrown out again a second later just because
			// its own timer happened to be due.
			hold := time.Duration(rc.AutoPartyDungeonDwellSec) * time.Second
			if hold > 0 && !since.IsZero() && time.Since(since) < hold {
				return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "dungeon_hold"}
			}
			// A known empty roster is the disband the other members leave behind:
			// the server sends this member the emptied roster once the last of them
			// walks out. Nobody is left inside for it to keep company, so the last
			// member leaves its own run rather than being held as a stranger's
			// guest forever.
			disbanded := known && len(members) == 0
			if !disbanded && (!known || !r.partyIsFleet(members)) {
				// The run belongs to a party the fleet does not own, so it is not the
				// fleet's to leave. The robot was asked into it and stays for as long as
				// that party keeps it.
				return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "dungeon_foreign_party"}
			}
			if err := party.LeaveDungeon(uid); err != nil {
				return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed,
					Message: fmt.Sprintf("dungeon exit failed: %v (started by this robot: %t)", err, selfStarted)}
			}
			leftRun = true
		}
		if !known || len(members) <= 1 {
			if leftRun {
				return robotcap.ActionResult{UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent, Message: "dungeon_exited"}
			}
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "no_party"}
		}
		if !leftRun {
			// Nothing has happened yet. This is either a formation in progress or a
			// party that never got anywhere, and only its age tells them apart.
			since, ok := party.PartySince(uid)
			if !ok || time.Since(since) < partyFormationGrace {
				return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "party_forming"}
			}
		}
		if !r.partyIsFleet(members) {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "party_foreign"}
		}
		if err := party.LeaveParty(uid); err != nil {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed,
				Message: fmt.Sprintf("party leave failed: %v", err)}
		}
		return robotcap.ActionResult{
			UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent,
			Message: fmt.Sprintf("party_left members=%d dungeon=%t", len(members), leftRun),
		}
	})
}

// partyIsFleet reports whether every member of the party is one of this fleet's
// online robots.
//
// It reads the live status map, so a member that has gone offline reads as a
// stranger and the party is left alone. That is the safe direction: the cost of
// being wrong is a party of robots that stands still until the demand layer
// recycles one of them, while the cost of the other mistake is a fleet walking
// out of a player's party.
func (r *RobotRuntime) partyIsFleet(members []int) bool {
	if len(members) == 0 {
		return false
	}
	status := r.manager.runtimeStatusMapCopy()
	online := make(map[int]bool, len(status))
	for _, st := range status {
		if st.CID > 0 {
			online[st.CID] = true
		}
	}
	for _, member := range members {
		if !online[member] {
			return false
		}
	}
	return true
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
	// A pinned robot answers to its pin and to nothing else. The demand layer
	// owns follow_account -- pollPlayer reads it, fillFloatingLayer places the
	// budget on the rings around it, and demandLocationFor is the pin that
	// comes out. Reading the account here as well made it a second, rule-free
	// implementation of the same intent: accountFollowTarget names the follow
	// account's village and draws an area from that whole village at random, so
	// it ignored the rings, the per-map cap, the resident rows and the level
	// mix, and it outranked the pin.
	//
	// The 2026-10-16 lab run is what that costs. The demand layer logged
	// `demand_plan resident=65 floating=135 desired=200 shedding=0 player=38/1`
	// -- every online robot pinned, 65 of them to the three villages the
	// operator named -- while populationReport found no robot at any of those
	// three and 297 of 300 in the one village the follow account sat in. Login
	// applied the pins; the first AutoMove tick walked them off. 98 robots ended
	// up in a single area, and each of their area transitions fanned out to all
	// 98.
	//
	// Returning no target instead sends the move down AutoMove's plain branch,
	// whose randomTarget draws only inside the robot's own village and area --
	// the pin's area -- so a pinned robot wanders where the operator put it and
	// stays there.
	if _, ok := m.demandLocationFor(uid); ok {
		return robotaction.FollowTarget{}, false
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
