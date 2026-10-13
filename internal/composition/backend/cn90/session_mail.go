package cn90

import (
	"context"
	"fmt"
	"time"

	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// The 90CN mailbox at session level. Server authority: the server tree's
// `internal/modules/dnf/mail` (handler.go dispatch, owner.go Claim/ChangeState,
// body.go writers).
//
// Three properties of the server shape every method here:
//
//   - op96 answers twice. The class1 acknowledgement carries the not-loaded
//     count, while the whole snapshot arrives separately as a class0/0x61
//     notification. Both belong to one open, so an open waits for both and
//     reports the acknowledgement's failure code rather than sitting out the
//     timeout.
//   - A claim never rewrites the mailbox page. The server moves the items into
//     the character's bag and answers with the rows it settled, deliberately
//     leaving the letter as the client last saw it, so state that matters after
//     a claim has to come from a fresh open.
//   - Claiming does not remove a letter; only op134 status 0 does. A mailbox
//     therefore grows for as long as its owner keeps claiming without deleting.

// mailRequestTimeout bounds one mailbox request/response exchange. The server
// answers from durable state in one pass, so this only guards a wedged session.
const mailRequestTimeout = 10 * time.Second

// MailboxOpen opens the session's mailbox and returns the full snapshot. The
// class0/0x61 snapshot is the only source of claim-object ids, which is what
// every claim is built from.
func (s *Session) MailboxOpen(ctx context.Context) (protocol.MailboxSnapshot, error) {
	if s == nil || s.client == nil {
		return protocol.MailboxSnapshot{}, fmt.Errorf("90CN session is closed")
	}
	ctx, cancel := mailRequestContext(ctx)
	defer cancel()
	acked := make(chan error, 1)
	snapshot := make(chan protocol.MailboxSnapshot, 1)
	failure := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		switch {
		case packet.Class == protocol.ClassCommand && packet.Type == protocol.CmdMailboxOpen:
			_, ok, code := protocol.MailboxOpenResult(packet.Body)
			if ok {
				deliverAck(acked, nil)
				return
			}
			deliverAck(acked, fmt.Errorf("rejected with code %d", code))
		case packet.Class == protocol.ClassNotice && packet.Type == protocol.NotiMailboxList:
			parsed, err := protocol.ParseMailboxSnapshot(packet.Body)
			if err != nil {
				deliverErr(failure, err)
				return
			}
			select {
			case snapshot <- parsed:
			default:
			}
		}
	})
	defer cleanup()
	if err := s.client.MailboxOpen(ctx); err != nil {
		return protocol.MailboxSnapshot{}, err
	}
	var parsed protocol.MailboxSnapshot
	acknowledged := false
	loaded := false
	for !acknowledged || !loaded {
		select {
		case err := <-acked:
			if err != nil {
				foundationlog.Robotf("CN90_MAIL_OPEN_REJECTED cid=%d err=%v\n", s.selfCharacterID, err)
				return protocol.MailboxSnapshot{}, fmt.Errorf("90CN mailbox open: %w", err)
			}
			acknowledged = true
		case parsed = <-snapshot:
			loaded = true
		case err := <-failure:
			return protocol.MailboxSnapshot{}, err
		case <-ctx.Done():
			return protocol.MailboxSnapshot{}, fmt.Errorf("90CN mailbox open response: %w", ctx.Err())
		case <-s.Done():
			return protocol.MailboxSnapshot{}, fmt.Errorf("90CN session ended during mailbox open")
		}
	}
	return parsed, nil
}

// MailboxClaim claims the named objects and returns the rows the server
// settled. An empty request is a no-op: the server rejects a zero-count op95
// before dispatch, and there is nothing to ask for.
//
// Objects that are already gone are skipped rather than rejected, so a batch
// built from a stale snapshot still settles whatever is left. The server only
// reports a failure when nothing in the batch was claimable, which arrives here
// as a rejection with code 4 -- the same code an attachment that does not fit
// the bag produces, so the caller has to read it together with the snapshot it
// claimed from.
func (s *Session) MailboxClaim(ctx context.Context, claimObjectIDs []uint32) ([]protocol.MailboxClaimResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("90CN session is closed")
	}
	if len(claimObjectIDs) == 0 {
		return nil, nil
	}
	ctx, cancel := mailRequestContext(ctx)
	defer cancel()
	settled := make(chan []protocol.MailboxClaimResult, 1)
	failure := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdMailboxExtractItem {
			return
		}
		claimed, err := protocol.MailboxExtractResult(packet.Body)
		if err != nil {
			deliverErr(failure, err)
			return
		}
		select {
		case settled <- claimed:
		default:
		}
	})
	defer cleanup()
	if err := s.client.MailboxClaim(ctx, claimObjectIDs); err != nil {
		return nil, err
	}
	select {
	case claimed := <-settled:
		foundationlog.Robotf("CN90_MAIL_CLAIMED cid=%d requested=%d settled=%d\n", s.selfCharacterID, len(claimObjectIDs), len(claimed))
		return claimed, nil
	case err := <-failure:
		foundationlog.Robotf("CN90_MAIL_CLAIM_REJECTED cid=%d requested=%d err=%v\n", s.selfCharacterID, len(claimObjectIDs), err)
		return nil, fmt.Errorf("90CN mailbox claim: %w", err)
	case <-ctx.Done():
		return nil, fmt.Errorf("90CN mailbox claim response: %w", ctx.Err())
	case <-s.Done():
		return nil, fmt.Errorf("90CN session ended during mailbox claim")
	}
}

// MailboxDelete removes the named letters (op134 status 0). The server also
// pushes a class0/0x62 removal for each, which the session does not consume:
// the acknowledgement already carries the outcome, and the notification only
// tells a mailbox UI to drop rows it is showing.
func (s *Session) MailboxDelete(ctx context.Context, mailIDs []uint32) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN session is closed")
	}
	if len(mailIDs) == 0 {
		return nil
	}
	ctx, cancel := mailRequestContext(ctx)
	defer cancel()
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdChangeLetterStat {
			return
		}
		ok, code := protocol.MailboxAckResult(packet.Body)
		if ok {
			deliverAck(ack, nil)
			return
		}
		deliverAck(ack, fmt.Errorf("rejected with code %d", code))
	})
	defer cleanup()
	if err := s.client.MailboxChangeState(ctx, mailIDs, protocol.MailboxStatusDelete); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			foundationlog.Robotf("CN90_MAIL_DELETE_REJECTED cid=%d count=%d err=%v\n", s.selfCharacterID, len(mailIDs), err)
			return fmt.Errorf("90CN mailbox delete: %w", err)
		}
		foundationlog.Robotf("CN90_MAIL_DELETED cid=%d count=%d\n", s.selfCharacterID, len(mailIDs))
		return nil
	case <-ctx.Done():
		return fmt.Errorf("90CN mailbox delete confirmation: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during mailbox delete")
	}
}

// mailRequestContext bounds one exchange without shortening a caller's own
// deadline.
func mailRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, mailRequestTimeout)
}
