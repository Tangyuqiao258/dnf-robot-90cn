package robotaction

import (
	"fmt"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotspawn "robot/internal/capability/robotspawn"
	"robot/internal/foundation/mathx"
	"robot/internal/shared"
	"time"
)

type MoveService struct {
	Env MoveEnv
}

type MoveEnv interface {
	DispatchMoveStep(info robotcap.Info, targetVillage, targetArea, targetX, targetY, step, steps, speed int, rc robotconfig.RuntimeConfig) error
	LoadMapCatalog() []shared.MapCatalogItem
	RandBetween(min, max int) int
	RuntimeStatus(uid int) (robotcap.RuntimeStatus, bool)
	RuntimeStatusMap() map[int]robotcap.RuntimeStatus
	SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error)
}

type FollowTarget struct {
	Village int
	Area    int
	X       int
	Y       int
}

func (s MoveService) Move(req robotcap.CommandRequest, rc robotconfig.RuntimeConfig) (robotcap.CommandResult, error) {
	env := s.Env
	robots, err := env.SelectRobots(req)
	if err != nil {
		return robotcap.CommandResult{}, err
	}
	maps := env.LoadMapCatalog()
	status := env.RuntimeStatusMap()
	result := robotcap.NewCommandResult(len(robots))
	type movePlan struct {
		robot       robotcap.Info
		steps       int
		target      [2]int
		speeds      []int
		failed      bool
		resultIndex int
	}
	var plans []movePlan
	for _, robot := range robots {
		if st, ok := status[robot.UID]; ok && robotcap.ActiveRuntimeStatus(st) {
			if st.Village != 0 || st.Area != 0 || st.X != 0 || st.Y != 0 {
				robot.Village, robot.Area, robot.X, robot.Y = st.Village, st.Area, st.X, st.Y
			}
			if st.RobotType == 2 || st.RobotType == 3 {
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: true, State: robotcap.ActionStateStore, Message: "skip moving store robot"})
				continue
			}
		}
		targetX, targetY := s.randomTarget(robot, rc, maps)
		steps := env.RandBetween(mathx.MaxInt(2, rc.MoveSteps-1), mathx.MinInt(8, rc.MoveSteps+2))
		speeds := make([]int, steps)
		for i := range speeds {
			speeds[i] = env.RandBetween(rc.MoveSpeedMin, rc.MoveSpeedMax)
		}
		result.Accepted++
		result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateAccepted})
		plans = append(plans, movePlan{robot: robot, steps: steps, target: [2]int{targetX, targetY}, speeds: speeds, resultIndex: len(result.Robots) - 1})
	}
	maxSteps := 0
	for _, plan := range plans {
		maxSteps = mathx.MaxInt(maxSteps, plan.steps)
	}
	for step := 1; step <= maxSteps; step++ {
		for index := range plans {
			plan := &plans[index]
			if plan.failed || step > plan.steps {
				continue
			}
			if err := env.DispatchMoveStep(plan.robot, plan.robot.Village, plan.robot.Area, plan.target[0], plan.target[1], step, plan.steps, plan.speeds[step-1], rc); err != nil {
				plan.failed = true
				result.Failed++
				item := &result.Robots[plan.resultIndex]
				item.OK = false
				item.State = robotcap.ActionStateFailed
				item.Message = err.Error()
			}
		}
		if step < maxSteps {
			delay := rc.MoveStepDelayMS
			if delay > 0 {
				delay = env.RandBetween(mathx.MaxInt(300, delay/2), mathx.MaxInt(300, delay*3/2))
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
		}
	}
	waitMS := rc.MoveStepDelayMS*rc.MoveSteps + 500
	if waitMS < 500 {
		waitMS = 500
	}
	time.Sleep(time.Duration(waitMS) * time.Millisecond)
	status = env.RuntimeStatusMap()
	for i := range result.Robots {
		if result.Robots[i].State == robotcap.ActionStateFailed {
			continue
		}
		if st, ok := status[result.Robots[i].UID]; ok && robotcap.ActiveRuntimeStatus(st) {
			result.Robots[i].OK = true
			result.Robots[i].State = st.StateName
			result.Confirmed++
		} else if result.Robots[i].State == robotcap.ActionStateAccepted {
			result.Robots[i].State = robotcap.ActionStatePending
			result.Robots[i].Message = "move not confirmed by runtime state"
		}
	}
	return result, nil
}

