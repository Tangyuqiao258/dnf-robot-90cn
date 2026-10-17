package scheduler

import (
	"context"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
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

// followMoveTransport is a party-run transport that also publishes one leader
// position and records every town move the move service dispatches, so the
// follow round can be driven end to end without a live server.
type followMoveTransport struct {
	*partyRunTransport
	target    shared.PartyFollowTarget
	hasTarget bool
	moves     []shared.RuntimeMoveCommand
}

func (t *followMoveTransport) PartyFollowTarget(int) (shared.PartyFollowTarget, bool) {
	return t.target, t.hasTarget
}

func (t *followMoveTransport) MoveTown(_ context.Context, cmd shared.RuntimeMoveCommand) error {
	t.moves = append(t.moves, cmd)
	return nil
}

// followMoveManager builds a runtime whose only robot stands in a live party on
// a map with movement geometry, so AutoMove takes the follow branch instead of
// the login-room escape.
func followMoveManager(t *testing.T, transport *followMoveTransport) *RobotRuntime {
	t.Helper()
	if transport.status == nil {
		transport.status = map[int]robotcap.RuntimeStatus{
			7: {UID: 7, CID: 700, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 12, PartyActive: true},
		}
	}
	manager := testRobotManagerWithConfig(t, "[move]\nmove_steps = 2\nmove_step_delay_ms = 0\n")
	manager.SetBackendActionTransport(transport)
	manager.SetTownMapCatalog([]shared.MapCatalogItem{{
		Village: 3, Area: 12, Use: true,
		Rectangles: []shared.MapRectangle{{XMin: 600, XMax: 800, YMin: 200, YMax: 300}},
	}})
	manager.runtimeState.snapshot = transport.status
	manager.runtimeState.updatedAt = time.Now()
	return NewRobotRuntime(manager)
}

// TestAutoMoveWalksAPartyMemberToItsLeader is the regression for the
// 2026-10-17 report that a human's invited dummies never walked with him. The
// actor's follow duty calls AutoMove for a party member, and this is what
// AutoMove must do when the session has a leader position: walk to a point
// inside the follow radius around that leader. Before the follow duty existed,
// the party guard silenced this move round entirely, so the tracked leader
// position was never walked to.
func TestAutoMoveWalksAPartyMemberToItsLeader(t *testing.T) {
	transport := &followMoveTransport{
		partyRunTransport: &partyRunTransport{},
		target:            shared.PartyFollowTarget{CharacterID: 800, Village: 3, Area: 12, X: 700, Y: 220, ObservedAt: time.Now()},
		hasTarget:         true,
	}
	runtime := followMoveManager(t, transport)

	result := runtime.AutoMove(7)
	if !result.OK || result.State != robotcap.ActionStateMoved {
		t.Fatalf("party follow move = %s/%s", result.State, result.Message)
	}
	if len(transport.moves) == 0 {
		t.Fatal("party follow dispatched no town move")
	}
	last := transport.moves[len(transport.moves)-1]
	if last.Village != 3 || last.Area != 12 {
		t.Fatalf("move destination = %d/%d want the leader's map 3/12", last.Village, last.Area)
	}
	if last.X < 580 || last.X > 820 || last.Y < 190 || last.Y > 250 {
		t.Fatalf("move destination = %d,%d outside the follow radius around 700,220", last.X, last.Y)
	}
}

// A party member with no leader position must stand still rather than fall
// through to the solo wander: the party owns its member's movement, and a
// follower that wanders on its own is not following anybody.
func TestAutoMoveLeavesAPartyMemberWithoutALeaderStandingStill(t *testing.T) {
	transport := &followMoveTransport{partyRunTransport: &partyRunTransport{}}
	runtime := followMoveManager(t, transport)

	result := runtime.AutoMove(7)
	if result.OK || result.State != robotcap.ActionStateOffline {
		t.Fatalf("party member without a leader = %s/%s", result.State, result.Message)
	}
	if len(transport.moves) != 0 {
		t.Fatalf("party member without a leader moved: %v", transport.moves)
	}
}

// A character inside a run has no town move to make. The follow duty now calls
// AutoMove for every party member every few seconds, and a party run holds its
// members for the whole dwell, so without this guard every follower would spend
// the run asking the server for town moves from a dungeon.
func TestAutoMoveRefusesInsideADungeonRun(t *testing.T) {
	transport := &followMoveTransport{
		partyRunTransport: &partyRunTransport{runActive: true},
		target:            shared.PartyFollowTarget{CharacterID: 800, Village: 3, Area: 12, X: 700, Y: 220, ObservedAt: time.Now()},
		hasTarget:         true,
	}
	runtime := followMoveManager(t, transport)

	result := runtime.AutoMove(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "in_dungeon" {
		t.Fatalf("move inside a run = %s/%s want a cancelled in_dungeon", result.State, result.Message)
	}
	if len(transport.moves) != 0 {
		t.Fatalf("move inside a run dispatched %d town moves", len(transport.moves))
	}
}
