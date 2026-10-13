package cn90

import (
	"encoding/binary"
	"fmt"
)

// Same-area directed chat (op17 request, op118 notice).
//
// This protocol generation has no broadcast channel. The one thing the server
// forwards is a single message between two characters that share a town area
// (dnfbridge/online_social.go), so every request names its target and nothing
// here can address "everyone nearby".
//
// The target is matched by character id alone. The name that travels beside it
// is required to be non-empty, is compared to nothing and is not what the
// receiver is shown: the delivery notice carries the sender's name as the
// server resolves it from its own roster. Both strings are byte-transparent
// DSTRs, and the message is handed to the target unchanged, so whatever
// encoding the sender writes is what the receiver has to decode.

// PeerChatMessageType is the message type the live client's chat menu sends.
// The server never branches on it, it only echoes the byte into the peer state
// row and the delivery notice, so the value has to be reproduced rather than
// understood.
const PeerChatMessageType byte = 0x24

// peerChatRequestMinimum is the shortest body the server will parse: one
// message type byte, the target id, the reserved word and two empty DSTRs. An
// empty message and an empty name are refused on top of that, so this encoder
// never produces anything that short.
const peerChatRequestMinimum = 15

// peerChatDstrMaximum mirrors the server's own cap on one DSTR payload.
const peerChatDstrMaximum = 4096

// PeerChatRequestBody builds the class1/op17 request:
// `u8 message type, u16 target id, u32 reserved, dstr message, dstr target name`.
//
// The server rejects a body that does not end exactly after the name, one whose
// target id is zero and one whose name is empty, and it drops an empty message
// later on; all four refusals are silent, so they are caught here instead. The
// name is placed in the request because the server requires a non-empty one
// there; the target is found by id, so the name only has to be the right
// character's name for the log the server writes beside the delivery.
func PeerChatRequestBody(messageType byte, targetID uint16, message, targetName []byte) ([]byte, error) {
	switch {
	case targetID == 0:
		return nil, fmt.Errorf("cn90 peer chat needs a target character id")
	case len(message) == 0:
		return nil, fmt.Errorf("cn90 peer chat needs a message")
	case len(message) > peerChatDstrMaximum:
		return nil, fmt.Errorf("cn90 peer chat message is %d bytes, over the %d byte limit", len(message), peerChatDstrMaximum)
	case len(targetName) == 0:
		return nil, fmt.Errorf("cn90 peer chat needs a target name")
	case len(targetName) > peerChatDstrMaximum:
		return nil, fmt.Errorf("cn90 peer chat target name is %d bytes, over the %d byte limit", len(targetName), peerChatDstrMaximum)
	}
	body := make([]byte, 0, peerChatRequestMinimum+len(message)+len(targetName))
	body = append(body, messageType)
	body = binary.LittleEndian.AppendUint16(body, targetID)
	body = binary.LittleEndian.AppendUint32(body, 0)
	body = appendDstr(body, message)
	body = appendDstr(body, targetName)
	return body, nil
}

// PeerChatAck classifies the class1/op17 reply. The server answers it only
// after every check it makes has passed, so a refusal of any kind - malformed
// body, target offline, target in another area, empty message - leaves the
// request unanswered rather than carrying an error.
func PeerChatAck(body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("cn90 peer chat returned an empty body")
	}
	if body[0] == 1 {
		return nil
	}
	if len(body) >= 2 {
		return fmt.Errorf("cn90 peer chat rejected with code %d", body[1])
	}
	return fmt.Errorf("cn90 peer chat was rejected")
}

// PeerChatNotice is the class0/op118 message the server delivers to the target
// of a directed chat.
type PeerChatNotice struct {
	MessageType byte
	ChannelID   byte
	SenderID    uint16
	SenderName  string
	Message     string
}

// ParsePeerChatNotice decodes `u8 message type, u8 channel id, u8 reserved,
// u8 channel id, u16 sender id, dstr sender name, dstr message`. The name and
// the message arrive as raw bytes with no declared encoding.
func ParsePeerChatNotice(body []byte) (PeerChatNotice, bool) {
	r := &wireReader{body: body}
	notice := PeerChatNotice{
		MessageType: r.byte(),
		ChannelID:   r.byte(),
	}
	r.skip(1) // reserved
	r.skip(1) // the channel id, repeated
	notice.SenderID = r.u16()
	notice.SenderName = DecodeWireName(r.dstr())
	notice.Message = DecodeWireName(r.dstr())
	if r.err != nil || r.remaining() != 0 {
		return PeerChatNotice{}, false
	}
	return notice, true
}

// appendDstr appends the server's own length-prefixed string form: a u32 byte
// length followed by that many raw bytes, with no terminator.
func appendDstr(body, raw []byte) []byte {
	body = binary.LittleEndian.AppendUint32(body, uint32(len(raw)))
	return append(body, raw...)
}
