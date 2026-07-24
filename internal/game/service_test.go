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

func newSvc(repo Repository) *Service { return NewService(nil, repo) }

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
	if ch.Goal == "" || ch.Greeting == "" || len(ch.Options) == 0 || len(ch.Emotions) == 0 {
		t.Fatalf("character %q underspecified: %+v", ch.Key, ch)
	}
	if ch.MaxSteps <= 0 || ch.Threshold <= 0 {
		t.Fatalf("character %q needs MaxSteps>0 and Threshold>0: %+v", ch.Key, ch)
	}
	if _, err := ContentFor("nope"); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
}

func TestMockEvaluator(t *testing.T) {
	ch := defaultChar(t)
	ev := MockEvaluator{}
	ctx := context.Background()

	// A single strong option (+2) is short of the threshold (3): not achieved.
	if res, err := ev.Judge(ctx, ch, nil, "domofon"); err != nil || res.Achieved || res.Reply == "" {
		t.Fatalf("domofon: res=%+v err=%v; want not-achieved with a reply", res, err)
	}
	// domofon(+2) then lusy(+1) reaches the threshold: achieved + pleased.
	if res, err := ev.Judge(ctx, ch, []string{"domofon"}, "lusy"); err != nil || !res.Achieved || res.Emotion != "pleased" {
		t.Fatalf("domofon+lusy: res=%+v err=%v; want achieved + pleased", res, err)
	}
	// A rude option pushes conviction negative: annoyed, not achieved.
	if res, err := ev.Judge(ctx, ch, nil, "diver"); err != nil || res.Achieved || res.Emotion != "annoyed" {
		t.Fatalf("diver: res=%+v err=%v; want annoyed, not achieved", res, err)
	}
	if _, err := ev.Judge(ctx, ch, nil, "nope"); !errors.Is(err, ErrOptionNotFound) {
		t.Fatalf("unknown option err = %v; want ErrOptionNotFound", err)
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
			svc := newSvc(repo)
			_, err := svc.SubmitRun(context.Background(), "acc-1", tt.gameKey, tt.charKey, true, tt.steps)
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
		if _, err := newSvc(repo).Leaderboard(context.Background(), GameSmalltalkKhimki, tt.in); err != nil {
			t.Fatalf("Leaderboard(%d): %v", tt.in, err)
		}
		if repo.gotLimit != tt.want {
			t.Fatalf("limit in=%d -> repo got %d; want %d", tt.in, repo.gotLimit, tt.want)
		}
	}
	if _, err := newSvc(&fakeRepo{}).Leaderboard(context.Background(), "nope", 5); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
}
