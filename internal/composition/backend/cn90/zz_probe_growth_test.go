package cn90

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// zz_probe_growth_test.go is a TEMPORARY reconnaissance probe for the 外传职业
// self-awakening question: which jobs the archive releases no transfer branch
// for, and whether their .chr carries [awakening N] rows anyway.
// Enable with CN90_PROBE_GROWTH=1.

func TestZZProbeGrowth(t *testing.T) {
	if os.Getenv("CN90_PROBE_GROWTH") == "" {
		t.Skip("set CN90_PROBE_GROWTH=1")
	}
	pvfPath := os.Getenv("CN90_PROBE_GROWTH_PVF")
	if pvfPath == "" {
		root := liveRuntimeRoot(t)
		layout, err := resolveRuntimeLayout(root)
		if err != nil {
			t.Fatal(err)
		}
		pvfPath, _, err = layout.pvfPath()
		if err != nil {
			t.Fatal(err)
		}
	}
	out := os.Getenv("CN90_PROBE_GROWTH_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "growth_probe.txt")
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }
	say("PVF=%s\n", pvfPath)

	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	jobs := make([]int, 0, len(catalogs.StatTables))
	for job := range catalogs.StatTables {
		jobs = append(jobs, job)
	}
	sort.Ints(jobs)

	say("\n=== per job: released branches | growtype rows | awakening rows ===\n")
	say("%-4s %-12s %-14s %s\n", "job", "branches", "growtype", "awakening[n][stage]")
	for _, job := range jobs {
		tables := catalogs.StatTables[job]
		grow := make([]int, 0, 7)
		for n := 0; n <= 6; n++ {
			if tables.GrowtypeSet[n] {
				grow = append(grow, n)
			}
		}
		awake := make([]string, 0, 21)
		for n := 0; n <= 6; n++ {
			for stage := 1; stage <= 2; stage++ {
				if tables.AwakenSet[n][stage] {
					awake = append(awake, fmt.Sprintf("%d/%d", n, stage))
				}
			}
		}
		say("%-4d %-12v %-14v %v\n", job, catalogs.JobGrows[job], grow, awake)
	}

	say("\n=== jobs with no released branch ===\n")
	for _, job := range jobs {
		if len(catalogs.JobGrows[job]) > 0 {
			continue
		}
		tables := catalogs.StatTables[job]
		say("job=%d branches=%v growtype1=%v awaken1=%v awaken2=%v\n",
			job, catalogs.JobGrows[job], tables.GrowtypeSet[1],
			tables.AwakenSet[1][1], tables.AwakenSet[1][2])
	}

	say("\n=== fallback jobs: %v ===\n", catalogs.StatFallbackJobs)

	// Raw text of the 外传 .chr files so the [growtype]/[awakening] layout is
	// visible rather than inferred.
	say("\n=== raw character.lst ===\n")
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	lst, _ := archive.ReadText("character/character.lst")
	say("%s\n", lst)
	for _, match := range probeListEntry.FindAllStringSubmatch(lst, -1) {
		if match[1] != "9" && match[1] != "10" && match[1] != "0" {
			continue
		}
		path := probePath(match[2])
		if path == "" {
			continue
		}
		body, err := archive.ReadText(path)
		if err != nil || body == "" {
			say("\n----- job=%s path=%s <unreadable: %v> -----\n", match[1], path, err)
			continue
		}
		say("\n----- job=%s path=%s (len=%d) -----\n%s\n", match[1], path, len(body), trimTo(body, 3000))
	}

	t.Logf("wrote growth probe to %s", out)
}
