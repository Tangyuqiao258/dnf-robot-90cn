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

// TestAvailableBackendsExposeCompleteCapabilityMatrix walks the catalog rather
// than a hand-kept list: a capability added to the catalog has to show up in
// every registered backend's status map, since a caller cannot tell a
// capability a backend lacks from one the backend never mentions.
func TestAvailableBackendsExposeCompleteCapabilityMatrix(t *testing.T) {
	want := shared.AllCapabilities()
	if len(want) == 0 {
		t.Fatal("the capability catalog is empty")
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
