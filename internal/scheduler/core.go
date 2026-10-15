package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotspawn "robot/internal/capability/robotspawn"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/config"
	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

type RobotManager struct {
	mutationMu                 lockhub.RWLocker
	actorMutationMu            lockhub.Locker
	database                   any
	robotState                 robotstate.Directory
	cfg                        *config.SysConfig
	doll                       Runtime
	gameCommandGate            shared.GameCommandGate
	actions                    BackendActionTransport
	sessions                   sessionDriver
	backendRobotCreator        BackendRobotCreator
	backendRobotCleaner        BackendRobotCleaner
	backendRobotPurger         BackendRobotPurger
	backendPopulationInspector BackendPopulationInspector
	backendInfo                shared.BackendInfo
	persistenceInspector       shared.PersistenceInspector
	systemAnnouncer            shared.SystemAnnouncer
	worldShout                 WorldShout
	locks                      *lockhub.Hub
	startedAt                  time.Time
	autoMu                     lockhub.Locker
	sessionMu                  lockhub.Locker
	rand                       *rand.Rand
	sessionLastLogout          map[int]time.Time
	sessionReloginDelay        time.Duration
	sessionLogoutCleanupAt     time.Time
	autoStoreBusy              map[int]bool
	autoStoreItemPending       int
	autoStoreDisjointPending   int
	cleanupPendingUIDs         map[int]time.Time
	autoStoreActive            int
	autoItemStoreActive        int
	autoEnabled                bool
	autoPortSince              time.Time
	autoPortReady              bool
	autoPortLog                time.Time
	autoPortProbeAt            time.Time
	autoPortProbeAddr          string
	autoPortProbeOpen          bool
	autoPortProbeError         string
	autoPortProbeInflight      bool
	autoPortDial               func(string, string, time.Duration) (net.Conn, error)
	// autoPortProbe is an adapter-provided readiness probe. Backends whose
	// game listener allocates a session per connection install one so the
	// periodic AutoGate check does not create game work.
	autoPortProbe                   func() bool
	autoStats                       robotcap.AutoStatus
	autoBreakerUntil                time.Time
	autoBreakerReason               string
	autoBreakerLastCheck            time.Time
	autoBreakerLastOnlineFailed     int
	autoBreakerLastMoveFailed       int
	autoBreakerLastShoutLocalFailed int
	autoBreakerLastShoutWorldFailed int
	autoBreakerLastStoreFailed      int
	onlineGateMu                    lockhub.Locker
	onlineTokens                    float64
	onlineTokenAt                   time.Time
	onlineInFlight                  int
	onlineBreakerStreak             int
	spawnRepairMu                   lockhub.Locker
	schedulerLastOnlineSuccess      int
	schedulerLastOnlineFailed       int
	schedulerRecentOnlineSuccess    int
	schedulerRecentOnlineFailed     int
	onlineAttemptSuccess            int
	onlineAttemptFailed             int
	schedulerLastAttemptSuccess     int
	schedulerLastAttemptFailed      int
	schedulerRecentAttemptSuccess   int
	schedulerRecentAttemptFailed    int
	schedulerLastStoreSuccess       int
	schedulerLastStoreFailed        int
	schedulerLastStoreNoPoint       int
	schedulerRecentStoreSuccess     int
	schedulerRecentStoreFailed      int
	schedulerRecentStoreNoPoint     int
	runtimeState                    runtimeStateTable
	followLookupMu                  lockhub.Locker
	followLookup                    followAccountLookup
	followLookupInFlight            string
	schedulerStatus                 robotcap.SchedulerStatus
	nextOperationID                 int64
	operations                      []robotcap.OperationStatus
	structuralOp                    string
	structuralOpStarted             time.Time
	structuralOpGeneration          uint64
	actorContainerOp                string
	actorContainerOpStarted         time.Time
	actorContainerOpGeneration      uint64
	configApplyMu                   lockhub.Locker
	configSnapshot                  atomic.Pointer[robotConfigSnapshot]
	runtimeFilesWatched             atomic.Bool
	runtimeFileWatchMu              lockhub.Locker
	runtimeFilePoller               *filewatch.Poller
	shoutTemplateSnapshot           atomic.Pointer[robottemplate.ShoutTemplates]
	nameTemplateSnapshot            atomic.Pointer[robottemplate.NameTemplates]
	supervisor                      *RobotSupervisor
	storePolicy                     shared.BackendStorePolicy
	crowdPolicy                     shared.CrowdMapPolicy
	demandPinMu                     lockhub.RWLocker
	demandPins                      map[int]demandLocation
	storeRuntime                    BackendStoreRuntime
	expertJobProfessionWriter       BackendExpertJobProfessionWriter
	serverNoticeTrigger             shared.ServerNoticer
	serverNoticeStock               shared.ServerNoticeStockWriter
	serverNoticeMu                  lockhub.Locker
	serverNoticeAreas               map[string]*serverNoticeAreaWindow
	followAccountLocator            shared.FollowAccountLocator
	playerLocator                   shared.PlayerLocator
	accountOnlineChecker            shared.AccountOnlineChecker
	storePointsCoord                *storecap.PointCoordinator
	worldHornCache                  *storecap.WorldHornCache
	storePoolLock                   lockhub.Locker
	storeItemPool                   *storecap.ItemPool
	storeTitleLock                  lockhub.Locker
	storeTitles                     *storecap.TitleCatalog
	storeTitleSnapshot              atomic.Pointer[storecap.TitleCatalog]
	storeTitlePathSnapshot          atomic.Pointer[storeTitlePathValue]
	storeTitlePath                  string
	storeTitlesLoaded               bool
	townMapCatalogMu                lockhub.RWLocker
	townMapCatalog                  []shared.MapCatalogItem
	positionWrites                  atomic.Pointer[positionBatcher]
	characterCacheInvalidate        func(uid int) error
	mailNotifier                    MailNotifier
	mailNotifyNext                  time.Time
	mailNotifyRunning               bool
	mailNotifyDone                  chan struct{}
	mailNotifyCancel                context.CancelFunc
	mailNotifyLastErrorLog          time.Time
	partyAccountRangeSink           func(start, end int)
	backgroundMu                    lockhub.Locker
	backgroundWG                    sync.WaitGroup
	shuttingDown                    bool
	shutdownOnce                    sync.Once
	shutdownErr                     error
}

