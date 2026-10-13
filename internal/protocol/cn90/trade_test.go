package cn90

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// tradeTestMoveRow builds the twenty-eight byte op19 body the way the server's
// own wire replay does, so the fixture below is anchored to the decoder rather
// than to this package's encoder.
func tradeTestMoveRow(sourceList byte, sourceSlot int16, instance, count int32, destinationList byte, destinationSlot int16) []byte {
	body := make([]byte, tradeMoveBodyBytes)
	body[0] = sourceList
	binary.LittleEndian.PutUint16(body[1:3], uint16(sourceSlot))
	binary.LittleEndian.PutUint32(body[3:7], uint32(instance))
	binary.LittleEndian.PutUint32(body[7:11], uint32(count))
	body[11] = destinationList
	binary.LittleEndian.PutUint16(body[12:14], uint16(destinationSlot))
	return body
}

func TestTradeOfferBodyMatchesTheServerDecode(t *testing.T) {
	body, err := TradeOfferBody(97, 0, 1)
	if err != nil {
		t.Fatalf("offer body: %v", err)
	}
	if len(body) != tradeMoveBodyBytes {
		t.Fatalf("offer body length = %d want %d", len(body), tradeMoveBodyBytes)
	}
	want := tradeTestMoveRow(TradeBagListType, 97, 0, 1, TradeListType, 0)
	if !bytes.Equal(body, want) {
		t.Fatalf("offer body = % X want % X", body, want)
	}
	// The destination row is deliberately zero: the server assigns the visible
	// row itself and echoes it back in the acknowledgement.
	if slot := int16(binary.LittleEndian.Uint16(body[12:14])); slot != 0 {
		t.Fatalf("offer destination slot = %d want the server-assigned zero", slot)
	}
}

func TestTradeOfferBodyRefusesEmptyAndNegativeMoves(t *testing.T) {
	for name, run := range map[string]func() ([]byte, error){
		"negative slot":  func() ([]byte, error) { return TradeOfferBody(-1, 0, 1) },
		"zero count":     func() ([]byte, error) { return TradeOfferBody(9, 0, 0) },
		"negative count": func() ([]byte, error) { return TradeOfferBody(9, 0, -3) },
		"zero gold":      func() ([]byte, error) { return TradeGoldBody(0) },
		"negative gold":  func() ([]byte, error) { return TradeGoldBody(-1) },
		"negative row":   func() ([]byte, error) { return TradeWithdrawBody(-1) },
	} {
		t.Run(name, func(t *testing.T) {
			if body, err := run(); err == nil {
				t.Fatalf("move was accepted: % X", body)
			}
		})
	}
}

// TestTradeGoldBodyUsesTheSlotZeroShape pins the gold row to the shape the
// server recognizes by position alone: bag list, slot zero, zero instance, a
// positive count and trade slot zero.
func TestTradeGoldBodyUsesTheSlotZeroShape(t *testing.T) {
	body, err := TradeGoldBody(1000)
	if err != nil {
		t.Fatalf("gold body: %v", err)
	}
	want := tradeTestMoveRow(TradeBagListType, 0, 0, 1000, TradeListType, 0)
	if !bytes.Equal(body, want) {
		t.Fatalf("gold body = % X want % X", body, want)
	}
}

func TestTradeWithdrawBodyTargetsTheBag(t *testing.T) {
	body, err := TradeWithdrawBody(int16(TradeOfferSlotFirst))
	if err != nil {
		t.Fatalf("withdraw body: %v", err)
	}
	want := tradeTestMoveRow(TradeListType, int16(TradeOfferSlotFirst), 0, 0, TradeBagListType, 0)
	if !bytes.Equal(body, want) {
		t.Fatalf("withdraw body = % X want % X", body, want)
	}
}

