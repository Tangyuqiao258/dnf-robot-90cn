package cn90

import (
	"context"
	"fmt"
	"time"

	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// Player-to-player item trade.
//
// The window lives entirely on the server (dnfbridge/item_trade_online.go). It
// opens when the target answers a mode-1 op10 request with op11, and from there
// three wire shapes drive it: op19 moves items and gold in and out, op24
// carries the two boundaries, and class0/op15, /op17, /op16 and /op18 report
// what the other side did. Nothing is client-authoritative: the server holds
// every offer, assigns the visible rows itself and writes the exchange
// atomically when both sides consent, so a session that simply follows the
// notices cannot invent an item.
//
// The one asymmetry worth knowing: the final consent is never broadcast. The
// server holds the first consent privately so the second participant still gets
// its own dialog, which is why a session only learns the peer is ready by
// sending its own consent and watching for the completion.

const (
	// tradeAckTimeout bounds the wait for the class1 acknowledgement of one trade
	// command. The server answers every command it dispatches, so silence means
	// the request never reached the handler.
	tradeAckTimeout = 5 * time.Second
	// tradeEventQueue is how many window events can wait for the worker at once.
	tradeEventQueue = 4
)

// tradeEventKind is one step the worker has to perform.
type tradeEventKind int

const (
	// tradeEventPrompt answers a trade prompt that arrived from another player.
	tradeEventPrompt tradeEventKind = iota
	// tradeEventOffer puts this session's stack into a window that just opened.
	tradeEventOffer
	// tradeEventReady declares the shared "adding done" boundary reached and
	// sends this session's final consent.
	tradeEventReady
)

// tradeState is the session's view of the trade window. The packet observer
// writes it from the drain goroutine while the worker reads and updates it, so
// every field sits behind the state's own lock.
type tradeState struct {
	mu lockhub.RWLocker
	// prompt is the character whose trade prompt has not been answered yet.
	prompt uint16
	// open is true between the moment the window opened and the completion or
	// rollback that ends it.
	open bool
	peer uint16
	// ownState and peerState are the last boundary each side was reported at.
	ownState  byte
	peerState byte
	// offered is true once this window's automatic offer started, so a second
	// activation of the same window cannot give a second stack away.
	offered bool
	// consented is true once the consent step was queued, so repeated state rows
	// do not queue it again.
	consented bool
	// ownRows are the trade rows the server assigned to this session's offers.
	ownRows []uint16
	// peerRows is the last item seen on each of the peer's rows.
	peerRows map[uint16]uint32
}

func (st *tradeState) rememberPrompt(source uint16) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prompt = source
}

// takePrompt consumes the pending prompt. Only one prompt is ever pending: the
// server replaces a participant's trade when a new one begins, so answering the
// newest is what the window actually is.
func (st *tradeState) takePrompt() (uint16, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	source := st.prompt
	st.prompt = 0
	return source, source != 0
}

// openWindow records that the trade with peer is live and reports whether this
// call is what opened it. Both the acceptor path (after its own op11
// acknowledgement) and the initiator path (on the class0/0x0008 notice) call
// it, and the second caller must not restart the window's work.
func (st *tradeState) openWindow(peer uint16) bool {
	if peer == 0 {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.open && st.peer == peer {
		return false
	}
	st.open, st.peer = true, peer
	st.ownState, st.peerState = 0, 0
	st.offered, st.consented = false, false
	st.ownRows = nil
	st.peerRows = make(map[uint16]uint32)
	return true
}

func (st *tradeState) closeWindow() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.open, st.peer = false, 0
	st.ownState, st.peerState = 0, 0
	st.offered, st.consented = false, false
	st.prompt = 0
	st.ownRows = nil
	st.peerRows = nil
}

func (st *tradeState) peerCharacter() (uint16, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if !st.open || st.peer == 0 {
		return 0, false
	}
	return st.peer, true
}

// beginOffer claims the automatic offer slot for this window, so the window
// yields exactly one stack no matter how many times the offer step is reached.
func (st *tradeState) beginOffer() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.open || st.offered {
		return false
	}
	st.offered = true
	return true
}

func (st *tradeState) noteOwnOffer(tradeSlot uint16) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.ownRows = append(st.ownRows, tradeSlot)
}

