package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

// mergeTownNeedQuests extends the completed gate set with every town area's
// [need quest] requirement so a robot can be placed in any spawn area the map
// catalog offers. A quest that a town area requires but the gate set marks
// active is promoted to completed: one quest row cannot be both.
func mergeTownNeedQuests(gates capabilitypvf.QuestGates, maps []shared.MapCatalogItem) capabilitypvf.QuestGates {
	completed := make(map[int]struct{}, len(gates.CompletedQuestIDs))
	for _, questID := range gates.CompletedQuestIDs {
		completed[questID] = struct{}{}
	}
	required := make(map[int]struct{}, 8)
	for _, item := range maps {
		for _, questID := range item.NeedQuests {
			if questID > 0 {
				required[questID] = struct{}{}
			}
		}
	}
	active := make([]int, 0, len(gates.ActiveQuestIDs))
	for _, questID := range gates.ActiveQuestIDs {
		if _, needed := required[questID]; needed {
			continue
		}
		active = append(active, questID)
	}
	gates.ActiveQuestIDs = active
	for questID := range required {
		if _, exists := completed[questID]; exists {
			continue
		}
		completed[questID] = struct{}{}
		gates.CompletedQuestIDs = append(gates.CompletedQuestIDs, questID)
	}
	return gates
}

// SeedRobotQuestGates rewrites each robot's quest state so no quest-based
// dungeon gate or town-area requirement can block it: accumulated quest rows
// are cleared and replaced with the fixed gate set (persistent gates
// completed, in-progress and quest connection gates active). Already matching
// robots are left untouched.
func SeedRobotQuestGates(ctx context.Context, databasePath string, robots []robotcap.Info, gates capabilitypvf.QuestGates) (int, error) {
	if strings.TrimSpace(databasePath) == "" || len(robots) == 0 || gates.Empty() {
		return 0, nil
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return 0, fmt.Errorf("open 90CN quest gate database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if err := applySQLiteWritePragmas(ctx, db); err != nil {
		return 0, fmt.Errorf("configure 90CN quest gate database: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin 90CN quest gate seed: %w", err)
	}
	defer tx.Rollback()
	changedRobots := 0
	for index := range robots {
		if robots[index].CID <= 0 {
			continue
		}
		changed, err := applyQuestGatesTx(ctx, tx, strconv.Itoa(robots[index].CID), gates)
		if err != nil {
			return changedRobots, err
		}
		if changed {
			changedRobots++
		}
	}
	if changedRobots == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit 90CN quest gate seed: %w", err)
	}
	return changedRobots, nil
}

// applyQuestGates makes the character's quest rows hold exactly the gate set.
func applyQuestGates(ctx context.Context, db *sql.DB, characterID string, gates capabilitypvf.QuestGates) error {
	if gates.Empty() {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed, err := applyQuestGatesTx(ctx, tx, characterID, gates)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return tx.Commit()
}

// applyQuestGatesTx replaces the character's quest state with the fixed gate
// set and reports whether any row changed. The server reads every row for
// admission and maze selection, while the fixed 30-slot client projection is
// filled by the server's own presentation planner, so row overflow neither
// blocks entry nor floods the client.
func applyQuestGatesTx(ctx context.Context, tx *sql.Tx, characterID string, gates capabilitypvf.QuestGates) (bool, error) {
	if gates.Empty() {
		return false, nil
	}
	satisfied, err := questGatesSatisfied(ctx, tx, characterID, gates)
	if err != nil {
		return false, err
	}
	if satisfied {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dnf_quest_state_extra WHERE character_id=?`, characterID); err != nil {
		return false, fmt.Errorf("clear 90CN quest extras id=%s: %w", characterID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dnf_quest_states WHERE character_id=?`, characterID); err != nil {
		return false, fmt.Errorf("clear 90CN quest states id=%s: %w", characterID, err)
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO dnf_quests (character_id, updated_at) VALUES (?, ?) ON CONFLICT(character_id) DO UPDATE SET updated_at=excluded.updated_at`,
		characterID, now); err != nil {
		return false, fmt.Errorf("upsert 90CN quest parent id=%s: %w", characterID, err)
	}
	insert := func(questID int, status string) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO dnf_quest_states (character_id, state_group, quest_id, status, trigger_type, progress_value, reward_select_index, multiplier, state_updated_at)
			 VALUES (?, 'states', ?, ?, 0, 0, 0, 0, ?)`,
			characterID, questID, status, now); err != nil {
			return fmt.Errorf("seed 90CN quest state id=%s quest=%d status=%s: %w", characterID, questID, status, err)
		}
		return nil
	}
	completed := make(map[int]struct{}, len(gates.CompletedQuestIDs))
	for _, questID := range gates.CompletedQuestIDs {
		completed[questID] = struct{}{}
		if err := insert(questID, "completed"); err != nil {
			return false, err
		}
	}
	for _, questID := range gates.ActiveQuestIDs {
		if _, isCompleted := completed[questID]; isCompleted {
			continue
		}
		if err := insert(questID, "active"); err != nil {
			return false, err
		}
	}
	return true, nil
}

func questGatesSatisfied(ctx context.Context, tx *sql.Tx, characterID string, gates capabilitypvf.QuestGates) (bool, error) {
	completed, err := queryQuestIDSet(ctx, tx,
		`SELECT quest_id FROM dnf_quest_states WHERE character_id=? AND state_group='states' AND lower(status) IN ('complete','completed','cleared','finished','done')`,
		characterID)
	if err != nil {
		return false, fmt.Errorf("read 90CN quest completions id=%s: %w", characterID, err)
	}
	active, err := queryQuestIDSet(ctx, tx,
		`SELECT quest_id FROM dnf_quest_states WHERE character_id=? AND state_group='states' AND lower(status)='active'`,
		characterID)
	if err != nil {
		return false, fmt.Errorf("read 90CN active quests id=%s: %w", characterID, err)
	}
	return sameIDSet(completed, gates.CompletedQuestIDs) && sameIDSet(active, gates.ActiveQuestIDs), nil
}

func queryQuestIDSet(ctx context.Context, tx *sql.Tx, query string, args ...any) (map[int]struct{}, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]struct{})
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func sameIDSet(existing map[int]struct{}, want []int) bool {
	if len(existing) != len(want) {
		return false
	}
	for _, id := range want {
		if _, ok := existing[id]; !ok {
			return false
		}
	}
	return true
}
