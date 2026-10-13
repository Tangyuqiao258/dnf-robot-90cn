package cn90

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// Legacy frames are the 13-byte header (cmd, type, total length, checksum,
// sequence) plus body.
const testLegacyFrameHeaderLen = 13

// newPartyTestSession builds one auto-accepting session whose client speaks
// over an in-memory pipe, so the responder goroutine's answer can be read back
// without a live server.
func newPartyTestSession(t *testing.T) (*Session, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	ctx, cancel := context.WithCancel(context.Background())
	session.startPartyAutoAccept(ctx)
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session, serverConn
}

// readTestLegacyFrame reads one framed client packet and reports whether a
// frame arrived within the timeout.
func readTestLegacyFrame(conn net.Conn, timeout time.Duration) (byte, uint16, []byte, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	header := make([]byte, testLegacyFrameHeaderLen)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, 0, nil, false
	}
	length := int(binary.LittleEndian.Uint32(header[3:7]))
	if length < testLegacyFrameHeaderLen {
		return 0, 0, nil, false
	}
	body := make([]byte, length-testLegacyFrameHeaderLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, 0, nil, false
	}
	return header[0], binary.LittleEndian.Uint16(header[1:3]), body, true
}

func TestSessionPartyAutoAcceptAnswersInvitePrompt(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want []byte
	}{
		// Quick-party mode 13 accepts with value 1.
		{"quick invite", []byte{0xe9, 0x03, 13, 0, 0, 0, 0}, []byte{0xe9, 0x03, 13, 1, 0, 0, 0}},
		// Ordinary mode 0 accepts with value 0; the tail is ignored.
		{"ordinary invite", append([]byte{0xe9, 0x03, 0, 0, 0, 0, 0}, make([]byte, 6)...), []byte{0xe9, 0x03, 0, 0, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, serverConn := newPartyTestSession(t)
			session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiRequestPeer, Body: tc.body})
			cmd, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
			if !ok {
				t.Fatal("no op11 answer was written")
			}
			if cmd != 1 || typ != protocol.CmdResponsePeer {
				t.Fatalf("answer frame cmd=%d type=%d", cmd, typ)
			}
			if !bytes.Equal(body, tc.want) {
				t.Fatalf("answer body = % X want % X", body, tc.want)
			}
		})
	}
}

func TestSessionPartyAutoAcceptSkipsTradePrompt(t *testing.T) {
	session, serverConn := newPartyTestSession(t)
	// The server forwards the mode-1 trade prompt through the same op7 notice.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiRequestPeer,
		Body: []byte{1, 0, 1, 0x0a, 0x82, 0x01, 0, 0, 0, 0, 0},
	})
	if _, _, _, ok := readTestLegacyFrame(serverConn, 300*time.Millisecond); ok {
		t.Fatal("trade prompt was answered as a party invite")
	}
}

func TestSessionPartyAutoAcceptDisabledStaysSilent(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	// A session that never starts the auto-accept workflow keeps the client
	// untouched.
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiRequestPeer,
		Body: []byte{0xe9, 0x03, 13, 0, 0, 0, 0},
	})
	if _, _, _, ok := readTestLegacyFrame(serverConn, 300*time.Millisecond); ok {
		t.Fatal("disabled session answered an invite")
	}
}
