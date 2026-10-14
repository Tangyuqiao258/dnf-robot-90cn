package shared

import "testing"

// TestItemNameBlocked pins the operator's barred name fragments. The fleet may
// never be granted release placeholders (后续确认), event-only stock (活动), or
// rental stock (租): all three look wrong or incomplete in a live world, and the
// first also carries no gameplay data at all.
func TestItemNameBlocked(t *testing.T) {
	for _, testCase := range []struct {
		name string
		want bool
	}{
		{"", false},
		{"追之契约-魔界重甲", false},
		{"+12 泰拉石巨剑", false},
		{"后续确认用道具", true},
		{"新春活动称号", true},
		{"邪龙的血饮 (活动)", true},
		{"赛丽亚的祝福 [活动]", true},
		{"+12 强化券 (租赁)", true},
		{"泰拉石巨剑租", true},
	} {
		if got := ItemNameBlocked(testCase.name); got != testCase.want {
			t.Errorf("ItemNameBlocked(%q) = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}
