package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// TestZZProbeSets runs the real worn-equipment selection against the live
// catalog for a level-90 robot of every job and mastery branch, and reports
// what each one ends up wearing: how complete its material set is, which set
// keys it draws, and at what item level. This is the reproduction A1-3 needs --
// the operator's requirement is a complete 5-piece set per material at level
// 90, and the only way to know whether the selector delivers one is to run it.
// Enable with CN90_PROBE_SETS=1.
func TestZZProbeSets(t *testing.T) {
	if os.Getenv("CN90_PROBE_SETS") == "" {
		t.Skip("set CN90_PROBE_SETS=1")
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
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := capabilitypvf.ProjectItemCatalogs(archive)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int]shared.EquipmentCatalogItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}

	// The fleet's own configuration, read from the deployed ini so the probe
	// measures what the robots actually run rather than a stand-in.
	rc := robotconfig.Default()
	if loaded, err := robotconfig.LoadFile(filepath.Join(filepath.Dir(root), "robot-run", "config", "conf", "robot_config.ini")); err == nil {
		rc = loaded
	} else {
		t.Logf("config not loaded from the robot-run tree (%v); using defaults", err)
	}

	out := filepath.Join(os.TempDir(), "sets_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	say("PVF=%s\nitems=%d equip_slots=%v set_min_slots=%d prefer_sets=%v\n\n",
		pvfPath, len(items), rc.EquipSlots, rc.EquipSetMinSlots, rc.PreferEquipSets)

	// Deterministic sweep: the same seed sequence for every branch so a
	// difference between branches is the pool, not the draw.
	const draws = 40
	for job := 0; job <= 15; job++ {
		for _, grow := range []int{0, 1, 2} {
			histogram := map[string]int{}
			levelHist := map[int]int{}
			partial := 0
			for draw := 0; draw < draws; draw++ {
				seed := draw + 1
				randIntn := func(n int) int { return seed*n%7 + seed%n }
				selected := equipmentcap.SelectEquipment(items, 90, job, grow, rc, randIntn)
				bySet := map[string][]int{}
				for itemType, item := range selected {
					if itemType >= 3 && itemType <= 7 {
						bySet[item.SetKey] = append(bySet[item.SetKey], itemType)
						levelHist[item.Level]++
					}
				}
				best, bestCount := "", 0
				for key, slots := range bySet {
					if key == "" {
						continue
					}
					if len(slots) > bestCount {
						best, bestCount = key, len(slots)
					}
				}
				if bestCount < 5 {
					partial++
				}
				histogram[fmt.Sprintf("%s x%d", best, bestCount)]++
			}
			keys := make([]string, 0, len(histogram))
			for key := range histogram {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			levels := make([]int, 0, len(levelHist))
			for level := range levelHist {
				levels = append(levels, level)
			}
			sort.Ints(levels)
			say("job %-3d grow %d  armor set incomplete in %d/%d draws  worn armor levels=%v\n",
				job, grow, partial, draws, levels)
			for i, key := range keys {
				if i >= 6 {
					say("        ... %d more outcomes\n", len(keys)-i)
					break
				}
				say("        %-40s %d\n", key, histogram[key])
			}
		}
	}

	// How many complete level-90 material sets the archive now offers, per
	// material, with the gate off.
	say("\n=== complete 5-piece armor sets at level 90, by material ===\n")
	setMembers := map[string]map[int]string{}
	for _, item := range items {
		if item.ItemType < 3 || item.ItemType > 7 || item.Level != 90 || item.Rarity != equipmentcap.RarityEpic {
			continue
		}
		if item.GrantBlocked || item.Expire {
			continue
		}
		key := item.SetKey
		if key == "" {
			continue
		}
		if setMembers[key] == nil {
			setMembers[key] = map[int]string{}
		}
		material := armorMaterial(item.Path)
		setMembers[key][item.ItemType] = material
	}
	completeByMaterial := map[string]int{}
	for key, slots := range setMembers {
		if len(slots) != 5 {
			continue
		}
		material := slots[3]
		completeByMaterial[material]++
		say("  %-14s %s\n", material, key)
	}
	say("complete sets by material: %v\n", completeByMaterial)

	t.Logf("wrote set-selection probe to %s", out)
}
