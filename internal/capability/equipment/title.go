package equipment

import (
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// titleSlot is the equipment slot a title occupies. The game's slot 2 is the
// title name, and item type 2 is what the catalog files a title under.
const titleSlot = 2

// TitleTierItemIDs returns the titles a robot of this level may wear, in the
// order they are drawn from.
//
// A title is worn rather than drawn -- the operator names the item the fleet
// wears -- with one exception: a tier. The operator asked for one rank of four
// 称号 handed out at random -- "称号增加龙之威仪,龙之挑战,桃园结义与兽人守护神一档随机
// 发放" -- so a configured tier is drawn from per robot, which is what makes a
// crowd of them read as a crowd. The top rank still wins when the level reaches
// it: that rank exists to say "level 90", and a draw inside it would say nothing.
//
// 桃园结义's two records carry a truncated bracket in their name (桃园结义 [义,
// 桃园结义 [侠) and the grant gate therefore bars them: the operator's rule is that
// no bracketed name reaches a robot. Naming the tier does not exempt them, so the
// tier hands out the three titles the gate lets through.
func TitleTierItemIDs(level int, rc robotconfig.RuntimeConfig) []int {
	if !rc.TitleEnabled {
		return nil
	}
	if rc.TitleTopLevel > 0 && level >= rc.TitleTopLevel && rc.TitleTopItem > 0 {
		return []int{rc.TitleTopItem}
	}
	if len(rc.TitleTierItems) > 0 {
		return rc.TitleTierItems
	}
	if rc.TitleItem > 0 {
		return []int{rc.TitleItem}
	}
	return nil
}

// PinnedTitleItem returns the first title id a robot of this level may wear, or 0
// when titles are switched off or nothing is pinned. Callers that need the worn
// title rather than the question "is a title pinned at all" draw from the tier.
func PinnedTitleItem(level int, rc robotconfig.RuntimeConfig) int {
	ids := TitleTierItemIDs(level, rc)
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

// TitleAllowed reports whether a robot of this level may wear the title with
// that id. The check is the tier, not the pin's first entry: a robot that drew
// 龙之挑战 holds a title this level is owed, and a repair pass that accepted only
// the first id would strip it and hand out another every time it ran.
func TitleAllowed(level int, rc robotconfig.RuntimeConfig, id int) bool {
	for _, want := range TitleTierItemIDs(level, rc) {
		if want == id {
			return true
		}
	}
	return false
}

// pinnedTitle finds the catalog record of the title this robot wears. A tier
// whose ids the catalog does not carry leaves the slot empty rather than falling
// back to a draw: the operator asked for those titles, or for none.
func pinnedTitle(level int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem, randIntn func(int) int) (shared.EquipmentCatalogItem, bool) {
	ids := TitleTierItemIDs(level, rc)
	if len(ids) == 0 {
		return shared.EquipmentCatalogItem{}, false
	}
	id := ids[safeRandIntn(randIntn, len(ids))]
	for _, item := range items {
		if item.ID == id && item.ItemType == titleSlot && !item.Expire && !item.GrantBlocked {
			return item, true
		}
	}
	return shared.EquipmentCatalogItem{}, false
}

// equipSlotsFor returns the slots a loadout covers: the configured ones, plus
// the title slot while a title is pinned. Titles never join the candidate pool
// -- SelectEquipment pins them after the draw -- but the repair check has to
// walk the slot, or a robot would keep a title the operator has since changed.
func equipSlotsFor(level int, rc robotconfig.RuntimeConfig) []int {
	slots := rc.EquipSlots
	if len(slots) == 0 {
		slots = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	if PinnedTitleItem(level, rc) <= 0 {
		return slots
	}
	for _, slot := range slots {
		if slot == titleSlot {
			return slots
		}
	}
	out := make([]int, 0, len(slots)+1)
	out = append(out, slots...)
	return append(out, titleSlot)
}
