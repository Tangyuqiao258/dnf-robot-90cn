package cn90

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/layout"
)

// writeTestRuntime builds a minimal DNF90 one-click layout: a project root
// carrying runtime/config/instance.json plus the PVF and SQLite files the
// instance points at. Unknown instance fields are included on purpose so the
// reader stays tolerant of newer server releases.
func writeTestRuntime(t *testing.T, channelListen string) (projectRoot string, instancePath string) {
	t.Helper()
	projectRoot = t.TempDir()
	runtimeRoot := filepath.Join(projectRoot, projectRuntimeDirName)
	configDir := filepath.Join(runtimeRoot, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	instance := `{
  "schemaVersion": 1,
  "installationId": "inst_test",
  "mode": "local-single-account",
  "server": {
    "advertiseIp": "127.0.0.1",
    "channelListen": "` + channelListen + `",
    "adminListen": "127.0.0.1:18111",
    "adminToken": "adm_test",
    "accountId": "dnf:test",
    "packetLog": false,
    "partyUdpRelayPortStart": 30000,
    "partyUdpRelayPortCount": 64
  },
  "database": {"mode": "sqlite", "path": "data/dnf90.db"},
  "game": {
    "shardId": "9999",
    "pvfPath": "data/dnf/Script.pvf",
    "pvfMaxBytes": 536870912,
    "channelInfoPath": "data/dnf/channel_info.etc"
  },
  "protocol": {
    "profile": "90cn-decode-bypass-v1",
    "gameUpperHeader": "server16",
    "gameUpperBodyCodec": "plaintext",
    "gameUpperClientBodyCodec": "plaintext",
    "gameOuterToken": "de509f65e9ccaae621cb7278fc2b8e6c",
    "channelServerIndex": 1,
    "channelAdvertiseServerIndex": 0
  },
  "build": {"goExecutable": "go"},
  "client": {"directory": "", "gamePortBase": 11000, "initialGamePort": 11011, "hookCreate": true}
}`
	instancePath = filepath.Join(configDir, "instance.json")
	if err := os.WriteFile(instancePath, []byte(instance), 0644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runtimeRoot, "data", "dnf", "Script.pvf"))
	writeTestFile(t, filepath.Join(runtimeRoot, "data", "dnf90.db"))
	return projectRoot, instancePath
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRuntimeLayoutAcceptsProjectRootAndRuntimeDir(t *testing.T) {
	projectRoot, instancePath := writeTestRuntime(t, "127.0.0.1:7001")
	runtimeRoot := filepath.Join(projectRoot, projectRuntimeDirName)

	for _, input := range []string{projectRoot, runtimeRoot} {
		serverLayout, err := resolveRuntimeLayout(input)
		if err != nil {
			t.Fatalf("resolve %q: %v", input, err)
		}
		if serverLayout.root != runtimeRoot {
			t.Fatalf("runtime root = %q, want %q", serverLayout.root, runtimeRoot)
		}
		if serverLayout.instance.InstallationID != "inst_test" {
			t.Fatalf("installation = %q", serverLayout.instance.InstallationID)
		}
	}
	if _, err := resolveRuntimeLayout(""); err == nil {
		t.Fatal("empty server directory was accepted")
	}
	if _, err := resolveRuntimeLayout(t.TempDir()); err == nil {
		t.Fatal("directory without a DNF90 runtime was accepted")
	}
	if _, err := resolveRuntimeLayout(instancePath); err == nil {
		t.Fatal("an instance.json file was accepted as the server directory")
	}
	if _, err := resolveRuntimeLayout(filepath.Join(runtimeRoot, "data", "dnf", "Script.pvf")); err == nil {
		t.Fatal("a .pvf file was accepted as the server directory")
	}
}

func TestResolvePVFPathFollowsInstance(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	want := filepath.Join(projectRoot, projectRuntimeDirName, "data", "dnf", "Script.pvf")
	if got, err := ResolvePVFPath(projectRoot); err != nil || got != want {
		t.Fatalf("PVF path = %q, %v", got, err)
	}
	if _, err := ResolvePVFPath(filepath.Join(projectRoot, "go-server")); err == nil {
		t.Fatal("a directory without a runtime was accepted")
	}
}

func TestResolvePVFPathRejectsMissingArchive(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	if err := os.Remove(filepath.Join(projectRoot, projectRuntimeDirName, "data", "dnf", "Script.pvf")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolvePVFPath(projectRoot); err == nil {
		t.Fatal("missing Script.pvf was accepted")
	}
}

func TestResolveDatabasePathFollowsInstanceAndOverrides(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	runtimeRoot := filepath.Join(projectRoot, projectRuntimeDirName)
	want := filepath.Join(runtimeRoot, "data", "dnf90.db")

	if got, err := ResolveDatabasePath(projectRoot, ""); err != nil || got != want {
		t.Fatalf("instance database = %q, %v", got, err)
	}
	serverLayout, err := resolveRuntimeLayout(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if path, source, err := resolveDatabasePath(serverLayout, ""); err != nil || path != want || source != "instance database.path" {
		t.Fatalf("instance database path=%q source=%q err=%v", path, source, err)
	}

	explicit := filepath.Join(t.TempDir(), "custom.db")
	writeTestFile(t, explicit)
	if got, err := ResolveDatabasePath(projectRoot, explicit); err != nil || got != explicit {
		t.Fatalf("explicit database = %q, %v", got, err)
	}
	if path, source, err := resolveDatabasePath(serverLayout, explicit); err != nil || path != explicit || source != "adapter database_path setting" {
		t.Fatalf("explicit database path=%q source=%q err=%v", path, source, err)
	}

	relative := filepath.Join(runtimeRoot, "custom.db")
	writeTestFile(t, relative)
	if got, err := ResolveDatabasePath(projectRoot, "custom.db"); err != nil || got != relative {
		t.Fatalf("relative database = %q, %v", got, err)
	}

	if _, err := ResolveDatabasePath(projectRoot, filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("missing explicit database was accepted")
	}
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDatabasePath(projectRoot, ""); err == nil {
		t.Fatal("missing instance database was accepted")
	}
}

func TestResolveChannelCatalogUsesChannelInfoAndOverride(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	channelInfo := "[server] 1\n" +
		"   19   `<chn_channel_info_046>`   1   `[crack]`   0 \n" +
		"   251  `<chn_channel_info_046>`   40  `[crack]`   0 \n" +
		"   501  `<chn_channel_info_039>`   13  `[pvp]`     0 \n" +
		"   3    `<chn_channel_info_001>`   0   `[cain]`    0 \n" +
		"[/server]\n"
	if err := os.WriteFile(filepath.Join(projectRoot, projectRuntimeDirName, "data", "dnf", "channel_info.etc"), []byte(channelInfo), 0644); err != nil {
		t.Fatal(err)
	}
	serverLayout, err := resolveRuntimeLayout(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog, source, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if source != "instance game.channelInfoPath" {
		t.Fatalf("catalog source = %q", source)
	}
	// The ordinary [cain] channel wins over the crack/pvp entrances.
	if len(catalog.ports) != 1 || catalog.ports[0] != 11003 {
		t.Fatalf("catalog ports = %v", catalog.ports)
	}
	if got := catalog.portForAccount("robot17000001", 0); got != 11003 {
		t.Fatalf("account port = %d", got)
	}
	if got := catalog.portForAccount("robot17000001", 0); got != catalog.portForAccount("robot17000001", 0) {
		t.Fatal("account channel selection is not stable")
	}

	// Without an ordinary channel the crack bootstrap channels form the pool.
	crackOnly := strings.Replace(channelInfo, "   3    `<chn_channel_info_001>`   0   `[cain]`    0 \n", "", 1)
	if err := os.WriteFile(filepath.Join(projectRoot, projectRuntimeDirName, "data", "dnf", "channel_info.etc"), []byte(crackOnly), 0644); err != nil {
		t.Fatal(err)
	}
	crackCatalog, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(crackCatalog.ports) != 2 || crackCatalog.ports[0] != 11019 || crackCatalog.ports[1] != 11251 {
		t.Fatalf("crack catalog ports = %v", crackCatalog.ports)
	}

	pinned, source, err := resolveChannelCatalog(serverLayout, 10011)
	if err != nil || source != "adapter game_port setting" || len(pinned.ports) != 1 || pinned.ports[0] != 10011 {
		t.Fatalf("override catalog = %v/%q, %v", pinned.ports, source, err)
	}
	if _, _, err := resolveChannelCatalog(serverLayout, 70000); err == nil {
		t.Fatal("out-of-range override was accepted")
	}
}

// TestResolveChannelCatalogClassifiesChannelsByAreaGroup pins the production
// channel_info.etc shape: the ordinary [granfloris] town channel is type 11,
// which the inherited S4A21 type table misreads as special, while the raid
// groups carry types that table never listed. The pool must hold the single
// ordinary channel, at gamePortBase + 11 (11011 on the live server).
func TestResolveChannelCatalogClassifiesChannelsByAreaGroup(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	channelInfo := "[server] 1\n" +
		"   11   `<chn_channel_info_002>`   11  `[granfloris]`   0 \n" +
		"   19   `<chn_channel_info_046>`   1   `[crack]`        0 \n" +
		"   251  `<chn_channel_info_046>`   40  `[crack]`        0 \n" +
		"   501  `<chn_channel_info_039>`   13  `[pvp]`          0 \n" +
		"   252  `<chn_channel_info_039>`   8   `[pvp]`          0 \n" +
		"   221  `<chn_channel_info_047>`   23  `[anton_raid]`   0 \n" +
		"   241  `<chn_channel_info_048>`   32  `[luke_raid]`    0 \n" +
		"[/server]\n"
	if err := os.WriteFile(filepath.Join(projectRoot, projectRuntimeDirName, "data", "dnf", "channel_info.etc"), []byte(channelInfo), 0644); err != nil {
		t.Fatal(err)
	}
	serverLayout, err := resolveRuntimeLayout(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.ports) != 1 || catalog.ports[0] != 11011 {
		t.Fatalf("catalog ports = %v, want [11011]", catalog.ports)
	}
}

// TestResolveChannelCatalogFollowsInstanceGamePortBase checks the channel
// ports follow client.gamePortBase instead of a hardcoded base, and that a
// runtime predating the field falls back to the native default base.
func TestResolveChannelCatalogFollowsInstanceGamePortBase(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	channelInfo := "[server] 1\n" +
		"   3    `<chn_channel_info_001>`   0   `[cain]`    0 \n" +
		"[/server]\n"
	if err := os.WriteFile(filepath.Join(projectRoot, projectRuntimeDirName, "data", "dnf", "channel_info.etc"), []byte(channelInfo), 0644); err != nil {
		t.Fatal(err)
	}
	serverLayout, err := resolveRuntimeLayout(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	serverLayout.instance.Client.GamePortBase = 12000
	catalog, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.ports) != 1 || catalog.ports[0] != 12003 {
		t.Fatalf("catalog ports = %v, want [12003]", catalog.ports)
	}

	serverLayout.instance.Client.GamePortBase = 0
	catalog, _, err = resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.ports) != 1 || catalog.ports[0] != 11003 {
		t.Fatalf("fallback catalog ports = %v, want [11003]", catalog.ports)
	}
}

func TestAdminEndpointFollowsInstance(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	serverLayout, err := resolveRuntimeLayout(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	address, token, err := serverLayout.adminEndpoint()
	if err != nil || address != "127.0.0.1:18111" || token != "adm_test" {
		t.Fatalf("admin endpoint = %q/%q, %v", address, token, err)
	}
}

func TestNewRuntimeTransportValidatesAddress(t *testing.T) {
	if _, err := NewRuntimeTransport("", singlePortCatalog(7001), nil); err == nil {
		t.Fatal("empty host was accepted")
	}
	if _, err := NewRuntimeTransport("127.0.0.1", channelCatalog{}, nil); err == nil {
		t.Fatal("empty channel catalog was accepted")
	}
	transport, err := NewRuntimeTransport("127.0.0.1", singlePortCatalog(7001), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.CloseAll(); err != nil {
		t.Fatalf("close transport: %v", err)
	}
}

func TestComposeRuntimeRequiresAdapterInputs(t *testing.T) {
	projectRoot, _ := writeTestRuntime(t, "127.0.0.1:7001")
	paths := layout.New(t.TempDir())
	cases := []RuntimeComposeOptions{
		{},
		{AccountPrefix: "robot"},
		{AccountPrefix: "robot", ConnectIP: "127.0.0.1"},
		{AccountPrefix: "robot", ConnectIP: "127.0.0.1", ServerDirectory: projectRoot},
	}
	for _, opts := range cases {
		opts.Paths = paths
		if _, err := ComposeRuntime(context.Background(), opts); err == nil {
			t.Fatalf("incomplete options accepted: %+v", opts)
		}
	}
	// Missing random sources fail before any file or network access.
	_, err := ComposeRuntime(context.Background(), RuntimeComposeOptions{
		AccountPrefix: "robot", ConnectIP: "127.0.0.1", ServerDirectory: projectRoot,
		Paths: paths, Config: robotconfig.Default(),
	})
	if err == nil {
		t.Fatal("missing random sources accepted")
	}
}
