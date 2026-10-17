package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// partyRunTransport is the party-run half of a backend action transport, driven
// by hand: it records what the runtime asked for and answers the questions the
// runtime asks back.
type partyRunTransport struct {
	status map[int]robotcap.RuntimeStatus

	// accept is how many of the invitations this transport answers. The rest are
	// recorded and ignored, which is what a robot that walked out of the area
	// looks like from the leader's side.
	accept int
	// roster is the leader's party as the server would report it. A nil roster
	// means the server has never sent one.
	roster     []int
	partySince time.Time

	runActive      bool
	runSelfStarted bool
	runSince       time.Time
	runSettled     bool
	enterErr       error
	clearErr       error

	invites     []int
	entered     []uint32
	cleared     []int
	leftRuns    []int
	leftParties []int
}

func (t *partyRunTransport) MoveTown(context.Context, shared.RuntimeMoveCommand) error { return nil }
func (t *partyRunTransport) ShoutLocal(context.Context, shared.RuntimeShoutCommand) error {
	return nil
}

func (t *partyRunTransport) RuntimeStatusMap() map[int]robotcap.RuntimeStatus { return t.status }

func (t *partyRunTransport) InviteParty(uid, target int) error {
	t.invites = append(t.invites, target)
	if len(t.invites) <= t.accept {
		t.roster = append(t.roster, target)
	}
	return nil
}

func (t *partyRunTransport) PartyMembers(int) ([]int, bool) {
	if t.roster == nil {
		return nil, false
	}
	return append([]int(nil), t.roster...), true
}

func (t *partyRunTransport) PartySince(int) (time.Time, bool) {
	if t.partySince.IsZero() {
		return time.Time{}, false
	}
	return t.partySince, true
}

func (t *partyRunTransport) LeaveParty(uid int) error {
	t.leftParties = append(t.leftParties, uid)
	t.roster = nil
	return nil
}

func (t *partyRunTransport) DungeonState(int) (bool, bool, time.Time) {
	return t.runActive, t.runSelfStarted, t.runSince
}

func (t *partyRunTransport) DungeonSettled(int) bool {
	return t.runSettled
}

func (t *partyRunTransport) EnterDungeon(_ int, dungeonID uint32, _ byte) error {
	if t.enterErr != nil {
		return t.enterErr
	}
	t.entered = append(t.entered, dungeonID)
	t.runActive = true
	t.runSelfStarted = true
	t.runSince = time.Now()
	return nil
}

func (t *partyRunTransport) ClearDungeon(uid int) error {
	t.cleared = append(t.cleared, uid)
	return t.clearErr
}

func (t *partyRunTransport) LeaveDungeon(uid int) error {
	t.leftRuns = append(t.leftRuns, uid)
	t.runActive = false
	t.runSelfStarted = false
	t.runSince = time.Time{}
	return nil
}

// partyRunConfig is the INI one party-run test runs on: the feature on, a party
// of three, and an invite window short enough that a test that waits it out
// waits three seconds rather than twelve.
const partyRunConfig = "[auto]\nauto_party_dungeon = true\nauto_party_dungeon_size = 3\n" +
	"auto_party_dungeon_invite_timeout_sec = 3\nauto_party_dungeon_dwell_sec = 60\n" +
	"auto_dungeon_enter_id = 12345\nauto_dungeon_enter_difficulty = 3\n"

// partyRunManager builds a manager whose only robot is uid 7 on village 3 area
// 12, with the two neighbours the tests invite standing on the same map.
func partyRunManager(t *testing.T, transport *partyRunTransport) *RobotRuntime {
	t.Helper()
	return partyRunManagerWithConfig(t, transport, partyRunConfig)
}

func partyRunManagerWithConfig(t *testing.T, transport *partyRunTransport, config string) *RobotRuntime {
	t.Helper()
	if transport.status == nil {
		transport.status = map[int]robotcap.RuntimeStatus{
			7: {UID: 7, CID: 700, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 12},
			8: {UID: 8, CID: 800, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 12},
			9: {UID: 9, CID: 900, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 12},
		}
	}
	manager := testRobotManagerWithConfig(t, config)
	manager.SetBackendActionTransport(transport)
	manager.runtimeState.snapshot = transport.status
	manager.runtimeState.updatedAt = time.Now()
	return NewRobotRuntime(manager)
}

