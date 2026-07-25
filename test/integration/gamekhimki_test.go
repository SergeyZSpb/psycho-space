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

func TestGameKhimkiFlow(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL)) // LLM wired to the fake server
	defer app.Close()

	cli := loginAs(t, app.URL, "3001", "user")

	// Unauthenticated access is gated.
	if st, _ := doJSON(t, &http.Client{}, http.MethodGet, app.URL+"/api/game-khimki/config?game=smalltalk_khimki", nil); st != http.StatusUnauthorized {
		t.Fatalf("anon config status %d; want 401", st)
	}

	// Config: characters + default character (no answer options — LLM-generated).
	st, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-khimki/config?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("config status %d body %v", st, cfg)
	}
	charKey, _ := cfg["default_character"].(string)
	if charKey == "" || cfg["characters"] == nil {
		t.Fatalf("config missing default_character/characters: %v", cfg)
	}
	if st, _ := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-khimki/config?game=nope", nil); st != http.StatusNotFound {
		t.Fatalf("unknown game config status %d; want 404", st)
	}

	// The config carries the tension scale so the client never hardcodes it.
	if cfg["max_anger"].(float64) != 100 || cfg["start_anger"].(float64) <= 0 ||
		cfg["anger_lose_at"].(float64) != 90 {
		t.Fatalf("config should carry the tension scale: %v", cfg)
	}

	attempt := func(transcript []map[string]string, choice string) (int, map[string]any) {
		return doJSON(t, cli, http.MethodPost, app.URL+"/api/game-khimki/attempt", map[string]any{
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

	// /attempt is rate-limited to 10/min per IP because every call costs money,
	// and this test drives more turns than that. The remaining judge checks run
	// against a second app instance, which has its own limiter — we are testing
	// the judge here, not the limiter (that has its own test).
	app2 := httptest.NewServer(buildApp(vkSrv.URL))
	defer app2.Close()
	cli2 := loginAs(t, app2.URL, "3001", "user") // same account, fresh session
	attempt2 := func(body map[string]any) (int, map[string]any) {
		body["game_key"], body["character_key"] = "smalltalk_khimki", charKey
		return doJSON(t, cli2, http.MethodPost, app2.URL+"/api/game-khimki/attempt", body)
	}

	// Options the judge already offered travel back in the transcript and reach the
	// system prompt, so it can stop proposing the same lines. The fake judge
	// reports what it saw.
	st, rRep := attempt2(map[string]any{
		"choice": "повторы",
		"transcript": []map[string]any{
			{"choice": "привет", "reply": "ну", "art": "vanya_angry", "anger": 50,
				"options": []string{"уже это предлагал"}, "themes_done": []string{}},
		},
	})
	if st != http.StatusOK || rRep["reply"] != "already_offered=да" {
		t.Fatalf("offered options should reach the prompt: status %d res %v", st, rRep)
	}

	// The tension scale round-trips: we send it, the judge returns the new value,
	// and theme progress comes back alongside it.
	// The transcript engages the alcohol theme twice, which is what the backend now
	// requires before it believes the judge's claim that the theme is open — an
	// unsupported claim is dropped (the judge marked a theme on turn one in prod).
	st, rCalm := doJSON(t, cli, http.MethodPost, app.URL+"/api/game-khimki/attempt", map[string]any{
		"game_key": "smalltalk_khimki", "character_key": charKey, "choice": "теплеет", "anger": 60,
		"transcript": []map[string]any{
			{"choice": "может, пора завязать пить?", "reply": "не могу без бутылки", "anger": 60},
			{"choice": "тяжело бросать?", "reply": "тянет каждый вечер выпить", "anger": 60},
		},
	})
	if st != http.StatusOK || rCalm["anger"].(float64) != 25 || rCalm["game_over"] != false {
		t.Fatalf("calming turn: status %d res %v; want 200 anger 25", st, rCalm)
	}
	if themes, _ := rCalm["themes_done"].([]any); len(themes) != 1 || themes[0] != "alcohol" {
		t.Fatalf("themes_done = %v; want [alcohol]", rCalm["themes_done"])
	}

	// Theme progress the client carries steers the prompt: what is already open is
	// dropped from the still-closed list the judge is shown.
	st, rTh := attempt2(map[string]any{
		"choice": "темы", "transcript": []any{}, "anger": 50,
		"themes_done": []string{"alcohol"},
	})
	if st != http.StatusOK || rTh["reply"] != "open_themes=да" {
		t.Fatalf("open themes should reach the prompt: status %d res %v", st, rTh)
	}

	// Reaching the kill line ends the run on its own — the fake judge returns
	// anger 90 with NO game_over flag, and the backend must still lose the run and
	// force the punch art. This is the case the real model never triggered on its
	// own: it crawled to 95 and sat there.
	st, rMax := attempt2(map[string]any{"choice": "бесит", "transcript": []any{}, "anger": 80})
	if st != http.StatusOK || rMax["game_over"] != true {
		t.Fatalf("kill line: status %d res %v; want 200 game_over=true", st, rMax)
	}
	if rMax["anger"].(float64) != 90 || rMax["art"] != "vanya_game_over_hits_us" {
		t.Fatalf("kill line res = %v; want anger 90 and the forced punch art", rMax)
	}
	if opts, _ := rMax["options"].([]any); len(opts) != 0 {
		t.Fatalf("a lost run should clear options: %v", rMax)
	}

	// Garbled-but-recoverable JSON (fake LLM keys on "кривой") is salvaged: the
	// turn goes through with all four options, instead of costing the player a
	// move for the model's typo.
	st, rFix := attempt2(map[string]any{"choice": "кривой", "transcript": []any{}, "anger": 60})
	if st != http.StatusOK {
		t.Fatalf("garbled reply: status %d res %v; want 200 (salvaged)", st, rFix)
	}
	if rFix["reply"] != "Не нужна мне твоя помощь!" || rFix["anger"].(float64) != 70 {
		t.Fatalf("salvaged turn = %v; want the model's reply and anger 70", rFix)
	}
	if opts, _ := rFix["options"].([]any); len(opts) != 4 {
		t.Fatalf("salvaged options = %v; want the 3 stray ones recovered", rFix["options"])
	}

	// A content-filter refusal (fake LLM keys on "цензура"): HTTP 200 from the
	// provider but prose instead of JSON. That is the dialogue's problem, not an
	// outage — 422 with a stable code, so the SPA can tell the player to pick a
	// different line instead of showing a scary gateway error.
	st, rC := attempt2(map[string]any{"choice": "цензура", "transcript": []any{}})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("filtered turn: status %d res %v; want 422", st, rC)
	}
	if rC["error"] != "llm_unparsable" {
		t.Fatalf("filtered turn error = %v; want llm_unparsable", rC["error"])
	}
	if tr, _ := rC["trace_id"].(string); tr == "" {
		t.Fatalf("filtered turn should carry a trace_id for the user to quote: %v", rC)
	}

	// Unknown character -> 404. On its own instance: /attempt allows 5 a minute
	// per IP and both earlier instances are already at that limit, so this would
	// otherwise be answered 429 by the limiter before reaching the handler.
	app3 := httptest.NewServer(buildApp(vkSrv.URL))
	defer app3.Close()
	cli3 := loginAs(t, app3.URL, "3001", "user")
	if st, _ := doJSON(t, cli3, http.MethodPost, app3.URL+"/api/game-khimki/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": "nobody", "transcript": []any{}, "choice": "x"}); st != http.StatusNotFound {
		t.Fatalf("unknown character attempt status %d; want 404", st)
	}

	// Record four runs: wins of 3 and 14 steps, losses of 6 and 21.
	for _, run := range []struct {
		success bool
		steps   int
	}{{true, 3}, {true, 14}, {false, 6}, {false, 21}} {
		if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game-khimki/runs",
			map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey,
				"success": run.success, "steps": run.steps}); st != http.StatusCreated {
			t.Fatalf("submit run %+v status %d", run, st)
		}
	}

	// The leaderboard is four record boards over SINGLE runs, each row carrying
	// the record steps plus the player's tally.
	st, lb := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-khimki/runs/leaderboard?game=smalltalk_khimki", nil)
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
	st, me := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-khimki/runs/me?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("stats status %d", st)
	}
	if me["successes"].(float64) != 2 || me["plays"].(float64) != 4 || me["best_steps"].(float64) != 3 {
		t.Fatalf("stats = %v; want successes 2 plays 4 best_steps 3", me)
	}

	// With no LLM configured, the judge endpoint is unavailable (503).
	appNoLLM := httptest.NewServer(buildAppCfg(vkSrv.URL, ""))
	defer appNoLLM.Close()
	cliNoLLM := loginAs(t, appNoLLM.URL, "3002", "user")
	if st, _ := doJSON(t, cliNoLLM, http.MethodPost, appNoLLM.URL+"/api/game-khimki/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "transcript": []any{}, "choice": "x"}); st != http.StatusServiceUnavailable {
		t.Fatalf("no-LLM attempt status %d; want 503", st)
	}
}

