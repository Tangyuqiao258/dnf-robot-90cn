package cn90

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// dungeonTestStartMapBody builds one class0/op29 body with a 19-byte head and
// one 21-byte row per actor.
func dungeonTestStartMapBody(x, y byte, mapID uint32, actors []protocol.DungeonStartMapActor) []byte {
	body := make([]byte, 19+21*len(actors))
	body[0] = x
	body[1] = y
	binary.LittleEndian.PutUint32(body[14:18], mapID)
	body[18] = byte(len(actors))
	for index, actor := range actors {
		row := body[19+21*index:]
		binary.LittleEndian.PutUint16(row[6:8], actor.ObjectKey)
		binary.LittleEndian.PutUint32(row[8:12], actor.Code)
		row[12] = actor.Level
		row[13] = actor.Type
		row[20] = actor.Blocking
	}
	return body
}

func dungeonTestMonster(key uint16) protocol.DungeonStartMapActor {
	return protocol.DungeonStartMapActor{ObjectKey: key, Type: 1}
}

func dungeonTestBoss(key uint16) protocol.DungeonStartMapActor {
	return protocol.DungeonStartMapActor{ObjectKey: key, Type: protocol.DungeonActorTypeBoss}
}

func dispatchDungeonStartMap(session *Session, body []byte) {
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonStartMap, Body: body,
	})
}

// startClearingSession marks a session inside a run and gives it its first
// room, so ClearDungeon finds both where the server says it stands and an
// active run.
func startClearingSession(t *testing.T, room []byte) (*Session, net.Conn) {
	t.Helper()
	session, serverConn := newDungeonTestSession(t)
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdSelectDungeon, Body: []byte{1, 0, 0},
	})
	dispatchDungeonStartMap(session, room)
	return session, serverConn
}

// wantDungeonFrame reads one frame and checks its class and opcode.
func wantDungeonFrame(t *testing.T, conn net.Conn, class byte, typ uint16, what string) []byte {
	t.Helper()
	gotClass, gotType, body, ok := readTestLegacyFrame(conn, 5*time.Second)
	if !ok {
		t.Fatalf("no %s request was written", what)
	}
	if gotClass != class || gotType != typ {
		t.Fatalf("%s frame = class %d op %d want class %d op %d", what, gotClass, gotType, class, typ)
	}
	return body
}

// TestSessionClearDungeonWalksToTheBossAndSettles covers the whole run: the
// room's monsters are reported dead, the one reachable neighbour is asked for,
// the boss room is cleared, the boss check opens the settlement and the play
// result answers it.
func TestSessionClearDungeonWalksToTheBossAndSettles(t *testing.T) {
	start := dungeonTestStartMapBody(0, 0, 100, []protocol.DungeonStartMapActor{
		dungeonTestMonster(5001),
		dungeonTestMonster(5002),
	})
	session, conn := startClearingSession(t, start)

	done := make(chan error, 1)
	go func() { done <- session.ClearDungeon(context.Background()) }()

	// The starting room: both monsters reported by the session's own object key.
	for _, key := range []uint16{5001, 5002} {
		body := wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdDieMonster, "death report")
		if len(body) != protocol.DieMonsterTemplateBodySize {
			t.Fatalf("op39 body = %d bytes want the capture shape", len(body))
		}
		if target := binary.LittleEndian.Uint32(body[0:4]); target != uint32(key) {
			t.Fatalf("op39 target = %d want %d", target, key)
		}
		if owner := binary.LittleEndian.Uint16(body[4:6]); owner != 42 {
			t.Fatalf("op39 owner = %d want the acting character", owner)
		}
	}

	// The only neighbour tried first is the open one.
	body := wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdMoveMap, "room move")
	if len(body) != 100 || body[0] != 1 || body[1] != 0 {
		t.Fatalf("op45 body = %d bytes target=%d:%d want the right neighbour", len(body), body[0], body[1])
	}
	dispatchDungeonStartMap(session, dungeonTestStartMapBody(1, 0, 101, []protocol.DungeonStartMapActor{
		dungeonTestBoss(6001),
	}))

	body = wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdDieMonster, "boss death report")
	if target := binary.LittleEndian.Uint32(body[0:4]); target != 6001 {
		t.Fatalf("boss report target = %d want 6001", target)
	}
	body = wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdBossDieCheck, "boss check")
	if len(body) != 39 {
		t.Fatalf("op117 body = %d bytes want 39", len(body))
	}
	if related := binary.LittleEndian.Uint16(body[0:2]); related != 42 {
		t.Fatalf("op117 related actor = %d want the acting character", related)
	}
	if target := binary.LittleEndian.Uint16(body[2:4]); target != 6001 {
		t.Fatalf("op117 target = %d want the boss", target)
	}

	// The settlement entry is the gate the play result is validated against.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonSettlementEntry, Body: []byte{1},
	})
	body = wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdSetPlayResult, "play result")
	if len(body) != 42 {
		t.Fatalf("op46 body = %d bytes want the row-free shape", len(body))
	}

	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiSceneTransition})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clear returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clear did not finish after the town transition")
	}
}

