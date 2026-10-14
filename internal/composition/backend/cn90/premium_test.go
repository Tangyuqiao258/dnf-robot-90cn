package cn90

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
)

func newPremiumTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dnf90.db")
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE dnf_account_metadata(
		account_id VARCHAR(128) COLLATE NOCASE NOT NULL,
		entry_key VARCHAR(128) COLLATE NOCASE NOT NULL,
		entry_value LONGTEXT COLLATE NOCASE NOT NULL,
		PRIMARY KEY (account_id, entry_key));
	INSERT INTO dnf_account_metadata(account_id, entry_key, entry_value) VALUES
	('robot17001000','source','dnfbridge'),
	('robot17001001','source','dnfbridge');`); err != nil {
		t.Fatal(err)
	}
	return path
}

func readAccountMetadata(t *testing.T, db *sql.DB, account, key string) string {
	t.Helper()
	var value string
	err := db.QueryRow(`SELECT entry_value FROM dnf_account_metadata WHERE account_id=? AND entry_key=?`, account, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// The key is the whole contract with the game server: it reads this account's
// membership through premium.ExpireAt, which is a lookup of premium_expire_56.
// A rename here would silently grant nothing.
func TestBlackDiamondMetadataKeyMatchesTheServerContract(t *testing.T) {
	if blackDiamondExpireMetadataKey != "premium_expire_"+strconv.Itoa(blackDiamondPremiumType) {
		t.Fatalf("key=%q type=%d", blackDiamondExpireMetadataKey, blackDiamondPremiumType)
	}
	if blackDiamondPremiumType != 56 {
		t.Fatalf("black diamond premium type = %d, want the server's type 56", blackDiamondPremiumType)
	}
}

func TestWriteBlackDiamondExpiryGrantsTheConfiguredTerm(t *testing.T) {
	path := newPremiumTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if err := writeBlackDiamondExpiry(context.Background(), db, "robot17001000", 3650, now); err != nil {
		t.Fatal(err)
	}
	got := readAccountMetadata(t, db, "robot17001000", blackDiamondExpireMetadataKey)
	want := now.Add(3650 * 24 * time.Hour).Unix()
	if got != strconv.FormatInt(want, 10) {
		t.Fatalf("expiry=%q want %d", got, want)
	}
	// The account's other metadata rows must survive the insert.
	if other := readAccountMetadata(t, db, "robot17001000", "source"); other != "dnfbridge" {
		t.Fatalf("source=%q, want the existing row untouched", other)
	}
	// The grant is account-scoped: a sibling account is not a member.
	if sibling := readAccountMetadata(t, db, "robot17001001", blackDiamondExpireMetadataKey); sibling != "" {
		t.Fatalf("sibling expiry=%q, want no membership", sibling)
	}
}

func TestWriteBlackDiamondExpiryDoesNotShortenAMembership(t *testing.T) {
	path := newPremiumTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	// An operator who bought a longer term by hand. The reconcile only knows the
	// configured one, so it must not cut this back.
	long := now.Add(20 * 365 * 24 * time.Hour).Unix()
	if _, err := db.Exec(`INSERT INTO dnf_account_metadata(account_id, entry_key, entry_value) VALUES (?,?,?)`,
		"robot17001000", blackDiamondExpireMetadataKey, strconv.FormatInt(long, 10)); err != nil {
		t.Fatal(err)
	}
	if err := writeBlackDiamondExpiry(context.Background(), db, "robot17001000", 3650, now); err != nil {
		t.Fatal(err)
	}
	if got := readAccountMetadata(t, db, "robot17001000", blackDiamondExpireMetadataKey); got != strconv.FormatInt(long, 10) {
		t.Fatalf("expiry=%q, want the longer date %d kept", got, long)
	}
}

func TestWriteBlackDiamondExpiryRenewsALapsedMembership(t *testing.T) {
	path := newPremiumTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	// A 30-day shop membership that ran out: the server treats it as no
	// membership at all, so the fleet grant has to replace it.
	lapsed := now.Add(-24 * time.Hour).Unix()
	if _, err := db.Exec(`INSERT INTO dnf_account_metadata(account_id, entry_key, entry_value) VALUES (?,?,?)`,
		"robot17001000", blackDiamondExpireMetadataKey, strconv.FormatInt(lapsed, 10)); err != nil {
		t.Fatal(err)
	}
	if err := writeBlackDiamondExpiry(context.Background(), db, "robot17001000", 3650, now); err != nil {
		t.Fatal(err)
	}
	want := now.Add(3650 * 24 * time.Hour).Unix()
	if got := readAccountMetadata(t, db, "robot17001000", blackDiamondExpireMetadataKey); got != strconv.FormatInt(want, 10) {
		t.Fatalf("expiry=%q want %d", got, want)
	}
}

func TestWriteBlackDiamondExpirySkipsAProfileWithoutTheTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnf90.db")
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	// The purge fixture models a profile whose tables are the character-scoped
	// ones only, which is what the older C# mirror databases look like.
	if err := writeBlackDiamondExpiry(context.Background(), db, "robot17001000", 3650, time.Now().UTC()); err != nil {
		t.Fatalf("grants against a profile without account metadata must be a no-op: %v", err)
	}
}

func TestBlackDiamondGrantDaysFollowsTheSwitch(t *testing.T) {
	enabled := robotconfig.Default()
	if enabled.BlackDiamondDays <= 0 {
		t.Fatal("the built-in term grants nothing")
	}
	applier := &SQLiteLoadoutApplier{Config: enabled}
	if got := applier.blackDiamondGrantDays(); got != enabled.BlackDiamondDays {
		t.Fatalf("enabled grant = %d, want %d", got, enabled.BlackDiamondDays)
	}
	disabled := enabled
	disabled.BlackDiamondEnabled = false
	applier = &SQLiteLoadoutApplier{Config: disabled}
	if got := applier.blackDiamondGrantDays(); got != 0 {
		t.Fatalf("disabled grant = %d, want 0", got)
	}
	var nilApplier *SQLiteLoadoutApplier
	if got := nilApplier.blackDiamondGrantDays(); got != 0 {
		t.Fatalf("nil applier grant = %d, want 0", got)
	}
	// The switch is the configuration surface, so it must survive the ini.
	zero := enabled
	zero.BlackDiamondDays = 0
	robotconfig.Normalize(&zero)
	if zero.BlackDiamondDays != enabled.BlackDiamondDays {
		t.Fatalf("a zero term normalized to %d, want the built-in %d", zero.BlackDiamondDays, enabled.BlackDiamondDays)
	}
}
