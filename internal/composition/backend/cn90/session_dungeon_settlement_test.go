package cn90

import (
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// dungeonTestRun activates the session's run the way a follower learns about
// it: the server pushes the run projections after the character is inside.
// The room projection draws the follower's op37 ack, which is consumed here so
// the test's next frame is the one the chain under test writes.
func dungeonTestRun(t *testing.T, session *Session, conn net.Conn) {
	t.Helper()
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiDungeonInfo})
	dispatchFollowedStartMap(t, session, conn, []byte{0, 1, 0, 0})
}

func dungeonTestSettlementEntry(session *Session) {
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiDungeonSettlementEntry, Body: []byte{1}})
}

func dungeonTestClearReward(session *Session) {
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiDungeonClearReward, Body: make([]byte, 274)})
}

// TestSessionSettlementChainAnswersEveryGate covers the whole result-page
// chain on a session that never called ClearDungeon: the follower shape. The
// server drops op42 silently while any of op46, op123, op69 and op70 is
// missing, so the test asserts each request in order and that the chain stops
// once the card layout arrives.
func TestSessionSettlementChainAnswersEveryGate(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	dungeonTestRun(t, session, serverConn)
	dungeonTestSettlementEntry(session)

	cls, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
	if !ok {
		t.Fatalf("no op46 play result was written after the settlement entry")
	}
	if cls != protocol.ClassCommand || typ != protocol.CmdSetPlayResult {
		t.Fatalf("first request = class %d op %d want class %d op %d",
			cls, typ, protocol.ClassCommand, protocol.CmdSetPlayResult)
	}
	if want := protocol.SetPlayResultBody(); len(body) != len(want) {
		t.Fatalf("op46 body len = %d want %d", len(body), len(want))
	}

	dungeonTestClearReward(session)
	cls, typ, body, ok = readTestLegacyFrame(serverConn, 5*time.Second)
	if !ok {
		t.Fatalf("no op123 statistic was written after the clear reward")
	}
	if cls != protocol.ClassCommand || typ != protocol.CmdCharacterStatistic {
		t.Fatalf("second request = class %d op %d want class %d op %d",
			cls, typ, protocol.ClassCommand, protocol.CmdCharacterStatistic)
	}
	if want := protocol.CharacterStatisticBody(); len(body) != len(want) {
		t.Fatalf("op123 body len = %d want %d", len(body), len(want))
	}

	// The score-scroll request is paced by the worker's tick, not by an
	// inbound packet.
	cls, typ, body, ok = readTestLegacyFrame(serverConn, 5*time.Second)
	if !ok {
		t.Fatalf("no op69 score-scroll request was written")
	}
	if cls != protocol.ClassCommand || typ != protocol.CmdScoreScrollState {
		t.Fatalf("third request = class %d op %d want class %d op %d",
			cls, typ, protocol.ClassCommand, protocol.CmdScoreScrollState)
	}
	if len(body) != 0 {
		t.Fatalf("op69 body = % X want it empty", body)
	}

	session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdScoreScrollState, Body: []byte{1}})
	cls, typ, body, ok = readTestLegacyFrame(serverConn, 5*time.Second)
	if !ok {
		t.Fatalf("no op70 card-layout request was written after the op69 answer")
	}
	if cls != protocol.ClassCommand || typ != protocol.CmdCardSelectRightState {
		t.Fatalf("fourth request = class %d op %d want class %d op %d",
			cls, typ, protocol.ClassCommand, protocol.CmdCardSelectRightState)
	}
	if len(body) != 0 {
		t.Fatalf("op70 body = % X want it empty", body)
	}

	session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdCardSelectRightState, Body: make([]byte, 16)})
	if _, _, _, ok := readTestLegacyFrame(serverConn, 500*time.Millisecond); ok {
		t.Fatalf("the chain wrote again after the card layout arrived")
	}
}

// TestSessionSettlementChainSkipsThePlayResultItAlreadySent keeps the leader's
// ClearDungeon path from re-sending an op46 the clear already wrote: the
// marker it sets is what the worker reads.
func TestSessionSettlementChainSkipsThePlayResultItAlreadySent(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	dungeonTestRun(t, session, serverConn)
	session.dungeon.markPlayResultSent(time.Now())
	dungeonTestSettlementEntry(session)

	// The marker is recent, so the worker must not resend op46 inside the
	// resend window.
	if _, typ, _, ok := readTestLegacyFrame(serverConn, 500*time.Millisecond); ok {
		t.Fatalf("the worker wrote op %d despite the play result already being sent", typ)
	}
	dungeonTestClearReward(session)
	_, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second)
	if !ok {
		t.Fatalf("no request was written after the clear reward")
	}
	if typ != protocol.CmdCharacterStatistic {
		t.Fatalf("first request op = %d want op %d (the play result was already sent)",
			typ, protocol.CmdCharacterStatistic)
	}
}

// TestSessionSettlementChainResetsWithTheRun makes sure a second run pays the
// chain again instead of treating the first run's answers as the new run's.
func TestSessionSettlementChainResetsWithTheRun(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	dungeonTestRun(t, session, serverConn)
	dungeonTestSettlementEntry(session)
	if _, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok || typ != protocol.CmdSetPlayResult {
		t.Fatalf("first run did not start the chain: op=%d ok=%t", typ, ok)
	}
	// The town transition ends the run.
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiSceneTransition, Body: []byte{1}})
	if session.InDungeon() {
		t.Fatalf("the run stayed active after the scene transition")
	}
	if handshake := session.dungeon.settlementHandshake(); handshake.playResultSent {
		t.Fatalf("the play result marker survived the run: %+v", handshake)
	}

	dungeonTestRun(t, session, serverConn)
	dungeonTestSettlementEntry(session)
	if _, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok || typ != protocol.CmdSetPlayResult {
		t.Fatalf("second run did not restart the chain: op=%d ok=%t", typ, ok)
	}
}
