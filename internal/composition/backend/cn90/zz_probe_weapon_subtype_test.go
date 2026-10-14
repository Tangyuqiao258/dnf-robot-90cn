package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// zz_probe_weapon_subtype_test.go is a TEMPORARY reconnaissance probe for the
// weapon sub-type question. Enable with CN90_PROBE_WEAPON=1.

var probeListEntry = regexp.MustCompile("(\\d+)\\s+`([^`]+)`")

func probePath(path string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
}

type probeRec struct {
	id      string
	path    string
	race    string
	subtype string
}

func probeWeaponRecords(lst string) []probeRec {
	recs := []probeRec{}
	for _, m := range probeListEntry.FindAllStringSubmatch(lst, -1) {
		p := probePath(m[2])
		if !strings.Contains(p, "/weapon/") {
			continue
		}
		full := p
		if !strings.HasPrefix(full, "equipment/") {
			full = "equipment/" + full
		}
		parts := strings.Split(full, "/")
		rel := strings.TrimPrefix(p, "equipment/")
		if len(parts) < 6 || parts[0] != "equipment" || parts[1] != "character" {
			recs = append(recs, probeRec{m[1], rel, "?", "?"})
			continue
		}
		recs = append(recs, probeRec{m[1], rel, parts[2], parts[4]})
	}
	return recs
}

func TestZZProbeWeaponSubtype(t *testing.T) {
	if os.Getenv("CN90_PROBE_WEAPON") == "" {
		t.Skip("set CN90_PROBE_WEAPON=1")
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
	out := os.Getenv("CN90_PROBE_WEAPON_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "weapon_subtype_probe.txt")
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }
	say("PVF=%s\n", pvfPath)

	lst, _ := archive.ReadText("equipment/equipment.lst")
	if lst == "" {
		t.Fatal("equipment.lst empty")
	}
	recs := probeWeaponRecords(lst)
	say("weapon records in equipment.lst = %d\n", len(recs))

	jobBlock := regexp.MustCompile(`(?is)\[usable job\](.*?)\[/usable job\]`)
	reqJobSkill := regexp.MustCompile(`(?is)\[required job skill\](.*?)\[/required job skill\]`)
	anyTag := regexp.MustCompile(`(?m)^\s*\[([^\]]+)\]`)
	// tag names containing job/weapon/skill, to prove what exists.
	jobishTags := map[string]int{}
	jobBlocks := map[string]map[string]int{}
	reqBlocks := map[string]map[string]int{}
	eqTypeInts := map[string]map[string]int{}
	subTypes := map[string]map[string]int{}
	groupNames := map[string]map[string]int{}
	namesById := map[string]string{}

	for _, r := range recs {
		body, err := archive.ReadText("equipment/" + r.path)
		if err != nil || body == "" {
			continue
		}
		key := r.race + "/" + r.subtype
		for _, tm := range anyTag.FindAllStringSubmatch(body, -1) {
			low := strings.ToLower(tm[1])
			if strings.Contains(low, "job") || strings.Contains(low, "weapon") || strings.Contains(low, "skill") {
				jobishTags["["+tm[1]+"]"]++
			}
		}
		add := func(m map[string]map[string]int, value string) {
			if m[key] == nil {
				m[key] = map[string]int{}
			}
			m[key][value]++
		}
		if m := jobBlock.FindStringSubmatch(body); m != nil {
			add(jobBlocks, strings.Join(strings.Fields(strings.ReplaceAll(m[1], "\n", " ")), " "))
		} else {
			add(jobBlocks, "<none>")
		}
		if m := reqJobSkill.FindStringSubmatch(body); m != nil {
			add(reqBlocks, strings.Join(strings.Fields(strings.ReplaceAll(m[1], "\n", " ")), " "))
		} else {
			add(reqBlocks, "<none>")
		}
		if name := tagTextOf(body, "name"); name != "" {
			namesById[r.id] = name
		}
		_ = namesById
		add(eqTypeInts, tagLineAfter(body, "equipment type"))
		add(subTypes, tagLineAfter(body, "sub type"))
		if n := tagTextOf(body, "item group name"); n != "" {
			add(groupNames, n)
		} else {
			add(groupNames, "<none>")
		}
	}

	keys := sortedStringKeys(jobBlocks)
	say("\n=== [usable job] / [required job skill] per race/subtype ===\n")
	for _, k := range keys {
		say("%s (n=%d)\n", k, sumCounts(jobBlocks[k]))
		say("  usable job :\n")
		for _, v := range rankedKeys(jobBlocks[k], 12) {
			say("      %s x%d\n", v, jobBlocks[k][v])
		}
		say("  required job skill:\n")
		for _, v := range rankedKeys(reqBlocks[k], 12) {
			say("      %s x%d\n", v, reqBlocks[k][v])
		}
	}

	say("\n=== weapon-subtype tags: [equipment type] trailing int / [sub type] / [item group name] ===\n")
	for _, k := range keys {
		say("%s\n  equipment type ints: %s\n  sub type: %s\n  item group: %s\n", k,
			formatCounts(eqTypeInts[k]), formatCounts(subTypes[k]), formatCounts(groupNames[k]))
	}

	say("\n=== tag names containing job/weapon/skill (whole archive of weapon records) ===\n")
	say("%s\n", formatCounts(jobishTags))

	// Armor control group: do armor records carry [required job skill]?
	armorReq := map[string]int{}
	armorJob := map[string]int{}
	armorCount := 0
	for _, m := range probeListEntry.FindAllStringSubmatch(lst, -1) {
		p := probePath(m[2])
		if !strings.Contains(p, "/jacket/") && !strings.Contains(p, "/pants/") &&
			!strings.Contains(p, "/shoulder/") && !strings.Contains(p, "/shoes/") &&
			!strings.Contains(p, "/waist/") {
			continue
		}
		body, err := archive.ReadText("equipment/" + strings.TrimPrefix(p, "equipment/"))
		if err != nil || body == "" {
			continue
		}
		armorCount++
		if m := reqJobSkill.FindStringSubmatch(body); m != nil {
			armorReq[strings.Join(strings.Fields(strings.ReplaceAll(m[1], "\n", " ")), " ")]++
		} else {
			armorReq["<none>"]++
		}
		if m := jobBlock.FindStringSubmatch(body); m != nil {
			armorJob[strings.Join(strings.Fields(strings.ReplaceAll(m[1], "\n", " ")), " ")]++
		} else {
			armorJob["<none>"]++
		}
	}
	say("\n=== armor (jacket/pants/shoulder/shoes/waist) control group, n=%d ===\n", armorCount)
	say("  [usable job]: %s\n", formatCounts(armorJob))
	say("  [required job skill]: %s\n", formatCounts(armorReq))

	// One record per (race, subtype) that DOES and DOES NOT carry the tag.
	say("\n=== raw samples ===\n")
	dumped := map[string]int{}
	for _, r := range recs {
		key := r.race + "/" + r.subtype
		if dumped[key] >= 1 && !(r.subtype == "beamsword" && dumped[key] < 3) {
			continue
		}
		body, err := archive.ReadText("equipment/" + r.path)
		if err != nil || body == "" {
			continue
		}
		if reqJobSkill.FindStringSubmatch(body) == nil {
			continue
		}
		dumped[key]++
		if dumped[key] > 2 {
			continue
		}
		say("\n----- %s id=%s path=%s -----\n%s\n", key, r.id, r.path, trimTo(body, 2600))
	}

	t.Logf("wrote probe to %s", out)
}

// tagTextOf returns the first backticked value that follows a [tag] line.
func tagTextOf(body, tag string) string {
	lower := strings.ToLower(body)
	idx := strings.Index(lower, "["+strings.ToLower(tag)+"]")
	if idx < 0 {
		return ""
	}
	rest := body[idx:]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[nl+1:]
	}
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			return ""
		}
		return strings.Trim(strings.TrimSpace(line), "`")
	}
	return ""
}

