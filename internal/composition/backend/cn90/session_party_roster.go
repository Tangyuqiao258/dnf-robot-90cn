package cn90

import (
	"context"
	"fmt"
	"time"

	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// Party membership, both directions.
//
// This session is a player as far as the server is concerned, so it can invite
// as well as answer: op10 mode 13 records an invitation server-side and
// forwards the prompt to the target, exactly like the trade request already
// does with its own mode. The answer comes back as the class0/0x0099 roster,
// which the server pushes to every member on every membership change, so one
// observer covers the inviter, the invitee and the disband.

// partyRosterState is the session's view of the party it is in. The packet
// observer writes it from the drain goroutine and the runtime reads it, so the
// membership sits behind the state's own lock.
type partyRosterState struct {
	mu lockhub.RWLocker
	// members are the character ids the server last reported, in slot order. A
	// zero-length roster is a real answer: it is how a disband or a leave is
	// delivered.
	members []int
	known   bool
	// since dates the membership the fleet is waiting on. A party that forms and
	// then never gets anywhere -- the leader's entry was refused, or it never got
	// as far as entering -- is a party with nothing to do, and the only way to
	// tell it apart from one that is still being assembled is how long it has
	// stood. The clock restarts when the party's size changes, because that is
	// the change being waited for: members answering an invitation, or members
	// giving up and leaving. A swap that keeps the size is not something to wait
	// on, so it leaves the date alone.
	since time.Time
}

func (st *partyRosterState) set(members []int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	changed := !st.known || len(st.members) != len(members)
	st.members = append(st.members[:0], members...)
	st.known = true
	if changed {
		st.since = time.Now()
	}
}

func (st *partyRosterState) snapshot() ([]int, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if !st.known {
		return nil, false
	}
	return append([]int(nil), st.members...), true
}

func (st *partyRosterState) appeared() (time.Time, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if !st.known || st.since.IsZero() {
		return time.Time{}, false
	}
	return st.since, true
}

// startPartyRoster installs the observer that keeps the membership current. It
// only records; the invite path is a plain send.
func (s *Session) startPartyRoster(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if s.roster == nil {
		s.roster = &partyRosterState{}
	}
	cleanup := s.setPacketObserver(s.observePartyRoster)
	go func() {
		defer cleanup()
		<-ctx.Done()
	}()
}

func (s *Session) observePartyRoster(packet protocol.Packet) {
	state := s.roster
	if state == nil {
		return
	}
	if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiPartyMemberRealtime {
		return
	}
	members, ok := protocol.ParsePartyRealtimeMembers(packet.Body)
	if !ok {
		return
	}
	ids := make([]int, 0, len(members))
	for _, member := range members {
		ids = append(ids, int(member))
	}
	previous, known := state.snapshot()
	state.set(ids)
	// Membership changes are rare and worth a line each: they are the only
	// record that a party formed, gained or lost a member, or ended.
	if !known || len(previous) != len(ids) {
		foundationlog.Robotf("CN90_PARTY_ROSTER cid=%d members=%v\n", s.selfCharacterID, ids)
	}
}

// PartyMembers returns the character ids of the party this session is in, and
// whether the server has reported a roster at all. A known empty roster means
// the session is in no party.
func (s *Session) PartyMembers() ([]int, bool) {
	if s == nil || s.roster == nil {
		return nil, false
	}
	return s.roster.snapshot()
}

// PartyActive reports whether the session shares a party with someone else.
//
// A roster of one is deliberately not active: the server keeps the party alive
// for its last member, and treating that survivor as a member would park a
// robot that everyone else has already left. The server's own leader rule uses
// the same threshold (partyStateAtRequest members > 1).
func (s *Session) PartyActive() bool {
	members, known := s.PartyMembers()
	return known && len(members) > 1
}

// PartySince reports when the current membership appeared, and whether a roster
// has been seen at all.
func (s *Session) PartySince() (time.Time, bool) {
	if s == nil || s.roster == nil {
		return time.Time{}, false
	}
	return s.roster.appeared()
}

// InviteParty sends the op10 invite for another character. The target has to
// stand in this session's town area: the server forwards the prompt only
// through onlinePlayers.PeerInSameArea and drops it silently otherwise.
func (s *Session) InviteParty(ctx context.Context, targetCharacterID int) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN party invite needs an open session")
	}
	if targetCharacterID <= 0 || targetCharacterID > 0xffff {
		return fmt.Errorf("90CN party invite target %d is out of range", targetCharacterID)
	}
	if targetCharacterID == int(s.selfCharacterID) {
		return fmt.Errorf("90CN party invite target is the inviting character")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.client.RequestPeer(ctx, uint16(targetCharacterID), protocol.PartyInviteMode, 0, 0)
}

// LeaveParty ends this session's own membership. It is the only way out of a
// party that needs no role: the server takes the request from any member and
// answers with the emptied roster this session is already watching, so a caller
// that waits on PartyActive sees the result without reading the acknowledgement.
func (s *Session) LeaveParty(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN party leave needs an open session")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.client.LeaveParty(ctx)
}
