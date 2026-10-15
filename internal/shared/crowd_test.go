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

func TestCrowdAreaPolicyApplyLaysTheFloorWhereTheMapCanHoldIt(t *testing.T) {
	policy := CrowdAreaPolicy{MaxPerArea: 15, TransitSlots: 2, MinPerArea: 5}
	cases := []struct {
		name string
		rule CrowdMapRule
		want CrowdMapRule
	}{
		{
			name: "an ordinary map takes the cap and the floor",
			rule: CrowdMapRule{StallKind: ExpertJobStoreDisjoint, StallMax: CrowdStallUnlimited},
			want: CrowdMapRule{StallKind: ExpertJobStoreDisjoint, StallMax: CrowdStallUnlimited, MaxRobots: 15, TransitSlots: 2, MinRobots: 5},
		},
		{
			name: "a road the world caps below the floor keeps its own count and still lets two pass",
			rule: CrowdMapRule{MaxRobots: 2},
			want: CrowdMapRule{MaxRobots: 2, TransitSlots: 2},
		},
		{
			name: "a map the world caps between the two takes the floor",
			rule: CrowdMapRule{MaxRobots: 8},
			want: CrowdMapRule{MaxRobots: 8, TransitSlots: 2, MinRobots: 5},
		},
		{
			name: "a closed map holds nobody and wants nobody",
			rule: CrowdMapRule{Closed: true},
			want: CrowdMapRule{Closed: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.Apply(39, 0, tc.rule); got != tc.want {
				t.Fatalf("Apply(39/0, %+v) = %+v, want %+v", tc.rule, got, tc.want)
			}
		})
	}
	closed := CrowdAreaPolicy{ClosedVillages: []int{111}, MinPerArea: 5}
	if got := closed.Apply(111, 3, CrowdMapRule{}); !got.Closed || got.MinRobots != 0 {
		t.Fatalf("a closed village kept a floor: %+v", got)
	}
	// A floor is the operator's, so turning the per-area cap off must not turn
	// the floor off with it.
	uncapped := CrowdAreaPolicy{MinPerArea: 5}
	if got := uncapped.Apply(39, 0, CrowdMapRule{}); got.MinRobots != 5 || got.MaxRobots != 0 {
		t.Fatalf("uncapped map = %+v, want the floor alone", got)
	}
}

func TestCrowdAreaPolicyApplyGivesGatheringPlacesTheirOwnCap(t *testing.T) {
	gathering := CrowdMapRule{CrowdExempt: true, StallKind: ExpertJobStoreEnchant, StallMax: 1}
	policy := CrowdAreaPolicy{MaxPerArea: 15, TransitSlots: 2, HotPerArea: 20}
	got := policy.Apply(39, 0, gathering)
	want := CrowdMapRule{CrowdExempt: true, StallKind: ExpertJobStoreEnchant, StallMax: 1, MaxRobots: 20, TransitSlots: 2}
	if got != want {
		t.Fatalf("gathering place = %+v, want %+v", got, want)
	}
	// Everything else stays on the ordinary cap, exemption and all cleared.
	if got := policy.Apply(39, 1, CrowdMapRule{}); got.MaxRobots != 15 || got.TransitSlots != 2 {
		t.Fatalf("ordinary map = %+v, want the ordinary cap", got)
	}
	// With the hot cap off, a gathering place is capped like any other map: the
	// old behaviour, kept for an operator who sets no hot number.
	if got := (CrowdAreaPolicy{MaxPerArea: 15}).Apply(39, 0, gathering); got.MaxRobots != 15 || got.CrowdExempt {
		t.Fatalf("gathering place without a hot cap = %+v, want the ordinary cap", got)
	}
	// A hot cap no greater than the ordinary one changes nothing either.
	if got := (CrowdAreaPolicy{MaxPerArea: 25, HotPerArea: 20}).Apply(39, 0, gathering); got.MaxRobots != 25 || got.CrowdExempt {
		t.Fatalf("gathering place under a small hot cap = %+v, want the ordinary cap", got)
	}
	// The hot cap stands on its own: an operator who caps nothing else still
	// wants the maps a player stands in to stop somewhere.
	alone := CrowdAreaPolicy{HotPerArea: 20}
	if got := alone.Apply(39, 0, gathering); got.MaxRobots != 20 || !got.CrowdExempt {
		t.Fatalf("gathering place with no ordinary cap = %+v, want the hot cap alone", got)
	}
	if got := alone.Apply(39, 1, CrowdMapRule{}); got.MaxRobots != 0 {
		t.Fatalf("ordinary map with no ordinary cap = %+v, want it untouched", got)
	}
	// A closed village still closes over the hot cap.
	if got := (CrowdAreaPolicy{ClosedVillages: []int{39}, HotPerArea: 20}).Apply(39, 0, gathering); !got.Closed {
		t.Fatalf("closed gathering place = %+v, want it closed", got)
	}
}
