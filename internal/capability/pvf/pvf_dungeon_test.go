package pvf

import (
	"reflect"
	"testing"
)

func TestTownAreaDungeonIDsReadsEveryMarker(t *testing.T) {
	// A town area may open several dungeons, and every marker carries its own id
	// before the next tag.
	block := "\n0 `Example/Gate.map`\n`[gate]`\n`[dungeon gate]`\n14\n`[dungeon gate]`\n151\n[/area]"
	if ids := townAreaDungeonIDs(block); !reflect.DeepEqual(ids, []int{14, 151}) {
		t.Fatalf("ids=%v, want the two marked dungeons", ids)
	}
	for _, body := range []string{
		"`[normal]`\n0 `Example/Plain.map`",
		// A marker whose id is missing must not swallow the next tag's numbers.
		"`[dungeon gate]`\n`[gate]`\n474 234",
	} {
		if ids := townAreaDungeonIDs(body); len(ids) != 0 {
			t.Fatalf("body %q produced ids %v", body, ids)
		}
	}
}

func TestDungeonEntryLevelOfReadsTheLevelGate(t *testing.T) {
	// The minimum required level is the gate the game enforces; basis level is
	// only how the encounter is tuned.
	body := "[name]\n`Dark`\n[basis level]\n23\n[minimum required level]\n21\n"
	if level := dungeonEntryLevelOf(body); level != 21 {
		t.Fatalf("level=%d, want the minimum required level 21", level)
	}
	if level := dungeonEntryLevelOf("[name]\n`Plain`\n[basis level] 55\n"); level != 55 {
		t.Fatalf("level=%d, want the basis level fallback 55", level)
	}
	if level := dungeonEntryLevelOf("[name]\n`Unknown`\n"); level != 0 {
		t.Fatalf("level=%d, want 0 for a dungeon that states no level", level)
	}
}

func TestExtractMapListProjectsDungeonEntryLevel(t *testing.T) {
	geometry := []byte("[virtual movable area]\n10 20 300 100\n[/virtual movable area]\n")
	a := &pvfArchive{files: map[string]*pvfFile{
		"town/town.lst": {Data: []byte("1 `Example.twn`")},
		"town/example.twn": {Data: []byte(
			"[name]\n`Example`\n[limit level]\n40\n" +
				"[area]\n0 `Example/Gate.map`\n`[gate]`\n`[dungeon gate]`\n14\n[/area]\n" +
				"[area]\n1 `Example/Deep.map`\n`[gate]`\n`[dungeon gate]`\n151\n[/area]\n" +
				"[area]\n2 `Example/Plain.map`\n`[normal]`\n[/area]\n[end]\n")},
		"map/example/gate.map":  {Data: geometry},
		"map/example/deep.map":  {Data: geometry},
		"map/example/plain.map": {Data: geometry},
		"dungeon/dungeon.lst":   {Data: []byte("14 `Act2/DarkEntrance.dgn`\n151 `Act11/Deep.dgn`\n7 `Act1/Unused.dgn`")},
		"dungeon/act2/darkentrance.dgn": {Data: []byte(
			"[name]\n`Dark`\n[basis level]\n23\n[minimum required level]\n21\n[end]\n")},
		"dungeon/act11/deep.dgn": {Data: []byte("[name]\n`Deep`\n[basis level]\n55\n[end]\n")},
	}}
	maps := extractMapList(a, "town/town.lst", "town/")
	if len(maps) != 3 {
		t.Fatalf("maps=%+v", maps)
	}
	if !maps[0].DungeonGate || maps[0].DungeonLevel != 21 {
		t.Fatalf("first entrance=%+v, want the dungeon's minimum required level 21", maps[0])
	}
	if !maps[1].DungeonGate || maps[1].DungeonLevel != 55 {
		t.Fatalf("second entrance=%+v, want the basis level fallback 55", maps[1])
	}
	// A map without an entrance keeps no dungeon level, so placement leaves the
	// robot at the level it drew.
	if maps[2].DungeonGate || maps[2].DungeonLevel != 0 {
		t.Fatalf("plain map=%+v, want no dungeon level", maps[2])
	}
}
