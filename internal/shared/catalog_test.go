package shared

import "testing"

// TestItemNameBlocked pins the operator's barred name fragments. The fleet may
// never be granted release placeholders (后续确认), event-only stock (活动),
// rental stock (租), or clone 装扮 (克隆): the first three look wrong or
// incomplete in a live world, and the placeholder also carries no gameplay data
// at all.
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
	} {
		if got := ItemNameBlocked(testCase.name); got != testCase.want {
			t.Errorf("ItemNameBlocked(%q) = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}
