package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestLiveDumpEquipFacts is a read-only reconnaissance helper for the
// equipment/level work: it links every dungeon-gate town area to the dungeon it
// opens and reads that dungeon's entry level, samples the armor items the
// catalog carries, and groups the avatar catalog by set key so a complete rare
// avatar set can be recognised. It writes a UTF-8 text file and never touches
// the game or database.
func TestLiveDumpEquipFacts(t *testing.T) {
	if os.Getenv("CN90_LIVE_DUMP_EQUIP_FACTS") == "" {
		t.Skip("set CN90_LIVE_DUMP_EQUIP_FACTS=1 to dump equipment facts")
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
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("CN90_LIVE_DUMP_EQUIP_FACTS_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "equip_facts.txt")
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) {
		fmt.Fprintf(file, format, args...)
	}

	dungeonLst, _ := archive.ReadText("dungeon/dungeon.lst")
	dungeons := listEntries(dungeonLst, ".dgn")
	townLst, _ := archive.ReadText("town/town.lst")
	towns := listEntries(townLst, ".twn")

	say("=== every dungeon-gate town area and the dungeon it opens ===\n")
	say("dungeon.lst entries=%d town.lst entries=%d\n", len(dungeons), len(towns))
	say("village area | gate id | dungeon path | basis level | min required level | name\n")
	villages := make([]int, 0, len(towns))
	for id := range towns {
		villages = append(villages, id)
	}
	sort.Ints(villages)
	linked := 0
	for _, village := range villages {
		text, _ := archive.ReadText(dumpPath("town/" + towns[village]))
		for _, block := range areaBlocks(text) {
			id, mapPath, gate := dungeonGateArea(block)
			if id < 0 {
				continue
			}
			linked++
			path := dungeons[gate]
			level, minLevel, name := 0, 0, ""
			if path != "" {
				body, _ := archive.ReadText(dumpPath("dungeon/" + path))
				level = tagValue(body, "basis level")
				minLevel = tagValue(body, "minimum required level")
				name = tagName(body)
			}
			say("%d/%d | %d | %s | %d | %d | %s (map=%s)\n",
				village, id, gate, path, level, minLevel, name, mapPath)
		}
	}
	say("gate areas=%d\n", linked)

	say("\n=== armor items (level >= 80) ===\n")
	armorMaterials := map[string]int{}
	printed := 0
	for _, item := range catalogs.Equipment {
		if item.ItemType < 2 || item.ItemType > 12 {
			continue
		}
		if material := armorMaterial(item.Path); material != "" {
			armorMaterials[material]++
		}
		if item.Level < 80 || printed >= 24 {
			continue
		}
		printed++
		say("%d | type=%d slot=%s lvl=%d rarity=%d | %s | set=%s | jobs=%v\n",
			item.ID, item.ItemType, item.Slot, item.Level, item.Rarity, item.Path, item.SetKey, item.UseJob)
	}
	say("armor material tokens: %s\n", histogram(armorMaterials))

	say("\n=== avatar sets with at least six slots (item type 20..27) ===\n")
	type avatarSet struct {
		slots   map[int]bool
		jobs    map[int]int
		rarity  map[int]int
		samples []string
		total   int
	}
	sets := map[string]*avatarSet{}
	for _, item := range catalogs.Equipment {
		if item.ItemType < 20 || item.ItemType > 27 {
			continue
		}
		key := item.SetKey
		if key == "" {
			continue
		}
		entry := sets[key]
		if entry == nil {
			entry = &avatarSet{slots: map[int]bool{}, jobs: map[int]int{}, rarity: map[int]int{}}
			sets[key] = entry
		}
		entry.total++
		entry.slots[item.ItemType] = true
		for _, job := range item.UseJob {
			entry.jobs[job]++
		}
		entry.rarity[item.Rarity]++
		if len(entry.samples) < 6 {
			entry.samples = append(entry.samples, item.Name)
		}
	}
	keys := make([]string, 0, len(sets))
	for key := range sets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := sets[key]
		if len(entry.slots) < 6 {
			continue
		}
		say("set=%s slots=%d items=%d roles=%s rarity=%s\n  %s\n",
			key, len(entry.slots), entry.total, histogramInt(entry.jobs), histogramInt(entry.rarity),
			strings.Join(entry.samples, " ; "))
	}
	say("\n--- avatars whose name contains 稀有, grouped by set key ---\n")
	rare := map[string]int{}
	rareSlots := map[string]map[int]bool{}
	for _, item := range catalogs.Equipment {
		if item.ItemType < 20 || item.ItemType > 27 || !strings.Contains(item.Name, "稀有") {
			continue
		}
		rare[item.SetKey]++
		if rareSlots[item.SetKey] == nil {
			rareSlots[item.SetKey] = map[int]bool{}
		}
		rareSlots[item.SetKey][item.ItemType] = true
	}
	rareKeys := make([]string, 0, len(rare))
	for key := range rare {
		rareKeys = append(rareKeys, key)
	}
	sort.Strings(rareKeys)
	for _, key := range rareKeys {
		say("set=%s items=%d slots=%d\n", key, rare[key], len(rareSlots[key]))
	}
	say("\n--- rare avatar name families ---\n")
	type family struct {
		count        int
		minID, maxID int
		slots        map[int]bool
		jobs         map[int]int
	}
	families := map[string]*family{}
	for _, item := range catalogs.Equipment {
		if item.ItemType < 20 || item.ItemType > 27 || !strings.Contains(item.Name, "稀有") {
			continue
		}
		base := item.Name
		if index := strings.Index(base, " ("); index > 0 {
			base = base[:index]
		}
		entry := families[base]
		if entry == nil {
			entry = &family{minID: item.ID, maxID: item.ID, slots: map[int]bool{}, jobs: map[int]int{}}
			families[base] = entry
		}
		entry.count++
		if item.ID < entry.minID {
			entry.minID = item.ID
		}
		if item.ID > entry.maxID {
			entry.maxID = item.ID
		}
		entry.slots[item.ItemType] = true
		for _, job := range item.UseJob {
			entry.jobs[job]++
		}
	}
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := families[name]
		say("%s | items=%d slots=%d ids=%d..%d jobs=%s\n",
			name, entry.count, len(entry.slots), entry.minID, entry.maxID, histogramInt(entry.jobs))
	}

	say("\n--- armor material by job (level >= 60, job-restricted items only) ---\n")
	byJob := map[int]map[string]int{}
	universal := 0
	restricted := 0
	for _, item := range catalogs.Equipment {
		if item.ItemType < 2 || item.ItemType > 12 || item.Level < 60 {
			continue
		}
		material := armorMaterial(item.Path)
		if material == "" {
			continue
		}
		jobSpecific := false
		for _, job := range item.UseJob {
			if job == 100 {
				continue
			}
			jobSpecific = true
			if byJob[job] == nil {
				byJob[job] = map[string]int{}
			}
			byJob[job][material]++
		}
		if jobSpecific {
			restricted++
		} else {
			universal++
		}
	}
	say("job-restricted=%d universal=%d\n", restricted, universal)
	for _, job := range sortedKeys(byJob) {
		say("job=%d %s\n", job, histogram(byJob[job]))
	}

	say("\n--- armor material by slot directory (level >= 60) ---\n")
	slotMaterials := map[string]int{}
	for _, item := range catalogs.Equipment {
		if item.ItemType < 2 || item.ItemType > 12 || item.Level < 60 {
			continue
		}
		parts := strings.Split(dumpPath(item.Path), "/")
		if len(parts) < 3 {
			continue
		}
		slotMaterials[parts[len(parts)-3]+"/"+parts[len(parts)-2]]++
	}
	say("%s\n", histogram(slotMaterials))

	say("\n=== projected dungeon-gate maps (town catalog) ===\n")
	gates := 0
	for _, mp := range catalogs.TownMaps {
		if !mp.DungeonGate {
			continue
		}
		gates++
		say("%d/%d | town level=%d | min level=%d | dungeon level=%d\n",
			mp.Village, mp.Area, mp.Level, mp.MinLevel, mp.DungeonLevel)
	}
	say("catalog gate maps=%d of %d\n", gates, len(catalogs.TownMaps))

	t.Logf("wrote equipment facts to %s", out)
}

