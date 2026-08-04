package gamefintech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

// layoutBody is what a row's `body` column holds: the geometry and nothing else.
//
// THE ID IS NOT STORED, and that is the point of it being a content hash. It is
// recomputed on load, so the id a client is told can never disagree with the
// floor it describes — there is no path by which a row could carry a stale one.
type layoutBody struct {
	Solids  []Solid  `json:"solids"`
	Windows []Window `json:"windows"`
}

// CurrentLayout reads the floor in force: the newest row still standing.
func (PostgresRepository) CurrentLayout(ctx context.Context, q db.DBTX) (Layout, error) {
	var body []byte
	err := q.QueryRow(ctx,
		`SELECT body FROM game_fintech_layouts
		 WHERE deleted_at IS NULL
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return Layout{}, ErrNoLayout
	}
	if err != nil {
		return Layout{}, fmt.Errorf("gamefintech: read layout: %w", err)
	}
	var lb layoutBody
	if err := json.Unmarshal(body, &lb); err != nil {
		return Layout{}, fmt.Errorf("gamefintech: decode layout: %w", err)
	}
	return Layout{Solids: lb.Solids, Windows: lb.Windows}.WithID(), nil
}

// InsertLayout writes a floor and makes it the current one.
func (PostgresRepository) InsertLayout(ctx context.Context, q db.DBTX, l Layout, source string) error {
	body, err := json.Marshal(layoutBody{Solids: l.Solids, Windows: l.Windows})
	if err != nil {
		return fmt.Errorf("gamefintech: encode layout: %w", err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO game_fintech_layouts (source, body) VALUES ($1, $2)`,
		source, body); err != nil {
		return fmt.Errorf("gamefintech: insert layout: %w", err)
	}
	return nil
}

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

// TopShifts is the leaderboard by money: the BEST SHIFT PER ACCOUNT, and only
// among shifts no older than `since`.
func (r PostgresRepository) TopShifts(ctx context.Context, q db.DBTX, since time.Time, limit int) ([]Shift, error) {
	return r.topShiftsBy(ctx, q, metricSalary, since, limit)
}

// TopShiftsBySeconds is the same board scored on how long the shift lasted.
func (r PostgresRepository) TopShiftsBySeconds(ctx context.Context, q db.DBTX, since time.Time, limit int) ([]Shift, error) {
	return r.topShiftsBy(ctx, q, metricSeconds, since, limit)
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
// THE WINDOW IS INSIDE THE DISTINCT ON, and it has to be. Filtering the outer
// query instead would pick each account's best shift of all time and then drop it
// for being old — so a player whose best week was in February would be missing
// from the board entirely, rather than ranked on the best he has done since.
// «Best of the last seven days» is what the inner query has to mean.
func (PostgresRepository) topShiftsBy(ctx context.Context, q db.DBTX, metric string, since time.Time, limit int) ([]Shift, error) {
	rows, err := q.Query(ctx,
		`SELECT `+shiftColumns+`
		   FROM (
		     SELECT DISTINCT ON (account_id) `+shiftColumns+`
		       FROM game_fintech_shifts
		      WHERE deleted_at IS NULL AND created_at >= $1
		      ORDER BY account_id, `+metric+` DESC, created_at DESC
		   ) best
		  ORDER BY `+metric+` DESC, created_at ASC
		  LIMIT $2`,
		since, limit)
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
