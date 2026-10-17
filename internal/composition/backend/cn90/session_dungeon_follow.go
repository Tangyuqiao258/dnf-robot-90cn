package cn90

import (
	"context"
	"math/rand"
	"time"

	foundationlog "robot/internal/foundation/log"
)

// Follower-side room hunting.
//
// A party follower never walks a run. The server owns the walk: when the
// leader's op45 commits, moveRuntimePartyFollowers moves every member's
// runtime into the next room and pushes each member its own op29 room
// projection (dnfbridge/dungeon_navigation_runtime.go). What a follower can
// still do is fight the room it was moved into. This protocol generation keeps
// no monster hit points, so a room clears when every blocking actor has been
// reported dead with op39, and a party member's report is validated against its
// own runtime and then projected to the rest of the room
// (dnfbridge/dungeon_party_combat_runtime.go
// handleRuntimePartyDungeonMonsterDeathRequest). That is the whole duty: watch
// the room projections, fight each new room's actors the way a player does, and
// never send op45 - a follower that also moved would fight the leader's walk.
//
// Every blocking actor is fought, bosses and hostile AI/APC bosses included:
// the follower is a player at the keyboard, and a player fights whatever stands
// in the room. The deaths are paced like a real fight (engage delay, one kill
// every couple of seconds, a long boss fight) instead of the instant sweep the
// duty used to make - a follower that reports the whole room dead before its
// leader has even loaded is not playing the game, and it steals the kill credit
// and drops from the human who actually fights the monsters.
//
// The duty runs only when the server pulled this session into someone else's
// run (dungeonState.followed). A session that opened its own run walks it with
// ClearDungeon instead, and two walkers on one run would report the same deaths
// twice.

// The pace knobs are variables rather than constants so tests can shrink them;
// production always uses the values set here.
var (
	// dungeonFollowRoomTimeout bounds one room, boss fight included. The writes
	// go to a socket that is already being written by the settlement worker
	// only after a run settles, so the ceiling only stops a stalled connection
	// from holding the run's room signal forever.
	dungeonFollowRoomTimeout = 2 * time.Minute
	// dungeonFollowEngageDelay is how long the follower "walks into" a newly
	// projected room before the first swing. A player reads the room, picks a
	// target and closes in; reporting a death the instant the projection lands
	// is the shape that cleared rooms while the human was still loading.
	dungeonFollowEngageDelayMin = 900 * time.Millisecond
	dungeonFollowEngageDelayMax = 1800 * time.Millisecond
	// dungeonFollowKillDelay spaces the ordinary kills of a room. It is the
	// time one monster takes to die under a real player's rotation, not a wire
	// pacing knob.
	dungeonFollowKillDelayMin = 1400 * time.Millisecond
	dungeonFollowKillDelayMax = 3000 * time.Millisecond
	// dungeonFollowBossFight is the fight length for a boss or hostile AI/APC
	// boss row. The human fights the same boss in the same room; the follower's
	// report only lands after a fight of comparable length, so whichever client
	// actually finishes the boss wins the race instead of the follower sweeping
	// the room on arrival.
	dungeonFollowBossFightMin = 22 * time.Second
	dungeonFollowBossFightMax = 40 * time.Second
)

// dungeonFollowDelay returns a random duration in [min, max].
func dungeonFollowDelay(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)+1))
}

// dungeonFollowRun is the worker's bookkeeping across signals. It lives in the
// worker goroutine only.
type dungeonFollowRun struct {
	// since identifies the run the bookkeeping belongs to. The dungeon state
	// resets its room sequence on every run, and so does this state.
	since time.Time
	// seq is the last room projection acted on.
	seq uint64
	// reported is the object keys this session already reported dead in this
	// run. A layered room can be projected twice under one coordinate, and a
	// repeat report of a dead actor would only be dropped server-side.
	reported map[uint16]bool
}

