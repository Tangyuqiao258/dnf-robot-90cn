package robotlifecycle

import (
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

func TestBuildProtocolRobotPlansReusesCommonNameAndSpawnPolicies(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 2, IDStart: 17000000, AccountPrefix: "acct", PasswordHash: "hash",
		Config:   robotconfig.RuntimeConfig{LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{2}, SpawnFallbackVillage: 1, SpawnArea: 3, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200},
		Names:    robottemplate.NameTemplates{Common: []string{"Alpha", "Beta"}},
		Maps:     []shared.MapCatalogItem{{Village: 1, Area: 3, Level: 1, Use: true, XMin: 90, XMax: 110, YMin: 190, YMax: 210}},
		JobGrows: map[int][]int{1: {2}},
	})
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	// first grow 2 (transfer branch) + second grow 1 (level 50 opens the first
	// awakening, not the second) = 0x12.
	if plans[0].Info.Job != 1 || plans[0].Info.Grow != 0x12 || plans[0].Info.Level != 50 || plans[0].Info.Area != 3 {
		t.Fatalf("first=%+v", plans[0])
	}
	if plans[0].Request.AccountName != "acct17000000" || plans[0].Request.RobotUID != 17000000 {
		t.Fatalf("request=%+v", plans[0].Request)
	}
	if plans[0].Info.Name == plans[1].Info.Name {
		t.Fatalf("duplicate names: %+v", plans)
	}
}

func TestBuildProtocolRobotPlansKeepsUntransferableJobsAtZero(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17000000, AccountPrefix: "acct",
		Config:   robotconfig.RuntimeConfig{LevelMin: 50, LevelMax: 50, Jobs: []int{9}, GrowTypes: []int{1, 2}, SpawnFallbackVillage: 1, SpawnArea: 3, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200},
		Names:    robottemplate.NameTemplates{Common: []string{"Alpha"}},
		JobGrows: map[int][]int{0: {1, 2}},
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	// Job 9 has no released transfer branch, so an awakening must not be planned.
	if plans[0].Info.Grow != 0 {
		t.Fatalf("untransferable job grow=%d", plans[0].Info.Grow)
	}
}

func TestBuildProtocolRobotPlansBalancesSpawnAreas(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 2, IDStart: 17000000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{LevelMin: 80, LevelMax: 80, Jobs: []int{0}, GrowTypes: []int{0}, SpawnFallbackVillage: 1, SpawnArea: 0},
		Names:  robottemplate.NameTemplates{Common: []string{"Alpha", "Beta"}},
		Maps: []shared.MapCatalogItem{
			{Village: 2, Area: 5, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
			{Village: 3, Area: 7, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	first, second := plans[0].Info, plans[1].Info
	if first.Village == second.Village && first.Area == second.Area {
		t.Fatalf("both plans landed in the same area: %d/%d and %d/%d", first.Village, first.Area, second.Village, second.Area)
	}
	if first.Village == 1 && first.Area == 0 {
		t.Fatalf("plan kept the fallback default instead of a catalog map: %+v", first)
	}
}

// planPolicy is a two-map world: 2/9 is an enchanting market, 4/1 is an
// ordinary map that comes first in the catalog, so a plan that lands on 2/9 can
// only have got there through the crowd rules.
type planPolicy struct{}

func (planPolicy) CrowdMapRule(village, area int) shared.CrowdMapRule {
	if village == 2 && area == 9 {
		return shared.CrowdMapRule{StallKind: shared.ExpertJobStoreEnchant, StallMax: 1}
	}
	return shared.CrowdMapRule{StallKind: shared.ExpertJobStoreDisjoint, StallMax: shared.CrowdStallUnlimited}
}

func planMarketMaps() []shared.MapCatalogItem {
	return []shared.MapCatalogItem{
		{Village: 4, Area: 1, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 2, Area: 9, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 200, XMax: 300, YMin: 0, YMax: 100}}},
	}
}

func TestBuildProtocolRobotPlansStationsTheTradeOnItsMarket(t *testing.T) {
	plan := func(t *testing.T, enchantRatio int) robotcap.Info {
		t.Helper()
		plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
			Count: 1, IDStart: 17001000, AccountPrefix: "acct",
			Config: robotconfig.RuntimeConfig{
				LevelMin: 80, LevelMax: 80, Jobs: []int{0}, GrowTypes: []int{0},
				SpawnFallbackVillage: 1, StoreEnchantRatioPercent: enchantRatio,
			},
			Names:       robottemplate.NameTemplates{Common: []string{"Alpha"}},
			Maps:        planMarketMaps(),
			CrowdPolicy: planPolicy{},
			RandIntn:    func(int) int { return 0 },
			RandBetween: func(min, max int) int { return min },
		})
		if err != nil || len(plans) != 1 {
			t.Fatalf("plans=%+v err=%v", plans, err)
		}
		return plans[0].Info
	}
	// uid 17001000 % 100 = 0, so a 19% share makes this robot an enchanter.
	if info := plan(t, 19); info.Village != 2 || info.Area != 9 {
		t.Fatalf("enchanter planned at %d/%d, want its market 2/9", info.Village, info.Area)
	}
	// Without a share the robot disassembles, and 2/9 is not its market.
	if info := plan(t, 0); info.Village == 2 && info.Area == 9 {
		t.Fatalf("disassembler planned on the enchanting market: %d/%d", info.Village, info.Area)
	}
}

