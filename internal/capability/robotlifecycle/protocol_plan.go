package robotlifecycle

import (
	"fmt"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

type ProtocolPlanOptions struct {
	Count         int
	IDStart       int
	AccountPrefix string
	PasswordHash  string
	Config        robotconfig.RuntimeConfig
	Names         robottemplate.NameTemplates
	Maps          []shared.MapCatalogItem
	// Locations is the current robot directory occupancy. Non-fixed spawns
	// use it to pick the least crowded usable area instead of sampling raw
	// map-catalog entries.
	Locations []shared.MapLocation
	JobGrows  map[int][]int
	// AwakeningRows reports whether a job's .chr carries the [awakening stage]
	// row that a grow_type of first|stage<<4 selects. It is what lets the 外传
	// classes awaken: the archive releases no transfer branch for them, so only
	// the stat table can say the awakening is real. A plan built without it
	// keeps the conservative rule and never awakens an untransferred robot.
	AwakeningRows func(job, first, stage int) bool
	// CrowdPolicy lets the plan station a new robot on a map the world's rules
	// reserve for its trade. A plan built without one spreads robots exactly as
	// it did before the rules existed.
	CrowdPolicy shared.CrowdMapPolicy
	NameExists  func(string) bool
	RandIntn    func(int) int
	RandBetween func(int, int) int
}

type ProtocolRobotPlan struct {
	Info    robotcap.Info
	Request shared.ProvisionCharacterRequest
}

func BuildProtocolRobotPlans(options ProtocolPlanOptions) ([]ProtocolRobotPlan, error) {
	if options.Count <= 0 || options.Count > 1000 {
		return nil, fmt.Errorf("protocol plan count must be between 1 and 1000")
	}
	if options.IDStart <= 0 {
		return nil, fmt.Errorf("protocol plan id start must be positive")
	}
	if len(options.Config.Jobs) == 0 {
		return nil, fmt.Errorf("protocol plan requires at least one job")
	}
	if len(options.Config.GrowTypes) == 0 {
		options.Config.GrowTypes = []int{0}
	}
	if options.AccountPrefix == "" {
		options.AccountPrefix = "robot"
	}
	levels := options.Config.LevelMin
	if levels <= 0 {
		levels = 1
	}
	levelMax := options.Config.LevelMax
	if levelMax < levels {
		levelMax = levels
	}
	plans := make([]ProtocolRobotPlan, 0, options.Count)
	used := make(map[string]struct{}, options.Count)
	env := protocolPlanRandom{randIntn: options.RandIntn, randBetween: options.RandBetween, policy: options.CrowdPolicy}
	spawnMaps := options.Maps
	if !options.Config.SpawnFixed {
		spawnMaps = robotspawn.NormalMaps(options.Maps)
	}
	for index := 0; index < options.Count; index++ {
		uid := options.IDStart + index
		job := chooseInt(options.Config.Jobs, options.RandIntn)
		level := env.RandBetween(levels, levelMax)
		info := robotcap.Info{UID: uid, Level: level, Job: job, Port: 0, Village: options.Config.SpawnFallbackVillage, Area: options.Config.SpawnArea, X: options.Config.SpawnXMin, Y: options.Config.SpawnYMin}
		if options.Config.SpawnFixed {
			// Fixed spawns are applied again at online time; keep the legacy
			// catalog sample so the plan still carries a plausible position.
			if mp, ok := robotspawn.RandomMap(env, options.Maps, level); ok {
				info.Village, info.Area = mp.Village, mp.Area
				if x, y, pointOK := robotspawn.RandomPointInMap(env, mp); pointOK {
					info.X, info.Y = x, y
				}
			}
		} else if target, ok := robotspawn.BalancedFamilyLocation(env, spawnMaps, level, options.Locations, shared.MapAreaKey{}, shared.ExpertJobStoreKindForUID(uid, options.Config.StoreEnchantRatioPercent)); ok {
			info.Village, info.Area = target.Map.Village, target.Map.Area
			info.X, info.Y = target.X, target.Y
			options.Locations = append(options.Locations, shared.MapLocation{Village: info.Village, Area: info.Area, X: info.X, Y: info.Y})
		} else if mp, ok := robotspawn.RandomMap(env, spawnMaps, level); ok {
			info.Village, info.Area = mp.Village, mp.Area
			if x, y, pointOK := robotspawn.RandomPointInMap(env, mp); pointOK {
				info.X, info.Y = x, y
			}
		}
		// A robot standing at a dungeon entrance is the crowd a player walks past
		// on the way in, so it is leveled to the dungeon it guards: a little above
		// the entry requirement, never below it. The guard raises a level, it does
		// not set one: the entry level is the dungeon's *lowest*, so writing it
		// outright dragged a level-90 robot standing at a level-81 gate down to
		// 82..85. Only a robot drawn below the gate is lifted to it.
		if dungeonLevel := dungeonLevelOf(options.Maps, info.Village, info.Area); dungeonLevel > 0 {
			if guardLevel := dungeonLevel + env.RandBetween(1, 4); guardLevel > info.Level {
				info.Level = guardLevel
			}
		}
		// The transfer and the awakening follow the level the robot ended up
		// with, so an entrance guard is grown like the character its level says
		// it is rather than like the level it was drawn with.
		awakeningRows := AwakeningAvailable(nil)
		if options.AwakeningRows != nil {
			awakeningRows = func(first, stage int) bool { return options.AwakeningRows(job, first, stage) }
		}
		firstGrow, grow := SelectJobGrowth(job, info.Level, options.JobGrows, options.Config.GrowTypes, awakeningRows, env.RandIntn)
		info.Grow = grow
		info.Name = robottemplate.AllocateName(uid, job, firstGrow, used, options.Config, options.Names, options.NameExists, options.RandBetween)
		plans = append(plans, ProtocolRobotPlan{Info: info, Request: shared.ProvisionCharacterRequest{AccountName: fmt.Sprintf("%s%d", options.AccountPrefix, uid), PasswordHash: options.PasswordHash, CharacterName: info.Name, Job: job, RobotUID: uid}})
	}
	return plans, nil
}

// dungeonLevelOf returns the entry level of the dungeon the map at village/area
// opens, or zero when that map opens no dungeon whose entry level is known.
func dungeonLevelOf(maps []shared.MapCatalogItem, village, area int) int {
	for _, mp := range maps {
		if mp.Village == village && mp.Area == area {
			return mp.DungeonLevel
		}
	}
	return 0
}

type protocolPlanRandom struct {
	randIntn    func(int) int
	randBetween func(int, int) int
	policy      shared.CrowdMapPolicy
}

func (protocolPlanRandom) FollowAccountVillage(string) (int, bool, error) { return 0, false, nil }

// CrowdMapRule keeps a planned spawn out of the maps the crowd rules close and
// out of the markets another robot already holds.
func (r protocolPlanRandom) CrowdMapRule(village, area int) shared.CrowdMapRule {
	if r.policy == nil {
		return shared.CrowdMapRule{}
	}
	return r.policy.CrowdMapRule(village, area)
}

func (r protocolPlanRandom) RandIntn(n int) int {
	if n <= 0 || r.randIntn == nil {
		return 0
	}
	v := r.randIntn(n)
	if v < 0 || v >= n {
		return 0
	}
	return v
}
func (r protocolPlanRandom) RandBetween(min, max int) int {
	if max <= min || r.randBetween == nil {
		return min
	}
	return r.randBetween(min, max)
}
func chooseInt(values []int, randIntn func(int) int) int {
	if len(values) == 0 {
		return 0
	}
	if randIntn == nil {
		return values[0]
	}
	index := randIntn(len(values))
	if index < 0 || index >= len(values) {
		index = 0
	}
	return values[index]
}

// Level gates of the game's growth path: a character may transfer at
// TransferLevel, awaken once at FirstAwakeningLevel and a second time at
// SecondAwakeningLevel. A fleet grown along these bands looks like characters
// that actually played instead of a heap of base classes.
const (
	TransferLevel        = 15
	FirstAwakeningLevel  = 50
	SecondAwakeningLevel = 75
)

// AwakeningForLevel returns the awakening stage a level opens: none below the
// first gate, the first awakening from it, and the second from the second gate.
func AwakeningForLevel(level int) int {
	switch {
	case level >= SecondAwakeningLevel:
		return 2
	case level >= FirstAwakeningLevel:
		return 1
	default:
		return 0
	}
}

// AwakeningStagesForLevel narrows a configured awakening pool to the stage the
// level band opens. Only a robot below every gate keeps the configured pool,
// which is the one case the ini still decides.
func AwakeningStagesForLevel(level int, configured []int) []int {
	stage := AwakeningForLevel(level)
	if stage <= 0 {
		return configured
	}
	return []int{stage}
}

// AwakeningAvailable reports whether one [awakening stage] row exists for a
// transfer branch. The archive is the only authority on it, because the two
// ways a character ends up at branch 0 differ: a job the archive releases no
// transfer branch for can still awaken on its own — the 外传职业 keep their
// awakening rows in the base [growtype 1] block, which is the block a
// grow_type of 0 selects — while a job whose branch list is empty for any other
// reason has no awakening row at all. A nil predicate keeps the conservative
// rule: an awakening only on a released branch.
type AwakeningAvailable func(first, stage int) bool

// SelectJobGrowth picks the transfer branch and the packed grow_type for one
// robot at a given level. The level gates both steps: below TransferLevel the
// robot stays a base class, and the awakening stage follows AwakeningForLevel.
// The server rejects an awakening it cannot compute stats for, so `available`
// has the last word on whether the stage stands. The first return value is the
// transfer branch used by name templates.
func SelectJobGrowth(job, level int, jobGrows map[int][]int, growTypes []int, available AwakeningAvailable, randIntn func(int) int) (int, int) {
	if level < TransferLevel {
		return 0, 0
	}
	return SelectBranchGrowth(jobGrows[job], AwakeningStagesForLevel(level, growTypes), available, randIntn)
}

// SelectBranchGrowth is SelectJobGrowth for a job whose released branch list is
// already resolved.
func SelectBranchGrowth(branches []int, growTypes []int, available AwakeningAvailable, randIntn func(int) int) (int, int) {
	first := chooseFirstGrow(branches, randIntn)
	awakening := chooseInt(growTypes, randIntn)
	if awakening < 0 {
		awakening = 0
	}
	if awakening > 2 {
		awakening = 2
	}
	if awakening > 0 && !awakeningReleased(branches, first, awakening, available) {
		awakening = 0
	}
	return first, (awakening << 4) | (first & 0x0F)
}

// awakeningReleased reports whether the archive can grow branch `first` to
// `stage`. A predicate from the archive decides it outright; without one, the
// branch list is all that is known, and a job whose branches are released has
// an awakening row under each of them while an untransferred character has
// none.
func awakeningReleased(branches []int, first, stage int, available AwakeningAvailable) bool {
	if available != nil {
		return available(first, stage)
	}
	return first != 0 && len(branches) > 0
}

// chooseFirstGrow picks a transfer branch from the job's PVF grow catalog. The
// server's CharacterStatComputer guards the first nibble to 0..5, so malformed
// or out-of-range branches fall back to "not transferred".
func chooseFirstGrow(branches []int, randIntn func(int) int) int {
	if len(branches) == 0 {
		return 0
	}
	index := 0
	if randIntn != nil {
		index = randIntn(len(branches))
		if index < 0 || index >= len(branches) {
			index = 0
		}
	}
	branch := branches[index]
	if branch < 1 || branch > 5 {
		return 0
	}
	return branch
}