// TestGameKhimkiAssets covers the DB blob store: an uploaded art is served publicly
// and advertised in the config; missing arts 404 and keep no image URL.
func TestGameKhimkiAssets(t *testing.T) {
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
	resp, err := http.Get(app.URL + "/api/game-assets/smalltalk_khimki/vanya_neutral")
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
	r, err := http.Get(app.URL + "/api/game-assets/smalltalk_khimki/nope")
	if err != nil {
		t.Fatalf("get unknown asset: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown asset status %d; want 404", r.StatusCode)
	}

	// Config advertises an image URL only for the uploaded art.
	cli := loginAs(t, app.URL, "3004", "user")
	_, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-khimki/config?game=smalltalk_khimki", nil)
	arts := cfg["characters"].([]any)[0].(map[string]any)["arts"].([]any)
	img := map[string]string{}
	for _, a := range arts {
		m := a.(map[string]any)
		k, _ := m["key"].(string)
		v, _ := m["image"].(string)
		img[k] = v
	}
	// The advertised URL must be the shared-infrastructure asset path, never a
	// game-owned one — the pre-rename /api/game/assets/ alias is deleted.
	if img["vanya_neutral"] != "/api/game-assets/smalltalk_khimki/vanya_neutral" {
		t.Fatalf("vanya_neutral image URL = %q; want the canonical /api/game-assets path", img["vanya_neutral"])
	}
	if img["vanya_angry"] != "" {
		t.Fatalf("vanya_angry has no upload, image should be empty, got %q", img["vanya_angry"])
	}
}

