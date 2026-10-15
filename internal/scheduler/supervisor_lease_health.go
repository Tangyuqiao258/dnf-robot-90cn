package scheduler

import (
	"context"
	"sort"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

const quarantineLogInterval = 5 * time.Minute

func (s *RobotSupervisor) releaseBrokenLeases(now time.Time, rc robotconfig.RuntimeConfig) {
	if !s.nextLeaseHealth.IsZero() && now.Before(s.nextLeaseHealth) {
		return
	}
	interval := time.Duration(rc.SchedulerMetricsIntervalSec) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	s.nextLeaseHealth = now.Add(interval)

	leases := s.ledger.LeaseSnapshots()
	if len(leases) == 0 {
		return
	}
	uids := make([]int, 0, len(leases))
	for _, item := range leases {
		uids = append(uids, item.UID)
	}
	alive, err := s.manager.aliveRobotUIDs(uids)
	if err != nil {
		robotLogf("[RobotSupervisor] lease_health_check_failed err=%v\n", err)
		return
	}
	for _, item := range leases {
		if alive[item.UID] && !item.Blocked {
			continue
		}
		if !s.ledger.BlockLeaseIfCurrent(item.UID, item.Actor) {
			continue
		}
		robotLogf("[RobotSupervisor] broken_lease uid=%d slot=%d action=release_cleanup\n", item.UID, item.Actor.SlotIDValue())
		released := item.Actor.ReleaseAndWait(10 * time.Second)
		if released != item.UID && released > 0 {
			s.ledger.UnleaseUID(released, item.Actor)
		}
		if released == item.UID || item.Actor.UIDValue() != item.UID {
			s.ledger.RemoveLeaseIfActor(item.UID, item.Actor)
			s.cleanupBrokenUID(item.UID)
		}
	}
}

func (s *RobotSupervisor) cleanupBrokenUID(uid int) {
	if uid <= 0 {
		return
	}
	result, err := s.manager.CleanupRobots(robotcap.CleanupRequest{UIDs: []int{uid}, Force: true, InternalConfirmedBroken: true})
	if err != nil {
		robotLogf("[RobotSupervisor] broken_cleanup_failed uid=%d err=%v\n", uid, err)
		return
	}
	if result.Deleted <= 0 {
		robotLogf("[RobotSupervisor] broken_cleanup_skipped uid=%d requested=%d skipped=%d\n", uid, result.Requested, result.Skipped)
		return
	}
	if s.ledger.ClearQuarantine(uid) {
		robotLogf("[RobotSupervisor] broken_cleanup_released uid=%d deleted=%d\n", uid, result.Deleted)
	} else {
		s.ledger.UnblockUID(uid)
	}
	robotLogf("[RobotSupervisor] broken_cleanup_done uid=%d deleted=%d skipped=%d\n", uid, result.Deleted, result.Skipped)
}

func (s *RobotSupervisor) cleanupBlockedUIDs(limit int) {
	for _, uid := range s.ledger.BlockedUIDs(limit) {
		if s.ledger.IsQuarantinedUID(uid) {
			s.logQuarantineDeferred(uid)
			continue
		}
		if s.actorOwnsUID(uid) {
			robotLogf("[RobotSupervisor] blocked_cleanup_deferred uid=%d reason=actor_still_attached\n", uid)
			continue
		}
		if s.runtime != nil && s.runtime.IsActive(uid) {
			closer, ok := s.runtime.(actorRuntimeForceCloser)
			if !ok || !closer.ForceClose(uid) {
				robotLogf("[RobotSupervisor] blocked_cleanup_deferred uid=%d reason=runtime_still_active\n", uid)
				continue
			}
		}
		s.cleanupBrokenUID(uid)
	}
}

func (s *RobotSupervisor) actorOwnsUID(uid int) bool {
	return s.ledger.ActorOwnsUID(uid)
}

// logQuarantineDeferred throttles the per-UID quarantine notice; the cleanup
// loop runs every second and a stuck UID would otherwise flood the log.
func (s *RobotSupervisor) logQuarantineDeferred(uid int) {
	if s.quarantineLogAt == nil {
		s.quarantineLogAt = make(map[int]time.Time)
	}
	now := time.Now()
	if loggedAt, ok := s.quarantineLogAt[uid]; ok && now.Sub(loggedAt) < quarantineLogInterval {
		return
	}
	s.quarantineLogAt[uid] = now
	robotLogf("[RobotSupervisor] blocked_cleanup_deferred uid=%d reason=logout_quarantine\n", uid)
}

func (m *RobotManager) aliveRobotUIDs(uids []int) (map[int]bool, error) {
	if m.robotState != nil {
		robots, err := m.robotState.SelectRobots(context.Background(), robotcap.CommandRequest{UIDs: append([]int(nil), uids...)})
		if err != nil {
			return nil, err
		}
		alive := make(map[int]bool, len(robots))
		for _, robot := range robots {
			alive[robot.UID] = true
		}
		return alive, nil
	}
	return nil, errSchedulerStorageUnavailable
}

func (s *RobotSupervisor) recycleActorUID(actor *actormodel.Actor, status actormodel.Status) {
	if status.UID <= 0 {
		return
	}
	robotLogf("[RobotSupervisor] recycle_uid slot=%d uid=%d health=%s reason=%s failures=%d first_failure=%s state=%s\n",
		status.SlotID, status.UID, status.Health, status.HealthReason, status.Failures, status.FirstFailureAt.Format(time.RFC3339), status.State)
	released := actor.ReleaseAndWait(15 * time.Second)
	if released != status.UID {
		robotLogf("[RobotSupervisor] recycle_deferred slot=%d uid=%d released=%d reason=runtime_close_unconfirmed\n",
			status.SlotID, status.UID, released)
		return
	}
	s.ledger.RemoveLeaseIfActor(released, actor)
	result, err := s.manager.CleanupRobots(robotcap.CleanupRequest{UIDs: []int{released}, Force: true, InternalConfirmedBroken: true})
	if err != nil {
		robotLogf("[RobotSupervisor] recycle_cleanup_failed uid=%d err=%v\n", released, err)
		s.ledger.BlockUID(released)
		return
	}
	robotLogf("[RobotSupervisor] recycle_cleanup_done uid=%d deleted=%d skipped=%d\n", released, result.Deleted, result.Skipped)
}

// recycleDesiredOfflineAutoActors releases the lease of auto actors that an
// operator logged out (onlineDesired=false while still holding a UID). Without
// this sweep the slot keeps its UID forever and the target online count can
// never recover without a process restart.
//
// The sweep runs one bounded batch at a time, off the tick, with a pause before
// the next. Each release is a real logout that waits for the session to close,
// so releasing a whole layer inside one tick would both stall the supervisor and
// hand the server a disconnect storm in the middle of its own login fill. The
// pause is the pace the demand layer turns its population over at, and the
// batch is what keeps that pace from depending on how many robots changed
// places at once.
func (s *RobotSupervisor) recycleDesiredOfflineAutoActors(now time.Time, rc robotconfig.RuntimeConfig) {
	s.releaseMu.Lock()
	if s.releaseRunning || now.Before(s.releaseNext) {
		s.releaseMu.Unlock()
		return
	}
	batch := s.desiredOfflineAutoActors(robotconfig.OfflineReleaseBatch(rc))
	if len(batch) == 0 {
		s.releaseMu.Unlock()
		return
	}
	s.releaseRunning = true
	done := make(chan struct{})
	s.releaseDone = done
	gap := robotconfig.OfflineReleaseGap(rc)
	s.releaseMu.Unlock()

	go func() {
		defer close(done)
		for _, actor := range batch {
			s.releaseDesiredOfflineActor(actor)
		}
		s.releaseMu.Lock()
		s.releaseRunning = false
		if s.releaseDone == done {
			s.releaseDone = nil
		}
		s.releaseNext = time.Now().Add(gap)
		s.releaseMu.Unlock()
	}()
}

// desiredOfflineAutoActors lists the actors a batch should log out, oldest slot
// first, so a plan that has to shed more than one batch sheds the same slots
// first every time instead of picking a different set each sweep.
func (s *RobotSupervisor) desiredOfflineAutoActors(limit int) []*actormodel.Actor {
	if limit <= 0 {
		return nil
	}
	var candidates []*actormodel.Actor
	for _, actor := range s.ledger.AutoActorPointers() {
		snapshot := actor.Snapshot()
		if snapshot.Mode != actormodel.ModeAuto || snapshot.UID <= 0 {
			continue
		}
		if snapshot.OnlineDesired || snapshot.State != actormodel.StateOffline {
			continue
		}
		candidates = append(candidates, actor)
	}
	if len(candidates) <= limit {
		return candidates
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].SlotIDValue() < candidates[j].SlotIDValue()
	})
	return candidates[:limit]
}

