package robotspawn

import (
	"fmt"
	"sort"
	"strings"

	"robot/internal/shared"
)

type BalancedTarget struct {
	Map shared.MapCatalogItem
	X   int
	Y   int
}

type mapCandidate struct {
	mp         shared.MapCatalogItem
	rectangles []shared.MapRectangle
	weight     int
}

func DistributedTargets(env RangeRandom, maps []shared.MapCatalogItem, levels []int, locations []shared.MapLocation) ([]BalancedTarget, bool) {
	if env == nil || len(levels) == 0 {
		return nil, false
	}
	policy := CrowdPolicy(env)
	maps = AdmittedMaps(env, maps)
	candidates := make([]mapCandidate, 0, len(maps))
	for _, mp := range maps {
		if !mp.Use || mp.Village < 0 || mp.Area < 0 {
			continue
		}
		rectangles := NormalizeRectangles(MapRectangles(mp))
		if len(rectangles) == 0 {
			continue
		}
		candidates = append(candidates, mapCandidate{mp: mp, rectangles: rectangles, weight: SmoothedRectanglesWeight(rectangles)})
	}
	if len(candidates) == 0 {
		return nil, false
	}

	areaCounts := make(map[shared.MapAreaKey]int, len(candidates))
	areaLocations := make(map[shared.MapAreaKey][]shared.MapLocation, len(candidates))
	for _, location := range locations {
		area := shared.MapAreaKey{Village: location.Village, Area: location.Area}
		for _, candidate := range candidates {
			if mapAreaKey(candidate.mp) != area {
				continue
			}
			for _, rectangle := range candidate.rectangles {
				if RectangleContains(rectangle, location.X, location.Y) {
					areaCounts[area]++
					areaLocations[area] = append(areaLocations[area], location)
					break
				}
			}
			break
		}
	}

	order := make([]int, len(levels))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		return levels[order[i]] < levels[order[j]]
	})

	out := make([]BalancedTarget, len(levels))
	for _, outputIndex := range order {
		level := levels[outputIndex]
		emptyAreas := eligibleMapIndexes(candidates, areaCounts, level, true, policy)
		eligible := emptyAreas
		if len(emptyAreas) == 0 {
			eligible = eligibleMapIndexes(candidates, areaCounts, level, false, policy)
		}
		if len(eligible) == 0 {
			continue
		}

		chosen := 0
		if len(emptyAreas) > 0 {
			chosen = randomIndex(env, emptyAreas)
		} else {
			chosen = leastLoadedMapIndex(env, candidates, eligible, areaCounts)
		}

		candidate := candidates[chosen]
		area := mapAreaKey(candidate.mp)
		x, y, pointOK := bestRandomPoint(env, candidate.rectangles, areaLocations[area])
		if !pointOK {
			continue
		}
		areaCounts[area]++
		location := shared.MapLocation{Village: candidate.mp.Village, Area: candidate.mp.Area, X: x, Y: y}
		areaLocations[area] = append(areaLocations[area], location)
		out[outputIndex] = BalancedTarget{Map: candidate.mp, X: x, Y: y}
	}
	return out, true
}

func BalancedLocation(env RangeRandom, maps []shared.MapCatalogItem, level int, locations []shared.MapLocation) (BalancedTarget, bool) {
	targets, ok := DistributedTargets(env, maps, []int{level}, locations)
	if !ok || len(targets) == 0 || !targets[0].Map.Use {
		return BalancedTarget{}, false
	}
	return targets[0], true
}

func eligibleMapIndexes(candidates []mapCandidate, counts map[shared.MapAreaKey]int, level int, emptyOnly bool, policy shared.CrowdMapPolicy) []int {
	eligible := make([]int, 0, len(candidates))
	for index, candidate := range candidates {
		if candidate.mp.Level > level {
			continue
		}
		if emptyOnly && counts[mapAreaKey(candidate.mp)] > 0 {
			continue
		}
		if CrowdOccupied(policy, candidate.mp.Village, candidate.mp.Area, counts[mapAreaKey(candidate.mp)]) {
			continue
		}
		eligible = append(eligible, index)
	}
	return eligible
}

func leastLoadedMapIndex(env RangeRandom, candidates []mapCandidate, indexes []int, counts map[shared.MapAreaKey]int) int {
	best := []int{indexes[0]}
	for _, index := range indexes[1:] {
		bestIndex := best[0]
		left := counts[mapAreaKey(candidates[index].mp)] * candidates[bestIndex].weight
		right := counts[mapAreaKey(candidates[bestIndex].mp)] * candidates[index].weight
		switch {
		case left < right:
			best = []int{index}
		case left == right:
			best = append(best, index)
		}
	}
	return randomIndex(env, best)
}

