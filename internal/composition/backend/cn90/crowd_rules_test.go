package cn90

import (
	"testing"

	"robot/internal/shared"
)

// crowdTestMap builds one projected town map. Only the fields the crowd rules
// read are set, so a test states a world instead of a PVF.
func crowdTestMap(village, area int, links []shared.MapAreaKey, mark func(*shared.MapCatalogItem)) shared.MapCatalogItem {
	mp := shared.MapCatalogItem{Village: village, Area: area, Use: true, Links: links}
	if mark != nil {
		mark(&mp)
	}
	return mp
}

// crowdTestWorld is a main town square, its gate onto a road, and a second town
// behind it whose Seria exit sits two transitions past the road.
//
//	39/0 gate -- 38/2 road -- 38/0 exit -- 38/1 dungeon entrance
//	  |                                        \_ 38/3 square -- 38/4 outpost
//	39/1 square -- 39/2 square
func crowdTestWorld() []shared.MapCatalogItem {
	return []shared.MapCatalogItem{
		crowdTestMap(39, 0, []shared.MapAreaKey{{Village: 38, Area: 2}, {Village: 39, Area: 1}}, func(mp *shared.MapCatalogItem) { mp.SeriaExit = true }),
		crowdTestMap(39, 1, []shared.MapAreaKey{{Village: 39, Area: 0}, {Village: 39, Area: 2}}, nil),
		crowdTestMap(39, 2, []shared.MapAreaKey{{Village: 39, Area: 1}}, nil),
		crowdTestMap(38, 2, []shared.MapAreaKey{{Village: 39, Area: 0}, {Village: 38, Area: 0}}, nil),
		crowdTestMap(38, 0, []shared.MapAreaKey{{Village: 38, Area: 2}, {Village: 38, Area: 1}, {Village: 38, Area: 3}}, func(mp *shared.MapCatalogItem) { mp.SeriaExit = true }),
		crowdTestMap(38, 1, []shared.MapAreaKey{{Village: 38, Area: 0}}, func(mp *shared.MapCatalogItem) { mp.DungeonGate = true }),
		crowdTestMap(38, 3, []shared.MapAreaKey{{Village: 38, Area: 0}, {Village: 38, Area: 4}}, nil),
		crowdTestMap(38, 4, []shared.MapAreaKey{{Village: 38, Area: 3}}, nil),
	}
}

func TestCrowdRulesPinStallsToTheirMapRole(t *testing.T) {
	rules := NewCrowdRules(crowdTestWorld())
	cases := []struct {
		name      string
		village   int
		area      int
		wantKind  shared.ExpertJobStoreKind
		wantMax   int
		exempt    bool
		hostsAny  bool
		maxRobots int
	}{
		{name: "main city gate enchants and may fill", village: 39, area: 0, wantKind: shared.ExpertJobStoreEnchant, wantMax: crowdStallPerMap, exempt: true, hostsAny: true},
		{name: "seria exit enchants once", village: 38, area: 0, wantKind: shared.ExpertJobStoreEnchant, wantMax: crowdStallPerMap, hostsAny: true},
		{name: "dungeon entrance disassembles once", village: 38, area: 1, wantKind: shared.ExpertJobStoreDisjoint, wantMax: crowdStallPerMap, hostsAny: true},
		{name: "ordinary square disassembles freely", village: 38, area: 3, wantKind: shared.ExpertJobStoreDisjoint, wantMax: shared.CrowdStallUnlimited, hostsAny: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := rules.CrowdMapRule(tc.village, tc.area)
			if rule.StallKind != tc.wantKind || rule.StallMax != tc.wantMax {
				t.Fatalf("stall rule = %s/%d, want %s/%d", rule.StallKind.Name(), rule.StallMax, tc.wantKind.Name(), tc.wantMax)
			}
			if rule.CrowdExempt != tc.exempt {
				t.Fatalf("CrowdExempt = %t, want %t", rule.CrowdExempt, tc.exempt)
			}
			if got := rule.AllowsStall(tc.wantKind, 0); got != tc.hostsAny {
				t.Fatalf("AllowsStall(%s) = %t, want %t", tc.wantKind.Name(), got, tc.hostsAny)
			}
		})
	}
	if max := rules.CrowdMapRule(38, 0).StallMax; !rules.CrowdMapRule(38, 0).AllowsStall(shared.ExpertJobStoreEnchant, max-1) {
		t.Fatal("a Seria exit must admit the stall up to its cap")
	}
	if rules.CrowdMapRule(38, 0).AllowsStall(shared.ExpertJobStoreEnchant, crowdStallPerMap) {
		t.Fatal("a Seria exit must refuse a second enchanting stall")
	}
	if rules.CrowdMapRule(38, 1).AllowsStall(shared.ExpertJobStoreEnchant, 0) {
		t.Fatal("a dungeon entrance must refuse an enchanting stall")
	}
}

