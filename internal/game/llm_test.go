package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
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
	res, err := ev.Judge(context.Background(), testChar(), []Exchange{{Choice: "привет", Reply: "ну"}}, "домой", StartAnger, nil)
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
	// The last message carries the player's line, the volatile state and the
	// restated JSON contract — a roleplay-strong model otherwise answers in
	// character and ignores the format.
	last := req.Messages[len(req.Messages)-1].Content
	if !strings.Contains(last, "Реплика игрока: домой") {
		t.Fatalf("last message = %q; want it to carry the current choice", last)
	}
	if !strings.Contains(last, "ТОЛЬКО одним JSON") {
		t.Fatal("last message should restate the JSON-only contract")
	}
	// Nothing volatile may sit in the system message: it is the cached prefix.
	if strings.Contains(req.Messages[0].Content, "Текущее напряжение") {
		t.Fatal("the tension value must not be in the system prompt (it would break the prefix cache)")
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
	res, err := ev.Judge(context.Background(), testChar(), nil, "", StartAnger, nil)
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
	if _, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger, nil); err == nil {
		t.Fatal("want error on http 500")
	}
	// 200 but content isn't valid JSON.
	junk := llmServer(t, "not json", http.StatusOK, nil, nil)
	defer junk.Close()
	ev2 := NewOpenAIEvaluator(config.LLM{BaseURL: junk.URL, APIKey: "k", Model: "m"})
	if _, err := ev2.Judge(context.Background(), testChar(), nil, "x", StartAnger, nil); err == nil {
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
	_, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger, nil)
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
	_, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger, nil)
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
	res, err := ev.Judge(context.Background(), ch, nil, "ты никчёмный", StartAnger, nil)
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
	res, err := ev.Judge(context.Background(), ch, nil, "x", StartAnger, nil)
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
			// The whole point: crossing the kill line loses the run with no
			// game_over flag from the model.
			name:         "reaching the kill line ends the run",
			content:      `{"reply":"Всё, доигрался","art":"vanya_neutral","anger":90,"achieved":false,"options":["a","b"]}`,
			angerIn:      80,
			wantAnger:    AngerLoseAt,
			wantGameOver: true,
		},
		{
			// Measured against the real model the scale crawled to 95 and sat
			// there; below the line it must NOT end the run.
			name:      "just under the line keeps the run alive",
			content:   `{"reply":"Ну ты и наглый","art":"vanya_angry","anger":89,"achieved":false,"options":["a","b","c","d"]}`,
			angerIn:   80,
			wantAnger: 89,
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
			res, err := ev.Judge(context.Background(), ch, nil, "x", tt.angerIn, nil)
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

// Winning on the turn the scale crosses the kill line is still a win.
func TestOpenAIEvaluatorAchievedBeatsFullAnger(t *testing.T) {
	content := `{"reply":"Ладно, заходи","art":"hallway_pass","anger":100,"achieved":true,"options":[]}`
	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), testChar(), nil, "x", 95, nil)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !res.Achieved || res.GameOver {
		t.Fatalf("res = %+v; want achieved and not game_over", res)
	}
}

