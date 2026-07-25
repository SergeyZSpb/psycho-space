package gamekhimki

import (
	"context"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

func (PostgresRepository) RecordRun(ctx context.Context, q db.DBTX, accountID, gameKey, characterKey string, success bool, steps int) (Run, error) {
	var run Run
	err := q.QueryRow(ctx,
		`INSERT INTO game_khimki_runs (account_id, game_key, character_key, success, steps)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING id::text, account_id::text, game_key, character_key, success, steps, created_at`,
		accountID, gameKey, characterKey, success, steps,
	).Scan(&run.ID, &run.AccountID, &run.GameKey, &run.CharacterKey, &run.Success, &run.Steps, &run.CreatedAt)
	return run, err
}

// Records returns one row per account holding its four extreme step counts. The
// aggregates are NULL for a player with no win (or no loss) yet, so they scan
// into pointers and the service leaves those accounts off that board. One query
// feeds all four boards — the data is tiny, so ranking happens in Go.
func (PostgresRepository) Records(ctx context.Context, q db.DBTX, gameKey string) ([]PlayerRecords, error) {
	rows, err := q.Query(ctx, `
		SELECT account_id::text,
		       count(*)                                  AS plays,
		       count(*) FILTER (WHERE success)            AS wins,
		       count(*) FILTER (WHERE NOT success)        AS losses,
		       max(steps) FILTER (WHERE success)          AS longest_win,
		       min(steps) FILTER (WHERE success)          AS shortest_win,
		       max(steps) FILTER (WHERE NOT success)      AS longest_loss,
		       min(steps) FILTER (WHERE NOT success)      AS shortest_loss
		FROM game_khimki_runs
		WHERE game_key = $1 AND deleted_at IS NULL
		GROUP BY account_id`, gameKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayerRecords
	for rows.Next() {
		var p PlayerRecords
		if err := rows.Scan(&p.AccountID, &p.Plays, &p.Wins, &p.Losses,
			&p.LongestWin, &p.ShortestWin, &p.LongestLoss, &p.ShortestLoss); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (PostgresRepository) StatsFor(ctx context.Context, q db.DBTX, gameKey, accountID string) (PlayerStats, error) {
	var st PlayerStats
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE success),
		       count(*),
		       COALESCE(min(steps) FILTER (WHERE success), 0)
		FROM game_khimki_runs
		WHERE game_key = $1 AND account_id = $2::uuid AND deleted_at IS NULL`,
		gameKey, accountID,
	).Scan(&st.Successes, &st.Plays, &st.BestSteps)
	return st, err
}
