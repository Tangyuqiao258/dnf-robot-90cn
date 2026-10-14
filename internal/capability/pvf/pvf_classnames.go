package pvf

import (
	"regexp"
	"strings"

	"robot/internal/shared"
)

// Class-name vocabulary (职业名字) for the class-specific special equipment.
//
// The archive ships one special-equipment item per class for several set
// prefixes, so a prefix that looks like a single family is really a dozen
// near-identical items whose only difference is the class in the name:
//
//	圣文碑石 - 剑魂 | 圣文碑石 - 狂战士 | 圣文碑石 - 气功师 (女) | ...
//	圣者金牌 - 剑圣 | 圣者金牌 - 狱血魔神 | 圣者金牌 - 百花缭乱 | ...
//	诸神之智 - 剑魂 | 真神的庇护 - 剑圣 | 灵石玉髓 - 剑圣 | 悔悟之印 - 剑魂
//
// Those items are single-branch gear: the name says 剑魂 while [usable job]
// says 鬼剑士(男), so a 狂战士 robot can wear a 剑魂 item. The fleet is not
// class-specific gear's audience, and handing one out puts an item in a robot's
// slot that reads as a mislabel, so no robot is ever granted them.
//
// The name is the only place the archive states the class for this batch, so
// the rule is a name rule: an item whose name carries a class name as a whole
// token after the separator the archive uses (" - " or " : ") is blocked.

// itemClassSeparator splits an item name into the tokens a class name can sit
// in. Both separators occur in the class-specific batch.
var itemClassSeparator = regexp.MustCompile(`[-:]`)

// itemClassVariant drops the trailing build variant, so 气功师 (女) compares as
// 气功师 and 元素师 (火) as 元素师.
var itemClassVariant = regexp.MustCompile(`\s*[(（][^)）]*[)）]\s*$`)

// itemTypeSpecialSupport / MagicStone / Earring are the [equipment type] values
// of the three slots that make up 特殊装备. A name can carry a dash for other
// reasons across the rest of the catalog (armor set pieces, weapon variants),
// so the class-name rule is applied to these three types only.
const (
	itemTypeSpecialSupport    = 11
	itemTypeSpecialMagicStone = 12
	itemTypeSpecialEarring    = 13
)

// ProjectClassNames returns every name the archive gives a playable class: the
// base class name of each character .chr, its [growtype name] transfer branch
// names, and every [awakening name] row inside them. Commented placeholders and
// the phantom positions parseJobGrowNames blanks are left out, so the list
// holds names the archive really releases.
func ProjectClassNames(archive TownTextArchive) []string {
	if archive == nil {
		return nil
	}
	return projectClassNames(func(path string) string {
		text, _ := archive.ReadText(path)
		return text
	})
}

func projectClassNames(readText func(string) string) []string {
	lst := readText("character/character.lst")
	if lst == "" {
		return nil
	}
	seen := make(map[string]struct{})
	names := make([]string, 0, 128)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "//") {
			return
		}
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, match := range pvfListEntryPattern.FindAllStringSubmatch(lst, -1) {
		body := readText(normalizePVFPath("character/" + match[2]))
		if body == "" {
			continue
		}
		for _, name := range parseJobGrowNames(body) {
			add(name)
		}
		for _, name := range parseAwakeningNames(body) {
			add(name)
		}
	}
	return names
}

// blockClassNamedSpecials marks every class-name-carrying 特殊装备 item
// ungrantaable. See the file comment for why the name, and only the name,
// decides this.
func blockClassNamedSpecials(items []shared.EquipmentCatalogItem, classNames []string) {
	if len(classNames) == 0 {
		return
	}
	for index := range items {
		switch items[index].ItemType {
		case itemTypeSpecialSupport, itemTypeSpecialMagicStone, itemTypeSpecialEarring:
		default:
			continue
		}
		if ClassNamedItem(items[index].Name, classNames) || ClassNamedItem(items[index].Name2, classNames) {
			items[index].GrantBlocked = true
		}
	}
}

// parseAwakeningNames reads every [awakening name] row of a .chr file. The
// section repeats once per awakening stage inside each [growtype N] block, and
// the names sit in the first non-empty line after the tag, the same shape
// [growtype name] uses.
func parseAwakeningNames(body string) []string {
	const tag = "[awakening name]"
	lower := strings.ToLower(body)
	var names []string
	for offset := 0; offset < len(lower); {
		start := strings.Index(lower[offset:], tag)
		if start < 0 {
			break
		}
		start += offset + len(tag)
		offset = start
		rest := strings.TrimLeft(body[start:], " \t\r\n")
		if rest == "" {
			break
		}
		if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
			rest = rest[:end]
		}
		for _, match := range pvfBacktickPattern.FindAllStringSubmatch(rest, -1) {
			names = append(names, match[1])
		}
	}
	return names
}

// ClassNamedItem reports whether an item name carries one of the class names as
// a whole token. Splitting on the separators instead of matching substrings
// keeps a name that merely contains the characters of a class name, and a class
// name that is itself a prefix of another (剑魂 inside 剑魂之魂), from being
// read as class-specific gear.
func ClassNamedItem(name string, classNames []string) bool {
	if name == "" || len(classNames) == 0 {
		return false
	}
	tokens := classNamedItemTokens(name)
	if len(tokens) == 0 {
		return false
	}
	for _, className := range classNames {
		for _, token := range tokens {
			if token == className {
				return true
			}
		}
	}
	return false
}

func classNamedItemTokens(name string) []string {
	parts := itemClassSeparator.Split(name, -1)
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimSpace(itemClassVariant.ReplaceAllString(part, ""))
		if part != "" {
			tokens = append(tokens, part)
		}
	}
	return tokens
}
