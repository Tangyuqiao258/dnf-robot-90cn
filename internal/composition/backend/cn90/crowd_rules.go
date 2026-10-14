package cn90

import (
	"hash/fnv"
	"sort"

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
//     here, one per map.
//   - Dungeon entrances: maps whose area declares dungeon gates. Each hosts one
//     disassembler stall, whose machine the profession writer leaves at grade 11.
//   - Connectors: maps that exist to link two towns and hold nothing else. They
//     stay empty most of the time and hold a single wanderer otherwise.
//   - The main city gate: the Seria-exit map of the main town may grow denser
//     than its neighbours.
//   - Ordinary maps: everything else. Enchanting exists nowhere but the Seria
//     exits, so an ordinary map hosts disassembler stalls only.
const (
	// crowdConnectorEmptyPercent is how often a connector mini-map stays empty.
	// The rest of the time it holds exactly one non-stalling wanderer.
	crowdConnectorEmptyPercent = 70
	// crowdConnectorMaxRobots is the wanderer a connector mini-map may hold
	// when it is not empty.
	crowdConnectorMaxRobots = 1
	// crowdStallPerMap is how many expert-job stalls one map hosts: enchanting
	// stalls on a Seria exit, disassembler stalls at a dungeon entrance.
	crowdStallPerMap = 1
	// crowdPlayerMapDistance closes maps this many transitions from the player
	// or further: the world only has to look alive near the player.
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
	rules.recomputeLocked()
	return rules
}

// crowdIsConnector reports whether a map exists only to link two towns: it has
// neighbours in exactly two towns and opens neither onto the room. A town square
// carries its own town plus its neighbours, so it lists more than two; a dead end
// lists one.
func crowdIsConnector(mp shared.MapCatalogItem) bool {
	if mp.SeriaExit || len(mp.Links) == 0 {
		return false
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
	if distance, known := r.distanceOf(shared.MapAreaKey{Village: village, Area: area}); known && distance >= crowdPlayerMapDistance {
		return shared.CrowdMapRule{Closed: true}
	}
	switch r.Role(village, area) {
	case CrowdRoleSeriaExit:
		return shared.CrowdMapRule{StallKind: shared.ExpertJobStoreEnchant, StallMax: crowdStallPerMap}
	case CrowdRoleMainCityGate:
		// The main town's gate square is the one map allowed to fill up: the
		// player crosses it on every trip, so it carries the crowd.
		return shared.CrowdMapRule{
			CrowdExempt: true,
			StallKind:   shared.ExpertJobStoreEnchant,
			StallMax:    crowdStallPerMap,
		}
	case CrowdRoleDungeonEntrance:
		return shared.CrowdMapRule{StallKind: shared.ExpertJobStoreDisjoint, StallMax: crowdStallPerMap}
	case CrowdRoleConnector:
		if crowdConnectorEmpty(village, area) {
			return shared.CrowdMapRule{Closed: true}
		}
		// The wanderer a connector holds must not stall: the map is a road, not
		// a market.
		return shared.CrowdMapRule{MaxRobots: crowdConnectorMaxRobots}
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

// SetPlayerTown records the town the player was last seen in and recomputes how
// far every map sits from it. Maps at or beyond crowdPlayerMapDistance stop
// taking robots. A town of zero (player unknown) clears the restriction.
//
// Only the town is known from the follow account, never the exact map, so the
// walk starts from every map of that town at once.
func (r *CrowdRules) SetPlayerTown(village int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.playerKey = shared.MapAreaKey{Village: village}
	r.recomputeLocked()
}

// PlayerTown reports the town the walk currently starts from.
func (r *CrowdRules) PlayerTown() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.playerKey.Village
}

// recomputeLocked walks the town graph outward from the player's town.
func (r *CrowdRules) recomputeLocked() {
	r.distance = nil
	if r.playerKey.Village <= 0 {
		return
	}
	distance := make(map[shared.MapAreaKey]int, len(r.roles))
	queue := make([]shared.MapAreaKey, 0, len(r.roles))
	for key := range r.roles {
		if key.Village == r.playerKey.Village {
			distance[key] = 0
			queue = append(queue, key)
		}
	}
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
// diagnostics.
func (r *CrowdRules) MapDistances() map[shared.MapAreaKey]int {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
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
