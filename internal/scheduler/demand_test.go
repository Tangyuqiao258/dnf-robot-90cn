package scheduler

import (
	"context"
	"testing"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// demandTestPlayer is the account the follow tests ask about. It stands for the
// operator's own character; the locator that answers for it is a fake, so the
// value only has to be stable.
const demandTestPlayer = "player-account"

// demandTestMaps is a small world: the main town's square and a neighbour, the
// gate town behind it, and a high-level town the fleet's low-level robots cannot
// stand in.
func demandTestMaps() []shared.MapCatalogItem {
	rectangle := []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}
	return []shared.MapCatalogItem{
		{Village: 39, Area: 0, Level: 1, Use: true, Rectangles: rectangle},
		{Village: 39, Area: 1, Level: 1, Use: true, Rectangles: rectangle},
		{Village: 38, Area: 0, Level: 1, Use: true, Rectangles: rectangle},
		{Village: 30, Area: 0, Level: 60, Use: true, Rectangles: rectangle},
	}
}

// demandTestCrowd is the part of a crowd policy the demand layer reads: the walk
// it computed outward from the player's map.
type demandTestCrowd struct {
	village   int
	area      int
	distances map[shared.MapAreaKey]int
}

func (c *demandTestCrowd) CrowdMapRule(int, int) shared.CrowdMapRule { return shared.CrowdMapRule{} }

func (c *demandTestCrowd) SetPlayerMap(village, area int) { c.village, c.area = village, area }

func (c *demandTestCrowd) MapDistances() map[shared.MapAreaKey]int { return c.distances }

// demandTestLocator answers with whatever position the test last set.
type demandTestLocator struct {
	location shared.PlayerLocation
	found    bool
	calls    int
}

func (l *demandTestLocator) LocatePlayer(context.Context, string) (shared.PlayerLocation, bool, error) {
	l.calls++
	return l.location, l.found, nil
}

func demandTestPool(levels ...int) []robotcap.Info {
	pool := make([]robotcap.Info, 0, len(levels))
	for index, level := range levels {
		pool = append(pool, robotcap.Info{UID: 17001000 + index, Level: level})
	}
	return pool
}

// demandTestSupervisor builds a supervisor whose fleet is a pool of robots the
// test hands it directly.
func demandTestSupervisor(t *testing.T, crowd shared.CrowdMapPolicy) *RobotSupervisor {
	t.Helper()
	manager := testRobotManagerWithConfig(t, "")
	manager.SetTownMapCatalog(demandTestMaps())
	if crowd != nil {
		manager.SetCrowdMapPolicy(crowd)
	}
	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	t.Cleanup(supervisor.stopAll)
	return supervisor
}

// leaseActorsToPool gives every pool robot a leased actor, which is what the
// layer finds when it looks for the actors it may stand down.
func leaseActorsToPool(t *testing.T, supervisor *RobotSupervisor, pool []robotcap.Info) []*actormodel.Actor {
	t.Helper()
	actors := ensureSupervisorActors(t, supervisor, len(pool))
	for index, actor := range actors {
		uid := pool[index].UID
		if !actor.AssignAndWait(uid, time.Second) {
			t.Fatalf("assign actor slot=%d uid=%d", index, uid)
		}
		if !supervisor.ledger.TryLeaseUID(uid, actor) {
			t.Fatalf("lease uid=%d", uid)
		}
	}
	return actors
}

