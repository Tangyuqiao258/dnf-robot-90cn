package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	"robot/internal/shared"
)

// TestZZProbeGrantBlock runs the item grant gate against the live archive and
// asserts the invariant it exists for: every record the client cannot resolve
// (no [name]), every 勇者 (rarity 5) item, and every name carrying 活动, 租 or
// 克隆 is marked GrantBlocked, while the pinned titles and the fleet's ordinary
// stock survive. It also reports what the gate leaves per job, so a rule that
// thins the 时装 or gear pools to the point of breaking selection shows up here.
// Enable with CN90_PROBE_GRANTBLOCK=1.
func TestZZProbeGrantBlock(t *testing.T) {
	if os.Getenv("CN90_PROBE_GRANTBLOCK") == "" {
		t.Skip("set CN90_PROBE_GRANTBLOCK=1")
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
	out := filepath.Join(os.TempDir(), "grantblock_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	say("PVF=%s\nequipment=%d stackable=%d\n\n", pvfPath, len(items), len(stackables))

	reasons := map[string]int{}
	violations := 0
	for _, list := range [][]shared.EquipmentCatalogItem{items, stackables} {
		for _, item := range list {
			reason := ""
			switch {
			case item.Name == "" || item.BadName:
				reason = "no-name"
			case item.Rarity == 5:
				reason = "rarity5"
			case shared.ItemNameBlocked(item.Name) || shared.ItemNameBlocked(item.Name2):
				reason = "fragment"
			}
			if reason == "" {
				continue
			}
			reasons[reason]++
			if !item.GrantBlocked {
				violations++
				if violations <= 10 {
					say("VIOLATION %d name=%q rarity=%d path=%s\n", item.ID, item.Name, item.Rarity, item.Path)
				}
			}
		}
	}
	total := 0
	for _, count := range reasons {
		total += count
	}
	say("blocked by reason: %v total=%d\n", reasons, total)
	say("INVARIANT violations: %d (want 0)\n\n", violations)

	byID := make(map[int]shared.EquipmentCatalogItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	// Every one of these was confirmed unreadable by the live GM tool on the
	// running fleet (found:false, blank name, HTTP 404 icon).
	badIDs := []int{63059, 63107, 63123, 63147, 63175, 63177, 63254, 63257, 63269, 63275, 63288, 63313,
		100230379, 100300205, 100352658, 100080399, 100180377, 100280362, 100312086, 100343177, 2681332}
	present, unblocked := 0, 0
	for _, id := range badIDs {
		item, ok := byID[id]
		if !ok {
			say("known-bad id %d: NOT in the catalog\n", id)
			continue
		}
		present++
		if !item.GrantBlocked {
			unblocked++
			say("known-bad id %d: LEFT GRANTABLE name=%q path=%s\n", id, item.Name, item.Path)
		}
	}
	say("\nknown-bad ids: present=%d/%d  left grantable=%d (want 0)\n", present, len(badIDs), unblocked)

	for _, id := range []int{400330105, 400330106} {
		item, ok := byID[id]
		say("pinned title %d: present=%v grant_blocked=%v rarity=%d\n", id, ok, item.GrantBlocked, item.Rarity)
	}

	// The operator ruled that no robot may wear a 克隆 (clone) avatar. This
	// reports what the fragment reaches -- how many records carry it, which item
	// types they are and what their paths look like -- so a fragment that would
	// also stop ordinary gear or 时装 shows up here instead of in the live world.
	cloneTypes := map[int]int{}
	cloneBlocked := 0
	cloneTotal := 0
	samples := make([]string, 0, 40)
	for _, list := range [][]shared.EquipmentCatalogItem{items, stackables} {
		for _, item := range list {
			if !strings.Contains(item.Name, "克隆") && !strings.Contains(item.Name2, "克隆") {
				continue
			}
			cloneTotal++
			cloneTypes[item.ItemType]++
			if item.GrantBlocked {
				cloneBlocked++
			}
			// Sample the records that are not one of the eight 装扮 slots: those
			// are what a fragment reaching past 装扮 would catch.
			if item.ItemType < 20 || item.ItemType > 27 {
				if len(samples) < cap(samples) {
					samples = append(samples, fmt.Sprintf("%d type=%d rarity=%d blocked=%v name=%q name2=%q path=%s",
						item.ID, item.ItemType, item.Rarity, item.GrantBlocked, item.Name, item.Name2, item.Path))
				}
			}
		}
	}
	say("\n克隆 names: total=%d already_blocked=%d by item type=%v\n", cloneTotal, cloneBlocked, cloneTypes)
	for _, sample := range samples {
		say("  克隆 %s\n", sample)
	}

	say("\n=== what survives, per job ===\n")
	jobs := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 11, 12, 13, 14, 15}
	for _, job := range jobs {
		avatarRarity := map[int]int{}
		avatarSlots := map[int]map[int]bool{}
		gearByRarity := map[int]int{}
		for _, item := range items {
			if item.GrantBlocked {
				continue
			}
			switch {
			case item.ItemType >= 20 && item.ItemType <= 29:
				if !equipmentcap.AvatarUsableByJob(item, job) {
					continue
				}
				avatarRarity[item.Rarity]++
				if avatarSlots[item.Rarity] == nil {
					avatarSlots[item.Rarity] = map[int]bool{}
				}
				avatarSlots[item.Rarity][item.ItemType-20] = true
			case item.ItemType >= 1 && item.ItemType <= 13:
				if !equipmentcap.UsableByJob(item.UseJob, job) {
					continue
				}
				gearByRarity[item.Rarity]++
			}
		}
		// slotCount reports how many distinct 时装 slots one rarity can fill. It
		// keys off the rarity value, not a slot index, so the numbers are only
		// comparable within a rarity.
		slotCount := func(rarity int) int { return len(avatarSlots[rarity]) }
		say("job %-3d 时装 rarity->items=%v  slots by rarity: %d->%d %d->%d %d->%d  gear rarity->items=%v\n",
			job, avatarRarity,
			2, slotCount(2), 8, slotCount(8), 9, slotCount(9), gearByRarity)
	}

	// Slot coverage of the worn eight 时装 slots for the rare band, which is what
	// the 71+ outfit needs.
	for _, job := range jobs {
		slots := map[int]bool{}
		for _, item := range items {
			if item.GrantBlocked || item.ItemType < 20 || item.ItemType > 27 || item.Rarity != 2 {
				continue
			}
			if equipmentcap.AvatarUsableByJob(item, job) {
				slots[item.ItemType-20] = true
			}
		}
		if len(slots) < 8 {
			say("SHORT: job %d has only %d/8 稀有装扮 slots\n", job, len(slots))
		}
	}

	t.Logf("wrote grant-block probe to %s", out)
	if violations != 0 || unblocked != 0 {
		t.Fatalf("gate violations=%d unblocked known-bad=%d", violations, unblocked)
	}
}
