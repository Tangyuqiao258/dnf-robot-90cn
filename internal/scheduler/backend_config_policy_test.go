package scheduler

import (
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// capabilityBackedSwitch names one automatic switch together with the
// capability it cannot work without. The switch may still be refused later by
// the transport, but a backend that does not declare the capability has no
// route at all, and leaving the switch on would make the dashboard advertise a
// feature the backend cannot serve.
type capabilityBackedSwitch struct {
	capability shared.BackendCapability
	name       string
	read       func(robotconfig.RuntimeConfig) bool
	enable     func(*robotconfig.RuntimeConfig)
}

// capabilityBackedSwitches is the whole mapping. Each per-feature test covers
// one entry in depth; this list is what keeps a switch added later from being
// wired to nothing, which is what happened to the notice switch: it was the one
// capability-backed switch the policy never cleared.
var capabilityBackedSwitches = []capabilityBackedSwitch{
	{shared.CapabilityMailNotification, "auto_mail_notify",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoMailNotify },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoMailNotify = true }},
	{shared.CapabilityMailNotification, "mail_enabled",
		func(rc robotconfig.RuntimeConfig) bool { return rc.MailEnabled },
		func(rc *robotconfig.RuntimeConfig) { rc.MailEnabled = true }},
	{shared.CapabilityParty, "auto_party_accept",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoPartyAccept },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoPartyAccept = true }},
	{shared.CapabilityParty, "auto_party_follow",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoPartyFollow },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoPartyFollow = true }},
	{shared.CapabilitySystemAnnouncement, "auto_system_announcement",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoSystemAnnouncement },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoSystemAnnouncement = true }},
	{shared.CapabilityEmote, "auto_emote",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoEmote },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoEmote = true }},
	{shared.CapabilityDungeonRevive, "auto_dungeon_revive",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoDungeonRevive },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoDungeonRevive = true }},
	{shared.CapabilityShout, "auto_chat_reply",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoChatReply },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoChatReply = true }},
	{shared.CapabilityTrade, "auto_trade",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoTrade },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoTrade = true }},
	{shared.CapabilityServerNotice, "auto_server_notice",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoServerNotice },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoServerNotice = true }},
}

// allCapabilityBackedSwitchesOn is the config an ini file asking for everything
// produces, before the policy has had a look at the backend.
func allCapabilityBackedSwitchesOn() robotconfig.RuntimeConfig {
	var rc robotconfig.RuntimeConfig
	for _, entry := range capabilityBackedSwitches {
		entry.enable(&rc)
	}
	return rc
}

// TestBackendConfigPolicyClearsEverySwitchWithoutItsCapability covers the whole
// mapping at once. A backend that declares nothing must not keep a single one
// of these switches on, whatever the ini file says.
func TestBackendConfigPolicyClearsEverySwitchWithoutItsCapability(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.ConfigureBackendRuntime(shared.BackendInfo{ID: shared.BackendID("no-capabilities")}, nil, nil)

	rc := allCapabilityBackedSwitchesOn()
	m.applyBackendConfigPolicy(&rc)
	for _, entry := range capabilityBackedSwitches {
		if entry.read(rc) {
			t.Errorf("%s stayed on for a backend that does not declare %s", entry.name, entry.capability)
		}
	}
}

// TestBackendConfigPolicyKeepsExactlyTheDeclaredCapabilitySwitches gives the
// backend one capability at a time. Keeping a switch whose capability was not
// declared is the same defect as clearing one that was, and the two are told
// apart here rather than in the aggregate test above.
func TestBackendConfigPolicyKeepsExactlyTheDeclaredCapabilitySwitches(t *testing.T) {
	for _, declared := range capabilityBackedSwitches {
		m := testRobotManagerWithConfig(t, "")
		m.ConfigureBackendRuntime(shared.BackendInfo{
			ID: shared.BackendID("only-" + string(declared.capability)),
			Capabilities: map[shared.BackendCapability]shared.CapabilityStatus{
				declared.capability: {Enabled: true},
			},
		}, nil, nil)

		rc := allCapabilityBackedSwitchesOn()
		m.applyBackendConfigPolicy(&rc)
		for _, entry := range capabilityBackedSwitches {
			want := entry.capability == declared.capability
			if entry.read(rc) != want {
				t.Errorf("with only %s declared, %s = %t, want %t",
					declared.capability, entry.name, entry.read(rc), want)
			}
		}
	}
}
