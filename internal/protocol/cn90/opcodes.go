package cn90

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Client command identities on the legacy frame (cmd=1). Several of these are
// reclassified by the server's stream splitter as raw upper requests, but the
// outbound frame layout stays the legacy one.
const (
	CmdEndpointRequest uint16 = 1
	// CmdSetUDPIPPort is the current client's class-1/op2 SET_UDP_IP_PORT
	// report: the dynamic P2P UDP endpoint that class0/op11 PARTY_IP_INFO
	// hands to the other party members. The server recognizes it by the exact
	// 31-byte current-EXE body; see setUDPIPPortBody.
	CmdSetUDPIPPort       uint16 = 2
	CmdExit               uint16 = 3
	CmdSelectCharacter    uint16 = 4
	CmdCreateCharacter    uint16 = 5
	CmdDeleteCharacter    uint16 = 6
	CmdGetUserInfo        uint16 = 8
	CmdRequestPeer        uint16 = 10
	CmdResponsePeer       uint16 = 11
	CmdEnterSelectDungeon uint16 = 15
	// CmdLeaveParty ends the sender's own party membership. The server consumes
	// the request whoever sends it -- leader or not -- ignores the body, and
	// answers with a one-byte acknowledgement plus the empty roster, so leaving is
	// the one party edit that needs no role and no arguments. The last member to
	// leave retires the party; a leader leaving with members still in it re-forms
	// it under a successor, which is why the whole fleet leaves its own way out
	// instead of the leader clearing the party for everyone.
	CmdLeaveParty    uint16 = 13
	CmdSelectDungeon uint16 = 16
	CmdPeerChat      uint16 = 17
	CmdMoveItemspace uint16 = 19
	// CmdItemTradeState is the one byte trade-window state. The server decodes
	// the same number as a scene-transition message and lets the trade handler
	// claim it first, but only while a trade is open on that session; nothing
	// here ever sends it outside a trade.
	CmdItemTradeState  uint16 = 24
	CmdSetUserPosition uint16 = 35
	CmdSetUserArea     uint16 = 36
	// CmdFinishLoading is the loading acknowledgement the server waits for
	// before it releases the dungeon scene. The run is already active by the
	// time it is accepted -- op16 commits it -- so this is the step that turns
	// "a run is running" into "the character is standing in it": the character
	// state push, the skill frame and the room placement all follow it.
	CmdFinishLoading uint16 = 37
	// CmdDieMonster reports one actor of the current room as dead. It is the
	// only way a run advances: the server keeps no monster hit points, so a room
	// clears when its actors have been reported, not when they were fought.
	CmdDieMonster   uint16 = 39
	CmdDieCharacter uint16 = 40
	CmdUseCoin      uint16 = 41
	// CmdGiveupGame abandons the run the session is inside and is the only
	// client-initiated exit besides the death timer. It is refused, silently,
	// while no run is active, so it can never move a character that is merely
	// standing in town.
	CmdGiveupGame uint16 = 42
	// CmdMoveMap walks the run to the next room. The server plans the step from
	// its own maze topology and answers with the new room rather than with an
	// acknowledgement, so a move that is refused (not adjacent, or the current
	// room is not cleared yet) is simply never answered.
	CmdMoveMap uint16 = 45
	// CmdSetPlayResult is the client's "the run is over" statement, and the only
	// body in this set the server validates by shape rather than by field. It is
	// answered with the settlement projections, and only after the settlement
	// entry notice has arrived.
	CmdSetPlayResult uint16 = 46
	// The settlement-seat requests after op46. The server walks the result
	// page as a client-driven chain: op69 asks for the score-scroll state,
	// op70 asks for the card layout, and op123 carries the six-u32 statistic
	// the server treats as the post-settlement barrier before op42 accepts a
	// completed run's exit. Each is bodyless except op123.
	CmdScoreScrollState     uint16 = 69
	CmdCardSelectRightState uint16 = 70
	CmdCharacterStatistic   uint16 = 123
	// CmdBossDieCheck closes the run on a boss the server has already been told
	// is dead. The boss's death report has to be committed first: a check that
	// arrives before it is refused outside a tutorial scene.
	CmdBossDieCheck       uint16 = 117
	CmdMailboxSend        uint16 = 94
	CmdMailboxExtractItem uint16 = 95
	CmdMailboxOpen        uint16 = 96
	CmdChangeLetterStat   uint16 = 134
	CmdChangeTutorial     uint16 = 143
	CmdChangeEmotion      uint16 = 254
	CmdAuctionAverage     uint16 = 182
	CmdAuctionRegister    uint16 = 183
	CmdAuctionCancel      uint16 = 184
	CmdAuctionBid         uint16 = 185
	CmdAuctionSearchItem  uint16 = 186
	CmdAuctionSearchPage  uint16 = 187
	CmdAuctionMyListings  uint16 = 188
	CmdAuctionMyBids      uint16 = 189
	CmdAuctionHistory     uint16 = 190
	CmdMultiMailboxSend   uint16 = 315
	CmdQueryCharacMailbox uint16 = 324
	CmdAuctionBuyPiece    uint16 = 334
	CmdCreateExpertStore  uint16 = 598
	CmdCloseExpertStore   uint16 = 600
	CmdCheckCharacterName uint16 = 692
	CmdMailboxRecipients  uint16 = 789
	CmdTownSceneReady     uint16 = 1345
	CmdCheckConnection    uint16 = 1276
)

