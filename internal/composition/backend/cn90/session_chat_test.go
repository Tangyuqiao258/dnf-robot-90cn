package cn90

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// newChatTestSession opens a session with chat wired the way a live one is.
// The reply worker is created only when replies are asked for, which is what
// keeps a receive-only session from being able to write at all.
func newChatTestSession(t *testing.T, replies []string) (*Session, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{
		client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42,
		chat: &chatState{}, chatReplies: replies, chatReplyEnabled: len(replies) > 0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.startChat(ctx)
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session, serverConn
}

// chatTestNotice builds the class0/op118 body the server sends to the target of
// a directed message.
func chatTestNotice(senderID uint16, name, message string) []byte {
	body := []byte{protocol.PeerChatMessageType, 1, 0, 1}
	body = binary.LittleEndian.AppendUint16(body, senderID)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(name)))
	body = append(body, name...)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(message)))
	return append(body, message...)
}

// chatTestReadRequest reads one op17 frame and returns the target id and the
// message the frame carries, so a test can assert on what the peer would see.
func chatTestReadRequest(t *testing.T, conn net.Conn, timeout time.Duration) (uint16, []byte, bool) {
	t.Helper()
	class, typ, body, ok := readTestLegacyFrame(conn, timeout)
	if !ok {
		return 0, nil, false
	}
	if class != protocol.ClassCommand || typ != protocol.CmdPeerChat {
		t.Errorf("chat frame = class %d op %d want class %d op %d", class, typ, protocol.ClassCommand, protocol.CmdPeerChat)
		return 0, nil, false
	}
	if len(body) < 7 {
		t.Errorf("chat body is too short: % X", body)
		return 0, nil, false
	}
	target := binary.LittleEndian.Uint16(body[1:3])
	messageLength := int(binary.LittleEndian.Uint32(body[7:11]))
	if len(body) < 11+messageLength {
		t.Errorf("chat body message overruns: % X", body)
		return 0, nil, false
	}
	return target, body[11 : 11+messageLength], true
}

func TestSessionSendChatAcknowledges(t *testing.T) {
	session, serverConn := newChatTestSession(t, nil)
	go func() {
		target, message, ok := chatTestReadRequest(t, serverConn, 5*time.Second)
		if !ok {
			return
		}
		if target != 7 {
			t.Errorf("target id = %d want 7", target)
		}
		if string(message) != "hello" {
			t.Errorf("message = %q want %q", message, "hello")
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdPeerChat, Body: []byte{1},
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.SendChat(ctx, 7, "bob", "hello"); err != nil {
		t.Fatalf("send chat: %v", err)
	}
}

// TestSessionSendChatReportsASilentDrop is the whole reason the request is
// acknowledged at all: the server answers nothing when the target is offline or
// in another area, so the caller's deadline is the only signal it gets.
func TestSessionSendChatReportsASilentDrop(t *testing.T) {
	session, serverConn := newChatTestSession(t, nil)
	go func() {
		if _, _, ok := chatTestReadRequest(t, serverConn, 5*time.Second); !ok {
			return
		}
		// No acknowledgement is dispatched: this is a dropped message.
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := session.SendChat(ctx, 7, "bob", "hello")
	if err == nil {
		t.Fatal("a dropped message was reported as delivered")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drop error = %v want a deadline", err)
	}
}

func TestSessionSendChatReportsARejection(t *testing.T) {
	session, serverConn := newChatTestSession(t, nil)
	go func() {
		if _, _, ok := chatTestReadRequest(t, serverConn, 5*time.Second); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdPeerChat, Body: []byte{0, 9},
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.SendChat(ctx, 7, "bob", "hello"); err == nil {
		t.Fatal("a rejected message was reported as delivered")
	}
}

// TestSessionShoutNeedsATarget pins the shape of the capability 90CN actually
// has. There is no broadcast, so a target-less intent must be refused rather
// than sent to a name the server would ignore.
func TestSessionShoutNeedsATarget(t *testing.T) {
	session, _ := newChatTestSession(t, nil)
	err := session.Shout(context.Background(), shared.ShoutIntent{Channel: shared.ShoutChannelArea, Message: "hi"})
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) {
		t.Fatalf("target-less shout error = %v want an unsupported capability", err)
	}
	if unsupported.Operation != shared.CapabilityShout || unsupported.Reason == "" {
		t.Fatalf("unsupported shout error = %+v", unsupported)
	}
	err = session.Shout(context.Background(), shared.ShoutIntent{
		Channel: shared.ShoutChannelWorld, Message: "hi", TargetCharacterID: 7, TargetName: "bob",
	})
	if !errors.As(err, &unsupported) {
		t.Fatalf("world shout error = %v want an unsupported capability", err)
	}
}

func TestSessionShoutSendsADirectedMessage(t *testing.T) {
	session, serverConn := newChatTestSession(t, nil)
	go func() {
		target, message, ok := chatTestReadRequest(t, serverConn, 5*time.Second)
		if !ok {
			return
		}
		if target != 9 || string(message) != "hi" {
			t.Errorf("shout went to %d with %q", target, message)
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdPeerChat, Body: []byte{1},
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := session.Shout(ctx, shared.ShoutIntent{
		Channel: shared.ShoutChannelArea, Message: "hi", TargetCharacterID: 9, TargetName: "bob",
	})
	if err != nil {
		t.Fatalf("directed shout: %v", err)
	}
}

// TestSessionChatReceivesADirectedMessage covers the receive half with replies
// switched off: the notice has to be recorded even when nothing answers it.
func TestSessionChatReceivesADirectedMessage(t *testing.T) {
	session, serverConn := newChatTestSession(t, nil)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPeerChatMessage,
		Body: chatTestNotice(77, "alice", "hello there"),
	})
	_ = serverConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a receive-only session wrote to the socket: % X", buffer[:n])
	}
}

// TestSessionChatRepliesToADirectedMessage proves the automatic answer reaches
// the original sender as its own op17 request.
//
// The frame is read on the test goroutine rather than a helper one, because the
// reply worker installs its acknowledgement observer before it writes: a read
// that returns therefore proves the observer is already in place, and the
// acknowledgement dispatched straight after it cannot be missed.
func TestSessionChatRepliesToADirectedMessage(t *testing.T) {
	session, serverConn := newChatTestSession(t, []string{"hello back"})
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPeerChatMessage,
		Body: chatTestNotice(77, "alice", "hello"),
	})
	target, message, ok := chatTestReadRequest(t, serverConn, 5*time.Second)
	if !ok {
		t.Fatal("the message was never answered")
	}
	if target != 77 {
		t.Errorf("reply target = %d want 77", target)
	}
	if string(message) != "hello back" {
		t.Errorf("reply message = %q want %q", message, "hello back")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdPeerChat, Body: []byte{1},
	})
}

