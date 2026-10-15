package robot

import "testing"

func TestUIDs(t *testing.T) {
	got := UIDs([]Info{{UID: 1}, {UID: 3}})
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("uids got %#v want [1 3]", got)
	}
}

func TestNewCommandResult(t *testing.T) {
	got := NewCommandResult(2)
	if got.Requested != 2 || len(got.Robots) != 0 || cap(got.Robots) != 2 {
		t.Fatalf("command result got requested=%d len=%d cap=%d", got.Requested, len(got.Robots), cap(got.Robots))
	}
}

func TestDBStateConstants(t *testing.T) {
	tests := map[string]string{
		DBStateExists:      "exists",
		DBStateMissingCore: "missing_core",
	}
	for got, want := range tests {
		if got != want {
			t.Fatalf("db state got %q want %q", got, want)
		}
	}
}

func TestOperationSummaries(t *testing.T) {
	cmd := CommandResult{Requested: 3, Accepted: 2, Confirmed: 1, Failed: 1}
	if got := CommandOperationSummary(cmd, nil); got != "requested=3 accepted=2 confirmed=1 failed=1" {
		t.Fatalf("command summary got %q", got)
	}
	cleanup := CleanupResult{Candidates: []CleanupCandidate{{UID: 1}, {UID: 2}}, Deleted: 1, Skipped: 1}
	if got := CleanupOperationSummary(cleanup, nil); got != "candidates=2 deleted=1 skipped=1" {
		t.Fatalf("cleanup summary got %q", got)
	}
}

func TestOperationStateConstants(t *testing.T) {
	tests := map[string]string{
		OperationStateRunning: "running",
		OperationStateDone:    "done",
		OperationStateFailed:  "failed",
		OperationStateUnknown: "unknown",
	}
	for got, want := range tests {
		if got != want {
			t.Fatalf("operation state got %q want %q", got, want)
		}
	}
}

func TestActionStateConstants(t *testing.T) {
	tests := map[string]string{
		ActionStateAccepted:           "accepted",
		ActionStateActorFull:          "actor_full",
		ActionStateAttached:           "attached",
		ActionStateAttachedOffline:    "attached_offline",
		ActionStateCancelled:          "cancelled",
		ActionStateClosed:             "closed",
		ActionStateFailed:             "failed",
		ActionStateIdle:               "idle",
		ActionStateLogoutFailed:       "logout_failed",
		ActionStateMissing:            "missing",
		ActionStateMissingActor:       "missing_actor",
		ActionStateMoved:              "moved",
		ActionStateNotConfirmed:       "not_confirmed",
		ActionStateNotOnline:          "not_online",
		ActionStateOffline:            "offline",
		ActionStateOnlineTimeout:      "online_timeout",
		ActionStatePending:            "pending",
		ActionStateQueueFailed:        "queue_failed",
		ActionStateRunning:            "running",
		ActionStateSchedulerBusy:      "scheduler_busy",
		ActionStateSent:               "sent",
		ActionStateStore:              "store",
		ActionStateStoreBusy:          "store_busy",
		ActionStateStoreFailed:        "store_failed",
		ActionStateStoreNoPoint:       "store_no_point",
		ActionStateStorePrepareFailed: "store_prepare_failed",
		ActionStateStoreStartFailed:   "store_start_failed",
		ActionStateStoreExpired:       "store_expired",
		ActionStateTimeout:            "timeout",
		ActionStateUIDNotAttached:     "uid_not_attached",
		ActionStateUnknownCommand:     "unknown_command",
	}
	for got, want := range tests {
		if got != want {
			t.Fatalf("action state got %q want %q", got, want)
		}
	}
}

func TestSchedulerModeConstants(t *testing.T) {
	tests := map[string]string{
		SchedulerModeBootstrap:   "bootstrap",
		SchedulerModeFill:        "fill",
		SchedulerModeStable:      "stable",
		SchedulerModeStore:       "store_expand",
		SchedulerModePressure:    "pressure",
		SchedulerModeBreaker:     "breaker",
		SchedulerModeMaintenance: "maintenance",
		SchedulerModeManual:      "manual",
	}
	for got, want := range tests {
		if got != want {
			t.Fatalf("scheduler mode got %q want %q", got, want)
		}
	}
}

func TestCopyRuntimeStatusMap(t *testing.T) {
	src := map[int]RuntimeStatus{1: {UID: 1, StateName: RuntimeStateRunning}}
	got := CopyRuntimeStatusMap(src)
	got[1] = RuntimeStatus{UID: 1, StateName: RuntimeStateStop}
	if src[1].StateName != RuntimeStateRunning {
		t.Fatalf("source map was mutated")
	}
}

func TestRequestScopes(t *testing.T) {
	if got := CommandRequestScope(CommandRequest{UIDs: []int{1, 2}}); got != "uids=2" {
		t.Fatalf("command scope got %q", got)
	}
	if got := CommandRequestScope(CommandRequest{Count: 3}); got != "count=3" {
		t.Fatalf("command count scope got %q", got)
	}
	if got := CleanupRequestScope(CleanupRequest{UIDs: []int{1}, Force: true}); got != "uids=1 force=true" {
		t.Fatalf("cleanup uid scope got %q", got)
	}
	if got := CleanupRequestScope(CleanupRequest{MinUID: 10, MaxUID: 20}); got != "range=10-20 force=false" {
		t.Fatalf("cleanup range scope got %q", got)
	}
}

func TestIsStructuralOperation(t *testing.T) {
	if !IsStructuralOperation(" cleanup ") {
		t.Fatalf("cleanup should be structural")
	}
	if IsStructuralOperation("online") {
		t.Fatalf("online should not be structural")
	}
}
