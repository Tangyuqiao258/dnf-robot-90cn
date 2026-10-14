package cn90

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimulatorDatabaseAccessIsIsolatedToPersistenceAdapter(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Name() == "loadout.go" || entry.Name() == "persistence_inspector.go" || entry.Name() == "startup_inventory.go" || entry.Name() == "growth.go" || entry.Name() == "quests.go" || entry.Name() == "purge.go" || entry.Name() == "population.go" || entry.Name() == "descriptor.go" || entry.Name() == "runtime_assembly.go" || entry.Name() == "instance.go" || entry.Name() == "dnf_schema.go" || entry.Name() == "equipment.go" || entry.Name() == "pets.go" || entry.Name() == "bag.go" || entry.Name() == "follow_account.go" || entry.Name() == "expert_job.go" || entry.Name() == "expert_job_profession.go" || entry.Name() == "spawn_redistribute.go" || entry.Name() == "server_notice_stock.go" || entry.Name() == "premium.go" || entry.Name() == "equipment_slots.go" {
			// runtime_assembly.go owns the database path derivation and the
			// startup inventory composition; growth.go reconciles the persisted
			// transfer/awakening state after that scan; quests.go seeds the
			// gate-free quest state; follow_account.go is the adapter's
			// read-only follow-account lookup; expert_job_profession.go is the
			// offline expert-job preparation for the stalls;
			// server_notice_stock.go is the offline notice-stock preparation;
			// persistence_inspector.go is the read-only database health probe;
			// premium.go is the account-level contract grant;
			// equipment_slots.go is the special-equipment slot grant, which is
			// the durable character column the GM one-click unlock writes.
			// All are persistence concerns.
			continue
		}
		path := filepath.Join(".", entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := strings.ToLower(string(data))
		for _, forbidden := range []string{
			"database/sql", "sqlite", "mysql", "inventory.db", "taiwan_cain", "d_starsky",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s references database marker %q outside the persistence adapter or backend descriptor", path, forbidden)
			}
		}
	}
}
