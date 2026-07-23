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

// DeleteItem soft-deletes an item. The author, or any admin/superadmin, may delete it.
func (s *Service) DeleteItem(ctx context.Context, itemID, actorID string, isAdmin bool) error {
	author, err := s.repo.ItemAuthor(ctx, s.q, itemID)
	if err != nil {
		return err
	}
	if author != actorID && !isAdmin {
		return ErrForbidden
	}
	return s.repo.SoftDeleteItem(ctx, s.q, itemID)
}

// DeleteComment soft-deletes a comment. The author, or any admin/superadmin, may delete it.
func (s *Service) DeleteComment(ctx context.Context, commentID, actorID string, isAdmin bool) error {
	author, err := s.repo.CommentAuthor(ctx, s.q, commentID)
	if err != nil {
		return err
	}
	if author != actorID && !isAdmin {
		return ErrForbidden
	}
	return s.repo.SoftDeleteComment(ctx, s.q, commentID)
}

// AddComment adds a comment to an item.
func (s *Service) AddComment(ctx context.Context, itemID, authorID, body string) (Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Comment{}, ErrEmptyComment
	}
	if len(body) > maxCommentBody {
		return Comment{}, ErrTooLong
	}
	ok, err := s.repo.Exists(ctx, s.q, itemID)
	if err != nil {
		return Comment{}, err
	}
	if !ok {
		return Comment{}, ErrNotFound
	}
	return s.repo.CreateComment(ctx, s.q, itemID, authorID, body)
}

// ListComments returns the comments on an item (top-voted first).
func (s *Service) ListComments(ctx context.Context, itemID, viewerID string) ([]Comment, error) {
	return s.repo.ListComments(ctx, s.q, itemID, viewerID)
}

// VoteComment upvotes a comment (idempotent).
func (s *Service) VoteComment(ctx context.Context, commentID, accountID string) error {
	ok, err := s.repo.CommentExists(ctx, s.q, commentID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrCommentNotFound
	}
	return s.repo.AddCommentVote(ctx, s.q, commentID, accountID)
}

// UnvoteComment removes a comment upvote (idempotent).
func (s *Service) UnvoteComment(ctx context.Context, commentID, accountID string) error {
	return s.repo.RemoveCommentVote(ctx, s.q, commentID, accountID)
}
