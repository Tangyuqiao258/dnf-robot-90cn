package shared

import "testing"

// TestItemNameBlocked pins the operator's barred name fragments. The fleet may
// never be granted release placeholders (后续确认), event-only stock (活动),
// rental stock (租), clone 装扮 (克隆), GM-only 装扮, the (旧) superseded copies,
// or any name the archive brackets: the [网吧] weapons, the 异界 skill armour and
// [幸运星] all reach the same rule.
func TestItemNameBlocked(t *testing.T) {
	for _, testCase := range []struct {
		name string
		want bool
	}{
		{"", false},
		{"追之契约-魔界重甲", false},
		{"+12 泰拉石巨剑", false},
		{"稀有装扮帽子 (鬼剑士)", false},
		{"后续确认用道具", true},
		{"新春活动称号", true},
		{"邪龙的血饮 (活动)", true},
		{"赛丽亚的祝福 [活动]", true},
		{"+12 强化券 (租赁)", true},
		{"泰拉石巨剑租", true},
		// Real archive records, read out of the live catalog.
		{"克隆装扮帽子 (鬼剑士)", true},
		{"稀有克隆装扮帽子 (鬼剑士)", true},
		{"克隆装扮礼盒", true},
		{"稀有克隆装扮礼盒 (部位)", true},
		{"神圣稀有克隆装扮套装礼盒", true},
		// GM-only pieces, the (旧) copies, and bracketed names -- read out of the
		// live catalog too, next to the pieces the rules must leave alone.
		{"暗黑骑士戾芒头盔 GM", true},
		{"暗影魅月魔音长发", false},
		{"(旧) 棉布长袍", true},
		{"破旧的棉布衬衫", false},
		{"骸麒魔剑 [网吧", true},
		{"[幸运星]", true},
		{"幸运星礼盒", false},
	} {
		if got := ItemNameBlocked(testCase.name); got != testCase.want {
			t.Errorf("ItemNameBlocked(%q) = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}
