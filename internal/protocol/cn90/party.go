package cn90

import (
	"encoding/binary"
	"fmt"
)

// The town party bridge. CmdRequestPeer records an invitation server-side and
// forwards a class0/NotiRequestPeer prompt to the target; CmdResponsePeer
// answers a forwarded prompt and is what actually creates or joins the
// runtime party (dnfbridge/party_online.go handleOnlineResponsePeer).

// partyRequestNoticeBytes is the shortest forwarded prompt body: quick-party
// modes 4 and 13 carry no tail at all.
const partyRequestNoticeBytes = 7

// responsePeerBodyBytes is the exact op11 body length the server decodes; any
// other length is rejected before dispatch.
const responsePeerBodyBytes = 7

// RequestPeerNotice is a parsed class0/NotiRequestPeer prompt. The server
// forwards an op10 request with the sender's character id in the source
// field, so SourceID is the inviter the op11 answer must target.
type RequestPeerNotice struct {
	SourceID uint16
	Mode     byte
	Value    uint32
}

// ParseRequestPeerNotice parses the forwarded prompt head
// (u16 sourceID + u8 mode + u32 value0). Mode-specific tails are ignored: the
// answer only needs the source, the mode and the accept value.
func ParseRequestPeerNotice(body []byte) (RequestPeerNotice, bool) {
	if len(body) < partyRequestNoticeBytes {
		return RequestPeerNotice{}, false
	}
	return RequestPeerNotice{
		SourceID: binary.LittleEndian.Uint16(body[:2]),
		Mode:     body[2],
		Value:    binary.LittleEndian.Uint32(body[3:7]),
	}, true
}

// PartyInviteAcceptValue maps a forwarded prompt mode to the op11 value that
// accepts it and reports whether the mode is a party invite at all.
// The current EXE accepts ordinary mode-0/12 prompts with value zero and
// quick-party modes 4/13 with value one; mode 1 is the trade prompt and mode
// 15 the town-menu marker, so neither is a party invite here
// (party_online.go isPartyInviteResponse / peerResponseRejected). The trade
// prompt is answered by the trade session instead, with the same op11 shape and
// a zero value.
func PartyInviteAcceptValue(mode byte) (uint32, bool) {
	switch mode {
	case 0, 12:
		return 0, true
	case 4, 13:
		return 1, true
	default:
		return 0, false
	}
}

// RequestPeerBody builds the op10 request body for a forwarded peer request
// mode. The server validates the body length strictly per mode: modes 0/12
// carry the 13-byte shape (u16 value1 + u32 value2 tail) and modes 1/4/13 the
// 11-byte shape (u32 value2 tail) (party/request.go DecodeRequestPeerRequest).
// Mode 1 is the trade request, which the server records as a pending invite and
// forwards to the target exactly like a party invite.
func RequestPeerBody(targetID uint16, mode byte, value0, value2 uint32) ([]byte, error) {
	var body []byte
	switch mode {
	case 0, 12:
		body = make([]byte, 13)
		binary.LittleEndian.PutUint32(body[9:13], value2)
	case TradeRequestMode, 4, 13:
		body = make([]byte, 11)
		binary.LittleEndian.PutUint32(body[7:11], value2)
	default:
		return nil, fmt.Errorf("cn90 peer request mode %d is not a forwarded peer request mode", mode)
	}
	binary.LittleEndian.PutUint16(body[:2], targetID)
	body[2] = mode
	binary.LittleEndian.PutUint32(body[3:7], value0)
	return body, nil
}

// ResponsePeerBody builds the seven-byte op11 answer the server decodes as
// u16 target + u8 mode + u32 value.
func ResponsePeerBody(targetID uint16, mode byte, value uint32) []byte {
	body := make([]byte, responsePeerBodyBytes)
	binary.LittleEndian.PutUint16(body[:2], targetID)
	body[2] = mode
	binary.LittleEndian.PutUint32(body[3:7], value)
	return body
}

// ResponsePeerResult reports whether a class1/op11 acknowledgement succeeded
// and, on failure, the server error code.
func ResponsePeerResult(body []byte) (bool, byte) {
	if len(body) >= 1 && body[0] == 1 {
		return true, 0
	}
	if len(body) >= 2 {
		return false, body[1]
	}
	return false, 0
}

// PartyInviteMode is the quick-party invite the current client emits from its
// party window. The server records it as a pending invite exactly like the
// ordinary mode 0, but the answering client accepts a mode 13 prompt with
// value one rather than zero, which is the shape the auto-accept session
// already sends back.
const PartyInviteMode byte = 13

// partyMemberRealtimeRowBytes is one roster row: u16 character id, u8 hp
// percent, u8 reserved and u8 party slot.
const partyMemberRealtimeRowBytes = 5

// ParsePartyRealtimeMembers parses the class0/0x0099 roster body
// (u8 count + count rows) and returns the member character ids in the server's
// slot order. An empty body with a zero count is a real answer — the session is
// in no party — so only a body that does not match its own count is rejected.
func ParsePartyRealtimeMembers(body []byte) ([]uint16, bool) {
	if len(body) < 1 {
		return nil, false
	}
	count := int(body[0])
	if len(body) != 1+count*partyMemberRealtimeRowBytes {
		return nil, false
	}
	members := make([]uint16, 0, count)
	for slot := 0; slot < count; slot++ {
		row := body[1+slot*partyMemberRealtimeRowBytes:]
		member := binary.LittleEndian.Uint16(row[:2])
		if member == 0 {
			continue
		}
		members = append(members, member)
	}
	return members, true
}
