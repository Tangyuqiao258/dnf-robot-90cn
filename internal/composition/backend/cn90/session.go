package cn90

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

type SessionFactory struct {
	// ConnectHost is the loopback host the DNF90 game listeners are bound to.
	ConnectHost string
	// Channels carries the runtime channel ports; the account name selects a
	// stable port so one robot process can populate several channels.
	Channels channelCatalog
	Timeout  time.Duration
	// Binder registers the robot process id with the account the DNF90
	// launcher would normally launch. It may be nil when the deployment only
	// uses the server fallback account.
	Binder *AccountBinder
}

// channelAddress resolves the game endpoint for one account.
func channelAddress(host string, channels channelCatalog, account string) (string, error) {
	port := channels.portForAccount(account, 0)
	if port <= 0 || port > 65535 || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("90CN game address is incomplete")
	}
	return net.JoinHostPort(host, fmt.Sprint(port)), nil
}

const sessionKeepaliveInterval = 5 * time.Second
const townAreaTransitionTimeout = 5 * time.Second

// storeAreaTransitionTimeout bounds the town/area transition that a store
// placement uses. Store slots stay held during the attempt and the server may
// answer the area notification late during an online burst.
const storeAreaTransitionTimeout = 12 * time.Second

// expertJobStoreAckTimeout bounds the op598/op600 store acknowledgement wait.
const expertJobStoreAckTimeout = 8 * time.Second

// crossTownPresenceTimeout bounds the portal-shaped login presence attempt that
// an assigned town differing from the home town uses.
const crossTownPresenceTimeout = 6 * time.Second

// initialTownRouteTimeout bounds the wait for the initial town transition
// (class0/op24) after the op143 checkpoint is sent.
const initialTownRouteTimeout = 15 * time.Second

// sessionKeepaliveAckTimeout is how long the server may stop answering
// CHECK_CONNECTION before the session is treated as half-open. Enforcement
// only starts after at least one keepalive ACK has been observed, so a server
// that answers only the login probe never triggers reconnect churn.
var sessionKeepaliveAckTimeout = 6 * sessionKeepaliveInterval

type Session struct {
	client             *protocol.Client
	cancel             context.CancelFunc
	done               chan struct{}
	keepaliveDone      chan struct{}
	packetObserverLock lockhub.RWLocker
	packetObservers    []*packetObserverRegistration
	terminationGuard   lockhub.Locker
	termination        func()
	// follow is the party leader tracker, created only for a session opened
	// with EnablePartyFollow.
	follow *partyFollowState
	// dungeon tracks the active run and the character's death state.
	dungeon *dungeonState
	// townPosition is the last town coordinate the server was told about. A
	// dungeon entry is refused while the return origin has no position report
	// behind it in the current town scene, so the entry re-reports this before
	// it opens the selector.
	townPosition *townPositionState
	// reviveQueue carries death notices to the revive worker. It is nil unless
	// the session was opened with EnableDungeonRevive, which is what keeps the
	// observer from queueing work nobody will send.
	reviveQueue     chan struct{}
	selfCharacterID uint16
	keepaliveGuard  lockhub.Locker
	lastCheckAck    time.Time
	checkAckSeen    bool
}

type packetObserverRegistration struct {
	fn func(protocol.Packet)
}

