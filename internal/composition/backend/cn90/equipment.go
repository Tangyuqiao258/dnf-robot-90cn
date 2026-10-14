package cn90

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	equipmentcap "robot/internal/capability/equipment"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// DNF90 native equipment tables and the server's initial-equipment writer
// constants.
const (
	dnfEquipmentsTable       = "dnf_equipments"
	dnfEquipmentEntriesTable = "dnf_equipment_entries"
	dnfEquipmentExtraTable   = "dnf_equipment_entry_extra"

	cn90InitialEquipmentCreateValue = uint32(1)
	cn90InitialEquipmentDefaultDur  = 40

	// cn90LoadoutMarkerKey marks a row the robot generated. Generate only: a row
	// without it was written by the server or by an operator and is never owned
	// by the robot.
	cn90LoadoutMarkerKey = "robot_loadout"
	// cn90LoadoutVersion is the marker's value. Bumping it re-rolls the whole
	// fleet exactly once: equipmentNeedsLoadout treats a row carrying an older
	// value as unmarked, so every robot written by an earlier version is rebuilt
	// on the next startup and operator edits survive from then on. Without the
	// version, a rule that changes what the selector would pick could never reach
	// a robot that already wears a loadout.
	//
	//	1 — first generated loadouts
	//	2 — armor follows the transfer branch's mastered material
	//	3 — one set per equipment family, gear level on the 5s, rarity and
	//	    upgrade from the level band, 增幅 from level 71, 耳环 at level 90
	//	4 — 时装 follows the level band, 稀有装扮 from level 71, 皮肤 never
	cn90LoadoutVersion = 4
)

// cn90WornSlotForItemType maps the capability/pvf equipment type to the DNF90
// worn slot (server initialEquipmentSlotMap).
var cn90WornSlotForItemType = map[int]int{
	1:  11, // weapon
	3:  13, // coat
	4:  14, // shoulder
	5:  15, // pants
	6:  16, // shoes
	7:  17, // waist
	8:  18, // amulet
	9:  19, // wrist
	10: 20, // ring
	11: 21, // support
	12: 22, // magic stone
	13: 23, // earring
}

// selectWornEquipment picks one item per configured worn slot for the level,
// job and transfer branch, and maps the capability equipment types onto the
// server's worn slots.
//
// The earring is not a special case here. 耳环 is the last member of the
// special-equipment family, so the generic selector offers it at level 90 like
// any other slot — which is also what lets it join the family's set instead of
// being drawn on its own.
func selectWornEquipment(items []shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, level, job, grow int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	selected := equipmentcap.SelectEquipment(items, level, job, grow, rc, randIntn)
	worn := make(map[int]shared.EquipmentCatalogItem, len(selected))
	for itemType, item := range selected {
		slot, ok := cn90WornSlotForItemType[itemType]
		if !ok || item.ID <= 0 {
			continue
		}
		worn[slot] = item
	}
	return worn
}

// cn90AvatarJob maps the robot job id onto the avatar catalog's job axis.
func cn90AvatarJob(job int) int {
	switch job {
	case 9:
		return 0
	case 10:
		return 3
	default:
		return job
	}
}

// buildCN90WornRawEntry mirrors the server's 43-byte initial-equipment
// summary: slot, item id, create value, durability at +10 and zero tail.
func buildCN90WornRawEntry(slot int, itemID int, durability uint16) []byte {
	raw := make([]byte, 43)
	raw[0] = byte(slot)
	binary.LittleEndian.PutUint32(raw[1:5], uint32(itemID))
	binary.LittleEndian.PutUint32(raw[5:9], cn90InitialEquipmentCreateValue)
	binary.LittleEndian.PutUint16(raw[10:12], durability)
	return raw
}

// equipmentTypeLine reads the `[equipment type]` declaration of one .equ file:
// the bracketed token (for example [weapon] or [hat avatar]) and the native
// trailing type number the server stores in equipment_type.
func equipmentTypeLine(text string) (string, int) {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(trimmed), "[equipment type]") {
			continue
		}
		if index+1 >= len(lines) {
			continue
		}
		value := strings.TrimSpace(lines[index+1])
		token := ""
		if start := strings.IndexByte(value, '['); start >= 0 {
			if end := strings.IndexByte(value[start:], ']'); end > 0 {
				token = value[start : start+end+1]
			}
		}
		native := 0
		if fields := strings.FieldsFunc(value, func(r rune) bool { return r == '`' || r == ' ' || r == '\t' || r == ']' || r == '[' }); len(fields) > 0 {
			if parsed, err := strconv.Atoi(strings.TrimSpace(fields[len(fields)-1])); err == nil {
				native = parsed
			}
		}
		return token, native
	}
	return "", 0
}

