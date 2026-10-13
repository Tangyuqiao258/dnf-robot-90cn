package actor

import (
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

type RobotRuntime interface {
	Config() robotconfig.RuntimeConfig
	Status(uid int) (robotcap.RuntimeStatus, bool)
	PartyActive(uid int) bool
	IsActive(uid int) bool
	FinishStoreState(uid, cid int, reason string)
	AddAutoOnline(success, failed int)
	AutoActionsEnabled(rc robotconfig.RuntimeConfig) bool
	RandomShoutMessage(randIntn func(int) int) string
	OnlineNoConfirm(uid int) robotcap.ActionResult
	Logout(uid int) robotcap.ActionResult
	Move(uid int) robotcap.ActionResult
	Shout(uid int, world bool) robotcap.ActionResult
	Store(uid int) robotcap.ActionResult
	AutoMove(uid int) robotcap.ActionResult
	AutoShout(uid int, world bool, msg string) robotcap.ActionResult
	AutoStore(uid int, shouldStop func() bool) robotcap.ActionResult
	AutoAuction(uid int) robotcap.ActionResult
	AutoMail(uid int) robotcap.ActionResult
	AutoServerNotice(uid int, shouldStop func() bool) robotcap.ActionResult
	ForceServerNotice(uid int) robotcap.ActionResult
	ExpireStore(uid int) robotcap.ActionResult
}

type Mode int

const (
	ModeAuto Mode = iota
	ModeManual
)

type Command int

const (
	CommandMove Command = iota
	CommandShoutLocal
	CommandShoutWorld
	CommandStore
	CommandServerNotice
	CommandOnline
	CommandLogout
)

type controlKind int

const (
	controlAssign controlKind = iota
	controlRelease
)

type State string

const (
	StateIdle      State = "idle"
	StateOffline   State = "attached_offline"
	StateAssigned  State = "assigned"
	StateOnline    State = "online"
	StateRunning   State = "running"
	StateBusy      State = "busy"
	StateReleasing State = "releasing"
)

type Snapshot struct {
	SlotID         int
	UID            int
	Mode           Mode
	State          State
	Busy           bool
	BusyKind       string
	OnlineDesired  bool
	LastOnlineTry  time.Time
	NextRetryAt    time.Time
	FirstFailureAt time.Time
	Failures       int
	FailureClass   string
	Quarantined    bool
}

// Failure classes decide how the scheduler reacts to an online failure.
// Transport failures (the server or the network is slow) only back off and
// retry; only data failures may recycle the robot's character.
const (
	FailureClassTransport = "transport"
	FailureClassData      = "data"
)

// onlineAttemptGate is implemented by the scheduler runtime. It is optional so
// the actor model still works with runtimes that do not expose the adaptive
// attempt budget (tests, future backends without login pacing).
type onlineAttemptGate interface {
	TryAcquireOnlineAttempt() bool
	ReleaseOnlineAttempt()
}

// onlineAttemptRecorder is implemented by the scheduler runtime so the online
// breaker can measure real login attempt outcomes separately from session
// churn (keepalive stalls and confirm timeouts).
type onlineAttemptRecorder interface {
	AddOnlineAttempt(success bool)
}

type Health string

const (
	HealthHealthy   Health = "healthy"
	HealthIdle      Health = "idle"
	HealthBusy      Health = "busy"
	HealthUnhealthy Health = "unhealthy"
)

type Status struct {
	Snapshot
	Health       Health
	HealthReason string
	RecycleUID   bool
}

func SnapshotEmpty(s Snapshot) bool {
	return s.UID <= 0
}

func SnapshotSchedulerPending(s Snapshot) bool {
	if SnapshotEmpty(s) {
		return true
	}
	switch s.State {
	case StateIdle, StateOffline, StateAssigned, StateOnline, StateReleasing:
		return true
	default:
		return false
	}
}

func Operation(s Snapshot) string {
	if s.BusyKind != "" {
		return s.BusyKind
	}
	switch s.State {
	case StateAssigned, StateOnline:
		return "online"
	case StateReleasing:
		return "release"
	case StateOffline:
		return "offline"
	default:
		return ""
	}
}

func StopPriority(uid int, status map[int]robotcap.RuntimeStatus) int {
	if uid <= 0 {
		return 0
	}
	st, ok := status[uid]
	if !ok || st.DisconnectReason != 0 || st.StateName == robotcap.RuntimeStateInit || st.StateName == robotcap.RuntimeStateLogin {
		return 1
	}
	if st.PartyActive {
		return 4
	}
	if st.RobotType == 2 || st.RobotType == 3 || st.StoreDisplayAck {
		return 2
	}
	return 3
}

func RandomizedDue(next *time.Time, now time.Time, minSec, maxSec int, randBetween func(int, int) int) bool {
	if minSec <= 0 || maxSec <= 0 || randBetween == nil {
		return false
	}
	if maxSec < minSec {
		minSec, maxSec = maxSec, minSec
	}
	if next.IsZero() {
		*next = now.Add(time.Duration(randBetween(minSec, maxSec)) * time.Second)
		return false
	}
	if now.Before(*next) {
		return false
	}
	*next = now.Add(time.Duration(randBetween(minSec, maxSec)) * time.Second)
	return true
}

type StatusConfig struct {
	BadFailures            int
	OnlineConfirmTimeoutMS int
}

type RuntimeStatusLookup func(uid int) (robotcap.RuntimeStatus, bool)

func EvaluateStatus(snapshot Snapshot, now time.Time, cfg StatusConfig, lookup RuntimeStatusLookup) Status {
	status := Status{Snapshot: snapshot, Health: HealthHealthy}
	if snapshot.Mode != ModeAuto || snapshot.UID <= 0 {
		status.Health = HealthIdle
		return status
	}
	if snapshot.Busy {
		status.Health = HealthBusy
		status.HealthReason = snapshot.BusyKind
		return status
	}
	if snapshot.Failures >= cfg.BadFailures {
		status.Health = HealthUnhealthy
		status.HealthReason = "failure_count"
		// Only data-level failures justify deleting and recreating the
		// character. Transport failures are retried with scheduler backoff.
		status.RecycleUID = snapshot.FailureClass == FailureClassData
		return status
	}
	if snapshot.State == StateOnline && !snapshot.LastOnlineTry.IsZero() {
		timeout := time.Duration(cfg.OnlineConfirmTimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		if now.Sub(snapshot.LastOnlineTry) > timeout {
			if lookup == nil {
				status.Health = HealthUnhealthy
				status.HealthReason = "online_confirm_timeout"
				return status
			}
			if st, ok := lookup(snapshot.UID); !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 {
				status.Health = HealthUnhealthy
				status.HealthReason = "online_confirm_timeout"
				return status
			}
		}
	}
	return status
}
