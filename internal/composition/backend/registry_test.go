package backend

import (
	"testing"

	"robot/internal/shared"
)

func TestSelectCN90AcrossSupportedPlatforms(t *testing.T) {
	if _, err := Select("missing", "linux"); err == nil {
		t.Fatal("unknown backend must fail closed")
	}
	for _, platform := range []string{"linux", "windows"} {
		if info, err := Select(DefaultID(), platform); err != nil || !info.Selectable {
			t.Fatalf("90CN on %s = %+v, %v", platform, info, err)
		}
	}
}

func TestAvailableBackendsExposeCompleteCapabilityMatrix(t *testing.T) {
	want := []shared.BackendCapability{
		shared.CapabilityProvision,
		shared.CapabilityTownMove,
		shared.CapabilityDungeonMove,
		shared.CapabilityShout,
		shared.CapabilityWorldShout,
		shared.CapabilityStore,
		shared.CapabilityParty,
		shared.CapabilityGuildInvite,
		shared.CapabilityPartyDebug,
		shared.CapabilitySkill,
		shared.CapabilityMarket,
		shared.CapabilityTrade,
		shared.CapabilityCleanup,
		shared.CapabilityMailNotification,
		shared.CapabilityDatabase,
	}
	for _, backend := range Available() {
		if backend.MaxOnline <= 0 {
			t.Fatalf("backend %s omitted max online limit", backend.ID)
		}
		for _, capability := range want {
			if _, ok := backend.Capabilities[capability]; !ok {
				t.Fatalf("backend %s omitted capability %s", backend.ID, capability)
			}
		}
	}
}
