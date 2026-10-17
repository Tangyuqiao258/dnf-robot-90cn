package cn90

import (
	"context"
	"time"

	foundationlog "robot/internal/foundation/log"
)

// The post-settlement handshake.
//
// A completed run is not left the way an active one is. The server accepts the
// op42 exit only after the whole result-page chain has crossed the wire from
// this character, and every step it is missing is dropped silently -- so a
// client that stops at op46 keeps receiving settlement packets while op42
// times out, which is exactly the shape of a hang.
//
// The chain, as the server's own handlers require it:
//
//	op31  settlement entry            (server to client, opens the window)
//	op46  play result                 (client says the run is over)
//	op35  clear reward                (server proof op46 was accepted)
//	op123 character statistic         (six u32; the barrier after the result)
//	op69  score-scroll state request  (server answers class-1; opens op70)
//	op70  card-layout request         (server answers class-1 layout)
//	op42  exit                        (server answers with the town op24)
//
// The card row itself is not the robot's to choose: on op42 the server
// auto-flips the free row and commits the reward before releasing the exit
// when the layout was sent but no selection was. What it will not do is send
// the layout on its own, so op69 and op70 stay client-driven here.
//
// Every member of the party owes this chain on its own session -- the server's
// settlement flags are per character, and the follower's op42 is dropped just
// as silently while its own op46/op123 are missing. Only the leader walks a
// dungeon; this worker is how the rest of the party gets out of one.

const (
	// dungeonSettlementTickInterval paces the retries while a step is waiting
	// for its answer. Every request in the chain is replay-safe, so a resend
	// costs one packet and nothing else.
	dungeonSettlementTickInterval = 2 * time.Second
	// dungeonSettlementResendInterval is how long a request may stay
	// unanswered before the worker sends it again.
	dungeonSettlementResendInterval = 3 * time.Second
	// dungeonSettlementSendTimeout bounds one write. The socket write is the
	// whole operation; the answers arrive on the observer.
	dungeonSettlementSendTimeout = 5 * time.Second
	// dungeonSettlementTimeout bounds the whole chain. The settlement page is
	// local work on the server, so a minute is already generous; the ceiling
	// only stops a worker from retrying a chain the server will never finish.
	dungeonSettlementTimeout = 2 * time.Minute
)

// startDungeonSettlement installs the queue and the worker that answers a
// settlement entry with the rest of the result-page chain. It is installed for
// every tracked session: a follower owes the same chain as the leader, and a
// session that never sees op31 never pays for it.
func (s *Session) startDungeonSettlement(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if s.dungeon == nil {
		s.dungeon = &dungeonState{}
	}
	queue := make(chan struct{}, 1)
	s.dungeon.mu.Lock()
	s.dungeon.settleQueue = queue
	s.dungeon.mu.Unlock()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case <-queue:
				s.settleDungeonAfterEntry(ctx)
			}
		}
	}()
}

// settleDungeonAfterEntry drives one settlement chain to its end. It returns
// when the chain is done, the run is left, the session ends, or the ceiling is
// reached; every exit leaves the state in place for a later run.
func (s *Session) settleDungeonAfterEntry(ctx context.Context) {
	deadline := time.NewTimer(dungeonSettlementTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(dungeonSettlementTickInterval)
	defer ticker.Stop()
	for {
		if s.advanceDungeonSettlement(ctx) {
			foundationlog.Robotf("CN90_DUNGEON_SETTLEMENT_READY cid=%d\n", s.selfCharacterID)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-deadline.C:
			foundationlog.Robotf("CN90_DUNGEON_SETTLEMENT_TIMEOUT cid=%d\n", s.selfCharacterID)
			return
		case <-ticker.C:
		}
		if !s.InDungeon() {
			return
		}
	}
}

// advanceDungeonSettlement sends the next request the chain still owes. It
// reports true once the card layout has been seen, which is the last gate the
// op42 exit waits for.
func (s *Session) advanceDungeonSettlement(ctx context.Context) bool {
	state := s.dungeon
	if state == nil {
		return true
	}
	step := state.settlementHandshake()
	if step.cardLayoutSeen {
		return true
	}
	now := time.Now()
	if !step.clearRewardSeen {
		if due(step.playResultAt, now) {
			s.sendSettlementRequest(ctx, func(sendCtx context.Context) error {
				return s.client.SetPlayResult(sendCtx)
			})
			state.markPlayResultSent(now)
		}
		return false
	}
	if !step.statisticSent {
		s.sendSettlementRequest(ctx, func(sendCtx context.Context) error {
			return s.client.SetCharacterStatistic(sendCtx)
		})
		state.markStatisticSent()
		return false
	}
	if !step.scoreScrollAcked && due(step.scoreScrollAt, now) {
		s.sendSettlementRequest(ctx, func(sendCtx context.Context) error {
			return s.client.RequestScoreScrollState(sendCtx)
		})
		state.markScoreScrollRequested(now)
		return false
	}
	if step.scoreScrollAcked && due(step.cardLayoutAt, now) {
		s.sendSettlementRequest(ctx, func(sendCtx context.Context) error {
			return s.client.RequestCardSelectRightState(sendCtx)
		})
		state.markCardLayoutRequested(now)
	}
	return false
}

// sendSettlementRequest performs one bounded write. A failed write is logged
// and left to the next tick: the chain is entirely retryable, and a session
// that is closing fails every write for the same reason.
func (s *Session) sendSettlementRequest(ctx context.Context, send func(context.Context) error) {
	sendCtx, cancel := context.WithTimeout(ctx, dungeonSettlementSendTimeout)
	defer cancel()
	if err := send(sendCtx); err != nil {
		foundationlog.Robotf("CN90_DUNGEON_SETTLEMENT_SEND_FAILED cid=%d err=%v\n", s.selfCharacterID, err)
	}
}

// due reports whether a request that was last sent at last is ready to be
// sent again. A zero last means it has never been sent.
func due(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= dungeonSettlementResendInterval
}
