package main

import (
	"os"
	"path/filepath"
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func testBackendInfo() shared.BackendInfo {
	return shared.BackendInfo{
		ID: "test",
		Settings: []shared.BackendSetting{
			{Key: "server_directory", Label: "Server directory", RuntimeSource: "server_directory", Required: true},
			{Key: "server_host", Label: "Host", RuntimeSource: "game_host", Required: true, Default: "127.0.0.1"},
			{Key: "game_port", Label: "Port", RuntimeSource: "game_port", Required: true, Default: "10011"},
			{Key: "database_path", Label: "Database"},
		},
	}
}

func TestApplyBackendSelectionSettings(t *testing.T) {
	cfg := &config.SysConfig{ServerDirectory: "old", RobotConnectIP: "old", RobotGamePort: 10010}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "192.0.2.21",
		"game_port":        "10011",
		"database_path":    `D:\game\DfoServer\Data\inventory.db`,
	}}
	if err := applyBackendSelectionSettings(cfg, testBackendInfo(), selection); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerDirectory != `D:\game\DfoServer` || cfg.RobotConnectIP != "192.0.2.21" || cfg.RobotConnectIPSetting != "192.0.2.21" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestApplyBackendSelectionSettingsDerivesOptionalDatabase(t *testing.T) {
	cfg := &config.SysConfig{}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "127.0.0.1",
		"game_port":        "10011",
	}}
	if err := applyBackendSelectionSettings(cfg, testBackendInfo(), selection); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerDirectory != `D:\game\DfoServer` || cfg.RobotConnectIP != "127.0.0.1" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestFillBackendSelectionSettingsFromConfig(t *testing.T) {
	cfg := &config.SysConfig{ServerDirectory: `..\Server-90`, RobotConnectIP: "127.0.0.1", RobotGamePort: 11011}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test")}
	if !fillBackendSelectionSettings(cfg, testBackendInfo(), &selection) {
		t.Fatal("empty selection was not filled")
	}
	if selection.Settings["server_directory"] != `..\Server-90` || selection.Settings["server_host"] != "127.0.0.1" || selection.Settings["game_port"] != "11011" {
		t.Fatalf("settings=%v", selection.Settings)
	}
	if selection.ConfigGeneration != 0 {
		t.Fatalf("generation=%d, want 0", selection.ConfigGeneration)
	}
	// The derived selection must satisfy the startup path without the
	// recovery page even though the config carries no explicit settings.
	if err := applyBackendSelectionSettings(cfg, testBackendInfo(), selection); err != nil {
		t.Fatal(err)
	}
}

func TestFillBackendSelectionSettingsKeepsExplicitValues(t *testing.T) {
	cfg := &config.SysConfig{ServerDirectory: `..\Server-90`, RobotConnectIP: "127.0.0.1", RobotGamePort: 11011}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `D:\custom\DfoServer`,
	}}
	if !fillBackendSelectionSettings(cfg, testBackendInfo(), &selection) {
		t.Fatal("missing fields were not filled")
	}
	if selection.Settings["server_directory"] != `D:\custom\DfoServer` {
		t.Fatalf("explicit value overwritten: %v", selection.Settings)
	}
	if selection.Settings["server_host"] != "127.0.0.1" || selection.Settings["game_port"] != "11011" {
		t.Fatalf("missing fields not derived: %v", selection.Settings)
	}
}

func TestFillBackendSelectionSettingsUsesFieldDefaults(t *testing.T) {
	selection := shared.BackendSelection{BackendID: shared.BackendID("test")}
	if !fillBackendSelectionSettings(&config.SysConfig{}, testBackendInfo(), &selection) {
		t.Fatal("defaults were not filled")
	}
	if _, ok := selection.Settings["server_directory"]; ok {
		t.Fatalf("required server directory must not be invented: %v", selection.Settings)
	}
	if selection.Settings["server_host"] != "127.0.0.1" || selection.Settings["game_port"] != "10011" {
		t.Fatalf("settings=%v", selection.Settings)
	}
}

func TestFillBackendSelectionSettingsRejectsPvf(t *testing.T) {
	cfg := &config.SysConfig{ServerDirectory: `D:\game\Script.pvf`, RobotConnectIP: "127.0.0.1"}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test")}
	fillBackendSelectionSettings(cfg, testBackendInfo(), &selection)
	if _, ok := selection.Settings["server_directory"]; ok {
		t.Fatalf("a .pvf file identified the server directory: %v", selection.Settings)
	}
}

func TestSaveBackendSelectionDerivesTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "backend_selection.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `..\Server-90`,
	}}
	if err := saveBackendSelection(path, &selection); err != nil {
		t.Fatal(err)
	}
	if selection.SelectedAt.IsZero() {
		t.Fatal("selected_at was not derived")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := shared.DecodeBackendSelection(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.BackendID != selection.BackendID || decoded.Settings["server_directory"] != `..\Server-90` || decoded.SelectedAt.IsZero() {
		t.Fatalf("decoded=%+v", decoded)
	}
}

func TestApplyBackendSelectionSettingsRequiresInitialization(t *testing.T) {
	if err := applyBackendSelectionSettings(&config.SysConfig{}, testBackendInfo(), shared.BackendSelection{}); err == nil {
		t.Fatal("missing backend settings unexpectedly accepted")
	}
	if err := applyBackendSelectionSettings(&config.SysConfig{}, shared.BackendInfo{ID: "empty"}, shared.BackendSelection{
		Settings: map[string]string{"server_directory": "x"},
	}); err == nil {
		t.Fatal("backend without declared settings unexpectedly accepted")
	}
}
