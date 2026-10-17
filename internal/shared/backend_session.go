package shared

import (
	"context"
	"time"
)

type OpenSessionRequest struct {
	AccountName   string
	PasswordHash  string
	CharacterSlot uint16
	// EnablePartyDungeonFollower is an explicit session intent. Backends may
	// implement it with their own verified workflow or leave party following
	// unsupported.
	EnablePartyDungeonFollower bool
	// EnablePartyAutoAccept makes the session answer forwarded party invite
	// prompts (class0/0x0007) with the accepting op11 response. Backends
	// without a party transport leave it unimplemented.
	EnablePartyAutoAccept bool
	// EnablePartyFollow makes the session track the live town position of the
	// party leader it accepted an invitation from, so the runtime can steer the
	// follower toward it. Backends without a party transport leave it
	// unimplemented.
	EnablePartyFollow bool
	// EnablePartyInvite makes the session track the party membership the server
	// reports (class0/0x0099), so the runtime can invite other characters and
	// learn which invitations were answered. Backends without a party transport
	// leave it unimplemented.
	EnablePartyInvite bool
	// EnableDungeonFollowCombat makes a pulled-in follower clear the rooms the
	// server moved it into by reporting their actors dead. Backends without a
	// dungeon combat transport leave it unimplemented. Turning it off keeps the
	// follower's per-room loading acknowledgement; only the death reports stop.
	EnableDungeonFollowCombat bool
	// EnableDungeonRevive makes the session answer the server's death notice
	// with a revive request, so a character that died inside a run is not
	// returned to town by the server's ten second timer. Backends without a
	// dungeon transport leave it unimplemented.
	EnableDungeonRevive bool
	// EnableChatReply makes the session answer a directed message from another
	// character on its own, with one line drawn from ChatReplies. Backends
	// without a directed-chat transport leave it unimplemented.
	EnableChatReply bool
	// ChatReplies are the lines an automatic chat reply is drawn from. They are
	// supplied per session rather than read by the adapter, because the template
	// file belongs to the runtime configuration the scheduler owns.
	ChatReplies []string
	// EnableTrade makes the session answer a same-area trade prompt and drive the
	// window to completion, offering one stack of its own stock. Backends without
	// a player-to-player trade transport leave it unimplemented.
	EnableTrade bool
	// TradeOfferStacks is how many of the backend's own seeded stock rows an
	// automatic trade may try to offer from. The rows themselves are backend
	// knowledge -- the adapter is the only side that knows which bag slots it
	// filled -- so the caller supplies the count and the adapter names the slots.
	TradeOfferStacks int
	// TradeOfferCount is how many items one automatic offer moves out of the bag.
	TradeOfferCount int
	// InitialTown* are robot-owned position metadata used by adapters that
	// need to distinguish an in-area coordinate move from an area transition.
	// They are not sent as game-database mutations or protocol credentials.
	InitialTownKnown bool
	InitialVillage   int
	InitialArea      int
	InitialX         int
	InitialY         int
	// HomeVillage is the town the server still considers current for this
	// character (its login route). Adapters whose server only accepts
	// cross-town transitions with an explicit source town use it to request
	// the assigned town with the portal shape.
	HomeVillage int
}

type TownMoveIntent struct {
	X, Y      int16
	Direction byte
	Motion    uint16
}

type TownAreaMoveIntent struct {
	Village int
	Area    int
	X       int16
	Y       int16
	// SourceVillage is the town the character currently stands in. When it is
	// positive and differs from Village the move is a cross-town transition,
	// which the server only accepts with the portal request shape.
	SourceVillage int
}

// PartyFollowTarget is a party leader's last known town position, tracked by a
// follower session from the same-area movement broadcast the server sends to
// every player sharing the mover's (town, area) key. It is a snapshot, not a
// subscription: ObservedAt is when the notice arrived, so a caller can tell a
// leader that is standing still from one whose notices stopped arriving.
type PartyFollowTarget struct {
	// CharacterID identifies the leader. A caller that switched leaders must
	// not carry the previous leader's position forward.
	CharacterID int
	Village     int
	Area        int
	X           int
	Y           int
	ObservedAt  time.Time
}

type ShoutChannel string

const (
	ShoutChannelArea  ShoutChannel = "area"
	ShoutChannelParty ShoutChannel = "party"
	ShoutChannelWorld ShoutChannel = "world"
)

type ShoutIntent struct {
	Channel ShoutChannel
	Message string
	// TargetCharacterID and TargetName address a directed message. A backend
	// without a broadcast channel needs both: the target is what the message is
	// delivered to, and the name is what the receiver is told about the sender.
	TargetCharacterID uint16
	TargetName        string
}

type RobotSession interface {
	MoveTown(context.Context, TownMoveIntent) error
	Shout(context.Context, ShoutIntent) error
	Close() error
}

type SessionFactory interface {
	OpenSession(context.Context, OpenSessionRequest) (RobotSession, error)
}