func (st *tradeState) notePeerOffer(tradeSlot uint16, itemID uint32, removed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.peerRows == nil {
		st.peerRows = make(map[uint16]uint32)
	}
	if removed {
		delete(st.peerRows, tradeSlot)
		return
	}
	st.peerRows[tradeSlot] = itemID
}

// applyStateRow records one class0/op17 row and reports whether the window has
// reached the point where this session may send its final consent. Consent only
// becomes legal once both sides sit at or past the shared "adding done"
// boundary, and it is claimed here so a burst of rows queues the step once.
func (st *tradeState) applyStateRow(characterID uint16, value byte, self uint16) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.open {
		return false
	}
	switch characterID {
	case self:
		st.ownState = value
	case st.peer:
		st.peerState = value
	default:
		return false
	}
	if st.consented {
		return false
	}
	if st.ownState < protocol.TradeStateAddingDone || st.peerState < protocol.TradeStateAddingDone {
		return false
	}
	st.consented = true
	return true
}

// readyForConsent reports whether the boundaries already let this session
// consent. The rows that announce a session's own boundary can arrive before
// the acknowledgement that follows them, so the offer step asks directly
// instead of waiting for a row that will not be sent twice.
func (st *tradeState) readyForConsent() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.open || st.consented {
		return false
	}
	if st.ownState < protocol.TradeStateAddingDone || st.peerState < protocol.TradeStateAddingDone {
		return false
	}
	st.consented = true
	return true
}

// releaseConsent returns the claim after a refused consent, so the next state
// row can try again.
func (st *tradeState) releaseConsent() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.consented = false
}

// startTrade runs the receive side for the life of the session. The observer
// only records and queues; the worker owns the socket writes, so a slow offer
// cannot stall the drain loop.
func (s *Session) startTrade(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if s.trade == nil {
		s.trade = &tradeState{}
	}
	cleanup := s.setPacketObserver(s.observeTrade)
	go func() {
		defer cleanup()
		<-ctx.Done()
	}()
	if !s.tradeEnabled {
		return
	}
	s.tradeQueue = make(chan tradeEventKind, tradeEventQueue)
	queue := s.tradeQueue
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case event := <-queue:
				switch event {
				case tradeEventPrompt:
					s.answerTradePrompt(ctx)
				case tradeEventOffer:
					s.offerTradeStack(ctx)
				case tradeEventReady:
					s.consentToTrade(ctx)
				}
			}
		}
	}()
}

// queueTradeEvent hands one step to the worker. It never blocks: a window that
// outruns its own worker is dropped rather than allowed to stall packet
// dispatch.
func (s *Session) queueTradeEvent(event tradeEventKind) {
	if s.tradeQueue == nil {
		return
	}
	select {
	case s.tradeQueue <- event:
	default:
		foundationlog.Robotf("CN90_TRADE_STEP_DROPPED cid=%d step=%d\n", s.selfCharacterID, int(event))
	}
}

