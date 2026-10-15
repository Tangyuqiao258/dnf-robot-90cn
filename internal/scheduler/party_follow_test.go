package scheduler

import (
	"context"
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// leaderFollowTransport is a backend action transport that publishes one live
// party leader position and records which uids were asked for it.
type leaderFollowTransport struct {
	target shared.PartyFollowTarget
	ok     bool
	seen   []int
}

func (*leaderFollowTransport) MoveTown(context.Context, shared.RuntimeMoveCommand) error { return nil }

func (*leaderFollowTransport) ShoutLocal(context.Context, shared.RuntimeShoutCommand) error {
	return nil
}

func (t *leaderFollowTransport) PartyFollowTarget(uid int) (shared.PartyFollowTarget, bool) {
	t.seen = append(t.seen, uid)
	return t.target, t.ok
}

func TestCurrentFollowTargetPrefersTheLivePartyLeader(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	transport := &leaderFollowTransport{
		target: shared.PartyFollowTarget{CharacterID: 1001, Village: 3, Area: 4, X: 700, Y: 200, ObservedAt: time.Now()},
		ok:     true,
	}
	m.SetBackendActionTransport(transport)

	target, ok := m.currentFollowTarget(7, m.loadRobotConfig(), nil)
	if !ok {
		t.Fatal("live leader was ignored")
	}
	if target.Village != 3 || target.Area != 4 || target.X != 700 || target.Y != 200 {
		t.Fatalf("target = %+v want village=3 area=4 x=700 y=200", target)
	}
	if len(transport.seen) != 1 || transport.seen[0] != 7 {
		t.Fatalf("leader asked for uids %v want [7]", transport.seen)
	}
}

func TestCurrentFollowTargetFallsBackWithoutALeader(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendActionTransport(&leaderFollowTransport{ok: false})

	// No leader and no configured follow account: the caller gets the old
	// random-roam behaviour rather than an error.
	if _, ok := m.currentFollowTarget(7, m.loadRobotConfig(), nil); ok {
		t.Fatal("follow target appeared without a leader or a follow account")
	}
}

func TestCurrentFollowTargetRejectsAnEmptyLeaderID(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	// ok=true with a zero character id is what a half-initialised tracker would
	// report; it must not become a walk to 0,0.
	m.SetBackendActionTransport(&leaderFollowTransport{ok: true})

	if _, ok := m.currentFollowTarget(7, m.loadRobotConfig(), nil); ok {
		t.Fatal("empty leader id became a follow target")
	}
}

// A pinned robot is the demand layer's placement, and the account-follow
// target is not allowed to overrule it: without this guard the first AutoMove
// tick after login walks every pinned robot to the follow account's village,
// which is how 65 robots pinned to three named villages ended up in the one the
// account sat in (2026-10-16 lab run, `demand_plan ... player=38/1`).
func TestCurrentFollowTargetLeavesAPinnedRobotOnItsPin(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[follow]\nfollow_account = dnf:test-account\n")
	m.SetBackendActionTransport(&leaderFollowTransport{ok: false})
	m.SetBackendFollowAccountLocator(&followAccountTestLocator{village: 38})
	t.Cleanup(func() { _ = m.Shutdown() })

	// Unpinned, the account target is live: the guard below is what removes it,
	// not a lookup that never resolves.
	target, ok := m.currentFollowTarget(7, m.loadRobotConfig(), nil)
	if !ok {
		t.Fatal("follow account produced no target for an unpinned robot")
	}
	if target.Village != 38 {
		t.Fatalf("unpinned target village = %d want 38", target.Village)
	}

	m.setDemandLocations(map[int]demandLocation{
		7: {village: 39, area: 0, x: 1700, y: 155},
	})
	if pinned, ok := m.currentFollowTarget(7, m.loadRobotConfig(), nil); ok {
		t.Fatalf("pinned robot got follow target %+v, want none", pinned)
	}
}

func TestBackendPartyFollowEnabledRequiresCapabilityAndSwitch(t *testing.T) {
	withParty := shared.BackendInfo{Capabilities: map[shared.BackendCapability]shared.CapabilityStatus{
		shared.CapabilityParty: {Enabled: true},
	}}
	withoutParty := shared.BackendInfo{Capabilities: map[shared.BackendCapability]shared.CapabilityStatus{
		shared.CapabilityParty: {Enabled: false},
	}}

	for _, tc := range []struct {
		name string
		info shared.BackendInfo
		rc   robotconfig.RuntimeConfig
		want bool
	}{
		{"switch on and backend supports it", withParty, robotconfig.RuntimeConfig{AutoPartyFollow: true}, true},
		{"switch off", withParty, robotconfig.RuntimeConfig{AutoPartyFollow: false}, false},
		{"backend without the party capability", withoutParty, robotconfig.RuntimeConfig{AutoPartyFollow: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backendPartyFollowEnabled(tc.info, tc.rc); got != tc.want {
				t.Fatalf("backendPartyFollowEnabled = %t want %t", got, tc.want)
			}
		})
	}
}

func TestBackendConfigPolicyClearsPartyFollowWithoutTheCapability(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	// A backend that does not declare the party capability must not leave the
	// accept or the follow switch on, whatever the ini file says.
	m.ConfigureBackendRuntime(shared.BackendInfo{ID: shared.BackendID("no-party")}, nil, nil)

	rc := robotconfig.RuntimeConfig{AutoPartyAccept: true, AutoPartyFollow: true}
	m.applyBackendConfigPolicy(&rc)
	if rc.AutoPartyFollow || rc.AutoPartyAccept {
		t.Fatalf("policy left party switches on: follow=%t accept=%t", rc.AutoPartyFollow, rc.AutoPartyAccept)
	}
}
