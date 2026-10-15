package robotconfig

import "time"

func TargetCapacity(rc RuntimeConfig) int {
	target := rc.AutoTargetOnlineCount
	if target < 0 {
		target = 0
	}
	if rc.MaxOnlineRobots > 0 && target > rc.MaxOnlineRobots {
		target = rc.MaxOnlineRobots
	}
	return target
}

// PoolCapacity is how many characters the robot directory should hold. It is
// the online target unless the operator asked for a larger pool.
//
// A pool wider than the online target is what lets a robot be logged in on
// demand -- one near the level of the player who just walked in -- without
// creating it first: a create is a provisioning round trip plus a loadout
// write, far too slow to answer "the player just moved". The surplus
// characters stay logged out until something asks for them.
func PoolCapacity(rc RuntimeConfig) int {
	pool := rc.PoolSize
	if target := TargetCapacity(rc); pool < target {
		pool = target
	}
	return pool
}

// OfflineReleaseBatch is how many robots one sweep of the demand layer may log
// out. The sweep is real logouts -- the session is closed and the lease
// released -- so it is bounded: a whole layer turning over at once is a
// disconnect storm against the server's login path, and the fleet's own slots
// spend that time neither online nor available.
func OfflineReleaseBatch(rc RuntimeConfig) int {
	batch := rc.SchedulerOfflineReleaseBatch
	if batch <= 0 {
		batch = 5
	}
	if batch > 120 {
		batch = 120
	}
	return batch
}

// OfflineReleaseGap is the pause between offline release sweeps.
func OfflineReleaseGap(rc RuntimeConfig) time.Duration {
	seconds := rc.SchedulerOfflineReleaseGapSec
	if seconds <= 0 {
		seconds = 5
	}
	if seconds > 3600 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}

// DemandPlayerPoll is how often the demand layer reads the player's position.
// The server writes a character's town, area and coordinates as the player
// moves, so this is the resolution at which the floating layer can react.
func DemandPlayerPoll(rc RuntimeConfig) time.Duration {
	seconds := rc.DemandPlayerPollSec
	if seconds <= 0 {
		seconds = 5
	}
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

// DemandPlayerGrace is how long the floating layer keeps its last set after the
// player stops being found. A logout, a closed client or one failed read must
// not read as "nobody is here" and pull a whole layer of robots offline; the
// grace window is what separates "the player left" from "the player blinked".
func DemandPlayerGrace(rc RuntimeConfig) time.Duration {
	seconds := rc.DemandPlayerGraceSec
	if seconds <= 0 {
		seconds = 600
	}
	if seconds > 86400 {
		seconds = 86400
	}
	return time.Duration(seconds) * time.Second
}

func CreateRoom(rc RuntimeConfig, existing int) int {
	room := TargetCapacity(rc) - existing
	if room < 0 {
		return 0
	}
	return room
}

func ScaleUpBatch(rc RuntimeConfig) int {
	batch := rc.SchedulerOnlineBatchSize
	if batch < 0 {
		return 0
	}
	if batch <= 0 {
		batch = 20
	}
	if batch > 120 {
		batch = 120
	}
	return batch
}

func PendingActorLimit(target int, rc RuntimeConfig) int {
	if target <= 0 {
		return 1
	}
	limit := OnlineStartRate(rc) * 8
	if limit < target/10 {
		limit = target / 10
	}
	if limit < 5 {
		limit = 5
	}
	if limit > 120 {
		limit = 120
	}
	return limit
}

func ScaleDownBatch(current, target int) int {
	delta := current - target
	if delta <= 0 {
		return 0
	}
	batch := current / 25
	if current%25 != 0 {
		batch++
	}
	if batch < 5 {
		batch = 5
	}
	if batch > 50 {
		batch = 50
	}
	if batch > delta {
		batch = delta
	}
	return batch
}

func OnlineStartRate(rc RuntimeConfig) int {
	rate := rc.SchedulerOnlineStartRate
	if rate < 0 {
		return 0
	}
	if rate <= 0 {
		rate = 20
	}
	if rate > 60 {
		rate = 60
	}
	return rate
}

func OnlineStartRateForNeed(need int, rc RuntimeConfig) int {
	rate := OnlineStartRate(rc)
	if rate <= 0 {
		return 0
	}
	if need <= 0 {
		return rate
	}
	timeout := rc.SchedulerOnlineFillTimeout
	if timeout <= 0 {
		timeout = 60
	}
	required := (need + timeout - 1) / timeout
	if required > rate {
		rate = required
	}
	if rate > 60 {
		return 60
	}
	return rate
}

func NormalizedTarget(rc RuntimeConfig) int {
	target := rc.AutoTargetOnlineCount
	if target < 0 {
		target = 0
	}
	if rc.MaxOnlineRobots > 0 && target > rc.MaxOnlineRobots {
		target = rc.MaxOnlineRobots
	}
	if target <= 0 {
		target = 20
	}
	return target
}

func TargetScale(target int) int {
	scale := target / 100
	if target%100 != 0 {
		scale++
	}
	if scale < 1 {
		scale = 1
	}
	if scale > 10 {
		scale = 10
	}
	return scale
}

func Clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// OnlineRetryBackoff returns the delay before the next login attempt for an
// actor that already failed `failures` times. The scheduler owns the base and
// cap through the adaptive config, so a load spike stretches every retry
// instead of only the initial fill. Jitter keeps the swarm from retrying in
// lockstep.
func OnlineRetryBackoff(rc RuntimeConfig, failures int, randIntn func(int) int) time.Duration {
	base := rc.SchedulerOnlineRetryBaseMS
	if base < 1000 {
		base = 5000
	}
	max := rc.SchedulerOnlineRetryMaxMS
	if max < base {
		max = base
	}
	shift := failures - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 6 {
		shift = 6
	}
	delay := base << shift
	if delay > max || delay <= 0 {
		delay = max
	}
	jitter := rc.SchedulerOnlineRetryJitterPct
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 50 {
		jitter = 50
	}
	if jitter > 0 && randIntn != nil {
		span := delay * jitter / 100
		if span > 0 {
			delay += randIntn(2*span+1) - span
		}
	}
	if delay < base/2 {
		delay = base / 2
	}
	return time.Duration(delay) * time.Millisecond
}
