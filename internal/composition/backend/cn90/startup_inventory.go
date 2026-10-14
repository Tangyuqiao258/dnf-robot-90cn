package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	foundationlog "robot/internal/foundation/log"
)

// startupDeleteLogLimit bounds the per-account deletion trace. Larger cleanups
// stay traceable through the aggregate counters and the DELETE_MORE line.
const startupDeleteLogLimit = 50

type StartupInventory struct {
	Robots            []robotcap.Info
	Identities        []robotstate.Identity
	ScannedAccounts   int
	DeletedAccounts   int
	DeletedCharacters int
	SweptCharacters   int
}

type SQLiteStartupInventory struct {
	DatabasePath  string
	AccountPrefix string
	Config        robotconfig.RuntimeConfig
}

type startupAccount struct {
	id         string
	uid        int
	name       string
	characters []startupCharacter
	// tombstoned holds the ids of the character rows the game server
	// soft-deleted (delete_flag <> 0). The row itself has to stay -- it is what
	// the server reserves the name with -- but the rows hanging off it are
	// garbage, and a soft-deleted character is not a character the account can
	// be judged by.
	tombstoned []string
}

type startupCharacter struct {
	id         string
	name       string
	job        int
	grow       int
	level      int
	slot       int
	deleteFlag int
	village    int
	area       int
	x, y       int
}

// ScanAndClean makes the game database the only durable source of 90CN robot
// identity. Accounts are owned only when their account id is exactly
// prefix+UID and the UID is inside the configured segment. Any owned account
// that is not a complete, usable one-character robot is removed together with
// its dependent rows.
//
// Only rows the game server leaves live decide that: a soft-deleted character
// carries delete_flag and is invisible to the game, so an account holding one
// live character and one tombstone is a healthy robot account, not an invalid
// one. Its tombstone keeps the row -- which is what the server reserves the
// name with -- while the rows hanging off it are swept.
//
// The scan is read-only. Deletion and sweeping run in a separate short write
// transaction that re-verifies each candidate against the recorded character
// set, so the game server is not blocked behind a full-table scan and no account
// is deleted when it changed between the scan and the cleanup.
func (s SQLiteStartupInventory) ScanAndClean(ctx context.Context) (StartupInventory, error) {
	var result StartupInventory
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	if strings.TrimSpace(s.DatabasePath) == "" {
		return result, fmt.Errorf("90CN startup inventory database path is required")
	}
	prefix := strings.TrimSpace(s.AccountPrefix)
	if prefix == "" {
		return result, fmt.Errorf("90CN startup inventory account prefix is required")
	}
	db, err := sql.Open("sqlite", s.DatabasePath)
	if err != nil {
		return result, fmt.Errorf("open 90CN startup inventory: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	conn, err := db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("connect 90CN startup inventory: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return result, fmt.Errorf("configure 90CN startup inventory: %w", err)
	}

	// Phase 1: read-only scan.
	accounts, err := s.readOwnedAccounts(ctx, conn, prefix)
	if err != nil {
		return result, err
	}
	result.ScannedAccounts = len(accounts)
	invalidAccounts := make([]startupAccount, 0)
	sweepAccounts := make([]startupAccount, 0)
	for _, account := range accounts {
		if len(account.characters) != 1 || !s.characterCompliant(account.characters[0]) {
			invalidAccounts = append(invalidAccounts, account)
			continue
		}
		character := account.characters[0]
		robot := robotcap.Info{
			UID: account.uid, CID: parseCharacterID(character.id), Name: character.name,
			Level: character.level, Job: character.job, Grow: character.grow,
			Village: character.village, Area: character.area, X: character.x, Y: character.y,
		}
		slot := uint16(character.slot)
		result.Robots = append(result.Robots, robot)
		result.Identities = append(result.Identities, robotstate.Identity{
			Backend: BackendID, Account: account.name, CharacterName: character.name, Slot: &slot,
		})
		if len(account.tombstoned) > 0 {
			sweepAccounts = append(sweepAccounts, account)
		}
	}

	// Phase 2: short write transaction.
	if len(invalidAccounts) > 0 || len(sweepAccounts) > 0 {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return result, fmt.Errorf("begin 90CN startup inventory: %w", err)
		}
		toDelete, deletedCharacters, err := s.verifyInvalidAccounts(ctx, tx, invalidAccounts)
		if err != nil {
			_ = tx.Rollback()
			return result, err
		}
		for index, account := range toDelete {
			if index >= startupDeleteLogLimit {
				break
			}
			reason := "incomplete account"
			if len(account.characters) == 1 {
				reason = "character non-compliant"
			}
			foundationlog.Robotf("STARTUP_INVENTORY_DELETE account=%s uid=%d characters=%d reason=%s\n",
				account.name, account.uid, len(account.characters), reason)
		}
		if omitted := len(toDelete) - startupDeleteLogLimit; omitted > 0 {
			foundationlog.Robotf("STARTUP_INVENTORY_DELETE_MORE accounts=%d\n", omitted)
		}
		for _, account := range toDelete {
			if err := deleteAccountRows(ctx, tx, account.name); err != nil {
				_ = tx.Rollback()
				return result, err
			}
		}
		sweptCharacters, err := s.sweepTombstonedCharacters(ctx, tx, sweepAccounts, toDelete)
		if err != nil {
			_ = tx.Rollback()
			return result, err
		}
		if err := tx.Commit(); err != nil {
			return result, fmt.Errorf("commit 90CN startup inventory: %w", err)
		}
		result.DeletedAccounts = len(toDelete)
		result.DeletedCharacters = deletedCharacters
		result.SweptCharacters = sweptCharacters
	}
	sort.Slice(result.Robots, func(i, j int) bool { return result.Robots[i].UID < result.Robots[j].UID })
	sort.Slice(result.Identities, func(i, j int) bool { return result.Identities[i].Account < result.Identities[j].Account })
	return result, nil
}

