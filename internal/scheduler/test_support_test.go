package scheduler

import (
	"os"
	"testing"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func testS4BackendInfo() shared.BackendInfo {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "unsupported in test backend"})
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityProvision, shared.CapabilityTownMove, shared.CapabilityDungeonFollow,
		shared.CapabilityShout, shared.CapabilityCleanup, shared.CapabilityDangerousDelete, shared.CapabilityDatabase,
	} {
		capabilities[capability] = shared.CapabilityStatus{Enabled: true}
	}
	return shared.BackendInfo{ID: shared.BackendID("test"), Capabilities: capabilities, MaxOnline: 10000}
}

func newRobotActor(slotID int, mode actormodel.Mode, runtime actormodel.RobotRuntime) *actormodel.Actor {
	return actormodel.NewActor(slotID, mode, runtime)
}

func (s *RobotSupervisor) stopAll() {
	actors := s.ledger.BeginDrainAllActors()
	s.stopDrainingActors(actors, actorStopWait)
}

type actorTestRuntime struct{}

func (actorTestRuntime) Config() robotconfig.RuntimeConfig {
	return robotconfig.RuntimeConfig{SystemActorPollMS: 60_000}
}
func (actorTestRuntime) Status(int) (robotcap.RuntimeStatus, bool) {
	return robotcap.RuntimeStatus{}, false
}
func (actorTestRuntime) PartyActive(int) bool                              { return false }
func (actorTestRuntime) IsActive(int) bool                                 { return false }
func (actorTestRuntime) FinishStoreState(int, int, string)                 {}
func (actorTestRuntime) AddAutoOnline(int, int)                            {}
func (actorTestRuntime) AutoActionsEnabled(robotconfig.RuntimeConfig) bool { return false }
func (actorTestRuntime) RandomShoutMessage(func(int) int) string           { return "" }
func (actorTestRuntime) OnlineNoConfirm(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) Logout(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid, OK: true}
}
func (actorTestRuntime) Move(uid int) robotcap.ActionResult { return robotcap.ActionResult{UID: uid} }
func (actorTestRuntime) Shout(uid int, _ bool) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) Store(uid int) robotcap.ActionResult { return robotcap.ActionResult{UID: uid} }
func (actorTestRuntime) AutoMove(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) AutoShout(uid int, _ bool, _ string) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) AutoStore(uid int, _ func() bool) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) AutoAuction(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) AutoMail(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) AutoEmote(uid int, _ uint16) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) AutoDungeonEnter(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) AutoServerNotice(uid int, _ func() bool) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) ForceServerNotice(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (actorTestRuntime) ExpireStore(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}

type snapshotActorRegistry struct {
	actorRegistry
	snapshots []actormodel.Snapshot
}

func (r snapshotActorRegistry) actorSnapshots() []actormodel.Snapshot {
	return r.snapshots
}

func testRobotManagerWithConfig(t *testing.T, robotConfig string) *RobotManager {
	t.Helper()
	configDir := t.TempDir()
	paths := layout.New(configDir)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if robotConfig != "" {
		if err := os.WriteFile(paths.RobotConfig(), []byte(robotConfig), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return NewRobotManager(nil, &config.SysConfig{ConfigDir: configDir}, nil)
}

func assertIntSlice(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length got %d want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice[%d] got %d want %d: got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}

func testRobotActor(t *testing.T, slotID int, mode actormodel.Mode, uid int) *actormodel.Actor {
	t.Helper()
	a := newRobotActor(slotID, mode, actorTestRuntime{})
	a.Start()
	t.Cleanup(func() { a.StopAndWait(time.Second) })
	if uid > 0 && !a.AssignAndWait(uid, time.Second) {
		t.Fatalf("assign actor slot=%d uid=%d", slotID, uid)
	}
	return a
}

func ensureSupervisorActors(t *testing.T, supervisor *RobotSupervisor, count int) []*actormodel.Actor {
	t.Helper()
	supervisor.ensureAutoActorSlots(robotconfig.RuntimeConfig{SchedulerOnlineBatchSize: count}, count)
	actors := supervisor.ledger.ActorPointers()
	if len(actors) != count {
		t.Fatalf("supervisor actors got %d want %d", len(actors), count)
	}
	t.Cleanup(supervisor.stopAll)
	return actors
}
