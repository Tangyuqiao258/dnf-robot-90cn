package equipment

import (
	"encoding/binary"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// The reference server's two titles, both universal level-1 items.
const (
	testTitleGuardian = 400330105 // 兽人守护神, rarity 2
	testTitleChosen   = 400330106 // 天选之人, rarity 6
)

func titleTestItems() []shared.EquipmentCatalogItem {
	return []shared.EquipmentCatalogItem{
		// One coat per band a test uses: the level bands hand a level-50 character
		// 神器 and a level-90 one 史诗, so no single coat is valid at both.
		{ID: 1, ItemType: 3, Level: 50, Rarity: 3, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/coat_a.equ"},
		{ID: 2, ItemType: 3, Level: 90, Rarity: 4, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/coat_b.equ"},
		{ID: testTitleGuardian, ItemType: 2, Level: 1, Rarity: 2, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/title/chn_400330105.equ"},
		// Rarity 6 sits above every band the fleet uses: the pin has to survive a
		// band that would have drawn nothing.
		{ID: testTitleChosen, ItemType: 2, Level: 1, Rarity: 6, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/title/chn_400330106.equ"},
	}
}

func titleTestConfig() robotconfig.RuntimeConfig {
	rc := robotconfig.Default()
	rc.EquipSlots = []int{3}
	rc.PreferEquipSets = false
	rc.TitleEnabled = true
	return rc
}

func TestPinnedTitleFollowsTheLevelGate(t *testing.T) {
	rc := titleTestConfig()
	rc.TitleItem, rc.TitleTopItem, rc.TitleTopLevel = testTitleGuardian, testTitleChosen, 90
	cases := []struct{ level, want int }{
		{1, testTitleGuardian},
		{50, testTitleGuardian},
		{89, testTitleGuardian},
		{90, testTitleChosen},
	}
	for _, tc := range cases {
		if got := PinnedTitleItem(tc.level, rc); got != tc.want {
			t.Errorf("PinnedTitleItem(%d) = %d, want %d", tc.level, got, tc.want)
		}
	}
	// The switch is the whole point: a title animates in the client, so nothing
	// is pinned until an operator turns it on.
	rc.TitleEnabled = false
	for _, level := range []int{50, 90} {
		if got := PinnedTitleItem(level, rc); got != 0 {
			t.Errorf("PinnedTitleItem(%d) with titles off = %d, want 0", level, got)
		}
	}
}

func TestSelectEquipmentPinsTheTitle(t *testing.T) {
	items := titleTestItems()
	rc := titleTestConfig()
	rc.TitleItem, rc.TitleTopItem, rc.TitleTopLevel = testTitleGuardian, testTitleChosen, 90

	// A level-50 robot wears the fleet title, even though its rarity band would
	// not have drawn one and slot 2 is not in equip_slots.
	rc.EquipSlots = []int{3}
	selected := SelectEquipment(items, 50, 0, 0, rc, func(int) int { return 0 })
	if selected[titleSlot].ID != testTitleGuardian {
		t.Fatalf("level-50 title = %+v, want %d", selected[titleSlot], testTitleGuardian)
	}
	if selected[3].ID != 1 {
		t.Fatalf("level-50 coat = %+v, want the cloth coat", selected[3])
	}

	// A level-90 robot wears the top title instead. Its rarity is 6, above the
	// configured maximum of 5, so this only works because a pin is exempt.
	selected = SelectEquipment(items, 90, 0, 0, rc, func(int) int { return 0 })
	if selected[titleSlot].ID != testTitleChosen {
		t.Fatalf("level-90 title = %+v, want %d", selected[titleSlot], testTitleChosen)
	}
}

func TestSelectEquipmentLeavesSlotTwoAloneWithTitlesOff(t *testing.T) {
	items := titleTestItems()
	rc := titleTestConfig()
	rc.TitleEnabled = false
	selected := SelectEquipment(items, 90, 0, 0, rc, func(int) int { return 0 })
	if _, ok := selected[titleSlot]; ok {
		t.Fatalf("titles off but slot %d = %+v", titleSlot, selected[titleSlot])
	}
}

func TestEquipmentSlotsNeedRepairWantsThePinnedTitle(t *testing.T) {
	items := titleTestItems()
	itemsByID := make(map[int]shared.EquipmentCatalogItem, len(items))
	for _, item := range items {
		itemsByID[item.ID] = item
	}
	rc := titleTestConfig()
	rc.TitleItem, rc.TitleTopItem, rc.TitleTopLevel = testTitleGuardian, testTitleChosen, 90

	raw := make([]byte, 12*61)
	coat := raw[(3-1)*61 : 3*61]
	title := raw[(titleSlot-1)*61 : titleSlot*61]
	write := func(dst []byte, itemID int) {
		binary.LittleEndian.PutUint32(dst[2:6], uint32(itemID))
		binary.LittleEndian.PutUint16(dst[11:13], 20)
	}
	write(coat, 1)

	// A bare robot at level 50 is missing the title the operator pinned.
	if !EquipmentSlotsNeedRepair(raw, itemsByID, 50, 0, 0, rc) {
		t.Fatal("a robot with no title was left alone while a title is pinned")
	}
	write(title, testTitleGuardian)
	if EquipmentSlotsNeedRepair(raw, itemsByID, 50, 0, 0, rc) {
		t.Fatal("a robot wearing the pinned fleet title was marked for repair")
	}
	// The other title is not what this level is owed, so it must be replaced.
	write(title, testTitleChosen)
	if !EquipmentSlotsNeedRepair(raw, itemsByID, 50, 0, 0, rc) {
		t.Fatal("the top title was accepted on a robot below the level gate")
	}
	write(title, testTitleChosen)
	// Level 90 is owed 史诗 gear, so the coat has to change with the level too.
	write(coat, 2)
	if EquipmentSlotsNeedRepair(raw, itemsByID, 90, 0, 0, rc) {
		t.Fatal("a level-90 robot wearing the top title was marked for repair")
	}
	// With the switch off the slot is not walked at all, so a stale title is
	// left where it is rather than stripped on every pass.
	rc.TitleEnabled = false
	write(title, testTitleChosen)
	write(coat, 1)
	if EquipmentSlotsNeedRepair(raw, itemsByID, 50, 0, 0, rc) {
		t.Fatal("titles off still repaired the title slot")
	}
}

func TestEquipSlotsForAddsTheTitleSlotOnlyWhilePinned(t *testing.T) {
	rc := titleTestConfig()
	rc.EquipSlots = []int{1, 3}
	rc.TitleEnabled = false
	if got := equipSlotsFor(50, rc); len(got) != 2 {
		t.Fatalf("equipSlotsFor with titles off = %v, want the configured pair", got)
	}
	rc.TitleEnabled = true
	got := equipSlotsFor(50, rc)
	if len(got) != 3 || got[2] != titleSlot {
		t.Fatalf("equipSlotsFor with a pinned title = %v, want the pair plus slot %d", got, titleSlot)
	}
	// An operator who already lists slot 2 must not get it twice.
	rc.EquipSlots = []int{1, 2, 3}
	if got := equipSlotsFor(50, rc); len(got) != 3 {
		t.Fatalf("equipSlotsFor = %v, want no duplicate slot %d", got, titleSlot)
	}
}
