package pvf

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"robot/internal/shared"
)

type pvfThemeGroup struct {
	key       string
	jobs      string
	minID     int
	maxID     int
	slots     map[int]bool
	tokenHits map[string]int
}

func attachPVFThemeMatches(items []shared.EquipmentCatalogItem, assigned map[int]bool, masterIDs map[int]bool) {
	groupsByKey := make(map[string]*pvfThemeGroup)
	for index := range items {
		item := items[index]
		if !assigned[item.ID] || item.SetKey == "" {
			continue
		}
		group := groupsByKey[item.SetKey]
		if group == nil {
			group = &pvfThemeGroup{key: item.SetKey, jobs: pvfItemJobsKey(item), minID: item.ID, maxID: item.ID, slots: make(map[int]bool), tokenHits: make(map[string]int)}
			groupsByKey[item.SetKey] = group
		}
		if item.ID < group.minID {
			group.minID = item.ID
		}
		if item.ID > group.maxID {
			group.maxID = item.ID
		}
		group.slots[item.ItemType] = true
		for token := range pvfThemeTokens(item.Name2) {
			group.tokenHits[token]++
		}
	}
	groups := make([]*pvfThemeGroup, 0, len(groupsByKey))
	for _, group := range groupsByKey {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].key < groups[j].key })

	for i := range items {
		item := &items[i]
		if assigned[item.ID] || item.ItemType < 20 || item.ItemType > 28 || masterIDs[item.ID] {
			continue
		}
		tokens := pvfThemeTokens(item.Name2)
		if len(tokens) == 0 {
			continue
		}
		bestDistance := 0
		matches := make([]*pvfThemeGroup, 0)
		for _, group := range groups {
			if group.jobs != pvfItemJobsKey(*item) || group.slots[item.ItemType] {
				continue
			}
			score := 0
			for token := range tokens {
				if group.tokenHits[token] > score {
					score = group.tokenHits[token]
				}
			}
			if score < 2 {
				continue
			}
			distance := pvfIDRangeDistance(item.ID, group.minID, group.maxID)
			if distance > 10000 {
				continue
			}
			if len(matches) == 0 || distance < bestDistance {
				bestDistance = distance
			}
			matches = append(matches, group)
		}
		if len(matches) == 0 {
			continue
		}
		keys := make([]string, 0, len(matches))
		for _, group := range matches {
			if pvfIDRangeDistance(item.ID, group.minID, group.maxID) <= bestDistance+32 {
				keys = append(keys, group.key)
			}
		}
		if len(keys) > 0 {
			sort.Strings(keys)
			item.SetKey = strings.Join(keys, "|")
			assigned[item.ID] = true
		}
	}
}

func pvfItemJobsKey(item shared.EquipmentCatalogItem) string {
	jobs := append([]int(nil), item.UseJob...)
	sort.Ints(jobs)
	parts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		parts = append(parts, strconv.Itoa(job))
	}
	return strings.Join(parts, ",")
}

func pvfThemeTokens(value string) map[string]bool {
	value = strings.ToLower(value)
	fields := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := make(map[string]bool)
	for _, field := range fields {
		if len(field) < 4 || pvfThemeStopWord(field) || strings.HasPrefix(field, "name") {
			continue
		}
		out[field] = true
	}
	return out
}

func pvfThemeStopWord(value string) bool {
	switch value {
	case "black", "white", "blue", "green", "yellow", "gold", "golden", "silver", "gray", "grey", "brown", "purple", "violet", "pink", "orange", "apricot", "cream", "creamy", "dark", "light", "deep", "red",
		"avatar", "official", "special", "clone", "rare", "event", "package", "equipment", "priest", "gunner", "fighter", "swordman", "slayer", "mage", "thief",
		"coat", "shirt", "shirts", "pants", "shoes", "boots", "belt", "waist", "hair", "face", "skin", "body", "hat", "cap", "neck", "necklace", "earring", "gloves":
		return true
	default:
		return false
	}
}

func pvfIDRangeDistance(id, minID, maxID int) int {
	if id < minID {
		return minID - id
	}
	if id > maxID {
		return id - maxID
	}
	return 0
}

