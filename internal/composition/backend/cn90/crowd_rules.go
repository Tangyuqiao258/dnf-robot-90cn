package cn90

import (
	"hash/fnv"
	"sort"
	"sync/atomic"

	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

// 90CN crowd rules: the town must look inhabited without looking crowded. Every
// rule is derived from the town catalog the runtime already projects from the
// PVF, so no map number is hand-maintained here and a PVF update cannot leave a
// rule pointing at a map that no longer exists.
//
// The roles the rules key on:
//
//   - Seria exits: the maps a character lands in when leaving the Seria room.
//     The room's portal table names them, and the enchanting stalls exist only
//     here, one per map. A village's landing square is a gathering place, so it
//     carries a crowd of its own.
//   - Dungeon entrances: maps whose area declares dungeon gates. Each hosts one
//     disassembler stall, whose machine the profession writer leaves at grade 11.
//     A gate hall is a gathering place too: players stand there while they wait.
//   - Connectors: maps that exist to link two towns, or that a single door both
//     enters and leaves, and that hold nothing else. They stay empty part of the
//     time and hold a wanderer or two otherwise.
//   - The main city gate: the Seria-exit map of the main town may grow denser
//     than its neighbours.
//   - Ordinary maps: everything else. Enchanting exists nowhere but the Seria
//     exits, so an ordinary map hosts disassembler stalls only.
const (
	// crowdConnectorEmptyPercent is how often a connector mini-map stays empty.
	// The rest of the time it holds a wanderer or two. The operator owns this
	// trade: most roads empty reads as a world nobody walks, every road busy
	// spends the fleet on maps a player only crosses.
	crowdConnectorEmptyPercent = 30
	// crowdConnectorMaxRobots is the default for how many robots a connector
	// mini-map may hold when it is not empty. The operator owns the live value
	// ([demand] junction_max_robots): a road is a place a player crosses rather
	// than a place a crowd gathers, but a world whose roads are all empty reads
	// as a world nobody walks.
	crowdConnectorMaxRobots = 2
	// crowdStallPerMap is how many expert-job stalls one map hosts: enchanting
	// stalls on a Seria exit, disassembler stalls at a dungeon entrance.
	crowdStallPerMap = 1
	// crowdPlayerMapDistance is the default for how many transitions from the
	// player the world still has to look alive: maps farther than this close.
	// The operator owns the live value ([demand] player_map_distance) because
	// the rule trades against the online target: a fleet spread thin over a
	// whole world is not a crowd the player ever sees, but a fleet penned into
	// three rings of maps cannot reach its own target. Zero turns the walk off
	// and leaves the whole world open.
	crowdPlayerMapDistance = 3
	// crowdMainCityVillage is Hendon Myre, the main town. Its Seria-exit map is
	// the one the fleet may fill.
	crowdMainCityVillage = 39
)

// CrowdMapRole is the part a town map plays in the world. It is exported for
// diagnostics and tests; the rules themselves only need CrowdMapRule.
type CrowdMapRole string

const (
	CrowdRoleNormal          CrowdMapRole = "normal"
	CrowdRoleSeriaExit       CrowdMapRole = "seria_exit"
	CrowdRoleMainCityGate    CrowdMapRole = "main_city_gate"
	CrowdRoleDungeonEntrance CrowdMapRole = "dungeon_entrance"
	CrowdRoleConnector       CrowdMapRole = "connector"
)

// CrowdRules is the 90CN world's per-map population policy. It is built once
// from the town catalog, then read by placement, movement and stall selection.
// The player's town may be updated at any time; every other input is fixed.
type CrowdRules struct {
	roles map[shared.MapAreaKey]CrowdMapRole
	// links is the town graph: the maps each map leads to.
	links map[shared.MapAreaKey][]shared.MapAreaKey

	mu        lockhub.RWLocker
	playerKey shared.MapAreaKey
	distance  map[shared.MapAreaKey]int
	// distanceLimit is the live reach of the walk above. It is an atomic rather
	// than a locked field because every placement decision reads it, and because
	// a new limit never invalidates the walk: the distances are how far every
	// map sits from the player, and the limit only decides which of them close.
	distanceLimit atomic.Int64
	// junctionMax is how many robots a connector mini-map may hold when it is
	// not one of the roads that stay empty. An atomic for the same reason: the
	// live value is the operator's, and every placement decision reads it.
	junctionMax atomic.Int64
}

// NewCrowdRules derives the world's rules from the projected town catalog.
func NewCrowdRules(maps []shared.MapCatalogItem) *CrowdRules {
	rules := &CrowdRules{
		roles: make(map[shared.MapAreaKey]CrowdMapRole, len(maps)),
		links: make(map[shared.MapAreaKey][]shared.MapAreaKey, len(maps)),
	}
	for _, mp := range maps {
		key := shared.MapAreaKey{Village: mp.Village, Area: mp.Area}
		if len(mp.Links) > 0 {
			rules.links[key] = append([]shared.MapAreaKey(nil), mp.Links...)
		}
		switch {
		case mp.SeriaExit:
			rules.roles[key] = CrowdRoleSeriaExit
			if mp.Village == crowdMainCityVillage {
				rules.roles[key] = CrowdRoleMainCityGate
			}
		case mp.DungeonGate:
			rules.roles[key] = CrowdRoleDungeonEntrance
		case crowdIsConnector(mp):
			rules.roles[key] = CrowdRoleConnector
		default:
			rules.roles[key] = CrowdRoleNormal
		}
	}
	rules.distanceLimit.Store(crowdPlayerMapDistance)
	rules.junctionMax.Store(crowdConnectorMaxRobots)
	rules.recomputeLocked()
	return rules
}

// SetJunctionMaxRobots sets how many robots a connector mini-map may hold when
// it is not one of the roads that stay empty. Zero makes a junction an ordinary
// area again, which leaves the operator's per-area cap to decide. Like the
// distance walk, it is read from the runtime configuration so an operator edit
// lands without a restart.
func (r *CrowdRules) SetJunctionMaxRobots(limit int) {
	if r == nil {
		return
	}
	r.junctionMax.Store(int64(limit))
}

// junctionMaxRobots reports the live cap on a connector mini-map, or zero when
// the operator turned the cap off.
func (r *CrowdRules) junctionMaxRobots() int {
	if r == nil {
		return 0
	}
	return int(r.junctionMax.Load())
}

// SetPlayerMapDistance sets how many transitions from the player still take
// robots. Zero or less turns the walk off, which opens the whole world: the
// operator asks for that when the fleet's own target needs more maps than the
// walk leaves open. It is called whenever the runtime configuration is read, so
// an operator edit lands without a restart.
func (r *CrowdRules) SetPlayerMapDistance(limit int) {
	if r == nil {
		return
	}
	r.distanceLimit.Store(int64(limit))
}

// playerMapDistance reports the live reach, or zero when the walk is off.
func (r *CrowdRules) playerMapDistance() int {
	if r == nil {
		return 0
	}
	if limit := int(r.distanceLimit.Load()); limit > 0 {
		return limit
	}
	return 0
}

// crowdIsConnector reports whether a map is a road rather than a place: it
// exists only to link two towns, or it has a single entrance and the way out is
// that same entrance, so a robot that walks in can only walk back out.
//
// Two kinds of map are never a road. A map that opens onto the Seria room is
// where characters land and the room itself is where they arrive. A dungeon
// entrance has a role of its own -- it hosts the disassembler its gate exists
// for -- so it keeps that role whichever roads run past it.
func crowdIsConnector(mp shared.MapCatalogItem) bool {
	if mp.SeriaExit || mp.DungeonGate || len(mp.Links) == 0 {
		return false
	}
	// One link is one door, and the way out of the map is that door.
	if len(mp.Links) == 1 {
		return true
	}
	towns := make(map[int]struct{}, len(mp.Links))
	for _, link := range mp.Links {
		if link.Village > 0 {
			towns[link.Village] = struct{}{}
		}
	}
	return len(towns) == 2
}

// Role returns the part one map plays in the world.
func (r *CrowdRules) Role(village, area int) CrowdMapRole {
	if r == nil {
		return CrowdRoleNormal
	}
	if role, ok := r.roles[shared.MapAreaKey{Village: village, Area: area}]; ok {
		return role
	}
	return CrowdRoleNormal
}

// CrowdMapRule implements shared.CrowdMapPolicy.
func (r *CrowdRules) CrowdMapRule(village, area int) shared.CrowdMapRule {
	if r == nil {
		return shared.CrowdMapRule{}
	}
	if limit := r.playerMapDistance(); limit > 0 {
		if distance, known := r.distanceOf(shared.MapAreaKey{Village: village, Area: area}); known && distance > limit {
			return shared.CrowdMapRule{Closed: true}
		}
	}
	switch r.Role(village, area) {
	case CrowdRoleSeriaExit:
		// A village's landing square is where its characters appear, so it is the
		// map a player stands in rather than walks past. It carries a crowd of
		// its own, under the operator's cap for such maps.
		return shared.CrowdMapRule{
			CrowdExempt: true,
			StallKind:   shared.ExpertJobStoreEnchant,
			StallMax:    crowdStallPerMap,
		}
	case CrowdRoleMainCityGate:
		// The main town's gate square is the one map allowed to fill up: the
		// player crosses it on every trip, so it carries the crowd.
		return shared.CrowdMapRule{
			CrowdExempt: true,
			StallKind:   shared.ExpertJobStoreEnchant,
			StallMax:    crowdStallPerMap,
		}
	case CrowdRoleDungeonEntrance:
		// The hall a dungeon gate opens on is the other map players stand in
		// while they wait, so it gathers a crowd for the same reason.
		return shared.CrowdMapRule{
			CrowdExempt: true,
			StallKind:   shared.ExpertJobStoreDisjoint,
			StallMax:    crowdStallPerMap,
		}
	case CrowdRoleConnector:
		if crowdConnectorEmpty(village, area) {
			return shared.CrowdMapRule{Closed: true}
		}
		// The wanderers a connector holds must not stall: the map is a road, not
		// a market.
		return shared.CrowdMapRule{MaxRobots: r.junctionMaxRobots()}
	case CrowdRoleNormal:
		// Enchanting is reserved for the Seria exits, so an ordinary map hosts
		// disassembler stalls only. Their count follows the store rotation: the
		// rule pins the kind, not how many.
		return shared.CrowdMapRule{
			StallKind: shared.ExpertJobStoreDisjoint,
			StallMax:  shared.CrowdStallUnlimited,
		}
	}
	return shared.CrowdMapRule{}
}

// SetPlayerMap records the map the player was last seen on and recomputes how
// far every map sits from it. Maps farther than crowdPlayerMapDistance stop
// taking robots, because a player three transitions out is still around the
// corner while a player four out is in another part of the world.
//
// The walk starts from the player's own map, not from their whole town: a town
// is thirty-odd maps, and a crowd spread one robot deep over all of them is not
// a crowd the player ever sees. A village of zero (player unknown) clears the
// restriction.
//
// A map the catalog does not describe keeps the previous seed rather than
// clearing it. A player inside a dungeon instance is the normal case -- the
// instance is not a town map -- and emptying the world for the ten minutes they
// spend in one would be visible from the outside.
func (r *CrowdRules) SetPlayerMap(village, area int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if village > 0 {
		if _, known := r.roles[shared.MapAreaKey{Village: village, Area: area}]; !known {
			return
		}
	}
	r.playerKey = shared.MapAreaKey{Village: village, Area: area}
	r.recomputeLocked()
}

// PlayerMap reports the map the walk currently starts from, or 0/0 when none
// does.
func (r *CrowdRules) PlayerMap() (village, area int) {
	if r == nil {
		return 0, 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.playerKey.Village, r.playerKey.Area
}

// recomputeLocked walks the town graph outward from the player's map.
func (r *CrowdRules) recomputeLocked() {
	r.distance = nil
	if r.playerKey.Village <= 0 {
		return
	}
	if _, known := r.roles[r.playerKey]; !known {
		return
	}
	distance := make(map[shared.MapAreaKey]int, len(r.roles))
	queue := make([]shared.MapAreaKey, 0, len(r.roles))
	distance[r.playerKey] = 0
	queue = append(queue, r.playerKey)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		next := distance[key] + 1
		for _, link := range r.links[key] {
			if _, seen := distance[link]; seen {
				continue
			}
			if _, known := r.roles[link]; !known {
				continue
			}
			distance[link] = next
			queue = append(queue, link)
		}
	}
	r.distance = distance
}

// distanceOf reports how many transitions the map sits from the player's town.
// Maps the walk never reached stay unrestricted.
func (r *CrowdRules) distanceOf(key shared.MapAreaKey) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.distance == nil {
		return 0, false
	}
	distance, ok := r.distance[key]
	return distance, ok
}

