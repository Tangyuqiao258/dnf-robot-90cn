package cn90

import (
	"context"
	"fmt"
	"time"

	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// Clearing a run: the leader-side walk.
//
// The server owns the run. It keeps no monster hit points, so a room clears when
// the client has reported every actor of the room dead with op39, and a run
// advances when the client asks for a neighbouring room with op45 - a request
// the server plans from its own maze topology and answers with the room itself,
// another op29. The maze layout is never sent, so a client discovers it by
// asking: a move that is refused (a wall, or a room that is not cleared yet) is
// never answered at all.
//
// That makes the walk a plain depth-first search. The room the session stands in
// is the one the last op29 announced; the four neighbours are the four adjacent
// coordinates; a neighbour that answers within the room wait is reachable and is
// cleared next, and one that stays silent is a wall for this visit. When all four
// neighbours are known the walk steps back to the room it came from, which is a
// move it already proved. The boss room is the end: its boss is reported dead,
// op117 closes the run, the op31 settlement entry opens the settlement window
// and op46 states the play result.
//
// Only the leader walks. The server moves the party's followers itself on every
// accepted op45 and projects every death to them, so a follower that also walked
// would fight the run it was pulled into.

const (
	// dungeonClearInitialRoomWait covers the first op29 of a run. Entry has
	// already waited for the scene load, so this is only the room projection
	// racing the loading acknowledgement.
	dungeonClearInitialRoomWait = 10 * time.Second
	// dungeonClearRoomWaitTimeout is how long a room move may stay unanswered
	// before the target counts as a wall. A successful move is answered by the
	// next op29 on the same write path, so the window only has to cover a round
	// trip; the timeout is what a wall costs.
	dungeonClearRoomWaitTimeout = 2 * time.Second
	// dungeonClearPollInterval paces the state polls. The room projection and
	// the settlement entry are both written by the packet observer, so this only
	// reads local state.
	dungeonClearPollInterval = 100 * time.Millisecond
	// dungeonClearKillInterval spaces out the death reports of one room. The
	// server commits each report on its own path, and a room is small enough
	// that the spacing is invisible next to the room wait.
	dungeonClearKillInterval = 120 * time.Millisecond
	// dungeonClearSettleDelay lets the server finish committing the room's last
	// death before the move that depends on the room being clear.
	dungeonClearSettleDelay = 300 * time.Millisecond
	// dungeonClearSettlementWaitTimeout is the window for the op31 settlement
	// entry after the boss check. It is the gate op46 is validated against, so
	// the play result waits for it rather than being sent blind.
	dungeonClearSettlementWaitTimeout = 15 * time.Second
	// dungeonClearReturnWaitTimeout is the window for the town transition after
	// the play result. A run that settles slowly is not a failed clear: the exit
	// duty still holds op42 for it, so the wait is best effort.
	dungeonClearReturnWaitTimeout = 10 * time.Second
	// dungeonClearRoomBudget bounds one clear. A maze is a handful of rooms, so
	// the budget is the guard against a projection that keeps answering the same
	// coordinate rather than a limit a real run should approach.
	dungeonClearRoomBudget = 80
	// dungeonClearContextTimeout is the ceiling for one whole clear when the
	// caller does not bring its own deadline.
	dungeonClearContextTimeout = 6 * time.Minute
)

// dungeonClearRoom is one maze coordinate.
type dungeonClearRoom struct {
	x byte
	y byte
}

func (r dungeonClearRoom) String() string { return fmt.Sprintf("%d:%d", r.x, r.y) }

// dungeonClearNeighbours lists the four adjacent coordinates. The order is
// fixed so a retry of the same walk asks the same question first.
func dungeonClearNeighbours(room dungeonClearRoom) []dungeonClearRoom {
	next := make([]dungeonClearRoom, 0, 4)
	if room.x < 255 {
		next = append(next, dungeonClearRoom{x: room.x + 1, y: room.y})
	}
	if room.x > 0 {
		next = append(next, dungeonClearRoom{x: room.x - 1, y: room.y})
	}
	if room.y < 255 {
		next = append(next, dungeonClearRoom{x: room.x, y: room.y + 1})
	}
	if room.y > 0 {
		next = append(next, dungeonClearRoom{x: room.x, y: room.y - 1})
	}
	return next
}

// ClearDungeon walks and clears the run the session is inside, as its leader.
// It returns once the boss room has been settled, or with the reason the walk
// could not finish. The run itself is left active on failure; the caller's exit
// duty is what returns the character to town.
func (s *Session) ClearDungeon(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN dungeon clear needs an open session")
	}
	if s.dungeon == nil {
		return fmt.Errorf("90CN dungeon clear needs dungeon tracking")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dungeonClearContextTimeout)
		defer cancel()
	}
	if !s.InDungeon() {
		return fmt.Errorf("90CN dungeon clear needs an active run")
	}
	view, seq, ok := s.awaitDungeonRoom(ctx, 0, nil, dungeonClearInitialRoomWait)
	if !ok {
		return fmt.Errorf("90CN dungeon clear saw no room projection")
	}
	foundationlog.Robotf("CN90_DUNGEON_CLEAR_START cid=%d room=%d:%d map=%d\n",
		s.selfCharacterID, view.X, view.Y, view.MapID)

	type frame struct {
		room dungeonClearRoom
	}
	stack := []frame{{room: dungeonClearRoom{x: view.X, y: view.Y}}}
	visited := map[dungeonClearRoom]bool{{x: view.X, y: view.Y}: true}
	moves, rooms := 0, 1
	current := stack[0].room

	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("90CN dungeon clear stopped at room %s: %w", current, err)
		}
		// The room is cleared before anything else: op45's preflight refuses a
		// move out of a room that still has announced monsters alive.
		boss, err := s.clearDungeonRoom(ctx, view)
		if err != nil {
			return err
		}
		if boss != nil {
			return s.settleDungeonRun(ctx, *boss)
		}
		if moves >= dungeonClearRoomBudget {
			return fmt.Errorf("90CN dungeon clear exhausted its room budget at %s (%d rooms)", current, rooms)
		}

		advanced := false
		for _, next := range dungeonClearNeighbours(current) {
			if visited[next] {
				continue
			}
			moves++
			nextView, nextSeq, reached, err := s.stepDungeonRoom(ctx, next, seq)
			if err != nil {
				return err
			}
			if !reached {
				// A refused move is never answered. The coordinate stays
				// unvisited on purpose: a later visit may retry it, and the
				// server only ever refuses for a reason of that visit.
				foundationlog.Robotf("CN90_DUNGEON_WALL cid=%d from=%s to=%s\n",
					s.selfCharacterID, current, next)
				continue
			}
			visited[next] = true
			stack = append(stack, frame{room: next})
			current, view, seq = next, nextView, nextSeq
			rooms++
			foundationlog.Robotf("CN90_DUNGEON_ROOM cid=%d room=%s map=%d monsters=%d\n",
				s.selfCharacterID, current, view.MapID, len(view.Monsters()))
			advanced = true
			break
		}
		if advanced {
			continue
		}
		// Every neighbour is known and none was reachable that has not been
		// visited. The way out is the room this one was entered from, and that
		// move is already proven.
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			break
		}
		parent := stack[len(stack)-1].room
		moves++
		parentView, parentSeq, reached, err := s.stepDungeonRoom(ctx, parent, seq)
		if err != nil {
			return err
		}
		if !reached {
			return fmt.Errorf("90CN dungeon clear lost the way back from %s to %s", current, parent)
		}
		current, view, seq = parent, parentView, parentSeq
	}
	return fmt.Errorf("90CN dungeon clear explored %d rooms without a boss room", rooms)
}

