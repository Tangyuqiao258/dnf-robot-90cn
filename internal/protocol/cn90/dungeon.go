package cn90

import (
	"encoding/binary"
	"fmt"
)

// selectDungeonBodySize is the fixed op16 request size the server enforces.
// A shorter or longer body is dropped without a reply, so the encoder has to
// produce exactly this many bytes.
const selectDungeonBodySize = 21

// SelectDungeonBody builds the 21-byte op16 entry request:
// `u32 dungeon id, u8 difficulty, u16 entry option, u8 selection mode,
// u8 runtime state, u16 runtime token, u32 reserved, u8 party state,
// u32 leader object key, u8 special mode`.
//
// Only the dungeon id and the difficulty carry meaning for a solo entry: the
// server rejects a non-zero entry option, selection mode, runtime state,
// reserved word, party state or special mode, and a runtime token other than 0
// or 0xffff. Everything the client would otherwise fill in (map graph, start
// map, boss coordinates) is derived server-side from PVF.
func SelectDungeonBody(dungeonID uint32, difficulty byte) []byte {
	body := make([]byte, selectDungeonBodySize)
	binary.LittleEndian.PutUint32(body[0:4], dungeonID)
	body[4] = difficulty
	return body
}

// dungeonSelectReasons names the failure codes the server sends back on op16.
// Only the codes the entry validator actually emits are listed; anything else
// is reported by number.
var dungeonSelectReasons = map[byte]string{
	0x00: "rejected",
	0x08: "not the party leader",
	0x0e: "level too low",
	0x11: "required entry item missing",
	0x16: "fatigue exhausted",
}

// DungeonSelectAck classifies an op16 reply. Success carries the server's
// upper-success prefix; failure is `{0, reason, 0}`.
func DungeonSelectAck(opcode uint16, body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("cn90 dungeon select op %d returned an empty body", opcode)
	}
	if body[0] == 1 {
		return nil
	}
	if len(body) >= 2 {
		if reason, ok := dungeonSelectReasons[body[1]]; ok {
			return fmt.Errorf("cn90 dungeon select op %d rejected: %s (code %d)", opcode, reason, body[1])
		}
		return fmt.Errorf("cn90 dungeon select op %d rejected with code %d", opcode, body[1])
	}
	return fmt.Errorf("cn90 dungeon select op %d was rejected", opcode)
}

// dungeonFinishLoadingBodySize is the op37 body length the server accepts. It
// takes only the plain eight-byte shape or a sixteen-byte protected wrapper and
// drops anything else without a reply, so the encoder emits the eight-byte one
// the live client sends.
const dungeonFinishLoadingBodySize = 8

// FinishLoadingBody builds the eight-byte op37 loading acknowledgement: two
// zero u32 words. The server gates on the length alone and decodes no field, so
// there is nothing to fill in.
func FinishLoadingBody() []byte {
	return make([]byte, dungeonFinishLoadingBodySize)
}

// DieCharacterBody builds the four-byte op40 body: `u16 x, u16 y`.
//
// Death is client-declared in this protocol generation - the server keeps no
// character hit points and never decides that a character died. The
// coordinates are only logged: the town a character returns to comes from its
// persisted position, not from this packet.
func DieCharacterBody(x, y int16) []byte {
	body := make([]byte, 4)
	binary.LittleEndian.PutUint16(body[0:2], uint16(x))
	binary.LittleEndian.PutUint16(body[2:4], uint16(y))
	return body
}

// UseCoinBody builds the two-byte op41 revive request: `u16 object key`.
//
// The server requires the key to be the acting character's own scene object
// key, which for a normally selected character is its character id.
func UseCoinBody(objectKey uint16) []byte {
	body := make([]byte, 2)
	binary.LittleEndian.PutUint16(body, objectKey)
	return body
}

// UseCoinAck classifies an op41 reply. Success is `{1, code}` with the code
// byte always zero; the only failure the server emits is `{0, 1}`.
func UseCoinAck(opcode uint16, body []byte) error {
	if len(body) < 2 {
		return fmt.Errorf("cn90 revive op %d returned a %d-byte body", opcode, len(body))
	}
	if body[0] == 1 {
		return nil
	}
	return fmt.Errorf("cn90 revive op %d rejected with code %d", opcode, body[1])
}

// DungeonDeathState is the class0/op32 notice the server sends to the acting
// session when it records a client-declared death or a revive. It is never
// broadcast to peers, so a party member never learns of another member's death.
type DungeonDeathState struct {
	// ObjectKey is the scene object key the notice is about.
	ObjectKey uint16
	// Revived is true for the revive notice and false for the death notice.
	Revived bool
	// Flag is the trailing byte, observed as zero in both notices.
	Flag byte
}

// Dungeon death-notice state bytes.
const (
	dungeonDeathStateDead   = byte(0)
	dungeonDeathStateRevive = byte(1)
)

const dungeonDeathStateBodySize = 4

// ParseDungeonDeathState decodes the four-byte body of the op32 notice:
// `u16 object key, u8 state, u8 flag`. A state byte other than dead or revive
// is reported rather than guessed at, because the server never emits one.
func ParseDungeonDeathState(opcode uint16, body []byte) (DungeonDeathState, error) {
	if len(body) != dungeonDeathStateBodySize {
		return DungeonDeathState{}, fmt.Errorf(
			"cn90 dungeon death notice op %d body is %d bytes, want %d",
			opcode, len(body), dungeonDeathStateBodySize)
	}
	state := body[2]
	if state != dungeonDeathStateDead && state != dungeonDeathStateRevive {
		return DungeonDeathState{}, fmt.Errorf(
			"cn90 dungeon death notice op %d carries unknown state %d", opcode, state)
	}
	return DungeonDeathState{
		ObjectKey: binary.LittleEndian.Uint16(body[0:2]),
		Revived:   state == dungeonDeathStateRevive,
		Flag:      body[3],
	}, nil
}
