package cn90

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// emoteTestAckBody is the three-byte success body the server builds from
// upperSuccessBody plus the index it stored.
func emoteTestAckBody(stored uint16) []byte {
	body := []byte{1, 0, 0}
	binary.LittleEndian.PutUint16(body[1:3], stored)
	return body
}

func TestSessionChangeEmotionSendsTheIndexAndReturnsTheStoredValue(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		cmd, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || cmd != 1 || typ != protocol.CmdChangeEmotion {
			return
		}
		if len(body) != 2 || binary.LittleEndian.Uint16(body) != 12 {
			t.Errorf("op254 body = %v, want the little-endian index 12", body)
			return
		}
		// The server clamps nothing, but it echoes what it persisted, and that
		// echo is the value a caller must treat as the truth.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdChangeEmotion, Body: emoteTestAckBody(12)})
	}()
	stored, err := session.ChangeEmotion(context.Background(), 12)
	if err != nil {
		t.Fatalf("emotion change failed: %v", err)
	}
	if stored != 12 {
		t.Fatalf("stored index = %d want 12", stored)
	}
}

func TestSessionChangeEmotionReportsARejection(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdChangeEmotion, Body: []byte{0, 4}})
	}()
	if _, err := session.ChangeEmotion(context.Background(), 3); err == nil {
		t.Fatal("a rejected emotion change returned no error")
	}
}

func TestSessionChangeEmotionIgnoresUnrelatedCommandAcks(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		// A different opcode answering with a body that would parse as a
		// success must not satisfy the wait.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdCheckConnection, Body: emoteTestAckBody(99)})
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.CmdChangeEmotion, Body: emoteTestAckBody(98)})
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdChangeEmotion, Body: emoteTestAckBody(11)})
	}()
	stored, err := session.ChangeEmotion(context.Background(), 11)
	if err != nil {
		t.Fatalf("emotion change failed: %v", err)
	}
	if stored != 11 {
		t.Fatalf("stored index = %d want 11", stored)
	}
}

func TestSessionChangeEmotionTimesOutWithoutAnAck(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		// Read the command so the send completes, then stay silent: the server
		// never answers a mood change for a session with no selected character.
		_, _, _, _ = readTestLegacyFrame(serverConn, 5*time.Second)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := session.ChangeEmotion(ctx, 5); err == nil {
		t.Fatal("a silent server produced no error")
	}
}
