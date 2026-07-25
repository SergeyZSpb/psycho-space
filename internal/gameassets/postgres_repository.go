package gameassets

import (
	"context"
	"errors"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/jackc/pgx/v5"
)

// PostgresRepository reads the game_assets blob table.
//
// The table name is deliberately unprefixed. Migration 007 moved the first
// game's runs into game_khimki_runs because a game's state is its own, but left
// this table alone: it is shared infrastructure and its game_key column is what
// scopes a row to a game.
type PostgresRepository struct{}

// NewPostgresRepository builds the Postgres-backed repository.
func NewPostgresRepository() PostgresRepository { return PostgresRepository{} }

// Bytes implements Repository.
func (PostgresRepository) Bytes(ctx context.Context, q db.DBTX, gameKey, artKey string) ([]byte, string, error) {
	var b []byte
	var ct string
	err := q.QueryRow(ctx,
		`SELECT bytes, content_type FROM game_assets WHERE game_key = $1 AND art_key = $2`,
		gameKey, artKey,
	).Scan(&b, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return b, ct, err
}

// Keys implements Repository.
func (PostgresRepository) Keys(ctx context.Context, q db.DBTX, gameKey string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT art_key FROM game_assets WHERE game_key = $1`, gameKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
