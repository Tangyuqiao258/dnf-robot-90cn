package cn90

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The vectors below mirror the server's own mailbox tests
// (internal/modules/dnf/mail/handler_test.go currentSendBody /
// currentMailIDList / the op96 snapshot literal), so the robot encoder is
// pinned to bytes the server already accepts.

func TestMailboxOpenBodyIsEmpty(t *testing.T) {
	if body := MailboxOpenBody(); len(body) != 0 {
		t.Fatalf("op96 body = %v, want empty", body)
	}
}

func TestMailboxSendBodyMatchesServerLayout(t *testing.T) {
	got, err := MailboxSendBody([]byte("receiver"), 50, MailAttachment{}, []byte("body"), false)
	if err != nil {
		t.Fatalf("MailboxSendBody error = %v", err)
	}
	want := []byte{
		0x08, 0x00, 0x00, 0x00, 'r', 'e', 'c', 'e', 'i', 'v', 'e', 'r',
		0x32, 0x00, 0x00, 0x00, // gold 50
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // the always-present zero attachment row
		0x04, 0x00, 0x00, 0x00, 'b', 'o', 'd', 'y',
		0x00, 0x00, 0x00, 0x00, // special
		0x00, // global
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("op94 body =\n% x\nwant\n% x", got, want)
	}
}

func TestMailboxMultiSendBodyCarriesAttachmentRows(t *testing.T) {
	got, err := MailboxMultiSendBody(
		[]byte("receiver"),
		50,
		[]MailAttachment{{ListType: 0, SlotIndex: 65, ItemID: 1001, Count: 2}},
		[]byte("same-account transfer"),
		false,
	)
	if err != nil {
		t.Fatalf("MailboxMultiSendBody error = %v", err)
	}
	want := []byte{
		0x08, 0x00, 0x00, 0x00, 'r', 'e', 'c', 'e', 'i', 'v', 'e', 'r',
		0x32, 0x00, 0x00, 0x00,
		0x01, // attachment count
		0x00, 0x41, 0x00, 0xE9, 0x03, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00,
		0x15, 0x00, 0x00, 0x00, 's', 'a', 'm', 'e', '-', 'a', 'c', 'c', 'o', 'u', 'n', 't', ' ', 't', 'r', 'a', 'n', 's', 'f', 'e', 'r',
		0x00, 0x00, 0x00, 0x00,
		0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("op315 body =\n% x\nwant\n% x", got, want)
	}
}

func TestMailboxSendBodyRejectsOversizedFields(t *testing.T) {
	long := bytes.Repeat([]byte{'a'}, MailboxRecipientMaxBytes+1)
	if _, err := MailboxSendBody(long, 0, MailAttachment{}, nil, false); err == nil {
		t.Fatal("oversized recipient was accepted")
	}
	if _, err := MailboxSendBody([]byte("x"), 0, MailAttachment{}, bytes.Repeat([]byte{'a'}, MailboxMessageMaxBytes+1), false); err == nil {
		t.Fatal("oversized message was accepted")
	}
	tooMany := make([]MailAttachment, MailboxAttachmentLimit+1)
	if _, err := MailboxMultiSendBody([]byte("x"), 0, tooMany, nil, false); err == nil {
		t.Fatal("oversized attachment set was accepted")
	}
}

func TestMailboxExtractBodyMatchesServerIDList(t *testing.T) {
	got, err := MailboxExtractBody([]uint32{2, 4})
	if err != nil {
		t.Fatalf("MailboxExtractBody error = %v", err)
	}
	want := []byte{0x02, 0, 0, 0, 0x02, 0, 0, 0, 0x04, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("op95 body = % x, want % x", got, want)
	}
	if _, err := MailboxExtractBody(nil); err == nil {
		t.Fatal("empty claim list was accepted")
	}
	if _, err := MailboxExtractBody([]uint32{0}); err == nil {
		t.Fatal("zero claim object was accepted")
	}
}

