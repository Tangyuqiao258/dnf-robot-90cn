package pvf

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// StatVector is one growth row of a .chr file. Values are the file's raw
// numbers scaled by ten, matching the server's CharacterStatComputer.
type StatVector struct {
	HpMax, MpMax, PhysAtk, PhysDef, MagAtk, MagDef int
	FireRes, WaterRes, DarkRes, LightRes           int
	InventoryLimit, HpRegen, MpRegen, MoveSpeed    int
	AttackSpeed, CastSpeed, HitRecovery, JumpPower int
	Weight                                         int
}

func (v *StatVector) add(other StatVector) {
	v.HpMax += other.HpMax
	v.MpMax += other.MpMax
	v.PhysAtk += other.PhysAtk
	v.PhysDef += other.PhysDef
	v.MagAtk += other.MagAtk
	v.MagDef += other.MagDef
	v.FireRes += other.FireRes
	v.WaterRes += other.WaterRes
	v.DarkRes += other.DarkRes
	v.LightRes += other.LightRes
	v.InventoryLimit += other.InventoryLimit
	v.HpRegen += other.HpRegen
	v.MpRegen += other.MpRegen
	v.MoveSpeed += other.MoveSpeed
	v.AttackSpeed += other.AttackSpeed
	v.CastSpeed += other.CastSpeed
	v.HitRecovery += other.HitRecovery
	v.JumpPower += other.JumpPower
	v.Weight += other.Weight
}

// CharacterStatTables is one job's parsed .chr growth graph: the initial value
// row, the [growtype N] transfer rows and the [awakening N] rows. The Set flags
// distinguish an absent row from an all-zero row.
type CharacterStatTables struct {
	Base        StatVector
	Growtype    [7]StatVector
	GrowtypeSet [7]bool
	Awakening   [7][3]StatVector
	AwakenSet   [7][3]bool
}

// FallbackCharacterStatTables is used when a .chr file is missing or cannot be
// parsed. Unlike the server's fixed fallback (which throws above level 1), the
// zero-delta growth rows keep provisioning working: the panel then shows the
// base row plus the premium bonus. ComposeRuntime logs every job that fell back
// so a broken .chr file stays visible.
func FallbackCharacterStatTables() CharacterStatTables {
	table := CharacterStatTables{
		Base: StatVector{
			HpMax: 1800, MpMax: 1400, PhysAtk: 75, PhysDef: 75, MagAtk: 45, MagDef: 45,
			InventoryLimit: 480000, MpRegen: 500, MoveSpeed: 8500,
			AttackSpeed: 8500, CastSpeed: 7000, HitRecovery: 6000, JumpPower: 4300, Weight: 500000,
		},
	}
	for n := 1; n <= 6; n++ {
		table.GrowtypeSet[n] = true
	}
	return table
}

// ProjectCharacterStatCatalog parses every job of character/character.lst into
// its growth tables. It also returns the jobs that fell back because their
// .chr file is missing, unreadable or malformed.
func ProjectCharacterStatCatalog(archive TownTextArchive) (map[int]CharacterStatTables, []int) {
	result := make(map[int]CharacterStatTables)
	if archive == nil {
		return result, nil
	}
	lst, _ := archive.ReadText("character/character.lst")
	if lst == "" {
		return result, nil
	}
	var fallbackJobs []int
	for _, match := range pvfListEntryPattern.FindAllStringSubmatch(lst, -1) {
		job, err := strconv.Atoi(match[1])
		if err != nil || job < 0 || job > 255 {
			continue
		}
		body, _ := archive.ReadText(normalizePVFPath("character/" + match[2]))
		tables, err := parseCharacterStatTables(body)
		if err != nil {
			result[job] = FallbackCharacterStatTables()
			fallbackJobs = append(fallbackJobs, job)
			continue
		}
		result[job] = tables
	}
	return result, fallbackJobs
}

