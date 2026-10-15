package robotaction

import (
	"errors"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type capturedMoveStep struct {
	info          robotcap.Info
	targetVillage int
	targetArea    int
	targetX       int
	targetY       int
}

type captureMoveEnv struct {
	steps  []capturedMoveStep
	failAt int
	robots []robotcap.Info
	status map[int]robotcap.RuntimeStatus
}

func (e *captureMoveEnv) DispatchMoveStep(info robotcap.Info, targetVillage, targetArea, targetX, targetY, _, _, _ int, _ robotconfig.RuntimeConfig) error {
	e.steps = append(e.steps, capturedMoveStep{
		info:          info,
		targetVillage: targetVillage,
		targetArea:    targetArea,
		targetX:       targetX,
		targetY:       targetY,
	})
	if e.failAt > 0 && len(e.steps) == e.failAt {
		return errors.New("move send failed")
	}
	return nil
}

func (*captureMoveEnv) LoadMapCatalog() []shared.MapCatalogItem { return nil }
func (*captureMoveEnv) RandBetween(min, _ int) int              { return min }
func (*captureMoveEnv) RuntimeStatus(int) (robotcap.RuntimeStatus, bool) {
	return robotcap.RuntimeStatus{}, false
}
func (e *captureMoveEnv) RuntimeStatusMap() map[int]robotcap.RuntimeStatus { return e.status }
func (e *captureMoveEnv) SelectRobots(robotcap.CommandRequest) ([]robotcap.Info, error) {
	return e.robots, nil
}

func TestMoveKeepsPersistedTownWhenRuntimeHasNoPositionYet(t *testing.T) {
	robot := robotcap.Info{UID: 101, Village: 1, Area: 2, X: 300, Y: 200}
	env := &captureMoveEnv{
		robots: []robotcap.Info{robot},
		status: map[int]robotcap.RuntimeStatus{101: {UID: 101, StateName: robotcap.RuntimeStateRunning}},
	}
	rc := robotconfig.RuntimeConfig{MoveSteps: 2, MoveSpeedMin: 100, MoveSpeedMax: 100}
	result, err := (MoveService{Env: env}).Move(robotcap.CommandRequest{UIDs: []int{101}}, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || len(env.steps) == 0 {
		t.Fatalf("result=%+v steps=%+v", result, env.steps)
	}
	if env.steps[0].info.Village != robot.Village || env.steps[0].info.Area != robot.Area {
		t.Fatalf("move source=%+v want village=%d area=%d", env.steps[0].info, robot.Village, robot.Area)
	}
}

func TestAutoMovePreservesSourceWhenFollowingAcrossAreas(t *testing.T) {
	env := &captureMoveEnv{}
	service := MoveService{Env: env}
	source := robotcap.Info{UID: 101, CID: 201, Village: 1, Area: 2, X: 100, Y: 50}
	target := FollowTarget{Village: 3, Area: 4, X: 500, Y: 200}
	rc := robotconfig.RuntimeConfig{
		MoveSteps:     2,
		MoveSpeedMin:  100,
		MoveSpeedMax:  100,
		FollowRadiusX: 20,
		FollowRadiusY: 10,
	}
	maps := []shared.MapCatalogItem{{Village: 3, Area: 4, XMin: 400, XMax: 600, YMin: 150, YMax: 250, Use: true}}

	if err := service.AutoMove(source, rc, maps, &target); err != nil {
		t.Fatal(err)
	}

	if len(env.steps) != 2 {
		t.Fatalf("move steps got %d want 2", len(env.steps))
	}
	for _, step := range env.steps {
		if step.info.Village != source.Village || step.info.Area != source.Area || step.info.X != source.X || step.info.Y != source.Y {
			t.Fatalf("source position was mutated: %+v", step.info)
		}
		if step.targetVillage != target.Village || step.targetArea != target.Area {
			t.Fatalf("target area got %d/%d want %d/%d", step.targetVillage, step.targetArea, target.Village, target.Area)
		}
	}
}

func TestAutoMoveRelocatesExistingRobotToConfiguredFixedTown(t *testing.T) {
	env := &captureMoveEnv{}
	service := MoveService{Env: env}
	source := robotcap.Info{UID: 101, Village: 1, Area: 2, X: 100, Y: 50}
	rc := robotconfig.RuntimeConfig{
		SpawnFixed: true, SpawnVillage: 3, SpawnArea: 4,
		SpawnXMin: 410, SpawnXMax: 490, SpawnYMin: 210, SpawnYMax: 250,
		MoveSteps: 2, MoveSpeedMin: 100, MoveSpeedMax: 100,
	}
	maps := []shared.MapCatalogItem{{
		Village: 3, Area: 4, Use: true,
		Rectangles: []shared.MapRectangle{{XMin: 400, XMax: 500, YMin: 200, YMax: 260}},
	}}

	if err := service.AutoMove(source, rc, maps, nil); err != nil {
		t.Fatal(err)
	}
	if len(env.steps) != 2 {
		t.Fatalf("move steps = %d, want 2", len(env.steps))
	}
	for _, step := range env.steps {
		if step.targetVillage != 3 || step.targetArea != 4 {
			t.Fatalf("fixed target area = %d/%d", step.targetVillage, step.targetArea)
		}
		if step.targetX < 410 || step.targetX > 490 || step.targetY < 210 || step.targetY > 250 {
			t.Fatalf("fixed target position = %d/%d", step.targetX, step.targetY)
		}
	}
}

func TestAutoMoveStopsAfterDispatchFailure(t *testing.T) {
	env := &captureMoveEnv{failAt: 2}
	service := MoveService{Env: env}
	rc := robotconfig.RuntimeConfig{MoveSteps: 4, MoveSpeedMin: 100, MoveSpeedMax: 100}
	err := service.AutoMove(robotcap.Info{UID: 101, X: 10, Y: 10}, rc, nil, nil)
	if err == nil || err.Error() != "move send failed" {
		t.Fatalf("AutoMove error = %v", err)
	}
	if len(env.steps) != 2 {
		t.Fatalf("move steps after failure = %d, want 2", len(env.steps))
	}
}

func TestAutoMoveRejectsGuildAgitFollowForGuildMember(t *testing.T) {
	env := &captureMoveEnv{}
	service := MoveService{Env: env}
	rc := robotconfig.RuntimeConfig{MoveSteps: 2, MoveSpeedMin: 100, MoveSpeedMax: 100}
	source := robotcap.Info{UID: 101, GuildID: 2, Village: 1, Area: 0, X: 10, Y: 10}
	target := FollowTarget{Village: shared.GuildAgitVillage, Area: 2, X: 100, Y: 100}

	err := service.AutoMove(source, rc, nil, &target)
	if err == nil {
		t.Fatal("guild member agit follow was accepted")
	}
	if len(env.steps) != 0 {
		t.Fatalf("unsafe move dispatched steps=%v", env.steps)
	}
}

func TestAutoMoveRejectsCrossAreaFollowWithoutUsableGeometry(t *testing.T) {
	env := &captureMoveEnv{}
	service := MoveService{Env: env}
	rc := robotconfig.RuntimeConfig{MoveSteps: 2, MoveSpeedMin: 100, MoveSpeedMax: 100}
	source := robotcap.Info{UID: 101, Village: 1, Area: 2, X: 10, Y: 10}
	target := FollowTarget{Village: 3, Area: 4, X: 100, Y: 100}

	err := service.AutoMove(source, rc, []shared.MapCatalogItem{{Village: 3, Area: 4, Use: false}}, &target)
	if err == nil || err.Error() != "follow target town area 3/4 has no usable movement geometry" {
		t.Fatalf("error=%v, want missing target geometry error", err)
	}
	if len(env.steps) != 0 {
		t.Fatalf("invalid cross-area follow dispatched steps=%v", env.steps)
	}
}

// The Seria room the server opens every login in is the one map a robot must
// never wander: it has no geometry and no players. A robot standing in one walks
// to a door instead, which is the only request shape the server answers from a
// room it will not let anyone stay in.
func TestAutoMoveWalksOutOfTheSeriaRoomThroughADoor(t *testing.T) {
	env := &captureMoveEnv{}
	service := MoveService{Env: env}
	source := robotcap.Info{UID: 101, Village: 38, Area: 1, X: 450, Y: 234}
	rc := robotconfig.RuntimeConfig{MoveSteps: 2, MoveSpeedMin: 100, MoveSpeedMax: 100}
	maps := []shared.MapCatalogItem{
		{Village: 38, Area: 1, Use: false, Links: []shared.MapAreaKey{{Village: 39, Area: 0}, {Village: 98, Area: 1}}},
		{Village: 39, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 300, XMax: 360, YMin: 180, YMax: 220}}},
		{Village: 98, Area: 1, Use: false, Rectangles: []shared.MapRectangle{{XMin: 300, XMax: 360, YMin: 180, YMax: 220}}},
	}

	if err := service.AutoMove(source, rc, maps, nil); err != nil {
		t.Fatal(err)
	}
	if len(env.steps) != 2 {
		t.Fatalf("move steps = %d, want 2", len(env.steps))
	}
	for _, step := range env.steps {
		if step.targetVillage != 39 || step.targetArea != 0 {
			t.Fatalf("room escape target = %d/%d, want the usable door 39/0", step.targetVillage, step.targetArea)
		}
		if step.info.Village != source.Village || step.info.Area != source.Area {
			t.Fatalf("room escape source was mutated: %+v", step.info)
		}
	}
}

