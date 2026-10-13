package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/lockhub"
)

type reconnectRuntime struct {
	mu     lockhub.Locker
	active bool
	online int
	config robotconfig.RuntimeConfig
}

func (r *reconnectRuntime) Config() robotconfig.RuntimeConfig { return r.config }

func (r *reconnectRuntime) Status(uid int) (robotcap.RuntimeStatus, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := robotcap.RuntimeStateStop
	if r.active {
		state = robotcap.RuntimeStateRunning
	}
	return robotcap.RuntimeStatus{UID: uid, StateName: state}, true
}

func (r *reconnectRuntime) PartyActive(int) bool { return false }

func (r *reconnectRuntime) IsActive(int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *reconnectRuntime) FinishStoreState(int, int, string)                 {}
func (r *reconnectRuntime) AddAutoOnline(int, int)                            {}
func (r *reconnectRuntime) AutoActionsEnabled(robotconfig.RuntimeConfig) bool { return false }
func (r *reconnectRuntime) RandomShoutMessage(func(int) int) string           { return "" }

func (r *reconnectRuntime) OnlineNoConfirm(uid int) robotcap.ActionResult {
	r.mu.Lock()
	r.online++
	r.active = true
	r.mu.Unlock()
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateRunning}
}

func (r *reconnectRuntime) Logout(uid int) robotcap.ActionResult {
	r.mu.Lock()
	r.active = false
	r.mu.Unlock()
	return robotcap.ActionResult{UID: uid, OK: true}
}

func (r *reconnectRuntime) Move(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) Shout(uid int, _ bool) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) Store(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) AutoMove(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) AutoShout(uid int, _ bool, _ string) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) AutoStore(uid int, _ func() bool) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) AutoAuction(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) AutoServerNotice(uid int, _ func() bool) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) ForceServerNotice(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}
func (r *reconnectRuntime) ExpireStore(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}

func (r *reconnectRuntime) onlineCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.online
}

func (r *reconnectRuntime) disconnect() {
	r.mu.Lock()
	r.active = false
	r.mu.Unlock()
}

func TestActorReconnectsAfterRuntimeDisconnect(t *testing.T) {
	runtime := &reconnectRuntime{config: robotconfig.RuntimeConfig{
		SystemActorPollMS:             100,
		ReconnectDelayMS:              0,
		OnlineConfirmTimeoutMS:        1000,
		SchedulerOnlineRetryBaseMS:    1000,
		SchedulerOnlineRetryMaxMS:     1000,
		SchedulerOnlineRetryJitterPct: 0,
	}}
	actor := NewActor(1, ModeAuto, runtime)
	actor.Start()
	defer actor.StopAndWait(time.Second)
	if !actor.AssignAndWait(17000001, time.Second) {
		t.Fatal("failed to assign actor")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && runtime.onlineCount() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.onlineCount(); got < 1 {
		t.Fatalf("initial online calls = %d, want at least 1", got)
	}

	runtime.disconnect()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && runtime.onlineCount() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.onlineCount(); got < 2 {
		t.Fatalf("online calls after disconnect = %d, want at least 2", got)
	}
}
