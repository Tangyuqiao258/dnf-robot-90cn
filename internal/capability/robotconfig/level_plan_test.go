package robotconfig

import (
	"reflect"
	"testing"
)

func TestParseLevelPlanReadsBands(t *testing.T) {
	bands, err := ParseLevelPlan(" 90-90:45 , 85-89:30 ,15-29:10")
	if err != nil {
		t.Fatal(err)
	}
	want := []LevelBand{
		{Min: 90, Max: 90, Count: 45},
		{Min: 85, Max: 89, Count: 30},
		{Min: 15, Max: 29, Count: 10},
	}
	if !reflect.DeepEqual(bands, want) {
		t.Fatalf("bands=%+v want %+v", bands, want)
	}
	// A single level is a band of one, not a range that happens to be closed.
	if bands[0].Min != bands[0].Max {
		t.Fatalf("90-90 read as %d..%d", bands[0].Min, bands[0].Max)
	}
}

func TestParseLevelPlanAcceptsASingleLevelRow(t *testing.T) {
	bands, err := ParseLevelPlan("90:45")
	if err != nil {
		t.Fatal(err)
	}
	if len(bands) != 1 || bands[0].Min != 90 || bands[0].Max != 90 || bands[0].Count != 45 {
		t.Fatalf("bands=%+v", bands)
	}
}

func TestParseLevelPlanTreatsAnEmptySpecAsNoPlan(t *testing.T) {
	bands, err := ParseLevelPlan("   ")
	if err != nil || bands != nil {
		t.Fatalf("bands=%+v err=%v", bands, err)
	}
}

func TestParseLevelPlanRejectsMalformedRows(t *testing.T) {
	for _, spec := range []string{
		"90-90",       // no count
		"90-90:",      // empty count
		"90-90:0",     // no robots
		"90-90:-3",    // negative count
		"90-89:5",     // inverted range
		"0-10:5",      // below level 1
		"250-260:5",   // above level 255
		"ninety:5",    // not a number
		"90-90:45,",   // trailing separator
		"90-90:x5",    // not a number
		"90-90:45:-1", // stray separator
	} {
		if _, err := ParseLevelPlan(spec); err == nil {
			t.Fatalf("spec %q was accepted", spec)
		}
	}
}

func TestLevelPoolSizeSumsTheBands(t *testing.T) {
	bands, err := ParseLevelPlan("90-90:45,85-89:30,15-29:10")
	if err != nil {
		t.Fatal(err)
	}
	if got := LevelPoolSize(bands); got != 85 {
		t.Fatalf("size=%d want 85", got)
	}
}

// TestLevelPoolDealsOnePerBandPerRound covers the property the online fleet
// depends on: the robots log in in uid order and there are far fewer slots than
// characters, so a pool written band by band would put its whole first hundred
// in the top band. Every prefix has to look like the plan.
func TestLevelPoolDealsOnePerBandPerRound(t *testing.T) {
	bands, err := ParseLevelPlan("90-90:3,80-84:2,20-24:1")
	if err != nil {
		t.Fatal(err)
	}
	pool := LevelPool(bands, func(min, max int) int { return max })
	want := []int{90, 84, 24, 90, 84, 90}
	if !reflect.DeepEqual(pool, want) {
		t.Fatalf("pool=%v want %v", pool, want)
	}
	// The smallest band runs out first and is simply skipped from then on.
	counts := map[int]int{}
	for _, level := range pool {
		counts[level]++
	}
	if counts[90] != 3 || counts[84] != 2 || counts[24] != 1 {
		t.Fatalf("band counts=%v", counts)
	}
}

func TestLevelPoolDrawsEachLevelFromItsBand(t *testing.T) {
	bands, err := ParseLevelPlan("85-89:4")
	if err != nil {
		t.Fatal(err)
	}
	// The draw is handed the band's own bounds and is free to return anything
	// between them: the pool must not narrow a band to a single level.
	draws := []int{85, 86, 88, 89}
	index := 0
	pool := LevelPool(bands, func(min, max int) int {
		if min != 85 || max != 89 {
			t.Fatalf("draw called with %d..%d, want the band 85..89", min, max)
		}
		level := draws[index]
		index++
		return level
	})
	if !reflect.DeepEqual(pool, draws) {
		t.Fatalf("pool=%v want %v", pool, draws)
	}
}

