package scheduler

import (
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestBackendConfigPolicyClearsEmoteWithoutTheCapability(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	// A backend that has no mood command must not leave the emote switch on,
	// whatever the ini file says.
	m.ConfigureBackendRuntime(shared.BackendInfo{ID: shared.BackendID("no-emote")}, nil, nil)

	rc := robotconfig.RuntimeConfig{AutoEmote: true}
	m.applyBackendConfigPolicy(&rc)
	if rc.AutoEmote {
		t.Fatal("policy left the emote switch on for a backend without the capability")
	}
}

func TestBackendConfigPolicyKeepsEmoteWithTheCapability(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.ConfigureBackendRuntime(shared.BackendInfo{
		ID: shared.BackendID("emote"),
		Capabilities: map[shared.BackendCapability]shared.CapabilityStatus{
			shared.CapabilityEmote: {Enabled: true},
		},
	}, nil, nil)

	rc := robotconfig.RuntimeConfig{AutoEmote: true}
	m.applyBackendConfigPolicy(&rc)
	if !rc.AutoEmote {
		t.Fatal("policy turned off a capability the backend declares")
	}
}
