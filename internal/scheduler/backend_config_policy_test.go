package scheduler

import (
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// capabilityBackedSwitch names one automatic switch together with the
// capabilities it cannot work without. The switch may still be refused later by
// the transport, but a backend that does not declare the capability has no
// route at all, and leaving the switch on would make the dashboard advertise a
// feature the backend cannot serve.
type capabilityBackedSwitch struct {
	// capabilities is a set, not a list of alternatives: a switch naming two is
	// one that needs both, which is what a party run does -- the party transport
	// gathers the members and the entry transport starts the run.
	capabilities []shared.BackendCapability
	name         string
	read         func(robotconfig.RuntimeConfig) bool
	enable       func(*robotconfig.RuntimeConfig)
}

// capabilityBackedSwitches is the whole mapping. Each per-feature test covers
// one entry in depth; this list is what keeps a switch added later from being
// wired to nothing, which is what happened to the notice switch: it was the one
// capability-backed switch the policy never cleared.
var capabilityBackedSwitches = []capabilityBackedSwitch{
	{[]shared.BackendCapability{shared.CapabilityMailNotification}, "auto_mail_notify",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoMailNotify },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoMailNotify = true }},
	{[]shared.BackendCapability{shared.CapabilityMailNotification}, "mail_enabled",
		func(rc robotconfig.RuntimeConfig) bool { return rc.MailEnabled },
		func(rc *robotconfig.RuntimeConfig) { rc.MailEnabled = true }},
	{[]shared.BackendCapability{shared.CapabilityParty}, "auto_party_accept",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoPartyAccept },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoPartyAccept = true }},
	{[]shared.BackendCapability{shared.CapabilityParty}, "auto_party_follow",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoPartyFollow },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoPartyFollow = true }},
	{[]shared.BackendCapability{shared.CapabilityParty, shared.CapabilityDungeonEnter}, "auto_party_dungeon",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoPartyDungeon },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoPartyDungeon = true }},
	{[]shared.BackendCapability{shared.CapabilitySystemAnnouncement}, "auto_system_announcement",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoSystemAnnouncement },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoSystemAnnouncement = true }},
	{[]shared.BackendCapability{shared.CapabilityEmote}, "auto_emote",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoEmote },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoEmote = true }},
	{[]shared.BackendCapability{shared.CapabilityDungeonRevive}, "auto_dungeon_revive",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoDungeonRevive },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoDungeonRevive = true }},
	{[]shared.BackendCapability{shared.CapabilityDungeonEnter}, "auto_dungeon_enter",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoDungeonEnter },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoDungeonEnter = true }},
	{[]shared.BackendCapability{shared.CapabilityShout}, "auto_chat_reply",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoChatReply },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoChatReply = true }},
	{[]shared.BackendCapability{shared.CapabilityTrade}, "auto_trade",
		func(rc robotconfig.RuntimeConfig) bool { return rc.AutoTrade },
		func(rc *robotconfig.RuntimeConfig) { rc.AutoTrade = true }},
	{[]shared.BackendCapability{shared.CapabilityServerNotice}, "auto_server_notice",
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
			t.Errorf("%s stayed on for a backend that does not declare %v", entry.name, entry.capabilities)
		}
	}
}

// TestBackendConfigPolicyKeepsExactlyTheDeclaredCapabilitySwitches gives the
// backend the capabilities one switch needs, one switch at a time. Keeping a
// switch whose capability was not declared is the same defect as clearing one
// that was, and the two are told apart here rather than in the aggregate test
// above. A switch whose own capabilities are a subset of what was declared
// stays on; every other switch has to be off.
func TestBackendConfigPolicyKeepsExactlyTheDeclaredCapabilitySwitches(t *testing.T) {
	for _, declared := range capabilityBackedSwitches {
		enabled := make(map[shared.BackendCapability]shared.CapabilityStatus, len(declared.capabilities))
		for _, capability := range declared.capabilities {
			enabled[capability] = shared.CapabilityStatus{Enabled: true}
		}
		m := testRobotManagerWithConfig(t, "")
		m.ConfigureBackendRuntime(shared.BackendInfo{
			ID:           shared.BackendID("only-" + declared.name),
			Capabilities: enabled,
		}, nil, nil)

		rc := allCapabilityBackedSwitchesOn()
		m.applyBackendConfigPolicy(&rc)
		for _, entry := range capabilityBackedSwitches {
			want := declaresAll(enabled, entry.capabilities)
			if entry.read(rc) != want {
				t.Errorf("with only %v declared, %s = %t, want %t",
					declared.capabilities, entry.name, entry.read(rc), want)
			}
		}
	}
}

// declaresAll reports whether every capability a switch needs is declared.
func declaresAll(enabled map[shared.BackendCapability]shared.CapabilityStatus, needed []shared.BackendCapability) bool {
	for _, capability := range needed {
		if !enabled[capability].Enabled {
			return false
		}
	}
	return true
}
