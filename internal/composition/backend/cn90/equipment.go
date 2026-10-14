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

// selectWornEquipment picks one item per configured worn slot for the level and
// job. The generic selector covers item types 1..12; earrings (13) are picked
// with the same level/rarity/job rules.
func selectWornEquipment(items []shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, level, job int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	selected := equipmentcap.SelectEquipment(items, level, job, rc, randIntn)
	worn := make(map[int]shared.EquipmentCatalogItem, len(selected)+1)
	for itemType, item := range selected {
		slot, ok := cn90WornSlotForItemType[itemType]
		if !ok || item.ID <= 0 {
			continue
		}
		worn[slot] = item
	}
	if equipmentTypeConfigured(rc.EquipSlots, 13) {
		if item, ok := selectEarring(items, rc, level, job, randIntn); ok {
			worn[23] = item
		}
	}
	return worn
}

func equipmentTypeConfigured(slots []int, wanted int) bool {
	if len(slots) == 0 {
		return true
	}
	for _, slot := range slots {
		if slot == wanted {
			return true
		}
	}
	return false
}

func selectEarring(items []shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, level, job int, randIntn func(int) int) (shared.EquipmentCatalogItem, bool) {
	best := -1
	candidates := make([]shared.EquipmentCatalogItem, 0, 8)
	for _, item := range items {
		if item.ItemType != 13 || item.ID <= 0 || item.Expire || item.BadName || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) {
			continue
		}
		if item.Level > level || !equipmentcap.UsableByJob(item.UseJob, job) {
			continue
		}
		if rc.EquipRarityMax > 0 && (item.Rarity < rc.EquipRarityMin || item.Rarity > rc.EquipRarityMax) {
			continue
		}
		if item.Level > best {
			best = item.Level
			candidates = candidates[:0]
		}
		if item.Level == best {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		return shared.EquipmentCatalogItem{}, false
	}
	index := 0
	if randIntn != nil && len(candidates) > 1 {
		index = randIntn(len(candidates))
	}
	return candidates[index], true
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
func (a *SQLiteLoadoutApplier) applyRobotLoadout(ctx context.Context, db *sql.DB, characterID string, job, level int) error {
	randIntn := a.RandIntn
	if randIntn == nil {
		randIntn = cryptoRandIntn
	}
	worn := selectWornEquipment(a.Equipment, a.Config, level, job, randIntn)
	avatars := equipmentcap.SelectAvatar(a.Equipment, cn90AvatarJob(job), a.Config, randIntn)
	if len(worn) == 0 && len(avatars) == 0 {
		return fmt.Errorf("90CN loadout has no compatible items for level=%d job=%d", level, job)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := a.replaceEquipmentRows(ctx, tx, characterID, worn, avatars, randIntn); err != nil {
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
func (a *SQLiteLoadoutApplier) replaceEquipmentRows(ctx context.Context, tx *sql.Tx, characterID string, worn, avatars map[int]shared.EquipmentCatalogItem, randIntn func(int) int) error {
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
		reinforce := byte(0)
		if a.Config.EquipIntensifyMax > 0 && slot == 11 {
			min := a.Config.EquipIntensifyMin
			max := a.Config.EquipIntensifyMax
			if min < 0 {
				min = 0
			}
			if max > 31 {
				max = 31
			}
			if max > min {
				reinforce = byte(min + randIntn(max-min+1))
			} else {
				reinforce = byte(min)
			}
		}
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
			"robot_loadout":            "1",
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
			"robot_loadout":              "1",
		} {
			if err := insertExtra(slot, key, value); err != nil {
				return err
			}
		}
	}
	return nil
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
