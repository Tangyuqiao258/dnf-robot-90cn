package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

type partyStateActionTransport struct{ activeUID int }

func (t partyStateActionTransport) MoveTown(context.Context, shared.RuntimeMoveCommand) error {
	return nil
}

func (t partyStateActionTransport) ShoutLocal(context.Context, shared.RuntimeShoutCommand) error {
	return nil
}

func (t partyStateActionTransport) PartyActive(uid int) bool { return uid == t.activeUID }

type shoutRecordingTransport struct {
	commands []shared.RuntimeShoutCommand
}

func (*shoutRecordingTransport) MoveTown(context.Context, shared.RuntimeMoveCommand) error {
	return nil
}
func (t *shoutRecordingTransport) ShoutLocal(_ context.Context, command shared.RuntimeShoutCommand) error {
	t.commands = append(t.commands, command)
	return nil
}

func TestSimulatorAutomaticShoutFallsBackToAreaChannel(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[shout]\nshout_send_enabled = true\n")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	transport := &shoutRecordingTransport{}
	m.SetBackendActionTransport(transport)
	m.runtimeState.snapshot = map[int]robotcap.RuntimeStatus{
		7: {UID: 7, StateName: robotcap.RuntimeStateRunning},
	}
	m.runtimeState.updatedAt = time.Now()
	result := NewRobotRuntime(m).AutoShout(7, true, "hello")
	if !result.OK || len(transport.commands) != 1 || transport.commands[0].Message != "hello" {
		t.Fatalf("result=%+v commands=%+v", result, transport.commands)
	}
	m.autoMu.Lock()
	stats := m.autoStats
	m.autoMu.Unlock()
	if stats.ShoutLocalSuccess != 1 || stats.ShoutWorldSuccess != 0 {
		t.Fatalf("shout stats local=%d world=%d", stats.ShoutLocalSuccess, stats.ShoutWorldSuccess)
	}
}

func TestRobotRuntimeUsesLiveBackendPartyState(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendActionTransport(partyStateActionTransport{activeUID: 7})
	runtime := NewRobotRuntime(m)
	if !runtime.PartyActive(7) || runtime.PartyActive(8) {
		t.Fatal("robot runtime did not use live backend party state")
	}
}

func TestSimulatorStoreIsRejectedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	_, err := m.StoreManaged(robotcap.CommandRequest{Count: 1})
	if err == nil {
		t.Fatal("simulator store unexpectedly entered actor workflow")
	}
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Backend != shared.BackendID("test") || unsupported.Operation != shared.CapabilityStore {
		t.Fatalf("error = %v, want 90CN store unsupported", err)
	}
	if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("error = %v, missing stable capability code", err)
	}
}

func TestSimulatorPartyAndSkillEntrypointsAreRejectedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	checks := []struct {
		name string
		run  func() error
		want shared.BackendCapability
	}{
		{name: "party debug", run: func() error { _, err := m.PartyDebugStatus(); return err }, want: shared.CapabilityPartyDebug},
		{name: "party skills", run: func() error { _, err := m.ReloadPartySkills(); return err }, want: shared.CapabilitySkill},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := check.run()
			var unsupported shared.UnsupportedCapabilityError
			if !errors.As(err, &unsupported) || unsupported.Backend != shared.BackendID("test") || unsupported.Operation != check.want {
				t.Fatalf("error = %v, want %s unsupported", err, check.want)
			}
			if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
				t.Fatalf("error = %v, missing stable capability code", err)
			}
		})
	}
}

func TestSimulatorWorldShoutIsRejectedBeforeActorWorkflow(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	_, err := m.ShoutManaged(robotcap.CommandRequest{Count: 1}, true)
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Operation != shared.CapabilityWorldShout {
		t.Fatalf("error = %v, want world shout unsupported", err)
	}
	if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("error = %v, missing stable capability code", err)
	}
}

func TestSimulatorAutoStoreIsSkippedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	result := NewRobotRuntime(m).AutoStore(7, nil)
	if result.State != robotcap.ActionStateCancelled || !strings.Contains(result.Message, shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("result = %+v, want skipped unsupported store", result)
	}
}

func TestSimulatorAdaptiveSchedulerNeverEntersStoreMode(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_target_online_count = 600\n")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	signals := adaptiveSchedulerSignals{
		Live: true, Running: 600, Actors: 600, GamePortReady: true,
		StoreUnsupported: m.requireBackendCapability(shared.CapabilityStore) != nil,
	}
	rc, decision := m.refreshAdaptiveRobotConfig(signals)
	if decision.Mode != schedulerPolicyStable || rc.SchedulerStoreConcurrent != 0 || rc.AutoStoreProbabilityPercent != 0 {
		t.Fatalf("decision=%+v concurrent=%d probability=%d", decision, rc.SchedulerStoreConcurrent, rc.AutoStoreProbabilityPercent)
	}
	m.updateSchedulerStatus(rc, signals, decision)
	status := m.SchedulerStatus()
	if status.Mode == robotcap.SchedulerModeStore || status.StoreTarget != 0 {
		t.Fatalf("status=%+v, want stable scheduler with no store target", status)
	}
}

