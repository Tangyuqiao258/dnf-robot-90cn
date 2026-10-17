package cn90

import (
	"context"
	"fmt"
	"time"

	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// Dungeon entry, death and revive.
//
// This protocol generation keeps no server-side character hit points: death is
// declared by the client with op40, and the server records it, answers with a
// class0/op32 notice and arms a ten second timer that returns the character to
// town unless an op41 revive arrives first. Both op40 and op41 are dropped
// without any reply while no run is active, so nothing here is meaningful
// outside a dungeon.
//
// The server makes a run active while handling op16, before it sends the
// acknowledgement, and it never waits for a client confirmation afterwards. So
// a successful op16 acknowledgement is itself proof that the run is live, and
// the op28/op29 notifications that follow are confirmation rather than a
// precondition.

const (
	// dungeonSelectAckTimeout covers the op15 selector reply and the op16 entry
	// acknowledgement. Both are answered from the same write path as an ordinary
	// command, so the window only has to cover a round trip.
	dungeonSelectAckTimeout = 10 * time.Second
	// dungeonDeathAckTimeout bounds the wait for a death or revive notice. A
	// death report has no failure reply at all, so a timeout is how a silently
	// dropped op40 shows up.
	dungeonDeathAckTimeout = 5 * time.Second
	// dungeonLoadingTimeout bounds the wait for the character-state push that
	// answers the loading acknowledgement. It is wider than a plain round trip
	// because the server builds the dungeon scene before it answers.
	dungeonLoadingTimeout = 10 * time.Second
)

// townPositionState is the last town coordinate the session reported with op35.
//
// The server binds the dungeon return origin from the position report it holds
// for the current town scene, and it clears that report whenever the town scene
// changes. An entry whose origin was never bound is dropped with
// `ordinary_dungeon_town_return_origin_unavailable`, without any reply, so the
// entry has to put a report in front of the selector itself rather than trust
// that some earlier move still counts.
type townPositionState struct {
	mu        lockhub.RWLocker
	known     bool
	x         int16
	y         int16
	direction byte
	motion    uint16
}

func (st *townPositionState) mark(x, y int16, direction byte, motion uint16) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.known = true
	st.x = x
	st.y = y
	st.direction = direction
	st.motion = motion
}

func (st *townPositionState) snapshot() (int16, int16, byte, uint16, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.x, st.y, st.direction, st.motion, st.known
}

// dungeonState is the session's view of the run it is inside. The packet
// observer writes it from the drain goroutine and the runtime reads it, so
// every field sits behind the state's own lock.
type dungeonState struct {
	mu         lockhub.RWLocker
	active     bool
	dungeonID  uint32
	difficulty byte
	// dead is true between a death notice and the matching revive notice.
	dead bool
	// loaded is true once the server has answered the loading acknowledgement.
	// A run is active from the op16 reply alone, but the character only stands
	// in the dungeon once the server has released the scene behind op37.
	loaded bool
	// since is when the current run became active. A party follower is pulled
	// into a run it never asked for, and how long it should stand in one is
	// measured from the run rather than from whenever its own timer fires.
	since time.Time
	// followed is true when the server pulled this session into a run it did
	// not open itself: the follower learns of the run from the op28/op29
	// projections alone, without ever seeing an op16 acknowledgement. That is
	// the exact split between "the server owns this walk" and "this session is
	// the walk's leader", and only a follower runs the room hunt duty.
	followed bool
	// room is the latest op29 projection of the room the character stands in.
	// The clearing client reads the room's actors and neighbours from it; the
	// maze itself is never announced, so the room projection is the only map
	// there is.
	room    protocol.DungeonStartMap
	hasRoom bool
	// roomSeq increments with every op29. A room move is answered with the new
	// room rather than with an acknowledgement, so a fresh sequence is the only
	// proof the server accepted it.
	roomSeq uint64
	// settlementSeq increments with every op31 settlement entry. It is the gate
	// op46 is validated against, so the play result waits for a sequence past
	// the one the run was observed with.
	settlementSeq uint64
	// settleQueue wakes the settlement worker. It is set once per session by
	// startDungeonSettlement and is nil for sessions that never start it, so a
	// signal is safe from any observer.
	settleQueue chan struct{}
	// roomQueue wakes the follower hunt worker on every room projection. It is
	// set once per session by startDungeonFollowDuty and is nil otherwise, so
	// the signal is safe from any observer.
	roomQueue chan struct{}
	// The post-settlement handshake state. The server walks the result page as
	// a client-driven chain - op46, op123, op69, op70 - and silently drops the
	// op42 exit until every step is on its side, so each step is recorded here:
	// playResultSent, statisticSent, clearRewardSeen (the op35 projection),
	// scoreScrollAcked (the class-1 op69 answer) and cardLayoutSeen (the class-1
	// op70 answer). The timestamps drive resends; a duplicate is replay-safe on
	// every one of these requests.
	playResultSent   bool
	statisticSent    bool
	clearRewardSeen  bool
	scoreScrollAcked bool
	cardLayoutSeen   bool
	playResultAt     time.Time
	scoreScrollAt    time.Time
	cardLayoutAt     time.Time
}

