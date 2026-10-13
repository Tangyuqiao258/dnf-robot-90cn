package cn90

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// liveEmotionIndex reads the durable emotion_index the emotion handler wrote.
// The op254 acknowledgement echoes the index the handler was given, so only the
// stored column can show that the value actually reached the character.
func liveEmotionIndex(t *testing.T, databasePath, account string) int {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureSQLitePool(db)
	var index int
	if err := db.QueryRow(
		`SELECT emotion_index FROM dnf_characters WHERE account_id=? AND delete_flag=0`, account,
	).Scan(&index); err != nil {
		t.Fatalf("read emotion_index for %s: %v", account, err)
	}
	return index
}

// TestLiveEmote proves the mood/emotion command against the real server: the
// session sends one op254 index, the server persists it on the character and
// answers on the same opcode with a scene-object refresh behind it.
func TestLiveEmote(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live emote")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	adminAddress, adminToken, err := serverLayout.adminEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	channels, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !liveListener(t, channels) {
		t.Skipf("no live 90CN game listener among %v", channels.ports)
	}
	const (
		account = "robot17000007"
		name    = "表情丁"
		uid     = 17000007
	)
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("pre-clean %s: %v", account, err)
	}
	result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: name, Job: 0, RobotUID: uid,
	})
	if err != nil {
		t.Fatalf("provision %s: %v", account, err)
	}
	if !result.Created || result.BackendSlot == nil {
		t.Fatalf("provision result for %s = %+v", account, result)
	}

	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	session, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: *result.BackendSlot,
		InitialTownKnown: true, InitialVillage: 38, InitialArea: 1, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	live, ok := session.(*Session)
	if !ok {
		t.Fatal("unexpected session type")
	}
	closed := false
	defer func() {
		if !closed {
			_ = session.Close()
		}
	}()
	t.Logf("session online: account=%s cid=%d", account, live.selfCharacterID)

	if got := liveEmotionIndex(t, databasePath, account); got != 0 {
		t.Fatalf("fresh character starts at emotion_index=%d want 0", got)
	}

	// After the acknowledgement the server rebuilds the acting session's scene
	// object row on class0/op2 so the mood window re-reads the new index. The
	// session does not wait for it, so the test watches the socket. The
	// acknowledgement itself arrives on the same opcode as the request and is
	// filtered out, otherwise it would satisfy the check on its own.
	refreshes := make(chan byte, 8)
	cleanup := live.setPacketObserver(func(packet protocol.Packet) {
		if packet.Type != protocol.NotiCharacterList || packet.Class == protocol.ClassCommand {
			return
		}
		select {
		case refreshes <- packet.Class:
		default:
		}
	})
	defer cleanup()

	// Two different indexes, each checked against the stored column: the first
	// proves the write, the second proves it tracks rather than sticking on a
	// value the character was created with.
	for _, want := range []uint16{6, 2} {
		stored, err := live.ChangeEmotion(ctx, want)
		if err != nil {
			t.Fatalf("emotion change to %d: %v", want, err)
		}
		if stored != want {
			t.Fatalf("op254 acknowledged %d for a request of %d", stored, want)
		}
		if got := liveEmotionIndex(t, databasePath, account); int(want) != got {
			t.Fatalf("stored emotion_index = %d after requesting %d", got, want)
		}
		t.Logf("emotion %d acknowledged and persisted", want)
	}

	sawRefresh := false
	waitRefresh := time.After(10 * time.Second)
	for !sawRefresh {
		select {
		case class := <-refreshes:
			t.Logf("server scene refresh: class%d op%d", class, protocol.NotiCharacterList)
			sawRefresh = true
		case <-waitRefresh:
			t.Fatal("the server sent no scene-object refresh after the emotion acknowledgement")
		}
	}

	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	closed = true
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("cleanup %s: %v", account, err)
	}
	t.Logf("test character removed")
}
