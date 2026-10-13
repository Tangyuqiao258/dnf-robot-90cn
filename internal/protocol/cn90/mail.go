package cn90

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// The 90CN letter/mailbox family. Server authority: the server tree's
// `internal/modules/dnf/mail` (request.go decoders, body.go writers); the
// opcode table marks the whole family AlignedSupportDirect against the current
// NoPack.exe readers. Text is GB18030 in both directions and a length or
// encoding violation fails the request instead of degrading to mojibake.
//
// Wire summary:
//
//	op96  MailboxOpen        body empty            -> class1/op96 {0x01, u16 notLoaded}
//	                                                 + class0/0x61 full snapshot
//	op94  MailboxSend        see MailboxSendBody   -> class1/op94 {0x01}
//	op315 MultiMailboxSend   multi-attachment form -> class1/op315 {0x01}
//	op95  MailboxExtractItem claim objects         -> class1/op95 {0x01, u32 n, n*12}
//	op134 ChangeLetterStat   ids + status          -> class1/op134 {0x01, u32 n, n*6}
//	op324 QueryCharacMailbox name + mode           -> class1/op324 {0x01, dstr, ...}
//	op789 MailboxRecipients  one server id byte    -> class0/718
//
// Every failure reply of the request/response family is the two-byte envelope
// {0x00, code}; op789 answers malformed requests with nothing at all.

const (
	// MailboxRecipientMaxBytes is the server's cap on a recipient name and on a
	// sender name echoed back in a snapshot (maxMailboxRecipientBytes).
	MailboxRecipientMaxBytes = 30
	// MailboxMessageMaxBytes caps the letter body (maxMailboxMessageBytes).
	MailboxMessageMaxBytes = 512
	// MailboxAttachmentLimit is the largest attachment set op315 accepts.
	MailboxAttachmentLimit = 10
	// MailboxBatchLimit is the largest id batch op95/op134 accept.
	MailboxBatchLimit = 100
	// MailboxItemRawSize is the fixed 119-byte durable item row. Its offset
	// 0x0b..0x16 fields are duplicated in the summary's fixed block, which is
	// what lets the parser tell an extended row from a plain one.
	MailboxItemRawSize = 0x77
)

// Letter state values for op134 (the server's letterStat: 1 unread, 2 read,
// 3 saved). Status 0 deletes and additionally pushes class0/0x62.
const (
	MailboxStatusDelete uint16 = 0
	MailboxStatusRead   uint16 = 2
	MailboxStatusSave   uint16 = 3
)

// MailAttachment is the exact op94/op315 attachment row. ItemID and Count are
// validation values: the server resolves the durable item from (ListType,
// SlotIndex) and rejects the send when the item there does not match.
type MailAttachment struct {
	ListType  byte
	SlotIndex uint16
	ItemID    uint32
	Count     uint32
}

// MailboxOpenBody is the op96 request: the body must be empty, any byte is
// rejected before dispatch.
func MailboxOpenBody() []byte {
	return nil
}

// MailboxSendBody builds op94, whose composer always writes exactly one
// attachment row; an all-zero row means "no attachment". A zero struct value
// therefore sends gold/text only.
func MailboxSendBody(recipientRaw []byte, gold uint32, attachment MailAttachment, messageRaw []byte, global bool) ([]byte, error) {
	body, err := mailboxSendPrefix(recipientRaw, gold, messageRaw, global)
	if err != nil {
		return nil, err
	}
	body = appendMailboxAttachment(body, attachment)
	return append(body, mailboxSendTail(messageRaw, global)...), nil
}

// MailboxMultiSendBody builds op315: a u8 attachment count followed by that
// many rows. Opcode 315 is the multi-attachment variant, not a multi-recipient
// one.
func MailboxMultiSendBody(recipientRaw []byte, gold uint32, attachments []MailAttachment, messageRaw []byte, global bool) ([]byte, error) {
	if len(attachments) > MailboxAttachmentLimit {
		return nil, fmt.Errorf("cn90 mailbox attachment count %d exceeds %d", len(attachments), MailboxAttachmentLimit)
	}
	body, err := mailboxSendPrefix(recipientRaw, gold, messageRaw, global)
	if err != nil {
		return nil, err
	}
	body = append(body, byte(len(attachments)))
	for _, attachment := range attachments {
		body = appendMailboxAttachment(body, attachment)
	}
	return append(body, mailboxSendTail(messageRaw, global)...), nil
}

