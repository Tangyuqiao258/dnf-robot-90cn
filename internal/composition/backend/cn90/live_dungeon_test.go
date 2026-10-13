package cn90

import (
	"context"
	"os"
	"testing"
	"time"

	"robot/internal/shared"
)

// liveDungeonID is Act1/MirkWood.dgn, the lowest entry bar in the lab PVF:
// `[minimum required level] 1`, no `[limit party count]` and no
// `[no fatigue]`/`[enter without fatigue]` tag, so a one-member party at the
// robot's level enters as soon as its fatigue stat is above zero. The ids are
// only meaningful against the PVF the lab runtime loads.
const liveDungeonID uint32 = 3

// TestLiveDungeonDeathRevive proves the run, death and revive chain against the
// real server: the session opens the selector (op15) and commits a solo entry
// (op16), the server makes the run active, the client declares a death (op40)
// and the session's revive worker answers the server's own death notice with an
// op41 revive before the ten second return timer fires.
//
// Nothing here follows a party into a run: entry is a single-member one, and it
// is a precondition rather than the point. Outside an active run the server
// drops op40 and op41 without any reply at all, which is why the entry has to
// come first.
func TestLiveDungeonDeathRevive(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live dungeon death/revive")
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
	channels, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !liveListener(t, channels) {
		t.Skipf("no live 90CN game listener among %v", channels.ports)
	}
	const (
		account = "robot17000009"
		name    = "副本丁"
		uid     = 17000009
	)
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
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
		EnableDungeonRevive: true,
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
	if live.InDungeon() {
		t.Fatal("a session in town started out inside a run")
	}

	if err := live.EnterDungeon(ctx, liveDungeonID, 0); err != nil {
		t.Fatalf("dungeon entry: %v", err)
	}
	if dungeonID, difficulty, active := live.DungeonIdentity(); !active || dungeonID != liveDungeonID || difficulty != 0 {
		t.Fatalf("dungeon identity = (%d, %d, %v) want (%d, 0, true)", dungeonID, difficulty, active, liveDungeonID)
	}
	t.Logf("run active: dungeon=%d", liveDungeonID)

	if err := live.DeclareDeath(ctx, 450, 234); err != nil {
		t.Fatalf("declare death: %v", err)
	}
	if !live.DungeonDead() {
		t.Fatal("the server's death notice was not recorded")
	}
	t.Logf("character reported dead cid=%d", live.selfCharacterID)

	// The revive worker answers the death notice on its own, so the test only
	// has to observe that the server's revive notice arrived before the ten
	// second town-return timer, which would otherwise end the run.
	deadline := time.After(9 * time.Second)
	for {
		if !live.DungeonDead() {
			t.Logf("character revived inside the run")
			break
		}
		select {
		case <-deadline:
			t.Fatal("the death timer was not beaten: the character was never revived")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !live.InDungeon() {
		t.Fatal("the revive ended the run")
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

// TestLiveDungeonDeathInTownIsDropped pins the reason the entry above exists:
// with no active run the server drops a client-declared death without answering
// at all, so a caller can never tell a dropped op40 from a slow one.
func TestLiveDungeonDeathInTownIsDropped(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live town death control")
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
	channels, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !liveListener(t, channels) {
		t.Skipf("no live 90CN game listener among %v", channels.ports)
	}
	const (
		account = "robot17000010"
		name    = "城镇丁"
		uid     = 17000010
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

	// A short deadline is the whole assertion: the server's only possible
	// answer to a town death is silence.
	deathCtx, deathCancel := context.WithTimeout(ctx, 3*time.Second)
	defer deathCancel()
	if err := live.DeclareDeath(deathCtx, 450, 234); err == nil {
		t.Fatal("a town death report was answered")
	}
	if live.DungeonDead() {
		t.Fatal("a town death report set the dead state")
	}
	t.Logf("town op40 was dropped without a notice, as expected")

	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	closed = true
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("cleanup %s: %v", account, err)
	}
}
