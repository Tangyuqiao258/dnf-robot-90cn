package cn90

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"robot/internal/foundation/charset"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

// ActionTransport adapts already-open 90CN sessions to scheduler actions.
// Session creation and lifecycle remain outside this adapter.
type ActionTransport struct {
	mu            lockhub.RWLocker
	factory       shared.SessionFactory
	sessions      map[int]shared.RobotSession
	status        map[int]shared.RuntimeStatus
	locationKnown map[int]bool
	// dungeonSelfStarted records the sessions whose current run this transport
	// asked for. A party member is pulled into the leader's run by the server
	// and never enters one itself, so this is what tells the exit duty which
	// side of a party run a robot is on.
	dungeonSelfStarted map[int]bool
	// stackables is the stockable catalog shared with the offline stage. The
	// assembly writes it once before the runtime starts and only reads it
	// afterwards, so it carries no lock of its own.
	stackables []shared.EquipmentCatalogItem
}

func NewActionTransport(factory ...shared.SessionFactory) *ActionTransport {
	var sessionFactory shared.SessionFactory
	if len(factory) > 0 {
		sessionFactory = factory[0]
	}
	return &ActionTransport{factory: sessionFactory, sessions: make(map[int]shared.RobotSession), status: make(map[int]shared.RuntimeStatus), locationKnown: make(map[int]bool)}
}

func (t *ActionTransport) Open(ctx context.Context, uid int, request shared.OpenSessionRequest) error {
	if t == nil || t.factory == nil {
		return fmt.Errorf("90CN session factory is not configured")
	}
	session, err := t.factory.OpenSession(ctx, request)
	if err != nil {
		return err
	}
	if err := t.Attach(uid, session); err != nil {
		_ = session.Close()
		return err
	}
	t.seedLoginLocation(uid, session, request)
	return nil
}

// seedLoginLocation records where the character stands after a login.
//
// The requested InitialTown* values are a wish: the server re-opens its own
// login location for every character-list login and honours the assigned town
// only as a second step it may refuse. Seeding the wish made the location map
// claim a town the character was not in, which sent a same-town move as a bare
// position report for a scene the character was absent from and named the wrong
// source town in every later cross-town portal request. The session's own
// observation is the answer; the request stays the fallback for a session that
// observed nothing, so this never reports less than the transport knew before.
func (t *ActionTransport) seedLoginLocation(uid int, session shared.RobotSession, request shared.OpenSessionRequest) {
	if !request.InitialTownKnown {
		return
	}
	village, area, x, y := request.InitialVillage, request.InitialArea, request.InitialX, request.InitialY
	landing, observed := loginLanding{}, false
	if reporter, ok := session.(interface {
		LoginLanding() (loginLanding, bool)
	}); ok {
		landing, observed = reporter.LoginLanding()
	}
	if observed {
		village, area, x, y = landing.village, landing.area, landing.x, landing.y
		if village != request.InitialVillage || area != request.InitialArea {
			foundationlog.Robotf("[CN90_LOCATION_SEED_OBSERVED] uid=%d requested=%d/%d/%d,%d landed=%d/%d/%d,%d\n",
				uid, request.InitialVillage, request.InitialArea, request.InitialX, request.InitialY, village, area, x, y)
		}
	} else {
		foundationlog.Robotf("[CN90_LOCATION_SEED_UNOBSERVED] uid=%d requested=%d/%d/%d,%d\n",
			uid, request.InitialVillage, request.InitialArea, request.InitialX, request.InitialY)
	}
	t.mu.Lock()
	status := t.status[uid]
	status.Village = village
	status.Area = area
	status.X = x
	status.Y = y
	t.status[uid] = status
	t.locationKnown[uid] = true
	t.mu.Unlock()
}