func bestRandomPoint(env RangeRandom, rectangles []shared.MapRectangle, occupied []shared.MapLocation) (int, int, bool) {
	candidateCount := 1
	if len(occupied) > 0 {
		candidateCount = 8
	}
	bestX, bestY := 0, 0
	bestDistance := int64(-1)
	for index := 0; index < candidateCount; index++ {
		x, y, ok := randomPointFromNormalized(env, rectangles)
		if !ok {
			return 0, 0, false
		}
		distance := nearestDistanceSquared(x, y, occupied)
		if distance > bestDistance {
			bestX, bestY, bestDistance = x, y, distance
		}
	}
	return bestX, bestY, true
}

func nearestDistanceSquared(x, y int, occupied []shared.MapLocation) int64 {
	if len(occupied) == 0 {
		return 0
	}
	best := int64(^uint64(0) >> 1)
	for _, location := range occupied {
		dx := int64(x - location.X)
		dy := int64(y - location.Y)
		distance := dx*dx + dy*dy
		if distance < best {
			best = distance
		}
	}
	return best
}

func randomIndex(env RangeRandom, values []int) int {
	choice := env.RandBetween(0, len(values)-1)
	if choice < 0 || choice >= len(values) {
		choice = 0
	}
	return values[choice]
}

func mapAreaKey(mp shared.MapCatalogItem) shared.MapAreaKey {
	return shared.MapAreaKey{Village: mp.Village, Area: mp.Area}
}

// Crowded reports whether the map family occupying village/area is denser than
// the least dense spawn family by the given bias, with a family-size floor.
// Mirror instances share one family, so a family can be over-full even when
// every single instance looks acceptable. A map the crowd rules let grow denser
// than its neighbours is never reported crowded.
func Crowded(maps []shared.MapCatalogItem, locations []shared.MapLocation, village, area, floor, bias int, policy shared.CrowdMapPolicy) bool {
	if floor <= 0 || bias <= 0 {
		return false
	}
	if CrowdExempt(policy, village, area) {
		return false
	}
	index := make(map[shared.MapAreaKey]string, len(maps))
	weights := make(map[string]int, len(maps))
	for _, mp := range maps {
		if !mp.Use {
			continue
		}
		key := MapFamilyKey(mp)
		if key == "" {
			continue
		}
		index[mapAreaKey(mp)] = key
		if weights[key] == 0 {
			weights[key] = SmoothedRectanglesWeight(MapRectangles(mp))
		}
	}
	mineKey, ok := index[shared.MapAreaKey{Village: village, Area: area}]
	if !ok || weights[mineKey] <= 0 {
		return false
	}
	counts := make(map[string]int, len(weights))
	for _, location := range locations {
		if key, ok := index[shared.MapAreaKey{Village: location.Village, Area: location.Area}]; ok {
			counts[key]++
		}
	}
	mine := counts[mineKey]
	if mine <= floor {
		return false
	}
	leastKey := ""
	for key := range weights {
		if leastKey == "" {
			leastKey = key
			continue
		}
		left := counts[key] * weights[leastKey]
		right := counts[leastKey] * weights[key]
		if left < right {
			leastKey = key
		}
	}
	if leastKey == "" || leastKey == mineKey {
		return false
	}
	return mine*weights[leastKey] > counts[leastKey]*weights[mineKey]*bias
}

// MapFamilyKey returns a stable signature of a map's movement geometry. Server
// town lists contain mirror instances (channels) of the same map as separate
// areas; capacity planning must treat them as one logical map.
func MapFamilyKey(mp shared.MapCatalogItem) string {
	rectangles := NormalizeRectangles(MapRectangles(mp))
	if len(rectangles) == 0 {
		return ""
	}
	sort.Slice(rectangles, func(i, j int) bool {
		if rectangles[i].XMin != rectangles[j].XMin {
			return rectangles[i].XMin < rectangles[j].XMin
		}
		if rectangles[i].YMin != rectangles[j].YMin {
			return rectangles[i].YMin < rectangles[j].YMin
		}
		if rectangles[i].XMax != rectangles[j].XMax {
			return rectangles[i].XMax < rectangles[j].XMax
		}
		return rectangles[i].YMax < rectangles[j].YMax
	})
	var builder strings.Builder
	for _, rectangle := range rectangles {
		fmt.Fprintf(&builder, "%d,%d,%d,%d;", rectangle.XMin, rectangle.YMin, rectangle.XMax, rectangle.YMax)
	}
	return builder.String()
}