func (st *dungeonState) markActive(dungeonID uint32, difficulty byte) {
	st.beginRun(dungeonID, difficulty, false)
}

// markFollowed records a run this session was pulled into by the server. A
// follower never sends op16, so the op28/op29 projections are the whole
// evidence of the run, and the run's leader stays the character that opened it.
func (st *dungeonState) markFollowed() {
	st.beginRun(0, 0, true)
}

func (st *dungeonState) beginRun(dungeonID uint32, difficulty byte, followed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.active = true
	st.dungeonID = dungeonID
	st.difficulty = difficulty
	st.dead = false
	st.loaded = false
	st.followed = followed
	st.since = time.Now()
	st.room = protocol.DungeonStartMap{}
	st.hasRoom = false
	st.roomSeq = 0
	st.settlementSeq = 0
	st.resetSettlementHandshakeLocked()
}

// markLeft clears the run. The server sends the town transition (class0/op24)
// both when the death timer expires and when a run ends, so it is the one
// signal that always means the session is no longer inside a dungeon.
func (st *dungeonState) markLeft() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.active = false
	st.dungeonID = 0
	st.difficulty = 0
	st.dead = false
	st.loaded = false
	st.followed = false
	st.since = time.Time{}
	st.room = protocol.DungeonStartMap{}
	st.hasRoom = false
	st.roomSeq = 0
	st.settlementSeq = 0
	st.resetSettlementHandshakeLocked()
}

func (st *dungeonState) markDeath(revived bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.dead = !revived
}

func (st *dungeonState) markLoaded() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.loaded = true
}

func (st *dungeonState) isLoaded() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.loaded
}

func (st *dungeonState) snapshot() (active bool, dungeonID uint32, difficulty byte, dead bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.active, st.dungeonID, st.difficulty, st.dead
}

func (st *dungeonState) startedAt() time.Time {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.since
}

// markRoom records one op29 projection and bumps the room sequence. A repeated
// projection of the same room still bumps it: the sequence answers "did the
// server say anything new", not "did the coordinate change", and a move that
// lands on a layered room can be answered by the same coordinate.
func (st *dungeonState) markRoom(view protocol.DungeonStartMap) {
	st.mu.Lock()
	st.room = view
	st.hasRoom = true
	st.roomSeq++
	queue := st.roomQueue
	st.mu.Unlock()
	if queue != nil {
		select {
		case queue <- struct{}{}:
		default:
		}
	}
}

// followState reports whether a run is active and, when it is, whether the
// server put this session in it rather than this session opening it.
func (st *dungeonState) followState() (active bool, followed bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.active, st.followed
}

