package cn90

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// newTradeTestSession opens a session with the trade module wired the way a
// live one is. The worker is created only when the session was asked to trade,
// which is what keeps a receive-only session from writing at all.
func newTradeTestSession(t *testing.T, stacks int) (*Session, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{
		client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42,
		trade: &tradeState{}, tradeEnabled: true, tradeOfferStacks: stacks, tradeOfferCount: 1,
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.startTrade(ctx)
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session, serverConn
}

// tradeTestPromptBody builds the forwarded class0/0x0007 head the server pushes
// to the target of an op10 request.
func tradeTestPromptBody(source uint16, mode byte) []byte {
	body := make([]byte, 7)
	binary.LittleEndian.PutUint16(body[:2], source)
	body[2] = mode
	return body
}

// tradeTestPeerResponseBody builds the class0/0x0008 notice the server sends
// the original requester once the target answers.
func tradeTestPeerResponseBody(source uint16, mode byte) []byte {
	body := make([]byte, 7)
	binary.LittleEndian.PutUint16(body[:2], source)
	body[2] = mode
	return body
}

// tradeTestStateRow builds the three byte class0/op17 state row.
func tradeTestStateRow(characterID uint16, state byte) []byte {
	return []byte{byte(characterID), byte(characterID >> 8), state}
}

// tradeTestMoveAckBody builds the eleven byte class1/op19 success body with the
// row the server assigned.
func tradeTestMoveAckBody(sourceList byte, sourceSlot int16, count int32, destinationSlot int16) []byte {
	body := []byte{1, sourceList}
	body = binary.LittleEndian.AppendUint16(body, uint16(sourceSlot))
	body = binary.LittleEndian.AppendUint32(body, uint32(count))
	body = append(body, protocol.TradeListType)
	return binary.LittleEndian.AppendUint16(body, uint16(destinationSlot))
}

// tradeTestAcceptAckBody builds the five byte class1/op11 acknowledgement a
// trade accept gets. The trailing byte is the one the server only appends for
// mode 1, which is what tells it apart from a party answer.
func tradeTestAcceptAckBody(target uint16) []byte {
	body := []byte{1}
	body = binary.LittleEndian.AppendUint16(body, target)
	return append(body, protocol.TradeRequestMode, 0)
}

func dispatchTradePrompt(session *Session, source uint16, mode byte) {
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiRequestPeer, Body: tradeTestPromptBody(source, mode),
	})
}

func dispatchTradePeerResponse(session *Session, source uint16) {
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPeerResponse, Body: tradeTestPeerResponseBody(source, protocol.TradeRequestMode),
	})
}

func dispatchTradeStateRow(session *Session, characterID uint16, state byte) {
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiTradeState, Body: tradeTestStateRow(characterID, state),
	})
}

// tradeTestReadFrame reads one frame and fails unless it is the named command.
func tradeTestReadFrame(t *testing.T, conn net.Conn, typ uint16, timeout time.Duration) ([]byte, bool) {
	t.Helper()
	class, got, body, ok := readTestLegacyFrame(conn, timeout)
	if !ok {
		return nil, false
	}
	if class != protocol.ClassCommand || got != typ {
		t.Errorf("frame = class %d op %d want class %d op %d", class, got, protocol.ClassCommand, typ)
		return nil, false
	}
	return body, true
}

