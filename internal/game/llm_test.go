package game

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SergeyZSpb/psycho-space/internal/config"
)

func testChar() Character {
	return Character{
		Key:        "c",
		Name:       "Дядя Ваня",
		Goal:       "пройти",
		Arts:       []Art{{Key: "vanya_angry"}, {Key: "vanya_neutral"}, {Key: "hallway_pass"}},
		Motivation: "m",
		Persona:    "p",
		TalkStyle:  "t",
	}
}

// llmServer stands in for an OpenAI-compatible endpoint. It records the request
// and returns `content` as the assistant message (status controls the HTTP code).
func llmServer(t *testing.T, content string, status int, gotAuth *string, gotReq *chatRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		if gotReq != nil {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, gotReq)
		}
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"boom"}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":`+strconv.Quote(content)+`}}]}`)
	}))
}

func TestOpenAIEvaluatorJudge(t *testing.T) {
	var auth string
	var req chatRequest
	content := `{"reply":"Ну проходи","art":"hallway_pass","achieved":true,"options":["ещё что-то"]}`
	srv := llmServer(t, content, http.StatusOK, &auth, &req)
	defer srv.Close()

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "deepseek-4-pro"})
	res, err := ev.Judge(context.Background(), testChar(), []Exchange{{Choice: "привет", Reply: "ну"}}, "домой", StartAnger)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if res.Reply != "Ну проходи" || res.Art != "hallway_pass" || !res.Achieved {
		t.Fatalf("res = %+v", res)
	}
	// Achieved ends the dialogue: options cleared regardless of what the model said.
	if len(res.Options) != 0 {
		t.Fatalf("achieved should clear options, got %v", res.Options)
	}
	if auth != "Bearer k" {
		t.Fatalf("auth = %q; want Bearer k", auth)
	}
	if req.Model != "deepseek-4-pro" {
		t.Fatalf("model = %q", req.Model)
	}
	// system + (user,assistant for the one prior exchange) + current user.
	if len(req.Messages) != 4 || req.Messages[0].Role != "system" {
		t.Fatalf("messages = %+v", req.Messages)
	}
	if req.Messages[3].Content != "домой" {
		t.Fatalf("last message = %q; want the current choice", req.Messages[3].Content)
	}
	if !strings.Contains(req.Messages[0].Content, "Дядя Ваня") {
		t.Fatal("system prompt should carry the persona (character name)")
	}
}

func TestOpenAIEvaluatorArtClampAndOptions(t *testing.T) {
	content := `{"reply":"Хм","art":"восторг","achieved":false,"options":["a","b"]}`
	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()
	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), testChar(), nil, "", StartAnger)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if res.Art != "vanya_angry" { // off-list -> clamped to the first allowed art
		t.Fatalf("art = %q; want vanya_angry (clamped to first)", res.Art)
	}
	if len(res.Options) != 2 {
		t.Fatalf("options = %v; want 2 (not achieved)", res.Options)
	}
}

func TestOpenAIEvaluatorErrors(t *testing.T) {
	// Non-200.
	bad := llmServer(t, "", http.StatusInternalServerError, nil, nil)
	defer bad.Close()
	ev := NewOpenAIEvaluator(config.LLM{BaseURL: bad.URL, APIKey: "k", Model: "m"})
	if _, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger); err == nil {
		t.Fatal("want error on http 500")
	}
	// 200 but content isn't valid JSON.
	junk := llmServer(t, "not json", http.StatusOK, nil, nil)
	defer junk.Close()
	ev2 := NewOpenAIEvaluator(config.LLM{BaseURL: junk.URL, APIKey: "k", Model: "m"})
	if _, err := ev2.Judge(context.Background(), testChar(), nil, "x", StartAnger); err == nil {
		t.Fatal("want error on non-JSON content")
	}
}

// A content-filter refusal arrives as HTTP 200 with plain prose instead of the
// JSON we asked for. It must come back as ErrLLMUnparsable (so the handler can
// answer 4xx rather than 502) and carry that prose, because the error is the
// only record of the root cause at the Info level prod runs at.
func TestOpenAIEvaluatorNonJSONIsUnparsable(t *testing.T) {
	const refusal = "Не люблю менять тему разговора, но эта тема кажется мне спорной."
	srv := llmServer(t, refusal, http.StatusOK, nil, nil)
	defer srv.Close()

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	_, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger)
	if !errors.Is(err, ErrLLMUnparsable) {
		t.Fatalf("err = %v; want ErrLLMUnparsable", err)
	}
	if !strings.Contains(err.Error(), refusal) {
		t.Fatalf("error should quote the offending content; got %q", err)
	}
}

// A transport/HTTP failure is a different animal: it stays a plain error so the
// handler still answers 502.
func TestOpenAIEvaluatorHTTPErrorIsNotUnparsable(t *testing.T) {
	srv := llmServer(t, "", http.StatusInternalServerError, nil, nil)
	defer srv.Close()

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	_, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger)
	if err == nil || errors.Is(err, ErrLLMUnparsable) {
		t.Fatalf("err = %v; want a non-ErrLLMUnparsable error", err)
	}
}

