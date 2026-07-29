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
	"github.com/SergeyZSpb/psycho-space/internal/gamekaren"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/coder/websocket"
)

// «СИМУЛЯТОР КАРЕНА» end to end: a real HTTP server, a real WebSocket, a real
// PostgreSQL, and the game's own simulation loop driven by a channel this file
// fires.
//
// Its own harness rather than a share of any other game's, which is the same
// rule that keeps the games' packages apart applied to their tests: this file
// can be deleted with the game and nothing else notices.
//
// THE CLOCK IS PACED, and that is the one thing here that differs from the
// shooter's harness. Every tick this file sends carries a timestamp exactly one
// SimStep after the last, AND is sent one real SimStep after the last, so the
// office's simulated clock and the wall clock agree to within a few
// milliseconds. That matters because this game has a rule measured in seconds —
// a shift shorter than MinShiftSeconds is dropped rather than written — and a
// test that fired two hundred ticks in a millisecond would be asserting against
// a shift that lasted no time at all on whichever of the two clocks the service
// happens to use. Pacing makes the assertion true under either reading, at a
// cost of a few seconds of wall clock in the two tests that need a shift to
// have genuinely lasted.
//
// Nothing here sleeps to wait for the world: every wait is bounded by a
// DEADLINE and advances the world while it waits.

// karenVK starts the fake VK ID server this file's logins go through, closed on
// cleanup.
//
// The account ids below are NUMERIC because this fake interpolates the code
// straight into a JSON `user_id` field — a code like "karen-play" produces
// unquoted nonsense and a 502 that looks like a VK outage rather than like a
// test's own mistake.
func karenVK(t *testing.T) string {
	t.Helper()
	srv := fakeVKDynamic()
	t.Cleanup(srv.Close)
	return srv.URL
}

// karenClock hands out the timestamps this file's ticks carry.
//
// It advances only when a tick is actually delivered, which is why peek and
// advance are separate: a select's send case evaluates its value even when
// another case wins, so reading the next timestamp inside the select would run
// the office's clock forward every time a frame arrived instead of every time
// the world stepped.
type karenClock struct {
	base time.Time
	n    int
}

func newKarenClock() *karenClock { return &karenClock{base: time.Now()} }

func (c *karenClock) peek() time.Time {
	return c.base.Add(time.Duration(c.n) * gamekaren.SimStep)
}

func (c *karenClock) advance() { c.n++ }

// buildAppKaren builds the app with a running hub and «СИМУЛЯТОР КАРЕНА» wired
// to it, returning the tick channel that drives the office and the service
// itself.
//
// The tick channel is UNBUFFERED: a send returns only once the simulation has
// taken it, so "the world advanced" is something this file knows rather than
// hopes.
func buildAppKaren(t *testing.T, vkBaseURL string) (http.Handler, chan time.Time, *gamekaren.Service) {
	t.Helper()
	hub := realtime.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-hub.Done()
	})

	svc := gamekaren.NewService(hub, gamekaren.Room, pool, gamekaren.NewPostgresRepository(), newAccountService())
	tick := make(chan time.Time)
	go svc.Run(ctx, tick)

	cfg := config.Config{
		Env:     "dev",
		BaseURL: "http://localhost", // origin allowlist for the socket
		VK:      config.VK{AppID: "app-1", ServiceToken: "svc", RedirectURI: vkRedirect, BaseURL: vkBaseURL},
	}
	h := httpapi.NewServer(httpapi.Deps{
		Config:   cfg,
		Pool:     pool,
		WebFS:    fstest.MapFS{"index.html": {Data: []byte("<html>psycho</html>")}},
		VK:       vk.New(vkBaseURL, "app-1", "svc", vkRedirect),
		Accounts: newAccountService(),
		Sessions: session.NewManager(pool, key(3), time.Hour, false),
		// The account service is wired because the leaderboard resolves display
		// names through it — a name is encrypted at rest, so the board cannot be
		// a join.
		GameKaren:   svc,
		Realtime:    hub,
		RealtimeCtx: ctx,
		RealtimeHandlers: map[string]realtime.Handler{
			gamekaren.Room: svc,
		},
	}).Handler()
	return observability.WrapHandler(h, "http.server"), tick, svc
}

