package pvf

import (
	"testing"
)

const testCharacterFile = `[name]
` + "`swordman`" + `
[initial value]
[HP MAX] 100
[MP MAX] 50
[physical attack] 10
[growtype 1]
[HP MAX] 1
[growtype 2]
[HP MAX] 2
[awakening 1]
[HP MAX] 3
[awakening 2]
[HP MAX] 4
[growtype 3]
[HP MAX] 20
`

func TestParseCharacterStatTablesSplitsGrowthAndAwakeningRows(t *testing.T) {
	tables, err := parseCharacterStatTables(testCharacterFile)
	if err != nil {
		t.Fatal(err)
	}
	if tables.Base.HpMax != 1000 || tables.Base.MpMax != 500 || tables.Base.PhysAtk != 100 {
		t.Fatalf("base row=%+v", tables.Base)
	}
	if !tables.GrowtypeSet[1] || !tables.GrowtypeSet[2] || !tables.GrowtypeSet[3] {
		t.Fatalf("growtype rows set=%v", tables.GrowtypeSet)
	}
	if tables.GrowtypeSet[4] || tables.GrowtypeSet[5] || tables.GrowtypeSet[6] {
		t.Fatalf("unexpected growtype rows set=%v", tables.GrowtypeSet)
	}
	if tables.Growtype[1].HpMax != 10 || tables.Growtype[2].HpMax != 20 || tables.Growtype[3].HpMax != 200 {
		t.Fatalf("growtype rows=%v", tables.Growtype)
	}
	if !tables.AwakenSet[2][1] || !tables.AwakenSet[2][2] {
		t.Fatalf("awakening rows set=%v", tables.AwakenSet)
	}
	if tables.Awakening[2][1].HpMax != 30 || tables.Awakening[2][2].HpMax != 40 {
		t.Fatalf("awakening rows=%v", tables.Awakening)
	}
	if tables.AwakenSet[3][1] {
		t.Fatalf("unexpected awakening row set=%v", tables.AwakenSet)
	}
	if _, err := parseCharacterStatTables("[initial value]\n[HP MAX] 1\n"); err == nil {
		t.Fatal("file without [growtype 1] was accepted")
	}
}

func TestProjectLevelThresholdsAndLevelExpFor(t *testing.T) {
	archive := testTextArchive{"character/exptable.tbl": "0 100 300 600 1000"}
	thresholds := ProjectLevelThresholds(archive)
	if len(thresholds) != 5 || thresholds[2] != 300 {
		t.Fatalf("thresholds=%v", thresholds)
	}
	for level, want := range map[int]int{1: 0, 2: 0, 3: 100, 4: 300, 5: 600, 6: 1000, 99: 1000} {
		if got := LevelExpFor(thresholds, level); got != want {
			t.Fatalf("level %d exp=%d want %d", level, got, want)
		}
	}
	if got := LevelExpFor(nil, 50); got != 0 {
		t.Fatalf("empty thresholds exp=%d", got)
	}
}

func TestProjectCharacterStatCatalogFallsBackOnMalformedFile(t *testing.T) {
	archive := testTextArchive{
		"character/character.lst":         "0 `Swordman/Swordman.chr` 1 `Broken/Broken.chr`",
		"character/swordman/swordman.chr": testCharacterFile,
		"character/broken/broken.chr":     "[name]\n`broken`\n",
	}
	catalog, fallbacks := ProjectCharacterStatCatalog(archive)
	if len(catalog) != 2 {
		t.Fatalf("catalog=%v", catalog)
	}
	if catalog[0].Base.HpMax != 1000 {
		t.Fatalf("parsed job=%+v", catalog[0])
	}
	if len(fallbacks) != 1 || fallbacks[0] != 1 {
		t.Fatalf("fallback jobs=%v", fallbacks)
	}
	fallback := catalog[1]
	if !fallback.GrowtypeSet[1] || fallback.Base.HpMax != 1800 {
		t.Fatalf("fallback job=%+v", fallback)
	}
	// The fallback keeps provisioning working above level 1 because its growth
	// rows are zero-delta: the base row is all a later stat read would see.
	if fallback.Growtype[1].HpMax != 0 || fallback.Growtype[6].HpMax != 0 {
		t.Fatalf("fallback growth rows are not zero-delta: %+v", fallback.Growtype)
	}
}
