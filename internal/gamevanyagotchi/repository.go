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

	// SetLocation records which of the five places an account's pet is in.
	//
	// Written EAGERLY, unlike the position beside it, and the difference is not an
	// inconsistency. A position changes five times a second while somebody is
	// walking and matters only when he comes back, so it is written once on
	// departure. A location changes when a person deliberately goes somewhere,
	// which is rare, human-paced and bounded by its own rate limit — and it has to
	// survive a crash rather than a graceful shutdown, because coming back to the
	// place you left is the whole of what it is for.
	//
	// The key is a catalogue value the caller has already resolved; the column is
	// plain text and would accept anything, which is exactly why the check is not
	// left to it.
	SetLocation(ctx context.Context, q db.DBTX, accountID, locationKey string) error

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
	// `singleton` and `remaining` both come from the CATALOGUE rather than from
	// anything in here naming a kind, which is what lets the "at most one active
	// of this kind" index express itself without a content key ever entering an
	// immutable migration. The insert is `ON CONFLICT DO NOTHING` against that
	// partial index, so a kind that may only have one active row cannot get two
	// however many callers race — and a kind that may have many is unaffected,
	// because it does not participate in the index at all.
	//
	// `remaining` is nil for every kind but a stocked one, and the column is
	// nullable precisely so that "this is not drawn down" and "this is drawn
	// down and is empty" are different values rather than the same nought.
	InsertWorldObject(ctx context.Context, q db.DBTX, kind, locationKey string, at Point, owner string, singleton bool, remaining *int, expires *time.Time) error

	// LiveWorldObjects reads what is standing in the WHOLE WORLD right now, newest
	// first within each location and capped per location.
	//
	// Every location in one read, because the frame carries all five — locations
	// are not rooms — and a cache holding only one of them would draw an empty лес
	// for anybody standing in it. One statement rather than a query per location,
	// because five round trips fail in halves: a cache refreshed for two locations
	// and not the other three is a world nobody was ever in.
	//
	// `perLocation` caps each place SEPARATELY, which is the property a single
	// world-wide cap cannot have: a busy evening in the yard would otherwise spend
	// the whole budget and starve every other location, up to and including the
	// location holding the key.
	//
	// Expired rows are filtered HERE rather than deleted anywhere: nothing
	// sweeps, because a sweeper would be the background timer this design does
	// not have.
	LiveWorldObjects(ctx context.Context, q db.DBTX, perLocation int) ([]WorldObject, error)

	// ClaimSingleton hands the one active object of a kind to one account, and
	// reports whether this caller is the one who got it. False means somebody
	// else was first — a lost race rather than an error.
	//
	// The SingleWinner half of the contest; DrawFromStock below is the other.
	// Which one a verb goes through is decided by the kind's catalogue
	// `Contest`, never by anything in this interface.
	ClaimSingleton(ctx context.Context, q db.DBTX, kind, locationKey, accountID string, at time.Time) (bool, error)

	// DrawFromStock takes one unit out of the one active stocked object of a
	// kind, and reports what is left and whether there was anything to take.
	//
	// The Stock half of the contest, and it CANNOT OVERSELL under any amount of
	// concurrency: the guard is `remaining > 0` inside a conditional UPDATE, so
	// a second player's statement blocks on the row the first is holding and
	// re-evaluates that guard against the value the first committed. False means
	// the crate was already empty — a lost race rather than an error, exactly as
	// a lost claim is.
	//
	// The decrement that reaches nought sets `exhausted_at` in the SAME
	// statement, which is what takes the row out of the partial unique index and
	// lets its replacement be inserted in the same transaction. Splitting those
	// would leave a window in which the index still held the empty crate and the
	// insert silently did nothing — a yard with a crate nobody can draw from and
	// no mechanism that would ever replace it.
	DrawFromStock(ctx context.Context, q db.DBTX, kind, locationKey string, at time.Time) (int, bool, error)

	// ActiveSingleton returns the id of the active object of a kind, ANYWHERE IN
	// THE WORLD.
	//
	// It names no location on purpose, and that is the "one key, not one per
	// location" ruling expressed where it is enforced: the partial unique index is
	// on `kind` alone, so "is there an active one of these" has exactly one answer
	// however many places there are to hide it. Asking per location would be a
	// question the index does not answer — four locations would each report no
	// key, each would try to spawn one, and three of the four inserts would be
	// silently refused.
	ActiveSingleton(ctx context.Context, q db.DBTX, kind string) (string, bool, error)
}

// AssetPresence reports which art keys have an uploaded image.
//
// Declared HERE and satisfied by the shared `gameassets` service, so the
// dependency points from this game at infrastructure and never the other way —
// the identical seam «Смолтолк в Химках» already has, and for the identical
// reason. Storing and serving bytes is a mechanism every game needs; WHICH keys
// this game expects is a rule of this game and lives in its catalogue
// (ADR-028/031).
//
// It is an interface rather than the concrete service so a test can answer it
// without a database, and so that a nil one is a legitimate state: art is never
// a hard dependency, and the yard is playable with none of it uploaded.
type AssetPresence interface {
	PresentKeys(ctx context.Context, gameKey string) (map[string]bool, error)
}