func TestSnippetTruncatesOnRunes(t *testing.T) {
	long := strings.Repeat("я", snippetRunes+100)
	got := snippet([]byte(long))
	if !utf8.ValidString(got) {
		t.Fatalf("snippet produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != snippetRunes+1 { // + the ellipsis
		t.Fatalf("rune count = %d; want %d", n, snippetRunes+1)
	}
	if short := snippet([]byte("  привет  ")); short != "привет" {
		t.Fatalf("short input should pass through trimmed, got %q", short)
	}
	if got := clampRunes("привет", 100); got != "привет" {
		t.Fatalf("clampRunes should pass short strings through, got %q", got)
	}
}

// Snapping ends the run: options are cleared and the art is forced to the
// character's game-over art, whatever the model asked to show.
func TestOpenAIEvaluatorGameOver(t *testing.T) {
	content := `{"reply":"Да пошёл ты!","art":"vanya_neutral","achieved":false,"game_over":true,"options":["ещё","и ещё"]}`
	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()

	ch := testChar()
	ch.Arts = append(ch.Arts, Art{Key: "vanya_game_over_hits_us"})
	ch.GameOverArt = "vanya_game_over_hits_us"

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), ch, nil, "ты никчёмный", StartAnger)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !res.GameOver || res.Achieved {
		t.Fatalf("res = %+v; want game_over without achieved", res)
	}
	if res.Art != "vanya_game_over_hits_us" {
		t.Fatalf("art = %q; want the forced game-over art", res.Art)
	}
	if len(res.Options) != 0 {
		t.Fatalf("game over should clear options, got %v", res.Options)
	}
}

// Winning and snapping on the same turn is a contradiction — the win wins.
func TestOpenAIEvaluatorAchievedBeatsGameOver(t *testing.T) {
	content := `{"reply":"Заходи","art":"hallway_pass","achieved":true,"game_over":true,"options":[]}`
	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()

	ch := testChar()
	ch.GameOverArt = "vanya_angry"

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), ch, nil, "x", StartAnger)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !res.Achieved || res.GameOver {
		t.Fatalf("res = %+v; want achieved and not game_over", res)
	}
	if res.Art != "hallway_pass" {
		t.Fatalf("art = %q; the win must keep its own art", res.Art)
	}
}

// The tension scale is the balance mechanism: the judge moves it, and a full
// scale ends the run even when the model itself never sets game_over.
func TestOpenAIEvaluatorAnger(t *testing.T) {
	ch := testChar()
	ch.Arts = append(ch.Arts, Art{Key: "vanya_game_over_hits_us"})
	ch.GameOverArt = "vanya_game_over_hits_us"

	tests := []struct {
		name         string
		content      string
		angerIn      int
		wantAnger    int
		wantGameOver bool
	}{
		{
			name:      "model raises it",
			content:   `{"reply":"Ты чё сказал?","art":"vanya_angry","anger":65,"achieved":false,"options":["a","b","c","d"]}`,
			angerIn:   StartAnger,
			wantAnger: 65,
		},
		{
			// A model that forgets the field must not stall the scale — that would
			// bring back the unloseable run the scale exists to fix. It drifts up.
			name:      "omitted drifts up instead of stalling",
			content:   `{"reply":"Ну?","art":"vanya_angry","achieved":false,"options":["a","b","c","d"]}`,
			angerIn:   70,
			wantAnger: 70 + angerDriftOnMissing,
		},
		{
			// The drift is clamped too, and still ends the run at the top.
			name:         "omitted at the top still ends the run",
			content:      `{"reply":"Ну?","art":"vanya_angry","achieved":false,"options":["a"]}`,
			angerIn:      MaxAnger - 1,
			wantAnger:    MaxAnger,
			wantGameOver: true,
		},
		{
			name:      "clamped to the scale",
			content:   `{"reply":"Ы","art":"vanya_angry","anger":-40,"achieved":false,"options":["a"]}`,
			angerIn:   50,
			wantAnger: 0,
		},
		{
			// The whole point: max tension loses the run with no game_over flag.
			name:         "full scale ends the run",
			content:      `{"reply":"Всё, доигрался","art":"vanya_neutral","anger":100,"achieved":false,"options":["a","b"]}`,
			angerIn:      90,
			wantAnger:    MaxAnger,
			wantGameOver: true,
		},
		{
			name:         "over the top is clamped and still ends the run",
			content:      `{"reply":"Всё","art":"vanya_neutral","anger":250,"achieved":false,"options":[]}`,
			angerIn:      90,
			wantAnger:    MaxAnger,
			wantGameOver: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := llmServer(t, tt.content, http.StatusOK, nil, nil)
			defer srv.Close()
			ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
			res, err := ev.Judge(context.Background(), ch, nil, "x", tt.angerIn)
			if err != nil {
				t.Fatalf("Judge: %v", err)
			}
			if res.Anger != tt.wantAnger {
				t.Errorf("anger = %d; want %d", res.Anger, tt.wantAnger)
			}
			if res.GameOver != tt.wantGameOver {
				t.Errorf("game_over = %v; want %v", res.GameOver, tt.wantGameOver)
			}
			if tt.wantGameOver {
				if res.Art != ch.GameOverArt {
					t.Errorf("art = %q; want the forced %q", res.Art, ch.GameOverArt)
				}
				if len(res.Options) != 0 {
					t.Errorf("a lost run should clear options, got %v", res.Options)
				}
			}
		})
	}
}

