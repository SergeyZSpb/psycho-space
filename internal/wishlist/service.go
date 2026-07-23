package wishlist

import (
	"context"
	"strings"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// Service is the wishlist business logic.
type Service struct {
	q    db.DBTX
	repo Repository
}

// NewService wires the wishlist service.
func NewService(q db.DBTX, repo Repository) *Service {
	return &Service{q: q, repo: repo}
}

// Create adds a new idea authored by authorID.
func (s *Service) Create(ctx context.Context, authorID, title, body string) (Item, error) {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		return Item{}, ErrEmptyTitle
	}
	if len(title) > maxTitle || len(body) > maxBody {
		return Item{}, ErrTooLong
	}
	return s.repo.Create(ctx, s.q, authorID, title, body)
}

// List returns items ordered by sort (defaulting to top).
func (s *Service) List(ctx context.Context, viewerID string, sort Sort) ([]Item, error) {
	if sort != SortTop && sort != SortNew {
		sort = SortTop
	}
	return s.repo.List(ctx, s.q, viewerID, sort)
}

// Vote registers an upvote (idempotent).
func (s *Service) Vote(ctx context.Context, itemID, accountID string) error {
	ok, err := s.repo.Exists(ctx, s.q, itemID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return s.repo.AddVote(ctx, s.q, itemID, accountID)
}

// Unvote removes an upvote (idempotent).
func (s *Service) Unvote(ctx context.Context, itemID, accountID string) error {
	return s.repo.RemoveVote(ctx, s.q, itemID, accountID)
}