// TestSessionClearDungeonTreatsAnUnansweredMoveAsAWall pins the only way a maze
// is discovered: a move the server refuses is never answered, so the walk has to
// try the next neighbour rather than wait forever.
func TestSessionClearDungeonTreatsAnUnansweredMoveAsAWall(t *testing.T) {
	start := dungeonTestStartMapBody(0, 0, 200, nil)
	session, conn := startClearingSession(t, start)

	done := make(chan error, 1)
	go func() { done <- session.ClearDungeon(context.Background()) }()

	// The first neighbour (1,0) is a wall: it is never answered.
	body := wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdMoveMap, "first room move")
	if body[0] != 1 || body[1] != 0 {
		t.Fatalf("first op45 target = %d:%d want 1:0", body[0], body[1])
	}
	// The second neighbour (0,1) answers with the boss room.
	body = wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdMoveMap, "second room move")
	if body[0] != 0 || body[1] != 1 {
		t.Fatalf("second op45 target = %d:%d want 0:1", body[0], body[1])
	}
	dispatchDungeonStartMap(session, dungeonTestStartMapBody(0, 1, 201, []protocol.DungeonStartMapActor{
		dungeonTestBoss(7001),
	}))

	wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdDieMonster, "boss death report")
	wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdBossDieCheck, "boss check")
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonSettlementEntry, Body: []byte{1},
	})
	wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdSetPlayResult, "play result")
	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiSceneTransition})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clear returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clear did not finish after the town transition")
	}
}

// TestSessionClearDungeonAcceptsTheEntryPushedOnTheBossDeath covers the order
// the live server actually uses: it commits the clear on the boss's own op39
// death report and pushes the op31 settlement entry before the client's op117
// boss check. The entry is therefore already open when the walk reaches the
// settlement, and the boss check is a replay that earns no second entry.
func TestSessionClearDungeonAcceptsTheEntryPushedOnTheBossDeath(t *testing.T) {
	start := dungeonTestStartMapBody(0, 0, 300, []protocol.DungeonStartMapActor{
		dungeonTestBoss(8001),
	})
	session, conn := startClearingSession(t, start)

	done := make(chan error, 1)
	go func() { done <- session.ClearDungeon(context.Background()) }()

	wantDungeonFrame(t, conn, protocol.ClassCommand, protocol.CmdDieMonster, "boss death report")
	// The server commits the clear behind that report, so the entry can land
	// while the walk is still in its settle delay.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonSettlementEntry, Body: []byte{1},
	})

	seen := map[uint16]bool{}
	for !seen[protocol.CmdSetPlayResult] || !seen[protocol.CmdBossDieCheck] {
		_, typ, _, ok := readTestLegacyFrame(conn, 5*time.Second)
		if !ok {
			t.Fatalf("frames after the early settlement entry = %v want op46 and op117", seen)
		}
		seen[typ] = true
	}
	// The worker already sent op46 when the entry landed, so the leader must
	// not send a second one.
	if _, typ, _, ok := readTestLegacyFrame(conn, 500*time.Millisecond); ok {
		t.Fatalf("the leader re-sent op %d after the worker's play result", typ)
	}

	session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiSceneTransition})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clear returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clear did not finish after the town transition")
	}
}

// TestDungeonClearNeighboursKeepsCoordinatesInTheWireRange covers the four
// neighbours at the edge of the byte coordinate space: a coordinate the body
// cannot name is not a neighbour.
func TestDungeonClearNeighboursKeepsCoordinatesInTheWireRange(t *testing.T) {
	rooms := dungeonClearNeighbours(dungeonClearRoom{x: 0, y: 0})
	if len(rooms) != 2 {
		t.Fatalf("neighbours at the origin = %d want 2", len(rooms))
	}
	for _, room := range rooms {
		if room.x > 1 || room.y > 1 {
			t.Fatalf("neighbour %s is out of the walkable range", room)
		}
	}
	// The fixed order is part of the contract: a retry asks the same question
	// first.
	var order []string
	for _, room := range dungeonClearNeighbours(dungeonClearRoom{x: 5, y: 5}) {
		order = append(order, room.String())
	}
	if got := strings.Join(order, ","); got != "6:5,4:5,5:6,5:4" {
		t.Fatalf("neighbour order = %s", got)
	}
}
