package scheduler

import (
	"context"
	"fmt"

	"robot/internal/capability/catalog"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

// CreateRobots starts a tracked structural operation and delegates the whole
// provisioning flow to the selected adapter's robot creator. The scheduler
// owns the operation bookkeeping only; PVF, database and protocol rules stay in
// the adapter.
func (m *RobotManager) CreateRobots(req robotcap.CreateRequest) ([]robotcap.Info, error) {
	if err := m.requireBackendCapability(shared.CapabilityProvision); err != nil {
		return nil, err
	}
	if m.backendRobotCreator == nil {
		return nil, shared.UnsupportedCapabilityError{
			Backend:   m.backendInfo.ID,
			Operation: shared.CapabilityProvision,
			Reason:    "backend robot creator is not configured",
		}
	}
	_, finishOperation, err := m.beginTrackedStructuralOperation("create", fmt.Sprintf("count=%d", req.Count))
	if err != nil {
		return nil, err
	}
	robots, opErr := m.backendRobotCreator.CreateRobots(context.Background(), req)
	finishOperation(fmt.Sprintf("created=%d", len(robots)), opErr)
	return robots, opErr
}

func equipmentCatalogByID(items []shared.EquipmentCatalogItem) map[int]shared.EquipmentCatalogItem {
	out := make(map[int]shared.EquipmentCatalogItem, len(items))
	for _, item := range items {
		if item.ID > 0 {
			out[item.ID] = item
		}
	}
	return out
}

func (m *RobotManager) loadItemCatalogs() catalog.ItemCatalogView {
	if m.cfg == nil {
		return catalog.ItemCatalogView{}
	}
	return catalog.ViewItemCatalogs(layout.New(m.cfg.ConfigDir).PVF)
}

func (m *RobotManager) applyConfiguredLocation(info *robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) {
	robotspawn.ApplyConfiguredLocation(spawnEnv{manager: m}, info, rc, maps)
}

func (m *RobotManager) randomMap(maps []shared.MapCatalogItem, level int) (shared.MapCatalogItem, bool) {
	return robotspawn.RandomMap(spawnEnv{manager: m}, maps, level)
}

type spawnEnv struct {
	manager *RobotManager
}

func (e spawnEnv) FollowAccountVillage(account string) (int, bool, error) {
	return e.manager.lookupFollowAccountVillage(account)
}

func (e spawnEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e spawnEnv) RandIntn(n int) int {
	return e.manager.randIntn(n)
}

// CrowdMapRule carries the world's per-map population policy into the spawn
// capability, which probes for it instead of taking a package dependency.
func (e spawnEnv) CrowdMapRule(village, area int) shared.CrowdMapRule {
	return e.manager.crowdMapRule(village, area)
}