// Server packet identities (upper msgID).
const (
	NotiChannelInfo   uint16 = 1
	ResponseEndpoint  uint16 = 1
	NotiCharacterList uint16 = 2
	ResponseSelect    uint16 = 4
	ResponseCreate    uint16 = 5
	ResponseDelete    uint16 = 6
	NotiRequestPeer   uint16 = 0x0007
	// NotiPeerResponse is the class0/0x0008 answer the server sends the original
	// requester when the target answers an op10 request. For a trade request it
	// is the only signal that the window opened.
	NotiPeerResponse uint16 = 0x0008
	// The trade window's own notice family. Opcodes 15..18 collide with the
	// client command numbers above; these are the class0 directions.
	NotiTradeChange     uint16 = 15
	NotiTradeCancel     uint16 = 16
	NotiTradeState      uint16 = 17
	NotiTradeFinish     uint16 = 18
	// NotiPartyPeerEndpoints is the class-0/0x000B PARTY_IP_INFO exchange. It
	// carries one 22-byte row per member with the UDP endpoint that member
	// should be reached on; the party HUD keeps a peer on "connecting" until
	// the P2P handshake to that endpoint completes.
	NotiPartyPeerEndpoints uint16 = 0x000B
	NotiUserPosition       uint16 = 0x0016
	NotiUserArea           uint16 = 0x0017
	NotiSceneTransition uint16 = 24
	// NotiPartyMemberRealtime is the class-0/0x0099 PARTY_MEMBER_REALTIME_INFO
	// roster. The server sends it to every member of a runtime party whenever
	// the membership changes, and an empty roster (count zero) is how it says
	// the party is gone, so it answers "am I in a party and with whom" without
	// reading the scene-side op9 slot table.
	NotiPartyMemberRealtime uint16 = 0x0099
	// NotiDungeonInfo and NotiDungeonStartMap are pushed class-0 once the
	// server has already made the run active; they are how a session learns it
	// is inside a dungeon, not a precondition for entering one.
	NotiDungeonInfo     uint16 = 28
	NotiDungeonStartMap uint16 = 29
	// NotiDungeonSettlementEntry tells the client the run is cleared and the
	// settlement window is open. Its body is one byte, and it is the gate the
	// op46 play result is validated against.
	NotiDungeonSettlementEntry uint16 = 31
	// NotiDungeonClearReward is the class-0/op35 settlement reward projection
	// the server sends only after the op46 play result has been accepted. Its
	// arrival is what opens the card-seat requests (op69/op70) and what proves
	// op46 was not dropped.
	NotiDungeonClearReward uint16 = 35
	// NotiFinishLoading is the class-0 character-state push the server sends
	// only after it has processed the op37 acknowledgement, so its arrival is
	// the session's proof that the dungeon scene finished loading. It shares
	// its number with CmdFinishLoading on the other side of the session.
	NotiFinishLoading uint16 = 37
	// NotiDungeonDeathState carries the acting session's own death and revive
	// notices. The server never sends it to anyone else.
	NotiDungeonDeathState uint16 = 32
	// NotiPeerChatMessage is the delivery notice the server sends to the target
	// of a directed chat message. Opcode 17 is reused on the same session as a
	// class0 peer state row, and the trade window pushes that same three-byte row
	// as NotiTradeState, so a reader must not take a class0/op17 packet as proof
	// that a message arrived. Opcode 118 is 0x76.
	NotiPeerChatMessage uint16 = 118
	NotiMailboxList     uint16 = 0x0061
	NotiMailboxRemove   uint16 = 0x0062
	NotiMailboxAlarm    uint16 = 0x0063
	// NotiAuctionService shares the number with CmdAuctionRegister, but on the
	// other side of the session: the server pushes it as a class-0 notification
	// with the two-byte bodies {0,1} and {1,1} once per session, at the first
	// auction request. It is not the ACK of a registration.
	NotiAuctionService      uint16 = 183
	NotiCompletedQuestGate  uint16 = 356
	NotiExpertStoreCreate   uint16 = 538
	NotiExpertStoreClose    uint16 = 539
	NotiExpertStoreUpdate   uint16 = 544
	ResponseCheckName       uint16 = 692
	ResponseMailboxRoles    uint16 = 718
	ResponseCheckConnection uint16 = 1276
)

