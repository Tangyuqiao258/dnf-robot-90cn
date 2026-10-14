package robotspawn

import (
	"testing"

	"robot/internal/shared"
)

type zeroRandom struct{}

func (zeroRandom) RandBetween(min, max int) int { return min }

// stationEnv is a three-map world with two markets: 2/9 enchants, 3/7
// disassembles, and 4/1 is an ordinary map that pins disassemblers without a
// cap. The ordinary map comes first in the catalog on purpose: plain coverage
// spreading would always pick it, so a test that expects a market proves the
// stationing rule ran. It doubles as the crowd policy, since the spawn
// capability reads the policy off the environment it is handed.
type stationEnv struct {
	zeroRandom
}

func (stationEnv) CrowdMapRule(village, area int) shared.CrowdMapRule {
	switch {
	case village == 2 && area == 9:
		return shared.CrowdMapRule{StallKind: shared.ExpertJobStoreEnchant, StallMax: 1}
	case village == 3 && area == 7:
		return shared.CrowdMapRule{StallKind: shared.ExpertJobStoreDisjoint, StallMax: 1}
	default:
		return shared.CrowdMapRule{StallKind: shared.ExpertJobStoreDisjoint, StallMax: shared.CrowdStallUnlimited}
	}
}

var (
	stationOrdinary = shared.MapAreaKey{Village: 4, Area: 1}
	stationEnchant  = shared.MapAreaKey{Village: 2, Area: 9}
	stationDisjoint = shared.MapAreaKey{Village: 3, Area: 7}
)

func stationMaps() []shared.MapCatalogItem {
	rectangle := func(xMin int) []shared.MapRectangle {
		return []shared.MapRectangle{{XMin: xMin, XMax: xMin + 100, YMin: 0, YMax: 100}}
	}
	return []shared.MapCatalogItem{
		{Village: stationOrdinary.Village, Area: stationOrdinary.Area, Use: true, Rectangles: rectangle(0)},
		{Village: stationEnchant.Village, Area: stationEnchant.Area, Use: true, Rectangles: rectangle(200)},
		{Village: stationDisjoint.Village, Area: stationDisjoint.Area, Use: true, Rectangles: rectangle(400)},
	}
}

func stationTarget(t *testing.T, wanted shared.ExpertJobStoreKind, held []shared.MapLocation) shared.MapAreaKey {
	t.Helper()
	target, ok := BalancedFamilyLocation(stationEnv{}, stationMaps(), 80, held, shared.MapAreaKey{}, wanted)
	if !ok {
		t.Fatal("BalancedFamilyLocation returned no target")
	}
	return shared.MapAreaKey{Village: target.Map.Village, Area: target.Map.Area}
}

func mirrorMaps() []shared.MapCatalogItem {
	maps := []shared.MapCatalogItem{
		{Village: 3, Area: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 3, Area: 2, Use: true, Rectangles: []shared.MapRectangle{{XMin: 500, XMax: 600, YMin: 0, YMax: 100}}},
	}
	for area := 1; area <= 10; area++ {
		maps = append(maps, shared.MapCatalogItem{
			Village: 2, Area: area, Use: true,
			Rectangles: []shared.MapRectangle{{XMin: 1000, XMax: 1100, YMin: 0, YMax: 100}},
		})
	}
	return maps
}

func TestBalancedFamilyLocationFillsEmptyAreasFirst(t *testing.T) {
	maps := mirrorMaps()
	var locations []shared.MapLocation
	seen := map[shared.MapAreaKey]bool{}
	for index := 0; index < len(maps); index++ {
		target, ok := BalancedFamilyLocation(zeroRandom{}, maps, 80, locations, shared.MapAreaKey{}, shared.ExpertJobStoreNone)
		if !ok {
			t.Fatal("BalancedFamilyLocation returned no target")
		}
		key := shared.MapAreaKey{Village: target.Map.Village, Area: target.Map.Area}
		if seen[key] {
			t.Fatalf("area %v was filled twice before every area had a robot", key)
		}
		seen[key] = true
		locations = append(locations, shared.MapLocation{Village: key.Village, Area: key.Area, X: target.X, Y: target.Y})
	}
	if len(seen) != len(maps) {
		t.Fatalf("covered %d areas, want %d", len(seen), len(maps))
	}
}

