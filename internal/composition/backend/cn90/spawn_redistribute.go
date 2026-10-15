package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"strings"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	"robot/internal/shared"
)

// RedistributeRobotSpawns rewrites the persisted town/area/position of every
// robot character so a fleet that was spawned with a fixed location spreads
// across the configured village's eligible areas. It must run while the
// characters are offline: the server owns the live location and would
// overwrite an online write on its next save.
func RedistributeRobotSpawns(ctx context.Context, databasePath, pvfPath, accountPrefix string, rc robotconfig.RuntimeConfig) (int, error) {
	maps, err := ReadTownMapCatalog(pvfPath)
	if err != nil {
		return 0, err
	}
	return RedistributeRobotSpawnsWithMaps(ctx, databasePath, accountPrefix, rc, maps, []int{rc.SpawnVillage})
}

// RedistributeRobotSpawnsToVillages spreads the fleet across the given towns
// (round robin over the character order) instead of one village. Each robot
// still starts at its home login route and reaches its assigned town through
// the portal request shape, which the transport now emits for cross-town moves.
func RedistributeRobotSpawnsToVillages(ctx context.Context, databasePath, pvfPath, accountPrefix string, rc robotconfig.RuntimeConfig, villages []int) (int, error) {
	maps, err := ReadTownMapCatalog(pvfPath)
	if err != nil {
		return 0, err
	}
	return RedistributeRobotSpawnsWithMaps(ctx, databasePath, accountPrefix, rc, maps, villages)
}

// RedistributeRobotSpawnsWithMaps is the map-injected form used by tests.
func RedistributeRobotSpawnsWithMaps(ctx context.Context, databasePath, accountPrefix string, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem, villages []int) (int, error) {
	prefix := strings.TrimSpace(accountPrefix)
	if prefix == "" {
		return 0, fmt.Errorf("90CN spawn redistribution account prefix is required")
	}
	targets := make([]int, 0, len(villages))
	for _, village := range villages {
		if village > 0 {
			targets = append(targets, village)
		}
	}
	if len(targets) == 0 {
		if rc.SpawnVillage <= 0 {
			return 0, fmt.Errorf("90CN spawn redistribution requires at least one target village")
		}
		targets = []int{rc.SpawnVillage}
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return 0, fmt.Errorf("open 90CN spawn redistribution database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if err := applySQLiteWritePragmasNoForeignKeys(ctx, db); err != nil {
		return 0, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT character_id FROM dnf_characters WHERE account_id LIKE ? AND delete_flag=0 ORDER BY character_id`,
		prefix+"%")
	if err != nil {
		return 0, fmt.Errorf("read 90CN robot characters: %w", err)
	}
	characterIDs := make([]string, 0, 2048)
	for rows.Next() {
		var characterID string
		if err := rows.Scan(&characterID); err != nil {
			rows.Close()
			return 0, err
		}
		characterIDs = append(characterIDs, characterID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	source := rand.New(rand.NewSource(0x90C0))
	env := spawnDistributionEnv{randBetween: source.Intn, randIntn: source.Intn}
	updated := 0
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for index, characterID := range characterIDs {
		info := robotcap.Info{Level: rc.LevelMax}
		robotspawn.ApplyVillageLocation(env, &info, targets[index%len(targets)], rc, maps)
		if info.Village <= 0 || info.Area < 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE dnf_characters SET town_id=?, area_id=?, pos_x=?, pos_y=? WHERE character_id=?`,
			info.Village, info.Area, info.X, info.Y, characterID); err != nil {
			return updated, fmt.Errorf("redistribute 90CN character %s: %w", characterID, err)
		}
		updated++
	}
	if err := tx.Commit(); err != nil {
		return updated, fmt.Errorf("commit 90CN spawn redistribution: %w", err)
	}
	return updated, nil
}

type spawnDistributionEnv struct {
	randBetween func(int) int
	randIntn    func(int) int
}

func (e spawnDistributionEnv) FollowAccountVillage(string) (int, bool, error) {
	return 0, false, nil
}

func (e spawnDistributionEnv) RandBetween(min, max int) int {
	if max < min {
		min, max = max, min
	}
	if e.randBetween == nil || max <= min {
		return min
	}
	return min + e.randBetween(max-min+1)
}

func (e spawnDistributionEnv) RandIntn(n int) int {
	if e.randIntn == nil || n <= 0 {
		return 0
	}
	return e.randIntn(n)
}
