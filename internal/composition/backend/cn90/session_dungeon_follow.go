package cn90

import (
	"context"
	"time"

	foundationlog "robot/internal/foundation/log"
)

// Follower-side room hunting.
//
// A party follower never walks a run. The server owns the walk: when the
// leader's op45 commits, moveRuntimePartyFollowers moves every member's
// runtime into the next room and pushes each member its own op29 room
// projection (dnfbridge/dungeon_navigation_runtime.go). What a follower can
// still do is clear the room it was moved into. This protocol generation keeps
// no monster hit points, so a room clears when every blocking actor has been
// reported dead with op39, and a party member's report is validated against its
// own runtime and then projected to the rest of the room
// (dnfbridge/dungeon_party_combat_runtime.go
// handleRuntimePartyDungeonMonsterDeathRequest). That is the whole duty: watch
// the room projections, report the ordinary monsters of each new room dead, and
// never send op45 - a follower that also moved would fight the leader's walk.
//
// Only ordinary monsters are reported. The boss row is deliberately left to the
// run's leader: its death opens the op31 settlement entry for every member, and
// a follower that closed the run the instant the party entered the boss room
// would take the clear away from the character whose walk it is. The human-led
// case is the one that matters - the player crosses a door with no delay
// because the room behind it is already clear, while the boss room stays
// theirs.
//
// The duty runs only when the server pulled this session into someone else's
// run (dungeonState.followed). A session that opened its own run walks it with
// ClearDungeon instead, and two walkers on one run would report the same deaths
// twice.

const (
	// dungeonFollowRoomTimeout bounds the death reports of one room. The writes
	// go to a socket that is already being written by the settlement worker
	// only after a run settles, so the ceiling only stops a stalled connection
	// from holding the run's room signal forever.
	dungeonFollowRoomTimeout = 15 * time.Second
)

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
// session did not open.
func (s *Session) startDungeonFollowDuty(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
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

// clearFollowedRoom reports the ordinary monsters of the newest room dead. It
// returns immediately for anything that is not a follower's room projection, so
// the leader's own walk and every town session pass through untouched.
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
	keys := make([]uint16, 0, len(view.Actors))
	for _, actor := range view.Actors {
		if !actor.Monster() || actor.Boss() || state.reported[actor.ObjectKey] {
			continue
		}
		keys = append(keys, actor.ObjectKey)
	}
	if len(keys) == 0 {
		return
	}
	foundationlog.Robotf("CN90_DUNGEON_FOLLOW_ROOM cid=%d room=%d:%d map=%d monsters=%d\n",
		s.selfCharacterID, view.X, view.Y, view.MapID, len(keys))
	roomCtx, cancel := context.WithTimeout(ctx, dungeonFollowRoomTimeout)
	defer cancel()
	for index, key := range keys {
		if err := s.client.ReportMonsterDeath(roomCtx, uint32(key), s.selfCharacterID); err != nil {
			foundationlog.Robotf("CN90_DUNGEON_FOLLOW_KILL_FAILED cid=%d room=%d:%d object_key=%d err=%v\n",
				s.selfCharacterID, view.X, view.Y, key, err)
			return
		}
		state.reported[key] = true
		if index < len(keys)-1 {
			if err := dungeonClearSleep(roomCtx, dungeonClearKillInterval); err != nil {
				return
			}
		}
	}
}