func parseCharacterStatTables(body string) (CharacterStatTables, error) {
	var tables CharacterStatTables
	lower := strings.ToLower(body)
	initPos := strings.Index(lower, "[initial value]")
	if initPos < 0 {
		return tables, fmt.Errorf("character stat file has no [initial value] section")
	}
	var growtypePos [8]int
	for n := 1; n <= 6; n++ {
		position := strings.Index(lower, "[growtype "+strconv.Itoa(n)+"]")
		growtypePos[n] = position
		tables.GrowtypeSet[n] = position >= 0
	}
	growtypePos[7] = len(body)
	tables.Base = parseStatVector(body[initPos:nextGrowtypeBoundary(growtypePos, tables.GrowtypeSet, 1, len(body))])
	for n := 1; n <= 6; n++ {
		if !tables.GrowtypeSet[n] {
			continue
		}
		block := body[growtypePos[n]:nextGrowtypeBoundary(growtypePos, tables.GrowtypeSet, n+1, len(body))]
		blockLower := strings.ToLower(block)
		awakening1 := strings.Index(blockLower, "[awakening 1]")
		awakening2 := strings.Index(blockLower, "[awakening 2]")
		ownEnd := len(block)
		if awakening1 >= 0 {
			ownEnd = awakening1
		} else if awakening2 >= 0 {
			ownEnd = awakening2
		}
		tables.Growtype[n] = parseStatVector(block[:ownEnd])
		if awakening1 >= 0 {
			end := len(block)
			if awakening2 > awakening1 {
				end = awakening2
			}
			tables.Awakening[n][1] = parseStatVector(block[awakening1:end])
			tables.AwakenSet[n][1] = true
		}
		if awakening2 >= 0 {
			tables.Awakening[n][2] = parseStatVector(block[awakening2:])
			tables.AwakenSet[n][2] = true
		}
	}
	if !tables.GrowtypeSet[1] {
		return CharacterStatTables{}, fmt.Errorf("character stat file has no [growtype 1] section")
	}
	return tables, nil
}

func nextGrowtypeBoundary(positions [8]int, present [7]bool, from, fallback int) int {
	for n := from; n <= 6; n++ {
		if present[n] {
			return positions[n]
		}
	}
	return fallback
}

func parseStatVector(section string) StatVector {
	return StatVector{
		HpMax:          statValue(section, "HP MAX"),
		MpMax:          statValue(section, "MP MAX"),
		PhysAtk:        statValue(section, "physical attack"),
		PhysDef:        statValue(section, "physical defense"),
		MagAtk:         statValue(section, "magical attack"),
		MagDef:         statValue(section, "magical defense"),
		FireRes:        statValue(section, "fire resistance"),
		WaterRes:       statValue(section, "water resistance"),
		DarkRes:        statValue(section, "dark resistance"),
		LightRes:       statValue(section, "light resistance"),
		InventoryLimit: statValue(section, "inventory limit"),
		HpRegen:        statValue(section, "HP regen speed"),
		MpRegen:        statValue(section, "MP regen speed"),
		MoveSpeed:      statValue(section, "move speed"),
		AttackSpeed:    statValue(section, "attack speed"),
		CastSpeed:      statValue(section, "cast speed"),
		HitRecovery:    statValue(section, "hit recovery"),
		JumpPower:      statValue(section, "jump power"),
		Weight:         statValue(section, "weight"),
	}
}

func statValue(section, key string) int {
	pattern, ok := statPatterns[key]
	if !ok {
		return 0
	}
	match := pattern.FindStringSubmatch(section)
	if match == nil {
		return 0
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}
	return int(float32(value) * 10)
}

var statPatterns = buildStatPatterns()

func buildStatPatterns() map[string]*regexp.Regexp {
	keys := []string{
		"HP MAX", "MP MAX", "physical attack", "physical defense",
		"magical attack", "magical defense", "fire resistance", "water resistance",
		"dark resistance", "light resistance", "inventory limit", "HP regen speed",
		"MP regen speed", "move speed", "attack speed", "cast speed",
		"hit recovery", "jump power", "weight",
	}
	result := make(map[string]*regexp.Regexp, len(keys))
	for _, key := range keys {
		result[key] = regexp.MustCompile(`(?i)\[` + regexp.QuoteMeta(key) + `\]\s*([-\d.]+)`)
	}
	return result
}

// ProjectLevelThresholds parses character/ExpTable.tbl: the cumulative
// experience required to leave each level.
func ProjectLevelThresholds(archive TownTextArchive) []int {
	if archive == nil {
		return nil
	}
	text, _ := archive.ReadText(normalizePVFPath("character/ExpTable.tbl"))
	if text == "" {
		return nil
	}
	fields := strings.Fields(text)
	thresholds := make([]int, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		thresholds = append(thresholds, value)
	}
	return thresholds
}

// LevelExpFor returns the cumulative experience of a character at `level`,
// mirroring the server's "exp covers threshold(level-1)" convention.
func LevelExpFor(thresholds []int, level int) int {
	if level <= 1 || len(thresholds) == 0 {
		return 0
	}
	if level-1 > len(thresholds) {
		return thresholds[len(thresholds)-1]
	}
	return thresholds[level-2]
}