// tagLineAfter returns the raw text of the line following "[" + tag + "]" as a
// single space-joined string, which for [equipment type] is "`[weapon]` 21".
func tagLineAfter(body, tag string) string {
	lower := strings.ToLower(body)
	idx := strings.Index(lower, "["+strings.ToLower(tag)+"]")
	if idx < 0 {
		return "<absent>"
	}
	rest := body[idx+len(tag)+2:]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[nl+1:]
	}
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			return "<absent>"
		}
		return line
	}
	return "<absent>"
}

// TestZZProbeSkill33 resolves the skill ids named by the lightsaber
// [required job skill] block: skill/swordman.lst entry 33 and skill id 95 of
// the female swordman list.
func TestZZProbeSkill33(t *testing.T) {
	if os.Getenv("CN90_PROBE_WEAPON") == "" {
		t.Skip("set CN90_PROBE_WEAPON=1")
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
	out := filepath.Join(os.TempDir(), "skill_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	mainList, _ := archive.ReadText("skill/skilllist.lst")
	say("=== skill/skilllist.lst ===\n%s\n", mainList)
	for _, m := range probeListEntry.FindAllStringSubmatch(mainList, -1) {
		listPath := probePath(m[2])
		if !strings.HasSuffix(listPath, ".lst") {
			continue
		}
		if !strings.HasPrefix(listPath, "skill/") {
			listPath = "skill/" + listPath
		}
		body, err := archive.ReadText(listPath)
		if err != nil {
			continue
		}
		say("\n=== %s (job=%s) ===\n%s\n", listPath, m[1], body)
		for _, e := range probeListEntry.FindAllStringSubmatch(body, -1) {
			if e[1] != "33" && e[1] != "95" {
				continue
			}
			sklPath := probePath(e[2])
			if !strings.HasSuffix(sklPath, ".skl") {
				sklPath += ".skl"
			}
			if !strings.HasPrefix(sklPath, "skill/") {
				sklPath = "skill/" + sklPath
			}
			skl, err := archive.ReadText(sklPath)
			if err != nil {
				continue
			}
			say("\n----- %s id=%s -----\n%s\n", sklPath, e[1], trimTo(skl, 1600))
		}
	}
	t.Logf("wrote skill probe to %s", out)
}

func trimTo(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func sumCounts(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

func sortedStringKeys(m map[string]map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func rankedKeys(m map[string]int, limit int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}

func formatCounts(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for _, k := range rankedKeys(m, 12) {
		parts = append(parts, fmt.Sprintf("%q x%d", k, m[k]))
	}
	return strings.Join(parts, "  ")
}