func sortedKeys(values map[int]map[string]int) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

var dumpListEntryPattern = regexp.MustCompile("(\\d+)\\s+`([^`]+)`")

// listEntries maps the id of every .lst entry whose path ends in extension.
func listEntries(lst, extension string) map[int]string {
	out := map[int]string{}
	for _, match := range dumpListEntryPattern.FindAllStringSubmatch(lst, -1) {
		id, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		path := strings.TrimSpace(match[2])
		if !strings.HasSuffix(strings.ToLower(path), extension) {
			continue
		}
		if _, exists := out[id]; !exists {
			out[id] = path
		}
	}
	return out
}

var areaBlockPattern = regexp.MustCompile(`(?is)\[area\](.*?)\[/area\]`)

func areaBlocks(text string) []string {
	matches := areaBlockPattern.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match[1])
	}
	return out
}

// dungeonGateArea reads one [area] block: its id, its map path, and the dungeon
// id the block's [dungeon gate] marker carries. id is -1 when the block is not
// a dungeon gate.
func dungeonGateArea(block string) (id int, mapPath string, gate int) {
	flat := strings.ReplaceAll(block, "`", "")
	marker := strings.Index(strings.ToLower(flat), "[dungeon gate]")
	if marker < 0 {
		return -1, "", 0
	}
	fields := strings.Fields(flat)
	if len(fields) < 2 {
		return -1, "", 0
	}
	areaID, err := strconv.Atoi(fields[0])
	if err != nil {
		return -1, "", 0
	}
	if paths := dumpPathPattern.FindAllStringSubmatch(block, -1); len(paths) > 0 {
		mapPath = paths[0][1]
	}
	for _, field := range strings.Fields(flat[marker+len("[dungeon gate]"):]) {
		if strings.HasPrefix(field, "[") {
			break
		}
		if value, err := strconv.Atoi(field); err == nil {
			gate = value
			break
		}
	}
	return areaID, mapPath, gate
}

