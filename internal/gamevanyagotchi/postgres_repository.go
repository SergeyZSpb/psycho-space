package gamevanyagotchi

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/jackc/pgx/v5"
)

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

// petColumns is the projection every read of a pet uses, so the scan order can
// only be got wrong in one place.
const petColumns = `id::text, account_id::text, name, skin_key, location_key, died_at, created_at, x, y, last_seen_at`

// scanPet reads petColumns in order, so the projection can only be got wrong in
// one place.
func scanPet(row pgx.Row) (Pet, error) {
	var p Pet
	err := row.Scan(&p.ID, &p.AccountID, &p.Name, &p.SkinKey, &p.LocationKey,
		&p.DiedAt, &p.CreatedAt, &p.X, &p.Y, &p.LastSeenAt)
	return p, err
}

// EnsurePet creates the account's pet if it has none, then returns it.
//
// Two statements rather than one clever CTE, and they are safe in that order for
// a specific reason: the insert is `ON CONFLICT DO NOTHING`, so when two
// requests race, one inserts and the other does nothing — and by the time either
// runs its SELECT, a row exists. The alternative single-statement form (an
// INSERT ... RETURNING unioned with a SELECT) has to handle the case where the
// insert returned nothing, which is the same two reads with the race moved
// somewhere less obvious.
//
// The conflict target is deliberately unnamed. The arbiter is a PARTIAL unique
// index (one living pet per account, `WHERE deleted_at IS NULL`), and the bare
// form accepts any arbiter, whereas naming `(account_id)` would also require
// repeating that predicate for inference to succeed.
func (PostgresRepository) EnsurePet(ctx context.Context, q db.DBTX, accountID, skinKey, locationKey string) (Pet, error) {
	if _, err := q.Exec(ctx,
		`INSERT INTO game_vanyagotchi_pets (account_id, skin_key, location_key)
		 VALUES ($1::uuid, $2, $3)
		 ON CONFLICT DO NOTHING`,
		accountID, skinKey, locationKey,
	); err != nil {
		return Pet{}, err
	}

	return scanPet(q.QueryRow(ctx,
		`SELECT `+petColumns+`
		   FROM game_vanyagotchi_pets
		  WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountID,
	))
}

// FindPet reads without creating. pgx.ErrNoRows is the ordinary "no pet yet"
// answer here rather than a failure, so it is translated into the bool instead
// of being handed to a caller that would have to know about pgx to read it.
func (PostgresRepository) FindPet(ctx context.Context, q db.DBTX, accountID string) (Pet, bool, error) {
	p, err := scanPet(q.QueryRow(ctx,
		`SELECT `+petColumns+`
		   FROM game_vanyagotchi_pets
		  WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pet{}, false, nil
	}
	if err != nil {
		return Pet{}, false, err
	}
	return p, true, nil
}

// SleepingPets reads the yard's furniture: everybody who has ever stood
// somewhere, newest first. Bounded by the caller, because a frame carrying every
// pet who ever played would grow without limit as the game gets older.
func (PostgresRepository) SleepingPets(ctx context.Context, q db.DBTX, limit int) ([]Pet, error) {
	rows, err := q.Query(ctx,
		`SELECT `+petColumns+`
		   FROM game_vanyagotchi_pets
		  WHERE deleted_at IS NULL AND x IS NOT NULL AND y IS NOT NULL
		  ORDER BY last_seen_at DESC NULLS LAST
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pet
	for rows.Next() {
		p, err := scanPet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (PostgresRepository) SavePosition(ctx context.Context, q db.DBTX, accountID string, at Point, seen time.Time) error {
	_, err := q.Exec(ctx,
		`UPDATE game_vanyagotchi_pets
		    SET x = $2, y = $3, last_seen_at = $4, updated_at = now()
		  WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountID, at.X, at.Y, seen,
	)
	return err
}

// SetLocation records which of the five places a pet is in.
//
// One column and nothing else: the pet's x/y are deliberately left alone, so
// coming back after a restart puts him where he was standing IN THE LOCATION HE
// IS IN rather than at its entrance. Coordinates are normalised per location, so
// the pair means the same thing wherever he is.
func (PostgresRepository) SetLocation(ctx context.Context, q db.DBTX, accountID, locationKey string) error {
	_, err := q.Exec(ctx,
		`UPDATE game_vanyagotchi_pets
		    SET location_key = $2, updated_at = now()
		  WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountID, locationKey,
	)
	return err
}

func (PostgresRepository) Stats(ctx context.Context, q db.DBTX, petID string) ([]StatRow, error) {
	rows, err := q.Query(ctx,
		`SELECT stat_key, value, as_of
		   FROM game_vanyagotchi_pet_stats
		  WHERE pet_id = $1::uuid AND deleted_at IS NULL`,
		petID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatRow
	for rows.Next() {
		var r StatRow
		if err := rows.Scan(&r.Key, &r.Value, &r.AsOf); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SeedStats inserts the rows that are missing and leaves the rest alone.
//
// One statement over arrays rather than a loop, so seeding a pet with any number
// of stats is one round trip. The conflict target IS named here, because the
// arbiter is the table's primary key rather than a partial index.
//
// A TRAP FOR WHOEVER SOFT-DELETES THE FIRST STAT ROW. Nothing does today, and
// nothing should without reading this. Stats above filters `deleted_at IS NULL`,
// but this arbiter is the primary key, which counts a soft-deleted row — so a
// deleted stat would be invisible to the read, silently skipped by this insert,
// and gone from the response permanently. The pets table has the same shape and
// is safe only because ITS arbiter index is itself partial on `deleted_at IS
// NULL`. If a stat ever needs deleting, either give this table the same partial
// unique index, or hard-delete the row.
func (PostgresRepository) SeedStats(ctx context.Context, q db.DBTX, petID string, rows []StatRow) error {
	if len(rows) == 0 {
		return nil
	}
	keys, values, asOf := statArrays(rows)
	_, err := q.Exec(ctx,
		`INSERT INTO game_vanyagotchi_pet_stats (pet_id, stat_key, value, as_of)
		 SELECT $1::uuid, k, v, a
		   FROM unnest($2::text[], $3::float8[], $4::timestamptz[]) AS t(k, v, a)
		 ON CONFLICT (pet_id, stat_key) DO NOTHING`,
		petID, keys, values, asOf,
	)
	return err
}

// WriteStats upserts the pairs the decay reads, in one statement.
//
// Upserts rather than UPDATEs so it cannot silently write nothing: a stat added
// to the catalogue between a read and an action would have no row, and an UPDATE
// would report success having changed nothing at all. The conflict target IS
// named here, because the arbiter is the table's primary key rather than a
// partial index.
//
// One statement over arrays rather than a loop because the caller always writes
// every stat at once — see the interface comment for why that is not optional.
func (PostgresRepository) WriteStats(ctx context.Context, q db.DBTX, petID string, rows []StatRow) error {
	if len(rows) == 0 {
		return nil
	}
	keys, values, asOf := statArrays(rows)
	_, err := q.Exec(ctx,
		`INSERT INTO game_vanyagotchi_pet_stats (pet_id, stat_key, value, as_of)
		 SELECT $1::uuid, k, v, a
		   FROM unnest($2::text[], $3::float8[], $4::timestamptz[]) AS t(k, v, a)
		 ON CONFLICT (pet_id, stat_key)
		 DO UPDATE SET value = EXCLUDED.value, as_of = EXCLUDED.as_of, updated_at = now()`,
		petID, keys, values, asOf,
	)
	return err
}

// statArrays splits rows into the parallel arrays both statements bind.
func statArrays(rows []StatRow) ([]string, []float64, []time.Time) {
	keys := make([]string, len(rows))
	values := make([]float64, len(rows))
	asOf := make([]time.Time, len(rows))
	for i, r := range rows {
		keys[i], values[i], asOf[i] = r.Key, r.Value, r.AsOf
	}
	return keys, values, asOf
}

// MarkDied records the death, and the `died_at IS NULL` guard is what makes it
// happen exactly once however many readers observe the same death at the same
// moment. The one that wins writes the derived instant; the others change no
// rows and are told so.
func (PostgresRepository) MarkDied(ctx context.Context, q db.DBTX, petID string, at time.Time) (bool, error) {
	tag, err := q.Exec(ctx,
		`UPDATE game_vanyagotchi_pets
		    SET died_at = $2, updated_at = now()
		  WHERE id = $1::uuid AND died_at IS NULL AND deleted_at IS NULL`,
		petID, at,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (PostgresRepository) Revive(ctx context.Context, q db.DBTX, petID string) error {
	_, err := q.Exec(ctx,
		`UPDATE game_vanyagotchi_pets
		    SET died_at = NULL, updated_at = now()
		  WHERE id = $1::uuid AND deleted_at IS NULL`,
		petID,
	)
	return err
}

// AppendEvents writes a batch of verbs as one contiguous run of sequence
// numbers.
//
// The sequence is computed INSIDE the statement, from the pet's current
// maximum, so no read-then-write window exists for two connections to race in.
// `WITH ORDINALITY` is what preserves the caller's order: a batch of verbs
// arrives as a list whose order is meaningful — drink-then-relieve is a
// different pet from relieve-then-drink — and unnest alone promises nothing
// about the order rows come back in.
func (PostgresRepository) AppendEvents(ctx context.Context, q db.DBTX, petID string, verbs []string, at time.Time) ([]Event, error) {
	if len(verbs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx,
		`WITH base AS (
		     SELECT COALESCE(MAX(seq), 0) AS n FROM game_vanyagotchi_events WHERE pet_id = $1::uuid
		 )
		 INSERT INTO game_vanyagotchi_events (pet_id, seq, verb, at)
		 SELECT $1::uuid, base.n + v.ord, v.verb, $3::timestamptz
		   FROM unnest($2::text[]) WITH ORDINALITY AS v(verb, ord), base
		 RETURNING seq, verb, at`,
		petID, verbs, at,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Event, 0, len(verbs))
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.Verb, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// RETURNING does not promise an order, and a caller replaying these must
	// see them in the order they were applied.
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Events reads a pet's whole history, oldest first.
//
// Ordered by seq rather than by at, because a batch shares one instant and only
// seq distinguishes the verbs inside it. Soft-deleted rows are excluded like
// everywhere else, though nothing deletes an event today — the log is
// append-only by design and the column exists for consistency with every other
// table rather than for a use anybody has.
func (PostgresRepository) Events(ctx context.Context, q db.DBTX, petID string) ([]Event, error) {
	rows, err := q.Query(ctx,
		`SELECT seq, verb, at
		   FROM game_vanyagotchi_events
		  WHERE pet_id = $1::uuid AND deleted_at IS NULL
		  ORDER BY seq`,
		petID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.Verb, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertWorldObject puts one durable thing on the plane.
//
// The conflict target is deliberately UNNAMED, for the same reason EnsurePet's
// is: the arbiter is a PARTIAL unique index — one active row per singleton kind
// — and the bare form accepts any arbiter, whereas naming `(kind)` would also
// require repeating that predicate for inference to succeed. A non-singleton
// kind does not participate in the index at all, so the same statement inserts
// every deposit and refuses a second key.
func (PostgresRepository) InsertWorldObject(ctx context.Context, q db.DBTX, kind, locationKey string, at Point, owner string, singleton bool, remaining *int, expires *time.Time) error {
	var ownerArg any
	if owner != "" {
		ownerArg = owner
	}
	_, err := q.Exec(ctx,
		`INSERT INTO game_vanyagotchi_world_objects
		     (kind, location_key, x, y, singleton, owner_account_id, remaining, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6::uuid, $7, $8)
		 ON CONFLICT DO NOTHING`,
		kind, locationKey, at.X, at.Y, singleton, ownerArg, remaining, expires,
	)
	return err
}

// LiveWorldObjects reads what is standing in the whole world right now, capped
// per location.
//
// `now()` is the database's, and that is the honest clock for a row whose
// expiry the database wrote — but the caller filters again against the tick's
// instant when it renders, because a cached object has to disappear from every
// screen at the same moment without anybody asking here.
//
// THE CAP IS PER LOCATION AND IT IS A WINDOW FUNCTION RATHER THAN A LIMIT, which
// is the whole reason this statement is not the two lines it used to be. A plain
// `LIMIT` is one budget five places compete for, so a busy evening in the yard
// would fill it and every other location would come back empty — including,
// eventually, whichever one is holding the key, which would end a hunt with
// nothing failing anywhere. Partitioning by location gives each place its own
// allowance and no way to spend a neighbour's.
//
// THE ORDER INSIDE A PARTITION IS SINGLETONS FIRST, THEN NEWEST, and the first
// half of that is load-bearing rather than tidy. The world's own fixtures — the
// crate, the key — are the OLDEST rows in their location, because they are stood
// up once and the deposits pile on afterwards; ordered by age alone, a location
// at its cap would evict the crate, and the yard would quietly become a place
// where the store has vanished and nobody can drink. Singletons are exactly the
// rows that must never be evictable, and the column that says so is already
// there. Newest-first among the rest is what a player would expect: the cap
// keeps what somebody just did rather than what has been lying around longest.
func (PostgresRepository) LiveWorldObjects(ctx context.Context, q db.DBTX, perLocation int) ([]WorldObject, error) {
	rows, err := q.Query(ctx,
		`SELECT id::text, kind, location_key, x, y, expires_at, remaining
		   FROM (
		     SELECT id, kind, location_key, x, y, expires_at, remaining,
		            row_number() OVER (
		              PARTITION BY location_key
		              ORDER BY singleton DESC, created_at DESC
		            ) AS place
		       FROM game_vanyagotchi_world_objects
		      WHERE deleted_at IS NULL
		        AND exhausted_at IS NULL
		        AND spawns_at <= now()
		        AND (expires_at IS NULL OR expires_at > now())
		   ) ranked
		  WHERE place <= $1
		  ORDER BY location_key, place`,
		perLocation,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorldObject
	for rows.Next() {
		var o WorldObject
		// `remaining` scans into a *int because the column is nullable and the
		// nil means something: a kind nobody draws down, as against a stocked one
		// that has run out. Collapsing the two into a nought would make an empty
		// crate indistinguishable from a lost key.
		if err := rows.Scan(&o.ID, &o.Kind, &o.LocationKey, &o.At.X, &o.At.Y, &o.ExpiresAt, &o.Remaining); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ClaimSingleton is the contested claim: exactly one player wins, and the
// replacement is inserted in the same statement.
//
// THE DATABASE DECIDES, NOT THE HUB. The guard is `claimed_by IS NULL`, so two
// players tapping in the same millisecond are resolved by Postgres rather than
// by whichever frame the hub happened to fan out first — and a forged client
// claim cannot beat it, because there is nothing in the request that says who
// should win. Zero rows affected means you lost.
//
// `exhausted_at` is set by the SAME statement that claims it, which is what
// takes the row out of the partial unique index and lets the replacement below
// exist. Doing it in two statements would leave a window in which the index
// still held the old row and the insert silently did nothing.
func (PostgresRepository) ClaimSingleton(ctx context.Context, q db.DBTX, kind, locationKey, accountID string, at time.Time) (bool, error) {
	tag, err := q.Exec(ctx,
		`UPDATE game_vanyagotchi_world_objects
		    SET claimed_by = $3::uuid, claimed_at = $4, exhausted_at = $4, updated_at = now()
		  WHERE kind = $1
		    AND location_key = $2
		    AND claimed_by IS NULL
		    AND exhausted_at IS NULL
		    AND deleted_at IS NULL
		    AND spawns_at <= now()
		    AND (expires_at IS NULL OR expires_at > now())`,
		kind, locationKey, accountID, at,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DrawFromStock takes one out of the crate, and it is the statement the whole
// beer store is built on.
//
// IT CANNOT OVERSELL, AND NOT BY BEING CAREFUL. `remaining > 0` is inside the
// UPDATE's own WHERE clause, so a second player's statement blocks on the row
// lock the first is holding, and when that commits it re-evaluates the guard
// against the value that was actually written rather than the one it read on the
// way in. Six players pressing at the same instant on a crate of two get two
// beers and four refusals, decided by PostgreSQL. Reading `remaining` first and
// deciding in Go would be exactly the lost-update race this avoids, and it would
// only ever go wrong under the load the mechanic is designed to produce.
//
// `exhausted_at` IS SET BY THE SAME STATEMENT, on the draw that reaches nought,
// which is what takes the row out of the partial unique index and lets the
// replacement crate be inserted inside the same transaction. In two statements
// there would be a window where the index still held the empty crate, the insert
// would silently do nothing, and the yard would be left with a crate nobody can
// draw from and nothing that would ever replace it — the same permanent, silent
// failure a claim without its replacement causes.
//
// `remaining - 1` in the SET reads the row as it was before the UPDATE, which is
// what makes the CASE and the assignment agree about which value they are
// talking about. Zero rows means there was nothing to take: a lost race rather
// than an error, exactly as a lost claim is.
func (PostgresRepository) DrawFromStock(ctx context.Context, q db.DBTX, kind, locationKey string, at time.Time) (int, bool, error) {
	var left int
	err := q.QueryRow(ctx,
		`UPDATE game_vanyagotchi_world_objects
		    SET remaining    = remaining - 1,
		        -- CAST REQUIRED, and its absence is a runtime error rather than a
		        -- rejected query at build time. A bare $3 inside a CASE whose other
		        -- branch is an untyped NULL gives the planner nothing to infer
		        -- from, so it defaults the parameter to text and the assignment
		        -- fails with 42804 on the first draw.
		        exhausted_at = CASE WHEN remaining - 1 <= 0 THEN $3::timestamptz ELSE NULL END,
		        updated_at   = now()
		  WHERE kind = $1
		    AND location_key = $2
		    AND remaining > 0
		    AND exhausted_at IS NULL
		    AND deleted_at IS NULL
		    AND spawns_at <= now()
		    AND (expires_at IS NULL OR expires_at > now())
		 RETURNING remaining`,
		kind, locationKey, at,
	).Scan(&left)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return left, true, nil
}

// ActiveSingleton returns the id of the active object of a kind, if the world
// holds one anywhere.
//
// Used to decide whether a hunt needs starting. IT NAMES NO LOCATION, and the
// predicate here is deliberately the same one the partial unique index carries
// (`WHERE singleton AND exhausted_at IS NULL AND deleted_at IS NULL`, on `kind`
// alone) minus the flag itself — so the question this asks and the invariant the
// insert is judged against are the same question. Scoping it to a location would
// have made them different: five places would each report no key, each would try
// to spawn one, and four of the five inserts would be refused by an index the
// caller had just been told nothing about.
func (PostgresRepository) ActiveSingleton(ctx context.Context, q db.DBTX, kind string) (string, bool, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT id::text
		   FROM game_vanyagotchi_world_objects
		  WHERE kind = $1
		    AND exhausted_at IS NULL AND deleted_at IS NULL
		  LIMIT 1`,
		kind,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}
