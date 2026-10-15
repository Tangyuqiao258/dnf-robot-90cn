package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// TestZZProbeOperatorRules reads the lab's own Script.pvf and checks the three
// rules the operator gave on 2026-10-15 against it: no 宠物蛋 in the creature
// slot, no name carrying GM, (旧) or a bracket, and one 装扮 set per character
// chosen by the name its pieces share. This is the reproduction the rules need:
// the live archive is the only input that can say whether they hold.
// Enable with CN90_PROBE_RULES=1 and CN90_LIVE_RUNTIME=<lab root>.
func TestZZProbeOperatorRules(t *testing.T) {
	if os.Getenv("CN90_PROBE_RULES") == "" {
		t.Skip("set CN90_PROBE_RULES=1")
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
	items, stackables, err := capabilitypvf.ProjectItemCatalogs(archive)
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(os.TempDir(), "rules_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }
	fail := func(format string, args ...any) {
		fmt.Fprintf(file, "VIOLATION "+format, args...)
		t.Errorf(format, args...)
	}

	say("PVF=%s equipment=%d stackable=%d\n\n", pvfPath, len(items), len(stackables))

	// Rule 1: the name fragments the operator barred, counted on the records the
	// gate lets through and on the records it does not.
	say("=== blocked name fragments over the whole projection ===\n")
	for _, fragment := range shared.BlockedItemNameFragments {
		blocked, leaked := 0, 0
		for _, item := range append(append([]shared.EquipmentCatalogItem{}, items...), stackables...) {
			if item.Name == "" || !strings.Contains(item.Name, fragment) {
				continue
			}
			if item.GrantBlocked {
				blocked++
			} else {
				leaked++
			}
		}
		say("  %-8s blocked=%-6d leaked=%d\n", fragment, blocked, leaked)
		if leaked > 0 {
			fail("fragment %q reaches %d granted records\n", fragment, leaked)
		}
	}

	// Rule 2: the creature slot never takes an unhatched egg.
	say("\n=== creature slot (宠物蛋 must never be drawn) ===\n")
	rc := robotconfig.Default()
	if loaded, err := robotconfig.LoadFile(filepath.Join(filepath.Dir(root), "robot-run", "config", "conf", "robot_config.ini")); err == nil {
		rc = loaded
	}
	pets, eggs := 0, 0
	for seed := 1; seed <= 400; seed++ {
		pet, _, ok := equipmentcap.SelectPet(items, rc, func(n int) int { return seed * n % 7 })
		if !ok {
			continue
		}
		pets++
		if strings.Contains(pet.Name, "宠物蛋") || strings.Contains(pet.Name2, "宠物蛋") {
			eggs++
		}
		if pet.GrantBlocked {
			fail("creature draw handed over blocked item %d %q\n", pet.ID, pet.Name)
		}
	}
	say("  draws that produced a creature=%d  of them eggs=%d\n", pets, eggs)
	if eggs > 0 {
		fail("the creature slot took an unhatched egg %d times\n", eggs)
	}

	// Rule 3: every dressed robot wears one 装扮 set, named by its pieces, and no
	// piece the gate barred. Level 90 is the 稀有装扮 band, 50 the ordinary one.
	say("\n=== 装扮 per job: one name family, nothing barred ===\n")
	catalog := equipmentcap.PrepareAvatarCatalog(items, rc)
	for _, level := range []int{50, 90} {
		for job := 0; job <= 15; job++ {
			families := map[string]int{}
			dressed := 0
			for seed := 1; seed <= 40; seed++ {
				selected := catalog.Select(level, job, rc, func(n int) int { return seed * n % 7 })
				roots := map[string]bool{}
				for slot, item := range selected {
					dressed++
					if item.GrantBlocked {
						fail("job %d slot %d wore blocked item %d %q\n", job, slot, item.ID, item.Name)
					}
					if slot == equipmentcap.AvatarAuraSlot {
						continue
					}
					if root := equipmentcap.AvatarFamilyKey(item.Name); root != "" {
						roots[root] = true
					}
				}
				if len(roots) > 1 {
					fail("job %d level %d mixed %d 装扮 families: %v\n", job, level, len(roots), roots)
				}
				for root := range roots {
					families[root]++
				}
			}
			keys := make([]string, 0, len(families))
			for root := range families {
				keys = append(keys, root)
			}
			sort.Strings(keys)
			say("  level %-3d job %-3d pieces=%-4d families=%v\n", level, job, dressed, keys)
		}
	}

	t.Logf("wrote rules probe to %s", out)
}
