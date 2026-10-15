package cn90

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	"robot/internal/shared"
)

// TestZZProbeDemandPlan is the reproduction the demand layer needs before it is
// deployed: it reads the deployed ini with the runtime's own loader, projects
// the resident plan onto the map catalog the runtime exported, and asks the live
// lab database where the follow account is playing.
//
// Nothing here re-implements the layer's decision -- the placement itself is
// unit-tested -- so a green probe means the operator's configuration reaches the
// layer intact and the player it follows is findable.
// Enable with CN90_PROBE_DEMAND=1, plus CN90_LIVE_RUNTIME for the lab root and
// CN90_ROBOT_CONFIG_DIR for the deployed config directory.
func TestZZProbeDemandPlan(t *testing.T) {
	if os.Getenv("CN90_PROBE_DEMAND") == "" {
		t.Skip("set CN90_PROBE_DEMAND=1")
	}
	configDir := strings.TrimSpace(os.Getenv("CN90_ROBOT_CONFIG_DIR"))
	if configDir == "" {
		configDir = `G:\90-CN\robot-run\config`
	}
	iniPath := filepath.Join(configDir, "conf", "robot_config.ini")
	rc, err := robotconfig.LoadFile(iniPath)
	if err != nil {
		t.Fatalf("load %s: %v", iniPath, err)
	}

	out := filepath.Join(os.TempDir(), "demand_probe.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }
	fail := func(format string, args ...any) {
		fmt.Fprintf(file, "VIOLATION "+format, args...)
		t.Errorf(format, args...)
	}

	say("ini=%s\n", iniPath)
	say("follow_account=%q resident_plan=%q\n", rc.FollowAccount, rc.ResidentPlan)
	say("auto_target_online_count=%d pool_size=%d max_online_robots=%d -> target=%d pool=%d\n",
		rc.AutoTargetOnlineCount, rc.PoolSize, rc.MaxOnlineRobots,
		robotconfig.TargetCapacity(rc), robotconfig.PoolCapacity(rc))
	say("release_batch=%d release_gap=%s player_poll=%s player_grace=%s\n",
		robotconfig.OfflineReleaseBatch(rc), robotconfig.OfflineReleaseGap(rc),
		robotconfig.DemandPlayerPoll(rc), robotconfig.DemandPlayerGrace(rc))

	rows, err := robotconfig.ParseResidentPlan(rc.ResidentPlan)
	if err != nil {
		fail("resident_plan does not parse: %v\n", err)
	}
	say("\n=== resident rows ===\n")
	for _, row := range rows {
		say("  %d/%d-%d count=%d box=%t(%d,%d-%d,%d)\n",
			row.Village, row.AreaMin, row.AreaMax, row.Count, row.BoxSet, row.XMin, row.YMin, row.XMax, row.YMax)
	}
	if size, want := robotconfig.ResidentPlanSize(rows), 65; size != want {
		say("  NOTE resident_plan_size=%d\n", size)
	}

	// The catalog the running fleet reads is the one the runtime exported.
	catalogPath := filepath.Join(configDir, "pvf", "map_catalog.json")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read %s: %v", catalogPath, err)
	}
	var maps []shared.MapCatalogItem
	if err := json.Unmarshal(data, &maps); err != nil {
		t.Fatalf("parse %s: %v", catalogPath, err)
	}
	say("\n=== catalog %s maps=%d ===\n", catalogPath, len(maps))

	// The layer's own admission test, applied to the live catalog: a resident row
	// that admits no map silently stands nobody, which is the failure this probe
	// exists to catch.
	const fleetTopLevel = 90
	say("=== rows against the catalog (top fleet level %d) ===\n", fleetTopLevel)
	for _, row := range rows {
		usable, boxed := 0, 0
		for _, mp := range maps {
			if mp.Village != row.Village || mp.Area < row.AreaMin || mp.Area > row.AreaMax {
				continue
			}
			if !mp.Use || mp.Level > fleetTopLevel {
				continue
			}
			rectangles := robotspawn.MapRectangles(mp)
			if len(rectangles) == 0 {
				continue
			}
			usable++
			if !row.BoxSet {
				boxed++
				continue
			}
			for _, rectangle := range rectangles {
				if rectangle.XMax >= row.XMin && rectangle.XMin <= row.XMax &&
					rectangle.YMax >= row.YMin && rectangle.YMin <= row.YMax {
					boxed++
					break
				}
			}
		}
		say("  %d/%d-%d: maps=%d admits_box=%d\n", row.Village, row.AreaMin, row.AreaMax, usable, boxed)
		if usable == 0 {
			fail("resident row %d/%d-%d admits no usable map\n", row.Village, row.AreaMin, row.AreaMax)
		}
		if row.BoxSet && boxed == 0 {
			fail("resident row %d/%d-%d has a box no walkable rectangle covers\n", row.Village, row.AreaMin, row.AreaMax)
		}
	}

	// Where the player is playing, read from the live database with the same
	// locator the runtime installs.
	root := liveRuntimeRoot(t)
	databasePath := filepath.Join(root, projectRuntimeDirName, "data", "dnf90.db")
	locator := FollowAccountLocator{DatabasePath: databasePath}
	location, ok, err := locator.LocatePlayer(context.Background(), rc.FollowAccount)
	say("\n=== follow account %q via %s ===\n", rc.FollowAccount, databasePath)
	if err != nil {
		fail("locate player: %v\n", err)
	}
	if !ok || !location.Available() {
		fail("follow account %q resolves to no character: ok=%t available=%t\n",
			rc.FollowAccount, ok, location.Available())
	} else {
		say("  cid=%s name=%q level=%d village=%d area=%d pos=(%d,%d) seen=%s\n",
			location.CharacterID, location.Name, location.Level,
			location.Village, location.Area, location.X, location.Y, location.Seen)

		// The operator may write the character name instead of the account id.
		// That form is only real if it survives being written to an ini and read
		// back, so the probe writes one and reads it with the runtime's loader
		// before asking the database.
		named := filepath.Join(t.TempDir(), "robot_config.ini")
		body := "[follow]\nfollow_account = " + location.Name + "\n"
		if err := os.WriteFile(named, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		namedConfig, err := robotconfig.LoadFile(named)
		if err != nil {
			fail("read back an ini naming the character: %v\n", err)
		} else if namedConfig.FollowAccount != location.Name {
			fail("the character name did not survive the ini: got %q want %q\n",
				namedConfig.FollowAccount, location.Name)
		} else {
			byName, byNameOK, byNameErr := locator.LocatePlayer(context.Background(), namedConfig.FollowAccount)
			say("  by name %q: found=%t err=%v", namedConfig.FollowAccount, byNameOK, byNameErr)
			if byNameOK {
				say(" cid=%s village=%d area=%d", byName.CharacterID, byName.Village, byName.Area)
			}
			say("\n")
			if byNameErr != nil || !byNameOK {
				fail("the character name %q does not resolve to a character\n", namedConfig.FollowAccount)
			}
		}

		// The floating layer fills what the crowd rules left open around the
		// player, so the reach it actually has is worth printing.
		rules := NewCrowdRules(maps)
		rules.SetPlayerMap(location.Village, location.Area)
		distances := rules.MapDistances()
		counts := map[int]int{}
		for _, mp := range maps {
			distance, known := distances[shared.MapAreaKey{Village: mp.Village, Area: mp.Area}]
			if !known || !mp.Use || len(robotspawn.MapRectangles(mp)) == 0 {
				continue
			}
			if distance > 3 {
				continue
			}
			counts[distance]++
		}
		say("  floating reach by transitions:")
		for distance := 0; distance <= 3; distance++ {
			say(" %d=%d", distance, counts[distance])
		}
		say("\n")
		if counts[0] == 0 {
			fail("the player's map %d/%d is not in its own walk\n", location.Village, location.Area)
		}
		// What the halving budget does with the reach above, at the operator's
		// floating headcount: each ring takes half of what is left, and the
		// scheduler then caps each map, spilling the excess outward.
		rings := 0
		for distance := 0; distance <= 3; distance++ {
			if counts[distance] > 0 {
				rings = distance + 1
			}
		}
		if rings > 0 {
			budget := robotconfig.TargetCapacity(rc) - robotconfig.ResidentPlanSize(rows)
			say("  ring budget split of a %d-robot floating budget (before the per-map cap):", budget)
			remaining := budget
			for index := 0; index < rings; index++ {
				share := remaining
				if index < rings-1 {
					share = remaining / 2
					if share < 1 {
						share = 1
					}
				}
				say(" %d->%d", index, share)
				remaining -= share
			}
			say("\n")
		}
		if counts[1] == 0 && counts[2] == 0 && counts[3] == 0 {
			say("  NOTE the player's town has no neighbour map in reach; only the map itself will fill\n")
		}
	}

	say("\nprobe written to %s\n", out)
	t.Logf("demand probe written to %s", out)
}
