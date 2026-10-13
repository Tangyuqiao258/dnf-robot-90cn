package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

// TestMailScheduleHonorsTheEnableSwitch pins the timetable: a mailbox round runs
// when its timer is due and the feature is on, and never when the operator
// switched mail off, because the backend would still be asked otherwise.
func TestMailScheduleHonorsTheEnableSwitch(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:        true,
			MailEnabled:        true,
			MailIntervalMinSec: 1,
			MailIntervalMaxSec: 1,
		},
		status: robotcap.RuntimeStatus{UID: 7, CID: 9, StateName: robotcap.RuntimeStateRunning},
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	a.setOnlineDesired(true)

	now := time.Now()
	a.nextMail = now.Add(-time.Second)
	a.tick(now)
	if runtime.mails != 1 {
		t.Fatalf("mails = %d after a due timer, want 1", runtime.mails)
	}
	if a.nextMail.IsZero() || !a.nextMail.After(now) {
		t.Fatalf("the timer was not rearmed: %s", a.nextMail)
	}

	runtime.config.MailEnabled = false
	a.nextMail = now.Add(-time.Second)
	a.tick(now.Add(2 * time.Second))
	if runtime.mails != 1 {
		t.Fatalf("mails = %d with the feature off, want no further round", runtime.mails)
	}
}

// TestMailScheduleStaysOutOfTheWayOfAStall pins the interaction with the store
// robots: a stall is showing its stock, so the round must not interrupt it.
func TestMailScheduleStaysOutOfTheWayOfAStall(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:        true,
			MailEnabled:        true,
			MailIntervalMinSec: 1,
			MailIntervalMaxSec: 1,
		},
		status: robotcap.RuntimeStatus{UID: 7, CID: 9, StateName: robotcap.RuntimeStateRunning, RobotType: 2},
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	a.setOnlineDesired(true)
	a.nextMail = time.Now().Add(-time.Second)
	a.tick(time.Now())
	if runtime.mails != 0 {
		t.Fatalf("mails = %d in front of a stall, want 0", runtime.mails)
	}
}

// TestPartyWaitClearsTheMailTimer pins the schedule reset: a robot that joined a
// party stops every automatic action, and the mail timer is part of that
// timetable. A letter waits in the box indefinitely, so pausing the round
// delays a claim without losing it.
func TestPartyWaitClearsTheMailTimer(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:        true,
			MailEnabled:        true,
			MailIntervalMinSec: 1,
			MailIntervalMaxSec: 1,
		},
		status: robotcap.RuntimeStatus{UID: 7, CID: 9, StateName: robotcap.RuntimeStateRunning},
		party:  true,
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	a.setOnlineDesired(true)
	a.nextMail = time.Now().Add(-time.Second)
	a.tick(time.Now())
	if !a.nextMail.IsZero() {
		t.Fatal("entering a party left the mail timer armed")
	}
	if runtime.mails != 0 {
		t.Fatalf("mails = %d next to a party, want 0", runtime.mails)
	}

	// Leaving the party rearms the timer on the next tick.
	runtime.party = false
	a.tick(time.Now().Add(time.Second))
	if a.nextMail.IsZero() {
		t.Fatal("leaving a party did not rearm the mail timer")
	}
}
