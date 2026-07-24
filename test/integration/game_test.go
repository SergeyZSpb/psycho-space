//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SergeyZSpb/psycho-space/internal/game"
)

func TestGameFlow(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	cli := loginAs(t, app.URL, "3001", "user")

	// Unauthenticated access is gated.
	anon := &http.Client{}
	if st, _ := doJSON(t, anon, http.MethodGet, app.URL+"/api/game/config?game=smalltalk_khimki", nil); st != http.StatusUnauthorized {
		t.Fatalf("anon config status %d; want 401", st)
	}

	// Config: characters + default character.
	st, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/config?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("config status %d body %v", st, cfg)
	}
	if cfg["default_character"] == nil || cfg["characters"] == nil {
		t.Fatalf("config missing default_character/characters: %v", cfg)
	}
	if st, _ := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/config?game=nope", nil); st != http.StatusNotFound {
		t.Fatalf("unknown game config status %d; want 404", st)
	}

	// Turns are judged server-side. Use the authored profile (whitebox) to drive.
	g, _ := game.ContentFor("smalltalk_khimki")
	charKey := g.DefaultCharacter

	// A single strong option is short of the threshold: not achieved yet.
	if st, res := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "history": []string{}, "option_id": "domofon"}); st != http.StatusOK || res["achieved"] != false {
		t.Fatalf("turn 1: status %d res %v; want 200 achieved=false", st, res)
	}
	// domofon then lusy reaches the goal.
	if st, res := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "history": []string{"domofon"}, "option_id": "lusy"}); st != http.StatusOK || res["achieved"] != true {
		t.Fatalf("turn 2: status %d res %v; want 200 achieved=true", st, res)
	}
	// Unknown character -> 404.
	if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/attempt",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": "nobody", "history": []string{}, "option_id": "domofon"}); st != http.StatusNotFound {
		t.Fatalf("unknown character attempt status %d; want 404", st)
	}

	// Record a successful run (2 steps) and a failed run (5 steps).
	if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/runs",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "success": true, "steps": 2}); st != http.StatusCreated {
		t.Fatalf("submit success status %d", st)
	}
	if st, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/game/runs",
		map[string]any{"game_key": "smalltalk_khimki", "character_key": charKey, "success": false, "steps": 5}); st != http.StatusCreated {
		t.Fatalf("submit fail status %d", st)
	}

	// Leaderboard: one entry (me), 1 success, 2 plays, 7 total steps.
	st, lb := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/runs/leaderboard?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("leaderboard status %d", st)
	}
	entries, _ := lb["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("want 1 leaderboard entry, got %d: %v", len(entries), lb)
	}
	first, _ := entries[0].(map[string]any)
	if first["mine"] != true || first["successes"].(float64) != 1 || first["plays"].(float64) != 2 || first["steps"].(float64) != 7 {
		t.Fatalf("leaderboard top = %v; want mine, successes 1, plays 2, steps 7", first)
	}

	// My stats: 1 success, 2 plays, best 2 steps.
	st, me := doJSON(t, cli, http.MethodGet, app.URL+"/api/game/runs/me?game=smalltalk_khimki", nil)
	if st != http.StatusOK {
		t.Fatalf("stats status %d", st)
	}
	if me["successes"].(float64) != 1 || me["plays"].(float64) != 2 || me["best_steps"].(float64) != 2 {
		t.Fatalf("stats = %v; want successes 1 plays 2 best_steps 2", me)
	}
}
