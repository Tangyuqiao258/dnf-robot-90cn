package cn90

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// peerChatTestNotice builds the class0/op118 body the server delivers:
// `u8 message type, u8 channel id, u8 reserved, u8 channel id, u16 sender id,
// dstr sender name, dstr message`.
func peerChatTestNotice(messageType, channel byte, senderID uint16, name, message string) []byte {
	body := []byte{messageType, channel, 0, channel}
	body = binary.LittleEndian.AppendUint16(body, senderID)
	body = appendDstr(body, []byte(name))
	body = appendDstr(body, []byte(message))
	return body
}

// TestPeerChatRequestBodyMatchesTheLiveMenuShape pins the exact op17 body the
// server accepts. The fixture is the same one the server's own regression test
// uses, because the layout is what the server's decoder is written against and
// nothing in the request is optional.
func TestPeerChatRequestBodyMatchesTheLiveMenuShape(t *testing.T) {
	body, err := PeerChatRequestBody(PeerChatMessageType, 5, []byte("nihao "), []byte("hello"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	want := []byte{
		0x24,
		0x05, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x06, 0x00, 0x00, 0x00, 'n', 'i', 'h', 'a', 'o', ' ',
		0x05, 0x00, 0x00, 0x00, 'h', 'e', 'l', 'l', 'o',
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("op17 body = % X want % X", body, want)
	}
	// 1 message type + 2 target + 4 reserved + two u32-prefixed strings. The
	// server's own minimum parse length is 15, the same shape with both strings
	// empty, which this encoder never produces.
	if len(body) != 26 {
		t.Fatalf("op17 body length = %d want 26", len(body))
	}
}

// TestPeerChatRequestBodyRejectsWhatTheServerDropsSilently covers the four
// refusals the server answers with nothing at all. A caller can only find out
// here, because a dropped request and a slow one look identical on the wire.
func TestPeerChatRequestBodyRejectsWhatTheServerDropsSilently(t *testing.T) {
	if _, err := PeerChatRequestBody(PeerChatMessageType, 0, []byte("hi"), []byte("x")); err == nil {
		t.Fatal("a zero target id was accepted")
	}
	if _, err := PeerChatRequestBody(PeerChatMessageType, 5, nil, []byte("x")); err == nil {
		t.Fatal("an empty message was accepted")
	}
	if _, err := PeerChatRequestBody(PeerChatMessageType, 5, []byte("hi"), nil); err == nil {
		t.Fatal("an empty target name was accepted")
	}
	long := bytes.Repeat([]byte("a"), peerChatDstrMaximum+1)
	if _, err := PeerChatRequestBody(PeerChatMessageType, 5, long, []byte("x")); err == nil {
		t.Fatal("an oversized message was accepted")
	}
	if _, err := PeerChatRequestBody(PeerChatMessageType, 5, []byte("hi"), long); err == nil {
		t.Fatal("an oversized target name was accepted")
	}
	// The cap itself is inclusive: the server refuses only what is longer.
	if _, err := PeerChatRequestBody(PeerChatMessageType, 5, bytes.Repeat([]byte("a"), peerChatDstrMaximum), []byte("x")); err != nil {
		t.Fatalf("a message at the byte cap was rejected: %v", err)
	}
}

func TestPeerChatAckClassifiesTheReply(t *testing.T) {
	if err := PeerChatAck([]byte{1}); err != nil {
		t.Fatalf("success body was rejected: %v", err)
	}
	if err := PeerChatAck([]byte{1, 0, 0}); err != nil {
		t.Fatalf("padded success body was rejected: %v", err)
	}
	if err := PeerChatAck(nil); err == nil {
		t.Fatal("an empty reply was accepted as success")
	}
	if err := PeerChatAck([]byte{0, 7}); err == nil {
		t.Fatal("a failure code was accepted as success")
	}
	if err := PeerChatAck([]byte{0}); err == nil {
		t.Fatal("a bare failure was accepted as success")
	}
}

func TestParsePeerChatNotice(t *testing.T) {
	body := peerChatTestNotice(PeerChatMessageType, 3, 77, "alice", "hello there")
	notice, ok := ParsePeerChatNotice(body)
	if !ok {
		t.Fatalf("valid notice body % X did not parse", body)
	}
	if notice.MessageType != PeerChatMessageType || notice.ChannelID != 3 {
		t.Fatalf("notice header = %+v", notice)
	}
	if notice.SenderID != 77 || notice.SenderName != "alice" || notice.Message != "hello there" {
		t.Fatalf("notice payload = %+v", notice)
	}
}

// TestParsePeerChatNoticeRejectsThePeerStateRow guards the one packet that
// shares an opcode with this feature. The item-trade state machine pushes a
// three-byte class0/op17 row, and a reader that mistook it for a message would
// invent a sender out of a trade peer id.
func TestParsePeerChatNoticeRejectsThePeerStateRow(t *testing.T) {
	stateRow := []byte{77, 0, PeerChatMessageType}
	if _, ok := ParsePeerChatNotice(stateRow); ok {
		t.Fatal("a class0/op17 peer state row parsed as a delivery notice")
	}
	if _, ok := ParsePeerChatNotice(nil); ok {
		t.Fatal("an empty body parsed as a delivery notice")
	}
	// A trailing byte after the message means the body was not what it claimed.
	extended := append(peerChatTestNotice(PeerChatMessageType, 1, 5, "a", "b"), 0)
	if _, ok := ParsePeerChatNotice(extended); ok {
		t.Fatal("a notice with a trailing byte parsed")
	}
}
