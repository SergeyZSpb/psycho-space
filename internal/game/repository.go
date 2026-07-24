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
	// Records returns every account's extreme single runs for a game (longest and
	// shortest win, longest and shortest loss). Ranking and capping happen in the
	// service, which turns these into the four record boards.
	Records(ctx context.Context, q db.DBTX, gameKey string) ([]PlayerRecords, error)
	// StatsFor returns a single player's summary for a game.
	StatsFor(ctx context.Context, q db.DBTX, gameKey, accountID string) (PlayerStats, error)
	// AssetBytes returns an art image's bytes + content type, or ErrAssetNotFound.
	AssetBytes(ctx context.Context, q db.DBTX, gameKey, artKey string) ([]byte, string, error)
	// AssetKeys returns the art keys that have an uploaded image for a game.
	AssetKeys(ctx context.Context, q db.DBTX, gameKey string) ([]string, error)
}
