package equipment

import (
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// titleSlot is the equipment slot a title occupies. The game's slot 2 is the
// title name, and item type 2 is what the catalog files a title under.
const titleSlot = 2

// PinnedTitleItem returns the title item id a robot of this level wears, or 0
// when titles are switched off or nothing is pinned.
//
// A title is worn rather than drawn. The operator names the item the fleet
// wears and the one a robot at or above TitleTopLevel wears instead, so the
// same fleet can read as two ranks. Both reference titles are universal level-1
// items, which is why the level gate lives here: the item's own minimum level
// cannot say "only the level-90 robots".
func PinnedTitleItem(level int, rc robotconfig.RuntimeConfig) int {
	if !rc.TitleEnabled {
		return 0
	}
	if rc.TitleTopLevel > 0 && level >= rc.TitleTopLevel && rc.TitleTopItem > 0 {
		return rc.TitleTopItem
	}
	if rc.TitleItem > 0 {
		return rc.TitleItem
	}
	return 0
}

// pinnedTitle finds the pinned title's catalog record. A pin naming an item the
// catalog does not carry leaves the slot empty rather than falling back to a
// draw: the operator asked for that title, or for none.
func pinnedTitle(level int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem) (shared.EquipmentCatalogItem, bool) {
	id := PinnedTitleItem(level, rc)
	if id <= 0 {
		return shared.EquipmentCatalogItem{}, false
	}
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