func TestAwakeningForLevelFollowsTheLevelBands(t *testing.T) {
	for _, testCase := range []struct {
		level int
		want  int
	}{
		{level: 1, want: 0},
		{level: 49, want: 0},
		{level: 50, want: 1},
		{level: 74, want: 1},
		{level: 75, want: 2},
		{level: 90, want: 2},
	} {
		if got := AwakeningForLevel(testCase.level); got != testCase.want {
			t.Fatalf("level %d awakens at stage %d, want %d", testCase.level, got, testCase.want)
		}
	}
}

func TestSelectJobGrowthGatesTransferAndAwakeningByLevel(t *testing.T) {
	jobGrows := map[int][]int{0: {1, 2}}
	configured := []int{0, 1, 2}
	for _, testCase := range []struct {
		level  int
		branch int
		grow   int
	}{
		// Below the transfer gate the robot stays a base class, whatever the
		// ini's awakening pool says.
		{level: 10, branch: 0, grow: 0x00},
		// Transfer only: the awakening nibble stays empty until level 50.
		{level: 49, branch: 1, grow: 0x01},
		{level: 50, branch: 1, grow: 0x11},
		{level: 74, branch: 1, grow: 0x11},
		{level: 75, branch: 1, grow: 0x21},
		{level: 90, branch: 1, grow: 0x21},
	} {
		branch, grow := SelectJobGrowth(0, testCase.level, jobGrows, configured, nil, func(int) int { return 0 })
		if branch != testCase.branch || grow != testCase.grow {
			t.Fatalf("level %d grew branch=%d grow=0x%02X, want branch=%d grow=0x%02X",
				testCase.level, branch, grow, testCase.branch, testCase.grow)
		}
	}
}

// TestSelectJobGrowthSelfAwakensAJobWithNoBranch covers the 外传 classes: the
// archive releases no transfer branch for them, and their [awakening N] rows
// sit in the base block a grow_type of 0 selects, so the awakening nibble is
// theirs to take.
func TestSelectJobGrowthSelfAwakensAJobWithNoBranch(t *testing.T) {
	jobGrows := map[int][]int{9: {}, 10: {}}
	configured := []int{0, 1, 2}
	rows := func(job, first, stage int) bool {
		if job != 9 && job != 10 {
			return false
		}
		return first == 0 && stage >= 1 && stage <= 2
	}
	jobRows := func(job int) AwakeningAvailable {
		return func(first, stage int) bool { return rows(job, first, stage) }
	}
	for _, testCase := range []struct {
		level int
		grow  int
	}{
		{level: 49, grow: 0x00},
		{level: 50, grow: 0x10},
		{level: 74, grow: 0x10},
		{level: 75, grow: 0x20},
		{level: 90, grow: 0x20},
	} {
		branch, grow := SelectJobGrowth(9, testCase.level, jobGrows, configured, jobRows(9), func(int) int { return 0 })
		if branch != 0 || grow != testCase.grow {
			t.Fatalf("job 9 level %d grew branch=%d grow=0x%02X, want branch=0 grow=0x%02X",
				testCase.level, branch, grow, testCase.grow)
		}
	}
	// The same job without the archive's word on its awakening rows stays a
	// plain base class: an awakening the server cannot compute is worse than a
	// missing one.
	if _, grow := SelectJobGrowth(9, 90, jobGrows, configured, nil, func(int) int { return 0 }); grow != 0 {
		t.Fatalf("unverified job 9 grew 0x%02X, want 0x00", grow)
	}
}