func TestMailboxChangeStateBodyMatchesServerIDList(t *testing.T) {
	got, err := MailboxChangeStateBody([]uint32{123}, MailboxStatusRead)
	if err != nil {
		t.Fatalf("MailboxChangeStateBody error = %v", err)
	}
	want := []byte{0x01, 0, 0, 0, 0x7b, 0, 0, 0, 0x02, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("op134 body = % x, want % x", got, want)
	}
}

func TestMailboxRecipientListBodyIsOneByte(t *testing.T) {
	if got := MailboxRecipientListBody(3); !bytes.Equal(got, []byte{3}) {
		t.Fatalf("op789 body = % x, want 03", got)
	}
}

func TestMailboxQueryCharacterBodyEncodesNameAndMode(t *testing.T) {
	got, err := MailboxQueryCharacterBody([]byte("hunter"), 1)
	if err != nil {
		t.Fatalf("MailboxQueryCharacterBody error = %v", err)
	}
	want := []byte{0x06, 0, 0, 0, 'h', 'u', 'n', 't', 'e', 'r', 0x01}
	if !bytes.Equal(got, want) {
		t.Fatalf("op324 body = % x, want % x", got, want)
	}
	if _, err := MailboxQueryCharacterBody(nil, 0); err == nil {
		t.Fatal("empty query name was accepted")
	}
}

func TestMailboxOpenResultReadsAckAndErrors(t *testing.T) {
	notLoaded, ok, code := MailboxOpenResult([]byte{0x01, 0x00, 0x00})
	if !ok || notLoaded != 0 || code != 0 {
		t.Fatalf("op96 ACK = (%d,%t,%d), want (0,true,0)", notLoaded, ok, code)
	}
	if _, ok, code := MailboxOpenResult([]byte{0x00, 0x07}); ok || code != 7 {
		t.Fatalf("op96 error = (%t,%d), want (false,7)", ok, code)
	}
}

func TestMailboxAckResultReadsEnvelope(t *testing.T) {
	if ok, _ := MailboxAckResult([]byte{0x01}); !ok {
		t.Fatal("single-byte success was not accepted")
	}
	if ok, code := MailboxAckResult([]byte{0x00, 0x04}); ok || code != 4 {
		t.Fatalf("error envelope = (%t,%d), want (false,4)", ok, code)
	}
}

