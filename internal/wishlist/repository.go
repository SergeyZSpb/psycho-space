package wishlist

import (
	"context"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// Repository is the storage boundary for wishlist items and votes.
type Repository interface {
	Create(ctx context.Context, q db.DBTX, authorID, title, body string) (Item, error)
	List(ctx context.Context, q db.DBTX, viewerID string, sort Sort) ([]Item, error)
	Exists(ctx context.Context, q db.DBTX, itemID string) (bool, error)
	AddVote(ctx context.Context, q db.DBTX, itemID, accountID string) error
	RemoveVote(ctx context.Context, q db.DBTX, itemID, accountID string) error
}
