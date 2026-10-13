package cn90

import (
	"context"
	"errors"
	"testing"
	"time"

	"robot/internal/shared"
)

type actionTestSession struct {
	town    shared.TownMoveIntent
	shout   shared.ShoutIntent
	moveErr error
	closed  bool
	done    chan struct{}
	party   bool
}

type callbackActionTestSession struct {
	actionTestSession
	callback func()
}

func (s *callbackActionTestSession) setTerminationCallback(callback func()) {
	s.callback = callback
}

type areaActionTestSession struct {
	actionTestSession
	area shared.TownAreaMoveIntent
}

func (s *areaActionTestSession) MoveTownArea(_ context.Context, intent shared.TownAreaMoveIntent) error {
	s.area = intent
	return nil
}

func (s *actionTestSession) MoveTown(_ context.Context, intent shared.TownMoveIntent) error {
	if s.moveErr != nil {
		return s.moveErr
	}
	s.town = intent
	return nil
}
func (s *actionTestSession) Shout(_ context.Context, intent shared.ShoutIntent) error {
	s.shout = intent
	return nil
}
func (s *actionTestSession) Close() error          { s.closed = true; return nil }
func (s *actionTestSession) Done() <-chan struct{} { return s.done }
func (s *actionTestSession) PartyActive() bool     { return s.party }

type actionTestFactory struct{ session shared.RobotSession }

func (f actionTestFactory) OpenSession(context.Context, shared.OpenSessionRequest) (shared.RobotSession, error) {
	return f.session, nil
}

func TestActionTransportMapsVerifiedTownActions(t *testing.T) {
	session := &actionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 7, Village: 2, Area: 4, X: 120, Y: 240, MoveType: 1, Speed: 30}); err != nil {
		t.Fatal(err)
	}
	if session.town.X != 120 || session.town.Y != 240 || session.town.Direction != 1 || session.town.Motion != 30 {
		t.Fatalf("town intent = %+v", session.town)
	}
	if status := transport.RuntimeStatusMap()[7]; status.Village != 2 || status.Area != 4 || status.X != 120 || status.Y != 240 {
		t.Fatalf("runtime status = %+v", status)
	}
	if err := transport.ShoutLocal(context.Background(), shared.RuntimeShoutCommand{UID: 7, Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	if session.shout.Channel != shared.ShoutChannelArea || session.shout.Message != "hello" {
		t.Fatalf("shout intent = %+v", session.shout)
	}
}

func TestActionTransportRoutesVerifiedTownAreaTransition(t *testing.T) {
	session := &areaActionTestSession{}
	transport := NewActionTransport(actionTestFactory{session: session})
	if err := transport.Open(context.Background(), 7, shared.OpenSessionRequest{
		AccountName:      "acct",
		InitialTownKnown: true,
		InitialVillage:   1,
		InitialArea:      2,
		InitialX:         100,
		InitialY:         200,
	}); err != nil {
		t.Fatal(err)
	}
	err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{
		UID: 7, Village: 1, Area: 3, X: 120, Y: 240,
	})
	if err != nil {
		t.Fatalf("cross-area error = %v", err)
	}
	if session.area != (shared.TownAreaMoveIntent{Village: 1, Area: 3, X: 120, Y: 240, SourceVillage: 1}) {
		t.Fatalf("cross-area intent = %+v", session.area)
	}
	if session.town != (shared.TownMoveIntent{}) {
		t.Fatalf("cross-area move also sent position packet: %+v", session.town)
	}
	status := transport.RuntimeStatusMap()[7]
	if status.Village != 1 || status.Area != 3 || status.X != 120 || status.Y != 240 {
		t.Fatalf("cross-area move did not update status: %+v", status)
	}
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{
		UID: 7, Village: 1, Area: 2, X: 120, Y: 240,
	}); err != nil {
		t.Fatalf("same-area move: %v", err)
	}
}

func TestActionTransportRejectsAreaTransitionWithoutBackendPrimitive(t *testing.T) {
	session := &actionTestSession{}
	transport := NewActionTransport(actionTestFactory{session: session})
	if err := transport.Open(context.Background(), 7, shared.OpenSessionRequest{
		AccountName: "acct", InitialTownKnown: true, InitialVillage: 1, InitialArea: 2,
	}); err != nil {
		t.Fatal(err)
	}
	err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 7, Village: 1, Area: 3})
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Operation != shared.CapabilityTownMove {
		t.Fatalf("area primitive error = %v", err)
	}
}

func TestActionTransportRejectsMissingSessionAndPositionOverflow(t *testing.T) {
	transport := NewActionTransport()
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 9}); err == nil {
		t.Fatal("missing session unexpectedly succeeded")
	}
	if err := transport.Attach(9, &actionTestSession{}); err != nil {
		t.Fatal(err)
	}
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 9, X: 100000}); err == nil {
		t.Fatal("overflow position unexpectedly succeeded")
	}
	for _, command := range []shared.RuntimeMoveCommand{
		{UID: 9, MoveType: -1},
		{UID: 9, MoveType: 256},
		{UID: 9, Speed: -1},
		{UID: 9, Speed: 65536},
	} {
		if err := transport.MoveTown(context.Background(), command); err == nil {
			t.Fatalf("out-of-range town command unexpectedly succeeded: %+v", command)
		}
	}
}

