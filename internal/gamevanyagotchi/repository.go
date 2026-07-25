package gamevanyagotchi

import (
	"context"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// Repository is the storage boundary for the durable half of the game. Every
// method takes a db.DBTX so it composes with a transaction — none of them needs
// one today, and that is a property of the design rather than an oversight:
// every write below is either idempotent or conditional, so two players (or two
// tabs) racing converge without a lock.
type Repository interface {
	// EnsurePet returns the account's living pet, creating it if there is none.
	// Idempotent and safe under concurrency: two tabs opening the game at the
	// same instant produce one pet.
	EnsurePet(ctx context.Context, q db.DBTX, accountID, skinKey, locationKey string) (Pet, error)

	// Stats returns the stored (value, as_of) pairs for a pet, undecayed. The
	// decay is applied by the caller, because it is a pure function of the pair
	// and the clock and has no business inside a query.
	Stats(ctx context.Context, q db.DBTX, petID string) ([]StatRow, error)

	// SeedStats inserts rows that do not exist yet and leaves any that do
	// untouched. This is what makes "adding a stat is a catalogue entry" true for
	// pets that already exist: the new stat has no row, so the next read seeds
	// it at its starting value.
	SeedStats(ctx context.Context, q db.DBTX, petID string, rows []StatRow) error

	// WriteStats upserts every row given, at the instant each carries.
	//
	// Plural, and every caller passes EVERY stat. That is the invariant the
	// coupled decay depends on: hp's drain is a function of the other stats'
	// trajectories, so all of them must share one as_of or the integration
	// window has a gap in which a driver's history is unknown. Writing one stat
	// alone would silently erase damage — see decay.go.
	WriteStats(ctx context.Context, q db.DBTX, petID string, rows []StatRow) error

	// MarkDied records the moment of death, exactly once. Reports whether this
	// call was the one that wrote it — so the first read to observe a death is
	// the one that records it, and every later read is a no-op.
	MarkDied(ctx context.Context, q db.DBTX, petID string, at time.Time) (bool, error)

	// Revive clears the death.
	Revive(ctx context.Context, q db.DBTX, petID string) error
}