func TestSimulatorVerifiedTownAndLocalShoutRemainAllowed(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	if err := m.requireBackendCapability(shared.CapabilityTownMove); err != nil {
		t.Fatalf("town move unexpectedly unsupported: %v", err)
	}
	if err := m.requireBackendCapability(shared.CapabilityShout); err != nil {
		t.Fatalf("local shout unexpectedly unsupported: %v", err)
	}
}

// TestDirectedShoutBackendRefusesATargetlessShout covers the backend whose
// chat is enabled but point-to-point, which is what the 90CN adapter declares.
// The capability check alone passes there, so without the mode the automatic
// template turn would send a message with no target and be booked as a failure.
func TestDirectedShoutBackendRefusesATargetlessShout(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	info := testS4BackendInfo()
	info.Capabilities[shared.CapabilityShout] = shared.CapabilityStatus{
		Enabled: true, Mode: shared.ShoutModeDirected, Reason: "chat is delivered to a named character only",
	}
	m.SetBackendRobotCreator(info, nil)

	var unsupported shared.UnsupportedCapabilityError
	if err := m.requireBroadcastShout(); !errors.As(err, &unsupported) {
		t.Fatalf("target-less shout error = %v, want an unsupported capability", err)
	} else if unsupported.Reason == "" || !strings.Contains(unsupported.Reason, "chat is delivered to a named character only") {
		t.Fatalf("target-less shout reason = %q", unsupported.Reason)
	}
	// The capability itself stays enabled: a caller that names a target is
	// entitled to send.
	if err := m.requireBackendCapability(shared.CapabilityShout); err != nil {
		t.Fatalf("directed chat became unsupported: %v", err)
	}
	// Every entry point that holds a target-less intent has to refuse. The
	// automatic turn reports its refusal as a cancelled action carrying the same
	// message, so the assertion is on the reason the caller sees.
	for name, run := range map[string]func() error{
		"auto shout": func() error {
			result := NewRobotRuntime(m).AutoShout(7, false, "hello")
			if result.State != robotcap.ActionStateCancelled {
				return fmt.Errorf("action was not cancelled: %+v", result)
			}
			return errors.New(result.Message)
		},
		"local shout command": func() error {
			_, err := m.ShoutManaged(robotcap.CommandRequest{Count: 1}, false)
			return err
		},
		"preferred shout command": func() error {
			_, err := m.ShoutPreferredManaged(robotcap.CommandRequest{Count: 1})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil {
				t.Fatal("a target-less shout was accepted")
			}
			if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) ||
				!strings.Contains(err.Error(), "operation="+string(shared.CapabilityShout)) {
				t.Fatalf("error = %v, want the shout capability refusal", err)
			}
		})
	}
}

type cleanupRecorder struct{ called bool }

func (c *cleanupRecorder) CleanupRobots(_ context.Context, req robotcap.CleanupRequest) (robotcap.CleanupResult, error) {
	c.called = true
	return robotcap.CleanupResult{DryRun: !req.Force, Requested: len(req.UIDs)}, nil
}

func TestSimulatorCleanupRoutesToProtocolAdapter(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	cleaner := &cleanupRecorder{}
	m.SetBackendRobotCleaner(cleaner)
	result, err := m.CleanupRobots(robotcap.CleanupRequest{UIDs: []int{7}})
	if err != nil || !cleaner.called || !result.DryRun || result.Requested != 1 {
		t.Fatalf("result=%+v called=%t err=%v", result, cleaner.called, err)
	}
}

func TestBackendCleanupNeverBypassesAdapterProtocol(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	if _, err := m.CleanupRobots(robotcap.CleanupRequest{UIDs: []int{7}}); err == nil || !strings.Contains(err.Error(), "cleanup adapter is not configured") {
		t.Fatalf("error = %v, want missing adapter failure", err)
	}
}

type persistenceInspectorStub struct {
	status shared.PersistenceStatus
}

func (s persistenceInspectorStub) Status(context.Context) shared.PersistenceStatus { return s.status }

func TestSimulatorDatabaseStatusUsesBackendInspector(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	info := testS4BackendInfo()
	m.ConfigureBackendRuntime(info, persistenceInspectorStub{status: shared.PersistenceStatus{OK: true, Engine: "sqlite", Writable: true}}, nil)
	status := m.DatabaseStatus()
	if !status.OK || status.Engine != "sqlite" || !status.Writable {
		t.Fatalf("status = %+v, want backend persistence result", status)
	}
}
