package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

// TestAuctionScheduleHonorsTheEnableSwitch pins the timetable: an auction round
// runs when its timer is due and the feature is on, and never when the operator
// switched auctions off, because the backend would still be asked otherwise.
func TestAuctionScheduleHonorsTheEnableSwitch(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:           true,
			AuctionEnabled:        true,
			AuctionIntervalMinSec: 1,
			AuctionIntervalMaxSec: 1,
		},
		status: robotcap.RuntimeStatus{UID: 7, CID: 9, StateName: robotcap.RuntimeStateRunning},
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	a.setOnlineDesired(true)

	now := time.Now()
	a.nextAuction = now.Add(-time.Second)
	a.tick(now)
	if runtime.auctions != 1 {
		t.Fatalf("auctions = %d after a due timer, want 1", runtime.auctions)
	}
	if a.nextAuction.IsZero() || !a.nextAuction.After(now) {
		t.Fatalf("the timer was not rearmed: %s", a.nextAuction)
	}

	runtime.config.AuctionEnabled = false
	a.nextAuction = now.Add(-time.Second)
	a.tick(now.Add(2 * time.Second))
	if runtime.auctions != 1 {
		t.Fatalf("auctions = %d with the feature off, want no further round", runtime.auctions)
	}
}

// TestAuctionScheduleStaysOutOfTheWayOfAStall pins the interaction with the
// store robots: a stall is showing its stock, so the round must not interrupt it.
func TestAuctionScheduleStaysOutOfTheWayOfAStall(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:           true,
			AuctionEnabled:        true,
			AuctionIntervalMinSec: 1,
			AuctionIntervalMaxSec: 1,
		},
		status: robotcap.RuntimeStatus{UID: 7, CID: 9, StateName: robotcap.RuntimeStateRunning, RobotType: 2},
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	a.setOnlineDesired(true)
	a.nextAuction = time.Now().Add(-time.Second)
	a.tick(time.Now())
	if runtime.auctions != 0 {
		t.Fatalf("auctions = %d in front of a stall, want 0", runtime.auctions)
	}
}

// TestPartyWaitClearsTheAuctionTimer pins the schedule reset: a robot that
// joined a party stops every automatic action, and the auction timer is part of
// that timetable. The listings already on the board stay there for hours, so
// pausing the round costs the market nothing.
func TestPartyWaitClearsTheAuctionTimer(t *testing.T) {
	runtime := &partyWaitRuntime{
		config: robotconfig.RuntimeConfig{
			AutoActions:           true,
			AuctionEnabled:        true,
			AuctionIntervalMinSec: 1,
			AuctionIntervalMaxSec: 1,
		},
		status: robotcap.RuntimeStatus{UID: 7, CID: 9, StateName: robotcap.RuntimeStateRunning},
		party:  true,
	}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(7)
	a.setOnlineDesired(true)
	a.nextAuction = time.Now().Add(-time.Second)
	a.tick(time.Now())
	if !a.nextAuction.IsZero() {
		t.Fatal("entering a party left the auction timer armed")
	}
	if runtime.auctions != 0 {
		t.Fatalf("auctions = %d next to a party, want 0", runtime.auctions)
	}

	// Leaving the party rearms the timer on the next tick.
	runtime.party = false
	a.tick(time.Now().Add(time.Second))
	if a.nextAuction.IsZero() {
		t.Fatal("leaving a party did not rearm the auction timer")
	}
}
