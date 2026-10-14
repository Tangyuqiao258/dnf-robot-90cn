package cn90

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// newDungeonTestSession opens a session whose run and death state is tracked
// the way a live one is, but without the revive worker: the tests that drive
// Revive by hand have to own the socket themselves.
func newDungeonTestSession(t *testing.T) (*Session, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	ctx, cancel := context.WithCancel(context.Background())
	session.startDungeonTracking(ctx)
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session, serverConn
}

// dungeonTestDeathNotice builds the class0/op32 body the server sends to the
// acting session: `u16 object key, u8 state, u8 flag`.
func dungeonTestDeathNotice(objectKey uint16, revived bool) []byte {
	body := make([]byte, 4)
	binary.LittleEndian.PutUint16(body[0:2], objectKey)
	if revived {
		body[2] = 1
	}
	return body
}

func TestSessionEnterDungeonWalksTheSelectorThenLoadsTheScene(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	go func() {
		cls, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no op15 selector request was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdEnterSelectDungeon {
			t.Errorf("selector frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdEnterSelectDungeon)
			return
		}
		if len(body) != 0 {
			t.Errorf("op15 body = % X want it empty", body)
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdEnterSelectDungeon, Body: []byte{1, 0},
		})

		cls, typ, body, ok = readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no op16 entry request was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdSelectDungeon {
			t.Errorf("entry frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdSelectDungeon)
			return
		}
		// The selector acknowledgement is not a licence to send anything: the
		// entry request still has to be the exact 21-byte shape.
		if want := protocol.SelectDungeonBody(0x0001abcd, 2); !bytes.Equal(body, want) {
			t.Errorf("op16 body = % X want % X", body, want)
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdSelectDungeon, Body: []byte{1, 0, 0},
		})

		// An active run is not yet a loaded scene: the server holds the
		// character-state push until the loading acknowledgement arrives.
		cls, typ, body, ok = readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no op37 loading acknowledgement was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdFinishLoading {
			t.Errorf("loading frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdFinishLoading)
			return
		}
		// The server gates op37 on the body length alone, so the exact
		// eight-byte shape is the whole contract.
		if want := protocol.FinishLoadingBody(); !bytes.Equal(body, want) {
			t.Errorf("op37 body = % X want % X", body, want)
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiFinishLoading, Body: make([]byte, 87),
		})
	}()
	if err := session.EnterDungeon(context.Background(), 0x0001abcd, 2); err != nil {
		t.Fatalf("dungeon entry failed: %v", err)
	}
	if !session.InDungeon() {
		t.Fatal("a successful entry did not mark the run active")
	}
	if !session.DungeonLoaded() {
		t.Fatal("a completed entry did not mark the scene loaded")
	}
	if dungeonID, difficulty, active := session.DungeonIdentity(); !active || dungeonID != 0x0001abcd || difficulty != 2 {
		t.Fatalf("dungeon identity = (%d, %d, %v) want (0x0001abcd, 2, true)", dungeonID, difficulty, active)
	}
}

func TestSessionLeaveDungeonGivesUpAndWaitsForTheTownTransition(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	session.dungeon.markActive(5, 0)
	session.dungeon.markLoaded()
	go func() {
		cls, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no op42 give-up request was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdGiveupGame {
			t.Errorf("give-up frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdGiveupGame)
			return
		}
		// The server reads no field and answers with the scene transition alone,
		// so the empty body is the whole request.
		if len(body) != 0 {
			t.Errorf("op42 body = % X want it empty", body)
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiSceneTransition, Body: []byte{1, 2, 3},
		})
	}()
	if err := session.LeaveDungeon(context.Background()); err != nil {
		t.Fatalf("dungeon exit failed: %v", err)
	}
	if session.InDungeon() {
		t.Fatal("a completed exit left the run marked active")
	}
	if session.DungeonLoaded() {
		t.Fatal("a completed exit left the scene marked loaded")
	}
}

func TestSessionLeaveDungeonRefusesWithoutAnActiveRun(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	// The server drops op42 without any reply while no run is active, so sending
	// it in town would report a timeout for a request that was never going to be
	// answered. The refusal has to happen before the write.
	if err := session.LeaveDungeon(context.Background()); err == nil {
		t.Fatal("an exit outside a run was accepted")
	}
	if _, _, _, ok := readTestLegacyFrame(serverConn, 300*time.Millisecond); ok {
		t.Fatal("an exit outside a run wrote a frame")
	}
}

