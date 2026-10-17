package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/config"
	"robot/internal/shared"
)

// fillBackendSelectionSettings completes a persisted selection from the loaded
// robot configuration. A packaged installation already ships a tuned
// robot_config.ini, so the startup path must be able to derive the backend
// settings from it instead of sending the operator through the recovery page.
// Completing the selection keeps the configuration generation at zero, which
// is what stops PrepareBackendRuntime from moving the shipped
// robot_config.ini aside on the first start.
func fillBackendSelectionSettings(cfg *config.SysConfig, info shared.BackendInfo, selection *shared.BackendSelection) bool {
	if cfg == nil || selection == nil || len(info.Settings) == 0 {
		return false
	}
	filled := false
	for _, field := range info.Settings {
		source := strings.TrimSpace(field.RuntimeSource)
		if source == "" || strings.TrimSpace(selection.Settings[field.Key]) != "" {
			continue
		}
		value := runtimeSourceValue(cfg, source)
		if value == "" {
			value = strings.TrimSpace(field.Default)
		}
		if value == "" {
			continue
		}
		if selection.Settings == nil {
			selection.Settings = make(map[string]string, len(info.Settings))
		}
		selection.Settings[field.Key] = value
		filled = true
	}
	return filled
}

// runtimeSourceValue mirrors the webadmin dialog defaults
// (internal/entry/webadmin/backend_selection.go runtimeSettingDefault) so the
// startup path and the workbench derive identical settings from config.ini.
func runtimeSourceValue(cfg *config.SysConfig, source string) string {
	switch strings.TrimSpace(source) {
	case "server_directory":
		value := strings.TrimSpace(cfg.ServerDirectory)
		if strings.EqualFold(filepath.Ext(value), ".pvf") {
			return ""
		}
		if stat, err := os.Stat(value); err == nil && !stat.IsDir() {
			return filepath.Dir(value)
		}
		return value
	case "game_host":
		return strings.TrimSpace(cfg.RobotConnectIP)
	case "game_port":
		if cfg.RobotGamePort > 0 {
			return strconv.Itoa(cfg.RobotGamePort)
		}
	}
	return ""
}

// saveBackendSelection persists a derived selection so the workbench reads the
// same settings the startup path applied.
func saveBackendSelection(path string, selection *shared.BackendSelection) error {
	if selection.SelectedAt.IsZero() {
		selection.SelectedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(selection, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(data, '\n'), 0600)
}

// applyBackendSelectionSettings maps the adapter-declared settings onto the
// process configuration using each field's runtime source. Adapter key names
// stay in the descriptor; the composition root only knows the runtime sources.
func applyBackendSelectionSettings(cfg *config.SysConfig, info shared.BackendInfo, selection shared.BackendSelection) error {
	if cfg == nil {
		return fmt.Errorf("backend settings require config")
	}
	if len(selection.Settings) == 0 {
		return fmt.Errorf("backend settings are not configured")
	}
	if len(info.Settings) == 0 {
		return fmt.Errorf("backend %s declares no runtime settings", info.ID)
	}
	applied := make(map[string]bool, len(info.Settings))
	for _, field := range info.Settings {
		source := strings.TrimSpace(field.RuntimeSource)
		if source == "" {
			continue
		}
		value := strings.TrimSpace(selection.Settings[field.Key])
		if value == "" {
			value = strings.TrimSpace(field.Default)
		}
		if value == "" {
			if field.Required {
				return fmt.Errorf("%s is required", field.Label)
			}
			continue
		}
		switch source {
		case "server_directory":
			cfg.ServerDirectory = value
		case "game_host":
			cfg.RobotConnectIPSetting = value
			cfg.RobotConnectIP = value
		case "game_port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("%s must be between 1 and 65535", field.Label)
			}
			cfg.RobotGamePort = port
		default:
			continue
		}
		applied[source] = true
	}
	// The adapter descriptor declares which runtime sources it needs; only
	// fields marked required must have been applied.
	for _, field := range info.Settings {
		source := strings.TrimSpace(field.RuntimeSource)
		if source == "" || !field.Required {
			continue
		}
		if !applied[source] {
			return fmt.Errorf("backend %s runtime settings are incomplete", info.ID)
		}
	}
	return nil
}
