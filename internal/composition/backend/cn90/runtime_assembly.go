package cn90

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"robot/internal/capability/catalog"
	capabilitypvf "robot/internal/capability/pvf"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

// RuntimeBundle holds the assembled 90CN runtime components. The composition
// root installs them on the scheduler through the backend-neutral ports.
type RuntimeBundle struct {
	Inventory      StartupInventory
	State          *robotstate.MemoryStore
	Loadouts       *SQLiteLoadoutApplier
	Transport      *ActionTransport
	Creator        RobotCreator
	Cleaner        RobotCleaner
	Purger         SQLiteRobotPurger
	Inspector      SQLitePopulationInspector
	DatabasePath   string
	GameAddress    string
	GamePort       int
	AdminAddress   string
	AdminToken     string
	TownMaps       []shared.MapCatalogItem
	FollowAccounts FollowAccountLocator
	// CrowdRules is the world's one per-map population policy. The scheduler and
	// the creator share this instance, so a player-town update reaches both the
	// placement of running robots and the planning of new ones.
	CrowdRules *CrowdRules
}

// Close releases the adapter-owned resources in shutdown order.
func (b RuntimeBundle) Close() error {
	var err error
	if b.Loadouts != nil {
		err = errors.Join(err, b.Loadouts.Close())
	}
	if b.Transport != nil {
		err = errors.Join(err, b.Transport.CloseAll())
	}
	return err
}

// RuntimeComposeOptions carries the backend-neutral inputs needed to assemble
// the 90CN runtime. GamePort 0 means "resolve the channel port from the DNF90
// runtime instance"; a positive value is an operator override.
type RuntimeComposeOptions struct {
	ServerDirectory string
	DatabasePath    string
	AccountPrefix   string
	ConnectIP       string
	GamePort        int
	Paths           layout.Paths
	Config          robotconfig.RuntimeConfig
	RandIntn        func(int) int
	RandBetween     func(int, int) int
}

