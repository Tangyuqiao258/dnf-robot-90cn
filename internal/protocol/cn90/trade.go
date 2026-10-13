package cn90

import (
	"encoding/binary"
	"fmt"
)

// Player-to-player item trade. The server keeps the window in
// dnfbridge/item_trade_online.go: a trade exists only while both characters
// stand in the same town area, the initiator names the target with op10 mode 1,
// the target answers op11 mode 1, and the window is then driven by op19 item
// moves plus op24 state transitions until both sides consent.
//
// Every number below is read off that file and off the server's own wire replay
// (item_trade_online_test.go), not guessed from the client.

// TradeListType is the trade container. It is the destination list of an offer
// and the source list of a withdrawal. The server also accepts list type 1 as
// an offer source, but the trade container itself is always 4.
const TradeListType byte = 4

// TradeBagListType is the character's main inventory, the only other list type
// an offer may come from (item_trade_online.go addOnlineItemTradeOffer). Gold
// is deposited from slot zero of this same list.
const TradeBagListType byte = 0

// TradeRequestMode is the op10/op11 mode that names a trade rather than a party
// invite. The server forwards it, records it as a pending invite and, on the
// op11 answer, opens the trade window instead of creating a party
// (party_online.go handleOnlineResponsePeer).
const TradeRequestMode byte = 1

// TradeOfferSlots is the server's offer-slot window. Slot zero holds the gold
// row and slots one and two the other trade pseudo rows, so the nine visible
// item rows are 3..11; the server assigns them itself and echoes the assigned
// row in the op19 acknowledgement.
const (
	TradeOfferSlotFirst uint16 = 3
	TradeOfferSlotLast  uint16 = 11
)

// tradeMoveBodyBytes is the exact op19 body length the server decodes
// (inventory.DecodeMoveItemspaceRequest). Any other length is rejected before
// the trade state machine sees it.
const tradeMoveBodyBytes = 28

// TradeChangeEntryBytes is the fixed class0/op15 row size the server projects
// for one offer change (currentItemListEntryWireSize).
const TradeChangeEntryBytes = 0x77

// Trade window states, as the server's op24 decoder spells them
// (item_trade_online.go, currentItemTrade* constants). State 5 and state 1 are
// the same boundary on the wire: the live client submits 5 for "adding
// complete" and the server projects it to 1 before telling anyone. A session
// that sends 1 directly reaches the same state.
const (
	// TradeStateNeutral clears a previous selection while the window is open.
	TradeStateNeutral byte = 0
	// TradeStateAddingDone is the shared "my offer is final" boundary.
	TradeStateAddingDone byte = 1
	// TradeStateOfferAck acknowledges the peer's offer change. It carries no
	// state transition and is never broadcast.
	TradeStateOfferAck byte = 2
	// TradeStateReady is the final consent. The server never broadcasts it: the
	// first consent is held server-side so the second participant still gets its
	// dialog, and the second consent commits the trade atomically.
	TradeStateReady byte = 3
	// TradeStateAddRequest is the live client's "adding complete" spelling,
	// projected to TradeStateAddingDone.
	TradeStateAddRequest byte = 5
)

// TradeMoveRequest is the decoded op19 body. SourceInstance and
// DestinationInstance are the opaque live-item values the client copies out of
// its own item object; the server validates the authoritative repository slot
// and count instead, so a session may send zero there.
type TradeMoveRequest struct {
	SourceList          byte
	SourceSlot          int16
	SourceInstance      int32
	Count               int32
	DestinationList     byte
	DestinationSlot     int16
	DestinationInstance int32
	DestinationStack    int32
	ActorIndex          int32
	TrailingState0      byte
	TrailingState1      byte
}

// TradeMoveBody assembles the twenty-eight byte op19 body.
func TradeMoveBody(request TradeMoveRequest) []byte {
	body := make([]byte, tradeMoveBodyBytes)
	body[0] = request.SourceList
	binary.LittleEndian.PutUint16(body[1:3], uint16(request.SourceSlot))
	binary.LittleEndian.PutUint32(body[3:7], uint32(request.SourceInstance))
	binary.LittleEndian.PutUint32(body[7:11], uint32(request.Count))
	body[11] = request.DestinationList
	binary.LittleEndian.PutUint16(body[12:14], uint16(request.DestinationSlot))
	binary.LittleEndian.PutUint32(body[14:18], uint32(request.DestinationInstance))
	binary.LittleEndian.PutUint32(body[18:22], uint32(request.DestinationStack))
	binary.LittleEndian.PutUint32(body[22:26], uint32(request.ActorIndex))
	body[26] = request.TrailingState0
	body[27] = request.TrailingState1
	return body
}

