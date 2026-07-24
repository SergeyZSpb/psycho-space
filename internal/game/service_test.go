package game

import (
	"context"
	"errors"
	"testing"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// fakeRepo records calls and returns canned data; it lets us unit-test the
// service's validation/clamping without a database.
type fakeRepo struct {
	recordCalls int
	gotLimit    int
}

func (f *fakeRepo) RecordRun(_ context.Context, _ db.DBTX, accountID, gameKey, characterKey string, success bool, steps int) (Run, error) {
	f.recordCalls++
	return Run{ID: "run-1", AccountID: accountID, GameKey: gameKey, CharacterKey: characterKey, Success: success, Steps: steps}, nil
}

func (f *fakeRepo) Leaderboard(_ context.Context, _ db.DBTX, _ string, limit int) ([]LeaderboardEntry, error) {
	f.gotLimit = limit
	return nil, nil
}

func (f *fakeRepo) StatsFor(_ context.Context, _ db.DBTX, _, _ string) (PlayerStats, error) {
	return PlayerStats{}, nil
}

// stubEval is a test double for the LLM judge, so service-level tests don't do I/O.
type stubEval struct {
	res           TurnResult
	err           error
	gotChoice     string
	gotTranscript []Exchange
	calls         int
}

func (s *stubEval) Judge(_ context.Context, _ Character, transcript []Exchange, choice string) (TurnResult, error) {
	s.calls++
	s.gotTranscript = transcript
	s.gotChoice = choice
	return s.res, s.err
}

func newSvc(repo Repository, eval Evaluator) *Service { return NewService(nil, repo, eval) }

func defaultChar(t *testing.T) Character {
	t.Helper()
	g, err := ContentFor(GameSmalltalkKhimki)
	if err != nil {
		t.Fatalf("ContentFor: %v", err)
	}
	ch, ok := g.findCharacter(g.DefaultCharacter)
	if !ok {
		t.Fatalf("default character %q not found in game", g.DefaultCharacter)
	}
	return ch
}

func TestContentFor(t *testing.T) {
	g, err := ContentFor(GameSmalltalkKhimki)
	if err != nil {
		t.Fatalf("ContentFor: %v", err)
	}
	if g.GameKey != GameSmalltalkKhimki || len(g.Characters) == 0 || g.DefaultCharacter == "" {
		t.Fatalf("game = %+v; want key + characters + default", g)
	}
	ch := defaultChar(t)
	if ch.Goal == "" || ch.Greeting == "" || len(ch.Arts) == 0 || ch.MaxSteps <= 0 {
		t.Fatalf("character %q underspecified: %+v", ch.Key, ch)
	}
	// Persona prompt material must be present (it drives the LLM judge).
	if ch.Persona == "" || ch.Motivation == "" || ch.TalkStyle == "" {
		t.Fatalf("character %q missing persona prompt fields: %+v", ch.Key, ch)
	}
	if _, err := ContentFor("nope"); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
}

func TestServiceJudgeRouting(t *testing.T) {
	charKey := defaultChar(t).Key

	// Unknown game / character short-circuit before the evaluator.
	if _, err := newSvc(&fakeRepo{}, &stubEval{}).Judge(context.Background(), "nope", charKey, nil, ""); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
	if _, err := newSvc(&fakeRepo{}, &stubEval{}).Judge(context.Background(), GameSmalltalkKhimki, "nobody", nil, ""); !errors.Is(err, ErrUnknownCharacter) {
		t.Fatalf("unknown character err = %v; want ErrUnknownCharacter", err)
	}

	// A valid call is delegated to the evaluator with the transcript + choice.
	ev := &stubEval{res: TurnResult{Reply: "ок", Art: "vanya_neutral", Achieved: true}}
	tr := []Exchange{{Choice: "привет", Reply: "ну"}}
	res, err := newSvc(&fakeRepo{}, ev).Judge(context.Background(), GameSmalltalkKhimki, charKey, tr, "домой")
	if err != nil || !res.Achieved || ev.calls != 1 || ev.gotChoice != "домой" || len(ev.gotTranscript) != 1 {
		t.Fatalf("delegate: res=%+v err=%v ev=%+v", res, err, ev)
	}
}

func TestSubmitRunValidation(t *testing.T) {
	charKey := defaultChar(t).Key
	tests := []struct {
		name    string
		gameKey string
		charKey string
		steps   int
		wantErr error
	}{
		{"ok success", GameSmalltalkKhimki, charKey, 3, nil},
		{"unknown game", "nope", charKey, 1, ErrUnknownGame},
		{"unknown character", GameSmalltalkKhimki, "nobody", 1, ErrUnknownCharacter},
		{"negative steps", GameSmalltalkKhimki, charKey, -1, ErrStepsRange},
		{"huge steps", GameSmalltalkKhimki, charKey, maxSteps + 1, ErrStepsRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			_, err := newSvc(repo, &stubEval{}).SubmitRun(context.Background(), "acc-1", tt.gameKey, tt.charKey, true, tt.steps)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v; want %v", err, tt.wantErr)
			}
			if want := tt.wantErr == nil; (repo.recordCalls == 1) != want {
				t.Fatalf("recordCalls=%d; wantRecorded=%v", repo.recordCalls, want)
			}
		})
	}
}

func TestLeaderboardLimitClamped(t *testing.T) {
	tests := []struct{ in, want int }{
		{0, defaultLeaderboardLimit},
		{-5, defaultLeaderboardLimit},
		{10, 10},
		{maxLeaderboardLimit + 50, maxLeaderboardLimit},
	}
	for _, tt := range tests {
		repo := &fakeRepo{}
		if _, err := newSvc(repo, &stubEval{}).Leaderboard(context.Background(), GameSmalltalkKhimki, tt.in); err != nil {
			t.Fatalf("Leaderboard(%d): %v", tt.in, err)
		}
		if repo.gotLimit != tt.want {
			t.Fatalf("limit in=%d -> repo got %d; want %d", tt.in, repo.gotLimit, tt.want)
		}
	}
	if _, err := newSvc(&fakeRepo{}, &stubEval{}).Leaderboard(context.Background(), "nope", 5); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
}