// TestLevelPoolIndexIsAnchoredAtTheUIDOrigin covers the resume case: the
// provisioner creates one robot at a time and restarts, so the level a uid
// draws must depend on the uid alone and not on how many robots came before it.
func TestLevelPoolIndexIsAnchoredAtTheUIDOrigin(t *testing.T) {
	for _, testCase := range []struct {
		uid    int
		origin int
		size   int
		want   int
	}{
		{uid: 17001000, origin: 17001000, size: 300, want: 0},
		{uid: 17001299, origin: 17001000, size: 300, want: 299},
		{uid: 17001300, origin: 17001000, size: 300, want: 0},
		{uid: 17001500, origin: 17001000, size: 300, want: 200},
		// A uid below the origin, or a config that names no origin at all, still
		// has to land inside the pool.
		{uid: 17000999, origin: 17001000, size: 300, want: 299},
		{uid: 17001005, origin: 0, size: 300, want: 17001005 % 300},
		{uid: 17001005, origin: 17001000, size: 0, want: 0},
	} {
		if got := LevelPoolIndex(testCase.uid, testCase.origin, testCase.size); got != testCase.want {
			t.Fatalf("uid=%d origin=%d size=%d got %d want %d",
				testCase.uid, testCase.origin, testCase.size, got, testCase.want)
		}
	}
}

func TestPoolCapacityFallsBackToTheOnlineTarget(t *testing.T) {
	target := RuntimeConfig{AutoTargetOnlineCount: 100, MaxOnlineRobots: 10000}
	if got := PoolCapacity(target); got != 100 {
		t.Fatalf("pool=%d want the online target 100", got)
	}
	// A pool below the online target cannot be honoured: every actor slot needs
	// a character.
	if got := PoolCapacity(RuntimeConfig{AutoTargetOnlineCount: 100, MaxOnlineRobots: 10000, PoolSize: 40}); got != 100 {
		t.Fatalf("pool=%d want the online target 100", got)
	}
	if got := PoolCapacity(RuntimeConfig{AutoTargetOnlineCount: 100, MaxOnlineRobots: 10000, PoolSize: 300}); got != 300 {
		t.Fatalf("pool=%d want 300", got)
	}
	// The online target is itself capped by max_online_robots, and the pool
	// follows the capped value rather than the raw ini number.
	if got := PoolCapacity(RuntimeConfig{AutoTargetOnlineCount: 500, MaxOnlineRobots: 120, PoolSize: 300}); got != 300 {
		t.Fatalf("pool=%d want 300", got)
	}
}

func TestLoadReadsLevelPlanAndPoolSize(t *testing.T) {
	rc, err := Parse("[create]\nlevel_plan = 90-90:45,85-89:30\npool_size = 300\nrobot_uid_start = 17001000\nrobot_uid_end = 17001999\n")
	if err != nil {
		t.Fatal(err)
	}
	if rc.LevelPlan != "90-90:45,85-89:30" || rc.PoolSize != 300 {
		t.Fatalf("level_plan=%q pool_size=%d", rc.LevelPlan, rc.PoolSize)
	}
}

func TestValidationRejectsABrokenLevelPlan(t *testing.T) {
	if _, err := Parse("[create]\nlevel_plan = 90-89:5\n"); err == nil {
		t.Fatal("an inverted level range was accepted")
	}
	if _, err := Parse("[create]\nlevel_plan = 90-90\n"); err == nil {
		t.Fatal("a level plan without counts was accepted")
	}
}

// TestValidationRejectsAPoolWiderThanTheUIDRange covers the configuration that
// would look fine until the fleet needed its last fifty robots: the uid range,
// not pool_size, is what bounds how many characters can exist.
func TestValidationRejectsAPoolWiderThanTheUIDRange(t *testing.T) {
	_, err := Parse("[create]\nrobot_uid_start = 17001000\nrobot_uid_end = 17001999\npool_size = 1001\n")
	if err == nil {
		t.Fatal("a pool wider than the uid range was accepted")
	}
	if _, err := Parse("[create]\nrobot_uid_start = 17001000\nrobot_uid_end = 17001999\npool_size = 1000\n"); err != nil {
		t.Fatalf("a pool exactly as wide as the uid range was rejected: %v", err)
	}
}
