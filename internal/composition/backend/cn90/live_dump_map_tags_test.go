package cn90

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestLiveDumpMapTags is a read-only reconnaissance helper: it lists the tag
// vocabulary of every town map file referenced from town.lst and dumps the raw
// bodies of the sections that can link one map to another. It writes a UTF-8
// text file and never touches the game or database.
func TestLiveDumpMapTags(t *testing.T) {
	if os.Getenv("CN90_LIVE_DUMP_MAP_TAGS") == "" {
		t.Skip("set CN90_LIVE_DUMP_MAP_TAGS=1 to dump town map tags")
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

	lst := readText("town/town.lst")
	fields := strings.Fields(strings.ReplaceAll(lst, "`", ""))
	refsByMap := map[string][]string{}
	order := []string{}
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
			if _, ok := refsByMap[key]; !ok {
				order = append(order, key)
			}
			refsByMap[key] = append(refsByMap[key], fmt.Sprintf("v%d/a%d", village, area))
		}
	}
	sort.Strings(order)

	out := os.Getenv("CN90_LIVE_DUMP_MAP_TAGS_OUT")
	if out == "" {
		out = "C:\\Users\\wangh\\.local\\bin\\jobs\\eef80d7d\\tmp\\map_tags.txt"
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	interest := regexp.MustCompile(`(?i)passage|portal|link|gate|dungeon|connect|enter|exit|target|move|area|town|npc`)
	tagCount := map[string]int{}
	missing := []string{}
	bodies := map[string]string{}
	for _, path := range order {
		body := readText("map/" + path)
		if body == "" {
			slash := strings.LastIndex(path, "/")
			if slash >= 0 {
				body = readText("map/" + path[:slash+1] + "(r)" + path[slash+1:])
			}
		}
		if body == "" {
			missing = append(missing, path)
			continue
		}
		bodies[path] = body
		for _, line := range strings.Split(body, "\n") {
			s := strings.TrimSpace(line)
			if len(s) < 3 || !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
				continue
			}
			tagCount[strings.ToLower(s)]++
		}
	}

	fmt.Fprintf(file, "===== town maps referenced: %d, bodies read: %d, missing: %d =====\n", len(order), len(bodies), len(missing))
	for _, path := range missing {
		fmt.Fprintf(file, "MISSING %s\n", path)
	}
	fmt.Fprintf(file, "\n===== tag frequency =====\n")
	tags := make([]string, 0, len(tagCount))
	for tag := range tagCount {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		if tagCount[tags[i]] == tagCount[tags[j]] {
			return tags[i] < tags[j]
		}
		return tagCount[tags[i]] > tagCount[tags[j]]
	})
	for _, tag := range tags {
		fmt.Fprintf(file, "%-46s %d\n", tag, tagCount[tag])
	}

	fmt.Fprintf(file, "\n===== interesting sections =====\n")
	for _, path := range order {
		body, ok := bodies[path]
		if !ok {
			continue
		}
		var found []string
		lines := strings.Split(body, "\n")
		for i, line := range lines {
			s := strings.TrimSpace(line)
			if len(s) < 3 || !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
				continue
			}
			if !interest.MatchString(s) {
				continue
			}
			end := i + 1
			for end < len(lines) && end < i+40 {
				if strings.TrimSpace(lines[end]) == "[/"+strings.Trim(s, "[]")+"]" {
					end++
					break
				}
				end++
			}
			found = append(found, strings.Join(lines[i:end], "\n"))
		}
		if len(found) == 0 {
			continue
		}
		fmt.Fprintf(file, "\n--- %s   refs=%s ---\n%s\n", path, strings.Join(refsByMap[path], ","), strings.Join(found, "\n"))
	}
	t.Logf("wrote %s (%d maps)", out, len(bodies))
}

// areaMapAndID pulls the area id and the map path out of one raw [area] block.
func areaMapAndID(block string) (int, string) {
	flat := strings.ReplaceAll(block, "`", "")
	if idx := strings.Index(flat, "[area]"); idx >= 0 {
		flat = flat[idx+len("[area]"):]
	}
	area := -1
	path := ""
	for _, line := range strings.Split(flat, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || s == "[/area]" {
			continue
		}
		if area < 0 {
			if fields := strings.Fields(s); len(fields) > 0 {
				if n := atoiLoose(fields[0]); n >= 0 {
					area = n
				}
			}
		}
		for _, tok := range strings.Fields(s) {
			if path == "" && strings.HasSuffix(tok, ".map") {
				path = tok
			}
		}
	}
	return area, path
}
