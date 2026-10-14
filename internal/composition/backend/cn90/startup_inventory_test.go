package cn90

import (
	"context"
	"path/filepath"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
)

func startupInventoryTestConfig() robotconfig.RuntimeConfig {
	return robotconfig.RuntimeConfig{
		LevelMin: 60, LevelMax: 90, Jobs: []int{0, 1},
		RobotUIDStart: 17000000, RobotUIDEnd: 17000099,
	}
}

// newStartupInventoryTestDatabase builds the tables the startup inventory reads
// and writes, seeds the accounts and characters the caller passes, and fans one
// dependent row out per character so a sweep has something to remove.
func newStartupInventoryTestDatabase(t *testing.T, seed string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dnf90.db")
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	_, err := db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE dnf_accounts(account_id TEXT PRIMARY KEY, state TEXT, updated_at TEXT);
CREATE TABLE dnf_characters(character_id TEXT PRIMARY KEY, account_id TEXT NOT NULL, slot INT, name TEXT, job TEXT, level INT, grow_type INT, delete_flag INT DEFAULT 0, town_id INT DEFAULT 0, area_id INT DEFAULT 0, pos_x INT DEFAULT 0, pos_y INT DEFAULT 0, updated_at TEXT);
CREATE TABLE dnf_character_stats(character_id TEXT, stat_key TEXT, stat_value INT);
CREATE TABLE dnf_inventory_items(character_id TEXT, entry_key TEXT, item_id INT);
CREATE TABLE dnf_settings(scope TEXT, key TEXT);
CREATE TABLE dnf_setting_values(scope TEXT, key TEXT, value TEXT);
` + seed + `
INSERT INTO dnf_character_stats(character_id,stat_key,stat_value) SELECT character_id,'hp',1 FROM dnf_characters;
INSERT INTO dnf_inventory_items(character_id,entry_key,item_id) SELECT character_id,'0:0',1 FROM dnf_characters;
INSERT INTO dnf_settings(scope,key) SELECT 'character:'||character_id||':container_state','header' FROM dnf_characters;
INSERT INTO dnf_setting_values(scope,key,value) SELECT 'character:'||character_id||':container_state','header','x' FROM dnf_characters;`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSQLiteStartupInventorySweepsATombstoneInsteadOfItsAccount pins the defect
// this cleanup was written for: the game server soft-deletes a character by
// setting delete_flag, and the old scan counted that row as a second character,
// declared the account incomplete and deleted the live character along with it.
// The account now stays, the tombstone row stays -- the server reserves the
// character name with it -- and only the rows hanging off it go.
func TestSQLiteStartupInventorySweepsATombstoneInsteadOfItsAccount(t *testing.T) {
	path := newStartupInventoryTestDatabase(t, `
INSERT INTO dnf_accounts(account_id) VALUES ('robot17000001'),('robot17000002'),('player17000003');
INSERT INTO dnf_characters(character_id,account_id,slot,name,job,level,grow_type,delete_flag) VALUES
('101','robot17000001',0,'live','1',70,0,0),
('102','robot17000001',1,'dead','1',70,0,1),
('103','robot17000002',0,'healthy','1',70,0,0),
('104','player17000003',1,'moved','1',70,0,1);`)
	inventory := SQLiteStartupInventory{DatabasePath: path, AccountPrefix: "robot", Config: startupInventoryTestConfig()}
	result, err := inventory.ScanAndClean(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ScannedAccounts != 2 || len(result.Robots) != 2 || len(result.Identities) != 2 {
		t.Fatalf("inventory=%+v", result)
	}
	if result.DeletedAccounts != 0 || result.DeletedCharacters != 0 || result.SweptCharacters != 1 {
		t.Fatalf("inventory=%+v", result)
	}

	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	// The tombstone is cleaned, not removed.
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id='102' AND delete_flag=1`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id='102'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_inventory_items WHERE character_id='102'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_settings WHERE scope='character:102:container_state'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_setting_values WHERE scope='character:102:container_state'`, 0)
	// The account and its live character are untouched.
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id='robot17000001'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id='101'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_inventory_items WHERE character_id='101'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_settings WHERE scope='character:101:container_state'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id='103'`, 1)
	// A row outside the robot segment is not the robot's to sweep.
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id='104'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_settings WHERE scope='character:104:container_state'`, 1)
}

// TestSQLiteStartupInventoryDeletesAnAccountWithNoLiveCharacter keeps the rule
// the sweep must not weaken: an account whose characters are all tombstones has
// no robot in it, so the account goes and takes its rows with it.
func TestSQLiteStartupInventoryDeletesAnAccountWithNoLiveCharacter(t *testing.T) {
	path := newStartupInventoryTestDatabase(t, `
