//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/gamefintech"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/coder/websocket"
)

// «СИМУЛЯТОР ФИНТЕХА» end to end: a real HTTP server, a real WebSocket, a real
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

// fintechVK starts the fake VK ID server this file's logins go through, closed on
// cleanup.
//
// The account ids below are NUMERIC because this fake interpolates the code
// straight into a JSON `user_id` field — a code like "fintech-play" produces
// unquoted nonsense and a 502 that looks like a VK outage rather than like a
// test's own mistake.
func fintechVK(t *testing.T) string {
	t.Helper()
	srv := fakeVKDynamic()
	t.Cleanup(srv.Close)
	return srv.URL
}

// fintechClock hands out the timestamps this file's ticks carry.
//
// It advances only when a tick is actually delivered, which is why peek and
// advance are separate: a select's send case evaluates its value even when
// another case wins, so reading the next timestamp inside the select would run
// the office's clock forward every time a frame arrived instead of every time
// the world stepped.
type fintechClock struct {
	base time.Time
	n    int
}

func newFintechClock() *fintechClock { return &fintechClock{base: time.Now()} }

func (c *fintechClock) peek() time.Time {
	return c.base.Add(time.Duration(c.n) * gamefintech.SimStep)
}

func (c *fintechClock) advance() { c.n++ }

// buildAppFintech builds the app with a running hub and «СИМУЛЯТОР ФИНТЕХА» wired
// to it, returning the tick channel that drives the office and the service
// itself.
//
// The tick channel is UNBUFFERED: a send returns only once the simulation has
// taken it, so "the world advanced" is something this file knows rather than
// hopes.
func buildAppFintech(t *testing.T, vkBaseURL string) (http.Handler, chan time.Time, *gamefintech.Service) {
	t.Helper()
	hub := realtime.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-hub.Done()
	})

	svc := gamefintech.NewService(hub, gamefintech.Room, pool, gamefintech.NewPostgresRepository(), newAccountService())
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
		GameFintech: svc,
		Realtime:    hub,
		RealtimeCtx: ctx,
		RealtimeHandlers: map[string]realtime.Handler{
			gamefintech.Room: svc,
		},
	}).Handler()
	return observability.WrapHandler(h, "http.server"), tick, svc
}

// dialFintech opens a socket in this game's room. The room is a query parameter,
// and asking for one nothing listens to is refused at the handshake.
func dialFintech(t *testing.T, appURL, cookie, room string) (*websocket.Conn, *http.Response, error) {
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
	resp, err := cli.Post(base+"/api/game-fintech/shifts", "application/json", nil)
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
	req, _ := http.NewRequest(http.MethodDelete, base+"/api/game-fintech/shifts/current", nil)
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("leave shift: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// fintechStep sends exactly one tick, bounded so a stalled simulation fails the
// test rather than hanging it.
func fintechStep(t *testing.T, tick chan time.Time, clock *fintechClock) {
	t.Helper()
	select {
	case tick <- clock.peek():
		clock.advance()
	case <-time.After(5 * time.Second):
		t.Fatal("the office stopped taking ticks")
	}
}

// fintechLongEnough is how long a shift has to run before the service will write
// it down, plus enough margin that a runner's scheduling jitter cannot put it
// under the line.
func fintechLongEnough() time.Duration {
	return time.Duration(gamefintech.MinShiftSeconds*float64(time.Second)) + 700*time.Millisecond
}

// fintechWork advances the office at real time for d, so a shift genuinely lasts
// that long on the wall clock as well as on the simulated one.
//
// This is the shape MinShiftSeconds forces: the rule is stated in seconds, and
// the only way to be right about it whichever clock the service reads is to make
// the two agree. It also keeps the bald man where he belongs — he closes at
// BossSpeed, so a burst of ticks that took no real time would still bring him
// across the office and end a shift this test wanted to end another way.
func fintechWork(t *testing.T, tick chan time.Time, clock *fintechClock, d time.Duration) {
	t.Helper()
	pace := time.NewTicker(gamefintech.SimStep)
	defer pace.Stop()
	until := time.After(d)
	for {
		select {
		case <-until:
			return
		case <-pace.C:
			fintechStep(t, tick, clock)
		}
	}
}

// fintechAgeWithoutTheChase makes a shift OLD ENOUGH TO RECORD without making it
// long enough for the bald man to end it.
//
// The two clocks come apart here, and that is the whole point. MinShiftSeconds
// is a WALL-CLOCK rule — a shift is worth a row if it lasted that long by
// StartedAt — while the chase advances only on SIMULATED ticks. fintechWork paces
// ticks at real time so the two run together, which was safe while everybody
// spawned 16.5 m from him (3.8 s of head start) and is not now that a spawn is
// DRAWN: the floor is spawnFromBoss, so pumping a full 3.7 s of simulation raced
// him and lost. CI caught it as a 404 on the leave, the shift having already
// ended as `promoted`.
//
// So this spends one simulated second — enough that standing still has earned
// something, which is what the test then asserts — and lets the rest of the wall
// clock pass with the office standing still.
func fintechAgeWithoutTheChase(t *testing.T, tick chan time.Time, clock *fintechClock, d time.Duration) {
	t.Helper()
	started := time.Now()
	for i := 0; i < gamefintech.SimHz; i++ {
		fintechStep(t, tick, clock)
	}
	if rest := d - time.Since(started); rest > 0 {
		time.Sleep(rest)
	}
}

// waitForFintechFrame pumps the office until a frame of the given type arrives.
//
// Bounded by TIME, never by a count of iterations: a bound on how hard it tries
// is not a bound on how long it waits, and on a loaded runner the two are
// nothing alike. Frames are drained continuously while it pumps, because the
// per-connection send buffer is finite and a test that stops reading is a test
// that gets its own socket closed under it.
func waitForFintechFrame(t *testing.T, frames <-chan []byte, tick chan time.Time, clock *fintechClock, want string, within time.Duration) map[string]any {
	t.Helper()
	pace := time.NewTicker(gamefintech.SimStep)
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
			// A short bound rather than the blocking send fintechStep uses: the
			// simulation is briefly busy every tick, and skipping one costs this
			// loop 50 ms where waiting for it would stop frames being drained.
			select {
			case tick <- clock.peek():
				clock.advance()
			case <-time.After(gamefintech.SimStep):
			}
		case <-deadline:
			t.Fatalf("no %s frame arrived", want)
			return nil
		}
	}
}