func (t *ActionTransport) Attach(uid int, session shared.RobotSession) error {
	if t == nil || uid <= 0 || session == nil {
		return fmt.Errorf("90CN action transport requires uid and session")
	}
	t.mu.Lock()
	if _, exists := t.sessions[uid]; exists {
		t.mu.Unlock()
		return fmt.Errorf("90CN session already attached for uid %d", uid)
	}
	t.sessions[uid] = session
	status := shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateRunning, State: 3, RunStartTime: time.Now().Unix()}
	// A session that reaches the transport has already selected its character,
	// so the id can be published here. Nothing else in the transport writes
	// CID, and the auction round refuses to run without it.
	if identified, ok := session.(interface{ CharacterID() int }); ok {
		status.CID = identified.CharacterID()
	}
	t.status[uid] = status
	delete(t.locationKnown, uid)
	t.mu.Unlock()
	if lifecycle, ok := session.(interface{ setTerminationCallback(func()) }); ok {
		lifecycle.setTerminationCallback(func() { t.reapSession(uid, session) })
		return nil
	}
	if lifecycle, ok := session.(interface{ Done() <-chan struct{} }); ok {
		done := lifecycle.Done()
		if done != nil {
			go t.watchSession(uid, session, done)
		}
	}
	return nil
}

func (t *ActionTransport) watchSession(uid int, session shared.RobotSession, done <-chan struct{}) {
	<-done
	t.reapSession(uid, session)
}

func (t *ActionTransport) reapSession(uid int, session shared.RobotSession) {
	t.mu.Lock()
	if t.sessions[uid] == session {
		delete(t.sessions, uid)
		t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateStop}
		delete(t.locationKnown, uid)
		delete(t.dungeonSelfStarted, uid)
	}
	t.mu.Unlock()
}

func (t *ActionTransport) Detach(uid int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.sessions, uid)
	t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateStop}
	delete(t.locationKnown, uid)
	delete(t.dungeonSelfStarted, uid)
	t.mu.Unlock()
}

func (t *ActionTransport) Close(uid int) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	session := t.sessions[uid]
	delete(t.sessions, uid)
	t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateStop}
	delete(t.locationKnown, uid)
	t.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close()
}

