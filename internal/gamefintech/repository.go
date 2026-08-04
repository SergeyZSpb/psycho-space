package gamefintech

import (
	"context"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/google/uuid"
)

// Shift is one finished shift.
//
// This is the ENTIRE durable footprint of the game. The office is a constant,
// a position is meaningless once the shift is over, and the world is
// deliberately lost on restart — so what survives is a summary of what happened,
// written once, at the end.
//
// THERE IS NO NAME ON IT, and that is not an omission. Every personal field on
// an account is encrypted at rest (AES-256-GCM, see internal/account), so no
// SQL join can produce a display name — there is nothing in the accounts table
// but ciphertext, and the key lives in the process rather than in Postgres. The
// leaderboard's `name` is therefore resolved by the HTTP layer from AccountID
// through the account service, exactly as the wishlist's author block and
// «Смолтолк в Химках»'s player block already do. This package stays free of any
// dependency on internal/account, which is what keeps it deletable.
type Shift struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	// Cause is why it ended: CausePromoted or CauseLeft. A plain string all the
	// way down to a `text` column, so a third ending is a catalogue entry and
	// not a migration.
	Cause string
	// Salary is rubles earned, and Seconds is how long it lasted. Both are
	// double precision because the simulation produces fractions of both and
	// rounding them at the boundary would make a leaderboard that disagrees with
	// the number the player watched.
	Salary  float64
	Seconds float64
	// CreatedAt is when the row was written, which is within a tick of when the
	// shift ended.
	CreatedAt time.Time
}

// LayoutSource says where a stored floor came from. It is set by the endpoint
// that wrote the row and never by the payload, so it cannot be claimed.
const (
	// SourceStarting is the floor a fresh database opens with.
	SourceStarting = "starting"
)

// Repository is this game's persistence. Everything else about a shift lives in
// memory for the few minutes it exists; the floor lives here because a floor
// somebody drew has to survive a deploy.
type Repository interface {
	// CurrentLayout reads the floor in force — the newest row that has not been
	// deleted. It answers ErrNoLayout on an empty table, which is a state rather
	// than a failure: it is what a fresh database looks like.
	CurrentLayout(ctx context.Context, q db.DBTX) (Layout, error)
	// InsertLayout writes a floor and makes it the current one.
	InsertLayout(ctx context.Context, q db.DBTX, l Layout, source string) error
	// InsertShift records a finished shift. It is the only write this game
	// makes.
	InsertShift(ctx context.Context, q db.DBTX, s Shift) error
	// RecentShifts lists one account's own last shifts, newest first. It is what
	// the splash screen shows, and it is what makes "the shift was written" a
	// thing anybody can check in production without opening a database.
	RecentShifts(ctx context.Context, q db.DBTX, accountID uuid.UUID, limit int) ([]Shift, error)
	// TopShifts is the leaderboard by MONEY: the best shift per account, not the
	// best rows. One good afternoon would otherwise fill the whole board, which
	// is both boring and exactly the wrong incentive.
	//
	// `since` is the oldest a shift may be and still rank — the caller's clock
	// rather than the database's, which is what lets a test place a row on either
	// side of the boundary without waiting a week. Nothing is deleted: a shift
	// older than this is simply not selected, and RecentShifts still returns it.
	TopShifts(ctx context.Context, q db.DBTX, since time.Time, limit int) ([]Shift, error)
	// TopShiftsBySeconds is the same board scored on the other dimension: the
	// LONGEST shift per account.
	//
	// TWO BOARDS RATHER THAN ONE SORTED TWO WAYS, because they are two different
	// games. Money rewards standing still through a ramp; length rewards
	// surviving a floor that speeds up every twenty seconds, and the best way to
	// do one is not the best way to do the other. A single board would quietly
	// declare one of them the point.
	TopShiftsBySeconds(ctx context.Context, q db.DBTX, since time.Time, limit int) ([]Shift, error)
}