// BalancedFamilyLocation fills empty areas first, then balances across logical
// map families (unique geometry) by density and picks the least crowded mirror
// instance inside the chosen family. currentArea (when set) is avoided for the
// instance pick so a relocation actually changes map.
//
// wanted is the expert-job trade the robot being placed holds, or None when the
// caller does not know it. A trade the crowd rules pin to particular maps sends
// the robot to those maps first, one per market, before ordinary spreading.
func BalancedFamilyLocation(env RangeRandom, maps []shared.MapCatalogItem, level int, locations []shared.MapLocation, currentArea shared.MapAreaKey, wanted shared.ExpertJobStoreKind) (BalancedTarget, bool) {
	type family struct {
		key        string
		weight     int
		candidates []mapCandidate
		count      int
	}
	policy := CrowdPolicy(env)
	maps = AdmittedMaps(env, maps)
	families := make(map[string]*family, len(maps))
	order := make([]*family, 0, len(maps))
	areaCounts := make(map[shared.MapAreaKey]int, len(locations))
	for _, location := range locations {
		areaCounts[shared.MapAreaKey{Village: location.Village, Area: location.Area}]++
	}
	for _, mp := range maps {
		if !mp.Use || mp.Village < 0 || mp.Area < 0 || mp.Level > level {
			continue
		}
		if CrowdOccupied(policy, mp.Village, mp.Area, areaCounts[mapAreaKey(mp)]) {
			continue
		}
		rectangles := NormalizeRectangles(MapRectangles(mp))
		if len(rectangles) == 0 {
			continue
		}
		key := MapFamilyKey(mp)
		group := families[key]
		if group == nil {
			group = &family{key: key, weight: SmoothedRectanglesWeight(rectangles)}
			families[key] = group
			order = append(order, group)
		}
		group.candidates = append(group.candidates, mapCandidate{mp: mp, rectangles: rectangles})
	}
	if len(order) == 0 {
		return BalancedTarget{}, false
	}
	targetFrom := func(candidate mapCandidate) (BalancedTarget, bool) {
		occupied := make([]shared.MapLocation, 0, areaCounts[mapAreaKey(candidate.mp)])
		for _, location := range locations {
			if location.Village == candidate.mp.Village && location.Area == candidate.mp.Area {
				occupied = append(occupied, location)
			}
		}
		x, y, pointOK := bestRandomPoint(env, candidate.rectangles, occupied)
		if !pointOK {
			return BalancedTarget{}, false
		}
		return BalancedTarget{Map: candidate.mp, X: x, Y: y}, true
	}

	// Markets first: a robot whose trade the rules pin to particular maps is
	// stationed where that trade can open, one robot per map, before ordinary
	// spreading starts. Without this an enchanter lands wherever the draw puts
	// it and never opens the stall its trade exists for.
	stations := make([]mapCandidate, 0, len(maps))
	for _, group := range order {
		for _, candidate := range group.candidates {
			if areaCounts[mapAreaKey(candidate.mp)] > 0 {
				continue
			}
			if CrowdStallStation(policy, candidate.mp.Village, candidate.mp.Area, wanted, 0) {
				stations = append(stations, candidate)
			}
		}
	}
	if len(stations) > 0 {
		indexes := make([]int, len(stations))
		for index := range indexes {
			indexes[index] = index
		}
		return targetFrom(stations[randomIndex(env, indexes)])
	}

	// Coverage first: every enterable map should hold robots before any map
	// takes a second one, otherwise mirror-heavy villages leave large town
	// areas empty.
	emptyAreas := make([]mapCandidate, 0, len(maps))
	for _, group := range order {
		for _, candidate := range group.candidates {
			if areaCounts[mapAreaKey(candidate.mp)] == 0 {
				emptyAreas = append(emptyAreas, candidate)
			}
		}
	}
	if len(emptyAreas) > 0 {
		indexes := make([]int, len(emptyAreas))
		for index := range indexes {
			indexes[index] = index
		}
		return targetFrom(emptyAreas[randomIndex(env, indexes)])
	}

	for _, group := range order {
		for _, candidate := range group.candidates {
			group.count += areaCounts[mapAreaKey(candidate.mp)]
		}
	}
	best := []int{0}
	for index := 1; index < len(order); index++ {
		left := order[index].count * order[best[0]].weight
		right := order[best[0]].count * order[index].weight
		switch {
		case left < right:
			best = []int{index}
		case left == right:
			best = append(best, index)
		}
	}
	chosen := order[randomIndex(env, best)]

	instanceIndexes := make([]int, 0, len(chosen.candidates))
	for index := range chosen.candidates {
		if currentArea.Village > 0 && len(chosen.candidates) > 1 && mapAreaKey(chosen.candidates[index].mp) == currentArea {
			continue
		}
		instanceIndexes = append(instanceIndexes, index)
	}
	if len(instanceIndexes) == 0 {
		for index := range chosen.candidates {
			instanceIndexes = append(instanceIndexes, index)
		}
	}
	instanceBest := []int{instanceIndexes[0]}
	for _, index := range instanceIndexes[1:] {
		left := areaCounts[mapAreaKey(chosen.candidates[index].mp)]
		right := areaCounts[mapAreaKey(chosen.candidates[instanceBest[0]].mp)]
		switch {
		case left < right:
			instanceBest = []int{index}
		case left == right:
			instanceBest = append(instanceBest, index)
		}
	}
	return targetFrom(chosen.candidates[randomIndex(env, instanceBest)])
}
