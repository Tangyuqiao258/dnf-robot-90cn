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
}

type RobotSession interface {
	MoveTown(context.Context, TownMoveIntent) error
	Shout(context.Context, ShoutIntent) error
	Close() error
}

type SessionFactory interface {
	OpenSession(context.Context, OpenSessionRequest) (RobotSession, error)
}
