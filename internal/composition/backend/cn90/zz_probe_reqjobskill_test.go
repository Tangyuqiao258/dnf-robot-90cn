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

// TestZZProbeRequiredJobSkill scans every record of equipment/equipment.lst for
// the [required job skill] tag and reports where it appears. TEMPORARY.
func TestZZProbeRequiredJobSkill(t *testing.T) {
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
	out := filepath.Join(os.TempDir(), "reqjobskill_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	lst, _ := archive.ReadText("equipment/equipment.lst")
	reqBlock := regexp.MustCompile(`(?is)\[required job skill\](.*?)\[/required job skill\]`)
	weaponTypeTag := regexp.MustCompile(`(?i)\[weapon type\]`)
	groupName := regexp.MustCompile(`(?is)\[item group name\]`)
	byHead := map[string]int{}
	byBlock := map[string]int{}
	scanned := 0
	weaponTypeHits := 0
	groupNameHits := 0
	for _, m := range probeListEntry.FindAllStringSubmatch(lst, -1) {
		p := probePath(m[2])
		rel := strings.TrimPrefix(p, "equipment/")
		body, err := archive.ReadText("equipment/" + rel)
		if err != nil || body == "" {
			continue
		}
		scanned++
		if weaponTypeTag.MatchString(body) {
			weaponTypeHits++
			if weaponTypeHits <= 8 {
				say("  [weapon type] present id=%s path=%s\n", m[1], rel)
			}
		}
		if groupName.MatchString(body) {
			groupNameHits++
		}
		block := reqBlock.FindStringSubmatch(body)
		if block == nil {
			continue
		}
		parts := strings.Split(rel, "/")
		head := rel
		if len(parts) >= 5 && parts[0] == "character" {
			head = strings.Join(parts[:5], "/")
		} else if len(parts) > 2 {
			head = strings.Join(parts[:2], "/")
		}
		byHead[head]++
		byBlock[strings.Join(strings.Fields(strings.ReplaceAll(block[1], "\n", " ")), " ")]++
	}

	say("scanned equipment records = %d\n", scanned)
	say("[required job skill] records = %d\n", sumCounts(byHead))
	say("[weapon type] tag hits = %d\n", weaponTypeHits)
	say("[item group name] hits = %d\n", groupNameHits)
	say("\n--- by path head ---\n")
	heads := make([]string, 0, len(byHead))
	for k := range byHead {
		heads = append(heads, k)
	}
	sort.Strings(heads)
	for _, k := range heads {
		say("  %s x%d\n", k, byHead[k])
	}
	say("\n--- block text ---\n")
	for _, v := range rankedKeys(byBlock, 50) {
		say("  %q x%d\n", v, byBlock[v])
	}
	t.Logf("wrote probe to %s", out)
}
