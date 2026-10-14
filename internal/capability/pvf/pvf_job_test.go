package pvf

import (
	"reflect"
	"testing"
)

func TestProjectJobGrowCatalogReadsReleasedBranches(t *testing.T) {
	archive := testTextArchive{
		"character/character.lst":         "0 `Swordman/Swordman.chr` 1 `Fighter/Fighter.chr` 2 `Gunner/Gunner.chr`",
		"character/swordman/swordman.chr": "[name]\r\n`swordman`\r\n[growtype name]\r\n`鬼剑士` `剑魂` `鬼泣` `//阿修罗` `狂战士`\r\n[growtype 1]\r\n`skill`\r\n[growtype 2]\r\n`skill`\r\n[growtype 3]\r\n`skill`\r\n[growtype 5]\r\n`skill`\r\n",
		"character/fighter/fighter.chr":   "[growtype name] `格斗家` `气功师` `散打`\r\n[growtype 2]\r\n`skill`\r\n[growtype 3]\r\n`skill`\r\n",
		"character/gunner/gunner.chr":     "[name]\r\n`gunner`\r\n",
	}

	got := ProjectJobGrowCatalog(archive)
	want := map[int][]int{0: {1, 2, 4}, 1: {1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("job grow catalog = %v, want %v", got, want)
	}
}

func TestParseJobGrowBranchesIgnoresMissingAndMalformedNames(t *testing.T) {
	if branches := parseJobGrowBranches("[name]\r\n`base`\r\n"); len(branches) != 0 {
		t.Fatalf("missing tag branches = %v", branches)
	}
	if branches := parseJobGrowBranches("[growtype name] `base` `` `//soon`"); len(branches) != 0 {
		t.Fatalf("placeholder-only branches = %v", branches)
	}
	// Values above the server's first-grow guard must not be offered.
	body := "[growtype name] `base` `a` `b` `c` `d` `e` `f`[growtype 2][growtype 3][growtype 4][growtype 5][growtype 6]"
	if branches := parseJobGrowBranches(body); !reflect.DeepEqual(branches, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("guarded branches = %v", branches)
	}
	// Branches without their [growtype N] stat section are not offered.
	body = "[growtype name] `base` `a` `b`[growtype 2]"
	if branches := parseJobGrowBranches(body); !reflect.DeepEqual(branches, []int{1}) {
		t.Fatalf("section-less branches = %v", branches)
	}
}

// The 90CN archive repeats the base class name in a sixth [growtype name]
// position for jobs 3, 5 and 6 (魔法师, 神枪手, 暗夜使者). Position 5 is not a
// transfer branch there, so offering it creates characters whose branch the
// server cannot resolve.
func TestParseJobGrowBranchesDropsRepeatedBaseClassName(t *testing.T) {
	body := "[growtype name] `魔法师` `元素师` `召唤师` `战斗法师` `魔道学者` `魔法师`" +
		"[growtype 1][growtype 2][growtype 3][growtype 4][growtype 5][growtype 6]"
	if branches := parseJobGrowBranches(body); !reflect.DeepEqual(branches, []int{1, 2, 3, 4}) {
		t.Fatalf("repeated base name branches = %v", branches)
	}
	// A genuinely released fifth branch keeps working: the male mage's 次元行者
	// differs from its base class name.
	body = "[growtype name] `魔法师` `元素爆破师` `冰结师` `血法师` `逐风者` `次元行者`" +
		"[growtype 1][growtype 2][growtype 3][growtype 4][growtype 5][growtype 6]"
	if branches := parseJobGrowBranches(body); !reflect.DeepEqual(branches, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("released fifth branch = %v", branches)
	}
}

func TestProjectJobGrowNamesKeepsPositionalNames(t *testing.T) {
	archive := testTextArchive{
		"character/character.lst": "3 `Mage/Mage.chr`",
		"character/mage/mage.chr": "[growtype name] `魔法师` `元素师` `//召唤师` `战斗法师`\r\n[growtype 2][growtype 4]",
	}
	got := ProjectJobGrowNames(archive)
	want := map[int][]string{3: {"魔法师", "元素师", "", "战斗法师"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("job grow names = %v, want %v", got, want)
	}
	if names := ProjectJobGrowNames(nil); len(names) != 0 {
		t.Fatalf("nil archive names = %v", names)
	}
}
