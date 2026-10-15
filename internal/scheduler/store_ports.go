package scheduler

import (
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func (m *RobotManager) storeWorkflow() storecap.Workflow {
	return storecap.Workflow{Env: storeWorkflowEnv{manager: m}}
}

type storeWorkflowEnv struct {
	manager *RobotManager
}

func (e storeWorkflowEnv) AddAutoStore(success, failed, expired int) {
	e.manager.addAutoStore(success, failed, expired)
}

func (e storeWorkflowEnv) AcquireAutoStoreSlot(rc robotconfig.RuntimeConfig) (func(), bool) {
	return e.manager.acquireAutoItemStoreSlot(rc)
}

func (e storeWorkflowEnv) BeginStoreBusy(uid int) bool {
	return e.manager.beginStoreBusy(uid)
}

func (e storeWorkflowEnv) Config() robotconfig.RuntimeConfig {
	return e.manager.loadRobotConfig()
}

func (e storeWorkflowEnv) EndStoreBusy(uid int) {
	e.manager.endStoreBusy(uid)
}

func (e storeWorkflowEnv) EnsureStoreInventoryAndStall(info robotcap.Info, rc robotconfig.RuntimeConfig) error {
	return e.manager.storePreparerWithPool(rc).EnsureInventoryAndStall(info, rc)
}

func (e storeWorkflowEnv) FinishStoreState(uid, cid int, reason string) {
	e.manager.finishStoreState(uid, cid, reason)
}

func (e storeWorkflowEnv) InvalidateCharacterCache(uid int) error {
	return e.manager.invalidateCharacterCache(uid)
}

func (e storeWorkflowEnv) Logf(format string, args ...interface{}) {
	robotLogf(format, args...)
}

func (e storeWorkflowEnv) Logout(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	return e.manager.sessionService().Logout(req)
}

func (e storeWorkflowEnv) MarkStoreStarted(uid int) error {
	return errSchedulerStorageUnavailable
}

func (e storeWorkflowEnv) Online(req robotcap.CommandRequest, confirm bool) (robotcap.CommandResult, error) {
	return e.manager.sessionService().Online(req, confirm, e.manager.loadRobotConfig())
}

func (e storeWorkflowEnv) PrepareStorePosition(info robotcap.Info) error {
	return errSchedulerStorageUnavailable
}

func (e storeWorkflowEnv) RefreshCharacterForWrite(uid int, shouldStop func() bool) (bool, error) {
	return e.manager.refreshCharacterForWrite(uid, shouldStop)
}

func (e storeWorkflowEnv) ResumeCharacterAfterWrite(uid int, shouldStop func() bool) (bool, error) {
	return e.manager.resumeCharacterAfterWrite(uid, shouldStop)
}

func (e storeWorkflowEnv) OfflineCharacterForWrite(uid int, shouldStop func() bool) (bool, error) {
	return e.manager.offlineCharacterForWrite(uid, shouldStop)
}

func (e storeWorkflowEnv) WaitCharacterRunning(uid int, shouldStop func() bool) (bool, error) {
	return e.manager.waitCharacterRunning(uid, shouldStop)
}

func (e storeWorkflowEnv) RestoreAutoNormalOnline(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) (robotcap.Info, bool) {
	return e.manager.restoreAutoNormalOnline(info, rc, reason)
}

func (e storeWorkflowEnv) RobotGamePort() int {
	return e.manager.cfg.RobotGamePort
}

func (e storeWorkflowEnv) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	return e.manager.runtimeStatusMap()
}

func (e storeWorkflowEnv) SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	return e.manager.selectRobots(req)
}

func (e storeWorkflowEnv) SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool {
	runtime := e.manager.storeSessionRuntime()
	return runtime != nil && runtime.SetAreaFrom(uid, village, area, x, y, fromVillage, fromArea)
}

func (e storeWorkflowEnv) StartPrivateStore(uid int, title string) bool {
	return e.manager.doll.StartPrivateStore(uid, title)
}

func (e storeWorkflowEnv) StoreTitle(uid int, rc robotconfig.RuntimeConfig) string {
	return e.manager.storeTitle(uid, rc)
}

func (e storeWorkflowEnv) StorePoints() *storecap.PointCoordinator {
	return e.manager.storePoints()
}

func (e storeWorkflowEnv) SyncRobotCharacterVillage(cid int, village int) error {
	return errSchedulerStorageUnavailable
}

func (e storeWorkflowEnv) WaitAccountOffline(uid int, shouldStop func() bool) (bool, error) {
	return e.manager.waitAccountOffline(uid, shouldStop)
}

func (m *RobotManager) storePreparer() storecap.Preparer {
	return storecap.Preparer{
		Env:        storePreparationEnv{manager: m},
		WorldHorns: m.worldHornCache,
	}
}

func (m *RobotManager) storePreparerWithPool(rc robotconfig.RuntimeConfig) storecap.Preparer {
	preparer := m.storePreparer()
	preparer.Pool = m.storePool(rc)
	return preparer
}

func (m *RobotManager) storePool(rc robotconfig.RuntimeConfig) *storecap.ItemPool {
	m.storePoolLock.Lock()
	defer m.storePoolLock.Unlock()
	if m.storeItemPool == nil {
		catalogs := m.loadItemCatalogs()
		m.storeItemPool = storecap.BuildItemPool(catalogs.Equipment, catalogs.Stackable, rc.StoreEquipmentIntensifyMin, rc.StoreEquipmentIntensifyMax)
		robotLogf("[StorePool] generated material=%d equipment=%d intensify=%d..%d\n", len(m.storeItemPool.Materials), len(m.storeItemPool.Equipment), rc.StoreEquipmentIntensifyMin, rc.StoreEquipmentIntensifyMax)
	}
	return m.storeItemPool
}