// observeTrade classifies the trade family and queues the worker steps. Nothing
// here writes to the socket.
func (s *Session) observeTrade(packet protocol.Packet) {
	state := s.trade
	if state == nil || packet.Class != protocol.ClassNotice {
		return
	}
	switch packet.Type {
	case protocol.NotiRequestPeer:
		notice, ok := protocol.ParseRequestPeerNotice(packet.Body)
		if !ok || notice.Mode != protocol.TradeRequestMode || !s.usablePeer(notice.SourceID) {
			return
		}
		state.rememberPrompt(notice.SourceID)
		foundationlog.Robotf("CN90_TRADE_PROMPT cid=%d peer=%d\n", s.selfCharacterID, notice.SourceID)
		s.queueTradeEvent(tradeEventPrompt)
	case protocol.NotiPeerResponse:
		notice, ok := protocol.ParsePeerResponseNotice(packet.Body)
		if !ok || notice.Mode != protocol.TradeRequestMode || !s.usablePeer(notice.SourceID) {
			return
		}
		if state.openWindow(notice.SourceID) {
			foundationlog.Robotf("CN90_TRADE_OPEN cid=%d role=initiator peer=%d\n", s.selfCharacterID, notice.SourceID)
			s.queueTradeEvent(tradeEventOffer)
		}
	case protocol.NotiTradeState:
		notice, ok := protocol.ParseTradeStateNotice(packet.Body)
		if !ok {
			return
		}
		if state.applyStateRow(notice.CharacterID, notice.State, s.selfCharacterID) {
			s.queueTradeEvent(tradeEventReady)
		}
	case protocol.NotiTradeChange:
		notice, ok := protocol.ParseTradeChangeNotice(packet.Body)
		if !ok {
			return
		}
		if _, open := state.peerCharacter(); !open {
			return
		}
		state.notePeerOffer(notice.TradeSlot, notice.ItemID, notice.Removed)
		foundationlog.Robotf("CN90_TRADE_PEER_OFFER cid=%d slot=%d item=%d removed=%t\n",
			s.selfCharacterID, notice.TradeSlot, notice.ItemID, notice.Removed)
	case protocol.NotiTradeFinish:
		notice, ok := protocol.ParseTradeFinishNotice(packet.Body)
		if !ok {
			return
		}
		peer, open := state.peerCharacter()
		state.closeWindow()
		if !open {
			return
		}
		foundationlog.Robotf("CN90_TRADE_FINISHED cid=%d peer=%d received=%d\n",
			s.selfCharacterID, peer, len(notice.Transfers))
	case protocol.NotiTradeCancel:
		notice, ok := protocol.ParseTradeCancelNotice(packet.Body)
		if !ok {
			return
		}
		peer, open := state.peerCharacter()
		state.closeWindow()
		if !open {
			return
		}
		foundationlog.Robotf("CN90_TRADE_CANCELLED cid=%d peer=%d returned=%d\n",
			s.selfCharacterID, peer, len(notice.Entries))
	}
}

// usablePeer rejects a notice that names this session's own character or no
// character at all: neither can be traded with.
func (s *Session) usablePeer(characterID uint16) bool {
	return characterID != 0 && characterID != s.selfCharacterID
}

// TradeOpen reports the peer of the trade this session currently has open.
func (s *Session) TradeOpen() (uint16, bool) {
	if s == nil || s.trade == nil {
		return 0, false
	}
	return s.trade.peerCharacter()
}

// answerTradePrompt accepts one prompt and then runs the window's offer step.
func (s *Session) answerTradePrompt(ctx context.Context) {
	prompt, ok := s.trade.takePrompt()
	if !ok {
		return
	}
	if err := s.AcceptTrade(ctx, prompt); err != nil {
		foundationlog.Robotf("CN90_TRADE_ACCEPT_FAILED cid=%d peer=%d err=%v\n", s.selfCharacterID, prompt, err)
		return
	}
	if !s.trade.openWindow(prompt) {
		return
	}
	foundationlog.Robotf("CN90_TRADE_OPEN cid=%d role=acceptor peer=%d\n", s.selfCharacterID, prompt)
	s.offerTradeStack(ctx)
}

// tradeOfferBagRows expands the configured stock count into the bag rows the
// character's offline seeding filled. The seed writes its stacks upward from
// cn90AuctionStockBaseSlot, so the rows are contiguous and the adapter is the
// only side that has to know where they start.
func tradeOfferBagRows(stacks int) []int {
	if stacks <= 0 {
		return nil
	}
	rows := make([]int, 0, stacks)
	for index := 0; index < stacks; index++ {
		rows = append(rows, cn90AuctionStockBaseSlot+index)
	}
	return rows
}

// offerTradeStack moves one stack out of the bag into the window and then
// declares this side finished adding.
//
// A robot has no live inventory read, so the bag rows are named rather than
// discovered: they come from the offline stock the character was seeded with,
// and the first row that still holds an item is the offer. A row that was
// already sold is refused by the server, which is why the rows are tried in
// order rather than trusted.
func (s *Session) offerTradeStack(ctx context.Context) {
	state := s.trade
	peer, open := state.peerCharacter()
	if !open || !state.beginOffer() {
		return
	}
	rows := tradeOfferBagRows(s.tradeOfferStacks)
	if len(rows) == 0 {
		foundationlog.Robotf("CN90_TRADE_OFFER_SKIPPED cid=%d peer=%d reason=no_stock_rows_configured\n", s.selfCharacterID, peer)
		return
	}
	count := s.tradeOfferCount
	if count <= 0 {
		count = 1
	}
	ack, err := s.offerFirstStockedRow(ctx, rows, count)
	if err != nil {
		foundationlog.Robotf("CN90_TRADE_OFFER_FAILED cid=%d peer=%d rows=%d err=%v\n", s.selfCharacterID, peer, len(rows), err)
		return
	}
	state.noteOwnOffer(uint16(ack.DestinationSlot))
	foundationlog.Robotf("CN90_TRADE_OFFERED cid=%d peer=%d bag_slot=%d trade_slot=%d count=%d\n",
		s.selfCharacterID, peer, ack.SourceSlot, ack.DestinationSlot, ack.Count)
	if err := s.SetTradeState(ctx, protocol.TradeStateAddRequest); err != nil {
		foundationlog.Robotf("CN90_TRADE_ADDING_FAILED cid=%d peer=%d err=%v\n", s.selfCharacterID, peer, err)
		return
	}
	foundationlog.Robotf("CN90_TRADE_ADDING_DONE cid=%d peer=%d\n", s.selfCharacterID, peer)
	if state.readyForConsent() {
		s.queueTradeEvent(tradeEventReady)
	}
}