// mailboxSendPrefix writes the fields that precede the attachment rows. The
// message and flags follow them, so they are validated here and appended by
// mailboxSendTail.
func mailboxSendPrefix(recipientRaw []byte, gold uint32, messageRaw []byte, _ bool) ([]byte, error) {
	if len(recipientRaw) == 0 || len(recipientRaw) > MailboxRecipientMaxBytes {
		return nil, fmt.Errorf("cn90 mailbox recipient must be 1..%d bytes, got %d", MailboxRecipientMaxBytes, len(recipientRaw))
	}
	if len(messageRaw) > MailboxMessageMaxBytes {
		return nil, fmt.Errorf("cn90 mailbox body must be at most %d bytes, got %d", MailboxMessageMaxBytes, len(messageRaw))
	}
	body := make([]byte, 0, 4+len(recipientRaw)+4+1+12+4+len(messageRaw)+4+1)
	body = appendDSTR(body, recipientRaw)
	return appendU32(body, gold), nil
}

// mailboxAttachmentRowBytes is the on-wire attachment row: u8 list type, u16
// slot, u32 item id, u32 count. The server's decoder reads exactly these
// eleven bytes and then requires the tail to start immediately, so a pad byte
// here would be read as the message length.
const mailboxAttachmentRowBytes = 11

func appendMailboxAttachment(body []byte, attachment MailAttachment) []byte {
	var row [mailboxAttachmentRowBytes]byte
	row[0] = attachment.ListType
	binary.LittleEndian.PutUint16(row[1:3], attachment.SlotIndex)
	binary.LittleEndian.PutUint32(row[3:7], attachment.ItemID)
	binary.LittleEndian.PutUint32(row[7:11], attachment.Count)
	return append(body, row[:]...)
}

