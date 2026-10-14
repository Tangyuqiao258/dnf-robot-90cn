package equipment

// Equipment level bands (装备稀有度/强化按等级带).
//
// The operator names one rarity and one upgrade range per level band, so a
// robot's gear gets fancier as it levels, and from level 85 it carries 增幅
// rather than 强化. Rarity and upgrade sit in one table because they change
// together: a band is a statement about how a robot of that level should look.
//
// The rarity numbers are the PVF's [rarity] field, named the way the game
// server names them in its etc/amplifyitem.etc reader:
//
//	0 common    普通
//	1 uncommon  高级
//	2 rare      稀有 (紫)
//	3 unique    神器 (粉)
//	4 epic      史诗 (橙)
//	5 chronicle 勇者 (异界)
//	6 legendary 传说
//
// The bands are deliberately not ordered by that number. The operator ranks
// 传说 above 神器 and 史诗 above 传说, so the sequence runs 2, 3, 6, 4.
// Chronicle gear is never granted: it is the 异界 set.
const (
	RarityCommon    = 0
	RarityUncommon  = 1
	RarityRare      = 2
	RarityUnique    = 3
	RarityEpic      = 4
	RarityChronicle = 5
	RarityLegendary = 6

	// equipmentLevelGrid is the step the gear level snaps down to: a level-87
	// robot wears level-85 equipment rather than the 87 the item's own minimum
	// level would allow.
	equipmentLevelGrid = 5
)

// EquipmentBand is one level band: the highest character level it covers, the
// rarity that band's gear carries, the reinforcement its gear is rolled at,
// and whether that reinforcement is 增幅 rather than 强化.
type EquipmentBand struct {
	// MaxLevel is the highest level the band covers. Zero means no ceiling,
	// which is how the last band is written.
	MaxLevel   int
	Rarity     int
	UpgradeMin int
	UpgradeMax int
	// Amplified marks 增幅: the same upgrade level, carried by equipment that
	// has been purified, which the client draws and prices differently.
	Amplified bool
}

// equipmentBands is the band table. The last entry has no ceiling, so every
// level is covered and a caller never has to handle a level with no band. The
// operator wrote the first two as a 强化 range and the last two as 增幅, which
// is why Amplified turns on at 71 and stays on.
var equipmentBands = []EquipmentBand{
	{MaxLevel: 35, Rarity: RarityRare, UpgradeMin: 7, UpgradeMax: 9},
	{MaxLevel: 70, Rarity: RarityUnique, UpgradeMin: 10, UpgradeMax: 11},
	{MaxLevel: 84, Rarity: RarityLegendary, UpgradeMin: 10, UpgradeMax: 12, Amplified: true},
	{MaxLevel: 0, Rarity: RarityEpic, UpgradeMin: 9, UpgradeMax: 13, Amplified: true},
}

// armorFallbackRarities is the order an armor slot falls back through when the
// character's band rarity has nothing to offer it, best first in the operator's
// quality order.
//
// A band states how a robot of that level should look, and the selector honors
// it wherever the archive can pay for it. Where the archive cannot, an empty
// slot is the worst outcome, and the archive cannot for exactly the band the
// fleet lives in: a census of the clean armor records below level 70 — unblocked,
// unexpired, client-parseable — finds nine 神器 布甲, nine 轻甲, six 皮甲, and no
// 重甲 or 板甲 at all, so without a fallback nineteen of the twenty mastery
// branches (every 重甲 and 板甲 one) would dress no armor at any level from 36 to
// 70. The same archive holds 41 重甲 and 14 板甲 史诗 records at those levels and
// 21 传说 records per material, which is where the slot lands instead.
//
// 勇者 is absent on purpose: it is the 异界 set and no robot may wear it, however
// thin the band's own pool turns out to be.
var armorFallbackRarities = []int{RarityEpic, RarityLegendary, RarityRare, RarityUncommon, RarityCommon}

// EquipmentBandFor returns the band a character of this level wears. A level
// below the first band's ceiling still lands in a band, and a negative level
// falls into the first one rather than out of the table.
func EquipmentBandFor(level int) EquipmentBand {
	for _, band := range equipmentBands {
		if band.MaxLevel > 0 && level <= band.MaxLevel {
			return band
		}
	}
	return equipmentBands[len(equipmentBands)-1]
}

// EquipmentLevels returns the band table, for callers that have to state the
// whole rule rather than one level's slice of it.
func EquipmentLevels() []EquipmentBand {
	out := make([]EquipmentBand, len(equipmentBands))
	copy(out, equipmentBands)
	return out
}

// BandRarityFor returns the rarity a character of this level wears.
func BandRarityFor(level int) int {
	return EquipmentBandFor(level).Rarity
}

// EquipLevelFor rounds a character level down to the equipment level grid, so
// the fleet's gear levels read as round numbers. Levels below the first step
// are left alone: there is nothing to round down to.
func EquipLevelFor(level int) int {
	if level < equipmentLevelGrid {
		return level
	}
	return level - level%equipmentLevelGrid
}