// TradeOfferBody moves a bag stack into the window. The destination row is zero
// because the client does not allocate the visible row itself: the server picks
// the next free row in 3..11 and echoes it in the acknowledgement.
func TradeOfferBody(bagSlot int16, instance int32, count int32) ([]byte, error) {
	switch {
	case bagSlot < 0:
		return nil, fmt.Errorf("cn90 trade offer needs a bag slot")
	case count <= 0:
		return nil, fmt.Errorf("cn90 trade offer needs a positive count")
	}
	return TradeMoveBody(TradeMoveRequest{
		SourceList:      TradeBagListType,
		SourceSlot:      bagSlot,
		SourceInstance:  instance,
		Count:           count,
		DestinationList: TradeListType,
	}), nil
}

// TradeWithdrawBody takes an offered row back out of the window. The trade row
// is the source and the bag the destination; the server matches the destination
// list against the list the offer came from.
func TradeWithdrawBody(tradeSlot int16) ([]byte, error) {
	if tradeSlot < 0 {
		return nil, fmt.Errorf("cn90 trade withdrawal needs a trade slot")
	}
	return TradeMoveBody(TradeMoveRequest{
		SourceList:      TradeListType,
		SourceSlot:      tradeSlot,
		DestinationList: TradeBagListType,
	}), nil
}

// TradeGoldBody deposits gold. The server recognizes the gold row by shape
// alone: source list zero, source slot zero, a zero instance value, a positive
// count and destination slot zero (isOnlineItemTradeGoldDeposit).
func TradeGoldBody(amount int32) ([]byte, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("cn90 trade gold deposit needs a positive amount")
	}
	return TradeMoveBody(TradeMoveRequest{
		SourceList:      TradeBagListType,
		Count:           amount,
		DestinationList: TradeListType,
	}), nil
}

// TradeMoveAck is the decoded class1/op19 success body. The server rewrites
// DestinationSlot to the row it actually assigned, so a caller that wants to
// withdraw the offer again must use this value rather than the zero it sent.
type TradeMoveAck struct {
	SourceList      byte
	SourceSlot      int16
	Count           int32
	DestinationList byte
	DestinationSlot int16
}

// ParseTradeMoveAck decodes the ten byte success body the server appends behind
// the one byte success flag (buildOnlineItemTradeMoveAck).
func ParseTradeMoveAck(body []byte) (TradeMoveAck, bool) {
	if len(body) < 11 || body[0] != 1 {
		return TradeMoveAck{}, false
	}
	return TradeMoveAck{
		SourceList:      body[1],
		SourceSlot:      int16(binary.LittleEndian.Uint16(body[2:4])),
		Count:           int32(binary.LittleEndian.Uint32(body[4:8])),
		DestinationList: body[8],
		DestinationSlot: int16(binary.LittleEndian.Uint16(body[9:11])),
	}, true
}

// TradeMoveResult reports whether a class1/op19 acknowledgement succeeded and,
// on failure, the server error code.
func TradeMoveResult(body []byte) (bool, byte) {
	if len(body) >= 1 && body[0] == 1 {
		return true, 0
	}
	if len(body) >= 2 {
		return false, body[1]
	}
	return false, 0
}

// TradeStateBody builds the one byte op24 body. The server rejects any other
// length before it looks at the trade.
func TradeStateBody(state byte) ([]byte, error) {
	switch state {
	case TradeStateNeutral, TradeStateAddingDone, TradeStateOfferAck, TradeStateReady, TradeStateAddRequest:
		return []byte{state}, nil
	default:
		return nil, fmt.Errorf("cn90 trade state %d is not one the server accepts", state)
	}
}

// TradeStateNotice is a class0/op17 trade row. The server sends the same row to
// both participants for every state change, so a session sees its own character
// id here as often as its peer's and must filter on the id.
//
// Opcode 17 is shared with the directed-chat state row, which is why this
// three-byte shape is not enough on its own to identify a message; the trade
// reader only runs while a trade is open, and the chat reader only accepts the
// 118 delivery notice.
type TradeStateNotice struct {
	CharacterID uint16
	State       byte
}

// ParseTradeStateNotice decodes the three byte class0/op17 row.
func ParseTradeStateNotice(body []byte) (TradeStateNotice, bool) {
	if len(body) != 3 {
		return TradeStateNotice{}, false
	}
	return TradeStateNotice{
		CharacterID: binary.LittleEndian.Uint16(body[:2]),
		State:       body[2],
	}, true
}

// TradeChangeNotice is a class0/op15 row announcing one offer change on the
// peer's side of the window. Only the head is interpreted: the row carries a
// full projected item entry, but the trade itself needs nothing more than which
// row changed and whether it appeared or disappeared.
type TradeChangeNotice struct {
	TradeSlot uint16
	ItemID    uint32
	// Removed is true for the withdrawal row, which the server writes as the
	// slot plus an all-ones item id (buildOnlineItemTradeRemovedEntry).
	Removed bool
}

