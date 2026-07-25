package gamevanyagotchi

import (
	"context"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

// petColumns is the projection every read of a pet uses, so the scan order can
// only be got wrong in one place.
const petColumns = `id::text, account_id::text, name, skin_key, location_key, died_at, created_at`

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

	var p Pet
	err := q.QueryRow(ctx,
		`SELECT `+petColumns+`
		   FROM game_vanyagotchi_pets
		  WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountID,
	).Scan(&p.ID, &p.AccountID, &p.Name, &p.SkinKey, &p.LocationKey, &p.DiedAt, &p.CreatedAt)
	return p, err
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
