package equipment

import (
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// avatarBandTestItems is one complete 时装 family per grade plus the two slots
// the bands treat specially: an ordinary 皮肤 the robot must never be given, and
// one 光环 per grade, which the bands must not narrow.
func avatarBandTestItems() []shared.EquipmentCatalogItem {
	items := make([]shared.EquipmentCatalogItem, 0, 20)
	for slot := 0; slot < AvatarSlotCount; slot++ {
		items = append(items,
			shared.EquipmentCatalogItem{ID: 100 + slot, ItemType: 20 + slot, Rarity: CommonAvatarRarity, Name: "plain", SetKey: "plain_set", UseJob: []int{7}},
			shared.EquipmentCatalogItem{ID: 200 + slot, ItemType: 20 + slot, Rarity: RareAvatarRarity, Name: "sky", SetKey: "sky_set", UseJob: []int{7}},
		)
	}
	items = append(items,
		shared.EquipmentCatalogItem{ID: 300, ItemType: 20 + AvatarSkinSlot, Rarity: CommonAvatarRarity, Name: "skin"},
		shared.EquipmentCatalogItem{ID: 400, ItemType: 20 + AvatarAuraSlot, Rarity: CommonAvatarRarity, Name: "plain aura"},
		shared.EquipmentCatalogItem{ID: 401, ItemType: 20 + AvatarAuraSlot, Rarity: RareAvatarRarity, Name: "sky aura"},
	)
	return items
}

// TestSelectAvatarGrantsNothingBelowTheFloor covers the operator's second
// correction: under level 30 the client only takes 普通时装, and splitting the
// low levels that finely was not worth it, so nothing is granted at all.
func TestSelectAvatarGrantsNothingBelowTheFloor(t *testing.T) {
	items := avatarBandTestItems()
	rc := robotconfig.Default()
	rc.AvatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7, 9}
	for _, level := range []int{1, AvatarMinLevel - 1} {
		if selected := SelectAvatar(items, level, 7, rc, func(int) int { return 0 }); len(selected) != 0 {
			t.Fatalf("level %d was dressed anyway: %+v", level, selected)
		}
	}
	if selected := SelectAvatar(items, AvatarMinLevel, 7, rc, func(int) int { return 0 }); len(selected) == 0 {
		t.Fatalf("level %d, the first level over the floor, was left undressed", AvatarMinLevel)
	}
}

func TestAvatarRarityFollowsTheLevelBand(t *testing.T) {
	for _, testCase := range []struct{ level, want int }{
		{1, CommonAvatarRarity},
		{50, CommonAvatarRarity},
		{RareAvatarMinLevel - 1, CommonAvatarRarity},
		{RareAvatarMinLevel, RareAvatarRarity},
		{90, RareAvatarRarity},
	} {
		if got := AvatarRarityFor(testCase.level); got != testCase.want {
			t.Errorf("AvatarRarityFor(%d) = %d, want %d", testCase.level, got, testCase.want)
		}
	}
}

// TestAvatarSetMinSlotsTakesTheWholeSetFromTheRareBand pins the operator's
// "稀有装扮套装应该是发8个slot": in the rare band the floor is every slot, so only
// a group covering all eight can be the one that is taken, whatever the
// configured floor says.
func TestAvatarSetMinSlotsTakesTheWholeSetFromTheRareBand(t *testing.T) {
	if got := AvatarSetMinSlotsFor(RareAvatarMinLevel, 6); got != AvatarSlotCount {
		t.Fatalf("rare band min slots = %d, want %d", got, AvatarSlotCount)
	}
	if got := AvatarSetMinSlotsFor(RareAvatarMinLevel, 2); got != AvatarSlotCount {
		t.Fatalf("rare band min slots = %d, want %d even with a low configured floor", got, AvatarSlotCount)
	}
	for _, configured := range []int{2, 6, 8} {
		if got := AvatarSetMinSlotsFor(50, configured); got != configured {
			t.Errorf("below the band min slots = %d, want the configured %d", got, configured)
		}
	}
	// A floor outside the slot range is pulled back into it rather than left to
	// make the set pass unfindable.
	if got := AvatarSetMinSlotsFor(50, 99); got != AvatarSlotCount {
		t.Errorf("oversized floor = %d, want %d", got, AvatarSlotCount)
	}
}