// TestSessionTradeOfferSendsTheTwentyEightByteMove pins the op19 shape. The
// server decodes exactly twenty-eight bytes and rejects anything else before
// the trade state machine sees it, so the length is the whole contract.
func TestSessionTradeOfferSendsTheTwentyEightByteMove(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		body, ok := tradeTestReadFrame(t, serverConn, protocol.CmdMoveItemspace, 5*time.Second)
		if !ok {
			return
		}
		if len(body) != 28 {
			t.Errorf("op19 body = %d bytes want 28: % X", len(body), body)
		}
		if body[0] != protocol.TradeBagListType {
			t.Errorf("source list = %d want %d", body[0], protocol.TradeBagListType)
		}
		if slot := int16(binary.LittleEndian.Uint16(body[1:3])); slot != 113 {
			t.Errorf("source slot = %d want 113", slot)
		}
		if count := int32(binary.LittleEndian.Uint32(body[7:11])); count != 3 {
			t.Errorf("count = %d want 3", count)
		}
		if body[11] != protocol.TradeListType {
			t.Errorf("destination list = %d want %d", body[11], protocol.TradeListType)
		}
		// The client does not allocate the visible row: it sends zero and the
		// server answers with the row it picked.
		if slot := int16(binary.LittleEndian.Uint16(body[12:14])); slot != 0 {
			t.Errorf("destination slot = %d want 0", slot)
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdMoveItemspace,
			Body: tradeTestMoveAckBody(protocol.TradeBagListType, 113, 3, 3),
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, err := session.OfferTradeItem(ctx, 113, 3)
	if err != nil {
		t.Fatalf("offer item: %v", err)
	}
	if ack.DestinationSlot != 3 {
		t.Fatalf("assigned row = %d want 3", ack.DestinationSlot)
	}
}

func TestSessionTradeOfferReportsARefusal(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdMoveItemspace, 5*time.Second); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdMoveItemspace, Body: []byte{0, 12},
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.OfferTradeItem(ctx, 113, 1); err == nil {
		t.Fatal("a refused offer was reported as accepted")
	}
}

// TestSessionTradeWithdrawalNamesTheTradeRow pins the other direction of the
// same move: a withdrawal sources the trade container and names the row the
// server assigned, not the bag row the item came from.
func TestSessionTradeWithdrawalNamesTheTradeRow(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		body, ok := tradeTestReadFrame(t, serverConn, protocol.CmdMoveItemspace, 5*time.Second)
		if !ok {
			return
		}
		if body[0] != protocol.TradeListType {
			t.Errorf("source list = %d want %d", body[0], protocol.TradeListType)
		}
		if slot := int16(binary.LittleEndian.Uint16(body[1:3])); slot != 5 {
			t.Errorf("source slot = %d want 5", slot)
		}
		if body[11] != protocol.TradeBagListType {
			t.Errorf("destination list = %d want %d", body[11], protocol.TradeBagListType)
		}
		if count := int32(binary.LittleEndian.Uint32(body[7:11])); count != 0 {
			t.Errorf("count = %d want 0", count)
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdMoveItemspace,
			Body: tradeTestMoveAckBody(protocol.TradeListType, 5, 1, 113),
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.WithdrawTradeOffer(ctx, 5); err != nil {
		t.Fatalf("withdraw offer: %v", err)
	}
}

// TestSessionTradeDepositNamesGoldByShape pins the pseudo row: the server
// recognizes gold by source list zero, source slot zero and a positive count,
// so the amount is the only thing a caller names.
func TestSessionTradeDepositNamesGoldByShape(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		body, ok := tradeTestReadFrame(t, serverConn, protocol.CmdMoveItemspace, 5*time.Second)
		if !ok {
			return
		}
		if body[0] != protocol.TradeBagListType {
			t.Errorf("source list = %d want %d", body[0], protocol.TradeBagListType)
		}
		if slot := int16(binary.LittleEndian.Uint16(body[1:3])); slot != 0 {
			t.Errorf("source slot = %d want 0", slot)
		}
		if amount := int32(binary.LittleEndian.Uint32(body[7:11])); amount != 5000 {
			t.Errorf("amount = %d want 5000", amount)
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdMoveItemspace,
			Body: tradeTestMoveAckBody(protocol.TradeBagListType, 0, 5000, 0),
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.DepositTradeGold(ctx, 5000); err != nil {
		t.Fatalf("deposit gold: %v", err)
	}
}

// TestSessionTradeAcceptWaitsForTheFiveByteAck is the one place the trade and
// the party share a wire: both answer op11, and only the trade answer carries
// the trailing byte. A party answer must not satisfy a trade accept.
func TestSessionTradeAcceptWaitsForTheFiveByteAck(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		body, ok := tradeTestReadFrame(t, serverConn, protocol.CmdResponsePeer, 5*time.Second)
		if !ok {
			return
		}
		if len(body) != 7 {
			t.Errorf("op11 body = %d bytes want 7: % X", len(body), body)
		}
		if target := binary.LittleEndian.Uint16(body[:2]); target != 77 {
			t.Errorf("target = %d want 77", target)
		}
		if body[2] != protocol.TradeRequestMode {
			t.Errorf("mode = %d want %d", body[2], protocol.TradeRequestMode)
		}
		// A party-shaped answer first: it must not be mistaken for the trade's.
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: []byte{1, 77, 0, 0, 0, 0, 0},
		})
		time.Sleep(50 * time.Millisecond)
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: tradeTestAcceptAckBody(77),
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if err := session.AcceptTrade(ctx, 77); err != nil {
		t.Fatalf("accept trade: %v", err)
	}
	// The four byte answer above would have released the wait almost at once;
	// the five byte one only arrives after the deliberate pause.
	if elapsed := time.Since(started); elapsed < 40*time.Millisecond {
		t.Fatalf("a party answer released the trade accept after %v", elapsed)
	}
}

