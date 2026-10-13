package cn90

import (
	"context"
	"fmt"
	"time"

	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// emoteAckTimeout bounds the wait for the op254 reply. The server answers from
// the same write path the other single-command opcodes use, so the window only
// has to cover a round trip.
const emoteAckTimeout = 5 * time.Second

// ChangeEmotion sets the character's mood/emotion index and waits for the
// op254 acknowledgement.
//
// The server stores the index in the character's durable "emotion_index" stat
// without validating it, and it does not broadcast the change: other players
// pick the new mood up from their next actor-list rebuild. So a nil return
// means the value is persisted server-side, not that anyone has seen it yet.
func (s *Session) ChangeEmotion(ctx context.Context, emotionIndex uint16) (uint16, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, emoteAckTimeout)
		defer cancel()
	}
	ack := make(chan uint16, 1)
	rejected := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdChangeEmotion {
			return
		}
		stored, err := protocol.ChangeEmotionAck(protocol.CmdChangeEmotion, packet.Body)
		if err != nil {
			select {
			case rejected <- err:
			default:
			}
			return
		}
		select {
		case ack <- stored:
		default:
		}
	})
	defer cleanup()
	if err := s.client.ChangeEmotion(ctx, emotionIndex); err != nil {
		return 0, err
	}
	select {
	case stored := <-ack:
		foundationlog.Robotf("CN90_EMOTE cid=%d requested=%d stored=%d\n", s.selfCharacterID, emotionIndex, stored)
		return stored, nil
	case err := <-rejected:
		return 0, err
	case <-ctx.Done():
		return 0, fmt.Errorf("90CN emotion change confirmation: %w", ctx.Err())
	case <-s.Done():
		return 0, fmt.Errorf("90CN session ended during emotion change")
	}
}
