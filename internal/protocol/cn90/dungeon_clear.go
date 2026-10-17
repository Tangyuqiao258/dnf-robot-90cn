package cn90

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Clearing a run: the client side of dungeon combat.
//
// A run is opened with op16 and released with op37 - see dungeon.go. What
// happens between them is four requests, and the server validates every one of
// them by its exact length, so each builder here emits a fixed-size body rather
// than a variable one:
//
//	op39  report a monster dead   native layout, 74 bytes with no combat entry
//	op45  move to the next room   100 bytes; only the target room is read
//	op117 boss die check          39 bytes; the boss must already be dead
//	op46  play result             42 bytes; the shape alone is validated
//
// The room a session stands in is the server's, not the client's: it is pushed
// as op29 and its actor rows carry the scene object keys a death report names.
// The maze layout itself is never sent - a room is reached by asking for a
// neighbouring coordinate and reading the room that comes back.

// --- op29 current-dungeon start map (server to client) ---

// The op29 body is a 19-byte head followed by 21-byte actor rows and a tail of
// passive-object entries, ridable groups and the party member index. Only the
// head and the rows are decoded here.
//
//	0  u8   room x
//	1  u8   room y
//	2  u8   layered-room flag
//	3  u32  seed
//	7  u8   hell-party mode
//	8  u8   the byte after it
//	9  u32  room state
//	13 u8   room state flag
//	14 u32  map id
//	18 u8   actor count
//
// and one 21-byte actor row:
//
//	0  u16  template order
//	2  u32  packet index
//	6  u16  object key
//	8  u32  monster code
//	12 u8   level
//	13 u8   actor type
//	14 u8   flag 0
//	15 u8   flag 1
//	16 u32  extra state
//	20 u8   room-clear byte
const (
	dungeonStartMapHeaderSize = 19
	dungeonStartMapActorSize  = 21
)

// Actor types the server writes into an op29 row. Type 3 is an ordinary boss
// and type 8 a hostile AI/APC boss: the death of either ends combat. Type 4 is
// an NPC row, which never counts toward a room clear.
const (
	DungeonActorTypeBoss          = byte(3)
	DungeonActorTypeHostileAIBoss = byte(8)
	DungeonActorTypeNPC           = byte(4)
)

// DungeonStartMapActor is one actor row, reduced to the fields a clearing
// client reads.
type DungeonStartMapActor struct {
	// ObjectKey is the scene object key this actor's death is reported with.
	ObjectKey uint16
	// Code is the monster code the row was built from.
	Code uint32
	// Level is the actor's resolved level.
	Level byte
	// Type is the actor type; see the DungeonActorType constants.
	Type byte
	// Blocking is the server's reverse-polarity room-clear byte: zero makes the
	// actor participate in the alive scan, one skips it.
	Blocking byte
}

// Monster reports whether the actor has to be reported dead for its room to
// clear. NPC rows are announced with the skip byte set, so the type check is
// belt and braces rather than the rule itself.
func (a DungeonStartMapActor) Monster() bool {
	return a.Blocking == 0 && a.Type != DungeonActorTypeNPC
}

// Boss reports whether the actor's death ends the run.
func (a DungeonStartMapActor) Boss() bool {
	return a.Type == DungeonActorTypeBoss || a.Type == DungeonActorTypeHostileAIBoss
}

// DungeonStartMap is the part of one op29 body a clearing client uses.
type DungeonStartMap struct {
	X     byte
	Y     byte
	MapID uint32
	// Actors are the room's announced actors in packet order.
	Actors []DungeonStartMapActor
}

// Boss returns the room's boss row, if it announced one.
func (m DungeonStartMap) Boss() (DungeonStartMapActor, bool) {
	for _, actor := range m.Actors {
		if actor.Boss() {
			return actor, true
		}
	}
	return DungeonStartMapActor{}, false
}

// Monsters returns the object keys of the actors whose death the room clear
// waits for.
func (m DungeonStartMap) Monsters() []uint16 {
	keys := make([]uint16, 0, len(m.Actors))
	for _, actor := range m.Actors {
		if actor.Monster() {
			keys = append(keys, actor.ObjectKey)
		}
	}
	return keys
}

