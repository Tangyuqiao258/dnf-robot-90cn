package cn90

import (
	"context"
	"encoding/binary"
	"net"
	"testing"

	protocol "robot/internal/protocol/cn90"
)

// newPartyFollowTestSession builds one session that tracks its party leader.
// The client is only there so startPartyFollow accepts the session: the tracker
// never writes to the socket, so nothing is ever read from the peer end.
func newPartyFollowTestSession(t *testing.T) *Session {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	ctx, cancel := context.WithCancel(context.Background())
	session.startPartyFollow(ctx)
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session
}

// partyInvitePromptBody builds the forwarded class0/0x0007 head.
func partyInvitePromptBody(inviter uint16, mode byte) []byte {
	body := make([]byte, 7)
	binary.LittleEndian.PutUint16(body[:2], inviter)
	body[2] = mode
	return body
}

func peerAckBody(ok bool) []byte {
	if ok {
		return []byte{1, 0}
	}
	return []byte{0, 9}
}

func testUserPositionBody(actor, x, y uint16) []byte {
	body := make([]byte, 9)
	binary.LittleEndian.PutUint16(body[0:2], actor)
	binary.LittleEndian.PutUint16(body[2:4], x)
	binary.LittleEndian.PutUint16(body[4:6], y)
	body[6] = 5
	return body
}

func testUserAreaBody(actor uint16, town, area byte, x, y uint16) []byte {
	body := make([]byte, 10)
	binary.LittleEndian.PutUint16(body[0:2], actor)
	body[2] = town
	body[3] = area
	binary.LittleEndian.PutUint16(body[4:6], x)
	binary.LittleEndian.PutUint16(body[6:8], y)
	return body
}

func dispatchInvitePrompt(session *Session, inviter uint16, mode byte) {
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiRequestPeer, Body: partyInvitePromptBody(inviter, mode)})
}

func dispatchPeerAck(session *Session, ok bool) {
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: peerAckBody(ok)})
}

func dispatchPosition(session *Session, actor, x, y uint16) {
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiUserPosition, Body: testUserPositionBody(actor, x, y)})
}

// acceptLeader drives the prompt and acknowledgement pair that names a leader.
func acceptLeader(session *Session, inviter uint16) {
	dispatchInvitePrompt(session, inviter, 13)
	dispatchPeerAck(session, true)
}

func TestSessionPartyFollowTracksAcceptedLeaderPosition(t *testing.T) {
	session := newPartyFollowTestSession(t)
	const leader = 1001

	if _, ok := session.PartyFollowTarget(); ok {
		t.Fatal("session reported a follow target before any invite")
	}
	acceptLeader(session, leader)

	// The leader is known, but the server has not broadcast a position yet, so
	// there is still nowhere for the follower to walk.
	if _, ok := session.PartyFollowTarget(); ok {
		t.Fatal("session reported a target before the leader moved")
	}

	dispatchPosition(session, leader, 520, 310)
	target, ok := session.PartyFollowTarget()
	if !ok {
		t.Fatal("session lost the leader position")
	}
	if target.CharacterID != leader || target.X != 520 || target.Y != 310 {
		t.Fatalf("target = %+v want cid=%d x=520 y=310", target, leader)
	}
	if target.ObservedAt.IsZero() {
		t.Fatal("target has no observation time")
	}

	dispatchPosition(session, leader, 640, 355)
	if target, _ = session.PartyFollowTarget(); target.X != 640 || target.Y != 355 {
		t.Fatalf("target did not advance: %+v", target)
	}
}

func TestSessionPartyFollowIgnoresUnrelatedActors(t *testing.T) {
	session := newPartyFollowTestSession(t)
	const leader = 1001
	acceptLeader(session, leader)
	dispatchPosition(session, leader, 520, 310)

	// A stranger walking through the same area must not move the follower.
	dispatchPosition(session, 2002, 900, 900)
	target, ok := session.PartyFollowTarget()
	if !ok {
		t.Fatal("unrelated actor dropped the target")
	}
	if target.X != 520 || target.Y != 310 {
		t.Fatalf("unrelated actor moved the target: %+v", target)
	}
}

func TestSessionPartyFollowIgnoresUnrelatedAreaNotice(t *testing.T) {
	session := newPartyFollowTestSession(t)
	const leader = 1001
	acceptLeader(session, leader)
	dispatchPosition(session, leader, 520, 310)

	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiUserArea, Body: testUserAreaBody(2002, 7, 3, 10, 20)})
	target, _ := session.PartyFollowTarget()
	if target.Village != 0 || target.Area != 0 {
		t.Fatalf("unrelated area notice retargeted the follower: %+v", target)
	}
}

func TestSessionPartyFollowAreaNoticeMovesTheLeaderAcrossAreas(t *testing.T) {
	session := newPartyFollowTestSession(t)
	const leader = 1001
	acceptLeader(session, leader)
	dispatchPosition(session, leader, 520, 310)

	// 0x0017 is the only notice a follower in another area ever receives.
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiUserArea, Body: testUserAreaBody(leader, 3, 4, 700, 200)})
	target, ok := session.PartyFollowTarget()
	if !ok {
		t.Fatal("area notice dropped the target")
	}
	if target.Village != 3 || target.Area != 4 || target.X != 700 || target.Y != 200 {
		t.Fatalf("target = %+v want village=3 area=4 x=700 y=200", target)
	}
}

