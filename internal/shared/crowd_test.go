package shared

import "testing"

func TestCrowdMapRuleAllowsStall(t *testing.T) {
	cases := []struct {
		name string
		rule CrowdMapRule
		kind ExpertJobStoreKind
		held int
		want bool
	}{
		{"zero rule hosts nothing", CrowdMapRule{}, ExpertJobStoreDisjoint, 0, false},
		{"closed map refuses its own trade", CrowdMapRule{Closed: true, StallKind: ExpertJobStoreEnchant, StallMax: 1}, ExpertJobStoreEnchant, 0, false},
		{"pinned trade admits the first stall", CrowdMapRule{StallKind: ExpertJobStoreEnchant, StallMax: 1}, ExpertJobStoreEnchant, 0, true},
		{"pinned trade is full", CrowdMapRule{StallKind: ExpertJobStoreEnchant, StallMax: 1}, ExpertJobStoreEnchant, 1, false},
		{"pinned trade refuses the other one", CrowdMapRule{StallKind: ExpertJobStoreEnchant, StallMax: 1}, ExpertJobStoreDisjoint, 0, false},
		{"no trade requested", CrowdMapRule{StallKind: ExpertJobStoreEnchant, StallMax: 1}, ExpertJobStoreNone, 0, false},
		{"unlimited admits any count", CrowdMapRule{StallKind: ExpertJobStoreDisjoint, StallMax: CrowdStallUnlimited}, ExpertJobStoreDisjoint, 99, true},
		{"unlimited still pins the trade", CrowdMapRule{StallKind: ExpertJobStoreDisjoint, StallMax: CrowdStallUnlimited}, ExpertJobStoreEnchant, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rule.AllowsStall(tc.kind, tc.held); got != tc.want {
				t.Fatalf("AllowsStall(%s, held=%d) = %t, want %t", tc.kind.Name(), tc.held, got, tc.want)
			}
		})
	}
}