func TestAutoPartyDungeonEnterGathersTheMapAndCommitsOneRun(t *testing.T) {
	transport := &partyRunTransport{accept: 2, roster: []int{700}}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonEnter(7)
	if !result.OK {
		t.Fatalf("party run refused: state=%s message=%s", result.State, result.Message)
	}
	if len(transport.invites) != 2 {
		t.Fatalf("invitations = %v want the two neighbours", transport.invites)
	}
	if transport.invites[0] != 800 || transport.invites[1] != 900 {
		t.Fatalf("invitations = %v want the neighbours in uid order", transport.invites)
	}
	if len(transport.entered) != 1 || transport.entered[0] != 12345 {
		t.Fatalf("dungeon entries = %v want the configured dungeon", transport.entered)
	}
	if !strings.Contains(result.Message, "party=3") {
		t.Fatalf("result message = %q want the gathered size", result.Message)
	}
	// The run and the party are both the exit duty's business, so a formation that
	// succeeded must not have ended either of them.
	if len(transport.leftParties) != 0 || len(transport.leftRuns) != 0 {
		t.Fatalf("formation ended the round itself: parties=%v runs=%v", transport.leftParties, transport.leftRuns)
	}
	// Clearing is its own switch, and this config leaves it off.
	if len(transport.cleared) != 0 {
		t.Fatalf("a switched-off clear ran: %v", transport.cleared)
	}
}

// partyRunClearConfig is the party-run config with the clear switch on: the
// run is entered and then walked to its boss.
const partyRunClearConfig = partyRunConfig + "auto_dungeon_clear = true\n"

func TestAutoPartyDungeonEnterClearsTheRunWhenAsked(t *testing.T) {
	transport := &partyRunTransport{accept: 2, roster: []int{700}}
	runtime := partyRunManagerWithConfig(t, transport, partyRunClearConfig)

	result := runtime.AutoPartyDungeonEnter(7)
	if !result.OK {
		t.Fatalf("clearing party run refused: state=%s message=%s", result.State, result.Message)
	}
	if len(transport.cleared) != 1 || transport.cleared[0] != 7 {
		t.Fatalf("clears = %v want one by the leader", transport.cleared)
	}
	if !strings.Contains(result.Message, "cleared") {
		t.Fatalf("result message = %q want the clear in it", result.Message)
	}
	// The run belongs to the exit duty; the clear must not have ended it.
	if len(transport.leftRuns) != 0 || len(transport.leftParties) != 0 {
		t.Fatalf("clear ended the round itself: runs=%v parties=%v", transport.leftRuns, transport.leftParties)
	}
}

func TestAutoPartyDungeonEnterReportsAClearThatCouldNotFinish(t *testing.T) {
	transport := &partyRunTransport{accept: 2, roster: []int{700}, clearErr: errors.New("no boss room")}
	runtime := partyRunManagerWithConfig(t, transport, partyRunClearConfig)

	result := runtime.AutoPartyDungeonEnter(7)
	if result.State != robotcap.ActionStateFailed {
		t.Fatalf("result = %s/%s want a failure", result.State, result.Message)
	}
	if !strings.Contains(result.Message, "clear failed") {
		t.Fatalf("result message = %q want the clear failure", result.Message)
	}
	// Entering succeeded, so the party is inside a live run the exit duty has to
	// take out; the leader must not tear the party down here.
	if len(transport.leftRuns) != 0 || len(transport.leftParties) != 0 {
		t.Fatalf("a failed clear ended the round itself: runs=%v parties=%v", transport.leftRuns, transport.leftParties)
	}
}

