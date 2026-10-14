package pvf

import (
	"strings"
	"testing"
)

// fileHead opens a fake PVF body the way the archive does. Real bodies start
// with #PVF_File (the script decoder writes it for compiled files), and without
// it the leading "[name]" would be eaten by the string trim in Archive.text,
// which is what the first version of this test tripped over.
const fileHead = "#PVF_File\r\n"

// TestExtractItemListBlocksUnresolvableRecords pins the operator's rule: an item
// the client cannot resolve must never be granted. The game client and the GM
// tool both resolve an item by its [name] record, so a nameless .equ is exactly
// the "no icon, no info" entry that broke their item windows. 勇者 gear is
// barred by the operator's separate rarity rule, and event/rental stock by name.
func TestExtractItemListBlocksUnresolvableRecords(t *testing.T) {
	archive := &pvfArchive{files: map[string]*pvfFile{
		"equipment/equipment.lst": {Data: []byte(strings.Join([]string{
			"101030240 `character/swordman/weapon/hsword/101030240.equ`",
			"100230379 `character/common/belt/harmor/100230379.equ`",
			"100080387 `character/common/jacket/harmor/100080387.equ`",
			"27151 `character/swordman/weapon/ssword/27151.equ`",
			"10163950 `character/swordman/weapon/club/club_10163950.equ`",
		}, "\n"))},
		"equipment/character/swordman/weapon/hsword/101030240.equ": {
			Data: []byte(fileHead + "[name]\r\n`细雪之舞`\r\n[rarity]\r\n3\r\n[equipment type]\r\n`weapon`\r\n"),
		},
		"equipment/character/common/belt/harmor/100230379.equ": {
			Data: []byte(fileHead + "[rarity]\r\n3\r\n[equipment type]\r\n`waist`\r\n"),
		},
		"equipment/character/common/jacket/harmor/100080387.equ": {
			Data: []byte(fileHead + "[name]\r\n`追之契约-魔界重甲`\r\n[rarity]\r\n5\r\n[equipment type]\r\n`coat`\r\n"),
		},
		"equipment/character/swordman/weapon/ssword/27151.equ": {
			Data: []byte(fileHead + "[name]\r\n`邪龙的血饮 (活动)`\r\n[rarity]\r\n3\r\n[equipment type]\r\n`weapon`\r\n"),
		},
		"equipment/character/swordman/weapon/club/club_10163950.equ": {
			Data: []byte(fileHead + "[name]\r\n`+10 泰拉石图腾 (租赁)`\r\n[rarity]\r\n3\r\n[equipment type]\r\n`weapon`\r\n"),
		},
	}}

	seen := map[int]bool{}
	blocked := map[int]bool{}
	for _, item := range extractItemList(archive, "equipment/equipment.lst", "equipment/", false) {
		seen[item.ID] = true
		blocked[item.ID] = item.GrantBlocked
	}
	for _, testCase := range []struct {
		id   int
		why  string
		want bool
	}{
		{101030240, "named ordinary gear", false},
		{100230379, "no [name] record", true},
		{100080387, "勇者 rarity 5", true},
		{27151, "活动 gear", true},
		{10163950, "租 rental gear", true},
	} {
		if !seen[testCase.id] {
			t.Fatalf("item %d (%s) missing from the catalog", testCase.id, testCase.why)
		}
		if blocked[testCase.id] != testCase.want {
			t.Errorf("item %d (%s) GrantBlocked = %v, want %v", testCase.id, testCase.why, blocked[testCase.id], testCase.want)
		}
	}
}

// TestExtractStackableListBlocksErrorStringPlaceholders covers the stackable
// half. A record whose [name] is the parser's ErrorString placeholder has no
// readable name either, so the same gate bars it.
func TestExtractStackableListBlocksErrorStringPlaceholders(t *testing.T) {
	archive := &pvfArchive{files: map[string]*pvfFile{
		"stackable/stackable.lst": {Data: []byte("10163950 `material/10163950.stk`\n10163951 `material/10163951.stk`")},
		"stackable/material/10163950.stk": {
			Data: []byte(fileHead + "[name]\r\n`ErrorString`\r\n[stack limit]\r\n100\r\n"),
		},
		"stackable/material/10163951.stk": {
			Data: []byte(fileHead + "[name]\r\n`无色小晶块`\r\n[stack limit]\r\n1000\r\n"),
		},
	}}
	items := extractItemList(archive, "stackable/stackable.lst", "stackable/", true)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	byID := map[int]bool{}
	for _, item := range items {
		byID[item.ID] = item.GrantBlocked
	}
	if !byID[10163950] {
		t.Errorf("ErrorString placeholder 10163950 was left grantable")
	}
	if byID[10163951] {
		t.Errorf("named stackable 10163951 was blocked")
	}
}
