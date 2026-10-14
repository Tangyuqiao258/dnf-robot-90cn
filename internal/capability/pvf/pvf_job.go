package pvf

import (
	"regexp"
	"strconv"
	"strings"
)

// pvfListEntryPattern matches one `<id> `path“ entry of a PVF LST file. The
// game server's character list uses exactly this shape (see its
// CharacterStatComputer / CharacterSkillProfile loaders).
var pvfListEntryPattern = regexp.MustCompile("(\\d+)\\s+`([^`]+)`")

// pvfBacktickPattern extracts every backtick-delimited name.
var pvfBacktickPattern = regexp.MustCompile("`([^`]*)`")

// maxA21FirstGrow mirrors the server's CharacterStatComputer guard: the first
// grow nibble must not exceed 5.
const maxA21FirstGrow = 5

// ProjectJobGrowCatalog mirrors the game server's character/*.chr transfer
// graph: character/character.lst maps a job id to its .chr file, and the
// [growtype name] line lists the base class name followed by every transfer
// branch name. Commented placeholder names ("//...") are declared but not
// released and must not be offered. The result maps a job id to the valid
// first-grow (transfer) values of that job.
func ProjectJobGrowCatalog(archive TownTextArchive) map[int][]int {
	result := make(map[int][]int)
	for job, names := range projectJobGrowNames(archive) {
		if branches := releasedGrowBranches(names); len(branches) > 0 {
			result[job] = branches
		}
	}
	return result
}

// ProjectJobGrowNames is ProjectJobGrowCatalog with the names kept: the result
// maps a job id to its [growtype name] list, index 0 the base class and index N
// the transfer branch whose first-grow value is N. An entry is empty when the
// .chr declares that position without releasing it. Callers that have to
// identify a branch by something other than its number (armor mastery, for one)
// key off these names.
func ProjectJobGrowNames(archive TownTextArchive) map[int][]string {
	result := make(map[int][]string)
	for job, names := range projectJobGrowNames(archive) {
		if len(names) > 1 {
			result[job] = names
		}
	}
	return result
}

func projectJobGrowNames(archive TownTextArchive) map[int][]string {
	result := make(map[int][]string)
	if archive == nil {
		return result
	}
	lst, _ := archive.ReadText("character/character.lst")
	if lst == "" {
		return result
	}
	for _, match := range pvfListEntryPattern.FindAllStringSubmatch(lst, -1) {
		job, err := strconv.Atoi(match[1])
		if err != nil || job < 0 || job > 255 {
			continue
		}
		path := normalizePVFPath("character/" + match[2])
		body, _ := archive.ReadText(path)
		if names := parseJobGrowNames(body); len(names) > 0 {
			result[job] = names
		}
	}
	return result
}

// releasedGrowBranches turns a [growtype name] list into the first-grow values a
// character may actually take. The server's CharacterStatComputer guards the
// nibble to maxA21FirstGrow, and parseJobGrowNames blanked every position the
// .chr does not release.
func releasedGrowBranches(names []string) []int {
	branches := make([]int, 0, len(names))
	for index := 1; index <= maxA21FirstGrow && index < len(names); index++ {
		if names[index] == "" {
			continue
		}
		branches = append(branches, index)
	}
	return branches
}

// parseJobGrowBranches reads the first-grow values of a .chr file.
func parseJobGrowBranches(body string) []int {
	return releasedGrowBranches(parseJobGrowNames(body))
}

// parseJobGrowNames reads the [growtype name] line of a .chr file. Index 0 is
// the base class; each following name is a transfer branch whose 1-based
// position is the first-grow value. A position is blanked when the .chr
// declares it without releasing it, which the installed archive does three
// ways:
//
//   - a commented placeholder ("//..."),
//   - the base class name written again — jobs 3, 5, 6 and 11 carry such a
//     phantom sixth position, and the GM tool's ProfessionCatalog90 skips it
//     with the same comparison,
//   - a missing [growtype N] stat section, which the server needs to build the
//     character's stats.
func parseJobGrowNames(body string) []string {
	lower := strings.ToLower(body)
	start := strings.Index(lower, "[growtype name]")
	if start < 0 {
		return nil
	}
	rest := body[start+len("[growtype name]"):]
	// The server regex treats the names as the first non-empty line after the
	// tag, so accept both the same-line and next-line layouts.
	rest = strings.TrimLeft(rest, " \t\r\n")
	if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
		rest = rest[:end]
	}
	matches := pvfBacktickPattern.FindAllStringSubmatch(rest, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, strings.TrimSpace(match[1]))
	}
	base := names[0]
	for index := 1; index < len(names); index++ {
		name := names[index]
		if name == "" || strings.HasPrefix(name, "//") || strings.EqualFold(name, base) ||
			!hasPVFTag(body, "growtype "+strconv.Itoa(index+1)) {
			names[index] = ""
		}
	}
	return names
}

func hasPVFTag(body, tag string) bool {
	return strings.Contains(strings.ToLower(body), "["+strings.ToLower(tag)+"]")
}
