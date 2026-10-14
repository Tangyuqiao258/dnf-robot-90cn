package cn90

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotconfig "robot/internal/capability/robotconfig"
)

// pvfTemplateEntryPattern matches one `<id> `path“ entry of character.lst and
// pvfTemplateSkillPattern one `<skill id> <level>` pair of a .chr [skill] block.
var (
	pvfTemplateEntryPattern = regexp.MustCompile("(\\d+)\\s+`([^`]+)`")
	pvfTemplateSkillPattern = regexp.MustCompile("(\\d+)\\s+\\d+")
)

// baseSkillTemplate returns the first [skill] block of a .chr file: the skill
// template the server creates a new character from, which ends before the first
// transfer-branch section.
func baseSkillTemplate(body string) string {
	if cut := branchSectionStart(body); cut >= 0 {
		body = body[:cut]
	}
	lower := strings.ToLower(body)
	start := strings.Index(lower, "[skill]")
	if start < 0 {
		return ""
	}
	rest := body[start+len("[skill]"):]
	if end := strings.Index(strings.ToLower(rest), "[/skill]"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// branchSectionStart returns the offset of the first transfer-branch section,
// [growtype <n>], or -1 when the file carries none.
//
// The number is what makes it a section: the template's own header is
// [growtype name], and 黑暗武士 and 缔造者 both write that header above their [skill]
// block, so cutting at a bare "[growtype" would read those two templates as
// empty and hand their jobs the default five materials.
func branchSectionStart(body string) int {
	offset := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(trimmed, "[growtype") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "[growtype"))
			if rest != "" && rest[0] >= '0' && rest[0] <= '9' {
				return offset
			}
		}
		offset += len(line) + 1
	}
	return -1
}

func sameArmorMaterialSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	remaining := make(map[string]int, len(got))
	for _, material := range got {
		remaining[material]++
	}
	for _, material := range want {
		remaining[material]--
		if remaining[material] < 0 {
			return false
		}
	}
	return true
}