func TestSessionEnterDungeonReportsASceneThatNeverLoads(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdEnterSelectDungeon, Body: []byte{1, 0},
		})
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdSelectDungeon, Body: []byte{1, 0, 0},
		})
		// The acknowledgement is accepted and then the scene never comes up.
		_, _, _, _ = readTestLegacyFrame(serverConn, 5*time.Second)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := session.EnterDungeon(ctx, 0x0001abcd, 0); err == nil {
		t.Fatal("an entry whose scene never loaded returned no error")
	}
	// op16 commits the run before the scene is released, so the run stays active
	// even though the entry reported a failure. A caller that took the error as
	// "nothing happened" would leave the character inside a run.
	if !session.InDungeon() {
		t.Fatal("the committed run was cleared by the loading failure")
	}
	if session.DungeonLoaded() {
		t.Fatal("a scene that never loaded was marked loaded")
	}
}

func TestSessionEnterDungeonReportsARejectedEntry(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdEnterSelectDungeon, Body: []byte{1, 0},
		})
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		// 0x0e is the level check: the entry itself was well formed.
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdSelectDungeon, Body: []byte{0, 0x0e, 0},
		})
	}()
	err := session.EnterDungeon(context.Background(), 1, 0)
	if err == nil {
		t.Fatal("a rejected entry returned no error")
	}
	if session.InDungeon() {
		t.Fatal("a rejected entry marked the run active")
	}
}

func TestSessionEnterDungeonTimesOutWhenTheSelectorIsDropped(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	// The server drops an op15 it will not honour without any reply, so a
	// timeout is the only thing that distinguishes it from a slow answer.
	go func() {
		_, _, _, _ = readTestLegacyFrame(serverConn, 5*time.Second)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := session.EnterDungeon(ctx, 1, 0); err == nil {
		t.Fatal("a dropped selector returned no error")
	}
	if session.InDungeon() {
		t.Fatal("a dropped selector marked the run active")
	}
}

func TestSessionEnterDungeonRefusesAnEmptyDungeonID(t *testing.T) {
	session, _ := newDungeonTestSession(t)
	if err := session.EnterDungeon(context.Background(), 0, 0); err == nil {
		t.Fatal("dungeon id 0 was accepted")
	}
}

func TestSessionDungeonTracksDeathAndReviveForItsOwnKeyOnly(t *testing.T) {
	session, _ := newDungeonTestSession(t)
	session.observeDungeon(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdSelectDungeon, Body: []byte{1, 0, 0},
	})
	if !session.InDungeon() {
		t.Fatal("the entry acknowledgement did not mark the run active")
	}
	// The notice is only ever sent to the acting session, so a mismatched key
	// means the packet is not about this character.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
		Body: dungeonTestDeathNotice(7, false),
	})
	if session.DungeonDead() {
		t.Fatal("another character's death notice marked this session dead")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
		Body: dungeonTestDeathNotice(42, false),
	})
	if !session.DungeonDead() {
		t.Fatal("this character's death notice was not recorded")
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
		Body: dungeonTestDeathNotice(42, true),
	})
	if session.DungeonDead() {
		t.Fatal("the revive notice did not clear the death state")
	}
}

func TestSessionDungeonIgnoresMalformedDeathNotices(t *testing.T) {
	session, _ := newDungeonTestSession(t)
	session.dungeon.markActive(5, 0)
	for _, body := range [][]byte{nil, {42, 0}, {42, 0, 9, 0}} {
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState, Body: body,
		})
	}
	if session.DungeonDead() {
		t.Fatal("a malformed death notice was recorded as a death")
	}
}

func TestSessionDungeonTownTransitionEndsTheRun(t *testing.T) {
	session, _ := newDungeonTestSession(t)
	session.dungeon.markActive(5, 0)
	session.dungeon.markDeath(false)
	// The server sends class0/op24 both when the ten second death timer runs
	// out and when a run is left normally: either way the character is back in
	// town and op40/op41 are meaningless again.
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiSceneTransition, Body: []byte{1, 2, 3},
	})
	if session.InDungeon() {
		t.Fatal("the town transition left the run marked active")
	}
	if session.DungeonDead() {
		t.Fatal("the town transition left the death state set")
	}
}

