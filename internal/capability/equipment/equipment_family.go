package equipment

import (
	"robot/internal/shared"
)

// Set families (成套发放).
//
// The operator asked for whole sets — "万世荣光直接发一套, 首饰发一套, 特殊装备
// 也有成套的" — which is three sets, not one. Picking the single widest set in
// the archive only ever dresses one family: an armor set covers 5 slots and
// beats every accessory and special set on coverage, so those two families were
// left to the random fill and arrived mismatched.
//
// The archive builds its sets inside these three groups and never across them,
// so one best set is chosen per family and the three are merged. A family whose
// slots are not configured, or that the archive has no set for at that band, is
// left unselected and the caller's random fill covers it.
var equipmentFamilies = [][]int{
	{3, 4, 5, 6, 7}, // armor 防具: 上衣 下装 头肩 腰带 鞋
	{8, 9, 10},      // accessory 首饰: 项链 手镯 戒指
	{11, 12, 13},    // special 特殊装备: 辅助装备 魔法石 耳环
}

// EarringItemType is 耳环, the last equipment slot the game opened, and
// EarringMinLevel is the level at which a character may wear one: every earring
// in the archive has a minimum level of 90. The slot also has to be unlocked on
// the character (ex_equip_slot_stat), which the loadout writer does for level-90
// robots — see the equipment-slot unlock in this package's consumer.
const (
	EarringItemType = 13
	EarringMinLevel = 90
)

// WornItemTypeOfSlot maps a worn slot to its item type. It is one wider than
// SlotToItemType, which stops at 12 because the legacy compressed equipment blob
// it guards is 12 slots long and a 13th slot would run off the end. The modern
// per-slot storage has an earring slot, so selection uses this one.
func WornItemTypeOfSlot(slot int) int {
	if slot >= 1 && slot <= EarringItemType {
		return slot
	}
	return 0
}

// withoutWornSlot drops one slot from a configured slot list. It is the mirror
// of appendWornSlot: a slot the configuration lists but the character's level
// does not open. The input slice is never written to.
func withoutWornSlot(slots []int, unwanted int) []int {
	out := make([]int, 0, len(slots))
	for _, slot := range slots {
		if slot == unwanted {
			continue
		}
		out = append(out, slot)
	}
	return out
}

// appendWornSlot adds a slot to a configured slot list without duplicating one
// the operator already listed. The input slice is never written to.
func appendWornSlot(slots []int, wanted int) []int {
	for _, slot := range slots {
		if slot == wanted {
			return slots
		}
	}
	out := make([]int, 0, len(slots)+1)
	out = append(out, slots...)
	return append(out, wanted)
}

// SelectFamilySetItems selects one complete set per equipment family.
//
// Within a family the widest set wins, so the family arrives as one set rather
// than as a set plus a few strays. A set that does not cover the whole family
// is still taken when nothing does: the archive's per-band inventory is thin
// (the special-equipment pool at the top band is a few dozen items), and a
// partial set is closer to what the operator asked for than a random draw.
func SelectFamilySetItems(candidatesBySlot map[int][]shared.EquipmentCatalogItem, minSlots int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	groups := buildSetGroups(candidatesBySlot)
	selected := make(map[int]shared.EquipmentCatalogItem)
	for _, family := range equipmentFamilies {
		wanted := 0
		for _, slot := range family {
			if len(candidatesBySlot[slot]) > 0 {
				wanted++
			}
		}
		if wanted == 0 {
			continue
		}
		group := bestFamilySetGroup(groups, family, wanted, minSlots, randIntn)
		if group == nil {
			continue
		}
		for slot, item := range selectSetGroup(group, randIntn) {
			selected[slot] = item
		}
	}
	return selected
}

// bestFamilySetGroup scores the sets that live entirely inside one family. A
// set covering slots outside the family is skipped: the cross-family part keys
// (pvf_part_12625 spans 3..12, for one) describe a progression tier rather than
// the family grouping the operator is asking for, and letting one win would
// dress one family and starve the others.
func bestFamilySetGroup(groups map[string]*setGroup, family []int, wanted, minSlots int, randIntn func(int) int) *setGroup {
	inFamily := make(map[int]bool, len(family))
	for _, slot := range family {
		inFamily[slot] = true
	}
	floor := minSlots
	if floor < 2 {
		floor = 2
	}
	// A complete family set first; the floor is only a fallback, so a small
	// family (three accessory slots) is not held to a minimum sized for armor.
	for _, required := range []int{wanted, floor} {
		var best []*setGroup
		bestScore := -1
		for _, group := range groups {
			covered, inside := 0, true
			for slot := range group.bySlot {
				if !inFamily[slot] {
					inside = false
					break
				}
				covered++
			}
			if !inside || covered < required {
				continue
			}
			score := covered*1000000 + SafeAvg(group.levelSum, group.count)*1000 + SafeAvg(group.raritySum, group.count)
			if score > bestScore {
				bestScore = score
				best = []*setGroup{group}
			} else if score == bestScore {
				best = append(best, group)
			}
		}
		if len(best) > 0 {
			return best[safeRandIntn(randIntn, len(best))]
		}
	}
	return nil
}