// ComposeRuntime builds every 90CN runtime component: PVF catalogs, the
// database path and startup inventory, the persistence applier, the protocol
// transport and the lifecycle ports. Path and protocol rules stay in this
// adapter; the composition root only wires the returned components.
func ComposeRuntime(ctx context.Context, opts RuntimeComposeOptions) (RuntimeBundle, error) {
	bundle := RuntimeBundle{}
	prefix := strings.TrimSpace(opts.AccountPrefix)
	if prefix == "" {
		return bundle, fmt.Errorf("90CN account prefix is required")
	}
	if strings.TrimSpace(opts.ConnectIP) == "" {
		return bundle, fmt.Errorf("90CN game address is incomplete")
	}
	if opts.RandIntn == nil || opts.RandBetween == nil {
		return bundle, fmt.Errorf("90CN runtime requires random sources")
	}

	stageStarted := time.Now()
	logStartupStage := func(stage string) {
		foundationlog.Robotf("CN90_STARTUP_STAGE stage=%s elapsed_ms=%d\n", stage, time.Since(stageStarted).Milliseconds())
		stageStarted = time.Now()
	}

	serverLayout, err := resolveRuntimeLayout(opts.ServerDirectory)
	if err != nil {
		return bundle, fmt.Errorf("90CN runtime: %w", err)
	}
	pvfPath, pvfSource, err := serverLayout.pvfPath()
	if err != nil {
		return bundle, fmt.Errorf("PVF: %w", err)
	}
	foundationlog.Robotf("CN90_PVF_RESOLVED path=%s source=%s\n", pvfPath, pvfSource)
	logStartupStage("pvf_resolve")
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		return bundle, fmt.Errorf("PVF catalog: %w", err)
	}
	logStartupStage("pvf_catalog")
	if len(catalogs.StatFallbackJobs) > 0 {
		foundationlog.Robotf("CN90_STAT_FALLBACK jobs=%v\n", catalogs.StatFallbackJobs)
	}
	if err := ExportItemCatalogs(opts.Paths, catalogs.Equipment, catalogs.Stackable); err != nil {
		return bundle, fmt.Errorf("item catalog: %w", err)
	}
	if err := ExportTownMapCatalog(opts.Paths, catalogs.TownMaps); err != nil {
		return bundle, fmt.Errorf("town map catalog: %w", err)
	}
	logStartupStage("item_catalog_export")
	databasePath, databaseSource, err := resolveDatabasePath(serverLayout, opts.DatabasePath)
	if err != nil {
		return bundle, fmt.Errorf("loadout database: %w", err)
	}
	foundationlog.Robotf("CN90_DATABASE_RESOLVED path=%s source=%s\n", databasePath, databaseSource)
	channels, channelSource, err := resolveChannelCatalog(serverLayout, opts.GamePort)
	if err != nil {
		return bundle, fmt.Errorf("90CN channel catalog: %w", err)
	}
	gamePort := channels.ports[0]
	adminAddress, adminToken, err := serverLayout.adminEndpoint()
	if err != nil {
		return bundle, fmt.Errorf("90CN admin endpoint: %w", err)
	}
	foundationlog.Robotf("CN90_CHANNEL_RESOLVED ports=%v address=%s source=%s\n",
		channels.ports, net.JoinHostPort(opts.ConnectIP, fmt.Sprint(gamePort)), channelSource)
	inventory, err := (SQLiteStartupInventory{
		DatabasePath: databasePath, AccountPrefix: prefix, Config: opts.Config,
	}).ScanAndClean(ctx)
	if err != nil {
		return bundle, fmt.Errorf("startup inventory: %w", err)
	}
	logStartupStage("startup_inventory")
	reconciled, err := ReconcileRobotGrowth(ctx, databasePath, inventory.Robots, opts.Config.GrowTypes, catalogs.JobGrows, catalogs.StatTables, opts.Config.ReconcileAwakening, opts.RandIntn)
	if err != nil {
		return bundle, fmt.Errorf("growth reconcile: %w", err)
	}
	logStartupStage("growth_reconcile")
	if reconciled > 0 {
		foundationlog.Robotf("CN90_GROWTH_RECONCILED count=%d\n", reconciled)
	}
	state := robotstate.NewMemoryStore(inventory.Robots)
	if err := state.RegisterIdentities(ctx, inventory.Identities); err != nil {
		return bundle, fmt.Errorf("startup identities: %w", err)
	}
	logStartupStage("state_identities")
	questGates := mergeEquipmentSlotUnlockQuests(mergeTownNeedQuests(catalogs.QuestGates, catalogs.TownMaps))
	seeded, err := SeedRobotQuestGates(ctx, databasePath, inventory.Robots, questGates)
	if err != nil {
		return bundle, fmt.Errorf("quest gates: %w", err)
	}
	slots, err := GrantEquipmentSlotUnlocks(ctx, databasePath, inventory.Robots)
	if err != nil {
		return bundle, fmt.Errorf("equipment slot unlock: %w", err)
	}
	if slots > 0 {
		foundationlog.Robotf("CN90_EQUIPMENT_SLOTS_UNLOCKED characters=%d\n", slots)
	}
	if seeded > 0 {
		foundationlog.Robotf("CN90_QUEST_GATES_SEEDED completed=%d active=%d robots=%d\n",
			len(questGates.CompletedQuestIDs), len(questGates.ActiveQuestIDs), seeded)
	}
	loadouts, err := NewSQLiteLoadoutApplier(ctx, databasePath, opts.Config, catalogs.Equipment, pvfPath, opts.RandIntn)
	if err != nil {
		return bundle, fmt.Errorf("loadout applier: %w", err)
	}
	logStartupStage("loadout_applier")
	loadouts.LevelThresholds = catalogs.LevelThresholds
	loadouts.QuestGates = questGates
	loadouts.Stackables = catalogs.Stackable
	loadouts.Amplify = catalogs.Amplify
	replaced, err := loadouts.ReconcileRobotLoadouts(ctx, prefix, inventory.Robots)
	if err != nil {
		_ = loadouts.Close()
		return bundle, fmt.Errorf("loadout reconcile: %w", err)
	}
	logStartupStage("loadout_reconcile")
	if replaced > 0 {
		foundationlog.Robotf("CN90_LOADOUT_RECONCILED robots=%d\n", replaced)
	}
	transport, err := NewRuntimeTransport(opts.ConnectIP, channels, NewAccountBinder(adminAddress, adminToken))
	if err != nil {
		_ = loadouts.Close()
		return bundle, fmt.Errorf("transport: %w", err)
	}
	// The auction round recomputes the same stock plan the offline stage wrote
	// into the bag, so it needs the catalog the seeder used.
	transport.stackables = catalogs.Stackable
	logStartupStage("transport")
	address := net.JoinHostPort(opts.ConnectIP, fmt.Sprint(gamePort))
	names := catalog.NameTemplates(opts.Paths.Templates)
	binder := NewAccountBinder(adminAddress, adminToken)

	bundle.Inventory = inventory
	bundle.State = state
	bundle.Loadouts = loadouts
	bundle.Transport = transport
	bundle.DatabasePath = databasePath
	bundle.GameAddress = address
	bundle.GamePort = gamePort
	bundle.AdminAddress = adminAddress
	bundle.AdminToken = adminToken
	bundle.TownMaps = catalogs.TownMaps
	bundle.CrowdRules = NewCrowdRules(catalogs.TownMaps)
	bundle.FollowAccounts = FollowAccountLocator{DatabasePath: databasePath}
	bundle.Creator = RobotCreator{
		Provisioner: Provisioner{ConnectHost: opts.ConnectIP, Channels: channels, Binder: binder},
		BatchStore:  state, IdentityStore: state, RobotCatalog: state,
		Config: opts.Config, Names: names, Maps: catalogs.TownMaps, JobGrows: catalogs.JobGrows,
		StatTables:    catalogs.StatTables,
		CrowdPolicy:   bundle.CrowdRules,
		AccountPrefix: prefix, IDStart: opts.Config.RobotUIDStart,
		RandIntn: opts.RandIntn, RandBetween: opts.RandBetween,
		Loadouts: loadouts, Profiles: loadouts,
	}
	bundle.Cleaner = RobotCleaner{
		Protocol: CharacterDeleter{ConnectHost: opts.ConnectIP, Channels: channels, Binder: binder},
		State:    state, Sessions: transport,
	}
	bundle.Purger = SQLiteRobotPurger{
		DatabasePath: databasePath, AccountPrefix: prefix, State: state, Sessions: transport,
	}
	bundle.Inspector = SQLitePopulationInspector{
		DatabasePath: databasePath, AccountPrefix: prefix, Config: opts.Config,
		Maps: catalogs.TownMaps, EquipmentSets: itemSetKeys(catalogs.Equipment),
		AvatarSets: itemSetKeys(catalogs.Equipment),
	}
	return bundle, nil
}