func TestSessionTradeAcceptReportsARefusal(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdResponsePeer, 5*time.Second); !ok {
			return
		}
		// The five byte shape with the failure flag.
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: []byte{0, 77, 1, 9, 0},
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := session.AcceptTrade(ctx, 77); err == nil {
		t.Fatal("a refused accept was reported as accepted")
	}
}

// TestSessionTradeBeginWaitsForThePeerResponse covers the initiator half. The
// op10 request is never acknowledged on its own, so the class0/0x0008 notice is
// the only signal a request was answered at all.
func TestSessionTradeBeginWaitsForThePeerResponse(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		body, ok := tradeTestReadFrame(t, serverConn, protocol.CmdRequestPeer, 5*time.Second)
		if !ok {
			return
		}
		if len(body) != 11 {
			t.Errorf("op10 mode 1 body = %d bytes want 11: % X", len(body), body)
		}
		if target := binary.LittleEndian.Uint16(body[:2]); target != 77 {
			t.Errorf("target = %d want 77", target)
		}
		if body[2] != protocol.TradeRequestMode {
			t.Errorf("mode = %d want %d", body[2], protocol.TradeRequestMode)
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiPeerResponse, Body: tradeTestPeerResponseBody(77, protocol.TradeRequestMode),
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.BeginTrade(ctx, 77); err != nil {
		t.Fatalf("begin trade: %v", err)
	}
}

// TestSessionTradeBeginReportsASilentRequest is the reason BeginTrade waits at
// all: the server records an unanswered invitation and forwards the prompt with
// no reply to the requester, so a deadline is the only refusal a caller sees.
func TestSessionTradeBeginReportsASilentRequest(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	go func() {
		_, _ = tradeTestReadFrame(t, serverConn, protocol.CmdRequestPeer, 5*time.Second)
		// No notice is dispatched: the target never answered.
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := session.BeginTrade(ctx, 77)
	if err == nil {
		t.Fatal("an unanswered request was reported as accepted")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("silent request error = %v want a deadline", err)
	}
}

// TestSessionTradeRefusesItsOwnCharacter guards the loop: a prompt or a
// response attributed to this session has no peer to trade with.
func TestSessionTradeRefusesItsOwnCharacter(t *testing.T) {
	session, _ := newTradeTestSession(t, 0)
	if _, err := session.OfferTradeItem(context.Background(), 113, 1); err == nil {
		t.Fatal("an offer outside a window was accepted")
	}
	if err := session.AcceptTrade(context.Background(), session.selfCharacterID); err == nil {
		t.Fatal("a session accepted a prompt from itself")
	}
	if err := session.BeginTrade(context.Background(), 0); err == nil {
		t.Fatal("a session opened a trade with character zero")
	}
}

// TestSessionTradeAutoAnswersAPromptAndOffersAStack is the whole automatic
// round: a prompt arrives, the session accepts it, puts one stock row into the
// window and declares its own boundary.
func TestSessionTradeAutoAnswersAPromptAndOffersAStack(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 2)

	dispatchTradePrompt(session, 77, protocol.TradeRequestMode)
	if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdResponsePeer, 5*time.Second); !ok {
		t.Fatal("the trade prompt was never answered")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: tradeTestAcceptAckBody(77),
	})

	body, ok := tradeTestReadFrame(t, serverConn, protocol.CmdMoveItemspace, 5*time.Second)
	if !ok {
		t.Fatal("the window was opened but no stock was offered")
	}
	if slot := int16(binary.LittleEndian.Uint16(body[1:3])); slot != 113 {
		t.Errorf("first offer row = %d want 113", slot)
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdMoveItemspace,
		Body: tradeTestMoveAckBody(protocol.TradeBagListType, 113, 1, 3),
	})

	state, ok := tradeTestReadFrame(t, serverConn, protocol.CmdItemTradeState, 5*time.Second)
	if !ok {
		t.Fatal("the offer was never declared finished")
	}
	if len(state) != 1 || state[0] != protocol.TradeStateAddRequest {
		t.Fatalf("adding-done body = % X want %02X", state, protocol.TradeStateAddRequest)
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdItemTradeState, Body: []byte{1},
	})
	if peer, open := session.TradeOpen(); !open || peer != 77 {
		t.Fatalf("window = (%d, %v) want (77, true)", peer, open)
	}
}