func TestAvatarSlotsInUseDropsTheSkinSlot(t *testing.T) {
	got := AvatarSlotsInUse([]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	want := []int{0, 1, 2, 3, 4, 5, 6, 7, 9}
	if len(got) != len(want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("slots = %v, want %v", got, want)
		}
	}
	// An empty setting is the default list, and it carries 光环 but not 皮肤.
	defaults := AvatarSlotsInUse(nil)
	if len(defaults) != AvatarSlotCount+1 {
		t.Fatalf("default slots = %v, want the %d 时装 slots plus 光环", defaults, AvatarSlotCount)
	}
	for _, slot := range defaults {
		if slot == AvatarSkinSlot {
			t.Fatalf("default slots = %v, want no 皮肤", defaults)
		}
	}
	// A slot named twice is dressed once.
	if got := AvatarSlotsInUse([]int{0, 0, 3}); len(got) != 2 {
		t.Fatalf("slots = %v, want the duplicate dropped", got)
	}
}

func TestSelectAvatarDressesTheRareBandAsOneSet(t *testing.T) {
	items := avatarBandTestItems()
	rc := robotconfig.Default()
	rc.AvatarSlots = nil
	rc.PreferAvatarSets = true
	rc.AvatarSetMinSlots = 6

	rare := SelectAvatar(items, RareAvatarMinLevel, 7, rc, func(int) int { return 0 })
	for slot := 0; slot < AvatarSlotCount; slot++ {
		item, ok := rare[slot]
		if !ok {
			t.Fatalf("level %d left 时装 slot %d empty: %+v", RareAvatarMinLevel, slot, rare)
		}
		if item.Rarity != RareAvatarRarity {
			t.Fatalf("level %d slot %d wore rarity %d, want the 稀有装扮 grade", RareAvatarMinLevel, slot, item.Rarity)
		}
		if item.SetKey != "sky_set" {
			t.Fatalf("level %d slot %d wore set %q, want the whole 稀有装扮 set", RareAvatarMinLevel, slot, item.SetKey)
		}
	}

	plain := SelectAvatar(items, RareAvatarMinLevel-1, 7, rc, func(int) int { return 0 })
	for slot := 0; slot < AvatarSlotCount; slot++ {
		item, ok := plain[slot]
		if !ok {
			t.Fatalf("level %d left 时装 slot %d empty: %+v", RareAvatarMinLevel-1, slot, plain)
		}
		if item.Rarity != CommonAvatarRarity {
			t.Fatalf("level %d slot %d wore rarity %d, want the ordinary grade", RareAvatarMinLevel-1, slot, item.Rarity)
		}
	}
}

// TestSelectAvatarKeepsOneNameFamily covers the operator's third rule for 装扮:
// 前四个字一样的基本就是一套的. The archive's own set key cannot do this job -- every
// 稀有装扮 piece of a job carries pvf_part_3 -- and the two families below share
// their four-character root on purpose, which is the collision that would put a
// 魔音 helmet on a 幻影 body.
func TestSelectAvatarKeepsOneNameFamily(t *testing.T) {
	slotWords := []string{"头盔", "长发", "眼妆", "披风", "胸甲", "护腿", "腰带", "长靴"}
	items := make([]shared.EquipmentCatalogItem, 0, 16)
	for slot := 0; slot < AvatarSlotCount; slot++ {
		items = append(items,
			shared.EquipmentCatalogItem{ID: 1000 + slot, ItemType: 20 + slot, Rarity: RareAvatarRarity, Name: "暗影魅月魔音" + slotWords[slot], SetKey: "pvf_part_3", UseJob: []int{7}},
			shared.EquipmentCatalogItem{ID: 2000 + slot, ItemType: 20 + slot, Rarity: RareAvatarRarity, Name: "暗影魅月幻影" + slotWords[slot], SetKey: "pvf_part_3", UseJob: []int{7}},
		)
	}
	rc := robotconfig.Default()
	rc.AvatarSlots = nil
	rc.PreferAvatarSets = true
	rc.AvatarSetMinSlots = 6

	// Both ends of the draw, so the seed piece is not always the same one.
	for _, randIntn := range []func(int) int{func(int) int { return 0 }, func(n int) int { return n - 1 }} {
		selected := SelectAvatar(items, RareAvatarMinLevel, 7, rc, randIntn)
		roots := map[string]bool{}
		for slot := 0; slot < AvatarSlotCount; slot++ {
			item, ok := selected[slot]
			if !ok {
				t.Fatalf("slot %d left empty: %+v", slot, selected)
			}
			roots[string([]rune(item.Name)[:6])] = true
		}
		if len(roots) != 1 {
			t.Fatalf("dressed from %d 装扮 sets: %v", len(roots), roots)
		}
	}
}

