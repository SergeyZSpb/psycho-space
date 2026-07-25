package gamekhimki

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// fakeRepo records calls and returns canned data; it lets us unit-test the
// service's validation/clamping without a database.
type fakeRepo struct {
	recordCalls  int
	recordsCalls int
	records      []PlayerRecords
}

func (f *fakeRepo) RecordRun(_ context.Context, _ db.DBTX, accountID, gameKey, characterKey string, success bool, steps int) (Run, error) {
	f.recordCalls++
	return Run{ID: "run-1", AccountID: accountID, GameKey: gameKey, CharacterKey: characterKey, Success: success, Steps: steps}, nil
}

func (f *fakeRepo) Records(_ context.Context, _ db.DBTX, _ string) ([]PlayerRecords, error) {
	f.recordsCalls++
	return f.records, nil
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
	gotAnger      int
	gotThemes     []string
	calls         int
}

func (s *stubEval) Judge(_ context.Context, _ Character, transcript []Exchange, choice string, anger int, themesDone []string) (TurnResult, error) {
	s.calls++
	s.gotTranscript = transcript
	s.gotChoice = choice
	s.gotAnger = anger
	s.gotThemes = themesDone
	return s.res, s.err
}

func newSvc(repo Repository, eval Evaluator) *Service { return NewService(nil, repo, eval, nil) }

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
	if ch.Goal == "" || ch.Greeting == "" || len(ch.Arts) == 0 {
		t.Fatalf("character %q underspecified: %+v", ch.Key, ch)
	}
	// Static opening: greeting + first options, so the game starts without an LLM call.
	if len(ch.OpeningOptions) == 0 {
		t.Fatalf("character %q has no static opening options", ch.Key)
	}
	// Judge prompt material must be present; the internal win/lose conditions
	// are server-only (never the public Goal).
	if ch.Objective == "" || ch.Failure == "" || ch.Persona == "" || ch.Motivation == "" || ch.TalkStyle == "" {
		t.Fatalf("character %q missing judge prompt fields (Objective/Failure/Persona/Motivation/TalkStyle): %+v", ch.Key, ch)
	}
	// The game-over art must exist in the catalog, or a lost run shows nothing.
	if ch.GameOverArt == "" {
		t.Fatalf("character %q has no GameOverArt", ch.Key)
	}
	if !slices.Contains(ch.artKeys(), ch.GameOverArt) {
		t.Fatalf("GameOverArt %q is not in the art catalog %v", ch.GameOverArt, ch.artKeys())
	}
	if _, err := ContentFor("nope"); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
}

// Дядя Ваня answers in rap, which means his lines are multi-line verse: the talk
// style has to say so and the static greeting has to already be in that register,
// or the very first thing the player sees contradicts everything after it.
func TestCharacterRaps(t *testing.T) {
	ch := defaultChar(t)
	low := strings.ToLower(ch.TalkStyle)
	if !strings.Contains(low, "рэп") {
		t.Errorf("talk style should call for rap; got %q", ch.TalkStyle)
	}
	if !strings.Contains(low, "рифм") {
		t.Errorf("talk style should call for rhyme; got %q", ch.TalkStyle)
	}
	// Rhyme must never win over substance — that was the risk of the change.
	if !strings.Contains(low, "смысл важнее") {
		t.Error("talk style should subordinate rhyme to meaning")
	}
	if n := strings.Count(ch.Greeting, "\n"); n < 2 {
		t.Errorf("greeting should be verse (>=3 lines); got %d newlines in %q", n, ch.Greeting)
	}
}

// The character's third deep theme is alcohol, not drugs: drug-flavoured prompt
// material makes the provider's content filter answer in plain prose instead of
// the model's JSON, which the player sees as a 502. Guard the whole text
// surface we send to the model, not just the objective.
func TestContentAvoidsDrugFlavouredPrompts(t *testing.T) {
	g, err := ContentFor(GameSmalltalkKhimki)
	if err != nil {
		t.Fatalf("ContentFor: %v", err)
	}
	ch := defaultChar(t)
	text := strings.ToLower(strings.Join(append([]string{
		g.Intro, ch.Goal, ch.Greeting, ch.Objective, ch.Motivation, ch.Persona, ch.TalkStyle,
	}, ch.OpeningOptions...), " "))

	for _, banned := range []string{"ширн", "торчк", "нарик", "наркот", "вещест", "доза", "герыч"} {
		if strings.Contains(text, banned) {
			t.Errorf("prompt material contains drug-flavoured %q; the story is about alcohol", banned)
		}
	}
	if !strings.Contains(text, "алког") && !strings.Contains(text, "выпи") {
		t.Error("prompt material should carry the alcohol theme")
	}
}

