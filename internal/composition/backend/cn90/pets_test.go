package cn90

import (
	"context"
	"encoding/binary"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/charset"
	"robot/internal/shared"
)

func newPetTestDatabase(t *testing.T) string {
	t.Helper()
	path := newEquipmentTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	_, err := db.Exec(`
DROP TABLE IF EXISTS dnf_inventory_items;
DROP TABLE IF EXISTS dnf_pet_entries;
CREATE TABLE dnf_inventories(character_id TEXT PRIMARY KEY, updated_at TEXT);
CREATE TABLE dnf_inventory_items(character_id TEXT NOT NULL, collection_name TEXT NOT NULL, entry_key TEXT NOT NULL, item_id BIGINT NOT NULL DEFAULT 0, item_count BIGINT NOT NULL DEFAULT 0, bind_flag TINYINT DEFAULT 0, expire_at TEXT, raw_entry BLOB, PRIMARY KEY (character_id, collection_name, entry_key));
CREATE TABLE dnf_inventory_item_extra(character_id TEXT NOT NULL, collection_name TEXT NOT NULL, entry_key TEXT NOT NULL, extra_key TEXT NOT NULL, extra_value TEXT NOT NULL, PRIMARY KEY (character_id, collection_name, entry_key, extra_key));
CREATE TABLE dnf_pets(character_id TEXT PRIMARY KEY, equipped_key TEXT NOT NULL DEFAULT '', town_display INT NOT NULL DEFAULT 0, updated_at TEXT);
CREATE TABLE dnf_pet_entries(character_id TEXT NOT NULL, pet_key TEXT NOT NULL, creature_key BIGINT NOT NULL DEFAULT 0, item_id BIGINT NOT NULL DEFAULT 0, source_list_type TINYINT DEFAULT 0, source_slot_index SMALLINT DEFAULT 0, pet_name TEXT NOT NULL DEFAULT '', name_raw BLOB, satiety TINYINT DEFAULT 0, satiety_micros BIGINT DEFAULT 0, mode_flag TINYINT DEFAULT 0, mode1_field_0a TINYINT DEFAULT 0, mode1_field_0b TINYINT DEFAULT 0, pet_level BIGINT DEFAULT 0, pet_exp BIGINT DEFAULT 0, tail_flag TINYINT DEFAULT 0, raw_entry BLOB, PRIMARY KEY (character_id, pet_key));
CREATE TABLE dnf_pet_entry_extra(character_id TEXT NOT NULL, entry_key TEXT NOT NULL, extra_key TEXT NOT NULL, extra_value TEXT NOT NULL, PRIMARY KEY (character_id, entry_key, extra_key));
CREATE TABLE dnf_pet_clear_tokens(character_id TEXT NOT NULL, pet_key TEXT NOT NULL, token_order INT NOT NULL DEFAULT 0, token TEXT NOT NULL, applied INT NOT NULL DEFAULT 0, PRIMARY KEY (character_id, pet_key, token));
CREATE TABLE dnf_pet_artifacts(character_id TEXT NOT NULL, entry_key TEXT NOT NULL, item_id BIGINT NOT NULL DEFAULT 0, item_count BIGINT NOT NULL DEFAULT 0, bind_flag TINYINT DEFAULT 0, expire_at TEXT, raw_entry BLOB, PRIMARY KEY (character_id, entry_key));
CREATE TABLE dnf_pet_artifact_extra(character_id TEXT NOT NULL, entry_key TEXT NOT NULL, extra_key TEXT NOT NULL, extra_value TEXT NOT NULL, PRIMARY KEY (character_id, entry_key, extra_key));`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func petTestCatalog() []shared.EquipmentCatalogItem {
	items := equipmentTestCatalog()
	items = append(items,
		shared.EquipmentCatalogItem{ID: 60001, Name: "petit", Path: "equipment/creature/petit/petit.equ", ItemType: 30, Level: 1, Rarity: 1},
		shared.EquipmentCatalogItem{ID: 60101, Name: "artifact red", Path: "equipment/creature/artifact_red/red.equ", ItemType: 31, Level: 1},
		shared.EquipmentCatalogItem{ID: 60102, Name: "artifact blue", Path: "equipment/creature/artifact_blue/blue.equ", ItemType: 32, Level: 1},
		shared.EquipmentCatalogItem{ID: 60103, Name: "artifact green", Path: "equipment/creature/artifact_green/green.equ", ItemType: 33, Level: 1},
	)
	return items
}

func TestRobotLoadoutWritesPets(t *testing.T) {
	path := newPetTestDatabase(t)
	config := robotconfig.Default()
	config.EquipSlots = []int{1}
	config.AvatarSlots = []int{0}
	config.MinAvatarSlots = 0
	config.PetEnabled = true
	config.PetProbabilityPercent = 100
	config.PetArtifactEnabled = true
	config.MinPetArtifactSlots = 3
	config.MaxPetArtifactSlots = 3
	applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, petTestCatalog(), "", func(int) int { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	defer applier.Close()
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	if err := applier.applyRobotPet(context.Background(), db, "9"); err != nil {
		t.Fatal(err)
	}

	var equippedKey string
	var townDisplay int
	if err := db.QueryRow(`SELECT equipped_key, town_display FROM dnf_pets WHERE character_id='9'`).Scan(&equippedKey, &townDisplay); err != nil {
		t.Fatal(err)
	}
	if equippedKey != "1" || townDisplay != 1 {
		t.Fatalf("pet parent equipped=%q display=%d", equippedKey, townDisplay)
	}

	var itemID, level, satiety, listType, slotIndex int
	if err := db.QueryRow(`SELECT item_id, pet_level, satiety, source_list_type, source_slot_index FROM dnf_pet_entries WHERE character_id='9' AND pet_key='1'`).Scan(&itemID, &level, &satiety, &listType, &slotIndex); err != nil {
		t.Fatal(err)
	}
	if itemID != 60001 || level != 1 || satiety != 100 || listType != 3 || slotIndex != 26 {
		t.Fatalf("pet entry item=%d level=%d satiety=%d list=%d slot=%d", itemID, level, satiety, listType, slotIndex)
	}
	var satietyMicros int64
	if err := db.QueryRow(`SELECT satiety_micros FROM dnf_pet_entries WHERE character_id='9' AND pet_key='1'`).Scan(&satietyMicros); err != nil {
		t.Fatal(err)
	}
	if satietyMicros != 100*cn90PetSatietyScale {
		t.Fatalf("pet satiety micros = %d", satietyMicros)
	}
	var petMarker int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_pet_entry_extra WHERE character_id='9' AND entry_key='1' AND extra_key='robot_loadout'`).Scan(&petMarker); err != nil {
		t.Fatal(err)
	}
	if petMarker != 1 {
		t.Fatal("pet entry is missing the robot_loadout marker")
	}

	var raw []byte
	var wornItem int
	if err := db.QueryRow(`SELECT item_id, raw_entry FROM dnf_equipment_entries WHERE character_id='9' AND entry_key='26'`).Scan(&wornItem, &raw); err != nil {
		t.Fatal(err)
	}
	if wornItem != 60001 || len(raw) != 46 || raw[0] != 26 || binary.LittleEndian.Uint32(raw[1:5]) != 60001 {
		t.Fatalf("pet equipment row item=%d raw=% X", wornItem, raw)
	}
	if binary.LittleEndian.Uint32(raw[24:28]) != 1 {
		t.Fatalf("pet equipment serial at +24 = % X", raw[24:28])
	}
	var serialExtra string
	if err := db.QueryRow(`SELECT extra_value FROM dnf_equipment_entry_extra WHERE character_id='9' AND entry_key='26' AND extra_key='creature_serial_or_handle'`).Scan(&serialExtra); err != nil {
		t.Fatal(err)
	}
	if serialExtra != "1" {
		t.Fatalf("pet equipment serial extra = %q", serialExtra)
	}

	var invCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_inventory_items WHERE character_id='9'`).Scan(&invCount); err != nil {
		t.Fatal(err)
	}
	if invCount != 0 {
		t.Fatalf("worn creature must not stay in the pet inventory, rows=%d", invCount)
	}

	var kinds string
	if err := db.QueryRow(`SELECT group_concat(entry_key, ',') FROM (SELECT entry_key FROM dnf_pet_artifacts WHERE character_id='9' ORDER BY entry_key)`).Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if kinds != "blue,green,red" {
		t.Fatalf("pet artifact kinds = %q", kinds)
	}
	var artifactCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_pet_artifact_extra WHERE character_id='9' AND extra_key='robot_loadout'`).Scan(&artifactCount); err != nil {
		t.Fatal(err)
	}
	if artifactCount != 3 {
		t.Fatalf("pet artifact markers = %d", artifactCount)
	}

	// A second application replaces the state instead of duplicating it.
	if err := applier.applyRobotPet(context.Background(), db, "9"); err != nil {
		t.Fatal(err)
	}
	var entries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_pet_entries WHERE character_id='9'`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("pet entries after reapply = %d", entries)
	}
}

func TestReconcileAppliesPetsToEquippedRobot(t *testing.T) {
	path := newPetTestDatabase(t)
	config := robotconfig.Default()
	config.EquipSlots = []int{1}
	config.AvatarSlots = []int{0}
	config.MinAvatarSlots = 0
	config.PetEnabled = true
	config.PetProbabilityPercent = 100
	config.PetArtifactEnabled = false
	applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, petTestCatalog(), "", func(int) int { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	defer applier.Close()
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	// Simulate a robot equipped by the equipment stage: worn rows carry the
	// robot_loadout marker but no creature state exists yet.
	if _, err := db.Exec(`INSERT INTO dnf_equipments(character_id, updated_at) VALUES ('9', '2026-01-01T00:00:00Z');
INSERT INTO dnf_equipment_entry_extra(character_id, entry_key, extra_key, extra_value) VALUES ('9','11','robot_loadout','1');`); err != nil {
		t.Fatal(err)
	}
	replaced, err := applier.ReconcileRobotLoadouts(context.Background(), "robot", []robotcap.Info{{UID: 17000009, CID: 9, Name: "tester"}})
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 1 {
		t.Fatalf("reconciled robots = %d, want 1", replaced)
	}
	var pets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_pet_entries WHERE character_id='9'`).Scan(&pets); err != nil {
		t.Fatal(err)
	}
	if pets != 1 {
		t.Fatalf("reconciled pet entries = %d", pets)
	}
	// A second reconcile sees the pet marker and leaves the state untouched.
	replaced, err = applier.ReconcileRobotLoadouts(context.Background(), "robot", []robotcap.Info{{UID: 17000009, CID: 9, Name: "tester"}})
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 0 {
		t.Fatalf("second reconcile replaced %d robots", replaced)
	}
}

func TestRobotPetHonoursDisabledConfig(t *testing.T) {
	path := newPetTestDatabase(t)
	config := robotconfig.Default()
	config.PetEnabled = false
	applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, petTestCatalog(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer applier.Close()
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	if err := applier.applyRobotPet(context.Background(), db, "9"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_pets WHERE character_id='9'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("disabled pet config produced %d rows", count)
	}
}

// TestRobotPetNameRawIsGBKUnderTheServerLimit pins the wire encoding of the
// creature name. The 90CN server rejects a name_raw of 30 bytes or more and,
// when name_raw is NULL, falls back to the UTF-8 bytes of pet_name -- which
// every long Chinese creature name exceeds, keeping the character offline with
// "pet state is invalid: key=1 legacy_name_bytes=32". The row must carry the
// GBK bytes instead, shortened on rune boundaries when even GBK would reach
// the limit.
func TestRobotPetNameRawIsGBKUnderTheServerLimit(t *testing.T) {
	cases := []struct {
		name     string
		wantKept string
	}{
		// 32 UTF-8 bytes, 23 GBK bytes: the exact shape observed live.
		{name: "迷你战士宠物SS级 - 契约", wantKept: "迷你战士宠物SS级 - 契约"},
		// 40 GBK bytes: must be shortened below the 30-byte wire limit.
		{name: "迷迷你战士宠物SS级契约迷迷你战士宠物", wantKept: ""},
	}
	for _, tc := range cases {
		path := newPetTestDatabase(t)
		config := robotconfig.Default()
		config.EquipSlots = []int{1}
		config.AvatarSlots = []int{0}
		config.MinAvatarSlots = 0
		config.PetEnabled = true
		config.PetProbabilityPercent = 100
		config.PetArtifactEnabled = false
		catalog := append(petTestCatalog(),
			shared.EquipmentCatalogItem{ID: 60002, Name: tc.name, Path: "equipment/creature/long/long.equ", ItemType: 30, Level: 1, Rarity: 1})
		applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, catalog, "", func(int) int { return 1 })
		if err != nil {
			t.Fatal(err)
		}
		db := openPurgeTestDatabase(t, path)
		if err := applier.applyRobotPet(context.Background(), db, "9"); err != nil {
			applier.Close()
			db.Close()
			t.Fatal(err)
		}
		var raw []byte
		var itemID int
		if err := db.QueryRow(`SELECT item_id, name_raw FROM dnf_pet_entries WHERE character_id='9' AND pet_key='1'`).Scan(&itemID, &raw); err != nil {
			applier.Close()
			db.Close()
			t.Fatal(err)
		}
		applier.Close()
		db.Close()
		if itemID != 60002 {
			t.Fatalf("case %q: pet item=%d, want the long-named creature", tc.name, itemID)
		}
		if len(raw) == 0 {
			t.Fatalf("case %q: name_raw is NULL; the server would fall back to %d UTF-8 bytes and reject the character", tc.name, len(tc.name))
		}
		if len(raw) >= cn90CreatureNameRawLimit {
			t.Fatalf("case %q: name_raw=%d bytes, over the server limit", tc.name, len(raw))
		}
		decoded := charset.DecodeWireName(raw)
		if tc.wantKept != "" {
			if decoded != tc.wantKept {
				t.Fatalf("case %q: name_raw decodes to %q", tc.name, decoded)
			}
			continue
		}
		if decoded == "" || len([]rune(decoded)) >= len([]rune(tc.name)) {
			t.Fatalf("case %q: over-long name was not shortened on rune boundaries: %q", tc.name, decoded)
		}
	}
}