// BackendActionTransport is the backend-neutral action port used by the
// scheduler. Concrete packet construction remains in backend adapters.
type BackendActionTransport interface {
	MoveTown(context.Context, shared.RuntimeMoveCommand) error
	ShoutLocal(context.Context, shared.RuntimeShoutCommand) error
}

type BackendSessionTransport interface {
	Open(context.Context, int, shared.OpenSessionRequest) error
	Close(int) error
}

type BackendRobotCreator interface {
	CreateRobots(context.Context, robotcap.CreateRequest) ([]robotcap.Info, error)
}

type BackendRobotCleaner interface {
	CleanupRobots(context.Context, robotcap.CleanupRequest) (robotcap.CleanupResult, error)
}

// BackendRobotPurger owns backend identifier resolution and durable deletion.
// The scheduler only coordinates lifecycle state around the returned plan.
type BackendRobotPurger interface {
	PlanDangerousDelete(context.Context, robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error)
	ExecuteDangerousDelete(context.Context, robotcap.DangerousDeletePlan) (robotcap.DangerousDeleteResult, error)
}

type BackendPopulationInspector interface {
	PopulationReport(context.Context) (robotcap.PopulationReport, error)
}

func (m *RobotManager) SetBackendRobotCreator(info shared.BackendInfo, creator BackendRobotCreator) {
	if m != nil {
		m.backendInfo = info
		m.backendRobotCreator = creator
	}
}

