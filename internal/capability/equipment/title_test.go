package equipment

import (
	"encoding/binary"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// The reference server's titles, all universal level-1 items.
const (
	testTitleGuardian  = 400330105 // 兽人守护神, rarity 2
	testTitleChosen    = 400330106 // 天选之人, rarity 6
	testTitleDragon    = 400330093 // 龙之威仪, rarity 3
	testTitleChallenge = 400330092 // 龙之挑战, rarity 2
)

func titleTestItems() []shared.EquipmentCatalogItem {
	return []shared.EquipmentCatalogItem{
		// One coat per band a test uses: the level bands hand a level-50 character
		// 神器 and a level-90 one 史诗, so no single coat is valid at both.
		{ID: 1, ItemType: 3, Level: 50, Rarity: 3, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/coat_a.equ"},
		{ID: 2, ItemType: 3, Level: 90, Rarity: 4, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/coat_b.equ"},
		{ID: testTitleGuardian, ItemType: 2, Level: 1, Rarity: 2, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/title/chn_400330105.equ"},
		// The tier the operator named: three of the four reach a robot, because
		// 桃园结义's records carry the truncated bracket the grant gate bars.
		{ID: testTitleDragon, ItemType: 2, Level: 1, Rarity: 3, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/title/chn_400330093.equ"},
		{ID: testTitleChallenge, ItemType: 2, Level: 1, Rarity: 2, Durability: 20, UseJob: []int{100}, Path: "equipment/character/common/title/chn_400330092.equ"},
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

func TestTitleTierRanksBelowTheTopLevel(t *testing.T) {
	rc := titleTestConfig()
	rc.TitleItem, rc.TitleTopItem, rc.TitleTopLevel = testTitleGuardian, testTitleChosen, 90
	rc.TitleTierItems = []int{testTitleGuardian, testTitleDragon, testTitleChallenge}

	got := TitleTierItemIDs(50, rc)
	if len(got) != 3 || got[0] != testTitleGuardian || got[2] != testTitleChallenge {
		t.Fatalf("TitleTierItemIDs(50) = %v, want the configured tier", got)
	}
	// The top rank is a statement about the level, so a draw inside it would say
	// nothing: a level-90 robot wears the top title and only that.
	if got := TitleTierItemIDs(90, rc); len(got) != 1 || got[0] != testTitleChosen {
		t.Fatalf("TitleTierItemIDs(90) = %v, want only %d", got, testTitleChosen)
	}
	// The repair check asks "is anything pinned", and the first id answers it.
	if got := PinnedTitleItem(50, rc); got != testTitleGuardian {
		t.Fatalf("PinnedTitleItem(50) = %d, want %d", got, testTitleGuardian)
	}
	// Without a tier the single pin is the whole fleet's title, as before.
	rc.TitleTierItems = nil
	if got := TitleTierItemIDs(50, rc); len(got) != 1 || got[0] != testTitleGuardian {
		t.Fatalf("TitleTierItemIDs(50) without a tier = %v, want only %d", got, testTitleGuardian)
	}
	rc.TitleEnabled = false
	if got := TitleTierItemIDs(50, rc); got != nil {
		t.Fatalf("TitleTierItemIDs with titles off = %v, want nil", got)
	}
}

func TestSelectEquipmentDrawsWithinTheTitleTier(t *testing.T) {
	items := titleTestItems()
	rc := titleTestConfig()
	rc.TitleItem, rc.TitleTopItem, rc.TitleTopLevel = testTitleGuardian, testTitleChosen, 90
	rc.TitleTierItems = []int{testTitleGuardian, testTitleDragon, testTitleChallenge}

	// The draw index picks the rank, so two robots of the same level differ.
	for _, tc := range []struct{ pick, want int }{
		{0, testTitleGuardian},
		{1, testTitleDragon},
		{2, testTitleChallenge},
	} {
		selected := SelectEquipment(items, 50, 0, 0, rc, func(int) int { return tc.pick })
		if selected[titleSlot].ID != tc.want {
			t.Fatalf("tier draw %d gave title %+v, want %d", tc.pick, selected[titleSlot], tc.want)
		}
	}
	// At the top level the tier is not consulted at all.
	selected := SelectEquipment(items, 90, 0, 0, rc, func(int) int { return 1 })
	if selected[titleSlot].ID != testTitleChosen {
		t.Fatalf("level-90 title = %+v, want %d", selected[titleSlot], testTitleChosen)
	}
}

func TestEquipmentSlotsNeedRepairAcceptsAnyTierTitle(t *testing.T) {
	items := titleTestItems()
	itemsByID := make(map[int]shared.EquipmentCatalogItem, len(items))
	for _, item := range items {
		itemsByID[item.ID] = item
	}
	rc := titleTestConfig()
	rc.TitleItem, rc.TitleTopItem, rc.TitleTopLevel = testTitleGuardian, testTitleChosen, 90
	rc.TitleTierItems = []int{testTitleGuardian, testTitleDragon, testTitleChallenge}

	raw := make([]byte, 12*61)
	binary.LittleEndian.PutUint32(raw[(3-1)*61+2:(3-1)*61+6], 1)
	binary.LittleEndian.PutUint16(raw[(3-1)*61+11:(3-1)*61+13], 20)
	writeTitle := func(id int) {
		slot := raw[(titleSlot-1)*61 : titleSlot*61]
		binary.LittleEndian.PutUint32(slot[2:6], uint32(id))
		binary.LittleEndian.PutUint16(slot[11:13], 20)
	}
	// Every rank of the tier is what this level is owed, so none of them is
	// repaired -- a repair pass that accepted only the first id would strip the
	// other two and hand out a fresh title on every sweep.
	for _, id := range rc.TitleTierItems {
		writeTitle(id)
		if EquipmentSlotsNeedRepair(raw, itemsByID, 50, 0, 0, rc) {
			t.Fatalf("a level-50 robot wearing tier title %d was marked for repair", id)
		}
	}
	// The top rank is still the top rank's: it does not satisfy the tier.
	writeTitle(testTitleChosen)
	if !EquipmentSlotsNeedRepair(raw, itemsByID, 50, 0, 0, rc) {
		t.Fatal("the top title was accepted on a robot below the level gate")
	}
	// At level 90 the tier gives way to the top rank, so a tier title no longer
	// satisfies the slot. The coat moves to the level-90 band at the same time.
	binary.LittleEndian.PutUint32(raw[(3-1)*61+2:(3-1)*61+6], 2)
	writeTitle(testTitleDragon)
	if !EquipmentSlotsNeedRepair(raw, itemsByID, 90, 0, 0, rc) {
		t.Fatal("a tier title was accepted at level 90, where the top rank is owed")
	}
	writeTitle(testTitleChosen)
	if EquipmentSlotsNeedRepair(raw, itemsByID, 90, 0, 0, rc) {
		t.Fatal("a level-90 robot wearing the top title was marked for repair")
	}
}

func TestPinnedTitleSkipsTierIDsTheCatalogDoesNotCarry(t *testing.T) {
	// 桃园结义's ids are the operator's, but the records are gate-blocked rather
	// than absent; a tier naming an id the catalog never carries must leave the
	// slot empty instead of quietly wearing a different title.
	items := []shared.EquipmentCatalogItem{
		{ID: testTitleGuardian, ItemType: 2, Level: 1, Rarity: 2, UseJob: []int{100}},
	}
	rc := titleTestConfig()
	rc.TitleTierItems = []int{400330071}
	if _, ok := pinnedTitle(50, rc, items, func(int) int { return 0 }); ok {
		t.Fatal("a tier id absent from the catalog still produced a title")
	}
	// The gate barring a record is the other half: a blocked one is not worn.
	blocked := []shared.EquipmentCatalogItem{
		{ID: testTitleGuardian, ItemType: 2, Level: 1, Rarity: 2, UseJob: []int{100}, GrantBlocked: true},
	}
	rc.TitleTierItems = []int{testTitleGuardian}
	if _, ok := pinnedTitle(50, rc, blocked, func(int) int { return 0 }); ok {
		t.Fatal("a grant-blocked title was worn")
	}
}