// roomSnapshot returns the latest room projection and its sequence.
func (st *dungeonState) roomSnapshot() (protocol.DungeonStartMap, uint64, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.room, st.roomSeq, st.hasRoom
}

// markSettlement records one op31 settlement entry.
func (st *dungeonState) markSettlement() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.settlementSeq++
}

// settlementSnapshot returns the number of settlement entries seen for this
// run.
func (st *dungeonState) settlementSnapshot() uint64 {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.settlementSeq
}

// settlementHandshake is the worker's view of the post-settlement chain. It is
// one snapshot rather than five getters so the worker never mixes a flag from
// before a run reset with a timestamp from after it.
type dungeonSettlementHandshake struct {
	playResultSent   bool
	statisticSent    bool
	clearRewardSeen  bool
	scoreScrollAcked bool
	cardLayoutSeen   bool
	playResultAt     time.Time
	scoreScrollAt    time.Time
	cardLayoutAt     time.Time
}

func (st *dungeonState) settlementHandshake() dungeonSettlementHandshake {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return dungeonSettlementHandshake{
		playResultSent:   st.playResultSent,
		statisticSent:    st.statisticSent,
		clearRewardSeen:  st.clearRewardSeen,
		scoreScrollAcked: st.scoreScrollAcked,
		cardLayoutSeen:   st.cardLayoutSeen,
		playResultAt:     st.playResultAt,
		scoreScrollAt:    st.scoreScrollAt,
		cardLayoutAt:     st.cardLayoutAt,
	}
}

func (st *dungeonState) resetSettlementHandshakeLocked() {
	st.playResultSent = false
	st.statisticSent = false
	st.clearRewardSeen = false
	st.scoreScrollAcked = false
	st.cardLayoutSeen = false
	st.playResultAt = time.Time{}
	st.scoreScrollAt = time.Time{}
	st.cardLayoutAt = time.Time{}
}

// claimPlayResult reserves the play result for a caller that is about to send
// it, without claiming it was sent. The settlement worker treats the timestamp
// as the last send, so it stays quiet for one resend interval and takes over
// only if the caller's own send never lands.
func (st *dungeonState) claimPlayResult(at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.playResultSent {
		return
	}
	st.playResultAt = at
}

func (st *dungeonState) markPlayResultSent(at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.playResultSent = true
	st.playResultAt = at
}

func (st *dungeonState) markStatisticSent() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.statisticSent = true
}

func (st *dungeonState) markClearRewardSeen() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.clearRewardSeen = true
}

func (st *dungeonState) markScoreScrollRequested(at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.scoreScrollAt = at
}

func (st *dungeonState) markScoreScrollAcked() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.scoreScrollAcked = true
}

func (st *dungeonState) markCardLayoutRequested(at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.cardLayoutAt = at
}

func (st *dungeonState) markCardLayoutSeen() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.cardLayoutSeen = true
}

// signalSettlement wakes the settlement worker without ever blocking the
// packet observer: the queue holds one token and extra signals collapse into
// it, because the worker re-reads the whole state on every pass.
func (st *dungeonState) signalSettlement() {
	queue := st.settleQueue
	if queue == nil {
		return
	}
	select {
	case queue <- struct{}{}:
	default:
	}
}

// InDungeon reports whether the server has an active run for this session.
func (s *Session) InDungeon() bool {
	if s == nil || s.dungeon == nil {
		return false
	}
	active, _, _, _ := s.dungeon.snapshot()
	return active
}

// DungeonSince reports when the active run started. It is a zero time when no
// run is active.
func (s *Session) DungeonSince() time.Time {
	if s == nil || s.dungeon == nil {
		return time.Time{}
	}
	return s.dungeon.startedAt()
}