// ConfigureBackendRuntime installs the selected backend's shared runtime
// ports. Concrete database and announcement behavior remains in composition.
func (m *RobotManager) ConfigureBackendRuntime(info shared.BackendInfo, persistence shared.PersistenceInspector, announcer shared.SystemAnnouncer) {
	if m == nil {
		return
	}
	m.backendInfo = info
	m.persistenceInspector = persistence
	m.systemAnnouncer = announcer
}

func (m *RobotManager) SetBackendRobotCleaner(cleaner BackendRobotCleaner) {
	if m != nil {
		m.backendRobotCleaner = cleaner
	}
}

func (m *RobotManager) SetBackendRobotPurger(purger BackendRobotPurger) {
	if m != nil {
		m.backendRobotPurger = purger
	}
}

func (m *RobotManager) SetBackendPopulationInspector(inspector BackendPopulationInspector) {
	if m != nil {
		m.backendPopulationInspector = inspector
	}
}

func (m *RobotManager) PopulationReport(ctx context.Context) (robotcap.PopulationReport, error) {
	if m == nil {
		return robotcap.PopulationReport{}, fmt.Errorf("population inspector is not configured")
	}
	if m.backendPopulationInspector == nil {
		return robotcap.PopulationReport{}, fmt.Errorf("backend %s population inspector is not configured", m.backendInfo.ID)
	}
	return m.backendPopulationInspector.PopulationReport(ctx)
}

func (m *RobotManager) SetBackendSessionTransport(transport BackendSessionTransport) {
	if m != nil && transport != nil {
		m.sessions = protocolSessionDriver{manager: m, transport: transport}
	}
}

// SetGameCommandGate installs the selected backend's runtime precondition.
func (m *RobotManager) SetGameCommandGate(gate shared.GameCommandGate) {
	if m != nil && gate != nil {
		m.gameCommandGate = gate
	}
}

func (m *RobotManager) CheckGameCommand() error {
	if m == nil || m.gameCommandGate == nil {
		return nil
	}
	return m.gameCommandGate.Check()
}

func (m *RobotManager) SetBackendActionTransport(transport BackendActionTransport) {
	if m != nil && transport != nil {
		m.actions = transport
	}
}

// SetTownMapCatalog publishes an immutable backend-projected town map
// snapshot. An empty snapshot restores the runtime-file fallback.
func (m *RobotManager) SetTownMapCatalog(maps []shared.MapCatalogItem) {
	if m == nil {
		return
	}
	copyMaps := cloneTownMapCatalog(maps)
	m.townMapCatalogMu.Lock()
	m.townMapCatalog = copyMaps
	m.townMapCatalogMu.Unlock()
}

type storeTitlePathValue struct {
	path string
}

// SetBackendStorePolicy installs the selected adapter's store semantics. The
// scheduler keeps the orchestration; wire codes and shop constants stay in the
// adapter.
func (m *RobotManager) SetBackendStorePolicy(policy shared.BackendStorePolicy) {
	if m == nil {
		return
	}
	m.storePolicy = policy
}

// SetCrowdMapPolicy installs the backend's per-map population policy. Every
// placement, movement and stall decision reads the same rules, so a map cannot
// be closed to spawns but open to stalls.
func (m *RobotManager) SetCrowdMapPolicy(policy shared.CrowdMapPolicy) {
	if m == nil {
		return
	}
	m.crowdPolicy = policy
}

// crowdMapRule returns the crowd rule of one map, or the unrestricted rule when
// the backend installed no policy.
func (m *RobotManager) crowdMapRule(village, area int) shared.CrowdMapRule {
	if m == nil || m.crowdPolicy == nil {
		return shared.CrowdMapRule{}
	}
	return m.crowdPolicy.CrowdMapRule(village, area)
}

// crowdStallAllows reports whether a robot of one kind may open its stall in a
// map. A manager without a crowd policy allows every map, which keeps the
// per-robot trade the only rule.
func (m *RobotManager) crowdStallAllows(village, area int, kind shared.ExpertJobStoreKind) bool {
	if m == nil || m.crowdPolicy == nil {
		return true
	}
	rule := m.crowdPolicy.CrowdMapRule(village, area)
	return rule.AllowsStall(kind, m.crowdStallHeld(village, area, kind))
}

