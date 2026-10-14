package equipment

import (
	"strings"

	"robot/internal/shared"
)

// Weapon sub-type restrictions (武器种类对口).
//
// A weapon's [usable job] block names base classes only, so it cannot say that
// a lightsaber belongs to 剑魂 and 流浪武士 alone. The PVF states that
// separately: all 574 character/swordman/weapon/beamsword/*.equ records carry
//
//	[required job skill]
//	`[swordman]` 33 `[demonic swordman]` 33 `[at swordman]` 95
//	[/required job skill]
//
// where skill 33 is 光剑掌握 (Swordman/EquipLightSword.skl), a passive whose
// [growtype maximum level] is nonzero only at the branches that master it:
// 10 at positions 1 and 5 of the swordman list, 1 at position 0 of the demonic
// swordman's, and 1 at position 4 of the at-swordman's.
//
// Neither the catalog projection nor the server carries that gate: the archive
// reader has no case for the tag, and the live server reads [required job
// skill] only in the 支援兵 dungeon-companion generator, never when a player
// equips. So the table is stated here, the same way the armor mastery table
// is, and the cn90 backend checks it against the archive.
//
// Lightsaber is the only sub-type this PVF gates. Every other weapon
// directory is unrestricted by the archive, and a restriction the archive does
// not state would be invention, so none is applied.
const (
	// WeaponLightsaber is the directory 光剑 records live in.
	WeaponLightsaber = "beamsword"
	// weaponItemType is the catalog item type of a weapon.
	weaponItemType = 1
	// weaponPathToken is the directory the sub-type hangs under:
	// equipment/character/<race>/weapon/<sub-type>/<file>.equ.
	weaponPathToken = "weapon"
)

// lightsaberMasterBranches names, per job, the transfer branches whose
// 光剑掌握 is above level 0. Branch 0 is absent on purpose: an untransferred
// swordman may wear any weapon its base class lists, and a table entry that
// contradicts the game is worse than no entry.
//
// Job 9 (黑暗武士) is absent for the same reason: its own skill 33 entry gives
// position 0 a maximum level of 1, and the job has no branches to key on.
var lightsaberMasterBranches = map[int][]int{
	0:  {1, 5}, // 剑魂, 剑影
	11: {4},    // 流浪武士
}

// WeaponSubtypeOfPath returns the weapon sub-type directory of an item path,
// for example "beamsword" in
// equipment/character/swordman/weapon/beamsword/beamsword_0251.equ. It reads
// the directory immediately under "weapon" so a race or part directory that
// happens to be spelled like a sub-type cannot be mistaken for one, and
// returns "" for anything that is not under a weapon directory.
func WeaponSubtypeOfPath(path string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	parts := strings.Split(strings.Trim(normalized, "/"), "/")
	for index, part := range parts {
		if part != weaponPathToken {
			continue
		}
		if index+1 >= len(parts) {
			return ""
		}
		// The last segment is the file; a sub-type must be a directory.
		if index+1 == len(parts)-1 {
			return ""
		}
		return parts[index+1]
	}
	return ""
}

// LightsaberAllowed reports whether the character behind job/grow masters
// 光剑掌握 and may therefore wear a lightsaber. Jobs the table does not name
// are unrestricted.
func LightsaberAllowed(job, grow int) bool {
	branches, listed := lightsaberMasterBranches[job]
	if !listed {
		return true
	}
	branch := grow & 0x0f
	if branch == 0 {
		// An untransferred character of a listed job keeps the base class's
		// freedom; only the branches the table names are gated.
		return true
	}
	for _, allowed := range branches {
		if allowed == branch {
			return true
		}
	}
	return false
}

// weaponAllowed applies the sub-type filter. Only weapons whose path names the
// gated sub-type are affected; every other item type and sub-type passes.
func weaponAllowed(item shared.EquipmentCatalogItem, job, grow int) bool {
	if item.ItemType != weaponItemType || LightsaberAllowed(job, grow) {
		return true
	}
	return WeaponSubtypeOfPath(item.Path) != WeaponLightsaber
}
