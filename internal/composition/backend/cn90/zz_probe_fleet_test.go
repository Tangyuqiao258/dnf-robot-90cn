package cn90

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	"robot/internal/shared"
)

// TestZZProbeFleetGrowth reports the live fleet's job and grow distribution and
// audits every live character's worn set against the rules the selector applies.
// It answers the questions the operator's report raises: the armor-material
// filter keys off the transfer branch in the low nibble of grow_type and
// ArmorMaterialsFor returns nil -- no restriction at all -- when a job has no
// fixed-material entry at branch 0, so whether that leaves anyone unrestricted
// depends on who actually sits at branch 0, which only the database can say.
//
// Enable with CN90_PROBE_FLEET=1.
func TestZZProbeFleetGrowth(t *testing.T) {
	if os.Getenv("CN90_PROBE_FLEET") == "" {
		t.Skip("set CN90_PROBE_FLEET=1")
	}
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	pvfPath, _, err := layout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := layout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	out := filepath.Join(os.TempDir(), "fleet_growth.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	type row struct {
		characterID, job, level, grow, deleted int
		account                                string
	}
	var all []row
	rows, err := db.Query(`SELECT character_id, account_id, job, level, grow_type, delete_flag FROM ` + dnfCharactersTable)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r row
		var id, job string
		if err := rows.Scan(&id, &r.account, &job, &r.level, &r.grow, &r.deleted); err != nil {
			t.Fatal(err)
		}
		r.characterID, _ = strconv.Atoi(id)
		r.job, _ = strconv.Atoi(job)
		all = append(all, r)
	}
	rows.Close()
	say("characters total=%d\n", len(all))

	counts := map[string]int{}
	for _, r := range all {
		state := "deleted"
		if r.deleted == 0 {
			state = "live"
		}
		counts[fmt.Sprintf("%s branch0=%v", state, r.grow&0x0f == 0)]++
	}
	say("character states: %v\n", counts)

	say("\njobs (live characters only), grow values seen:\n")
	liveByJob := map[int]map[int]int{}
	for _, r := range all {
		if r.deleted != 0 {
			continue
		}
		if liveByJob[r.job] == nil {
			liveByJob[r.job] = map[int]int{}
		}
		liveByJob[r.job][r.grow]++
	}
	jobs := make([]int, 0, len(liveByJob))
	for job := range liveByJob {
		jobs = append(jobs, job)
	}
	sort.Ints(jobs)
	for _, job := range jobs {
		keys := make([]int, 0, len(liveByJob[job]))
		for grow := range liveByJob[job] {
			keys = append(keys, grow)
		}
		sort.Ints(keys)
		parts := make([]string, 0, len(keys))
		for _, grow := range keys {
			parts = append(parts, fmt.Sprintf("grow=%d(branch=%d,stage=%d)x%d", grow, grow&0x0f, (grow>>4)&0x0f, liveByJob[job][grow]))
		}
		say("job %-3d live=%-4d %v\n", job, countLive(liveByJob[job]), parts)
	}

	say("\nlive characters per account prefix:\n")
	prefixes := map[string]int{}
	liveByPrefixBranch := map[string]map[int]int{}
	for _, r := range all {
		if r.deleted != 0 {
			continue
		}
		prefix := r.account
		if len(prefix) > 10 {
			prefix = prefix[:10]
		}
		prefixes[prefix]++
		if liveByPrefixBranch[prefix] == nil {
			liveByPrefixBranch[prefix] = map[int]int{}
		}
		liveByPrefixBranch[prefix][r.grow&0x0f]++
	}
	keys := make([]string, 0, len(prefixes))
	for key := range prefixes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		say("%-12s live=%-6d branch=%v\n", key, prefixes[key], liveByPrefixBranch[key])
	}

	say("\nArmorMaterialsFor per live (job, grow):\n")
	seen := map[string]bool{}
	for _, r := range all {
		if r.deleted != 0 {
			continue
		}
		key := fmt.Sprintf("job=%d grow=%d", r.job, r.grow)
		if seen[key] {
			continue
		}
		seen[key] = true
		materials := equipmentcap.ArmorMaterialsFor(r.job, r.grow)
		note := ""
		if len(materials) == 0 {
			note = "  <-- UNRESTRICTED"
		}
		say("%s -> %v%s\n", key, materials, note)
	}

	// Every live character's worn set, judged by the selector's own rules.
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int]shared.EquipmentCatalogItem, len(catalogs.Equipment))
	for _, item := range catalogs.Equipment {
		if _, exists := byID[item.ID]; !exists {
			byID[item.ID] = item
		}
	}
	wornByCharacter := map[int][]int{}
	wornRows, err := db.Query(`SELECT character_id, slot_index, item_id FROM ` + dnfEquipmentEntriesTable)
	if err != nil {
		t.Fatal(err)
	}
	for wornRows.Next() {
		var id string
		var slot, itemID int
		if err := wornRows.Scan(&id, &slot, &itemID); err != nil {
			t.Fatal(err)
		}
		characterID, convErr := strconv.Atoi(id)
		if convErr != nil || slot == cn90CreatureWornSlot {
			continue
		}
		wornByCharacter[characterID] = append(wornByCharacter[characterID], itemID)
	}
	wornRows.Close()

	say("\n=== worn-set audit (live characters) ===\n")
	totals := struct{ jobBad, materialBad, rarity5, blocked, unknown, total int }{}
	for _, r := range all {
		if r.deleted != 0 {
			continue
		}
		materials := equipmentcap.ArmorMaterialsFor(r.job, r.grow)
		var jobBad, materialBad, rarity5, blocked, unknown int
		items := wornByCharacter[r.characterID]
		for _, itemID := range items {
			item, known := byID[itemID]
			if !known {
				unknown++
				continue
			}
			if item.Rarity == 5 {
				rarity5++
			}
			if item.GrantBlocked {
				blocked++
			}
			if item.ItemType >= 1 && item.ItemType <= 13 && !equipmentcap.UsableByJob(item.UseJob, r.job) {
				jobBad++
			}
			if item.ItemType >= 3 && item.ItemType <= 7 {
				material := equipmentcap.ArmorMaterialOfPath(item.Path)
				allowed := false
				for _, candidate := range materials {
					if candidate == material {
						allowed = true
					}
				}
				if !allowed {
					materialBad++
				}
			}
		}
		totals.total += len(items)
		totals.jobBad += jobBad
		totals.materialBad += materialBad
		totals.rarity5 += rarity5
		totals.blocked += blocked
		totals.unknown += unknown
		if jobBad+materialBad+rarity5+blocked+unknown > 0 {
			say("%-16s job=%-3d grow=%-3d items=%-3d wrongJob=%-3d wrongMaterial=%-3d rarity5=%-3d blocked=%-3d unknown=%-3d\n",
				r.account, r.job, r.grow, len(items), jobBad, materialBad, rarity5, blocked, unknown)
		}
	}
	say("TOTAL items=%d wrongJob=%d wrongMaterial=%d rarity5=%d blocked=%d unknown=%d\n",
		totals.total, totals.jobBad, totals.materialBad, totals.rarity5, totals.blocked, totals.unknown)

	say("\nloadout markers among live characters:\n")
	markerRows, err := db.Query(`SELECT extra_value, COUNT(*) FROM ` + dnfEquipmentExtraTable +
		` WHERE extra_key=? GROUP BY extra_value ORDER BY extra_value`, cn90LoadoutMarkerKey)
	if err != nil {
		t.Fatal(err)
	}
	for markerRows.Next() {
		var value string
		var count int
		if err := markerRows.Scan(&value, &count); err != nil {
			t.Fatal(err)
		}
		say("%s=%s entries=%d\n", cn90LoadoutMarkerKey, value, count)
	}
	markerRows.Close()
	say("current cn90LoadoutVersion=%d\n", cn90LoadoutVersion)

	t.Logf("wrote fleet growth probe to %s", out)
}