func (f SessionFactory) OpenSession(ctx context.Context, request shared.OpenSessionRequest) (shared.RobotSession, error) {
	if strings.TrimSpace(request.AccountName) == "" {
		return nil, fmt.Errorf("90CN session account name is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	openCtx, cancelOpen := context.WithTimeout(ctx, timeout)
	defer cancelOpen()

	// The DNF90 server binds a connection to the account registered for the
	// TCP owner process id, resolved at accept time. Registration, dial and
	// the first inbound packet therefore form one critical section: without
	// the first packet the accept lookup may not have run yet, and a later
	// registration for the next robot would leak into this connection.
	var first *protocol.Packet
	client, err := f.dialBound(openCtx, request.AccountName, &first)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = client.Close()
		}
	}()

	if _, err := client.CompleteHandshakeFrom(openCtx, first); err != nil {
		return nil, fmt.Errorf("90CN login: %w", err)
	}
	if err := client.RequestRoster(openCtx); err != nil {
		return nil, err
	}
	if _, err := waitUpperPacket(openCtx, client, protocol.ClassNotice, protocol.NotiCharacterList); err != nil {
		return nil, fmt.Errorf("90CN roster: %w", err)
	}
	if err := client.SelectCharacter(openCtx, request.CharacterSlot); err != nil {
		return nil, err
	}
	selectPacket, err := waitUpperPacket(openCtx, client, protocol.ClassCommand, protocol.ResponseSelect)
	if err != nil {
		return nil, fmt.Errorf("90CN select character: %w", err)
	}
	selfCharacterID, err := protocol.SelectResultCharacterID(selectPacket.Body)
	if err != nil {
		return nil, fmt.Errorf("90CN select character: %w", err)
	}
	// A completed character must acknowledge the initial town checkpoint so
	// the server commits the town actor and transition before op35/op36 are
	// owned by the scene.
	if err := client.ProgressInitialTown(openCtx); err != nil {
		return nil, err
	}
	if _, err := waitUpperPacket(openCtx, client, protocol.ClassNotice, protocol.NotiSceneTransition); err != nil {
		return nil, fmt.Errorf("90CN initial town: %w", err)
	}
	// The live client acknowledges the first typed op24 with legacy 1345
	// u32(2); the server defers the scene tail (and later store acceptance)
	// until that boundary. A rejected ack only degrades projections, so the
	// login continues.
	if err := client.TownSceneReady(openCtx); err != nil {
		foundationlog.Robotf("CN90_SCENE_READY_FAILED err=%v\n", err)
	}
	townPositionKnown := false
	if request.InitialTownKnown {
		if err := initializeTownPresence(openCtx, client, request); err != nil {
			// The persisted login location is owned by the server. A spawn
			// request that does not match it is rejected server-side; keep the
			// session online instead of failing the whole login.
			foundationlog.Robotf("CN90_TOWN_PRESENCE_DEFERRED err=%v\n", err)
		} else {
			townPositionKnown = true
		}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	session := &Session{
		client: client, cancel: cancel, done: make(chan struct{}), keepaliveDone: make(chan struct{}),
		selfCharacterID: selfCharacterID,
	}
	// The entry presence above reported a coordinate with op36, which is a
	// scene transition rather than a coordinate report: the server accepts it
	// but keeps no op35 position for the new scene. Recording it is still what
	// the entry has to re-report, because it is where the character stands.
	if townPositionKnown {
		session.townPosition = &townPositionState{}
		session.townPosition.mark(int16(request.InitialX), int16(request.InitialY), 0, 0)
	}
	if request.EnablePartyAutoAccept {
		session.startPartyAutoAccept(runCtx)
	}
	if request.EnablePartyFollow {
		session.startPartyFollow(runCtx)
	}
	// The run and death state is tracked for every session: it is what tells the
	// runtime whether a dungeon command is meaningful at all, and recording it
	// costs nothing. Only the revive worker writes to the socket, and only when
	// the request asked for it.
	session.startDungeonTracking(runCtx)
	if request.EnableDungeonRevive {
		session.startDungeonRevive(runCtx)
	}
	go session.drain(runCtx)
	go session.keepalive(runCtx)
	closeOnError = false
	return session, nil
}

// dialBound registers the account for this process id, dials the game channel
// and returns the connection plus the first inbound packet.
func (f SessionFactory) dialBound(ctx context.Context, accountName string, first **protocol.Packet) (*protocol.Client, error) {
	address, err := channelAddress(f.ConnectHost, f.Channels, accountName)
	if err != nil {
		return nil, err
	}
	client, packet, err := dialBoundSession(ctx, f.Binder, address, accountName)
	if err != nil {
		return nil, err
	}
	*first = packet
	return client, nil
}

func initializeTownPresence(ctx context.Context, client *protocol.Client, request shared.OpenSessionRequest) error {
	if request.InitialVillage < 0 || request.InitialVillage > 255 || request.InitialArea < 0 || request.InitialArea > 255 {
		return fmt.Errorf("90CN initial town out of range: %d/%d", request.InitialVillage, request.InitialArea)
	}
	if request.InitialX < math.MinInt16 || request.InitialX > math.MaxInt16 || request.InitialY < math.MinInt16 || request.InitialY > math.MaxInt16 {
		return fmt.Errorf("90CN initial town position out of range: %d,%d", request.InitialX, request.InitialY)
	}
	// An assigned town that differs from the server-side home town must be
	// requested with the cross-town portal shape, otherwise the server answers
	// with a blocked event and the robot stays on its home route until the
	// first move relocates it.
	if request.HomeVillage > 0 && request.HomeVillage != request.InitialVillage {
		portalCtx, cancel := context.WithTimeout(ctx, crossTownPresenceTimeout)
		sendErr := client.SetUserAreaPortal(portalCtx, byte(request.InitialVillage), byte(request.InitialArea),
			int16(request.InitialX), int16(request.InitialY), 5, uint16(request.HomeVillage))
		if sendErr == nil {
			if confirmErr := waitTownPresence(portalCtx, client, request); confirmErr == nil {
				cancel()
				return nil
			}
		}
		cancel()
	}
	if err := client.SetUserArea(ctx, byte(request.InitialVillage), byte(request.InitialArea), int16(request.InitialX), int16(request.InitialY), 5); err != nil {
		return fmt.Errorf("90CN enter town: %w", err)
	}
	if err := waitTownPresence(ctx, client, request); err != nil {
		return fmt.Errorf("90CN enter town confirmation: %w", err)
	}
	return nil
}

// waitTownPresence waits for the area notification that confirms the requested
// town/area/position.
func waitTownPresence(ctx context.Context, client *protocol.Client, request shared.OpenSessionRequest) error {
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return err
		}
		if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiUserArea || len(packet.Body) < 8 {
			continue
		}
		if int(packet.Body[2]) != request.InitialVillage || int(packet.Body[3]) != request.InitialArea ||
			int16(binary.LittleEndian.Uint16(packet.Body[4:6])) != int16(request.InitialX) ||
			int16(binary.LittleEndian.Uint16(packet.Body[6:8])) != int16(request.InitialY) {
			continue
		}
		return nil
	}
}

