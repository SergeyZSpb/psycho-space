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
		       (SELECT count(*) FROM wishlist_votes v WHERE v.item_id = i.id) AS votes,
		       EXISTS(SELECT 1 FROM wishlist_votes v WHERE v.item_id = i.id AND v.account_id = $1::uuid) AS voted_by_me,
		       (SELECT count(*) FROM wishlist_comments c WHERE c.item_id = i.id AND c.deleted_at IS NULL) AS comment_count
		FROM wishlist_items i
		WHERE i.deleted_at IS NULL
		ORDER BY `+order, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.AuthorID, &it.Title, &it.Body, &it.CreatedAt, &it.Votes, &it.VotedByMe, &it.CommentCount); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (PostgresRepository) CreateComment(ctx context.Context, q db.DBTX, itemID, authorID, body string) (Comment, error) {
	var c Comment
	err := q.QueryRow(ctx,
		`INSERT INTO wishlist_comments (item_id, account_id, body)
		 VALUES ($1::uuid, $2::uuid, $3)
		 RETURNING id::text, item_id::text, account_id::text, body, created_at`,
		itemID, authorID, body,
	).Scan(&c.ID, &c.ItemID, &c.AuthorID, &c.Body, &c.CreatedAt)
	return c, err
}

func (PostgresRepository) ListComments(ctx context.Context, q db.DBTX, itemID, viewerID string) ([]Comment, error) {
	rows, err := q.Query(ctx, `
		SELECT c.id::text, c.item_id::text, c.account_id::text, c.body, c.created_at,
		       (SELECT count(*) FROM wishlist_comment_votes cv WHERE cv.comment_id = c.id) AS votes,
		       EXISTS(SELECT 1 FROM wishlist_comment_votes cv WHERE cv.comment_id = c.id AND cv.account_id = $2::uuid) AS voted_by_me
		FROM wishlist_comments c
		WHERE c.item_id = $1::uuid AND c.deleted_at IS NULL
		ORDER BY votes DESC, c.created_at ASC`, itemID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.ItemID, &c.AuthorID, &c.Body, &c.CreatedAt, &c.Votes, &c.VotedByMe); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (PostgresRepository) CommentExists(ctx context.Context, q db.DBTX, commentID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM wishlist_comments WHERE id = $1::uuid AND deleted_at IS NULL)`, commentID,
	).Scan(&ok)
	return ok, err
}

func (PostgresRepository) AddCommentVote(ctx context.Context, q db.DBTX, commentID, accountID string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO wishlist_comment_votes (comment_id, account_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT (comment_id, account_id) DO NOTHING`, commentID, accountID)
	return err
}

func (PostgresRepository) RemoveCommentVote(ctx context.Context, q db.DBTX, commentID, accountID string) error {
	_, err := q.Exec(ctx,
		`DELETE FROM wishlist_comment_votes WHERE comment_id = $1::uuid AND account_id = $2::uuid`, commentID, accountID)
	return err
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
