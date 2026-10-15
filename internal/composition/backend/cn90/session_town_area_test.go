package cn90

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// townAreaTestSession builds a session with a character id and nothing else: the
// area transition reads no other state, and the test owns the socket.
func townAreaTestSession(t *testing.T) (*Session, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session, serverConn
}

func TestTownAreaAnswerClassifiesTheServersAnswer(t *testing.T) {
	intent := shared.TownAreaMoveIntent{Village: 98, Area: 1, X: 700, Y: 200, SourceVillage: 38}
	for _, tc := range []struct {
		name      string
		packet    protocol.Packet
		self      uint16
		wantValid bool
		wantMatch bool
		wantWhere string
	}{
		{
			name: "the requested map confirms the transition",
			packet: protocol.Packet{
				Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
				Body: testUserAreaBody(42, 98, 1, 700, 200),
			},
			self: 42, wantValid: true, wantMatch: true,
		},
		{
			name: "the room answers a request to leave it with its own exit",
			packet: protocol.Packet{
				Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
				Body: testUserAreaBody(42, 38, 0, 812, 355),
			},
			self: 42, wantValid: true, wantMatch: false, wantWhere: "38/0/812/355",
		},
		{
			name: "another character's area change says nothing about this one",
			packet: protocol.Packet{
				Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
				Body: testUserAreaBody(99, 98, 1, 700, 200),
			},
			self: 42, wantValid: false,
		},
		{
			name: "a session without a selected character cannot filter by key",
			packet: protocol.Packet{
				Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
				Body: testUserAreaBody(99, 98, 1, 700, 200),
			},
			self: 0, wantValid: true, wantMatch: true,
		},
		{
			name: "another notice type is not a landing",
			packet: protocol.Packet{
				Class: protocol.ClassNotice, Type: protocol.NotiUserPosition,
				Body: testUserAreaBody(42, 98, 1, 700, 200),
			},
			self: 42, wantValid: false,
		},
		{
			name: "a truncated body is not a landing",
			packet: protocol.Packet{
				Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
				Body: testUserAreaBody(42, 98, 1, 700, 200)[:7],
			},
			self: 42, wantValid: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation, matched := townAreaAnswer(tc.packet, tc.self, intent)
			if observation.valid != tc.wantValid {
				t.Fatalf("valid = %v want %v", observation.valid, tc.wantValid)
			}
			if !tc.wantValid {
				if matched {
					t.Fatal("a packet that is not a landing matched the intent")
				}
				return
			}
			if matched != tc.wantMatch {
				t.Fatalf("matched = %v want %v", matched, tc.wantMatch)
			}
			if tc.wantWhere != "" {
				where := fmt.Sprintf("%d/%d/%d/%d", observation.village, observation.area, observation.x, observation.y)
				if where != tc.wantWhere {
					t.Fatalf("landing = %s want %s", where, tc.wantWhere)
				}
			}
		})
	}
}

func TestSessionMoveTownAreaAdoptsARedirectedLanding(t *testing.T) {
	session, serverConn := townAreaTestSession(t)
	go func() {
		cls, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no area transition request was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdSetUserArea {
			t.Errorf("transition frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdSetUserArea)
			return
		}
		// The Seria lobby has no direct cross-town portal, so the server answers
		// with the room's own exit instead of the requested map.
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
			Body: testUserAreaBody(42, 38, 0, 812, 355),
		})
	}()
	err := session.MoveTownArea(context.Background(), shared.TownAreaMoveIntent{
		Village: 98, Area: 1, X: 700, Y: 200, SourceVillage: 38,
	})
	if err == nil {
		t.Fatal("a redirect was reported as the requested transition")
	}
	observation, valid := session.LastTownAreaObservation()
	if !valid {
		t.Fatal("the redirected landing was not recorded")
	}
	if observation.village != 38 || observation.area != 0 || observation.x != 812 || observation.y != 355 {
		t.Fatalf("recorded landing = %d/%d/%d/%d want 38/0/812/355",
			observation.village, observation.area, observation.x, observation.y)
	}
}

func TestSessionMoveTownAreaRecordsTheConfirmedLanding(t *testing.T) {
	session, serverConn := townAreaTestSession(t)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			t.Errorf("no area transition request was written")
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
			Body: testUserAreaBody(42, 98, 1, 700, 200),
		})
	}()
	if err := session.MoveTownArea(context.Background(), shared.TownAreaMoveIntent{
		Village: 98, Area: 1, X: 700, Y: 200, SourceVillage: 38,
	}); err != nil {
		t.Fatalf("confirmed transition failed: %v", err)
	}
	observation, valid := session.LastTownAreaObservation()
	if !valid || observation.village != 98 || observation.area != 1 {
		t.Fatalf("recorded landing = %+v valid=%v want 98/1", observation, valid)
	}
}

func TestSessionMoveTownAreaIgnoresAPeersAreaNotice(t *testing.T) {
	session, serverConn := townAreaTestSession(t)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			t.Errorf("no area transition request was written")
			return
		}
		// A partymate entering another map arrives while this session waits. It
		// carries the peer's key, so it must not be read as this character's
		// landing.
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
			Body: testUserAreaBody(99, 98, 1, 700, 200),
		})
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
			Body: testUserAreaBody(42, 38, 0, 812, 355),
		})
	}()
	if err := session.MoveTownArea(context.Background(), shared.TownAreaMoveIntent{
		Village: 98, Area: 1, X: 700, Y: 200, SourceVillage: 38,
	}); err == nil {
		t.Fatal("the peer notice was mistaken for the requested landing")
	}
	observation, valid := session.LastTownAreaObservation()
	if !valid || observation.village != 38 || observation.area != 0 {
		t.Fatalf("recorded landing = %+v valid=%v want this character's 38/0", observation, valid)
	}
}

func TestActionTransportMoveTownCorrectsItsBeliefFromARedirect(t *testing.T) {
	session, serverConn := townAreaTestSession(t)
	transport := NewActionTransport()
	transport.sessions[7] = session
	transport.status[7] = shared.RuntimeStatus{UID: 7, Village: 38, Area: 1, X: 450, Y: 234}
	transport.locationKnown[7] = true
	go func() {
		cls, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no area transition request was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdSetUserArea {
			t.Errorf("transition frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdSetUserArea)
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiUserArea,
			Body: testUserAreaBody(42, 38, 0, 812, 355),
		})
	}()
	err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{
		UID: 7, Village: 98, Area: 1, X: 700, Y: 200,
	})
	if err == nil {
		t.Fatal("a redirected transition was reported as a clean move")
	}
	status := transport.status[7]
	if status.Village != 38 || status.Area != 0 || status.X != 812 || status.Y != 355 {
		t.Fatalf("transport belief = %d/%d/%d/%d want the announced 38/0/812/355",
			status.Village, status.Area, status.X, status.Y)
	}
	if !transport.locationKnown[7] {
		t.Fatal("the corrected belief was not marked as known")
	}
}
