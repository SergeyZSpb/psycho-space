package gamekhimki

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
}

// AssetPresence reports which art keys have an uploaded image. It is declared
// here, and satisfied by the shared gameassets service, so the dependency points
// from this game at infrastructure and never the other way — and so a test can
// substitute it without a database.
type AssetPresence interface {
	PresentKeys(ctx context.Context, gameKey string) (map[string]bool, error)
}
