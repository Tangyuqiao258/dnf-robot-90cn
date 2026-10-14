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
		shared.CapabilityStore, shared.CapabilityParty, shared.CapabilityMarket,
		shared.CapabilityMailNotification, shared.CapabilityEmote,
		shared.CapabilityDungeonRevive, shared.CapabilityDungeonEnter, shared.CapabilityShout,
		shared.CapabilityTrade,
	} {
		if !info.Supports(capability) {
			t.Fatalf("verified capability %s is disabled: %+v", capability, info.Capabilities[capability])
		}
	}
	if info.Capabilities[shared.CapabilityDatabase].Mode != "sqlite_health" {
		t.Fatalf("database mode = %+v", info.Capabilities[shared.CapabilityDatabase])
	}
	// Chat is enabled but point-to-point. Without the mode a caller holding a
	// target-less shout intent would have no way to know it cannot be served.
	if mode := info.Capabilities[shared.CapabilityShout].Mode; mode != shared.ShoutModeDirected {
		t.Fatalf("shout mode = %q want %q", mode, shared.ShoutModeDirected)
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityDungeonFollow, shared.CapabilityGuildInvite,
	} {
		if info.Supports(capability) || info.Capabilities[capability].Reason == "" {
			t.Fatalf("%s must remain disabled with reason: %+v", capability, info.Capabilities[capability])
		}
	}
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityDungeonMove, shared.CapabilityWorldShout,
		shared.CapabilityPartyDebug,
		shared.CapabilitySkill,
		shared.CapabilityDiagnostics,
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
