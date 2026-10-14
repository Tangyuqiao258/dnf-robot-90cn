package equipment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// zz_probe_band_coverage_test.go is a TEMPORARY reconnaissance probe. It answers
// the question the level-band rarity rule turns into a hard requirement: does
// the exported catalog actually hold, for every band's rarity, an item a robot
// of that band may wear in every configured worn slot? Enable with
// CN90_PROBE_BAND=1 and point CN90_PROBE_BAND_CATALOG at equipment_catalog.json.

func TestZZProbeBandCoverage(t *testing.T) {
	if os.Getenv("CN90_PROBE_BAND") == "" {
		t.Skip("set CN90_PROBE_BAND=1")
	}
	catalogPath := os.Getenv("CN90_PROBE_BAND_CATALOG")
	if catalogPath == "" {
		catalogPath = "G:/90-CN/robot-run/config/pvf/equipment_catalog.json"
	}
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var items []shared.EquipmentCatalogItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(os.TempDir(), "band_coverage_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }
	say("catalog=%s items=%d\n", catalogPath, len(items))

	// rarity histogram per item type, to see what the archive actually offers.
	byTypeRarity := map[int]map[int]int{}
	for _, item := range items {
		if byTypeRarity[item.ItemType] == nil {
			byTypeRarity[item.ItemType] = map[int]int{}
		}
		byTypeRarity[item.ItemType][item.Rarity]++
	}
	types := make([]int, 0, len(byTypeRarity))
	for itemType := range byTypeRarity {
		types = append(types, itemType)
	}
	sort.Ints(types)
	say("\n=== rarity histogram per item type ===\n")
	for _, itemType := range types {
		say("type %-3d %v\n", itemType, byTypeRarity[itemType])
	}

	config := robotconfig.Default()
	config.EquipSlots = []int{1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	jobs := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	say("\n=== per band: slots with no candidate, per job (%d jobs) ===\n", len(jobs))
	say("%-6s %-4s %-8s %s\n", "level", "rar", "gearLvl", "jobs with holes -> (slot: itemType)")
	for _, level := range []int{10, 35, 40, 50, 60, 70, 71, 80, 84, 86, 90, 95} {
		band := EquipmentBandFor(level)
		gearLevel := EquipLevelFor(level)
		holes := map[string][]int{}
		for _, job := range jobs {
			for _, grow := range []int{0, 0x11, 0x21} {
				slots := config.EquipSlots
				if level >= EarringMinLevel {
					slots = appendWornSlot(slots, EarringItemType)
				}
				bySlot, _ := equipmentCandidates(items, level, job, grow, config)
				_ = slots
				for slot, list := range bySlot {
					if len(list) == 0 {
						key := fmt.Sprintf("job%d grow%d", job, grow)
						holes[key] = append(holes[key], slot)
					}
				}
			}
		}
		keys := make([]string, 0, len(holes))
		for key := range holes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		say("%-6d %-4d %-8d %d job/grow combos with holes\n", level, band.Rarity, gearLevel, len(keys))
		shown := 0
		for _, key := range keys {
			if shown >= 8 {
				say("       ... %d more\n", len(keys)-shown)
				break
			}
			say("       %s slots=%v\n", key, holes[key])
			shown++
		}
	}

	// Which rarities exist at or below each band's gear level, per worn type.
	say("\n=== rarities present at or below each gear level, per worn type ===\n")
	for _, level := range []int{35, 50, 70, 80, 90} {
		gearLevel := EquipLevelFor(level)
		present := map[int]map[int]int{}
		for _, item := range items {
			if item.ItemType < 1 || item.ItemType > 13 || item.Level > gearLevel {
				continue
			}
			if present[item.ItemType] == nil {
				present[item.ItemType] = map[int]int{}
			}
			present[item.ItemType][item.Rarity]++
		}
		say("gear level %d (band rarity %d):\n", gearLevel, BandRarityFor(level))
		for _, itemType := range []int{1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13} {
			say("    type %-3d %v\n", itemType, present[itemType])
		}
	}

	t.Logf("wrote band coverage probe to %s", out)
}