// ParseDungeonStartMap decodes the op29 body. The tail behind the actor rows is
// left unread: nothing a clearing client does depends on it.
func ParseDungeonStartMap(body []byte) (DungeonStartMap, error) {
	if len(body) < dungeonStartMapHeaderSize {
		return DungeonStartMap{}, fmt.Errorf(
			"cn90 dungeon start map body is %d bytes, want at least %d",
			len(body), dungeonStartMapHeaderSize)
	}
	count := int(body[18])
	if len(body) < dungeonStartMapHeaderSize+count*dungeonStartMapActorSize {
		return DungeonStartMap{}, fmt.Errorf(
			"cn90 dungeon start map announces %d actors but carries only %d bytes",
			count, len(body))
	}
	view := DungeonStartMap{
		X:      body[0],
		Y:      body[1],
		MapID:  binary.LittleEndian.Uint32(body[14:18]),
		Actors: make([]DungeonStartMapActor, 0, count),
	}
	for index := 0; index < count; index++ {
		row := body[dungeonStartMapHeaderSize+index*dungeonStartMapActorSize:]
		view.Actors = append(view.Actors, DungeonStartMapActor{
			ObjectKey: binary.LittleEndian.Uint16(row[6:8]),
			Code:      binary.LittleEndian.Uint32(row[8:12]),
			Level:     row[12],
			Type:      row[13],
			Blocking:  row[20],
		})
	}
	return view, nil
}

// --- op39 report a monster dead (client to server) ---

// The native op39 body is a 31-byte prefix, one 14-byte entry per reported
// combat, a 39-byte runtime tail and a four-byte finalizer. The finalizer is
// appended only while the body before it is at most 400 bytes, which is why the
// size is computed rather than fixed.
//
//	0  u32  target object key
//	4  u16  owner object key
//	6  u64  combat scalar
//	14 u32  combat scalar
//	18 u64  combat scalar
//	26 u32  combat scalar
//	30 u8   combat entry count
//	31      count * 14-byte entries
//	        then the 39-byte runtime tail and the finalizer
const (
	dieMonsterNativePrefixSize      = 31
	dieMonsterNativeCombatEntrySize = 14
	dieMonsterNativeRuntimeTailSize = 39
	dieMonsterNativeFinalizerSize   = 4
)

// DieMonsterNativeBodySize is the op39 body for a report that carries no combat
// entry of its own. The server reads the two keys and the entry count; the
// tail, the scalars and the finalizer are retained without being interpreted,
// which is what lets the entry-less report stand in for a client that has no
// combat detail to describe.
const DieMonsterNativeBodySize = dieMonsterNativePrefixSize + dieMonsterNativeRuntimeTailSize + dieMonsterNativeFinalizerSize

// DieMonsterBody builds the op39 kill report: the target actor's object key as
// the server announced it in op29, and the reporting character's own object
// key, which for a normally selected character is its character id.
//
// The server accepts the report only from the character the room's actors are
// bound to, and it accepts one target at a time - the whole body is validated
// against the room's live actors before anything is committed.
func DieMonsterBody(targetObjectKey uint32, ownerObjectKey uint16) []byte {
	body := make([]byte, DieMonsterNativeBodySize)
	binary.LittleEndian.PutUint32(body[0:4], targetObjectKey)
	binary.LittleEndian.PutUint16(body[4:6], ownerObjectKey)
	// The entry count stays zero: a zero count is the one shape that reaches the
	// ordinary death path without the client having to prove a combat entry.
	body[30] = 0
	return body
}

// --- op45 move to the next room (client to server) ---

// dungeonMoveMapBodySize is the exact op45 length the server enforces. It plans
// the step from its own room graph, so a body that names the target room is all
// it needs; the position, move kind and sequence fields a client fills in while
// walking are read for nothing.
const dungeonMoveMapBodySize = 100

// MoveMapBody builds the op45 room move: the target room's maze coordinates,
// everything else zero. The server answers a successful move by pushing the new
// room as another op29, so the answer is the room, not an acknowledgement.
func MoveMapBody(nextX, nextY byte) []byte {
	body := make([]byte, dungeonMoveMapBodySize)
	body[0] = nextX
	body[1] = nextY
	return body
}

