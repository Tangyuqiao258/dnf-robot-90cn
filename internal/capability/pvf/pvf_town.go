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
