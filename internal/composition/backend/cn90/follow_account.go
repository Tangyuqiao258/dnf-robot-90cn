package cn90

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"robot/internal/shared"
)

// FollowAccountLocator resolves where a human account is playing from the game
// database, so the scheduler can put robots around it.
//
// The 90CN schema keys characters by `dnf_characters.account_id`, an opaque
// string the server mints ( `robot17001062` for a fleet account, `dnf:<hash>`
// for one that logged in through the client ). It holds no login name:
// `dnf_legacy_accounts` carries the m_id the client types, but nothing joins it
// to a character, so a lookup has to name either the account id or the
// character. Both are accepted, which is what lets an operator write the name
// they see on screen into follow_account.
type FollowAccountLocator struct {
	DatabasePath string
}

// followAccountCharacterSQL selects the character a follow account names. The
// two predicates are an either/or: the configured value is an account id or a
// character name, and the operator should not have to know which the schema
// calls it.
//
// updated_at is read as text on purpose. The column is declared DATETIME(6) but
// every writer here stores Go's own time formatting in it, and the driver hands
// such a value back as a string -- scanning it into a time.Time fails the whole
// query, which is exactly how a position lookup turns into "the player is
// nowhere" without a single error in the log beyond this one.
const followAccountCharacterSQL = `
SELECT c.character_id, c.name, c.level, c.town_id, c.area_id, c.pos_x, c.pos_y, c.updated_at
FROM dnf_characters c
JOIN dnf_accounts a ON a.account_id = c.account_id
WHERE c.delete_flag = 0 AND (a.account_id = ? OR c.name = ?)
ORDER BY c.updated_at DESC, c.character_id DESC
LIMIT 1`

// followAccountSeenLayouts are the timestamp spellings the readers of this
// database have to accept: Go's time formatting (what the robot and the server
// write), then the two common SQL ones for rows another tool left behind.
var followAccountSeenLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
}

// followAccountSeen reads the row's timestamp. It is diagnostics -- the demand
// layer times its own grace window -- so an unreadable value leaves the field
// zero rather than failing the lookup: a player whose position cannot be dated
// is still a player the fleet can stand next to.
func followAccountSeen(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range followAccountSeenLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// FollowAccountVillageLastPlayed returns the town the follow account's most
// recently played character is in.
func (l FollowAccountLocator) FollowAccountVillageLastPlayed(ctx context.Context, account string) (int, bool, error) {
	location, ok, err := l.LocatePlayer(ctx, account)
	if err != nil || !ok {
		return 0, false, err
	}
	return location.Village, location.Available(), nil
}

// LocatePlayer returns where the follow account is playing and at what level.
//
// A follow account that names nothing the database knows is not an error: it is
// a player who has not logged in yet, and the demand layer reads that as "the
// floating layer has nothing to follow" rather than as a failure to retry.
func (l FollowAccountLocator) LocatePlayer(ctx context.Context, account string) (shared.PlayerLocation, bool, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return shared.PlayerLocation{}, false, nil
	}
	if strings.TrimSpace(l.DatabasePath) == "" {
		return shared.PlayerLocation{}, false, fmt.Errorf("90CN follow account database path is required")
	}
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(l.DatabasePath))
	if err != nil {
		return shared.PlayerLocation{}, false, fmt.Errorf("open 90CN follow account database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return shared.PlayerLocation{}, false, fmt.Errorf("configure 90CN follow account database: %w", err)
	}
	var (
		location shared.PlayerLocation
		updated  sql.NullString
	)
	err = db.QueryRowContext(ctx, followAccountCharacterSQL, account, account).Scan(
		&location.CharacterID, &location.Name, &location.Level,
		&location.Village, &location.Area, &location.X, &location.Y, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return shared.PlayerLocation{}, false, nil
	}
	if err != nil {
		return shared.PlayerLocation{}, false, fmt.Errorf("query 90CN follow account %s: %w", account, err)
	}
	location.Seen = followAccountSeen(updated.String)
	return location, true, nil
}