// TestTradeMoveAckReadsTheAssignedRow uses the acknowledgement the server's own
// replay asserts for a one-item offer: success, bag slot 97, one item, trade
// row 3.
func TestTradeMoveAckReadsTheAssignedRow(t *testing.T) {
	raw := []byte{1, 0, 97, 0, 1, 0, 0, 0, 4, 3, 0}
	ack, ok := ParseTradeMoveAck(raw)
	if !ok {
		t.Fatalf("ack was rejected: % X", raw)
	}
	if ack.SourceList != TradeBagListType || ack.SourceSlot != 97 || ack.Count != 1 ||
		ack.DestinationList != TradeListType || ack.DestinationSlot != int16(TradeOfferSlotFirst) {
		t.Fatalf("ack = %+v", ack)
	}
	if ok, code := TradeMoveResult(raw); !ok || code != 0 {
		t.Fatalf("result ok=%t code=%d", ok, code)
	}
	// A refusal carries only the flag and the code; it must never be read as an
	// acknowledgement of a move.
	if ok, code := TradeMoveResult([]byte{0, 7}); ok || code != 7 {
		t.Fatalf("refusal ok=%t code=%d", ok, code)
	}
	if _, ok := ParseTradeMoveAck([]byte{0, 7}); ok {
		t.Fatal("a refusal was parsed as an acknowledgement")
	}
	if _, ok := ParseTradeMoveAck(raw[:10]); ok {
		t.Fatal("a truncated acknowledgement was accepted")
	}
}

func TestTradeStateBodyAcceptsOnlyServerStates(t *testing.T) {
	for _, state := range []byte{
		TradeStateNeutral, TradeStateAddingDone, TradeStateOfferAck, TradeStateReady, TradeStateAddRequest,
	} {
		body, err := TradeStateBody(state)
		if err != nil {
			t.Fatalf("state %d refused: %v", state, err)
		}
		if len(body) != 1 || body[0] != state {
			t.Fatalf("state %d body = % X", state, body)
		}
	}
	for _, state := range []byte{4, 6, 200} {
		if body, err := TradeStateBody(state); err == nil {
			t.Fatalf("state %d was accepted: % X", state, body)
		}
	}
}

func TestTradeStateNoticeReadsTheThreeByteRow(t *testing.T) {
	notice, ok := ParseTradeStateNotice([]byte{5, 0, TradeStateAddingDone})
	if !ok || notice.CharacterID != 5 || notice.State != TradeStateAddingDone {
		t.Fatalf("notice = %+v ok=%t", notice, ok)
	}
	// The class1/op24 success body is a single byte and must not be mistaken for
	// a state row, and neither must a longer projection.
	for _, raw := range [][]byte{{1}, {5, 0}, {5, 0, 1, 0}} {
		if _, ok := ParseTradeStateNotice(raw); ok {
			t.Fatalf("row % X was accepted as a trade state", raw)
		}
	}
}

func TestTradeChangeNoticeMarksTheRemovalRow(t *testing.T) {
	row := make([]byte, TradeChangeEntryBytes)
	binary.LittleEndian.PutUint16(row[:2], TradeOfferSlotFirst)
	binary.LittleEndian.PutUint32(row[2:6], 10008073)
	notice, ok := ParseTradeChangeNotice(row)
	if !ok || notice.TradeSlot != TradeOfferSlotFirst || notice.ItemID != 10008073 || notice.Removed {
		t.Fatalf("change = %+v ok=%t", notice, ok)
	}
	removed := make([]byte, TradeChangeEntryBytes)
	binary.LittleEndian.PutUint16(removed[:2], TradeOfferSlotFirst)
	binary.LittleEndian.PutUint32(removed[2:6], ^uint32(0))
	notice, ok = ParseTradeChangeNotice(removed)
	if !ok || !notice.Removed || notice.TradeSlot != TradeOfferSlotFirst {
		t.Fatalf("removal = %+v ok=%t", notice, ok)
	}
	if _, ok := ParseTradeChangeNotice(row[:5]); ok {
		t.Fatal("a truncated change row was accepted")
	}
}

