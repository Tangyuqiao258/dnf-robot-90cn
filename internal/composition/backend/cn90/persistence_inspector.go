package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

// PersistenceInspector reports the 90CN SQLite health for the Web runtime
// status.
type PersistenceInspector struct {
	DatabasePath string
	cache        *persistenceStatusCache
}

type persistenceStatusCache struct {
	mu      lockhub.Locker
	status  shared.PersistenceStatus
	expires time.Time
}

const persistenceStatusTTL = 15 * time.Second

func NewPersistenceInspector(databasePath string) PersistenceInspector {
	return PersistenceInspector{DatabasePath: databasePath, cache: &persistenceStatusCache{}}
}

func (i PersistenceInspector) Status(ctx context.Context) shared.PersistenceStatus {
	if i.cache == nil {
		return i.checkStatus(ctx)
	}
	i.cache.mu.Lock()
	defer i.cache.mu.Unlock()
	if time.Now().Before(i.cache.expires) {
		return i.cache.status
	}
	status := i.checkStatus(ctx)
	i.cache.status = status
	i.cache.expires = time.Now().Add(persistenceStatusTTL)
	return status
}

func (i PersistenceInspector) checkStatus(ctx context.Context) shared.PersistenceStatus {
	started := time.Now()
	status := shared.PersistenceStatus{
		Engine: "sqlite", Target: filepath.Clean(strings.TrimSpace(i.DatabasePath)), CheckedAt: started,
	}
	fail := func(err error) shared.PersistenceStatus {
		status.Error = err.Error()
		status.LatencyMS = time.Since(started).Milliseconds()
		return status
	}
	if strings.TrimSpace(i.DatabasePath) == "" {
		return fail(fmt.Errorf("90CN database path is empty"))
	}
	file, err := os.OpenFile(i.DatabasePath, os.O_RDWR, 0)
	if err != nil {
		return fail(fmt.Errorf("open 90CN database read-write: %w", err))
	}
	if err := file.Close(); err != nil {
		return fail(fmt.Errorf("close 90CN database probe: %w", err))
	}
	status.Writable = true
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(i.DatabasePath))
	if err != nil {
		return fail(fmt.Errorf("open 90CN database: %w", err))
	}
	configureSQLitePool(db)
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return fail(fmt.Errorf("configure 90CN database inspection: %w", err))
	}
	if err := db.PingContext(ctx); err != nil {
		return fail(fmt.Errorf("ping 90CN database: %w", err))
	}
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		return fail(fmt.Errorf("query 90CN database: value=%d err=%v", one, err))
	}
	status.SelectVerified = true
	if err := validateLoadoutSchema(ctx, db); err != nil {
		return fail(err)
	}
	status.OK = true
	status.LatencyMS = time.Since(started).Milliseconds()
	return status
}

func sqliteReadOnlyDSN(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}).String()
}
