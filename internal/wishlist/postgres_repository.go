package wishlist

import (
	"context"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

func (PostgresRepository) Create(ctx context.Context, q db.DBTX, authorID, title, body string) (Item, error) {
	var it Item
	err := q.QueryRow(ctx,
		`INSERT INTO wishlist_items (account_id, title, body)
		 VALUES ($1::uuid, $2, $3)
		 RETURNING id::text, account_id::text, title, body, created_at`,
		authorID, title, body,
	).Scan(&it.ID, &it.AuthorID, &it.Title, &it.Body, &it.CreatedAt)
	return it, err
}

func (PostgresRepository) List(ctx context.Context, q db.DBTX, viewerID string, sort Sort) ([]Item, error) {
	order := "votes DESC, i.created_at DESC"
	if sort == SortNew {
		order = "i.created_at DESC"
	}
	rows, err := q.Query(ctx, `
		SELECT i.id::text, i.account_id::text, i.title, i.body, i.created_at,
		       COUNT(v.id) AS votes,
		       COALESCE(bool_or(v.account_id = $1::uuid), false) AS voted_by_me
		FROM wishlist_items i
		LEFT JOIN wishlist_votes v ON v.item_id = i.id
		WHERE i.deleted_at IS NULL
		GROUP BY i.id
		ORDER BY `+order, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.AuthorID, &it.Title, &it.Body, &it.CreatedAt, &it.Votes, &it.VotedByMe); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (PostgresRepository) Exists(ctx context.Context, q db.DBTX, itemID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM wishlist_items WHERE id = $1::uuid AND deleted_at IS NULL)`, itemID,
	).Scan(&ok)
	return ok, err
}

func (PostgresRepository) AddVote(ctx context.Context, q db.DBTX, itemID, accountID string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO wishlist_votes (item_id, account_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT (item_id, account_id) DO NOTHING`, itemID, accountID)
	return err
}

func (PostgresRepository) RemoveVote(ctx context.Context, q db.DBTX, itemID, accountID string) error {
	_, err := q.Exec(ctx,
		`DELETE FROM wishlist_votes WHERE item_id = $1::uuid AND account_id = $2::uuid`, itemID, accountID)
	return err
}
