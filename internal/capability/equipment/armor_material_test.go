package equipment

import (
	"encoding/binary"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestArmorMaterialsForReadsTheTransferBranch(t *testing.T) {
	cases := []struct {
		job, grow int
		want      []string
	}{
		// 阿修罗 is branch 4 of the male swordman; the awakening stage in the
		// high nibble must not change the material.
		{job: 0, grow: 0x04, want: []string{ArmorPlate}},
		{job: 0, grow: 0x14, want: []string{ArmorPlate}},
		{job: 0, grow: 0x24, want: []string{ArmorPlate}},
		{job: 0, grow: 0x01, want: []string{ArmorLight}},
		{job: 0, grow: 0x03, want: []string{ArmorHeavy}},
		// 驱魔师 wears plate: the class splits between a physical and a magical
		// build, and the robot only ever generates the physical one.
		{job: 4, grow: 0x03, want: []string{ArmorPlate}},
		// 男魔法师's fifth branch is real: the archive names it 次元行者 rather
		// than repeating the base class.
		{job: 8, grow: 0x05, want: []string{ArmorCloth}},
		// The jobs whose avatars and weapons the archive files under knight,
		// demoniclancer and gunblader.
		{job: 11, grow: 0x02, want: []string{ArmorPlate}},
		{job: 12, grow: 0x04, want: []string{ArmorLight}},
		{job: 13, grow: 0x04, want: []string{ArmorLeather}},
		{job: 14, grow: 0x03, want: []string{ArmorCloth}},
		{job: 15, grow: 0x01, want: []string{ArmorLight}},
		// 守护者 is where the archive order and the usual class listing differ:
		// 帕拉丁 is branch 3 and 龙骑士 branch 4.
		{job: 12, grow: 0x03, want: []string{ArmorPlate}},
		// A robot that never transferred wears anything.
		{job: 0, grow: 0, want: nil},
		{job: 2, grow: 0, want: nil},
		// 黑暗武士 masters four of the five materials and never transfers;
		// 缔造者 masters plate and never transfers.
		{job: 9, grow: 0, want: []string{ArmorCloth, ArmorLight, ArmorHeavy, ArmorPlate}},
		{job: 10, grow: 0, want: []string{ArmorPlate}},
		// A branch the table does not know must not narrow the catalog.
		{job: 0, grow: 0x05, want: nil},
		{job: 99, grow: 0x01, want: nil},
	}
	for _, tc := range cases {
		got := ArmorMaterialsFor(tc.job, tc.grow)
		if len(got) != len(tc.want) {
			t.Errorf("ArmorMaterialsFor(%d, %#x) = %q, want %q", tc.job, tc.grow, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("ArmorMaterialsFor(%d, %#x) = %q, want %q", tc.job, tc.grow, got, tc.want)
				break
			}
		}
	}
}

// TestDarkKnightArmorExcludesLeather pins the one multi-material class: the
// table says 黑暗武士 masters 布甲、轻甲、重甲、板甲 and has no 皮甲 mastery.
func TestDarkKnightArmorExcludesLeather(t *testing.T) {
	rc := robotconfig.Default()
	rc.EquipSlots = []int{3}
	rc.EquipRarityMin, rc.EquipRarityMax = 0, 5
	rc.PreferEquipSets = false
	items := []shared.EquipmentCatalogItem{
		{ID: 1, ItemType: 3, Level: 50, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/a.equ"},
		{ID: 2, ItemType: 3, Level: 50, UseJob: []int{100}, Path: "equipment/character/common/jacket/leather/b.equ"},
		{ID: 3, ItemType: 3, Level: 50, UseJob: []int{100}, Path: "equipment/character/common/jacket/harmor/c.equ"},
	}
	selected := SelectEquipment(items, 50, 9, 0, rc, func(int) int { return 0 })
	if selected[3].ID != 1 {
		t.Fatalf("dark knight coat = %+v, want the first non-leather item", selected)
	}
}

func TestArmorMasteryTableIsWellFormed(t *testing.T) {
	for job, branches := range jobArmorMastery {
		seen := make(map[int]bool, len(branches))
		for _, mastery := range branches {
			if mastery.Branch < 1 || mastery.Branch > 5 {
				t.Errorf("job %d branch %d is outside the server's first-grow guard", job, mastery.Branch)
			}
			if seen[mastery.Branch] {
				t.Errorf("job %d lists branch %d twice", job, mastery.Branch)
			}
			seen[mastery.Branch] = true
			if mastery.Name == "" {
				t.Errorf("job %d branch %d has no PVF name to check against", job, mastery.Branch)
			}
			if !isArmorMaterial(mastery.Material) {
				t.Errorf("job %d branch %d material %q is not a PVF material directory", job, mastery.Branch, mastery.Material)
			}
		}
	}
	for job, materials := range fixedJobArmorMaterials {
		if len(materials) == 0 {
			t.Errorf("job %d lists no fixed material, which would leave it unrestricted", job)
		}
		for _, material := range materials {
			if !isArmorMaterial(material) {
				t.Errorf("job %d fixed material %q is not a PVF material directory", job, material)
			}
		}
	}
}

func TestArmorMaterialOfPathReadsTheItemDirectory(t *testing.T) {
	cases := []struct{ path, want string }{
		{"equipment/character/common/jacket/plate/coat_exorcismpray.equ", ArmorPlate},
		{"equipment/character/swordman/equip/pants/larmor/pants_a.equ", ArmorLight},
		{"EQUIPMENT\\CHARACTER\\COMMON\\SHOES\\HARMOR\\boots_a.equ", ArmorHeavy},
		{"equipment/character/common/jacket/leather/vest_a.equ", ArmorLeather},
		{"equipment/character/common/waist/cloth/belt_a.equ", ArmorCloth},
		// equipment/character/common/etc holds ten non-armor records that the
		// catalog classifies as coat or waist; "etc" is not a material.
		{"equipment/character/common/etc/bravery.equ", ""},
		{"equipment/character/common/jacket/coat_a.equ", ""},
		{"cloth", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := ArmorMaterialOfPath(tc.path); got != tc.want {
			t.Errorf("ArmorMaterialOfPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSelectEquipmentRestrictsArmorToTheMasteredMaterial(t *testing.T) {
	rc := robotconfig.Default()
	rc.EquipSlots = []int{3, 5}
	rc.EquipRarityMin, rc.EquipRarityMax = 0, 5
	rc.PreferEquipSets = false
	items := []shared.EquipmentCatalogItem{
		{ID: 1, ItemType: 3, Level: 50, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/coat_a.equ"},
		{ID: 2, ItemType: 3, Level: 50, UseJob: []int{100}, Path: "equipment/character/common/jacket/plate/coat_b.equ"},
		{ID: 3, ItemType: 5, Level: 50, UseJob: []int{100}, Path: "equipment/character/common/pants/harmor/pants_a.equ"},
		{ID: 4, ItemType: 5, Level: 50, UseJob: []int{100}, Path: "equipment/character/common/pants/plate/pants_b.equ"},
	}
	// 阿修罗: branch 4 of the male swordman, plate only.
	selected := SelectEquipment(items, 50, 0, 0x04, rc, func(int) int { return 0 })
	if selected[3].ID != 2 || selected[5].ID != 4 {
		t.Fatalf("asura armor = %+v, want the plate pair", selected)
	}
	// A robot that never transferred keeps the whole catalog.
	selected = SelectEquipment(items, 50, 0, 0, rc, func(int) int { return 0 })
	if len(selected) != 2 {
		t.Fatalf("untransferred armor = %+v, want both slots filled", selected)
	}
}

func TestEquipmentSlotsNeedRepairRejectsArmorOfAnotherMaterial(t *testing.T) {
	rc := robotconfig.RuntimeConfig{EquipSlots: []int{3}, EquipRarityMax: 5}
	items := map[int]shared.EquipmentCatalogItem{
		100: {ID: 100, ItemType: 3, Level: 50, Rarity: 3, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/coat_a.equ"},
		101: {ID: 101, ItemType: 3, Level: 50, Rarity: 3, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/jacket/plate/coat_b.equ"},
	}
	raw := make([]byte, 12*61)
	coat := raw[(3-1)*61 : 3*61]
	wear := func(itemID int) {
		binary.LittleEndian.PutUint32(coat[2:6], uint32(itemID))
		binary.LittleEndian.PutUint16(coat[11:13], 20)
	}
	wear(100)
	// 阿修罗, grow_type 0x04.
	if !EquipmentSlotsNeedRepair(raw, items, 50, 0, 0x04, rc) {
		t.Fatal("cloth armor was accepted for a plate-mastery branch")
	}
	wear(101)
	if EquipmentSlotsNeedRepair(raw, items, 50, 0, 0x04, rc) {
		t.Fatal("plate armor was marked for repair on a plate-mastery branch")
	}
	// The same cloth coat is fine for a robot that never transferred.
	wear(100)
	if EquipmentSlotsNeedRepair(raw, items, 50, 0, 0, rc) {
		t.Fatal("untransferred robot was not allowed to keep its cloth coat")
	}
}
