package store

import (
	"errors"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type maintenanceTestEnv struct {
	maps       []shared.MapCatalogItem
	locations  []shared.MapLocation
	restored   robotcap.Info
	restoreErr error
	synced     bool
}

func (e *maintenanceTestEnv) LoadMapCatalog() []shared.MapCatalogItem { return e.maps }
func (*maintenanceTestEnv) Logf(string, ...interface{})               {}
func (*maintenanceTestEnv) RandBetween(min, _ int) int                { return min }
func (e *maintenanceTestEnv) RandomMap(_ []shared.MapCatalogItem, _ int) (shared.MapCatalogItem, bool) {
	if len(e.maps) == 0 {
		return shared.MapCatalogItem{}, false
	}
	return e.maps[0], true
}
func (e *maintenanceTestEnv) RobotLocations() ([]shared.MapLocation, error) {
	return append([]shared.MapLocation(nil), e.locations...), nil
}
func (*maintenanceTestEnv) ResetPrivateStore(int) {}
func (e *maintenanceTestEnv) RestoreDummyNormal(info robotcap.Info) error {
	e.restored = info
	return e.restoreErr
}
func (*maintenanceTestEnv) RevokeStorePermission(int, int) error { return nil }
func (*maintenanceTestEnv) SelectRobots(robotcap.CommandRequest) ([]robotcap.Info, error) {
	return nil, nil
}
func (e *maintenanceTestEnv) SyncCharacterVillage(int, int) (int, error) {
	e.synced = true
	return 0, nil
}

func TestRestoreAutoNormalPositionSelectsEmptyMovableArea(t *testing.T) {
	env := &maintenanceTestEnv{
		maps: []shared.MapCatalogItem{
			{Village: 1, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 10, XMax: 20, YMin: 30, YMax: 40}}},
			{Village: 2, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 100, XMax: 120, YMin: 130, YMax: 140}}},
		},
		locations: []shared.MapLocation{{Village: 1, Area: 0, X: 15, Y: 35}},
	}
	info := robotcap.Info{UID: 17000001, CID: 1, Level: 85}
	normal, err := (Maintenance{Env: env}).RestoreAutoNormalPosition(info, robotconfig.Default(), "test")
	if err != nil {
		t.Fatalf("restore normal: %v", err)
	}
	if normal.Village != 2 || normal.Area != 0 || normal.X != 100 || normal.Y != 130 {
		t.Fatalf("normal=%+v, want empty village 2 rectangle", normal)
	}
	if env.restored != normal {
		t.Fatalf("restored=%+v, want %+v", env.restored, normal)
	}
}

func TestRestoreAutoNormalPositionCanReturnToEmptyGateArea(t *testing.T) {
	env := &maintenanceTestEnv{
		maps: []shared.MapCatalogItem{
			{Village: 1, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 10, XMax: 20, YMin: 30, YMax: 40}}},
			{Village: 1, Area: 1, Use: true, Gate: true, Rectangles: []shared.MapRectangle{{XMin: 100, XMax: 120, YMin: 130, YMax: 140}}},
		},
		locations: []shared.MapLocation{{Village: 1, Area: 0, X: 15, Y: 35}},
	}
	info := robotcap.Info{UID: 17000001, CID: 1, Level: 85}
	normal, err := (Maintenance{Env: env}).RestoreAutoNormalPosition(info, robotconfig.Default(), "test")
	if err != nil {
		t.Fatalf("restore normal: %v", err)
	}
	if normal.Village != 1 || normal.Area != 1 || normal.X != 100 || normal.Y != 130 {
		t.Fatalf("normal=%+v, want empty gate area", normal)
	}
}

func TestRestoreAutoNormalPositionExcludesGuildAgitForGuildMember(t *testing.T) {
	env := &maintenanceTestEnv{
		maps: []shared.MapCatalogItem{
			{Village: shared.GuildAgitVillage, Area: 2, Use: true, Rectangles: []shared.MapRectangle{{XMin: 10, XMax: 20, YMin: 30, YMax: 40}}},
			{Village: 1, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 100, XMax: 120, YMin: 130, YMax: 140}}},
		},
		locations: []shared.MapLocation{{Village: shared.GuildAgitVillage, Area: 2, X: 15, Y: 35}},
	}
	info := robotcap.Info{UID: 17000001, CID: 1, GuildID: 2, Level: 85}
	normal, err := (Maintenance{Env: env}).RestoreAutoNormalPosition(info, robotconfig.Default(), "test")
	if err != nil {
		t.Fatalf("restore normal: %v", err)
	}
	if normal.Village != 1 || normal.Area != 0 {
		t.Fatalf("normal=%+v, want non-agit village 1/0", normal)
	}
}