// DungeonSettled reports whether the current run has already produced its
// settlement entry. A settled run is a cleared run: every member owes nothing
// to it any more except its own exit, and leaving it no longer abandons a
// party still fighting inside.
func (s *Session) DungeonSettled() bool {
	if s == nil || s.dungeon == nil {
		return false
	}
	return s.dungeon.settlementSnapshot() > 0
}

// DungeonDead reports whether the last death notice was a death rather than a
// revive.
func (s *Session) DungeonDead() bool {
	if s == nil || s.dungeon == nil {
		return false
	}
	_, _, _, dead := s.dungeon.snapshot()
	return dead
}

// DungeonIdentity returns the dungeon the session entered and the difficulty
// it asked for.
func (s *Session) DungeonIdentity() (uint32, byte, bool) {
	if s == nil || s.dungeon == nil {
		return 0, 0, false
	}
	active, dungeonID, difficulty, _ := s.dungeon.snapshot()
	return dungeonID, difficulty, active
}

// DungeonLoaded reports whether the dungeon scene finished loading. A run can
// be active without it: op16 commits the run, and only the loading
// acknowledgement makes the server release the scene the character stands in.
func (s *Session) DungeonLoaded() bool {
	if s == nil || s.dungeon == nil {
		return false
	}
	return s.dungeon.isLoaded()
}

// startDungeonTracking installs the observer that keeps the run and death state
// current. It only records; the revive worker is what writes to the socket.
func (s *Session) startDungeonTracking(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if s.dungeon == nil {
		s.dungeon = &dungeonState{}
	}
	cleanup := s.setPacketObserver(s.observeDungeon)
	go func() {
		defer cleanup()
		<-ctx.Done()
	}()
	// The settlement worker is part of tracking rather than of the revive
	// switch: leaving a completed run is required for every session that can
	// be pulled into one, follower included, and it never writes without the
	// op31 entry that opens a completed run.
	s.startDungeonSettlement(ctx)
	// The follower hunt duty is part of tracking for the same reason: a
	// follower is moved through the leader's rooms by the server and has no
	// other turn that would clear them, and the duty never writes unless the
	// server itself put this session in someone else's run.
	s.startDungeonFollowDuty(ctx)
}

// startDungeonRevive runs the worker that answers a death notice with an op41
// revive. The observer only queues the notice, so a slow socket cannot stall
// the drain loop, exactly like the party auto-accept worker.
func (s *Session) startDungeonRevive(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if s.dungeon == nil {
		s.dungeon = &dungeonState{}
	}
	s.reviveQueue = make(chan struct{}, 1)
	queue := s.reviveQueue
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case <-queue:
				s.reviveAfterDeath(ctx)
			}
		}
	}()
}

// reviveAfterDeath answers one death notice. It is deliberately quiet about the
// common failure: when the ten second timer has already expired the server
// drops the revive, and the town return that follows is the expected outcome
// rather than a fault.
func (s *Session) reviveAfterDeath(ctx context.Context) {
	reviveCtx, cancel := context.WithTimeout(ctx, dungeonDeathAckTimeout)
	defer cancel()
	if err := s.Revive(reviveCtx); err != nil {
		foundationlog.Robotf("CN90_DUNGEON_REVIVE_FAILED cid=%d err=%v\n", s.selfCharacterID, err)
		return
	}
	foundationlog.Robotf("CN90_DUNGEON_REVIVED cid=%d\n", s.selfCharacterID)
}

