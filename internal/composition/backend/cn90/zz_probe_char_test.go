package cn90

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// TestZZProbeCharacterLoadout reads one live character's worn set out of the
// running server's database and confronts it with what the current source would
// select for that character's (job, level, grow). It exists because the operator
// reported a 魔法师 wearing gear that is neither its job nor its armor material:
// the question is whether the shipped selector produced it or the record
// predates the selector, and only the record itself can answer that.
//
// Set CN90_PROBE_CHAR to an account id, character id or name fragment. The
// live ini is read from CN90_PROBE_CHAR_INI, defaulting to the robot-run config.
func TestZZProbeCharacterLoadout(t *testing.T) {
	needle := strings.TrimSpace(os.Getenv("CN90_PROBE_CHAR"))
	if needle == "" {
		t.Skip("set CN90_PROBE_CHAR=<account|character id|name fragment>")
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
	iniPath := strings.TrimSpace(os.Getenv("CN90_PROBE_CHAR_INI"))
	if iniPath == "" {
		iniPath = `G:\90-CN\robot-run\config\conf\robot_config.ini`
	}
	rc, err := robotconfig.LoadFile(iniPath)
	if err != nil {
		t.Fatalf("load %s: %v", iniPath, err)
	}
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

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	out := filepath.Join(os.TempDir(), "char_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	say("db=%s\nini=%s\ncatalog equipment=%d\nneedle=%q\n\n", databasePath, iniPath, len(catalogs.Equipment), needle)

	type character struct {
		id, name, account string
		job, level, grow  int
		deleted           int
	}
	var characters []character
	rows, err := db.Query(`SELECT character_id, account_id, name, job, level, grow_type, delete_flag FROM `+dnfCharactersTable+
		` WHERE character_id=? OR account_id LIKE ? OR name LIKE ?`, needle, "%"+needle+"%", "%"+needle+"%")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c character
		var job string
		if err := rows.Scan(&c.id, &c.account, &c.name, &job, &c.level, &c.grow, &c.deleted); err != nil {
			t.Fatal(err)
		}
		c.job, _ = strconv.Atoi(strings.TrimSpace(job))
		characters = append(characters, c)
	}
	rows.Close()
	if len(characters) == 0 {
		t.Fatalf("no character matches %q", needle)
	}
	say("matched %d character(s)\n", len(characters))

	for _, c := range characters {
		materials := equipmentcap.ArmorMaterialsFor(c.job, c.grow)
		band := equipmentcap.EquipmentBandFor(c.level)
		say("\n=== character %s (%s) id=%s job=%d level=%d grow=%d deleted=%d ===\n",
			c.name, c.account, c.id, c.job, c.level, c.grow, c.deleted)
		say("armor materials for (job=%d grow=%d): %v | branch=%d | equip level cap=%d | band rarity=%d\n",
			c.job, c.grow, materials, c.grow&0x0f, equipmentcap.EquipLevelFor(c.level), equipmentcap.BandRarityFor(c.level))
		_ = band

		type worn struct {
			slot, itemID int
			extras       map[string]string
		}
		var items []worn
		rows, err := db.Query(`SELECT slot_index, item_id FROM `+dnfEquipmentEntriesTable+` WHERE character_id=? ORDER BY slot_index`, c.id)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var w worn
			if err := rows.Scan(&w.slot, &w.itemID); err != nil {
				t.Fatal(err)
			}
			items = append(items, w)
		}
		rows.Close()
		extrasBySlot := map[int]map[string]string{}
		rows, err = db.Query(`SELECT entry_key, extra_key, extra_value FROM `+dnfEquipmentExtraTable+` WHERE character_id=?`, c.id)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var key, extraKey, extraValue string
			if err := rows.Scan(&key, &extraKey, &extraValue); err != nil {
				t.Fatal(err)
			}
			slot, convErr := strconv.Atoi(strings.TrimSpace(key))
			if convErr != nil {
				continue
			}
			if extrasBySlot[slot] == nil {
				extrasBySlot[slot] = map[string]string{}
			}
			extrasBySlot[slot][extraKey] = extraValue
		}
		rows.Close()

		say("\nworn entries=%d\n", len(items))
		say("slot | itemID | name | type | lvl | rarity | job ok | material | material ok | blocked | path\n")
		for _, w := range items {
			item, known := byID[w.itemID]
			if !known {
				say("%-4d | %-9d | NOT IN THE CATALOG (extras=%v)\n", w.slot, w.itemID, extrasBySlot[w.slot])
				continue
			}
			material := ""
			if item.ItemType >= 2 && item.ItemType <= 12 {
				material = armorMaterial(item.Path)
			}
			jobOK := equipmentcap.UsableByJob(item.UseJob, c.job)
			materialOK := true
			if material != "" {
				materialOK = false
				for _, allowed := range materials {
					if allowed == material {
						materialOK = true
					}
				}
			}
			loadoutMarker := extrasBySlot[w.slot][cn90LoadoutMarkerKey]
			say("%-4d | %-9d | %-24s | %-3d | %-4d | %-2d | %-7v | %-8s | %-11v | %-7v | %s (loadout=%q)\n",
				w.slot, item.ID, item.Name, item.ItemType, item.Level, item.Rarity, jobOK, material, materialOK, item.GrantBlocked, item.Path, loadoutMarker)
		}

		// What the shipped selector makes of the same character. Seeded per run so
		// the draw is reproducible, and reported slot by slot for comparison.
		selected := equipmentcap.SelectEquipment(catalogs.Equipment, c.level, c.job, c.grow, rc, nil)
		slots := make([]int, 0, len(selected))
		for slot := range selected {
			slots = append(slots, slot)
		}
		sort.Ints(slots)
		say("\n--- what the current source selects for job=%d level=%d grow=%d ---\n", c.job, c.level, c.grow)
		for _, slot := range slots {
			item := selected[slot]
			material := ""
			if item.ItemType >= 2 && item.ItemType <= 12 {
				material = armorMaterial(item.Path)
			}
			say("slot %-3d type=%-3d %-24s lvl=%-3d rarity=%-2d material=%-8s jobs=%v\n",
				slot, item.ItemType, item.Name, item.Level, item.Rarity, material, item.UseJob)
		}

		avatars := equipmentcap.SelectAvatar(catalogs.Equipment, c.level, cn90AvatarJob(c.job), rc, nil)
		avatarSlots := make([]int, 0, len(avatars))
		for slot := range avatars {
			avatarSlots = append(avatarSlots, slot)
		}
		sort.Ints(avatarSlots)
		say("\n--- 时装 the source selects (catalog job=%d) ---\n", cn90AvatarJob(c.job))
		for _, slot := range avatarSlots {
			item := avatars[slot]
			say("slot %-3d type=%-3d %-30s lvl=%-3d rarity=%-2d jobs=%v\n",
				slot, item.ItemType, item.Name, item.Level, item.Rarity, item.UseJob)
		}
	}

	t.Logf("wrote character probe to %s", out)
}
