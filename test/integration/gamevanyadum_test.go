//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/gamevanyadum"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// «ВАНЯДУМ» end to end: a real HTTP server, a real WebSocket, a real
// PostgreSQL, and the game's own simulation loop driven by a channel this test
// fires.
//
// Its own harness rather than a share of the yard's, which is the same rule that
// keeps the two games' packages apart applied to their tests: this file can be
// deleted with the game and nothing else notices.
//
// The tick is a channel and never a ticker, so every assertion below is "advance
// exactly N steps, then look" rather than "sleep and hope". That is the whole
// reason the simulation takes its tick as a parameter in production.

// dumVK starts the fake VK ID server this file's logins go through, closed on
// cleanup. The dynamic one, because loginAs derives the account from the code it
// sends and every test here uses a different account.
//
// The account ids below are NUMERIC because this fake interpolates the code
// straight into a JSON `user_id` field — a code like "dum-play" produces
// unquoted nonsense and a 502 that looks like a VK outage rather than like a
// test's own mistake.
func dumVK(t *testing.T) string {
	t.Helper()
	srv := fakeVKDynamic()
	t.Cleanup(srv.Close)
	return srv.URL
}

// buildAppVanyadum builds the app with a running hub and «ВАНЯДУМ» wired to it,
// returning the tick channel that drives the simulation and the service itself.
func buildAppVanyadum(t *testing.T, vkBaseURL string) (http.Handler, chan time.Time, *gamevanyadum.Service) {
	t.Helper()
	hub := realtime.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-hub.Done()
	})

	svc := gamevanyadum.NewService(hub, gamevanyadum.Room, pool, gamevanyadum.NewPostgresRepository())
	tick := make(chan time.Time)
	go svc.Run(ctx, tick)

	cfg := config.Config{
		Env:     "dev",
		BaseURL: "http://localhost", // origin allowlist for the socket
		VK:      config.VK{AppID: "app-1", ServiceToken: "svc", RedirectURI: vkRedirect, BaseURL: vkBaseURL},
	}
	h := httpapi.NewServer(httpapi.Deps{
		Config:       cfg,
		Pool:         pool,
		WebFS:        fstest.MapFS{"index.html": {Data: []byte("<html>psycho</html>")}},
		VK:           vk.New(vkBaseURL, "app-1", "svc", vkRedirect),
		Accounts:     newAccountService(),
		Sessions:     session.NewManager(pool, key(3), time.Hour, false),
		GameVanyadum: svc,
		Realtime:     hub,
		RealtimeCtx:  ctx,
		RealtimeHandlers: map[string]realtime.Handler{
			gamevanyadum.Room: svc,
		},
	}).Handler()
	return observability.WrapHandler(h, "http.server"), tick, svc
}

// dialVanyadum opens a socket in this game's room. The room is a query
// parameter, and asking for one nothing listens to is refused at the handshake.
func dialVanyadum(t *testing.T, appURL, cookie, room string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", cookie)
	}
	h.Set("Origin", "http://localhost")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL(appURL, "/api/realtime?room="+room), &websocket.DialOptions{HTTPHeader: h})
}

// startRun begins a run over HTTP and returns the decoded body.
func startRun(t *testing.T, cli *http.Client, base string) map[string]any {
	t.Helper()
	resp, err := cli.Post(base+"/api/game-vanyadum/runs", "application/json", nil)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start run: status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return out
}

// waitForFrame pumps the simulation until a frame of the given type arrives.
// It advances the world rather than waiting for it, which is what keeps this
// file free of sleeps.
func waitForFrame(t *testing.T, frames <-chan []byte, tick chan time.Time, want string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for i := 0; i < 400; i++ {
		select {
		case raw := <-frames:
			var f map[string]any
			if err := json.Unmarshal(raw, &f); err != nil {
				continue
			}
			if f["t"] == want {
				return f
			}
		case tick <- time.Unix(int64(i), 0):
		case <-deadline:
			t.Fatalf("no %s frame arrived", want)
		}
	}
	t.Fatalf("no %s frame arrived", want)
	return nil
}

