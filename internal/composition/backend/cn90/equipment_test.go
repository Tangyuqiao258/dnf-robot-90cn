package cn90

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func newEquipmentTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dnf90.db")
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	_, err := db.Exec(`
CREATE TABLE dnf_characters(character_id TEXT PRIMARY KEY, account_id TEXT NOT NULL, slot INT NOT NULL DEFAULT 0, name TEXT NOT NULL DEFAULT '', job TEXT NOT NULL DEFAULT '', level INT NOT NULL DEFAULT 0, grow_type INT NOT NULL DEFAULT 0, delete_flag INT NOT NULL DEFAULT 0, town_id INT NOT NULL DEFAULT 38, area_id INT NOT NULL DEFAULT 1, pos_x INT NOT NULL DEFAULT 450, pos_y INT NOT NULL DEFAULT 234, tutorial_completed INT NOT NULL DEFAULT 0, exp BIGINT NOT NULL DEFAULT 0, gold BIGINT NOT NULL DEFAULT 0, updated_at TEXT);
CREATE TABLE dnf_character_stats(character_id TEXT, stat_key TEXT, stat_value BIGINT DEFAULT 0, PRIMARY KEY (character_id, stat_key));
CREATE TABLE dnf_character_locations(character_id TEXT PRIMARY KEY, channel_id INT DEFAULT 0, town_id BIGINT DEFAULT 0, dungeon_id BIGINT DEFAULT 0, room_id TEXT DEFAULT '');
CREATE TABLE dnf_pet_entries(character_id TEXT);
CREATE TABLE dnf_quest_states(character_id TEXT, state_group TEXT, quest_id BIGINT);
CREATE TABLE dnf_skill_states(character_id TEXT, skill_id BIGINT);
CREATE TABLE dnf_equipments(character_id TEXT PRIMARY KEY, updated_at TEXT);
CREATE TABLE dnf_equipment_entries(character_id TEXT NOT NULL, entry_key TEXT NOT NULL, slot_index SMALLINT NOT NULL DEFAULT 0, item_id BIGINT NOT NULL DEFAULT 0, bind_flag TINYINT DEFAULT 0, expire_at TEXT, raw_entry BLOB, PRIMARY KEY (character_id, entry_key));
CREATE TABLE dnf_equipment_entry_extra(character_id TEXT NOT NULL, entry_key TEXT NOT NULL, extra_key TEXT NOT NULL, extra_value TEXT NOT NULL, PRIMARY KEY (character_id, entry_key, extra_key));
CREATE TABLE dnf_inventories(character_id TEXT PRIMARY KEY, updated_at TEXT);
CREATE TABLE dnf_inventory_items(character_id TEXT NOT NULL, collection_name TEXT NOT NULL, entry_key TEXT NOT NULL, item_id BIGINT NOT NULL DEFAULT 0, item_count BIGINT NOT NULL DEFAULT 0, bind_flag TINYINT NOT NULL DEFAULT 0, expire_at TEXT, raw_entry BLOB, PRIMARY KEY (character_id, collection_name, entry_key));
CREATE TABLE dnf_inventory_item_extra(character_id TEXT NOT NULL, collection_name TEXT NOT NULL, entry_key TEXT NOT NULL, extra_key TEXT NOT NULL, extra_value TEXT NOT NULL, PRIMARY KEY (character_id, collection_name, entry_key, extra_key));
INSERT INTO dnf_characters(character_id,account_id,slot,name,job,level,delete_flag,tutorial_completed) VALUES ('9','robot17000009',0,'测试者','2',70,0,1);`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func equipmentTestCatalog() []shared.EquipmentCatalogItem {
	items := []shared.EquipmentCatalogItem{
		{ID: 101, Name: "手枪", Path: "equipment/character/gunner/weapon/automatic/101.equ", ItemType: 1, Level: 60, Rarity: 3, Durability: 40, UseJob: []int{2}},
		{ID: 102, Name: "上衣", Path: "equipment/character/common/jacket/cloth/102.equ", ItemType: 3, Level: 60, Rarity: 3, Durability: 30},
		{ID: 103, Name: "护肩", Path: "equipment/character/common/shoulder/larmor/103.equ", ItemType: 4, Level: 60, Rarity: 3, Durability: 20},
		{ID: 104, Name: "下装", Path: "equipment/character/common/pants/cloth/104.equ", ItemType: 5, Level: 60, Rarity: 3, Durability: 25},
		{ID: 105, Name: "鞋子", Path: "equipment/character/common/shoes/cloth/105.equ", ItemType: 6, Level: 60, Rarity: 3, Durability: 20},
		{ID: 106, Name: "腰带", Path: "equipment/character/common/belt/106.equ", ItemType: 7, Level: 60, Rarity: 3, Durability: 15},
		{ID: 107, Name: "项链", Path: "equipment/character/common/amulet/107.equ", ItemType: 8, Level: 60, Rarity: 3, Durability: 15},
		{ID: 108, Name: "手镯", Path: "equipment/character/common/wrist/108.equ", ItemType: 9, Level: 60, Rarity: 3, Durability: 15},
		{ID: 109, Name: "戒指", Path: "equipment/character/common/ring/109.equ", ItemType: 10, Level: 60, Rarity: 3, Durability: 15},
		{ID: 110, Name: "辅助装备", Path: "equipment/character/common/support/110.equ", ItemType: 11, Level: 60, Rarity: 3, Durability: 15},
		{ID: 111, Name: "魔法石", Path: "equipment/character/common/magicstone/111.equ", ItemType: 12, Level: 60, Rarity: 3, Durability: 15},
		{ID: 112, Name: "耳环", Path: "equipment/character/common/earring/112.equ", ItemType: 13, Level: 60, Rarity: 3, Durability: 15},
		{ID: 200, Name: "帽子", Path: "equipment/character/gunner/avatar/cap/200.equ", ItemType: 20, Level: 1, Rarity: 1, UseJob: []int{2}},
		{ID: 201, Name: "头发", Path: "equipment/character/gunner/avatar/hair/201.equ", ItemType: 21, Level: 1, Rarity: 1, UseJob: []int{2}},
		{ID: 202, Name: "脸", Path: "equipment/character/gunner/avatar/face/202.equ", ItemType: 22, Level: 1, Rarity: 1, UseJob: []int{2}},
	}
	return items
}

// TestReconcileRebuildsLoadoutWrittenBeforeTheCurrentVersion covers the fleet
// re-roll: rows carrying an older marker version are rebuilt once, and only
// once. Without the version a selection-rule change would never reach a robot
// that already wears a loadout; without the second reconcile assertion the
// version check could degenerate into wiping the fleet on every startup.
func TestReconcileRebuildsLoadoutWrittenBeforeTheCurrentVersion(t *testing.T) {
	path := newEquipmentTestDatabase(t)
	config := robotconfig.Default()
	config.EquipSlots = []int{1, 3}
	config.AvatarSlots = []int{0}
	config.MinAvatarSlots = 0
	config.PetEnabled = false
	config.EquipRarityMax = 5
	applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, equipmentTestCatalog(), "", func(int) int { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	defer applier.Close()

	// A robot the previous loadout version equipped, wearing one item in slot 11.
	db := openPurgeTestDatabase(t, path)
	if _, err := db.Exec(fmt.Sprintf(`INSERT INTO dnf_equipments(character_id, updated_at) VALUES ('9','2026-01-01T00:00:00Z');
INSERT INTO dnf_equipment_entries(character_id, entry_key, slot_index, item_id) VALUES ('9','11',11,101);
INSERT INTO dnf_equipment_entry_extra(character_id, entry_key, extra_key, extra_value) VALUES ('9','11','robot_loadout','%d');`, cn90LoadoutVersion-1)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	robots := []robotcap.Info{{UID: 17000009, CID: 9, Name: "tester"}}
	replaced, err := applier.ReconcileRobotLoadouts(context.Background(), "robot", robots)
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 1 {
		t.Fatalf("reconciled robots = %d, want the one stale robot rebuilt", replaced)
	}

	db = openPurgeTestDatabase(t, path)
	defer db.Close()
	var stale, current int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_equipment_entry_extra WHERE character_id='9' AND extra_key='robot_loadout' AND CAST(extra_value AS INTEGER) < ?`, cn90LoadoutVersion).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("rows still marked by an older version = %d", stale)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_equipment_entry_extra WHERE character_id='9' AND extra_key='robot_loadout' AND CAST(extra_value AS INTEGER) = ?`, cn90LoadoutVersion).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current == 0 {
		t.Fatal("rebuilt loadout carries no marker at the current version")
	}

	// The rebuild is a one-time pass: the next startup leaves the robot alone.
	replaced, err = applier.ReconcileRobotLoadouts(context.Background(), "robot", robots)
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 0 {
		t.Fatalf("second reconcile rebuilt %d robots, want the loadout left alone", replaced)
	}
}

func TestRobotLoadoutWritesWornRows(t *testing.T) {
	path := newEquipmentTestDatabase(t)
	config := robotconfig.Default()
	config.EquipSlots = []int{1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	config.AvatarSlots = []int{0, 1, 2}
	config.MinAvatarSlots = 0
	config.PreferAvatarSets = false
	config.EquipRarityMax = 5
	// equip_intensify_min/max no longer decides the reinforcement; the level band
	// does. The fixture character is level 70, whose band rolls 10..11, and the
	// applier's random source returns 0, so every item lands on the band's floor.
	applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, equipmentTestCatalog(), "", func(int) int { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	defer applier.Close()
	if err := applier.applyAccountLoadout(context.Background(), "robot17000009", robotcap.Info{}); err != nil {
		t.Fatal(err)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()

	var entries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_equipment_entries WHERE character_id='9'`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries < 12 {
		t.Fatalf("worn entries = %d, want at least 12", entries)
	}
	var slots string
	if err := db.QueryRow(`SELECT group_concat(slot_index, ',') FROM (SELECT slot_index FROM dnf_equipment_entries WHERE character_id='9' ORDER BY slot_index)`).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	// Slot 23 is 耳环, which a level-70 character is not owed: every earring in
	// the archive needs level 90. TestRobotLoadoutGivesTheEarringOnlyToLevel90
	// covers the slot itself.
	for _, want := range []string{"11", "13", "14", "15", "16", "17", "18", "19", "20", "21", "22"} {
		if !containsIntList(slots, want) {
			t.Fatalf("worn slots %q missing %s", slots, want)
		}
	}
	if containsIntList(slots, "23") {
		t.Fatalf("worn slots %q carry an earring below level %d", slots, equipmentcap.EarringMinLevel)
	}
	// Weapon reinforcement is projected through ext_data0 and comes from the
	// level band, which for level 70 is 10..11.
	var reinforce string
	if err := db.QueryRow(`SELECT extra_value FROM dnf_equipment_entry_extra WHERE character_id='9' AND entry_key='11' AND extra_key='ext_data0'`).Scan(&reinforce); err != nil {
		t.Fatal(err)
	}
	if reinforce != "10" {
		t.Fatalf("weapon ext_data0 = %q, want 10", reinforce)
	}
	var durability string
	if err := db.QueryRow(`SELECT extra_value FROM dnf_equipment_entry_extra WHERE character_id='9' AND entry_key='11' AND extra_key='durability'`).Scan(&durability); err != nil {
		t.Fatal(err)
	}
	if durability != "40" {
		t.Fatalf("weapon durability = %q", durability)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT raw_entry FROM dnf_equipment_entries WHERE character_id='9' AND entry_key='11'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 || raw[0] != 11 || uint32(raw[1])|uint32(raw[2])<<8|uint32(raw[3])<<16|uint32(raw[4])<<24 != 101 {
		t.Fatalf("weapon raw entry = %X", raw)
	}
	// Avatars use the actor slot with the direct appearance extra.
	var avatarCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_equipment_entries WHERE character_id='9' AND slot_index IN (0,1,2)`).Scan(&avatarCount); err != nil {
		t.Fatal(err)
	}
	if avatarCount < 1 {
		t.Fatalf("equipped avatars = %d", avatarCount)
	}
	var ability string
	if err := db.QueryRow(`SELECT extra_value FROM dnf_equipment_entry_extra WHERE character_id='9' AND entry_key='0' AND extra_key='avatar_ability_no'`).Scan(&ability); err != nil {
		t.Fatal(err)
	}
	if ability != "0" {
		t.Fatalf("avatar ability = %q", ability)
	}
	var appearance string
	if err := db.QueryRow(`SELECT extra_value FROM dnf_equipment_entry_extra WHERE character_id='9' AND entry_key='0' AND extra_key='current_exe_equipment_type'`).Scan(&appearance); err != nil {
		t.Fatal(err)
	}
	if appearance != "0" {
		t.Fatalf("avatar appearance slot = %q", appearance)
	}
}

// TestRobotLoadoutGivesTheEarringOnlyToLevel90 covers 耳环 against the level
// gate the operator set. 89 is the boundary worth pinning: it is already inside
// the band that hands out 史诗 gear, so the earring's absence there is the
// gate's doing and not the band's.
func TestRobotLoadoutGivesTheEarringOnlyToLevel90(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		// Level 85 rather than 90 so the coat stays wearable at 89: the applier
		// refuses a loadout with no compatible items, and the earring is the only
		// thing this test wants to see appear at the boundary.
		{ID: 103, Name: "上衣", Path: "equipment/character/common/jacket/cloth/103.equ", ItemType: 3, Level: 85, Rarity: 4, Durability: 30},
		{ID: 112, Name: "耳环", Path: "equipment/character/common/earring/112.equ", ItemType: 13, Level: 90, Rarity: 4, Durability: 15},
	}
	config := robotconfig.Default()
	config.EquipSlots = []int{3, 13}
	config.AvatarSlots = []int{0, 1, 2}
	config.MinAvatarSlots = 0
	config.PetEnabled = false
	for _, testCase := range []struct {
		level int
		want  int
	}{
		{level: 89, want: 0},
		{level: 90, want: 112},
	} {
		path := newEquipmentTestDatabase(t)
		setEquipmentTestLevel(t, path, testCase.level)
		applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, items, "", func(int) int { return 0 })
		if err != nil {
			t.Fatal(err)
		}
		if err := applier.applyAccountLoadout(context.Background(), "robot17000009", robotcap.Info{}); err != nil {
			applier.Close()
			t.Fatal(err)
		}
		applier.Close()
		db := openPurgeTestDatabase(t, path)
		var itemID int
		err = db.QueryRow(`SELECT item_id FROM dnf_equipment_entries WHERE character_id='9' AND slot_index=23`).Scan(&itemID)
		db.Close()
		if testCase.want == 0 {
			if err == nil {
				t.Fatalf("level %d wore earring %d, want no earring at all", testCase.level, itemID)
			}
			continue
		}
		if err != nil {
			t.Fatalf("level %d earring lookup: %v", testCase.level, err)
		}
		if itemID != testCase.want {
			t.Fatalf("level %d earring = %d, want %d", testCase.level, itemID, testCase.want)
		}
	}
}

// setEquipmentTestLevel moves the fixture character off the level
// newEquipmentTestDatabase gives it, so a test can pin a level gate.
func setEquipmentTestLevel(t *testing.T, path string, level int) {
	t.Helper()
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`UPDATE dnf_characters SET level=? WHERE character_id='9'`, level); err != nil {
		t.Fatal(err)
	}
}

func containsIntList(list, want string) bool {
	for _, value := range splitComma(list) {
		if value == want {
			return true
		}
	}
	return false
}

func splitComma(value string) []string {
	out := make([]string, 0, 16)
	current := ""
	for _, r := range value {
		if r == ',' {
			out = append(out, current)
			current = ""
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}