var dumpPathPattern = regexp.MustCompile("`([^`]+)`")

func dumpPath(path string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
}

// tagValue returns the first integer written under one [tag] of a PVF text.
func tagValue(body, tag string) int {
	lower := strings.ToLower(body)
	index := strings.Index(lower, "["+tag+"]")
	if index < 0 {
		return 0
	}
	for _, line := range strings.Split(body[index+len(tag)+2:], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			return 0
		}
		value, err := strconv.Atoi(strings.Fields(trimmed)[0])
		if err != nil {
			return 0
		}
		return value
	}
	return 0
}

func tagName(body string) string {
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		if !strings.EqualFold(strings.TrimSpace(line), "[name]") {
			continue
		}
		for _, next := range lines[index+1:] {
			trimmed := strings.TrimSpace(next)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "[") {
				return ""
			}
			return trimmed
		}
	}
	return ""
}

func histogram(values map[string]int) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
	}
	return strings.Join(parts, " ")
}

func histogramInt(values map[int]int) string {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d=%d", key, values[key]))
	}
	return strings.Join(parts, " ")
}

// armorMaterial returns the material directory of an armor item path, for
// example "plate" in equipment/character/fighter/equip/jacket/plate/xx.equ.
func armorMaterial(path string) string {
	parts := strings.Split(dumpPath(path), "/")
	for _, part := range parts {
		switch part {
		case "cloth", "larmor", "harmor", "plate", "larmor2", "harmor2":
			return part
		}
	}
	return ""
}
