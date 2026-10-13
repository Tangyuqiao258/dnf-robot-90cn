package cn90

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// keepSceneArea is the Elvengard area the squad stands in: 0 is the main town
// map (Cataclysm/Town/Elvengard/new_Elvengard.map where traveling players
// arrive); 1 would be Seria's room (new_seria_room.map, gate 450/234).
const keepSceneArea = 0

// TestLiveKeepSceneElvengard is the operator driver: it provisions a squad of
// robots, logs them in at Elvengard (town 38) and holds the sessions open so
// the operator can verify the robots inside the real game client. It is env
// gated, never runs in the normal suite, leaves the characters in the
// database, and closes the sessions cleanly once the stop sentinel appears.
func TestLiveKeepSceneElvengard(t *testing.T) {
	if os.Getenv("CN90_LIVE_KEEPSCENE") != "1" {
		t.Skip("set CN90_LIVE_KEEPSCENE=1 to hold a robot squad in Elvengard")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	adminAddress, adminToken, err := serverLayout.adminEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	// Pin the operator's game port (the config.ini [Ports] Game equivalent) so
	// the squad lands on the default gameplay channel through singlePortCatalog
	// instead of the ranked channel catalog, without touching the server's
	// channel tables.
	const pinnedGamePort = 11011
	channels, catalogSource, err := resolveChannelCatalog(serverLayout, pinnedGamePort)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("channel catalog pinned to %d (%s): ports=%v", pinnedGamePort, catalogSource, channels.ports)
	if !liveListener(t, channels) {
		t.Skipf("no live 90CN game listener among %v", channels.ports)
	}
	pvfPath, _, err := serverLayout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}

	stopFile := filepath.Join(root, "STOP_KEEPSCENE")
	marker := filepath.Join(root, "keepscene-online.txt")
	_ = os.Remove(stopFile)
	_ = os.Remove(marker)

	const (
		village = 38 // 艾尔文防线 (town/new_Elvengard.twn)
		level   = 70
	)
	squad := []struct {
		account string
		name    string
		uid     int
		job     int
		x       int
		y       int
	}{
		{"robot17000011", "自动机器人甲", 17000011, 0, 380, 300},
		{"robot17000012", "自动机器人乙", 17000012, 1, 520, 320},
		{"robot17000013", "自动机器人丙", 17000013, 2, 660, 300},
		{"robot17000014", "自动机器人丁", 17000014, 3, 800, 330},
		{"robot17000015", "自动机器人戊", 17000015, 4, 940, 310},
	}

	binder := NewAccountBinder(adminAddress, adminToken)
	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	applier, err := NewSQLiteLoadoutApplier(context.Background(), databasePath, robotconfig.Default(), catalogs.Equipment, pvfPath, nil)
	if err != nil {
		t.Fatalf("loadout applier: %v", err)
	}
	defer applier.Close()
	applier.LevelThresholds = catalogs.LevelThresholds

	// Phase 1: prepare every character while it is still offline.
	slots := make([]uint16, len(squad))
	for i, member := range squad {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		if err := liveClearAccount(ctx, binder, channels, member.account); err != nil {
			cancel()
			t.Fatalf("pre-clean %s: %v", member.account, err)
		}
		result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
			AccountName: member.account, CharacterName: member.name, Job: member.job, RobotUID: member.uid,
		})
		if err != nil {
			cancel()
			t.Fatalf("provision %s: %v", member.account, err)
		}
		if !result.Created || result.BackendSlot == nil {
			cancel()
			t.Fatalf("provision result for %s = %+v", member.account, result)
		}
		info := robotcap.Info{UID: member.uid, Name: result.CharacterName}
		leveled, err := applier.InitializeCharacter(ctx, member.account, info, level, 0)
		if err != nil {
			cancel()
			t.Fatalf("initialize %s: %v", member.account, err)
		}
		if leveled.Level != level {
			cancel()
			t.Fatalf("initialized %s level = %+v", member.account, leveled)
		}
		slots[i] = *result.BackendSlot
		cancel()
		t.Logf("prepared account=%s name=%s uid=%d slot=%d job=%d level=%d",
			member.account, member.name, member.uid, slots[i], member.job, leveled.Level)
	}

	// Phase 2: log the squad in and hold the sessions. A spawn request the
	// server rejects keeps the session online at the persisted location and
	// only logs CN90_TOWN_PRESENCE_DEFERRED, so watch the output for it.
	sessions := make([]*Session, 0, len(squad))
	defer func() {
		for _, session := range sessions {
			_ = session.Close()
		}
	}()
	online := make([]string, 0, len(squad))
	for i, member := range squad {
		openCtx, openCancel := context.WithTimeout(context.Background(), 45*time.Second)
		session, err := factory.OpenSession(openCtx, shared.OpenSessionRequest{
			AccountName: member.account, CharacterSlot: slots[i],
			InitialTownKnown: true, InitialVillage: village, InitialArea: keepSceneArea,
			InitialX: member.x, InitialY: member.y,
		})
		openCancel()
		if err != nil {
			t.Fatalf("open session %s: %v", member.account, err)
		}
		liveSession, ok := session.(*Session)
		if !ok {
			t.Fatalf("unexpected session type for %s", member.account)
		}
		sessions = append(sessions, liveSession)

		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			liveSession.keepaliveGuard.Lock()
			seen := liveSession.checkAckSeen
			liveSession.keepaliveGuard.Unlock()
			if seen {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		liveSession.keepaliveGuard.Lock()
		acknowledged := liveSession.checkAckSeen
		liveSession.keepaliveGuard.Unlock()
		t.Logf("session online account=%s cid=%d ack=%v pos=(%d,%d) village=%d area=%d",
			member.account, liveSession.selfCharacterID, acknowledged, member.x, member.y, village, keepSceneArea)
		online = append(online, fmt.Sprintf("%s %s cid=%d ack=%v pos=(%d,%d)", member.account, member.name, liveSession.selfCharacterID, acknowledged, member.x, member.y))
	}

	if err := os.WriteFile(marker, []byte(strings.Join(online, "\n")+"\n"), 0o644); err != nil {
		t.Logf("marker write failed: %v", err)
	}
	t.Logf("KEEPSCENE ONLINE robots=%d marker=%s", len(online), marker)
	t.Logf("squad is standing in Elvengard village=%d area=%d; the operator can log in now", village, keepSceneArea)
	t.Logf("holding the scene; create %s to close the sessions", stopFile)
	for {
		if _, err := os.Stat(stopFile); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	_ = os.Remove(stopFile)
	t.Log("stop sentinel found; closing the squad")
}