// itemSetKeys maps equipment item ids to their PVF set keys so the population
// report can tell which robots wear a matched set.
func itemSetKeys(items []shared.EquipmentCatalogItem) map[int]string {
	keys := make(map[int]string, len(items))
	for _, item := range items {
		if item.ID <= 0 {
			continue
		}
		if key := strings.TrimSpace(item.SetKey); key != "" {
			keys[item.ID] = key
		}
	}
	return keys
}

// NewRuntimeTransport dials the 90CN game server for both actions and session
// lifecycle. The same transport implements both scheduler ports. Each account
// is registered for this process id before dialing and is spread across the
// runtime channel ports.
func NewRuntimeTransport(connectHost string, channels channelCatalog, binder *AccountBinder) (*ActionTransport, error) {
	if strings.TrimSpace(connectHost) == "" || len(channels.ports) == 0 {
		return nil, fmt.Errorf("90CN game address is incomplete")
	}
	factory := SessionFactory{ConnectHost: connectHost, Channels: channels, Binder: binder}
	return NewActionTransport(factory), nil
}

// ExportItemCatalogs publishes the projected equipment and stackable catalogs
// for other Robot stages that read them from disk.
func ExportItemCatalogs(paths layout.Paths, equipment, stackable []shared.EquipmentCatalogItem) error {
	if err := capabilitypvf.WriteJSON(paths.PVFEquipment(), equipment); err != nil {
		return fmt.Errorf("write 90CN equipment catalog: %w", err)
	}
	if err := capabilitypvf.WriteJSON(paths.PVFStackable(), stackable); err != nil {
		return fmt.Errorf("write 90CN stackable catalog: %w", err)
	}
	return nil
}

