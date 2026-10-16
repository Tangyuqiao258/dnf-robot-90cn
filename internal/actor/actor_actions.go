package actor

import (
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	foundationlog "robot/internal/foundation/log"
)

const (
	// partyExitIntervalMinSec/MaxSec pace the exit duty of a party run. It is the
	// only scheduled round that answers a state the robot cannot walk out of by
	// itself, so it ticks in tens of seconds rather than in the minutes every
	// other automatic action is paced at.
	partyExitIntervalMinSec = 15
	partyExitIntervalMaxSec = 30
)

func (a *Actor) logoutCurrentUID() robotcap.ActionResult {
	uid := a.uidValue()
	if uid <= 0 {
		a.setState(StateIdle)
		return robotcap.ActionResult{OK: true, State: robotcap.ActionStateIdle}
	}
	cid := 0
	st, statusOK := a.runtime.Status(uid)
	if statusOK {
		cid = st.CID
	}
	a.finishStoreStateIfNeeded(uid, cid, st, statusOK, "logout")
	a.setOnlineDesired(false)
	a.clearAutoSchedule()
	a.setBusy(true, "logout")
	defer a.setBusy(false, "")
	res := a.runtime.Logout(uid)
	if res.UID == 0 {
		res.UID = uid
	}
	if res.OK {
		res.State = robotcap.ActionStateAttachedOffline
	}
	a.setState(StateOffline)
	return res
}

func (a *Actor) handleCommand(cmd Command) robotcap.ActionResult {
	uid := a.uidValue()
	if uid <= 0 {
		return robotcap.ActionResult{OK: false, State: robotcap.ActionStateIdle, Message: "actor has no uid"}
	}
	switch cmd {
	case CommandOnline:
		a.setOnlineDesired(true)
		a.setState(StateAssigned)
		return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateAccepted}
	case CommandLogout:
		return a.logoutCurrentUID()
	}
	switch cmd {
	case CommandMove, CommandShoutLocal, CommandShoutWorld, CommandStore, CommandServerNotice, CommandEmote:
		st, _ := a.runtime.Status(uid)
		if st.PartyActive || a.runtime.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled, Message: "party active"}
		}
	}
	a.setBusy(true, "command")
	defer a.setBusy(false, "")
	switch cmd {
	case CommandMove:
		if res, ok := a.ensureOnlineForCommand(); !ok {
			return res
		}
		return a.runtime.Move(uid)
	case CommandShoutLocal:
		if res, ok := a.ensureOnlineForCommand(); !ok {
			return res
		}
		return a.runtime.Shout(uid, false)
	case CommandShoutWorld:
		if res, ok := a.ensureOnlineForCommand(); !ok {
			return res
		}
		return a.runtime.Shout(uid, true)
	case CommandStore:
		// The store workflow prepares inventory while offline and performs its
		// own confirmed login. A preliminary login here makes the workflow log
		// straight back out and can leave some server variants reusing the old
		// inventory snapshot on the second login.
		res := a.runtime.Store(uid)
		if res.OK {
			rc := a.runtime.Config()
			a.setStoreUntil(time.Now().Add(robotconfig.StoreDurationForUID(rc.AutoStoreDurationSec, uid)))
		}
		return res
	case CommandServerNotice:
		// The notice workflow prepares its stock offline and performs its own
		// confirmed login, mirroring the store workflow's offline cycle.
		return a.runtime.ForceServerNotice(uid)
	case CommandEmote:
		if res, ok := a.ensureOnlineForCommand(); !ok {
			return res
		}
		rc := a.runtime.Config()
		return a.runtime.AutoEmote(uid, uint16(a.randBetween(rc.AutoEmoteIndexMin, rc.AutoEmoteIndexMax)))
	}
	return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateUnknownCommand}
}

