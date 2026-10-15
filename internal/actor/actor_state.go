package actor

import (
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

func (a *Actor) status(now time.Time, rc robotconfig.RuntimeConfig) Status {
	s := a.snapshot()
	return EvaluateStatus(s, now, StatusConfig{
		BadFailures:            rc.SchedulerBadFailures,
		OnlineConfirmTimeoutMS: rc.OnlineConfirmTimeoutMS,
	}, a.runtimeStatusLookup())
}

// runtimeStatusLookup returns the runtime status reader for this actor without
// touching actor state.
func (a *Actor) runtimeStatusLookup() RuntimeStatusLookup {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.Status
}

func (a *Actor) resetForUID(uid int) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.generation++
	a.uid = uid
	a.clearAutoScheduleLocked()
	a.lastOnlineTry = time.Time{}
	a.nextRetryAt = time.Time{}
	a.firstFailureAt = time.Time{}
	a.failures = 0
	a.failureClass = ""
	a.onlineEstablished = false
	a.busy = false
	a.busyKind = ""
	a.releaseRequested = false
	a.onlineDesired = uid > 0
	a.quarantined = false
	if uid > 0 {
		a.state = StateAssigned
	} else {
		a.state = StateIdle
	}
}

func (a *Actor) setReleaseRequested(v bool) {
	a.stateMu.Lock()
	a.releaseRequested = v
	a.stateMu.Unlock()
}

func (a *Actor) releaseRequestedValue() bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.releaseRequested
}

func (a *Actor) setOnlineDesired(v bool) {
	a.stateMu.Lock()
	a.onlineDesired = v
	a.stateMu.Unlock()
}

// SetOnlineDesired is the demand layer's lever on one actor: an actor the layer
// no longer wants online stops being re-established, drops to offline, and is
// then released by the supervisor's throttled sweep. It deliberately does not
// log out by itself -- a whole layer turning over at once is a disconnect storm
// against the server's login path -- so the caller sets the flag and lets the
// sweep pace the real logouts.
func (a *Actor) SetOnlineDesired(v bool) {
	a.setOnlineDesired(v)
}

func (a *Actor) onlineDesiredValue() bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.onlineDesired
}

func (a *Actor) markOnlineHealthy() {
	a.stateMu.Lock()
	a.failures = 0
	a.firstFailureAt = time.Time{}
	a.lastOnlineTry = time.Time{}
	a.nextRetryAt = time.Time{}
	a.failureClass = ""
	a.onlineEstablished = true
	a.stateMu.Unlock()
}

func (a *Actor) onlineEstablishedValue() bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.onlineEstablished
}

// markOnlineEstablished records that the session was observed live.
func (a *Actor) markOnlineEstablished() {
	a.stateMu.Lock()
	a.onlineEstablished = true
	a.stateMu.Unlock()
}

// clearOnlineEstablished reports whether a live session was lost. Callers use
// it to count one transport failure so a keepalive stall or server disconnect
// backs off instead of reconnecting immediately.
func (a *Actor) clearOnlineEstablished() bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	was := a.onlineEstablished
	a.onlineEstablished = false
	return was
}

func (a *Actor) clearOnlineAttempt() {
	a.stateMu.Lock()
	a.lastOnlineTry = time.Time{}
	a.stateMu.Unlock()
}

func (a *Actor) markOnlinePending(now time.Time) {
	a.stateMu.Lock()
	if a.firstFailureAt.IsZero() {
		a.firstFailureAt = now
	}
	a.stateMu.Unlock()
}

