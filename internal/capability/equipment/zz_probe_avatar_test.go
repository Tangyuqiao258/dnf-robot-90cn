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

// zz_probe_avatar_test.go is a RECONNAISSANCE probe for the level-differentiated
// 时装 rule. Before the selection rule can key on level, the archive has to say
// what it actually holds: which avatar rarities exist, whether they need a level
// at all, and whether a whole 稀有装扮 set exists per job and slot. Enable with
// CN90_PROBE_AVATAR=1 and point CN90_PROBE_AVATAR_CATALOG at
// equipment_catalog.json.

func TestZZProbeAvatarCatalog(t *testing.T) {
	if os.Getenv("CN90_PROBE_AVATAR") == "" {
		t.Skip("set CN90_PROBE_AVATAR=1")
	}
	catalogPath := os.Getenv("CN90_PROBE_AVATAR_CATALOG")
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
	out := filepath.Join(os.TempDir(), "avatar_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }
	say("catalog=%s items=%d\n", catalogPath, len(items))

	// Item types at or above 20, so the avatar band is visible next to anything
	// else the archive keeps up there.
	byType := map[int]int{}
	for _, item := range items {
		if item.ItemType >= 20 {
			byType[item.ItemType]++
		}
	}
	types := make([]int, 0, len(byType))
	for itemType := range byType {
		types = append(types, itemType)
	}
	sort.Ints(types)
	say("\n=== item types >= 20 ===\n")
	for _, itemType := range types {
		say("type %-3d count=%d\n", itemType, byType[itemType])
	}

	// Rarity histogram per avatar item type.
	say("\n=== avatar rarity histogram (item type 20..29) ===\n")
	byTypeRarity := map[int]map[int]int{}
	for _, item := range items {
		if item.ItemType < 20 || item.ItemType > 29 {
			continue
		}
		if byTypeRarity[item.ItemType] == nil {
			byTypeRarity[item.ItemType] = map[int]int{}
		}
		byTypeRarity[item.ItemType][item.Rarity]++
	}
	for _, itemType := range []int{20, 21, 22, 23, 24, 25, 26, 27, 28, 29} {
		say("type %-3d %v\n", itemType, byTypeRarity[itemType])
	}

	// Per rarity: level range, how many distinct SetKeys, and the biggest set.
	say("\n=== avatar rarity profile ===\n")
	for _, rarity := range []int{0, 1, 2, 3, 4} {
		levels := map[int]int{}
		setSizes := map[string]map[int]struct{}{}
		count := 0
		samples := []string{}
		for _, item := range items {
			if item.ItemType < 20 || item.ItemType > 29 || item.Rarity != rarity {
				continue
			}
			if item.Expire || item.GrantBlocked || !AvatarRenderable(item) {
				continue
			}
			count++
			levels[item.Level]++
			if item.SetKey != "" {
				if setSizes[item.SetKey] == nil {
					setSizes[item.SetKey] = map[int]struct{}{}
				}
				setSizes[item.SetKey][item.ItemType] = struct{}{}
			}
			if len(samples) < 6 {
				samples = append(samples, fmt.Sprintf("%d/%s/jobs=%v", item.ID, item.Name, item.UseJob))
			}
		}
		levelKeys := make([]int, 0, len(levels))
		for level := range levels {
			levelKeys = append(levelKeys, level)
		}
		sort.Ints(levelKeys)
		say("rarity %d: usable=%d levels=%v setkeys=%d\n", rarity, count, levelKeys, len(setSizes))
		for _, sample := range samples {
			say("    %s\n", sample)
		}
	}

	// The archive's own set records, scored the way selection scores them.
	config := robotconfig.Default()
	config.AvatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	jobs := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	say("\n=== per job: best avatar set group (slots, size, avg rarity) ===\n")
	for _, job := range jobs {
		bySlot := map[int][]shared.EquipmentCatalogItem{}
		for _, item := range items {
			if item.ID == 0 || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) || !AvatarRenderable(item) {
				continue
			}
			if item.ItemType < 20 || item.ItemType > 29 {
				continue
			}
			if !AvatarUsableByJob(item, job) {
				continue
			}
			slot := item.ItemType - 20
			bySlot[slot] = append(bySlot[slot], item)
		}
		groups := buildSetGroups(bySlot)
		type scored struct {
			key    string
			slots  int
			rarity int
			level  int
		}
		best := make([]scored, 0, len(groups))
		for key, group := range groups {
			best = append(best, scored{key: key, slots: len(group.bySlot), rarity: SafeAvg(group.raritySum, group.count), level: SafeAvg(group.levelSum, group.count)})
		}
		sort.Slice(best, func(i, j int) bool { return best[i].slots > best[j].slots })
		// Rarity mix of every candidate the job may wear, and how many slots the
		// job can even fill at each rarity.
		raritySlots := map[int]map[int]struct{}{}
		for slot, list := range bySlot {
			for _, item := range list {
				if raritySlots[item.Rarity] == nil {
					raritySlots[item.Rarity] = map[int]struct{}{}
				}
				raritySlots[item.Rarity][slot] = struct{}{}
			}
		}
		rarityKeys := make([]int, 0, len(raritySlots))
		for rarity := range raritySlots {
			rarityKeys = append(rarityKeys, rarity)
		}
		sort.Ints(rarityKeys)
		say("job %-3d slots=%d rarity->slotcount=%v\n", job, len(bySlot), func() map[int]int {
			m := map[int]int{}
			for _, rarity := range rarityKeys {
				m[rarity] = len(raritySlots[rarity])
			}
			return m
		}())
		for index, group := range best {
			if index >= 5 {
				break
			}
			say("      set %-28s slots=%-3d avgRarity=%-3d avgLevel=%d\n", group.key, group.slots, group.rarity, group.level)
		}
	}

	// What the level-30 and level-90 稀有装扮 actually are. A level gate on an
	// avatar is unusual, so it is worth seeing whether those are the top tier.
	say("\n=== 稀有装扮 by level ===\n")
	byLevel := map[int][]string{}
	for _, item := range items {
		if item.ItemType < 20 || item.ItemType > 29 || item.Rarity != 2 {
			continue
		}
		if item.Expire || item.GrantBlocked || !AvatarRenderable(item) {
			continue
		}
		if len(byLevel[item.Level]) < 8 {
			byLevel[item.Level] = append(byLevel[item.Level], fmt.Sprintf("%d/%s jobs=%v", item.ID, item.Name, item.UseJob))
		}
	}
	levelKeys := make([]int, 0, len(byLevel))
	for level := range byLevel {
		levelKeys = append(levelKeys, level)
	}
	sort.Ints(levelKeys)
	for _, level := range levelKeys {
		say("level %-3d %v\n", level, byLevel[level])
	}

	// The decisive question for "高等级人物发稀有装扮套装": with the candidate
	// pool narrowed to one rarity, can a job still be dressed head to toe, and is
	// there a whole set among the survivors?
	say("\n=== per job, per rarity: slots covered and best set group ===\n")
	for _, rarity := range []int{0, 2} {
		say("rarity %d:\n", rarity)
		for _, job := range jobs {
			bySlot := map[int][]shared.EquipmentCatalogItem{}
			for _, item := range items {
				if item.ItemType < 20 || item.ItemType > 29 || item.Rarity != rarity {
					continue
				}
				if item.ID == 0 || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) || !AvatarRenderable(item) {
					continue
				}
				if !AvatarUsableByJob(item, job) {
					continue
				}
				slot := item.ItemType - 20
				bySlot[slot] = append(bySlot[slot], item)
			}
			groups := buildSetGroups(bySlot)
			bestKey, bestSlots := "", 0
			for key, group := range groups {
				if len(group.bySlot) > bestSlots {
					bestKey, bestSlots = key, len(group.bySlot)
				}
			}
			missing := []int{}
			for slot := 0; slot <= 9; slot++ {
				if len(bySlot[slot]) == 0 {
					missing = append(missing, slot)
				}
			}
			say("    job %-3d filled=%d/10 missing=%v bestSet=%s(%d)\n", job, 10-len(missing), missing, bestKey, bestSlots)
		}
	}

	// Which slots the archive's own 稀有装扮 set covers, since the operator's
	// "8 slots" is a claim about exactly this.
	{
		bySlot := map[int][]shared.EquipmentCatalogItem{}
		for _, item := range items {
			if item.SetKey != "pvf_part_3" {
				continue
			}
			slot := item.ItemType - 20
			bySlot[slot] = append(bySlot[slot], item)
		}
		slots := make([]int, 0, len(bySlot))
		for slot := range bySlot {
			slots = append(slots, slot)
		}
		sort.Ints(slots)
		say("\n=== pvf_part_3 slots (slot -> item types -> jobs) ===\n")
		for _, slot := range slots {
			jobs := map[int]int{}
			types := map[int]int{}
			for _, item := range bySlot[slot] {
				types[item.ItemType]++
				for _, job := range item.UseJob {
					jobs[job]++
				}
			}
			say("slot %-2d type=%-3v items=%-4d jobs=%v\n", slot, types, len(bySlot[slot]), jobs)
		}
	}

	// The level gate only matters if selection can see it: what the per-band
	// pools would look like for a level-50 and a level-90 robot.
	say("\n=== which rarities a level gate would leave per job (rarity<=2, level<=X) ===\n")
	for _, maxLevel := range []int{1, 30, 90} {
		line := []string{}
		for _, job := range jobs {
			slots := map[int]struct{}{}
			for _, item := range items {
				if item.ItemType < 20 || item.ItemType > 29 || item.Rarity != 2 || item.Level > maxLevel {
					continue
				}
				if item.Expire || item.GrantBlocked || !AvatarRenderable(item) || !AvatarUsableByJob(item, job) {
					continue
				}
				slots[item.ItemType-20] = struct{}{}
			}
			line = append(line, fmt.Sprintf("job%d:%d", job, len(slots)))
		}
		say("稀有装扮 with level<=%-3d %v\n", maxLevel, line)
	}

	t.Logf("wrote avatar probe to %s", out)
}