func parseCharacterID(value string) int {
	id, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// readOwnedAccounts reads every account that matches the strict robot identity
// (prefix + numeric UID inside the configured segment) together with its
// character rows. Live and soft-deleted rows are kept apart: only the live ones
// take part in the compliance decision, and the soft-deleted ids go to the
// sweep. Accounts without a live character are reported as incomplete so the
// cleanup phase can remove them.
func (s SQLiteStartupInventory) readOwnedAccounts(ctx context.Context, conn *sql.Conn, prefix string) ([]startupAccount, error) {
	byID := make(map[string]*startupAccount)
	accounts := make([]*startupAccount, 0)
	ensure := func(accountID string) (*startupAccount, bool) {
		uid, ok := robotUIDForAccount(accountID, prefix, s.Config.RobotUIDStart, s.Config.RobotUIDEnd)
		if !ok {
			return nil, false
		}
		if account, exists := byID[accountID]; exists {
			return account, true
		}
		account := &startupAccount{id: accountID, uid: uid, name: accountID}
		byID[accountID] = account
		accounts = append(accounts, account)
		return account, true
	}

	accountRows, err := conn.QueryContext(ctx, `SELECT account_id FROM `+dnfAccountsTable+` WHERE account_id LIKE ?`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("read 90CN robot accounts: %w", err)
	}
	for accountRows.Next() {
		var accountID string
		if err := accountRows.Scan(&accountID); err != nil {
			accountRows.Close()
			return nil, fmt.Errorf("scan 90CN robot account: %w", err)
		}
		ensure(accountID)
	}
	if err := accountRows.Close(); err != nil {
		return nil, fmt.Errorf("close 90CN robot account rows: %w", err)
	}

	characterRows, err := conn.QueryContext(ctx,
		`SELECT character_id, account_id, name, job, level, grow_type, slot, delete_flag, town_id, area_id, pos_x, pos_y FROM `+
			dnfCharactersTable+` WHERE account_id LIKE ? ORDER BY account_id, slot`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("read 90CN robot characters: %w", err)
	}
	defer characterRows.Close()
	for characterRows.Next() {
		var characterID, accountID, name, jobText string
		var character startupCharacter
		if err := characterRows.Scan(
			&characterID, &accountID, &name, &jobText, &character.level, &character.grow,
			&character.slot, &character.deleteFlag, &character.village, &character.area, &character.x, &character.y,
		); err != nil {
			return nil, fmt.Errorf("scan 90CN robot character: %w", err)
		}
		account, owned := ensure(accountID)
		if !owned {
			continue
		}
		character.id = characterID
		character.name = strings.TrimSpace(name)
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(jobText)); parseErr == nil {
			character.job = parsed
		}
		if character.deleteFlag != 0 {
			account.tombstoned = append(account.tombstoned, character.id)
			continue
		}
		account.characters = append(account.characters, character)
	}
	if err := characterRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate 90CN robot characters: %w", err)
	}

	out := make([]startupAccount, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, *account)
	}
	return out, nil
}

// robotUIDForAccount enforces the strict ownership rule: the account id must
// be exactly prefix + numeric UID inside the configured segment.
func robotUIDForAccount(accountID, prefix string, start, end int) (int, bool) {
	accountID = strings.TrimSpace(accountID)
	if !strings.HasPrefix(accountID, prefix) {
		return 0, false
	}
	uidText := strings.TrimPrefix(accountID, prefix)
	uid, err := strconv.Atoi(uidText)
	if err != nil {
		return 0, false
	}
	if uid < start || uid > end {
		return 0, false
	}
	return uid, true
}

// characterCompliant reports whether one active character is a usable robot at
// this stage: non-empty name, configured level window and a configured job.
func (s SQLiteStartupInventory) characterCompliant(character startupCharacter) bool {
	if character.deleteFlag != 0 {
		return false
	}
	if strings.TrimSpace(character.name) == "" || character.id == "" {
		return false
	}
	if character.level < s.Config.LevelMin || character.level > s.Config.LevelMax {
		return false
	}
	if !jobAllowed(s.Config.Jobs, character.job) {
		return false
	}
	return true
}

func jobAllowed(jobs []int, job int) bool {
	if len(jobs) == 0 {
		return true
	}
	for _, allowed := range jobs {
		if allowed == job {
			return true
		}
	}
	return false
}

