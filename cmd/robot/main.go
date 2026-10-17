package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	runtimeinit "robot/internal/bootstrap/runtime"
	"robot/internal/capability/robotconfig"
	backendregistry "robot/internal/composition/backend"
	"robot/internal/entry/webadmin"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	robotlog "robot/internal/foundation/robotlog"
	"robot/internal/shared"
)

// version is injected at build time with -ldflags "-X main.version=<value>".
var version = "dev"

func main() {
	if handled := handlePasswordHashCLI(); handled {
		return
	}
	code := 0
	for {
		code = runMain()
		if code != 2 {
			break
		}
	}
	if code != 0 {
		waitForFatalExit()
	}
	os.Exit(code)
}

// handlePasswordHashCLI implements `robot -hash-password <value>`, which prints
// a WebPasswordHash value for config.ini and exits.
func handlePasswordHashCLI() bool {
	args := os.Args[1:]
	for index, arg := range args {
		value := ""
		switch {
		case arg == "-hash-password" || arg == "--hash-password":
			if index+1 < len(args) {
				value = args[index+1]
			}
		case strings.HasPrefix(arg, "-hash-password=") || strings.HasPrefix(arg, "--hash-password="):
			value = arg[strings.IndexByte(arg, '=')+1:]
		default:
			continue
		}
		if strings.TrimSpace(value) == "" {
			fmt.Fprintln(os.Stderr, "usage: robot -hash-password <password>")
			os.Exit(1)
		}
		hash, err := config.HashWebPassword(value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash password error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(hash)
		os.Exit(0)
	}
	return false
}

func runMain() int {
	if err := ensureWindowsRuntime(); err != nil {
		fmt.Fprintf(os.Stderr, "startup blocked: %v\n", err)
		return 1
	}
	robotlog.PrintfGreen("robot starting...\n")

	configPath, configDir, err := runtimeConfigPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve config path error: %v\n", err)
		return 1
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config error: %v\n", err)
		return 1
	}
	cfg.ConfigDir = configDir
	paths := layout.New(configDir)
	if err := paths.Ensure(); err != nil {
		fmt.Fprintf(os.Stderr, "create config dir error: %v\n", err)
		return 1
	}
	backendSelection, err := loadBackendSelection(paths.BackendSelection())
	if err != nil {
		return runRecoveryWeb(cfg, shared.BackendID(""), fmt.Sprintf("load backend selection: %v", err))
	}
	backendInfo, err := backendregistry.Select(backendSelection.BackendID, runtime.GOOS)
	if err != nil {
		return runRecoveryWeb(cfg, backendSelection.BackendID, fmt.Sprintf("backend selection: %v", err))
	}
	if fillBackendSelectionSettings(cfg, backendInfo, &backendSelection) {
		if err := saveBackendSelection(paths.BackendSelection(), &backendSelection); err != nil {
			return runRecoveryWeb(cfg, backendSelection.BackendID, fmt.Sprintf("save backend settings: %v", err))
		}
	}
	if err := applyBackendSelectionSettings(cfg, backendInfo, backendSelection); err != nil {
		return runRecoveryWeb(cfg, backendSelection.BackendID, fmt.Sprintf("backend settings: %v", err))
	}
	backendReinitialized, err := runtimeinit.PrepareBackendRuntime(paths, backendSelection)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend runtime preparation error: %v\n", err)
		return 1
	}
	robotlog.ConfigureLogRotation(cfg.LogMaxSizeMB, cfg.LogMaxBackups)
	if err := robotlog.LogInit(paths.RobotLog()); err != nil {
		fmt.Fprintf(os.Stderr, "init log error: %v\n", err)
		return 1
	}
	foundationlog.SetRobotSink(func(msg string) {
		robotlog.LogString(msg)
	})
	defer func() {
		foundationlog.SetRobotSink(nil)
		robotlog.LogClose()
	}()
	robotlog.LogString(fmt.Sprintf("ROBOT_CONFIG path=%s config_dir=%s\n", configPath, cfg.ConfigDir))
	robotlog.LogString(fmt.Sprintf("ROBOT_VERSION version=%s go=%s os=%s arch=%s\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH))
	robotlog.LogString(fmt.Sprintf("BACKEND_SELECTED id=%s generation=%d selected_at=%s capabilities=%d\n", backendInfo.ID, backendSelection.ConfigGeneration, backendSelection.SelectedAt.UTC().Format(time.RFC3339), len(backendInfo.Capabilities)))
	if backendReinitialized {
		robotlog.LogString(fmt.Sprintf("BACKEND_RUNTIME_REINITIALIZED id=%s generation=%d\n", backendInfo.ID, backendSelection.ConfigGeneration))
	}
	for {
		code := runBackend(cfg, paths, backendInfo, backendSelection)
		if code != 2 {
			return code
		}
		cfg, err = config.LoadConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reload config error: %v\n", err)
			return 1
		}
		cfg.ConfigDir = configDir
		backendSelection, err = loadBackendSelection(paths.BackendSelection())
		if err != nil {
			return runRecoveryWeb(cfg, shared.BackendID(""), fmt.Sprintf("reload backend selection: %v", err))
		}
		backendInfo, err = backendregistry.Select(backendSelection.BackendID, runtime.GOOS)
		if err != nil {
			return runRecoveryWeb(cfg, backendSelection.BackendID, fmt.Sprintf("reload backend selection: %v", err))
		}
		if fillBackendSelectionSettings(cfg, backendInfo, &backendSelection) {
			if err := saveBackendSelection(paths.BackendSelection(), &backendSelection); err != nil {
				return runRecoveryWeb(cfg, backendSelection.BackendID, fmt.Sprintf("reload backend settings: %v", err))
			}
		}
		if err := applyBackendSelectionSettings(cfg, backendInfo, backendSelection); err != nil {
			return runRecoveryWeb(cfg, backendSelection.BackendID, fmt.Sprintf("reload backend settings: %v", err))
		}
		backendReinitialized, err = runtimeinit.PrepareBackendRuntime(paths, backendSelection)
		if err != nil {
			fmt.Fprintf(os.Stderr, "backend runtime preparation error: %v\n", err)
			return 1
		}
		robotlog.LogString(fmt.Sprintf("BACKEND_SELECTED id=%s generation=%d selected_at=%s capabilities=%d\n", backendInfo.ID, backendSelection.ConfigGeneration, backendSelection.SelectedAt.UTC().Format(time.RFC3339), len(backendInfo.Capabilities)))
		if backendReinitialized {
			robotlog.LogString(fmt.Sprintf("BACKEND_RUNTIME_REINITIALIZED id=%s generation=%d\n", backendInfo.ID, backendSelection.ConfigGeneration))
		}
	}
}

