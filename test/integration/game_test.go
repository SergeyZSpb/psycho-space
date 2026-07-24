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

	// Pushing too far (fake LLM keys on "хамство"): the character snaps, the run
	// is over, and the backend forces the game-over art over the model's choice.
	st, rGO := attempt(nil, "хамство")
	if st != http.StatusOK || rGO["game_over"] != true || rGO["achieved"] != false {
		t.Fatalf("game over turn: status %d res %v; want 200 game_over=true", st, rGO)
	}
	if rGO["art"] != "vanya_game_over_hits_us" {
		t.Fatalf("game over art = %v; want the forced vanya_game_over_hits_us", rGO["art"])
	}
	if opts, _ := rGO["options"].([]any); len(opts) != 0 {
		t.Fatalf("game over should clear options: %v", rGO)
	}

	// A content-filter refusal (fake LLM keys on "цензура"): HTTP 200 from the
	// provider but prose instead of JSON. That is the dialogue's problem, not an
	// outage — 422 with a stable code, so the SPA can tell the player to pick a
	// different line instead of showing a scary gateway error.
	st, rC := attempt(nil, "цензура")
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("filtered turn: status %d res %v; want 422", st, rC)
	}
	if rC["error"] != "llm_unparsable" {
		t.Fatalf("filtered turn error = %v; want llm_unparsable", rC["error"])
	}
	if tr, _ := rC["trace_id"].(string); tr == "" {
		t.Fatalf("filtered turn should carry a trace_id for the user to quote: %v", rC)
	}

	// Unknown character -> 404.
	if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": "nobody", "transcript": []any{}, "choice": "x"}); st != http.StatusNotFound {
		t.Fatalf("unknown character attempt status %d; want 404", st)
	}

	// Record four runs: wins of 3 and 14 steps, losses of 6 and 21.
	for _, run := range []struct {
		success bool
		steps   int
	}{{true, 3}, {true, 14}, {false, 6}, {false, 21}} {
		if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/runs",
			map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey,
				"success": run.success, "steps": run.steps}); st != http.StatusCreated {
			t.Fatalf("submit run %+v status %d", run, st)
		}
	}

	// The leaderboard is four record boards over SINGLE runs, each row carrying
	// the record steps plus the player's tally.
	st, lb := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/runs/leaderboard?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("leaderboard status %d", st)
	}
	boards, _ := lb["boards"].(map[string]any)
	if boards == nil {
		t.Fatalf("leaderboard should return boards: %v", lb)
	}
	wantTop := map[string]float64{
		"longest_win":   14,
		"shortest_win":  3,
		"longest_loss":  21,
		"shortest_loss": 6,
	}
	for board, wantSteps := range wantTop {
		rows, _ := boards[board].([]any)
		if len(rows) != 1 {
			t.Fatalf("board %s: want 1 row, got %d: %v", board, len(rows), boards[board])
		}
		row, _ := rows[0].(map[string]any)
		if row["steps"].(float64) != wantSteps {
			t.Errorf("board %s steps = %v; want %v", board, row["steps"], wantSteps)
		}
		// Same tally on every board: 4 plays, 2 wins, 2 losses, and it's me.
		if row["mine"] != true || row["plays"].(float64) != 4 ||
			row["wins"].(float64) != 2 || row["losses"].(float64) != 2 {
			t.Errorf("board %s row = %v; want mine, plays 4, wins 2, losses 2", board, row)
		}
	}
	if len(boards) != 4 {
		t.Fatalf("want 4 boards, got %d: %v", len(boards), boards)
	}

	// My stats: 2 successes, 4 plays, best (fewest steps in a win) 3.
	st, me := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/runs/me?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("stats status %d", st)
	}
	if me["successes"].(float64) != 2 || me["plays"].(float64) != 4 || me["best_steps"].(float64) != 3 {
		t.Fatalf("stats = %v; want successes 2 plays 4 best_steps 3", me)
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
