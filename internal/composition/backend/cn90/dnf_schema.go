package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// DNF90 native per-character tables. The server creates no ON DELETE cascades,
// so account/character removal must clean every dependent table explicitly.
var dnfCharacterScopedTables = []string{
	"dnf_character_stats",
	"dnf_character_locations",
	"dnf_character_rosters",
	"dnf_character_roster_equipment",
	"dnf_character_roster_lists",
	"dnf_inventory_items",
	"dnf_inventory_item_extra",
	"dnf_inventories",
	"dnf_equipment_entries",
	"dnf_equipment_entry_extra",
	"dnf_equipments",
	"dnf_pet_entries",
	"dnf_pet_entry_extra",
	"dnf_pet_clear_tokens",
	"dnf_pet_artifacts",
	"dnf_pet_artifact_extra",
	"dnf_pets",
	"dnf_quest_states",
	"dnf_quest_state_extra",
	"dnf_quests",
	"dnf_skill_states",
	"dnf_skill_layouts",
	"dnf_skill_cooldowns",
	"dnf_skills",
	"dnf_mails",
	"dnf_mail_metadata",
	"dnf_mail_attachments",
	"dnf_mail_attachment_extra",
	"dnf_mailboxes",
	"dnf_dungeon_permissions",
	// Legacy C# mirrors the robot writes (subtype0/1 tails) keep INTEGER ids.
	"dnf_legacy_character_subtype0_fields",
	"dnf_legacy_character_subtype1_fields",
}

var dnfAccountScopedTables = []string{
	"dnf_account_inventory_items",
	"dnf_account_inventory_item_extra",
	"dnf_account_inventories",
	"dnf_account_metadata",
}

// deleteCharacterRows removes one character and its dependent rows inside the
// caller's transaction. Tables missing from an older database are skipped.
func deleteCharacterRows(ctx context.Context, tx *sql.Tx, characterID string) error {
	characterID = strings.TrimSpace(characterID)
	if characterID == "" {
		return fmt.Errorf("90CN character id is empty")
	}
	existing, err := existingTables(ctx, tx)
	if err != nil {
		return err
	}
	if err := sweepCharacterChildRows(ctx, tx, existing, characterID); err != nil {
		return err
	}
	if existing[dnfCharactersTable] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+dnfCharactersTable+` WHERE character_id=?`, characterID); err != nil {
			return fmt.Errorf("delete 90CN character %s: %w", characterID, err)
		}
	}
	return nil
}

// sweepCharacterChildRows removes every dependent row of one character but
// leaves the character row itself. The game server soft-deletes a character:
// it sets delete_flag and keeps the row, which is all that holds the name, so a
// row that is still flagged deleted must stay while its data rows go. Tables
// missing from an older database are skipped.
func sweepCharacterChildRows(ctx context.Context, tx *sql.Tx, existing map[string]bool, characterID string) error {
	characterID = strings.TrimSpace(characterID)
	if characterID == "" {
		return fmt.Errorf("90CN character id is empty")
	}
	for _, table := range dnfCharacterScopedTables {
		if !existing[table] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE character_id=?`, characterID); err != nil {
			return fmt.Errorf("sweep 90CN character %s from %s: %w", characterID, table, err)
		}
	}
	if existing["dnf_settings"] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM dnf_settings WHERE scope LIKE ?`, "character:"+characterID+":%"); err != nil {
			return fmt.Errorf("sweep 90CN character %s settings: %w", characterID, err)
		}
	}
	if existing["dnf_setting_values"] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM dnf_setting_values WHERE scope LIKE ?`, "character:"+characterID+":%"); err != nil {
			return fmt.Errorf("sweep 90CN character %s setting values: %w", characterID, err)
		}
	}
	return nil
}

// deleteAccountRows removes one account, its characters and account-scoped
// rows inside the caller's transaction.
func deleteAccountRows(ctx context.Context, tx *sql.Tx, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("90CN account id is empty")
	}
	existing, err := existingTables(ctx, tx)
	if err != nil {
		return err
	}
	if existing[dnfCharactersTable] {
		rows, err := tx.QueryContext(ctx, `SELECT character_id FROM `+dnfCharactersTable+` WHERE account_id=?`, accountID)
		if err != nil {
			return fmt.Errorf("list 90CN account %s characters: %w", accountID, err)
		}
		characterIDs := make([]string, 0, 4)
		for rows.Next() {
			var characterID string
			if err := rows.Scan(&characterID); err != nil {
				rows.Close()
				return fmt.Errorf("scan 90CN account %s character: %w", accountID, err)
			}
			characterIDs = append(characterIDs, characterID)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close 90CN account %s character rows: %w", accountID, err)
		}
		for _, characterID := range characterIDs {
			if err := deleteCharacterRows(ctx, tx, characterID); err != nil {
				return err
			}
		}
	}
	for _, table := range dnfAccountScopedTables {
		if !existing[table] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id=?`, accountID); err != nil {
			return fmt.Errorf("delete 90CN account %s from %s: %w", accountID, table, err)
		}
	}
	if existing["dnf_settings"] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM dnf_settings WHERE scope LIKE ?`, "account:"+accountID+":%"); err != nil {
			return fmt.Errorf("delete 90CN account %s settings: %w", accountID, err)
		}
	}
	if existing["dnf_setting_values"] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM dnf_setting_values WHERE scope LIKE ?`, "account:"+accountID+":%"); err != nil {
			return fmt.Errorf("delete 90CN account %s setting values: %w", accountID, err)
		}
	}
	if existing[dnfAccountsTable] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+dnfAccountsTable+` WHERE account_id=?`, accountID); err != nil {
			return fmt.Errorf("delete 90CN account %s: %w", accountID, err)
		}
	}
	return nil
}

func existingTables(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return nil, fmt.Errorf("list 90CN database tables: %w", err)
	}
	defer rows.Close()
	tables := make(map[string]bool, 64)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan 90CN database table: %w", err)
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate 90CN database tables: %w", err)
	}
	return tables, nil
}