// equipmentMetadataFor reads one equipment item's native type and a random
// reinforcement level using the applier's archive connection.
func (a *SQLiteLoadoutApplier) equipmentMetadataFor(item shared.EquipmentCatalogItem) (string, int) {
	archive := a.loadoutArchive()
	if archive == nil || strings.TrimSpace(item.Path) == "" {
		return "", 0
	}
	if cached, ok := a.nativeTypes[item.Path]; ok {
		return cached.token, cached.native
	}
	text, err := archive.ReadText(item.Path)
	if err != nil {
		a.nativeTypes[item.Path] = equipmentTypeInfo{}
		return "", 0
	}
	token, native := equipmentTypeLine(text)
	if a.nativeTypes == nil {
		a.nativeTypes = make(map[string]equipmentTypeInfo)
	}
	a.nativeTypes[item.Path] = equipmentTypeInfo{token: token, native: native}
	return token, native
}

type equipmentTypeInfo struct {
	token  string
	native int
}

func (a *SQLiteLoadoutApplier) loadoutArchive() *cn90PVFArchive {
	if a.archive != nil {
		return a.archive
	}
	if a.archiveTried {
		return nil
	}
	a.archiveTried = true
	if strings.TrimSpace(a.PVFPath) == "" {
		return nil
	}
	archive, err := openCN90PVF(a.PVFPath)
	if err != nil {
		return nil
	}
	a.archive = archive
	a.nativeTypes = make(map[string]equipmentTypeInfo)
	return archive
}

// applyRobotLoadout selects and writes the robot's worn equipment and avatars.
// It runs inside the persistence executor so the full replacement is ordered.
func (a *SQLiteLoadoutApplier) applyRobotLoadout(ctx context.Context, db *sql.DB, characterID string, job, level, grow int) error {
	randIntn := a.RandIntn
	if randIntn == nil {
		randIntn = cryptoRandIntn
	}
	worn := selectWornEquipment(a.Equipment, a.Config, level, job, grow, randIntn)
	avatars := equipmentcap.SelectAvatar(a.Equipment, level, cn90AvatarJob(job), a.Config, randIntn)
	if len(worn) == 0 && len(avatars) == 0 {
		return fmt.Errorf("90CN loadout has no compatible items for level=%d job=%d", level, job)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := a.replaceEquipmentRows(ctx, tx, characterID, worn, avatars, level, job, grow, randIntn); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit 90CN loadout character=%s: %w", characterID, err)
	}
	return nil
}

