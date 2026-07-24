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
}