// TestArmorMasterySelectsMatchingRealArmor runs the real selector over the
// installed catalog: every armor slot a transferred branch fills has to come
// from the material the branch masters, and at least one armor slot has to be
// filled so the check cannot pass by selecting nothing.
//
// Level 50 is the level worth pinning: it is inside the band whose rarity (神器)
// the archive holds no 重甲 or 板甲 for at any level up to 70, so the 重甲 and 板甲
// branches are dressed entirely by armorFallbackRarities. A selector that lost
// either the material filter or the fallback fails here rather than in the game.
func TestArmorMasterySelectsMatchingRealArmor(t *testing.T) {
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	pvfPath, _, err := layout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	// The selection knobs the live ini sets, so the check runs the selector the
	// running fleet does.
	rc := robotconfig.Default()
	rc.EquipSlots = []int{1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	rc.EquipRarityMin, rc.EquipRarityMax = 0, 5
	rc.PreferEquipSets = true
	rc.EquipSetMinSlots = 5
	randIntn := func(int) int { return 0 }
	for job := 0; job <= 15; job++ {
		for _, mastery := range equipmentcap.ArmorMasteryBranches(job) {
			worn := selectWornEquipment(catalogs.Equipment, rc, 50, job, mastery.Branch, randIntn)
			armor := 0
			for _, item := range worn {
				if item.ItemType < 3 || item.ItemType > 7 {
					continue
				}
				armor++
				if got := equipmentcap.ArmorMaterialOfPath(item.Path); got != mastery.Material {
					t.Errorf("job %d %s: %s is %s, want %s", job, mastery.Name, item.Path, got, mastery.Material)
				}
			}
			if armor == 0 {
				t.Errorf("job %d %s: no armor slot was filled (worn=%d master=%s)", job, mastery.Name, len(worn), mastery.Material)
			}
		}
	}
}

// TestArmorMasteryMatchesPVFBranches checks the armor mastery table against the
// archive it was read from. The game server keeps no machine-readable copy of
// the table — an item's material is only its path, and [usable job] stops at
// the base class — so this comparison is the one thing that catches a PVF whose
// branch order or branch set differs from the one the table was written from.
// It reads the installed runtime like the other live checks and skips without
// one.
func TestArmorMasteryMatchesPVFBranches(t *testing.T) {
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	pvfPath, _, err := layout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	names := capabilitypvf.ProjectJobGrowNames(archive)
	if len(names) == 0 {
		t.Fatal("archive releases no character grow names")
	}
	// The jobs the robot creates, per the ini's jobs key. A job the archive
	// releases a branch for must be covered by the table; a branch the archive
	// does not release must not be in it.
	for job := 0; job <= 15; job++ {
		unchecked := map[int]string{}
		for _, mastery := range equipmentcap.ArmorMasteryBranches(job) {
			unchecked[mastery.Branch] = mastery.Name
		}
		for branch, name := range names[job] {
			if branch == 0 || name == "" {
				continue
			}
			switch got, ok := unchecked[branch]; {
			case !ok:
				t.Errorf("job %d branch %d (%s) has no armor mastery entry", job, branch, name)
			case got != name:
				t.Errorf("job %d branch %d is %s in the archive but %s in the table", job, branch, name, got)
			}
			delete(unchecked, branch)
		}
		for branch, name := range unchecked {
			t.Errorf("job %d branch %d is %s in the table but the archive does not release it", job, branch, name)
		}
	}
}

// TestBaseArmorMasteryMatchesJobTemplates checks the untransferred rule against
// the archive. The game server builds a new character from character/<class>.chr
// and that file's first [skill] block is the template it starts from, so the
// armor masteries listed there are what the character wears before it
// transfers. Fourteen of the sixteen classes list 基础防具精通 alone
// (ArmorMasteryEtc, id 511), which names all five materials and so narrows
// nothing; 黑暗武士's template lists 布甲、轻甲、重甲 and 板甲 with no 皮甲, and
// 缔造者's lists 板甲 alone. The transfer branches are covered by
// TestArmorMasteryMatchesPVFBranches.
func TestBaseArmorMasteryMatchesJobTemplates(t *testing.T) {
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	pvfPath, _, err := layout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	lst, err := archive.ReadText("character/character.lst")
	if err != nil {
		t.Fatal(err)
	}
	// The ids the archive gives the generic armor masteries in every class skill
	// list. 基础防具精通 (511) is deliberately absent: every class holds it, and it
	// covers all five materials.
	materialOfSkill := map[string]string{
		"172": equipmentcap.ArmorLight,
		"173": equipmentcap.ArmorHeavy,
		"185": equipmentcap.ArmorLeather,
		"187": equipmentcap.ArmorCloth,
		"196": equipmentcap.ArmorPlate,
	}
	checked := 0
	for _, entry := range pvfTemplateEntryPattern.FindAllStringSubmatch(lst, -1) {
		job, parseErr := strconv.Atoi(entry[1])
		if parseErr != nil || job < 0 || job > 15 {
			continue
		}
		body, readErr := archive.ReadText("character/" + entry[2])
		if readErr != nil || body == "" {
			t.Fatalf("job %d template character/%s is unreadable: %v", job, entry[2], readErr)
		}
		want := make([]string, 0, 5)
		for _, skill := range pvfTemplateSkillPattern.FindAllStringSubmatch(baseSkillTemplate(body), -1) {
			if material, ok := materialOfSkill[skill[1]]; ok {
				want = append(want, material)
			}
		}
		if len(want) == 0 {
			want = []string{
				equipmentcap.ArmorCloth, equipmentcap.ArmorLeather, equipmentcap.ArmorLight,
				equipmentcap.ArmorHeavy, equipmentcap.ArmorPlate,
			}
		}
		if got := equipmentcap.ArmorMaterialsFor(job, 0); !sameArmorMaterialSet(got, want) {
			t.Errorf("job %d wears %q, but its character template names %q", job, got, want)
		}
		checked++
	}
	if checked != 16 {
		t.Fatalf("checked %d character templates, want the archive's 16 jobs", checked)
	}
}