func TestActionTransportDoesNotCommitTownStatusWhenSendFails(t *testing.T) {
	session := &actionTestSession{moveErr: errors.New("town move rejected")}
	transport := NewActionTransport()
	if err := transport.Attach(9, session); err != nil {
		t.Fatal(err)
	}
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{
		UID: 9, Village: 3, Area: 4, X: 120, Y: 240,
	}); err == nil || err.Error() != "town move rejected" {
		t.Fatalf("move error = %v", err)
	}
	if status := transport.RuntimeStatusMap()[9]; status.Village != 0 || status.Area != 0 || status.X != 0 || status.Y != 0 {
		t.Fatalf("failed town move committed status = %+v", status)
	}
}

func TestActionTransportOwnsSessionLifecycle(t *testing.T) {
	session := &actionTestSession{}
	transport := NewActionTransport(actionTestFactory{session: session})
	if err := transport.Open(context.Background(), 7, shared.OpenSessionRequest{AccountName: "acct", CharacterSlot: 2}); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.StateName != shared.RuntimeStateRunning {
		t.Fatalf("status after open = %+v", status)
	}
	if err := transport.Close(7); err != nil || !session.closed {
		t.Fatalf("close err=%v closed=%v", err, session.closed)
	}
	if status := transport.RuntimeStatusMap()[7]; status.StateName != shared.RuntimeStateStop {
		t.Fatalf("status after close = %+v", status)
	}
}

func TestActionTransportReportsSessionUptime(t *testing.T) {
	transport := NewActionTransport()
	if err := transport.Attach(7, &actionTestSession{}); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	status := transport.status[7]
	status.RunStartTime = time.Now().Unix() - 5
	transport.status[7] = status
	transport.mu.Unlock()
	status = transport.RuntimeStatusMap()[7]
	if status.RunStartTime == 0 || status.UptimeSeconds < 5 {
		t.Fatalf("runtime status did not report uptime: %+v", status)
	}
}

func TestActionTransportReapsUnexpectedSessionEnd(t *testing.T) {
	session := &actionTestSession{done: make(chan struct{})}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	close(session.done)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if status := transport.RuntimeStatusMap()[7]; status.StateName == shared.RuntimeStateStop {
			if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 7}); err == nil {
				t.Fatal("move unexpectedly succeeded after session end")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session was not reaped: %+v", transport.RuntimeStatusMap())
}

func TestActionTransportUsesAdapterTerminationCallback(t *testing.T) {
	session := &callbackActionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	if session.callback == nil {
		t.Fatal("90CN termination callback was not registered")
	}
	session.callback()
	if status := transport.RuntimeStatusMap()[7]; status.StateName != shared.RuntimeStateStop {
		t.Fatalf("callback did not reap session: %+v", status)
	}
}

func TestActionTransportReconnectStartsWithFreshTownSnapshot(t *testing.T) {
	first := &actionTestSession{done: make(chan struct{})}
	second := &actionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, first); err != nil {
		t.Fatal(err)
	}
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{
		UID: 7, Village: 2, Area: 4, X: 120, Y: 240,
	}); err != nil {
		t.Fatal(err)
	}
	transport.Detach(7)
	if err := transport.Attach(7, second); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.StateName != shared.RuntimeStateRunning || status.Village != 0 || status.Area != 0 || status.X != 0 || status.Y != 0 {
		t.Fatalf("reconnected status = %+v, want fresh running snapshot", status)
	}
	// Simulate the stale session watcher deterministically instead of racing
	// it with a sleep: the identity check must ignore the old session.
	transport.reapSession(7, first)
	if status := transport.RuntimeStatusMap()[7]; status.StateName != shared.RuntimeStateRunning {
		t.Fatalf("stale session watcher changed new status = %+v", status)
	}
	close(first.done)
}

// cidActionTestSession reports a selected character id the way the real 90CN
// session does once it has picked its character.
type cidActionTestSession struct {
	actionTestSession
	cid int
}

func (s *cidActionTestSession) CharacterID() int { return s.cid }

// TestActionTransportPublishesSelectedCharacterID pins the hand-off the auction
// round depends on: the stock plan it lists is a pure function of the character
// id -- the same function the offline seeder used -- and the round refuses to
// run without it. A transport that never publishes the id leaves every auction
// round reporting "needs a selected character".
func TestActionTransportPublishesSelectedCharacterID(t *testing.T) {
	transport := NewActionTransport()
	if err := transport.Attach(7, &cidActionTestSession{cid: 42}); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.CID != 42 {
		t.Fatalf("runtime status CID = %d, want 42", status.CID)
	}

	// A reconnect attaches a fresh session, so the status has to follow it
	// rather than keep the reaped session's id.
	transport.Detach(7)
	if err := transport.Attach(7, &cidActionTestSession{cid: 77}); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.CID != 77 {
		t.Fatalf("reconnected runtime status CID = %d, want 77", status.CID)
	}
}

// TestActionTransportLeavesCIDUnsetWithoutSelectedCharacter covers the other
// half of the contract: a session that cannot name its character must stay at
// zero rather than be reported as one that has.
func TestActionTransportLeavesCIDUnsetWithoutSelectedCharacter(t *testing.T) {
	transport := NewActionTransport()
	if err := transport.Attach(7, &actionTestSession{}); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.CID != 0 {
		t.Fatalf("runtime status CID = %d, want 0", status.CID)
	}
}