type nameFamilyGroup struct {
	key   string
	slots map[int]bool
}

// attachNameFamilySets assigns a shared set key to items whose names share a
// family within the same job group, provided the family covers at least
// minSlots distinct item types. Both the avatar and the equipment family
// detection use the same two-pass grouping.
func attachNameFamilySets(items []shared.EquipmentCatalogItem, prefix string, inFamily func(shared.EquipmentCatalogItem) bool, familyOf func(string) string, minSlots int) {
	groups := make(map[string]*nameFamilyGroup)
	for index := range items {
		item := items[index]
		if !inFamily(item) {
			continue
		}
		family := familyOf(item.Name)
		if family == "" {
			continue
		}
		key := pvfItemJobsKey(item) + ":" + family
		group := groups[key]
		if group == nil {
			group = &nameFamilyGroup{key: prefix + shortHash(key), slots: make(map[int]bool)}
			groups[key] = group
		}
		group.slots[item.ItemType] = true
	}
	for index := range items {
		item := items[index]
		if !inFamily(item) || strings.HasPrefix(item.SetKey, "pvf_") {
			continue
		}
		family := familyOf(item.Name)
		if family == "" {
			continue
		}
		group := groups[pvfItemJobsKey(item)+":"+family]
		if group == nil || len(group.slots) < minSlots {
			continue
		}
		items[index].SetKey = group.key
	}
}

// Some simulator PVFs do not expose avatar set-master records. Their item names
// still carry a shared theme while color and slot words differ per slot.
func attachAvatarNameFamilies(items []shared.EquipmentCatalogItem) {
	attachNameFamilySets(items, "avatar_", func(item shared.EquipmentCatalogItem) bool {
		return item.ItemType >= 20 && item.ItemType <= 29
	}, avatarNameFamily, 6)
}

func attachEquipmentNameFamilies(items []shared.EquipmentCatalogItem) {
	attachNameFamilySets(items, "equipment_", func(item shared.EquipmentCatalogItem) bool {
		return equipmentArmorSetSlot(item.ItemType)
	}, equipmentNameFamily, 5)
}

func equipmentArmorSetSlot(itemType int) bool {
	// A five-piece set is the armor block only. Weapons, jewelry and the two
	// special slots use independent item families and must not inflate armor
	// set coverage.
	return itemType >= 3 && itemType <= 7
}

func equipmentNameFamily(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.EqualFold(value, "errorstring") {
		return ""
	}
	for _, word := range []string{
		"黑色", "白色", "红色", "绿色", "蓝色", "黄色", "金色", "银色", "灰色", "紫色", "粉色", "橙色", "杏色", "乳白色", "古铜色", "巧克力色",
		"武器", "称号", "上衣", "护肩", "肩甲", "下装", "裤子", "鞋", "鞋子", "战靴", "长靴", "靴", "腰带", "腰部", "项链", "手镯", "戒指", "辅助装备", "魔法石", "胸甲", "护腿", "头盔",
	} {
		value = strings.ReplaceAll(value, word, "")
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
	if len([]rune(value)) < 3 || strings.HasPrefix(value, "name") {
		return ""
	}
	return value
}

func avatarNameFamily(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.EqualFold(value, "errorstring") {
		return ""
	}
	for _, word := range []string{
		"黑色", "白色", "红色", "绿色", "蓝色", "黄色", "金色", "银色", "灰色", "紫色", "粉色", "橙色", "杏色", "乳白色", "古铜色", "巧克力色", "透明",
		"高级", "稀有", "普通", "华丽", "灿烂", "白金", "帽子", "头发", "脸部", "面部", "眼影", "墨镜", "耳环", "胸部", "胸", "上衣", "外套", "裤子", "鞋子", "鞋", "腰部", "腰带", "皮肤", "光环", "装扮", "套装",
		"cap", "hat", "hair", "face", "neck", "coat", "jacket", "pants", "shoes", "waist", "skin", "aura", "avatar",
	} {
		value = strings.ReplaceAll(value, word, "")
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
	if len([]rune(value)) < 3 || strings.HasPrefix(value, "name") {
		return ""
	}
	return value
}