// crowdStallHeld counts the stalls of one kind already open in a map. A stall
// counts from the moment the session reports it, so two robots racing for the
// last slot of a map are separated by the first success surfacing in the status
// map; the point pool separates the rest.
func (m *RobotManager) crowdStallHeld(village, area int, kind shared.ExpertJobStoreKind) int {
	held := 0
	for _, st := range m.runtimeStatusMap() {
		if st.Village != village || st.Area != area {
			continue
		}
		switch {
		case kind == shared.ExpertJobStoreEnchant && st.EnchantActive:
			held++
		case kind == shared.ExpertJobStoreDisjoint && st.DisjointActive:
			held++
		}
	}
	return held
}

// BackendStoreRuntime is the adapter-owned session surface for store actions.
// Adapters with a protocol session implement it directly; legacy backends fall
// back to the doll runtime through storeSessionRuntime.
type BackendStoreRuntime interface {
	SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool
	StartExpertJobStore(uid int, kind shared.ExpertJobStoreKind, cost uint32) bool
	CloseExpertJobStore(uid int) bool
}

// BackendExpertJobProfessionWriter prepares an expert-job profession in the
// adapter's persistence boundary. It runs while the account is offline and
// before the next login; backends without a writer keep the capability
// disabled instead of faking the protocol state. The read-only probes let the
// scheduler skip the offline cycle while the stall still has durability.
type BackendExpertJobProfessionWriter interface {
	EnsureDisjointProfession(cid int) error
	DisjointProfessionReady(cid int) (bool, error)
	EnsureEnchantProfession(cid int) error
	EnchantProfessionReady(cid int) (bool, error)
}

// SetBackendStoreRuntime installs the selected adapter's store session
// operations.
func (m *RobotManager) SetBackendStoreRuntime(runtime BackendStoreRuntime) {
	if m != nil && runtime != nil {
		m.storeRuntime = runtime
	}
}

// SetBackendExpertJobProfessionWriter installs the selected adapter's offline
// expert-job preparation.
func (m *RobotManager) SetBackendExpertJobProfessionWriter(writer BackendExpertJobProfessionWriter) {
	if m != nil && writer != nil {
		m.expertJobProfessionWriter = writer
	}
}

// SetBackendServerNoticeRuntime installs the selected adapter's live-session
// notice trigger.
func (m *RobotManager) SetBackendServerNoticeRuntime(runtime shared.ServerNoticer) {
	if m != nil && runtime != nil {
		m.serverNoticeTrigger = runtime
	}
}

// SetBackendServerNoticeStockWriter installs the selected adapter's offline
// notice stock preparation.
func (m *RobotManager) SetBackendServerNoticeStockWriter(writer shared.ServerNoticeStockWriter) {
	if m != nil && writer != nil {
		m.serverNoticeStock = writer
	}
}

// storeSessionRuntime prefers the adapter store runtime and falls back to the
// legacy doll runtime so native test doubles keep working.
func (m *RobotManager) storeSessionRuntime() BackendStoreRuntime {
	if m == nil {
		return nil
	}
	if m.storeRuntime != nil {
		return m.storeRuntime
	}
	if m.doll == nil {
		return nil
	}
	return dollStoreRuntime{runtime: m.doll}
}

func (m *RobotManager) expertJobWriter() BackendExpertJobProfessionWriter {
	if m == nil {
		return nil
	}
	return m.expertJobProfessionWriter
}

type dollStoreRuntime struct {
	runtime Runtime
}

func (d dollStoreRuntime) SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool {
	return d.runtime.SetAreaFrom(uid, village, area, x, y, fromVillage, fromArea)
}

