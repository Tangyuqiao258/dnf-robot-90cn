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
//
// The 0x0016 fan-out is why a position notice also answers the question the
// packet itself does not ask: who else is standing here. OnlinePlayerManager
// .UpdatePosition collects the mover's peers from byArea[key] alone, and the
// two other senders of 0x0016 (initial_town_location_notifications.go, the
// op36 transition reply) address only the mover's own session with the mover's
// own actor key. So a 0x0016 naming the leader is proof that the leader stands
// in this session's town area, and it is the only cheap proof there is: a
// leader that never crosses an area boundary never triggers the 0x0017 that
// carries the town and area explicitly, which left every follower of a
// stationary-area leader without an area to walk to and therefore standing
// still.

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
		// Op11 answers a party invite and a trade prompt alike, and both succeed
		// with the same leading byte, so the two are told apart by the trailing
		// byte the server only appends for a trade. Without this a trade accept
		// would promote a party candidate that was never answered.
		if _, isTradeAck := protocol.ParseTradeResponseAck(packet.Body); isTradeAck {
			return
		}
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
		village, area, standing := s.ownTownArea()
		tracked, located := state.observePosition(notice.ActorKey, notice.X, notice.Y, village, area, standing)
		if tracked {
			foundationlog.Robotf("CN90_PARTY_FOLLOW_POSITION cid=%d leader=%d x=%d y=%d\n", s.selfCharacterID, notice.ActorKey, notice.X, notice.Y)
		}
		if located {
			foundationlog.Robotf("CN90_PARTY_FOLLOW_COLOCATED cid=%d leader=%d town=%d area=%d\n", s.selfCharacterID, notice.ActorKey, village, area)
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

// ownTownArea reports the town area this session last had confirmed by the
// server. It is the area a leader-position notice implicitly places the leader
// in, and it is deliberately read from the transition answers rather than from
// the scheduler's own idea of where the robot stands: the two agree in steady
// state, and the one the server answered is the one the broadcast was scoped to.
func (s *Session) ownTownArea() (int, int, bool) {
	if s == nil {
		return 0, 0, false
	}
	observation, valid := s.LastTownAreaObservation()
	if !valid {
		return 0, 0, false
	}
	return observation.village, observation.area, true
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

// observePosition records one leader position notice and reports, first,
// whether the notice belonged to the tracked leader and, second, whether it
// also taught the state where the leader is standing.
//
// The second answer comes from the session's own confirmed town area, which is
// sound because the server only broadcasts a mover's 0x0016 to the players
// registered in the mover's area: reading the notice at all means sharing it.
// A session that has not had an area transition answered yet (the login room,
// before the first escape) has nothing to stamp and leaves the area unknown,
// which is the same standing the follower was in before this existed.
func (st *partyFollowState) observePosition(actor, x, y uint16, village, area int, standing bool) (bool, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.hasLeader || actor != st.leader {
		return false, false
	}
	st.x, st.y = int(x), int(y)
	st.observed = time.Now()
	located := standing && (st.village != village || st.area != area)
	if located {
		st.village, st.area = village, area
	}
	return true, located
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
