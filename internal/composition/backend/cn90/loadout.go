package cn90

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"

	_ "modernc.org/sqlite"
)

const cn90PersistenceTimeout = 15 * time.Second

// DNF90 native persistence tables. The server prefixes every repository table
// with its configured table_prefix ("dnf" in the shipped profile); the legacy
// C# mirrors are dnf_legacy_* and are only read as fallbacks.
const (
	dnfCharactersTable      = "dnf_characters"
	dnfCharacterStatsTable  = "dnf_character_stats"
	dnfAccountsTable        = "dnf_accounts"
	dnfAccountMetadataTable = "dnf_account_metadata"
)

type CharacterLoadoutApplier interface {
	ApplyCharacterLoadout(context.Context, string, robotcap.Info) error
}

type CharacterProfileReader interface {
	ResolveCharacterProfile(context.Context, string, robotcap.Info) (robotcap.Info, error)
}

type CharacterProfileAdapter interface {
	CharacterProfileReader
	ApplyPlannedCharacterLevel(context.Context, string, robotcap.Info, int) (robotcap.Info, error)
}

type CharacterInitializer interface {
	InitializeCharacter(context.Context, string, robotcap.Info, int, int) (robotcap.Info, error)
}

// SQLiteLoadoutApplier owns the 90CN robot character progression writes. The
// first persistence stage covers level/experience/grow: equipment, avatars and
// pets are delivered by the server's own character initialization defaults and
// their robot-side generation is a later stage.
type SQLiteLoadoutApplier struct {
	DatabasePath    string
	Config          robotconfig.RuntimeConfig
	LevelThresholds []int
	Equipment       []shared.EquipmentCatalogItem
	QuestGates      capabilitypvf.QuestGates
	PVFPath         string
	RandIntn        func(int) int
	db              *sql.DB
	archive         *cn90PVFArchive
	archiveTried    bool
	nativeTypes     map[string]equipmentTypeInfo
	executorMu      lockhub.Locker
	executor        *persistenceExecutor
	closed          bool
}

func NewSQLiteLoadoutApplier(ctx context.Context, databasePath string, config robotconfig.RuntimeConfig, equipment []shared.EquipmentCatalogItem, pvfPath string, randIntn func(int) int) (*SQLiteLoadoutApplier, error) {
	if strings.TrimSpace(databasePath) == "" {
		return nil, fmt.Errorf("90CN loadout database path is required")
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open 90CN loadout database: %w", err)
	}
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure 90CN loadout database: %w", err)
	}
	if err := validateLoadoutSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteLoadoutApplier{
		DatabasePath: databasePath, Config: config,
		Equipment: equipment, PVFPath: pvfPath, RandIntn: randIntn,
		db: db, executor: newPersistenceExecutor(cn90PersistenceQueueSize),
	}, nil
}

func (a *SQLiteLoadoutApplier) Close() error {
	if a == nil {
		return nil
	}
	a.executorMu.Lock()
	executor := a.executor
	a.executor = nil
	a.closed = true
	a.executorMu.Unlock()
	executor.Close()
	if a.db == nil {
		return nil
	}
	err := a.db.Close()
	a.db = nil
	return err
}

func (a *SQLiteLoadoutApplier) persistenceDo(ctx context.Context, run func(context.Context) error) error {
	if a == nil {
		return fmt.Errorf("90CN loadout applier is nil")
	}
	a.executorMu.Lock()
	if a.closed {
		a.executorMu.Unlock()
		return fmt.Errorf("90CN loadout applier is closed")
	}
	executor := a.executor
	a.executorMu.Unlock()
	if executor == nil {
		return run(ctx)
	}
	return executor.Do(ctx, run)
}

func (a *SQLiteLoadoutApplier) database(ctx context.Context) (*sql.DB, func(), error) {
	if a.db != nil {
		return a.db, func() {}, nil
	}
	db, err := sql.Open("sqlite", a.DatabasePath)
	if err != nil {
		return nil, nil, err
	}
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, func() { _ = db.Close() }, nil
}