// replaceEquipmentRows replaces the complete worn set: equipment, avatars and
// their extras. Parents are upserted first, matching the server repository
// write shape.
//
// Every worn item is stamped with its level band's upgrade range, and from the
// band that carries 增幅 (level 71) it is also given the robot's amplification
// attribute and that attribute's initial value. Avatars are never upgraded or
// amplified: they are cosmetic, and the client draws an amplify marker on any
// slot that carries the extras.
func (a *SQLiteLoadoutApplier) replaceEquipmentRows(ctx context.Context, tx *sql.Tx, characterID string, worn, avatars map[int]shared.EquipmentCatalogItem, level, job, grow int, randIntn func(int) int) error {
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+dnfEquipmentsTable+` (character_id, updated_at) VALUES (?, ?) ON CONFLICT(character_id) DO UPDATE SET updated_at=excluded.updated_at`,
		characterID, now); err != nil {
		return fmt.Errorf("upsert 90CN equipment parent character=%s: %w", characterID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+dnfEquipmentExtraTable+` WHERE character_id=?`, characterID); err != nil {
		return fmt.Errorf("clear 90CN equipment extras character=%s: %w", characterID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+dnfEquipmentEntriesTable+` WHERE character_id=?`, characterID); err != nil {
		return fmt.Errorf("clear 90CN equipment entries character=%s: %w", characterID, err)
	}

	insertEntry := func(slot int, itemID int, raw []byte) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+dnfEquipmentEntriesTable+` (character_id, entry_key, slot_index, item_id, bind_flag, expire_at, raw_entry) VALUES (?, ?, ?, ?, 0, NULL, ?)`,
			characterID, strconv.Itoa(slot), slot, itemID, raw); err != nil {
			return fmt.Errorf("insert 90CN equipment character=%s slot=%d: %w", characterID, slot, err)
		}
		return nil
	}
	insertExtra := func(slot int, key, value string) error {
		if value == "" {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+dnfEquipmentExtraTable+` (character_id, entry_key, extra_key, extra_value) VALUES (?, ?, ?, ?)`,
			characterID, strconv.Itoa(slot), key, value); err != nil {
			return fmt.Errorf("insert 90CN equipment extra character=%s slot=%d %s: %w", characterID, slot, key, err)
		}
		return nil
	}

	band := equipmentcap.EquipmentBandFor(level)
	amplifyAttribute := 0
	if band.Amplified {
		amplifyAttribute = equipmentcap.AmplifyAttributeFor(job, grow)
	}
	for slot, item := range worn {
		if item.ID <= 0 {
			continue
		}
		durability := item.Durability
		if durability <= 0 {
			durability = cn90InitialEquipmentDefaultDur
		}
		if durability > 0xffff {
			durability = 0xffff
		}
		reinforce := rollBandUpgrade(band, randIntn)
		if err := insertEntry(slot, item.ID, buildCN90WornRawEntry(slot, item.ID, uint16(durability))); err != nil {
			return err
		}
		token, native := a.equipmentMetadataFor(item)
		_ = token
		for key, value := range map[string]string{
			"source":                   "pvf_create_equipment_list",
			"pvf_path":                 item.Path,
			"current_exe_create_value": strconv.FormatUint(uint64(cn90InitialEquipmentCreateValue), 10),
			"durability":               strconv.Itoa(durability),
			"max_durability":           strconv.Itoa(durability),
			"repair_gold":              "0",
			cn90LoadoutMarkerKey:       strconv.Itoa(cn90LoadoutVersion),
		} {
			if err := insertExtra(slot, key, value); err != nil {
				return err
			}
		}
		if native > 0 {
			if err := insertExtra(slot, "equipment_type", strconv.Itoa(native)); err != nil {
				return err
			}
		}
		if reinforce > 0 {
			if err := insertExtra(slot, "ext_data0", strconv.Itoa(int(reinforce))); err != nil {
				return err
			}
		}
		if value := a.Amplify.InitialValue(amplifyAttribute, band.Rarity); value > 0 {
			if err := insertExtra(slot, "amplify_type", strconv.Itoa(amplifyAttribute)); err != nil {
				return err
			}
			if err := insertExtra(slot, "amplify_value", strconv.Itoa(value)); err != nil {
				return err
			}
		}
	}

	for slot, item := range avatars {
		if item.ID <= 0 {
			continue
		}
		if err := insertEntry(slot, item.ID, buildCN90WornRawEntry(slot, item.ID, 0)); err != nil {
			return err
		}
		token, _ := a.equipmentMetadataFor(item)
		for key, value := range map[string]string{
			"source":                     "robot_avatar",
			"item_kind":                  "avatar",
			"pvf_path":                   item.Path,
			"current_exe_equipment_type": strconv.Itoa(slot),
			"avatar_ability_no":          "0",
			"equipment_type":             token,
			cn90LoadoutMarkerKey:         strconv.Itoa(cn90LoadoutVersion),
		} {
			if err := insertExtra(slot, key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// maxUpgradeLevel is the ceiling the client's packed upgrade byte can hold: the
// level sits in its low five bits (see the server's setUpgradeLevel).
const maxUpgradeLevel = 31

// rollBandUpgrade draws one item's upgrade level from its level band. The band
// states a range rather than a value so a fleet does not read as identical, and
// the range is what the operator asked for: each band carries its own 强化 or
// 增幅 window.
func rollBandUpgrade(band equipmentcap.EquipmentBand, randIntn func(int) int) byte {
	min := band.UpgradeMin
	max := band.UpgradeMax
	if min < 0 {
		min = 0
	}
	if max > maxUpgradeLevel {
		max = maxUpgradeLevel
	}
	if max <= min {
		return byte(clampUpgrade(min))
	}
	if randIntn == nil {
		return byte(clampUpgrade(min))
	}
	return byte(clampUpgrade(min + randIntn(max-min+1)))
}

func clampUpgrade(level int) int {
	if level < 0 {
		return 0
	}
	if level > maxUpgradeLevel {
		return maxUpgradeLevel
	}
	return level
}

// cryptoRandIntn is the fallback source when the scheduler did not provide one.
func cryptoRandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(value.Int64())
}
