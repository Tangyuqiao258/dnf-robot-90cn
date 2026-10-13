package cn90

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"robot/internal/foundation/charset"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// Same-area directed chat.
//
// The server has no broadcast channel: the only chat it forwards is one message
// between two characters standing in the same town area, addressed by character
// id (dnfbridge/online_social.go). A message to a target that is offline, in
// another town or in another area of the same town is dropped without any reply
// at all, so the acknowledgement timeout is the only way that refusal shows up.
//
// The name that travels with a request is compared to nothing: the receiver is
// shown the sender's name as the server resolves it from its own roster. The
// name a session uses to answer is therefore the one it read out of the
// delivery notice, which is authoritative.

const (
	// chatAckTimeout bounds the wait for the class1/op17 success. The server
	// answers it after every check it makes, so an unanswered request is a
	// refusal and not a slow link.
	chatAckTimeout = 5 * time.Second
	// chatReplyCooldown is how long one session stays quiet towards a sender it
	// has already answered. Two sessions that both auto-reply would otherwise
	// answer each other for as long as they stand together.
	chatReplyCooldown = 30 * time.Second
	// chatReplyQueue is how many unanswered senders can wait for a reply at once.
	chatReplyQueue = 4
)

// chatReplyTarget is one sender waiting to be answered. The name is carried
// along because it is read from the delivery notice, which is the only place
// the sender's name is known.
type chatReplyTarget struct {
	id   uint16
	name string
}

// chatState is the session's view of the chat it has received. The packet
// observer writes it from the drain goroutine while the reply worker reads and
// updates it, so every field sits behind the state's own lock.
type chatState struct {
	mu lockhub.RWLocker
	// replied is the last time this session answered each sender.
	replied map[uint16]time.Time
}

// coolingDown reports whether the sender was answered recently enough that
// another reply would only be noise.
func (st *chatState) coolingDown(sender uint16, now time.Time, cooldown time.Duration) bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	last, ok := st.replied[sender]
	return ok && now.Sub(last) < cooldown
}

func (st *chatState) markReplied(sender uint16, now time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.replied == nil {
		st.replied = make(map[uint16]time.Time)
	}
	st.replied[sender] = now
}

// startChat runs the receive side for the life of the session. The observer only
// records and queues; the reply worker owns the socket writes, so a slow reply
// cannot stall the drain loop.
func (s *Session) startChat(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	if s.chat == nil {
		s.chat = &chatState{}
	}
	cleanup := s.setPacketObserver(s.observeChat)
	go func() {
		defer cleanup()
		<-ctx.Done()
	}()
	if !s.chatReplyEnabled || len(s.chatReplies) == 0 {
		return
	}
	s.chatQueue = make(chan chatReplyTarget, chatReplyQueue)
	queue := s.chatQueue
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case target := <-queue:
				s.replyToChat(ctx, target)
			}
		}
	}()
}

// observeChat records one delivery notice and queues an automatic reply. The
// message text is decoded and logged; nothing here writes to the socket.
func (s *Session) observeChat(packet protocol.Packet) {
	state := s.chat
	if state == nil {
		return
	}
	if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiPeerChatMessage {
		return
	}
	notice, ok := protocol.ParsePeerChatNotice(packet.Body)
	if !ok {
		return
	}
	foundationlog.Robotf("CN90_CHAT_RECEIVED cid=%d from=%d name=%s text=%q bytes=%d\n",
		s.selfCharacterID, notice.SenderID, notice.SenderName, notice.Message, len(notice.Message))
	// A notice from this session's own character, or one without a usable
	// sender, has nowhere to reply to.
	if notice.SenderID == 0 || notice.SenderID == s.selfCharacterID {
		return
	}
	if s.chatQueue == nil {
		return
	}
	now := time.Now()
	if state.coolingDown(notice.SenderID, now, chatReplyCooldown) {
		return
	}
	select {
	case s.chatQueue <- chatReplyTarget{id: notice.SenderID, name: notice.SenderName}:
		// The cooldown starts at the moment the reply is queued, so a second
		// message that arrives while the first is still waiting does not queue a
		// duplicate.
		state.markReplied(notice.SenderID, now)
	default:
	}
}

// replyToChat answers one sender with a line from the session's reply pool. A
// reply that the server refuses is logged and dropped: the sender is a passer-by
// and nothing about the session depends on the exchange.
func (s *Session) replyToChat(ctx context.Context, target chatReplyTarget) {
	if len(s.chatReplies) == 0 {
		return
	}
	line := strings.TrimSpace(s.chatReplies[rand.Intn(len(s.chatReplies))])
	if line == "" {
		return
	}
	replyCtx, cancel := context.WithTimeout(ctx, chatAckTimeout)
	defer cancel()
	if err := s.SendChat(replyCtx, target.id, target.name, line); err != nil {
		foundationlog.Robotf("CN90_CHAT_REPLY_FAILED cid=%d to=%d err=%v\n", s.selfCharacterID, target.id, err)
		return
	}
	foundationlog.Robotf("CN90_CHAT_REPLIED cid=%d to=%d\n", s.selfCharacterID, target.id)
}

// SendChat delivers one directed message and waits for the server to accept it.
//
// Both strings go on the wire as GBK, the code page the client reads, so the
// receiver renders them the way a real client would.
func (s *Session) SendChat(ctx context.Context, targetID uint16, targetName, message string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN directed chat needs an open session")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, chatAckTimeout)
		defer cancel()
	}
	wireMessage, err := charset.EncodeGBKString(message)
	if err != nil {
		return fmt.Errorf("90CN directed chat message: %w", err)
	}
	wireName, err := charset.EncodeGBKString(targetName)
	if err != nil {
		return fmt.Errorf("90CN directed chat target name: %w", err)
	}
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdPeerChat {
			return
		}
		select {
		case ack <- protocol.PeerChatAck(packet.Body):
		default:
		}
	})
	defer cleanup()
	if err := s.client.SendPeerChat(ctx, protocol.PeerChatMessageType, targetID, wireMessage, wireName); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		// The server drops a message it will not deliver without answering, so
		// this is the expected outcome for a target that is not in the area.
		return fmt.Errorf("90CN directed chat acknowledgement: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during a directed chat")
	}
	foundationlog.Robotf("CN90_CHAT_SENT cid=%d target=%d bytes=%d\n", s.selfCharacterID, targetID, len(wireMessage))
	return nil
}

// Shout sends one directed message. The server has no broadcast channel, so an
// intent without a target has nowhere to go and is refused rather than sent
// somewhere it does not belong.
func (s *Session) Shout(ctx context.Context, intent shared.ShoutIntent) error {
	if intent.Channel != "" && intent.Channel != shared.ShoutChannelArea {
		return shared.UnsupportedCapabilityError{
			Backend:   BackendID,
			Operation: shared.CapabilityShout,
			Reason:    fmt.Sprintf("90CN has no %s chat: the server only forwards a directed message between two characters in the same town area", intent.Channel),
		}
	}
	if intent.TargetCharacterID == 0 || strings.TrimSpace(intent.TargetName) == "" {
		return shared.UnsupportedCapabilityError{
			Backend:   BackendID,
			Operation: shared.CapabilityShout,
			Reason:    "90CN has no broadcast shout: a directed message needs a target character id and name",
		}
	}
	return s.SendChat(ctx, intent.TargetCharacterID, intent.TargetName, intent.Message)
}