// waitUpperPacket reads packets until one matches the class and type.
func waitUpperPacket(ctx context.Context, client *protocol.Client, class byte, typ uint16) (protocol.Packet, error) {
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return protocol.Packet{}, err
		}
		if packet.Class == class && packet.Type == typ {
			return packet, nil
		}
	}
}

func (s *Session) MoveTown(ctx context.Context, intent shared.TownMoveIntent) error {
	if err := s.client.SetUserPosition(ctx, intent.X, intent.Y, intent.Direction, intent.Motion); err != nil {
		return err
	}
	s.rememberTownPosition(intent.X, intent.Y, intent.Direction, intent.Motion)
	return nil
}

// rememberTownPosition records the coordinate the server has just been told
// about. A dungeon entry re-reports it, so a move that failed to send must not
// be remembered.
func (s *Session) rememberTownPosition(x, y int16, direction byte, motion uint16) {
	if s == nil {
		return
	}
	if s.townPosition == nil {
		s.townPosition = &townPositionState{}
	}
	s.townPosition.mark(x, y, direction, motion)
}

func (s *Session) MoveTownArea(ctx context.Context, intent shared.TownAreaMoveIntent) error {
	if intent.Village < 0 || intent.Village > 255 || intent.Area < 0 || intent.Area > 255 {
		return fmt.Errorf("90CN town area out of range: %d/%d", intent.Village, intent.Area)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, townAreaTransitionTimeout)
		defer cancel()
	}
	confirmed := make(chan struct{}, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiUserArea || len(packet.Body) < 8 ||
			int(packet.Body[2]) != intent.Village ||
			int(packet.Body[3]) != intent.Area ||
			int16(binary.LittleEndian.Uint16(packet.Body[4:6])) != intent.X ||
			int16(binary.LittleEndian.Uint16(packet.Body[6:8])) != intent.Y {
			return
		}
		select {
		case confirmed <- struct{}{}:
		default:
		}
	})
	defer cleanup()
	if intent.SourceVillage > 0 && intent.SourceVillage != intent.Village {
		// Cross-town transitions only pass the server's route guard when the
		// request carries the current town in the portal opaque field.
		if err := s.client.SetUserAreaPortal(ctx, byte(intent.Village), byte(intent.Area), intent.X, intent.Y, 5, uint16(intent.SourceVillage)); err != nil {
			return err
		}
	} else if err := s.client.SetUserArea(ctx, byte(intent.Village), byte(intent.Area), intent.X, intent.Y, 5); err != nil {
		return err
	}
	select {
	case <-confirmed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.Done():
		return fmt.Errorf("90CN session ended during town area transition")
	}
}

