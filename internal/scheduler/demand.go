package scheduler

import (
	"sort"
	"strings"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

// The demand layer answers one question every tick: which robots should be
// logged in, and where should each of them stand.
//
// The fleet is a pool, not a shift. Three layers decide who is online:
//
//   - Resident rows: crowds the operator keeps standing whatever the player is
//     doing (Hendon Myre's Kiri and Feng Zhen, the West Coast, the Metro
//     Center). They are pinned to a map and a rectangle, and a pinned robot
//     skips both the spawn rules' crowding repair and the player's reach.
//   - The floating layer: robots around the human player, on maps within a few
//     transitions of wherever the player is standing, picked to match the
//     player's level. This is the layer that makes the world look inhabited
//     while somebody levels a character.
//   - Nothing else. A robot in neither layer stops being desired, drops to
//     offline and is released by the throttled sweep, so the online budget
//     goes to the robots that are doing something.
//
// The layer only ever turns robots off. Turning them on is the ordinary online
// fill's job (assignIdleAutoActors), which leases a uid and marks the actor
// wanted in one step; a layer that also turned robots on would fight a manual
// logout the operator typed into the console.
const (
	// demandPlayerHops is how many map transitions from the player the floating
	// layer still fills. "切换三次以内" is the operator's rule: three transitions
	// away is still around the corner, four is a different part of the world.
	demandPlayerHops = 3
	// demandLogInterval is how often the layer writes its summary. The plan is
	// rebuilt every tick; logging it every tick would drown the file it shares
	// with the fleet's health lines.
	demandLogInterval = 5 * time.Minute
	// demandMapCap is how many robots the layer may stand on one map, residents
	// included. Without it the player's own map takes its whole ring, which is a
	// third of the fleet standing in one square: past a few dozen characters the
	// square stops reading as a busy town and starts reading as a bug. Robots a
	// capped map turns away spill outward ring by ring instead of being dropped.
	demandMapCap = 45
)

// demandPlan is one pass's answer. active distinguishes "the layer decided
// nothing should be online" from "there is no demand layer at all", which is
// what an operator with an empty plan gets.
type demandPlan struct {
	active        bool
	pins          map[int]demandLocation
	desired       map[int]bool
	residentCount int
	floatingCount int
	player        shared.PlayerLocation
	playerKnown   bool
}

// demandMemory is the demand layer's own state. It lives here rather than as
// loose fields on the supervisor so the fleet's struct stays about the fleet.
type demandMemory struct {
	mu       lockhub.Locker
	plan     *demandPlan
	spec     string
	rows     []robotconfig.ResidentRow
	nextPoll time.Time
	player   shared.PlayerLocation
	playerAt time.Time
	loggedAt time.Time
}

// demandUIDs returns the uids the layer wants online, or nil when it is off.
// The online fill reads it to lease only from that set.
func (s *RobotSupervisor) demandUIDs() map[int]bool {
	s.demand.mu.Lock()
	defer s.demand.mu.Unlock()
	if s.demand.plan == nil || !s.demand.plan.active {
		return nil
	}
	out := make(map[int]bool, len(s.demand.plan.desired))
	for uid := range s.demand.plan.desired {
		out[uid] = true
	}
	return out
}

// residentRows parses the configured plan, remembering the last text it read.
func (s *RobotSupervisor) residentRows(rc robotconfig.RuntimeConfig) []robotconfig.ResidentRow {
	spec := strings.TrimSpace(rc.ResidentPlan)
	s.demand.mu.Lock()
	defer s.demand.mu.Unlock()
	if spec == s.demand.spec {
		return s.demand.rows
	}
	rows, err := robotconfig.ParseResidentPlan(spec)
	if err != nil {
		// The config loader validates the same text, so a failure here is a plan
		// written after startup; refuse it rather than standing down an operator's
		// crowd on a typo.
		robotLogf("[RobotSupervisor] demand_plan_invalid err=%v\n", err)
		return s.demand.rows
	}
	s.demand.spec = spec
	s.demand.rows = rows
	return rows
}

// applyOnlineDemand rebuilds the plan and installs it. It runs before the
// offline sweep and before the online fill, so both see the same tick's answer.
func (s *RobotSupervisor) applyOnlineDemand(now time.Time, rc robotconfig.RuntimeConfig) {
	rows := s.residentRows(rc)
	account := strings.TrimSpace(rc.FollowAccount)
	if len(rows) == 0 && account == "" {
		s.standDownDemand()
		return
	}
	player, playerKnown := s.pollPlayer(now, rc, account)
	robots, err := s.manager.selectRobots(robotcap.CommandRequest{Count: robotconfig.PoolCapacity(rc) + 1})
	if err != nil {
		robotLogf("[RobotSupervisor] demand_select_failed err=%v\n", err)
		return
	}
	plan := s.buildDemandPlan(rows, player, playerKnown, robots, rc)
	s.installDemandPlan(now, plan)
}

// pollPlayer reads the player's position, at most once per configured interval,
// and keeps the last one for the grace window.
//
// The grace exists because a position read can fail, and because a player who
// closes the client looks exactly like a player who blinked. Holding the layer
// through that is what keeps a relog from emptying the world around them.
func (s *RobotSupervisor) pollPlayer(now time.Time, rc robotconfig.RuntimeConfig, account string) (shared.PlayerLocation, bool) {
	if account == "" {
		return shared.PlayerLocation{}, false
	}
	s.demand.mu.Lock()
	due := !now.Before(s.demand.nextPoll)
	if due {
		s.demand.nextPoll = now.Add(robotconfig.DemandPlayerPoll(rc))
	}
	s.demand.mu.Unlock()
	if due {
		located, ok, err := s.manager.locatePlayer(account)
		switch {
		case err != nil:
			robotLogf("[RobotSupervisor] demand_player_lookup_failed account=%s err=%v\n", account, err)
		case ok && located.Available():
			s.demand.mu.Lock()
			changed := s.demand.player.Village != located.Village || s.demand.player.Area != located.Area
			s.demand.player = located
			s.demand.playerAt = now
			s.demand.mu.Unlock()
			if changed {
				robotLogf("[RobotSupervisor] demand_player_moved village=%d area=%d level=%d\n",
					located.Village, located.Area, located.Level)
			}
		}
	}
	s.demand.mu.Lock()
	defer s.demand.mu.Unlock()
	if s.demand.playerAt.IsZero() {
		return shared.PlayerLocation{}, false
	}
	if now.Sub(s.demand.playerAt) > robotconfig.DemandPlayerGrace(rc) {
		return shared.PlayerLocation{}, false
	}
	return s.demand.player, s.demand.player.Available()
}

// buildDemandPlan fills the resident rows first and spends what is left of the
// online budget on the maps around the player.
func (s *RobotSupervisor) buildDemandPlan(rows []robotconfig.ResidentRow, player shared.PlayerLocation, playerKnown bool, robots []robotcap.Info, rc robotconfig.RuntimeConfig) demandPlan {
	plan := demandPlan{
		active:      true,
		pins:        make(map[int]demandLocation),
		desired:     make(map[int]bool),
		player:      player,
		playerKnown: playerKnown,
	}
	pool := make([]robotcap.Info, len(robots))
	copy(pool, robots)
	sort.Slice(pool, func(i, j int) bool { return pool[i].UID < pool[j].UID })
	used := make(map[int]bool, len(pool))
	previous := s.demandPinsSnapshot()

	for _, row := range rows {
		s.fillResidentRow(&plan, row, pool, used, previous)
	}
	plan.residentCount = len(plan.desired)

	budget := robotconfig.TargetCapacity(rc)
	if budget <= 0 {
		budget = rc.MaxOnlineRobots
	}
	remaining := budget - plan.residentCount
	if playerKnown && remaining > 0 {
		s.fillFloatingLayer(&plan, pool, used, player, remaining, rc)
	}
	plan.floatingCount = len(plan.desired) - plan.residentCount
	return plan
}

// fillResidentRow stands one row's robots on one of its maps.
//
// A robot already standing in the row keeps its place: the plan is rebuilt every
// tick, and re-drawing the crowd each time would walk it across the square all
// day. Only when the row is short does the layer reach into the pool, in uid
// order, which is the order the level plan deals bands out in -- so any prefix
// of it is a mix of levels rather than one band.
func (s *RobotSupervisor) fillResidentRow(plan *demandPlan, row robotconfig.ResidentRow, pool []robotcap.Info, used map[int]bool, previous map[int]demandLocation) {
	taken := 0
	for _, robot := range pool {
		if taken >= row.Count {
			return
		}
		if used[robot.UID] {
			continue
		}
		located, ok := previous[robot.UID]
		if !ok || !residentRowCovers(row, located) {
			continue
		}
		if !s.residentRowAdmits(row, robot) {
			continue
		}
		point, ok := s.residentPoint(row, robot)
		if !ok {
			continue
		}
		plan.pins[robot.UID] = point
		plan.desired[robot.UID] = true
		used[robot.UID] = true
		taken++
	}
	index := 0
	for _, robot := range pool {
		if taken >= row.Count {
			return
		}
		if used[robot.UID] || !s.residentRowAdmits(row, robot) {
			continue
		}
		maps := s.manager.residentMaps(row, robot.Level)
		if len(maps) == 0 {
			continue
		}
		// Spread the row over its maps instead of piling every robot on the
		// first one: a row naming an area range means the whole range.
		point, ok := residentMapPoint(row, maps[index%len(maps)], robot.UID)
		index++
		if !ok {
			continue
		}
		plan.pins[robot.UID] = point
		plan.desired[robot.UID] = true
		used[robot.UID] = true
		taken++
	}
}

// residentRowAdmits reports whether a robot is allowed to stand in a row at all:
// one of the row's maps has to accept its level.
func (s *RobotSupervisor) residentRowAdmits(row robotconfig.ResidentRow, robot robotcap.Info) bool {
	maps := s.manager.residentMaps(row, robot.Level)
	if len(maps) == 0 {
		return false
	}
	if !row.BoxSet {
		return true
	}
	// A row with a box stands inside it, so the box has to sit on a map the
	// robot's level opens.
	for _, mp := range maps {
		for _, rectangle := range robotspawn.MapRectangles(mp) {
			if rectangle.XMax >= row.XMin && rectangle.XMin <= row.XMax &&
				rectangle.YMax >= row.YMin && rectangle.YMin <= row.YMax {
				return true
			}
		}
	}
	return false
}

// residentPoint places a robot already standing in the row, or one the row's box
// was written for.
func (s *RobotSupervisor) residentPoint(row robotconfig.ResidentRow, robot robotcap.Info) (demandLocation, bool) {
	if row.BoxSet {
		x, y := demandPoint(robot.UID, row.XMin, row.XMax, row.YMin, row.YMax)
		return demandLocation{village: row.Village, area: row.AreaMin, x: x, y: y}, true
	}
	maps := s.manager.residentMaps(row, robot.Level)
	if len(maps) == 0 {
		return demandLocation{}, false
	}
	return residentMapPoint(row, maps[0], robot.UID)
}

func residentMapPoint(row robotconfig.ResidentRow, mp shared.MapCatalogItem, uid int) (demandLocation, bool) {
	rectangles := robotspawn.MapRectangles(mp)
	if len(rectangles) == 0 {
		return demandLocation{}, false
	}
	rectangle := rectangles[uid%len(rectangles)]
	x, y := demandPoint(uid, rectangle.XMin, rectangle.XMax, rectangle.YMin, rectangle.YMax)
	return demandLocation{village: mp.Village, area: mp.Area, x: x, y: y}, true
}

// residentRowCovers reports whether a remembered pin still sits in the row, so a
// robot keeps its place only while the place is still the row's.
func residentRowCovers(row robotconfig.ResidentRow, pin demandLocation) bool {
	if pin.village != row.Village || pin.area < row.AreaMin || pin.area > row.AreaMax {
		return false
	}
	if !row.BoxSet {
		return true
	}
	return pin.x >= row.XMin && pin.x <= row.XMax && pin.y >= row.YMin && pin.y <= row.YMax
}

// fillFloatingLayer stands robots around the player: the maps within reach of
// wherever the player is, filled with the robots closest to the player's level.
//
// Level comes first and distance second. A level-90 player walking through a
// low-level town should see a crowd of level-90 characters, not a crowd of
// whoever happened to be free -- "最好是符合等级的假人" is the whole point of
// holding a 300-character pool instead of logging in the first hundred.
//
// The budget thins with distance: each ring takes half of what is left, so the
// square the player is standing on fills up while the edge of the reach holds a
// handful. Spreading the same robots evenly over every map in reach is the one
// arrangement that is invisible from everywhere.
func (s *RobotSupervisor) fillFloatingLayer(plan *demandPlan, pool []robotcap.Info, used map[int]bool, player shared.PlayerLocation, budget int, rc robotconfig.RuntimeConfig) {
	rings := s.floatingRings(player)
	if len(rings) == 0 {
		return
	}
	candidates := make([]robotcap.Info, 0, len(pool))
	for _, robot := range pool {
		if !used[robot.UID] {
			candidates = append(candidates, robot)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := demandLevelGap(candidates[i].Level, player.Level)
		right := demandLevelGap(candidates[j].Level, player.Level)
		if left != right {
			return left < right
		}
		return candidates[i].UID < candidates[j].UID
	})
	occupancy := make(map[shared.MapAreaKey]int, len(pool))
	for _, robot := range pool {
		pin, ok := plan.pins[robot.UID]
		if !ok {
			continue
		}
		occupancy[shared.MapAreaKey{Village: pin.village, Area: pin.area}]++
	}
	placed := make(map[int]bool, len(candidates))
	remaining := budget
	for index := 0; index < len(rings) && remaining > 0; index++ {
		share := remaining
		if index < len(rings)-1 {
			// Half for this ring, half for everything farther out. The outermost
			// ring takes whatever is left, so a budget the inner rings could not
			// spend still ends up somewhere.
			share = remaining / 2
			if share < 1 {
				share = 1
			}
		}
		taken := 0
		for _, robot := range candidates {
			if taken >= share {
				break
			}
			if placed[robot.UID] {
				continue
			}
			mp, ok := demandPickMap(rings[index], occupancy, robot.Level)
			if !ok {
				// No map of this ring opens at the robot's level; the next
				// candidate is the next closest level, and an outer ring may
				// still take this one.
				continue
			}
			pin, ok := s.floatingPoint(mp, robot.UID, player, rc)
			if !ok {
				continue
			}
			plan.pins[robot.UID] = pin
			plan.desired[robot.UID] = true
			occupancy[shared.MapAreaKey{Village: mp.Village, Area: mp.Area}]++
			placed[robot.UID] = true
			taken++
		}
		remaining -= taken
	}
}

// demandMapTarget is one map the floating layer may fill, with how many
// transitions from the player it sits.
type demandMapTarget struct {
	mp       shared.MapCatalogItem
	distance int
}

// floatingRings groups the maps in the player's reach by distance, nearest ring
// first, each ring sorted by map key so a plan rebuild picks the same maps.
func (s *RobotSupervisor) floatingRings(player shared.PlayerLocation) [][]demandMapTarget {
	targets := s.floatingMaps(player)
	rings := make([][]demandMapTarget, 0, demandPlayerHops+1)
	for _, target := range targets {
		if len(rings) == 0 || rings[len(rings)-1][0].distance != target.distance {
			rings = append(rings, []demandMapTarget{target})
			continue
		}
		rings[len(rings)-1] = append(rings[len(rings)-1], target)
	}
	return rings
}

// floatingMaps lists the maps the floating layer may use: in use, walkable, and
// inside the player's reach. Nearest first, so a budget that runs out runs out
// at the edge of the reach rather than somewhere random inside it.
func (s *RobotSupervisor) floatingMaps(player shared.PlayerLocation) []demandMapTarget {
	var out []demandMapTarget
	distances := s.manager.playerMapDistances()
	for _, mp := range s.manager.loadMapCatalog() {
		if !mp.Use || len(robotspawn.MapRectangles(mp)) == 0 {
			continue
		}
		distance := 0
		if distances != nil {
			// The walk never reached the map means it is not reachable from where
			// the player is standing: farther than "out of reach", so it is
			// dropped rather than clamped.
			walked, known := distances[shared.MapAreaKey{Village: mp.Village, Area: mp.Area}]
			if !known {
				continue
			}
			distance = walked
		} else if mp.Village != player.Village || mp.Area != player.Area {
			continue
		}
		if distance > demandPlayerHops {
			continue
		}
		out = append(out, demandMapTarget{mp: mp, distance: distance})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].distance != out[j].distance {
			return out[i].distance < out[j].distance
		}
		if out[i].mp.Village != out[j].mp.Village {
			return out[i].mp.Village < out[j].mp.Village
		}
		return out[i].mp.Area < out[j].mp.Area
	})
	return out
}