// MapDistances returns the distance of every map the walk reached, for
// diagnostics and for the floating layer, which fills the near rings first.
//
// No walk means nothing: the caller reads an empty answer as "no map is in
// reach", and a nil one as "this world has no notion of reach at all".
func (r *CrowdRules) MapDistances() map[shared.MapAreaKey]int {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.distance == nil {
		return nil
	}
	out := make(map[shared.MapAreaKey]int, len(r.distance))
	for key, distance := range r.distance {
		out[key] = distance
	}
	return out
}

// RoleCounts summarizes the derived roles, for diagnostics.
func (r *CrowdRules) RoleCounts() map[CrowdMapRole]int {
	counts := map[CrowdMapRole]int{}
	if r == nil {
		return counts
	}
	for _, role := range r.roles {
		counts[role]++
	}
	return counts
}

// MapKeys returns every map the rules know about, sorted.
func (r *CrowdRules) MapKeys() []shared.MapAreaKey {
	keys := make([]shared.MapAreaKey, 0, len(r.roles))
	for key := range r.roles {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Village == keys[j].Village {
			return keys[i].Area < keys[j].Area
		}
		return keys[i].Village < keys[j].Village
	})
	return keys
}

// crowdConnectorEmpty decides, once per map for the life of the process, whether
// a connector mini-map stays empty. The draw is a pure function of the map key,
// so a restart does not reshuffle which roads are busy.
func crowdConnectorEmpty(village, area int) bool {
	return int(crowdMapHash(village, area)%100) < crowdConnectorEmptyPercent
}

func crowdMapHash(village, area int) uint32 {
	hash := fnv.New32a()
	var buffer [8]byte
	for index, value := range [2]int{village, area} {
		for shift := 0; shift < 4; shift++ {
			buffer[index*4+shift] = byte(value >> (8 * shift))
		}
	}
	hash.Write(buffer[:])
	return hash.Sum32()
}