// Shout is not implemented for 90CN: the server has no broadcast shout. It
// only forwards same-area directed peer chat (the class1/op17 request carries
// a target id and name, delivered as a class0/op118 notice), which the
// target-less ShoutIntent cannot express.
func (s *Session) Shout(ctx context.Context, intent shared.ShoutIntent) error {
	return shared.UnsupportedCapabilityError{
		Backend:   BackendID,
		Operation: shared.CapabilityShout,
		Reason:    "90CN has no broadcast shout; only same-area directed peer chat exists and the target-less shout intent cannot express it",
	}
}

// StartExpertJobStore opens one expert-job stall and waits for the class1/op598
// acknowledgement the server sends after it registers the store.
func (s *Session) StartExpertJobStore(ctx context.Context, kind shared.ExpertJobStoreKind, charge uint32, name []byte, x, y int16) error {
	wireKind, err := expertJobWireKind(kind)
	if err != nil {
		return err
	}
	if len(name) == 0 {
		return fmt.Errorf("90CN expert store name is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, expertJobStoreAckTimeout)
		defer cancel()
	}
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Type != protocol.CmdCreateExpertStore {
			return
		}
		select {
		case ack <- protocol.ExpertJobStoreAck(protocol.CmdCreateExpertStore, packet.Body):
		default:
		}
	})
	defer cleanup()
	if err := s.client.CreateExpertJobStore(ctx, wireKind, name, charge, x, y, s.selfCharacterID); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			return fmt.Errorf("90CN expert store open rejected: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("90CN expert store open confirmation: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during expert store open")
	}
}

// CloseExpertJobStore closes the session's stall. A successful close is
// confirmed by the op539 notification the server broadcasts to the area (the
// owner included); only a rejected close answers on op600.
func (s *Session) CloseExpertJobStore(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, expertJobStoreAckTimeout)
		defer cancel()
	}
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		switch packet.Type {
		case protocol.CmdCloseExpertStore:
			select {
			case ack <- protocol.ExpertJobStoreAck(protocol.CmdCloseExpertStore, packet.Body):
			default:
			}
		case protocol.NotiExpertStoreClose:
			if len(packet.Body) < 2 || binary.LittleEndian.Uint16(packet.Body[0:2]) != s.selfCharacterID {
				return
			}
			select {
			case ack <- nil:
			default:
			}
		}
	})
	defer cleanup()
	if err := s.client.CloseExpertJobStore(ctx); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			return fmt.Errorf("90CN expert store close rejected: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("90CN expert store close confirmation: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during expert store close")
	}
}

