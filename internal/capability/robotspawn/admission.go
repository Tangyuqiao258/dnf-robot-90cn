package robotspawn

import "robot/internal/shared"

// CrowdPolicy probes a spawn environment for the world's crowd map policy. The
// spawn capability is backend-neutral, so the policy arrives through the same
// optional-interface idiom the store capability uses; an environment that
// carries none admits every map, exactly as before the rules existed.
func CrowdPolicy(env interface{}) shared.CrowdMapPolicy {
	if policy, ok := env.(shared.CrowdMapPolicy); ok {
		return policy
	}
	return nil
}

// AdmittedMaps drops the maps the crowd rules keep empty. Occupancy caps are
// deliberately not applied here: only a caller that knows how many robots a map
// already holds can enforce those, so max-robot rules are checked where the
// occupancy is in hand.
func AdmittedMaps(env interface{}, maps []shared.MapCatalogItem) []shared.MapCatalogItem {
	policy := CrowdPolicy(env)
	if policy == nil {
		return maps
	}
	out := make([]shared.MapCatalogItem, 0, len(maps))
	for _, mp := range maps {
		if policy.CrowdMapRule(mp.Village, mp.Area).AllowsRobot() {
			out = append(out, mp)
		}
	}
	return out
}

// CrowdOccupied reports whether a map has already reached the robot count its
// crowd rule allows. A map without a max-robot rule is never occupied.
func CrowdOccupied(policy shared.CrowdMapPolicy, village, area, held int) bool {
	if policy == nil {
		return false
	}
	rule := policy.CrowdMapRule(village, area)
	if rule.Closed {
		return true
	}
	return rule.MaxRobots > 0 && held >= rule.MaxRobots
}

// CrowdTransitFull reports whether a map has reached the count it may hold
// counting the robots that are only passing through. A map at its standing cap
// still lets TransitSlots more cross it, because a passer-by leaves again; a
// map at that ceiling stops taking even those. A closed map answers true, so a
// caller that only wants to move a robot across asks this and nothing else.
func CrowdTransitFull(policy shared.CrowdMapPolicy, village, area, held int) bool {
	if policy == nil {
		return false
	}
	rule := policy.CrowdMapRule(village, area)
	if rule.Closed {
		return true
	}
	ceiling := rule.TransitCeiling()
	return ceiling > 0 && held >= ceiling
}

// CrowdFloor reports how many robots a map wants before an empty neighbour
// takes its first one. A map the rules leave to the ordinary spreading, or a
// road the rules cap below any floor, answers zero and is covered like any
// other map.
func CrowdFloor(policy shared.CrowdMapPolicy, village, area int) int {
	if policy == nil {
		return 0
	}
	return policy.CrowdMapRule(village, area).MinRobots
}

// CrowdExempt reports whether the crowd rules let a map grow past the ordinary
// density cap.
func CrowdExempt(policy shared.CrowdMapPolicy, village, area int) bool {
	if policy == nil {
		return false
	}
	return policy.CrowdMapRule(village, area).CrowdExempt
}

// CrowdStallStation reports whether a map is one the crowd rules still want a
// stall dummy for: it pins a bounded number of stalls of that trade and none of
// them stands there yet. Ordinary maps are never a station, since their stall
// count is unlimited and answering yes for them would fill the world with
// disassemblers instead of the entrances the rules single out.
func CrowdStallStation(policy shared.CrowdMapPolicy, village, area int, kind shared.ExpertJobStoreKind, held int) bool {
	if policy == nil || kind == shared.ExpertJobStoreNone {
		return false
	}
	rule := policy.CrowdMapRule(village, area)
	return rule.StallMax > 0 && rule.AllowsStall(kind, held)
}
