package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
)

// TestZZProbePoolReset deletes every robot account in the configured uid
// segment from the live game database, so the fleet can be provisioned again
// from a different [create] level_plan. A character's level is written once, at
// creation, and the server derives its stats from it, so a fleet that has to
// change level composition has to be rebuilt rather than re-leveled.
//
// It is the same deletion the startup inventory runs on an account it judges
// unusable (deleteAccountRows), applied to healthy accounts on purpose. Stop
// the robot first: its in-memory directory still holds the robots until it
// restarts and re-reads the database.
//
// Set CN90_PROBE_RESET=1 to run it. The account prefix comes from
// CN90_PROBE_RESET_PREFIX (default "robot"); the uid segment and the config
// path come from the robot ini, CN90_PROBE_CHAR_INI overriding the default.
func TestZZProbePoolReset(t *testing.T) {
	if os.Getenv("CN90_PROBE_RESET") != "1" {
		t.Skip("set CN90_PROBE_RESET=1 to delete the live robot accounts")
	}
	root := liveRuntimeRoot(t)
	layout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := layout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSpace(os.Getenv("CN90_PROBE_RESET_PREFIX"))
	if prefix == "" {
		prefix = "robot"
	}
	iniPath := strings.TrimSpace(os.Getenv("CN90_PROBE_CHAR_INI"))
	if iniPath == "" {
		iniPath = `G:\90-CN\robot-run\config\conf\robot_config.ini`
	}
	rc, err := robotconfig.LoadFile(iniPath)
	if err != nil {
		t.Fatalf("load %s: %v", iniPath, err)
	}

	ctx := context.Background()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureSQLitePool(db)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		t.Fatal(err)
	}

	// Collect the accounts first, under the same identity rule the startup
	// inventory uses: exact prefix plus a numeric uid inside the segment.
	rows, err := conn.QueryContext(ctx, `SELECT account_id FROM `+dnfAccountsTable+` WHERE account_id LIKE ? ORDER BY account_id`, prefix+"%")
	if err != nil {
		t.Fatal(err)
	}
	var accounts []string
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if _, ok := robotUIDForAccount(accountID, prefix, rc.RobotUIDStart, rc.RobotUIDEnd); ok {
			accounts = append(accounts, accountID)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(os.TempDir(), "pool_reset.txt")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	say := func(format string, args ...any) { fmt.Fprintf(file, format, args...) }
	say("db=%s\nini=%s\nprefix=%s segment=%d..%d\naccounts=%d\n\n",
		databasePath, iniPath, prefix, rc.RobotUIDStart, rc.RobotUIDEnd, len(accounts))
	if len(accounts) == 0 {
		say("nothing to delete\n")
		t.Logf("no robot accounts in %d..%d; see %s", rc.RobotUIDStart, rc.RobotUIDEnd, out)
		return
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, accountID := range accounts {
		if err := deleteAccountRows(ctx, tx, accountID); err != nil {
			_ = tx.Rollback()
			t.Fatalf("delete %s: %v", accountID, err)
		}
		say("deleted %s\n", accountID)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	say("\ncommitted %d account deletions\n", len(accounts))
	t.Logf("deleted %d robot accounts in %d..%d; see %s", len(accounts), rc.RobotUIDStart, rc.RobotUIDEnd, out)
}
