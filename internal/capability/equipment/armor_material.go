package equipment

import (
	"strings"

	"robot/internal/shared"
)

// The game's PVF stores armor under
// equipment/character/<race>/<part>/<material>/<item>.equ, where <part> is the
// item type directory (jacket, shoulder, pants, shoes, waist). A scan of every
// item type 3..7 record in the live Script.pvf finds exactly these five
// material directories, plus ten equipment/character/common/etc records that
// are not armor at all.
const (
	ArmorCloth   = "cloth"   // 布甲
	ArmorLeather = "leather" // 皮甲
	ArmorLight   = "larmor"  // 轻甲
	ArmorHeavy   = "harmor"  // 重甲
	ArmorPlate   = "plate"   // 板甲

	firstArmorItemType = 3 // coat
	lastArmorItemType  = 7 // waist
)

// ArmorMasteryBranch is one transfer branch the table covers: its first-grow
// value, the [growtype name] the PVF lists at that position, and the single
// armor material the branch masters.
type ArmorMasteryBranch struct {
	Branch   int
	Name     string
	Material string
}

// jobArmorMastery is the armor mastery table, keyed by the job id the robot
// stores (the base class) and the transfer branch in the low nibble of
// grow_type. Only branches character/*.chr actually releases are listed, so the
// phantom sixth position jobs 3, 5 and 6 carry is absent. A job the table has
// no entry for is unrestricted.
//
// The game server does not carry this table anywhere the robot can read — the
// PVF's [usable job] tag only distinguishes base classes, and the material
// directory of an item is just its path — so it is stated here and checked
// against the archive by the armor-mastery test in the cn90 backend.
var jobArmorMastery = map[int][]ArmorMasteryBranch{
	// 鬼剑士(男): 剑魂 | 鬼泣 | 狂战士 | 阿修罗
	0: {
		{Branch: 1, Name: "剑魂", Material: ArmorLight},
		{Branch: 2, Name: "鬼泣", Material: ArmorCloth},
		{Branch: 3, Name: "狂战士", Material: ArmorHeavy},
		{Branch: 4, Name: "阿修罗", Material: ArmorPlate},
	},
	// 格斗家(女): 气功师 | 散打 | 街霸 | 柔道家
	1: {
		{Branch: 1, Name: "气功师", Material: ArmorCloth},
		{Branch: 2, Name: "散打", Material: ArmorLight},
		{Branch: 3, Name: "街霸", Material: ArmorHeavy},
		{Branch: 4, Name: "柔道家", Material: ArmorLight},
	},
	// 神枪手(男): 漫游枪手 | 枪炮师 | 机械师 | 弹药专家
	2: {
		{Branch: 1, Name: "漫游枪手", Material: ArmorLeather},
		{Branch: 2, Name: "枪炮师", Material: ArmorHeavy},
		{Branch: 3, Name: "机械师", Material: ArmorCloth},
		{Branch: 4, Name: "弹药专家", Material: ArmorLeather},
	},
	// 魔法师(女): 元素师 | 召唤师 | 战斗法师 | 魔道学者
	3: {
		{Branch: 1, Name: "元素师", Material: ArmorCloth},
		{Branch: 2, Name: "召唤师", Material: ArmorCloth},
		{Branch: 3, Name: "战斗法师", Material: ArmorLeather},
		{Branch: 4, Name: "魔道学者", Material: ArmorLeather},
	},
	// 圣职者(男): 圣骑士 | 蓝拳圣使 | 驱魔师 | 复仇者
	4: {
		{Branch: 1, Name: "圣骑士", Material: ArmorPlate},
		{Branch: 2, Name: "蓝拳圣使", Material: ArmorLight},
		{Branch: 3, Name: "驱魔师", Material: ArmorPlate},
		{Branch: 4, Name: "复仇者", Material: ArmorHeavy},
	},
	// 神枪手(女): 漫游枪手 | 枪炮师 | 机械师 | 弹药专家
	5: {
		{Branch: 1, Name: "漫游枪手", Material: ArmorLeather},
		{Branch: 2, Name: "枪炮师", Material: ArmorHeavy},
		{Branch: 3, Name: "机械师", Material: ArmorCloth},
		{Branch: 4, Name: "弹药专家", Material: ArmorLeather},
	},
	// 暗夜使者: 刺客 | 死灵术士 | 忍者 | 影舞者
	6: {
		{Branch: 1, Name: "刺客", Material: ArmorLeather},
		{Branch: 2, Name: "死灵术士", Material: ArmorLight},
		{Branch: 3, Name: "忍者", Material: ArmorCloth},
		{Branch: 4, Name: "影舞者", Material: ArmorLeather},
	},
	// 格斗家(男): 气功师 | 散打 | 街霸 | 柔道家
	7: {
		{Branch: 1, Name: "气功师", Material: ArmorCloth},
		{Branch: 2, Name: "散打", Material: ArmorLight},
		{Branch: 3, Name: "街霸", Material: ArmorHeavy},
		{Branch: 4, Name: "柔道家", Material: ArmorLight},
	},
	// 魔法师(男): 元素爆破师 | 冰结师 | 血法师 | 逐风者 | 次元行者
	8: {
		{Branch: 1, Name: "元素爆破师", Material: ArmorCloth},
		{Branch: 2, Name: "冰结师", Material: ArmorLeather},
		{Branch: 3, Name: "血法师", Material: ArmorLeather},
		{Branch: 4, Name: "逐风者", Material: ArmorLeather},
		{Branch: 5, Name: "次元行者", Material: ArmorCloth},
	},
	// 女鬼剑士: 驭剑士 | 暗殿骑士 | 契魔者 | 流浪武士
	11: {
		{Branch: 1, Name: "驭剑士", Material: ArmorLight},
		{Branch: 2, Name: "暗殿骑士", Material: ArmorPlate},
		{Branch: 3, Name: "契魔者", Material: ArmorHeavy},
		{Branch: 4, Name: "流浪武士", Material: ArmorLight},
	},
	// 守护者: 精灵骑士 | 混沌魔灵 | 帕拉丁 | 龙骑士. The archive lists 帕拉丁
	// third and 龙骑士 fourth, which is not the order the class is usually
	// written in — the branch numbers follow the archive.
	12: {
		{Branch: 1, Name: "精灵骑士", Material: ArmorPlate},
		{Branch: 2, Name: "混沌魔灵", Material: ArmorHeavy},
		{Branch: 3, Name: "帕拉丁", Material: ArmorPlate},
		{Branch: 4, Name: "龙骑士", Material: ArmorLight},
	},
	// 魔枪士: 征战者 | 决战者 | 狩猎者 | 暗枪士
	13: {
		{Branch: 1, Name: "征战者", Material: ArmorHeavy},
		{Branch: 2, Name: "决战者", Material: ArmorLight},
		{Branch: 3, Name: "狩猎者", Material: ArmorHeavy},
		{Branch: 4, Name: "暗枪士", Material: ArmorLeather},
	},
	// 女圣职者: 圣骑士 | 异端审判者 | 巫女 | 诱魔者
	14: {
		{Branch: 1, Name: "圣骑士", Material: ArmorPlate},
		{Branch: 2, Name: "异端审判者", Material: ArmorHeavy},
		{Branch: 3, Name: "巫女", Material: ArmorCloth},
		{Branch: 4, Name: "诱魔者", Material: ArmorLeather},
	},
	// 枪剑士: 暗刃 | 特工 | 战线佣兵 | 源能专家
	15: {
		{Branch: 1, Name: "暗刃", Material: ArmorLight},
		{Branch: 2, Name: "特工", Material: ArmorLeather},
		{Branch: 3, Name: "战线佣兵", Material: ArmorHeavy},
		{Branch: 4, Name: "源能专家", Material: ArmorCloth},
	},
}