// TestSessionTradeIgnoresAPartyInvitePrompt is the twin of the party-follow
// guard: the two prompts share op10 and are told apart by the mode alone.
func TestSessionTradeIgnoresAPartyInvitePrompt(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 2)
	dispatchTradePrompt(session, 77, 13)
	dispatchTradePrompt(session, 77, 0)
	_ = serverConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a party invite opened a trade: % X", buffer[:n])
	}
	if _, open := session.TradeOpen(); open {
		t.Fatal("a party invite opened a trade window")
	}
}

// TestSessionTradeOffersExactlyOneStackPerWindow pins the guard that keeps a
// repeated prompt from giving a second stack away: one window, one offer.
func TestSessionTradeOffersExactlyOneStackPerWindow(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 2)
	dispatchTradePrompt(session, 77, protocol.TradeRequestMode)
	if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdResponsePeer, 5*time.Second); !ok {
		t.Fatal("the trade prompt was never answered")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: tradeTestAcceptAckBody(77),
	})
	if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdMoveItemspace, 5*time.Second); !ok {
		t.Fatal("no stock was offered")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdMoveItemspace,
		Body: tradeTestMoveAckBody(protocol.TradeBagListType, 113, 1, 3),
	})
	if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdItemTradeState, 5*time.Second); !ok {
		t.Fatal("the offer was never declared finished")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdItemTradeState, Body: []byte{1},
	})
	// A duplicate of the same prompt arrives while the window is open. The
	// server replaces a participant's trade when a new one begins, so the
	// session answers it, but the open window must not offer again.
	dispatchTradePrompt(session, 77, protocol.TradeRequestMode)
	if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdResponsePeer, 5*time.Second); !ok {
		t.Fatal("the second prompt was never answered")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: tradeTestAcceptAckBody(77),
	})
	_ = serverConn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a second stack was offered into the same window: % X", buffer[:n])
	}
}

// TestSessionTradeConsentsOnlyWhenBothSidesAreDone pins the boundary the server
// never announces: the first consent is held privately, so this session can
// only learn the peer is ready by consenting itself, and it must not consent
// before the peer has said its offer is final.
func TestSessionTradeConsentsOnlyWhenBothSidesAreDone(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	// The peer's answer opens the window without an offer, because this session
	// was given no stock to offer from.
	dispatchTradePeerResponse(session, 77)
	if _, open := session.TradeOpen(); !open {
		t.Fatal("the peer response did not open a window")
	}
	dispatchTradeStateRow(session, session.selfCharacterID, protocol.TradeStateAddingDone)
	_ = serverConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("consent was sent while the peer was still adding: % X", buffer[:n])
	}
	dispatchTradeStateRow(session, 77, protocol.TradeStateAddingDone)
	body, ok := tradeTestReadFrame(t, serverConn, protocol.CmdItemTradeState, 5*time.Second)
	if !ok {
		t.Fatal("consent was never sent after both sides finished adding")
	}
	if len(body) != 1 || body[0] != protocol.TradeStateReady {
		t.Fatalf("consent body = % X want %02X", body, protocol.TradeStateReady)
	}
}