func TestServiceJudgeRouting(t *testing.T) {
	charKey := defaultChar(t).Key

	// Unknown game / character short-circuit before the evaluator.
	if _, err := newSvc(&fakeRepo{}, &stubEval{}).Judge(context.Background(), "nope", charKey, nil, "", StartAnger, nil); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
	if _, err := newSvc(&fakeRepo{}, &stubEval{}).Judge(context.Background(), GameSmalltalkKhimki, "nobody", nil, "", StartAnger, nil); !errors.Is(err, ErrUnknownCharacter) {
		t.Fatalf("unknown character err = %v; want ErrUnknownCharacter", err)
	}

	// A valid call is delegated with the transcript, the choice and the tension.
	ev := &stubEval{res: TurnResult{Reply: "ок", Art: "vanya_neutral", Achieved: true}}
	tr := []Exchange{{Choice: "привет", Reply: "ну"}}
	res, err := newSvc(&fakeRepo{}, ev).Judge(
		context.Background(), GameSmalltalkKhimki, charKey, tr, "домой", 75, []string{"sahur"})
	if err != nil || !res.Achieved || ev.calls != 1 || ev.gotChoice != "домой" || len(ev.gotTranscript) != 1 {
		t.Fatalf("delegate: res=%+v err=%v ev=%+v", res, err, ev)
	}
	if ev.gotAnger != 75 {
		t.Fatalf("evaluator got anger %d; want the caller's 75", ev.gotAnger)
	}
	if !reflect.DeepEqual(ev.gotThemes, []string{"sahur"}) {
		t.Fatalf("evaluator got themes %v; want the caller's [sahur]", ev.gotThemes)
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

func iptr(n int) *int { return &n }

// The four boards rank a player's best/worst SINGLE run, and a player only
// appears on boards they hold a record on.
func TestBuildBoards(t *testing.T) {
	records := []PlayerRecords{
		{AccountID: "a", Plays: 5, Wins: 2, Losses: 3,
			LongestWin: iptr(14), ShortestWin: iptr(3), LongestLoss: iptr(21), ShortestLoss: iptr(2)},
		{AccountID: "b", Plays: 1, Wins: 1, // never lost
			LongestWin: iptr(9), ShortestWin: iptr(9)},
		{AccountID: "c", Plays: 2, Losses: 2, // never won
			LongestLoss: iptr(30), ShortestLoss: iptr(1)},
		{AccountID: "d", Plays: 1, Wins: 1, // ties with b
			LongestWin: iptr(9), ShortestWin: iptr(9)},
	}
	boards := buildBoards(records, 10)

	// Every row carries the record steps AND the player's overall tally.
	a := func(steps int) RecordEntry { return RecordEntry{"a", steps, 5, 2, 3} }
	b := RecordEntry{"b", 9, 1, 1, 0}
	d := RecordEntry{"d", 9, 1, 1, 0}
	c := func(steps int) RecordEntry { return RecordEntry{"c", steps, 2, 0, 2} }
	want := map[RecordBoard][]RecordEntry{
		// Longest boards descend; b and d tie on 9 and break on account id.
		BoardLongestWin:   {a(14), b, d},
		BoardShortestWin:  {a(3), b, d},
		BoardLongestLoss:  {c(30), a(21)},
		BoardShortestLoss: {c(1), a(2)},
	}
	for board, wantEntries := range want {
		got := boards[board]
		if !reflect.DeepEqual(got, wantEntries) {
			t.Errorf("board %s = %+v; want %+v", board, got, wantEntries)
		}
	}
	if len(boards) != len(RecordBoards) {
		t.Fatalf("got %d boards; want %d", len(boards), len(RecordBoards))
	}
}

// A player with no runs at all holds no records and appears nowhere.
func TestBuildBoardsSkipsPlayersWithoutRecords(t *testing.T) {
	boards := buildBoards([]PlayerRecords{{AccountID: "ghost"}}, 10)
	for _, board := range RecordBoards {
		if len(boards[board]) != 0 {
			t.Errorf("board %s should be empty, got %+v", board, boards[board])
		}
	}
}

func TestLeaderboardLimitClamped(t *testing.T) {
	// More record holders than any cap, so the limit is what bounds each board.
	var records []PlayerRecords
	for i := 0; i < maxLeaderboardLimit+60; i++ {
		steps := i + 1
		records = append(records, PlayerRecords{
			AccountID:   fmt.Sprintf("acc-%04d", i),
			LongestWin:  &steps,
			ShortestWin: &steps,
			LongestLoss: &steps, ShortestLoss: &steps,
		})
	}
	tests := []struct{ in, want int }{
		{0, defaultLeaderboardLimit},
		{-5, defaultLeaderboardLimit},
		{10, 10},
		{maxLeaderboardLimit + 50, maxLeaderboardLimit},
	}
	for _, tt := range tests {
		repo := &fakeRepo{records: records}
		boards, err := newSvc(repo, &stubEval{}).Leaderboard(context.Background(), GameSmalltalkKhimki, tt.in)
		if err != nil {
			t.Fatalf("Leaderboard(%d): %v", tt.in, err)
		}
		if repo.recordsCalls != 1 {
			t.Fatalf("limit in=%d: repo called %d times; want 1 query for all boards", tt.in, repo.recordsCalls)
		}
		for _, board := range RecordBoards {
			if len(boards[board]) != tt.want {
				t.Fatalf("limit in=%d board %s len %d; want %d", tt.in, board, len(boards[board]), tt.want)
			}
		}
	}
	if _, err := newSvc(&fakeRepo{}, &stubEval{}).Leaderboard(context.Background(), "nope", 5); !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("unknown game err = %v; want ErrUnknownGame", err)
	}
}
