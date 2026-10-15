package store

import (
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotspawn "robot/internal/capability/robotspawn"
	"robot/internal/shared"
)

type Maintenance struct {
	Env MaintenanceEnv
}

type MaintenanceEnv interface {
	LoadMapCatalog() []shared.MapCatalogItem
	Logf(format string, args ...interface{})
	RandBetween(min, max int) int
	RandomMap(maps []shared.MapCatalogItem, level int) (shared.MapCatalogItem, bool)
	RobotLocations() ([]shared.MapLocation, error)
	ResetPrivateStore(uid int)
	RestoreDummyNormal(info robotcap.Info) error
	RevokeStorePermission(uid, cid int) error
	SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error)
	SyncCharacterVillage(cid int, village int) (int, error)
}

// demandLocationEnv is the part of MaintenanceEnv that knows the operator's
// demand plan. An env that does not implement it leaves the fallback position
// alone, which is what an adapter with no plan wants.
type demandLocationEnv interface {
	DemandLocation(uid int) (village, area, x, y int, ok bool)
}

func (m Maintenance) RestoreAutoNormalPosition(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) (robotcap.Info, error) {
	env := m.Env
	maps := env.LoadMapCatalog()
	normal := m.randomNormalPosition(info, rc, maps)
	if err := env.RestoreDummyNormal(normal); err != nil {
		env.Logf("[AutoStore] uid=%d restore_normal_write_failed reason=%s pos=%d/%d/%d/%d err=%v\n",
			normal.UID, reason, normal.Village, normal.Area, normal.X, normal.Y, err)
		return normal, err
	}
	if statPrev, err := env.SyncCharacterVillage(normal.CID, normal.Village); err != nil {
		env.Logf("[AutoStore] uid=%d restore_charac_village_sync_failed cid=%d village=%d err=%v\n",
			normal.UID, normal.CID, normal.Village, err)
	} else {
		env.Logf("[AutoStore] cid=%d charac_village_synced village=%d stat_prev=%d\n", normal.CID, normal.Village, statPrev)
	}
	env.Logf("[AutoStore] uid=%d restore_normal reason=%s pos=%d/%d/%d/%d\n",
		normal.UID, reason, normal.Village, normal.Area, normal.X, normal.Y)
	return normal, nil
}

func (m Maintenance) FinishStoreState(uid, cid int, reason string) {
	if uid <= 0 {
		return
	}
	env := m.Env
	if cid <= 0 {
		if robots, err := env.SelectRobots(robotcap.CommandRequest{UIDs: []int{uid}}); err == nil && len(robots) > 0 {
			cid = robots[0].CID
		}
	}
	if err := env.RevokeStorePermission(uid, cid); err != nil {
		env.Logf("[StoreCleanup] uid=%d cid=%d reason=%s err=%v\n", uid, cid, reason, err)
	}
	env.ResetPrivateStore(uid)
	env.Logf("[StoreCleanup] uid=%d cid=%d reason=%s\n", uid, cid, reason)
}

// randomNormalPosition picks where a robot stands once its store round is over.
// The operator's demand plan outranks every rule below: a robot the demand
// layer pinned goes back to its pin, because the pin is the operator saying
// where that robot belongs. Without this check the restore sweep was free to
// move a named crowd off the map it was named for.
func (m Maintenance) randomNormalPosition(info robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) robotcap.Info {
	env := m.Env
	normal := info
	if plans, ok := env.(demandLocationEnv); ok {
		if village, area, x, y, pinned := plans.DemandLocation(info.UID); pinned {
			normal.Village, normal.Area = village, area
			normal.X, normal.Y = x, y
			return normal
		}
	}
	normal.Village = rc.SpawnFallbackVillage
	normal.Area = rc.SpawnArea
	normal.X = env.RandBetween(rc.SpawnXMin, rc.SpawnXMax)
	normal.Y = env.RandBetween(rc.SpawnYMin, rc.SpawnYMax)
	normalMaps := shared.FilterGenericAreaMaps(FilterNormalMaps(maps), info.GuildID)
	locations, locationErr := env.RobotLocations()
	balanced := false
	if locationErr == nil {
		if target, ok := robotspawn.BalancedLocation(env, normalMaps, normal.Level, locations); ok {
			normal.Village = target.Map.Village
			normal.Area = target.Map.Area
			normal.X = target.X
			normal.Y = target.Y
			balanced = true
		}
	} else {
		env.Logf("[AutoStore] uid=%d restore_normal_locations_failed err=%v\n", normal.UID, locationErr)
	}
	if !balanced {
		if mp, ok := env.RandomMap(normalMaps, normal.Level); ok {
			normal.Village = mp.Village
			normal.Area = mp.Area
			if x, y, pointOK := robotspawn.RandomPointInMap(env, mp); pointOK {
				normal.X, normal.Y = x, y
			}
		}
	}
	// The configured spawn location is deliberately not applied here. With
	// follow_account set it resolves to whichever town the operator's own
	// character is standing in, so every store round ended by dragging its
	// robot into that one town: the whole fleet, the named resident crowds
	// included, converged on a single map and the server's session loops
	// stalled under the crowd. The balanced and random picks above already
	// land on a usable normal map, which is all a restore needs.
	if !mapAreaIncluded(normalMaps, normal.Village, normal.Area) {
		normal.Village = rc.SpawnFallbackVillage
		normal.Area = rc.SpawnArea
		normal.X = env.RandBetween(rc.SpawnXMin, rc.SpawnXMax)
		normal.Y = env.RandBetween(rc.SpawnYMin, rc.SpawnYMax)
		if !mapAreaIncluded(normalMaps, normal.Village, normal.Area) {
			normal.Village = 1
			normal.Area = 0
		}
	}
	return normal
}

func FilterNormalMaps(maps []shared.MapCatalogItem) []shared.MapCatalogItem {
	if len(maps) == 0 {
		return nil
	}
	out := make([]shared.MapCatalogItem, 0, len(maps))
	for _, mp := range maps {
		if IsNormalMapEligible(mp) {
			out = append(out, mp)
		}
	}
	return out
}

func mapAreaIncluded(maps []shared.MapCatalogItem, village, area int) bool {
	for _, mp := range maps {
		if mp.Village == village && mp.Area == area {
			return true
		}
	}
	return false
}