// --- op117 boss die check (client to server) ---

// dungeonBossDieCheckBodySize is the exact op117 length the server enforces,
// read as `u16 related actor object key, u16 target object key` and a fixed
// tail. The server refuses the request unless the reserved word and the byte at
// offset 12 are zero, which is what a zero-filled tail gives it.
const dungeonBossDieCheckBodySize = 39

// BossDieCheckBody asks the server to close the run on a boss it has already
// seen die. The related actor is the acting character's own object key and the
// target is the boss's, and the order matters: the boss's op39 has to be
// committed first, because a check that arrives before the death is retained
// only for a tutorial scene and refused everywhere else.
func BossDieCheckBody(relatedActorObjectKey, targetObjectKey uint16) []byte {
	body := make([]byte, dungeonBossDieCheckBodySize)
	binary.LittleEndian.PutUint16(body[0:2], relatedActorObjectKey)
	binary.LittleEndian.PutUint16(body[2:4], targetObjectKey)
	return body
}

// --- op46 play result (client to server) ---

// dungeonPlayResultBodySize is the base op46 length. The server validates the
// shape alone - a base body plus zero to eight 7-byte rows - and reads no field
// at all, so the row-free shape is the one to send.
const dungeonPlayResultBodySize = 42

// SetPlayResultBody builds the op46 body that answers the op31 settlement
// entry. It is the client's "the run is over" statement: with it the server
// freezes the result plan it will not build earlier.
func SetPlayResultBody() []byte {
	return make([]byte, dungeonPlayResultBodySize)
}

// --- op123 character statistic (client to server) ---

// dungeonCharacterStatisticBodySize is the exact op123 length the server
// enforces: six u32 values from the client's result page. The server records
// them as telemetry only - it never turns them into rewards - so the barrier
// is the arrival of the packet, not its content.
const dungeonCharacterStatisticBodySize = 24

// CharacterStatisticBody builds the op123 body that crosses the server's
// post-settlement barrier. Zeroes are the values a client that did not track
// the run's statistics writes, and the server keeps them as observed data.
func CharacterStatisticBody() []byte {
	return make([]byte, dungeonCharacterStatisticBodySize)
}

// dieMonsterTemplateHex is one op39 body written by a live native client, kept
// as the shape to build from. It is the capture the server's own suite retains
// (dungeon_party_combat_runtime_test.go, TestNativePartyDungeonDeathAcknowledgesBothMembers),
// and only its two key fields are ever rewritten.
const dieMonsterTemplateHex = "9401000001000000000000000000000000000c0300000000000006000000011101010046020000000000000600e602c600000000005e700003000000f8010000000000008302d0004c02fe000c000f0007000000588fe7dd"

// dieMonsterTemplate decodes once at start. The literal is fixed and valid, so
// a failure here is a build defect rather than a runtime condition.
var dieMonsterTemplate = func() []byte {
	body, err := hex.DecodeString(dieMonsterTemplateHex)
	if err != nil {
		panic(fmt.Sprintf("cn90 op39 template is not hex: %v", err))
	}
	return body
}()

// DieMonsterTemplateBodySize is the capture's length: a 31-byte prefix, one
// 14-byte combat entry, the 39-byte tail and the finalizer.
const DieMonsterTemplateBodySize = dieMonsterNativePrefixSize + dieMonsterNativeCombatEntrySize + dieMonsterNativeRuntimeTailSize + dieMonsterNativeFinalizerSize

// DieMonsterTemplateBody builds the op39 body from the retained live capture,
// rewriting only the target and owner keys. It carries the one combat entry the
// native client writes with every kill; use it when the report has to read as
// the current client's own, and DieMonsterBody when there is no combat to
// describe.
func DieMonsterTemplateBody(targetObjectKey uint32, ownerObjectKey uint16) []byte {
	body := make([]byte, len(dieMonsterTemplate))
	copy(body, dieMonsterTemplate)
	binary.LittleEndian.PutUint32(body[0:4], targetObjectKey)
	binary.LittleEndian.PutUint16(body[4:6], ownerObjectKey)
	return body
}
