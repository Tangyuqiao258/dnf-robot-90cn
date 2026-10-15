package equipment

import "strings"

// Avatar bands (时装按等级分档).
//
// The operator asked for level-differentiated 时装 — "每个等级发不同的时装,高等级人物
// 发稀有装扮套装" — and then pinned the set's shape: the native creation code
// leaves 皮肤 and 武器装扮 empty, so a 稀有装扮 set is eight pieces. The archive
// agrees: the 时装 slots are item types 20..27 (帽子 头发 脸部 胸部 上衣 下装 鞋子
// 腰带), its own 稀有装扮 family pvf_part_3 covers exactly those eight, and 皮肤
// (item type 28) is the ninth avatar slot the set leaves out.
//
// The archive carries exactly two 时装 grades, so the rule is one threshold
// rather than a ladder. From the exported catalog, 149942 items:
//
//	rarity 0  普通装扮  36426 usable, all eight slots for jobs 0-8 and 11-15
//	rarity 1  —         no avatar at all
//	rarity 2  稀有装扮   2725 usable, pvf_part_3 covers all eight for each of
//	                    those jobs
//	rarity 3+ —         no avatar at all
//
// The threshold is 71 because that is where the equipment bands switch to 橙装
// and 增幅: a robot's gear and its 时装 change grade in the same step.
//
// 光环 (item type 29) is deliberately outside the bands. It is not part of the
// 时装 set — pvf_part_3 does not cover it — and the operator asked for it to
// keep the treatment it already has.
const (
	// AvatarMinLevel is the level from which a robot is dressed at all. Below it
	// the client only accepts 普通时装 — 高级时装 needs level 30 — and splitting the
	// low levels that finely is not worth it, so the operator's rule is to grant
	// nothing rather than to grant the one grade that fits.
	AvatarMinLevel = 30
	// RareAvatarMinLevel is the level from which a robot is dressed in a
	// complete 稀有装扮 set.
	RareAvatarMinLevel = 71
	// CommonAvatarRarity and RareAvatarRarity are the two grades the archive
	// carries for 时装, in the same PVF [rarity] field the equipment bands use.
	// The names mean something else here than they do for gear: for an avatar,
	// 2 is 稀有装扮 — the 天空套 family — not 紫装.
	CommonAvatarRarity = RarityCommon
	RareAvatarRarity   = RarityRare
	// AvatarSlotCount is how many slots make up the 时装 set, and therefore the
	// coverage a 稀有装扮 set has to reach to be the one that is taken.
	AvatarSlotCount = 8
	// AvatarSkinSlot is 皮肤 (item type 28), the slot the native creation code
	// leaves empty, so nothing is ever granted in it.
	AvatarSkinSlot = 8
	// AvatarAuraSlot is 光环 (item type 29). It is granted on its own terms,
	// outside the level bands.
	AvatarAuraSlot = 9
	// avatarSlotCeiling is the highest slot an avatar_slots setting may name.
	avatarSlotCeiling = AvatarAuraSlot
)

// Avatar set families (同名前缀即一套).
//
// The archive's own set key cannot group a 装扮 set. Every 稀有装扮 piece of a job
// carries the same key -- pvf_part_3 -- so the key names the whole 稀有装扮 family
// of that job, 279 pieces per slot for a thief, and a set drawn from it puts a
// helmet from one 天空套 next to a coat from another. The operator's rule is what
// the archive does say: the pieces of one set are named alike. 暗影魅月魔音头盔,
// 暗影魅月魔音长发 and 暗影魅月魔音眼妆 are one set, and "前四个字一样的基本就是一套的"
// is the rule verbatim.
//
// The key is therefore the first four characters of the name, and the family is
// then narrowed to the seed piece's own set: every other slot takes the candidate
// whose name shares the longest prefix with the seed. Two sets that share a
// four-character root -- the archive has 62 such collisions -- can therefore
// never be mixed, because a same-family candidate always shares more.
const (
	// AvatarFamilyMinPrefix is how many leading characters name a family.
	AvatarFamilyMinPrefix = 4
)

// AvatarFamilyKey returns the set family an avatar name belongs to. A name too
// short to name a family returns "", and the caller falls back to the archive's
// set key.
func AvatarFamilyKey(name string) string {
	runes := []rune(strings.TrimSpace(name))
	if len(runes) < AvatarFamilyMinPrefix {
		return ""
	}
	return string(runes[:AvatarFamilyMinPrefix])
}

// AvatarNameAffinity counts how many leading characters two avatar names share,
// capped at the shorter of the two.
func AvatarNameAffinity(left, right string) int {
	leftRunes, rightRunes := []rune(left), []rune(right)
	limit := len(leftRunes)
	if len(rightRunes) < limit {
		limit = len(rightRunes)
	}
	shared := 0
	for shared < limit && leftRunes[shared] == rightRunes[shared] {
		shared++
	}
	return shared
}

// AvatarRarityFor returns the 时装 grade a character of this level wears.
func AvatarRarityFor(level int) int {
	if level >= RareAvatarMinLevel {
		return RareAvatarRarity
	}
	return CommonAvatarRarity
}

// AvatarRarityBand maps an item's grade onto the band it belongs to.
//
// The band is a threshold rather than an equality so that a grade the archive
// gains later is still worn by somebody instead of vanishing: the only grades
// that reach the pool today are 普通装扮 (0) and 稀有装扮 (2), the ten rarity-3
// avatar records the archive also carries having no name and so failing the
// renderability filter.
func AvatarRarityBand(rarity int) int {
	if rarity >= RareAvatarRarity {
		return RareAvatarRarity
	}
	return CommonAvatarRarity
}

// AvatarSetMinSlotsFor returns the coverage a 时装 set has to reach to be taken.
//
// Below the threshold the operator's configured floor stands: any complete
// enough set is fine, and the random fill dresses the rest. From the threshold
// the set is the whole point, so the floor becomes every slot — that is what
// makes the archive's pvf_part_3 the only group that qualifies, and what the
// operator asked for when they said the 稀有装扮 set is eight pieces.
func AvatarSetMinSlotsFor(level int, configured int) int {
	if level >= RareAvatarMinLevel {
		return AvatarSlotCount
	}
	if configured < 2 {
		return 2
	}
	if configured > AvatarSlotCount {
		return AvatarSlotCount
	}
	return configured
}

// AvatarSlotsInUse returns the avatar slots a robot is actually dressed in: the
// configured list without 皮肤.
//
// 皮肤 is dropped rather than merely left to the candidate pool because the
// native creation code never fills it, and because a slot list that still
// carried it would report every robot as incomplete in the population panel. An
// empty list means the default set, which is the eight 时装 slots plus 光环.
func AvatarSlotsInUse(slots []int) []int {
	if len(slots) == 0 {
		slots = []int{0, 1, 2, 3, 4, 5, 6, 7, AvatarAuraSlot}
	}
	out := make([]int, 0, len(slots))
	seen := make(map[int]bool, len(slots))
	for _, slot := range slots {
		if slot < 0 || slot > avatarSlotCeiling || slot == AvatarSkinSlot || seen[slot] {
			continue
		}
		seen[slot] = true
		out = append(out, slot)
	}
	return out
}
