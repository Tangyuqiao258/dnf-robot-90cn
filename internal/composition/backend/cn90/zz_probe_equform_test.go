package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestZZProbeEquForm dumps what an .equ file actually says before its
// [equipment type] line, for three groups of records: the human character's
// worn gear, the level-90 plate sets the operator expects the fleet to wear,
// and a control that the compatibility gate leaves alone. The gate marks a
// record ClientIncompatible when any [add ...] appears before [equipment type];
// this is the raw text that rule reads, so it settles whether the rule is
// catching a real client-breaking form or a form the live client renders daily.
// Enable with CN90_PROBE_EQUFORM=1.
func TestZZProbeEquForm(t *testing.T) {
	if os.Getenv("CN90_PROBE_EQUFORM") == "" {
		t.Skip("set CN90_PROBE_EQUFORM=1")
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
	out := filepath.Join(os.TempDir(), "equform_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	type target struct {
		group string
		id    int
		path  string
	}
	targets := []target{
		// Every record below is worn right now by character 1 in the live lab
		// database: the client draws all of them, so any pre-type form one of
		// them carries is proven renderable.
		{"human-worn", 400330106, "equipment/character/common/title/chn_400330106.equ"},
		{"human-worn", 100090352, "equipment/character/common/jacket/plate/100090352.equ"},
		{"human-worn", 100190317, "equipment/character/common/shoulder/plate/100190317.equ"},
		{"human-worn", 100140347, "equipment/character/common/pants/plate/100140347.equ"},
		{"human-worn", 100290338, "equipment/character/common/shoes/plate/100290338.equ"},
		{"human-worn", 100240324, "equipment/character/common/belt/plate/100240324.equ"},
		{"human-worn", 400300070, "equipment/character/common/amulet/chn_400300070.equ"},
		{"human-worn", 400310070, "equipment/character/common/wrist/chn_400310070.equ"},
		{"human-worn", 400320074, "equipment/character/common/ring/chn_400320074.equ"},
		{"human-worn", 400340204, "equipment/character/common/support/chn_400340204.equ"},
		{"human-worn", 100352734, "equipment/character/common/magicstone/100352734.equ"},
		{"human-worn", 400390004, "equipment/character/common/earring/chn_400390004.equ"},
		{"lvl90-plate-set-12587", 100090367, "equipment/character/common/jacket/plate/100090367.equ"},
		{"lvl90-plate-set-12587", 100190333, "equipment/character/common/shoulder/plate/100190333.equ"},
		{"lvl90-plate-set-12587", 100140362, "equipment/character/common/pants/plate/100140362.equ"},
		{"lvl90-plate-set-12587", 100290353, "equipment/character/common/shoes/plate/100290353.equ"},
		{"lvl90-plate-set-12587", 100240340, "equipment/character/common/belt/plate/100240340.equ"},
		{"lvl90-plate-set-12629", 100090428, "equipment/character/common/jacket/plate/100090428.equ"},
		// Also flagged, also loaded by the client: everything below sits in
		// character 1's bag or warehouse and is drawn in the inventory grid.
		{"human-bag", 31343, "equipment/character/gunner/weapon/automatic/auto_supreme.equ"},
		{"human-bag", 140124, "equipment/character/common/jacket/plate/coat_mt_2choro125.equ"},
		{"human-bag", 200124, "equipment/character/common/pants/plate/pant_mt_2choro125.equ"},
		{"human-bag", 260124, "equipment/character/common/shoulder/plate/shd_mt_2choro125.equ"},
		{"human-bag", 320124, "equipment/character/common/belt/plate/belt_mt_2choro125.equ"},
		{"human-bag", 380124, "equipment/character/common/shoes/plate/shoe_mt_2choro125.equ"},
		{"human-bag", 100350881, "equipment/character/common/magicstone/100350881.equ"},
		{"human-bag", 101000728, "equipment/character/swordman/weapon/ssword/101000728.equ"},
		{"human-bag", 400330014, "equipment/character/common/title/chn_title_sao_400330014.equ"},
		{"control-clean", 100050203, "equipment/character/common/jacket/cloth/100050203.equ"},
		{"control-clean", 100050283, "equipment/character/common/jacket/cloth/100050283.equ"},
	}

	say("PVF=%s\n\n", pvfPath)
	for _, item := range targets {
		text, err := archive.ReadText(item.path)
		if err != nil {
			say("=== [%s] %d  READ ERROR: %v\n\n", item.group, item.id, err)
			continue
		}
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		typeLine := -1
		for i, line := range lines {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "[equipment type]") {
				typeLine = i
				break
			}
		}
		addBefore := 0
		tags := make([]string, 0, 8)
		limit := typeLine
		if limit < 0 {
			limit = len(lines)
		}
		for i := 0; i < limit; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(strings.ToLower(trimmed), "[add ") {
				addBefore++
				if len(tags) < 8 {
					tags = append(tags, trimmed)
				}
			}
		}
		say("=== [%s] %d  typeLine=%d  [add] before type=%d\n", item.group, item.id, typeLine, addBefore)
		say("    tags: %v\n", tags)
		stop := typeLine + 1
		if stop > len(lines) {
			stop = len(lines)
		}
		for i := 0; i < stop; i++ {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			say("    %3d| %s\n", i, lines[i])
		}
		say("\n")
	}

	// Catalog-wide census: how often each pre-type tag shows up across every
	// .equ in the archive, and how the two forms that trigger the gate compare.
	// A gate whose whole blast radius is [add price] would be killing records
	// over a price adjustment the client plainly ignores.
	say("=== pre-type [add] census over every .equ ===\n")
	tagCount := map[string]int{}
	filesWithAdd := 0
	filesTotal := 0
	priced := 0
	for key := range archive.paths {
		if !strings.HasPrefix(key, "equipment/") || !strings.HasSuffix(key, ".equ") {
			continue
		}
		filesTotal++
		text, err := archive.ReadText(key)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		found := false
		priceOnly := true
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "[equipment type]") {
				break
			}
			if !strings.HasPrefix(lower, "[add ") {
				continue
			}
			found = true
			tagCount[lower]++
			if lower != "[add price]" {
				priceOnly = false
			}
		}
		if found {
			filesWithAdd++
		}
		if found && priceOnly {
			priced++
		}
	}
	say("equ files=%d  with a pre-type [add]=%d  of which [add price] only=%d\n", filesTotal, filesWithAdd, priced)
	ranked := make([]string, 0, len(tagCount))
	for tag := range tagCount {
		ranked = append(ranked, tag)
	}
	sort.Slice(ranked, func(i, j int) bool { return tagCount[ranked[i]] > tagCount[ranked[j]] })
	for i, tag := range ranked {
		if i >= 25 {
			break
		}
		say("   %-40s %d\n", tag, tagCount[tag])
	}

	t.Logf("wrote .equ form probe to %s", out)
}