func (t *ActionTransport) CloseAll() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	sessions := make([]shared.RobotSession, 0, len(t.sessions))
	for uid, session := range t.sessions {
		delete(t.sessions, uid)
		t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateStop}
		delete(t.locationKnown, uid)
		sessions = append(sessions, session)
	}
	t.mu.Unlock()
	var closeErr error
	for _, session := range sessions {
		if err := session.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func (t *ActionTransport) RuntimeStatusMap() map[int]shared.RuntimeStatus {
	if t == nil {
		return map[int]shared.RuntimeStatus{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[int]shared.RuntimeStatus, len(t.status))
	for uid, status := range t.status {
		if status.StateName == shared.RuntimeStateRunning && status.RunStartTime > 0 {
			status.UptimeSeconds = int(time.Now().Unix() - status.RunStartTime)
			if status.UptimeSeconds < 0 {
				status.UptimeSeconds = 0
			}
		}
		// The party rule every scheduled action reads is only as good as this
		// field, and nothing else writes it for this adapter.
		if session := t.sessions[uid]; session != nil {
			if provider, ok := session.(interface{ PartyActive() bool }); ok {
				status.PartyActive = provider.PartyActive()
			}
		}
		out[uid] = status
	}
	return out
}

func (t *ActionTransport) session(uid int) (shared.RobotSession, error) {
	if t == nil {
		return nil, fmt.Errorf("90CN action transport is nil")
	}
	t.mu.RLock()
	session := t.sessions[uid]
	t.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("90CN session is not attached for uid %d", uid)
	}
	return session, nil
}

// PartyFollowTarget reports the attached session's live party leader position.
// A session opened without EnablePartyFollow has no tracker and reports false,
// which leaves the scheduler on whatever follow policy it had before.
func (t *ActionTransport) PartyFollowTarget(uid int) (shared.PartyFollowTarget, bool) {
	session, err := t.session(uid)
	if err != nil {
		return shared.PartyFollowTarget{}, false
	}
	provider, ok := session.(interface {
		PartyFollowTarget() (shared.PartyFollowTarget, bool)
	})
	if !ok {
		return shared.PartyFollowTarget{}, false
	}
	return provider.PartyFollowTarget()
}

// emoteActionTimeout bounds one mood change. The command is a single request
// answered from the same write path the other opcodes use, so the window only
// has to cover a round trip; the scene refresh the server appends afterwards is
// not waited for.
const emoteActionTimeout = 10 * time.Second

// ChangeEmotion sets one session's mood/emotion index and reports the index the
// server stored. A session that is not on the 90CN adapter has no such method,
// so the scheduler can offer every backend the same schedule.
func (t *ActionTransport) ChangeEmotion(uid int, emotionIndex uint16) (uint16, error) {
	if t == nil {
		return 0, fmt.Errorf("90CN emotion change needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return 0, err
	}
	emoter, ok := session.(interface {
		ChangeEmotion(context.Context, uint16) (uint16, error)
	})
	if !ok {
		return 0, fmt.Errorf("90CN session does not expose the emotion protocol")
	}
	ctx, cancel := context.WithTimeout(context.Background(), emoteActionTimeout)
	defer cancel()
	return emoter.ChangeEmotion(ctx, emotionIndex)
}

// dungeonActionTimeout is the ceiling for one whole dungeon round trip: the
// entry budget the session applies itself (two acknowledgements plus the scene
// load) with room for the exit leg after it. The session shortens its own
// windows inside this, so the ceiling only has to be the outer bound.
const dungeonActionTimeout = 60 * time.Second

// partyInviteTimeout bounds one op10 party invitation. The op10 acknowledgement
// is written on the same path as an ordinary command, so the window only has to
// cover a round trip; the answer that matters arrives later, as a roster
// notice, and is not waited for here.
const partyInviteTimeout = 10 * time.Second

// EnterDungeon walks one session through a dungeon entry and waits until the
// scene has loaded. It returns once the character is standing in the run, not
// when the server has merely committed it.
func (t *ActionTransport) EnterDungeon(uid int, dungeonID uint32, difficulty byte) error {
	if t == nil {
		return fmt.Errorf("90CN dungeon entry needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return err
	}
	entrant, ok := session.(interface {
		EnterDungeon(context.Context, uint32, byte) error
	})
	if !ok {
		return fmt.Errorf("90CN session does not expose the dungeon entry protocol")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dungeonActionTimeout)
	defer cancel()
	if err := entrant.EnterDungeon(ctx, dungeonID, difficulty); err != nil {
		return err
	}
	t.markDungeonOrigin(uid, true)
	return nil
}

// dungeonClearActionTimeout is the ceiling for one whole clear. It is larger
// than the entry budget because a clear walks the maze room by room, and each
// refused move costs its own room wait.
const dungeonClearActionTimeout = 6 * time.Minute

// ClearDungeon walks and clears the run the session's party is inside, as the
// run's leader. It returns once the settlement has been opened and answered, or
// with the reason the walk could not finish; the run is left as it stands.
func (t *ActionTransport) ClearDungeon(uid int) error {
	if t == nil {
		return fmt.Errorf("90CN dungeon clear needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return err
	}
	clearer, ok := session.(interface {
		ClearDungeon(context.Context) error
	})
	if !ok {
		return fmt.Errorf("90CN session does not expose the dungeon clear protocol")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dungeonClearActionTimeout)
	defer cancel()
	return clearer.ClearDungeon(ctx)
}

// markDungeonOrigin records whether the run a session is inside was started by
// this transport. A robot can end up in a dungeon without ever asking for one:
// the server drags every party member into the leader's run. The exit duty
// belongs to whoever started it, so the follower side has to be able to tell
// the two apart.
func (t *ActionTransport) markDungeonOrigin(uid int, selfStarted bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dungeonSelfStarted == nil {
		t.dungeonSelfStarted = make(map[int]bool)
	}
	if selfStarted {
		t.dungeonSelfStarted[uid] = true
		return
	}
	delete(t.dungeonSelfStarted, uid)
}

// DungeonState reports whether the session has an active run, whether this
// transport was the one that asked for it, and when the run started.
func (t *ActionTransport) DungeonState(uid int) (active bool, selfStarted bool, since time.Time) {
	session, err := t.session(uid)
	if err != nil {
		return false, false, time.Time{}
	}
	reporter, ok := session.(interface {
		InDungeon() bool
		DungeonSince() time.Time
	})
	if !ok || !reporter.InDungeon() {
		return false, false, time.Time{}
	}
	t.mu.RLock()
	selfStarted = t.dungeonSelfStarted[uid]
	t.mu.RUnlock()
	return true, selfStarted, reporter.DungeonSince()
}

// DungeonSettled reports whether the session's active run has already produced
// its settlement entry, i.e. the run is cleared and only the member's own exit
// is left.
func (t *ActionTransport) DungeonSettled(uid int) bool {
	session, err := t.session(uid)
	if err != nil {
		return false
	}
	provider, ok := session.(interface{ DungeonSettled() bool })
	if !ok {
		return false
	}
	return provider.DungeonSettled()
}

// PartySince reports when the session's current party appeared, and whether the
// server has reported a roster at all.
func (t *ActionTransport) PartySince(uid int) (time.Time, bool) {
	session, err := t.session(uid)
	if err != nil {
		return time.Time{}, false
	}
	provider, ok := session.(interface{ PartySince() (time.Time, bool) })
	if !ok {
		return time.Time{}, false
	}
	return provider.PartySince()
}

// PartyActive reports whether the session shares a party with another
// character. The scheduler's party rule is built on it: a party owns its
// member's actions, so the answer has to come from the live roster rather than
// from a flag nothing writes.
func (t *ActionTransport) PartyActive(uid int) bool {
	members, known := t.PartyMembers(uid)
	return known && len(members) > 1
}

// PartyMembers returns the character ids of the party the session is in and
// whether the server has reported a roster at all.
func (t *ActionTransport) PartyMembers(uid int) ([]int, bool) {
	session, err := t.session(uid)
	if err != nil {
		return nil, false
	}
	provider, ok := session.(interface{ PartyMembers() ([]int, bool) })
	if !ok {
		return nil, false
	}
	return provider.PartyMembers()
}

// InviteParty sends an op10 party invitation from one session to another
// character. The server forwards it only to a target standing in the inviter's
// town area and drops it silently otherwise, so a caller that wants an answer
// has to pick the target from the same map.
func (t *ActionTransport) InviteParty(uid int, targetCharacterID int) error {
	if t == nil {
		return fmt.Errorf("90CN party invite needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return err
	}
	inviter, ok := session.(interface {
		InviteParty(context.Context, int) error
	})
	if !ok {
		return fmt.Errorf("90CN session does not expose the party invite protocol")
	}
	ctx, cancel := context.WithTimeout(context.Background(), partyInviteTimeout)
	defer cancel()
	return inviter.InviteParty(ctx, targetCharacterID)
}

// LeaveParty ends one session's own party membership. It is the counterpart of
// InviteParty and the reason a formed party is not permanent: the server takes
// the leave from any member, so a fleet that gathers itself can also let itself
// go without a leader having to disband anything.
func (t *ActionTransport) LeaveParty(uid int) error {
	if t == nil {
		return fmt.Errorf("90CN party leave needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return err
	}
	leaver, ok := session.(interface{ LeaveParty(context.Context) error })
	if !ok {
		return fmt.Errorf("90CN session does not expose the party leave protocol")
	}
	ctx, cancel := context.WithTimeout(context.Background(), partyInviteTimeout)
	defer cancel()
	return leaver.LeaveParty(ctx)
}

// LeaveDungeon abandons the run a session is inside and waits until the
// character is back in town. The exit is part of the entry contract rather than
// a separate feature: nothing in this adapter drives a character inside a run,
// so a robot left in one would sit there while the rest of its schedule assumes
// it is in town.
func (t *ActionTransport) LeaveDungeon(uid int) error {
	if t == nil {
		return fmt.Errorf("90CN dungeon exit needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return err
	}
	leaver, ok := session.(interface {
		LeaveDungeon(context.Context) error
	})
	if !ok {
		return fmt.Errorf("90CN session does not expose the dungeon exit protocol")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dungeonActionTimeout)
	defer cancel()
	if err := leaver.LeaveDungeon(ctx); err != nil {
		return err
	}
	t.markDungeonOrigin(uid, false)
	return nil
}

func (t *ActionTransport) MoveTown(ctx context.Context, command shared.RuntimeMoveCommand) error {
	session, err := t.session(command.UID)
	if err != nil {
		return err
	}
	if command.X < math.MinInt16 || command.X > math.MaxInt16 || command.Y < math.MinInt16 || command.Y > math.MaxInt16 {
		return fmt.Errorf("90CN town position out of range: %d,%d", command.X, command.Y)
	}
	if command.MoveType < 0 || command.MoveType > math.MaxUint8 {
		return fmt.Errorf("90CN town move type out of range: %d", command.MoveType)
	}
	if command.Speed < 0 || command.Speed > math.MaxUint16 {
		return fmt.Errorf("90CN town move speed out of range: %d", command.Speed)
	}
	t.mu.RLock()
	known := t.locationKnown[command.UID]
	status := t.status[command.UID]
	t.mu.RUnlock()
	if known && (status.Village != command.Village || status.Area != command.Area) {
		areaMover, ok := session.(interface {
			MoveTownArea(context.Context, shared.TownAreaMoveIntent) error
		})
		if !ok {
			return shared.UnsupportedCapabilityError{Backend: BackendID, Operation: shared.CapabilityTownMove, Reason: "90CN area transition protocol is not available on this session"}
		}
		if err := areaMover.MoveTownArea(ctx, shared.TownAreaMoveIntent{Village: command.Village, Area: command.Area, X: int16(command.X), Y: int16(command.Y), SourceVillage: status.Village}); err != nil {
			// The transition failed, but the server may still have announced a
			// landing — a room answers a request to leave it with the room's own
			// exit target. That announcement is where the character really
			// stands, and keeping the old belief would make every later move
			// originate from a map it already left.
			t.adoptObservedTownArea(command.UID, session)
			return err
		}
	}
	if !known || status.Village == command.Village && status.Area == command.Area {
		if err := session.MoveTown(ctx, shared.TownMoveIntent{X: int16(command.X), Y: int16(command.Y), Direction: byte(command.MoveType), Motion: uint16(command.Speed)}); err != nil {
			return err
		}
	}
	t.mu.Lock()
	updated := t.status[command.UID]
	updated.UID = command.UID
	updated.Village = command.Village
	updated.Area = command.Area
	updated.X = command.X
	updated.Y = command.Y
	t.status[command.UID] = updated
	t.locationKnown[command.UID] = true
	t.mu.Unlock()
	return nil
}

// adoptObservedTownArea rewrites one robot's believed location from the last
// area notification its session saw. The server is the authority: when it
// answers a transition with a map other than the requested one (the Seria lobby
// has no direct cross-town portal, so it answers with the room's own exit), the
// character stands where the server said, and a transport that kept the
// requested position would send every later move from a scene the character has
// already left. Observed coordinates are kept as the movement target for the
// next step, so the caller's own X/Y is not carried over.
func (t *ActionTransport) adoptObservedTownArea(uid int, session shared.RobotSession) {
	observer, ok := session.(interface {
		LastTownAreaObservation() (townAreaObservation, bool)
	})
	if !ok {
		return
	}
	observation, valid := observer.LastTownAreaObservation()
	if !valid {
		return
	}
	t.mu.Lock()
	updated := t.status[uid]
	updated.UID = uid
	updated.Village = observation.village
	updated.Area = observation.area
	updated.X = int(observation.x)
	updated.Y = int(observation.y)
	t.status[uid] = updated
	t.locationKnown[uid] = true
	t.mu.Unlock()
}

func (t *ActionTransport) ShoutLocal(ctx context.Context, command shared.RuntimeShoutCommand) error {
	session, err := t.session(command.UID)
	if err != nil {
		return err
	}
	return session.Shout(ctx, shared.ShoutIntent{Channel: shared.ShoutChannelArea, Message: command.Message})
}

// SetAreaFrom moves one robot to a store coordinate. The scheduler passes the
// previous area for diagnostics; the transport derives the transition from its
// own known location, so a same-area move only sends SET_USER_POSITION. Store
// transitions use a longer confirmation window than normal movement because
// they run during store-pressure bursts.
func (t *ActionTransport) SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), storeAreaTransitionTimeout)
	defer cancel()
	if err := t.MoveTown(ctx, shared.RuntimeMoveCommand{UID: uid, Village: village, Area: area, X: x, Y: y}); err != nil {
		foundationlog.Robotf("[CN90_STORE_SET_AREA_FAILED] uid=%d from=%d/%d to=%d/%d/%d/%d err=%v\n",
			uid, fromVillage, fromArea, village, area, x, y, err)
		return false
	}
	return true
}

// expertJobStoreName encodes the stall name the owner broadcasts. Chinese
// names go on the wire as GBK bytes, matching the client's code page.
func expertJobStoreName(kind shared.ExpertJobStoreKind) ([]byte, error) {
	switch kind {
	case shared.ExpertJobStoreDisjoint:
		return charset.EncodeGBKString("分解机")
	case shared.ExpertJobStoreEnchant:
		return charset.EncodeGBKString("附魔机")
	default:
		return nil, fmt.Errorf("90CN expert store kind %s is not supported", kind.Name())
	}
}

// StartExpertJobStore opens one expert-job stall on the robot's current
// position and waits for the server acknowledgement.
func (t *ActionTransport) StartExpertJobStore(uid int, kind shared.ExpertJobStoreKind, cost uint32) bool {
	session, err := t.session(uid)
	if err != nil {
		foundationlog.Robotf("[CN90_STORE_START_FAILED] uid=%d kind=%s reason=session_unavailable err=%v\n", uid, kind.Name(), err)
		return false
	}
	starter, ok := session.(interface {
		StartExpertJobStore(context.Context, shared.ExpertJobStoreKind, uint32, []byte, int16, int16) error
	})
	if !ok {
		foundationlog.Robotf("[CN90_STORE_START_FAILED] uid=%d kind=%s reason=protocol_not_implemented\n", uid, kind.Name())
		return false
	}
	t.mu.RLock()
	status, known := t.status[uid]
	t.mu.RUnlock()
	if !known {
		foundationlog.Robotf("[CN90_STORE_START_FAILED] uid=%d kind=%s reason=location_unknown\n", uid, kind.Name())
		return false
	}
	name, err := expertJobStoreName(kind)
	if err != nil {
		foundationlog.Robotf("[CN90_STORE_START_FAILED] uid=%d kind=%s reason=name err=%v\n", uid, kind.Name(), err)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), expertJobStoreAckTimeout)
	defer cancel()
	if err := starter.StartExpertJobStore(ctx, kind, cost, name, int16(status.X), int16(status.Y)); err != nil {
		foundationlog.Robotf("[CN90_STORE_START_FAILED] uid=%d kind=%s at=%d/%d/%d/%d err=%v\n",
			uid, kind.Name(), status.Village, status.Area, status.X, status.Y, err)
		return false
	}
	t.markStoreOpened(uid, kind)
	foundationlog.Robotf("[CN90_STORE_STARTED] uid=%d kind=%s at=%d/%d/%d/%d charge=%d\n",
		uid, kind.Name(), status.Village, status.Area, status.X, status.Y, cost)
	return true
}

// markStoreOpened publishes the store state the scheduler waits for. The
// server acks the create on op598 but never sends the owner a create
// notification, so the acknowledgement is the authoritative success signal.
func (t *ActionTransport) markStoreOpened(uid int, kind shared.ExpertJobStoreKind) {
	if t == nil || uid <= 0 {
		return
	}
	t.mu.Lock()
	status := t.status[uid]
	status.UID = uid
	status.RobotType = 3
	status.StoreCreated = true
	status.StoreDisplayAck = true
	if kind == shared.ExpertJobStoreDisjoint {
		status.DisjointCreateSent = true
		status.DisjointDirectAck = true
		status.DisjointActive = true
		status.LastDisjointError = 0
	} else {
		status.EnchantCreateSent = true
		status.EnchantDirectAck = true
		status.EnchantActive = true
		status.LastEnchantError = 0
	}
	t.status[uid] = status
	t.mu.Unlock()
}

// clearStoreOpened drops the store state after a close or a failed attempt so
// the scheduler does not keep counting the robot as an active stall owner.
func (t *ActionTransport) clearStoreOpened(uid int) {
	if t == nil || uid <= 0 {
		return
	}
	t.mu.Lock()
	status := t.status[uid]
	status.RobotType = 0
	status.StoreCreated = false
	status.StoreDisplayAck = false
	status.DisjointCreateSent = false
	status.DisjointDirectAck = false
	status.DisjointActive = false
	status.EnchantCreateSent = false
	status.EnchantDirectAck = false
	status.EnchantActive = false
	t.status[uid] = status
	t.mu.Unlock()
}

// CloseExpertJobStore closes the robot's stall. The server also closes a stall
// when the session ends, so a missing acknowledgement only means the store was
// already gone.
func (t *ActionTransport) CloseExpertJobStore(uid int) bool {
	session, err := t.session(uid)
	if err != nil {
		return false
	}
	closer, ok := session.(interface {
		CloseExpertJobStore(context.Context) error
	})
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), expertJobStoreAckTimeout)
	defer cancel()
	if err := closer.CloseExpertJobStore(ctx); err != nil {
		foundationlog.Robotf("[CN90_STORE_CLOSE_FAILED] uid=%d err=%v\n", uid, err)
		t.clearStoreOpened(uid)
		return false
	}
	t.clearStoreOpened(uid)
	foundationlog.Robotf("[CN90_STORE_CLOSED] uid=%d\n", uid)
	return true
}

// AccountOnline reports whether the transport still holds a game session for
// uid. The DNF90 server keeps account sessions in memory, so the robot's own
// attachment is the only boundary it can observe; the scheduler's relogin
// delay still gives the server time to finish its final character save.
func (t *ActionTransport) AccountOnline(uid int) (bool, error) {
	if t == nil || uid <= 0 {
		return false, nil
	}
	t.mu.RLock()
	_, online := t.sessions[uid]
	t.mu.RUnlock()
	return online, nil
}

var _ interface {
	MoveTown(context.Context, shared.RuntimeMoveCommand) error
	ShoutLocal(context.Context, shared.RuntimeShoutCommand) error
	SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool
	StartExpertJobStore(uid int, kind shared.ExpertJobStoreKind, cost uint32) bool
	CloseExpertJobStore(uid int) bool
	AccountOnline(uid int) (bool, error)
} = (*ActionTransport)(nil)