// clearDungeonRoom reports every announced monster of the room dead and returns
// the room's boss row when it announced one. The boss is only returned after its
// own report is committed, because op117 refuses a boss the server has not seen
// die.
func (s *Session) clearDungeonRoom(ctx context.Context, view protocol.DungeonStartMap) (*protocol.DungeonStartMapActor, error) {
	keys := view.Monsters()
	for index, key := range keys {
		if err := s.client.ReportMonsterDeath(ctx, uint32(key), s.selfCharacterID); err != nil {
			return nil, fmt.Errorf("90CN dungeon clear reported %d dead: %w", key, err)
		}
		if index < len(keys)-1 {
			if err := dungeonClearSleep(ctx, dungeonClearKillInterval); err != nil {
				return nil, err
			}
		}
	}
	if len(keys) > 0 {
		foundationlog.Robotf("CN90_DUNGEON_ROOM_KILLED cid=%d room=%d:%d monsters=%d\n",
			s.selfCharacterID, view.X, view.Y, len(keys))
		if err := dungeonClearSleep(ctx, dungeonClearSettleDelay); err != nil {
			return nil, err
		}
	}
	if boss, ok := view.Boss(); ok {
		return &boss, nil
	}
	return nil, nil
}

// settleDungeonRun closes the run on a boss the server has already been told is
// dead: op117, the op31 settlement entry it answers with, and the op46 play
// result the entry gates.
func (s *Session) settleDungeonRun(ctx context.Context, boss protocol.DungeonStartMapActor) error {
	// The settlement entry can already be on the wire by the time this runs.
	// The server commits the clear on the boss's last op39 death report and
	// pushes op31 itself, nearly a room delay before the op117 below; op117 is
	// then an exact replay the server answers without a second entry. So the
	// wait below is for "this run has a settlement window", not for one that
	// arrives after the boss check.
	//
	// The settlement worker answers the same op31 from its own goroutine. The
	// leader claims the play result first: the worker stays quiet for one
	// resend interval and would only take over if this path's send never
	// landed.
	s.dungeon.claimPlayResult(time.Now())
	if err := s.client.CheckBossDeath(ctx, s.selfCharacterID, boss.ObjectKey); err != nil {
		return fmt.Errorf("90CN dungeon boss check: %w", err)
	}
	foundationlog.Robotf("CN90_DUNGEON_BOSS_CHECK cid=%d boss=%d\n", s.selfCharacterID, boss.ObjectKey)
	if !s.awaitDungeonSettlement(ctx, dungeonClearSettlementWaitTimeout) {
		return fmt.Errorf("90CN dungeon clear saw no settlement entry for boss %d", boss.ObjectKey)
	}
	if !s.dungeon.settlementHandshake().playResultSent {
		if err := s.client.SetPlayResult(ctx); err != nil {
			return fmt.Errorf("90CN dungeon play result: %w", err)
		}
		// The settlement worker owns the rest of the result-page chain and
		// would otherwise resend this same op46; recording it here keeps the
		// leader's chain one send per step. The body is the same fixed shape
		// either way, so a race between the two is a replay the server accepts.
		s.dungeon.markPlayResultSent(time.Now())
	}
	foundationlog.Robotf("CN90_DUNGEON_CLEARED cid=%d boss=%d\n", s.selfCharacterID, boss.ObjectKey)
	// The town transition is the server's own doing once the result is
	// produced; waiting for it keeps a caller from leaving a settled run behind,
	// but a slow settlement is the exit duty's business, not a failed clear.
	s.awaitDungeonTownReturn(ctx, dungeonClearReturnWaitTimeout)
	return nil
}