// EndpointRequestSize is the exact body length the server accepts for the
// class1/op1 endpoint request that unlocks a bound session login. Both the
// current EXE (590) and the 90CN reference client (598) are accepted; the body
// content is not consumed.
const EndpointRequestSize = 590

// InitialTownProgress triggers the server's initial town actor/transition
// route after character selection (op143 with prefix 0 and commit flag 1).
const InitialTownProgress uint32 = 36

func binaryLE16(value uint16) []byte {
	out := make([]byte, 2)
	binary.LittleEndian.PutUint16(out, value)
	return out
}

// EndpointRequestBody returns the zero body for the endpoint request. The
// server validates the length only.
func EndpointRequestBody() []byte {
	return make([]byte, EndpointRequestSize)
}

// RosterRequestBody asks for the character list. The three-byte shape keeps
// the request on the legacy dispatch route.
func RosterRequestBody() []byte {
	return []byte{0, 0, 2}
}

// SelectCharacterBody selects a roster slot through the 16-byte plain shape
// the server decodes as `u32 slot + 12 zero bytes`.
func SelectCharacterBody(slot uint16) []byte {
	body := make([]byte, 16)
	binary.LittleEndian.PutUint32(body[:4], uint32(slot))
	return body
}

// createCharacterOptionBytes is the fixed appearance-option tail the server's
// splitter expects after the name (5 + nameLen + 8). Zero options request the
// default appearance.
const createCharacterOptionBytes = 8

// CreateCharacterBody builds `u8 job + u32 nameLen + name + 8 option bytes`.
func CreateCharacterBody(job byte, name []byte) ([]byte, error) {
	if len(name) < 2 || len(name) > 30 {
		return nil, fmt.Errorf("character name must be 2..30 bytes")
	}
	body := make([]byte, 5+len(name)+createCharacterOptionBytes)
	body[0] = job
	binary.LittleEndian.PutUint32(body[1:5], uint32(len(name)))
	copy(body[5:], name)
	return body, nil
}

// DeleteCharacterBody builds `u16 slot + u32 nameLen + name`.
func DeleteCharacterBody(slot uint16, name []byte) ([]byte, error) {
	if len(name) < 1 || len(name) > 30 {
		return nil, fmt.Errorf("character name must be 1..30 bytes")
	}
	body := make([]byte, 6+len(name))
	binary.LittleEndian.PutUint16(body[:2], slot)
	binary.LittleEndian.PutUint32(body[2:6], uint32(len(name)))
	copy(body[6:], name)
	return body, nil
}

// CheckCharacterNameBody builds `u32 nameLen + name`, the exact op692 shape.
func CheckCharacterNameBody(name []byte) ([]byte, error) {
	if len(name) < 1 || len(name) > 30 {
		return nil, fmt.Errorf("character name must be 1..30 bytes")
	}
	body := make([]byte, 4+len(name))
	binary.LittleEndian.PutUint32(body[:4], uint32(len(name)))
	copy(body[4:], name)
	return body, nil
}

// TutorialProgressBody builds the six-byte op143 shape
// `u8 prefix, u32 progress, u8 commit flag`.
func TutorialProgressBody(progress uint32) []byte {
	body := make([]byte, 6)
	binary.LittleEndian.PutUint32(body[1:5], progress)
	body[5] = 1
	return body
}

