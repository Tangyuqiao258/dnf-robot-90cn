package cn90

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestLiveDumpTownNames is a read-only reconnaissance helper: it dumps every
// town (village) entry from the runtime PVF together with its raw [area]
// blocks so the Chinese map names can be matched against village/area ids.
// It writes a UTF-8 text file and never touches the game or database.
func TestLiveDumpTownNames(t *testing.T) {
	if os.Getenv("CN90_LIVE_DUMP_TOWN") == "" {
		t.Skip("set CN90_LIVE_DUMP_TOWN=1 to dump town names")
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
	out := os.Getenv("CN90_LIVE_DUMP_OUT")
	if out == "" {
		out = "C:\\Users\\wangh\\.local\\bin\\jobs\\eef80d7d\\tmp\\town_dump.txt"
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	lst := readText("town/town.lst")
	fmt.Fprintf(file, "===== town/town.lst raw, %d bytes =====\n%s\n", len(lst), lst)

	fields := strings.Fields(strings.ReplaceAll(lst, "`", ""))
	for i := 0; i+1 < len(fields); i++ {
		id := atoiLoose(fields[i])
		if id <= 0 {
			continue
		}
		path := fields[i+1]
		if !strings.ContainsAny(path, "/.") {
			continue
		}
		i++
		body := readText("town/" + path)
		if body == "" {
			body = readText("town/" + path + ".twn")
		}
		if body == "" {
			body = readText("town/" + path + ".map")
		}
		if body == "" {
			continue
		}
		fmt.Fprintf(file, "\n===== village %d file town/%s (%d bytes) =====\n%s\n", id, path, len(body), body)
		for _, area := range rawAreaBlocks(body) {
			fmt.Fprintf(file, "--- village %d area block ---\n%s\n", id, area)
		}
	}
	t.Logf("wrote %s", out)
}

func atoiLoose(value string) int {
	out := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return -1
		}
		out = out*10 + int(r-'0')
	}
	return out
}

func rawAreaBlocks(body string) []string {
	var blocks []string
	rest := body
	for {
		start := strings.Index(rest, "[area]")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start:], "[/area]")
		if end < 0 {
			break
		}
		blocks = append(blocks, strings.TrimSpace(rest[start:start+end+len("[/area]")]))
		rest = rest[start+end+len("[/area]"):]
	}
	return blocks
}