func TestAutoMoveStaysWhenEveryDoorOutOfAnUnusableMapIsUnusable(t *testing.T) {
	env := &captureMoveEnv{}
	service := MoveService{Env: env}
	source := robotcap.Info{UID: 101, Village: 38, Area: 1, X: 450, Y: 234}
	rc := robotconfig.RuntimeConfig{MoveSteps: 2, MoveSpeedMin: 100, MoveSpeedMax: 100}
	maps := []shared.MapCatalogItem{
		{Village: 38, Area: 1, Use: false, Links: []shared.MapAreaKey{{Village: 98, Area: 1}, {Village: 99, Area: 0}}},
		{Village: 98, Area: 1, Use: true, Level: 90, Rectangles: []shared.MapRectangle{{XMin: 300, XMax: 360, YMin: 180, YMax: 220}}},
		{Village: 99, Area: 0, Use: false},
	}

	if err := service.AutoMove(source, rc, maps, nil); err != nil {
		t.Fatal(err)
	}
	for _, step := range env.steps {
		if step.targetVillage != source.Village || step.targetArea != source.Area {
			t.Fatalf("doorless room escape target = %d/%d", step.targetVillage, step.targetArea)
		}
	}
}

func TestAutoMoveKeepsWanderingItsOwnMapWhenThatMapIsAdmitted(t *testing.T) {
	env := &captureMoveEnv{}
	service := MoveService{Env: env}
	source := robotcap.Info{UID: 101, Village: 39, Area: 8, X: 300, Y: 200}
	rc := robotconfig.RuntimeConfig{MoveSteps: 2, MoveSpeedMin: 100, MoveSpeedMax: 100}
	maps := []shared.MapCatalogItem{
		{Village: 39, Area: 8, Use: true, Links: []shared.MapAreaKey{{Village: 39, Area: 0}}, Rectangles: []shared.MapRectangle{{XMin: 260, XMax: 340, YMin: 180, YMax: 230}}},
		{Village: 39, Area: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 100, XMax: 200, YMin: 100, YMax: 150}}},
	}

	if err := service.AutoMove(source, rc, maps, nil); err != nil {
		t.Fatal(err)
	}
	for _, step := range env.steps {
		if step.targetVillage != source.Village || step.targetArea != source.Area {
			t.Fatalf("admitted map wander target = %d/%d, want its own map", step.targetVillage, step.targetArea)
		}
	}
}