func TestDemandPlanStandsItsResidentsAndShedsTheRest(t *testing.T) {
	supervisor := demandTestSupervisor(t, nil)
	rc := robotconfig.Default()
	rc.ResidentPlan = "39/0:2"
	rc.AutoTargetOnlineCount = 4
	pool := demandTestPool(90, 90, 90, 90, 90)

	rows := supervisor.residentRows(rc)
	if len(rows) != 1 || rows[0].Count != 2 {
		t.Fatalf("parsed rows = %+v", rows)
	}
	plan := supervisor.buildDemandPlan(rows, shared.PlayerLocation{}, false, pool, rc)
	if !plan.active {
		t.Fatal("a plan with rows must be active")
	}
	if plan.residentCount != 2 || plan.floatingCount != 0 {
		t.Fatalf("plan counts resident=%d floating=%d, want 2/0", plan.residentCount, plan.floatingCount)
	}
	if len(plan.desired) != 2 {
		t.Fatalf("desired = %v, want two residents and nobody else", plan.desired)
	}
	for uid, pin := range plan.pins {
		if pin.village != 39 || pin.area != 0 {
			t.Fatalf("resident uid=%d pinned to %d/%d, want 39/0", uid, pin.village, pin.area)
		}
		if pin.x < 0 || pin.x > 100 || pin.y < 0 || pin.y > 100 {
			t.Fatalf("resident uid=%d pinned outside the map rectangle: %+v", uid, pin)
		}
	}

	actors := leaseActorsToPool(t, supervisor, pool)
	supervisor.installDemandPlan(time.Now(), plan)
	for index, actor := range actors {
		uid := pool[index].UID
		want := plan.desired[uid]
		if got := actor.Snapshot().OnlineDesired; got != want {
			t.Fatalf("uid=%d onlineDesired = %t, want %t", uid, got, want)
		}
		if _, ok := supervisor.manager.demandLocationFor(uid); ok != want {
			t.Fatalf("uid=%d pin installed = %t, want %t", uid, ok, want)
		}
	}
}

func TestDemandPlanKeepsResidentPinsAcrossRebuilds(t *testing.T) {
	supervisor := demandTestSupervisor(t, nil)
	rc := robotconfig.Default()
	rc.ResidentPlan = "39/0:2;39/1:2"
	rc.AutoTargetOnlineCount = 4
	pool := demandTestPool(90, 90, 90, 90)

	rows := supervisor.residentRows(rc)
	first := supervisor.buildDemandPlan(rows, shared.PlayerLocation{}, false, pool, rc)
	supervisor.installDemandPlan(time.Now(), first)

	// The pool comes back in a different order on the next tick; the crowd must
	// not walk across the square because of it.
	shuffled := []robotcap.Info{pool[3], pool[1], pool[0], pool[2]}
	second := supervisor.buildDemandPlan(rows, shared.PlayerLocation{}, false, shuffled, rc)
	for uid, pin := range first.pins {
		if second.pins[uid] != pin {
			t.Fatalf("uid=%d moved on rebuild: %+v -> %+v", uid, pin, second.pins[uid])
		}
	}
	if len(second.pins) != len(first.pins) {
		t.Fatalf("pins = %d, want %d", len(second.pins), len(first.pins))
	}
}

func TestDemandPlanFloatingLayerPrefersThePlayersLevel(t *testing.T) {
	supervisor := demandTestSupervisor(t, nil)
	rc := robotconfig.Default()
	rc.ResidentPlan = "39/0:1"
	rc.AutoTargetOnlineCount = 4
	pool := demandTestPool(20, 85, 90, 88, 70)
	player := shared.PlayerLocation{Village: 39, Area: 0, X: 50, Y: 50, Level: 90}

	rows := supervisor.residentRows(rc)
	plan := supervisor.buildDemandPlan(rows, player, true, pool, rc)
	if plan.residentCount != 1 || plan.floatingCount != 3 {
		t.Fatalf("plan counts resident=%d floating=%d, want 1/3", plan.residentCount, plan.floatingCount)
	}
	// The one resident is the first robot in uid order; the floating budget is
	// the three robots closest to the player's level.
	floating := make(map[int]bool, plan.floatingCount)
	for uid := range plan.desired {
		if _, pinned := plan.pins[uid]; !pinned {
			t.Fatal("an online robot must be pinned somewhere")
		}
		if uid != pool[0].UID {
			floating[uid] = true
		}
	}
	for _, want := range []int{pool[2].UID, pool[3].UID, pool[1].UID} {
		if !floating[want] {
			t.Fatalf("floating layer missed uid=%d: %v", want, floating)
		}
	}
	if floating[pool[4].UID] {
		t.Fatalf("floating layer took a level-70 robot over level-85/88/90: %v", floating)
	}
	// A robot on the player's own map stands inside the follow radius, so the
	// player sees it rather than reading about it on another map.
	for uid := range floating {
		pin := plan.pins[uid]
		if pin.village != 39 || pin.area != 0 {
			t.Fatalf("uid=%d floated to %d/%d, want the player's map", uid, pin.village, pin.area)
		}
		if pin.x < 0 || pin.x > 100 || pin.y < 0 || pin.y > 100 {
			t.Fatalf("uid=%d stands outside the map: %+v", uid, pin)
		}
	}
}