// expertJobWireKind maps the shared stall kind onto the wire byte.
func expertJobWireKind(kind shared.ExpertJobStoreKind) (byte, error) {
	switch kind {
	case shared.ExpertJobStoreDisjoint:
		return protocol.ExpertJobStoreDisjoint, nil
	case shared.ExpertJobStoreEnchant:
		return protocol.ExpertJobStoreEnchant, nil
	default:
		return 0, fmt.Errorf("90CN expert store kind %s is not supported", kind.Name())
	}
}

func (s *Session) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	exitCtx, cancelExit := context.WithTimeout(context.Background(), 2*time.Second)
	_ = s.client.Exit(exitCtx)
	cancelExit()
	if s.cancel != nil {
		s.cancel()
	}
	err := s.client.Close()
	if s.done != nil {
		<-s.done
	}
	if s.keepaliveDone != nil {
		<-s.keepaliveDone
	}
	return err
}

func (s *Session) Done() <-chan struct{} {
	if s == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return s.done
}

// CharacterID reports the character this session selected. The transport
// publishes it as the runtime status CID, which is what the auction round keys
// its stock plan on: the plan is a pure function of the character id, so the
// runtime recomputes exactly the bag rows the offline seeder wrote, and without
// the id the round has no way to name them.
func (s *Session) CharacterID() int {
	if s == nil {
		return 0
	}
	return int(s.selfCharacterID)
}

// setTerminationCallback keeps session lifecycle notification inside the
// 90CN adapter. It avoids one waiter goroutine per online robot without
// widening the shared RobotSession contract.
func (s *Session) setTerminationCallback(callback func()) {
	if s == nil {
		return
	}
	s.terminationGuard.Lock()
	select {
	case <-s.done:
		s.terminationGuard.Unlock()
		if callback != nil {
			callback()
		}
		return
	default:
		s.termination = callback
		s.terminationGuard.Unlock()
	}
}

func (s *Session) signalTermination() {
	close(s.done)
	s.terminationGuard.Lock()
	callback := s.termination
	s.termination = nil
	s.terminationGuard.Unlock()
	if callback != nil {
		callback()
	}
}

// partyAutoAcceptQueue bounds the hand-off between the drain loop and the
// responder goroutine. A full queue drops the prompt instead of blocking
// packet dispatch.
const partyAutoAcceptQueue = 8

// partyAutoAcceptSendTimeout bounds one op11 acceptance write.
const partyAutoAcceptSendTimeout = 5 * time.Second

// startPartyAutoAccept answers forwarded party invite prompts for the whole
// session. The observer only classifies and enqueues; a responder goroutine
// performs the op11 write so a slow write never stalls the drain loop.
// Forwarded peer requests that are not party invites (the mode-1 trade prompt
// in particular) stay unanswered.
func (s *Session) startPartyAutoAccept(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	type partyInvite struct {
		inviterID uint16
		mode      byte
		value     uint32
	}
	queue := make(chan partyInvite, partyAutoAcceptQueue)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		switch {
		case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiRequestPeer:
			notice, ok := protocol.ParseRequestPeerNotice(packet.Body)
			if !ok {
				return
			}
			value, isPartyInvite := protocol.PartyInviteAcceptValue(notice.Mode)
			if !isPartyInvite {
				return
			}
			select {
			case queue <- partyInvite{inviterID: notice.SourceID, mode: notice.Mode, value: value}:
				foundationlog.Robotf("CN90_PARTY_INVITE_RECEIVED cid=%d inviter=%d mode=%d\n", s.selfCharacterID, notice.SourceID, notice.Mode)
			default:
				foundationlog.Robotf("CN90_PARTY_INVITE_DROPPED cid=%d inviter=%d mode=%d\n", s.selfCharacterID, notice.SourceID, notice.Mode)
			}
		case packet.Class == protocol.ClassCommand && packet.Type == protocol.CmdResponsePeer:
			ok, code := protocol.ResponsePeerResult(packet.Body)
			foundationlog.Robotf("CN90_PARTY_RESPONSE_ACK cid=%d ok=%t code=%d\n", s.selfCharacterID, ok, code)
		}
	})
	go func() {
		defer cleanup()
		for {
			select {
			case <-ctx.Done():
				return
			case invite := <-queue:
				sendCtx, cancel := context.WithTimeout(ctx, partyAutoAcceptSendTimeout)
				err := s.client.ResponsePeer(sendCtx, invite.inviterID, invite.mode, invite.value)
				cancel()
				if err != nil {
					foundationlog.Robotf("CN90_PARTY_ACCEPT_FAILED cid=%d inviter=%d mode=%d err=%v\n", s.selfCharacterID, invite.inviterID, invite.mode, err)
					continue
				}
				foundationlog.Robotf("CN90_PARTY_ACCEPT_SENT cid=%d inviter=%d mode=%d value=%d\n", s.selfCharacterID, invite.inviterID, invite.mode, invite.value)
			}
		}
	}()
}