// Winning on the turn the scale fills is still a win.
func TestOpenAIEvaluatorAchievedBeatsFullAnger(t *testing.T) {
	content := `{"reply":"Ладно, заходи","art":"hallway_pass","anger":100,"achieved":true,"options":[]}`
	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), testChar(), nil, "x", 95)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !res.Achieved || res.GameOver {
		t.Fatalf("res = %+v; want achieved and not game_over", res)
	}
}

// The judge can only move the scale if it is told where the scale stands.
func TestBuildMessagesCarriesAnger(t *testing.T) {
	sys := buildMessages(testChar(), nil, "x", 73)[0].Content
	if !strings.Contains(sys, "сейчас 73") {
		t.Fatalf("system prompt should state the current tension; got:\n%s", sys)
	}
	if !strings.Contains(sys, `"anger"`) {
		t.Fatal("system prompt should ask for the anger field back")
	}
	if !strings.Contains(sys, "НАКАПЛИВАЮТСЯ") {
		t.Fatal("system prompt should tell the model the conversation accumulates")
	}
}

// An out-of-range value from a tampering client is clamped before it reaches the
// prompt, so it can neither disable the scale nor pre-lose the run.
func TestJudgeClampsIncomingAnger(t *testing.T) {
	var req chatRequest
	content := `{"reply":"Ну?","art":"vanya_angry","achieved":false,"options":["a"]}`
	srv := llmServer(t, content, http.StatusOK, nil, &req)
	defer srv.Close()

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), testChar(), nil, "x", -999)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	// Clamped to 0 on the way in; this reply omits "anger", so it drifts from 0.
	if res.Anger != angerDriftOnMissing {
		t.Fatalf("anger = %d; want %d (clamped to 0 in, then drift)", res.Anger, angerDriftOnMissing)
	}
	if !strings.Contains(req.Messages[0].Content, "сейчас 0") {
		t.Fatal("prompt should carry the clamped tension")
	}
}

func TestClampAnger(t *testing.T) {
	for _, tt := range []struct{ in, want int }{
		{-1, 0}, {0, 0}, {50, 50}, {MaxAnger, MaxAnger}, {MaxAnger + 1, MaxAnger},
	} {
		if got := ClampAnger(tt.in); got != tt.want {
			t.Errorf("ClampAnger(%d) = %d; want %d", tt.in, got, tt.want)
		}
	}
}

func TestOptionsWhilePlaying(t *testing.T) {
	if optionsWhilePlaying(true, []string{"a", "b"}) != nil {
		t.Fatal("achieved should return no options")
	}
	if got := optionsWhilePlaying(false, []string{"1", "2", "3", "4", "5"}); len(got) != optionCount {
		t.Fatalf("more than 4 should cap to %d, got %d", optionCount, len(got))
	}
	if got := optionsWhilePlaying(false, []string{"1", "2", "3"}); len(got) != 3 {
		t.Fatalf("3 options should pass through, got %d", len(got))
	}
}

func TestWindowTranscript(t *testing.T) {
	tr := []Exchange{{"a1", "b1"}, {"a2", "b2"}, {"a3", "b3"}}
	if got := windowTranscript(tr, 0); len(got) != 0 {
		t.Fatalf("zero budget should drop all, got %d", len(got))
	}
	if got := windowTranscript(tr, 1_000_000); len(got) != 3 {
		t.Fatalf("big budget should keep all, got %d", len(got))
	}
	// Room for only the last exchange.
	last := estTokens("a3") + estTokens("b3")
	if got := windowTranscript(tr, last); len(got) != 1 || got[0].Choice != "a3" {
		t.Fatalf("small budget should keep only the newest, got %+v", got)
	}
}

func TestBuildMessagesDropsOldHistory(t *testing.T) {
	big := strings.Repeat("я", 6000) // ~3000 est tokens per field
	var tr []Exchange
	for i := 0; i < 100; i++ {
		tr = append(tr, Exchange{Choice: big, Reply: big})
	}
	msgs := buildMessages(testChar(), tr, "финал", StartAnger)
	included := (len(msgs) - 2) / 2 // minus system + current user
	if included >= 100 {
		t.Fatalf("old history not dropped: included=%d of 100", included)
	}
	if included == 0 {
		t.Fatal("everything dropped; expected some recent history to fit")
	}
}
