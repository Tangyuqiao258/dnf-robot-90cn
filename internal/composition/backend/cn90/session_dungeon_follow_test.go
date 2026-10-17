package cn90

import (
	"encoding/binary"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// The follower duty answers a room projection the server pushed into a session
// that never opened a run itself. The ordinary monsters are reported dead; the
// boss row is left to the run's leader, and the follower never asks for a room
// move of its own.
func TestSessionDungeonFollowDutyKillsANewRoomButNotTheBoss(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	dispatchDungeonStartMap(session, dungeonTestStartMapBody(0, 0, 100, []protocol.DungeonStartMapActor{
		dungeonTestMonster(5001),
		dungeonTestBoss(6001),
		dungeonTestMonster(5002),
	}))
	if active, followed := session.dungeon.followState(); !active || !followed {
		t.Fatalf("room projection state = (active %v, followed %v) want both set", active, followed)
	}
	for _, key := range []uint16{5001, 5002} {
		body := wantDungeonFrame(t, serverConn, protocol.ClassCommand, protocol.CmdDieMonster, "follow death report")
		if target := binary.LittleEndian.Uint32(body[0:4]); target != uint32(key) {
			t.Fatalf("op39 target = %d want %d", target, key)
		}
		if owner := binary.LittleEndian.Uint16(body[4:6]); owner != 42 {
			t.Fatalf("op39 owner = %d want the acting character", owner)
		}
	}
	// Nothing else may follow: the boss is the leader's to close, and a room
	// move is the leader's to ask for.
	if _, _, _, ok := readTestLegacyFrame(serverConn, 400*time.Millisecond); ok {
		t.Fatal("the follower duty wrote a frame past the ordinary monsters")
	}
}

// A session that opened the run itself walks it with ClearDungeon; the follower
// duty must stay out of that walk even though the same op29 arrives.
func TestSessionDungeonFollowDutyLeavesTheLeadersRunToTheLeader(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdSelectDungeon, Body: []byte{1, 0, 0},
	})
	dispatchDungeonStartMap(session, dungeonTestStartMapBody(0, 0, 100, []protocol.DungeonStartMapActor{
		dungeonTestMonster(5001),
	}))
	if _, followed := session.dungeon.followState(); followed {
		t.Fatal("the session that committed the run was marked as a follower")
	}
	if _, _, _, ok := readTestLegacyFrame(serverConn, 400*time.Millisecond); ok {
		t.Fatal("the follower duty reported a death in the leader's own run")
	}
}

// Every op29 bumps the room sequence, and a layered room can be projected twice
// under one coordinate. The duty reports each object key once per run and picks
// the new room's keys up on the next projection.
func TestSessionDungeonFollowDutyReportsEachRoomOnce(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	room := dungeonTestStartMapBody(0, 0, 100, []protocol.DungeonStartMapActor{
		dungeonTestMonster(5001),
	})
	dispatchDungeonStartMap(session, room)
	body := wantDungeonFrame(t, serverConn, protocol.ClassCommand, protocol.CmdDieMonster, "first room death report")
	if target := binary.LittleEndian.Uint32(body[0:4]); target != 5001 {
		t.Fatalf("op39 target = %d want 5001", target)
	}
	dispatchDungeonStartMap(session, room)
	if _, _, _, ok := readTestLegacyFrame(serverConn, 300*time.Millisecond); ok {
		t.Fatal("a repeated projection reported the same monster dead twice")
	}
	dispatchDungeonStartMap(session, dungeonTestStartMapBody(1, 0, 101, []protocol.DungeonStartMapActor{
		dungeonTestMonster(6001),
	}))
	body = wantDungeonFrame(t, serverConn, protocol.ClassCommand, protocol.CmdDieMonster, "next room death report")
	if target := binary.LittleEndian.Uint32(body[0:4]); target != 6001 {
		t.Fatalf("op39 target = %d want 6001", target)
	}
}

// A room projection can still be queued when the run ends. Once the town
// transition has cleared the run it must not be turned into death reports in
// town: op39 is dropped there, but a follower writing after the run is over is
// still a fight with the server's own scene.
func TestSessionDungeonFollowDutyStopsWhenTheRunEnds(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	session.dungeon.markFollowed()
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiSceneTransition, Body: []byte{1, 2, 3},
	})
	session.dungeon.markRoom(protocol.DungeonStartMap{
		X: 0, Y: 0, MapID: 100,
		Actors: []protocol.DungeonStartMapActor{dungeonTestMonster(5001)},
	})
	if _, _, _, ok := readTestLegacyFrame(serverConn, 400*time.Millisecond); ok {
		t.Fatal("a room projection after the run ended was reported dead")
	}
	if session.InDungeon() {
		t.Fatal("the town transition left the run marked active")
	}
}
