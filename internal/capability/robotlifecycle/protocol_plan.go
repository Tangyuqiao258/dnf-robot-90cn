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
		firstGrow, grow := SelectJobGrowth(job, options.JobGrows, options.Config.GrowTypes, env.RandIntn)
		level := env.RandBetween(levels, levelMax)
		name := robottemplate.AllocateName(uid, job, firstGrow, used, options.Config, options.Names, options.NameExists, options.RandBetween)
		info := robotcap.Info{UID: uid, Name: name, Level: level, Job: job, Grow: grow, Port: 0, Village: options.Config.SpawnFallbackVillage, Area: options.Config.SpawnArea, X: options.Config.SpawnXMin, Y: options.Config.SpawnYMin}
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
		plans = append(plans, ProtocolRobotPlan{Info: info, Request: shared.ProvisionCharacterRequest{AccountName: fmt.Sprintf("%s%d", options.AccountPrefix, uid), PasswordHash: options.PasswordHash, CharacterName: name, Job: job, RobotUID: uid}})
	}
	return plans, nil
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

// SelectJobGrowth picks the transfer branch and the packed grow_type for one
// robot. grow_types selects the awakening stage (0..2); the server rejects an
// awakening without a transfer, so an untransferable job stays at 0. The first
// return value is the transfer branch used by name templates.
func SelectJobGrowth(job int, jobGrows map[int][]int, growTypes []int, randIntn func(int) int) (int, int) {
	return SelectBranchGrowth(jobGrows[job], growTypes, randIntn)
}

// SelectBranchGrowth is SelectJobGrowth for a job whose released branch list is
// already resolved.
func SelectBranchGrowth(branches []int, growTypes []int, randIntn func(int) int) (int, int) {
	first := chooseFirstGrow(branches, randIntn)
	awakening := chooseInt(growTypes, randIntn)
	if awakening < 0 {
		awakening = 0
	}
	if awakening > 2 {
		awakening = 2
	}
	if first == 0 {
		awakening = 0
	}
	return first, (awakening << 4) | (first & 0x0F)
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
