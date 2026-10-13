package pvf

import (
	"strings"

	"robot/internal/shared"
)

type pvfListEntry struct {
	ID   int
	Path string
}

func parsePVFList(text string) []pvfListEntry {
	rawFields := strings.Fields(strings.ReplaceAll(text, "`", ""))
	fields := make([]string, 0, len(rawFields))
	for _, field := range rawFields {
		if strings.EqualFold(field, "#PVF_File") {
			continue
		}
		fields = append(fields, field)
	}
	// The list is a flat sequence of id/path pairs, but sparse or malformed
	// rows (a missing id, a path containing whitespace) shift every following
	// pair. Resynchronize on the next id-like field instead of silently
	// dropping the remainder of the list.
	out := make([]pvfListEntry, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i++ {
		id := atoi(fields[i])
		if id <= 0 {
			continue
		}
		path := normalizePVFPath(fields[i+1])
		if path == "" || !pvfListPathLike(path) {
			continue
		}
		out = append(out, pvfListEntry{ID: id, Path: path})
		i++
	}
	return out
}

// pvfListPathLike distinguishes a file reference from a stray value token so a
// misaligned pair cannot be accepted as a path.
func pvfListPathLike(path string) bool {
	return strings.ContainsAny(path, "/.")
}

func parseStackableTradeBlock(body string, item *shared.EquipmentCatalogItem) {
	if item == nil {
		return
	}
	block := section(body, "[trade]", "[/trade]")
	if strings.TrimSpace(block) == "" {
		return
	}
	item.TradeBlock = true
	if value, ok := parsePVFBoolFlag(block, "available trade"); ok {
		item.CanTrade = &value
		if value {
			item.Trade = true
		} else {
			item.NoTrade = true
		}
	}
	if value, ok := parsePVFBoolFlag(block, "available auction"); ok {
		item.CanAuction = &value
		item.Auction = value
	}
	if value, ok := parsePVFBoolFlag(block, "available shop"); ok {
		item.CanShop = &value
		item.Shop = value
	}
	if value, ok := parsePVFBoolFlag(block, "available drop"); ok {
		item.CanDrop = &value
	}
}

func parsePVFBoolFlag(block, name string) (bool, bool) {
	lines := splitPVFLines(block)
	name = strings.ToLower(strings.TrimSpace(name))
	for i, line := range lines {
		cleaned := strings.ToLower(cleanPVFString(line))
		if !strings.Contains(cleaned, name) {
			continue
		}
		fields := strings.Fields(cleaned)
		for idx, field := range fields {
			if field == "0" {
				return false, true
			}
			if field == "1" {
				return true, true
			}
			if strings.Contains(field, name) && idx+1 < len(fields) {
				return atoi(fields[idx+1]) != 0, true
			}
		}
		if i+1 < len(lines) {
			return atoi(lines[i+1]) != 0, true
		}
		return false, true
	}
	return false, false
}
