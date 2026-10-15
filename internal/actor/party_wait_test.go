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