func (a *Actor) ensureOnlineForCommand() (robotcap.ActionResult, bool) {
	uid := a.uidValue()
	if uid <= 0 {
		return robotcap.ActionResult{OK: false, State: robotcap.ActionStateIdle, Message: "actor has no uid"}, false
	}
	if a.runtime.IsActive(uid) {
		return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateRunning}, true
	}
	a.setOnlineDesired(true)
	a.setState(StateAssigned)
	rc := a.runtime.Config()
	timeout := time.Duration(rc.OnlineConfirmTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if a.releaseRequestedValue() {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled}, false
		}
		if a.runtime.IsActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateRunning}, true
		}
		a.ensureOnline(time.Now())
		time.Sleep(500 * time.Millisecond)
	}
	return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOnlineTimeout, Message: "not confirmed running"}, false
}

func (a *Actor) tick(now time.Time) {
	if a.releaseRequestedValue() || a.stateValue() == StateReleasing {
		return
	}
	uid := a.uidValue()
	if uid <= 0 {
		return
	}
	if !a.onlineDesiredValue() {
		if a.stateValue() != StateOffline && !a.busyValue() {
			a.setState(StateOffline)
		}
		return
	}
	rc := a.runtime.Config()
	if !a.runtime.IsActive(uid) {
		// A previously live session disappeared (keepalive stall or server
		// disconnect). Count one transport failure so the reconnect backs off
		// instead of immediately hammering the server again.
		if a.clearOnlineEstablished() {
			failures := a.recordFailure(now, FailureClassTransport)
			delay := robotconfig.OnlineRetryBackoff(rc, failures, a.randIntn)
			a.setNextRetryAt(now.Add(delay))
			foundationlog.Robotf("[Actor] online_lost slot=%d uid=%d failures=%d retry_in=%s\n", a.slotIDValue(), uid, failures, delay)
		}
		a.ensureOnline(now)
		return
	}
	st, ok := a.runtime.Status(uid)
	if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 {
		return
	}
	if a.stateValue() != StateRunning {
		a.runtime.AddAutoOnline(1, 0)
	}
	if !a.onlineEstablishedValue() {
		a.recordOnlineAttempt(true)
	}
	a.markOnlineHealthy()
	a.setState(StateRunning)
	// The exit duty is the one round the party guard below does not silence, so
	// it sits above that guard with its own copy of the automatic gate the solo
	// rounds sit behind. A robot inside a live party is exactly the robot this
	// duty exists to take back out of a run -- and a fleet party's roster lasts
	// for as long as its members stand in the dungeon, so a guard that returned
	// first would park every one of them there for good, which is what it did
	// before this round moved up. It is the one round that runs for a stall as
	// well: a robot left inside a run is a robot nothing else in the fleet can
	// help, and leaving a run is not a scene change a stall has to survive.
	if a.modeValue() == ModeAuto && a.runtime.AutoActionsEnabled(rc) && a.nextPartyExitDue(now, rc) {
		var res robotcap.ActionResult
		a.runBusy("party_exit", func() {
			res = a.runtime.AutoPartyDungeonExit(uid)
		})
		if res.OK {
			foundationlog.Robotf("[Actor] party_exit slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		} else if res.State == robotcap.ActionStateFailed {
			foundationlog.Robotf("[Actor] party_exit_failed slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		}
	}
	if st.PartyActive || a.runtime.PartyActive(uid) {
		a.clearAutoActionSchedule()
		return
	}
	isStore := st.RobotType == 2 || st.RobotType == 3
	if isStore && a.storeUntilMissing() {
		grace := time.Duration(rc.AutoStoreTickSec) * time.Second
		if grace <= 0 {
			grace = 10 * time.Second
		}
		a.setStoreUntil(now.Add(grace))
		foundationlog.Robotf("[Actor] store_expire_recovered slot=%d uid=%d grace=%s\n", a.slotIDValue(), uid, grace)
	}
	if isStore && a.storeExpired(now) {
		a.runBusy("store_expire", func() {
			a.runtime.ExpireStore(uid)
		})
		a.clearStoreUntil()
		return
	}
	if a.modeValue() != ModeAuto {
		return
	}
	if !a.runtime.AutoActionsEnabled(rc) {
		return
	}
	if a.nextShoutDue(now, rc) {
		a.runBusy("shout", func() {
			a.runtime.AutoShout(uid, true, a.randomShoutMessage())
		})
	}
	if !isStore && a.nextServerNoticeDue(now, rc) {
		var res robotcap.ActionResult
		a.runBusy("server_notice", func() {
			defer a.clearOnlineAttempt()
			res = a.runtime.AutoServerNotice(uid, a.shouldStopAutoStore)
		})
		switch res.State {
		case robotcap.ActionStateNoticed, robotcap.ActionStateCancelled, robotcap.ActionStateOffline:
			a.markOnlineHealthy()
		default:
			foundationlog.Robotf("[Actor] server_notice_failed slot=%d uid=%d state=%s message=%s\n", a.slotIDValue(), uid, res.State, res.Message)
			a.markOnlineHealthy()
		}
	}
	if !isStore && a.nextStoreDue(now, rc) {
		if rc.AutoStoreProbabilityPercent > 0 && a.randIntn(100) < rc.AutoStoreProbabilityPercent {
			var res robotcap.ActionResult
			a.runBusy("store", func() {
				defer a.clearOnlineAttempt()
				res = a.runtime.AutoStore(uid, a.shouldStopAutoStore)
			})
			if res.OK {
				a.setStoreUntil(time.Now().Add(robotconfig.StoreDurationForUID(rc.AutoStoreDurationSec, uid)))
			} else if res.State == robotcap.ActionStateStoreBusy {
				a.markOnlineHealthy()
				a.setNextStore(time.Now().Add(time.Duration(rc.AutoStoreFailCooldownSec) * time.Second))
			} else if res.State == robotcap.ActionStateStoreFailed || res.State == robotcap.ActionStateStoreNoPoint {
				// A full market earns the same cooldown as a failed store and
				// nothing more: the session is healthy, so the round must not
				// fall through to the transport-failure branch below and
				// quarantine a robot that simply had no pitch to stand on.
				a.markOnlineHealthy()
				a.setNextStore(time.Now().Add(time.Duration(rc.AutoStoreFailCooldownSec) * time.Second))
			} else if res.State != robotcap.ActionStateCancelled {
				a.recordFailure(time.Now(), FailureClassTransport)
			}
			return
		}
	}
	if !isStore && a.nextAuctionDue(now, rc) {
		var res robotcap.ActionResult
		a.runBusy("auction", func() {
			res = a.runtime.AutoAuction(uid)
		})
		// A refused listing or a listing that was already sold is ordinary
		// auction business and stays inside the round, so a failure here means
		// the session itself could not be used. It is logged rather than
		// counted: a new feature must not quarantine an otherwise healthy robot
		// while its live behavior is still being watched.
		if res.State == robotcap.ActionStateFailed {
			foundationlog.Robotf("[Actor] auction_failed slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		}
	}
	if !isStore && a.nextMailDue(now, rc) {
		var res robotcap.ActionResult
		a.runBusy("mail", func() {
			res = a.runtime.AutoMail(uid)
		})
		// A mailbox that answers with a rejection is ordinary mail business and
		// stays inside the round, so a failure here means the session itself could
		// not be used. It is logged for the same reason the auction round logs:
		// a new feature must not quarantine an otherwise healthy robot while its
		// live behavior is still being watched.
		if res.State == robotcap.ActionStateFailed {
			foundationlog.Robotf("[Actor] mail_failed slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		}
	}
	if !isStore && a.nextEmoteDue(now, rc) {
		// The index is drawn here rather than in the runtime so every robot
		// walks its own sequence instead of sharing one draw per tick.
		emotionIndex := uint16(a.randBetween(rc.AutoEmoteIndexMin, rc.AutoEmoteIndexMax))
		var res robotcap.ActionResult
		a.runBusy("emote", func() {
			res = a.runtime.AutoEmote(uid, emotionIndex)
		})
		// A rejected mood change is ordinary cosmetic business and stays inside
		// the round, for the same reason the auction and mail rounds log rather
		// than count: a new feature must not quarantine a healthy robot while
		// its live behavior is still being watched.
		if res.State == robotcap.ActionStateFailed {
			foundationlog.Robotf("[Actor] emote_failed slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		}
	}
	if !isStore && a.nextDungeonEnterDue(now, rc) {
		var res robotcap.ActionResult
		a.runBusy("dungeon_enter", func() {
			res = a.runtime.AutoDungeonEnter(uid)
		})
		// A refused or failed entry is ordinary dungeon business and stays inside
		// the round, for the same reason the auction, mail and emote rounds log
		// rather than count.
		if res.State == robotcap.ActionStateFailed {
			foundationlog.Robotf("[Actor] dungeon_enter_failed slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		}
	}
	if !isStore && a.nextPartyDungeonDue(now, rc) {
		var res robotcap.ActionResult
		a.runBusy("party_dungeon", func() {
			res = a.runtime.AutoPartyDungeonEnter(uid)
		})
		// A formation that found nobody to gather is ordinary business on a quiet
		// map and stays inside the round, for the same reason the other rounds log
		// rather than count.
		if res.OK {
			foundationlog.Robotf("[Actor] party_dungeon slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		} else if res.State == robotcap.ActionStateFailed {
			foundationlog.Robotf("[Actor] party_dungeon_failed slot=%d uid=%d message=%s\n", a.slotIDValue(), uid, res.Message)
		}
	}
	if !isStore && a.nextMoveDue(now, rc) {
		a.runBusy("move", func() {
			a.runtime.AutoMove(uid)
		})
	}
}

func (a *Actor) clearAutoSchedule() {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.clearAutoScheduleLocked()
}

func (a *Actor) clearAutoActionSchedule() {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.nextMove = time.Time{}
	a.nextShout = time.Time{}
	a.nextStore = time.Time{}
	a.nextAuction = time.Time{}
	a.nextMail = time.Time{}
	a.nextEmote = time.Time{}
	a.nextDungeonEnter = time.Time{}
	a.nextPartyDungeon = time.Time{}
	a.nextPartyExit = time.Time{}
	a.nextServerNotice = time.Time{}
}

func (a *Actor) clearAutoScheduleLocked() {
	a.nextMove = time.Time{}
	a.nextShout = time.Time{}
	a.nextStore = time.Time{}
	a.nextAuction = time.Time{}
	a.nextMail = time.Time{}
	a.nextEmote = time.Time{}
	a.nextDungeonEnter = time.Time{}
	a.nextPartyDungeon = time.Time{}
	a.nextPartyExit = time.Time{}
	a.nextServerNotice = time.Time{}
	a.storeUntil = time.Time{}
}

func (a *Actor) storeExpired(now time.Time) bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return !a.storeUntil.IsZero() && now.After(a.storeUntil)
}

func (a *Actor) storeUntilMissing() bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.storeUntil.IsZero()
}

func (a *Actor) finishStoreStateIfNeeded(uid, cid int, st robotcap.RuntimeStatus, statusOK bool, reason string) {
	if uid <= 0 {
		return
	}
	storeScheduled := !a.storeUntilMissing()
	storeRuntime := statusOK && (st.RobotType == 2 || st.RobotType == 3)
	if !storeScheduled && !storeRuntime {
		return
	}
	a.runtime.FinishStoreState(uid, cid, reason)
}

func (a *Actor) clearStoreUntil() {
	a.stateMu.Lock()
	a.storeUntil = time.Time{}
	a.stateMu.Unlock()
}

func (a *Actor) setStoreUntil(t time.Time) {
	a.stateMu.Lock()
	a.storeUntil = t
	a.stateMu.Unlock()
}

func (a *Actor) setNextStore(t time.Time) {
	a.stateMu.Lock()
	a.nextStore = t
	a.stateMu.Unlock()
}

func (a *Actor) nextShoutDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextShout, now, rc.AutoShoutIntervalMinSec, rc.AutoShoutIntervalMaxSec)
}

func (a *Actor) nextStoreDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextStore, now, rc.AutoStoreIntervalMinSec, rc.AutoStoreIntervalMaxSec)
}

// nextAuctionDue paces one robot's auction round. The interval is measured in
// minutes because a listing lives for hours: the board only needs a robot to
// keep its few slots filled, not to poll them.
func (a *Actor) nextAuctionDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	if !rc.AuctionEnabled {
		return false
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextAuction, now, rc.AuctionIntervalMinSec, rc.AuctionIntervalMaxSec)
}

// nextMailDue paces one robot's mailbox round. Settlement mail arrives when a
// trade settles, which is not on the robot's clock, so the interval is short
// relative to the auction one: a robot that only checks occasionally still has
// to clear the letters before its box fills with its own history.
func (a *Actor) nextMailDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	if !rc.MailEnabled {
		return false
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextMail, now, rc.MailIntervalMinSec, rc.MailIntervalMaxSec)
}

// nextEmoteDue paces one robot's mood change. It is the slowest of the social
// timers on purpose: the stored mood is a persistent trait other players read
// off the actor row, so a robot that flips it often reads as glitchy rather
// than lively.
func (a *Actor) nextEmoteDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	if !rc.AutoEmote {
		return false
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextEmote, now, rc.AutoEmoteIntervalMinSec, rc.AutoEmoteIntervalMaxSec)
}

// nextDungeonEnterDue paces one robot's dungeon round trip. The interval is
// measured in minutes because the round commits a run on the character and the
// server records it: this is the least frequent thing a robot does, not a
// social tick.
func (a *Actor) nextDungeonEnterDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	if !rc.AutoDungeonEnter {
		return false
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextDungeonEnter, now, rc.AutoDungeonEnterIntervalMinSec, rc.AutoDungeonEnterIntervalMaxSec)
}

// nextPartyDungeonDue paces one robot's attempt to gather a party and take it
// into a run. It is the slowest round in the fleet: it commits runs on every
// member it gathers, and the whole point of a party is that it is rare enough to
// be worth looking at.
func (a *Actor) nextPartyDungeonDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	if !rc.AutoPartyDungeon {
		return false
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextPartyDungeon, now, rc.AutoPartyDungeonIntervalMinSec, rc.AutoPartyDungeonIntervalMaxSec)
}

// nextPartyExitDue paces the exit duty. It is a fast tick on purpose: it is what
// takes a robot out of a run it was dragged into, and standing in a dungeon is
// the one state nothing else in the fleet can act from.
func (a *Actor) nextPartyExitDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	if !rc.AutoPartyDungeon {
		return false
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextPartyExit, now, partyExitIntervalMinSec, partyExitIntervalMaxSec)
}