// The judge can only move the scale if it is told where the scale stands — and
// that value belongs in the volatile tail, never in the cached system prefix.
func TestBuildMessagesCarriesAnger(t *testing.T) {
	msgs, _ := buildMessages(testChar(), nil, "x", 73, nil, nil)
	sys, tail := msgs[0].Content, msgs[len(msgs)-1].Content
	if !strings.Contains(tail, "Текущее напряжение: 73") {
		t.Fatalf("tail should state the current tension; got:\n%s", tail)
	}
	if strings.Contains(sys, "73") {
		t.Fatal("the tension value must not appear in the cached system prompt")
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
	res, err := ev.Judge(context.Background(), testChar(), nil, "x", -999, nil)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	// Clamped to 0 on the way in; this reply omits "anger", so it drifts from 0.
	if res.Anger != angerDriftOnMissing {
		t.Fatalf("anger = %d; want %d (clamped to 0 in, then drift)", res.Anger, angerDriftOnMissing)
	}
	if !strings.Contains(req.Messages[len(req.Messages)-1].Content, "Текущее напряжение: 0") {
		t.Fatal("prompt tail should carry the clamped tension")
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

// The real payload that cost a player a turn in prod (trace
// 3da1f8809b2112b24f01348c384d3c9f): YandexGPT closed the options array after one
// item, parked the other three under a "[" key, and left a raw newline inside the
// last key — "invalid character '\n' in string literal". Everything the turn
// needs is in there, so it must be recovered rather than 422'd.
func TestSalvagesGarbledJSONFromProd(t *testing.T) {
	const content = `{"reply": "Не нужна мне твоя помощь, отвали!", "art": "vanya_angry", ` +
		`"anger": 70, "achieved": false, "game_over": false, ` +
		`"options": ["Может, всё-таки присядем?"], ` +
		`"[": ["Поговорим о том, что тебя беспокоит?", "Может, о друге вспомним?", "А ты пить совсем не любишь?"], ` +
		"\"]}\n\": []}"

	// Precondition: the payload really is invalid JSON, so this test exercises
	// the recovery path rather than the happy one.
	var probe judgeReply
	if json.Unmarshal([]byte(content), &probe) == nil {
		t.Fatal("payload should be invalid JSON; the test has lost its point")
	}

	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()
	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), testChar(), nil, "Давай я тебе помогу", 60, nil)
	if err != nil {
		t.Fatalf("Judge should salvage this reply, got: %v", err)
	}
	if res.Reply != "Не нужна мне твоя помощь, отвали!" {
		t.Errorf("reply = %q", res.Reply)
	}
	if res.Anger != 70 {
		t.Errorf("anger = %d; want the 70 the model asked for", res.Anger)
	}
	if res.Achieved || res.GameOver {
		t.Errorf("res = %+v; want the run still live", res)
	}
	// The three options parked under the junk key are recovered, so the player
	// still gets a full set of choices.
	if len(res.Options) != optionCount {
		t.Fatalf("options = %v; want %d recovered", res.Options, optionCount)
	}
	if res.Options[0] != "Может, всё-таки присядем?" || res.Options[3] != "А ты пить совсем не любишь?" {
		t.Errorf("recovered options in unexpected order: %v", res.Options)
	}
}

func TestSalvageJudgeReply(t *testing.T) {
	tests := []struct {
		name    string
		content string
		ok      bool
		wantOpt int
	}{
		{
			name: "raw newline inside a string",
			// Real tab and newline characters inside the JSON strings.
			content: "{\"reply\": \"Ну\tтак\", \"art\": \"vanya_angry\", \"options\": [\"a\nb\"]}",
			ok:      true,
			wantOpt: 1,
		},
		{
			name:    "junk keys are dropped",
			content: `{"reply":"Ну","art":"vanya_angry","totally":"unexpected","options":["a","b","c","d"]}`,
			ok:      true,
			wantOpt: 4,
		},
		{
			// Nothing to show the player: not a usable turn.
			name:    "empty reply is not salvageable",
			content: `{"reply":"   ","art":"vanya_angry","options":["a"]}`,
			ok:      false,
		},
		{
			// A content-filter refusal was never JSON — it must still 422.
			name:    "prose is not salvageable",
			content: "Не люблю менять тему разговора, но эта тема кажется мне спорной.",
			ok:      false,
		},
		{
			name:    "truncated beyond repair",
			content: `{"reply":"Ну","art":"vanya_a`,
			ok:      false,
		},
		{
			// Stray arrays only fill up to the option count, never past it.
			name: "stray options are capped",
			content: `{"reply":"Ну","options":["a"],"x":["b","c","d","e","f"],` +
				"\"y\n\":[\"g\"]}",
			ok:      true,
			wantOpt: optionCount,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := salvageJudgeReply(tt.content)
			if ok != tt.ok {
				t.Fatalf("salvaged = %v; want %v (got %+v)", ok, tt.ok, got)
			}
			if ok && len(got.Options) != tt.wantOpt {
				t.Fatalf("options = %v; want %d", got.Options, tt.wantOpt)
			}
		})
	}
}