func (s *Session) observeDungeon(packet protocol.Packet) {
	state := s.dungeon
	if state == nil {
		return
	}
	switch {
	case packet.Class == protocol.ClassCommand && packet.Type == protocol.CmdSelectDungeon:
		// The entry acknowledgement arrives before EnterDungeon returns; keeping
		// it here as well means a session opened by someone else still learns it
		// is inside a run.
		if protocol.DungeonSelectAck(packet.Type, packet.Body) == nil {
			state.markActive(0, 0)
		}
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiDungeonDeathState:
		notice, err := protocol.ParseDungeonDeathState(packet.Type, packet.Body)
		if err != nil {
			return
		}
		if notice.ObjectKey != s.selfCharacterID {
			return
		}
		state.markDeath(notice.Revived)
		if notice.Revived {
			foundationlog.Robotf("CN90_DUNGEON_ALIVE cid=%d\n", s.selfCharacterID)
			return
		}
		foundationlog.Robotf("CN90_DUNGEON_DIED cid=%d\n", s.selfCharacterID)
		if s.reviveQueue != nil {
			select {
			case s.reviveQueue <- struct{}{}:
			default:
			}
		}
	case packet.Class == protocol.ClassNotice &&
		(packet.Type == protocol.NotiDungeonInfo || packet.Type == protocol.NotiDungeonStartMap):
		// The entry projections. A session that ran the selector itself has
		// already marked the run active from the op16 reply; these are what tell
		// a session the server moved on its own, which is how a party follower
		// learns it is standing inside the leader's run. Without them a follower
		// never finds out: op42 is dropped while no run is active, so a follower
		// that does not know it is in one stays in the dungeon until its session
		// ends.
		if active, _, _, _ := state.snapshot(); !active {
			// This session never sent op16 for this run, so the server put it
			// here on someone else's walk. The flag is what separates a follower
			// from the run's leader for the room hunt duty below.
			state.markFollowed()
			foundationlog.Robotf("CN90_DUNGEON_FOLLOWED cid=%d op=%d\n", s.selfCharacterID, packet.Type)
		}
		if packet.Type == protocol.NotiDungeonStartMap {
			// The room projection is what the clearing client walks and kills
			// with; parsing it here keeps the packet observer the only writer.
			if view, err := protocol.ParseDungeonStartMap(packet.Body); err == nil {
				state.markRoom(view)
			}
		}
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiDungeonSettlementEntry:
		// The settlement window opening is the gate the op46 play result is
		// validated against, so it is recorded per run rather than acted on.
		if active, _, _, _ := state.snapshot(); active {
			state.markSettlement()
			// The settlement worker walks the rest of the chain from here: op46
			// then op123 then op69 then op70, whichever of them this session
			// still owes. Followers owe the same chain as the leader.
			state.signalSettlement()
		}
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiDungeonClearReward:
		// The clear-reward projection is the server's own proof that the op46
		// play result was accepted rather than dropped; the card requests are
		// only answered after it.
		if active, _, _, _ := state.snapshot(); active && state.settlementSnapshot() > 0 {
			state.markClearRewardSeen()
			state.signalSettlement()
		}
	case packet.Class == protocol.ClassCommand && packet.Type == protocol.CmdScoreScrollState:
		// The class-1 op69 answer is what unlocks the op70 card-layout request.
		if active, _, _, _ := state.snapshot(); active && state.settlementSnapshot() > 0 {
			state.markScoreScrollAcked()
			state.signalSettlement()
		}
	case packet.Class == protocol.ClassCommand && packet.Type == protocol.CmdCardSelectRightState:
		// The class-1 op70 layout is the last client-side gate before op42
		// accepts a completed run's exit.
		if active, _, _, _ := state.snapshot(); active && state.settlementSnapshot() > 0 {
			state.markCardLayoutSeen()
			state.signalSettlement()
		}
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiFinishLoading:
		// The character-state push that answers the loading acknowledgement.
		// It only means "the scene is up" inside a run, so it is ignored when
		// no run is active rather than taken as proof of one.
		if active, _, _, _ := state.snapshot(); active {
			state.markLoaded()
		}
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiSceneTransition:
		state.markLeft()
	}
}

