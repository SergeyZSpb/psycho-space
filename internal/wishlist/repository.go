package wishlist

import (
	"context"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// Repository is the storage boundary for wishlist items, comments, and votes.
type Repository interface {
	Create(ctx context.Context, q db.DBTX, authorID, title, body string) (Item, error)
	List(ctx context.Context, q db.DBTX, viewerID string, sort Sort) ([]Item, error)
	Exists(ctx context.Context, q db.DBTX, itemID string) (bool, error)
	AddVote(ctx context.Context, q db.DBTX, itemID, accountID string) error
	RemoveVote(ctx context.Context, q db.DBTX, itemID, accountID string) error

	ItemAuthor(ctx context.Context, q db.DBTX, itemID string) (string, error)
	SoftDeleteItem(ctx context.Context, q db.DBTX, itemID string) error

	CreateComment(ctx context.Context, q db.DBTX, itemID, authorID, body string) (Comment, error)
	ListComments(ctx context.Context, q db.DBTX, itemID, viewerID string) ([]Comment, error)
	CommentExists(ctx context.Context, q db.DBTX, commentID string) (bool, error)
	CommentAuthor(ctx context.Context, q db.DBTX, commentID string) (string, error)
	SoftDeleteComment(ctx context.Context, q db.DBTX, commentID string) error
	AddCommentVote(ctx context.Context, q db.DBTX, commentID, accountID string) error
	RemoveCommentVote(ctx context.Context, q db.DBTX, commentID, accountID string) error
}
