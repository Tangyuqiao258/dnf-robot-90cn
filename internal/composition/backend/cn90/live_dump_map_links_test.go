package cn90

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestLiveDumpMapLinks is a read-only reconnaissance helper: it decodes the
// [town movable area] portal table of every town map (x y w h village area per
// group) together with the [town movable area name] labels, so the town map
// graph and its Chinese place names can be read off the PVF. It writes a UTF-8
// text file and never touches the game or database.
func TestLiveDumpMapLinks(t *testing.T) {
	if os.Getenv("CN90_LIVE_DUMP_MAP_LINKS") == "" {
		t.Skip("set CN90_LIVE_DUMP_MAP_LINKS=1 to dump town map links")
	}
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	pvfPath, _, err := layout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	readText := func(path string) string {
		text, _ := archive.ReadText(path)
		return text
	}

	type areaRef struct {
		village int
		area    int
		path    string
	}
	var refs []areaRef
	seen := map[string]bool{}
	lst := readText("town/town.lst")
	fields := strings.Fields(strings.ReplaceAll(lst, "`", ""))
	for i := 0; i+1 < len(fields); i++ {
		village := atoiLoose(fields[i])
		if village <= 0 {
			continue
		}
		listPath := fields[i+1]
		if !strings.ContainsAny(listPath, "/.") {
			continue
		}
		i++
		body := readText("town/" + listPath)
		if body == "" {
			continue
		}
		for _, block := range rawAreaBlocks(body) {
			area, mapPath := areaMapAndID(block)
			if mapPath == "" {
				continue
			}
			key := strings.ToLower(mapPath)
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, areaRef{village: village, area: area, path: key})
		}
	}

	out := os.Getenv("CN90_LIVE_DUMP_MAP_LINKS_OUT")
	if out == "" {
		out = "C:\\Users\\wangh\\.local\\bin\\jobs\\eef80d7d\\tmp\\map_links.txt"
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	extraTags := []string{"[pathgate pos]", "[move town]", "[can teleport]", "[dungeon start area]", "[is seria room warp]"}
	read := 0
	for _, ref := range refs {
		raw := readText("map/" + ref.path)
		if raw == "" {
			slash := strings.LastIndex(ref.path, "/")
			if slash >= 0 {
				raw = readText("map/" + ref.path[:slash+1] + "(r)" + ref.path[slash+1:])
			}
		}
		if raw == "" {
			fmt.Fprintf(file, "v%d/a%d %s MISSING\n\n", ref.village, ref.area, ref.path)
			continue
		}
		read++
		fmt.Fprintf(file, "v%d/a%d %s\n", ref.village, ref.area, ref.path)
		fmt.Fprintf(file, "    links: %s\n", formatLinks(parseMovableTargets(sectionBody(raw, "[town movable area]", "[/town movable area]"))))
		if names := parseMovableNames(sectionBody(raw, "[town movable area name]", "[/town movable area name]")); len(names) > 0 {
			fmt.Fprintf(file, "    labels: %s\n", strings.Join(names, " | "))
		}
		for _, tag := range extraTags {
			text := strings.Join(strings.Fields(sectionBody(raw, tag, "[/"+strings.Trim(tag, "[]")+"]")), " ")
			if text == "" {
				continue
			}
			if len(text) > 240 {
				text = text[:240]
			}
			fmt.Fprintf(file, "    %s %s\n", tag, text)
		}
		fmt.Fprintln(file)
	}
	t.Logf("wrote %s (%d areas, %d map bodies)", out, len(refs), read)
}

// sectionBody returns the raw text between a tag and its closing tag, or up to
// the first blank line when the tag has no closing tag.
func sectionBody(text, open, close string) string {
	start := strings.Index(text, open)
	if start < 0 {
		return ""
	}
	rest := text[start+len(open):]
	if end := strings.Index(rest, close); end >= 0 {
		return rest[:end]
	}
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

type movableTarget struct {
	X, Y, W, H int
	Village    int
	Area       int
}

// parseMovableTargets decodes the [town movable area] table: six integers per
// group (x y width height target village target area).
func parseMovableTargets(text string) []movableTarget {
	values := intFields(text)
	targets := make([]movableTarget, 0, len(values)/6)
	for i := 0; i+5 < len(values); i += 6 {
		targets = append(targets, movableTarget{
			X: values[i], Y: values[i+1], W: values[i+2], H: values[i+3],
			Village: values[i+4], Area: values[i+5],
		})
	}
	return targets
}

// parseMovableNames returns the backtick-quoted labels of
// [town movable area name], in order.
func parseMovableNames(text string) []string {
	names := []string{}
	rest := text
	for {
		start := strings.Index(rest, "`")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start+1:], "`")
		if end < 0 {
			break
		}
		names = append(names, strings.TrimSpace(rest[start+1:start+1+end]))
		rest = rest[start+1+end+1:]
	}
	return names
}

func formatLinks(targets []movableTarget) string {
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Village <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("v%d/a%d@%d,%d,%dx%d", target.Village, target.Area, target.X, target.Y, target.W, target.H))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, " ")
}

func intFields(text string) []int {
	flat := nestedBlock.ReplaceAllString(text, " ")
	flat = strings.NewReplacer("`", " ").Replace(flat)
	out := []int{}
	for _, field := range strings.Fields(flat) {
		value, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		out = append(out, value)
	}
	return out
}

// nestedBlock matches an inline [tag] number [/tag] sub-block, which carries
// gate conditions (quest ids) rather than portal coordinates.
var nestedBlock = regexp.MustCompile(`(?s)\[[a-z][a-z0-9 ]*\]\s*[\d\s]*\s*\[/[a-z][a-z0-9 ]*\]`)