// StartExpertJobStore maps the legacy doll runtime's disjoint-only entrypoint.
// Native test doubles never implemented an enchanter stall.
func (d dollStoreRuntime) StartExpertJobStore(uid int, kind shared.ExpertJobStoreKind, cost uint32) bool {
	if kind != shared.ExpertJobStoreDisjoint {
		return false
	}
	return d.runtime.StartDisjointStore(uid, cost)
}

func (dollStoreRuntime) CloseExpertJobStore(int) bool { return false }

// SetBackendFollowAccountLocator installs the adapter's follow-account lookup.
func (m *RobotManager) SetBackendFollowAccountLocator(locator shared.FollowAccountLocator) {
	if m == nil {
		return
	}
	m.followAccountLocator = locator
}

// SetBackendAccountOnlineChecker installs the adapter's account-online probe.
func (m *RobotManager) SetBackendAccountOnlineChecker(checker shared.AccountOnlineChecker) {
	if m == nil {
		return
	}
	m.accountOnlineChecker = checker
}

// SetBackendPlayerLocator installs the adapter's player-position lookup, which
// is what lets the demand layer follow the human around the world.
func (m *RobotManager) SetBackendPlayerLocator(locator shared.PlayerLocator) {
	if m == nil {
		return
	}
	m.playerLocator = locator
}

// locatePlayer reads where the follow account is playing. An adapter that
// cannot answer returns ok=false rather than an error: the demand layer treats
// "not found" and "not implemented" alike, as a floating layer with nothing to
// follow.
func (m *RobotManager) locatePlayer(account string) (shared.PlayerLocation, bool, error) {
	if m == nil || m.playerLocator == nil {
		return shared.PlayerLocation{}, false, nil
	}
	return m.playerLocator.LocatePlayer(context.Background(), account)
}

// lookupFollowAccountVillage queries the adapter for the follow account's last
// played village. Without an adapter implementation the follow target is
// unavailable.
func (m *RobotManager) lookupFollowAccountVillage(account string) (int, bool, error) {
	if m == nil || m.followAccountLocator == nil {
		return 0, false, errSchedulerStorageUnavailable
	}
	return m.followAccountLocator.FollowAccountVillageLastPlayed(context.Background(), account)
}

// accountOnline asks the adapter whether the account still has a session.
func (m *RobotManager) accountOnline(uid int) (bool, error) {
	if m == nil || m.accountOnlineChecker == nil {
		return false, errSchedulerStorageUnavailable
	}
	return m.accountOnlineChecker.AccountOnline(uid)
}

// expertJobStoreCost returns the adapter-declared gold cost for one stall
// kind. Adapters without enchant-specific pricing reuse the disjoint cost.
func (m *RobotManager) expertJobStoreCost(kind shared.ExpertJobStoreKind) uint32 {
	if m == nil || m.storePolicy == nil {
		return 0
	}
	if kind == shared.ExpertJobStoreEnchant {
		if policy, ok := m.storePolicy.(interface{ EnchantStoreCost() uint32 }); ok {
			return policy.EnchantStoreCost()
		}
	}
	return m.storePolicy.DisjointStoreCost()
}

// expertJobStoreFailure maps an adapter failure code to a stable reason string
// and whether the same session may retry at another coordinate.
func (m *RobotManager) expertJobStoreFailure(kind shared.ExpertJobStoreKind, errCode byte) (string, bool) {
	if m != nil && m.storePolicy != nil {
		if kind == shared.ExpertJobStoreEnchant {
			if policy, ok := m.storePolicy.(interface {
				EnchantFailure(byte) (string, bool)
			}); ok {
				return policy.EnchantFailure(errCode)
			}
		}
		return m.storePolicy.DisjointFailure(errCode)
	}
	prefix := "disjoint"
	if kind == shared.ExpertJobStoreEnchant {
		prefix = "enchant"
	}
	if errCode == 0 {
		return prefix + "_failed", false
	}
	return fmt.Sprintf("%s_err_0x%02x", prefix, errCode), false
}