func (s *RobotSupervisor) releaseDesiredOfflineActor(actor *actormodel.Actor) {
	snapshot := actor.Snapshot()
	if snapshot.UID <= 0 {
		return
	}
	robotLogf("[RobotSupervisor] recycle_offline_auto slot=%d uid=%d\n", snapshot.SlotID, snapshot.UID)
	released := actor.ReleaseAndWait(10 * time.Second)
	if released != snapshot.UID {
		robotLogf("[RobotSupervisor] recycle_offline_auto_deferred slot=%d uid=%d released=%d\n",
			snapshot.SlotID, snapshot.UID, released)
		return
	}
	s.ledger.RemoveLeaseIfActor(released, actor)
}

// waitForOfflineReleaseSweep blocks until the in-flight batch has finished. The
// shutdown path calls it so a release cannot still be closing sessions while the
// fleet is being torn down.
func (s *RobotSupervisor) waitForOfflineReleaseSweep(timeout time.Duration) {
	s.releaseMu.Lock()
	done := s.releaseDone
	s.releaseMu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (s *RobotSupervisor) recycleUnhealthyActors(now time.Time, rc robotconfig.RuntimeConfig) {
	type recycleCandidate struct {
		actor  *actormodel.Actor
		status actormodel.Status
	}
	var unhealthy []recycleCandidate
	for _, actor := range s.ledger.AutoActorPointers() {
		status := actor.Status(now, rc)
		if status.RecycleUID {
			unhealthy = append(unhealthy, recycleCandidate{actor: actor, status: status})
		}
	}
	if len(s.recycleCooldown) > 0 {
		for uid, until := range s.recycleCooldown {
			if !now.Before(until) {
				delete(s.recycleCooldown, uid)
			}
		}
	}
	for _, item := range unhealthy {
		uid := item.status.UID
		if until, cooling := s.recycleCooldown[uid]; cooling && now.Before(until) {
			s.skipRecycleInCooldown(item.actor, item.status, until)
			continue
		}
		if s.recycleCooldown == nil {
			s.recycleCooldown = make(map[int]time.Time)
		}
		s.recycleCooldown[uid] = now.Add(time.Duration(rc.SchedulerRecycleCooldownSec) * time.Second)
		s.recycleActorUID(item.actor, item.status)
	}
}

// skipRecycleInCooldown releases a repeatedly failing UID without deleting and
// recreating its character again. The UID stays blocked until manual cleanup,
// so a data failure cannot turn into a delete/recreate loop.
func (s *RobotSupervisor) skipRecycleInCooldown(actor *actormodel.Actor, status actormodel.Status, until time.Time) {
	released := actor.ReleaseAndWait(15 * time.Second)
	if released != status.UID {
		robotLogf("[RobotSupervisor] recycle_cooldown_deferred slot=%d uid=%d released=%d until=%s\n",
			status.SlotID, status.UID, released, until.Format(time.RFC3339))
		return
	}
	s.ledger.RemoveLeaseIfActor(released, actor)
	s.ledger.BlockUID(released)
	robotLogf("[RobotSupervisor] recycle_cooldown_skip slot=%d uid=%d until=%s\n", status.SlotID, status.UID, until.Format(time.RFC3339))
}