// fintechRows counts an account's rows in this game's one table.
func fintechRows(t *testing.T, uid string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM game_fintech_shifts WHERE account_id = $1::uuid`,
		accountIDByUID(t, uid)).Scan(&n)
	if err != nil {
		t.Fatalf("count shifts: %v", err)
	}
	return n
}

// fintechCause reads the cause of an account's single written shift.
func fintechCause(t *testing.T, uid string) string {
	t.Helper()
	var cause string
	err := pool.QueryRow(context.Background(),
		`SELECT cause FROM game_fintech_shifts WHERE account_id = $1::uuid ORDER BY created_at DESC LIMIT 1`,
		accountIDByUID(t, uid)).Scan(&cause)
	if err != nil {
		t.Fatalf("read cause: %v", err)
	}
	return cause
}

// waitForFintechRow waits for the writer goroutine to land the row, bounded by a
// deadline. The write is deliberately asynchronous to the simulation — stalling
// a twenty-hertz loop for one summary row is the refused trade — so "it was
// written" is something this file waits for rather than something it can read
// the instant the shift ends.
func waitForFintechRow(t *testing.T, uid string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fintechRows(t, uid) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the shift never reached Postgres: %d rows, want %d", fintechRows(t, uid), want)
}

func TestFintechTheTwoBoardsRankTheSamePeopleDifferently(t *testing.T) {
	// THE POINT OF SCORING TWO DIMENSIONS, and it cannot be shown by playing: a
	// shift's money and its length are correlated when both come out of the same
	// simulation, so proving the boards are genuinely different needs a player who
	// earned a lot quickly and one who earned little slowly.
	//
	// The rows are written straight to Postgres — direct DB setup rather than a
	// flag that makes the game deterministic, which is the project's rule (no
	// test-only machinery in a production path). Everything downstream of the
	// INSERT is real: the DISTINCT ON, both orderings, the name lookup through the
	// account service, and the JSON the splash reads.
	app, _, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()

	rich := loginAs(t, srv.URL, "920010", "user")
	_ = loginAs(t, srv.URL, "920011", "user")
	for _, row := range []struct {
		uid     string
		salary  float64
		seconds float64
	}{
		// A fortune in a hurry, and a pittance over a long afternoon.
		{"920010", 900_000, 30},
		{"920011", 1_000, 900},
	} {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO game_fintech_shifts (id, account_id, cause, salary, seconds)
			 VALUES (gen_random_uuid(), $1::uuid, 'left', $2, $3)`,
			accountIDByUID(t, row.uid), row.salary, row.seconds); err != nil {
			t.Fatalf("seed shift for %s: %v", row.uid, err)
		}
	}

	resp, err := rich.Get(srv.URL + "/api/game-fintech/shifts/top?limit=5")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var board struct {
		Salary []struct {
			Name    string  `json:"name"`
			Salary  float64 `json:"salary"`
			Seconds float64 `json:"seconds"`
		} `json:"salary"`
		Seconds []struct {
			Name    string  `json:"name"`
			Salary  float64 `json:"salary"`
			Seconds float64 `json:"seconds"`
		} `json:"seconds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&board); err != nil {
		t.Fatal(err)
	}
	if len(board.Salary) == 0 || len(board.Seconds) == 0 {
		t.Fatalf("a board came back empty: %+v", board)
	}
	// The man who earned 900 000 in half a minute tops the money board; the man
	// who stood there for a quarter of an hour tops the other one. A single board
	// sorted twice could not produce this.
	if board.Salary[0].Salary != 900_000 {
		t.Fatalf("the money board is led by %+v", board.Salary[0])
	}
	if board.Seconds[0].Seconds != 900 {
		t.Fatalf("the length board is led by %+v", board.Seconds[0])
	}
	if board.Salary[0].Name == board.Seconds[0].Name {
		t.Fatalf("both boards are led by the same person: %+v", board)
	}
	// And a row carries BOTH numbers wherever it appears, so the two boards read
	// as one scoreboard rather than as two lists of unrelated figures.
	if board.Seconds[0].Salary <= 0 || board.Salary[0].Seconds <= 0 {
		t.Fatalf("a board row is missing the other dimension: %+v", board)
	}
	// The limit is honoured on both, since one client parameter now bounds two
	// queries.
	if len(board.Salary) > 5 || len(board.Seconds) > 5 {
		t.Fatalf("?limit=5 returned %d and %d rows", len(board.Salary), len(board.Seconds))
	}
}

func TestFintechAScoreOlderThanAWeekIsOffTheBoardAndStillInYourHistory(t *testing.T) {
	// BOTH HALVES OF THE RULE, and the second one is the reason it is a window
	// rather than a delete: a record stops ranking after gamefintech.BoardWindow,
	// and the row it came from is untouched — «мои смены» still lists it.
	//
	// The ages are written straight into created_at, because the only other way to
	// have an eight-day-old row is to wait eight days. Everything downstream of the
	// INSERT is real: the windowed DISTINCT ON, both orderings, the name lookup and
	// the JSON the splash screen reads.
	app, _, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()

	// THE NUMBERS ARE UNIQUE TO THIS TEST ON PURPOSE. Every test in this file
	// shares one database, so the boards also carry rows seeded by its neighbours
	// — asserting on the length of a board, or on what leads it, would be
	// asserting on the rest of the suite. These three values appear nowhere else,
	// so «is it on the board» and «which of my two rows ranks higher» are both
	// answerable whatever else is in the table.
	const (
		legendarySalary, legendarySeconds = 987_654, 4_321
		vetSalary, vetSeconds             = 1_234, 43
		newcomerSalary, newcomerSeconds   = 2_345, 87
	)
	veteran := loginAs(t, srv.URL, "920020", "user")
	_ = loginAs(t, srv.URL, "920021", "user")
	for _, row := range []struct {
		uid     string
		salary  float64
		seconds float64
		age     string
	}{
		// The veteran's legendary afternoon, a week and a day ago: it would beat
		// everything else here on both numbers if the window were not there.
		{"920020", legendarySalary, legendarySeconds, "8 days"},
		// ...and what he has managed since, which is what he should be ranked on.
		{"920020", vetSalary, vetSeconds, "1 hour"},
		// A newcomer, this afternoon, who therefore ranks above him on both.
		{"920021", newcomerSalary, newcomerSeconds, "2 hours"},
	} {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO game_fintech_shifts (id, account_id, cause, salary, seconds, created_at)
			 VALUES (gen_random_uuid(), $1::uuid, 'left', $2, $3, now() - $4::interval)`,
			accountIDByUID(t, row.uid), row.salary, row.seconds, row.age); err != nil {
			t.Fatalf("seed shift for %s: %v", row.uid, err)
		}
	}

	resp, err := veteran.Get(srv.URL + "/api/game-fintech/shifts/top?limit=50")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var board struct {
		Salary []struct {
			Salary  float64 `json:"salary"`
			Seconds float64 `json:"seconds"`
		} `json:"salary"`
		Seconds []struct {
			Salary  float64 `json:"salary"`
			Seconds float64 `json:"seconds"`
		} `json:"seconds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&board); err != nil {
		t.Fatal(err)
	}
	// rankOf is where a score sits on a board, or -1 when it is not on it at all.
	rankOf := func(rows []struct {
		Salary  float64 `json:"salary"`
		Seconds float64 `json:"seconds"`
	}, salary, seconds float64) int {
		for i, row := range rows {
			if row.Salary == salary && row.Seconds == seconds {
				return i
			}
		}
		return -1
	}
	if at := rankOf(board.Salary, legendarySalary, legendarySeconds); at >= 0 {
		t.Fatalf("the eight-day-old record is still on the money board, at %d: %+v", at, board.Salary)
	}
	if at := rankOf(board.Seconds, legendarySalary, legendarySeconds); at >= 0 {
		t.Fatalf("the eight-day-old record is still on the length board, at %d: %+v", at, board.Seconds)
	}
	// AND THE VETERAN IS STILL ON THE BOARD, ranked on what he has done this week
	// rather than dropped from it. That is the difference between windowing inside
	// the DISTINCT ON and windowing outside it: outside, his best-ever row would be
	// chosen and then discarded for being old, and he would vanish from the board
	// while a worse player stayed.
	vetMoney := rankOf(board.Salary, vetSalary, vetSeconds)
	newMoney := rankOf(board.Salary, newcomerSalary, newcomerSeconds)
	if vetMoney < 0 || newMoney < 0 || newMoney > vetMoney {
		t.Fatalf("the money board is not this week's: veteran at %d, newcomer at %d: %+v",
			vetMoney, newMoney, board.Salary)
	}
	vetLength := rankOf(board.Seconds, vetSalary, vetSeconds)
	newLength := rankOf(board.Seconds, newcomerSalary, newcomerSeconds)
	if vetLength < 0 || newLength < 0 || newLength > vetLength {
		t.Fatalf("the length board is not this week's: veteran at %d, newcomer at %d: %+v",
			vetLength, newLength, board.Seconds)
	}

	// NOTHING WAS DELETED. His own list still has all three of his shifts, the old
	// one included, which is the whole reason this is a read-time window.
	mineResp, err := veteran.Get(srv.URL + "/api/game-fintech/shifts/me?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer mineResp.Body.Close()
	var mine struct {
		Shifts []struct {
			Salary float64 `json:"salary"`
		} `json:"shifts"`
	}
	if err := json.NewDecoder(mineResp.Body).Decode(&mine); err != nil {
		t.Fatal(err)
	}
	if len(mine.Shifts) != 2 {
		t.Fatalf("my shifts returned %d rows, want both of the veteran's: %+v", len(mine.Shifts), mine.Shifts)
	}
	var kept bool
	for _, s := range mine.Shifts {
		if s.Salary == legendarySalary {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("the aged-out shift is gone from my own history: %+v", mine.Shifts)
	}
}

func TestFintechConfigIsServedAndIsTheWholeCatalogue(t *testing.T) {
	app, _, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920001", "user")

	resp, err := cli.Get(srv.URL + "/api/game-fintech/config")
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
	if !seen[gamefintech.CausePromoted] || !seen[gamefintech.CauseLeft] {
		t.Fatalf("the catalogue is missing an ending the code writes: %+v", cfg.Endings)
	}
	if len(cfg.BossLines) == 0 {
		t.Fatal("the bald man has nothing to say")
	}
	if cfg.MaxOccupants <= 0 {
		t.Fatalf("max_occupants is %d", cfg.MaxOccupants)
	}
}

func TestFintechShiftIsCreatedAndResumable(t *testing.T) {
	app, _, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920002", "user")

	first := startShift(t, cli, srv.URL)
	if first["shift_id"] == nil || first["shift_id"] == "" {
		t.Fatalf("no shift id in %v", first)
	}
	// The room travels with the shift so the view never hardcodes it — the name
	// lives in the game's own package and nowhere else.
	if first["room"] != gamefintech.Room {
		t.Fatalf("room is %v, want %q", first["room"], gamefintech.Room)
	}
	// WHO YOU ARE, drawn by the server and named by the catalogue. It rides the
	// shift response rather than a frame because it is constant for the life of a
	// shift, and it is not omitempty: zero is Карен, a real answer, and an absent
	// field would be indistinguishable from him.
	persona, ok := first["persona"].(float64)
	if !ok {
		t.Fatalf("no persona in %v", first)
	}
	if persona < 0 || int(persona) >= len(gamefintech.Personas) {
		t.Fatalf("persona %v is outside the cast of %d", persona, len(gamefintech.Personas))
	}

	// The office is STATIC and already in the catalogue, so a shift start sends
	// no level. This is the load-bearing difference from the shooter, and it is
	// worth pinning: re-sending an office nobody generated would be pure waste.
	if _, ok := first["level"]; ok {
		t.Fatalf("a shift start sent a level: %v", first)
	}

	// A refusal rather than a silent replacement: dropping the occupant would
	// throw away a shift open on the player's other tab.
	resp, err := cli.Post(srv.URL+"/api/game-fintech/shifts", "application/json", nil)
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
	cur, err := cli.Get(srv.URL + "/api/game-fintech/shifts/current")
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
	none, err := other.Get(srv.URL + "/api/game-fintech/shifts/current")
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

func TestFintechWalkingOutWritesTheShift(t *testing.T) {
	// Walking out IS a result here, unlike the shooter's «сдаться»: the point of
	// the game is the money, and money earned by standing still until you got
	// bored counts exactly as much as money earned until he reached you.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920004", "user")
	clock := newFintechClock()

	startShift(t, cli, srv.URL)
	fintechAgeWithoutTheChase(t, tick, clock, fintechLongEnough())

	if code := leaveShift(t, cli, srv.URL); code != http.StatusNoContent {
		t.Fatalf("leave: status %d, want 204", code)
	}
	waitForFintechRow(t, "920004", 1)
	if c := fintechCause(t, "920004"); c != gamefintech.CauseLeft {
		t.Fatalf("cause is %q, want %q", c, gamefintech.CauseLeft)
	}

	// And the player can see it without opening a database, which is what makes
	// this half of the iteration verifiable in production.
	resp, err := cli.Get(srv.URL + "/api/game-fintech/shifts/me?limit=10")
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
	if len(mine.Shifts) != 1 || mine.Shifts[0].Cause != gamefintech.CauseLeft {
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
	plain, err := cli.Get(srv.URL + "/api/game-fintech/shifts/me")
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
	top, err := cli.Get(srv.URL + "/api/game-fintech/shifts/top?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer top.Body.Close()
	// TWO BOARDS IN ONE RESPONSE, because the splash draws them side by side and a
	// screen that needed two requests to render would be the chattiness the
	// client–server rule exists to prevent. The keys are the metrics themselves.
	type boardRow struct {
		Name    string  `json:"name"`
		Cause   string  `json:"cause"`
		Salary  float64 `json:"salary"`
		Seconds float64 `json:"seconds"`
	}
	var board struct {
		Salary  []boardRow `json:"salary"`
		Seconds []boardRow `json:"seconds"`
	}
	if err := json.NewDecoder(top.Body).Decode(&board); err != nil {
		t.Fatal(err)
	}
	for name, rows := range map[string][]boardRow{"salary": board.Salary, "seconds": board.Seconds} {
		found := false
		for _, row := range rows {
			// Every row on both boards carries BOTH numbers, because a board that
			// showed only its own metric would make the two unreadable together.
			if row.Name != "" && row.Salary > 0 && row.Seconds > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("nobody on the %s board has a name and both numbers: %+v", name, rows)
		}
	}
	// And they are SORTED BY THEIR OWN METRIC, which is the only thing that makes
	// them two boards rather than one payload printed twice.
	for i := 1; i < len(board.Salary); i++ {
		if board.Salary[i].Salary > board.Salary[i-1].Salary {
			t.Fatalf("the money board is not ordered by money: %+v", board.Salary)
		}
	}
	for i := 1; i < len(board.Seconds); i++ {
		if board.Seconds[i].Seconds > board.Seconds[i-1].Seconds {
			t.Fatalf("the length board is not ordered by length: %+v", board.Seconds)
		}
	}
}

// firstSpot reads a prop mask off a frame and answers the lowest catalogue spot
// that has one standing on it.
//
// A MASK RATHER THAN AN INDEX, because the office keeps one bottle and one кальян
// per person on the floor — so «which spot» has as many answers as there are
// people, and a walker only needs the nearest one to aim at. Lowest rather than
// nearest keeps the fixture deterministic; a solo test has exactly one bit set
// anyway.
func firstSpot(raw any, spots int) (int, bool) {
	mask, ok := raw.(float64)
	if !ok {
		return 0, false
	}
	for i := 0; i < spots; i++ {
		if int(mask)&(1<<i) != 0 {
			return i, true
		}
	}
	return 0, false
}

func TestFintechTheRouterTakesClaudeOffTheWire(t *testing.T) {
	// «РОУТЕР УПАЛ», END TO END over a real socket. The unit tests drive the office
	// directly; this is the only place that proves the frame actually arrives with
	// Claude MISSING — which is the whole client contract, since an absent `cl` is
	// what tells a browser to stop drawing him.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920020", "user")
	clock := newFintechClock()

	startShift(t, cli, srv.URL)
	conn, _, err := dialFintech(t, srv.URL, cookieHeader(t, cli, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)

	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
		t.Fatal(err)
	}
	// He is on the floor to begin with, or the absence below proves nothing.
	first := waitForFintechFrame(t, frames, tick, clock, "fintech_snap", 15*time.Second)
	if _, ok := first["cl"]; !ok {
		t.Fatalf("Claude was already missing before anything happened: %v", first)
	}

	// NO TARGET ON THE FRAME. There is one Claude and the effect is the whole
	// office's, so the verb carries a name and nothing else.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_do","v":"router"}`)); err != nil {
		t.Fatal(err)
	}

	// The office acknowledges by STATE rather than by a reply, like every verb
	// here: `cl` stops being sent, `ca` says how long for, `rd` disables the button
	// and the caller's own balloon says who did it.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the office never took the router down")
		}
		f := waitForFintechFrame(t, frames, tick, clock, "fintech_snap", 15*time.Second)
		ca, _ := f["ca"].(float64)
		rd, _ := f["rd"].(float64)
		p, _ := f["p"].(float64)
		if ca <= 0 {
			continue
		}
		if _, present := f["cl"]; present {
			t.Fatalf("he is away for %v ms and still on the frame: %v", ca, f)
		}
		if rd <= 0 {
			t.Fatalf("the router is down and the button has no cooldown: %v", f)
		}
		if int(p) != gamefintech.RouterLine {
			t.Fatalf("the caller is saying line %v, want the router's %d", p, gamefintech.RouterLine)
		}
		break
	}
}

