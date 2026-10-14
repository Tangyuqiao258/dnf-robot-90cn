package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

// emoteRuntime records the emotion indexes the actor actually dispatched, so a
// test can tell a fired round from one that only looked due.
type emoteRuntime struct {
	*partyWaitRuntime
	indexes []uint16
}

func (r *emoteRuntime) AutoEmote(uid int, emotionIndex uint16) robotcap.ActionResult {
	r.indexes = append(r.indexes, emotionIndex)
	return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateSent}
}

func (r *emoteRuntime) AutoDungeonEnter(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid}
}

func newEmoteRuntime(rc robotconfig.RuntimeConfig) *emoteRuntime {
	return &emoteRuntime{partyWaitRuntime: &partyWaitRuntime{
		config: rc,
		status: robotcap.RuntimeStatus{UID: 7, StateName: robotcap.RuntimeStateRunning},
	}}
}

func emoteTestConfig() robotconfig.RuntimeConfig {
	return robotconfig.RuntimeConfig{
		AutoActions:             true,
		AutoEmote:               true,
		AutoEmoteIntervalMinSec: 1,
		AutoEmoteIntervalMaxSec: 1,
		AutoEmoteIndexMin:       3,
		AutoEmoteIndexMax:       5,
	}
}

func TestEmoteRoundDrawsAnIndexInsideTheConfiguredRange(t *testing.T) {
	runtime := newEmoteRuntime(emoteTestConfig())
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)

	now := time.Now()
	a.nextEmote = now.Add(-time.Second)
	a.tick(now)

	if len(runtime.indexes) != 1 {
		t.Fatalf("emote round dispatched %d times want 1", len(runtime.indexes))
	}
	if got := runtime.indexes[0]; got < 3 || got > 5 {
		t.Fatalf("emotion index %d is outside the configured 3..5", got)
	}
	if !a.nextEmote.After(now) {
		t.Fatal("the emote round did not reschedule itself")
	}
}

func TestEmoteRoundStaysOffWhenTheSwitchIsOff(t *testing.T) {
	rc := emoteTestConfig()
	rc.AutoEmote = false
	runtime := newEmoteRuntime(rc)
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)

	now := time.Now()
	overdue := now.Add(-time.Second)
	a.nextEmote = overdue
	a.tick(now)

	if len(runtime.indexes) != 0 {
		t.Fatalf("switched-off emote round still dispatched %v", runtime.indexes)
	}
	// A switched-off timer is left alone rather than rescheduled, so turning it
	// back on does not wait out an interval that was never consumed.
	if !a.nextEmote.Equal(overdue) {
		t.Fatalf("switched-off emote round moved its timer to %s", a.nextEmote)
	}
}

func TestPartyActiveActorDoesNotEmote(t *testing.T) {
	runtime := newEmoteRuntime(emoteTestConfig())
	runtime.party = true
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)

	now := time.Now()
	a.nextEmote = now.Add(-time.Second)
	a.tick(now)

	if len(runtime.indexes) != 0 {
		t.Fatalf("party actor emoted: %v", runtime.indexes)
	}
	if !a.nextEmote.IsZero() {
		t.Fatal("party wait did not clear the emote schedule")
	}
	res := a.handleCommand(CommandEmote)
	if res.OK || res.State != robotcap.ActionStateCancelled || res.Message != "party active" {
		t.Fatalf("party emote command result = %+v", res)
	}
	if len(runtime.indexes) != 0 {
		t.Fatalf("party emote command dispatched: %v", runtime.indexes)
	}
}

func TestEmoteCommandDrawsAnIndexAndDispatchesIt(t *testing.T) {
	rc := emoteTestConfig()
	rc.AutoEmoteIndexMin, rc.AutoEmoteIndexMax = 2, 2
	runtime := newEmoteRuntime(rc)
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)

	res := a.handleCommand(CommandEmote)
	if !res.OK {
		t.Fatalf("emote command result = %+v", res)
	}
	if len(runtime.indexes) != 1 || runtime.indexes[0] != 2 {
		t.Fatalf("emote command dispatched %v want one index of 2", runtime.indexes)
	}
}

func TestEmoteIndexRangeIsClampedAtOne(t *testing.T) {
	// Zero is the neutral mood the scene tail hands the client when a character
	// has never chosen one, so the rotation must never drift back into it.
	rc := robotconfig.RuntimeConfig{AutoEmoteIndexMin: 0, AutoEmoteIndexMax: -4}
	robotconfig.Normalize(&rc)
	if rc.AutoEmoteIndexMin != 1 || rc.AutoEmoteIndexMax != 1 {
		t.Fatalf("clamped range = %d..%d want 1..1", rc.AutoEmoteIndexMin, rc.AutoEmoteIndexMax)
	}
}