func TestTradeFinishNoticeDecodesTransfers(t *testing.T) {
	// The receiving side of the server's replay gets one transfer: trade row 3
	// landing in bag slot 65.
	body := []byte{1, 0, 3, 0, 65, 0}
	notice, ok := ParseTradeFinishNotice(body)
	if !ok || len(notice.Transfers) != 1 {
		t.Fatalf("finish = %+v ok=%t", notice, ok)
	}
	if notice.Transfers[0].TradeSlot != TradeOfferSlotFirst || notice.Transfers[0].DestinationSlot != 65 {
		t.Fatalf("transfer = %+v", notice.Transfers[0])
	}
	// The giving side receives an empty list: the peer took nothing from it.
	empty, ok := ParseTradeFinishNotice([]byte{0, 0})
	if !ok || len(empty.Transfers) != 0 {
		t.Fatalf("empty finish = %+v ok=%t", empty, ok)
	}
	for _, raw := range [][]byte{{1, 0, 3, 0}, {1, 0}, {1, 0, 3, 0, 65, 0, 0}} {
		if _, ok := ParseTradeFinishNotice(raw); ok {
			t.Fatalf("truncated finish % X was accepted", raw)
		}
	}
}

func TestTradeCancelNoticeDecodesEntriesIncludingGold(t *testing.T) {
	// One item rows plus the gold pseudo row the server prepends when gold was
	// on the table.
	body := []byte{
		2, 0,
		0, 0, TradeBagListType, 0, 0,
		byte(TradeOfferSlotFirst), 0, TradeBagListType, 97, 0,
	}
	notice, ok := ParseTradeCancelNotice(body)
	if !ok || len(notice.Entries) != 2 {
		t.Fatalf("cancel = %+v ok=%t", notice, ok)
	}
	if notice.Entries[0].TradeSlot != 0 || notice.Entries[0].SourceList != TradeBagListType || notice.Entries[0].SourceSlot != 0 {
		t.Fatalf("gold row = %+v", notice.Entries[0])
	}
	if notice.Entries[1].TradeSlot != TradeOfferSlotFirst || notice.Entries[1].SourceSlot != 97 {
		t.Fatalf("item row = %+v", notice.Entries[1])
	}
	for _, raw := range [][]byte{{1, 0, 3, 0, 0}, {1, 0, 3, 0, 0, 0}} {
		if _, ok := ParseTradeCancelNotice(raw); ok {
			t.Fatalf("truncated cancel % X was accepted", raw)
		}
	}
	// A single five-byte row is well formed even with every field zero: the gold
	// pseudo row is exactly that shape, so the parser must accept it.
	if notice, ok := ParseTradeCancelNotice([]byte{1, 0, 0, 0, 0, 0, 0}); !ok || len(notice.Entries) != 1 {
		t.Fatalf("gold-only cancel = %+v ok=%t", notice, ok)
	}
}

// TestTradeRequestModeUsesTheElevenByteShape covers the op10 body the server
// decodes for mode 1 alongside the quick-party modes.
func TestTradeRequestModeUsesTheElevenByteShape(t *testing.T) {
	body, err := RequestPeerBody(77, TradeRequestMode, 0, 0)
	if err != nil {
		t.Fatalf("trade request body: %v", err)
	}
	if len(body) != 11 || body[2] != TradeRequestMode {
		t.Fatalf("trade request body = % X", body)
	}
	if target := binary.LittleEndian.Uint16(body[:2]); target != 77 {
		t.Fatalf("trade request target = %d", target)
	}
}

// TestParsePeerResponseNoticeReadsTheAcceptance covers the class0/0x0008 notice
// that tells the requester the trade window opened.
func TestParsePeerResponseNoticeReadsTheAcceptance(t *testing.T) {
	notice, ok := ParsePeerResponseNotice([]byte{9, 0, TradeRequestMode, 0, 0, 0, 0})
	if !ok || notice.SourceID != 9 || notice.Mode != TradeRequestMode || notice.Value != 0 {
		t.Fatalf("notice = %+v ok=%t", notice, ok)
	}
	if _, ok := ParsePeerResponseNotice([]byte{9, 0, TradeRequestMode}); ok {
		t.Fatal("a truncated peer response was accepted")
	}
}

// TestTradePromptIsNotAPartyInvite guards the boundary the party follow already
// relies on: the trade prompt must never be answered as a party invite.
func TestTradePromptIsNotAPartyInvite(t *testing.T) {
	if value, ok := PartyInviteAcceptValue(TradeRequestMode); ok {
		t.Fatalf("trade mode accepted as a party invite with value %d", value)
	}
}