func TestMailboxExtractResultReadsClaimedRows(t *testing.T) {
	// The server's own buildExtractAck layout for one gold row and one
	// attachment row: {0x01, u32 count, count * (u32 mail, u32 claim, u32 status)}.
	body := []byte{
		0x01, 0x02, 0x00, 0x00, 0x00,
		0x11, 0x00, 0x00, 0x00, 0x11, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x11, 0x00, 0x00, 0x00, 0x22, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	claimed, err := MailboxExtractResult(body)
	if err != nil {
		t.Fatalf("MailboxExtractResult error = %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed rows = %d, want 2", len(claimed))
	}
	if claimed[0] != (MailboxClaimResult{MailID: 0x11, ClaimObjectID: 0x11}) {
		t.Fatalf("gold row = %+v", claimed[0])
	}
	if claimed[1] != (MailboxClaimResult{MailID: 0x11, ClaimObjectID: 0x22}) {
		t.Fatalf("attachment row = %+v", claimed[1])
	}
}

func TestMailboxExtractResultReadsThePerObjectStatus(t *testing.T) {
	// The status is the low word of the trailing u32; the high word is reserved
	// and must not be folded into it.
	body := []byte{0x01, 0x01, 0x00, 0x00, 0x00, 0x05, 0x00, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00}
	claimed, err := MailboxExtractResult(body)
	if err != nil {
		t.Fatalf("MailboxExtractResult error = %v", err)
	}
	if len(claimed) != 1 || claimed[0].Status != 4 {
		t.Fatalf("claimed = %+v, want one row with status 4", claimed)
	}
}

func TestMailboxExtractResultRejectsRejectionAndDamage(t *testing.T) {
	if _, err := MailboxExtractResult([]byte{0x00, 0x04}); err == nil {
		t.Fatal("a rejected extract was parsed as success")
	}
	// A row count that disagrees with the body is damage, not a partial result.
	if _, err := MailboxExtractResult([]byte{0x01, 0x02, 0x00, 0x00, 0x00, 0x11, 0x00, 0x00, 0x00, 0x11, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}); err == nil {
		t.Fatal("a truncated row list was accepted")
	}
	if _, err := MailboxExtractResult([]byte{0x01}); err == nil {
		t.Fatal("a success envelope with no count was accepted")
	}
	if _, err := MailboxExtractResult(append([]byte{0x01, 0x01, 0x00, 0x00, 0x00}, make([]byte, 13)...)); err == nil {
		t.Fatal("a padded row list was accepted")
	}
}

func TestMailboxExtractResultRefusesAnOversizedRowCount(t *testing.T) {
	// A corrupted length must not become an allocation: the server never
	// answers with more rows than MailboxBatchLimit.
	body := []byte{0x01, 0xff, 0xff, 0xff, 0xff}
	if _, err := MailboxExtractResult(body); err == nil {
		t.Fatal("an oversized row count was accepted")
	}
}

// mailboxTestSnapshotOf builds a snapshot from summary values, skipping the
// wire encoding: these two projections read fields, not bytes.
func mailboxTestSnapshotOf(summaries ...MailboxSummary) MailboxSnapshot {
	return MailboxSnapshot{Summaries: summaries}
}

func TestMailboxClaimableIDsSkipsEmptyReceiptRows(t *testing.T) {
	snapshot := mailboxTestSnapshotOf(
		// A letter with one attachment: the attachment row is the claim target.
		MailboxSummary{ClaimObjectID: 7001, MailID: 55, ItemID: 2600021, HasItem: true, ItemCount: 3},
		// A second attachment of the same letter: another claim target, and the
		// only one carrying the letter's gold.
		MailboxSummary{ClaimObjectID: 7002, MailID: 55, ItemID: 2600022, HasItem: true, ItemCount: 1, Gold: 500},
		// A gold-only letter: the claim object is the letter's own wire id.
		MailboxSummary{ClaimObjectID: 56, Gold: 1200},
		// An empty receipt: a claimed letter or a text-only one. Neither has
		// anything to hand over, so it must not be offered.
		MailboxSummary{ClaimObjectID: 57},
	)
	got := MailboxClaimableIDs(snapshot)
	want := []uint32{7001, 7002, 56}
	if len(got) != len(want) {
		t.Fatalf("claimable = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claimable = %v, want %v", got, want)
		}
	}
}

func TestMailboxClaimableIDsDropsDuplicatesAndZeroIDs(t *testing.T) {
	got := MailboxClaimableIDs(mailboxTestSnapshotOf(
		MailboxSummary{ClaimObjectID: 7001, HasItem: true},
		MailboxSummary{ClaimObjectID: 7001, HasItem: true},
		MailboxSummary{ClaimObjectID: 0, HasItem: true},
	))
	if len(got) != 1 || got[0] != 7001 {
		t.Fatalf("claimable = %v, want [7001]", got)
	}
	if empty := MailboxClaimableIDs(MailboxSnapshot{}); len(empty) != 0 {
		t.Fatalf("empty snapshot claimable = %v, want none", empty)
	}
}

func TestMailboxDrainedIDsNamesLettersByEitherField(t *testing.T) {
	snapshot := mailboxTestSnapshotOf(
		// A letter with a pending attachment is not drained, however many rows
		// it spreads over.
		MailboxSummary{ClaimObjectID: 7001, MailID: 55, HasItem: true},
		MailboxSummary{ClaimObjectID: 7002, MailID: 55, HasItem: true},
		// A letter whose only row is a receipt carries its id in the claim
		// object field instead, because the mail field is zero there.
		MailboxSummary{ClaimObjectID: 56},
		// A text-only letter is projected the same way and is reported too; the
		// caller decides whether to delete it.
		MailboxSummary{ClaimObjectID: 57},
		// A letter that only holds gold is still pending.
		MailboxSummary{ClaimObjectID: 58, Gold: 1200},
	)
	got := MailboxDrainedIDs(snapshot)
	want := []uint32{56, 57}
	if len(got) != len(want) {
		t.Fatalf("drained = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("drained = %v, want %v", got, want)
		}
	}
}