// The repair must not touch the JSON's own structure or already-escaped text.
func TestEscapeControlCharsInStrings(t *testing.T) {
	tests := []struct{ in, want string }{
		{`{"a":"b"}`, `{"a":"b"}`},
		{"{\"a\":\"line\nbreak\"}", `{"a":"line\nbreak"}`},
		{`{"a":"already\nescaped"}`, `{"a":"already\nescaped"}`},
		// A newline between tokens is structure, not string content: left alone.
		{"{\n\"a\":\"b\"\n}", "{\n\"a\":\"b\"\n}"},
		// An escaped quote must not be read as the end of the string.
		{`{"a":"quote\" inside"}`, `{"a":"quote\" inside"}`},
	}
	for _, tt := range tests {
		if got := escapeControlCharsInStrings(tt.in); got != tt.want {
			t.Errorf("escape(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

// themedChar has the three-theme structure the real character has.
func themedChar() Character {
	ch := testChar()
	ch.Arts = append(ch.Arts, Art{Key: "vanya_game_over_hits_us"})
	ch.GameOverArt = "vanya_game_over_hits_us"
	// Keywords included: engagement is measured from them, and several behaviours
	// (auto-opening, claim confirmation, slot targeting) key off it.
	ch.Themes = []Theme{
		{Key: "woman_children", Label: "тоска по женщине и детям",
			Keywords: []string{"баб", "жен", "дет"}},
		{Key: "sahur", Label: "дружба с Тунг Тунг Сахуром",
			Keywords: []string{"сахур", "друг"}},
		{Key: "alcohol", Label: "отношения с алкоголем",
			Keywords: []string{"пить", "выпи", "завяз", "бутыл"}},
	}
	return ch
}

// The judge is told which themes are still closed, so the first option slot has
// somewhere to steer. Measured without this, alcohol showed up in 1 of 40 offered
// options and the win was practically unreachable.
func TestBuildMessagesNamesOpenThemes(t *testing.T) {
	ch := themedChar()

	msgs, _ := buildMessages(ch, nil, "x", StartAnger, []string{"sahur"}, nil)
	tail := msgs[len(msgs)-1].Content
	if !strings.Contains(tail, "alcohol") || !strings.Contains(tail, "woman_children") {
		t.Fatalf("still-closed themes should be listed in the tail; got:\n%s", tail)
	}
	if strings.Contains(tail, "sahur — ") {
		t.Fatal("an already-opened theme must not be listed as still closed")
	}
	// The tail names WHICH open theme the first slot should aim at, chosen as the
	// least-engaged one so a subject already being discussed is not pushed again.
	if !strings.Contains(tail, "Тема для первого варианта:") {
		t.Fatalf("tail should name the theme for the first option; got:\n%s", tail)
	}

	// All three open: all three listed.
	allOpen, _ := buildMessages(ch, nil, "x", StartAnger, nil, nil)
	for _, key := range []string{"woman_children", "sahur", "alcohol"} {
		if !strings.Contains(allOpen[len(allOpen)-1].Content, key) {
			t.Errorf("theme %q missing from the open list", key)
		}
	}

	// Nothing left to open: the prompt switches to closing the dialogue out.
	done, _ := buildMessages(ch, nil, "x", StartAnger, []string{"woman_children", "sahur", "alcohol"}, nil)
	if !strings.Contains(done[len(done)-1].Content, "Все твои глубинные темы уже раскрыты") {
		t.Fatalf("with every theme open the prompt should push toward the ending")
	}
}

// The four options are asked for by role, because asking for "four varied
// options" produced four ways of saying "давай поговорим".
func TestBuildMessagesAsksForRoleSlots(t *testing.T) {
	msgs, _ := buildMessages(themedChar(), nil, "x", StartAnger, nil, nil)
	sys := msgs[0].Content
	for _, want := range []string{
		"тема для первого варианта", // slot 1: aims at a named open theme
		"тёплый",      // slot 2: current topic, warm
		"грубый",      // slot 3: a real way to lose
		"ДРУГУЮ тему", // slot 4: must leave the current subject
		"НЕ БОЛЬШЕ ДВУХ вариантов об одной теме",
		"минимум ТРИ разные темы",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("prompt missing role-slot instruction %q", want)
		}
	}
}

// The character's own past lines ARE the assistant messages, so they are never
// re-listed; the system prompt teaches the model to read the footers and not to
// repeat itself, and that instruction is static (therefore cacheable).
func TestSystemPromptExplainsJSONHistory(t *testing.T) {
	msgs, _ := buildMessages(themedChar(), nil, "x", StartAnger, nil, nil)
	sys := msgs[0].Content
	for _, want := range []string{"РОВНО в том формате", "прошлых \"options\"", "прежние зачины"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt should explain how to read the history; missing %q", want)
		}
	}
}

// A past turn is replayed as the JSON the judge returned for it, so the model
// reads correctly-formatted examples of its own output. Prod trace
// c771c3ed23c4fba6f2a0b439f3862a90: with prose-plus-footer history the model
// imitated the FOOTER instead of answering in JSON, and the turn was lost.
func TestJudgeReplayJSON(t *testing.T) {
	a := 55
	got := judgeReplayJSON(Exchange{
		Reply: "Ну чё", Art: "vanya_warming", Anger: &a,
		Options: []string{"раз", "  ", "два"}, ThemesDone: []string{"sahur"},
	})

	// It must parse back as exactly the shape we demand of the model.
	var jr judgeReply
	if err := json.Unmarshal([]byte(got), &jr); err != nil {
		t.Fatalf("replay is not valid judge JSON: %v\n%s", err, got)
	}
	if jr.Reply != "Ну чё" || jr.Art != "vanya_warming" {
		t.Errorf("replay lost the reply/art: %s", got)
	}
	if jr.Anger == nil || int(*jr.Anger) != 55 {
		t.Errorf("replay lost the tension: %s", got)
	}
	if !reflect.DeepEqual(jr.Options, []string{"раз", "два"}) {
		t.Errorf("replay options = %v; want blanks dropped", jr.Options)
	}
	// Every field the model must produce has to be present, or the example teaches
	// it to omit that field too.
	for _, field := range []string{`"reply"`, `"art"`, `"anger"`, `"achieved"`, `"game_over"`, `"themes_done"`, `"options"`} {
		if !strings.Contains(got, field) {
			t.Errorf("replay omits %s — an incomplete example teaches omission: %s", field, got)
		}
	}
	// A past turn by definition did not end the run.
	if jr.Achieved || jr.GameOver {
		t.Errorf("a replayed past turn must not look finished: %s", got)
	}
	// And it must not carry the old footer syntax the model learned to copy.
	if strings.Contains(got, "напряжение:") || strings.Contains(got, "предлагал:") {
		t.Errorf("replay must not use the footer format: %s", got)
	}
}

// A turn from an older client, with only the prose, still replays as valid JSON.
func TestJudgeReplayJSONMinimalExchange(t *testing.T) {
	got := judgeReplayJSON(Exchange{Reply: "Ну чё"})
	var jr judgeReply
	if err := json.Unmarshal([]byte(got), &jr); err != nil {
		t.Fatalf("replay is not valid judge JSON: %v\n%s", err, got)
	}
	if jr.Reply != "Ну чё" || len(jr.Options) != 0 {
		t.Errorf("unexpected replay: %s", got)
	}
}

// The history the model reads is JSON, and the system prompt says so.
func TestBuildMessagesReplaysHistoryAsJSON(t *testing.T) {
	a := 70
	tr := []Exchange{{Choice: "привет", Reply: "ну чё", Art: "vanya_angry", Anger: &a,
		Options: []string{"раз", "два"}}}
	msgs, _ := buildMessages(testChar(), tr, "дальше", StartAnger, nil, nil)

	var assistant string
	for _, m := range msgs {
		if m.Role == "assistant" && strings.Contains(m.Content, "ну чё") {
			assistant = m.Content
		}
	}
	var jr judgeReply
	if err := json.Unmarshal([]byte(assistant), &jr); err != nil {
		t.Fatalf("history turn should be replayed as JSON, got %q", assistant)
	}
	if !strings.Contains(msgs[0].Content, "РОВНО в том формате") {
		t.Error("system prompt should tell the model the history shows the required format")
	}
}

func TestRecentReplies(t *testing.T) {
	var tr []Exchange
	for i := 0; i < maxRecentReplies+5; i++ {
		tr = append(tr, Exchange{Reply: fmt.Sprintf("реплика-%d", i)})
	}
	got := recentReplies(tr)
	if len(got) != maxRecentReplies {
		t.Fatalf("len = %d; want the cap %d", len(got), maxRecentReplies)
	}
	if got[0] != fmt.Sprintf("реплика-%d", maxRecentReplies+4) {
		t.Fatalf("first = %q; want the newest reply", got[0])
	}
	// Blank replies are skipped, and a long one is clamped.
	got = recentReplies([]Exchange{{Reply: "  "}, {Reply: strings.Repeat("я", 300)}})
	if len(got) != 1 || utf8.RuneCountInString(got[0]) > 121 {
		t.Fatalf("got %d replies, first %d runes", len(got), utf8.RuneCountInString(got[0]))
	}
}

// Theme progress is monotonic, clamped to the character's own keys, and survives
// a model that forgets the field.
func TestThemeProgress(t *testing.T) {
	ch := themedChar()

	if got := clampThemes(ch, []string{"sahur", "не-тема", "SAHUR"}); !reflect.DeepEqual(got, []string{"sahur"}) {
		t.Fatalf("clampThemes = %v; want [sahur] (junk dropped, deduped)", got)
	}
	if got := clampThemes(ch, nil); got != nil {
		t.Fatalf("clampThemes(nil) = %v; want nil", got)
	}
	// Merge is a union in the character's own order, so it is stable.
	if got := mergeThemes(ch, []string{"alcohol"}, []string{"sahur", "alcohol"}); !reflect.DeepEqual(got, []string{"sahur", "alcohol"}) {
		t.Fatalf("mergeThemes = %v; want [sahur alcohol] in character order", got)
	}

	// A claim the conversation supports is merged in. The transcript below engages
	// alcohol twice, which meets themeConfirmTurns.
	content := `{"reply":"Ну","art":"vanya_angry","anger":50,"achieved":false,` +
		`"themes_done":["alcohol"],"options":["a","b","c","d"]}`
	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()
	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	drinking := []Exchange{
		{Choice: "А ты пробовал бросить пить?", Reply: "Тянет каждый вечер"},
		{Choice: "Тяжело завязать?", Reply: "Куда без бутылки"},
	}
	res, err := ev.Judge(context.Background(), ch, drinking, "x", 40, []string{"sahur"})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !reflect.DeepEqual(res.ThemesDone, []string{"sahur", "alcohol"}) {
		t.Fatalf("themes = %v; want the union [sahur alcohol]", res.ThemesDone)
	}

	// A claim with NOTHING behind it is rejected: the judge marked `sahur` open on
	// turn one in prod, before the friendship had been mentioned at all.
	res, err = ev.Judge(context.Background(), ch, nil, "x", 40, nil)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if len(res.ThemesDone) != 0 {
		t.Fatalf("themes = %v; an unsupported claim must be rejected", res.ThemesDone)
	}

	// A reply that OMITS the field keeps what the player already earned.
	bare := llmServer(t, `{"reply":"Ну","art":"vanya_angry","anger":50,"options":["a"]}`, http.StatusOK, nil, nil)
	defer bare.Close()
	ev2 := NewOpenAIEvaluator(config.LLM{BaseURL: bare.URL, APIKey: "k", Model: "m"})
	res2, err := ev2.Judge(context.Background(), ch, nil, "x", 40, []string{"sahur"})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !reflect.DeepEqual(res2.ThemesDone, []string{"sahur"}) {
		t.Fatalf("themes = %v; an omitted field must not wipe progress", res2.ThemesDone)
	}
}

// The real payload that cost a turn in prod (trace e8a9583de2e9993a7016f511aaa27f3a):
// flawless JSON except the tension arrived quoted, "anger": "35". A whole usable
// turn must not be thrown away over the quotes.
func TestQuotedNumbersAndBoolsAreAccepted(t *testing.T) {
	const content = `{"reply": "Ну что ж, каждому своё, правда?", "art": "vanya_warming", ` +
		`"anger": "35", "achieved": "false", "game_over": "false", "themes_done": ["sahur"], ` +
		`"options": ["а", "б", "в", "г"]}`

	srv := llmServer(t, content, http.StatusOK, nil, nil)
	defer srv.Close()
	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	res, err := ev.Judge(context.Background(), themedChar(), nil, "x", 40, nil)
	if err != nil {
		t.Fatalf("a quoted number should not cost the turn: %v", err)
	}
	if res.Anger != 35 {
		t.Errorf(`anger = %d; want 35 parsed from "35"`, res.Anger)
	}
	if res.Achieved || res.GameOver {
		t.Errorf(`res = %+v; want both flags false from "false"`, res)
	}
	if len(res.Options) != optionCount {
		t.Errorf("options = %v; want %d", res.Options, optionCount)
	}
}

func TestFlexIntAndFlexBool(t *testing.T) {
	var n flexInt
	for _, in := range []string{`42`, `"42"`, `42.0`, `"42.7"`} {
		n = 0
		if err := n.UnmarshalJSON([]byte(in)); err != nil {
			t.Errorf("flexInt(%s): %v", in, err)
		} else if n != 42 {
			t.Errorf("flexInt(%s) = %d; want 42", in, n)
		}
	}
	// Absent-ish values leave it at zero without erroring...
	for _, in := range []string{`null`, `""`} {
		n = 7
		if err := n.UnmarshalJSON([]byte(in)); err != nil {
			t.Errorf("flexInt(%s): %v", in, err)
		}
	}
	// ...but real junk is still an error, so the salvage path can see it.
	if err := n.UnmarshalJSON([]byte(`"очень злой"`)); err == nil {
		t.Error("flexInt should reject non-numeric text")
	}

	var b flexBool
	for in, want := range map[string]bool{`true`: true, `"true"`: true, `"TRUE"`: true,
		`false`: false, `"false"`: false, `null`: false} {
		b = false
		if err := b.UnmarshalJSON([]byte(in)); err != nil {
			t.Errorf("flexBool(%s): %v", in, err)
		} else if bool(b) != want {
			t.Errorf("flexBool(%s) = %v; want %v", in, bool(b), want)
		}
	}
	if err := b.UnmarshalJSON([]byte(`"ага"`)); err == nil {
		t.Error("flexBool should reject non-boolean text")
	}
}

// --- the theme-steering loop, measured in prod and fixed ---------------------
//
// Prod session, 29 turns: the moment two of three themes were marked, the first
// option slot was forced at the only remaining one every turn, the whole
// conversation became that subject, the judge never marked it, and 15 of 20 option
// sets had all four choices on the same topic. These guard each link in that chain.

func TestThemeEngagementCountsWholeRun(t *testing.T) {
	ch := themedChar()
	tr := []Exchange{
		{Choice: "А ты пробовал бросить пить?", Reply: "Тянет"},
		{Choice: "Расскажи про друга", Reply: "Сахур мне как брат"},
		{Choice: "Погода сегодня", Reply: "Ну да"},
	}
	eng := themeEngagement(ch, tr, "а дети у тебя есть?")
	if eng["alcohol"] != 1 {
		t.Errorf("alcohol engagement = %d; want 1", eng["alcohol"])
	}
	if eng["sahur"] != 1 {
		t.Errorf("sahur engagement = %d; want 1", eng["sahur"])
	}
	// The current line counts too — steering must react to it on this very turn.
	if eng["woman_children"] != 1 {
		t.Errorf("woman_children engagement = %d; want 1 from the current choice", eng["woman_children"])
	}
}

// A theme discussed to death is opened by the server even if the judge never says
// so. This is the fix for the unwinnable run.
func TestAutoMarkThemesBreaksTheLoop(t *testing.T) {
	ch := themedChar()
	var tr []Exchange
	for i := 0; i < themeAutoDoneTurns; i++ {
		tr = append(tr, Exchange{Choice: "может, пора завязать?", Reply: "не могу без бутылки"})
	}
	eng := themeEngagement(ch, tr, "")
	got := autoMarkThemes(ch, nil, eng)
	if !slices.Contains(got, "alcohol") {
		t.Fatalf("themes = %v; alcohol should be opened after %d engaged turns", got, themeAutoDoneTurns)
	}
	// One turn short of the threshold, it is left alone.
	if got := autoMarkThemes(ch, nil, map[string]int{"alcohol": themeAutoDoneTurns - 1}); len(got) != 0 {
		t.Fatalf("themes = %v; want none below the threshold", got)
	}
}

func TestConfirmThemesRejectsUnsupportedClaims(t *testing.T) {
	ch := themedChar()
	eng := map[string]int{"alcohol": themeConfirmTurns, "sahur": 0}

	// Supported claim accepted, unsupported one dropped.
	got := confirmThemes(ch, nil, []string{"alcohol", "sahur"}, eng)
	if !reflect.DeepEqual(got, []string{"alcohol"}) {
		t.Fatalf("got %v; want only the supported [alcohol]", got)
	}
	// Already-open themes are never taken away — progress is monotonic.
	got = confirmThemes(ch, []string{"sahur"}, []string{"sahur"}, eng)
	if !slices.Contains(got, "sahur") {
		t.Fatalf("got %v; an already-open theme must stay open", got)
	}
}

// The first slot aims at the LEAST discussed open theme, and stops aiming at all
// once every remaining theme is already in play.
func TestSteerThemePicksLeastEngaged(t *testing.T) {
	ch := themedChar()

	got, ok := steerTheme(ch, nil, map[string]int{"woman_children": 3, "sahur": 1, "alcohol": 0})
	if !ok || got.Key != "alcohol" {
		t.Fatalf("steer = %+v ok=%v; want the least-engaged alcohol", got, ok)
	}
	// Only one theme left and it is already the subject: stop pushing it. This is
	// exactly the state that produced 15/20 single-topic option sets in prod.
	if _, ok := steerTheme(ch, []string{"woman_children", "sahur"}, map[string]int{"alcohol": 9}); ok {
		t.Fatal("a saturated last theme must NOT be steered at again")
	}
	// Nothing open at all.
	if _, ok := steerTheme(ch, []string{"woman_children", "sahur", "alcohol"}, nil); ok {
		t.Fatal("no open themes should mean no steer target")
	}
}

func TestCurrentTopic(t *testing.T) {
	ch := themedChar()
	if got, ok := currentTopic(ch, nil, "а ты пить пробовал бросить?"); !ok || got.Key != "alcohol" {
		t.Fatalf("topic = %+v ok=%v; want alcohol from the current line", got, ok)
	}
	// Falls back to the last exchange when the current line is neutral.
	tr := []Exchange{{Choice: "x", Reply: "Сахур мне как брат"}}
	if got, ok := currentTopic(ch, tr, "ага"); !ok || got.Key != "sahur" {
		t.Fatalf("topic = %+v ok=%v; want sahur from the last reply", got, ok)
	}
	if _, ok := currentTopic(ch, nil, "какая погода"); ok {
		t.Error("a neutral line should name no topic")
	}
}

// The tail tells the model what the conversation is on, so slot 4 can leave it,
// and stops naming a steer target once the last theme is saturated.
func TestTailNamesTopicAndReleasesSteer(t *testing.T) {
	ch := themedChar()
	var tr []Exchange
	for i := 0; i < themeConfirmTurns; i++ {
		tr = append(tr, Exchange{Choice: "давай завязывай пить", Reply: "не могу"})
	}
	eng := themeEngagement(ch, tr, "")
	msgs, _ := buildMessages(ch, tr, "и что дальше?", StartAnger, []string{"woman_children", "sahur"}, eng)
	tail := msgs[len(msgs)-1].Content

	if !strings.Contains(tail, "не навязывай ничего") {
		t.Errorf("a saturated last theme should release the first slot; got:\n%s", tail)
	}
	if !strings.Contains(tail, "Сейчас разговор о: alcohol") {
		t.Errorf("tail should name the current topic so slot 4 can avoid it; got:\n%s", tail)
	}
	if !strings.Contains(tail, "отметь тему раскрытой") {
		t.Errorf("a long-discussed open theme should be flagged to the judge; got:\n%s", tail)
	}
}

// A reply cut off at max_tokens is a truncation, not bad formatting. Measured on
// deepseek-v4-flash once the four role slots made answers longer: exactly 900
// completion tokens, finish_reason "length", unparsable, whole turn wasted.
func TestTruncatedReplyReportsTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":`+
			strconv.Quote(`{"reply":"Ну чё, слышь, я тут стою и`)+
			`},"finish_reason":"length"}],"usage":{"completion_tokens":1500}}`)
	}))
	defer srv.Close()

	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	_, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger, nil)
	if !errors.Is(err, ErrLLMUnparsable) {
		t.Fatalf("err = %v; want ErrLLMUnparsable", err)
	}
	if !strings.Contains(err.Error(), "truncated") || !strings.Contains(err.Error(), "finish_reason=length") {
		t.Fatalf("error should name the truncation, got: %v", err)
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

// The judge can only stop repeating options if it is shown what it already
// offered — newest first, de-duplicated, and including the static opening set.
func TestRecentlyOffered(t *testing.T) {
	ch := testChar()
	ch.OpeningOptions = []string{"первый статичный", "второй статичный"}
	windowed := []Exchange{
		{Choice: "a", Reply: "r", Options: []string{"старый вариант", "общий"}},
		{Choice: "b", Reply: "r", Options: []string{"свежий вариант", "  ОБЩИЙ  ", ""}},
	}

	got := recentlyOffered(ch, windowed)
	want := []string{
		// Newest exchange first...
		"свежий вариант", "ОБЩИЙ",
		// ...then older ones ("общий" already seen, case-insensitively)...
		"старый вариант",
		// ...then the static opening options.
		"первый статичный", "второй статичный",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recentlyOffered =\n %q\nwant\n %q", got, want)
	}
}

func TestRecentlyOfferedIsCapped(t *testing.T) {
	var windowed []Exchange
	for i := 0; i < 50; i++ {
		windowed = append(windowed, Exchange{
			Options: []string{fmt.Sprintf("вариант-%d-a", i), fmt.Sprintf("вариант-%d-b", i)},
		})
	}
	got := recentlyOffered(testChar(), windowed)
	if len(got) != maxRecentlyOffered {
		t.Fatalf("len = %d; want the cap %d", len(got), maxRecentlyOffered)
	}
	// The cap keeps the newest, which are the ones worth not repeating.
	if got[0] != "вариант-49-a" {
		t.Fatalf("first = %q; want the newest option", got[0])
	}
}

// The offered options must reach the prompt, and must fall out of it exactly when
// their exchange is forgotten — the same window as the rest of the history.
func TestBuildMessagesCarriesOfferedOptions(t *testing.T) {
	tr := []Exchange{{Choice: "привет", Reply: "ну", Options: []string{"уже предлагал это"}}}
	msgs, _ := buildMessages(testChar(), tr, "дальше", StartAnger, nil, nil)
	// The options ride along inside the history — appended once to the reply they
	// were offered with — rather than being re-listed in the tail every turn.
	var history string
	for _, m := range msgs[1 : len(msgs)-1] {
		history += m.Content + "\n"
	}
	if !strings.Contains(history, `"уже предлагал это"`) {
		t.Fatalf("offered options should be in the replayed history JSON; got:\n%s", history)
	}
	if strings.Contains(msgs[len(msgs)-1].Content, "уже предлагал это") {
		t.Fatal("options must not be re-sent in the tail — that is the duplication we removed")
	}

	// A transcript far past the window: the old exchange and its options are gone.
	// Sized from the constant so this keeps testing the window if the budget moves.
	big := strings.Repeat("я", historyTokens*2)
	droppedMsgs, _ := buildMessages(testChar(), []Exchange{
		{Choice: big, Reply: big, Options: []string{"забытый вариант"}},
	}, "дальше", StartAnger, nil, nil)
	sysDropped := droppedMsgs[len(droppedMsgs)-1].Content
	if strings.Contains(sysDropped, "забытый вариант") {
		t.Fatal("options of a forgotten exchange must not survive in the prompt")
	}
}

// With no history there is nothing to avoid repeating except the static opening
// options — and the block is skipped entirely when even those are absent.
func TestBuildMessagesOmitsOfferedBlockWhenEmpty(t *testing.T) {
	ch := testChar()
	ch.OpeningOptions = nil
	msgs, _ := buildMessages(ch, nil, "", StartAnger, nil, nil)
	sys := msgs[len(msgs)-1].Content
	if strings.Contains(sys, "УЖЕ предлагал") {
		t.Fatalf("nothing offered yet, so the block should be absent; got:\n%s", sys)
	}
}

// Options are part of what an exchange costs, or the window would under-count
// and the prompt could overflow.
func TestExchangeTokensCountsOptions(t *testing.T) {
	bare := Exchange{Choice: "a", Reply: "b"}
	withOpts := Exchange{Choice: "a", Reply: "b", Options: []string{"один", "два"}}
	if exchangeTokens(withOpts) <= exchangeTokens(bare) {
		t.Fatalf("options should add cost: %d vs %d", exchangeTokens(withOpts), exchangeTokens(bare))
	}
	want := estTokens("a") + estTokens("b") + estTokens("один") + estTokens("два")
	if got := exchangeTokens(withOpts); got != want {
		t.Fatalf("exchangeTokens = %d; want %d", got, want)
	}
}

func TestWindowTranscript(t *testing.T) {
	tr := []Exchange{
		{Choice: "a1", Reply: "b1"},
		{Choice: "a2", Reply: "b2"},
		{Choice: "a3", Reply: "b3"},
	}
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
	// Each exchange costs about a third of the budget, so a few fit and the rest
	// are dropped. Sized from the constant so a budget change can't turn this into
	// a test of nothing.
	big := strings.Repeat("я", historyTokens/6)
	var tr []Exchange
	for i := 0; i < 100; i++ {
		tr = append(tr, Exchange{Choice: big, Reply: big})
	}
	msgs, _ := buildMessages(testChar(), tr, "финал", StartAnger, nil, nil)
	included := (len(msgs) - 2) / 2 // minus system + current user
	if included >= 100 {
		t.Fatalf("old history not dropped: included=%d of 100", included)
	}
	if included == 0 {
		t.Fatal("everything dropped; expected some recent history to fit")
	}
}

// The history window is capped by turn count as well as by tokens, so a long game
// of short turns cannot grow the prompt without bound either.
func TestHistoryCappedByExchangeCount(t *testing.T) {
	var tr []Exchange
	for i := 0; i < historyExchanges*4; i++ {
		tr = append(tr, Exchange{Choice: "коротко", Reply: "тоже коротко"})
	}
	msgs, _ := buildMessages(testChar(), tr, "финал", StartAnger, nil, nil)
	included := (len(msgs) - 2) / 2
	if included != historyExchanges {
		t.Fatalf("included %d exchanges; want the cap %d", included, historyExchanges)
	}
	// And the tokens those short turns cost are nowhere near the token budget, so
	// it really is the count that bound it.
	var used int
	for _, ex := range tr[len(tr)-historyExchanges:] {
		used += exchangeTokens(ex)
	}
	if used >= historyTokens {
		t.Fatalf("this case should be count-bound, not token-bound (used %d of %d)", used, historyTokens)
	}
}

// The completion is capped too: a runaway generation must not cost many times a
// normal turn.
func TestRequestCapsCompletionTokens(t *testing.T) {
	var req chatRequest
	srv := llmServer(t, `{"reply":"ну","art":"vanya_angry","options":["a"]}`, http.StatusOK, nil, &req)
	defer srv.Close()
	ev := NewOpenAIEvaluator(config.LLM{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	if _, err := ev.Judge(context.Background(), testChar(), nil, "x", StartAnger, nil); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if req.MaxTokens != maxCompletionTokens {
		t.Fatalf("max_tokens = %d; want %d", req.MaxTokens, maxCompletionTokens)
	}
}
