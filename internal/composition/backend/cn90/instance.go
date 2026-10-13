package cn90

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// The DNF90 one-click project keeps one authoritative instance description at
// runtime/config/instance.json. The server process, the launcher and the game
// channel all derive their endpoints and asset paths from it, so the robot
// follows the same file instead of duplicating ports and paths in its own
// settings.
const (
	instanceRelativePath      = "config/instance.json"
	projectRuntimeDirName     = "runtime"
	projectGoModuleRelPath    = "go-server/go.mod"
	projectInstanceExampleRel = "deploy/templates/instance.example.json"
	defaultPVFRelativePath    = "data/dnf/Script.pvf"
	defaultDatabaseRelPath    = "data/dnf90.db"
)

// serverInstance is the subset of runtime/config/instance.json the robot needs.
type serverInstance struct {
	SchemaVersion  int    `json:"schemaVersion"`
	InstallationID string `json:"installationId"`
	Mode           string `json:"mode"`
	Server         struct {
		AdvertiseIP   string `json:"advertiseIp"`
		ChannelListen string `json:"channelListen"`
		AdminListen   string `json:"adminListen"`
		AdminToken    string `json:"adminToken"`
		AccountID     string `json:"accountId"`
	} `json:"server"`
	Database struct {
		Mode string `json:"mode"`
		Path string `json:"path"`
	} `json:"database"`
	Game struct {
		ShardID         string `json:"shardId"`
		PVFPath         string `json:"pvfPath"`
		PVFMaxBytes     int64  `json:"pvfMaxBytes"`
		ChannelInfoPath string `json:"channelInfoPath"`
	} `json:"game"`
	Protocol struct {
		Profile         string `json:"profile"`
		GameOuterToken  string `json:"gameOuterToken"`
		ChannelServerID int    `json:"channelServerIndex"`
	} `json:"protocol"`
	Client struct {
		Directory       string `json:"directory"`
		GamePortBase    int    `json:"gamePortBase"`
		InitialGamePort int    `json:"initialGamePort"`
	} `json:"client"`
}

// runtimeLayout is the resolved runtime root plus the parsed instance.
type runtimeLayout struct {
	root     string
	instance serverInstance
}

