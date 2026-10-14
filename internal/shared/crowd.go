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