// offerFirstStockedRow tries the named bag rows in order and returns the first
// offer the server accepted.
func (s *Session) offerFirstStockedRow(ctx context.Context, rows []int, count int) (protocol.TradeMoveAck, error) {
	var lastErr error
	for _, slot := range rows {
		ack, err := s.OfferTradeItem(ctx, slot, count)
		if err == nil {
			return ack, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no bag row was configured")
	}
	return protocol.TradeMoveAck{}, lastErr
}

// consentToTrade sends this session's final consent. The server refuses it
// while the peer has not reached the shared boundary, so a refusal releases the
// claim and lets the next state row try again.
func (s *Session) consentToTrade(ctx context.Context) {
	peer, open := s.trade.peerCharacter()
	if !open {
		return
	}
	if err := s.SetTradeState(ctx, protocol.TradeStateReady); err != nil {
		foundationlog.Robotf("CN90_TRADE_CONSENT_FAILED cid=%d peer=%d err=%v\n", s.selfCharacterID, peer, err)
		s.trade.releaseConsent()
		return
	}
	foundationlog.Robotf("CN90_TRADE_CONSENT_SENT cid=%d peer=%d\n", s.selfCharacterID, peer)
}

// tradeContext applies the trade acknowledgement budget when the caller has not
// chosen one of its own.
func tradeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, tradeAckTimeout)
}

// BeginTrade asks another character to open a trade window and waits until it
// does.
//
// The op10 request carries no acknowledgement: the server records a pending
// invitation and forwards the prompt, so an unaccepted request is silent and
// the only signal a session can see is the class0/0x0008 notice the server
// sends once the target answers. That notice is also what opens the window, so
// a caller that drives the window by hand can wait for TradeOpen instead.
func (s *Session) BeginTrade(ctx context.Context, targetCharacterID uint16) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN trade needs an open session")
	}
	if !s.usablePeer(targetCharacterID) {
		return fmt.Errorf("90CN trade needs a target character other than this session's own")
	}
	ctx, cancel := tradeContext(ctx)
	defer cancel()
	accepted := make(chan struct{}, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiPeerResponse {
			return
		}
		notice, ok := protocol.ParsePeerResponseNotice(packet.Body)
		if !ok || notice.Mode != protocol.TradeRequestMode || notice.SourceID != targetCharacterID {
			return
		}
		select {
		case accepted <- struct{}{}:
		default:
		}
	})
	defer cleanup()
	if err := s.client.RequestPeer(ctx, targetCharacterID, protocol.TradeRequestMode, 0, 0); err != nil {
		return err
	}
	select {
	case <-accepted:
	case <-ctx.Done():
		return fmt.Errorf("90CN trade request acknowledgement: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during a trade request")
	}
	foundationlog.Robotf("CN90_TRADE_REQUESTED cid=%d peer=%d\n", s.selfCharacterID, targetCharacterID)
	return nil
}