// resolveRuntimeLayout maps the configured server directory onto the DNF90
// runtime root and loads runtime/config/instance.json. The directory may be
// the one-click project root (containing runtime/) or the runtime directory
// itself.
func resolveRuntimeLayout(serverDirectory string) (runtimeLayout, error) {
	root, err := resolveRuntimeRoot(serverDirectory)
	if err != nil {
		return runtimeLayout{}, err
	}
	instancePath := filepath.Join(root, filepath.FromSlash(instanceRelativePath))
	data, err := os.ReadFile(instancePath)
	if err != nil {
		return runtimeLayout{}, fmt.Errorf("read %s: %w", instancePath, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	// The DNF90 server may add instance fields between releases; only the
	// subset the robot reads is decoded and unknown members are tolerated.
	var instance serverInstance
	if err := decoder.Decode(&instance); err != nil {
		return runtimeLayout{}, fmt.Errorf("parse %s: %w", instancePath, err)
	}
	if instance.SchemaVersion != 1 {
		return runtimeLayout{}, fmt.Errorf("%s: schemaVersion must be 1, got %d", instancePath, instance.SchemaVersion)
	}
	if mode := strings.TrimSpace(instance.Mode); mode != "local-single-account" {
		return runtimeLayout{}, fmt.Errorf("%s: mode %q is not supported by this robot build", instancePath, mode)
	}
	return runtimeLayout{root: root, instance: instance}, nil
}

// resolveRuntimeRoot accepts either the one-click project root or the runtime
// directory. A project root is recognized by go-server/go.mod plus the
// instance template; a runtime directory carries config/instance.json.
func resolveRuntimeRoot(serverDirectory string) (string, error) {
	value := strings.TrimSpace(serverDirectory)
	if value == "" {
		return "", fmt.Errorf("90CN server directory is empty")
	}
	if strings.EqualFold(filepath.Ext(value), ".pvf") {
		return "", fmt.Errorf("90CN server directory %q is a .pvf file; configure the DNF90 project or runtime directory", value)
	}
	base, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve 90CN server directory %q: %w", value, err)
	}
	if stat, statErr := os.Stat(base); statErr == nil && !stat.IsDir() {
		base = filepath.Dir(base)
	}
	candidates := []string{
		filepath.Join(base, projectRuntimeDirName),
		base,
	}
	for _, candidate := range candidates {
		if hasInstanceFile(candidate) || isProjectRoot(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf(
		"90CN runtime not found under %q; expected %s or %s",
		base,
		filepath.Join(base, projectRuntimeDirName, filepath.FromSlash(instanceRelativePath)),
		filepath.Join(base, filepath.FromSlash(instanceRelativePath)),
	)
}

func hasInstanceFile(dir string) bool {
	return isRegularFile(filepath.Join(dir, filepath.FromSlash(instanceRelativePath)))
}

func isProjectRoot(dir string) bool {
	return isRegularFile(filepath.Join(dir, filepath.FromSlash(projectGoModuleRelPath))) &&
		isRegularFile(filepath.Join(dir, filepath.FromSlash(projectInstanceExampleRel)))
}

// pvfPath resolves the PVF archive: the instance game.pvfPath, or the locked
// 90CN default when the field is empty.
func (l runtimeLayout) pvfPath() (string, string, error) {
	relative := strings.TrimSpace(l.instance.Game.PVFPath)
	source := "instance game.pvfPath"
	if relative == "" {
		relative = defaultPVFRelativePath
		source = "default data/dnf/Script.pvf"
	}
	if filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("instance game.pvfPath %q must be relative to the runtime directory", relative)
	}
	candidate := filepath.Join(l.root, filepath.FromSlash(relative))
	if !isRegularFile(candidate) {
		return "", "", fmt.Errorf("90CN PVF not found at %s", candidate)
	}
	return candidate, source, nil
}

// databasePath resolves the SQLite database: the instance database.path, or
// the locked 90CN default when the field is empty.
func (l runtimeLayout) databasePath() (string, string, error) {
	relative := strings.TrimSpace(l.instance.Database.Path)
	source := "instance database.path"
	if relative == "" {
		relative = defaultDatabaseRelPath
		source = "default data/dnf90.db"
	}
	if filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("instance database.path %q must be relative to the runtime directory", relative)
	}
	return filepath.Join(l.root, filepath.FromSlash(relative)), source, nil
}

// channelInfoPath resolves the runtime channel_info.etc the game ports are
// derived from. The channel directory port (server.channelListen) is a
// different protocol and is not a valid game endpoint.
func (l runtimeLayout) channelInfoPath() (string, string) {
	relative := strings.TrimSpace(l.instance.Game.ChannelInfoPath)
	source := "instance game.channelInfoPath"
	if relative == "" {
		relative = defaultChannelFile
		source = "default data/dnf/channel_info.etc"
	}
	return filepath.Join(l.root, filepath.FromSlash(relative)), source
}

// adminEndpoint resolves the loopback admin API address and token that the
// launcher uses to bind a client process to an account.
func (l runtimeLayout) adminEndpoint() (string, string, error) {
	listen := strings.TrimSpace(l.instance.Server.AdminListen)
	if listen == "" {
		return "", "", fmt.Errorf("instance server.adminListen is empty")
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return "", "", fmt.Errorf("instance server.adminListen %q: %w", listen, err)
	}
	token := strings.TrimSpace(l.instance.Server.AdminToken)
	if token == "" {
		return "", "", fmt.Errorf("instance server.adminToken is empty")
	}
	return listen, token, nil
}
