package robotspawn

import (
	"strings"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type Env interface {
	FollowAccountVillage(account string) (int, bool, error)
	RandBetween(min, max int) int
	RandIntn(n int) int
}

func ApplyConfiguredLocation(env Env, info *robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) {
	if env == nil || info == nil {
		return
	}
	if rc.SpawnFixed {
		village := rc.SpawnVillage
		if village < 1 {
			village = 1
		}
		ApplyVillageLocation(env, info, village, rc, maps)
		return
	}
	account := strings.TrimSpace(rc.FollowAccount)
	if account == "" {
		return
	}
	village, ok, err := env.FollowAccountVillage(account)
	if err != nil || !ok {
		return
	}
	ApplyVillageLocation(env, info, village, rc, maps)
}

func ApplyVillageLocation(env Env, info *robotcap.Info, village int, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) {
	maps = AdmittedMaps(env, maps)
	var candidates []shared.MapCatalogItem
	for _, mp := range maps {
		if mp.Use && mp.Village == village && mp.Level <= info.Level {
			candidates = append(candidates, mp)
		}
	}
	if len(candidates) == 0 {
		return
	}
	info.Village = village
	if rc.SpawnArea >= 0 {
		var areaMatches []shared.MapCatalogItem
		for _, mp := range candidates {
			if mp.Area == rc.SpawnArea {
				areaMatches = append(areaMatches, mp)
			}
		}
		if len(areaMatches) > 0 {
			candidates = areaMatches
		}
	}
	mp := candidates[safeRandIntn(env, len(candidates))]
	info.Area = mp.Area
	rectangles := MapRectangles(mp)
	configured := IntersectRectangles(rectangles, shared.MapRectangle{XMin: rc.SpawnXMin, XMax: rc.SpawnXMax, YMin: rc.SpawnYMin, YMax: rc.SpawnYMax})
	if len(configured) > 0 {
		rectangles = configured
	}
	if x, y, ok := RandomPoint(env, rectangles); ok {
		info.X, info.Y = x, y
	}
}

func RandomMap(env Env, maps []shared.MapCatalogItem, level int) (shared.MapCatalogItem, bool) {
	maps = AdmittedMaps(env, maps)
	var candidates []shared.MapCatalogItem
	for _, mp := range maps {
		if mp.Use && mp.Village >= 0 && mp.Area >= 0 && mp.Level <= level {
			candidates = append(candidates, mp)
		}
	}
	if len(candidates) == 0 {
		return shared.MapCatalogItem{}, false
	}
	return candidates[safeRandIntn(env, len(candidates))], true
}

// HasUsableMap reports whether the catalog contains a usable map for the
// village/area with at least one movement rectangle. Adopted characters can
// carry the server's default town (1/0), which is not a real map; callers use
// this to detect and repair such positions before logging in.
func HasUsableMap(maps []shared.MapCatalogItem, village, area int) bool {
	for _, mp := range maps {
		if mp.Use && mp.Village == village && mp.Area == area && len(MapRectangles(mp)) > 0 {
			return true
		}
	}
	return false
}

// NormalMaps filters the catalog to areas eligible for ordinary robot spawns:
// usable geometry and not marked PvP-only. The catalog shape is shared with the
// store capability, but spawn eligibility stays a spawn concern. Areas whose
// kind is not the store kind are still normal towns and must stay eligible.
func NormalMaps(maps []shared.MapCatalogItem) []shared.MapCatalogItem {
	out := make([]shared.MapCatalogItem, 0, len(maps))
	for _, mp := range maps {
		if !mp.Use {
			continue
		}
		if mp.NormalEligible != nil && !*mp.NormalEligible {
			continue
		}
		out = append(out, mp)
	}
	return out
}

func safeRandIntn(env Env, n int) int {
	if env == nil || n <= 0 {
		return 0
	}
	v := env.RandIntn(n)
	if v < 0 || v >= n {
		return 0
	}
	return v
}
