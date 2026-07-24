package game

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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
	res, err := ev.Judge(context.Background(), testChar(), []Exchange{{Choice: "привет", Reply: "ну"}}, "домой")
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
	res, err := ev.Judge(context.Background(), testChar(), nil, "")
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
	if _, err := ev.Judge(context.Background(), testChar(), nil, "x"); err == nil {
		t.Fatal("want error on http 500")
	}
	// 200 but content isn't valid JSON.
	junk := llmServer(t, "not json", http.StatusOK, nil, nil)
	defer junk.Close()
	ev2 := NewOpenAIEvaluator(config.LLM{BaseURL: junk.URL, APIKey: "k", Model: "m"})
	if _, err := ev2.Judge(context.Background(), testChar(), nil, "x"); err == nil {
		t.Fatal("want error on non-JSON content")
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
	msgs := buildMessages(testChar(), tr, "финал")
	included := (len(msgs) - 2) / 2 // minus system + current user
	if included >= 100 {
		t.Fatalf("old history not dropped: included=%d of 100", included)
	}
	if included == 0 {
		t.Fatal("everything dropped; expected some recent history to fit")
	}
}