// TestSelectJobGrowthDropsAnAwakeningTheArchiveDoesNotHave covers a released
// branch whose stage the .chr does not carry: the selector must fall back to
// the transfer alone rather than write a grow_type the server refuses.
func TestSelectJobGrowthDropsAnAwakeningTheArchiveDoesNotHave(t *testing.T) {
	jobGrows := map[int][]int{0: {1, 2}}
	configured := []int{2}
	rows := AwakeningAvailable(func(first, stage int) bool { return false })
	branch, grow := SelectJobGrowth(0, 90, jobGrows, configured, rows, func(int) int { return 0 })
	if branch != 1 || grow != 0x01 {
		t.Fatalf("branch=%d grow=0x%02X, want branch=1 grow=0x01", branch, grow)
	}
}

func TestBuildProtocolRobotPlansLevelsDungeonEntranceRobots(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17001000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 85, Jobs: []int{0}, GrowTypes: []int{0, 1, 2},
			SpawnFallbackVillage: 1,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha"}},
		Maps: []shared.MapCatalogItem{{
			Village: 39, Area: 0, Level: 1, Use: true, DungeonGate: true, DungeonLevel: 81,
			Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}},
		}},
		JobGrows:    map[int][]int{0: {1, 2}},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	// An entrance guard is leveled to its dungeon: entry 81 plus the smallest
	// draw of the 1..4 spread, not the level the plan sampled from 50..85.
	if plans[0].Info.Level != 82 {
		t.Fatalf("entrance guard level=%d, want 82", plans[0].Info.Level)
	}
	// 82 is past the second awakening gate, so the robot is grown for the level
	// it ended up with rather than the band it was drawn from.
	if plans[0].Info.Grow != 0x21 {
		t.Fatalf("entrance guard grow=0x%02X, want 0x21", plans[0].Info.Grow)
	}
}

// TestBuildProtocolRobotPlansNeverLowersAGateRobot covers the other half of the
// entrance rule. A map's DungeonLevel is the *lowest* entry requirement of the
// dungeons it opens, so the gate rewrite used to drag a robot drawn above the
// gate down to just past it -- a level-90 robot at a level-81 gate came out at
// 82..85, which is how the fleet lost its high-level members. The guard may
// only raise a level.
func TestBuildProtocolRobotPlansNeverLowersAGateRobot(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17001000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{
			LevelMin: 90, LevelMax: 90, Jobs: []int{0}, GrowTypes: []int{2},
			SpawnFallbackVillage: 1,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha"}},
		Maps: []shared.MapCatalogItem{{
			Village: 39, Area: 0, Level: 1, Use: true, DungeonGate: true, DungeonLevel: 81,
			Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}},
		}},
		JobGrows:    map[int][]int{0: {1}},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return max },
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	// The widest draw of the 1..4 spread still lands at 85, below the level the
	// robot was drawn with, so the level must survive untouched.
	if plans[0].Info.Level != 90 {
		t.Fatalf("level-90 gate robot planned level=%d, want 90", plans[0].Info.Level)
	}
}

func TestBuildProtocolRobotPlansKeepsTheDrawnLevelAwayFromDungeons(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17001000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{0}, GrowTypes: []int{2},
			SpawnFallbackVillage: 1,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha"}},
		Maps: []shared.MapCatalogItem{{
			Village: 4, Area: 1, Level: 1, Use: true,
			Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}},
		}},
		JobGrows:    map[int][]int{0: {1}},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	if plans[0].Info.Level != 50 || plans[0].Info.Grow != 0x11 {
		t.Fatalf("ordinary map planned level=%d grow=0x%02X, want 50/0x11", plans[0].Info.Level, plans[0].Info.Grow)
	}
}

