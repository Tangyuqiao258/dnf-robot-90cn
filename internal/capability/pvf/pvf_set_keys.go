package pvf

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"robot/internal/shared"
)

func deriveItemSetKey(path, body string, item shared.EquipmentCatalogItem) string {
	if item.ItemType <= 0 {
		return ""
	}
	if key := explicitPVFSetKey(body); key != "" {
		return "pvf_" + shortHash(key)
	}
	// Equipment paths are storage layout, not set metadata. In simulator PVFs a
	// shared directory is commonly reused by titles, jewelry, special slots,
	// and unrelated armor pieces. Never expose that path as a set key.
	if partSetIndex := pvfPartSetIndex(body); partSetIndex > 0 {
		return pvfPartSetKey(partSetIndex)
	}
	// Equipment paths and names are presentation/storage data, not set
	// metadata. A missing PVF set field means the item is standalone.
	_ = path
	return ""
}

func pvfPartSetIndex(body string) int { return atoi(pvfTagValue(body, "part set index")) }

func pvfPartSetKey(index int) string { return "pvf_part_" + strconv.Itoa(index) }

type pvfItemSetInfo struct {
	masterID    int
	memberIDs   []int
	descriptors []string
}

func parsePVFItemSetInfo(body string) pvfItemSetInfo {
	return pvfItemSetInfo{
		masterID:    pvfSetMasterID(body),
		memberIDs:   pvfSetItemIDs(body),
		descriptors: pvfSetDescriptors(body),
	}
}

func resolvePVFItemSetKeys(items []shared.EquipmentCatalogItem, setInfo map[int]pvfItemSetInfo) {
	if len(items) == 0 || len(setInfo) == 0 {
		return
	}
	masterFor := make(map[int]int)
	masterDescriptors := make(map[string]int)
	masterIDs := make(map[int]bool)
	for i := range items {
		item := &items[i]
		info := setInfo[item.ID]
		if info.masterID > 0 {
			masterFor[item.ID] = info.masterID
		}
		if !containsInt(info.memberIDs, item.ID) {
			continue
		}
		masterIDs[item.ID] = true
		masterFor[item.ID] = item.ID
		for _, memberID := range info.memberIDs {
			masterFor[memberID] = item.ID
		}
		for _, descriptor := range info.descriptors {
			key := pvfDescriptorLookupKey(*item, descriptor)
			if key == "" {
				continue
			}
			if previous, exists := masterDescriptors[key]; !exists {
				masterDescriptors[key] = item.ID
			} else if previous != item.ID {
				masterDescriptors[key] = 0
			}
		}
	}

	assigned := make(map[int]bool)
	for i := range items {
		item := &items[i]
		if masterID := masterFor[item.ID]; masterID > 0 {
			item.SetKey = pvfMasterSetKey(masterID)
			assigned[item.ID] = true
			continue
		}
		for _, descriptor := range setInfo[item.ID].descriptors {
			masterID := masterDescriptors[pvfDescriptorLookupKey(*item, descriptor)]
			if masterID <= 0 {
				continue
			}
			item.SetKey = pvfMasterSetKey(masterID)
			assigned[item.ID] = true
			break
		}
	}

	attachPVFThemeMatches(items, assigned, masterIDs)
}

func pvfSetMasterID(body string) int {
	return atoi(pvfTagValue(body, "set item master"))
}

func pvfSetItemIDs(body string) []int {
	return pvfSectionInts(body, "set item")
}

func pvfMasterSetKey(masterID int) string {
	return "pvf_" + shortHash("set item master:"+strconv.Itoa(masterID))
}

func pvfSetDescriptors(body string) []string {
	values := []string{pvfTagValue(body, "set name"), pvfTagValue(body, "explain")}
	out := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = normalizePVFSetDescriptor(value)
		if value == "" || strings.EqualFold(value, "errorstring") || !pvfSetDescriptorReliable(value) || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func pvfSetDescriptorReliable(value string) bool {
	return strings.Contains(value, "套裝") || strings.Contains(value, "套装") || strings.Contains(value, "禮包") || strings.Contains(value, "礼包")
}

func normalizePVFSetDescriptor(value string) string {
	value = strings.ToLower(cleanPVFString(value))
	value = strings.NewReplacer("禮包", "套裝", "礼包", "套裝", "套装", "套裝").Replace(value)
	return strings.Join(strings.Fields(value), "")
}

func pvfDescriptorLookupKey(item shared.EquipmentCatalogItem, descriptor string) string {
	if descriptor == "" {
		return ""
	}
	jobs := append([]int(nil), item.UseJob...)
	sort.Ints(jobs)
	parts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		parts = append(parts, strconv.Itoa(job))
	}
	return strings.Join(parts, ",") + ":" + descriptor
}

func explicitPVFSetKey(body string) string {
	lines := splitPVFLines(body)
	for i, line := range lines {
		lower := strings.ToLower(cleanPVFString(line))
		switch lower {
		case "set item master", "set", "set name", "equipment set":
			if value := cleanPVFString(nextLine(lines, i)); value != "" && !strings.EqualFold(value, "ErrorString") {
				return lower + ":" + value
			}
		}
	}
	return ""
}

// NormalizeEquipmentSetKeys repairs catalogs produced by older Robot builds.
// Only explicit PVF set keys are retained. Paths and display names cannot
// prove a set relationship and are deliberately discarded.
func NormalizeEquipmentSetKeys(items []shared.EquipmentCatalogItem) {
	for i := range items {
		if items[i].ItemType >= 1 && items[i].ItemType <= 12 && !strings.HasPrefix(items[i].SetKey, "pvf_") {
			items[i].SetKey = ""
		}
	}
	attachEquipmentNameFamilies(items)
}

func shortHash(value string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(sum[:])[:10]
}