// demandPickMap takes the least crowded map of one ring that admits the robot's
// level and has room left: a ring fills evenly, so no single map of it carries
// the whole crowd.
func demandPickMap(targets []demandMapTarget, occupancy map[shared.MapAreaKey]int, level int) (shared.MapCatalogItem, bool) {
	best := -1
	bestCount := 0
	for index, target := range targets {
		if target.mp.Level > level {
			continue
		}
		count := occupancy[shared.MapAreaKey{Village: target.mp.Village, Area: target.mp.Area}]
		if count >= demandMapCap {
			continue
		}
		if best < 0 || count < bestCount {
			best, bestCount = index, count
		}
	}
	if best < 0 {
		return shared.MapCatalogItem{}, false
	}
	return targets[best].mp, true
}

// floatingPoint stands a robot on a map the player is on, inside the configured
// follow radius of the player's own position. On any other map the player's
// coordinates mean nothing, so the map's own rectangle answers.
func (s *RobotSupervisor) floatingPoint(mp shared.MapCatalogItem, uid int, player shared.PlayerLocation, rc robotconfig.RuntimeConfig) (demandLocation, bool) {
	rectangles := robotspawn.MapRectangles(mp)
	if len(rectangles) == 0 {
		return demandLocation{}, false
	}
	rectangle := rectangles[uid%len(rectangles)]
	if mp.Village != player.Village || mp.Area != player.Area {
		x, y := demandPoint(uid, rectangle.XMin, rectangle.XMax, rectangle.YMin, rectangle.YMax)
		return demandLocation{village: mp.Village, area: mp.Area, x: x, y: y}, true
	}
	radiusX, radiusY := rc.FollowRadiusX, rc.FollowRadiusY
	if radiusX <= 0 {
		radiusX = 1
	}
	if radiusY <= 0 {
		radiusY = 1
	}
	xMin := maxInt(rectangle.XMin, player.X-radiusX)
	xMax := minInt(rectangle.XMax, player.X+radiusX)
	yMin := maxInt(rectangle.YMin, player.Y-radiusY)
	yMax := minInt(rectangle.YMax, player.Y+radiusY)
	if xMax < xMin || yMax < yMin {
		// The player is standing outside the map's own walkable rectangle (an
		// instance the catalog describes differently). Fall back to the map.
		xMin, xMax = rectangle.XMin, rectangle.XMax
		yMin, yMax = rectangle.YMin, rectangle.YMax
	}
	x, y := demandPoint(uid, xMin, xMax, yMin, yMax)
	return demandLocation{village: mp.Village, area: mp.Area, x: x, y: y}, true
}

