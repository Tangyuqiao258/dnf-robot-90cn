package pvf

import (
	"testing"
)

// fixtureText builds the text reader the item list is parsed through. The
// fixtures are handed over as already-decoded text rather than as archive bytes
// because of what Archive.text does to a body: it cleans the whole decoded text
// with cleanPVFString, which trims "`[]" off its ends. Real bodies open with the
// #PVF_File header the script decoder writes, so that trim lands on the header;
// a fixture body written to open with [name] would lose the bracket and the tag
// would stop matching.
//
// The bytes-to-text step is pinned where it can be pinned honestly: by the
// decoder's own test (DecodePVFBytes prefers GBK, and a GBK name whose bytes also
// read as Big5 is the case it exists for) and by the standing probe over the live
// archive.
//
// The names and paths below are the live archive's own records, so the cases pin
// the operator's rule against what the fleet actually draws from.
func fixtureText(files map[string]string) func(string) string {
	return func(path string) string { return files[normalizePVFPath(path)] }
}

// TestExtractItemListBlocksUnresolvableRecords pins the operator's rule: an item
// the client cannot resolve must never be granted. The game client and the GM
// tool both resolve an item by its [name] record, so a nameless .equ is exactly
// the "no icon, no info" entry that broke their item windows. 勇者 gear is
// barred by the operator's separate rarity rule, event/rental stock and clone
// 装扮 by name.
func TestExtractItemListBlocksUnresolvableRecords(t *testing.T) {
	readText := fixtureText(map[string]string{
		"equipment/equipment.lst": "101030240 `character/swordman/weapon/hsword/101030240.equ`\n" +
			"100230379 `character/common/belt/harmor/100230379.equ`\n" +
			"100080387 `character/common/jacket/harmor/100080387.equ`\n" +
			"27151 `character/swordman/weapon/ssword/27151.equ`\n" +
			"10163950 `character/swordman/weapon/club/club_10163950.equ`\n" +
			"42600 `character/swordman/avatar/cap/acap_clonesw.equ`\n" +
			"42601 `character/swordman/avatar/cap/acap_rareclonesw.equ`\n" +
			"29300 `character/swordman/avatar/cap/acap_rare.equ`",
		"equipment/character/swordman/weapon/hsword/101030240.equ":     "[name]\r\n`细雪之舞`\r\n[rarity]\r\n3\r\n[equipment type]\r\n`weapon`\r\n",
		"equipment/character/common/belt/harmor/100230379.equ":         "[rarity]\r\n3\r\n[equipment type]\r\n`waist`\r\n",
		"equipment/character/common/jacket/harmor/100080387.equ":       "[name]\r\n`追之契约-魔界重甲`\r\n[rarity]\r\n5\r\n[equipment type]\r\n`coat`\r\n",
		"equipment/character/swordman/weapon/ssword/27151.equ":         "[name]\r\n`邪龙的血饮 (活动)`\r\n[rarity]\r\n3\r\n[equipment type]\r\n`weapon`\r\n",
		"equipment/character/swordman/weapon/club/club_10163950.equ":   "[name]\r\n`+10 泰拉石图腾 (租赁)`\r\n[rarity]\r\n3\r\n[equipment type]\r\n`weapon`\r\n",
		"equipment/character/swordman/avatar/cap/acap_clonesw.equ":     "[name]\r\n`克隆装扮帽子 (鬼剑士)`\r\n[rarity]\r\n0\r\n[equipment type]\r\n`cap`\r\n",
		"equipment/character/swordman/avatar/cap/acap_rareclonesw.equ": "[name]\r\n`稀有克隆装扮帽子 (鬼剑士)`\r\n[rarity]\r\n2\r\n[equipment type]\r\n`cap`\r\n",
		"equipment/character/swordman/avatar/cap/acap_rare.equ":        "[name]\r\n`稀有装扮帽子 (鬼剑士)`\r\n[rarity]\r\n2\r\n[equipment type]\r\n`cap`\r\n",
	})

	seen := map[int]bool{}
	blocked := map[int]bool{}
	names := map[int]string{}
	for _, item := range extractItemListFromText(readText, "equipment/equipment.lst", "equipment/", false) {
		seen[item.ID] = true
		blocked[item.ID] = item.GrantBlocked
		names[item.ID] = item.Name
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
		{42600, "克隆装扮", true},
		{42601, "稀有克隆装扮", true},
		{29300, "the 稀有装扮 the fleet may wear", false},
	} {
		if !seen[testCase.id] {
			t.Fatalf("item %d (%s) missing from the catalog", testCase.id, testCase.why)
		}
		if blocked[testCase.id] != testCase.want {
			t.Errorf("item %d (%s) GrantBlocked = %v, want %v (name=%q)",
				testCase.id, testCase.why, blocked[testCase.id], testCase.want, names[testCase.id])
		}
	}
}

// TestExtractStackableListBlocksErrorStringPlaceholders covers the stackable
// half. A record whose [name] is the parser's ErrorString placeholder has no
// readable name either, so the same gate bars it -- and the 克隆装扮礼盒 that
// opens into a clone avatar is barred by name on the same pass.
func TestExtractStackableListBlocksErrorStringPlaceholders(t *testing.T) {
	readText := fixtureText(map[string]string{
		"stackable/stackable.lst": "10163950 `material/10163950.stk`\n" +
			"10163951 `material/10163951.stk`\n" +
			"2683623 `cash/chn_20140424_laborday_package/chn_clone_rare_avatarbox_2683623.stk`",
		"stackable/material/10163950.stk": "[name]\r\n`ErrorString`\r\n[stack limit]\r\n100\r\n",
		"stackable/material/10163951.stk": "[name]\r\n`无色小晶块`\r\n[stack limit]\r\n1000\r\n",
		"stackable/cash/chn_20140424_laborday_package/chn_clone_rare_avatarbox_2683623.stk": "[name]\r\n`克隆装扮礼盒`\r\n[stack limit]\r\n1\r\n",
	})
	items := extractItemListFromText(readText, "stackable/stackable.lst", "stackable/", true)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	byID := map[int]bool{}
	for _, item := range items {
		byID[item.ID] = item.GrantBlocked
	}
	if !byID[10163950] {
		t.Errorf("ErrorString placeholder 10163950 was left grantable")
	}
	if !byID[2683623] {
		t.Errorf("克隆装扮礼盒 2683623 was left grantable")
	}
	if byID[10163951] {
		t.Errorf("named stackable 10163951 was blocked")
	}
}
