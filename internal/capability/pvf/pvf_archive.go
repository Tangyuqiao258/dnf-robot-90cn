package pvf

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"robot/internal/foundation/charset"
	"robot/internal/shared"
)

type pvfFile struct {
	Data []byte
}

type pvfArchive struct {
	files      map[string]*pvfFile
	stringList []string
}

func openPVF(path string) (*pvfArchive, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 56 {
		return nil, fmt.Errorf("pvf too small")
	}
	headerLen := uint64(binary.LittleEndian.Uint32(raw[0:4]))
	if headerLen > uint64(len(raw)-4) {
		return nil, fmt.Errorf("pvf header truncated")
	}
	pos := 4 + int(headerLen)
	if pos+16 > len(raw) {
		return nil, fmt.Errorf("pvf header truncated")
	}
	treeLenValue := uint64(binary.LittleEndian.Uint32(raw[pos+4 : pos+8]))
	treeCRC := binary.LittleEndian.Uint32(raw[pos+8 : pos+12])
	fileCount := uint64(binary.LittleEndian.Uint32(raw[pos+12 : pos+16]))
	treeStart := pos + 16
	if treeLenValue > uint64(len(raw)-treeStart) {
		return nil, fmt.Errorf("pvf file tree truncated")
	}
	treeLen := int(treeLenValue)
	treeEnd := treeStart + treeLen
	tree := append([]byte(nil), raw[treeStart:treeEnd]...)
	decryptPVFBlock(tree, treeLen, treeCRC)
	dataStart := treeEnd
	if dataStart > len(raw) {
		return nil, fmt.Errorf("pvf data section truncated")
	}
	archive := &pvfArchive{files: make(map[string]*pvfFile)}
	offset := 0
	parsed := uint64(0)
	for parsed < fileCount {
		if len(tree)-offset < 20 {
			return nil, fmt.Errorf("pvf file tree entry %d truncated", parsed)
		}
		nameLen := uint64(binary.LittleEndian.Uint32(tree[offset+4 : offset+8]))
		if nameLen > uint64(len(tree)-offset-20) {
			return nil, fmt.Errorf("pvf file tree entry %d name truncated", parsed)
		}
		nameEnd := offset + 8 + int(nameLen)
		name := normalizePVFPath(string(tree[offset+8 : nameEnd]))
		fileSize := uint64(binary.LittleEndian.Uint32(tree[nameEnd : nameEnd+4]))
		fileCRC := binary.LittleEndian.Uint32(tree[nameEnd+4 : nameEnd+8])
		fileOffset := uint64(binary.LittleEndian.Uint32(tree[nameEnd+8 : nameEnd+12]))
		offset = nameEnd + 12
		parsed++
		if name == "" {
			return nil, fmt.Errorf("pvf file tree entry %d has empty name", parsed-1)
		}
		if _, exists := archive.files[name]; exists {
			return nil, fmt.Errorf("pvf file tree entry %d duplicates %q", parsed-1, name)
		}
		if fileSize == 0 {
			archive.files[name] = &pvfFile{Data: nil}
			continue
		}
		aligned := (fileSize + 3) &^ uint64(3)
		if fileOffset > uint64(len(raw)-dataStart) {
			return nil, fmt.Errorf("pvf file tree entry %d offset out of bounds", parsed-1)
		}
		start := dataStart + int(fileOffset)
		if aligned > uint64(len(raw)-start) {
			return nil, fmt.Errorf("pvf file tree entry %d data out of bounds", parsed-1)
		}
		end := start + int(aligned)
		// The archive owns raw for its lifetime. Decrypt valid file regions in
		// place and retain bounded slices instead of duplicating the entire PVF
		// payload a second time at startup.
		data := raw[start:end]
		decryptPVFBlock(data, int(aligned), fileCRC)
		data = data[:int(fileSize):int(fileSize)]
		archive.files[name] = &pvfFile{Data: data}
	}
	if parsed != fileCount {
		return nil, fmt.Errorf("pvf file tree entry count %d does not match header %d", parsed, fileCount)
	}
	archive.loadStringTable()
	return archive, nil
}