func TestVanyadumConfigIsServedAndIsTheWholeCatalogue(t *testing.T) {
	app, _, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910001", "user")

	resp, err := cli.Get(srv.URL + "/api/game-vanyadum/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var cfg struct {
		Player   map[string]float64 `json:"player"`
		Pickups  []map[string]any   `json:"pickups"`
		Surfaces []map[string]any   `json:"surfaces"`
		Sim      map[string]float64 `json:"sim"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	// Everything the client needs in order to draw, label and TEACH this game
	// comes from here — the splash screen's rules cheatsheet is generated from
	// it, so a missing field is a rule the player is never told.
	if cfg.Player["walk_speed"] <= 0 || cfg.Player["max_step"] <= 0 {
		t.Fatalf("player config is incomplete: %+v", cfg.Player)
	}
	if len(cfg.Pickups) == 0 || len(cfg.Surfaces) == 0 {
		t.Fatal("a catalogue with nothing in it teaches nothing")
	}
	if cfg.Sim["input_hz"] <= 0 || cfg.Sim["max_commands"] <= 0 {
		t.Fatalf("the client is not told the rates it has to match: %+v", cfg.Sim)
	}
}

func TestVanyadumRunIsCreatedWithAWholeLevel(t *testing.T) {
	app, _, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910002", "user")

	body := startRun(t, cli, srv.URL)
	level, ok := body["level"].(map[string]any)
	if !ok {
		t.Fatalf("no level in %v", body)
	}
	// The level is sent ONCE, here, and never on a snapshot — which is the
	// single most expensive mistake available in this game.
	for _, key := range []string{"sectors", "walls", "pickups", "spawn"} {
		if level[key] == nil {
			t.Fatalf("level is missing %q", key)
		}
	}
	if sectors, _ := level["sectors"].([]any); len(sectors) < 2 {
		t.Fatalf("a заброшка with %d rooms is not one", len(sectors))
	}
}

func TestVanyadumSecondRunIsRefusedAndResumable(t *testing.T) {
	app, _, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910003", "user")

	first := startRun(t, cli, srv.URL)

	// A refusal rather than a silent replacement: dropping the arena would throw
	// away a run open on the player's other tab.
	resp, err := cli.Post(srv.URL+"/api/game-vanyadum/runs", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second start: status %d, want 409", resp.StatusCode)
	}

	// And the run is resumable, which is what a page reload does.
	cur, err := cli.Get(srv.URL + "/api/game-vanyadum/runs/current")
	if err != nil {
		t.Fatal(err)
	}
	defer cur.Body.Close()
	var resumed map[string]any
	if err := json.NewDecoder(cur.Body).Decode(&resumed); err != nil {
		t.Fatal(err)
	}
	if resumed["run_id"] != first["run_id"] {
		t.Fatalf("resumed a different run: %v vs %v", resumed["run_id"], first["run_id"])
	}
}

func TestVanyadumGivingUpWritesNothing(t *testing.T) {
	// A run somebody walked out of is not a result, so the only rows in this
	// game's one table are runs that actually ended.
	app, tick, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910004", "user")

	startRun(t, cli, srv.URL)
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/game-vanyadum/runs/current", nil)
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("abandon: status %d", resp.StatusCode)
	}

	// Give the loop a few ticks to have written something, if it were going to.
	for i := 0; i < 5; i++ {
		tick <- time.Unix(int64(i), 0)
	}
	if n := runRowCount(t, "910004"); n != 0 {
		t.Fatalf("an abandoned run left %d rows behind", n)
	}
}

func TestVanyadumTheSocketSimulatesAndAnswersWithSnapshots(t *testing.T) {
	app, tick, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910005", "user")

	body := startRun(t, cli, srv.URL)
	conn, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)

	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"vanyadum_hello"}`)); err != nil {
		t.Fatal(err)
	}
	ready := waitForFrame(t, frames, tick, "vanyadum_ready")
	if ready["run_id"] != body["run_id"] {
		t.Fatalf("attached to %v, started %v", ready["run_id"], body["run_id"])
	}

	first := waitForFrame(t, frames, tick, "vanyadum_snap")
	startX, startY := first["x"], first["y"]

	// Walk in several directions, so the test does not depend on which way the
	// spawn faces or where the nearest wall is. The client sends INTENT — axes
	// and an angle — and never a position.
	//
	// BATCHED, exactly as the browser batches: the socket allows ten frames a
	// second and this test writes as fast as it can, so one frame per sub-step
	// trips the platform's rate limiter and the connection is closed under it.
	// That is the limiter doing its job — it is the reason the client samples at
	// four times the send rate and packs the sub-steps into one frame — so the
	// test sends the same shape rather than asking for an exemption.
	const batches, perFrame = 8, 4
	for i := 0; i < batches; i++ {
		cmds := make([]map[string]any, 0, perFrame)
		for j := 0; j < perFrame; j++ {
			// One sequence per COMMAND, one-based: reconciliation has to hear
			// "I applied three of your four", and the server drops anything at
			// or below what it has already folded in.
			seq := i*perFrame + j + 1
			cmds = append(cmds, map[string]any{
				"q": seq, "dt": 0.05, "my": 1, "yaw": float64(seq) * 0.6,
			})
		}
		msg, _ := json.Marshal(map[string]any{"t": "vanyadum_input", "k": 0, "cmds": cmds})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
		// Several steps per frame, so the whole batch is actually simulated: the
		// time budget only lets a tick spend a tick's worth of it.
		for k := 0; k < perFrame; k++ {
			tick <- time.Unix(int64(i*perFrame+k), 0)
		}
	}

	// Read PAST the snapshots already queued: the frame channel is buffered, so
	// the next one out of it describes a tick from before the walking started
	// and would compare equal for reasons that have nothing to do with the
	// simulation. Keep pumping until the position actually differs.
	var moved map[string]any
	for i := 0; i < 200 && moved == nil; i++ {
		select {
		case raw := <-frames:
			var f map[string]any
			if json.Unmarshal(raw, &f) == nil && f["t"] == "vanyadum_snap" &&
				(f["x"] != startX || f["y"] != startY) {
				moved = f
			}
		case tick <- time.Unix(int64(1000+i), 0):
		}
	}
	if moved == nil {
		t.Fatalf("thirty-two steps of walking moved nobody from %v/%v", startX, startY)
	}
	// The acknowledgement client-side prediction reconciles against: the last
	// COMMAND sequence the server folded in.
	if ack, _ := moved["ack"].(float64); ack < 1 {
		t.Fatalf("ack never advanced: %v", moved["ack"])
	}
	// The timeline entity interpolation runs on. A snapshot without it cannot
	// be placed between two others.
	if tick, _ := moved["k"].(float64); tick < 1 {
		t.Fatalf("snapshot carries no tick: %v", moved["k"])
	}
	if _, ok := moved["hp"]; !ok {
		t.Fatal("a snapshot with no health in it")
	}
}

