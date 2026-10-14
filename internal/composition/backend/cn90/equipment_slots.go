package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
)

// Special equipment slots (特殊装备槽位).
//
// 辅助装备, 魔法石 and 耳环 live in three slots the character has to unlock. The
// durable state is dnf_characters.ex_equip_slot_stat, whose bits are the client
// bitset the server sends in the selected-character body:
//
//	0x01 辅助装备   0x02 魔法石   0x10 耳环
//
// The GM tool's one-click unlock (POST /local/equipment-slots-unlock, the
// server's dnfquest.ApplyEquipmentSlotUnlock) does two things in one
// transaction: it ORs the mask into that column, and it completes the PVF
// qualification chain that would otherwise have to be quested for. The chain,
// read out of the live archive and confirmed against a character the GM tool
// unlocked by hand, is:
//
//	辅助装备  674 n_quest/gent/nightassault/epic_60_nightassault_second_1.qst
//	          649 n_quest/gent/nightassault/epic_60_nightassault_second_2.qst
//	魔法石    675 n_quest/lufthafen/pirateonthetrain/..._magic_stone_1.qst
//	          650 n_quest/lufthafen/pirateonthetrain/..._magic_stone_2.qst
//	耳环     2635 n_quest/new_quest/metrocenter/system_90_earring_01.qst
//	         2636 n_quest/new_quest/metrocenter/system_90_earring_02.qst
//
// The GM operation writes those quests state-only, deliberately without the
// level and material gates the chain would otherwise carry, so completing the
// earring pair for a robot below level 90 is what the operator's own tool does.
// The bit is another matter: 耳环 is worn only at EarringMinLevel, so a robot
// below it is left without the earring bit and the selector never offers one.
const (
	exEquipSlotSupport    = 0x01
	exEquipSlotMagicStone = 0x02
	exEquipSlotEarring    = 0x10

	// exEquipSlotSupportMagicStone is what every robot gets.
	exEquipSlotSupportMagicStone = exEquipSlotSupport | exEquipSlotMagicStone
)

// equipmentSlotUnlockQuests is the qualification chain, in the order the GM
// transaction walks it (prerequisites first).
var equipmentSlotUnlockQuests = []int{649, 674, 650, 675, 2635, 2636}

// mergeEquipmentSlotUnlockQuests folds the slot qualification chain into the
// completed gate set, which is what makes the completions survive: the gate
// seeder replaces a character's whole quest state with the gate set on any
// mismatch, so a chain completed outside the set would be erased on the next
// startup.
func mergeEquipmentSlotUnlockQuests(gates capabilitypvf.QuestGates) capabilitypvf.QuestGates {
	completed := make(map[int]struct{}, len(gates.CompletedQuestIDs)+len(equipmentSlotUnlockQuests))
	for _, questID := range gates.CompletedQuestIDs {
		completed[questID] = struct{}{}
	}
	for _, questID := range equipmentSlotUnlockQuests {
		if _, exists := completed[questID]; exists {
			continue
		}
		completed[questID] = struct{}{}
		gates.CompletedQuestIDs = append(gates.CompletedQuestIDs, questID)
	}
	active := gates.ActiveQuestIDs[:0]
	for _, questID := range gates.ActiveQuestIDs {
		if _, isCompleted := completed[questID]; isCompleted {
			continue
		}
		active = append(active, questID)
	}
	gates.ActiveQuestIDs = active
	return gates
}

// GrantEquipmentSlotUnlocks writes the special-equipment slot bits for the
// fleet. It mirrors the GM one-click's durable effect without the HTTP round
// trip: characters are offline while provisioning runs, and the bits are read
// back from this column when the server builds the selected-character body.
//
// The write is idempotent and only touches rows that still need a bit, so it is
// safe to run on every startup and it repairs a fleet an operator reset.
func GrantEquipmentSlotUnlocks(ctx context.Context, databasePath string, robots []robotcap.Info) (int, error) {
	if strings.TrimSpace(databasePath) == "" || len(robots) == 0 {
		return 0, nil
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return 0, fmt.Errorf("open 90CN equipment slot database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return 0, fmt.Errorf("configure 90CN equipment slot database: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin 90CN equipment slot grant: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	changed := 0
	for index := range robots {
		if robots[index].CID <= 0 {
			continue
		}
		characterID := strconv.Itoa(robots[index].CID)
		// The stored level decides the earring bit, not the catalog copy: a robot
		// whose level moved since startup must not be granted a slot it cannot use,
		// and one that reached 90 must not be denied it. The UPDATE only matches a
		// row still missing a bit, so a second run writes nothing.
		masks := []int{exEquipSlotSupportMagicStone}
		if robots[index].Level >= equipmentcap.EarringMinLevel {
			masks = append(masks, exEquipSlotEarring)
		}
		for _, mask := range masks {
			result, err := tx.ExecContext(ctx,
				`UPDATE `+dnfCharactersTable+`
				    SET ex_equip_slot_stat = ex_equip_slot_stat | ?, updated_at = ?
				  WHERE character_id = ? AND delete_flag = 0 AND (ex_equip_slot_stat & ?) <> ?`,
				mask, now, characterID, mask, mask)
			if err != nil {
				return changed, fmt.Errorf("grant 90CN equipment slots cid=%d: %w", robots[index].CID, err)
			}
			if rows, err := result.RowsAffected(); err == nil && rows > 0 {
				changed += int(rows)
			}
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit 90CN equipment slot grant: %w", err)
	}
	return changed, nil
}
