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

	// FindPet returns the account's pet without creating one, reporting whether
	// there was any.
	//
	// Separate from EnsurePet because the plane must not conjure a pet for
	// somebody who has merely opened a socket: creating one is what the first
	// HTTP read does, deliberately and once. A player whose client has not asked
	// for its state yet simply renders with catalogue defaults.
	FindPet(ctx context.Context, q db.DBTX, accountID string) (Pet, bool, error)

	// SleepingPets returns the pets that have stood somewhere, most recently
	// seen first, up to limit.
	//
	// Read ONCE per process, lazily, on the first hello — never on a timer and
	// never on the broadcast. It exists so that a restart does not empty the
	// yard of everybody who is asleep in it: without it the plane would only
	// know about people who had connected since the last deploy, and the first
	// visitor after one would find the field bare.
	SleepingPets(ctx context.Context, q db.DBTX, limit int) ([]Pet, error)

	// SavePosition records where an account's pet was standing, and when.
	//
	// Written once per session on the last disconnect, never per move: thirty
	// players moving at the socket's ten messages a second would be three
	// hundred writes a second to persist something that is only read when they
	// come back.
	SavePosition(ctx context.Context, q db.DBTX, accountID string, at Point, seen time.Time) error

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

	// AppendEvents adds verbs to a pet's history, in the order given, and
	// returns them stamped with the sequence numbers they were assigned.
	//
	// The log is append-only and this is what keeps it so: the next `seq` is
	// computed inside the statement from the pet's current maximum, and a
	// unique index on (pet_id, seq) means two connections racing to act on the
	// same pet cannot take the same slot — the loser gets a constraint
	// violation and retries rather than silently overwriting a sibling event.
	//
	// Written in the SAME transaction as the snapshot it produces (see
	// Service.Do), so the log and the stat rows can never disagree about what
	// happened. `at` is the server's instant for the whole batch: a batch is one
	// acceptance, so its verbs share a time and are ordered by seq alone.
	AppendEvents(ctx context.Context, q db.DBTX, petID string, verbs []string, at time.Time) ([]Event, error)

	// Events returns a pet's history in order, oldest first.
	//
	// Not on the read path — the stat rows are the snapshot and are current, so
	// a plain read never folds. This exists for replay: reproducing a pet from
	// its history, and applying a retuned catalogue retroactively.
	Events(ctx context.Context, q db.DBTX, petID string) ([]Event, error)

	// MarkDied records the moment of death, exactly once. Reports whether this
	// call was the one that wrote it — so the first read to observe a death is
	// the one that records it, and every later read is a no-op.
	MarkDied(ctx context.Context, q db.DBTX, petID string, at time.Time) (bool, error)

	// Revive clears the death.
	Revive(ctx context.Context, q db.DBTX, petID string) error

	// InsertWorldObject puts one durable thing on the plane.
	//
	// `singleton` comes from the CATALOGUE rather than from a predicate naming
	// a kind, which is what lets the "at most one active of this kind" index
	// express itself without a content key ever entering an immutable
	// migration. The insert is `ON CONFLICT DO NOTHING` against that partial
	// index, so a kind that may only have one active row cannot get two however
	// many callers race — and a kind that may have many is unaffected, because
	// it does not participate in the index at all.
	InsertWorldObject(ctx context.Context, q db.DBTX, kind, locationKey string, at Point, owner string, singleton bool, expires *time.Time) error

	// LiveWorldObjects reads what is standing in a location right now, newest
	// first and capped.
	//
	// Expired rows are filtered HERE rather than deleted anywhere: nothing
	// sweeps, because a sweeper would be the background timer this design does
	// not have. The cap is what bounds the frame the caller is about to build.
	LiveWorldObjects(ctx context.Context, q db.DBTX, locationKey string, limit int) ([]WorldObject, error)
}
