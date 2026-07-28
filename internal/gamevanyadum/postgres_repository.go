package gamevanyadum

import (
	"context"
	"fmt"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/google/uuid"
)

// PostgresRepository is the pgx-backed Repository. Two statements, both trivial:
// this game's durable footprint is one summary row per finished run.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

// runColumns is the projection every read uses, so the scan order can only be
// got wrong in one place.
const runColumns = `id, account_id, seed, success, seconds, beer, created_at`

// InsertRun records a finished run.
func (PostgresRepository) InsertRun(ctx context.Context, q db.DBTX, r Run) error {
	_, err := q.Exec(ctx,
		`INSERT INTO game_vanyadum_runs (id, account_id, seed, success, seconds, beer)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		r.ID, r.AccountID, r.Seed, r.Success, r.Seconds, r.Beer)
	if err != nil {
		return fmt.Errorf("gamevanyadum: insert run: %w", err)
	}
	return nil
}

// RecentRuns lists an account's own last runs, newest first.
func (PostgresRepository) RecentRuns(ctx context.Context, q db.DBTX, accountID uuid.UUID, limit int) ([]Run, error) {
	rows, err := q.Query(ctx,
		`SELECT `+runColumns+`
		   FROM game_vanyadum_runs
		  WHERE account_id = $1 AND deleted_at IS NULL
		  ORDER BY created_at DESC
		  LIMIT $2`,
		accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("gamevanyadum: recent runs: %w", err)
	}
	defer rows.Close()

	out := make([]Run, 0, limit)
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.AccountID, &r.Seed, &r.Success, &r.Seconds, &r.Beer, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("gamevanyadum: scan run: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gamevanyadum: iterate runs: %w", err)
	}
	return out, nil
}