// fixedJobArmorMaterials covers the jobs the archive gives no transfer branch,
// so the grow nibble is always 0. Its value is the set of materials the class
// masters: 黑暗武士 masters four of the five and is the reason this is a set
// rather than the single material a transfer branch carries; 缔造者 masters one.
var fixedJobArmorMaterials = map[int][]string{
	9:  {ArmorCloth, ArmorLight, ArmorHeavy, ArmorPlate}, // 黑暗武士: no leather
	10: {ArmorPlate},                                     // 缔造者
}

// ArmorMaterialsFor returns the armor materials the character behind job/grow
// may wear, or nil when nothing restricts it. grow_type packs the awakening
// stage in the high nibble and the transfer branch in the low one, so the
// branch is the low nibble. A robot whose branch this table does not know is
// left unrestricted: a table that is missing an entry must widen the catalog,
// never silently empty it.
func ArmorMaterialsFor(job, grow int) []string {
	branch := grow & 0x0f
	if branch == 0 {
		return fixedJobArmorMaterials[job]
	}
	for _, mastery := range jobArmorMastery[job] {
		if mastery.Branch == branch {
			return []string{mastery.Material}
		}
	}
	return nil
}

// ArmorMasteryBranches returns the branches the table covers for a job. The
// armor-mastery archive check compares their names against character/*.chr, so
// a PVF whose branch order differs cannot silently mis-key the table.
func ArmorMasteryBranches(job int) []ArmorMasteryBranch {
	branches := jobArmorMastery[job]
	out := make([]ArmorMasteryBranch, len(branches))
	copy(out, branches)
	return out
}

// ArmorMaterialOfPath returns the material directory of an armor item path, for
// example "plate" in
// equipment/character/common/jacket/plate/coat_exorcismpray.equ. It reads only
// the directory the file sits in, so a part or race directory spelled like a
// material cannot be mistaken for one.
func ArmorMaterialOfPath(path string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	parts := strings.Split(strings.Trim(normalized, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	material := parts[len(parts)-2]
	if !isArmorMaterial(material) {
		return ""
	}
	return material
}

func isArmorMaterial(token string) bool {
	switch token {
	case ArmorCloth, ArmorLeather, ArmorLight, ArmorHeavy, ArmorPlate:
		return true
	default:
		return false
	}
}

// armorAllowed applies the material filter. Only coat, shoulder, pants, shoes
// and waist carry a material; every other item type is unrestricted. An armor
// item whose path names no known material is refused while a material is
// required, because nothing in its record proves it matches.
func armorAllowed(item shared.EquipmentCatalogItem, materials []string) bool {
	if len(materials) == 0 || item.ItemType < firstArmorItemType || item.ItemType > lastArmorItemType {
		return true
	}
	material := ArmorMaterialOfPath(item.Path)
	if material == "" {
		return false
	}
	for _, allowed := range materials {
		if allowed == material {
			return true
		}
	}
	return false
}