// EnterDungeon walks the selector flow that starts a run: op15 opens the
// selector and binds the character's current town position as the return
// origin, then op16 commits the entry.
//
// The order matters. The server binds the origin while answering op15 and never
// retries, and an op16 whose origin was never bound is dropped silently. The
// origin is not the op15 request itself but the last op35 position report the
// server holds for the current town scene, so this re-reports the session's
// last known town coordinate first: a session that has just logged in has
// never sent one, and the scene clears it on every town change.
func (s *Session) EnterDungeon(ctx context.Context, dungeonID uint32, difficulty byte) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN dungeon entry needs an open session")
	}
	if dungeonID == 0 {
		return fmt.Errorf("90CN dungeon entry needs a dungeon id")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*dungeonSelectAckTimeout+dungeonLoadingTimeout)
		defer cancel()
	}
	if err := s.reportTownPosition(ctx); err != nil {
		return err
	}
	if err := s.awaitDungeonAck(ctx, protocol.CmdEnterSelectDungeon, func() error {
		return s.client.EnterDungeonSelect(ctx)
	}); err != nil {
		return fmt.Errorf("90CN dungeon selector: %w", err)
	}
	if err := s.awaitDungeonAck(ctx, protocol.CmdSelectDungeon, func() error {
		return s.client.SelectDungeon(ctx, dungeonID, difficulty)
	}); err != nil {
		return fmt.Errorf("90CN dungeon entry: %w", err)
	}
	if s.dungeon != nil {
		s.dungeon.markActive(dungeonID, difficulty)
	}
	foundationlog.Robotf("CN90_DUNGEON_ENTERED cid=%d dungeon=%d difficulty=%d\n",
		s.selfCharacterID, dungeonID, difficulty)
	// The run is live from the op16 reply, but the scene is not. The server
	// holds the character-state push, the skill frame and the room placement
	// until the loading acknowledgement arrives, so an entry that stops here
	// leaves the character inside a run it never finishes loading into.
	if err := s.finishDungeonLoading(ctx); err != nil {
		return err
	}
	foundationlog.Robotf("CN90_DUNGEON_LOADED cid=%d dungeon=%d\n", s.selfCharacterID, dungeonID)
	return nil
}

// finishDungeonLoading sends the op37 acknowledgement and waits for the
// server's own class0/op37 character-state push, which is the only signal that
// the dungeon scene is up.
//
// A timeout does not undo the entry: op16 already committed the run. It means
// the character is inside a run whose scene never finished loading, so the
// caller is told rather than left to assume the entry completed.
func (s *Session) finishDungeonLoading(ctx context.Context) error {
	loaded := make(chan struct{}, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiFinishLoading {
			return
		}
		select {
		case loaded <- struct{}{}:
		default:
		}
	})
	defer cleanup()
	if err := s.client.FinishLoading(ctx); err != nil {
		return err
	}
	select {
	case <-loaded:
		if s.dungeon != nil {
			s.dungeon.markLoaded()
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("op%d dungeon loading: %w", protocol.CmdFinishLoading, ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended while the dungeon was loading")
	}
}

// LeaveDungeon abandons the run the session is inside and waits for the town
// transition that confirms the character is back.
//
// The server answers op42 with nothing at all -- the class0/op24 scene
// transition it rebuilds from the entry's frozen origin is the whole reply --
// and it drops the request without a reply while no run is active. That is why
// this refuses to send when no run is active rather than letting it time out:
// a timeout is the shape of a dropped request, so sending one that is known to
// be dropped would report a fault that never happened.
func (s *Session) LeaveDungeon(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN dungeon exit needs an open session")
	}
	if !s.InDungeon() {
		return fmt.Errorf("90CN dungeon exit needs an active run")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dungeonSelectAckTimeout)
		defer cancel()
	}
	town := make(chan struct{}, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiSceneTransition {
			return
		}
		select {
		case town <- struct{}{}:
		default:
		}
	})
	defer cleanup()
	if err := s.client.GiveUpDungeon(ctx); err != nil {
		return err
	}
	select {
	case <-town:
		if s.dungeon != nil {
			s.dungeon.markLeft()
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("op%d dungeon exit: %w", protocol.CmdGiveupGame, ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended while leaving the dungeon")
	}
}

// reportTownPosition re-sends the last known town coordinate with op35 so the
// server has a current-scene report to freeze as the dungeon return origin.
//
// The server answers op35 with nothing, so there is no acknowledgement to wait
// for; the ordering inside the connection is what carries the guarantee. When
// the session has never learnt a coordinate there is nothing honest to report,
// and the entry is left to fail the way the server reports it.
func (s *Session) reportTownPosition(ctx context.Context) error {
	state := s.townPosition
	if state == nil {
		return nil
	}
	x, y, direction, motion, known := state.snapshot()
	if !known {
		return nil
	}
	if err := s.client.SetUserPosition(ctx, x, y, direction, motion); err != nil {
		return fmt.Errorf("90CN dungeon entry position report: %w", err)
	}
	return nil
}

// awaitDungeonAck sends one dungeon command and waits for the command-class
// acknowledgement carrying the same opcode. A rejected request is dropped
// without a reply, so the timeout is the only signal for a silent rejection.
func (s *Session) awaitDungeonAck(ctx context.Context, opcode uint16, send func() error) error {
	result := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != opcode {
			return
		}
		select {
		case result <- protocol.DungeonSelectAck(opcode, packet.Body):
		default:
		}
	})
	defer cleanup()
	if err := send(); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("op%d confirmation: %w", opcode, ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during op%d", opcode)
	}
}

