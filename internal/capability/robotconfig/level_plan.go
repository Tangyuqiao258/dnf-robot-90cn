package robotconfig

import (
	"fmt"
	"strconv"
	"strings"
)

// LevelBand is one row of a level plan: Count robots wear a level drawn from
// [Min,Max].
//
// The fleet's level composition is a list of bands rather than one range
// because a uniform draw over level_min..level_max cannot say "mostly high
// level": raising level_min to get more level-90 robots also deletes every
// low-level one, and the crowd a player walks past needs both -- the town keeps
// a spread of levels while the level cap gets the bulk of them.
type LevelBand struct {
	Min   int
	Max   int
	Count int
}

// ParseLevelPlan reads the `[create] level_plan` syntax: a comma-separated list
// of `<level|low-high>:<count>` rows, in any order, e.g.
//
//	90-90:45,85-89:30,80-84:25,15-29:10
//
// An empty spec returns no bands and no error: it means the operator kept the
// uniform level_min..level_max draw.
func ParseLevelPlan(spec string) ([]LevelBand, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	bands := make([]LevelBand, 0, len(parts))
	for _, part := range parts {
		row := strings.TrimSpace(part)
		if row == "" {
			return nil, fmt.Errorf("rows must not be empty")
		}
		levelSpec, countSpec, ok := strings.Cut(row, ":")
		if !ok {
			return nil, fmt.Errorf("row %q must be written as <level>:<count>", row)
		}
		lowSpec, highSpec, ranged := strings.Cut(levelSpec, "-")
		low, err := strconv.Atoi(strings.TrimSpace(lowSpec))
		if err != nil {
			return nil, fmt.Errorf("row %q must start with a level", row)
		}
		high := low
		if ranged {
			if high, err = strconv.Atoi(strings.TrimSpace(highSpec)); err != nil {
				return nil, fmt.Errorf("row %q must end its level range with a level", row)
			}
		}
		count, err := strconv.Atoi(strings.TrimSpace(countSpec))
		if err != nil {
			return nil, fmt.Errorf("row %q must end with a count", row)
		}
		if low < 1 || high > 255 {
			return nil, fmt.Errorf("row %q must stay between level 1 and 255", row)
		}
		if high < low {
			return nil, fmt.Errorf("row %q has an inverted level range", row)
		}
		if count <= 0 {
			return nil, fmt.Errorf("row %q must ask for at least one robot", row)
		}
		bands = append(bands, LevelBand{Min: low, Max: high, Count: count})
	}
	return bands, nil
}

// LevelPoolSize is how many robots a plan describes.
func LevelPoolSize(bands []LevelBand) int {
	total := 0
	for _, band := range bands {
		total += band.Count
	}
	return total
}

// LevelPool expands a plan into one level per robot slot, drawing each level
// from its band through draw.
//
// The bands are dealt round-robin rather than concatenated. Robots come online
// in uid order and the online slots are far fewer than the pool, so a pool
// written band by band would put its first hundred -- the ones actually
// standing in town -- all in the highest band and leave the rest of the mix
// offline. Dealing one level per band per round makes every prefix of the pool
// proportional to the plan, whatever the login order turns out to be.
func LevelPool(bands []LevelBand, draw func(min, max int) int) []int {
	rounds := 0
	for _, band := range bands {
		if band.Count > rounds {
			rounds = band.Count
		}
	}
	pool := make([]int, 0, LevelPoolSize(bands))
	for round := 0; round < rounds; round++ {
		for _, band := range bands {
			if round >= band.Count {
				continue
			}
			level := band.Min
			if draw != nil {
				level = draw(band.Min, band.Max)
			}
			pool = append(pool, level)
		}
	}
	return pool
}

// LevelPoolIndex maps a robot uid onto its slot in the plan's pool.
//
// The pool is anchored at robot_uid_start so a uid always draws the same level:
// provisioning runs one robot at a time and resumes after every restart, and an
// index counted over that sequence would hand the pool out from the top again
// after each one. The modulo covers a uid range wider than the plan, where the
// surplus uids reuse the table; the validator keeps the reverse case -- a plan
// wider than the range -- out of the config.
func LevelPoolIndex(uid, origin, size int) int {
	if size <= 0 {
		return 0
	}
	offset := (uid - origin) % size
	if offset < 0 {
		offset += size
	}
	return offset
}