// nextServerNoticeDue is the per-robot timer. The fleet-wide gap and hourly
// cap are claimed inside the action, so many robots may be due at once.
func (a *Actor) nextServerNoticeDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	if !rc.AutoServerNotice {
		return false
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextServerNotice, now, rc.ServerNoticeRobotIntervalMinSec, rc.ServerNoticeRobotIntervalMaxSec)
}

func (a *Actor) nextMoveDue(now time.Time, rc robotconfig.RuntimeConfig) bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.randomizedDue(&a.nextMove, now, rc.AutoMoveIntervalMinSec, rc.AutoMoveIntervalMaxSec)
}

func (a *Actor) randIntn(n int) int {
	if n <= 0 {
		return 0
	}
	if a.rand == nil {
		return 0
	}
	return a.rand.Intn(n)
}

func (a *Actor) randBetween(min, max int) int {
	if max < min {
		min, max = max, min
	}
	return min + a.randIntn(max-min+1)
}

func (a *Actor) randomizedDue(next *time.Time, now time.Time, minSec, maxSec int) bool {
	return RandomizedDue(next, now, minSec, maxSec, a.randBetween)
}

func (a *Actor) randomShoutMessage() string {
	return a.runtime.RandomShoutMessage(a.randIntn)
}

func (a *Actor) shouldStopAutoStore() bool {
	if a.releaseRequestedValue() {
		return true
	}
	uid := a.uidValue()
	if uid <= 0 {
		return true
	}
	if a.runtime.PartyActive(uid) {
		return true
	}
	st, ok := a.runtime.Status(uid)
	return ok && st.PartyActive
}