func TestAvatarFamilyKeyCountsRunes(t *testing.T) {
	if got := AvatarFamilyKey("  暗影魅月魔音眼妆 "); got != "暗影魅月" {
		t.Fatalf("family key = %q", got)
	}
	if got := AvatarFamilyKey("时装"); got != "" {
		t.Fatalf("a two-character name named the family %q", got)
	}
	if got := AvatarNameAffinity("暗影魅月魔音眼妆", "暗影魅月幻影长发"); got != 4 {
		t.Fatalf("affinity = %d, want the four shared characters", got)
	}
	if got := AvatarNameAffinity("sky", "sky"); got != 3 {
		t.Fatalf("affinity = %d, want the whole shorter name", got)
	}
}

// TestSelectAvatarNeverDressesTheSkinSlot is the operator's other half: the
// native creation code leaves 皮肤 empty, so a catalog entry for it must not be
// worn at any level, even though the slot is the ninth avatar slot and the
// catalog carries one.
func TestSelectAvatarNeverDressesTheSkinSlot(t *testing.T) {
	items := avatarBandTestItems()
	rc := robotconfig.Default()
	rc.AvatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	for _, level := range []int{50, RareAvatarMinLevel, 90} {
		selected := SelectAvatar(items, level, 7, rc, func(int) int { return 0 })
		if item, ok := selected[AvatarSkinSlot]; ok {
			t.Fatalf("level %d wore 皮肤 %d", level, item.ID)
		}
	}
	// And a slot list that names only 皮肤 dresses nothing at all.
	rc.AvatarSlots = []int{AvatarSkinSlot}
	if selected := SelectAvatar(items, 90, 7, rc, func(int) int { return 0 }); len(selected) != 0 {
		t.Fatalf("a 皮肤-only slot list selected %+v", selected)
	}
}

// TestSelectAvatarKeepsTheAuraOutsideTheBand covers what the operator asked to
// leave alone: 光环 is not part of the 时装 set, so it is drawn at every level
// and from both grades rather than being narrowed with the 时装.
func TestSelectAvatarKeepsTheAuraOutsideTheBand(t *testing.T) {
	items := avatarBandTestItems()
	rc := robotconfig.Default()
	rc.AvatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7, 9}
	for _, level := range []int{50, 90} {
		selected := SelectAvatar(items, level, 7, rc, func(int) int { return 0 })
		if selected[AvatarAuraSlot].ID == 0 {
			t.Fatalf("level %d wore no 光环: %+v", level, selected)
		}
	}
	// An operator who does not list 光环 does not get one.
	rc.AvatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7}
	if selected := SelectAvatar(items, 90, 7, rc, func(int) int { return 0 }); selected[AvatarAuraSlot].ID != 0 {
		t.Fatalf("an unlisted 光环 was worn anyway: %+v", selected)
	}
}

// TestSelectAvatarFallsBackWhenNoRareSetIsComplete keeps the fleet dressed on an
// archive whose 稀有装扮 family is short: the set floor must not leave slots
// empty, it only decides which group wins.
func TestSelectAvatarFallsBackWhenNoRareSetIsComplete(t *testing.T) {
	items := make([]shared.EquipmentCatalogItem, 0, AvatarSlotCount)
	for slot := 0; slot < AvatarSlotCount; slot++ {
		items = append(items, shared.EquipmentCatalogItem{
			ID: 200 + slot, ItemType: 20 + slot, Rarity: RareAvatarRarity, Name: "sky",
			SetKey: "sky_set", UseJob: []int{7},
		})
	}
	// Drop one slot's only candidate: no group can cover all eight now.
	items = items[1:]
	rc := robotconfig.Default()
	rc.AvatarSlots = []int{0, 1, 2, 3, 4, 5, 6, 7}
	rc.PreferAvatarSets = true
	rc.AvatarSetMinSlots = 6

	selected := SelectAvatar(items, 90, 7, rc, func(int) int { return 0 })
	filled := 0
	for slot := 0; slot < AvatarSlotCount; slot++ {
		if selected[slot].ID != 0 {
			filled++
		}
	}
	if filled != AvatarSlotCount-1 {
		t.Fatalf("selected %+v, want the seven slots the archive can still fill", selected)
	}
}
