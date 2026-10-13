package cn90

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	equipmentcap "robot/internal/capability/equipment"
	"robot/internal/foundation/charset"
	"robot/internal/shared"
)

// DNF90 native creature tables and the server's own writer constants.
const (
	dnfInventoriesTable      = "dnf_inventories"
	dnfInventoryItemsTable   = "dnf_inventory_items"
	dnfInventoryExtraTable   = "dnf_inventory_item_extra"
	dnfPetsTable             = "dnf_pets"
	dnfPetEntriesTable       = "dnf_pet_entries"
	dnfPetEntryExtraTable    = "dnf_pet_entry_extra"
	dnfPetClearTokensTable   = "dnf_pet_clear_tokens"
	dnfPetArtifactsTable     = "dnf_pet_artifacts"
	dnfPetArtifactExtraTable = "dnf_pet_artifact_extra"

	cn90InventorySlotCollection = "slots"
	cn90PetInventoryList        = 7
	cn90PetInventoryBodySlot    = 0
	cn90PetSatietyScale         = int64(1_000_000)
	cn90PetInitialSatiety       = 100
	cn90PetInitialLevel         = 1
	// cn90PetWornSourceListType is the equipment list (3): the server validates
	// a worn creature's source as exactly list 3 slot 26.
	cn90PetWornSourceListType = 3
	cn90CreatureWornSlot      = 26
	cn90CreatureSerial        = 1
	// cn90CreatureNameRawLimit is the server's creature-name byte limit: both
	// the pet state loader (pet/owner.go) and the body writer (pet/body.go)
	// reject a name_raw of 30 bytes or more.
	cn90CreatureNameRawLimit = 30
)

// cn90ArtifactKindByItemType maps the capability/pvf artifact item type to the
// semantic kind stored by the server's PetRecord.
var cn90ArtifactKindByItemType = map[int]string{31: "red", 32: "blue", 33: "green"}

// buildCN90CreatureNameRaw encodes a creature name the way the 90CN wire
// expects it: the client's GBK bytes, not the UTF-8 string. The server stores
// name_raw opaquely, sends it verbatim to the client, and only falls back to
// UTF-8 bytes of pet_name when name_raw is NULL -- a fallback rejected at 30
// bytes, which every long Chinese name reaches (ten characters are 30 UTF-8
// bytes but only 20 GBK bytes), leaving the character unable to log in with
// "pet state is invalid: legacy_name_bytes=...". Writing the GBK bytes keeps
// the row loadable and shows the same name in the client. A name whose GBK
// form would still reach the limit is shortened on rune boundaries; an empty
// result leaves the legacy NULL name and an unnamed creature.
func buildCN90CreatureNameRaw(name string) []byte {
	runes := []rune(name)
	for len(runes) > 0 {
		raw, err := charset.EncodeGBKString(string(runes))
		if err == nil && len(raw) < cn90CreatureNameRawLimit {
			return raw
		}
		runes = runes[:len(runes)-1]
	}
	return nil
}

// applyRobotPet replaces the character's creature state with a generated pet
// and optional artifacts. A robot without a compatible creature keeps no pet
// rows; the loadout reconcile re-attempts on the next startup.
func (a *SQLiteLoadoutApplier) applyRobotPet(ctx context.Context, db *sql.DB, characterID string) error {
	randIntn := a.RandIntn
	if randIntn == nil {
		randIntn = cryptoRandIntn
	}
	if !a.Config.PetEnabled {
		return nil
	}
	// The pet probability is derived from the character id so a robot keeps its
	// pet across restarts instead of re-rolling on every reconcile.
	if a.Config.PetProbabilityPercent < 100 && cn90DeterministicPercent(characterID) >= a.Config.PetProbabilityPercent {
		return nil
	}
	pet, artifacts, ok := equipmentcap.SelectPet(a.Equipment, a.Config, randIntn)
	if !ok || pet.ID <= 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := a.replacePetRows(ctx, tx, characterID, pet, artifacts); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit 90CN pet character=%s: %w", characterID, err)
	}
	return nil
}

