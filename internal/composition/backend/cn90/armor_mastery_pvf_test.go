package cn90

import (
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotconfig "robot/internal/capability/robotconfig"
)

// TestArmorMasterySelectsMatchingRealArmor runs the real selector over the
// installed catalog: every armor slot a transferred branch fills has to come
// from the material the branch masters, and at least one armor slot has to be
// filled so the check cannot pass by selecting nothing.
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
				t.Errorf("job %d %s: no armor slot was filled", job, mastery.Name)
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