func TestDemandPlanFloatingLayerStaysInsideThePlayersReach(t *testing.T) {
	crowd := &demandTestCrowd{distances: map[shared.MapAreaKey]int{
		{Village: 39, Area: 0}: 0,
		{Village: 39, Area: 1}: 2,
		{Village: 38, Area: 0}: 4,
	}}
	supervisor := demandTestSupervisor(t, crowd)
	rc := robotconfig.Default()
	rc.AutoTargetOnlineCount = 6
	pool := demandTestPool(90, 90, 90, 90, 90, 90)
	player := shared.PlayerLocation{Village: 39, Area: 0, X: 50, Y: 50, Level: 90}

	plan := supervisor.buildDemandPlan(nil, player, true, pool, rc)
	if plan.floatingCount != len(pool) {
		t.Fatalf("floating = %d, want %d", plan.floatingCount, len(pool))
	}
	areas := map[int]int{}
	for uid, pin := range plan.pins {
		if pin.village == 38 {
			t.Fatalf("uid=%d stood %d transitions out, past the layer's reach", uid, crowd.distances[shared.MapAreaKey{Village: 38, Area: 0}])
		}
		if pin.village == 30 {
			t.Fatalf("uid=%d stood on a map the walk never reached: %+v", uid, pin)
		}
		areas[pin.area]++
	}
	if areas[0] == 0 || areas[1] == 0 {
		t.Fatalf("the layer must fill every map in reach before moving out: %v", areas)
	}

	supervisor.installDemandPlan(time.Now(), plan)
	if crowd.village != 39 || crowd.area != 0 {
		t.Fatalf("player map = %d/%d, want 39/0", crowd.village, crowd.area)
	}
}

func TestDemandPlanFloatingLayerThinsWithDistance(t *testing.T) {
	crowd := &demandTestCrowd{distances: map[shared.MapAreaKey]int{
		{Village: 39, Area: 0}: 0,
		{Village: 39, Area: 1}: 1,
		{Village: 38, Area: 0}: 2,
	}}
	supervisor := demandTestSupervisor(t, crowd)
	rc := robotconfig.Default()
	rc.AutoTargetOnlineCount = 8
	pool := demandTestPool(90, 90, 90, 90, 90, 90, 90, 90)
	player := shared.PlayerLocation{Village: 39, Area: 0, X: 50, Y: 50, Level: 90}

	plan := supervisor.buildDemandPlan(nil, player, true, pool, rc)
	if plan.floatingCount != len(pool) {
		t.Fatalf("floating = %d, want %d", plan.floatingCount, len(pool))
	}
	// Each ring takes half of what is left, so the walk the player is standing on
	// carries the crowd and the ring two maps out carries a quarter of it.
	counts := map[shared.MapAreaKey]int{}
	for _, pin := range plan.pins {
		counts[shared.MapAreaKey{Village: pin.village, Area: pin.area}]++
	}
	for key, want := range map[shared.MapAreaKey]int{
		{Village: 39, Area: 0}: 4,
		{Village: 39, Area: 1}: 2,
		{Village: 38, Area: 0}: 2,
	} {
		if counts[key] != want {
			t.Fatalf("map %v holds %d, want %d (all: %v)", key, counts[key], want, counts)
		}
	}
}