// configureSQLitePool keeps every adapter-owned connection bounded to one
// writer/reader. The 90CN server may hold the same file open, so creating a
// pool per actor would only increase lock contention and SQLITE_BUSY retries.
func configureSQLitePool(db *sql.DB) {
	if db == nil {
		return
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
}

// ApplyCharacterLoadout regenerates the account's worn equipment and avatars.
// It is the explicit loadout path for adopted characters; fresh characters use
// InitializeCharacter.
func (a *SQLiteLoadoutApplier) ApplyCharacterLoadout(ctx context.Context, account string, info robotcap.Info) error {
	return a.applyAccountLoadout(ctx, account, info)
}

// ReconcileRobotLoadouts gives robots without any worn rows their generated
// loadout. Robots that already wear equipment are left untouched so operator
// edits survive restarts.
func (a *SQLiteLoadoutApplier) ReconcileRobotLoadouts(ctx context.Context, accountPrefix string, robots []robotcap.Info) (int, error) {
	if a == nil || len(robots) == 0 {
		return 0, nil
	}
	prefix := strings.TrimSpace(accountPrefix)
	if prefix == "" {
		return 0, fmt.Errorf("90CN loadout reconcile account prefix is required")
	}
	replaced := 0
	for index := range robots {
		info := robots[index]
		if info.UID <= 0 || strings.TrimSpace(info.Name) == "" {
			continue
		}
		account := prefix + strconv.Itoa(info.UID)
		needs, err := a.equipmentNeedsLoadout(ctx, account)
		if err != nil {
			return replaced, err
		}
		if !needs {
			continue
		}
		if err := a.applyAccountLoadout(ctx, account, info); err != nil {
			return replaced, fmt.Errorf("reconcile 90CN loadout uid=%d: %w", info.UID, err)
		}
		replaced++
	}
	return replaced, nil
}

// equipmentNeedsLoadout reports whether the account's active character still
// wears only server starter rows (or nothing): generated rows carry the
// robot_loadout marker, so any marked row means the loadout was already
// applied and operator edits must survive. When pets are enabled the creature
// state is checked the same way so a character created before the pet stage is
// upgraded on the next startup, and a creature row written before the GBK name
// encoding is re-applied so its name_raw stops overflowing the server limit.
func (a *SQLiteLoadoutApplier) equipmentNeedsLoadout(ctx context.Context, account string) (bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	db, release, err := a.database(readCtx)
	if err != nil {
		return false, fmt.Errorf("open 90CN loadout database: %w", err)
	}
	defer release()
	row, err := loadCharacterProfileRow(readCtx, db, account)
	if err != nil {
		return false, err
	}
	var marked int
	if err := db.QueryRowContext(readCtx,
		`SELECT COUNT(*) FROM `+dnfEquipmentExtraTable+` WHERE character_id=? AND extra_key='robot_loadout'`,
		row.characterID).Scan(&marked); err != nil {
		return false, fmt.Errorf("count 90CN generated equipment character=%s: %w", row.characterID, err)
	}
	if marked == 0 {
		return true, nil
	}
	if !a.Config.PetEnabled {
		return false, nil
	}
	if a.Config.PetProbabilityPercent < 100 && cn90DeterministicPercent(row.characterID) >= a.Config.PetProbabilityPercent {
		return false, nil
	}
	var petMarked int
	if err := db.QueryRowContext(readCtx,
		`SELECT COUNT(*) FROM `+dnfPetEntryExtraTable+` WHERE character_id=? AND extra_key='robot_loadout'`,
		row.characterID).Scan(&petMarked); err != nil {
		return false, fmt.Errorf("count 90CN generated pet character=%s: %w", row.characterID, err)
	}
	if petMarked == 0 {
		return true, nil
	}
	// A marked creature entry with a name but no name_raw was written before the
	// GBK name encoding landed: the server then falls back to the UTF-8 bytes of
	// pet_name and refuses the state at its 30-byte limit, keeping the character
	// offline. Re-apply once to repair such a row.
	var nameless int
	if err := db.QueryRowContext(readCtx,
		`SELECT COUNT(*) FROM `+dnfPetEntriesTable+` WHERE character_id=? AND name_raw IS NULL AND pet_name <> ''`,
		row.characterID).Scan(&nameless); err != nil {
		return false, fmt.Errorf("count 90CN creature names character=%s: %w", row.characterID, err)
	}
	return nameless > 0, nil
}

// ResolveCharacterProfile reads the robot account's active character row.
func (a *SQLiteLoadoutApplier) ResolveCharacterProfile(ctx context.Context, account string, info robotcap.Info) (robotcap.Info, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return info, fmt.Errorf("90CN character profile account is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	db, release, err := a.database(ctx)
	if err != nil {
		return info, fmt.Errorf("open 90CN character profile database: %w", err)
	}
	defer release()
	row, err := loadCharacterProfileRow(ctx, db, account)
	if err != nil {
		return info, err
	}
	return row.apply(info), nil
}

// ApplyPlannedCharacterLevel writes the configured level and keeps the stored
// grow type.
func (a *SQLiteLoadoutApplier) ApplyPlannedCharacterLevel(ctx context.Context, account string, info robotcap.Info, level int) (robotcap.Info, error) {
	return a.writeProgression(ctx, account, info, level, info.Grow)
}

// InitializeCharacter writes the planned level and grow type for a freshly
// created character, marks the dungeon tutorial as completed (the DNF90 server
// otherwise routes a fresh character into the tutorial preview instead of the
// town scene) and applies the generated equipment/avatar loadout.
func (a *SQLiteLoadoutApplier) InitializeCharacter(ctx context.Context, account string, info robotcap.Info, level, grow int) (robotcap.Info, error) {
	updated, err := a.writeProgression(ctx, account, info, level, grow)
	if err != nil {
		return updated, err
	}
	if err := a.markTutorialCompleted(ctx, account); err != nil {
		return updated, err
	}
	if err := a.applyAccountLoadout(ctx, account, updated); err != nil {
		return updated, err
	}
	if err := a.seedAccountQuestGates(ctx, account); err != nil {
		return updated, err
	}
	return updated, nil
}

// seedAccountQuestGates installs the fixed quest gate set for the account's
// character so quest-based town/dungeon gates cannot block it.
func (a *SQLiteLoadoutApplier) seedAccountQuestGates(ctx context.Context, account string) error {
	if a.QuestGates.Empty() {
		return nil
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return fmt.Errorf("90CN quest gate account is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	return a.persistenceDo(ctx, func(jobCtx context.Context) error {
		db, release, err := a.database(jobCtx)
		if err != nil {
			return fmt.Errorf("open 90CN quest gate database: %w", err)
		}
		defer release()
		row, err := loadCharacterProfileRow(jobCtx, db, account)
		if err != nil {
			return err
		}
		return applyQuestGates(jobCtx, db, row.characterID, a.QuestGates)
	})
}

// applyAccountLoadout resolves the account's character row and replaces its
// worn equipment, avatars and creature state.
func (a *SQLiteLoadoutApplier) applyAccountLoadout(ctx context.Context, account string, info robotcap.Info) error {
	account = strings.TrimSpace(account)
	if account == "" {
		return fmt.Errorf("90CN loadout account is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	return a.persistenceDo(ctx, func(jobCtx context.Context) error {
		db, release, err := a.database(jobCtx)
		if err != nil {
			return fmt.Errorf("open 90CN loadout database: %w", err)
		}
		defer release()
		row, err := loadCharacterProfileRow(jobCtx, db, account)
		if err != nil {
			return err
		}
		if row.level < 1 {
			row.level = 1
		}
		if err := a.applyRobotLoadout(jobCtx, db, row.characterID, row.job, row.level); err != nil {
			return err
		}
		return a.applyRobotPet(jobCtx, db, row.characterID)
	})
}

// markTutorialCompleted persists the same marker the server writes after the
// final tutorial checkpoint (dnf_characters.tutorial_completed=1).
func (a *SQLiteLoadoutApplier) markTutorialCompleted(ctx context.Context, account string) error {
	account = strings.TrimSpace(account)
	if account == "" {
		return fmt.Errorf("90CN tutorial marker account is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	return a.persistenceDo(ctx, func(jobCtx context.Context) error {
		db, release, err := a.database(jobCtx)
		if err != nil {
			return fmt.Errorf("open 90CN tutorial marker database: %w", err)
		}
		defer release()
		if _, err := db.ExecContext(jobCtx,
			`UPDATE `+dnfCharactersTable+` SET tutorial_completed=?, updated_at=? WHERE account_id=? AND delete_flag=0`,
			1, time.Now().UTC(), account); err != nil {
			return fmt.Errorf("mark 90CN character tutorial completed for %s: %w", account, err)
		}
		return nil
	})
}

func (a *SQLiteLoadoutApplier) writeProgression(ctx context.Context, account string, info robotcap.Info, level, grow int) (robotcap.Info, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return info, fmt.Errorf("90CN progression account is required")
	}
	if level < 1 {
		level = 1
	}
	if grow < 0 || grow > 0xFF {
		return info, fmt.Errorf("90CN progression grow %d is out of range", grow)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	err := a.persistenceDo(ctx, func(jobCtx context.Context) error {
		db, release, err := a.database(jobCtx)
		if err != nil {
			return fmt.Errorf("open 90CN progression database: %w", err)
		}
		defer release()
		return a.updateCharacterProgression(jobCtx, db, account, info, level, grow)
	})
	if err != nil {
		return info, err
	}
	info.Level = level
	info.Grow = grow
	if info.CID == 0 {
		if resolved, err := ResolveCharacterProfileViaTable(ctx, a, account, info); err == nil {
			info = resolved
		}
	}
	return info, nil
}

// ResolveCharacterProfileViaTable re-reads the row after a write so the caller
// receives the server-allocated character id and name.
func ResolveCharacterProfileViaTable(ctx context.Context, a *SQLiteLoadoutApplier, account string, info robotcap.Info) (robotcap.Info, error) {
	return a.ResolveCharacterProfile(ctx, account, info)
}

func (a *SQLiteLoadoutApplier) updateCharacterProgression(ctx context.Context, db *sql.DB, account string, info robotcap.Info, level, grow int) error {
	row, err := loadCharacterProfileRow(ctx, db, account)
	if err != nil {
		return err
	}
	exp := 0
	if level > 0 && level-1 < len(a.LevelThresholds) {
		exp = a.LevelThresholds[level-1]
	}
	name := strings.TrimSpace(info.Name)
	query := `UPDATE ` + dnfCharactersTable + ` SET level=?, exp=?, grow_type=?, updated_at=? WHERE character_id=?`
	if name != "" {
		query = `UPDATE ` + dnfCharactersTable + ` SET level=?, exp=?, grow_type=?, updated_at=?, name=? WHERE character_id=?`
		if _, err := db.ExecContext(ctx, query, level, exp, grow, time.Now().UTC(), name, row.characterID); err != nil {
			return fmt.Errorf("write 90CN progression for %s: %w", account, err)
		}
		return nil
	}
	if _, err := db.ExecContext(ctx, query, level, exp, grow, time.Now().UTC(), row.characterID); err != nil {
		return fmt.Errorf("write 90CN progression for %s: %w", account, err)
	}
	return nil
}

type characterProfileRow struct {
	characterID string
	name        string
	job         int
	level       int
	grow        int
	slot        int
	townID      int
	areaID      int
	posX        int
	posY        int
}

func (r characterProfileRow) apply(info robotcap.Info) robotcap.Info {
	info.Name = r.name
	info.Job = r.job
	info.Level = r.level
	info.Grow = r.grow
	if id, err := strconv.Atoi(strings.TrimSpace(r.characterID)); err == nil {
		info.CID = id
	}
	if r.townID > 0 {
		info.Village = r.townID
	}
	if r.areaID > 0 || r.townID > 0 {
		info.Area = r.areaID
	}
	info.X = r.posX
	info.Y = r.posY
	return info
}

func loadCharacterProfileRow(ctx context.Context, db *sql.DB, account string) (characterProfileRow, error) {
	var row characterProfileRow
	var job string
	query := `SELECT character_id, name, job, level, grow_type, slot, town_id, area_id, pos_x, pos_y FROM ` +
		dnfCharactersTable + ` WHERE account_id=? AND delete_flag=0 ORDER BY slot LIMIT 1`
	err := db.QueryRowContext(ctx, query, account).Scan(
		&row.characterID, &row.name, &job, &row.level, &row.grow, &row.slot, &row.townID, &row.areaID, &row.posX, &row.posY,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return characterProfileRow{}, fmt.Errorf("90CN account %s has no active character", account)
	}
	if err != nil {
		return characterProfileRow{}, fmt.Errorf("read 90CN character profile %s: %w", account, err)
	}
	if parsed, parseErr := strconv.Atoi(strings.TrimSpace(job)); parseErr == nil {
		row.job = parsed
	}
	return row, nil
}

// validateLoadoutSchema checks the native 90CN tables the robot writes to.
func validateLoadoutSchema(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{
		dnfCharactersTable, dnfCharacterStatsTable,
		"dnf_inventory_items", "dnf_equipment_entries", "dnf_pet_entries",
		"dnf_quest_states", "dnf_skill_states", "dnf_character_locations",
	} {
		var name string
		err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("90CN database is missing required table %s; start the DNF90 server once so it creates its schema", table)
		}
		if err != nil {
			return fmt.Errorf("validate 90CN database schema (%s): %w", table, err)
		}
	}
	return nil
}

var _ CharacterLoadoutApplier = (*SQLiteLoadoutApplier)(nil)
var _ CharacterProfileAdapter = (*SQLiteLoadoutApplier)(nil)
var _ CharacterInitializer = (*SQLiteLoadoutApplier)(nil)
