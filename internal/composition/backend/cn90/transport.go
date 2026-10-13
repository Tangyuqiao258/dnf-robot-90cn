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
	if request.InitialTownKnown {
		t.mu.Lock()
		status := t.status[uid]
		status.Village = request.InitialVillage
		status.Area = request.InitialArea
		status.X = request.InitialX
		status.Y = request.InitialY
		t.status[uid] = status
		t.locationKnown[uid] = true
		t.mu.Unlock()
	}
	return nil
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
	t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateRunning, State: 3, RunStartTime: time.Now().Unix()}
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