func TestMailboxDrainedIDsStaysEmptyWhileAnythingIsPending(t *testing.T) {
	// A claimed attachment and a pending gold row on the same letter: the
	// settled row must not mark the letter drained while the gold is unclaimed.
	got := MailboxDrainedIDs(mailboxTestSnapshotOf(
		MailboxSummary{ClaimObjectID: 7001, MailID: 55, Gold: 900},
	))
	if len(got) != 0 {
		t.Fatalf("drained = %v, want none while the gold is unclaimed", got)
	}
}

func TestMailboxAlarmCountReadsTheTwoByteWord(t *testing.T) {
	// The server's own BuildAlarmNotification(513) vector.
	count, ok := MailboxAlarmCount([]byte{0x01, 0x02})
	if !ok || count != 513 {
		t.Fatalf("alarm = (%d,%t), want (513,true)", count, ok)
	}
	if _, ok := MailboxAlarmCount([]byte{0x01, 0x02, 0x03}); ok {
		t.Fatal("a three-byte alarm body was accepted")
	}
}

func TestMailboxQueryResultParsesNameGrowLevelJob(t *testing.T) {
	body := []byte{0x01, 0x02, 0, 0, 0, 'A', 'B', 0x13, 0x2a, 0x00, 0x07, 0x00, 0x01, 0x00, 0x00}
	got, err := MailboxQueryResult(body)
	if err != nil {
		t.Fatalf("MailboxQueryResult error = %v", err)
	}
	if got.Name != "AB" || got.Grow != 0x13 || got.Level != 42 || got.Job != 7 {
		t.Fatalf("query = %+v, want name=AB grow=19 level=42 job=7", got)
	}
	if _, err := MailboxQueryResult([]byte{0x00, 0x03}); err == nil {
		t.Fatal("query error envelope was accepted as a result")
	}
}

func TestMailboxRemovedIDs(t *testing.T) {
	ids, err := MailboxRemovedIDs([]byte{0x02, 0, 0, 0, 0x07, 0, 0, 0, 0x09, 0, 0, 0})
	if err != nil {
		t.Fatalf("MailboxRemovedIDs error = %v", err)
	}
	if len(ids) != 2 || ids[0] != 7 || ids[1] != 9 {
		t.Fatalf("removed ids = %v, want [7 9]", ids)
	}
	if _, err := MailboxRemovedIDs([]byte{0x02, 0, 0, 0, 0x07, 0, 0, 0}); err == nil {
		t.Fatal("truncated removal notification was accepted")
	}
}

// mailboxTestRawRow mirrors the server's mailboxAttachmentRaw so the summary
// vector is a faithful re-encoding of a durable row.
func mailboxTestRawRow(itemID, count uint32) []byte {
	raw := make([]byte, MailboxItemRawSize)
	binary.LittleEndian.PutUint32(raw[2:6], itemID)
	binary.LittleEndian.PutUint32(raw[6:10], count)
	binary.LittleEndian.PutUint16(raw[0x0b:0x0d], 0x1234)
	raw[0x0d] = 1
	binary.LittleEndian.PutUint32(raw[0x0e:0x12], 0xDEADBEEF)
	raw[0x12] = 7
	raw[0x13] = 9
	binary.LittleEndian.PutUint16(raw[0x14:0x16], 0x0042)
	binary.LittleEndian.PutUint32(raw[0x38:0x3c], 0x60000000)
	return raw
}