func TestSessionDeclareDeathSendsThePositionAndWaitsForTheNotice(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	session.dungeon.markActive(5, 0)
	go func() {
		cls, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no op40 death report was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdDieCharacter {
			t.Errorf("death frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdDieCharacter)
			return
		}
		if want := protocol.DieCharacterBody(-300, 1024); !bytes.Equal(body, want) {
			t.Errorf("op40 body = % X want % X", body, want)
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
			Body: dungeonTestDeathNotice(42, false),
		})
	}()
	if err := session.DeclareDeath(context.Background(), -300, 1024); err != nil {
		t.Fatalf("death report failed: %v", err)
	}
	if !session.DungeonDead() {
		t.Fatal("the death notice did not mark the session dead")
	}
}

func TestSessionDeclareDeathTimesOutWithoutANotice(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	go func() {
		_, _, _, _ = readTestLegacyFrame(serverConn, 5*time.Second)
	}()
	// Outside a run the server drops op40 without any reply at all, which is
	// exactly the case this timeout has to surface.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := session.DeclareDeath(ctx, 0, 0); err == nil {
		t.Fatal("a dropped death report returned no error")
	}
}

func TestSessionReviveSendsTheOwnObjectKeyAndWaitsForBothReplies(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	session.dungeon.markActive(5, 0)
	session.dungeon.markDeath(false)
	go func() {
		cls, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			t.Errorf("no op41 revive request was written")
			return
		}
		if cls != protocol.ClassCommand || typ != protocol.CmdUseCoin {
			t.Errorf("revive frame = class %d op %d want class %d op %d",
				cls, typ, protocol.ClassCommand, protocol.CmdUseCoin)
			return
		}
		if want := protocol.UseCoinBody(42); !bytes.Equal(body, want) {
			t.Errorf("op41 body = % X want % X", body, want)
			return
		}
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdUseCoin, Body: []byte{1, 0},
		})
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
			Body: dungeonTestDeathNotice(42, true),
		})
	}()
	if err := session.Revive(context.Background()); err != nil {
		t.Fatalf("revive failed: %v", err)
	}
	if session.DungeonDead() {
		t.Fatal("a successful revive left the session dead")
	}
}

func TestSessionReviveReportsARejectedRequest(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	session.dungeon.markActive(5, 0)
	session.dungeon.markDeath(false)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		// The server answers `{0, 1}` when the death timer has already expired.
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassCommand, Type: protocol.CmdUseCoin, Body: []byte{0, 1},
		})
	}()
	if err := session.Revive(context.Background()); err == nil {
		t.Fatal("a rejected revive returned no error")
	}
}

func TestSessionReviveNeedsASelectedCharacter(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	// The object key has to be the acting character's own, so a session that
	// has not selected one has nothing to send.
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), dungeon: &dungeonState{}}
	if err := session.Revive(context.Background()); err == nil {
		t.Fatal("a revive without a character id was accepted")
	}
	if _, _, _, ok := readTestLegacyFrame(serverConn, 300*time.Millisecond); ok {
		t.Fatal("a revive without a character id wrote a frame")
	}
}

func TestSessionDungeonReviveWorkerAnswersItsOwnDeathNotice(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	session.startDungeonRevive(ctx)
	t.Cleanup(cancel)

	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdSelectDungeon, Body: []byte{1, 0, 0},
	})
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
		Body: dungeonTestDeathNotice(42, false),
	})
	cls, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
	if !ok {
		t.Fatal("the revive worker wrote no op41 request")
	}
	if cls != protocol.ClassCommand || typ != protocol.CmdUseCoin {
		t.Fatalf("worker frame = class %d op %d want class %d op %d",
			cls, typ, protocol.ClassCommand, protocol.CmdUseCoin)
	}
	if want := protocol.UseCoinBody(42); !bytes.Equal(body, want) {
		t.Fatalf("worker op41 body = % X want % X", body, want)
	}
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassCommand, Type: protocol.CmdUseCoin, Body: []byte{1, 0},
	})
	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
		Body: dungeonTestDeathNotice(42, true),
	})
	if session.DungeonDead() {
		t.Fatal("the worker's revive left the session dead")
	}
}

func TestSessionDungeonReviveWorkerIgnoresAnotherCharactersDeath(t *testing.T) {
	session, serverConn := newDungeonTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	session.startDungeonRevive(ctx)
	t.Cleanup(cancel)

	session.dispatchPacket(protocol.Packet{
		Class: protocol.ClassNotice, Type: protocol.NotiDungeonDeathState,
		Body: dungeonTestDeathNotice(7, false),
	})
	if _, _, _, ok := readTestLegacyFrame(serverConn, 300*time.Millisecond); ok {
		t.Fatal("the worker revived on another character's death notice")
	}
}