func (a *Actor) ensureOnline(now time.Time) {
	uid := a.uidValue()
	if uid <= 0 {
		return
	}
	if a.runtime.IsActive(uid) {
		if !a.onlineEstablishedValue() {
			a.recordOnlineAttempt(true)
		}
		a.markOnlineHealthy()
		a.setState(StateRunning)
		a.runtime.AddAutoOnline(1, 0)
		return
	}
	rc := a.runtime.Config()
	if a.onlineAttemptTimedOut(uid, now, rc) {
		failures := a.recordFailure(now, FailureClassTransport)
		a.runtime.AddAutoOnline(0, 1)
		a.recordOnlineAttempt(false)
		delay := robotconfig.OnlineRetryBackoff(rc, failures, a.randIntn)
		foundationlog.Robotf("[Actor] online_confirm_timeout slot=%d uid=%d failures=%d retry_in=%s\n", a.slotIDValue(), uid, failures, delay)
		a.clearOnlineAttempt()
		a.setNextRetryAt(now.Add(delay))
		return
	}
	if a.onlineConfirmPending(uid, now, rc) {
		a.markOnlinePending(now)
		return
	}
	// The scheduler owns the adaptive attempt budget: the fixed per-actor
	// reconnect delay is replaced by a scheduler-computed retry deadline and a
	// shared rate/in-flight gate.
	if retryAt := a.nextRetryAtValue(); !retryAt.IsZero() && now.Before(retryAt) {
		return
	}
	if gate, ok := a.runtime.(onlineAttemptGate); ok {
		if !gate.TryAcquireOnlineAttempt() {
			return
		}
		defer gate.ReleaseOnlineAttempt()
	}
	a.setLastOnlineTry(now)
	a.setState(StateOnline)
	res := a.runtime.OnlineNoConfirm(uid)
	if a.releaseRequestedValue() {
		return
	}
	if res.OK || res.State == robotcap.ActionStateRunning {
		a.markOnlineHealthy()
		a.runtime.AddAutoOnline(1, 0)
		a.recordOnlineAttempt(true)
		return
	}
	if res.State == robotcap.ActionStateAccepted || res.State == robotcap.RuntimeStateInit || res.State == robotcap.RuntimeStateLogin {
		a.markOnlinePending(now)
		return
	}
	failures := a.recordFailure(now, classifyOnlineFailure(res))
	a.runtime.AddAutoOnline(0, 1)
	a.recordOnlineAttempt(false)
	// A definitive failure ends the attempt: without clearing lastOnlineTry the
	// actor would sit in the confirm-pending window for the full
	// online_confirm_timeout before the scheduler backoff even applies.
	a.clearOnlineAttempt()
	delay := robotconfig.OnlineRetryBackoff(rc, failures, a.randIntn)
	a.setNextRetryAt(now.Add(delay))
	foundationlog.Robotf("[Actor] online_failed slot=%d uid=%d failures=%d class=%s retry_in=%s state=%s msg=%s\n",
		a.slotIDValue(), uid, failures, a.failureClassValue(), delay, res.State, res.Message)
}

// classifyOnlineFailure separates failures that need a character repair from
// failures that only need a slower retry. Markers stay conservative: anything
// unrecognized is a transport failure so a load spike can never trigger the
// destructive delete/recreate path.
func classifyOnlineFailure(res robotcap.ActionResult) string {
	if res.State == robotcap.ActionStateMissing || res.State == robotcap.ActionStateMissingActor {
		return FailureClassData
	}
	message := strings.ToLower(res.Message)
	for _, marker := range []string{
		"no usable character",
		"character not found",
		"identity",
		"profession",
		"provision",
		"uid range",
	} {
		if strings.Contains(message, marker) {
			return FailureClassData
		}
	}
	return FailureClassTransport
}

// recordOnlineAttempt reports a real login attempt outcome to the scheduler
// runtime when it supports attempt accounting.
func (a *Actor) recordOnlineAttempt(success bool) {
	if recorder, ok := a.runtime.(onlineAttemptRecorder); ok {
		recorder.AddOnlineAttempt(success)
	}
}
