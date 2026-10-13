package cn90

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// newMailTestSession builds one session whose client speaks over an in-memory
// pipe, so a test plays the server side of the exchange.
func newMailTestSession(t *testing.T) (*Session, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session, serverConn
}

func mailTestAppendU32(body []byte, value uint32) []byte {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return append(body, encoded[:]...)
}

// mailTestAppendSummary writes one class0/0x61 summary row the way the server's
// mailboxBodyWriter does. The fixed block ahead of the durable row duplicates
// six of that row's own fields, and the parser only accepts a plain row whose
// copies agree, so both halves are built from the same values.
func mailTestAppendSummary(body []byte, claimObjectID, gold, itemID uint32, hasItem bool, mailID uint32) []byte {
	count := uint32(0)
	if hasItem {
		count = 1
	}
	raw := make([]byte, protocol.MailboxItemRawSize)
	binary.LittleEndian.PutUint32(raw[2:6], itemID)
	binary.LittleEndian.PutUint32(raw[6:10], count)

	body = mailTestAppendU32(body, claimObjectID) // claim object
	body = mailTestAppendU32(body, 0)             // sender id
	body = mailTestAppendU32(body, 0)             // sender name length
	body = mailTestAppendU32(body, gold)
	body = mailTestAppendU32(body, itemID)
	if hasItem {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	body = mailTestAppendU32(body, count)  // item count
	body = append(body, 0, 0)              // bound u16
	body = append(body, 0)                 // bound byte
	body = mailTestAppendU32(body, 0)      // bound u32
	body = append(body, 0, 0)              // bound bytes
	body = append(body, 0, 0)              // bound u16
	body = append(body, raw...)            // durable item row
	body = mailTestAppendU32(body, 0)      // remaining seconds
	body = mailTestAppendU32(body, mailID) // mail id
	return append(body, 0)                 // mail type
}

// mailTestSnapshot builds a class0/0x61 body with no detail rows.
func mailTestSnapshot(summaries ...[]byte) []byte {
	body := []byte{byte(len(summaries)), 0}
	for _, summary := range summaries {
		body = append(body, summary...)
	}
	body = append(body, 0, 0) // notLoaded
	return append(body, 0, 0) // no details
}

func TestSessionMailboxOpenWaitsForAckAndSnapshot(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		cmd, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || cmd != 1 || typ != protocol.CmdMailboxOpen {
			return
		}
		if len(body) != 0 {
			t.Errorf("op96 body length = %d, want the empty body", len(body))
		}
		// The server answers on two channels: the class1 acknowledgement, then
		// the class0 snapshot that actually carries the rows.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdMailboxOpen, Body: []byte{1, 0, 0}})
		session.dispatchPacket(protocol.Packet{
			Class: protocol.ClassNotice,
			Type:  protocol.NotiMailboxList,
			Body:  mailTestSnapshot(mailTestAppendSummary(nil, 7001, 0, 2600021, true, 55)),
		})
	}()
	snapshot, err := session.MailboxOpen(context.Background())
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	if len(snapshot.Summaries) != 1 || snapshot.Summaries[0].ClaimObjectID != 7001 {
		t.Fatalf("snapshot = %+v, want one summary claiming object 7001", snapshot)
	}
	if claimable := protocol.MailboxClaimableIDs(snapshot); len(claimable) != 1 || claimable[0] != 7001 {
		t.Fatalf("claimable = %v, want [7001]", claimable)
	}
}

func TestSessionMailboxOpenReportsTheRejectionCode(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		if _, _, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok {
			return
		}
		// A rejection arrives as the class1 envelope alone: waiting for the
		// snapshot it will never send would burn the whole request timeout.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdMailboxOpen, Body: []byte{0, 7}})
	}()
	start := time.Now()
	_, err := session.MailboxOpen(context.Background())
	if err == nil || !strings.Contains(err.Error(), "code 7") {
		t.Fatalf("open error = %v, want a rejection naming code 7", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("open waited %s on a rejection, want an immediate answer", elapsed)
	}
}

func TestSessionMailboxClaimSkipsAnEmptyRequest(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	claimed, err := session.MailboxClaim(context.Background(), nil)
	if err != nil || claimed != nil {
		t.Fatalf("empty claim = (%v,%v), want (nil,nil)", claimed, err)
	}
	// Nothing may reach the wire: the server rejects a zero-count op95 before
	// dispatch, so sending one would only produce an error frame.
	if err := serverConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := readTestLegacyFrame(serverConn, 100*time.Millisecond); ok {
		t.Fatal("an empty claim still sent a frame")
	}
}

func TestSessionMailboxClaimReadsTheSettledRows(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		cmd, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || cmd != 1 || typ != protocol.CmdMailboxExtractItem {
			return
		}
		if count := binary.LittleEndian.Uint32(body[0:4]); count != 2 {
			t.Errorf("op95 count = %d, want 2", count)
		}
		if first := binary.LittleEndian.Uint32(body[4:8]); first != 7001 {
			t.Errorf("op95 first id = %d, want 7001", first)
		}
		ack := []byte{1, 1, 0, 0, 0}
		ack = mailTestAppendU32(ack, 55)
		ack = mailTestAppendU32(ack, 7001)
		ack = mailTestAppendU32(ack, 0)
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdMailboxExtractItem, Body: ack})
	}()
	claimed, err := session.MailboxClaim(context.Background(), []uint32{7001, 7002})
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if len(claimed) != 1 || claimed[0].MailID != 55 || claimed[0].ClaimObjectID != 7001 {
		t.Fatalf("claimed = %+v, want one row for mail 55 object 7001", claimed)
	}
}

func TestSessionMailboxDeleteSendsTheDeleteStatus(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	go func() {
		cmd, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || cmd != 1 || typ != protocol.CmdChangeLetterStat {
			return
		}
		if count := binary.LittleEndian.Uint32(body[0:4]); count != 1 {
			t.Errorf("op134 count = %d, want 1", count)
		}
		if id := binary.LittleEndian.Uint32(body[4:8]); id != 55 {
			t.Errorf("op134 id = %d, want 55", id)
		}
		if status := binary.LittleEndian.Uint16(body[8:10]); status != protocol.MailboxStatusDelete {
			t.Errorf("op134 status = %d, want the delete status", status)
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdChangeLetterStat, Body: []byte{1, 0}})
	}()
	if err := session.MailboxDelete(context.Background(), []uint32{55}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
}

func TestSessionMailboxDeleteSkipsAnEmptyRequest(t *testing.T) {
	session, serverConn := newMailTestSession(t)
	if err := session.MailboxDelete(context.Background(), nil); err != nil {
		t.Fatalf("empty delete = %v, want nil", err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := readTestLegacyFrame(serverConn, 100*time.Millisecond); ok {
		t.Fatal("an empty delete still sent a frame")
	}
}