// installDemandPlan publishes the plan: the pins the placement layer reads, the
// player's town the crowd rules walk from, and the wanted flag on every actor.
func (s *RobotSupervisor) installDemandPlan(now time.Time, plan demandPlan) {
	s.demand.mu.Lock()
	s.demand.plan = &plan
	logDue := now.Sub(s.demand.loggedAt) >= demandLogInterval
	if logDue {
		s.demand.loggedAt = now
	}
	s.demand.mu.Unlock()

	s.manager.setDemandLocations(plan.pins)
	if plan.playerKnown {
		s.manager.setPlayerMap(plan.player.Village, plan.player.Area)
	}
	shed := 0
	for _, actor := range s.ledger.AutoActorPointers() {
		snapshot := actor.Snapshot()
		if snapshot.Mode != actormodel.ModeAuto || snapshot.UID <= 0 || !snapshot.OnlineDesired {
			continue
		}
		if plan.desired[snapshot.UID] {
			continue
		}
		actor.SetOnlineDesired(false)
		shed++
	}
	if logDue {
		robotLogf("[RobotSupervisor] demand_plan resident=%d floating=%d desired=%d shedding=%d player=%d/%d known=%t\n",
			plan.residentCount, plan.floatingCount, len(plan.desired), shed,
			plan.player.Village, plan.player.Area, plan.playerKnown)
	}
}

