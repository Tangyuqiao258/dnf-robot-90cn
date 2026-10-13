package pvf

import (
	"strings"

	"robot/internal/shared"
)

func jobFromEquipmentPath(path string) int {
	p := "/" + strings.ToLower(normalizePVFPath(path)) + "/"
	switch {
	case strings.Contains(p, "/character/swordman/at_avatar/"):
		return 11
	case strings.Contains(p, "/character/swordman/"):
		return 0
	case strings.Contains(p, "/character/fighter/at_avatar/"):
		return 7
	case strings.Contains(p, "/character/fighter/"):
		return 1
	case strings.Contains(p, "/character/gunner/at_avatar/"):
		return 5
	case strings.Contains(p, "/character/gunner/"):
		return 2
	case strings.Contains(p, "/character/mage/at_avatar/"):
		return 8
	case strings.Contains(p, "/character/mage/"):
		return 3
	case strings.Contains(p, "/character/priest/at_avatar/"):
		return 14
	case strings.Contains(p, "/character/priest/"):
		return 4
	case strings.Contains(p, "/character/thief/"):
		return 6
	case strings.Contains(p, "/character/demonicswordman/"):
		return 9
	default:
		return -1
	}
}

func applyEquipmentPathJob(item *shared.EquipmentCatalogItem, pathJob int) {
	if item == nil || pathJob < 0 {
		return
	}
	if item.ItemType >= 20 && item.ItemType <= 29 {
		item.UseJob = []int{pathJob}
		return
	}
	if len(item.UseJob) == 0 {
		item.UseJob = []int{pathJob}
	}
}

func equipmentTypeFromPath(path string) (int, string) {
	p := "/" + strings.ToLower(normalizePVFPath(path)) + "/"
	if !strings.Contains(p, "/avatar/") && !strings.Contains(p, "/at_avatar/") {
		return 0, ""
	}
	switch {
	case strings.Contains(p, "/cap/"):
		return 20, "hatavatar"
	case strings.Contains(p, "/hair/"):
		return 21, "hairavatar"
	case strings.Contains(p, "/face/"):
		return 22, "faceavatar"
	case strings.Contains(p, "/coat/"):
		return 23, "coatavatar"
	case strings.Contains(p, "/pants/"):
		return 24, "pantsavatar"
	case strings.Contains(p, "/shoes/"):
		return 25, "shoesavatar"
	case strings.Contains(p, "/neck/"):
		return 26, "breastavatar"
	case strings.Contains(p, "/belt/"):
		return 27, "waistavatar"
	case strings.Contains(p, "/skin/"):
		return 28, "skinavatar"
	case strings.Contains(p, "/aura/"):
		return 29, "auroraavatar"
	default:
		return 0, ""
	}
}

func parseJobs(text string) []int {
	seen := map[int]bool{}
	var out []int
	add := func(name string) {
		id := jobID(cleanPVFString(name))
		if id >= 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	rest := text
	for {
		start := strings.Index(rest, "[")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start+1:], "]")
		if end < 0 {
			break
		}
		add(rest[start+1 : start+1+end])
		rest = rest[start+1+end+1:]
	}
	if len(out) > 0 {
		return out
	}
	fields := strings.Fields(cleanPVFString(text))
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if i+1 < len(fields) {
			pair := field + " " + fields[i+1]
			before := len(out)
			add(pair)
			if len(out) > before {
				i++
				continue
			}
		}
		add(field)
	}
	return out
}

func jobID(job string) int {
	switch strings.ToLower(strings.ReplaceAll(job, " ", "")) {
	case "all":
		return 100
	case "swordman":
		return 0
	case "fighter":
		return 1
	case "gunner":
		return 2
	case "mage":
		return 3
	case "priest":
		return 4
	case "atpriest":
		return 14
	case "atgunner":
		return 5
	case "thief":
		return 6
	case "atfighter":
		return 7
	case "atmage":
		return 8
	case "demonicswordman":
		return 9
	case "creatormage":
		return 10
	default:
		return -1
	}
}

func equipmentType(v string) int {
	switch normalizeEquipmentTypeName(v) {
	case "weapon":
		return 1
	case "titlename", "title", "title name":
		return 2
	case "coat":
		return 3
	case "shoulder":
		return 4
	case "pants":
		return 5
	case "shoes":
		return 6
	case "waist":
		return 7
	case "amulet":
		return 8
	case "wrist":
		return 9
	case "ring":
		return 10
	case "support":
		return 11
	case "magicstone", "magic stone", "magic_stone":
		return 12
	case "earring", "ear ring":
		return 13
	case "hatavatar":
		return 20
	case "hairavatar":
		return 21
	case "faceavatar":
		return 22
	case "coatavatar":
		return 23
	case "pantsavatar":
		return 24
	case "shoesavatar":
		return 25
	case "breastavatar":
		return 26
	case "waistavatar":
		return 27
	case "skinavatar":
		return 28
	case "auroraavatar":
		return 29
	case "creature":
		return 30
	case "artifactred":
		return 31
	case "artifactblue":
		return 32
	case "artifactgreen":
		return 33
	default:
		return -1
	}
}

func normalizeEquipmentTypeName(v string) string {
	// Some adapter PVFs append a numeric subtype after the bracketed type,
	// for example "[weapon] 20". The common catalog only needs the type token.
	v = strings.ToLower(strings.TrimSpace(v))
	if start := strings.IndexByte(v, '['); start >= 0 {
		if end := strings.IndexByte(v[start+1:], ']'); end > 0 {
			v = v[start+1 : start+1+end]
		}
	} else if end := strings.IndexByte(v, ']'); end > 0 {
		// cleanPVFString may already have removed the opening delimiters.
		v = v[:end]
	} else {
		v = cleanPVFString(v)
	}
	v = cleanPVFString(v)
	v = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(v)
	switch v {
	case "redartifact", "creatureartifactred", "creatureredartifact", "petartifactred", "petredartifact":
		return "artifactred"
	case "blueartifact", "creatureartifactblue", "creatureblueartifact", "petartifactblue", "petblueartifact":
		return "artifactblue"
	case "greenartifact", "creatureartifactgreen", "creaturegreenartifact", "petartifactgreen", "petgreenartifact":
		return "artifactgreen"
	default:
		return v
	}
}
