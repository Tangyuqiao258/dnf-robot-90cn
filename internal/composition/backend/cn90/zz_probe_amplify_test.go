package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	capabilitypvf "robot/internal/capability/pvf"
)

// TestZZProbeAmplifyTable dumps the archive facts the amplification spec needs:
// every job's released transfer branches (so the operator's per-branch attribute
// list can be keyed to real branch numbers) and etc/amplifyitem.etc, whose
// [option mapping table] is the only authority on what amplify attribute 1..4
// means. Enable with CN90_PROBE_AMPLIFY=1.
func TestZZProbeAmplifyTable(t *testing.T) {
	if os.Getenv("CN90_PROBE_AMPLIFY") == "" {
		t.Skip("set CN90_PROBE_AMPLIFY=1")
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
	out := filepath.Join(os.TempDir(), "amplify_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }

	say("PVF=%s\n\n=== [growtype name] per job ===\n", pvfPath)
	names := capabilitypvf.ProjectJobGrowNames(archive)
	jobs := make([]int, 0, len(names))
	for job := range names {
		jobs = append(jobs, job)
	}
	sort.Ints(jobs)
	for _, job := range jobs {
		say("job %2d: %q\n", job, names[job])
	}

	text, err := archive.ReadText("etc/amplifyitem.etc")
	if err != nil {
		t.Fatalf("read etc/amplifyitem.etc: %v", err)
	}
	say("\n=== etc/amplifyitem.etc (len %d) ===\n%s\n===END===\n", len(text), text)

	// The 外传 jobs carry no [growtype name] branch, but the operator says they
	// still have a first and second self-awakening. Dump their .chr so the
	// growtype sections say whether that is true.
	lst, _ := archive.ReadText("character/character.lst")
	for _, m := range probeListEntry.FindAllStringSubmatch(lst, -1) {
		if m[1] != "9" && m[1] != "10" && m[1] != "0" {
			continue
		}
		body, err := archive.ReadText(probePath("character/" + m[2]))
		if err != nil || body == "" {
			say("\n=== character/%s (job %s) UNREADABLE err=%v ===\n", m[2], m[1], err)
			continue
		}
		say("\n=== character/%s (job %s) growtype sections ===\n", m[2], m[1])
		for _, line := range strings.Split(body, "\n") {
			low := strings.ToLower(strings.TrimSpace(line))
			if strings.HasPrefix(low, "[growtype") || strings.HasPrefix(low, "[name]") || strings.HasPrefix(low, "[awakening") {
				say("%s\n", strings.TrimSpace(line))
			}
		}
	}
	t.Logf("wrote probe to %s", out)
}
