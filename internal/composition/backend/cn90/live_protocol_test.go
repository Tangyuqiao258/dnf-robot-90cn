package cn90

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// defaultLiveRuntime is the locally installed 90CN one-click runtime used when
// CN90_LIVE_RUNTIME is not set. The live checks skip when it is absent.
const defaultLiveRuntime = `D:\cache\game\DNF\DNF90-source-oneclick`

func liveRuntimeRoot(t *testing.T) string {
	t.Helper()
	root := strings.TrimSpace(os.Getenv("CN90_LIVE_RUNTIME"))
	if root == "" {
		root = defaultLiveRuntime
	}
	if !isRegularFile(filepath.Join(root, projectRuntimeDirName, "data", "dnf90.db")) {
		t.Skipf("90CN live runtime is not present at %s", root)
	}
	return root
}

// liveListener reports whether the running server accepts connections on the
// resolved channel port.
func liveListener(t *testing.T, channels channelCatalog) bool {
	t.Helper()
	for _, port := range channels.ports {
		conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// TestLivePersistenceReadOnly validates the adapter's read paths against the
// running server's database: schema validation, PVF catalogs and the
// population report.
func TestLivePersistenceReadOnly(t *testing.T) {
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	status := NewPersistenceInspector(databasePath).Status(context.Background())
	if !status.OK {
		t.Fatalf("persistence status = %+v", status)
	}
	if !status.Writable || !status.SelectVerified {
		t.Fatalf("persistence status flags = %+v", status)
	}
	pvfPath, _, err := serverLayout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	maps, err := ReadTownMapCatalog(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	inspector := SQLitePopulationInspector{
		DatabasePath: databasePath, AccountPrefix: "robot", Config: robotconfig.Default(), Maps: maps,
	}
	report, err := inspector.PopulationReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live database: accounts=%d characters=%d jobs=%v areas=%d/%d",
		report.Accounts, report.Characters, report.Jobs, report.OccupiedAreas, report.AvailableAreas)
}

// TestLiveRobotProvisionAndSession drives the complete robot lifecycle against
// the running DNF90 server: account binding, character provisioning, level
// write, login, select, initial town transition, area spawn and heartbeat.
// The test removes its character again before it returns.
func TestLiveRobotProvisionAndSession(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live protocol lifecycle")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
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
	pvfPath, _, err := serverLayout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	const account = "robot17000001"
	const characterName = "自动机器人甲"
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Clean any previous test state first.
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}

	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: characterName, Job: 2, RobotUID: 17000001,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !result.Created || result.BackendSlot == nil {
		t.Fatalf("provision result = %+v", result)
	}
	t.Logf("provisioned character=%q slot=%d", result.CharacterName, *result.BackendSlot)

	// Write the planned level while the character is offline.
	applier, err := NewSQLiteLoadoutApplier(ctx, databasePath, robotconfig.Default(), catalogs.Equipment, pvfPath, nil)
	if err != nil {
		t.Fatalf("loadout applier: %v", err)
	}
	defer applier.Close()
	applier.LevelThresholds = catalogs.LevelThresholds
	// The production default keeps a robot's pet on only eighty percent of
	// character ids -- applyRobotPet gates on a hash of the id so a robot does
	// not re-roll on every reconcile. This test deletes and re-creates its
	// character on every run, so the server hands out a fresh id and the hash
	// is re-rolled with it: the pet assertions below would fail on roughly one
	// run in five for a reason that has nothing to do with what they check.
	// Every unit test forces the same value, and for the same reason.
	applier.Config.PetProbabilityPercent = 100
	info := robotcap.Info{UID: 17000001, Name: result.CharacterName}
	leveled, err := applier.InitializeCharacter(ctx, account, info, 70, 0)
	if err != nil {
		t.Fatalf("initialize character: %v", err)
	}
	if leveled.Level != 70 {
		t.Fatalf("leveled info = %+v", leveled)
	}
	liveEquipmentSummary(t, databasePath, account)
	livePetSummary(t, databasePath, account)

	// Log in and take the selected character through the town route.
	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	session, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: *result.BackendSlot,
		InitialTownKnown: true, InitialVillage: 38, InitialArea: 1, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	liveSession, ok := session.(*Session)
	if !ok {
		t.Fatal("unexpected session type")
	}
	t.Logf("session online: cid=%d", liveSession.selfCharacterID)

	// The heartbeat is answered only after a character is selected.
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		liveSession.keepaliveGuard.Lock()
		seen := liveSession.checkAckSeen
		liveSession.keepaliveGuard.Unlock()
		if seen {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	liveSession.keepaliveGuard.Lock()
	acknowledged := liveSession.checkAckSeen
	liveSession.keepaliveGuard.Unlock()
	if !acknowledged {
		t.Fatal("server never acknowledged the op1276 heartbeat")
	}

	// Move inside the login town and verify the server accepts the request.
	moveCtx, moveCancel := context.WithTimeout(ctx, 10*time.Second)
	defer moveCancel()
	if err := session.MoveTown(moveCtx, shared.TownMoveIntent{X: 520, Y: 260, Direction: 3, Motion: 100}); err != nil {
		t.Fatalf("move town: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	t.Logf("heartbeat acknowledged and town position sent")

	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	// Remove the test character so the operator's database stays clean.
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	t.Logf("test character removed")
}

// liveEquipmentSummary logs the worn rows the loadout writer produced so the
// live run records the exact equipment projection.
func liveEquipmentSummary(t *testing.T, databasePath, account string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureSQLitePool(db)
	rows, err := db.Query(`SELECT e.slot_index, e.item_id, length(e.raw_entry),
 (SELECT COUNT(*) FROM dnf_equipment_entry_extra x WHERE x.character_id=e.character_id AND x.entry_key=e.entry_key)
 FROM dnf_equipment_entries e JOIN dnf_characters c ON c.character_id=e.character_id
 WHERE c.account_id=? AND c.delete_flag=0 ORDER BY e.slot_index`, account)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	slots := make([]string, 0, 24)
	seen := make(map[int]bool, 24)
	for rows.Next() {
		var slot, item, rawLen, extras int
		if err := rows.Scan(&slot, &item, &rawLen, &extras); err != nil {
			t.Fatal(err)
		}
		if seen[slot] {
			t.Fatalf("duplicate worn slot %d in the live loadout", slot)
		}
		seen[slot] = true
		slots = append(slots, fmt.Sprintf("%d:%d(raw%d/ex%d)", slot, item, rawLen, extras))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(slots) < 12 {
		t.Fatalf("live loadout produced %d worn rows, want at least 12: %v", len(slots), slots)
	}
	t.Logf("live loadout worn rows: %v", slots)
}

// livePetSummary logs the creature rows the pet writer produced so the live
// run records the creature projection the server later reads.
func livePetSummary(t *testing.T, databasePath, account string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureSQLitePool(db)
	var equippedKey string
	var townDisplay int
	if err := db.QueryRow(`SELECT p.equipped_key, p.town_display FROM dnf_pets p JOIN dnf_characters c ON c.character_id=p.character_id WHERE c.account_id=? AND c.delete_flag=0`, account).
		Scan(&equippedKey, &townDisplay); err != nil {
		t.Fatalf("live pet parent: %v", err)
	}
	if equippedKey == "" || townDisplay != 1 {
		t.Fatalf("live pet parent equipped=%q display=%d", equippedKey, townDisplay)
	}
	var petItem, petLevel, satiety int
	if err := db.QueryRow(`SELECT item_id, pet_level, satiety FROM dnf_pet_entries e JOIN dnf_characters c ON c.character_id=e.character_id WHERE c.account_id=? AND c.delete_flag=0`, account).
		Scan(&petItem, &petLevel, &satiety); err != nil {
		t.Fatalf("live pet entry: %v", err)
	}
	var wornItem int
	var raw []byte
	if err := db.QueryRow(`SELECT item_id, raw_entry FROM dnf_equipment_entries e JOIN dnf_characters c ON c.character_id=e.character_id WHERE c.account_id=? AND c.delete_flag=0 AND e.entry_key='26'`, account).
		Scan(&wornItem, &raw); err != nil {
		t.Fatalf("live pet equipment row: %v", err)
	}
	if wornItem != petItem || len(raw) != 46 || binary.LittleEndian.Uint32(raw[24:28]) == 0 {
		t.Fatalf("live pet equipment row item=%d petItem=%d rawLen=%d", wornItem, petItem, len(raw))
	}
	var artifactKinds string
	if err := db.QueryRow(`SELECT COALESCE(group_concat(entry_key, ','), '') FROM (SELECT a.entry_key FROM dnf_pet_artifacts a JOIN dnf_characters c ON c.character_id=a.character_id WHERE c.account_id=? AND c.delete_flag=0 ORDER BY a.entry_key)`, account).
		Scan(&artifactKinds); err != nil {
		t.Fatalf("live pet artifacts: %v", err)
	}
	t.Logf("live pet: item=%d level=%d satiety=%d serial=%d artifacts=[%s]", petItem, petLevel, satiety, binary.LittleEndian.Uint32(raw[24:28]), artifactKinds)
}

// liveClearAccount logs into the account and removes every character on it.
func liveClearAccount(ctx context.Context, binder *AccountBinder, channels channelCatalog, account string) error {
	address, err := channelAddress("127.0.0.1", channels, account)
	if err != nil {
		return err
	}
	client, first, err := dialBoundSession(ctx, binder, address, account)
	if err != nil {
		return err
	}
	defer client.Close()
	if _, err := client.CompleteHandshakeFrom(ctx, first); err != nil {
		return err
	}
	if err := client.RequestRoster(ctx); err != nil {
		return err
	}
	rosterPacket, err := waitUpperPacket(ctx, client, protocol.ClassNotice, protocol.NotiCharacterList)
	if err != nil {
		return err
	}
	return clearRobotAccountRoster(ctx, client, rosterPacket.Body)
}

// TestLiveBoundAccountRosterNames prints the account/roster state for manual
// inspection without mutating anything.
func TestLiveBoundAccountRosterNames(t *testing.T) {
	if os.Getenv("CN90_LIVE_ROSTER") != "1" {
		t.Skip("set CN90_LIVE_ROSTER=1 to inspect live rosters")
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
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for uid := 17000000; uid <= 17000005; uid++ {
		account := fmt.Sprintf("robot%d", uid)
		address, err := channelAddress("127.0.0.1", channels, account)
		if err != nil {
			t.Fatal(err)
		}
		client, first, err := dialBoundSession(ctx, binder, address, account)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.CompleteHandshakeFrom(ctx, first); err != nil {
			_ = client.Close()
			t.Fatal(err)
		}
		if err := client.RequestRoster(ctx); err != nil {
			_ = client.Close()
			t.Fatal(err)
		}
		packet, err := waitUpperPacket(ctx, client, protocol.ClassNotice, protocol.NotiCharacterList)
		if err != nil {
			_ = client.Close()
			t.Fatal(err)
		}
		entries, err := protocol.DecodeCharacterRoster(packet.Body)
		_ = client.Close()
		if err != nil {
			t.Fatalf("account %s roster: %v", account, err)
		}
		t.Logf("account=%s characters=%d", account, len(entries))
	}
}