func (s MoveService) AutoMove(info robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem, follow *FollowTarget) error {
	targetVillage, targetArea := info.Village, info.Area
	var targetX, targetY int
	if follow != nil {
		targetVillage, targetArea = follow.Village, follow.Area
		if targetVillage != info.Village || targetArea != info.Area {
			if !robotspawn.HasUsableMap(maps, targetVillage, targetArea) {
				return fmt.Errorf("follow target town area %d/%d has no usable movement geometry", targetVillage, targetArea)
			}
		}
		targetInfo := info
		targetInfo.Village = targetVillage
		targetInfo.Area = targetArea
		targetX, targetY = s.followTarget(targetInfo, *follow, rc, maps)
	} else if rc.SpawnFixed && robotspawn.HasUsableMap(maps, rc.SpawnVillage, rc.SpawnArea) {
		targetVillage, targetArea = rc.SpawnVillage, rc.SpawnArea
		targetInfo := info
		targetInfo.Village, targetInfo.Area = targetVillage, targetArea
		targetX, targetY = s.randomTarget(targetInfo, rc, maps)
	} else if door, ok := s.strandedDoor(info, maps); ok {
		targetVillage, targetArea = door.Village, door.Area
		targetInfo := info
		targetInfo.Village, targetInfo.Area = targetVillage, targetArea
		targetX, targetY = s.randomTarget(targetInfo, rc, maps)
	} else {
		targetX, targetY = s.randomTarget(info, rc, maps)
	}
	if (targetVillage != info.Village || targetArea != info.Area) && !shared.GenericAreaAllowed(info.GuildID, targetVillage) {
		return fmt.Errorf("generic area transport is unavailable for guild member destination %d/%d", targetVillage, targetArea)
	}
	// A destination the crowd rules keep out of reach - closed outright, or full
	// to its transit ceiling - is not a failure: the robot simply stays where it
	// is and wanders there instead, and the next round looks again. Refusing the
	// move quietly is what keeps a closed scene from filling with robots that
	// were only told to walk somewhere.
	if (targetVillage != info.Village || targetArea != info.Area) && s.transitRefused(targetVillage, targetArea) {
		targetVillage, targetArea = info.Village, info.Area
		targetX, targetY = s.randomTarget(info, rc, maps)
	}
	steps := s.Env.RandBetween(mathx.MaxInt(2, rc.MoveSteps-1), mathx.MinInt(8, rc.MoveSteps+2))
	for step := 1; step <= steps; step++ {
		if st, ok := s.Env.RuntimeStatus(info.UID); ok && (st.RobotType == 2 || st.RobotType == 3 || st.StateName != robotcap.RuntimeStateRunning) {
			return nil
		}
		speed := s.Env.RandBetween(rc.MoveSpeedMin, rc.MoveSpeedMax)
		if err := s.Env.DispatchMoveStep(info, targetVillage, targetArea, targetX, targetY, step, steps, speed, rc); err != nil {
			return err
		}
		if step < steps && rc.MoveStepDelayMS > 0 {
			delay := s.Env.RandBetween(mathx.MaxInt(300, rc.MoveStepDelayMS/2), mathx.MaxInt(300, rc.MoveStepDelayMS*3/2))
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
	}
	return nil
}

// strandedDoor answers where a robot that stands in a map the catalog does not
// admit should walk, instead of pacing inside that map.
//
// The server opens every character-list login in the shared Seria room, which
// the catalog marks use=false with no rectangles: a robot told to wander its own
// map circled inside that room for as long as the session lasted and never asked
// to leave, so the room held a third of the fleet where no player can see it.
// The way out of a map is one of its doors, and a map's own portal table is the
// list of them. Choosing among those keeps the destination one the server
// proves a route to: a destination drawn from the whole catalog can be a scene
// the current town has no route to at all, and a request for one of those is
// dropped without an answer, which is how a store round spent its whole retry
// budget on village 98.
func (s MoveService) strandedDoor(info robotcap.Info, maps []shared.MapCatalogItem) (shared.MapCatalogItem, bool) {
	if robotspawn.HasUsableMap(maps, info.Village, info.Area) {
		return shared.MapCatalogItem{}, false
	}
	var doors []shared.MapCatalogItem
	for _, mp := range maps {
		if mp.Village != info.Village || mp.Area != info.Area {
			continue
		}
		for _, link := range mp.Links {
			if door, ok := usableDoor(maps, link, info.Level); ok {
				doors = append(doors, door)
			}
		}
		break
	}
	doors = robotspawn.AdmittedMaps(s.Env, doors)
	if len(doors) == 0 {
		return shared.MapCatalogItem{}, false
	}
	return doors[s.Env.RandBetween(0, len(doors)-1)], true
}

// usableDoor resolves one portal entry into a map a robot may be sent to. A door
// the catalog calls unusable, one above the robot's level, or one without
// movement geometry is not a way out and stays out of the choice.
func usableDoor(maps []shared.MapCatalogItem, link shared.MapAreaKey, level int) (shared.MapCatalogItem, bool) {
	for _, mp := range maps {
		if mp.Village != link.Village || mp.Area != link.Area {
			continue
		}
		if mp.Use && mp.Level <= level && len(robotspawn.MapRectangles(mp)) > 0 {
			return mp, true
		}
		return shared.MapCatalogItem{}, false
	}
	return shared.MapCatalogItem{}, false
}

// transitRefused reports whether a map may not even be crossed. The standing
// cap decides where robots are placed; the transit ceiling above it decides the
// wall a moving robot actually meets, and the two differ on purpose: an area at
// its standing count is still walkable, because the robots in it are leaving
// again.
func (s MoveService) transitRefused(village, area int) bool {
	policy := robotspawn.CrowdPolicy(s.Env)
	if policy == nil {
		return false
	}
	held := 0
	for _, st := range s.Env.RuntimeStatusMap() {
		if st.Village == village && st.Area == area {
			held++
		}
	}
	return robotspawn.CrowdTransitFull(policy, village, area, held)
}

func (s MoveService) followTarget(info robotcap.Info, target FollowTarget, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) (int, int) {
	xMin, xMax := target.X-rc.FollowRadiusX, target.X+rc.FollowRadiusX
	yMin, yMax := target.Y-rc.FollowRadiusY, target.Y+rc.FollowRadiusY
	if rc.FollowRadiusX <= 0 {
		xMin, xMax = target.X-120, target.X+120
	}
	if rc.FollowRadiusY <= 0 {
		yMin, yMax = target.Y-30, target.Y+30
	}
	for _, mp := range maps {
		if mp.Use && mp.Village == target.Village && mp.Area == target.Area {
			intersections := robotspawn.IntersectRectangles(robotspawn.MapRectangles(mp), shared.MapRectangle{XMin: xMin, XMax: xMax, YMin: yMin, YMax: yMax})
			if x, y, ok := robotspawn.RandomPoint(s.Env, intersections); ok {
				return x, y
			}
			return s.randomTarget(info, rc, maps)
		}
	}
	return s.randomTarget(info, rc, maps)
}

func (s MoveService) randomTarget(info robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) (int, int) {
	var rectangles []shared.MapRectangle
	for _, mp := range maps {
		if mp.Use && mp.Village == info.Village && mp.Area == info.Area {
			rectangles = robotspawn.MapRectangles(mp)
			break
		}
	}
	if rc.SpawnFixed {
		configured := robotspawn.IntersectRectangles(rectangles, shared.MapRectangle{XMin: rc.SpawnXMin, XMax: rc.SpawnXMax, YMin: rc.SpawnYMin, YMax: rc.SpawnYMax})
		if len(configured) > 0 {
			rectangles = configured
		}
	}
	if len(rectangles) == 0 {
		rectangles = []shared.MapRectangle{{XMin: mathx.MaxInt(0, info.X-120), XMax: info.X + 120, YMin: mathx.MaxInt(0, info.Y-40), YMax: info.Y + 40}}
	}
	for i := 0; i < 12; i++ {
		x, y, ok := robotspawn.RandomPoint(s.Env, rectangles)
		if !ok {
			break
		}
		if mathx.AbsInt(x-info.X) >= 40 || mathx.AbsInt(y-info.Y) >= 15 {
			return x, y
		}
	}
	if x, y, ok := robotspawn.RandomPoint(s.Env, rectangles); ok {
		return x, y
	}
	return info.X, info.Y
}