func TestFintechAShortShiftIsNotWritten(t *testing.T) {
	// The negative half of the test above. A shift is dropped rather than
	// written below MinShiftSeconds, so opening the game, tapping НАЧАТЬ СМЕНУ
	// and immediately leaving does not put a row nobody meant into the only
	// table this game has.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920005", "user")
	clock := newFintechClock()

	startShift(t, cli, srv.URL)
	if code := leaveShift(t, cli, srv.URL); code != http.StatusNoContent {
		t.Fatalf("leave: status %d, want 204", code)
	}

	// Give the loop and the writer several steps to have written something, if
	// they were going to.
	for i := 0; i < 10; i++ {
		fintechStep(t, tick, clock)
	}
	if n := fintechRows(t, "920005"); n != 0 {
		t.Fatalf("a shift that lasted no time left %d rows behind", n)
	}

	// And leaving again is a 404 rather than a second write.
	if code := leaveShift(t, cli, srv.URL); code != http.StatusNotFound {
		t.Fatalf("second leave: status %d, want 404", code)
	}
}

func TestFintechTheSocketSimulatesAndAnswersWithSnapshots(t *testing.T) {
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920006", "user")
	clock := newFintechClock()

	body := startShift(t, cli, srv.URL)
	conn, _, err := dialFintech(t, srv.URL, cookieHeader(t, cli, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)

	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
		t.Fatal(err)
	}
	ready := waitForFintechFrame(t, frames, tick, clock, "fintech_ready", 15*time.Second)
	if ready["shift_id"] != body["shift_id"] {
		t.Fatalf("attached to %v, started %v", ready["shift_id"], body["shift_id"])
	}
	// AND WHICH TICK THE SHIFT BEGAN ON, which is how the client draws its length
	// without a byte on the snapshot. It is sent once, here, because it is constant
	// for the life of a shift — so a missing field would leave the clock reading
	// 0:00 for the whole of it.
	if _, ok := ready["k0"].(float64); !ok {
		t.Fatalf("the ready frame carries no k0, so the shift has no clock: %v", ready)
	}

	first := waitForFintechFrame(t, frames, tick, clock, "fintech_snap", 15*time.Second)
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
				"q": seq, "dt": gamefintech.SimStep.Seconds(), "mx": 0, "my": 1,
			})
		}
		msg, _ := json.Marshal(map[string]any{"t": "fintech_input", "k": 0, "cmds": cmds})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
		// Several steps per frame, so the whole batch is actually simulated: the
		// time budget only lets a tick spend a tick's worth of it.
		for k := 0; k < perFrame; k++ {
			fintechStep(t, tick, clock)
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
	pace := time.NewTicker(gamefintech.SimStep)
	defer pace.Stop()
	deadline := time.After(5 * time.Second)
	for moved == nil {
		select {
		case raw := <-frames:
			var f map[string]any
			if json.Unmarshal(raw, &f) == nil && f["t"] == "fintech_snap" &&
				(f["x"] != startX || f["y"] != startY) {
				moved = f
			}
		case <-pace.C:
			select {
			case tick <- clock.peek():
				clock.advance()
			case <-time.After(gamefintech.SimStep):
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

func TestFintechResendingUnacknowledgedInputDoesNotWalkYouTwice(t *testing.T) {
	// INPUT REDUNDANCY, END TO END. The browser repeats the tail of whatever the
	// office has not acknowledged in every frame, so one lost packet costs no
	// input at all. That is only free while a repeat is DROPPED — and the office
	// deduplicated against the wrong number, so a repeat of a command that was
	// queued but not yet simulated was applied a second time. The player was
	// dragged forward while walking and kept walking after the stick came up,
	// because the office was still working through movement they asked for once.
	//
	// Driven over a real socket rather than against the office directly, because
	// the shape that produces it is the WIRE's: a frame carries four sub-steps
	// where a tick affords two, so half the queue is always in exactly the state
	// that used to duplicate.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920011", "user")
	clock := newFintechClock()

	startShift(t, cli, srv.URL)
	conn, _, err := dialFintech(t, srv.URL, cookieHeader(t, cli, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)

	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
		t.Fatal(err)
	}
	waitForFintechFrame(t, frames, tick, clock, "fintech_ready", 15*time.Second)
	first := waitForFintechFrame(t, frames, tick, clock, "fintech_snap", 15*time.Second)
	startX, _ := first["x"].(float64)
	startY, _ := first["y"].(float64)

	// THE DIRECTION IS CHOSEN FROM WHERE THE SPAWN ACTUALLY LANDED, and that is
	// not fussiness — it is how this test first failed in CI. A spawn is DRAWN
	// (Office.spawnPoint), so a hardcoded heading walks into a desk on some runs
	// and a wall on others, and a walk cut short reads exactly like the defect
	// under test read backwards: 36 cm where 128 cm was sent. The assertion is
	// about distance, so the path has to be clear for the distance to mean
	// anything.
	const subStep = 0.025
	const steps = 8
	dist := steps * subStep * gamefintech.WalkSpeed
	dirX, dirY := clearHeading(t, startX/100, startY/100, dist)

	send := func(from, to int) {
		cmds := make([]map[string]any, 0, to-from+1)
		for q := from; q <= to; q++ {
			cmds = append(cmds, map[string]any{"q": q, "dt": subStep, "mx": dirX, "my": dirY})
		}
		msg, _ := json.Marshal(map[string]any{"t": "fintech_input", "k": 0, "cmds": cmds})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
	}

	// One frame of four sub-steps, then ONE tick — which affords two of them, so
	// sequences three and four are still queued when the next frame lands.
	send(1, 4)
	fintechStep(t, tick, clock)
	// The next frame exactly as the browser builds it: the unacknowledged tail
	// first, then the fresh sub-steps.
	send(1, 8)

	// Pump until everything is acknowledged, bounded by a DEADLINE and never by a
	// count of attempts. Paced, so the bald man does not cross the office while
	// this waits.
	var done map[string]any
	pace := time.NewTicker(gamefintech.SimStep)
	defer pace.Stop()
	deadline := time.After(10 * time.Second)
	for done == nil {
		select {
		case raw := <-frames:
			var f map[string]any
			if json.Unmarshal(raw, &f) != nil || f["t"] != "fintech_snap" {
				continue
			}
			if ack, _ := f["ack"].(float64); ack >= 8 {
				done = f
			}
		case <-pace.C:
			select {
			case tick <- clock.peek():
				clock.advance()
			case <-time.After(gamefintech.SimStep):
			}
		case <-deadline:
			t.Fatal("the office never acknowledged all eight sub-steps")
		}
	}

	endX, _ := done["x"].(float64)
	endY, _ := done["y"].(float64)
	// Centimetres on the wire; eight sub-steps of walking and not one more.
	got := math.Hypot(endX-startX, endY-startY)
	if want := dist * 100; math.Abs(got-want) > 2 {
		t.Fatalf("walked %.0f cm where %.0f cm was sent — a queued command was applied twice", got, want)
	}
}

// clearHeading is a unit vector from `at` along which `dist` metres of floor are
// free of both the walls and the furniture.
//
// It exists because a spawn is DRAWN: any hardcoded heading is clear from some
// spawns and blocked from others, which makes a distance assertion flaky in a way
// that looks exactly like the bug it is asserting about. Four axis headings, the
// first that is clear — the office is sixteen metres of floor with eight desks in
// it, so from anywhere legal at least one of them is.
//
// Sampled every five centimetres rather than clipped exactly: this is a test
// picking a direction, not the simulation resolving a collision, and a sample
// finer than a tenth of the player's diameter cannot step over a desk.
func clearHeading(t *testing.T, x, y, dist float64) (float64, float64) {
	t.Helper()
	const r = gamefintech.PlayerRadius
	free := func(px, py float64) bool {
		if px < r || py < r || px > gamefintech.OfficeW-r || py > gamefintech.OfficeH-r {
			return false
		}
		for _, d := range gamefintech.Desks {
			if px > d.X-r && px < d.X+d.W+r && py > d.Y-r && py < d.Y+d.H+r {
				return false
			}
		}
		return true
	}
	for _, dir := range [4][2]float64{{0, 1}, {0, -1}, {1, 0}, {-1, 0}} {
		ok := true
		for s := 0.0; s <= dist+1e-9 && ok; s += 0.05 {
			ok = free(x+dir[0]*s, y+dir[1]*s)
		}
		if ok {
			return dir[0], dir[1]
		}
	}
	t.Fatalf("no clear %.2f m walk from %.2f/%.2f in any direction", dist, x, y)
	return 0, 0
}

func TestFintechBeingCaughtWritesTheShift(t *testing.T) {
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
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920007", "user")
	clock := newFintechClock()

	startShift(t, cli, srv.URL)
	conn, _, err := dialFintech(t, srv.URL, cookieHeader(t, cli, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
		t.Fatal(err)
	}
	waitForFintechFrame(t, frames, tick, clock, "fintech_ready", 15*time.Second)

	over := waitForFintechFrame(t, frames, tick, clock, "fintech_over", 60*time.Second)
	if over["cause"] != gamefintech.CausePromoted {
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
	waitForFintechRow(t, "920007", 1)
	if c := fintechCause(t, "920007"); c != gamefintech.CausePromoted {
		t.Fatalf("cause is %q, want %q", c, gamefintech.CausePromoted)
	}
}

func TestFintechRoomIsRefusedWhenNothingListens(t *testing.T) {
	// The room registry is the platform's, and an unregistered name is refused at
	// the handshake rather than opened and ignored: a socket nothing reads spends
	// one of the connections an account is allowed, and the client cannot tell
	// the difference from the inside.
	app, _, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920008", "user")

	_, resp, err := dialFintech(t, srv.URL, cookieHeader(t, cli, srv.URL), "no-such-room")
	if err == nil {
		t.Fatal("a room with no handler was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unknown room, got %v", resp)
	}

	// This harness registers «СИМУЛЯТОР ФИНТЕХА» and nothing else, so even the
	// yard's room is unknown here — which is the registry doing its job rather
	// than a list of every room that has ever existed.
	if _, resp, err := dialFintech(t, srv.URL, cookieHeader(t, cli, srv.URL), httpapi.DefaultRoom); err == nil {
		t.Fatal("an unregistered room was accepted")
	} else if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %v", resp)
	}
}

func TestFintechTwoPeopleInOneOfficeSeeEachOther(t *testing.T) {
	// CO-OP VISIBILITY, END TO END: two accounts, two real sockets, one shared
	// office, over a real Postgres. Everything below the HTTP layer has been
	// multi-occupant since iteration 1 (ADR-052, ADR-056) — what this proves is
	// that the office says so on the wire.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "920010", "user")
	cliB := loginAs(t, srv.URL, "920011", "user")
	clock := newFintechClock()

	startShift(t, cliA, srv.URL)
	startShift(t, cliB, srv.URL)

	connA, _, err := dialFintech(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialFintech(t, srv.URL, cookieHeader(t, cliB, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)

	ctx := context.Background()
	for _, c := range []*websocket.Conn{connA, connB} {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
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
			f := waitForFintechFrame(t, frames, tick, clock, "fintech_snap", 15*time.Second)
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
		f := waitForFintechFrame(t, framesA, tick, clock, "fintech_snap", 15*time.Second)
		if raw, ok := f["pr"].([]any); !ok || len(raw) == 0 {
			break
		}
	}
}

func TestFintechTwoShiftsDoNotStartOnTheSameTile(t *testing.T) {
	// The other half of the spawn change. A fixed spawn put a joiner INSIDE
	// whoever was already playing, on the one spot the bald man was walking
	// towards — and made death-and-rejoin a free teleport to the safe end.
	//
	// The unit tests pin the invariants over hundreds of draws; this one proves
	// the drawn position is what actually reaches the wire.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "920012", "user")
	cliB := loginAs(t, srv.URL, "920013", "user")
	clock := newFintechClock()

	startShift(t, cliA, srv.URL)
	startShift(t, cliB, srv.URL)

	conn, _, err := dialFintech(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("никто never appeared in the office")
		}
		f := waitForFintechFrame(t, frames, tick, clock, "fintech_snap", 15*time.Second)
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

func TestFintechPointingHimAtAColleagueOverTheSocket(t *testing.T) {
	// The verb, end to end: two accounts, two real sockets, one office, and the
	// bald man changing his mind because somebody said so.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "920014", "user")
	cliB := loginAs(t, srv.URL, "920015", "user")
	clock := newFintechClock()

	startShift(t, cliA, srv.URL)
	startShift(t, cliB, srv.URL)

	connA, _, err := dialFintech(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialFintech(t, srv.URL, cookieHeader(t, cliB, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)

	ctx := context.Background()
	for _, c := range []*websocket.Conn{connA, connB} {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
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
		f := waitForFintechFrame(t, framesA, tick, clock, "fintech_snap", 15*time.Second)
		if raw, ok := f["pr"].([]any); ok && len(raw) > 0 {
			target, _ = raw[0].(map[string]any)["i"].(string)
		}
	}

	// Fire it, naming him by that handle and nothing else.
	verb := `{"t":"fintech_do","v":"redirect","tg":"` + target + `"}`
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
		f := waitForFintechFrame(t, framesA, tick, clock, "fintech_snap", 15*time.Second)
		rc, _ := f["rc"].(float64)
		p, _ := f["p"].(float64)
		if rc > 0 && int(p) == gamefintech.RedirectLine {
			break
		}
	}

	// And his COLLEAGUE sees who did it: the same index, on the peer entry.
	deadline = time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("b never saw what a said about him")
		}
		f := waitForFintechFrame(t, framesB, tick, clock, "fintech_snap", 15*time.Second)
		raw, ok := f["pr"].([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		if p, _ := raw[0].(map[string]any)["p"].(float64); int(p) == gamefintech.RedirectLine {
			break
		}
	}
}

func TestFintechBuyingHimARoundIsVisibleToTheWholeOffice(t *testing.T) {
	// «Набухать лысого», end to end — and the claim that matters is the last
	// one: being drunk is a fact about the OFFICE, not about the screen of
	// whoever bought the round. One Карен walks to the bottle and BOTH of them
	// watch him wobble.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "920016", "user")
	cliB := loginAs(t, srv.URL, "920017", "user")
	clock := newFintechClock()

	startShift(t, cliA, srv.URL)
	startShift(t, cliB, srv.URL)

	connA, _, err := dialFintech(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialFintech(t, srv.URL, cookieHeader(t, cliB, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)

	ctx := context.Background()
	for _, c := range []*websocket.Conn{connA, connB} {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
			t.Fatal(err)
		}
	}

	// Walk A at the bottle. Batched exactly as the browser batches — one frame
	// per sub-step trips the socket's rate limiter, which is the limiter doing
	// its job rather than something to ask an exemption from.
	var seq int
	walk := func(mx, my float64) {
		cmds := make([]map[string]any, 0, 4)
		for j := 0; j < 4; j++ {
			seq++
			cmds = append(cmds, map[string]any{
				"q": seq, "dt": gamefintech.SimStep.Seconds(), "mx": mx, "my": my,
			})
		}
		raw, _ := json.Marshal(map[string]any{"t": "fintech_input", "cmds": cmds})
		if err := connA.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}

	// Steer towards the bottle from wherever the draw put him, re-reading his
	// position as he goes. Bounded by TIME, never by a count of attempts.
	// EVERY OTHER SNAPSHOT, not every one. The socket allows ten frames a second
	// and snapshots arrive at ten a second, so steering on each of them sits
	// exactly on the limiter — which then closes the connection, and the failure
	// presents as "no fintech_snap frame arrived" rather than as a rate limit.
	// Steering at half rate is also what a browser does: it samples faster than
	// it sends and packs the sub-steps into one frame.
	deadline := time.Now().Add(25 * time.Second)
	drunk, n := false, 0
	for !drunk && time.Now().Before(deadline) {
		f := waitForFintechFrame(t, framesA, tick, clock, "fintech_snap", 15*time.Second)
		x, _ := f["x"].(float64)
		y, _ := f["y"].(float64)
		// Where a bottle is NOW: they move, and the frame names WHICH of the
		// catalogue's spots have one as a bit per spot rather than as a position.
		spot := gamefintech.BottleSpots[0]
		if i, ok := firstSpot(f["bs"], len(gamefintech.BottleSpots)); ok {
			spot = gamefintech.BottleSpots[i]
		}
		dx := spot.X - x/100
		dy := spot.Y - y/100
		d := math.Hypot(dx, dy)
		n++
		if d > 1e-6 && n%2 == 0 {
			walk(dx/d, dy/d)
		}
		if b, ok := f["b"].(map[string]any); ok {
			if dv, _ := b["d"].(float64); dv > 0 {
				drunk = true
			}
		}
	}
	if !drunk {
		t.Fatal("walking onto the bottle never got him drunk")
	}

	// AND HIS COLLEAGUE SEES IT, without having gone anywhere near the bottle.
	deadline = time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("b never saw the bald man wobble")
		}
		f := waitForFintechFrame(t, framesB, tick, clock, "fintech_snap", 15*time.Second)
		if b, ok := f["b"].(map[string]any); ok {
			if dv, _ := b["d"].(float64); dv > 0 {
				break
			}
		}
	}
}

func TestFintechWalkingToTheHookahPutsACloudOnTheWire(t *testing.T) {
	// «Кальян» end to end: a real HTTP shift, a real socket, the real 20 Hz tick,
	// and a player who actually walks there.
	//
	// DELIBERATELY SOLO, and the reason is the mechanic working rather than a
	// limitation. A second idle client would be caught while the first was still
	// walking — precisely BECAUSE the first goes untouchable, which hands the лысый
	// the only other target in the room. That the whole office can see a colleague's
	// cloud is pinned where it can be arranged exactly (TestAColleagueSeesTheCloudToo
	// on the office, and the layout suite on the peer's figure); what only this suite
	// can prove is that walking onto the thing over a real socket puts `iv` on a real
	// frame.
	app, tick, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920020", "user")
	clock := newFintechClock()

	startShift(t, cli, srv.URL)

	conn, _, err := dialFintech(t, srv.URL, cookieHeader(t, cli, srv.URL), gamefintech.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)

	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"fintech_hello"}`)); err != nil {
		t.Fatal(err)
	}

	var seq int
	walk := func(mx, my float64) {
		cmds := make([]map[string]any, 0, 4)
		for j := 0; j < 4; j++ {
			seq++
			cmds = append(cmds, map[string]any{
				"q": seq, "dt": gamefintech.SimStep.Seconds(), "mx": mx, "my": my,
			})
		}
		raw, _ := json.Marshal(map[string]any{"t": "fintech_input", "cmds": cmds})
		if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}

	// Steered on TIME rather than on a count of attempts, and at half the snapshot
	// rate — steering on every frame sits exactly on the socket's ten-a-second
	// limiter, which then closes the connection and presents as "no frame arrived".
	deadline := time.Now().Add(25 * time.Second)
	clouded, n := false, 0
	for !clouded && time.Now().Before(deadline) {
		f := waitForFintechFrame(t, frames, tick, clock, "fintech_snap", 15*time.Second)
		x, _ := f["x"].(float64)
		y, _ := f["y"].(float64)
		// Where one is NOW — the bottle's arrangement exactly.
		spot := gamefintech.HookahSpots[0]
		if i, ok := firstSpot(f["hs"], len(gamefintech.HookahSpots)); ok {
			spot = gamefintech.HookahSpots[i]
		}
		dx := spot.X - x/100
		dy := spot.Y - y/100
		d := math.Hypot(dx, dy)
		n++
		if d > 1e-6 && n%2 == 0 {
			walk(dx/d, dy/d)
		}
		if iv, ok := f["iv"].(float64); ok && iv > 0 {
			clouded = true
		}
	}
	if !clouded {
		t.Fatal("walking onto the кальян never put a cloud on the wire")
	}
}

func TestFintechEveryShiftIsSomebodyElse(t *testing.T) {
	// «Make each player receive random Карен / Андрюха / Саня / Даша, not fixed
	// assignment.» The draw always existed, and nothing end-to-end asserted that it
	// reached the client — which mattered, because for one deploy the served
	// `personas` array was nil and every shift therefore looked identical whatever
	// the server had drawn.
	//
	// Over real HTTP, through the real service, and asserted on DISTINCT VALUES rather
	// than on any single one: two fair draws agree one time in four, so a single
	// comparison would be flaky by construction.
	app, _, _ := buildAppFintech(t, fintechVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "920030", "user")

	cast := len(gamefintech.Personas)
	seen := map[int]bool{}
	// Enough shifts that four fair draws are overwhelmingly likely to show at least
	// two distinct values: the chance of forty draws all agreeing is 4^-39.
	for i := 0; i < 40 && len(seen) < cast; i++ {
		got := startShift(t, cli, srv.URL)
		persona, ok := got["persona"].(float64)
		if !ok {
			t.Fatalf("shift %d carried no persona: %v", i, got)
		}
		if persona < 0 || int(persona) >= cast {
			t.Fatalf("shift %d drew persona %v, outside a cast of %d", i, persona, cast)
		}
		seen[int(persona)] = true

		req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/game-fintech/shifts/current", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if len(seen) < 2 {
		t.Fatalf("forty shifts were all the same person: %v — the draw is not reaching the client", seen)
	}
	t.Logf("forty shifts drew %d of %d personas: %v", len(seen), cast, seen)
}
