package gamevanyadum

import (
	"context"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/google/uuid"
)

// Run is one finished attempt at the заброшка.
//
// This is the ENTIRE durable footprint of the game. A level is a pure function
// of its seed, a position is meaningless once the run is over, and an arena is
// deliberately lost on restart — so what survives is a summary of what happened,
// written once, at the end.
//
// Seed is stored so a run can be replayed or argued about: two people comparing
// times over the same заброшка is the only reason a leaderboard would be
// interesting, and it costs eight bytes to keep that possible.
type Run struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Seed      int64
	Success   bool
	Seconds   int
	Beer      int
	CreatedAt time.Time
}

// Repository is this game's persistence, and it is two statements wide on
// purpose. Everything else about a run lives in memory for the few minutes it
// exists.
type Repository interface {
	// InsertRun records a finished run.
	InsertRun(ctx context.Context, q db.DBTX, r Run) error
	// RecentRuns lists an account's own last runs, newest first. It exists so a
	// human can see that persistence worked without opening a database — the
	// splash screen shows it, and it is what makes "the run was written" a thing
	// anybody can check in production.
	RecentRuns(ctx context.Context, q db.DBTX, accountID uuid.UUID, limit int) ([]Run, error)
}
