package scheduler

import (
	"context"
	robotcap "robot/internal/capability/robot"
	robotaction "robot/internal/capability/robotaction"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func (m *RobotManager) moveService() robotaction.MoveService {
	return robotaction.MoveService{Env: moveActionEnv{manager: m}}
}

type moveActionEnv struct {
	manager *RobotManager
}

func (e moveActionEnv) DispatchMoveStep(info robotcap.Info, targetVillage, targetArea, targetX, targetY, step, steps, speed int, rc robotconfig.RuntimeConfig) error {
	x := info.X + (targetX-info.X)*step/steps
	y := info.Y + (targetY-info.Y)*step/steps
	command := shared.RuntimeMoveCommand{
		UID:      info.UID,
		Village:  targetVillage,
		Area:     targetArea,
		X:        x,
		Y:        y,
		MoveType: rc.MoveType,
		Speed:    speed,
	}
	err := e.manager.actions.MoveTown(context.Background(), command)
	if err != nil {
		return err
	}
	if step == steps {
		if batcher := e.manager.positionWrites.Load(); batcher != nil {
			if err := batcher.Queue(info, targetVillage, targetArea, targetX, targetY); err != nil {
				robotLogf("POSITION_BATCH_QUEUE_FAILED uid=%d err=%v\n", info.UID, err)
			}
		}
	}
	return nil
}

func (e moveActionEnv) LoadMapCatalog() []shared.MapCatalogItem {
	return e.manager.loadMapCatalog()
}

// CrowdMapRule carries the world's per-map population policy into the move
// capability, which probes for it. A robot walking somewhere reads the same
// rule a robot being placed reads, so an area the operator closed cannot be
// reached by walking instead of by spawning.
func (e moveActionEnv) CrowdMapRule(village, area int) shared.CrowdMapRule {
	return e.manager.crowdMapRule(village, area)
}

func (e moveActionEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e moveActionEnv) RuntimeStatus(uid int) (robotcap.RuntimeStatus, bool) {
	return e.manager.runtimeStatus(uid)
}

func (e moveActionEnv) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	return e.manager.runtimeStatusMap()
}

func (e moveActionEnv) SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	return e.manager.selectRobots(req)
}