func TestCrowdRulesConnectorHostsNoStall(t *testing.T) {
	rules := NewCrowdRules(crowdTestWorld())
	if role := rules.Role(38, 2); role != CrowdRoleConnector {
		t.Fatalf("road 38/2 role = %s, want %s", role, CrowdRoleConnector)
	}
	rule := rules.CrowdMapRule(38, 2)
	if rule.AllowsStall(shared.ExpertJobStoreDisjoint, 0) || rule.AllowsStall(shared.ExpertJobStoreEnchant, 0) {
		t.Fatal("a connector road must host no stall at all")
	}
	if rule.Closed {
		if !crowdConnectorEmpty(38, 2) {
			t.Fatal("a closed connector must be one the draw empties")
		}
		return
	}
	if rule.MaxRobots != crowdConnectorMaxRobots {
		t.Fatalf("open connector MaxRobots = %d, want %d", rule.MaxRobots, crowdConnectorMaxRobots)
	}
}

func TestCrowdRulesDistanceClosesFarMaps(t *testing.T) {
	rules := NewCrowdRules(crowdTestWorld())
	if rules.CrowdMapRule(38, 1).Closed {
		t.Fatal("a town is unrestricted while the player's map is unknown")
	}
	rules.SetPlayerMap(39, 0)
	if village, area := rules.PlayerMap(); village != 39 || area != 0 {
		t.Fatalf("PlayerMap = %d/%d, want 39/0", village, area)
	}
	// The walk starts from the map the player stands on, not from their whole
	// town: the neighbouring squares of 39 are one transition out, not zero.
	distances := rules.MapDistances()
	for key, want := range map[shared.MapAreaKey]int{
		{Village: 39, Area: 0}: 0,
		{Village: 39, Area: 1}: 1,
		{Village: 38, Area: 2}: 1,
		{Village: 39, Area: 2}: 2,
		{Village: 38, Area: 0}: 2,
		{Village: 38, Area: 1}: 3,
		{Village: 38, Area: 3}: 3,
		{Village: 38, Area: 4}: 4,
	} {
		if got := distances[key]; got != want {
			t.Fatalf("distance of %v = %d, want %d", key, got, want)
		}
	}
	// Three transitions out is still around the corner: the operator asked for a
	// world that looks alive within three switches, so those maps stay open.
	for _, key := range []shared.MapAreaKey{{Village: 38, Area: 1}, {Village: 38, Area: 3}} {
		if rule := rules.CrowdMapRule(key.Village, key.Area); rule.Closed {
			t.Fatalf("map %v three transitions from the player must stay open: %+v", key, rule)
		}
	}
	if rule := rules.CrowdMapRule(38, 4); !rule.Closed || rule.AllowsRobot() {
		t.Fatalf("map 38/4 four transitions from the player must be closed: %+v", rule)
	}
	// A player inside a dungeon instance stands on a map no town catalog
	// describes. That must not empty the world they left behind.
	rules.SetPlayerMap(7000, 3)
	if village, area := rules.PlayerMap(); village != 39 || area != 0 {
		t.Fatalf("an unknown map must keep the previous seed, got %d/%d", village, area)
	}
	rules.SetPlayerMap(0, 0)
	if rules.CrowdMapRule(38, 1).Closed {
		t.Fatal("clearing the player's map must reopen the world")
	}
	if distances := rules.MapDistances(); distances != nil {
		t.Fatalf("MapDistances = %v, want nil after clearing", distances)
	}
}

func TestCrowdRulesRoleCounts(t *testing.T) {
	counts := NewCrowdRules(crowdTestWorld()).RoleCounts()
	want := map[CrowdMapRole]int{
		CrowdRoleNormal:          4,
		CrowdRoleSeriaExit:       1,
		CrowdRoleMainCityGate:    1,
		CrowdRoleDungeonEntrance: 1,
		CrowdRoleConnector:       1,
	}
	for role, count := range want {
		if counts[role] != count {
			t.Fatalf("role %s = %d maps, want %d (all: %v)", role, counts[role], count, counts)
		}
	}
}
