package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	"robot/internal/shared"
)

type runtimeActionTransport struct {
	manager *RobotManager
}

func (t runtimeActionTransport) MoveTown(_ context.Context, command shared.RuntimeMoveCommand) error {
	return t.manager.doll.Move(command)
}

func (t runtimeActionTransport) ShoutLocal(_ context.Context, command shared.RuntimeShoutCommand) error {
	return t.manager.doll.Shout(command)
}

func (t runtimeActionTransport) PartyActive(uid int) bool {
	return t.manager.doll.PartyActive(uid)
}

func (t runtimeActionTransport) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	if provider, ok := t.manager.doll.(runtimeStatusMapProvider); ok {
		status := provider.RuntimeStatusMap()
		if status != nil {
			return status
		}
	}
	status := make(map[int]robotcap.RuntimeStatus)
	for _, item := range t.manager.doll.RuntimeStatus() {
		status[item.UID] = item
	}
	return status
}

type sessionDriver interface {
	EnsureWorldHorn(int) error
	PrepareOnline(robotcap.Info, robotconfig.RuntimeConfig) robotcap.Info
	SendLogout(int) error
	SendOnline([]shared.RuntimeOnlineUser) error
	ForceClose(int) bool
}

type runtimeSessionDriver struct {
	manager *RobotManager
}

func (d runtimeSessionDriver) EnsureWorldHorn(cid int) error {
	return d.manager.storePreparer().EnsureWorldHornByCID(cid)
}

func (runtimeSessionDriver) PrepareOnline(info robotcap.Info, _ robotconfig.RuntimeConfig) robotcap.Info {
	return info
}

func (d runtimeSessionDriver) SendLogout(uid int) error {
	err := d.manager.doll.Logout(uid)
	if err == nil {
		d.manager.markSessionLogout(uid, time.Now())
	}
	return err
}

func (d runtimeSessionDriver) SendOnline(users []shared.RuntimeOnlineUser) error {
	d.manager.waitSessionRelogin(users)
	return d.manager.doll.Online(users)
}

func (d runtimeSessionDriver) ForceClose(uid int) bool {
	closer, ok := d.manager.doll.(interface{ ForceClose(int) bool })
	return ok && closer.ForceClose(uid)
}

type protocolSessionDriver struct {
	manager   *RobotManager
	transport BackendSessionTransport
}

func (protocolSessionDriver) EnsureWorldHorn(int) error { return nil }

const (
	// spawnCrowdFloor/Bias bound when an online character is relocated out of
	// an over-crowded map: it must hold more than spawnCrowdFloor robots and
	// more than spawnCrowdBias times the least crowded spawn area.
	spawnCrowdFloor = 14
	spawnCrowdBias  = 3
)

func (d protocolSessionDriver) PrepareOnline(info robotcap.Info, rc robotconfig.RuntimeConfig) robotcap.Info {
	maps := d.manager.loadMapCatalog()
	if rc.SpawnFixed || strings.TrimSpace(rc.FollowAccount) != "" {
		d.manager.applyConfiguredLocation(&info, rc, maps)
		return info
	}
	spawnMaps := robotspawn.NormalMaps(maps)
	if len(spawnMaps) == 0 {
		spawnMaps = maps
	}
	// Serialize repair decisions: each one must see the previous location
	// write-back, otherwise concurrent logins pile into the same mirror.
	d.manager.spawnRepairMu.Lock()
	defer d.manager.spawnRepairMu.Unlock()
	locations, err := d.manager.robotLocations()
	if err != nil {
		locations = nil
	}
	valid := robotspawn.HasUsableMap(spawnMaps, info.Village, info.Area)
	crowded := false
	if valid && len(locations) > 0 {
		counts := make(map[shared.MapAreaKey]int, len(locations))
		for _, location := range locations {
			counts[shared.MapAreaKey{Village: location.Village, Area: location.Area}]++
		}
		mine := counts[shared.MapAreaKey{Village: info.Village, Area: info.Area}]
		least := mine
		for _, mp := range spawnMaps {
			if count := counts[shared.MapAreaKey{Village: mp.Village, Area: mp.Area}]; count < least {
				least = count
				if least == 0 {
					break
				}
			}
		}
		crowded = mine > spawnCrowdFloor && mine > least*spawnCrowdBias
		if !crowded {
			// Mirror instances count as one logical map: a family can be
			// over-full even when every single instance looks acceptable.
			crowded = robotspawn.Crowded(spawnMaps, locations, info.Village, info.Area, spawnCrowdFloor, spawnCrowdBias)
		}
	}
	if valid && !crowded {
		return info
	}
	// Adopted or freshly created characters can carry the server's default
	// town (1/0), an event/housing area, or pile up in a single map. None of
	// those are stable homes, so pick a capacity-balanced regular town and
	// record it immediately so concurrent decisions see the same occupancy.
	if target, ok := robotspawn.BalancedFamilyLocation(spawnEnv{manager: d.manager}, spawnMaps, info.Level, locations, shared.MapAreaKey{Village: info.Village, Area: info.Area}); ok {
		robotLogf("[SpawnRepair] uid=%d cid=%d level=%d from=%d/%d -> %d/%d/%d/%d crowded=%t\n",
			info.UID, info.CID, info.Level, info.Village, info.Area, target.Map.Village, target.Map.Area, target.X, target.Y, crowded)
		info.Village, info.Area = target.Map.Village, target.Map.Area
		info.X, info.Y = target.X, target.Y
		d.manager.rememberRobotLocation(info.UID, info.Village, info.Area, info.X, info.Y)
	}
	return info
}