// expertJobStoreReasonRetryable decides whether a failure reason allows an
// in-session coordinate retry. Scheduler-owned reasons keep their meaning;
// adapter-owned reasons are classified by the adapter.
func (m *RobotManager) expertJobStoreReasonRetryable(kind shared.ExpertJobStoreKind, reason string) bool {
	switch reason {
	case "set_area_failed", "ack_timeout":
		return true
	}
	if m != nil && m.storePolicy != nil {
		if kind == shared.ExpertJobStoreEnchant {
			if policy, ok := m.storePolicy.(interface {
				EnchantReasonRetryable(string) (bool, bool)
			}); ok {
				if retry, known := policy.EnchantReasonRetryable(reason); known {
					return retry
				}
			} else if retry, known := m.storePolicy.DisjointReasonRetryable(reason); known {
				// Adapters without enchant-specific reasons share the
				// expert-store error space.
				return retry
			}
		}
		if retry, known := m.storePolicy.DisjointReasonRetryable(reason); known {
			return retry
		}
	}
	return false
}

// expertJobStoreKindForUID picks the stall kind for one robot. The choice is
// deterministic per UID so a character keeps one profession instead of
// swapping jobs on every store cycle.
func (m *RobotManager) expertJobStoreKindForUID(uid int, rc robotconfig.RuntimeConfig) shared.ExpertJobStoreKind {
	return shared.ExpertJobStoreKindForUID(uid, rc.StoreEnchantRatioPercent)
}

var errSchedulerStorageUnavailable = errors.New("scheduler robot state directory is not configured")

func NewRobotManager(database any, cfg *config.SysConfig, doll Runtime) *RobotManager {
	if doll == nil {
		doll = noopRuntime{}
	}
	manager := &RobotManager{
		database:            database,
		cfg:                 cfg,
		doll:                doll,
		gameCommandGate:     allowGameCommandGate{},
		worldShout:          noopWorldShout{},
		locks:               lockhub.New(),
		startedAt:           time.Now(),
		rand:                rand.New(rand.NewSource(time.Now().UnixNano())),
		cleanupPendingUIDs:  make(map[int]time.Time),
		sessionLastLogout:   make(map[int]time.Time),
		sessionReloginDelay: 15 * time.Second,
		worldHornCache:      storecap.NewWorldHornCache(),
	}
	manager.actions = runtimeActionTransport{manager: manager}
	manager.sessions = runtimeSessionDriver{manager: manager}
	manager.positionWrites.Store(newPositionBatcher(manager.positionRepo(), defaultPositionBatchOptions()))
	return manager
}

type allowGameCommandGate struct{}

func (allowGameCommandGate) Check() error { return nil }

// SetRobotStateDirectory injects the adapter-owned robot state directory.
// The selected adapter installs this during runtime initialization.
func (m *RobotManager) SetRobotStateDirectory(directory robotstate.Directory) {
	if m == nil {
		return
	}
	m.robotState = directory
	if batcher := m.positionWrites.Swap(newPositionBatcher(m.positionRepo(), defaultPositionBatchOptions())); batcher != nil {
		if err := batcher.Close(); err != nil {
			robotLogf("POSITION_BATCH_CLOSE_FAILED err=%v\n", err)
		}
	}
}

func (m *RobotManager) ensureSchedulerStorage() error {
	if m != nil && m.robotState != nil {
		return nil
	}
	return errSchedulerStorageUnavailable
}

func (m *RobotManager) selectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	if m.robotState != nil {
		return m.robotState.SelectRobots(context.Background(), req)
	}
	return nil, errSchedulerStorageUnavailable
}

func (m *RobotManager) robotLocations() ([]shared.MapLocation, error) {
	if m.robotState != nil {
		return m.robotState.RobotLocations(context.Background())
	}
	return nil, errSchedulerStorageUnavailable
}

// demandLocation is where the demand layer pins one robot: the map it must be
// logged in on and the point it starts from there.
type demandLocation struct {
	village int
	area    int
	x       int
	y       int
}

