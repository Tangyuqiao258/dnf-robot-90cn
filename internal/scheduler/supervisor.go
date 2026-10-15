package scheduler

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	actormodel "robot/internal/actor"
	"robot/internal/foundation/lockhub"
)

type RobotSupervisor struct {
	manager *RobotManager
	runtime actormodel.RobotRuntime

	ledger actormodel.Ledger

	stop  chan struct{}
	done  chan struct{}
	start sync.Once
	once  sync.Once

	shutdownMu         lockhub.Locker
	shutdownErr        error
	shutdownTimeout    time.Duration
	shutdownForceGrace time.Duration

	pressureMu      lockhub.Locker
	pressureRunning bool
	pressureDone    chan struct{}

	releaseMu      lockhub.Locker
	releaseRunning bool
	releaseDone    chan struct{}
	releaseNext    time.Time

	nextMetrics      time.Time
	nextGameGateLog  time.Time
	nextLeaseHealth  time.Time
	nextAnnouncement time.Time
	createFailures   int
	createNext       time.Time
	quarantineLogAt  map[int]time.Time
	recycleCooldown  map[int]time.Time

	// demand is the layer that decides who is online and where they stand.
	// A zero value is the fleet's plain behaviour: nothing pinned, everyone the
	// online fill reaches stays.
	demand demandMemory
}

func NewRobotSupervisor(manager *RobotManager, runtime actormodel.RobotRuntime) *RobotSupervisor {
	return &RobotSupervisor{
		manager:            manager,
		runtime:            runtime,
		ledger:             actormodel.NewLedger(),
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
		shutdownTimeout:    defaultSupervisorShutdownTimeout,
		shutdownForceGrace: defaultSupervisorForceGrace,
	}
}

func (s *RobotSupervisor) Start() {
	s.start.Do(func() { go s.loop() })
}

func (s *RobotSupervisor) Stop() {
	if err := s.StopWithError(); err != nil {
		robotLogf("[RobotSupervisor] shutdown_incomplete err=%v\n", err)
	}
}

func (s *RobotSupervisor) StopWithError() error {
	s.Start()
	s.once.Do(func() { close(s.stop) })
	<-s.done
	_, err := s.stoppedResult()
	return err
}

func (s *RobotSupervisor) loop() {
	defer close(s.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			s.shutdownMu.Lock()
			s.shutdownErr = s.shutdownActorsSafely()
			s.shutdownMu.Unlock()
			return
		case now := <-ticker.C:
			s.runSafely("tick", func() { s.tick(now) })
		}
	}
}

func (s *RobotSupervisor) runSafely(operation string, fn func()) (ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			robotLogf("[RobotSupervisor] panic operation=%s err=%v\n%s", operation, rec, debug.Stack())
			ok = false
		}
	}()
	fn()
	return true
}

func (s *RobotSupervisor) shutdownActorsSafely() (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			robotLogf("[RobotSupervisor] panic operation=shutdown err=%v\n%s", rec, debug.Stack())
			err = fmt.Errorf("supervisor shutdown panic: %v", rec)
		}
	}()
	return s.shutdownActors()
}

func (s *RobotSupervisor) tick(now time.Time) {
	s.ledger.ReapDoneDraining()
	signals := s.manager.adaptiveSchedulerSignals()
	rc, decision := s.manager.refreshAdaptiveRobotConfig(signals)
	s.manager.updateSchedulerStatus(rc, signals, decision)
	s.sendSystemAnnouncementIfDue(now, rc)
	s.manager.pollMailNotifications(now, rc)
	if s.handleAutoGuards(now, rc, signals) {
		return
	}
	if adopted := s.ledger.AdoptManualActors(); adopted > 0 {
		robotLogf("[RobotSupervisor] adopted_manual_actors count=%d\n", adopted)
	}
	s.maintainTarget(rc)
	// The demand layer runs after target maintenance so it sees the same actor
	// population the fill is about to draw from, and before the offline sweep so
	// a robot it stands down is released on this tick rather than the next.
	s.applyOnlineDemand(now, rc)
	s.releaseBrokenLeases(now, rc)
	s.cleanupBlockedUIDs(10)
	s.recycleUnhealthyActors(now, rc)
	s.recycleDesiredOfflineAutoActors(now, rc)
	s.assignIdleAutoActors(rc)
	s.updateMetrics(rc, signals)
}