func TestBalancedFamilyLocationDoesNotMultiplyMirrorInstances(t *testing.T) {
	maps := mirrorMaps()
	// One robot per area completes coverage; the mirror family now holds ten
	// robots while the two single-instance families hold one each.
	var locations []shared.MapLocation
	for _, mp := range maps {
		locations = append(locations, shared.MapLocation{Village: mp.Village, Area: mp.Area})
	}
	village3 := 0
	for index := 0; index < 2; index++ {
		target, ok := BalancedFamilyLocation(zeroRandom{}, maps, 80, locations, shared.MapAreaKey{}, shared.ExpertJobStoreNone)
		if !ok {
			t.Fatal("BalancedFamilyLocation returned no target")
		}
		if target.Map.Village == 3 {
			village3++
		}
		locations = append(locations, shared.MapLocation{Village: target.Map.Village, Area: target.Map.Area, X: target.X, Y: target.Y})
	}
	if village3 != 2 {
		t.Fatalf("mirror family took robots while single-instance families were emptier: village3=%d", village3)
	}
}

func TestBalancedFamilyLocationAvoidsCurrentArea(t *testing.T) {
	maps := []shared.MapCatalogItem{
		{Village: 2, Area: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 2, Area: 2, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
	}
	locations := []shared.MapLocation{
		{Village: 2, Area: 1},
		{Village: 2, Area: 1},
		{Village: 2, Area: 2},
	}
	target, ok := BalancedFamilyLocation(zeroRandom{}, maps, 80, locations, shared.MapAreaKey{Village: 2, Area: 2}, shared.ExpertJobStoreNone)
	if !ok {
		t.Fatal("BalancedFamilyLocation returned no target")
	}
	if target.Map.Village == 2 && target.Map.Area == 2 {
		t.Fatalf("relocation kept the current area: %+v", target)
	}
}

func TestBalancedFamilyLocationStationsTradesOnTheirMarkets(t *testing.T) {
	cases := []struct {
		name   string
		wanted shared.ExpertJobStoreKind
		want   shared.MapAreaKey
	}{
		{name: "enchanter", wanted: shared.ExpertJobStoreEnchant, want: stationEnchant},
		{name: "disassembler", wanted: shared.ExpertJobStoreDisjoint, want: stationDisjoint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stationTarget(t, tc.wanted, nil); got != tc.want {
				t.Fatalf("stationed at %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBalancedFamilyLocationLeavesMarketsItCannotTake(t *testing.T) {
	cases := []struct {
		name   string
		wanted shared.ExpertJobStoreKind
		held   []shared.MapLocation
	}{
		{
			name: "market already held", wanted: shared.ExpertJobStoreDisjoint,
			held: []shared.MapLocation{{Village: stationDisjoint.Village, Area: stationDisjoint.Area}},
		},
		{
			name: "enchanting market already held", wanted: shared.ExpertJobStoreEnchant,
			held: []shared.MapLocation{{Village: stationEnchant.Village, Area: stationEnchant.Area}},
		},
		{
			name: "no trade", wanted: shared.ExpertJobStoreNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stationTarget(t, tc.wanted, tc.held)
			if got == stationEnchant || got == stationDisjoint {
				t.Fatalf("took a market that was held, foreign or unclaimed by a trade: %v", got)
			}
			if got != stationOrdinary {
				t.Fatalf("fell back to %v, want the ordinary map %v", got, stationOrdinary)
			}
		})
	}
}

func TestCrowdStallStationOnlyAnswersForCappedMarkets(t *testing.T) {
	env := stationEnv{}
	if !CrowdStallStation(env, 3, 7, shared.ExpertJobStoreDisjoint, 0) {
		t.Fatal("empty capped dungeon gate must be a station")
	}
	if CrowdStallStation(env, 3, 7, shared.ExpertJobStoreDisjoint, 1) {
		t.Fatal("a full dungeon gate must not be a station")
	}
	if CrowdStallStation(env, 3, 7, shared.ExpertJobStoreEnchant, 0) {
		t.Fatal("a market must not host the other trade")
	}
	if CrowdStallStation(env, 4, 1, shared.ExpertJobStoreDisjoint, 0) {
		t.Fatal("an unlimited market must not be a station")
	}
	if CrowdStallStation(nil, 3, 7, shared.ExpertJobStoreDisjoint, 0) {
		t.Fatal("a manager without a policy must have no stations")
	}
}

func TestCrowdedCountsMirrorInstancesAsOneFamily(t *testing.T) {
	maps := []shared.MapCatalogItem{
		{Village: 2, Area: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 2, Area: 2, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 3, Area: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 1000, YMin: 0, YMax: 1000}}},
	}
	var locations []shared.MapLocation
	for index := 0; index < 8; index++ {
		locations = append(locations, shared.MapLocation{Village: 2, Area: 1 + index%2})
	}
	if !Crowded(maps, locations, 2, 1, 4, 2, nil) {
		t.Fatal("mirror family with an empty other village must count as crowded")
	}
	if Crowded(maps, nil, 2, 1, 4, 2, nil) {
		t.Fatal("empty directory must not be crowded")
	}
	if Crowded(maps, locations, 9, 9, 4, 2, nil) {
		t.Fatal("unknown area must not be crowded")
	}
}
