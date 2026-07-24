package game

import (
	"context"
	"slices"
	"strings"

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
// turn); anger is the tension going into the turn (the client carries it between
// turns, like the transcript).
func (s *Service) Judge(ctx context.Context, gameKey, characterKey string, transcript []Exchange, choice string, anger int) (TurnResult, error) {
	g, err := ContentFor(gameKey)
	if err != nil {
		return TurnResult{}, err
	}
	ch, ok := g.findCharacter(characterKey)
	if !ok {
		return TurnResult{}, ErrUnknownCharacter
	}
	return s.eval.Judge(ctx, ch, transcript, choice, anger)
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

// Leaderboard returns the four record boards for a game, each sorted and capped
// at limit. A player appears on a board only if they hold a record of that kind:
// no wins yet means no place on either win board.
func (s *Service) Leaderboard(ctx context.Context, gameKey string, limit int) (Boards, error) {
	if !KnownGame(gameKey) {
		return nil, ErrUnknownGame
	}
	if limit <= 0 {
		limit = defaultLeaderboardLimit
	}
	if limit > maxLeaderboardLimit {
		limit = maxLeaderboardLimit
	}
	records, err := s.repo.Records(ctx, s.q, gameKey)
	if err != nil {
		return nil, err
	}
	return buildBoards(records, limit), nil
}

// buildBoards turns per-account records into the four ranked boards. "Longest"
// boards sort by steps descending, "shortest" ascending; ties break on account
// id so the order is stable between calls.
func buildBoards(records []PlayerRecords, limit int) Boards {
	pick := map[RecordBoard]struct {
		steps func(PlayerRecords) *int
		desc  bool
	}{
		BoardLongestWin:   {func(p PlayerRecords) *int { return p.LongestWin }, true},
		BoardShortestWin:  {func(p PlayerRecords) *int { return p.ShortestWin }, false},
		BoardLongestLoss:  {func(p PlayerRecords) *int { return p.LongestLoss }, true},
		BoardShortestLoss: {func(p PlayerRecords) *int { return p.ShortestLoss }, false},
	}

	boards := make(Boards, len(RecordBoards))
	for _, board := range RecordBoards {
		spec := pick[board]
		entries := make([]RecordEntry, 0, len(records))
		for _, p := range records {
			if steps := spec.steps(p); steps != nil {
				entries = append(entries, RecordEntry{
					AccountID: p.AccountID,
					Steps:     *steps,
					Plays:     p.Plays,
					Wins:      p.Wins,
					Losses:    p.Losses,
				})
			}
		}
		slices.SortFunc(entries, func(a, b RecordEntry) int {
			if a.Steps != b.Steps {
				if spec.desc {
					return b.Steps - a.Steps
				}
				return a.Steps - b.Steps
			}
			return strings.Compare(a.AccountID, b.AccountID)
		})
		if len(entries) > limit {
			entries = entries[:limit]
		}
		boards[board] = entries
	}
	return boards
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
