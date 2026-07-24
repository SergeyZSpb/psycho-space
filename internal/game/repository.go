package game

import (
	"context"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// Repository is the storage boundary for game runs. All methods take a db.DBTX
// so they compose with transactions.
type Repository interface {
	// RecordRun inserts a finished run and returns it.
	RecordRun(ctx context.Context, q db.DBTX, accountID, gameKey, characterKey string, success bool, steps int) (Run, error)
	// Leaderboard aggregates per account for a game (most successes first, then
	// fewest total steps).
	Leaderboard(ctx context.Context, q db.DBTX, gameKey string, limit int) ([]LeaderboardEntry, error)
	// StatsFor returns a single player's summary for a game.
	StatsFor(ctx context.Context, q db.DBTX, gameKey, accountID string) (PlayerStats, error)
	// AssetBytes returns an art image's bytes + content type, or ErrAssetNotFound.
	AssetBytes(ctx context.Context, q db.DBTX, gameKey, artKey string) ([]byte, string, error)
	// AssetKeys returns the art keys that have an uploaded image for a game.
	AssetKeys(ctx context.Context, q db.DBTX, gameKey string) ([]string, error)
}