func TestVanyadumAFinishedRunIsWrittenDown(t *testing.T) {
	// The other end of the loop, and the only two database statements this game
	// makes. Driven by walking the player onto every pickup — reaching into the
	// arena would test the arena, which its own unit tests already do.
	app, tick, svc := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910006", "user")

	startRun(t, cli, srv.URL)
	conn, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"vanyadum_hello"}`)); err != nil {
		t.Fatal(err)
	}
	waitForFrame(t, frames, tick, "vanyadum_ready")

	// Walking the whole заброшка with a wandering bot would be a flaky test, so
	// the pickups are collected by pointing the player at each one in turn: the
	// arena is the test's to arrange, and the thing under test is what the
	// SERVICE does when the last one is taken.
	accountID, err := uuid.Parse(accountIDByUID(t, "910006"))
	if err != nil {
		t.Fatal(err)
	}
	arena, ok := svc.CurrentRun(accountID)
	if !ok {
		t.Fatal("no arena for the run that was just started")
	}
	total := len(arena.Level.Pickups)

	over := func() map[string]any {
		for i := 0; i < 400; i++ {
			select {
			case raw := <-frames:
				var f map[string]any
				if json.Unmarshal(raw, &f) == nil && f["t"] == "vanyadum_over" {
					return f
				}
			default:
			}
			// Stand on whichever pickup is next, then advance one step.
			for _, p := range arena.Level.Pickups {
				if !arena.Taken[p.ID] {
					owner := arena.Owner()
					owner.State.Pos = p.Pos
					owner.State.Sector = p.Sector
					break
				}
			}
			tick <- time.Unix(int64(i), 0)
		}
		t.Fatal("the run never ended")
		return nil
	}()

	if over["success"] != true {
		t.Fatalf("collecting everything should be a success: %v", over)
	}

	// And it reaches Postgres, which is the whole point: the arena is ephemeral
	// and this row is the only thing that outlives it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runRowCount(t, "910006") == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := runRowCount(t, "910006"); n != 1 {
		t.Fatalf("finished run wrote %d rows", n)
	}

	// And the player can see it without opening a database, which is what makes
	// this half of the iteration verifiable in production.
	resp, err := cli.Get(srv.URL + "/api/game-vanyadum/runs/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var mine struct {
		Runs []struct {
			Success bool `json:"success"`
			Beer    int  `json:"beer"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mine); err != nil {
		t.Fatal(err)
	}
	if len(mine.Runs) != 1 || !mine.Runs[0].Success || mine.Runs[0].Beer != total {
		t.Fatalf("my runs reads back wrong: %+v (expected %d beers)", mine.Runs, total)
	}
}

func TestVanyadumRoomIsRefusedWhenNothingListens(t *testing.T) {
	// The room registry is the platform's, and an unregistered name is refused at
	// the handshake rather than opened and ignored: a socket nothing reads spends
	// one of the three connections an account is allowed, and the client cannot
	// tell the difference from the inside.
	app, _, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910007", "user")

	_, resp, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), "no-such-room")
	if err == nil {
		t.Fatal("a room with no handler was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unknown room, got %v", resp)
	}

	// This harness registers «ВАНЯДУМ» and nothing else, so even the yard's room
	// is unknown here — which is the registry doing its job rather than a list
	// of every room that has ever existed.
	if _, resp, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), httpapi.DefaultRoom); err == nil {
		t.Fatal("an unregistered room was accepted")
	} else if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %v", resp)
	}
}

// runRowCount counts an account's rows in the game's one table.
func runRowCount(t *testing.T, uid string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM game_vanyadum_runs WHERE account_id = $1::uuid`,
		accountIDByUID(t, uid)).Scan(&n)
	if err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}
