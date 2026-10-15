package scheduler

import (
	"fmt"
	"strings"
	"time"

	actormodel "robot/internal/actor"
)

const (
	defaultSupervisorShutdownTimeout = 15 * time.Second
	defaultSupervisorForceGrace      = 2 * time.Second
	shutdownActorLogLimit            = 20
)

type actorRuntimeForceCloser interface {
	ForceClose(uid int) bool
}

func (s *RobotSupervisor) shutdownActors() error {
	timeout := s.shutdownTimeout
	if timeout <= 0 {
		timeout = defaultSupervisorShutdownTimeout
	}
	forceGrace := s.shutdownForceGrace
	if forceGrace <= 0 {
		forceGrace = defaultSupervisorForceGrace
	}
	if forceGrace >= timeout {
		forceGrace = timeout / 2
	}

	// An offline-release batch closes sessions in the background; let it finish
	// before the fleet is drained out from under it.
	s.waitForOfflineReleaseSweep(forceGrace)

	deadline := time.Now().Add(timeout)
	gracefulDeadline := deadline.Add(-forceGrace)
	actors := s.ledger.BeginDrainAllActors()
	requestActorStops(actors)
	pending := s.waitForDrainingActorsUntil(actors, gracefulDeadline)
	if len(pending) > 0 {
		robotLogf("[RobotSupervisor] shutdown_grace_expired pending=%d actors=%s\n", len(pending), describeActors(pending))
		attempted := s.forceCloseActors(pending)
		robotLogf("[RobotSupervisor] shutdown_force_close attempted=%d pending=%d\n", attempted, len(pending))
		pending = s.waitForDrainingActorsUntil(pending, deadline)
	}

	workersDone := s.waitPressureWorkerUntil(deadline)
	if len(pending) == 0 && workersDone {
		return nil
	}
	if len(pending) > 0 {
		robotLogf("[RobotSupervisor] shutdown_deadline_exceeded pending=%d actors=%s\n", len(pending), describeActors(pending))
	}
	return fmt.Errorf("supervisor shutdown timed out after %s: pending_actors=%d pressure_workers_done=%t actors=%s", timeout, len(pending), workersDone, describeActors(pending))
}

func (s *RobotSupervisor) forceCloseActors(actors []*actormodel.Actor) int {
	closer, ok := s.runtime.(actorRuntimeForceCloser)
	if !ok {
		return 0
	}
	attempted := 0
	for _, actor := range actors {
		uid := actor.UIDValue()
		if uid > 0 && closer.ForceClose(uid) {
			attempted++
		}
	}
	return attempted
}

func (s *RobotSupervisor) waitPressureWorkerUntil(deadline time.Time) bool {
	s.pressureMu.Lock()
	done := s.pressureDone
	s.pressureMu.Unlock()
	if done == nil {
		return true
	}
	wait := time.Until(deadline)
	if wait <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (s *RobotSupervisor) stoppedResult() (bool, error) {
	select {
	case <-s.done:
	default:
		return false, nil
	}

	s.shutdownMu.Lock()
	defer s.shutdownMu.Unlock()
	if s.shutdownErr != nil {
		s.ledger.ReapDoneDraining()
		if s.ledger.DrainingCount() == 0 && len(s.ledger.ActorPointers()) == 0 && s.pressureWorkerDone() {
			s.shutdownErr = nil
		}
	}
	return true, s.shutdownErr
}

func (s *RobotSupervisor) pressureWorkerDone() bool {
	s.pressureMu.Lock()
	running := s.pressureRunning
	done := s.pressureDone
	s.pressureMu.Unlock()
	if !running {
		return true
	}
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func describeActors(actors []*actormodel.Actor) string {
	if len(actors) == 0 {
		return "none"
	}
	limit := len(actors)
	if limit > shutdownActorLogLimit {
		limit = shutdownActorLogLimit
	}
	parts := make([]string, 0, limit+1)
	for _, actor := range actors[:limit] {
		snapshot := actor.Snapshot()
		parts = append(parts, fmt.Sprintf("uid=%d/slot=%d/state=%s/busy=%t/op=%s", snapshot.UID, snapshot.SlotID, snapshot.State, snapshot.Busy, actormodel.Operation(snapshot)))
	}
	if remaining := len(actors) - limit; remaining > 0 {
		parts = append(parts, fmt.Sprintf("and_%d_more", remaining))
	}
	return strings.Join(parts, ",")
}
