package game

import (
	"context"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// defaultLeaderboardLimit caps a leaderboard read when no limit is given.
const defaultLeaderboardLimit = 20

// maxLeaderboardLimit caps a caller-supplied limit.
const maxLeaderboardLimit = 100

// Service is the game business logic: serve content, judge dialogue turns,
// record runs, read the leaderboard.
type Service struct {
	q    db.DBTX
	repo Repository
	eval Evaluator
}

// NewService wires the game service with an evaluator (the LLM judge).
func NewService(q db.DBTX, repo Repository, eval Evaluator) *Service {
	return &Service{q: q, repo: repo, eval: eval}
}

// Content returns the game config (characters, assets). Server-side persona
// fields are hidden by json tags; answer options are LLM-generated, not here.
func (s *Service) Content(gameKey string) (Game, error) {
	return ContentFor(gameKey)
}

// Judge evaluates one dialogue turn against a character. transcript is the
// conversation so far; choice is what the player just said ("" on the opening
// turn).
func (s *Service) Judge(ctx context.Context, gameKey, characterKey string, transcript []Exchange, choice string) (TurnResult, error) {
	g, err := ContentFor(gameKey)
	if err != nil {
		return TurnResult{}, err
	}
	ch, ok := g.findCharacter(characterKey)
	if !ok {
		return TurnResult{}, ErrUnknownCharacter
	}
	return s.eval.Judge(ctx, ch, transcript, choice)
}

// SubmitRun validates and records a finished run.
func (s *Service) SubmitRun(ctx context.Context, accountID, gameKey, characterKey string, success bool, steps int) (Run, error) {
	g, err := ContentFor(gameKey)
	if err != nil {
		return Run{}, err
	}
	if _, ok := g.findCharacter(characterKey); !ok {
		return Run{}, ErrUnknownCharacter
	}
	if steps < 0 || steps > maxSteps {
		return Run{}, ErrStepsRange
	}
	return s.repo.RecordRun(ctx, s.q, accountID, gameKey, characterKey, success, steps)
}

// Leaderboard returns the per-account aggregate for a game.
func (s *Service) Leaderboard(ctx context.Context, gameKey string, limit int) ([]LeaderboardEntry, error) {
	if !KnownGame(gameKey) {
		return nil, ErrUnknownGame
	}
	if limit <= 0 {
		limit = defaultLeaderboardLimit
	}
	if limit > maxLeaderboardLimit {
		limit = maxLeaderboardLimit
	}
	return s.repo.Leaderboard(ctx, s.q, gameKey, limit)
}

// Stats returns a single player's summary for a game.
func (s *Service) Stats(ctx context.Context, gameKey, accountID string) (PlayerStats, error) {
	if !KnownGame(gameKey) {
		return PlayerStats{}, ErrUnknownGame
	}
	return s.repo.StatsFor(ctx, s.q, gameKey, accountID)
}

// Asset returns an art image's bytes + content type (from the DB blob store).
func (s *Service) Asset(ctx context.Context, gameKey, artKey string) ([]byte, string, error) {
	return s.repo.AssetBytes(ctx, s.q, gameKey, artKey)
}

// AssetKeys returns the set of art keys that have an uploaded image for a game.
func (s *Service) AssetKeys(ctx context.Context, gameKey string) (map[string]bool, error) {
	keys, err := s.repo.AssetKeys(ctx, s.q, gameKey)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set, nil
}
