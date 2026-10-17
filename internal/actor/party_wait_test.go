package actor

import (
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"testing"
	"time"
)

type partyWaitRuntime struct {
	auctions int
	mails    int
	emotes   int
	config   robotconfig.RuntimeConfig
	status   robotcap.RuntimeStatus
	party    bool
	moves    int
	shouts   int
	stores   int
	expires  int
	exits    int
}

func (r *partyWaitRuntime) Config() robotconfig.RuntimeConfig { return r.config }
func (r *partyWaitRuntime) PartyActive(uid int) bool {
	return uid == r.status.UID && r.party
}
func (r *partyWaitRuntime) Status(uid int) (robotcap.RuntimeStatus, bool) {
	if uid != r.status.UID {
		return robotcap.RuntimeStatus{}, false
	}
	return r.status, true
}
func (r *partyWaitRuntime) IsActive(uid int) bool {
	return uid == r.status.UID && r.status.StateName == robotcap.RuntimeStateRunning
}
func (r *partyWaitRuntime) FinishStoreState(int, int, string)                 {}
func (r *partyWaitRuntime) AddAutoOnline(int, int)                            {}
func (r *partyWaitRuntime) AutoActionsEnabled(robotconfig.RuntimeConfig) bool { return true }
func (r *partyWaitRuntime) RandomShoutMessage(func(int) int) string           { return "test" }
func (r *partyWaitRuntime) OnlineNoConfirm(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateRunning}
}
func (r *partyWaitRuntime) Logout(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateAttachedOffline}
}
func (r *partyWaitRuntime) Move(uid int) robotcap.ActionResult {
	r.moves++
	return robotcap.ActionResult{UID: uid, OK: true}
}
func (r *partyWaitRuntime) Shout(uid int, _ bool) robotcap.ActionResult {
	r.shouts++
	return robotcap.ActionResult{UID: uid, OK: true}
}
func (r *partyWaitRuntime) Store(uid int) robotcap.ActionResult {
	r.stores++
	return robotcap.ActionResult{UID: uid, OK: true}
}
func (r *partyWaitRuntime) AutoMove(uid int) robotcap.ActionResult {
	r.moves++
	return robotcap.ActionResult{UID: uid, OK: true}
}
func (r *partyWaitRuntime) AutoShout(uid int, _ bool, _ string) robotcap.ActionResult {
	r.shouts++
	return robotcap.ActionResult{UID: uid, OK: true}
}
func (r *partyWaitRuntime) AutoStore(uid int, _ func() bool) robotcap.ActionResult {
	r.stores++
	return robotcap.ActionResult{UID: uid, OK: true}
}
func (r *partyWaitRuntime) AutoAuction(uid int) robotcap.ActionResult {
	r.auctions++
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateSent}
}
func (r *partyWaitRuntime) AutoMail(uid int) robotcap.ActionResult {
	r.mails++
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateSent}
}
func (r *partyWaitRuntime) AutoEmote(uid int, _ uint16) robotcap.ActionResult {
	r.emotes++
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateSent}
}
func (r *partyWaitRuntime) AutoDungeonEnter(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *partyWaitRuntime) AutoPartyDungeonEnter(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *partyWaitRuntime) AutoPartyDungeonExit(uid int) robotcap.ActionResult {
	r.exits++
	return robotcap.ActionResult{UID: uid}
}
func (r *partyWaitRuntime) AutoServerNotice(uid int, _ func() bool) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateNoticed}
}
func (r *partyWaitRuntime) ForceServerNotice(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateNoticed}
}
func (r *partyWaitRuntime) ExpireStore(uid int) robotcap.ActionResult {
	r.expires++
	return robotcap.ActionResult{UID: uid, OK: true}
}

func TestPartyActiveActorUsesLiveStateAndWaits(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:                 true,
			AutoMoveIntervalMinSec:      1,
			AutoMoveIntervalMaxSec:      1,
			AutoShoutIntervalMinSec:     1,
			AutoShoutIntervalMaxSec:     1,
			AutoStoreIntervalMinSec:     1,
			AutoStoreIntervalMaxSec:     1,
			AutoStoreProbabilityPercent: 100,
		},
		status: robotcap.RuntimeStatus{UID: 7, StateName: robotcap.RuntimeStateRunning, RobotType: 2},
		party:  true,
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	now := time.Now()
	a.nextMove = now.Add(-time.Second)
	a.nextShout = now.Add(-time.Second)
	a.nextStore = now.Add(-time.Second)
	a.storeUntil = now.Add(-time.Second)

	a.tick(now)
	if runtime.moves != 0 || runtime.shouts != 0 || runtime.stores != 0 || runtime.expires != 0 {
		t.Fatalf("party actor ran automatic actions: moves=%d shouts=%d stores=%d expires=%d", runtime.moves, runtime.shouts, runtime.stores, runtime.expires)
	}
	if !a.nextMove.IsZero() || !a.nextShout.IsZero() || !a.nextStore.IsZero() {
		t.Fatal("party wait did not clear automatic schedule")
	}
	if !a.shouldStopAutoStore() {
		t.Fatal("party state did not cancel an in-flight automatic store")
	}
	for _, cmd := range []Command{CommandMove, CommandShoutLocal, CommandShoutWorld, CommandStore} {
		res := a.handleCommand(cmd)
		if res.OK || res.State != robotcap.ActionStateCancelled || res.Message != "party active" {
			t.Fatalf("party command %d result = %+v", cmd, res)
		}
	}
	if runtime.moves != 0 || runtime.shouts != 0 || runtime.stores != 0 {
		t.Fatalf("party actor dispatched manual actions: moves=%d shouts=%d stores=%d", runtime.moves, runtime.shouts, runtime.stores)
	}

	runtime.party = false
	runtime.status.RobotType = 0
	a.tick(now.Add(time.Second))
	if runtime.moves != 0 || runtime.shouts != 0 || runtime.stores != 0 || runtime.expires != 0 {
		t.Fatalf("leaving party ran overdue actions immediately: moves=%d shouts=%d stores=%d expires=%d", runtime.moves, runtime.shouts, runtime.stores, runtime.expires)
	}
	if a.nextMove.IsZero() || a.nextShout.IsZero() || a.nextStore.IsZero() {
		t.Fatal("leaving party did not restart automatic schedules")
	}
}