// TestSessionTradeRetriesConsentAfterARefusal covers the one race the protocol
// leaves open: this session's own boundary row can arrive before the
// acknowledgement of the op24 that caused it, so the first consent may be sent
// too early and refused. The next state row has to try again.
func TestSessionTradeRetriesConsentAfterARefusal(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	dispatchTradePeerResponse(session, 77)
	dispatchTradeStateRow(session, session.selfCharacterID, protocol.TradeStateAddingDone)
	dispatchTradeStateRow(session, 77, protocol.TradeStateAddingDone)
	if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdItemTradeState, 5*time.Second); !ok {
		t.Fatal("consent was never sent")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdItemTradeState, Body: []byte{0, 7},
	})
	// Release happens on the worker goroutine, so the claim is only free once
	// the refusal has been handled there.
	waitForConsentRelease(t, session)
	// The server re-broadcasts a state row once the peer is actually ready.
	dispatchTradeStateRow(session, 77, protocol.TradeStateAddingDone)
	if _, ok := tradeTestReadFrame(t, serverConn, protocol.CmdItemTradeState, 5*time.Second); !ok {
		t.Fatal("a refused consent was never retried")
	}
}

// waitForConsentRelease blocks until the worker has given the consent claim
// back, so a retry is not dispatched into a claim that is still held.
func waitForConsentRelease(t *testing.T, session *Session) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session.trade.mu.RLock()
		claimed := session.trade.consented
		session.trade.mu.RUnlock()
		if !claimed {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the refused consent was never released")
}

// TestSessionTradeCompletionClosesTheWindow covers the two ways a window ends.
// The completion is what a caller needs to know a transfer happened; the
// rollback is what it needs to know one did not.
func TestSessionTradeCompletionClosesTheWindow(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	dispatchTradePeerResponse(session, 77)
	if _, open := session.TradeOpen(); !open {
		t.Fatal("the peer response did not open a window")
	}
	// A completion with one transfer: the peer's row 3 landed in bag row 113.
	body := binary.LittleEndian.AppendUint16(nil, 1)
	body = binary.LittleEndian.AppendUint16(body, 3)
	body = binary.LittleEndian.AppendUint16(body, 113)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiTradeFinish, Body: body,
	})
	if peer, open := session.TradeOpen(); open {
		t.Fatalf("window stayed open on peer %d after completion", peer)
	}
	_ = serverConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a completed trade wrote to the socket: % X", buffer[:n])
	}
	// A rollback ends a second window the same way.
	dispatchTradePeerResponse(session, 88)
	if _, open := session.TradeOpen(); !open {
		t.Fatal("the second peer response did not open a window")
	}
	cancel := binary.LittleEndian.AppendUint16(nil, 1)
	cancel = binary.LittleEndian.AppendUint16(cancel, 3)
	cancel = append(cancel, protocol.TradeBagListType)
	cancel = binary.LittleEndian.AppendUint16(cancel, 113)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiTradeCancel, Body: cancel,
	})
	if peer, open := session.TradeOpen(); open {
		t.Fatalf("window stayed open on peer %d after rollback", peer)
	}
}

// TestSessionTradeIgnoresAMalformedStateRow keeps a truncated row from being
// read as a real state: the three byte shape is exact and shares its opcode
// with the directed-chat state row.
func TestSessionTradeIgnoresAMalformedStateRow(t *testing.T) {
	session, serverConn := newTradeTestSession(t, 0)
	dispatchTradePeerResponse(session, 77)
	// One byte short, and one byte long. Neither is a trade state row.
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiTradeState, Body: []byte{5, 0}})
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiTradeState, Body: []byte{5, 0, 1, 0}})
	_ = serverConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a malformed state row was acted on: % X", buffer[:n])
	}
}

// TestSessionTradeReceiveOnlyNeverWrites checks the gating the scheduler
// relies on: a session that was not asked to trade records the prompt and
// answers nothing.
func TestSessionTradeReceiveOnlyNeverWrites(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session := &Session{
		client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42,
		trade: &tradeState{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.startTrade(ctx)
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	dispatchTradePrompt(session, 77, protocol.TradeRequestMode)
	_ = serverConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a receive-only session wrote to the socket: % X", buffer[:n])
	}
}
