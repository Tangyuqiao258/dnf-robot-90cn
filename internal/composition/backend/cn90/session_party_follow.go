package cn90

import (
	"context"
	"time"

	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// Town party follow. The server broadcasts every town mover's new position as
// class0/0x0016 to the other players sharing its (town, area) key
// (dnfbridge/town_position.go broadcastTownPlayerMove) and announces area
// changes as class0/0x0017. Neither carries a party check, so a follower can
// read its leader's live position straight off the same broadcast the client
// uses for every other player on screen. The follower then walks to a point
// near that leader through the ordinary op35/op36 move path, which the server
// accepts from any session whose town-scene snapshot matches the character.

// partyFollowState is the follower's view of its party leader. The packet
// observer writes it from the drain goroutine while the scheduler reads it
// through PartyFollowTarget, so every field is behind the state's own lock.
type partyFollowState struct {
	mu lockhub.RWLocker
	// candidate is the inviter of a party prompt this session has not answered
	// yet. The server only creates the runtime party on the op11 answer, so the
	// inviter becomes the leader after the matching acknowledgement.
	candidate uint16
	// leader is the accepted inviter. party_online.go makes the op10 sender
	// slot 0 (1P) and the op11 answerer 2P, so the inviter is the leader.
	leader    uint16
	hasLeader bool
	village   int
	area      int
	x         int
	y         int
	// observed is when the leader's last position notice arrived. A zero value
	// means the leader is known but has not moved yet, which is not a position
	// the follower may walk to.
	observed time.Time
}

// startPartyFollow runs for the life of the session. The observer only records
// state; it never writes to the socket, so a slow reader cannot stall the drain
// loop.
func (s *Session) startPartyFollow(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if s.follow == nil {
		s.follow = &partyFollowState{}
	}
	cleanup := s.setPacketObserver(s.observePartyFollow)
	go func() {
		defer cleanup()
		<-ctx.Done()
	}()
}

func (s *Session) observePartyFollow(packet protocol.Packet) {
	state := s.follow
	if state == nil {
		return
	}
	switch {
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiRequestPeer:
		notice, ok := protocol.ParseRequestPeerNotice(packet.Body)
		if !ok {
			return
		}
		// A trade prompt (mode 1) or the town-menu marker (mode 15) never
		// becomes a party, so it must not set a candidate.
		if _, isPartyInvite := protocol.PartyInviteAcceptValue(notice.Mode); !isPartyInvite {
			return
		}
		if state.rememberCandidate(notice.SourceID, s.selfCharacterID) {
			foundationlog.Robotf("CN90_PARTY_FOLLOW_CANDIDATE cid=%d inviter=%d mode=%d\n", s.selfCharacterID, notice.SourceID, notice.Mode)
		}
	case packet.Class == protocol.ClassCommand && packet.Type == protocol.CmdResponsePeer:
		ok, code := protocol.ResponsePeerResult(packet.Body)
		if !ok {
			foundationlog.Robotf("CN90_PARTY_FOLLOW_ACCEPT_REJECTED cid=%d code=%d\n", s.selfCharacterID, code)
			return
		}
		if leader, promoted := state.promoteCandidate(s.selfCharacterID); promoted {
			foundationlog.Robotf("CN90_PARTY_FOLLOW_LEADER cid=%d leader=%d\n", s.selfCharacterID, leader)
		}
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiUserPosition:
		notice, ok := protocol.ParseUserPositionNotice(packet.Body)
		if !ok {
			return
		}
		if state.observePosition(notice.ActorKey, notice.X, notice.Y) {
			foundationlog.Robotf("CN90_PARTY_FOLLOW_POSITION cid=%d leader=%d x=%d y=%d\n", s.selfCharacterID, notice.ActorKey, notice.X, notice.Y)
		}
	case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiUserArea:
		notice, ok := protocol.ParseUserAreaNotice(packet.Body)
		if !ok {
			return
		}
		if state.observeArea(notice.ActorKey, int(notice.Town), int(notice.Area), notice.X, notice.Y) {
			foundationlog.Robotf("CN90_PARTY_FOLLOW_AREA cid=%d leader=%d town=%d area=%d x=%d y=%d\n", s.selfCharacterID, notice.ActorKey, notice.Town, notice.Area, notice.X, notice.Y)
		}
	}
}

// PartyFollowTarget returns the leader's last known town position. It is the
// narrow interface the action transport reads for the scheduler.
func (s *Session) PartyFollowTarget() (shared.PartyFollowTarget, bool) {
	if s == nil || s.follow == nil {
		return shared.PartyFollowTarget{}, false
	}
	return s.follow.target()
}

func (st *partyFollowState) rememberCandidate(inviter, self uint16) bool {
	if inviter == 0 || inviter == self {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	previous := st.candidate
	st.candidate = inviter
	return previous != inviter
}

// promoteCandidate turns the answered inviter into the leader. The op11
// acknowledgement carries no target id, so the candidate the responder just
// answered is the only link back to the inviter; the responder sends one op11
// at a time, which makes that link unambiguous.
func (st *partyFollowState) promoteCandidate(self uint16) (uint16, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.candidate == 0 || st.candidate == self {
		return 0, false
	}
	if st.hasLeader && st.leader == st.candidate {
		return st.leader, false
	}
	// A new leader invalidates the position tracked for the previous one: the
	// follower must not walk to where the old leader used to stand.
	st.leader = st.candidate
	st.hasLeader = true
	st.candidate = 0
	st.village, st.area, st.x, st.y = 0, 0, 0, 0
	st.observed = time.Time{}
	return st.leader, true
}

func (st *partyFollowState) observePosition(actor, x, y uint16) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.hasLeader || actor != st.leader {
		return false
	}
	st.x, st.y = int(x), int(y)
	st.observed = time.Now()
	return true
}

func (st *partyFollowState) observeArea(actor uint16, village, area int, x, y uint16) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.hasLeader || actor != st.leader {
		return false
	}
	st.village, st.area = village, area
	st.x, st.y = int(x), int(y)
	st.observed = time.Now()
	return true
}

func (st *partyFollowState) target() (shared.PartyFollowTarget, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if !st.hasLeader || st.observed.IsZero() {
		return shared.PartyFollowTarget{}, false
	}
	return shared.PartyFollowTarget{
		CharacterID: int(st.leader),
		Village:     st.village,
		Area:        st.area,
		X:           st.x,
		Y:           st.y,
		ObservedAt:  st.observed,
	}, true
}