// ExportTownMapCatalog publishes the projected town map catalog. The shared
// store point coordinator reads it from disk and validates its MD5 against the
// generated point cache.
func ExportTownMapCatalog(paths layout.Paths, maps []shared.MapCatalogItem) error {
	if err := capabilitypvf.WriteJSON(paths.PVFMaps(), maps); err != nil {
		return fmt.Errorf("write 90CN map catalog: %w", err)
	}
	return nil
}

// ResolvePVFPath resolves the Script.pvf the DNF90 runtime reads: the instance
// game.pvfPath (relative to the runtime directory), defaulting to the locked
// data/dnf/Script.pvf profile path. The server directory may be the one-click
// project root or the runtime directory itself.
func ResolvePVFPath(serverDirectory string) (string, error) {
	serverLayout, err := resolveRuntimeLayout(serverDirectory)
	if err != nil {
		return "", err
	}
	path, _, err := serverLayout.pvfPath()
	return path, err
}

// ResolveDatabasePath resolves the SQLite database the robot writes: the
// explicit adapter setting wins (absolute, or relative to the runtime
// directory), then the instance database.path (relative to the runtime
// directory), then the locked data/dnf90.db default.
func ResolveDatabasePath(serverDirectory, configured string) (string, error) {
	serverLayout, err := resolveRuntimeLayout(serverDirectory)
	if err != nil {
		return "", err
	}
	path, _, err := resolveDatabasePath(serverLayout, configured)
	return path, err
}

// resolveDatabasePath also reports which rule selected the database file so
// startup can log the exact database source.
func resolveDatabasePath(serverLayout runtimeLayout, configured string) (string, string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		candidate := value
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(serverLayout.root, candidate)
		}
		if !isRegularFile(candidate) {
			return "", "", fmt.Errorf("90CN database %q does not exist", candidate)
		}
		return candidate, "adapter database_path setting", nil
	}
	path, source, err := serverLayout.databasePath()
	if err != nil {
		return "", "", err
	}
	if !isRegularFile(path) {
		return "", "", fmt.Errorf("90CN database %q does not exist; start the DNF90 server once so it creates the SQLite file", path)
	}
	return path, source, nil
}

// resolveChannelCatalog returns the dialable game ports. An explicit operator
// override pins a single port; otherwise the runtime channel_info.etc defines
// the channel list and the instance client.gamePortBase defines the ports
// (gamePortBase + channel id).
func resolveChannelCatalog(serverLayout runtimeLayout, configured int) (channelCatalog, string, error) {
	if configured > 0 {
		if configured > 65535 {
			return channelCatalog{}, "", fmt.Errorf("game port %d is out of range", configured)
		}
		return singlePortCatalog(configured), "adapter game_port setting", nil
	}
	serverIndex := serverLayout.instance.Protocol.ChannelServerID
	if serverIndex <= 0 {
		serverIndex = 1
	}
	portBase := serverLayout.instance.Client.GamePortBase
	if portBase <= 0 {
		portBase = defaultNativeGamePortBase
	}
	channels, err := loadChannelCatalog(serverLayout.root, serverLayout.instance.Game.ChannelInfoPath, serverIndex, portBase)
	if err != nil {
		return channelCatalog{}, "", err
	}
	return channels, "instance game.channelInfoPath", nil
}

func isRegularFile(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}
