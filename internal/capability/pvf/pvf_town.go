package pvf

import (
	"strings"

	"robot/internal/shared"
)

type townArea struct {
	ID         int
	MapPath    string
	Gate       bool
	Kind       string
	MinLevel   int
	NeedQuests []int
	// DungeonGate marks an area that hosts dungeon entrances ([dungeon gate]).
	// Crowd rules pin the disassembler stalls to these areas.
	DungeonGate bool
	// GateX, GateY is the coordinate the server drops a character on when a
	// generic area transition targets this area ([gate] x y). Zero means the
	// area declares no arrival point.
	GateX int
	GateY int
}

func parseTownAreas(body string) []townArea {
	var out []townArea
	start := 0
	for {
		i := strings.Index(body[start:], "[area]")
		if i < 0 {
			break
		}
		i += start
		j := strings.Index(body[i:], "[/area]")
		if j < 0 {
			break
		}
		j += i
		block := body[i+len("[area]") : j]
		fields := strings.Fields(strings.ReplaceAll(block, "`", ""))
		if len(fields) >= 2 {
			id := atoi(fields[0])
			mapPath := normalizePVFPath(fields[1])
			if id >= 0 && mapPath != "" {
				lowerBlock := strings.ToLower(block)
				kind := "other"
				switch {
				case strings.Contains(lowerBlock, "[pvp]"):
					kind = "pvp"
				case strings.Contains(lowerBlock, "[gate]"):
					kind = "gate"
				case strings.Contains(lowerBlock, "[normal]"):
					kind = "normal"
				}
				area := townArea{ID: id, MapPath: mapPath, Gate: kind == "gate", Kind: kind}
				area.MinLevel, area.NeedQuests = townAreaRequirements(block)
				area.DungeonGate, area.GateX, area.GateY = townAreaArrival(block)
				out = append(out, area)
			}
		}
		start = j + len("[/area]")
	}
	return out
}

// townAreaRequirements reads the area's [need level] and [need quest] gate
// requirements from one [area] block.
func townAreaRequirements(block string) (int, []int) {
	lines := splitPVFLines(block)
	minLevel := 0
	quests := make([]int, 0, 4)
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		if !strings.HasPrefix(line, "[") {
			continue
		}
		section := strings.ToLower(strings.Trim(line, "[] "))
		values := make([]int, 0, 4)
		for next := index + 1; next < len(lines); next++ {
			candidate := strings.TrimSpace(lines[next])
			if candidate == "" {
				continue
			}
			if strings.HasPrefix(candidate, "[") {
				break
			}
			value := atoi(candidate)
			if value <= 0 {
				break
			}
			values = append(values, value)
		}
		switch section {
		case "need level":
			if len(values) > 0 && minLevel == 0 {
				minLevel = values[0]
			}
		case "need quest":
			quests = append(quests, values...)
		}
	}
	return minLevel, quests
}

// townAreaArrival reads the two crowd-relevant flags of one [area] block:
// whether the area hosts dungeon entrances, and the coordinate the server uses
// when a generic area transition lands a character here. The flags are
// backtick-quoted and may sit inline with the map path, so the block is scanned
// as flat text rather than line by line. "[dungeon gate]" carries a space, which
// is why the pair is matched as substrings instead of whitespace tokens.
func townAreaArrival(block string) (dungeonGate bool, gateX, gateY int) {
	flat := strings.ReplaceAll(block, "`", "")
	dungeonGate = strings.Contains(flat, "[dungeon gate]")
	// "[gate]" never matches inside "[dungeon gate]": there is a space, not an
	// opening bracket, before "gate]".
	index := strings.Index(flat, "[gate]")
	if index < 0 {
		return dungeonGate, 0, 0
	}
	fields := strings.Fields(flat[index+len("[gate]"):])
	if len(fields) >= 2 {
		gateX, gateY = atoi(fields[0]), atoi(fields[1])
	}
	return dungeonGate, gateX, gateY
}

// townMapMovableTargets decodes one [town movable area] portal table: six
// integers per group (x y width height target village target area). Inline
// [tag] digits [/tag] sub-blocks carry gate conditions rather than coordinates,
// so pvfDirectTagInts drops their numbers before the groups are cut. Groups
// without a target village are triggers into the map itself, not exits, and are
// skipped.
func townMapMovableTargets(body string) []shared.MapAreaKey {
	values := pvfDirectTagInts(body, "town movable area")
	links := make([]shared.MapAreaKey, 0, len(values)/6)
	for i := 0; i+5 < len(values); i += 6 {
		if values[i+4] <= 0 {
			continue
		}
		links = append(links, shared.MapAreaKey{Village: values[i+4], Area: values[i+5]})
	}
	return links
}

// townMapSeriaExits decodes the Seria room's portal table into the maps a
// character lands in when leaving the room: the door target in the room's own
// town, plus every zero-size return portal. A sized portal into another town
// opens a separate room (the marriage hall) rather than the street, so it is
// not an exit.
func townMapSeriaExits(body string, roomVillage int) []shared.MapAreaKey {
	values := pvfDirectTagInts(body, "town movable area")
	exits := make([]shared.MapAreaKey, 0, len(values)/6)
	for i := 0; i+5 < len(values); i += 6 {
		village, area := values[i+4], values[i+5]
		if village <= 0 {
			continue
		}
		if (values[i+2] > 0 || values[i+3] > 0) && village != roomVillage {
			continue
		}
		exits = append(exits, shared.MapAreaKey{Village: village, Area: area})
	}
	return exits
}

// townMapIsSeriaRoom reports whether a town map body is the Seria room. The
// marker is unique to that one map in the shipped PVF.
func townMapIsSeriaRoom(body string) bool {
	return strings.Contains(body, "[is seria room warp]")
}

func townMapArchivePath(path string) string {
	path = normalizePVFPath(path)
	if path == "" || strings.HasPrefix(path, "map/") {
		return path
	}
	return "map/" + path
}

func townMapMovableBounds(body string) (xMin, xMax, yMin, yMax int, ok bool) {
	rectangles := townMapMovableRectangles(body)
	if len(rectangles) == 0 {
		return 0, 0, 0, 0, false
	}
	xMin, xMax = rectangles[0].XMin, rectangles[0].XMax
	yMin, yMax = rectangles[0].YMin, rectangles[0].YMax
	for _, rectangle := range rectangles[1:] {
		if rectangle.XMin < xMin {
			xMin = rectangle.XMin
		}
		if rectangle.XMax > xMax {
			xMax = rectangle.XMax
		}
		if rectangle.YMin < yMin {
			yMin = rectangle.YMin
		}
		if rectangle.YMax > yMax {
			yMax = rectangle.YMax
		}
	}
	return xMin, xMax, yMin, yMax, true
}

func townMapMovableRectangles(body string) []shared.MapRectangle {
	values := pvfDirectTagInts(body, "virtual movable area")
	if len(values)%4 != 0 {
		return nil
	}
	rectangles := make([]shared.MapRectangle, 0, len(values)/4)
	for i := 0; i+3 < len(values); i += 4 {
		x, y, width, height := values[i], values[i+1], values[i+2], values[i+3]
		if width <= 0 || height <= 0 || width > 10000 || height > 10000 {
			continue
		}
		right, bottom := x+width, y+height
		if right < 0 || bottom < 0 || x > 65535 || y > 65535 {
			continue
		}
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if right > 65535 {
			right = 65535
		}
		if bottom > 65535 {
			bottom = 65535
		}
		rectangles = append(rectangles, shared.MapRectangle{XMin: x, XMax: right, YMin: y, YMax: bottom})
	}
	return rectangles
}