INSERT INTO dnf_accounts(account_id) VALUES ('robot17000004');
INSERT INTO dnf_characters(character_id,account_id,slot,name,job,level,grow_type,delete_flag) VALUES
('105','robot17000004',0,'gone','1',70,0,1);`)
	inventory := SQLiteStartupInventory{DatabasePath: path, AccountPrefix: "robot", Config: startupInventoryTestConfig()}
	result, err := inventory.ScanAndClean(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ScannedAccounts != 1 || len(result.Robots) != 0 {
		t.Fatalf("inventory=%+v", result)
	}
	if result.DeletedAccounts != 1 || result.DeletedCharacters != 1 || result.SweptCharacters != 0 {
		t.Fatalf("inventory=%+v", result)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id='robot17000004'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id='105'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id='105'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_settings WHERE scope='character:105:container_state'`, 0)
}

// TestSQLiteStartupInventoryDeletesAnAccountWithExtraLiveCharacters covers the
// other invalid shape with a tombstone mixed in: two live characters make the
// account invalid, the whole account is removed, and the tombstone is counted
// once with the rows the removal took rather than swept separately.
func TestSQLiteStartupInventoryDeletesAnAccountWithExtraLiveCharacters(t *testing.T) {
	path := newStartupInventoryTestDatabase(t, `
INSERT INTO dnf_accounts(account_id) VALUES ('robot17000005');
INSERT INTO dnf_characters(character_id,account_id,slot,name,job,level,grow_type,delete_flag) VALUES
('106','robot17000005',0,'one','1',70,0,0),
('107','robot17000005',1,'two','1',70,0,0),
('108','robot17000005',2,'gone','1',70,0,1);`)
	inventory := SQLiteStartupInventory{DatabasePath: path, AccountPrefix: "robot", Config: startupInventoryTestConfig()}
	result, err := inventory.ScanAndClean(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedAccounts != 1 || result.DeletedCharacters != 3 || result.SweptCharacters != 0 {
		t.Fatalf("inventory=%+v", result)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id='robot17000005'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id IN ('106','107','108')`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id IN ('106','107','108')`, 0)
}

// TestSweepTombstonedCharactersLeavesARestoredCharacterAlone pins the re-read
// inside the cleanup transaction: an id recorded as a tombstone at scan time is
// swept only while its row is still flagged deleted, so a character the game
// server restored keeps its data.
func TestSweepTombstonedCharactersLeavesARestoredCharacterAlone(t *testing.T) {
	path := newStartupInventoryTestDatabase(t, `
INSERT INTO dnf_accounts(account_id) VALUES ('robot17000006');
INSERT INTO dnf_characters(character_id,account_id,slot,name,job,level,grow_type,delete_flag) VALUES
('109','robot17000006',0,'live','1',70,0,0),
('110','robot17000006',1,'restored','1',70,0,0);`)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	inventory := SQLiteStartupInventory{DatabasePath: path, AccountPrefix: "robot", Config: startupInventoryTestConfig()}
	swept, err := inventory.sweepTombstonedCharacters(context.Background(), tx,
		[]startupAccount{{id: "robot17000006", name: "robot17000006", tombstoned: []string{"110"}}}, nil)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if swept != 0 {
		t.Fatalf("swept=%d, want the restored character left alone", swept)
	}
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id='110'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_inventory_items WHERE character_id='110'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_settings WHERE scope='character:110:container_state'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_setting_values WHERE scope='character:110:container_state'`, 1)
}