// AcceptTrade answers a trade prompt with the seven-byte op11 accept shape.
func (s *Session) AcceptTrade(ctx context.Context, targetCharacterID uint16) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN trade needs an open session")
	}
	if !s.usablePeer(targetCharacterID) {
		return fmt.Errorf("90CN trade needs a target character other than this session's own")
	}
	ctx, cancel := tradeContext(ctx)
	defer cancel()
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdResponsePeer {
			return
		}
		// The trade acknowledgement is the five-byte shape. A party answer is
		// four bytes and belongs to the party worker, so it is ignored here even
		// when both prompts are being answered at once.
		if _, ok := protocol.ParseTradeResponseAck(packet.Body); !ok {
			return
		}
		select {
		case ack <- nil:
		default:
		}
	})
	defer cleanup()
	if err := s.client.ResponsePeer(ctx, targetCharacterID, protocol.TradeRequestMode, 0); err != nil {
		return err
	}
	select {
	case err := <-ack:
		return err
	case <-ctx.Done():
		return fmt.Errorf("90CN trade accept acknowledgement: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during a trade accept")
	}
}

// OfferTradeItem moves one bag stack into the trade window and returns the
// acknowledgement, whose destination row is the row the server assigned. The
// caller must keep that row: it is the only handle a later withdrawal has.
func (s *Session) OfferTradeItem(ctx context.Context, bagSlot, count int) (protocol.TradeMoveAck, error) {
	return s.moveTradeItem(ctx, protocol.TradeMoveRequest{
		SourceList:      protocol.TradeBagListType,
		SourceSlot:      int16(bagSlot),
		Count:           int32(count),
		DestinationList: protocol.TradeListType,
	})
}

// WithdrawTradeOffer takes one offered row back out of the window.
func (s *Session) WithdrawTradeOffer(ctx context.Context, tradeSlot int) (protocol.TradeMoveAck, error) {
	return s.moveTradeItem(ctx, protocol.TradeMoveRequest{
		SourceList:      protocol.TradeListType,
		SourceSlot:      int16(tradeSlot),
		DestinationList: protocol.TradeBagListType,
	})
}

// DepositTradeGold puts gold on the table. The server recognizes the gold row
// by its shape alone, so the caller only names an amount.
func (s *Session) DepositTradeGold(ctx context.Context, amount int) (protocol.TradeMoveAck, error) {
	return s.moveTradeItem(ctx, protocol.TradeMoveRequest{
		SourceList:      protocol.TradeBagListType,
		Count:           int32(amount),
		DestinationList: protocol.TradeListType,
	})
}

// moveTradeItem sends one op19 move and waits for the server's own
// acknowledgement of it.
func (s *Session) moveTradeItem(ctx context.Context, request protocol.TradeMoveRequest) (protocol.TradeMoveAck, error) {
	if s == nil || s.client == nil {
		return protocol.TradeMoveAck{}, fmt.Errorf("90CN trade needs an open session")
	}
	ctx, cancel := tradeContext(ctx)
	defer cancel()
	ack := make(chan protocol.TradeMoveAck, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdMoveItemspace {
			return
		}
		parsed, ok := protocol.ParseTradeMoveAck(packet.Body)
		if !ok {
			return
		}
		select {
		case ack <- parsed:
		default:
		}
	})
	defer cleanup()
	if err := s.client.MoveItemspace(ctx, request); err != nil {
		return protocol.TradeMoveAck{}, err
	}
	select {
	case parsed := <-ack:
		return parsed, nil
	case <-ctx.Done():
		return protocol.TradeMoveAck{}, fmt.Errorf("90CN trade move acknowledgement: %w", ctx.Err())
	case <-s.Done():
		return protocol.TradeMoveAck{}, fmt.Errorf("90CN session ended during a trade move")
	}
}

// SetTradeState sends one op24 window state and waits for the server to accept
// it.
func (s *Session) SetTradeState(ctx context.Context, state byte) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN trade needs an open session")
	}
	if _, err := protocol.TradeStateBody(state); err != nil {
		return err
	}
	ctx, cancel := tradeContext(ctx)
	defer cancel()
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdItemTradeState {
			return
		}
		ok, code := protocol.TradeMoveResult(packet.Body)
		var result error
		if !ok {
			result = fmt.Errorf("cn90 trade state %d was refused with code %d", state, code)
		}
		select {
		case ack <- result:
		default:
		}
	})
	defer cleanup()
	if err := s.client.ItemTradeState(ctx, state); err != nil {
		return err
	}
	select {
	case err := <-ack:
		return err
	case <-ctx.Done():
		return fmt.Errorf("90CN trade state %d acknowledgement: %w", state, ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during a trade state")
	}
}