func (a *SQLiteLoadoutApplier) replacePetRows(ctx context.Context, tx *sql.Tx, characterID string, pet shared.EquipmentCatalogItem, artifacts map[int]shared.EquipmentCatalogItem) error {
	now := time.Now().UTC()
	serial := uint32(cn90CreatureSerial)
	petKey := strconv.FormatUint(uint64(serial), 10)

	// Parent row first; the server schema declares no cascades.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+dnfPetsTable+` (character_id, equipped_key, town_display, updated_at) VALUES (?, ?, 1, ?) ON CONFLICT(character_id) DO UPDATE SET equipped_key=excluded.equipped_key, town_display=excluded.town_display, updated_at=excluded.updated_at`,
		characterID, petKey, now); err != nil {
		return fmt.Errorf("upsert 90CN pet parent character=%s: %w", characterID, err)
	}

	// Replace the complete creature state.
	cleanupStatements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM ` + dnfPetEntryExtraTable + ` WHERE character_id=?`, []any{characterID}},
		{`DELETE FROM ` + dnfPetEntriesTable + ` WHERE character_id=?`, []any{characterID}},
		{`DELETE FROM ` + dnfPetClearTokensTable + ` WHERE character_id=?`, []any{characterID}},
		{`DELETE FROM ` + dnfPetArtifactExtraTable + ` WHERE character_id=?`, []any{characterID}},
		{`DELETE FROM ` + dnfPetArtifactsTable + ` WHERE character_id=?`, []any{characterID}},
		{`DELETE FROM ` + dnfEquipmentExtraTable + ` WHERE character_id=? AND entry_key=?`, []any{characterID, strconv.Itoa(cn90CreatureWornSlot)}},
		{`DELETE FROM ` + dnfEquipmentEntriesTable + ` WHERE character_id=? AND entry_key=?`, []any{characterID, strconv.Itoa(cn90CreatureWornSlot)}},
		{`DELETE FROM ` + dnfInventoryExtraTable + ` WHERE character_id=? AND collection_name=? AND entry_key LIKE '7:%'`, []any{characterID, cn90InventorySlotCollection}},
		{`DELETE FROM ` + dnfInventoryItemsTable + ` WHERE character_id=? AND collection_name=? AND entry_key LIKE '7:%'`, []any{characterID, cn90InventorySlotCollection}},
	}
	for _, statement := range cleanupStatements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("clear 90CN pet state character=%s: %w", characterID, err)
		}
	}

	// Worn creature row: the server's own equip writer repeats the serial at
	// +5 and +24 of a 46-byte entry.
	if err := insertPetEquipmentEntry(ctx, tx, characterID, pet, serial, petKey); err != nil {
		return err
	}
	// The creature entry records the equipment list (3) slot 26 as its source:
	// the server validates exactly this pair for a worn creature, and a worn
	// creature deliberately has no pet inventory row.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+dnfPetEntriesTable+` (character_id, pet_key, creature_key, item_id, source_list_type, source_slot_index, pet_name, name_raw, satiety, satiety_micros, mode_flag, mode1_field_0a, mode1_field_0b, pet_level, pet_exp, tail_flag, raw_entry)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, ?, 0, 0, NULL)`,
		characterID, petKey, serial, pet.ID, cn90PetWornSourceListType, cn90CreatureWornSlot,
		pet.Name, buildCN90CreatureNameRaw(pet.Name), cn90PetInitialSatiety, cn90PetInitialSatiety*cn90PetSatietyScale, cn90PetInitialLevel); err != nil {
		return fmt.Errorf("insert 90CN pet entry character=%s: %w", characterID, err)
	}
	petExtras := map[string]string{
		"creature_serial_or_handle": petKey,
		"creature_key":              petKey,
		"creature_pvf_path":         pet.Path,
		"robot_loadout":             "1",
	}
	if err := insertCN90Extras(ctx, tx, dnfPetEntryExtraTable,
		`INSERT INTO `+dnfPetEntryExtraTable+` (character_id, entry_key, extra_key, extra_value) VALUES (?, ?, ?, ?)`,
		[]any{characterID, petKey}, petExtras); err != nil {
		return err
	}

	// Equipped artifacts live only in the pet record, keyed by semantic kind.
	for _, itemType := range []int{31, 32, 33} {
		item, ok := artifacts[itemType]
		if !ok || item.ID <= 0 {
			continue
		}
		kind, ok := cn90ArtifactKindByItemType[itemType]
		if !ok {
			continue
		}
		raw := buildCN90ItemStackRawEntry(0, item.ID)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+dnfPetArtifactsTable+` (character_id, entry_key, item_id, item_count, bind_flag, expire_at, raw_entry) VALUES (?, ?, ?, 1, 0, NULL, ?)`,
			characterID, kind, item.ID, raw); err != nil {
			return fmt.Errorf("insert 90CN pet artifact character=%s kind=%s: %w", characterID, kind, err)
		}
		artifactExtras := map[string]string{
			"item_kind":     "artifact",
			"artifact_kind": kind,
			"pvf_path":      item.Path,
			"raw_entry_hex": hex.EncodeToString(raw),
			"robot_loadout": "1",
		}
		if err := insertCN90Extras(ctx, tx, dnfPetArtifactExtraTable,
			`INSERT INTO `+dnfPetArtifactExtraTable+` (character_id, entry_key, extra_key, extra_value) VALUES (?, ?, ?, ?)`,
			[]any{characterID, kind}, artifactExtras); err != nil {
			return err
		}
	}
	return nil
}

func insertPetEquipmentEntry(ctx context.Context, tx *sql.Tx, characterID string, pet shared.EquipmentCatalogItem, serial uint32, petKey string) error {
	raw := buildPetCreatureEquipEntry(cn90CreatureWornSlot, pet.ID, serial)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+dnfEquipmentEntriesTable+` (character_id, entry_key, slot_index, item_id, bind_flag, expire_at, raw_entry) VALUES (?, ?, ?, ?, 0, NULL, ?)`,
		characterID, strconv.Itoa(cn90CreatureWornSlot), cn90CreatureWornSlot, pet.ID, raw); err != nil {
		return fmt.Errorf("insert 90CN pet equipment row character=%s: %w", characterID, err)
	}
	extras := map[string]string{
		"source":                    "robot_pet",
		"equipped_slot":             strconv.Itoa(cn90CreatureWornSlot),
		"creature_serial_or_handle": petKey,
		"creature_key":              petKey,
		"creature_pvf_path":         pet.Path,
		"creature_name":             pet.Name,
		"pet_enchant_card_item_id":  "0",
		"enchant_card_id":           "0",
		"enchant_upgrade_count":     "0",
		"value_a":                   "0",
		"byte_12":                   "0",
		"raw_entry_hex":             hex.EncodeToString(raw),
		"robot_loadout":             "1",
	}
	return insertCN90Extras(ctx, tx, dnfEquipmentExtraTable,
		`INSERT INTO `+dnfEquipmentExtraTable+` (character_id, entry_key, extra_key, extra_value) VALUES (?, ?, ?, ?)`,
		[]any{characterID, strconv.Itoa(cn90CreatureWornSlot)}, extras)
}

func insertCN90Extras(ctx context.Context, tx *sql.Tx, table, query string, prefixArgs []any, extras map[string]string) error {
	for key, value := range extras {
		if strings.TrimSpace(value) == "" {
			continue
		}
		args := append(append([]any(nil), prefixArgs...), key, value)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("insert 90CN extra table=%s key=%s: %w", table, key, err)
		}
	}
	return nil
}

// buildCN90ItemStackRawEntry builds the canonical 0x77 inventory row for one
// item stack: slot, item id, create value one and zeroed option fields.
func buildCN90ItemStackRawEntry(slot int, itemID int) []byte {
	raw := make([]byte, 0x77)
	binary.LittleEndian.PutUint16(raw[0x00:0x02], uint16(slot))
	binary.LittleEndian.PutUint32(raw[0x02:0x06], uint32(itemID))
	binary.LittleEndian.PutUint32(raw[0x06:0x0A], 1)
	return raw
}

// buildPetCreatureEquipEntry mirrors the server's own creature equip writer:
// the serial is repeated at +5 (constructor argument) and +24 (the field the
// current client reads back).
func buildPetCreatureEquipEntry(slot int, itemID int, serial uint32) []byte {
	raw := make([]byte, 46)
	raw[0] = byte(slot)
	binary.LittleEndian.PutUint32(raw[1:5], uint32(itemID))
	binary.LittleEndian.PutUint32(raw[5:9], serial)
	binary.LittleEndian.PutUint32(raw[24:28], serial)
	return raw
}

// cn90DeterministicPercent hashes a key into 0..99 so probability decisions are
// stable across restarts for one account or character.
func cn90DeterministicPercent(seed string) int {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(seed))
	return int(hasher.Sum32() % 100)
}
