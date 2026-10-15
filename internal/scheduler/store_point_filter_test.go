package scheduler

import (
	"testing"

	robotcap "robot/internal/capability/robot"
	storecap "robot/internal/capability/store"
	"robot/internal/shared"
)

// stallRuleCrowd answers with one rule per map and the unrestricted rule
// everywhere else, which is how the 90CN rules behave: a map either hosts the
// kind of stall or it does not.
type stallRuleCrowd struct {
	rules map[shared.MapAreaKey]shared.CrowdMapRule
	calls int
}

func (c *stallRuleCrowd) CrowdMapRule(village, area int) shared.CrowdMapRule {
	c.calls++
	return c.rules[shared.MapAreaKey{Village: village, Area: area}]
}

func newStallRuleCrowd(rules map[shared.MapAreaKey]shared.CrowdMapRule) *stallRuleCrowd {
	return &stallRuleCrowd{rules: rules}
}

// A store round must never be handed a point whose map refuses the stall the
// round is about to open. Claiming one ended the round on the spot, which is
// what most of the store failure counter was made of.
func TestStorePointAllowedDropsMapsThatRefuseTheKind(t *testing.T) {
	normal := shared.MapAreaKey{Village: 38, Area: 6}
	seriaExit := shared.MapAreaKey{Village: 39, Area: 3}
	closed := shared.MapAreaKey{Village: 30, Area: 1}
	crowd := newStallRuleCrowd(map[shared.MapAreaKey]shared.CrowdMapRule{
		normal:    {StallKind: shared.ExpertJobStoreDisjoint, StallMax: shared.CrowdStallUnlimited},
		seriaExit: {StallKind: shared.ExpertJobStoreEnchant, StallMax: 1},
		closed:    {Closed: true},
	})

	manager := testRobotManagerWithConfig(t, "")
	manager.SetCrowdMapPolicy(crowd)
	runtime := &RobotRuntime{manager: manager}
	info := robotcap.Info{UID: 17001001}

	enchant := runtime.storePointAllowed(info, shared.ExpertJobStoreEnchant)
	if enchant(storecap.Position{Village: normal.Village, Area: normal.Area}) {
		t.Fatal("an ordinary map admitted an enchant stall the map would refuse")
	}
	if !enchant(storecap.Position{Village: seriaExit.Village, Area: seriaExit.Area}) {
		t.Fatal("the seria exit refused an enchant stall")
	}
	if enchant(storecap.Position{Village: closed.Village, Area: closed.Area}) {
		t.Fatal("a closed map admitted a stall")
	}

	disjoint := runtime.storePointAllowed(info, shared.ExpertJobStoreDisjoint)
	if !disjoint(storecap.Position{Village: normal.Village, Area: normal.Area}) {
		t.Fatal("an ordinary map refused a disassembler stall")
	}
	if disjoint(storecap.Position{Village: seriaExit.Village, Area: seriaExit.Area}) {
		t.Fatal("the seria exit admitted a disassembler stall")
	}
}

// The point pool walks every candidate of a map in turn, so the map's verdict is
// asked once and kept: a round pays one check per map it considers.
func TestStorePointAllowedAsksEachMapOnce(t *testing.T) {
	key := shared.MapAreaKey{Village: 38, Area: 0}
	crowd := newStallRuleCrowd(map[shared.MapAreaKey]shared.CrowdMapRule{
		key: {StallKind: shared.ExpertJobStoreDisjoint, StallMax: shared.CrowdStallUnlimited},
	})

	manager := testRobotManagerWithConfig(t, "")
	manager.SetCrowdMapPolicy(crowd)
	runtime := &RobotRuntime{manager: manager}

	allowed := runtime.storePointAllowed(robotcap.Info{UID: 17001001}, shared.ExpertJobStoreDisjoint)
	for point := 0; point < 5; point++ {
		if !allowed(storecap.Position{Village: key.Village, Area: key.Area, X: point, Y: point}) {
			t.Fatalf("candidate %d refused", point)
		}
	}
	if crowd.calls != 1 {
		t.Fatalf("crowd rule asked %d times for one map, want 1", crowd.calls)
	}
}