func (a *Actor) onlineConfirmPending(uid int, now time.Time, rc robotconfig.RuntimeConfig) bool {
	lastOnlineTry := a.lastOnlineTryValue()
	if lastOnlineTry.IsZero() {
		return false
	}
	timeout := time.Duration(rc.OnlineConfirmTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if now.Sub(lastOnlineTry) >= timeout {
		return false
	}
	st, ok := a.runtime.Status(uid)
	if !ok {
		return true
	}
	return st.DisconnectReason == 0 && (st.StateName == robotcap.RuntimeStateInit || st.StateName == robotcap.RuntimeStateLogin)
}

func (a *Actor) onlineAttemptTimedOut(uid int, now time.Time, rc robotconfig.RuntimeConfig) bool {
	lastOnlineTry := a.lastOnlineTryValue()
	if lastOnlineTry.IsZero() {
		return false
	}
	timeout := time.Duration(rc.OnlineConfirmTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if now.Sub(lastOnlineTry) < timeout {
		return false
	}
	st, ok := a.runtime.Status(uid)
	if !ok {
		return true
	}
	return st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0
}

func (a *Actor) lastOnlineTryValue() time.Time {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.lastOnlineTry
}

func (a *Actor) setLastOnlineTry(t time.Time) {
	a.stateMu.Lock()
	a.lastOnlineTry = t
	a.stateMu.Unlock()
}

func (a *Actor) recordFailure(now time.Time, class string) int {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.failures++
	a.failureClass = class
	if a.firstFailureAt.IsZero() {
		a.firstFailureAt = now
	}
	return a.failures
}

func (a *Actor) failureClassValue() string {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.failureClass
}

func (a *Actor) setNextRetryAt(t time.Time) {
	a.stateMu.Lock()
	a.nextRetryAt = t
	a.stateMu.Unlock()
}

func (a *Actor) nextRetryAtValue() time.Time {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.nextRetryAt
}

func (a *Actor) runBusy(kind string, fn func()) {
	a.setBusy(true, kind)
	defer a.setBusy(false, "")
	fn()
}

func (a *Actor) setBusy(v bool, kind string) {
	a.stateMu.Lock()
	a.busy = v
	a.busyKind = kind
	if v {
		a.state = StateBusy
	} else if a.uid > 0 {
		if a.onlineDesired {
			a.state = StateRunning
		} else {
			a.state = StateOffline
		}
	}
	a.stateMu.Unlock()
}

func (a *Actor) busyValue() bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.busy
}

func (a *Actor) setState(state State) {
	a.stateMu.Lock()
	a.state = state
	a.stateMu.Unlock()
}

func (a *Actor) snapshot() Snapshot {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return Snapshot{
		SlotID:         a.slotID,
		UID:            a.uid,
		Mode:           a.mode,
		State:          a.state,
		Busy:           a.busy,
		BusyKind:       a.busyKind,
		OnlineDesired:  a.onlineDesired,
		LastOnlineTry:  a.lastOnlineTry,
		NextRetryAt:    a.nextRetryAt,
		FirstFailureAt: a.firstFailureAt,
		Failures:       a.failures,
		FailureClass:   a.failureClass,
		Quarantined:    a.quarantined,
	}
}

func (a *Actor) quarantineCurrentUID() {
	a.stateMu.Lock()
	a.quarantined = a.uid > 0
	a.busy = false
	a.busyKind = ""
	a.onlineDesired = false
	a.releaseRequested = true
	if a.uid > 0 {
		a.state = StateReleasing
	}
	a.stateMu.Unlock()
}

func (a *Actor) leaseIdentity() (int, uint64) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.uid, a.generation
}

func (a *Actor) uidValue() int {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.uid
}

func (a *Actor) slotIDValue() int {
	if a == nil {
		return 0
	}
	return a.slotID
}

func (a *Actor) modeValue() Mode {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.mode
}

func (a *Actor) SetMode(mode Mode) {
	a.stateMu.Lock()
	a.mode = mode
	a.stateMu.Unlock()
}

func (a *Actor) stateValue() State {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.state
}

func (a *Actor) Status(now time.Time, rc robotconfig.RuntimeConfig) Status {
	return a.status(now, rc)
}

func (a *Actor) Snapshot() Snapshot {
	return a.snapshot()
}

func (a *Actor) UIDValue() int {
	return a.uidValue()
}

func (a *Actor) SlotIDValue() int {
	return a.slotIDValue()
}

func (a *Actor) ModeValue() Mode {
	return a.modeValue()
}
