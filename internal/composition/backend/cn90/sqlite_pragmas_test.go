package cn90

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// The robot is the second writer on the 90CN server's dnf90.db. SQLite's
// connection defaults are journal_mode=delete and synchronous=FULL, so a robot
// commit fsynced a rollback journal unless the write path said otherwise, and
// it held the file's single write lock for that fsync -- on the same file a
// player save needs. This pins the setup the write paths now share: WAL, and
// synchronous=NORMAL to match what the server itself writes with
// (repository/sqlite/db.go). The server's WAL is persistent in the file; the
// robot asserts it anyway so a file the server has not opened yet does not
// silently fall back.
func TestSQLiteWritePragmasSelectWALAndNormalSynchronous(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnf90.db")

	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	configureSQLitePool(plain)
	var defaultJournal string
	if err := plain.QueryRow(`PRAGMA journal_mode;`).Scan(&defaultJournal); err != nil {
		t.Fatal(err)
	}
	var defaultSync int
	if err := plain.QueryRow(`PRAGMA synchronous;`).Scan(&defaultSync); err != nil {
		t.Fatal(err)
	}
	if defaultSync != 2 {
		t.Fatalf("unconfigured connection synchronous=%d, want 2 (FULL); the premise of the write pragmas no longer holds", defaultSync)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if err := applySQLiteWritePragmas(context.Background(), db); err != nil {
		t.Fatalf("apply write pragmas: %v", err)
	}

	var journal string
	if err := db.QueryRow(`PRAGMA journal_mode;`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode=%q want wal", journal)
	}
	var synchronous int
	if err := db.QueryRow(`PRAGMA synchronous;`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous=%d want 1 (NORMAL)", synchronous)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys;`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d want 1", foreignKeys)
	}
	var busyTimeout int
	if err := db.QueryRow(`PRAGMA busy_timeout;`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout=%d want 5000", busyTimeout)
	}
}

// spawn_redistribute rewrites character town/area rows in place and never
// enabled foreign_keys; the load fix must not turn enforcement on underneath
// it. Both properties are asserted together so the two constants cannot drift
// into each other.
func TestSQLiteWritePragmasNoForeignKeysLeavesEnforcementOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnf90.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if err := applySQLiteWritePragmasNoForeignKeys(context.Background(), db); err != nil {
		t.Fatalf("apply write pragmas: %v", err)
	}

	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys;`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 0 {
		t.Fatalf("foreign_keys=%d want 0", foreignKeys)
	}
	var journal string
	if err := db.QueryRow(`PRAGMA journal_mode;`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode=%q want wal", journal)
	}
	var synchronous int
	if err := db.QueryRow(`PRAGMA synchronous;`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous=%d want 1 (NORMAL)", synchronous)
	}
}
