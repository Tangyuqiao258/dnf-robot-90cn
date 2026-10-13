package cn90

import (
	"testing"

	"robot/internal/shared"
)

func TestMetadataReflectsVerifiedCapabilities(t *testing.T) {
	info := Info()
	if info.ID != BackendID || !info.Selectable {
		t.Fatalf("90CN metadata = %+v", info)
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityProvision, shared.CapabilityTownMove,
		shared.CapabilityCleanup, shared.CapabilityDangerousDelete, shared.CapabilityDatabase,
		shared.CapabilityStore, shared.CapabilityParty,
	} {
		if !info.Supports(capability) {
			t.Fatalf("verified capability %s is disabled: %+v", capability, info.Capabilities[capability])
		}
	}
	if info.Capabilities[shared.CapabilityDatabase].Mode != "sqlite_health" {
		t.Fatalf("database mode = %+v", info.Capabilities[shared.CapabilityDatabase])
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityDungeonFollow, shared.CapabilityGuildInvite,
		shared.CapabilityShout,
	} {
		if info.Supports(capability) || info.Capabilities[capability].Reason == "" {
			t.Fatalf("%s must remain disabled with reason: %+v", capability, info.Capabilities[capability])
		}
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityDungeonMove, shared.CapabilityWorldShout,
		shared.CapabilityPartyDebug,
		shared.CapabilitySkill, shared.CapabilityMarket,
		shared.CapabilityDiagnostics,
		shared.CapabilityMailNotification,
		shared.CapabilitySystemAnnouncement, shared.CapabilityServiceControl,
	} {
		if info.Supports(capability) || info.Capabilities[capability].Reason == "" {
			t.Fatalf("%s must remain disabled with reason: %+v", capability, info.Capabilities[capability])
		}
	}
}

func TestDescriptorOwnsLocalizedSettingMetadata(t *testing.T) {
	settings := Info().Settings
	if len(settings) == 0 {
		t.Fatal("90CN settings are missing")
	}
	for _, setting := range settings {
		if setting.Key == "" || setting.Label == "" || setting.LabelZH == "" {
			t.Fatalf("setting metadata is incomplete: %+v", setting)
		}
		if setting.Hint == "" || setting.HintZH == "" {
			t.Fatalf("setting hint is incomplete: %+v", setting)
		}
	}
}
