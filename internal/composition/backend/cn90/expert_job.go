package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// DNF90 expert-job profession state. The server reads both values from
// dnf_character_stats; the store's machine endurance stays server-owned (the
// PVF default applies while the stat is absent, and the server writes
// consumption back into the same table).
const (
	cn90ExpertJobEnchanterType  = 1
	cn90ExpertJobDisjointerType = 3

	// cn90ExpertJobExperience is written for both professions. A high value
	// reaches the top experience level, which unlocks every qualification the
	// PVF defines for the stall owner.
	cn90ExpertJobExperience = int64(1_000_000_000)

	// cn90DisjointMachineGradeKey / cn90DisjointMachineGrade give every
	// disassembler the top machine grade: dungeon-entrance stalls are the only
	// place the fleet shows a disassembler, and a LV11 machine is what a live
	// player expects to find there. Without the stat the server keeps the PVF
	// default, grade 1.
	cn90DisjointMachineGradeKey = "expert_job_machine_grade"
	cn90DisjointMachineGrade    = int64(11)
)

// cn90ExpertJobStat is one profession row written while the account is offline.
type cn90ExpertJobStat struct {
	key   string
	value int64
}

// cn90ExpertJobStats is the row set for one profession.
func cn90ExpertJobStats(jobType int) []cn90ExpertJobStat {
	stats := []cn90ExpertJobStat{
		{key: "expert_job_type", value: int64(jobType)},
		{key: "expert_job_exp", value: cn90ExpertJobExperience},
	}
	if jobType == cn90ExpertJobDisjointerType {
		stats = append(stats, cn90ExpertJobStat{key: cn90DisjointMachineGradeKey, value: cn90DisjointMachineGrade})
	}
	return stats
}

// EnsureDisjointProfession prepares the disassembler machine qualification.
func (a *SQLiteLoadoutApplier) EnsureDisjointProfession(cid int) error {
	return a.ensureExpertJobProfession(cid, cn90ExpertJobDisjointerType)
}

// DisjointProfessionReady reports whether the character already carries the
// disassembler qualification.
func (a *SQLiteLoadoutApplier) DisjointProfessionReady(cid int) (bool, error) {
	return a.expertJobProfessionReady(cid, cn90ExpertJobDisjointerType)
}

// EnsureEnchantProfession prepares the enchanter qualification.
func (a *SQLiteLoadoutApplier) EnsureEnchantProfession(cid int) error {
	return a.ensureExpertJobProfession(cid, cn90ExpertJobEnchanterType)
}

// EnchantProfessionReady reports whether the character already carries the
// enchanter qualification.
func (a *SQLiteLoadoutApplier) EnchantProfessionReady(cid int) (bool, error) {
	return a.expertJobProfessionReady(cid, cn90ExpertJobEnchanterType)
}

func (a *SQLiteLoadoutApplier) ensureExpertJobProfession(cid int, jobType int) error {
	if cid <= 0 {
		return fmt.Errorf("90CN expert job character id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cn90PersistenceTimeout)
	defer cancel()
	return a.persistenceDo(ctx, func(jobCtx context.Context) error {
		db, release, err := a.database(jobCtx)
		if err != nil {
			return fmt.Errorf("open 90CN expert job database: %w", err)
		}
		defer release()
		tx, err := db.BeginTx(jobCtx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		characterID := fmt.Sprint(cid)
		for _, stat := range cn90ExpertJobStats(jobType) {
			if _, err := tx.ExecContext(jobCtx,
				`INSERT INTO `+dnfCharacterStatsTable+` (character_id, stat_key, stat_value) VALUES (?, ?, ?)
				 ON CONFLICT(character_id, stat_key) DO UPDATE SET stat_value=excluded.stat_value`,
				characterID, stat.key, stat.value); err != nil {
				return fmt.Errorf("write 90CN expert job stat %s character=%s: %w", stat.key, characterID, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit 90CN expert job character=%s: %w", characterID, err)
		}
		return nil
	})
}

func (a *SQLiteLoadoutApplier) expertJobProfessionReady(cid int, jobType int) (bool, error) {
	if cid <= 0 {
		return false, fmt.Errorf("90CN expert job character id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cn90PersistenceTimeout)
	defer cancel()
	db, release, err := a.database(ctx)
	if err != nil {
		return false, fmt.Errorf("open 90CN expert job database: %w", err)
	}
	defer release()
	var value int64
	err = db.QueryRowContext(ctx,
		`SELECT stat_value FROM `+dnfCharacterStatsTable+` WHERE character_id=? AND stat_key='expert_job_type'`,
		fmt.Sprint(cid)).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read 90CN expert job character=%d: %w", cid, err)
	}
	if int(value) != jobType {
		return false, nil
	}
	if jobType != cn90ExpertJobDisjointerType {
		return true, nil
	}
	// A disassembler also needs the crowd-rules machine grade. Robots prepared
	// before the grade was written report not-ready once more, which runs the
	// offline cycle and upgrades the machine in place.
	var grade int64
	err = db.QueryRowContext(ctx,
		`SELECT stat_value FROM `+dnfCharacterStatsTable+` WHERE character_id=? AND stat_key=?`,
		fmt.Sprint(cid), cn90DisjointMachineGradeKey).Scan(&grade)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read 90CN expert job machine grade character=%d: %w", cid, err)
	}
	return grade >= cn90DisjointMachineGrade, nil
}

// cn90ExpertJobStatKey sanitizes a key for logs and diagnostics.
func cn90ExpertJobStatKey(key string) string {
	return strings.TrimSpace(strings.ToLower(key))
}
