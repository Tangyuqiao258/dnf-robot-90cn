package shared

import (
	"context"
	"time"
)

// FollowAccountLocator resolves the last-played village of a follow account.
// The selected adapter owns the storage query; schedulers without a locator
// treat the follow-account spawn target as unavailable.
type FollowAccountLocator interface {
	FollowAccountVillageLastPlayed(ctx context.Context, account string) (village int, ok bool, err error)
}

// AccountOnlineChecker reports whether a game account still has an active
// session. Adapters that cannot answer must return an error instead of
// guessing.
type AccountOnlineChecker interface {
	AccountOnline(uid int) (bool, error)
}

// PlayerLocation is where the human player's character stands: the map it is
// on, the point it stands at and the level it is playing at. The floating layer
// reads it to decide which maps the fleet should fill and which robots are the
// right level for them.
type PlayerLocation struct {
	CharacterID string
	Name        string
	Level       int
	// Village and Area are the map key the rest of the runtime uses: the game
	// stores a town id and an area index, and the map catalog keys on exactly
	// that pair.
	Village int
	Area    int
	X       int
	Y       int
	// Seen is when the game last wrote the row, so a caller can tell a live
	// position from one left behind by a client that closed.
	Seen time.Time
}

// Available reports whether the location names a place on the map at all.
func (l PlayerLocation) Available() bool {
	return l.Village > 0
}

// PlayerLocator resolves the character a human player is currently playing.
// The selected adapter owns the storage query; a scheduler without a locator
// keeps the fleet's plain behaviour, where nothing follows the player.
type PlayerLocator interface {
	LocatePlayer(ctx context.Context, account string) (PlayerLocation, bool, error)
}