// dialKaren opens a socket in this game's room. The room is a query parameter,
// and asking for one nothing listens to is refused at the handshake.
func dialKaren(t *testing.T, appURL, cookie, room string) (*websocket.Conn, *http.Response, error) {
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

// startShift clocks in over HTTP and returns the decoded body.
func startShift(t *testing.T, cli *http.Client, base string) map[string]any {
	t.Helper()
	resp, err := cli.Post(base+"/api/game-karen/shifts", "application/json", nil)
	if err != nil {
		t.Fatalf("start shift: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start shift: status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode shift: %v", err)
	}
	return out
}

// leaveShift walks out over HTTP and returns the status code.
func leaveShift(t *testing.T, cli *http.Client, base string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, base+"/api/game-karen/shifts/current", nil)
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("leave shift: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// karenStep sends exactly one tick, bounded so a stalled simulation fails the
// test rather than hanging it.
func karenStep(t *testing.T, tick chan time.Time, clock *karenClock) {
	t.Helper()
	select {
	case tick <- clock.peek():
		clock.advance()
	case <-time.After(5 * time.Second):
		t.Fatal("the office stopped taking ticks")
	}
}

// karenLongEnough is how long a shift has to run before the service will write
// it down, plus enough margin that a runner's scheduling jitter cannot put it
// under the line.
func karenLongEnough() time.Duration {
	return time.Duration(gamekaren.MinShiftSeconds*float64(time.Second)) + 700*time.Millisecond
}

// karenWork advances the office at real time for d, so a shift genuinely lasts
// that long on the wall clock as well as on the simulated one.
//
// This is the shape MinShiftSeconds forces: the rule is stated in seconds, and
// the only way to be right about it whichever clock the service reads is to make
// the two agree. It also keeps the bald man where he belongs — he closes at
// BossSpeed, so a burst of ticks that took no real time would still bring him
// across the office and end a shift this test wanted to end another way.
func karenWork(t *testing.T, tick chan time.Time, clock *karenClock, d time.Duration) {
	t.Helper()
	pace := time.NewTicker(gamekaren.SimStep)
	defer pace.Stop()
	until := time.After(d)
	for {
		select {
		case <-until:
			return
		case <-pace.C:
			karenStep(t, tick, clock)
		}
	}
}

// karenAgeWithoutTheChase makes a shift OLD ENOUGH TO RECORD without making it
// long enough for the bald man to end it.
//
// The two clocks come apart here, and that is the whole point. MinShiftSeconds
// is a WALL-CLOCK rule — a shift is worth a row if it lasted that long by
// StartedAt — while the chase advances only on SIMULATED ticks. karenWork paces
// ticks at real time so the two run together, which was safe while everybody
// spawned 16.5 m from him (3.8 s of head start) and is not now that a spawn is
// DRAWN: the floor is spawnFromBoss, so pumping a full 3.7 s of simulation raced
// him and lost. CI caught it as a 404 on the leave, the shift having already
// ended as `promoted`.
//
// So this spends one simulated second — enough that standing still has earned
// something, which is what the test then asserts — and lets the rest of the wall
// clock pass with the office standing still.
func karenAgeWithoutTheChase(t *testing.T, tick chan time.Time, clock *karenClock, d time.Duration) {
	t.Helper()
	started := time.Now()
	for i := 0; i < gamekaren.SimHz; i++ {
		karenStep(t, tick, clock)
	}
	if rest := d - time.Since(started); rest > 0 {
		time.Sleep(rest)
	}
}

// waitForKarenFrame pumps the office until a frame of the given type arrives.
//
// Bounded by TIME, never by a count of iterations: a bound on how hard it tries
// is not a bound on how long it waits, and on a loaded runner the two are
// nothing alike. Frames are drained continuously while it pumps, because the
// per-connection send buffer is finite and a test that stops reading is a test
// that gets its own socket closed under it.
func waitForKarenFrame(t *testing.T, frames <-chan []byte, tick chan time.Time, clock *karenClock, want string, within time.Duration) map[string]any {
	t.Helper()
	pace := time.NewTicker(gamekaren.SimStep)
	defer pace.Stop()
	deadline := time.After(within)
	for {
		select {
		case raw := <-frames:
			var f map[string]any
			if err := json.Unmarshal(raw, &f); err != nil {
				continue
			}
			if f["t"] == want {
				return f
			}
		case <-pace.C:
			// A short bound rather than the blocking send karenStep uses: the
			// simulation is briefly busy every tick, and skipping one costs this
			// loop 50 ms where waiting for it would stop frames being drained.
			select {
			case tick <- clock.peek():
				clock.advance()
			case <-time.After(gamekaren.SimStep):
			}
		case <-deadline:
			t.Fatalf("no %s frame arrived", want)
			return nil
		}
	}
}

// karenRows counts an account's rows in this game's one table.
func karenRows(t *testing.T, uid string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM game_karen_shifts WHERE account_id = $1::uuid`,
		accountIDByUID(t, uid)).Scan(&n)
	if err != nil {
		t.Fatalf("count shifts: %v", err)
	}
	return n
}

// karenCause reads the cause of an account's single written shift.
func karenCause(t *testing.T, uid string) string {
	t.Helper()
	var cause string
	err := pool.QueryRow(context.Background(),
		`SELECT cause FROM game_karen_shifts WHERE account_id = $1::uuid ORDER BY created_at DESC LIMIT 1`,
		accountIDByUID(t, uid)).Scan(&cause)
	if err != nil {
		t.Fatalf("read cause: %v", err)
	}
	return cause
}

// waitForKarenRow waits for the writer goroutine to land the row, bounded by a
// deadline. The write is deliberately asynchronous to the simulation — stalling
// a twenty-hertz loop for one summary row is the refused trade — so "it was
// written" is something this file waits for rather than something it can read
// the instant the shift ends.
func waitForKarenRow(t *testing.T, uid string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if karenRows(t, uid) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the shift never reached Postgres: %d rows, want %d", karenRows(t, uid), want)
}

func TestKarenConfigIsServedAndIsTheWholeCatalogue(t *testing.T) {
	app, _, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920001", "user")

	resp, err := cli.Get(srv.URL + "/api/game-karen/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var cfg struct {
		GameKey string `json:"game_key"`
		Title   string `json:"title"`
		Office  struct {
			W     float64          `json:"w"`
			H     float64          `json:"h"`
			Desks []map[string]any `json:"desks"`
		} `json:"office"`
		Money   map[string]float64 `json:"money"`
		Move    map[string]float64 `json:"move"`
		Boss    map[string]float64 `json:"boss"`
		Sim     map[string]float64 `json:"sim"`
		Endings []struct {
			Key   string `json:"key"`
			Title string `json:"title"`
			Sub   string `json:"sub"`
		} `json:"endings"`
		BossLines    []string `json:"boss_lines"`
		MaxOccupants int      `json:"max_occupants"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	// Everything the client needs in order to draw, label and TEACH this game
	// comes from here — the splash screen's rules cheatsheet is GENERATED from
	// it, so a missing field is a rule the player is never told.
	if cfg.GameKey == "" || cfg.Title == "" {
		t.Fatalf("the catalogue does not say what game it is: %+v", cfg)
	}
	if cfg.Office.W <= 0 || cfg.Office.H <= 0 || len(cfg.Office.Desks) == 0 {
		t.Fatalf("an office with no floor and no furniture: %+v", cfg.Office)
	}
	// The money ramp IS the game, and every one of these numbers appears on the
	// cheatsheet.
	for _, k := range []string{"base_per_second", "ramp_seconds", "max_multiplier", "grace_ms"} {
		if cfg.Money[k] <= 0 {
			t.Fatalf("money config is missing %q: %+v", k, cfg.Money)
		}
	}
	// The rates the client has to match, or prediction is a guess.
	for _, k := range []string{"walk_speed", "dash_speed", "dash_ms", "dash_cooldown_ms", "input_hz", "max_commands"} {
		if cfg.Move[k] <= 0 {
			t.Fatalf("move config is missing %q: %+v", k, cfg.Move)
		}
	}
	for _, k := range []string{"speed", "catch_radius", "grin_range"} {
		if cfg.Boss[k] <= 0 {
			t.Fatalf("boss config is missing %q: %+v", k, cfg.Boss)
		}
	}
	if cfg.Sim["hz"] <= 0 || cfg.Sim["snapshot_hz"] <= 0 {
		t.Fatalf("the client is not told the rates it has to match: %+v", cfg.Sim)
	}
	// Both endings, because the over screen reads its title and its sub-line
	// from the catalogue rather than carrying Russian copy of its own.
	if len(cfg.Endings) < 2 {
		t.Fatalf("a game with %d endings: %+v", len(cfg.Endings), cfg.Endings)
	}
	seen := map[string]bool{}
	for _, e := range cfg.Endings {
		if e.Key == "" || e.Title == "" || e.Sub == "" {
			t.Fatalf("an ending with nothing to show: %+v", e)
		}
		seen[e.Key] = true
	}
	// Both causes the code can write have to be in here, or an over screen has
	// nothing to render for one of them.
	if !seen[gamekaren.CausePromoted] || !seen[gamekaren.CauseLeft] {
		t.Fatalf("the catalogue is missing an ending the code writes: %+v", cfg.Endings)
	}
	if len(cfg.BossLines) == 0 {
		t.Fatal("the bald man has nothing to say")
	}
	if cfg.MaxOccupants <= 0 {
		t.Fatalf("max_occupants is %d", cfg.MaxOccupants)
	}
}

func TestKarenShiftIsCreatedAndResumable(t *testing.T) {
	app, _, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920002", "user")

	first := startShift(t, cli, srv.URL)
	if first["shift_id"] == nil || first["shift_id"] == "" {
		t.Fatalf("no shift id in %v", first)
	}
	// The room travels with the shift so the view never hardcodes it — the name
	// lives in the game's own package and nowhere else.
	if first["room"] != gamekaren.Room {
		t.Fatalf("room is %v, want %q", first["room"], gamekaren.Room)
	}
	// The office is STATIC and already in the catalogue, so a shift start sends
	// no level. This is the load-bearing difference from the shooter, and it is
	// worth pinning: re-sending an office nobody generated would be pure waste.
	if _, ok := first["level"]; ok {
		t.Fatalf("a shift start sent a level: %v", first)
	}

	// A refusal rather than a silent replacement: dropping the occupant would
	// throw away a shift open on the player's other tab.
	resp, err := cli.Post(srv.URL+"/api/game-karen/shifts", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var refusal map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&refusal)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second start: status %d, want 409", resp.StatusCode)
	}
	if refusal["error"] != "shift_in_progress" {
		t.Fatalf("second start said %v", refusal["error"])
	}

	// And the shift is resumable, which is what a page reload does.
	cur, err := cli.Get(srv.URL + "/api/game-karen/shifts/current")
	if err != nil {
		t.Fatal(err)
	}
	defer cur.Body.Close()
	if cur.StatusCode != http.StatusOK {
		t.Fatalf("current: status %d", cur.StatusCode)
	}
	var resumed map[string]any
	if err := json.NewDecoder(cur.Body).Decode(&resumed); err != nil {
		t.Fatal(err)
	}
	if resumed["shift_id"] != first["shift_id"] {
		t.Fatalf("resumed a different shift: %v vs %v", resumed["shift_id"], first["shift_id"])
	}

	// And somebody who never clocked in gets a 404 rather than an empty shift,
	// because the splash screen decides which phase to render from exactly this.
	other := loginAs(t, srv.URL, "920003", "user")
	none, err := other.Get(srv.URL + "/api/game-karen/shifts/current")
	if err != nil {
		t.Fatal(err)
	}
	var missing map[string]any
	_ = json.NewDecoder(none.Body).Decode(&missing)
	none.Body.Close()
	if none.StatusCode != http.StatusNotFound {
		t.Fatalf("current with no shift: status %d, want 404", none.StatusCode)
	}
	if missing["error"] != "no_shift" {
		t.Fatalf("current with no shift said %v", missing["error"])
	}
}

func TestKarenWalkingOutWritesTheShift(t *testing.T) {
	// Walking out IS a result here, unlike the shooter's «сдаться»: the point of
	// the game is the money, and money earned by standing still until you got
	// bored counts exactly as much as money earned until he reached you.
	app, tick, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920004", "user")
	clock := newKarenClock()

	startShift(t, cli, srv.URL)
	karenAgeWithoutTheChase(t, tick, clock, karenLongEnough())

	if code := leaveShift(t, cli, srv.URL); code != http.StatusNoContent {
		t.Fatalf("leave: status %d, want 204", code)
	}
	waitForKarenRow(t, "920004", 1)
	if c := karenCause(t, "920004"); c != gamekaren.CauseLeft {
		t.Fatalf("cause is %q, want %q", c, gamekaren.CauseLeft)
	}

	// And the player can see it without opening a database, which is what makes
	// this half of the iteration verifiable in production.
	resp, err := cli.Get(srv.URL + "/api/game-karen/shifts/me?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var mine struct {
		Shifts []struct {
			Cause   string  `json:"cause"`
			Salary  float64 `json:"salary"`
			Seconds float64 `json:"seconds"`
		} `json:"shifts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mine); err != nil {
		t.Fatal(err)
	}
	if len(mine.Shifts) != 1 || mine.Shifts[0].Cause != gamekaren.CauseLeft {
		t.Fatalf("my shifts reads back wrong: %+v", mine.Shifts)
	}
	// Standing still is the whole job, so a shift that lasted seconds and earned
	// nothing would mean the money never accrued.
	if mine.Shifts[0].Salary <= 0 {
		t.Fatalf("a whole shift and no salary: %+v", mine.Shifts[0])
	}
	if mine.Shifts[0].Seconds <= 0 {
		t.Fatalf("a shift that took no time: %+v", mine.Shifts[0])
	}

	// An absent ?limit= is "your default", not "none": the handler passes what it
	// parsed straight through, so the page size is decided next to the SQL rather
	// than once per caller — and a service that read a missing limit as zero would
	// serve an empty list to anybody who left it off.
	plain, err := cli.Get(srv.URL + "/api/game-karen/shifts/me")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Body.Close()
	var unlimited struct {
		Shifts []map[string]any `json:"shifts"`
	}
	if err := json.NewDecoder(plain.Body).Decode(&unlimited); err != nil {
		t.Fatal(err)
	}
	if len(unlimited.Shifts) != 1 {
		t.Fatalf("my shifts with no limit returned %d rows", len(unlimited.Shifts))
	}

	// The board is the other half of the splash screen, and it is the only route
	// that resolves a display name — a name is encrypted at rest, so it cannot be
	// a join and this is the one place the lookup is exercised.
	top, err := cli.Get(srv.URL + "/api/game-karen/shifts/top?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer top.Body.Close()
	var board struct {
		Shifts []struct {
			Name    string  `json:"name"`
			Cause   string  `json:"cause"`
			Salary  float64 `json:"salary"`
			Seconds float64 `json:"seconds"`
		} `json:"shifts"`
	}
	if err := json.NewDecoder(top.Body).Decode(&board); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range board.Shifts {
		if row.Name != "" && row.Salary > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("nobody on the board has a name and a salary: %+v", board.Shifts)
	}
}

func TestKarenAShortShiftIsNotWritten(t *testing.T) {
	// The negative half of the test above. A shift is dropped rather than
	// written below MinShiftSeconds, so opening the game, tapping НАЧАТЬ СМЕНУ
	// and immediately leaving does not put a row nobody meant into the only
	// table this game has.
	app, tick, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920005", "user")
	clock := newKarenClock()

	startShift(t, cli, srv.URL)
	if code := leaveShift(t, cli, srv.URL); code != http.StatusNoContent {
		t.Fatalf("leave: status %d, want 204", code)
	}

	// Give the loop and the writer several steps to have written something, if
	// they were going to.
	for i := 0; i < 10; i++ {
		karenStep(t, tick, clock)
	}
	if n := karenRows(t, "920005"); n != 0 {
		t.Fatalf("a shift that lasted no time left %d rows behind", n)
	}

	// And leaving again is a 404 rather than a second write.
	if code := leaveShift(t, cli, srv.URL); code != http.StatusNotFound {
		t.Fatalf("second leave: status %d, want 404", code)
	}
}

func TestKarenTheSocketSimulatesAndAnswersWithSnapshots(t *testing.T) {
	app, tick, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920006", "user")
	clock := newKarenClock()

	body := startShift(t, cli, srv.URL)
	conn, _, err := dialKaren(t, srv.URL, cookieHeader(t, cli, srv.URL), gamekaren.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)

	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"karen_hello"}`)); err != nil {
		t.Fatal(err)
	}
	ready := waitForKarenFrame(t, frames, tick, clock, "karen_ready", 15*time.Second)
	if ready["shift_id"] != body["shift_id"] {
		t.Fatalf("attached to %v, started %v", ready["shift_id"], body["shift_id"])
	}

	first := waitForKarenFrame(t, frames, tick, clock, "karen_snap", 15*time.Second)
	startX, startY := first["x"], first["y"]
	// The boss rides on every snapshot: the client renders his position and his
	// grin, and a frame without him cannot be drawn.
	if _, ok := first["b"]; !ok {
		t.Fatalf("a snapshot with no bald man in it: %v", first)
	}

	// Walk DOWN the plane, INTO the room. It used to walk up, away from the bald
	// man, which was right while everybody spawned mid-floor at a known point —
	// and is wrong now that a spawn is drawn. The draw has to leave a head start
	// of spawnFromBoss, and he stands at the far wall, so a spawn is always in the
	// band at the OPPOSITE end: walking "away" now means walking into the wall
	// that is a few centimetres behind you, which moves nobody. That is exactly
	// how this failed — "thirty-two steps of walking moved nobody from 930/35",
	// 35 cm being hard against the top edge.
	//
	// Down is into open floor from anywhere the draw can put you, and it is safe
	// despite being towards him: this walks about five metres out of a head start
	// of fifteen, so the shift cannot end underneath the assertions.
	//
	// BATCHED, exactly as the browser batches: the socket allows ten frames a
	// second and one frame per sub-step trips the platform's rate limiter, which
	// closes the connection. That is the limiter doing its job — it is the reason
	// the client samples at four times the send rate and packs the sub-steps into
	// one frame — so the test sends the same shape rather than asking for an
	// exemption.
	const batches, perFrame = 8, 4
	for i := 0; i < batches; i++ {
		cmds := make([]map[string]any, 0, perFrame)
		for j := 0; j < perFrame; j++ {
			// One sequence per COMMAND, one-based: reconciliation has to hear "I
			// applied three of your four", and the server drops anything at or
			// below what it has already folded in.
			seq := i*perFrame + j + 1
			cmds = append(cmds, map[string]any{
				"q": seq, "dt": gamekaren.SimStep.Seconds(), "mx": 0, "my": 1,
			})
		}
		msg, _ := json.Marshal(map[string]any{"t": "karen_input", "k": 0, "cmds": cmds})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
		// Several steps per frame, so the whole batch is actually simulated: the
		// time budget only lets a tick spend a tick's worth of it.
		for k := 0; k < perFrame; k++ {
			karenStep(t, tick, clock)
		}
	}

	// Read PAST the snapshots already queued: the frame channel is buffered, so
	// the next one out of it describes a tick from before the walking started and
	// would compare equal for reasons that have nothing to do with the
	// simulation. Keep pumping until the position actually differs — bounded by a
	// deadline, never by a count of attempts.
	//
	// PACED like everything else here, which matters for a second reason: an
	// unpaced pump would fire thousands of ticks into a fifteen-second deadline,
	// bring the bald man all the way across the office, end the shift, and then
	// fail with "nobody moved" when what actually happened is that the player was
	// promoted mid-assertion.
	var moved map[string]any
	pace := time.NewTicker(gamekaren.SimStep)
	defer pace.Stop()
	deadline := time.After(5 * time.Second)
	for moved == nil {
		select {
		case raw := <-frames:
			var f map[string]any
			if json.Unmarshal(raw, &f) == nil && f["t"] == "karen_snap" &&
				(f["x"] != startX || f["y"] != startY) {
				moved = f
			}
		case <-pace.C:
			select {
			case tick <- clock.peek():
				clock.advance()
			case <-time.After(gamekaren.SimStep):
			}
		case <-deadline:
			t.Fatalf("thirty-two steps of walking moved nobody from %v/%v", startX, startY)
		}
	}
	// The acknowledgement client-side prediction reconciles against: the last
	// COMMAND sequence the server folded in.
	if ack, _ := moved["ack"].(float64); ack < 1 {
		t.Fatalf("ack never advanced: %v", moved["ack"])
	}
	// The timeline the client eases corrections along. A snapshot without it
	// cannot be placed relative to another.
	if k, _ := moved["k"].(float64); k < 1 {
		t.Fatalf("snapshot carries no tick: %v", moved["k"])
	}
	// The HUD is drawn entirely from these three, so a snapshot missing one is a
	// readout that never updates.
	for _, k := range []string{"pay", "m", "st"} {
		if _, ok := moved[k]; !ok {
			t.Fatalf("a snapshot with no %q in it: %v", k, moved)
		}
	}
}