// setDemandLocations replaces the pin table. A uid absent from the table keeps
// whatever location the ordinary spawn rules give it, so an empty table is the
// fleet's unmanaged behaviour rather than "everyone at 0/0".
func (m *RobotManager) setDemandLocations(pins map[int]demandLocation) {
	if m == nil {
		return
	}
	m.demandPinMu.Lock()
	m.demandPins = pins
	m.demandPinMu.Unlock()
}

// demandLocationFor reports where the demand layer pinned one robot.
//
// A pin is what the ordinary spawn path cannot express: the repair sweep is
// free to move a robot off a map the operator asked it to stand on, because its
// only inputs are the map catalog's capacity rules. A pinned robot is the
// operator's word, so the placement layer applies the pin first and returns.
func (m *RobotManager) demandLocationFor(uid int) (demandLocation, bool) {
	if m == nil || uid <= 0 {
		return demandLocation{}, false
	}
	m.demandPinMu.RLock()
	defer m.demandPinMu.RUnlock()
	pin, ok := m.demandPins[uid]
	return pin, ok
}

// setPlayerMap tells the world's crowd policy which map the player is on, so the
// maps far from it stop taking robots and the demand layer knows what "near the
// player" means. A backend whose policy has no notion of the player ignores it.
func (m *RobotManager) setPlayerMap(village, area int) {
	if m == nil {
		return
	}
	if setter, ok := m.crowdPolicy.(playerMapPolicy); ok {
		setter.SetPlayerMap(village, area)
	}
}

// playerMapDistances reports how many transitions every map sits from the
// player's town. A backend without the notion returns nothing, which the caller
// reads as "no map is out of reach".
func (m *RobotManager) playerMapDistances() map[shared.MapAreaKey]int {
	if m == nil {
		return nil
	}
	if provider, ok := m.crowdPolicy.(playerMapDistancePolicy); ok {
		return provider.MapDistances()
	}
	return nil
}

// playerMapPolicy is the part of a crowd policy that tracks the player.
type playerMapPolicy interface {
	SetPlayerMap(village, area int)
}

// playerMapDistancePolicy is the part of a crowd policy that reports the walk
// it computed outward from the player's town.
type playerMapDistancePolicy interface {
	MapDistances() map[shared.MapAreaKey]int
}

// residentMaps lists the maps one resident row may use for a robot of the given
// level: the row's village and area range, on a map that is in use and whose own
// level limit the robot meets.
//
// The crowd rule is deliberately not consulted. A resident row is the operator
// saying how many robots stand here; a policy that closes a map once the player
// walks away would otherwise empty the one crowd the operator asked to keep.
func (m *RobotManager) residentMaps(row robotconfig.ResidentRow, level int) []shared.MapCatalogItem {
	if m == nil {
		return nil
	}
	maps := m.loadMapCatalog()
	out := make([]shared.MapCatalogItem, 0, len(maps))
	for _, mp := range maps {
		if mp.Village != row.Village || mp.Area < row.AreaMin || mp.Area > row.AreaMax {
			continue
		}
		if !mp.Use || mp.Level > level {
			continue
		}
		if len(robotspawn.MapRectangles(mp)) == 0 {
			continue
		}
		out = append(out, mp)
	}
	return out
}

// rememberRobotLocation records an intended location in the robot directory so
// concurrent spawn/repair decisions share the same occupancy view. Movement
// keeps flowing through the position batcher; this is only for planning-time
// placements that would otherwise pile up because nobody sees them yet.
func (m *RobotManager) rememberRobotLocation(uid, village, area, x, y int) {
	if m == nil || m.robotState == nil || uid <= 0 {
		return
	}
	update := robotcap.PositionUpdate{UID: uid, Village: village, Area: area, X: x, Y: y}
	if err := m.robotState.UpdateRobotPositions(context.Background(), []robotcap.PositionUpdate{update}); err != nil {
		robotLogf("SPAWN_LOCATION_WRITE_FAILED uid=%d err=%v\n", uid, err)
	}
}

