package pvf

import (
	"strconv"
	"strings"
)

func pvfTagValue(body, tag string) string {
	lines := splitPVFLines(body)
	want := strings.ToLower(strings.TrimSpace(tag))
	for i, line := range lines {
		if strings.ToLower(cleanPVFString(line)) == want {
			return cleanPVFString(nextLine(lines, i))
		}
	}
	return ""
}

func pvfSectionInts(body, tag string) []int {
	lines := splitPVFLines(body)
	want := strings.ToLower(strings.TrimSpace(tag))
	for i, line := range lines {
		if strings.ToLower(cleanPVFString(line)) != want {
			continue
		}
		var out []int
		for _, valueLine := range lines[i+1:] {
			trimmed := strings.TrimSpace(valueLine)
			if strings.HasPrefix(trimmed, "[") {
				break
			}
			for _, field := range strings.Fields(cleanPVFString(trimmed)) {
				if value, err := strconv.Atoi(field); err == nil && value > 0 {
					out = append(out, value)
				}
			}
		}
		return out
	}
	return nil
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func appendUniqueInt(values []int, value int) []int {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func pvfDirectTagInts(body, tag string) []int {
	lines := splitPVFLines(body)
	want := strings.ToLower(strings.TrimSpace(tag))
	inside := false
	depth := 0
	var out []int
	for _, line := range lines {
		trimmed := strings.Trim(strings.TrimSpace(line), "`")
		name, closing, isTag := pvfTagLine(trimmed)
		if !inside {
			if isTag && !closing && name == want {
				inside = true
			}
			continue
		}
		if isTag {
			if closing && depth == 0 && name == want {
				return out
			}
			if closing {
				if depth > 0 {
					depth--
				}
			} else {
				depth++
			}
			continue
		}
		if depth > 0 {
			continue
		}
		for _, field := range strings.Fields(trimmed) {
			value, err := strconv.Atoi(field)
			if err == nil {
				out = append(out, value)
			}
		}
	}
	return out
}

func pvfTagLine(line string) (name string, closing, ok bool) {
	line = strings.TrimSpace(line)
	if len(line) < 2 || (!strings.HasPrefix(line, "[") && !strings.HasSuffix(line, "]")) {
		return "", false, false
	}
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
	if strings.HasPrefix(name, "/") {
		closing = true
		name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	}
	if name == "" {
		return "", false, false
	}
	return strings.ToLower(name), closing, true
}

func section(text, startTag, endTag string) string {
	start := strings.Index(text, startTag)
	if start < 0 {
		return ""
	}
	start += len(startTag)
	end := strings.Index(text[start:], endTag)
	if end < 0 {
		return ""
	}
	return text[start : start+end]
}

func splitPVFLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "\n")
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func nextLine(lines []string, i int) string {
	if i+1 >= len(lines) {
		return ""
	}
	return lines[i+1]
}

func cleanPVFString(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`[]\x00")
	return strings.TrimSpace(s)
}

func cleanPVFTableString(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
	return strings.TrimSpace(s)
}

func normalizePVFPath(s string) string {
	s = strings.TrimSpace(strings.Trim(s, "`\x00"))
	s = strings.ReplaceAll(s, "\\", "/")
	return strings.ToLower(s)
}

func atoi(s string) int {
	s = cleanPVFString(s)
	n, _ := strconv.Atoi(s)
	return n
}