// ParseTradeChangeNotice decodes the head of a class0/op15 row.
func ParseTradeChangeNotice(body []byte) (TradeChangeNotice, bool) {
	if len(body) < 6 {
		return TradeChangeNotice{}, false
	}
	itemID := binary.LittleEndian.Uint32(body[2:6])
	return TradeChangeNotice{
		TradeSlot: binary.LittleEndian.Uint16(body[:2]),
		ItemID:    itemID,
		Removed:   itemID == ^uint32(0),
	}, true
}

// TradeTransfer is one row the committed trade moved into the receiving
// character's bag.
type TradeTransfer struct {
	TradeSlot       uint16
	DestinationSlot uint16
}

// TradeFinishNotice is the class0/op18 completion. The server sends it to both
// participants after the atomic exchange has already been written, so an empty
// transfer list means "the peer took nothing from me", not "nothing happened".
type TradeFinishNotice struct {
	Transfers []TradeTransfer
}

// ParseTradeFinishNotice decodes the completion body (u16 count followed by
// one u16 trade row and one u16 bag row per transfer). A truncated tail is
// rejected rather than silently shortened.
func ParseTradeFinishNotice(body []byte) (TradeFinishNotice, bool) {
	if len(body) < 2 {
		return TradeFinishNotice{}, false
	}
	count := int(binary.LittleEndian.Uint16(body[:2]))
	if len(body) != 2+count*4 {
		return TradeFinishNotice{}, false
	}
	notice := TradeFinishNotice{Transfers: make([]TradeTransfer, 0, count)}
	for index := 0; index < count; index++ {
		row := body[2+index*4 : 6+index*4]
		notice.Transfers = append(notice.Transfers, TradeTransfer{
			TradeSlot:       binary.LittleEndian.Uint16(row[:2]),
			DestinationSlot: binary.LittleEndian.Uint16(row[2:4]),
		})
	}
	return notice, true
}

// TradeCancelEntry names one row the cancelled trade is handing back: a trade
// row and the list and slot it came from. Gold is spelled as the pseudo row
// zero with bag list and bag slot zero.
type TradeCancelEntry struct {
	TradeSlot  uint16
	SourceList byte
	SourceSlot uint16
}

// TradeCancelNotice is the class0/op16 rollback. The server sends it to both
// participants when a commit is rejected or a participant leaves.
type TradeCancelNotice struct {
	Entries []TradeCancelEntry
}

// ParseTradeCancelNotice decodes the rollback body (u16 count followed by one
// u16 trade row, one u8 source list and one u16 source slot per entry).
func ParseTradeCancelNotice(body []byte) (TradeCancelNotice, bool) {
	if len(body) < 2 {
		return TradeCancelNotice{}, false
	}
	count := int(binary.LittleEndian.Uint16(body[:2]))
	if len(body) != 2+count*5 {
		return TradeCancelNotice{}, false
	}
	notice := TradeCancelNotice{Entries: make([]TradeCancelEntry, 0, count)}
	for index := 0; index < count; index++ {
		row := body[2+index*5 : 7+index*5]
		notice.Entries = append(notice.Entries, TradeCancelEntry{
			TradeSlot:  binary.LittleEndian.Uint16(row[:2]),
			SourceList: row[2],
			SourceSlot: binary.LittleEndian.Uint16(row[3:5]),
		})
	}
	return notice, true
}

// ParseTradeResponseAck recognizes the class1/op11 answer to a trade accept and
// returns the character the request named. The server appends a trailing byte
// only for mode 1 (party.BuildResponsePeerAckPayload), so the length alone
// separates a trade acknowledgement from a party one and lets a reader tell the
// two apart without tracking what it sent.
func ParseTradeResponseAck(body []byte) (uint16, bool) {
	if len(body) != 5 || body[0] != 1 || body[3] != TradeRequestMode {
		return 0, false
	}
	return binary.LittleEndian.Uint16(body[1:3]), true
}

// ParsePeerResponseNotice decodes the class0/0x0008 notice the server sends the
// original requester once the target answers an op10 request
// (party.BuildResponsePeerNotice). For a trade request this is the only signal
// that the window opened: the server does not push a trade state row until one
// of the participants moves.
func ParsePeerResponseNotice(body []byte) (RequestPeerNotice, bool) {
	if len(body) < 7 {
		return RequestPeerNotice{}, false
	}
	return RequestPeerNotice{
		SourceID: binary.LittleEndian.Uint16(body[:2]),
		Mode:     body[2],
		Value:    binary.LittleEndian.Uint32(body[3:7]),
	}, true
}
