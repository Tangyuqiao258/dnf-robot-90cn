package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	runtimeinit "robot/internal/bootstrap/runtime"
	"robot/internal/capability/robotconfig"
	backendregistry "robot/internal/composition/backend"
	cn90backend "robot/internal/composition/backend/cn90"
	"robot/internal/entry/tcpapi"
	"robot/internal/entry/webadmin"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/foundation/network"
	"robot/internal/foundation/process"
	"robot/internal/scheduler"
	"robot/internal/shared"
)

// runBackend owns the selected adapter's complete initialization, dispatch,
// and shutdown lifecycle. Shared scheduling remains below the composition
// robotAccountPrefix is the account-name prefix owned by Robot identities.
const robotAccountPrefix = "robot"

// boundary; version-specific work stays in the selected adapter.
func runBackend(cfg *config.SysConfig, paths layout.Paths, info shared.BackendInfo, selection shared.BackendSelection) int {
	if err := runtimeinit.InitConfigForBackend(cfg, info); err != nil {
		foundationlog.Robotf("ADAPTER_RUNTIME_INIT_FAILED backend=%s err=%v\n", info.ID, err)
		return 1
	}
	rc, err := loadRequiredRobotConfig(paths.RobotConfig())
	if err != nil {
		foundationlog.Robotf("ADAPTER_RUNTIME_CONFIG_FAILED err=%v\n", err)
		return 1
	}
	if err := ensureAdapterOpenFileLimit(rc); err != nil {
		foundationlog.Robotf("ADAPTER_OPEN_FILE_CAPACITY_FAILED err=%v\n", err)
		return 1
	}
	manager := scheduler.NewRobotManager(nil, cfg, nil)
	// The adapter owns PVF, database, protocol and path rules; the composition
	// root only wires the returned components onto the scheduler ports.
	bundle, err := cn90backend.ComposeRuntime(context.Background(), cn90backend.RuntimeComposeOptions{
		ServerDirectory: cfg.ServerDirectory,
		DatabasePath:    backendSetting(selection, "database_path"),
		AccountPrefix:   robotAccountPrefix,
		ConnectIP:       cfg.RobotConnectIP,
		GamePort:        cfg.RobotGamePort,
		Paths:           paths,
		Config:          rc,
		RandIntn:        manager.RandIntn,
		RandBetween:     manager.RandBetween,
	})
	if err != nil {
		foundationlog.Robotf("ADAPTER_RUNTIME_FAILED err=%v\n", err)
		return 1
	}
	// The adapter resolves the channel port from the DNF90 runtime instance
	// when no explicit override is configured. Publish the effective port so
	// shared diagnostics and the game-port probe follow the same endpoint.
	if bundle.GamePort > 0 {
		cfg.RobotGamePort = bundle.GamePort
	}
	defer func() {
		if err := bundle.Close(); err != nil {
			foundationlog.Robotf("ADAPTER_RUNTIME_CLOSE_FAILED err=%v\n", err)
		}
	}()
	foundationlog.Robotf("ADAPTER_STARTUP_INVENTORY scanned=%d adopted=%d deleted_accounts=%d deleted_characters=%d swept_characters=%d\n",
		bundle.Inventory.ScannedAccounts, len(bundle.Inventory.Robots), bundle.Inventory.DeletedAccounts,
		bundle.Inventory.DeletedCharacters, bundle.Inventory.SweptCharacters)
	// Do not mark the generation as applied until adapter-specific
	// initialization (including transport composition and PVF projection) has
	// succeeded. A failed startup must retry the reinitialization next time.
	if err := runtimeinit.MarkBackendRuntimeApplied(paths, selection); err != nil {
		foundationlog.Robotf("ADAPTER_RUNTIME_MARK_FAILED err=%v\n", err)
		return 1
	}
	manager.ConfigureBackendRuntime(info, cn90backend.NewPersistenceInspector(bundle.DatabasePath), nil)
	manager.SetBackendStorePolicy(cn90backend.StorePolicy{})
	// The crowd rules come from the same projected town catalog the adapter hands
	// the scheduler, so placement, relocation and stall selection all read one
	// policy instead of three drifting copies of the map list.
	manager.SetCrowdMapPolicy(bundle.CrowdRules)
	manager.SetBackendStoreRuntime(bundle.Transport)
	manager.SetBackendExpertJobProfessionWriter(bundle.Loadouts)
	manager.SetBackendAccountOnlineChecker(bundle.Transport)
	manager.SetBackendFollowAccountLocator(bundle.FollowAccounts)
	// The same adapter knows where the player is standing, which is what the
	// demand layer's floating robots follow.
	manager.SetBackendPlayerLocator(bundle.FollowAccounts)
	manager.SetBackendRobotCreator(info, bundle.Creator)
	manager.SetRobotStateDirectory(bundle.State)
	manager.SetBackendActionTransport(bundle.Transport)
	manager.SetBackendSessionTransport(bundle.Transport)
	manager.SetTownMapCatalog(bundle.TownMaps)
	manager.SetBackendRobotCleaner(bundle.Cleaner)
	manager.SetBackendRobotPurger(bundle.Purger)
	manager.SetBackendPopulationInspector(bundle.Inspector)
	// The DNF90 game listener allocates a session per accepted connection, so
	// the periodic AutoGate readiness check uses the admin health route
	// instead of dialing the game port.
	manager.SetBackendPortProbe(cn90backend.NewBackendHealthProbe(bundle.AdminAddress))
	defer func() {
		if err := manager.Shutdown(); err != nil {
			foundationlog.Robotf("ADAPTER_MANAGER_SHUTDOWN_FAILED err=%v\n", err)
		}
	}()

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.RobotPort)
	tcpServer := network.NewTCPServer(addr)
	tcpServer.SetLimits(256, 90*time.Second, 15*time.Second)
	tcpServer.OnMessage(func(clientID string, raw []byte) {
		response := tcpapi.HandlePacket(clientID, string(raw), manager)
		if response != "" {
			if err := tcpServer.SendTo(clientID, []byte(response)); err != nil {
				foundationlog.Robotf("ADAPTER_TCP_RESPONSE_FAILED client=%s err=%v\n", clientID, err)
			}
		}
	})
	if err := tcpServer.Start(); err != nil {
		foundationlog.Robotf("ADAPTER_TCP_START_FAILED addr=%s err=%v\n", addr, err)
		return 1
	}
	defer tcpServer.Close()
	webCtx, webCancel := context.WithCancel(context.Background())
	webServer := webadmin.NewWithCatalog(cfg, fmt.Sprintf("127.0.0.1:%d", cfg.RobotPort), fmt.Sprintf("0.0.0.0:%d", cfg.WebPort), info.ID, backendregistry.Available())
	lifecycle := make(chan webadmin.LifecycleAction, 1)
	webServer.SetLifecycleHandler(func(action webadmin.LifecycleAction) {
		select {
		case lifecycle <- action:
		default:
		}
	})
	webDone := make(chan error, 1)
	go func() { webDone <- webServer.Serve(webCtx) }()
	defer func() {
		webCancel()
		select {
		case err := <-webDone:
			if err != nil {
				foundationlog.Robotf("WEB_SERVER_STOP_FAILED err=%v\n", err)
			}
		case <-time.After(6 * time.Second):
			foundationlog.Robotf("WEB_SERVER_STOP_TIMEOUT\n")
		}
	}()
	manager.StartAutoActions()
	foundationlog.Robotf("ROBOT_STARTED backend=%s tcp=%s\n", info.ID, addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	select {
	case action := <-lifecycle:
		foundationlog.Robotf("ROBOT_CYCLE_END backend=%s action=%s\n", info.ID, action)
		if action == webadmin.LifecycleReinitialize {
			return 2
		}
		return 0
	case <-sigCh:
		foundationlog.Robotf("ROBOT_STOPPING backend=%s\n", info.ID)
		return 0
	}
}

func backendSetting(selection shared.BackendSelection, key string) string {
	return selection.Settings[key]
}

func ensureAdapterOpenFileLimit(rc robotconfig.RuntimeConfig) error {
	if rc.MaxOnlineRobots < 1 {
		return fmt.Errorf("max_online_robots must be positive")
	}
	// Linux raises the descriptor soft limit here; other platforms only
	// validate the configured capacity.
	if err := process.EnsureOpenFileLimit(rc.MaxOnlineRobots, 64); err != nil {
		return err
	}
	return nil
}
