package equipment

// Amplification attributes (增幅属性).
//
// An amplified item carries one of four attributes, written to the client as a
// single number 1..4. This archive's etc/amplifyitem.etc names those numbers by
// the stat each one raises, not by the stat it belongs to:
//
//	[option mapping table]
//	`[NONE]` 0 `[physical defense]` 1 `[magical defense]` 2
//	`[physical attack]` 3 `[magical attack]` 4 `[all]` 5
//	[/option mapping table]
//
// The [invest option] section pairs each name with the material that grants it,
// and those materials name the stat outright:
//
//	`[physical defense]`  1186 纯净的生命增幅书  -> 体力
//	`[magical defense]`   1187 纯净的魔力增幅书  -> 精神
//	`[physical attack]`   1184 纯净的力量增幅书  -> 力量
//	`[magical attack]`    1185 纯净的智力增幅书  -> 智力
//
// So 1..4 runs 体力, 精神, 力量, 智力 — not 力量, 智力, 体力, 精神. The names
// in the mapping table are what the four stats buy: 体力 raises physical
// defense and 精神 raises magical defense, while 力量 raises physical attack
// and 智力 raises magical attack.
//
// Which attribute a robot amplifies follows its class, and for a class that
// branches, its transfer branch. The archive states that nowhere the robot can
// read, so the table is written here the way the armor mastery table is.
const (
	// AmplifyVitality is 体力, the etc file's [physical defense].
	AmplifyVitality = 1
	// AmplifySpirit is 精神, the etc file's [magical defense].
	AmplifySpirit = 2
	// AmplifyStrength is 力量, the etc file's [physical attack].
	AmplifyStrength = 3
	// AmplifyIntelligence is 智力, the etc file's [magical attack].
	AmplifyIntelligence = 4
)

// jobAmplifyAttribute maps a transfer branch to the attribute that branch
// amplifies. The branch numbers and their classes are the ones
// jobArmorMastery carries, and a test holds the two tables to the same branch
// set so neither can drift alone. A job the table does not name is not
// amplified.
//
// Two entries resolve an attribute the operator gave as a choice:
//
//   - 圣骑士(男) (job 4 branch 1) is 体精平衡 — the game reads whichever of
//     体力 and 精神 is higher — so 体力 is written and the balance holds.
//   - 驱魔师 (job 4 branch 3) exists as a physical and a magical build. A robot
//     has no build to inspect, so the physical one is written.
var jobAmplifyAttribute = map[int]map[int]int{
	// 鬼剑士(男): 剑魂 | 鬼泣 | 狂战士 | 阿修罗
	0: {1: AmplifyStrength, 2: AmplifyIntelligence, 3: AmplifyStrength, 4: AmplifyIntelligence},
	// 格斗家(女): 气功师 | 散打 | 街霸 | 柔道家
	1: {1: AmplifyIntelligence, 2: AmplifyStrength, 3: AmplifyStrength, 4: AmplifyStrength},
	// 神枪手(男): 漫游枪手 | 枪炮师 | 机械师 | 弹药专家
	2: {1: AmplifyStrength, 2: AmplifyStrength, 3: AmplifyIntelligence, 4: AmplifyStrength},
	// 魔法师(女): 元素师 | 召唤师 | 战斗法师 | 魔道学者
	3: {1: AmplifyIntelligence, 2: AmplifyIntelligence, 3: AmplifyIntelligence, 4: AmplifyIntelligence},
	// 圣职者(男): 圣骑士 | 蓝拳圣使 | 驱魔师 | 复仇者
	4: {1: AmplifyVitality, 2: AmplifyStrength, 3: AmplifyStrength, 4: AmplifyStrength},
	// 神枪手(女): 漫游枪手 | 枪炮师 | 机械师 | 弹药专家
	5: {1: AmplifyStrength, 2: AmplifyStrength, 3: AmplifyIntelligence, 4: AmplifyIntelligence},
	// 暗夜使者: 刺客 | 死灵术士 | 忍者 | 影舞者
	6: {1: AmplifyStrength, 2: AmplifyIntelligence, 3: AmplifyIntelligence, 4: AmplifyStrength},
	// 格斗家(男): 气功师 | 散打 | 街霸 | 柔道家
	7: {1: AmplifyIntelligence, 2: AmplifyStrength, 3: AmplifyStrength, 4: AmplifyStrength},
	// 魔法师(男): 元素爆破师 | 冰结师 | 血法师 | 逐风者 | 次元行者
	8: {1: AmplifyIntelligence, 2: AmplifyIntelligence, 3: AmplifyIntelligence, 4: AmplifyStrength, 5: AmplifyIntelligence},
	// 女鬼剑士: 驭剑士 | 暗殿骑士 | 契魔者 | 流浪武士
	11: {1: AmplifyStrength, 2: AmplifyIntelligence, 3: AmplifyStrength, 4: AmplifyStrength},
	// 守护者: 精灵骑士 | 混沌魔灵 | 帕拉丁 | 龙骑士
	12: {1: AmplifyStrength, 2: AmplifyIntelligence, 3: AmplifyStrength, 4: AmplifyStrength},
	// 魔枪士: 征战者 | 决战者 | 狩猎者 | 暗枪士
	13: {1: AmplifyStrength, 2: AmplifyStrength, 3: AmplifyStrength, 4: AmplifyIntelligence},
	// 女圣职者: 圣骑士 | 异端审判者 | 巫女 | 诱魔者
	14: {1: AmplifyIntelligence, 2: AmplifyStrength, 3: AmplifyIntelligence, 4: AmplifyIntelligence},
	// 枪剑士: 暗刃 | 特工 | 战线佣兵 | 源能专家
	15: {1: AmplifyStrength, 2: AmplifyStrength, 3: AmplifyStrength, 4: AmplifyIntelligence},
}

// fixedJobAmplifyAttribute covers the 外传 jobs, which the archive gives no
// transfer branch. 黑暗武士 is 力智同调 — either attribute has the same effect,
// so 力量 is written — and 缔造者 is 智力.
var fixedJobAmplifyAttribute = map[int]int{
	9:  AmplifyStrength,
	10: AmplifyIntelligence,
}

// AmplifyAttributeFor returns the attribute a character behind job/grow
// amplifies, or 0 when the table does not name it. A branch the table knows
// nothing about is not amplified: without a branch there is no evidence which
// attribute the class uses, and picking one would invent a rule.
func AmplifyAttributeFor(job, grow int) int {
	branch := grow & 0x0f
	if branch == 0 {
		return fixedJobAmplifyAttribute[job]
	}
	return jobAmplifyAttribute[job][branch]
}

// AmplifyAttributes returns the branch-to-attribute map of one job, for callers
// that have to state the whole rule rather than one character's slice of it.
func AmplifyAttributes(job int) map[int]int {
	out := make(map[int]int, len(jobAmplifyAttribute[job]))
	for branch, attribute := range jobAmplifyAttribute[job] {
		out[branch] = attribute
	}
	return out
}