// TestPartyActiveActorStillRunsThePartyExitDuty is the regression for the fleet
// freeze of 2026-10-16: the party guard returned before the exit duty, so a
// robot in a fleet party never ran it -- and a fleet party's roster lasts for as
// long as its members stand in the dungeon, so every member stayed in the run
// it was dragged into, with no round left that could take it out.
//
// It ticks twice on purpose. The duty paces itself, and the guard below it used
// to clear that pacing together with the solo rounds, so a single tick proved
// nothing: the duty ran whenever a test handed it a due time, while in the fleet
// every tick the guard returned on handed it a fresh interval instead, leaving
// it never due at all. The second tick is what tells those two apart.
func TestPartyActiveActorStillRunsThePartyExitDuty(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:                 true,
			AutoPartyDungeon:            true,
			AutoMoveIntervalMinSec:      1,
			AutoMoveIntervalMaxSec:      1,
			AutoShoutIntervalMinSec:     1,
			AutoShoutIntervalMaxSec:     1,
			AutoStoreIntervalMinSec:     1,
			AutoStoreIntervalMaxSec:     1,
			AutoStoreProbabilityPercent: 100,
		},
		status: robotcap.RuntimeStatus{UID: 7, StateName: robotcap.RuntimeStateRunning},
		party:  true,
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	now := time.Now()
	a.nextMove = now.Add(-time.Second)
	a.nextShout = now.Add(-time.Second)
	a.nextStore = now.Add(-time.Second)
	a.storeUntil = now.Add(-time.Second)

	a.tick(now)
	if runtime.exits != 0 {
		t.Fatalf("exit duty ran before it was due: exits=%d", runtime.exits)
	}
	a.tick(now.Add((partyExitIntervalMaxSec + 1) * time.Second))
	if runtime.exits != 1 {
		t.Fatalf("party actor skipped the exit duty: exits=%d", runtime.exits)
	}
	if runtime.moves != 0 || runtime.shouts != 0 || runtime.stores != 0 || runtime.expires != 0 {
		t.Fatalf("party actor ran solo rounds: moves=%d shouts=%d stores=%d expires=%d", runtime.moves, runtime.shouts, runtime.stores, runtime.expires)
	}
}

// TestPartyActiveActorStillRunsThePartyFollowDuty is the regression for the
// 2026-10-17 report that a human's invited dummies never walked with him: the
// party guard returned before the solo move round, so nothing but the exit duty
// ever ran for a party member. The follower session tracked the leader's live
// position the whole time (CN90_PARTY_FOLLOW_POSITION) and no round ever walked
// to it, which is why he could not lead them into a dungeon.
//
// It ticks twice for the same reason the exit-duty test does: the guard clears
// the solo schedule on every tick it returns on, so a follow timer that lived in
// that schedule would be re-armed to a fresh interval every tick and never come
// due. The second tick is what tells the two apart.
func TestPartyActiveActorStillRunsThePartyFollowDuty(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:                 true,
			AutoPartyFollow:             true,
			AutoMoveIntervalMinSec:      1,
			AutoMoveIntervalMaxSec:      1,
			AutoShoutIntervalMinSec:     1,
			AutoShoutIntervalMaxSec:     1,
			AutoStoreIntervalMinSec:     1,
			AutoStoreIntervalMaxSec:     1,
			AutoStoreProbabilityPercent: 100,
		},
		status: robotcap.RuntimeStatus{UID: 7, StateName: robotcap.RuntimeStateRunning},
		party:  true,
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	now := time.Now()
	a.nextMove = now.Add(-time.Second)
	a.nextShout = now.Add(-time.Second)
	a.nextStore = now.Add(-time.Second)

	a.tick(now)
	if runtime.moves != 0 {
		t.Fatalf("follow ran before it was due: moves=%d", runtime.moves)
	}
	if a.nextPartyFollow.IsZero() {
		t.Fatal("follow duty armed no timer")
	}
	a.tick(now.Add((partyFollowIntervalMaxSec + 1) * time.Second))
	if runtime.moves != 1 {
		t.Fatalf("party actor skipped the follow duty: moves=%d", runtime.moves)
	}
	if runtime.shouts != 0 || runtime.stores != 0 || runtime.expires != 0 {
		t.Fatalf("party actor ran solo rounds: shouts=%d stores=%d expires=%d", runtime.shouts, runtime.stores, runtime.expires)
	}
	if a.nextPartyFollow.IsZero() {
		t.Fatal("the party guard cleared the follow pacing")
	}
}
