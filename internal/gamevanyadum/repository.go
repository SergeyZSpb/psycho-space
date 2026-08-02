package gamevanyadum

import (
	"context"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/google/uuid"
)

// Visit is one stay in the заброшка: you opened a socket, you walked around, you
// left.
//
// This is the ENTIRE durable footprint of the game. The building is a pure
// function of its seed, a position is meaningless once you have gone, and the
// world is deliberately lost on restart — so what survives is a summary of what
// happened, written once, when the last connection has been gone past the grace.
//
// IT REPLACED A `Run`, AND THE DIFFERENCE IS THE WHOLE ITERATION. A run had a
// beginning, an objective and a result; a visit has none of those, because
// nothing in the building ends. So there is no `success` column here and there
// never will be: the field it replaced was `true` in every row ever written,
// which is another way of saying it recorded nothing.
//
// Seed is stored because a visit is a visit to a PARTICULAR building — the
// заброшка is regenerated whenever it empties, so without it the row cannot say
// which one you were in. Eight bytes to keep "we were in that one together"
// answerable.
//
// Kills, deaths and streaks are deliberately absent. They arrive with the gun
// and the enemy and will bring their own migration; columns added now would be
// columns nothing writes, against a requirement that does not exist yet.
type Visit struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Seed      int64
	// JoinedAt is when they walked in, and Seconds is how long they actually had
	// a connection — measured to their last seen one, never to the moment the
	// grace expired. See Occupant.Stayed.
	JoinedAt  time.Time
	Seconds   int
	Beer      int
	CreatedAt time.Time
}

// Repository is this game's persistence, and it is two statements wide on
// purpose. Everything else about a visit lives in memory for as long as the
// person is standing there.
type Repository interface {
	// InsertVisit records a finished visit.
	InsertVisit(ctx context.Context, q db.DBTX, v Visit) error
	// RecentVisits lists an account's own last visits, newest first. It exists so
	// a human can see that persistence worked without opening a database — the
	// splash screen shows it, and it is what makes "the visit was written" a
	// thing anybody can check in production.
	RecentVisits(ctx context.Context, q db.DBTX, accountID uuid.UUID, limit int) ([]Visit, error)
}
