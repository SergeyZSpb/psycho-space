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

// NewService wires the game service with the mock evaluator.
func NewService(q db.DBTX, repo Repository) *Service {
	return &Service{q: q, repo: repo, eval: MockEvaluator{}}
}

// NewServiceWithEvaluator wires the service with an explicit evaluator (used to
// inject the LLM judge once credentials exist).
func NewServiceWithEvaluator(q db.DBTX, repo Repository, eval Evaluator) *Service {
	return &Service{q: q, repo: repo, eval: eval}
}

// Content returns the game config (characters, options, assets). Server-side
// fields (persona prompts, mock tuning) are hidden by json tags.
func (s *Service) Content(gameKey string) (Game, error) {
	return ContentFor(gameKey)
}

// Judge evaluates one dialogue turn against a character.
func (s *Service) Judge(ctx context.Context, gameKey, characterKey string, priorOptionIDs []string, optionID string) (TurnResult, error) {
	g, err := ContentFor(gameKey)
	if err != nil {
		return TurnResult{}, err
	}
	ch, ok := g.findCharacter(characterKey)
	if !ok {
		return TurnResult{}, ErrUnknownCharacter
	}
	return s.eval.Judge(ctx, ch, priorOptionIDs, optionID)
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