// mailboxSendTail returns the trailing message/special/global fields. It is
// separate from the prefix because the attachment rows sit between the two.
func mailboxSendTail(messageRaw []byte, global bool) []byte {
	body := appendDSTR(nil, messageRaw)
	body = appendU32(body, 0) // special: the server accepts only zero
	if global {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	return body
}

// MailboxExtractBody builds op95: u32 count followed by the claim-object ids
// from the snapshot summaries (one per attachment, the mail id for gold or
// text-only rows).
func MailboxExtractBody(claimObjectIDs []uint32) ([]byte, error) {
	return mailboxIDListBody(claimObjectIDs, nil)
}

// MailboxChangeStateBody builds op134: u32 count, the mail ids, then one u16
// status. Status 0 deletes, 2 marks read and 3 saves.
func MailboxChangeStateBody(mailIDs []uint32, status uint16) ([]byte, error) {
	tail := make([]byte, 2)
	binary.LittleEndian.PutUint16(tail, status)
	return mailboxIDListBody(mailIDs, tail)
}

func mailboxIDListBody(ids []uint32, tail []byte) ([]byte, error) {
	if len(ids) == 0 || len(ids) > MailboxBatchLimit {
		return nil, fmt.Errorf("cn90 mailbox id count %d is outside 1..%d", len(ids), MailboxBatchLimit)
	}
	body := make([]byte, 0, 4+len(ids)*4+len(tail))
	body = appendU32(body, uint32(len(ids)))
	for _, id := range ids {
		if id == 0 {
			return nil, fmt.Errorf("cn90 mailbox id must not be zero")
		}
		body = appendU32(body, id)
	}
	return append(body, tail...), nil
}

// MailboxQueryCharacterBody builds op324: the recipient name then a mode byte.
func MailboxQueryCharacterBody(nameRaw []byte, mode byte) ([]byte, error) {
	if len(nameRaw) == 0 || len(nameRaw) > MailboxRecipientMaxBytes {
		return nil, fmt.Errorf("cn90 mailbox query name must be 1..%d bytes, got %d", MailboxRecipientMaxBytes, len(nameRaw))
	}
	body := appendDSTR(nil, nameRaw)
	return append(body, mode), nil
}

// MailboxRecipientListBody builds op789, whose body is exactly one server id
// byte. A malformed length gets no reply at all, so callers must not pad it.
func MailboxRecipientListBody(serverID byte) []byte {
	return []byte{serverID}
}

// MailboxAckResult reads the {success, code} envelope shared by op94/op95/
// op134/op315/op324. Success is the single byte 0x01.
func MailboxAckResult(body []byte) (bool, byte) {
	if len(body) >= 1 && body[0] == 1 {
		return true, 0
	}
	if len(body) >= 2 {
		return false, body[1]
	}
	return false, 0
}

// MailboxOpenResult reads the op96 ACK `{0x01, u16 notLoaded}` and reports the
// failure code otherwise.
func MailboxOpenResult(body []byte) (notLoaded uint16, ok bool, code byte) {
	if len(body) >= 1 && body[0] == 1 {
		if len(body) < 3 {
			return 0, false, 0
		}
		return binary.LittleEndian.Uint16(body[1:3]), true, 0
	}
	if len(body) >= 2 {
		return 0, false, body[1]
	}
	return 0, false, 0
}

// MailboxClaimResult is one 12-byte row of the op95 acknowledgement. The server
// writes the settled letter's wire id, the claim-object id that was asked for,
// and a trailing u32 whose low word is the per-object status -- zero for a
// successful extraction.
type MailboxClaimResult struct {
	MailID        uint32
	ClaimObjectID uint32
	Status        uint16
}

// MailboxExtractResult parses the op95 success body `{0x01, u32 n, n*12}`.
//
// The row count is checked against MailboxBatchLimit before allocating: the
// server never answers with more rows than the request carried ids, and a
// corrupted length must not turn into an unbounded allocation.
func MailboxExtractResult(body []byte) ([]MailboxClaimResult, error) {
	r := wireReader{body: body}
	if r.byte() != 1 {
		code := byte(0)
		if len(body) >= 2 {
			code = body[1]
		}
		return nil, fmt.Errorf("cn90 mailbox extract failed with code %d", code)
	}
	count := int(r.u32())
	if r.err != nil {
		return nil, fmt.Errorf("cn90 mailbox extract reply is truncated: %v", r.err)
	}
	if count > MailboxBatchLimit {
		return nil, fmt.Errorf("cn90 mailbox extract reply claims %d objects, above the %d accepted per request", count, MailboxBatchLimit)
	}
	claimed := make([]MailboxClaimResult, 0, count)
	for i := 0; i < count; i++ {
		var row MailboxClaimResult
		row.MailID = r.u32()
		row.ClaimObjectID = r.u32()
		// The status is the low word of a u32 whose high word is reserved.
		row.Status = uint16(r.u32())
		claimed = append(claimed, row)
	}
	if r.err != nil {
		return nil, fmt.Errorf("cn90 mailbox extract reply: %v", r.err)
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("cn90 mailbox extract reply has %d trailing bytes", r.remaining())
	}
	return claimed, nil
}

// MailboxClaimableIDs lists the claim-object ids a snapshot offers: one per
// attachment row, and the letter's own wire id for a row that carries only
// gold. Duplicates are dropped and the snapshot's own order is kept.
//
// The server projects a letter with nothing to hand over as a single empty
// receipt row, so a row carrying neither an item nor gold is never offered
// here. The caller must still batch the result: one snapshot may offer more
// ids than op95 accepts at once.
func MailboxClaimableIDs(snapshot MailboxSnapshot) []uint32 {
	ids := make([]uint32, 0, len(snapshot.Summaries))
	seen := make(map[uint32]struct{}, len(snapshot.Summaries))
	for _, summary := range snapshot.Summaries {
		if !summary.HasItem && summary.Gold == 0 {
			continue
		}
		if summary.ClaimObjectID == 0 {
			continue
		}
		if _, duplicate := seen[summary.ClaimObjectID]; duplicate {
			continue
		}
		seen[summary.ClaimObjectID] = struct{}{}
		ids = append(ids, summary.ClaimObjectID)
	}
	return ids
}

// MailboxDrainedIDs lists the wire mail ids of the letters a snapshot shows
// with nothing left to claim.
//
// A letter is named by its own wire id, which the server writes into the
// summary's mail field -- except on the empty receipt row it writes for a
// letter with nothing to hand over, where that field is zero and the id moves
// to the claim-object field. Both shapes are read here. The result therefore
// also names text-only letters, which were never claimable in the first place;
// the caller decides whether deleting those is what it wants.
func MailboxDrainedIDs(snapshot MailboxSnapshot) []uint32 {
	holdsContent := make(map[uint32]bool, len(snapshot.Summaries))
	order := make([]uint32, 0, len(snapshot.Summaries))
	for _, summary := range snapshot.Summaries {
		id := summary.MailID
		if id == 0 {
			id = summary.ClaimObjectID
		}
		if id == 0 {
			continue
		}
		if _, listed := holdsContent[id]; !listed {
			order = append(order, id)
			holdsContent[id] = false
		}
		if summary.HasItem || summary.Gold > 0 {
			holdsContent[id] = true
		}
	}
	drained := make([]uint32, 0, len(order))
	for _, id := range order {
		if !holdsContent[id] {
			drained = append(drained, id)
		}
	}
	return drained
}

// MailboxAlarmCount reads a class0/0x63 new-mail alarm: the body is exactly one
// u16 unread count. The server pushes a zero count as a reset and the real
// count once the town scene tail is ready.
func MailboxAlarmCount(body []byte) (uint16, bool) {
	if len(body) != 2 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(body), true
}

// MailboxCharacterQuery is the op324 reply payload: the queried name echoed
// raw, then the character's grow type, level, job and four state bytes.
type MailboxCharacterQuery struct {
	Name  string
	Grow  byte
	Level uint16
	Job   byte
}

// MailboxQueryResult parses the op324 success body
// `{0x01} + dstr(name) + u8 grow + u16 level + u8 job + 4 state bytes`.
func MailboxQueryResult(body []byte) (MailboxCharacterQuery, error) {
	r := wireReader{body: body}
	if r.byte() != 1 {
		code := byte(0)
		if len(body) >= 2 {
			code = body[1]
		}
		return MailboxCharacterQuery{}, fmt.Errorf("cn90 mailbox query failed with code %d", code)
	}
	query := MailboxCharacterQuery{Name: DecodeWireName(r.dstr())}
	query.Grow = r.byte()
	query.Level = r.u16()
	query.Job = r.byte()
	if r.skip(4) != nil {
		return MailboxCharacterQuery{}, fmt.Errorf("cn90 mailbox query reply is truncated: %v", r.err)
	}
	if r.err != nil {
		return MailboxCharacterQuery{}, fmt.Errorf("cn90 mailbox query reply: %v", r.err)
	}
	return query, nil
}

// MailboxSummary is one row of the class0/0x61 snapshot. A mail with several
// attachments produces several summaries, each with its own ClaimObjectID; a
// gold-only mail produces one carrying the gold; a text-only mail produces one
// zero "seed" row whose MailID is 0 and whose ClaimObjectID is the mail id.
type MailboxSummary struct {
	ClaimObjectID uint32
	SenderID      uint32
	SenderName    string
	Gold          uint32
	ItemID        uint32
	HasItem       bool
	ItemCount     uint32
	RemainingSecs uint32
	MailID        uint32
	MailType      byte
	// ItemRaw is the 119-byte durable item row; ItemRaw[0x0d] carries the bind
	// bit and ItemRaw[0x38:0x3c] the unix expiry, zero when the item never
	// expires.
	ItemRaw []byte
}

// Bind reports the item row's bind flag.
func (s MailboxSummary) Bind() bool {
	return len(s.ItemRaw) > 0x0d && s.ItemRaw[0x0d] != 0
}

// ExpireAt returns the item row's unix expiry, zero when it never expires. The
// server refuses to settle an auction prize or accept an attachment whose row
// carries a non-zero expiry.
func (s MailboxSummary) ExpireAt() uint32 {
	if len(s.ItemRaw) < 0x3c {
		return 0
	}
	return binary.LittleEndian.Uint32(s.ItemRaw[0x38:0x3c])
}

// MailboxDetail is one letter body row. LetterStat uses the MailboxStatus*
// values and MailType is the server's metadata projection (0 for ordinary mail).
type MailboxDetail struct {
	MailID     uint32
	SenderID   uint32
	SenderName string
	Body       string
	CreatedAt  uint32
	LetterStat uint16
	MailType   byte
}

// MailboxSnapshot is the parsed class0/0x61 body. PageFlag is zero for the full
// snapshot an op96 open returns; any other value means a paged refresh.
type MailboxSnapshot struct {
	PageFlag  byte
	Summaries []MailboxSummary
	NotLoaded uint16
	Details   []MailboxDetail
}

// ParseMailboxSnapshot parses the class0/0x61 body:
//
//	u8 summaryCount, u8 pageFlag, summaries[], u16 notLoaded, u16 detailCount, details[]
//
// Each summary's item block ends in the fixed 119-byte durable row. The server
// inserts five extra bytes (a zero DWORD and a zero flag) before that row when
// the attachment metadata says the equipment type is "creature" -- a server-side
// Extra key that never reaches the wire. The parser therefore reads the row
// where the durable row's own duplicated fields agree with the fixed block, and
// falls back to the extended layout otherwise.
func ParseMailboxSnapshot(body []byte) (MailboxSnapshot, error) {
	r := wireReader{body: body}
	summaryCount := int(r.byte())
	snapshot := MailboxSnapshot{PageFlag: r.byte()}
	snapshot.Summaries = make([]MailboxSummary, 0, summaryCount)
	for i := 0; i < summaryCount; i++ {
		summary, err := readMailboxSummary(&r)
		if err != nil {
			return MailboxSnapshot{}, fmt.Errorf("cn90 mailbox summary %d: %w", i, err)
		}
		snapshot.Summaries = append(snapshot.Summaries, summary)
	}
	snapshot.NotLoaded = r.u16()
	detailCount := int(r.u16())
	if r.err != nil {
		return MailboxSnapshot{}, fmt.Errorf("cn90 mailbox snapshot: %v", r.err)
	}
	snapshot.Details = make([]MailboxDetail, 0, detailCount)
	for i := 0; i < detailCount; i++ {
		detail, err := readMailboxDetail(&r)
		if err != nil {
			return MailboxSnapshot{}, fmt.Errorf("cn90 mailbox detail %d: %w", i, err)
		}
		snapshot.Details = append(snapshot.Details, detail)
	}
	if r.err != nil {
		return MailboxSnapshot{}, fmt.Errorf("cn90 mailbox snapshot: %v", r.err)
	}
	if r.remaining() != 0 {
		return MailboxSnapshot{}, fmt.Errorf("cn90 mailbox snapshot has %d trailing bytes", r.remaining())
	}
	return snapshot, nil
}

func readMailboxSummary(r *wireReader) (MailboxSummary, error) {
	var summary MailboxSummary
	summary.ClaimObjectID = r.u32()
	summary.SenderID = r.u32()
	summary.SenderName = DecodeWireName(r.dstr())
	summary.Gold = r.u32()
	if r.err != nil {
		return MailboxSummary{}, r.err
	}

	summary.ItemID = r.u32()
	summary.HasItem = r.byte() == 1
	itemCount := r.u32()
	boundU16 := r.u16()
	boundByte := r.byte()
	boundU32 := r.u32()
	boundByteA := r.byte()
	boundByteB := r.byte()
	boundU16B := r.u16()
	if r.err != nil {
		return MailboxSummary{}, r.err
	}
	raw, err := readMailboxItemRaw(r, summary.ItemID, itemCount, boundU16, boundByte, boundU32, boundByteA, boundByteB, boundU16B)
	if err != nil {
		return MailboxSummary{}, err
	}
	summary.ItemRaw = raw
	summary.ItemCount = itemCount
	summary.RemainingSecs = r.u32()
	summary.MailID = r.u32()
	summary.MailType = r.byte()
	if r.err != nil {
		return MailboxSummary{}, r.err
	}
	return summary, nil
}

// readMailboxItemRaw reads the 119-byte durable row, tolerating the five-byte
// creature extension. The row duplicates six fields the fixed block already
// carries, so the plain layout is accepted only when those copies agree.
func readMailboxItemRaw(r *wireReader, itemID, count uint32, boundU16 uint16, boundByte byte, boundU32 uint32, boundByteA, boundByteB byte, boundU16B uint16) ([]byte, error) {
	if r.remaining() < MailboxItemRawSize {
		return nil, fmt.Errorf("item row needs %d bytes, %d remain", MailboxItemRawSize, r.remaining())
	}
	plain := r.body[r.offset : r.offset+MailboxItemRawSize]
	if mailboxRawAgrees(plain, itemID, count, boundU16, boundByte, boundU32, boundByteA, boundByteB, boundU16B) {
		r.offset += MailboxItemRawSize
		return append([]byte(nil), plain...), nil
	}
	// Extended layout: a zero DWORD and a zero flag precede the durable row.
	if r.remaining() >= 5+MailboxItemRawSize && mailboxZeroExtension(r.body, r.offset) {
		extended := r.body[r.offset+5 : r.offset+5+MailboxItemRawSize]
		if mailboxRawAgrees(extended, itemID, count, boundU16, boundByte, boundU32, boundByteA, boundByteB, boundU16B) {
			r.offset += 5 + MailboxItemRawSize
			return append([]byte(nil), extended...), nil
		}
	}
	r.err = fmt.Errorf("item row does not match the summary fields")
	return nil, r.err
}

func mailboxRawAgrees(raw []byte, itemID, count uint32, boundU16 uint16, boundByte byte, boundU32 uint32, boundByteA, boundByteB byte, boundU16B uint16) bool {
	return binary.LittleEndian.Uint32(raw[2:6]) == itemID &&
		binary.LittleEndian.Uint32(raw[6:10]) == count &&
		binary.LittleEndian.Uint16(raw[0x0b:0x0d]) == boundU16 &&
		raw[0x0d] == boundByte &&
		binary.LittleEndian.Uint32(raw[0x0e:0x12]) == boundU32 &&
		raw[0x12] == boundByteA &&
		raw[0x13] == boundByteB &&
		binary.LittleEndian.Uint16(raw[0x14:0x16]) == boundU16B
}

func mailboxZeroExtension(body []byte, offset int) bool {
	return binary.LittleEndian.Uint32(body[offset:offset+4]) == 0 && body[offset+4] == 0
}

func readMailboxDetail(r *wireReader) (MailboxDetail, error) {
	var detail MailboxDetail
	detail.MailID = r.u32()
	detail.SenderID = r.u32()
	detail.SenderName = DecodeWireName(r.dstr())
	detail.Body = DecodeWireName(r.dstr())
	detail.CreatedAt = r.u32()
	detail.LetterStat = r.u16()
	detail.MailType = r.byte()
	if r.err != nil {
		return MailboxDetail{}, r.err
	}
	return detail, nil
}

// MailboxRemovedIDs parses the class0/0x62 body `u32 count + count u32 ids`
// pushed after an op134 delete.
func MailboxRemovedIDs(body []byte) ([]uint32, error) {
	r := wireReader{body: body}
	count := r.u32()
	if r.err != nil || uint64(r.remaining()) < uint64(count)*4 {
		return nil, fmt.Errorf("cn90 mailbox removal notification is truncated")
	}
	ids := make([]uint32, 0, count)
	for i := uint32(0); i < count; i++ {
		ids = append(ids, r.u32())
	}
	if r.err != nil {
		return nil, fmt.Errorf("cn90 mailbox removal notification: %v", r.err)
	}
	return ids, nil
}

// EncodeMailboxText encodes a wire string as the server's GB18030 payload,
// enforcing the same byte cap the server applies. An encoding failure is
// returned instead of sending mojibake.
func EncodeMailboxText(value string, maxBytes int) ([]byte, error) {
	encoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(value))
	if err != nil {
		return nil, fmt.Errorf("cn90 mailbox text is not encodable: %w", err)
	}
	if len(encoded) > maxBytes {
		return nil, fmt.Errorf("cn90 mailbox text is %d bytes, the limit is %d", len(encoded), maxBytes)
	}
	return encoded, nil
}

func appendDSTR(body []byte, raw []byte) []byte {
	body = appendU32(body, uint32(len(raw)))
	return append(body, raw...)
}

func appendU32(body []byte, value uint32) []byte {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return append(body, encoded[:]...)
}