// setPacketObserver is intentionally private to the 90CN adapter. It gives
// verified workflows a narrow way to consume packets already owned by the
// session drain without exposing raw packets to shared layers. Registrations
// are additive: overlapping waiters must not steal each other's packets.
func (s *Session) setPacketObserver(observer func(protocol.Packet)) func() {
	if s == nil {
		return func() {}
	}
	registration := &packetObserverRegistration{fn: observer}
	s.packetObserverLock.Lock()
	s.packetObservers = append(s.packetObservers, registration)
	s.packetObserverLock.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.packetObserverLock.Lock()
			for index, candidate := range s.packetObservers {
				if candidate == registration {
					s.packetObservers = append(s.packetObservers[:index], s.packetObservers[index+1:]...)
					break
				}
			}
			s.packetObserverLock.Unlock()
		})
	}
}

func (s *Session) dispatchPacket(packet protocol.Packet) {
	if s == nil {
		return
	}
	if packet.Class == protocol.ClassCommand && packet.Type == protocol.ResponseCheckConnection {
		s.recordCheckAck(time.Now())
	}
	s.packetObserverLock.RLock()
	observers := append([]*packetObserverRegistration(nil), s.packetObservers...)
	s.packetObserverLock.RUnlock()
	for _, observer := range observers {
		if observer != nil && observer.fn != nil {
			observer.fn(packet)
		}
	}
}

func (s *Session) drain(ctx context.Context) {
	defer s.signalTermination()
	_ = s.client.Run(ctx, func(packet protocol.Packet) error {
		s.dispatchPacket(packet)
		return nil
	})
}

func (s *Session) keepalive(ctx context.Context) {
	if s.keepaliveDone == nil {
		return
	}
	defer close(s.keepaliveDone)
	ticker := time.NewTicker(sessionKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.keepaliveStalled(time.Now()) {
				foundationlog.Robotf("CN90_KEEPALIVE_STALLED cid=%d timeout=%s\n", s.selfCharacterID, sessionKeepaliveAckTimeout)
				s.abort()
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, sessionKeepaliveInterval/2)
			err := s.client.CheckConnection(pingCtx)
			cancel()
			if err != nil {
				s.abort()
				return
			}
		}
	}
}

// recordCheckAck notes a CHECK_CONNECTION response. Enforcement of the
// half-open timeout only starts after the first observed response.
func (s *Session) recordCheckAck(now time.Time) {
	if s == nil {
		return
	}
	s.keepaliveGuard.Lock()
	s.lastCheckAck = now
	s.checkAckSeen = true
	s.keepaliveGuard.Unlock()
}

// keepaliveStalled reports whether the server stopped answering keepalives for
// longer than the configured timeout after having answered at least once.
func (s *Session) keepaliveStalled(now time.Time) bool {
	if s == nil {
		return false
	}
	s.keepaliveGuard.Lock()
	defer s.keepaliveGuard.Unlock()
	if !s.checkAckSeen {
		return false
	}
	return now.Sub(s.lastCheckAck) > sessionKeepaliveAckTimeout
}

func (s *Session) abort() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
}

var _ shared.SessionFactory = SessionFactory{}
var _ shared.RobotSession = (*Session)(nil)