func countLive(values map[int]int) int {
	total := 0
	for _, count := range values {
		total += count
	}
	return total
}

// TestZZProbeTombstones measures what a deleted character still occupies. The
// server soft-deletes a character (delete_flag=1) and leaves every child row in
// place, so a fleet that is repeatedly created and deleted grows the save file
// with rows nothing reads. Enable with CN90_PROBE_TOMBSTONES=1.
func TestZZProbeTombstones(t *testing.T) {
	if os.Getenv("CN90_PROBE_TOMBSTONES") == "" {
		t.Skip("set CN90_PROBE_TOMBSTONES=1")
	}
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := layout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	out := filepath.Join(os.TempDir(), "tombstones.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	say("db=%s\n", databasePath)
	if info, statErr := os.Stat(databasePath); statErr == nil {
		say("main file bytes=%d\n", info.Size())
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, statErr := os.Stat(databasePath + suffix); statErr == nil {
			say("%s bytes=%d\n", suffix, info.Size())
		}
	}
	var pageCount, pageSize, freelist int
	_ = db.QueryRow(`PRAGMA page_count`).Scan(&pageCount)
	_ = db.QueryRow(`PRAGMA page_size`).Scan(&pageSize)
	_ = db.QueryRow(`PRAGMA freelist_count`).Scan(&freelist)
	say("page_count=%d page_size=%d freelist=%d  (free bytes=%d)\n", pageCount, pageSize, freelist, freelist*pageSize)

	var total, deleted int
	if err := db.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN delete_flag<>0 THEN 1 ELSE 0 END) FROM ` + dnfCharactersTable).Scan(&total, &deleted); err != nil {
		t.Fatal(err)
	}
	say("\ncharacters total=%d deleted=%d live=%d\n", total, deleted, total-deleted)

	// Which accounts hold the tombstones, and can the purge path even reach
	// them? SQLiteRobotPurger selects accounts by uid, so a tombstone in an
	// account outside the configured uid range is unreachable by design.
	shapeRows, err := db.Query(`SELECT account_id,
		SUM(CASE WHEN delete_flag<>0 THEN 1 ELSE 0 END) AS dead,
		SUM(CASE WHEN delete_flag=0 THEN 1 ELSE 0 END) AS alive
		FROM ` + dnfCharactersTable + ` GROUP BY account_id HAVING dead > 0 ORDER BY dead DESC, account_id`)
	if err != nil {
		t.Fatal(err)
	}
	withDead, deadTotal, alsoAlive := 0, 0, 0
	say("\naccounts holding soft-deleted characters (top 25):\n")
	for shapeRows.Next() {
		var account string
		var dead, alive int
		if err := shapeRows.Scan(&account, &dead, &alive); err != nil {
			t.Fatal(err)
		}
		withDead++
		deadTotal += dead
		if alive > 0 {
			alsoAlive++
		}
		if withDead <= 25 {
			say("%-18s dead=%-4d alive=%-4d\n", account, dead, alive)
		}
	}
	shapeRows.Close()
	say("accounts with tombstones=%d tombstones=%d of which accounts that also hold a live character=%d\n",
		withDead, deadTotal, alsoAlive)

	// Every table keyed by character_id, split by whether the character is live.
	tableRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for tableRows.Next() {
		var name string
		if err := tableRows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	tableRows.Close()
	say("\ntable | total rows | rows of deleted characters\n")
	grandTotal, grandDeleted := 0, 0
	for _, table := range tables {
		columns, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			continue
		}
		hasCharacterID := false
		for columns.Next() {
			var column string
			if err := columns.Scan(&column); err != nil {
				break
			}
			if column == "character_id" {
				hasCharacterID = true
			}
		}
		columns.Close()
		if !hasCharacterID {
			continue
		}
		var rowsTotal, rowsDeleted int
		query := `SELECT COUNT(*), SUM(CASE WHEN character_id IN (SELECT character_id FROM ` + dnfCharactersTable + ` WHERE delete_flag<>0) THEN 1 ELSE 0 END) FROM ` + table
		if err := db.QueryRow(query).Scan(&rowsTotal, &rowsDeleted); err != nil {
			continue
		}
		if rowsTotal > 0 {
			say("%-34s | %-8d | %d\n", table, rowsTotal, rowsDeleted)
			grandTotal += rowsTotal
			grandDeleted += rowsDeleted
		}
	}
	say("%-34s | %-8d | %d\n", "TOTAL", grandTotal, grandDeleted)

	t.Logf("wrote tombstone probe to %s", out)
}