func (a *pvfArchive) loadStringTable() {
	f := a.files["stringtable.bin"]
	if f == nil || len(f.Data) < 8 {
		return
	}
	count := int(binary.LittleEndian.Uint32(f.Data[0:4]))
	if count <= 0 || len(f.Data) < 8 || count > (len(f.Data)-8)/4 {
		return
	}
	a.stringList = make([]string, count)
	for i := 0; i < count; i++ {
		start := int(binary.LittleEndian.Uint32(f.Data[4+i*4 : 8+i*4]))
		endOff := 8 + i*4
		end := int(binary.LittleEndian.Uint32(f.Data[endOff : endOff+4]))
		if end < start || start > len(f.Data)-4 || end > len(f.Data)-4 {
			continue
		}
		a.stringList[i] = cleanPVFTableString(charset.DecodePVFBytes(f.Data[start+4 : end+4]))
	}
}

func appendItemInfoCreatureArtifacts(equipment []shared.EquipmentCatalogItem, rawItemInfo string) []shared.EquipmentCatalogItem {
	rows := parseItemInfoRows(normalizeItemInfoRows(rawItemInfo))
	if len(rows) == 0 {
		return equipment
	}
	seen := make(map[int]bool, len(equipment))
	for _, item := range equipment {
		if item.ID > 0 {
			seen[item.ID] = true
		}
	}
	ids := make([]int, 0)
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		fields := rows[id]
		if len(fields) < 17 {
			continue
		}
		slot, itemType, ok := creatureArtifactCategory(fields[16])
		if !ok {
			continue
		}
		item := shared.EquipmentCatalogItem{
			ID:       id,
			Name:     cleanPVFString(fields[14]),
			Name2:    cleanPVFString(fields[15]),
			Path:     "etc/iteminfo.dat",
			Level:    atoi(fields[13]),
			ItemType: itemType,
			Slot:     slot,
			Rarity:   atoi(fields[1]),
			Attach:   "trade",
			Trade:    true,
			Auction:  true,
		}
		if item.Name == "" || shared.ItemNameBlocked(item.Name) || item.Rarity == chronicleRarity {
			item.GrantBlocked = true
		}
		equipment = append(equipment, item)
		seen[id] = true
	}
	return equipment
}

func creatureArtifactCategory(category string) (string, int, bool) {
	switch strings.TrimSpace(category) {
	case "14001":
		return "creature", 30, true
	case "14002":
		return "artifact red", 31, true
	case "14003":
		return "artifact blue", 32, true
	case "14004":
		return "artifact green", 33, true
	default:
		return "", 0, false
	}
}

// chronicleRarity is 勇者 (Chronicle) gear. No robot may wear it: it is the 异界
// set, it renders red in the client, and the operator has ruled it out of the
// fleet. Keep in step with equipment.RarityChronicle, which keeps the level
// bands off it as well.
const chronicleRarity = 5

func extractItemList(a *pvfArchive, listPath, prefix string, stackable bool) []shared.EquipmentCatalogItem {
	return extractItemListFromText(a.text, listPath, prefix, stackable)
}

