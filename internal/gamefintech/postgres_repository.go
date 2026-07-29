package gamefintech

import (
	"context"
	"fmt"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostgresRepository is the pgx-backed Repository, and the only file in this
// package with SQL in it. Three statements, all trivial: this game's durable
// footprint is one summary row per finished shift.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

// shiftColumns is the projection every read uses, so the scan order can only be
// got wrong in one place.
const shiftColumns = `id, account_id, cause, salary, seconds, created_at`

// InsertShift records a finished shift.
func (PostgresRepository) InsertShift(ctx context.Context, q db.DBTX, s Shift) error {
	_, err := q.Exec(ctx,
		`INSERT INTO game_fintech_shifts (id, account_id, cause, salary, seconds)
		 VALUES ($1, $2, $3, $4, $5)`,
		s.ID, s.AccountID, s.Cause, s.Salary, s.Seconds)
	if err != nil {
		return fmt.Errorf("gamefintech: insert shift: %w", err)
	}
	return nil
}

// RecentShifts lists an account's own last shifts, newest first.
func (PostgresRepository) RecentShifts(ctx context.Context, q db.DBTX, accountID uuid.UUID, limit int) ([]Shift, error) {
	rows, err := q.Query(ctx,
		`SELECT `+shiftColumns+`
		   FROM game_fintech_shifts
		  WHERE account_id = $1 AND deleted_at IS NULL
		  ORDER BY created_at DESC
		  LIMIT $2`,
		accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("gamefintech: recent shifts: %w", err)
	}
	return scanShifts(rows, limit, "recent")
}

// TopShifts is the leaderboard: the BEST SHIFT PER ACCOUNT.
//
// DISTINCT ON is what makes that one statement rather than a window function and
// a subquery — Postgres keeps the first row of each account_id group in the ORDER
// BY, so the ordering has to lead with account_id and then with what "best"
// means. The outer query then re-sorts those winners by salary, because the
// board is read by money and not by account id.
func (PostgresRepository) TopShifts(ctx context.Context, q db.DBTX, limit int) ([]Shift, error) {
	rows, err := q.Query(ctx,
		`SELECT `+shiftColumns+`
		   FROM (
		     SELECT DISTINCT ON (account_id) `+shiftColumns+`
		       FROM game_fintech_shifts
		      WHERE deleted_at IS NULL
		      ORDER BY account_id, salary DESC, created_at DESC
		   ) best
		  ORDER BY salary DESC, created_at ASC
		  LIMIT $1`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("gamefintech: top shifts: %w", err)
	}
	return scanShifts(rows, limit, "top")
}

// scanShifts drains a projection of shiftColumns. It closes the rows.
func scanShifts(rows pgx.Rows, limit int, what string) ([]Shift, error) {
	defer rows.Close()
	out := make([]Shift, 0, limit)
	for rows.Next() {
		var s Shift
		if err := rows.Scan(&s.ID, &s.AccountID, &s.Cause, &s.Salary, &s.Seconds, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("gamefintech: scan %s shift: %w", what, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gamefintech: iterate %s shifts: %w", what, err)
	}
	return out, nil
}
