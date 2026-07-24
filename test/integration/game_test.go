//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGameFlow(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL)) // LLM wired to the fake server
	defer app.Close()

	cli := loginAs(t, app.URL, "3001", "user")

	// Unauthenticated access is gated.
	if st, _ := doJSON(t, &http.Client{}, http.MethodGet, app.URL+"/api/game/config?game=smalltalk_khimki", nil); st != http.StatusUnauthorized {
		t.Fatalf("anon config status %d; want 401", st)
	}

	// Config: characters + default character (no answer options — LLM-generated).
	st, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/config?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("config status %d body %v", st, cfg)
	}
	charKey, _ := cfg["default_character"].(string)
	if charKey == "" || cfg["characters"] == nil {
		t.Fatalf("config missing default_character/characters: %v", cfg)
	}
	if st, _ := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/config?game=nope", nil); st != http.StatusNotFound {
		t.Fatalf("unknown game config status %d; want 404", st)
	}

	attempt := func(transcript []map[string]string, choice string) (int, map[string]any) {
		return doJSON(t, cli, http.MethodPost, app.URL+"/api/game/attempt", map[string]any{
			"game_key": "smalltalk_khimki", "character_key": charKey, "transcript": transcript, "choice": choice,
		})
	}

	// Opening turn (empty choice): not achieved, judge offers options.
	st, r0 := attempt(nil, "")
	if st != http.StatusOK || r0["achieved"] != false {
		t.Fatalf("opening: status %d res %v; want 200 achieved=false", st, r0)
	}
	if opts, _ := r0["options"].([]any); len(opts) == 0 {
		t.Fatalf("opening should offer options: %v", r0)
	}

	// A normal turn: still not achieved.
	st, rA := attempt(nil, "привет")
	if st != http.StatusOK || rA["achieved"] != false {
		t.Fatalf("turn A: status %d res %v; want achieved=false", st, rA)
	}

	// A convincing turn (fake LLM keys on "победа"): achieved, no more options.
	replyA, _ := rA["reply"].(string)
	st, rB := attempt([]map[string]string{{"choice": "привет", "reply": replyA}}, "победа")
	if st != http.StatusOK || rB["achieved"] != true {
		t.Fatalf("turn B: status %d res %v; want achieved=true", st, rB)
	}
	if opts, _ := rB["options"].([]any); len(opts) != 0 {
		t.Fatalf("achieved should clear options: %v", rB)
	}

	// Unknown character -> 404.
	if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": "nobody", "transcript": []any{}, "choice": "x"}); st != http.StatusNotFound {
		t.Fatalf("unknown character attempt status %d; want 404", st)
	}

	// Record a successful run (3 steps) and a failed run (6 steps).
	if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/runs",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "success": true, "steps": 3}); st != http.StatusCreated {
		t.Fatalf("submit success status %d", st)
	}
	if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/runs",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "success": false, "steps": 6}); st != http.StatusCreated {
		t.Fatalf("submit fail status %d", st)
	}

	// Leaderboard: one entry (me), 1 success, 2 plays, 9 total steps.
	st, lb := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/runs/leaderboard?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("leaderboard status %d", st)
	}
	entries, _ := lb["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("want 1 leaderboard entry, got %d: %v", len(entries), lb)
	}
	first, _ := entries[0].(map[string]any)
	if first["mine"] != true || first["successes"].(float64) != 1 || first["plays"].(float64) != 2 || first["steps"].(float64) != 9 {
		t.Fatalf("leaderboard top = %v; want mine, successes 1, plays 2, steps 9", first)
	}

	// My stats: 1 success, 2 plays, best 3 steps.
	st, me := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/runs/me?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("stats status %d", st)
	}
	if me["successes"].(float64) != 1 || me["plays"].(float64) != 2 || me["best_steps"].(float64) != 3 {
		t.Fatalf("stats = %v; want successes 1 plays 2 best_steps 3", me)
	}

	// With no LLM configured, the judge endpoint is unavailable (503).
	appNoLLM := httptest.NewServer(buildAppCfg(vkSrv.URL, ""))
	defer appNoLLM.Close()
	cli2 := loginAs(t, appNoLLM.URL, "3002", "user")
	if st, _ := doJSON(t, cli2, http.MethodPost, appNoLLM.URL+"/api/game/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "transcript": []any{}, "choice": "x"}); st != http.StatusServiceUnavailable {
		t.Fatalf("no-LLM attempt status %d; want 503", st)
	}
}

// TestGameAssets covers the DB blob store: an uploaded art is served publicly
// and advertised in the config; missing arts 404 and keep no image URL.
func TestGameAssets(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	// Upload an image directly, as the owner would over SSH.
	blob := []byte("\x00fake-webp\x01\x02")
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO game_assets (game_key, art_key, content_type, bytes) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (game_key, art_key) DO UPDATE SET bytes = EXCLUDED.bytes`,
		"smalltalk_khimki", "vanya_neutral", "image/webp", blob); err != nil {
		t.Fatalf("insert asset: %v", err)
	}

	// Public fetch (no auth), correct type + bytes.
	resp, err := http.Get(app.URL + "/api/game/assets/smalltalk_khimki/vanya_neutral")
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/webp" {
		t.Fatalf("asset status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if got, _ := io.ReadAll(resp.Body); !bytes.Equal(got, blob) {
		t.Fatalf("asset bytes mismatch")
	}

	// Unknown art -> 404.
	r, err := http.Get(app.URL + "/api/game/assets/smalltalk_khimki/nope")
	if err != nil {
		t.Fatalf("get unknown asset: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown asset status %d; want 404", r.StatusCode)
	}

	// Config advertises an image URL only for the uploaded art.
	cli := loginAs(t, app.URL, "3004", "user")
	_, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/config?game=smalltalk_khimki", nil)
	arts := cfg["characters"].([]any)[0].(map[string]any)["arts"].([]any)
	img := map[string]string{}
	for _, a := range arts {
		m := a.(map[string]any)
		k, _ := m["key"].(string)
		v, _ := m["image"].(string)
		img[k] = v
	}
	if img["vanya_neutral"] == "" {
		t.Fatalf("vanya_neutral should carry an image URL: %v", img)
	}
	if img["vanya_angry"] != "" {
		t.Fatalf("vanya_angry has no upload, image should be empty, got %q", img["vanya_angry"])
	}
}

// TestGameAttemptRateLimit checks the per-IP 10/min cap on the (paid) judge call.
func TestGameAttemptRateLimit(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL)) // fresh app -> fresh limiter
	defer app.Close()
	cli := loginAs(t, app.URL, "3003", "user")

	body := map[string]any{
		"game_key": "smalltalk_khimki", "character_key": "dyadya_vanya",
		"transcript": []any{}, "choice": "привет",
	}
	got429 := false
	for i := 0; i < 12; i++ { // limit is 10/min; the 11th+ should be blocked
		st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/attempt", body)
		if st == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected a 429 within 12 rapid attempts (limit 10/min per IP)")
	}
}
