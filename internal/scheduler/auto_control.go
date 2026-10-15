package scheduler

import (
	"errors"
	"net"
	"strconv"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

func (m *RobotManager) StartAutoActions() {
	m.autoMu.Lock()
	if m.supervisor != nil {
		if stopped, err := m.supervisor.stoppedResult(); !stopped || err != nil {
			m.autoMu.Unlock()
			return
		}
		m.supervisor = nil
	}
	runtime := NewRobotRuntime(m)
	supervisor := NewRobotSupervisor(m, runtime)
	m.supervisor = supervisor
	m.autoStoreBusy = make(map[int]bool)
	m.autoPortSince = time.Time{}
	m.autoPortReady = false
	m.autoPortLog = time.Time{}
	m.autoEnabled = true
	m.autoMu.Unlock()
	m.StartRuntimeFileWatcher()
	supervisor.Start()
}

func (m *RobotManager) stopAutoActions() error {
	m.autoMu.Lock()
	supervisor := m.supervisor
	m.autoEnabled = false
	m.autoMu.Unlock()
	if supervisor == nil {
		return nil
	}
	if err := supervisor.StopWithError(); err != nil {
		return err
	}
	m.autoMu.Lock()
	if m.supervisor == supervisor {
		m.supervisor = nil
	}
	m.autoMu.Unlock()
	return nil
}

func (m *RobotManager) Shutdown() error {
	if m == nil {
		return nil
	}
	m.shutdownOnce.Do(func() {
		m.stopAndWaitBackgroundWork()
		autoErr := m.stopAutoActions()
		m.stopRuntimeFileWatcher()
		m.waitMailNotifications()
		m.flushStorePointCache()
		batcher := m.positionWrites.Swap(nil)
		if batcher == nil {
			m.shutdownErr = autoErr
			return
		}
		m.shutdownErr = errors.Join(autoErr, batcher.Close())
	})
	return m.shutdownErr
}

// pauseAutoActions temporarily suspends automatic actions without touching the
// persisted config. Internal operations (dangerous delete) must not flip the
// operator's auto_actions setting: an earlier implementation disabled auto via
// SetAutoEnabled and never restored it, so one delete left the robot in manual
// mode until someone noticed.
func (m *RobotManager) pauseAutoActions() func() {
	m.autoMu.Lock()
	wasEnabled := m.autoEnabled
	m.autoEnabled = false
	m.autoMu.Unlock()
	return func() {
		m.autoMu.Lock()
		m.autoEnabled = wasEnabled
		m.autoMu.Unlock()
	}
}

func (m *RobotManager) SetAutoEnabled(enabled bool) (robotcap.AutoStatus, error) {
	if err := m.writeRobotConfigValues(map[string]string{
		"auto.auto_actions": strconv.FormatBool(enabled),
	}); err != nil {
		return m.AutoStatus(), err
	}
	m.autoMu.Lock()
	m.autoEnabled = enabled
	m.autoMu.Unlock()
	return m.AutoStatus(), nil
}

func (m *RobotManager) stopAutoActorsForDisabledConfig(supervisor *RobotSupervisor, rc robotconfig.RuntimeConfig) {
	if supervisor == nil {
		return
	}
	end := m.beginActorContainerOp("auto_stop")
	defer end()
	supervisor.stopAutoActors(rc)
	summary := robotcap.SummarizeRuntimeStatusMap(m.runtimeStatusMap())
	m.updateAutoSnapshot(rc, summary)
	m.updateAutoActorSnapshot(supervisor.actorCounts(time.Now(), rc))
	m.updateSchedulerStatus(rc, m.adaptiveSchedulerSignals(), schedulerPolicyDecision{Mode: schedulerPolicyManual, Reason: schedulerReasonAutoDisabled})
}

func (m *RobotManager) AutoStatus() robotcap.AutoStatus {
	rc := m.loadRobotConfig()
	summary := m.runtimeStatusSummarySnapshot()
	running, connecting, stores := summary.Running, summary.Connecting, summary.Stores
	m.autoMu.Lock()
	out := m.autoStats
	out.Enabled = m.autoEnabled && rc.AutoActions
	m.autoMu.Unlock()
	out.TargetOnline = rc.AutoTargetOnlineCount
	out.Running = running
	out.Connecting = connecting
	if out.GamePortAddress == "" {
		out.GamePortAddress = m.robotGamePortAddress()
	}
	out.StoreRunning = stores
	out.StoreItemRunning = summary.ItemStores
	out.StoreDisjointRunning = summary.DisjointStores
	out.UpdatedAt = time.Now()
	return out
}

func (m *RobotManager) autoActionsEnabled(rc robotconfig.RuntimeConfig) bool {
	m.autoMu.Lock()
	enabled := m.autoEnabled
	m.autoMu.Unlock()
	return enabled && rc.AutoActions
}

func (m *RobotManager) autoGamePortStable(now time.Time, rc robotconfig.RuntimeConfig) bool {
	addr := m.robotGamePortAddress()
	timeout := time.Duration(rc.AutoGamePortCheckTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	m.autoMu.Lock()
	probeCached := addr == m.autoPortProbeAddr && now.Before(m.autoPortProbeAt)
	open := m.autoPortProbeOpen
	errText := m.autoPortProbeError
	dial := m.autoPortDial
	backendProbe := m.autoPortProbe
	probeInFlight := m.autoPortProbeInflight
	m.autoMu.Unlock()
	if !probeCached && !probeInFlight {
		if dial == nil {
			dial = net.DialTimeout
		}
		// Probing runs in the background: a black-holed game port must not
		// stall the scheduler tick for the whole dial timeout.
		probeAddr := addr
		probeAt := now
		m.autoMu.Lock()
		m.autoPortProbeInflight = true
		m.autoMu.Unlock()
		go func() {
			probeOpen := false
			probeErr := ""
			if backendProbe != nil {
				probeOpen = backendProbe()
				if !probeOpen {
					probeErr = "backend readiness probe is not ready"
				}
			} else {
				conn, err := dial("tcp", probeAddr, timeout)
				probeOpen = err == nil
				if err != nil {
					probeErr = err.Error()
				}
				if conn != nil {
					_ = conn.Close()
				}
			}
			probeTTL := time.Second
			if !probeOpen {
				probeTTL = 3 * time.Second
			}
			m.autoMu.Lock()
			m.autoPortProbeAddr = probeAddr
			m.autoPortProbeOpen = probeOpen
			m.autoPortProbeError = probeErr
			m.autoPortProbeAt = probeAt.Add(probeTTL)
			m.autoPortProbeInflight = false
			m.autoMu.Unlock()
		}()
	}

	stableFor := time.Duration(rc.AutoGamePortStableSec) * time.Second
	if stableFor <= 0 {
		stableFor = 15 * time.Second
	}

	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	m.autoStats.GamePortAddress = addr
	if !open {
		if m.autoPortReady || now.Sub(m.autoPortLog) >= 10*time.Second {
			robotLogf("[AutoGate] game_port_not_ready addr=%s err=%s\n", addr, errText)
			m.autoPortLog = now
		}
		m.autoPortSince = time.Time{}
		m.autoPortReady = false
		m.autoStats.GamePortReady = false
		m.autoStats.GamePortStableAt = time.Time{}
		m.autoStats.UpdatedAt = now
		return false
	}
	if m.autoPortSince.IsZero() {
		m.autoPortSince = now
	}
	stableAt := m.autoPortSince.Add(stableFor)
	m.autoStats.GamePortStableAt = stableAt
	if now.Before(stableAt) {
		if now.Sub(m.autoPortLog) >= 10*time.Second {
			robotLogf("[AutoGate] game_port_wait_stable addr=%s stable_at=%s\n", addr, stableAt.Format(time.RFC3339))
			m.autoPortLog = now
		}
		m.autoPortReady = false
		m.autoStats.GamePortReady = false
		m.autoStats.UpdatedAt = now
		return false
	}
	if !m.autoPortReady {
		robotLogf("[AutoGate] game_port_stable addr=%s stable_for=%s\n", addr, stableFor)
	}
	m.autoPortReady = true
	m.autoStats.GamePortReady = true
	m.autoStats.UpdatedAt = now
	return true
}
