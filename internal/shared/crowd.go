package shared

// CrowdMapRule is what one town map may hold under a world's crowd rules. The
// zero value restricts nothing, so a policy that knows nothing about a map
// admits it exactly as before.
type CrowdMapRule struct {
	// Closed forbids robots from standing in the map at all.
	Closed bool
	// MaxRobots caps how many robots the map may hold; zero leaves the count to
	// the ordinary crowd balancing.
	MaxRobots int
	// TransitSlots is how many robots beyond MaxRobots the map still lets
	// through. A robot crossing a map is gone again a moment later, so a map
	// that has reached its standing count stays walkable for a couple of
	// passers-by instead of turning into a wall. Zero caps the map at MaxRobots
	// exactly.
	TransitSlots int
	// MinRobots is how many robots a map wants before an empty neighbour takes
	// its first one. A map holding one or two lonely robots reads as abandoned,
	// so the floor keeps the fleet together until a map looks inhabited instead
	// of spreading it one robot deep over the whole world. Zero leaves the
	// choice to the ordinary spreading, which covers every map first.
	MinRobots int
	// CrowdExempt lifts the ordinary density cap for a map the rules let grow
	// denser than its neighbours.
	CrowdExempt bool
	// StallKind is the expert-job stall the map hosts and StallMax how many of
	// them. StallMax zero forbids expert-job stalls in the map, and
	// CrowdStallUnlimited lets the map host any number of its kind.
	StallKind ExpertJobStoreKind
	StallMax  int
}

// CrowdStallUnlimited marks a map that hosts any number of stalls of its kind:
// the rules pin the kind but leave the count to the ordinary store rotation.
const CrowdStallUnlimited = -1

// AllowsRobot reports whether a robot may stand in the map under this rule.
func (r CrowdMapRule) AllowsRobot() bool { return !r.Closed }

// TransitCeiling is how many robots may be in the map at once counting the ones
// only passing through. A map with no standing cap has no ceiling either: the
// ordinary crowd balancing owns it, exactly as before TransitSlots existed.
func (r CrowdMapRule) TransitCeiling() int {
	if r.MaxRobots <= 0 {
		return 0
	}
	return r.MaxRobots + r.TransitSlots
}

// AllowsStall reports whether the map may host one more stall of kind. held is
// how many stalls of that kind the map already shows.
func (r CrowdMapRule) AllowsStall(kind ExpertJobStoreKind, held int) bool {
	if r.Closed || kind == ExpertJobStoreNone || r.StallKind != kind {
		return false
	}
	if r.StallMax == CrowdStallUnlimited {
		return true
	}
	return r.StallMax > 0 && held < r.StallMax
}

// CrowdMapPolicy is the per-map population policy of one world. A backend
// attaches it to the robot manager, and placement, movement and stall selection
// all read the same rules, so a map cannot be closed to spawns but open to
// stalls. Implementations must be safe for concurrent use.
type CrowdMapPolicy interface {
	CrowdMapRule(village, area int) CrowdMapRule
}

// CrowdAreaPolicy is the operator's world-wide population policy: the villages
// no robot may enter, how many robots one area may hold, and how many extra may
// pass through it. It is laid over whatever per-map rule the world derived from
// its own catalog, because "no robot stands here" and "this area holds at most
// N" are operating decisions that outrank any role a map plays in the world.
//
// The zero value changes nothing, so a fleet whose operator has set no policy
// keeps the world's own rules exactly as they were.
type CrowdAreaPolicy struct {
	// ClosedVillages are the villages no robot may stand in.
	ClosedVillages []int
	// MaxPerArea is how many robots one area may hold as a standing crowd. Zero
	// or less leaves the world's own caps alone.
	MaxPerArea int
	// TransitSlots is how many robots beyond MaxPerArea one area still lets
	// through.
	TransitSlots int
	// MinPerArea is how many robots one area wants before an empty one takes
	// its first. Zero leaves the world's own spreading alone, which fills every
	// map before any of them takes a second robot.
	MinPerArea int
	// HotPerArea is how many robots an area the world marks as a gathering place
	// may hold: the map a village's characters land in and the maps a dungeon
	// gate opens on. A player stands in those rather than walking past them, so
	// they carry a crowd of their own, and this cap is what keeps that crowd
	// from growing into a stutter. It answers a different question than
	// MaxPerArea -- not how full an area may get, but how full the areas a
	// player stands in may get -- so it is read on its own and applies whether or
	// not MaxPerArea is set. Zero, or a value no greater than MaxPerArea, leaves
	// the gathering places wherever the ordinary cap puts them.
	HotPerArea int
}

// ClosesVillage reports whether the operator closed a village to robots.
func (p CrowdAreaPolicy) ClosesVillage(village int) bool {
	for _, closed := range p.ClosedVillages {
		if closed == village {
			return true
		}
	}
	return false
}

// Apply returns the rule that governs one map once the operator policy is laid
// over the world's own rule.
//
// A closed village closes outright, and the closure comes first so that no role
// the catalog gave the map can reopen it. The per-area cap replaces a larger one
// and clears the density exemption: a map allowed to grow past the cap is not
// capped at all, and the operator's "at most N" is the whole point of the rule.
// The one exception is a gathering place the operator gave a hot cap above the
// ordinary one: that map is capped at the hot number instead and keeps its
// exemption, because the operator asked for exactly that kind of map to stay
// busier than its neighbours. The floor goes on last and only where
// the map can hold it: a road the world caps at two robots is a decision about
// that map, and asking it for five would undo the decision instead of applying
// the operator's.
func (p CrowdAreaPolicy) Apply(village, area int, rule CrowdMapRule) CrowdMapRule {
	if p.ClosesVillage(village) {
		return CrowdMapRule{Closed: true}
	}
	if rule.Closed {
		return rule
	}
	cap := p.MaxPerArea
	if rule.CrowdExempt && p.HotPerArea > cap {
		cap = p.HotPerArea
	} else {
		rule.CrowdExempt = false
	}
	if cap > 0 && (rule.MaxRobots <= 0 || rule.MaxRobots > cap) {
		rule.MaxRobots = cap
	}
	// The transit allowance is the operator's answer to how full a map is too
	// full to walk through, and it is the same answer everywhere: a road the
	// world caps at two is as much of a wall at two as a square is at fifteen.
	if rule.MaxRobots > 0 && p.TransitSlots > 0 {
		rule.TransitSlots = p.TransitSlots
	}
	if floor := p.MinPerArea; floor > rule.MinRobots && (rule.MaxRobots <= 0 || rule.MaxRobots >= floor) {
		rule.MinRobots = floor
	}
	return rule
}