// TestSessionPartyFollowLearnsTheAreaFromTheLeadersOwnMove covers the area a
// position notice implies without carrying it. A leader that never crosses an
// area boundary never triggers the 0x0017 that names a town and area, so before
// this the follower held no area at all, the walk was refused on the geometry
// guard and every party formed between two robots ended with the follower
// standing still. The broadcast only reaches players registered in the mover's
// area, so the notice is itself proof the two share the session's own area.
func TestSessionPartyFollowLearnsTheAreaFromTheLeadersOwnMove(t *testing.T) {
	session := newPartyFollowTestSession(t)
	const leader = 1001
	acceptLeader(session, leader)
	session.recordTownAreaObservation(townAreaObservation{village: 39, area: 1, valid: true})

	dispatchPosition(session, leader, 520, 310)
	target, ok := session.PartyFollowTarget()
	if !ok {
		t.Fatal("session lost the leader position")
	}
	if target.Village != 39 || target.Area != 1 {
		t.Fatalf("target = %+v want village=39 area=1", target)
	}
}

// TestSessionPartyFollowWithoutAConfirmedAreaKeepsNone is the other half: a
// session the server has never answered an area transition for - the login room
// before the first escape - has nothing to stamp and must not invent a map.
func TestSessionPartyFollowWithoutAConfirmedAreaKeepsNone(t *testing.T) {
	session := newPartyFollowTestSession(t)
	const leader = 1001
	acceptLeader(session, leader)
	dispatchPosition(session, leader, 520, 310)

	target, ok := session.PartyFollowTarget()
	if !ok {
		t.Fatal("session lost the leader position")
	}
	if target.Village != 0 || target.Area != 0 {
		t.Fatalf("an unconfirmed session named an area: %+v", target)
	}
}

// TestSessionPartyFollowCoLocationOutranksAStaleAreaNotice pins the precedence:
// a 0x0017 names where the leader went while the follower stayed behind, so the
// next position notice from that leader - which can only be delivered to an
// area they now share again - is the newer fact and replaces it.
func TestSessionPartyFollowCoLocationOutranksAStaleAreaNotice(t *testing.T) {
	session := newPartyFollowTestSession(t)
	const leader = 1001
	acceptLeader(session, leader)
	session.recordTownAreaObservation(townAreaObservation{village: 39, area: 1, valid: true})

	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiUserArea, Body: testUserAreaBody(leader, 40, 2, 700, 200)})
	if target, _ := session.PartyFollowTarget(); target.Village != 40 || target.Area != 2 {
		t.Fatalf("area notice did not move the leader: %+v", target)
	}
	dispatchPosition(session, leader, 520, 310)
	target, _ := session.PartyFollowTarget()
	if target.Village != 39 || target.Area != 1 {
		t.Fatalf("target = %+v want village=39 area=1", target)
	}
}

func TestSessionPartyFollowTradePromptIsNotACandidate(t *testing.T) {
	session := newPartyFollowTestSession(t)
	// Mode 1 is the trade prompt and is never answered, so a following session
	// must not treat its sender as a leader even if an op11 ack arrives.
	dispatchInvitePrompt(session, 1001, 1)
	dispatchPeerAck(session, true)
	if _, ok := session.PartyFollowTarget(); ok {
		t.Fatal("trade prompt became a follow leader")
	}
}

// TestSessionPartyFollowTradeAckIsNotAPartyAnswer covers the other direction of
// the shared opcode: a trade accept succeeds on op11 exactly like a party
// answer, so a session that set a candidate from an unanswered invite must not
// promote it when the trade module answers something else entirely.
func TestSessionPartyFollowTradeAckIsNotAPartyAnswer(t *testing.T) {
	session := newPartyFollowTestSession(t)
	dispatchInvitePrompt(session, 1001, 13)
	// The five byte shape the server only sends for a mode 1 answer.
	ack := []byte{1}
	ack = binary.LittleEndian.AppendUint16(ack, 2002)
	ack = append(ack, protocol.TradeRequestMode, 0)
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdResponsePeer, Body: ack})
	// The harm is not the promotion itself but the walk it enables: the server
	// broadcasts every town mover, so a promoted non-leader becomes a target on
	// the very next position notice it sends.
	dispatchPosition(session, 1001, 520, 310)
	if target, ok := session.PartyFollowTarget(); ok {
		t.Fatalf("a trade acknowledgement made %d a follow target: %+v", target.CharacterID, target)
	}
}

func TestSessionPartyFollowRejectedAnswerNamesNoLeader(t *testing.T) {
	session := newPartyFollowTestSession(t)
	dispatchInvitePrompt(session, 1001, 13)
	dispatchPeerAck(session, false)
	dispatchPosition(session, 1001, 520, 310)
	if _, ok := session.PartyFollowTarget(); ok {
		t.Fatal("rejected answer still named a leader")
	}
}

func TestSessionPartyFollowNewLeaderDropsTheOldPosition(t *testing.T) {
	session := newPartyFollowTestSession(t)
	acceptLeader(session, 1001)
	dispatchPosition(session, 1001, 520, 310)

	// Accepting a second party makes the new inviter the leader; the follower
	// must not keep walking to where the previous leader stood.
	acceptLeader(session, 3003)
	if _, ok := session.PartyFollowTarget(); ok {
		t.Fatal("previous leader position survived the leader change")
	}
	dispatchPosition(session, 3003, 77, 88)
	target, ok := session.PartyFollowTarget()
	if !ok || target.CharacterID != 3003 || target.X != 77 || target.Y != 88 {
		t.Fatalf("target = %+v ok=%t want cid=3003 x=77 y=88", target, ok)
	}
}

func TestSessionWithoutFollowKeepsNoTarget(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	// A session opened without EnablePartyFollow has no tracker at all, so the
	// same packets leave it reporting nothing.
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	acceptLeader(session, 1001)
	dispatchPosition(session, 1001, 520, 310)
	if _, ok := session.PartyFollowTarget(); ok {
		t.Fatal("session without follow reported a target")
	}
}