func TestRestoreAutoNormalPositionStopsWhenDummyWriteFails(t *testing.T) {
	wantErr := errors.New("write failed")
	env := &maintenanceTestEnv{
		maps:       []shared.MapCatalogItem{{Village: 1, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 10, XMax: 20, YMin: 30, YMax: 40}}}},
		restoreErr: wantErr,
	}
	info := robotcap.Info{UID: 17000001, CID: 1, Level: 85}
	_, err := (Maintenance{Env: env}).RestoreAutoNormalPosition(info, robotconfig.Default(), "test")
	if !errors.Is(err, wantErr) {
		t.Fatalf("restore error=%v want=%v", err, wantErr)
	}
	if env.synced {
		t.Fatal("character village sync ran after dummy write failure")
	}
}

// pinnedMaintenanceTestEnv is the base env plus the demand plan. Only it
// implements demandLocationEnv, so the other tests keep exercising the path an
// adapter with no plan takes.
type pinnedMaintenanceTestEnv struct {
	*maintenanceTestEnv
	village, area, x, y int
}

func (e pinnedMaintenanceTestEnv) DemandLocation(uid int) (int, int, int, int, bool) {
	if uid != 17000042 {
		return 0, 0, 0, 0, false
	}
	return e.village, e.area, e.x, e.y, true
}

// A store round must put a pinned robot back on the map the operator pinned it
// to. The whole fleet used to converge on one town because this path chose the
// follow account's town instead.
func TestRestoreAutoNormalPositionKeepsTheDemandPin(t *testing.T) {
	base := &maintenanceTestEnv{
		maps: []shared.MapCatalogItem{
			{Village: 1, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 10, XMax: 20, YMin: 30, YMax: 40}}},
			{Village: 2, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 100, XMax: 120, YMin: 130, YMax: 140}}},
		},
	}
	env := pinnedMaintenanceTestEnv{maintenanceTestEnv: base, village: 39, area: 0, x: 1686, y: 155}
	info := robotcap.Info{UID: 17000042, CID: 1, Level: 85}
	normal, err := (Maintenance{Env: env}).RestoreAutoNormalPosition(info, robotconfig.Default(), "store_expired")
	if err != nil {
		t.Fatalf("restore normal: %v", err)
	}
	if normal.Village != 39 || normal.Area != 0 || normal.X != 1686 || normal.Y != 155 {
		t.Fatalf("normal=%+v, want the demand pin 39/0/1686/155", normal)
	}
	if base.restored != normal {
		t.Fatalf("restored=%+v, want %+v", base.restored, normal)
	}
}

// A robot the plan does not want keeps the capacity-balanced pick even when the
// pin range is populated.
func TestRestoreAutoNormalPositionIgnoresOtherRobotsPins(t *testing.T) {
	base := &maintenanceTestEnv{
		maps: []shared.MapCatalogItem{
			{Village: 1, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 10, XMax: 20, YMin: 30, YMax: 40}}},
			{Village: 2, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 100, XMax: 120, YMin: 130, YMax: 140}}},
		},
		locations: []shared.MapLocation{{Village: 1, Area: 0, X: 15, Y: 35}},
	}
	env := pinnedMaintenanceTestEnv{maintenanceTestEnv: base, village: 39, area: 0, x: 1686, y: 155}
	info := robotcap.Info{UID: 17000043, CID: 1, Level: 85}
	normal, err := (Maintenance{Env: env}).RestoreAutoNormalPosition(info, robotconfig.Default(), "store_expired")
	if err != nil {
		t.Fatalf("restore normal: %v", err)
	}
	if normal.Village != 2 || normal.Area != 0 {
		t.Fatalf("normal=%+v, want the balanced empty village 2", normal)
	}
}
