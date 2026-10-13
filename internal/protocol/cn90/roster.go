package cn90

import (
	"fmt"
)

// CharacterRosterEntry is the identity exposed by the class0/op2 character
// list. The DNF90 roster carries a zero-based slot and the raw name bytes.
type CharacterRosterEntry struct {
	Slot    uint16
	Name    string
	NameRaw []byte
	Job     byte
	Grow    byte
	Level   byte
}

// DecodeCharacterRoster parses the class0/op2 mode-2 character list produced
// by buildNoPackRosterBody: a 15-byte prefix (count at [13:15]), one variable
// entry per row, then a four-field trailer. Appearance summary rows are
// skipped by their declared count; they are not robot identity.
func DecodeCharacterRoster(body []byte) ([]CharacterRosterEntry, error) {
	r := wireReader{body: body}
	if r.skip(13) != nil {
		return nil, fmt.Errorf("character roster prefix: %w", r.err)
	}
	count := int(r.u16())
	if r.err != nil {
		return nil, fmt.Errorf("character roster count: %w", r.err)
	}
	entries := make([]CharacterRosterEntry, 0, count)
	for i := 0; i < count; i++ {
		entry := CharacterRosterEntry{Slot: r.u16()}
		name := r.dstr()
		if r.err != nil {
			return nil, fmt.Errorf("character roster entry %d name: %w", i, r.err)
		}
		entry.NameRaw = append([]byte(nil), name...)
		entry.Name = DecodeWireName(name)
		if r.skip(2) != nil { // reserved bytes before job/grow/level
			return nil, fmt.Errorf("character roster entry %d prefix: %w", i, r.err)
		}
		entry.Job = r.byte()
		entry.Grow = r.byte()
		entry.Level = r.byte()
		if r.skip(2+4+4) != nil { // trailing zero byte pair and two zero ids
			return nil, fmt.Errorf("character roster entry %d: %w", i, r.err)
		}
		appearanceCount := int(r.byte())
		if r.err != nil {
			return nil, fmt.Errorf("character roster entry %d appearance count: %w", i, r.err)
		}
		if r.skip(appearanceCount*19) != nil {
			return nil, fmt.Errorf("character roster entry %d appearances: %w", i, r.err)
		}
		// 24 zero bytes, the 03 00 00 04 marker, 6 zero bytes, private pvp
		// byte, 2 zero bytes and the ordinary pvp entry byte.
		if r.skip(24+4+6+1+2+1) != nil {
			return nil, fmt.Errorf("character roster entry %d tail: %w", i, r.err)
		}
		entries = append(entries, entry)
	}
	// page count, roster flag and two trailing values.
	if r.skip(1+1+4+4) != nil {
		return nil, fmt.Errorf("character roster trailer: %w", r.err)
	}
	return entries, nil
}

// The little-endian cursor lives in wire_reader.go; the roster parser shares it
// with the mailbox and auction parsers.
