package game

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
		`INSERT INTO game_runs (account_id, game_key, character_key, success, steps)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING id::text, account_id::text, game_key, character_key, success, steps, created_at`,
		accountID, gameKey, characterKey, success, steps,
	).Scan(&run.ID, &run.AccountID, &run.GameKey, &run.CharacterKey, &run.Success, &run.Steps, &run.CreatedAt)
	return run, err
}

func (PostgresRepository) Leaderboard(ctx context.Context, q db.DBTX, gameKey string, limit int) ([]LeaderboardEntry, error) {
	rows, err := q.Query(ctx, `
		SELECT account_id::text,
		       count(*) FILTER (WHERE success) AS successes,
		       count(*)                        AS plays,
		       COALESCE(sum(steps), 0)         AS steps
		FROM game_runs
		WHERE game_key = $1 AND deleted_at IS NULL
		GROUP BY account_id
		ORDER BY successes DESC, steps ASC
		LIMIT $2`, gameKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeaderboardEntry
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.AccountID, &e.Successes, &e.Plays, &e.Steps); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (PostgresRepository) StatsFor(ctx context.Context, q db.DBTX, gameKey, accountID string) (PlayerStats, error) {
	var st PlayerStats
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE success),
		       count(*),
		       COALESCE(min(steps) FILTER (WHERE success), 0)
		FROM game_runs
		WHERE game_key = $1 AND account_id = $2::uuid AND deleted_at IS NULL`,
		gameKey, accountID,
	).Scan(&st.Successes, &st.Plays, &st.BestSteps)
	return st, err
}
