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

// TopShifts is the leaderboard by money: the BEST SHIFT PER ACCOUNT.
func (r PostgresRepository) TopShifts(ctx context.Context, q db.DBTX, limit int) ([]Shift, error) {
	return r.topShiftsBy(ctx, q, metricSalary, limit)
}

// TopShiftsBySeconds is the same board scored on how long the shift lasted.
func (r PostgresRepository) TopShiftsBySeconds(ctx context.Context, q db.DBTX, limit int) ([]Shift, error) {
	return r.topShiftsBy(ctx, q, metricSeconds, limit)
}

// The two things a shift is scored on. PACKAGE CONSTANTS, and that is what makes
// the interpolation below safe: `metric` is chosen from these two by a method on
// this type and never reaches here from a request, so there is no injection
// surface — no caller can name a third string.
const (
	metricSalary  = "salary"
	metricSeconds = "seconds"
)

// topShiftsBy is the leaderboard, scored on one column: the BEST SHIFT PER
// ACCOUNT.
//
// DISTINCT ON is what makes that one statement rather than a window function and
// a subquery — Postgres keeps the first row of each account_id group in the ORDER
// BY, so the ordering has to lead with account_id and then with what "best"
// means. The outer query then re-sorts those winners by the same column, because
// the board is read by the score and not by account id.
//
// ONE FUNCTION RATHER THAN TWO NEARLY-IDENTICAL STATEMENTS. The DISTINCT ON shape
// is the subtle part — the leading account_id, the tie-break on created_at, the
// re-sort outside — and a copy of it would be a second place for that subtlety to
// be got wrong the day either board is retuned. The only thing that varies is a
// column name, and it comes from the pair above.
//
// No index exists for the seconds ordering and none is added: this table holds
// one row per finished shift for a handful of friends, so the sort is a few
// hundred rows in memory, and a migration is forever.
func (PostgresRepository) topShiftsBy(ctx context.Context, q db.DBTX, metric string, limit int) ([]Shift, error) {
	rows, err := q.Query(ctx,
		`SELECT `+shiftColumns+`
		   FROM (
		     SELECT DISTINCT ON (account_id) `+shiftColumns+`
		       FROM game_fintech_shifts
		      WHERE deleted_at IS NULL
		      ORDER BY account_id, `+metric+` DESC, created_at DESC
		   ) best
		  ORDER BY `+metric+` DESC, created_at ASC
		  LIMIT $1`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("gamefintech: top shifts by %s: %w", metric, err)
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
