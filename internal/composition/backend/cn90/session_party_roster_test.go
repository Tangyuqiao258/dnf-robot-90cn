package cn90

import (
	"context"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// partyRosterBody builds the class0/0x0099 body the server pushes on every
// membership change: a count, then one five-byte row per member.
func partyRosterBody(characterIDs ...uint16) []byte {
	body := []byte{byte(len(characterIDs))}
	for slot, id := range characterIDs {
		body = append(body, byte(id), byte(id>>8), 100, 0, byte(slot))
	}
	return body
}

// newPartyRosterTestSession is a session with the roster observer installed and
// no party behind it.
func newPartyRosterTestSession(t *testing.T) *Session {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	ctx, cancel := context.WithCancel(context.Background())
	session.startPartyRoster(ctx)
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session
}

func TestSessionPartyRosterTracksMembershipChanges(t *testing.T) {
	session := newPartyRosterTestSession(t)
	if members, known := session.PartyMembers(); known {
		t.Fatalf("a fresh session already knows a roster: %v", members)
	}
	if _, known := session.PartySince(); known {
		t.Fatal("a fresh session already dates a party")
	}

	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: partyRosterBody(700, 800),
	})
	members, known := session.PartyMembers()
	if !known || len(members) != 2 || members[0] != 700 || members[1] != 800 {
		t.Fatalf("roster = %v known=%t want the two members in slot order", members, known)
	}
	if !session.PartyActive() {
		t.Fatal("a two-member roster was not active")
	}
	formed, ok := session.PartySince()
	if !ok {
		t.Fatal("a formed party has no date")
	}

	// A member leaves, so the party is a different size than it was. That is the
	// change the fleet waits on, and it re-dates the party: whoever is left has a
	// fresh window to get somewhere before the exit duty treats the party as
	// abandoned.
	time.Sleep(2 * time.Millisecond)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: partyRosterBody(700),
	})
	if members, _ := session.PartyMembers(); len(members) != 1 {
		t.Fatalf("roster after a leave = %v want the survivor alone", members)
	}
	if session.PartyActive() {
		t.Fatal("a one-member roster counted as an active party")
	}
	if again, _ := session.PartySince(); !again.After(formed) {
		t.Fatalf("the date did not follow a change of size: %v then %v", formed, again)
	}
}

func TestSessionPartyRosterDatesThePartyItJoins(t *testing.T) {
	session := newPartyRosterTestSession(t)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: partyRosterBody(700),
	})
	alone, _ := session.PartySince()
	time.Sleep(2 * time.Millisecond)

	// The invite is accepted and the party grows. This is a new membership, so
	// the exit duty has a fresh formation window rather than inheriting one that
	// may already have expired.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: partyRosterBody(700, 800),
	})
	joined, _ := session.PartySince()
	if !joined.After(alone) {
		t.Fatalf("joining a party kept the old date: %v then %v", alone, joined)
	}
}

func TestSessionPartyRosterReadsAnEmptyRosterAsNoParty(t *testing.T) {
	session := newPartyRosterTestSession(t)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: partyRosterBody(700, 800),
	})
	// The server's disband and leave answers both arrive as an empty roster,
	// which is a real answer and not a missing one.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: []byte{0},
	})
	members, known := session.PartyMembers()
	if !known || len(members) != 0 {
		t.Fatalf("roster after an empty push = %v known=%t", members, known)
	}
	if session.PartyActive() {
		t.Fatal("an emptied roster counted as an active party")
	}
}

func TestSessionPartyRosterIgnoresAShortBody(t *testing.T) {
	session := newPartyRosterTestSession(t)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: partyRosterBody(700, 800),
	})
	// A body whose count disagrees with its own length is not a roster, so the
	// membership the session already has must survive it untouched.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiPartyMemberRealtime,
		Body: []byte{2, 0xbc, 0x02},
	})
	if members, _ := session.PartyMembers(); len(members) != 2 {
		t.Fatalf("a malformed roster overwrote the membership: %v", members)
	}
	// A different notice on the same class is not a roster either.
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiRequestPeer, Body: []byte{0}})
	if members, _ := session.PartyMembers(); len(members) != 2 {
		t.Fatalf("an unrelated notice overwrote the membership: %v", members)
	}
}

func TestSessionLeavePartySendsTheEmptyClassOneFrame(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	defer clientConn.Close()
	defer serverConn.Close()

	// The write blocks until the far side reads, so the leave runs while this
	// test does the reading.
	left := make(chan error, 1)
	go func() { left <- session.LeaveParty(context.Background()) }()
	class, opcode, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
	if !ok {
		t.Fatal("no leave frame was written")
	}
	if err := <-left; err != nil {
		t.Fatalf("leave party: %v", err)
	}
	// Class one is where the server's own party handler lives, and the leave
	// takes no arguments at all -- the sender is the member.
	if class != 1 || opcode != protocol.CmdLeaveParty {
		t.Fatalf("leave frame class=%d opcode=%d want class 1 op %d", class, opcode, protocol.CmdLeaveParty)
	}
	if len(body) != 0 {
		t.Fatalf("leave frame body = % X want none", body)
	}
}

func TestSessionLeavePartyNeedsAnOpenSession(t *testing.T) {
	session := &Session{selfCharacterID: 42}
	if err := session.LeaveParty(context.Background()); err == nil {
		t.Fatal("a session with no client left a party")
	}
}