// startDungeonFollowDuty installs the queue and the worker that hunts the room
// a follower was moved into. Like the settlement worker it is installed for
// every tracked session and costs nothing until the server announces a run this
// session did not open. A session opened without the follow-combat switch gets
// no queue at all: its room projections are still recorded and its per-room
// acknowledgement still goes out, but the deaths are left to whoever drives
// the character (the human client on a natively placed actor), so the two
// sides never race to report the same room.
func (s *Session) startDungeonFollowDuty(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if !s.dungeonFollowCombat {
		return
	}
	if s.dungeon == nil {
		s.dungeon = &dungeonState{}
	}
	queue := make(chan struct{}, 1)
	s.dungeon.mu.Lock()
	s.dungeon.roomQueue = queue
	s.dungeon.mu.Unlock()
	go func() {
		state := dungeonFollowRun{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case <-queue:
				s.clearFollowedRoom(ctx, &state)
			}
		}
	}()
}

// clearFollowedRoom fights the actors of the newest room the way a player
// would. It returns immediately for anything that is not a follower's room
// projection, so the leader's own walk and every town session pass through
// untouched.
func (s *Session) clearFollowedRoom(ctx context.Context, state *dungeonFollowRun) {
	if s == nil || s.dungeon == nil || state == nil {
		return
	}
	active, followed := s.dungeon.followState()
	if !active {
		*state = dungeonFollowRun{}
		return
	}
	if !followed {
		return
	}
	view, seq, ok := s.dungeon.roomSnapshot()
	if !ok {
		return
	}
	since := s.dungeon.startedAt()
	if state.since != since {
		state.since = since
		state.seq = 0
		state.reported = make(map[uint16]bool)
	}
	if seq == state.seq {
		return
	}
	state.seq = seq
	// A character can report nothing while it is dead, and a room projection
	// that arrives after the settlement entry belongs to a run that is already
	// over.
	if s.DungeonDead() || s.dungeon.settlementSnapshot() > 0 {
		return
	}
	type followTarget struct {
		key  uint16
		boss bool
	}
	targets := make([]followTarget, 0, len(view.Actors))
	for _, actor := range view.Actors {
		if !actor.Monster() || state.reported[actor.ObjectKey] {
			continue
		}
		targets = append(targets, followTarget{key: actor.ObjectKey, boss: actor.Boss()})
	}
	if len(targets) == 0 {
		return
	}
	foundationlog.Robotf("CN90_DUNGEON_FOLLOW_ROOM cid=%d room=%d:%d map=%d monsters=%d\n",
		s.selfCharacterID, view.X, view.Y, view.MapID, len(targets))
	roomCtx, cancel := context.WithTimeout(ctx, dungeonFollowRoomTimeout)
	defer cancel()
	// Walk into the room and pick the first target before the first report.
	if err := dungeonClearSleep(roomCtx, dungeonFollowDelay(
		dungeonFollowEngageDelayMin, dungeonFollowEngageDelayMax)); err != nil {
		return
	}
	for _, target := range targets {
		// The run can end under a long boss fight: the leader's own kill opens
		// the settlement entry for every member, and a dead follower reports
		// nothing. Stop instead of racing a settled run.
		if s.DungeonDead() || s.dungeon.settlementSnapshot() > 0 {
			return
		}
		delay := dungeonFollowDelay(dungeonFollowKillDelayMin, dungeonFollowKillDelayMax)
		if target.boss {
			delay = dungeonFollowDelay(dungeonFollowBossFightMin, dungeonFollowBossFightMax)
		}
		if err := dungeonClearSleep(roomCtx, delay); err != nil {
			return
		}
		if err := s.client.ReportMonsterDeath(roomCtx, uint32(target.key), s.selfCharacterID); err != nil {
			foundationlog.Robotf("CN90_DUNGEON_FOLLOW_KILL_FAILED cid=%d room=%d:%d object_key=%d err=%v\n",
				s.selfCharacterID, view.X, view.Y, target.key, err)
			return
		}
		state.reported[target.key] = true
		foundationlog.Robotf("CN90_DUNGEON_FOLLOW_KILL cid=%d room=%d:%d object_key=%d boss=%t\n",
			s.selfCharacterID, view.X, view.Y, target.key, target.boss)
	}
}