// SetUserAreaBody builds the 16-byte op36 shape:
// `u8 town, u8 area, u16 x, u16 y, u8 direction, u16, u16, u32, u8`.
// The opaque tail must stay zero for an ordinary route click.
func SetUserAreaBody(town, area byte, x, y int16, direction byte) []byte {
	body := make([]byte, 16)
	body[0] = town
	body[1] = area
	binary.LittleEndian.PutUint16(body[2:4], uint16(x))
	binary.LittleEndian.PutUint16(body[4:6], uint16(y))
	body[6] = direction
	return body
}

// SetUserAreaPortalBody builds the cross-town portal shape of op36: the same
// 16-byte layout with the source town repeated in the first opaque u16. The
// server only admits a transition between towns when that field names the
// character's current town, so an ordinary route body is always blocked with
// cross_town_route_not_proven_by_current_pvf_topology.
func SetUserAreaPortalBody(town, area byte, x, y int16, direction byte, sourceTown uint16) []byte {
	body := SetUserAreaBody(town, area, x, y, direction)
	binary.LittleEndian.PutUint16(body[7:9], sourceTown)
	return body
}

// SetUserPositionBody builds the seven-byte op35 shape:
// `u16 x, u16 y, u8 movement code, u16 opaque scaled value`.
func SetUserPositionBody(x, y int16, movementCode byte, opaqueScaled uint16) []byte {
	body := make([]byte, 7)
	binary.LittleEndian.PutUint16(body[0:2], uint16(x))
	binary.LittleEndian.PutUint16(body[2:4], uint16(y))
	body[4] = movementCode
	binary.LittleEndian.PutUint16(body[5:7], opaqueScaled)
	return body
}

// ExitBody is the one-byte channel-exit body.
func ExitBody() []byte {
	return []byte{0}
}

// EndpointResultOK reports the class1/op1 success marker.
func EndpointResultOK(body []byte) bool {
	return len(body) >= 1 && body[0] == 1
}

// EndpointChannelPort reads the advertised channel port from the login
// success body for diagnostics. Layout: success, u8, u8 channelType, u8,
// u32 token, dstr server IP, i32 channel port.
func EndpointChannelPort(body []byte) (int, bool) {
	if len(body) < 1+1+1+1+1+4 {
		return 0, false
	}
	offset := 1 + 1 + 1 + 1 + 4
	if offset+4 > len(body) {
		return 0, false
	}
	ipLength := int(binary.LittleEndian.Uint32(body[offset : offset+4]))
	offset += 4
	if ipLength < 0 || offset+ipLength+4 > len(body) {
		return 0, false
	}
	offset += ipLength
	port := int(binary.LittleEndian.Uint32(body[offset : offset+4]))
	if port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

// SelectResultCharacterID reads the selected character id from a successful
// class1/op4 ACK.
func SelectResultCharacterID(body []byte) (uint16, error) {
	if len(body) < 11 {
		return 0, fmt.Errorf("select character response is truncated: %d", len(body))
	}
	if body[0] != 1 {
		return 0, fmt.Errorf("select character failed with result %d", body[0])
	}
	characterID := binary.LittleEndian.Uint16(body[9:11])
	if characterID == 0 {
		return 0, errors.New("select character response has an empty character id")
	}
	return characterID, nil
}

// CreateResultCharacterID reads the created character id from a successful
// class1/op5 ACK and reports the failure code otherwise.
func CreateResultCharacterID(body []byte) (uint16, byte, error) {
	if len(body) < 1 {
		return 0, 0, fmt.Errorf("create character response is empty")
	}
	if body[0] != 1 {
		code := byte(0)
		if len(body) >= 2 {
			code = body[1]
		}
		return 0, code, nil
	}
	if len(body) < 3 {
		return 0, 0, fmt.Errorf("create character success response is truncated: %d", len(body))
	}
	return binary.LittleEndian.Uint16(body[1:3]), 0, nil
}

// DeleteResult reports whether a class1/op6 ACK succeeded and, on failure, the
// server error code.
func DeleteResult(body []byte) (bool, byte) {
	if len(body) >= 1 && body[0] == 1 {
		return true, 0
	}
	if len(body) >= 2 {
		return false, body[1]
	}
	return false, 0
}

// CheckNameResult reports whether a class1/op692 ACK succeeded and, on
// failure, the server error code.
func CheckNameResult(body []byte) (bool, byte) {
	if len(body) >= 1 && body[0] == 1 {
		return true, 0
	}
	if len(body) >= 2 {
		return false, body[1]
	}
	return false, 0
}