// TestBuildProtocolRobotPlansDrawsLevelsFromThePlan covers the create path the
// supervisor actually uses: one plan per robot, each with its own uid. The
// level has to come from the plan's pool by uid, so the composition holds
// however many times provisioning runs and wherever a restart lands.
func TestBuildProtocolRobotPlansDrawsLevelsFromThePlan(t *testing.T) {
	optionFor := func(t *testing.T, uid int) ProtocolPlanOptions {
		t.Helper()
		return ProtocolPlanOptions{
			Count: 1, IDStart: uid, AccountPrefix: "acct",
			Config: robotconfig.RuntimeConfig{
				LevelMin: 50, LevelMax: 85, LevelPlan: "90-90:2,20-24:2",
				RobotUIDStart: 17001000, RobotUIDEnd: 17001999,
				Jobs: []int{0}, GrowTypes: []int{2}, SpawnFallbackVillage: 1,
			},
			Names: robottemplate.NameTemplates{Common: []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta"}},
			Maps: []shared.MapCatalogItem{{
				Village: 4, Area: 1, Level: 1, Use: true,
				Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}},
			}},
			JobGrows:    map[int][]int{0: {1}},
			RandIntn:    func(int) int { return 0 },
			RandBetween: func(min, max int) int { return min },
		}
	}
	// The pool deals the bands round-robin, so the uids alternate between them
	// instead of handing the first ninety-nine robots the top band.
	want := []int{90, 20, 90, 20, 90, 20}
	for offset, level := range want {
		uid := 17001000 + offset
		plans, err := BuildProtocolRobotPlans(optionFor(t, uid))
		if err != nil || len(plans) != 1 {
			t.Fatalf("uid=%d plans=%+v err=%v", uid, plans, err)
		}
		if plans[0].Info.Level != level {
			t.Fatalf("uid=%d level=%d want %d", uid, plans[0].Info.Level, level)
		}
	}
	// The plan outranks level_min..level_max even when it points outside them:
	// it is the operator's table, not a hint.
	if plans, err := BuildProtocolRobotPlans(optionFor(t, 17001001)); err != nil || plans[0].Info.Level != 20 {
		t.Fatalf("level 20 is below level_min 50 and was not planned: %+v err=%v", plans, err)
	}
}

// TestBuildProtocolRobotPlansKeepsThePlanOverTheEntranceGuard covers the clash
// between the two level rules. 赫顿玛尔's main town -- where 凯丽 and 风振 stand
// and where the fleet keeps its resident crowd -- is itself a dungeon gate with
// DungeonLevel 81, so the entrance guard lifted every low-level robot placed
// there to 82..85 and drained the plan's low bands. A configured plan owns the
// level; the guard stays for configs that have none.
func TestBuildProtocolRobotPlansKeepsThePlanOverTheEntranceGuard(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17001000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 85, LevelPlan: "20-24:1",
			RobotUIDStart: 17001000, RobotUIDEnd: 17001999,
			Jobs: []int{0}, GrowTypes: []int{0, 1, 2}, SpawnFallbackVillage: 1,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha"}},
		Maps: []shared.MapCatalogItem{{
			Village: 39, Area: 0, Level: 1, Use: true, DungeonGate: true, DungeonLevel: 81,
			Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}},
		}},
		JobGrows:    map[int][]int{0: {1, 2}},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	if plans[0].Info.Village != 39 || plans[0].Info.Area != 0 {
		t.Fatalf("the robot was not placed on the gate map: %+v", plans[0].Info)
	}
	if plans[0].Info.Level != 20 {
		t.Fatalf("entrance guard rewrote the planned level to %d, want 20", plans[0].Info.Level)
	}
}

// TestBuildProtocolRobotPlansRejectsABrokenLevelPlan keeps a typo in the ini
// from quietly dressing the fleet on the uniform draw instead.
func TestBuildProtocolRobotPlansRejectsABrokenLevelPlan(t *testing.T) {
	_, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17001000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 85, LevelPlan: "90-89:5",
			RobotUIDStart: 17001000, RobotUIDEnd: 17001999,
			Jobs: []int{0}, GrowTypes: []int{0}, SpawnFallbackVillage: 1,
		},
		Names:       robottemplate.NameTemplates{Common: []string{"Alpha"}},
		JobGrows:    map[int][]int{0: {1}},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err == nil {
		t.Fatal("a broken level plan was accepted")
	}
}

func TestBuildProtocolRobotPlansHonoursSeededOccupancy(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17000000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{LevelMin: 80, LevelMax: 80, Jobs: []int{0}, GrowTypes: []int{0}, SpawnFallbackVillage: 1, SpawnArea: 0},
		Names:  robottemplate.NameTemplates{Common: []string{"Alpha"}},
		Maps: []shared.MapCatalogItem{
			{Village: 2, Area: 5, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
			{Village: 3, Area: 7, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		},
		Locations:   []shared.MapLocation{{Village: 2, Area: 5, X: 1, Y: 1}},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	if plans[0].Info.Village != 3 || plans[0].Info.Area != 7 {
		t.Fatalf("occupied area was not avoided: %+v", plans[0].Info)
	}
}