// startupQueryer is implemented by both *sql.Conn and *sql.Tx so the scan can
// reuse one read helper before and inside the cleanup transaction.
type startupQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// verifyInvalidAccounts re-checks each candidate inside the cleanup
// transaction; a candidate that changed since the scan is skipped instead of
// deleted.
func (s SQLiteStartupInventory) verifyInvalidAccounts(ctx context.Context, queryer startupQueryer, candidates []startupAccount) ([]startupAccount, int, error) {
	toDelete := make([]startupAccount, 0, len(candidates))
	deletedCharacters := 0
	for _, account := range candidates {
		current, err := readStartupAccountState(ctx, queryer, account.name)
		if err != nil {
			return nil, 0, err
		}
		active, tombstoned := splitTombstoned(current)
		if !sameStartupCharacters(account.characters, active) || !sameStartupIDs(account.tombstoned, tombstoned) {
			foundationlog.Robotf("STARTUP_INVENTORY_SKIP_CHANGED account=%s reason=candidate changed during startup scan\n", account.name)
			continue
		}
		if len(active) == 1 && s.characterCompliant(active[0]) {
			foundationlog.Robotf("STARTUP_INVENTORY_SKIP_CHANGED account=%s reason=candidate is now compliant\n", account.name)
			continue
		}
		toDelete = append(toDelete, account)
		deletedCharacters += len(current)
	}
	return toDelete, deletedCharacters, nil
}

// sweepTombstonedCharacters removes the dependent rows of a soft-deleted
// character and leaves its dnf_characters row, and with it the name, in place.
// The rows are re-read inside the transaction and a character is swept only
// while its row is still flagged deleted, so one the game server restored
// between the scan and the cleanup keeps its data. Accounts that are being
// removed entirely are skipped: their rows go with them.
func (s SQLiteStartupInventory) sweepTombstonedCharacters(ctx context.Context, tx *sql.Tx, candidates, deleting []startupAccount) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	removed := make(map[string]bool, len(deleting))
	for _, account := range deleting {
		removed[account.name] = true
	}
	existing, err := existingTables(ctx, tx)
	if err != nil {
		return 0, err
	}
	swept := 0
	reported := 0
	for _, account := range candidates {
		if removed[account.name] {
			continue
		}
		current, err := readStartupAccountState(ctx, tx, account.name)
		if err != nil {
			return 0, err
		}
		_, tombstoned := splitTombstoned(current)
		for _, characterID := range tombstoned {
			if err := sweepCharacterChildRows(ctx, tx, existing, characterID); err != nil {
				return 0, err
			}
			if reported < startupDeleteLogLimit {
				foundationlog.Robotf("STARTUP_INVENTORY_SWEEP account=%s character=%s\n", account.name, characterID)
				reported++
			}
			swept++
		}
	}
	if omitted := swept - reported; omitted > 0 {
		foundationlog.Robotf("STARTUP_INVENTORY_SWEEP_MORE characters=%d\n", omitted)
	}
	return swept, nil
}

func readStartupAccountState(ctx context.Context, queryer startupQueryer, accountID string) ([]startupCharacter, error) {
	rows, err := queryer.QueryContext(ctx,
		`SELECT character_id, name, job, level, grow_type, slot, delete_flag FROM `+
			dnfCharactersTable+` WHERE account_id=? ORDER BY slot`, accountID)
	if err != nil {
		return nil, fmt.Errorf("re-read 90CN account %s: %w", accountID, err)
	}
	defer rows.Close()
	characters := make([]startupCharacter, 0, 2)
	for rows.Next() {
		var characterID, name, jobText string
		var character startupCharacter
		if err := rows.Scan(&characterID, &name, &jobText, &character.level, &character.grow, &character.slot, &character.deleteFlag); err != nil {
			return nil, fmt.Errorf("re-scan 90CN account %s character: %w", accountID, err)
		}
		character.id = characterID
		character.name = strings.TrimSpace(name)
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(jobText)); parseErr == nil {
			character.job = parsed
		}
		characters = append(characters, character)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("re-iterate 90CN account %s characters: %w", accountID, err)
	}
	return characters, nil
}

// splitTombstoned separates the rows the game server soft-deleted from the live
// ones, keeping each group in the slot order the query returned.
func splitTombstoned(characters []startupCharacter) (active []startupCharacter, tombstoned []string) {
	active = make([]startupCharacter, 0, len(characters))
	for _, character := range characters {
		if character.deleteFlag != 0 {
			tombstoned = append(tombstoned, character.id)
			continue
		}
		active = append(active, character)
	}
	return active, tombstoned
}

func sameStartupCharacters(before, after []startupCharacter) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index].id != after[index].id ||
			before[index].level != after[index].level ||
			before[index].job != after[index].job ||
			before[index].grow != after[index].grow ||
			before[index].deleteFlag != after[index].deleteFlag {
			return false
		}
	}
	return true
}

func sameStartupIDs(before, after []string) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index] != after[index] {
			return false
		}
	}
	return true
}
