package cn90

import (
	"fmt"
	"os"
	"testing"
)

// TestLiveDumpCrowdFacts is a read-only reconnaissance helper: it projects the
// town catalog the runtime actually loads and writes the crowd-relevant facts
// (portal links, Seria exits, dungeon gates, arrival points) of every area so
// they can be checked against an independently decoded dump. It writes a UTF-8
// text file and never touches the game or database.
func TestLiveDumpCrowdFacts(t *testing.T) {
	if os.Getenv("CN90_LIVE_DUMP_CROWD_FACTS") == "" {
		t.Skip("set CN90_LIVE_DUMP_CROWD_FACTS=1 to dump crowd facts")
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
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}

	out := os.Getenv("CN90_LIVE_DUMP_CROWD_FACTS_OUT")
	if out == "" {
		out = "C:\\Users\\wangh\\.local\\bin\\jobs\\eef80d7d\\tmp\\crowd_facts.txt"
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	seriaExits, dungeonGates, withGate := 0, 0, 0
	for _, mp := range catalogs.TownMaps {
		links := "(none)"
		if len(mp.Links) > 0 {
			links = ""
			for _, link := range mp.Links {
				links += fmt.Sprintf("v%d/a%d ", link.Village, link.Area)
			}
		}
		flags := ""
		if mp.SeriaExit {
			flags += " SERIA_EXIT"
			seriaExits++
		}
		if mp.DungeonGate {
			flags += " DUNGEON_GATE"
			dungeonGates++
		}
		if mp.GateX != 0 || mp.GateY != 0 {
			flags += fmt.Sprintf(" gate@%d,%d", mp.GateX, mp.GateY)
			withGate++
		}
		fmt.Fprintf(file, "v%d/a%d links=%s%s\n", mp.Village, mp.Area, links, flags)
	}
	t.Logf("wrote %s (%d areas, %d seria exits, %d dungeon gates, %d arrival points)",
		out, len(catalogs.TownMaps), seriaExits, dungeonGates, withGate)
}