// standDownDemand clears the layer, so a config with no plan and no follow
// account keeps the fleet's plain behaviour: everyone online, nothing pinned.
func (s *RobotSupervisor) standDownDemand() {
	s.demand.mu.Lock()
	off := s.demand.plan == nil || !s.demand.plan.active
	s.demand.plan = nil
	s.demand.playerAt = time.Time{}
	s.demand.mu.Unlock()
	if off {
		return
	}
	s.manager.setDemandLocations(nil)
	s.manager.setPlayerMap(0, 0)
}

func (s *RobotSupervisor) demandPinsSnapshot() map[int]demandLocation {
	s.demand.mu.Lock()
	defer s.demand.mu.Unlock()
	if s.demand.plan == nil {
		return nil
	}
	out := make(map[int]demandLocation, len(s.demand.plan.pins))
	for uid, pin := range s.demand.plan.pins {
		out[uid] = pin
	}
	return out
}

// demandLevelGap is how far a robot's level sits from the player's. Levels are
// what a player notices about the crowd around them, so the floating layer sorts
// by this before anything else.
func demandLevelGap(robot, player int) int {
	if robot >= player {
		return robot - player
	}
	return player - robot
}

// demandPoint draws a standing spot inside a rectangle from the robot's own uid,
// so a robot keeps its place across the plan rebuilds that happen every second,
// and a restart does not reshuffle the crowd.
func demandPoint(uid, xMin, xMax, yMin, yMax int) (int, int) {
	if xMax < xMin {
		xMin, xMax = xMax, xMin
	}
	if yMax < yMin {
		yMin, yMax = yMax, yMin
	}
	return demandSpan(uid, xMin, xMax), demandSpan(uid>>1, yMin, yMax)
}

func demandSpan(uid, min, max int) int {
	span := max - min
	if span <= 0 {
		return min
	}
	return min + int(demandPointHash(uint32(uid))%uint32(span+1))
}

// demandPointHash is FNV-1a over the uid: a placement must be stable across
// processes, so it cannot come from the process's own random source.
func demandPointHash(value uint32) uint32 {
	hash := uint32(2166136261)
	for shift := 0; shift < 4; shift++ {
		hash ^= (value >> (8 * shift)) & 0xff
		hash *= 16777619
	}
	return hash
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