func TestKarenBeingCaughtWritesTheShift(t *testing.T) {
	// The other ending, and the only one nobody asks for. Driven entirely
	// through production paths: the occupant stands where the office put him and
	// the bald man walks over, which is exactly what happens to a player who
	// stops paying attention. Nothing here reaches into the world to arrange it,
	// because a test hook in a production path is a thing this project does not
	// ship.
	//
	// He closes at BossSpeed from his spawn, so this takes several seconds of
	// paced ticking; the wait is bounded by a deadline and advances the world
	// while it waits.
	app, tick, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920007", "user")
	clock := newKarenClock()

	startShift(t, cli, srv.URL)
	conn, _, err := dialKaren(t, srv.URL, cookieHeader(t, cli, srv.URL), gamekaren.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"t":"karen_hello"}`)); err != nil {
		t.Fatal(err)
	}
	waitForKarenFrame(t, frames, tick, clock, "karen_ready", 15*time.Second)

	over := waitForKarenFrame(t, frames, tick, clock, "karen_over", 60*time.Second)
	if over["cause"] != gamekaren.CausePromoted {
		t.Fatalf("caught by the bald man should be a promotion: %v", over)
	}
	// The over screen reads both of these, and the ending's own words come from
	// the catalogue rather than from this frame.
	if _, ok := over["pay"]; !ok {
		t.Fatalf("an ending with no salary: %v", over)
	}
	if _, ok := over["secs"]; !ok {
		t.Fatalf("an ending with no duration: %v", over)
	}

	// And it reaches Postgres, which is the whole point: the office is in memory
	// and survives nothing, so this row is the only thing that outlives it.
	waitForKarenRow(t, "920007", 1)
	if c := karenCause(t, "920007"); c != gamekaren.CausePromoted {
		t.Fatalf("cause is %q, want %q", c, gamekaren.CausePromoted)
	}
}

func TestKarenRoomIsRefusedWhenNothingListens(t *testing.T) {
	// The room registry is the platform's, and an unregistered name is refused at
	// the handshake rather than opened and ignored: a socket nothing reads spends
	// one of the connections an account is allowed, and the client cannot tell
	// the difference from the inside.
	app, _, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920008", "user")

	_, resp, err := dialKaren(t, srv.URL, cookieHeader(t, cli, srv.URL), "no-such-room")
	if err == nil {
		t.Fatal("a room with no handler was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unknown room, got %v", resp)
	}

	// This harness registers «СИМУЛЯТОР КАРЕНА» and nothing else, so even the
	// yard's room is unknown here — which is the registry doing its job rather
	// than a list of every room that has ever existed.
	if _, resp, err := dialKaren(t, srv.URL, cookieHeader(t, cli, srv.URL), httpapi.DefaultRoom); err == nil {
		t.Fatal("an unregistered room was accepted")
	} else if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %v", resp)
	}
}

func TestKarenTwoPeopleInOneOfficeSeeEachOther(t *testing.T) {
	// CO-OP VISIBILITY, END TO END: two accounts, two real sockets, one shared
	// office, over a real Postgres. Everything below the HTTP layer has been
	// multi-occupant since iteration 1 (ADR-052, ADR-056) — what this proves is
	// that the office says so on the wire.
	app, tick, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "920010", "user")
	cliB := loginAs(t, srv.URL, "920011", "user")
	clock := newKarenClock()

	startShift(t, cliA, srv.URL)
	startShift(t, cliB, srv.URL)

	connA, _, err := dialKaren(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamekaren.Room)
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialKaren(t, srv.URL, cookieHeader(t, cliB, srv.URL), gamekaren.Room)
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)

	ctx := context.Background()
	for _, c := range []*websocket.Conn{connA, connB} {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"karen_hello"}`)); err != nil {
			t.Fatal(err)
		}
	}

	// Each of them has to SEE the other, so wait for a snapshot that carries a
	// peer rather than for the first snapshot of any kind: the two shifts start
	// on different ticks, so an early frame legitimately has an empty office in
	// it. Bounded by time, never by a count of tries.
	peerOf := func(frames <-chan []byte, who string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			f := waitForKarenFrame(t, frames, tick, clock, "karen_snap", 15*time.Second)
			raw, ok := f["pr"].([]any)
			if !ok || len(raw) == 0 {
				continue
			}
			return raw[0].(map[string]any)
		}
		t.Fatalf("%s never saw anybody else in the office", who)
		return nil
	}

	pa, pb := peerOf(framesA, "a"), peerOf(framesB, "b")

	// A HANDLE, NOT AN ACCOUNT (ADR-037). It is twelve base64url characters
	// minted per process, and it must not be either account's id — those are
	// UUIDs, so a peer entry that leaked one would be far longer than this.
	for who, p := range map[string]map[string]any{"a's peer": pa, "b's peer": pb} {
		id, _ := p["i"].(string)
		if len(id) != 12 {
			t.Fatalf("%s is identified by %q, which is not a 12-character handle", who, id)
		}
		if _, ok := p["x"]; !ok {
			t.Fatalf("%s has no position: %v", who, p)
		}
		// No name, no avatar URL, no salary: another Карен's money is his own
		// business and a URL on a frame that repeats ten times a second is what
		// the handle exists to avoid.
		for _, banned := range []string{"name", "display_name", "avatar", "avatar_url", "pay"} {
			if _, ok := p[banned]; ok {
				t.Fatalf("%s carries %q, which must not ride a repeating frame: %v", who, banned, p)
			}
		}
	}
	// And they are not the same person: each sees the OTHER.
	if pa["i"] == pb["i"] {
		t.Fatalf("both frames name the same peer %v — somebody is seeing himself", pa["i"])
	}

	// When one of them walks out, the other's office empties.
	if code := leaveShift(t, cliB, srv.URL); code != http.StatusNoContent {
		t.Fatalf("b leaving: status %d", code)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("a still sees a colleague who walked out")
		}
		f := waitForKarenFrame(t, framesA, tick, clock, "karen_snap", 15*time.Second)
		if raw, ok := f["pr"].([]any); !ok || len(raw) == 0 {
			break
		}
	}
}