func TestDemandPlanCappedMapSpillsOutward(t *testing.T) {
	crowd := &demandTestCrowd{distances: map[shared.MapAreaKey]int{
		{Village: 39, Area: 0}: 0,
		{Village: 39, Area: 1}: 1,
	}}
	supervisor := demandTestSupervisor(t, crowd)
	rc := robotconfig.Default()
	levels := make([]int, 100)
	for index := range levels {
		levels[index] = 90
	}
	rc.AutoTargetOnlineCount = len(levels)
	pool := demandTestPool(levels...)
	player := shared.PlayerLocation{Village: 39, Area: 0, X: 50, Y: 50, Level: 90}

	plan := supervisor.buildDemandPlan(nil, player, true, pool, rc)
	// Two maps at the cap hold ninety: the extra ten have nowhere to stand and
	// stay offline rather than piling onto a square nobody would believe.
	if want := 2 * demandMapCap; plan.floatingCount != want {
		t.Fatalf("floating = %d, want the reach's %d places", plan.floatingCount, want)
	}
	counts := map[shared.MapAreaKey]int{}
	for _, pin := range plan.pins {
		counts[shared.MapAreaKey{Village: pin.village, Area: pin.area}]++
	}
	// The player's map stops at the cap and the rest of the ring's share walks one
	// map out rather than piling onto the square.
	for _, key := range []shared.MapAreaKey{{Village: 39, Area: 0}, {Village: 39, Area: 1}} {
		if counts[key] != demandMapCap {
			t.Fatalf("map %v holds %d, want the cap of %d (all: %v)", key, counts[key], demandMapCap, counts)
		}
	}
}

func TestDemandPlanWithoutAPlanStandsDown(t *testing.T) {
	crowd := &demandTestCrowd{}
	supervisor := demandTestSupervisor(t, crowd)
	rc := robotconfig.Default()
	pool := demandTestPool(90, 90)
	player := shared.PlayerLocation{Village: 39, Area: 0, X: 50, Y: 50, Level: 90}

	plan := supervisor.buildDemandPlan(nil, player, true, pool, rc)
	supervisor.installDemandPlan(time.Now(), plan)
	if len(plan.desired) == 0 {
		t.Fatal("the layer pinned nobody to stand down")
	}
	if _, ok := supervisor.manager.demandLocationFor(pool[0].UID); !ok {
		t.Fatal("no pin installed")
	}

	// An empty plan and no follow account is the operator saying "plain fleet":
	// the layer must release the pins it held rather than keep the last plan.
	supervisor.applyOnlineDemand(time.Now(), rc)
	for _, robot := range pool {
		if _, ok := supervisor.manager.demandLocationFor(robot.UID); ok {
			t.Fatalf("uid=%d kept its pin after stand-down", robot.UID)
		}
	}
	if crowd.village != 0 || crowd.area != 0 {
		t.Fatalf("player map = %d/%d, want it cleared", crowd.village, crowd.area)
	}
	if uids := supervisor.demandUIDs(); uids != nil {
		t.Fatalf("standing down left a demand set: %v", uids)
	}
}

func TestDemandPlayerGraceHoldsTheFloatingLayer(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "")
	locator := &demandTestLocator{
		location: shared.PlayerLocation{Village: 39, Area: 0, X: 10, Y: 20, Level: 90},
		found:    true,
	}
	manager.SetBackendPlayerLocator(locator)
	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	rc := robotconfig.Default()
	rc.DemandPlayerPollSec = 5
	rc.DemandPlayerGraceSec = 600

	start := time.Now()
	if player, ok := supervisor.pollPlayer(start, rc, demandTestPlayer); !ok || player.Village != 39 {
		t.Fatalf("first poll = %+v ok=%t", player, ok)
	}
	if locator.calls != 1 {
		t.Fatalf("locator calls = %d, want 1", locator.calls)
	}
	// A second tick inside the poll interval reads the remembered position
	// instead of knocking on the database again.
	if _, ok := supervisor.pollPlayer(start.Add(time.Second), rc, demandTestPlayer); !ok {
		t.Fatal("the layer forgot the player between polls")
	}
	if locator.calls != 1 {
		t.Fatalf("locator calls = %d, want the poll interval to hold it at 1", locator.calls)
	}

	// The player closes the client: the read stops finding a character, and the
	// layer holds the world it built for a while instead of emptying instantly.
	locator.found = false
	if _, ok := supervisor.pollPlayer(start.Add(10*time.Second), rc, demandTestPlayer); !ok {
		t.Fatal("the layer emptied the moment the player stopped being found")
	}
	if _, ok := supervisor.pollPlayer(start.Add(9*time.Minute), rc, demandTestPlayer); !ok {
		t.Fatal("the layer emptied inside the grace window")
	}
	if _, ok := supervisor.pollPlayer(start.Add(11*time.Minute), rc, demandTestPlayer); ok {
		t.Fatal("the layer kept following a player who has been gone past the grace window")
	}
}