// TestGameKhimkiLegacyPathAliasIsGone pins the *absence* of the pre-rename
// /api/game/* prefix. That alias was registered as a second route group for
// exactly one deploy cycle, so a browser still running the previous SPA build out
// of cache could finish its run; that cycle is over and the registration was
// deleted from internal/httpapi/router.go.
//
// The test replaces TestGameKhimkiLegacyPathAlias, which pinned the alias while it
// existed. It asserts 404 rather than 401 on the gated path deliberately: 401
// would mean the route group is still registered and merely refusing the request,
// which is the failure mode a re-added alias would produce.
func TestGameKhimkiLegacyPathAliasIsGone(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	// An asset that genuinely exists, so a 404 on the old path can only mean the
	// route is gone — not that the blob is missing.
	blob := []byte("\x00fake-webp-alias\x01")
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO game_assets (game_key, art_key, content_type, bytes) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (game_key, art_key) DO UPDATE SET bytes = EXCLUDED.bytes`,
		"smalltalk_khimki", "hallway_pass", "image/webp", blob); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	resp, err := http.Get(app.URL + "/api/game/assets/smalltalk_khimki/hallway_pass")
	if err != nil {
		t.Fatalf("get asset via the removed alias: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("removed alias asset status %d; want 404 — /api/game/* must not be registered", resp.StatusCode)
	}

	// The canonical shared-infrastructure path still serves the same bytes, so
	// the deletion removed the alias and nothing else.
	canon, err := http.Get(app.URL + "/api/game-assets/smalltalk_khimki/hallway_pass")
	if err != nil {
		t.Fatalf("get asset via the canonical path: %v", err)
	}
	defer canon.Body.Close()
	if canon.StatusCode != http.StatusOK {
		t.Fatalf("canonical asset status %d; want 200", canon.StatusCode)
	}
	if got, _ := io.ReadAll(canon.Body); !bytes.Equal(got, blob) {
		t.Fatalf("canonical asset bytes mismatch")
	}

	// A gated route on the old prefix answers 404, not 401: 401 would mean the
	// route group is still registered and only the auth middleware is refusing.
	if st, _ := doJSON(t, &http.Client{}, http.MethodGet,
		app.URL+"/api/game/config?game=smalltalk_khimki", nil); st != http.StatusNotFound {
		t.Fatalf("anon config on the removed alias status %d; want 404", st)
	}
	cli := loginAs(t, app.URL, "3005", "user")
	if st, _ := doJSON(t, cli, http.MethodGet,
		app.URL+"/api/game/config?game=smalltalk_khimki", nil); st != http.StatusNotFound {
		t.Fatalf("approved config on the removed alias status %d; want 404", st)
	}
	// …while the canonical route is unaffected.
	st, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-khimki/config?game=smalltalk_khimki", nil)
	if st != http.StatusOK || cfg["default_character"] == nil {
		t.Fatalf("canonical config: status %d body %v; want 200 with a default_character", st, cfg)
	}
}

// TestGameKhimkiAttemptRateLimit checks the per-IP 10/min cap on the (paid) judge call.
func TestGameKhimkiAttemptRateLimit(t *testing.T) {
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
		st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game-khimki/attempt", body)
		if st == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected a 429 within 12 rapid attempts (limit 10/min per IP)")
	}
}