func extractItemListFromText(readText func(string) string, listPath, prefix string, stackable bool) []shared.EquipmentCatalogItem {
	var out []shared.EquipmentCatalogItem
	itemSetInfo := make(map[int]pvfItemSetInfo)
	for _, entry := range parsePVFList(readText(listPath)) {
		exts := []string{".equ"}
		if stackable {
			exts = []string{".stk"}
		}
		itemPath, body := textWithExt(readText, prefix+entry.Path, exts...)
		if body == "" {
			continue
		}
		lines := splitPVFLines(body)
		item := shared.EquipmentCatalogItem{ID: entry.ID, Path: itemPath}
		pathJob := -1
		if stackable {
			item.BasicMaterial = strings.HasPrefix(normalizePVFPath(entry.Path), "material/")
			parseStackableTradeBlock(body, &item)
		}
		if !stackable {
			if typ, slot := equipmentTypeFromPath(entry.Path); typ > 0 {
				item.ItemType = typ
				item.Slot = slot
			}
			item.PartSetIndex = pvfPartSetIndex(body)
			pathJob = jobFromEquipmentPath(entry.Path)
		}
		equipmentTypeSeen := false
		for i, line := range lines {
			lowerLine := strings.ToLower(line)
			if !stackable && !equipmentTypeSeen && strings.HasPrefix(lowerLine, "[add ") && strings.HasSuffix(lowerLine, "]") {
				// This DP2 client stops parsing when an extended [add ...] property
				// appears before [equipment type]. The missing type becomes sentinel
				// 26, which the auction UI uses as an out-of-bounds array index.
				item.ClientIncompatible = true
			}
			if stackable {
				switch {
				case strings.Contains(lowerLine, "not") && strings.Contains(lowerLine, "trade"):
					item.NoTrade = true
				case strings.Contains(lowerLine, "unable") && strings.Contains(lowerLine, "trade"):
					item.NoTrade = true
				case strings.Contains(lowerLine, "trade"):
					item.Trade = true
				case strings.Contains(lowerLine, "auction"):
					item.Auction = true
				case strings.Contains(lowerLine, "shop"):
					item.Shop = true
				}
			}
			switch line {
			case "[name]":
				item.Name = cleanPVFString(nextLine(lines, i))
				if stackable && strings.EqualFold(item.Name, "ErrorString") {
					item.BadName = true
				}
				if shared.ItemNameBlocked(item.Name) {
					item.GrantBlocked = true
				}
			case "[name2]":
				item.Name2 = cleanPVFString(nextLine(lines, i))
				if shared.ItemNameBlocked(item.Name2) {
					item.GrantBlocked = true
				}
			case "[rarity]":
				item.Rarity = atoi(nextLine(lines, i))
			case "[price]":
				item.Price = atoi(nextLine(lines, i))
			case "[value]":
				item.Value = atoi(nextLine(lines, i))
			case "[durability]":
				item.Durability = atoi(nextLine(lines, i))
			case "[minimum level]":
				item.Level = atoi(nextLine(lines, i))
			case "[stack limit]":
				item.StackLimit = atoi(nextLine(lines, i))
			case "[equipment type]":
				equipmentTypeSeen = true
				slot := cleanPVFString(nextLine(lines, i))
				if typ := equipmentType(slot); typ > 0 {
					item.Slot = slot
					item.ItemType = typ
				}
			case "[usable job]":
				for _, job := range parseJobs(section(body, "[usable job]", "[/usable job]")) {
					item.UseJob = appendUniqueInt(item.UseJob, job)
				}
			case "[attach type]":
				item.Attach = cleanPVFString(nextLine(lines, i))
			case "[icon]":
				item.Icon = cleanPVFString(nextLine(lines, i))
			case "[field image]":
				item.FieldImage = cleanPVFString(nextLine(lines, i))
			case "[need material]":
				item.NeedMaterial = true
			case "[sub type]":
				item.SubType = atoi(nextLine(lines, i))
			case "[expiration date]", "[usable period]":
				item.Expire = true
			case "[stackable type]":
				item.Slot = cleanPVFString(nextLine(lines, i))
			}
		}
		if !stackable {
			applyEquipmentPathJob(&item, pathJob)
		}
		if !stackable {
			item.SetKey = deriveItemSetKey(itemPath, body, item)
		} else if strings.Contains(strings.ToLower(item.Slot), "recipe") {
			item.RecipeTargetID = parseRecipeTargetID(body)
		}
		// Nothing the client cannot resolve may reach a robot. The game client
		// and the GM tool both key their item lookup on the [name] record, so a
		// nameless entry renders as an empty, iconless item; the archive's file
		// tree holds tens of thousands of those, mostly 时装 and 宠物. 勇者 gear
		// is barred by the operator's rule rather than by resolution. Marking
		// both here, once, keeps every grant path honest: they all already
		// refuse a GrantBlocked item.
		if item.Name == "" || item.BadName || item.Rarity == chronicleRarity {
			item.GrantBlocked = true
		}
		if item.ID > 0 && (item.ItemType > 0 || stackable) {
			out = append(out, item)
			if !stackable {
				itemSetInfo[item.ID] = parsePVFItemSetInfo(body)
			}
		}
	}
	if !stackable {
		resolvePVFItemSetKeys(out, itemSetInfo)
		blockClassNamedSpecials(out, projectClassNames(readText))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func parseRecipeTargetID(body string) int {
	values := pvfSectionInts(body, "int data")
	if len(values) == 0 {
		return 0
	}
	materialCount := values[0]
	targetIndex := 2*materialCount + 2
	if materialCount < 0 || targetIndex < 0 || targetIndex >= len(values) {
		return 0
	}
	return values[targetIndex]
}

func extractMapList(a *pvfArchive, listPath, prefix string) []shared.MapCatalogItem {
	return extractMapListFromText(a.text, listPath, prefix)
}

func extractMapListFromText(readText func(string) string, listPath, prefix string) []shared.MapCatalogItem {
	// Each projected area keeps the body of its map file: the town map text is
	// read once here, and the Seria-exit pass below needs the room's portal
	// table only after every area has been projected.
	type projectedArea struct {
		item       shared.MapCatalogItem
		body       string
		dungeonIDs []int
	}
	var projected []projectedArea
	for _, entry := range parsePVFList(readText(listPath)) {
		_, body := textWithExt(readText, prefix+entry.Path, ".twn", ".map")
		if body == "" {
			continue
		}
		lines := splitPVFLines(body)
		level := 0
		villageName := ""
		for i, line := range lines {
			switch line {
			case "[name]":
				villageName = cleanPVFString(nextLine(lines, i))
			case "[limit level]":
				level = atoi(nextLine(lines, i))
			}
		}
		for _, area := range parseTownAreas(body) {
			mapBody := townMapText(readText, townMapArchivePath(area.MapPath))
			rectangles := townMapMovableRectangles(mapBody)
			xMin, xMax, yMin, yMax, coordinateReady := townMapMovableBounds(mapBody)
			normalEligible := coordinateReady && area.Kind != "pvp"
			storeEligible := coordinateReady && area.Kind == "normal"
			storeProbe := coordinateReady && area.Kind == "other"
			projected = append(projected, projectedArea{
				item: shared.MapCatalogItem{
					Village: entry.ID, VillageName: villageName, Area: area.ID, Level: level,
					MinLevel: area.MinLevel, NeedQuests: append([]int(nil), area.NeedQuests...),
					XMin: xMin, XMax: xMax, YMin: yMin, YMax: yMax, Rectangles: rectangles, Use: coordinateReady, Gate: area.Gate,
					NormalEligible: boolPointer(normalEligible), StoreEligible: boolPointer(storeEligible), StoreProbe: boolPointer(storeProbe),
					Links: townMapMovableTargets(mapBody), DungeonGate: area.DungeonGate, GateX: area.GateX, GateY: area.GateY,
				},
				body:       mapBody,
				dungeonIDs: area.DungeonIDs,
			})
		}
	}
	// Dungeon entrances: the gate marker names the dungeon, and the dungeon's own
	// file states the level a character must reach to enter it. Only the dungeons
	// the world actually opens are read.
	gateIDs := make([]int, 0, 32)
	for _, entry := range projected {
		gateIDs = append(gateIDs, entry.dungeonIDs...)
	}
	gateLevels := dungeonEntryLevels(readText, gateIDs)
	for index := range projected {
		for _, id := range projected[index].dungeonIDs {
			level := gateLevels[id]
			if level <= 0 {
				continue
			}
			// An area may open several dungeons; the entrance a low-level
			// character can actually walk up to is the one that sets the crowd.
			if projected[index].item.DungeonLevel == 0 || level < projected[index].item.DungeonLevel {
				projected[index].item.DungeonLevel = level
			}
		}
	}
	// Seria-exit pass: the room is the one town map carrying the room marker,
	// and its portal table names every map a character lands in when leaving it.
	for index := range projected {
		if !townMapIsSeriaRoom(projected[index].body) {
			continue
		}
		for _, exit := range townMapSeriaExits(projected[index].body, projected[index].item.Village) {
			for mark := range projected {
				if projected[mark].item.Village == exit.Village && projected[mark].item.Area == exit.Area {
					projected[mark].item.SeriaExit = true
				}
			}
		}
		break
	}
	out := make([]shared.MapCatalogItem, 0, len(projected))
	for _, entry := range projected {
		out = append(out, entry.item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Village == out[j].Village {
			return out[i].Area < out[j].Area
		}
		return out[i].Village < out[j].Village
	})
	return out
}

func townMapText(readText func(string) string, path string) string {
	path = normalizePVFPath(path)
	if text := readText(path); text != "" {
		return text
	}
	slash := strings.LastIndex(path, "/")
	if slash < 0 {
		return ""
	}
	return readText(path[:slash+1] + "(r)" + path[slash+1:])
}

func textWithExt(readText func(string) string, path string, exts ...string) (string, string) {
	path = normalizePVFPath(path)
	if text := readText(path); text != "" {
		return path, text
	}
	for _, ext := range exts {
		if strings.HasSuffix(path, ext) {
			continue
		}
		candidate := path + ext
		if text := readText(candidate); text != "" {
			return candidate, text
		}
	}
	return "", ""
}

func boolPointer(value bool) *bool {
	return &value
}

func (a *pvfArchive) text(path string) string {
	f := a.files[normalizePVFPath(path)]
	if f == nil {
		return ""
	}
	if len(f.Data) >= 2 && binary.LittleEndian.Uint16(f.Data[0:2]) == 0xd0b0 {
		return a.decodeScript(f.Data)
	}
	return cleanPVFString(charset.DecodePVFBytes(f.Data))
}

func (a *pvfArchive) decodeScript(data []byte) string {
	var b strings.Builder
	b.WriteString("#PVF_File\r\n")
	for i := 2; i+5 <= len(data); i += 5 {
		typ := data[i]
		val := int32(binary.LittleEndian.Uint32(data[i+1 : i+5]))
		switch typ {
		case 2:
			b.WriteString(strconv.Itoa(int(val)))
			b.WriteByte('\t')
		case 4:
			b.WriteString(strconv.FormatFloat(float64(math.Float32frombits(uint32(val))), 'f', 2, 32))
			b.WriteByte('\t')
		case 5:
			b.WriteString("\r\n")
			b.WriteString(a.lookupString(int(val)))
			b.WriteString("\r\n")
		case 7:
			b.WriteByte('`')
			b.WriteString(a.lookupString(int(val)))
			b.WriteString("`\t\n")
		case 10:
			b.WriteString(a.lookupString(int(val)))
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

func (a *pvfArchive) lookupString(index int) string {
	if index >= 0 && index < len(a.stringList) && a.stringList[index] != "" {
		return a.stringList[index]
	}
	return "ErrorString"
}

func align4(v int) int {
	return (v + 3) &^ 3
}

func decryptPVFBlock(buf []byte, size int, key uint32) {
	if size > len(buf) {
		size = len(buf)
	}
	size = align4(size)
	if size > len(buf) {
		size = len(buf) &^ 3
	}
	for i := 0; i+4 <= size; i += 4 {
		v := binary.LittleEndian.Uint32(buf[i:i+4]) ^ 0x81a79011 ^ key
		binary.LittleEndian.PutUint32(buf[i:i+4], (v>>6)|(v<<(32-6)))
	}
}