func appendMailboxTestSummary(body []byte, claimObjectID, senderID uint32, senderName []byte, gold, itemID uint32, hasItem bool, raw []byte, remaining uint32, mailID uint32, mailType byte) []byte {
	body = appendU32(body, claimObjectID)
	body = appendU32(body, senderID)
	body = appendU32(body, uint32(len(senderName)))
	body = append(body, senderName...)
	body = appendU32(body, gold)
	body = appendU32(body, itemID)
	if hasItem {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	body = appendU32(body, binary.LittleEndian.Uint32(raw[6:10]))
	body = append(body, raw[0x0b:0x0d]...)
	body = append(body, raw[0x0d])
	body = append(body, raw[0x0e:0x12]...)
	body = append(body, raw[0x12], raw[0x13])
	body = append(body, raw[0x14:0x16]...)
	body = append(body, raw...)
	body = appendU32(body, remaining)
	body = appendU32(body, mailID)
	return append(body, mailType)
}

func TestParseMailboxSnapshotMatchesServerLiteral(t *testing.T) {
	// The server test's text-only vector: one zero seed summary and one detail.
	body := []byte{0x01, 0x00}
	body = appendMailboxTestSummary(body, 1, 0, nil, 0, 0, false, make([]byte, MailboxItemRawSize), 0, 0, 0)
	body = append(body, 0, 0) // notLoaded
	body = append(body, 1, 0) // one detail
	body = appendU32(body, 1)
	body = appendU32(body, 0)
	body = appendU32(body, 0)
	body = appendU32(body, 0)
	body = appendU32(body, 0)
	body = append(body, 2, 0, 0)

	got, err := ParseMailboxSnapshot(body)
	if err != nil {
		t.Fatalf("ParseMailboxSnapshot error = %v", err)
	}
	if got.PageFlag != 0 || len(got.Summaries) != 1 || len(got.Details) != 1 {
		t.Fatalf("snapshot = %+v, want one summary and one detail", got)
	}
	summary := got.Summaries[0]
	if summary.ClaimObjectID != 1 || summary.HasItem || summary.ItemCount != 0 || len(summary.ItemRaw) != MailboxItemRawSize {
		t.Fatalf("seed summary = %+v", summary)
	}
	if summary.MailID != 0 || summary.Bind() || summary.ExpireAt() != 0 {
		t.Fatalf("seed summary identity = %+v", summary)
	}
	detail := got.Details[0]
	if detail.MailID != 1 || detail.LetterStat != 2 || detail.SenderName != "" || detail.Body != "" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestParseMailboxSnapshotReadsAttachmentSummary(t *testing.T) {
	raw := mailboxTestRawRow(1001, 3)
	body := []byte{0x02, 0x00}
	body = appendMailboxTestSummary(body, 11, 88, []byte("sender"), 500, 1001, true, raw, 86400, 4321, 0)
	body = appendMailboxTestSummary(body, 12, 88, []byte("sender"), 0, 1001, true, raw, 86400, 4321, 0)
	body = append(body, 3, 0) // notLoaded
	body = append(body, 0, 0) // no detail rows

	got, err := ParseMailboxSnapshot(body)
	if err != nil {
		t.Fatalf("ParseMailboxSnapshot error = %v", err)
	}
	if len(got.Summaries) != 2 || got.NotLoaded != 3 {
		t.Fatalf("snapshot = %+v, want two summaries and notLoaded=3", got)
	}
	first := got.Summaries[0]
	if first.ClaimObjectID != 11 || first.MailID != 4321 || first.Gold != 500 || !first.HasItem || first.ItemID != 1001 || first.ItemCount != 3 {
		t.Fatalf("summary = %+v", first)
	}
	if !first.Bind() || first.ExpireAt() != 0x60000000 || first.RemainingSecs != 86400 {
		t.Fatalf("summary item row = bind %t expire %#x remaining %d", first.Bind(), first.ExpireAt(), first.RemainingSecs)
	}
	if got.Summaries[1].ClaimObjectID != 12 || got.Summaries[1].Gold != 0 {
		t.Fatalf("second summary = %+v", got.Summaries[1])
	}
}

func TestParseMailboxSnapshotToleratesCreatureExtension(t *testing.T) {
	raw := mailboxTestRawRow(1001, 3)
	plain := []byte{0x01, 0x00}
	plain = appendMailboxTestSummary(plain, 11, 88, nil, 0, 1001, true, raw, 60, 4321, 0)
	plain = append(plain, 0, 0, 0, 0)

	// The creature branch writes a zero DWORD and a zero flag before the row.
	extended := []byte{0x01, 0x00}
	extended = appendU32(extended, 11)
	extended = appendU32(extended, 88)
	extended = appendU32(extended, 0)
	extended = appendU32(extended, 0)
	extended = appendU32(extended, 1001)
	extended = append(extended, 1)
	extended = appendU32(extended, binary.LittleEndian.Uint32(raw[6:10]))
	extended = append(extended, raw[0x0b:0x0d]...)
	extended = append(extended, raw[0x0d])
	extended = append(extended, raw[0x0e:0x12]...)
	extended = append(extended, raw[0x12], raw[0x13])
	extended = append(extended, raw[0x14:0x16]...)
	extended = appendU32(extended, 0)
	extended = append(extended, 0)
	extended = append(extended, raw...)
	extended = appendU32(extended, 60)
	extended = appendU32(extended, 4321)
	extended = append(extended, 0)          // mail type
	extended = append(extended, 0, 0, 0, 0) // notLoaded and detail count

	plainSnapshot, err := ParseMailboxSnapshot(plain)
	if err != nil {
		t.Fatalf("plain snapshot error = %v", err)
	}
	extendedSnapshot, err := ParseMailboxSnapshot(extended)
	if err != nil {
		t.Fatalf("extended snapshot error = %v", err)
	}
	if len(plainSnapshot.Summaries) != 1 || len(extendedSnapshot.Summaries) != 1 {
		t.Fatalf("summary counts = %d plain, %d extended", len(plainSnapshot.Summaries), len(extendedSnapshot.Summaries))
	}
	if !bytes.Equal(plainSnapshot.Summaries[0].ItemRaw, extendedSnapshot.Summaries[0].ItemRaw) {
		t.Fatal("the creature extension shifted the durable item row")
	}
	if plainSnapshot.Summaries[0].MailID != 4321 || extendedSnapshot.Summaries[0].MailID != 4321 {
		t.Fatal("the creature extension shifted the summary tail")
	}
}

func TestParseMailboxSnapshotRejectsTruncationAndTrailingBytes(t *testing.T) {
	body := []byte{0x01, 0x00}
	body = appendMailboxTestSummary(body, 1, 0, nil, 0, 0, false, make([]byte, MailboxItemRawSize), 0, 0, 0)
	body = append(body, 0, 0, 0, 0)
	if _, err := ParseMailboxSnapshot(body[:len(body)-1]); err == nil {
		t.Fatal("a truncated snapshot was accepted")
	}
	if _, err := ParseMailboxSnapshot(append(append([]byte(nil), body...), 0x00)); err == nil {
		t.Fatal("a snapshot with a trailing byte was accepted")
	}
}

func TestEncodeMailboxTextUsesGB18030AndEnforcesTheCap(t *testing.T) {
	// GB18030 renders 中文 as four bytes; the server's encoder does the same.
	encoded, err := EncodeMailboxText("中文", MailboxRecipientMaxBytes)
	if err != nil {
		t.Fatalf("EncodeMailboxText error = %v", err)
	}
	if !bytes.Equal(encoded, []byte{0xD6, 0xD0, 0xCE, 0xC4}) {
		t.Fatalf("encoded = % x, want d6d0 cec4", encoded)
	}
	if _, err := EncodeMailboxText("中文", 3); err == nil {
		t.Fatal("an over-cap string was accepted")
	}
}
