package cn90

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Black Diamond membership.
//
// The game server keeps every premium contract as one account metadata row per
// type: the key is premium_expire_<type> and the value is an absolute unix
// second expiry, not a flag. The server reads it through premium.Active, which
// only asks whether that number is still ahead of now, so granting the
// membership means writing a date.
//
// Type 56 is Black Diamond. The server maps it from the runtime PVF's
// etc/premiumlist_new.etc [type] block, which is also where the shop item
// 900000056 (黑钻会员 30天, 2000 cera) gets its activation from, and the
// current EXE renders the name from dstr.dat 71003.
//
// Buying it in the cera shop is not a path the fleet can take: a robot account
// holds no cera and never opens the shop. The membership is account-scoped
// state in the same database the robot already writes those accounts' gear
// into, so the adapter grants it directly, exactly as it grants equipment.
const (
	blackDiamondPremiumType = 56
	// The key is spelled out rather than formatted so a reader can grep the
	// server's own premium.MetadataKey output against this file.
	blackDiamondExpireMetadataKey = "premium_expire_56"
)

// blackDiamondGrantDays returns the term to grant, or 0 when the stage is off.
// It is the one gate both call sites share: one of them already holds the
// persistence worker and must not queue, so it writes with the connection it
// has instead of going through ensureRobotBlackDiamond.
func (a *SQLiteLoadoutApplier) blackDiamondGrantDays() int {
	if a == nil || !a.Config.BlackDiamondEnabled || a.Config.BlackDiamondDays <= 0 {
		return 0
	}
	return a.Config.BlackDiamondDays
}

// ensureRobotBlackDiamond grants or extends one account's Black Diamond
// membership. Callers must not already be inside a persistence job: the
// executor runs one job at a time and Do waits for its result, so queueing from
// inside a job deadlocks on itself.
//
// The grant is monotonic: an expiry already further out than the configured
// term is left exactly as it is. That matters because the row is shared state.
// An operator who bought a longer membership by hand, or a previous run with a
// longer term, must not have it cut back by a reconcile that only knows the
// current configuration -- and because character deletion never touches account
// metadata, an account whose character was recreated still carries the original
// date, which this check is what preserves.
//
// Writing nothing when the date is already far enough is also what makes the
// call safe to repeat on every startup.
func (a *SQLiteLoadoutApplier) ensureRobotBlackDiamond(ctx context.Context, account string) error {
	days := a.blackDiamondGrantDays()
	if days <= 0 {
		return nil
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return fmt.Errorf("90CN black diamond account is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	return a.persistenceDo(ctx, func(jobCtx context.Context) error {
		db, release, err := a.database(jobCtx)
		if err != nil {
			return fmt.Errorf("open 90CN black diamond database: %w", err)
		}
		defer release()
		return writeBlackDiamondExpiry(jobCtx, db, account, days, time.Now().UTC())
	})
}

// writeBlackDiamondExpiry is the storage half, split out so a test can drive it
// against its own connection without an applier.
//
// A database without the metadata table grants nothing and reports no error.
// The table is part of the 90CN server's own schema, but the adapter also runs
// against the C# mirrors an older profile may hold, and the same reasoning the
// purge path uses applies here: a table that is not there is a feature that
// profile does not have, not a failure of the loadout stage around it.
func writeBlackDiamondExpiry(ctx context.Context, db *sql.DB, account string, days int, now time.Time) error {
	var present int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`,
		dnfAccountMetadataTable).Scan(&present); err != nil {
		return fmt.Errorf("probe 90CN account metadata table: %w", err)
	}
	if present == 0 {
		return nil
	}
	target := now.Add(time.Duration(days) * 24 * time.Hour).Unix()
	var current int64
	err := db.QueryRowContext(ctx,
		`SELECT entry_value FROM `+dnfAccountMetadataTable+` WHERE account_id=? AND entry_key=?`,
		account, blackDiamondExpireMetadataKey).Scan(&current)
	switch {
	case err == nil:
		if current >= target {
			// Already a member past the configured term: a renewal here could
			// only shorten it, so there is nothing to write.
			return nil
		}
	case errors.Is(err, sql.ErrNoRows):
		// No membership yet, or one the server already let lapse.
	default:
		return fmt.Errorf("read 90CN black diamond expiry for %s: %w", account, err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO `+dnfAccountMetadataTable+` (account_id, entry_key, entry_value) VALUES (?, ?, ?)
		 ON CONFLICT(account_id, entry_key) DO UPDATE SET entry_value=excluded.entry_value`,
		account, blackDiamondExpireMetadataKey, strconv.FormatInt(target, 10)); err != nil {
		return fmt.Errorf("write 90CN black diamond expiry for %s: %w", account, err)
	}
	return nil
}