func TestAutoPartyDungeonEnterRefusesASecondRunOnTheSameCharacter(t *testing.T) {
	transport := &partyRunTransport{accept: 2, roster: []int{700}, runActive: true}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonEnter(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "already_in_dungeon" {
		t.Fatalf("result = %s/%s want a cancelled already_in_dungeon", result.State, result.Message)
	}
	if len(transport.invites) != 0 || len(transport.entered) != 0 {
		t.Fatalf("a robot inside a run formed another: invites=%v entered=%v", transport.invites, transport.entered)
	}
}

func TestAutoPartyDungeonEnterEntersWithWhoeverAnswered(t *testing.T) {
	// One of the two answers. Waiting out the whole window for a third member
	// would only cost the leader its schedule, so the party goes with two.
	transport := &partyRunTransport{accept: 1, roster: []int{700}}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonEnter(7)
	if !result.OK {
		t.Fatalf("party run refused: state=%s message=%s", result.State, result.Message)
	}
	if !strings.Contains(result.Message, "party=2") {
		t.Fatalf("result message = %q want the answered size", result.Message)
	}
}

func TestAutoPartyDungeonEnterLeavesThePartyNobodyJoined(t *testing.T) {
	transport := &partyRunTransport{roster: []int{700}}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonEnter(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "party_not_formed" {
		t.Fatalf("result = %s/%s want a cancelled party_not_formed", result.State, result.Message)
	}
	if len(transport.entered) != 0 {
		t.Fatalf("a party of one entered a run: %v", transport.entered)
	}
	if len(transport.leftParties) != 1 {
		t.Fatalf("party leaves = %v want the leader to clear its own party", transport.leftParties)
	}
}

func TestAutoPartyDungeonEnterRefusesAMapWithNobodyToGather(t *testing.T) {
	transport := &partyRunTransport{
		status: map[int]robotcap.RuntimeStatus{
			7: {UID: 7, CID: 700, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 12},
			// Same village, different map: the server forwards an invitation only
			// inside the inviter's own area.
			8: {UID: 8, CID: 800, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 13},
			// Same map, but in a party of its own already.
			9: {UID: 9, CID: 900, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 12, PartyActive: true},
			// Same map, standing at a stall.
			10: {UID: 10, CID: 1000, StateName: robotcap.RuntimeStateRunning, Village: 3, Area: 12, RobotType: 2, StoreDisplayAck: true},
		},
		roster: []int{700},
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonEnter(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "no_candidates" {
		t.Fatalf("result = %s/%s want a cancelled no_candidates", result.State, result.Message)
	}
	if len(transport.invites) != 0 {
		t.Fatalf("invitations = %v want none", transport.invites)
	}
}

func TestAutoPartyDungeonEnterLeavesThePartyWhenTheRunIsRefused(t *testing.T) {
	transport := &partyRunTransport{accept: 2, roster: []int{700}, enterErr: errors.New("entry refused")}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonEnter(7)
	if result.State != robotcap.ActionStateFailed {
		t.Fatalf("result = %s/%s want a failure", result.State, result.Message)
	}
	// The members find their own way out through the exit duty; the leader's job
	// is not to leave them standing in a party it walked away from.
	if len(transport.leftParties) != 1 {
		t.Fatalf("party leaves = %v want the leader to leave the party it could not use", transport.leftParties)
	}
}

func TestAutoPartyDungeonExitHoldsTheRunBeforeLeavingIt(t *testing.T) {
	transport := &partyRunTransport{
		runActive: true,
		runSince:  time.Now(),
		roster:    []int{700, 800},
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "dungeon_hold" {
		t.Fatalf("result = %s/%s want a cancelled dungeon_hold", result.State, result.Message)
	}
	if len(transport.leftRuns) != 0 {
		t.Fatalf("a run inside its hold was left: %v", transport.leftRuns)
	}
}

func TestAutoPartyDungeonExitLeavesTheRunAndThenTheParty(t *testing.T) {
	transport := &partyRunTransport{
		runActive: true,
		runSince:  time.Now().Add(-5 * time.Minute),
		roster:    []int{700, 800},
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if !result.OK {
		t.Fatalf("exit refused: state=%s message=%s", result.State, result.Message)
	}
	if len(transport.leftRuns) != 1 {
		t.Fatalf("dungeon exits = %v want one", transport.leftRuns)
	}
	if len(transport.leftParties) != 1 {
		t.Fatalf("party leaves = %v want the party ended with the run", transport.leftParties)
	}
}

func TestAutoPartyDungeonExitEndsAPartyThatNeverGotAnywhere(t *testing.T) {
	// The formation was refused and nobody ever entered a run. The members are
	// the fleet's own robots, so the fleet is the one that has to end it.
	transport := &partyRunTransport{
		roster:     []int{700, 800, 900},
		partySince: time.Now().Add(-partyFormationGrace - time.Minute),
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if !result.OK {
		t.Fatalf("exit refused: state=%s message=%s", result.State, result.Message)
	}
	if len(transport.leftParties) != 1 {
		t.Fatalf("party leaves = %v want the abandoned party ended", transport.leftParties)
	}
}

func TestAutoPartyDungeonExitWaitsOutAFormation(t *testing.T) {
	transport := &partyRunTransport{
		roster:     []int{700, 800},
		partySince: time.Now(),
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "party_forming" {
		t.Fatalf("result = %s/%s want a cancelled party_forming", result.State, result.Message)
	}
	if len(transport.leftParties) != 0 {
		t.Fatalf("a party still being assembled was left: %v", transport.leftParties)
	}
}

func TestAutoPartyDungeonExitKeepsAPartyAStrangerIsIn(t *testing.T) {
	// Character 5000 is not one of this fleet's robots, so the party belongs to
	// whoever is running it and the fleet has no business ending it.
	transport := &partyRunTransport{
		roster:     []int{700, 5000},
		partySince: time.Now().Add(-partyFormationGrace - time.Minute),
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "party_foreign" {
		t.Fatalf("result = %s/%s want a cancelled party_foreign", result.State, result.Message)
	}
	if len(transport.leftParties) != 0 {
		t.Fatalf("a party holding a stranger was left: %v", transport.leftParties)
	}
}

// TestAutoPartyDungeonExitStaysInARunAStrangerIsIn is the run half of the rule
// above: character 5000 is not one of this fleet's robots, so the run is theirs
// as much as the robot's, and a robot walking out would leave them standing
// inside alone.
func TestAutoPartyDungeonExitStaysInARunAStrangerIsIn(t *testing.T) {
	transport := &partyRunTransport{
		runActive: true,
		runSince:  time.Now().Add(-5 * time.Minute),
		roster:    []int{700, 5000},
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "dungeon_foreign_party" {
		t.Fatalf("result = %s/%s want a cancelled dungeon_foreign_party", result.State, result.Message)
	}
	if len(transport.leftRuns) != 0 {
		t.Fatalf("a run holding a stranger was left: %v", transport.leftRuns)
	}
	if len(transport.leftParties) != 0 {
		t.Fatalf("a party holding a stranger was left: %v", transport.leftParties)
	}
}

// TestAutoPartyDungeonExitFollowsAPlayerOutOfTheParty covers the live flow the
// duty was extended for: robots run a party under a human player, the player
// leaves the party, and the robots must leave with them - out of the run and
// out of the party - instead of waiting out the dwell as if the run were still
// somebody else's. A run that started seconds ago is inside the dwell, so only
// the roster comparison can let the exit through.
func TestAutoPartyDungeonExitFollowsAPlayerOutOfTheParty(t *testing.T) {
	transport := &partyRunTransport{
		runActive: true,
		runSince:  time.Now().Add(-10 * time.Second),
		roster:    []int{700, 800, 5000},
		partySince: time.Now().Add(-10 * time.Second),
	}
	runtime := partyRunManager(t, transport)

	// First tick sees the player in the roster and records them as foreign.
	result := runtime.AutoPartyDungeonExit(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "dungeon_hold" {
		t.Fatalf("first tick = %s/%s want a cancelled dungeon_hold", result.State, result.Message)
	}
	// The player walks out; the server now reports only the fleet's robots.
	transport.roster = []int{700, 800}
	result = runtime.AutoPartyDungeonExit(7)
	if !result.OK {
		t.Fatalf("exit refused: state=%s message=%s", result.State, result.Message)
	}
	if len(transport.leftRuns) != 1 {
		t.Fatalf("run leaves = %v want the run left with the player", transport.leftRuns)
	}
	if len(transport.leftParties) != 1 {
		t.Fatalf("party leaves = %v want the now-fleet party ended", transport.leftParties)
	}
}

// TestAutoPartyDungeonExitLeavesASettledRunButKeepsTheParty is the return-to-town
// half: once the run is settled every member owes only its own exit, so a robot
// leaves the cleared run even while a player is still in the party - and it
// leaves the party alone, because the human is still in it and the robots are
// meant to come back to town as that party. The dwell must not hold a settled
// run either.
func TestAutoPartyDungeonExitLeavesASettledRunButKeepsTheParty(t *testing.T) {
	transport := &partyRunTransport{
		runActive:  true,
		runSettled: true,
		runSince:   time.Now().Add(-10 * time.Second),
		roster:     []int{700, 5000},
		partySince: time.Now().Add(-10 * time.Second),
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	// The run exit was sent; the party half is declined because the human is
	// still in it, and that refusal is what keeps the party intact.
	if result.State != robotcap.ActionStateCancelled || result.Message != "party_foreign" {
		t.Fatalf("result = %s/%s want a cancelled party_foreign after the run exit", result.State, result.Message)
	}
	if len(transport.leftRuns) != 1 {
		t.Fatalf("run leaves = %v want the settled run left", transport.leftRuns)
	}
	if len(transport.leftParties) != 0 {
		t.Fatalf("party leaves = %v want the party kept with the human", transport.leftParties)
	}
}

// TestAutoPartyDungeonExitLeavesARunItsPartyHasAlreadyLeft is the last member's
// case, and it is the one the live fleet hit: the other members leave first, the
// server dissolves the party, and this member's roster becomes a known empty one
// while it is still inside the run. Nobody else is left to protect, so the exit
// has to go through; treating the empty roster as a foreign party left the robot
// inside the dungeon forever (live: char 3 never sent its op42 after the party
// dissolved at 10:40:37).
func TestAutoPartyDungeonExitLeavesARunItsPartyHasAlreadyLeft(t *testing.T) {
	transport := &partyRunTransport{
		runActive: true,
		runSince:  time.Now().Add(-5 * time.Minute),
		roster:    []int{},
	}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if !result.OK {
		t.Fatalf("exit refused: state=%s message=%s", result.State, result.Message)
	}
	if len(transport.leftRuns) != 1 {
		t.Fatalf("dungeon exits = %v want the last member to leave its own run", transport.leftRuns)
	}
	if len(transport.leftParties) != 0 {
		t.Fatalf("party leaves = %v want none: there is no party left", transport.leftParties)
	}
}

func TestAutoPartyDungeonExitDoesNothingOutsideAParty(t *testing.T) {
	transport := &partyRunTransport{roster: []int{700}}
	runtime := partyRunManager(t, transport)

	result := runtime.AutoPartyDungeonExit(7)
	if result.State != robotcap.ActionStateCancelled || result.Message != "no_party" {
		t.Fatalf("result = %s/%s want a cancelled no_party", result.State, result.Message)
	}
}

func TestAutoPartyDungeonExitsAreOffWithoutTheSwitch(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "[auto]\nauto_party_dungeon = false\n")
	transport := &partyRunTransport{
		status: map[int]robotcap.RuntimeStatus{
			7: {UID: 7, CID: 700, StateName: robotcap.RuntimeStateRunning},
		},
		runActive: true,
		runSince:  time.Now().Add(-time.Hour),
	}
	manager.SetBackendActionTransport(transport)
	manager.runtimeState.snapshot = transport.status
	manager.runtimeState.updatedAt = time.Now()
	runtime := NewRobotRuntime(manager)

	if result := runtime.AutoPartyDungeonEnter(7); result.Message != "disabled" {
		t.Fatalf("enter = %s/%s want disabled", result.State, result.Message)
	}
	if result := runtime.AutoPartyDungeonExit(7); result.Message != "disabled" {
		t.Fatalf("exit = %s/%s want disabled", result.State, result.Message)
	}
	if len(transport.leftRuns) != 0 {
		t.Fatalf("a switched-off feature left a run: %v", transport.leftRuns)
	}
}

// TestBackendConfigPolicyNeedsBothHalvesOfAPartyRun is the dedicated half of
// the policy table: a party run is the one switch that needs two capabilities,
// so each of them alone has to clear it. Declaring only the party half would
// leave a fleet able to gather robots it cannot take anywhere; declaring only
// the entry half would leave it forming parties it can never enter with.
func TestBackendConfigPolicyNeedsBothHalvesOfAPartyRun(t *testing.T) {
	halves := []shared.BackendCapability{shared.CapabilityParty, shared.CapabilityDungeonEnter}
	for _, declared := range halves {
		other := halves[0]
		if declared == halves[0] {
			other = halves[1]
		}
		for _, capabilities := range []map[shared.BackendCapability]shared.CapabilityStatus{
			{declared: {Enabled: true}},
			{declared: {Enabled: true}, other: {Enabled: true}},
		} {
			_, bothDeclared := capabilities[other]
			m := testRobotManagerWithConfig(t, "")
			m.ConfigureBackendRuntime(shared.BackendInfo{
				ID:           shared.BackendID("party-run"),
				Capabilities: capabilities,
			}, nil, nil)

			rc := robotconfig.RuntimeConfig{AutoPartyDungeon: true}
			m.applyBackendConfigPolicy(&rc)
			if rc.AutoPartyDungeon != bothDeclared {
				t.Errorf("with %v declared, auto_party_dungeon = %t, want %t",
					capabilities, rc.AutoPartyDungeon, bothDeclared)
			}
		}
	}
}