// TestSessionChatReplyCooldownStopsAPingPong guards the arrangement the fleet
// actually runs in: two sessions that both auto-reply must not answer each
// other forever. The cooldown is what makes a fleet of identical sessions stand
// together without an endless echo.
func TestSessionChatReplyCooldownStopsAPingPong(t *testing.T) {
	session, serverConn := newChatTestSession(t, []string{"hello back"})
	notice := protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPeerChatMessage,
		Body: chatTestNotice(77, "alice", "hello"),
	}
	// Five messages from one sender in the same instant is the tightest form of
	// the loop this guards against.
	for i := 0; i < 5; i++ {
		session.dispatchPacket(notice)
	}
	if _, _, ok := chatTestReadRequest(t, serverConn, 3*time.Second); !ok {
		t.Fatal("the first message was never answered")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdPeerChat, Body: []byte{1},
	})
	// Anything further would arrive well inside the cooldown window.
	_ = serverConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a second message inside the cooldown was answered too: % X", buffer[:n])
	}
}

// TestSessionChatIgnoresItsOwnNotice pins the guard against a loop: a notice
// attributed to this session's own character has no peer to answer.
func TestSessionChatIgnoresItsOwnNotice(t *testing.T) {
	session, serverConn := newChatTestSession(t, []string{"hello back"})
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPeerChatMessage,
		Body: chatTestNotice(session.selfCharacterID, "self", "hello"),
	})
	_ = serverConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("the session answered its own message: % X", buffer[:n])
	}
}

// TestSessionChatIgnoresTheTradeStateRow is the reader-side twin of the
// protocol test: the item-trade state machine pushes a class0/op17 row on the
// same opcode family, and it must not be mistaken for a delivery.
func TestSessionChatIgnoresTheTradeStateRow(t *testing.T) {
	session, serverConn := newChatTestSession(t, []string{"hello back"})
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.CmdPeerChat, Body: []byte{77, 0, protocol.PeerChatMessageType},
	})
	_ = serverConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buffer := make([]byte, 64)
	if n, err := serverConn.Read(buffer); err == nil && n > 0 {
		t.Fatalf("a trade state row produced a chat reply: % X", buffer[:n])
	}
}

// TestSessionShoutEncodesAsGBK pins the code page. The server never inspects
// the bytes and the receiving client renders them as GBK, so a message sent as
// UTF-8 would arrive as mojibake on a real client.
func TestSessionShoutEncodesAsGBK(t *testing.T) {
	session, serverConn := newChatTestSession(t, nil)
	go func() {
		target, message, ok := chatTestReadRequest(t, serverConn, 5*time.Second)
		if !ok {
			return
		}
		// 你好 in GBK is C4 E3 BA C3.
		if target != 3 || !bytes.Equal(message, []byte{0xC4, 0xE3, 0xBA, 0xC3}) {
			t.Errorf("wire message = % X to %d want C4 E3 BA C3 to 3", message, target)
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdPeerChat, Body: []byte{1},
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.SendChat(ctx, 3, "bob", "你好"); err != nil {
		t.Fatalf("send chat: %v", err)
	}
}