// DeclareDeath reports the character as dead and waits for the server's own
// death notice. Death is client-declared in this protocol generation, so this
// is a statement rather than a report of something the server observed.
func (s *Session) DeclareDeath(ctx context.Context, x, y int16) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN character death needs an open session")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dungeonDeathAckTimeout)
		defer cancel()
	}
	notice, cleanup, err := s.awaitDungeonDeath(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := s.client.DeclareDeath(ctx, x, y); err != nil {
		return err
	}
	return notice(false)
}

// Revive answers the server's death notice with the op41 revive request. The
// server only accepts it while the death timer is armed, which lasts ten
// seconds, and it requires the requested object key to be the acting
// character's own.
func (s *Session) Revive(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN revive needs an open session")
	}
	key := s.selfCharacterID
	if key == 0 {
		return fmt.Errorf("90CN revive needs a selected character")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dungeonDeathAckTimeout)
		defer cancel()
	}
	revived, cleanup, err := s.awaitDungeonDeath(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	ack := make(chan error, 1)
	ackCleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdUseCoin {
			return
		}
		select {
		case ack <- protocol.UseCoinAck(packet.Type, packet.Body):
		default:
		}
	})
	defer ackCleanup()
	if err := s.client.UseCoin(ctx, key); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return fmt.Errorf("90CN revive acknowledgement: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during the revive")
	}
	return revived(true)
}

// awaitDungeonDeath installs the death-notice observer and returns a waiter for
// the notice carrying the wanted state. The caller owns cleanup.
func (s *Session) awaitDungeonDeath(ctx context.Context) (func(revived bool) error, func(), error) {
	notices := make(chan protocol.DungeonDeathState, 4)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiDungeonDeathState {
			return
		}
		notice, err := protocol.ParseDungeonDeathState(packet.Type, packet.Body)
		if err != nil || notice.ObjectKey != s.selfCharacterID {
			return
		}
		select {
		case notices <- notice:
		default:
		}
	})
	wait := func(revived bool) error {
		for {
			select {
			case notice := <-notices:
				if notice.Revived == revived {
					return nil
				}
			case <-ctx.Done():
				// In town the server drops op40 and op41 without any reply at all,
				// so this is the expected failure when no run is active.
				return fmt.Errorf("90CN dungeon state confirmation: %w", ctx.Err())
			case <-s.Done():
				return fmt.Errorf("90CN session ended while waiting for a dungeon state notice")
			}
		}
	}
	return wait, cleanup, nil
}