func (d protocolSessionDriver) SendLogout(uid int) error {
	err := d.transport.Close(uid)
	if err == nil {
		d.manager.markSessionLogout(uid, time.Now())
	}
	return err
}

func (d protocolSessionDriver) SendOnline(users []shared.RuntimeOnlineUser) error {
	rc := d.manager.loadRobotConfig()
	if err := d.manager.populateBackendSessionIdentities(users); err != nil {
		return err
	}
	opened := make([]int, 0, len(users))
	for _, user := range users {
		if user.AccountName == "" {
			d.closeOpened(opened)
			return fmt.Errorf("backend session account is required for uid %d", user.UID)
		}
		if err := d.transport.Open(context.Background(), user.UID, shared.OpenSessionRequest{
			AccountName: user.AccountName, PasswordHash: user.PasswordHash, CharacterSlot: uint16(user.CharacterSlot),
			EnablePartyDungeonFollower: backendPartyFollowerEnabled(d.manager.backendInfo, rc),
			EnablePartyAutoAccept:      backendPartyAutoAcceptEnabled(d.manager.backendInfo, rc),
			EnablePartyFollow:          backendPartyFollowEnabled(d.manager.backendInfo, rc),
			InitialTownKnown:           true,
			InitialVillage:             user.BirthVillage,
			InitialArea:                user.BirthArea,
			InitialX:                   user.BirthX,
			InitialY:                   user.BirthY,
			HomeVillage:                rc.SpawnFallbackVillage,
		}); err != nil {
			d.closeOpened(opened)
			return err
		}
		opened = append(opened, user.UID)
	}
	return nil
}

func backendPartyFollowerEnabled(info shared.BackendInfo, rc robotconfig.RuntimeConfig) bool {
	status := info.Capabilities[shared.CapabilityDungeonFollow]
	if !status.Enabled {
		return false
	}
	// Account mode requires the configured operator account; adapters may instead
	// declare auto-accept without requiring an account filter.
	if status.Mode == "account" {
		return strings.TrimSpace(rc.FollowAccount) != ""
	}
	return true
}

// backendPartyAutoAcceptEnabled reports whether new sessions should answer
// forwarded party invites. applyBackendConfigPolicy already clears the flag
// for backends without the party capability; the capability check here keeps
// a directly constructed config honest too.
func backendPartyAutoAcceptEnabled(info shared.BackendInfo, rc robotconfig.RuntimeConfig) bool {
	return rc.AutoPartyAccept && info.Supports(shared.CapabilityParty)
}

// backendPartyFollowEnabled reports whether new sessions should also track the
// accepted leader's live town position. Following rides on the same party
// transport as auto-accept, so it is gated by the same capability.
func backendPartyFollowEnabled(info shared.BackendInfo, rc robotconfig.RuntimeConfig) bool {
	return rc.AutoPartyFollow && info.Supports(shared.CapabilityParty)
}

func (d protocolSessionDriver) closeOpened(uids []int) {
	for _, uid := range uids {
		_ = d.transport.Close(uid)
	}
}

func (d protocolSessionDriver) ForceClose(uid int) bool {
	return d.transport.Close(uid) == nil
}

// CharacterCacheBoundaryIsSessionClose reports that closing the game session
// already releases the server-side character snapshot. Protocol backends have
// no separate NoCache command, so the scheduler must not require one before an
// offline database write.
func (protocolSessionDriver) CharacterCacheBoundaryIsSessionClose() bool { return true }
