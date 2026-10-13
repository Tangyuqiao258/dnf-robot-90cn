package cn90

import (
	"context"
	"fmt"
	"time"

	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// The mailbox behavior of one robot session: collect what the auction house and
// the server delivered, and keep the box from growing without bound. Server
// authority is the server tree's `internal/modules/dnf/mail` (handler.go
// dispatch, owner.go Claim and ChangeState, body.go writers).
//
// Two properties of the server shape the round:
//
//   - Auction settlement arrives as mail rather than in the auction reply, so
//     the stack a robot bought and the gold it sold for both wait in the box
//     until something opens it. Claiming is what actually delivers them.
//   - A claim republishes nothing and leaves the letter in place. The round
//     therefore reopens after claiming: the second snapshot is the only honest
//     answer to "what is left", and it is what separates a letter that is fully
//     drained from one whose attachment did not fit the bag.

// mailRoundTimeout bounds one whole round: the opening snapshot, the claim
// batches it decides on, and the reopening snapshot that follows them.
const mailRoundTimeout = 30 * time.Second

// mailSession is the slice of the 90CN session the mail round uses.
type mailSession interface {
	MailboxOpen(context.Context) (protocol.MailboxSnapshot, error)
	MailboxClaim(context.Context, []uint32) ([]protocol.MailboxClaimResult, error)
	MailboxDelete(context.Context, []uint32) error
}

// MailRound runs one mailbox cycle for uid and reports how many objects it
// claimed and how many letters it deleted. It is a no-op for a session that is
// not on the 90CN adapter, so the scheduler can offer every backend the same
// schedule.
//
// The round takes no policy: what a robot claims is whatever the server is
// holding for it, and what it deletes is only ever a letter it just drained
// itself. Whether a robot runs this round at all is the scheduler's decision.
func (t *ActionTransport) MailRound(uid int) (int, int, error) {
	if t == nil {
		return 0, 0, fmt.Errorf("90CN mail round needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return 0, 0, err
	}
	mailbox, ok := session.(mailSession)
	if !ok {
		return 0, 0, fmt.Errorf("90CN session does not expose the mailbox protocol")
	}
	ctx, cancel := context.WithTimeout(context.Background(), mailRoundTimeout)
	defer cancel()

	snapshot, err := mailbox.MailboxOpen(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("90CN mailbox open for uid %d: %w", uid, err)
	}
	claimable := protocol.MailboxClaimableIDs(snapshot)
	claimed := 0
	settledLetters := make(map[uint32]struct{})
	for _, batch := range mailBatches(claimable) {
		rows, err := mailbox.MailboxClaim(ctx, batch)
		if err != nil {
			// The round stops here rather than pressing on: a batch is refused
			// as a whole, so the next one would almost certainly be refused for
			// the same reason. Everything already settled stays settled, and the
			// letters it came from are left alone because the reopening snapshot
			// never happens.
			return claimed, 0, fmt.Errorf("90CN mailbox claim for uid %d: %w", uid, err)
		}
		claimed += len(rows)
		for _, row := range rows {
			if row.MailID != 0 {
				settledLetters[row.MailID] = struct{}{}
			}
		}
	}
	if claimed == 0 {
		return 0, 0, nil
	}
	// Reopen. A claim empties its letter but leaves it in the box, and the ids
	// the claim answered with are not proof that nothing is left: a letter whose
	// second attachment did not fit is half claimed. Only a fresh snapshot can
	// tell the two apart, so deletion is decided from one.
	reopened, err := mailbox.MailboxOpen(ctx)
	if err != nil {
		return claimed, 0, fmt.Errorf("90CN mailbox reopen for uid %d: %w", uid, err)
	}
	var drained []uint32
	for _, mailID := range protocol.MailboxDrainedIDs(reopened) {
		if _, ours := settledLetters[mailID]; ours {
			drained = append(drained, mailID)
		}
	}
	deleted := 0
	for _, batch := range mailBatches(drained) {
		if err := mailbox.MailboxDelete(ctx, batch); err != nil {
			return claimed, deleted, fmt.Errorf("90CN mailbox delete for uid %d: %w", uid, err)
		}
		deleted += len(batch)
	}
	foundationlog.Robotf("CN90_MAIL_ROUND uid=%d claimed=%d deleted=%d\n", uid, claimed, deleted)
	return claimed, deleted, nil
}

// mailBatches splits a claim-object or letter id list into the largest batches
// op95 and op134 accept. One snapshot may offer more ids than either command
// takes at once.
func mailBatches(ids []uint32) [][]uint32 {
	if len(ids) == 0 {
		return nil
	}
	batches := make([][]uint32, 0, (len(ids)+protocol.MailboxBatchLimit-1)/protocol.MailboxBatchLimit)
	for start := 0; start < len(ids); start += protocol.MailboxBatchLimit {
		end := start + protocol.MailboxBatchLimit
		if end > len(ids) {
			end = len(ids)
		}
		batches = append(batches, ids[start:end])
	}
	return batches
}
