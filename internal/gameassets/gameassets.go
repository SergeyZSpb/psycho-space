// Package gameassets stores and serves game art as blobs in Postgres, keyed by
// (game_key, art_key).
//
// This is shared infrastructure, not a game, and its unprefixed name says so:
// every game module is named Game<Name> at every layer, so the absence of that
// prefix is the signal that a package is game-agnostic (see CLAUDE.md → Games
// are self-contained modules). Storing bytes and serving them with a validated
// content type is a mechanism any game needs; it encodes no rule of any
// particular game. A game owns its state — runs, scores, pets — and must never
// share that; it does not own the blob store.
//
// The table has carried a game_key discriminator since it was created, so it was
// always a multi-game store. A game scopes its art by passing its own key.
package gameassets

import (
	"context"
	"errors"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// ErrNotFound means no blob exists for that (game, art) pair.
var ErrNotFound = errors.New("gameassets: not found")

// Repository is the storage boundary. Methods take a db.DBTX so they compose
// with transactions.
type Repository interface {
	// Bytes returns an art image's bytes and content type, or ErrNotFound.
	Bytes(ctx context.Context, q db.DBTX, gameKey, artKey string) ([]byte, string, error)
	// Keys returns the art keys that have an uploaded image for a game.
	Keys(ctx context.Context, q db.DBTX, gameKey string) ([]string, error)
}

// Service reads game art. It holds no game-specific knowledge: which keys a
// game expects lives in that game's own catalogue.
type Service struct {
	q    db.DBTX
	repo Repository
}

// NewService builds the service over a pool or transaction.
func NewService(q db.DBTX, repo Repository) *Service {
	return &Service{q: q, repo: repo}
}

// Bytes returns an art image's bytes and content type, or ErrNotFound.
func (s *Service) Bytes(ctx context.Context, gameKey, artKey string) ([]byte, string, error) {
	return s.repo.Bytes(ctx, s.q, gameKey, artKey)
}

// PresentKeys returns the art keys that have an uploaded image, as a set. A game
// uses it to advertise an image URL only for art that actually has one, falling
// back to a placeholder for the rest — so art is never a hard dependency and a
// game ships playable with zero images.
func (s *Service) PresentKeys(ctx context.Context, gameKey string) (map[string]bool, error) {
	keys, err := s.repo.Keys(ctx, s.q, gameKey)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set, nil
}
