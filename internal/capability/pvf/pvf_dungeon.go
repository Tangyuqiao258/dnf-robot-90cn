package pvf

import "strings"

// dungeonEntryLevelOf returns the level a character must reach before the
// dungeon described by one .dgn body opens. The game writes it as [minimum
// required level]; a dungeon that omits it falls back to [basis level], the
// level its monsters are tuned to. Zero means the body states neither.
func dungeonEntryLevelOf(body string) int {
	if level := pvfTagInt(body, "minimum required level"); level > 0 {
		return level
	}
	return pvfTagInt(body, "basis level")
}

// dungeonEntryLevels resolves the entry level of the dungeons the given
// [dungeon gate] ids name. dungeon/dungeon.lst maps a town area's gate id to the
// .dgn file describing that dungeon, and the file states the level its entrance
// requires. Ids the list or the file cannot answer for are absent from the
// result, which callers read as "level unknown".
func dungeonEntryLevels(readText func(string) string, ids []int) map[int]int {
	wanted := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	levels := make(map[int]int, len(wanted))
	for _, entry := range parsePVFList(readText("dungeon/dungeon.lst")) {
		if _, ok := wanted[entry.ID]; !ok {
			continue
		}
		if !strings.EqualFold(pvfPathExtension(entry.Path), ".dgn") {
			continue
		}
		if level := dungeonEntryLevelOf(readText(normalizePVFPath("dungeon/" + entry.Path))); level > 0 {
			levels[entry.ID] = level
		}
	}
	return levels
}
