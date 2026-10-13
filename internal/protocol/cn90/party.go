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
// 15 the town-menu marker, and both must stay unanswered
// (party_online.go isPartyInviteResponse / peerResponseRejected).
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

// RequestPeerBody builds the op10 request body for a party invite mode. The
// server validates the body length strictly per mode: modes 0/12 carry the
// 13-byte shape (u16 value1 + u32 value2 tail) and modes 4/13 the 11-byte
// shape (u32 value2 tail) (party/request.go DecodeRequestPeerRequest).
func RequestPeerBody(targetID uint16, mode byte, value0, value2 uint32) ([]byte, error) {
	var body []byte
	switch mode {
	case 0, 12:
		body = make([]byte, 13)
		binary.LittleEndian.PutUint32(body[9:13], value2)
	case 4, 13:
		body = make([]byte, 11)
		binary.LittleEndian.PutUint32(body[7:11], value2)
	default:
		return nil, fmt.Errorf("cn90 peer request mode %d is not a party invite mode", mode)
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