// stepDungeonRoom asks the server to move the run into the room at next. It
// reports the room the server answered with, which is the only proof the move
// was accepted; a refused move is never answered at all.
func (s *Session) stepDungeonRoom(ctx context.Context, next dungeonClearRoom, afterSeq uint64) (protocol.DungeonStartMap, uint64, bool, error) {
	if err := s.client.MoveDungeonRoom(ctx, next.x, next.y); err != nil {
		return protocol.DungeonStartMap{}, 0, false, fmt.Errorf("90CN dungeon move to %s: %w", next, err)
	}
	view, seq, ok := s.awaitDungeonRoom(ctx, afterSeq, &next, dungeonClearRoomWaitTimeout)
	return view, seq, ok, nil
}

// awaitDungeonRoom waits for a room projection newer than afterSeq. A nil target
// accepts any room; a target requires the room the server answered with to be
// the coordinate that was asked for.
func (s *Session) awaitDungeonRoom(ctx context.Context, afterSeq uint64, target *dungeonClearRoom, timeout time.Duration) (protocol.DungeonStartMap, uint64, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(dungeonClearPollInterval)
	defer ticker.Stop()
	for {
		view, seq, ok := s.dungeon.roomSnapshot()
		if ok && seq > afterSeq {
			if target == nil || (view.X == target.x && view.Y == target.y) {
				return view, seq, true
			}
		}
		select {
		case <-ctx.Done():
			return protocol.DungeonStartMap{}, 0, false
		case <-deadline.C:
			return protocol.DungeonStartMap{}, 0, false
		case <-ticker.C:
		}
	}
}

// awaitDungeonSettlement waits for the run's op31 settlement entry. Any entry
// for the current run counts: the sequence resets with the run, and the server
// pushes the entry as soon as it commits the clear, which is before the boss
// check the caller sends.
func (s *Session) awaitDungeonSettlement(ctx context.Context, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(dungeonClearPollInterval)
	defer ticker.Stop()
	for {
		if s.dungeon.settlementSnapshot() > 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

// awaitDungeonTownReturn waits for the run to stop being active, which the
// server says with the town transition. It reports whether the transition was
// seen; a false is a slow settlement, not a wrong one.
func (s *Session) awaitDungeonTownReturn(ctx context.Context, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(dungeonClearPollInterval)
	defer ticker.Stop()
	for {
		if !s.InDungeon() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

// dungeonClearSleep waits between two wire writes of one room. It shares the
// caller's context so a cancelled clear stops promptly.
func dungeonClearSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