func TestKarenTwoShiftsDoNotStartOnTheSameTile(t *testing.T) {
	// The other half of the spawn change. A fixed spawn put a joiner INSIDE
	// whoever was already playing, on the one spot the bald man was walking
	// towards — and made death-and-rejoin a free teleport to the safe end.
	//
	// The unit tests pin the invariants over hundreds of draws; this one proves
	// the drawn position is what actually reaches the wire.
	app, tick, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "920012", "user")
	cliB := loginAs(t, srv.URL, "920013", "user")
	clock := newKarenClock()

	startShift(t, cliA, srv.URL)
	startShift(t, cliB, srv.URL)

	conn, _, err := dialKaren(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamekaren.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"t":"karen_hello"}`)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("никто never appeared in the office")
		}
		f := waitForKarenFrame(t, frames, tick, clock, "karen_snap", 15*time.Second)
		raw, ok := f["pr"].([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		peer := raw[0].(map[string]any)
		mine := [2]float64{f["x"].(float64), f["y"].(float64)}
		theirs := [2]float64{peer["x"].(float64), peer["y"].(float64)}
		// Centimetres on the wire. They have had a moment to move, so this is a
		// floor rather than the exact spawn separation — the claim is only that
		// two people did not start life standing in the same place.
		if mine == theirs {
			t.Fatalf("two shifts started on the same tile: %v", mine)
		}
		return
	}
}

func TestKarenPointingHimAtAColleagueOverTheSocket(t *testing.T) {
	// The verb, end to end: two accounts, two real sockets, one office, and the
	// bald man changing his mind because somebody said so.
	app, tick, _ := buildAppKaren(t, karenVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "920014", "user")
	cliB := loginAs(t, srv.URL, "920015", "user")
	clock := newKarenClock()

	startShift(t, cliA, srv.URL)
	startShift(t, cliB, srv.URL)

	connA, _, err := dialKaren(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamekaren.Room)
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialKaren(t, srv.URL, cookieHeader(t, cliB, srv.URL), gamekaren.Room)
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)

	ctx := context.Background()
	for _, c := range []*websocket.Conn{connA, connB} {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"karen_hello"}`)); err != nil {
			t.Fatal(err)
		}
	}

	// Learn what A calls B — a pseudonym, which is the only name A has for him.
	var target string
	deadline := time.Now().Add(20 * time.Second)
	for target == "" {
		if time.Now().After(deadline) {
			t.Fatal("a never saw b in the office")
		}
		f := waitForKarenFrame(t, framesA, tick, clock, "karen_snap", 15*time.Second)
		if raw, ok := f["pr"].([]any); ok && len(raw) > 0 {
			target, _ = raw[0].(map[string]any)["i"].(string)
		}
	}

	// Fire it, naming him by that handle and nothing else.
	verb := `{"t":"karen_do","v":"redirect","tg":"` + target + `"}`
	if err := connA.Write(ctx, websocket.MessageText, []byte(verb)); err != nil {
		t.Fatal(err)
	}

	// A's own frame shows the cooldown running and the line over his head, which
	// is how any client knows the office accepted it — there is no reply.
	deadline = time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the office never acknowledged the verb")
		}
		f := waitForKarenFrame(t, framesA, tick, clock, "karen_snap", 15*time.Second)
		rc, _ := f["rc"].(float64)
		p, _ := f["p"].(float64)
		if rc > 0 && int(p) == gamekaren.RedirectLine {
			break
		}
	}

	// And his COLLEAGUE sees who did it: the same index, on the peer entry.
	deadline = time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("b never saw what a said about him")
		}
		f := waitForKarenFrame(t, framesB, tick, clock, "karen_snap", 15*time.Second)
		raw, ok := f["pr"].([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		if p, _ := raw[0].(map[string]any)["p"].(float64); int(p) == gamekaren.RedirectLine {
			break
		}
	}
}