func (m *RobotManager) storeTitle(uid int, _ robotconfig.RuntimeConfig) string {
	path := ""
	if m.cfg != nil && m.cfg.ConfigDir != "" {
		path = layout.New(m.cfg.ConfigDir).StoreTitles()
	}
	if currentPath := m.storeTitlePathSnapshot.Load(); currentPath != nil && currentPath.path == path {
		if titles := m.storeTitleSnapshot.Load(); titles != nil {
			return titles.TitleForUID(uid)
		}
	}
	m.storeTitleLock.Lock()
	defer m.storeTitleLock.Unlock()
	if !m.storeTitlesLoaded || m.storeTitlePath != path {
		catalog, err := storecap.LoadTitleCatalog(path)
		if err != nil {
			robotLogf("[StoreTitle] fallback path=%s err=%v\n", path, err)
			catalog = storecap.NewTitleCatalog(nil)
		}
		m.storeTitles = catalog
		m.storeTitlePath = path
		m.storeTitlesLoaded = true
		m.storeTitleSnapshot.Store(catalog)
		m.storeTitlePathSnapshot.Store(&storeTitlePathValue{path: path})
		robotLogf("[StoreTitle] loaded path=%s titles=%d\n", path, catalog.Len())
	}
	return m.storeTitles.TitleForUID(uid)
}

type storePreparationEnv struct {
	manager *RobotManager
}

func (e storePreparationEnv) EnsureStorePermissionRecord(uid, cid int) (storecap.PermissionStatus, error) {
	return storecap.PermissionStatus{}, errSchedulerStorageUnavailable
}

func (e storePreparationEnv) LoadInventory(cid int) ([]byte, error) {
	return nil, errSchedulerStorageUnavailable
}

func (e storePreparationEnv) Logf(format string, args ...interface{}) {
	robotLogf(format, args...)
}

func (e storePreparationEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e storePreparationEnv) ReplaceStoreStall(uid int, title string, items []storecap.StallItem) (storecap.StallResult, error) {
	return storecap.StallResult{}, errSchedulerStorageUnavailable
}

func (e storePreparationEnv) StoreTitle(uid int, rc robotconfig.RuntimeConfig) string {
	return e.manager.storeTitle(uid, rc)
}

func (e storePreparationEnv) SaveInventory(cid int, capacity int, raw []byte) error {
	return errSchedulerStorageUnavailable
}

func (e storePreparationEnv) SaveInventoryRaw(cid int, raw []byte) error {
	return errSchedulerStorageUnavailable
}

func (m *RobotManager) storeMaintenance() storecap.Maintenance {
	return storecap.Maintenance{Env: storeMaintenanceEnv{manager: m}}
}

type storeMaintenanceEnv struct {
	manager *RobotManager
}

func (e storeMaintenanceEnv) LoadMapCatalog() []shared.MapCatalogItem {
	return e.manager.loadMapCatalog()
}

// DemandLocation reports the position the operator's demand plan pinned this
// robot to, so a store round restores the robot to its pin rather than to a
// town the spawn settings would pick.
func (e storeMaintenanceEnv) DemandLocation(uid int) (int, int, int, int, bool) {
	pin, ok := e.manager.demandLocationFor(uid)
	if !ok {
		return 0, 0, 0, 0, false
	}
	return pin.village, pin.area, pin.x, pin.y, true
}

func (e storeMaintenanceEnv) Logf(format string, args ...interface{}) {
	robotLogf(format, args...)
}

func (e storeMaintenanceEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e storeMaintenanceEnv) RandomMap(maps []shared.MapCatalogItem, level int) (shared.MapCatalogItem, bool) {
	return e.manager.randomMap(maps, level)
}

func (e storeMaintenanceEnv) RobotLocations() ([]shared.MapLocation, error) {
	return e.manager.robotLocations()
}

// CrowdMapRule keeps the restore path from parking a retired stall robot in a
// map the crowd rules keep empty.
func (e storeMaintenanceEnv) CrowdMapRule(village, area int) shared.CrowdMapRule {
	return e.manager.crowdMapRule(village, area)
}

func (e storeMaintenanceEnv) ResetPrivateStore(uid int) {
	if e.manager.doll != nil {
		e.manager.doll.ResetPrivateStore(uid)
	}
}

func (e storeMaintenanceEnv) RestoreDummyNormal(info robotcap.Info) error {
	// Protocol adapters keep no dummylist table; recording the planned normal
	// position in the robot directory is what the next online selection reads.
	e.manager.rememberRobotLocation(info.UID, info.Village, info.Area, info.X, info.Y)
	return nil
}

func (e storeMaintenanceEnv) RevokeStorePermission(uid, cid int) error {
	if uid <= 0 {
		return nil
	}
	// Without the private item stall there is no store entitlement to revoke.
	return nil
}

func (e storeMaintenanceEnv) SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	return e.manager.selectRobots(req)
}

func (e storeMaintenanceEnv) SyncCharacterVillage(cid int, village int) (int, error) {
	// The server position plane is applied through the game session; there is
	// no character village column in this adapter's persistence boundary.
	return village, nil
}
