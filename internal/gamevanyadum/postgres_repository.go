package gamevanyadum

import (
	"context"
	"fmt"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/google/uuid"
)

// PostgresRepository is the pgx-backed Repository. Two statements, both trivial:
// this game's durable footprint is one summary row per visit.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

// visitColumns is the projection every read uses, so the scan order can only be
// got wrong in one place.
const visitColumns = `id, account_id, seed, joined_at, seconds, beer, created_at`

// InsertVisit records a finished visit.
func (PostgresRepository) InsertVisit(ctx context.Context, q db.DBTX, v Visit) error {
	_, err := q.Exec(ctx,
		`INSERT INTO game_vanyadum_visits (id, account_id, seed, joined_at, seconds, beer)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		v.ID, v.AccountID, v.Seed, v.JoinedAt, v.Seconds, v.Beer)
	if err != nil {
		return fmt.Errorf("gamevanyadum: insert visit: %w", err)
	}
	return nil
}

// RecentVisits lists an account's own last visits, newest first.
func (PostgresRepository) RecentVisits(ctx context.Context, q db.DBTX, accountID uuid.UUID, limit int) ([]Visit, error) {
	rows, err := q.Query(ctx,
		`SELECT `+visitColumns+`
		   FROM game_vanyadum_visits
		  WHERE account_id = $1 AND deleted_at IS NULL
		  ORDER BY created_at DESC
		  LIMIT $2`,
		accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("gamevanyadum: recent visits: %w", err)
	}
	defer rows.Close()

	out := make([]Visit, 0, limit)
	for rows.Next() {
		var v Visit
		if err := rows.Scan(&v.ID, &v.AccountID, &v.Seed, &v.JoinedAt, &v.Seconds, &v.Beer, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("gamevanyadum: scan visit: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gamevanyadum: iterate visits: %w", err)
	}
	return out, nil
}