func (m *RobotManager) positionRepo() robotPositionWriter {
	if m.robotState != nil {
		return m.robotState
	}
	if repository, ok := m.database.(robotPositionWriter); ok {
		return repository
	}
	return missingPositionRepository{}
}

func (m *RobotManager) lockHub() *lockhub.Hub {
	if m.locks == nil {
		m.locks = lockhub.New()
	}
	return m.locks
}

func (m *RobotManager) withCache(reason string, fn func()) {
	_ = m.lockHub().WithResource(lockScopeConfig, lockResourceRobotConfig, reason, func() error {
		fn()
		return nil
	})
}

// BeginBackgroundWork registers API work that must finish before Manager
// resources are closed. The mutex prevents Wait from racing a late Add.
func (m *RobotManager) BeginBackgroundWork() (func(), bool) {
	if m == nil {
		return nil, false
	}
	m.backgroundMu.Lock()
	if m.shuttingDown {
		m.backgroundMu.Unlock()
		return nil, false
	}
	m.backgroundWG.Add(1)
	m.backgroundMu.Unlock()
	var once sync.Once
	return func() { once.Do(m.backgroundWG.Done) }, true
}

func (m *RobotManager) stopAndWaitBackgroundWork() {
	m.backgroundMu.Lock()
	m.shuttingDown = true
	m.backgroundMu.Unlock()
	m.backgroundWG.Wait()
}

type Runtime interface {
	SessionRuntime
	MoveRuntime
	ShoutRuntime
	StatusRuntime
	StoreRuntime
	AreaRuntime
}

type SessionRuntime interface {
	Logout(uid int) error
	Online(users []shared.RuntimeOnlineUser) error
}

type MoveRuntime interface {
	Move(command shared.RuntimeMoveCommand) error
}

type ShoutRuntime interface {
	Shout(command shared.RuntimeShoutCommand) error
}

type StatusRuntime interface {
	RuntimeStatus() []robotcap.RuntimeStatus
	PartyActive(uid int) bool
}

type StoreRuntime interface {
	StartPrivateStore(uid int, title string) bool
	ResetPrivateStore(uid int) bool
	StartDisjointStore(uid int, cost uint32) bool
	SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool
}

type AreaRuntime interface {
	SetArea(uid int, village, area int, x, y int) bool
}

type missingPositionRepository struct{}

func (missingPositionRepository) UpdateRobotPositions(context.Context, []robotcap.PositionUpdate) error {
	return errors.New("scheduler position repository is not configured")
}

type noopRuntime struct{}

func (noopRuntime) Logout(int) error {
	return nil
}

func (noopRuntime) Move(shared.RuntimeMoveCommand) error {
	return nil
}

func (noopRuntime) Shout(shared.RuntimeShoutCommand) error {
	return nil
}

func (noopRuntime) Online([]shared.RuntimeOnlineUser) error {
	return nil
}

func (noopRuntime) RuntimeStatus() []robotcap.RuntimeStatus {
	return nil
}

func (noopRuntime) PartyActive(int) bool { return false }

func (noopRuntime) StartPrivateStore(uid int, title string) bool {
	return false
}

func (noopRuntime) ResetPrivateStore(uid int) bool {
	return false
}

func (noopRuntime) StartDisjointStore(uid int, cost uint32) bool {
	return false
}

func (noopRuntime) SetArea(uid int, village, area int, x, y int) bool {
	return false
}

func (noopRuntime) SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool {
	return false
}

type WorldShout interface {
	SendWorldShout(msg, name string, senderID uint16) error
	SendMonitorAnnouncement(kind, msg, name string, senderID uint16) error
}

type noopWorldShout struct{}

func (noopWorldShout) SendWorldShout(msg, name string, senderID uint16) error {
	return nil
}

func (noopWorldShout) SendMonitorAnnouncement(kind, msg, name string, senderID uint16) error {
	return nil
}

func robotLogf(format string, args ...interface{}) {
	foundationlog.Robotf(format, args...)
}