// ensureWindowsRuntime is intentionally kept at the process entry boundary so
// the temporary distribution restriction can be removed without touching the
// adapter, scheduler, or protocol layers.
func ensureWindowsRuntime() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("this distribution can only run on Windows")
	}
	return nil
}

func loadBackendSelection(path string) (shared.BackendSelection, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return shared.BackendSelection{BackendID: backendregistry.DefaultID()}, nil
	}
	if err != nil {
		return shared.BackendSelection{}, err
	}
	return shared.DecodeBackendSelection(data)
}

func runtimeConfigPaths() (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	paths, err := layout.FromExecutable(exe)
	if err != nil {
		return "", "", err
	}
	return paths.MainConfig(), paths.Root, nil
}

func loadRequiredRobotConfig(path string) (robotconfig.RuntimeConfig, error) {
	rc, err := robotconfig.LoadFile(path)
	if err != nil {
		return robotconfig.RuntimeConfig{}, fmt.Errorf("load %s: %w", path, err)
	}
	return rc, nil
}

func recoveryWebURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
}

func runRecoveryWeb(cfg *config.SysConfig, selected shared.BackendID, reason string) int {
	if cfg == nil {
		fmt.Fprintln(os.Stderr, "recovery web requires config")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := webadmin.NewRecoveryWithCatalog(
		cfg, "", fmt.Sprintf("0.0.0.0:%d", cfg.WebPort), selected, backendregistry.Available(), reason,
	)
	lifecycle := make(chan webadmin.LifecycleAction, 1)
	server.SetLifecycleHandler(func(action webadmin.LifecycleAction) {
		select {
		case lifecycle <- action:
		default:
		}
	})
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	fmt.Printf("Robot setup is available at %s\n", recoveryWebURL(cfg.WebPort))
	select {
	case <-ctx.Done():
		return 0
	case action := <-lifecycle:
		if action == webadmin.LifecycleReinitialize {
			return 2
		}
		return 0
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "recovery web error: %v\n", err)
			return 1
		}
		return 0
	}
}
