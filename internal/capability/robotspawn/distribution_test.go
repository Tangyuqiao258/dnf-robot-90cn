package robotspawn

import (
	"testing"

	"robot/internal/shared"
)

type zeroRandom struct{}

func (zeroRandom) RandBetween(min, max int) int { return min }

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
		target, ok := BalancedFamilyLocation(zeroRandom{}, maps, 80, locations, shared.MapAreaKey{})
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
		target, ok := BalancedFamilyLocation(zeroRandom{}, maps, 80, locations, shared.MapAreaKey{})
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
	target, ok := BalancedFamilyLocation(zeroRandom{}, maps, 80, locations, shared.MapAreaKey{Village: 2, Area: 2})
	if !ok {
		t.Fatal("BalancedFamilyLocation returned no target")
	}
	if target.Map.Village == 2 && target.Map.Area == 2 {
		t.Fatalf("relocation kept the current area: %+v", target)
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
