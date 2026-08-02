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
	// Bounded by TIME, not by a count of iterations.
	//
	// It used to give up after four hundred loops, which is a bound on how hard
	// it tries rather than on how long it waits — and the two are not the same
	// thing on a fast machine. A hello is answered on the connection's own read
	// pump, so the reply is asynchronous to this loop; on a CI runner four
	// hundred tick sends complete in milliseconds, long before the socket has
	// round-tripped, and the test failed with "no frame arrived" while the
	// server was working perfectly. It passed locally because a slower machine
	// happened to interleave differently, which is the worst way for a test to
	// be wrong.
	deadline := time.After(10 * time.Second)
	for i := 0; ; i++ {
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
			return nil
		}
	}
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
			// or below the highest it has ACCEPTED — which includes the ones
			// still waiting in its queue, and not only the ones it has folded
			// in.
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

func TestVanyadumRedundantInputBuysInsuranceAndNotDistance(t *testing.T) {
	// A player walks exactly as far as he asked to, over a real socket, while
	// resending his unacknowledged commands the way the browser does.
	//
	// THIS IS THE DEFECT «СИМУЛЯТОР ФИНТЕХА» FOUND, in the game that shipped it
	// first. A client repeats the tail of everything it has not seen
	// acknowledged so that one lost packet costs no input, and that is only free
	// while the arena drops the repeats. Dropping them by the last APPLIED
	// sequence drops the repeats of commands already stepped and accepts the
	// repeats of commands still WAITING — so the redundancy window buys
	// simulation instead of insurance, and the player is dragged forward while
	// walking and keeps walking after he lets go.
	//
	// The arena's own tests pin the queue arithmetic. This one pins that nothing
	// between the browser and that queue undoes it: the real frame, the real
	// per-frame cap in parseInput, the real socket, the real service loop.
	app, tick, svc := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910008", "user")

	startRun(t, cli, srv.URL)
	conn, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	wire := newDumWire(conn, readFrames(t, conn))

	wire.attach(t)

	// Where he starts and how much room he has, taken from the level rather than
	// from a snapshot. A level is a pure function of its seed and is never
	// written to after it is generated, so reading one while the simulation runs
	// is safe in a way that reading a player's position would not be — and the
	// walk has to be measured from a position no rounding has touched.
	accountID, err := uuid.Parse(accountIDByUID(t, "910008"))
	if err != nil {
		t.Fatal(err)
	}
	arena, ok := svc.CurrentRun(accountID)
	if !ok {
		t.Fatal("no arena for the run that was just started")
	}
	spawn := arena.Level.Spawn
	room := arena.Level.Sectors[arena.Level.SpawnSector]

	// One send window's worth of sampling, at the rates the catalogue publishes:
	// MaxCommandsPerFrame sub-steps of 1/(InputHz*MaxCommandsPerFrame) seconds
	// each, which is the client's whole output for a tenth of a second.
	const subStep = 1.0 / (gamevanyadum.InputHz * gamevanyadum.MaxCommandsPerFrame)
	// Four windows: enough that three of them land on a queue that is behind,
	// and few enough to stay inside the socket's own budget — each window costs
	// two messages with its delivery barrier, against a burst of twenty at ten a
	// second (internal/realtime/conn.go). A much longer drive would have to pace
	// its sends rather than fire them as fast as this one does.
	const windows = 4
	const eastward = math.Pi / 2 // yaw zero looks along +Y, so this walks along +X
	total := int64(windows * gamevanyadum.MaxCommandsPerFrame)
	asked := float64(total) * subStep * gamevanyadum.WalkSpeed

	// He walks in a straight line inside the room he spawned in, and the walk is
	// short enough that no wall can shorten it: the spawn is the room's centre,
	// every room is at least RoomMin across, and the generator never puts a
	// pickup in the spawn room, so nothing here can end the run underneath the
	// assertion either. Checked rather than assumed, because retuning RoomMin
	// would otherwise turn this test into a slow puzzle about collision.
	if clear := (room.MaxX-room.MinX)/2 - gamevanyadum.PlayerRadius; clear <= asked {
		t.Fatalf("the spawn room leaves %.2f m of clear floor eastward and this walk asks for %.2f m", clear, asked)
	}

	// THE CADENCE IS THE POINT, and it is expressed in ticks rather than in wall
	// time because the tick is injected: a send window is a tenth of a second,
	// which is two of the simulation's twenty steps a second.
	//
	// The first window gets ONE tick instead of two — a single late tick, the
	// cheapest thing a mobile connection produces. At the ideal cadence the
	// client's demand and the budget's accrual are exactly equal, so the queue
	// is empty every time a frame lands and a duplicate has nothing to duplicate;
	// one late tick puts the queue two commands behind, and because demand
	// equals accrual nothing ever brings it back. Every window after the first
	// therefore lands on a queue with something in it, which is the state the
	// redundancy rule is actually judged in.
	//
	// Each window is a round trip rather than a write: the frame is delivered,
	// then the world moves, then the client reads what came back before composing
	// the next one — which is the order the browser's own loop runs in, and the
	// only order in which the client's redundancy tail means anything.
	for w := 0; w < windows; w++ {
		wire.send(t, gamevanyadum.MaxCommandsPerFrame, dumCommand{Dt: subStep, MY: 1, Yaw: eastward})
		wire.attach(t) // the barrier: the frame is in the arena before a tick fires
		ticks := 2
		if w == 0 {
			ticks = 1
		}
		for i := 0; i < ticks; i++ {
			wire.step(t, tick)
		}
		wire.draw(t)
	}

	// The drive granted 2*windows-1 ticks against windows*100 ms of input, so the
	// arena cannot have caught up here — and if a retune ever makes it catch up,
	// every frame lands on an empty queue and this test quietly stops testing
	// anything.
	if wire.ack >= total {
		t.Fatalf("the arena acknowledged all %d commands during the walk, so no frame ever landed on a queue with anything in it", total)
	}

	// Now let it finish what it was asked for.
	acked := wire.pumpUntil(t, tick, "every command acknowledged",
		func(s dumSnapshot) bool { return s.Ack >= total })

	// And then keep ticking with nothing left to send, which is what letting go
	// of the controls looks like from here. The queue holds at most maxPending
	// commands of subStep each — the expression is arena.go's, mirrored because
	// the constant is unexported — so this many ticks empties whatever the
	// acknowledgement did not account for. Arithmetic on a bounded queue rather
	// than a wait on anything that could be slow.
	const queueCap = 4 * (gamevanyadum.MaxCommandsPerFrame + gamevanyadum.RedundantCommands)
	settleTicks := int64(math.Ceil(queueCap * subStep * gamevanyadum.SimHz))
	settled := wire.pumpUntil(t, tick, "the walk to settle",
		func(s dumSnapshot) bool { return s.Tick >= acked.Tick+settleTicks })

	// THE ACK IS A PROMISE THAT THERE IS NOTHING LEFT. A client drops everything
	// at or below it and stops predicting it, so movement an arena produces after
	// acknowledging the input that caused it is movement the client has no record
	// of and cannot reconcile against — it can only be corrected to, as a jump.
	if drag := math.Hypot(float64(settled.X-acked.X), float64(settled.Y-acked.Y)) / 100; drag > 0.02 {
		t.Fatalf("all %d commands were acknowledged and he then walked another %.2f m", total, drag)
	}

	travelled := math.Hypot(float64(settled.X)/100-spawn.X, float64(settled.Y)/100-spawn.Y)
	// Two centimetres is the wire's own resolution rather than a tolerance on the
	// simulation — a snapshot carries positions as whole centimetres — and the
	// defect this rules out is not subtle at that scale: these same four windows
	// walked 3.25 m where 2.00 m was asked for, an extra metre and a quarter
	// nobody pressed anything for.
	if math.Abs(travelled-asked) > 0.02 {
		t.Fatalf("walked %.2f m where %d sub-steps asked for %.2f m", travelled, total, asked)
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

// dumCommand is one sub-step on the wire, field for field as
// web/src/lib/vanyadumInput.ts puts it there.
//
// Written out rather than borrowed from the game's package, because the wire
// type there is unexported and deliberately so — it is the wire's shape, not the
// simulation's. The cost of the copy is that a field the wire grows later will
// not appear here, and this test will go on sending the frame an older client
// sends. That is tolerable exactly because an older client's frame is a shape
// the server has to keep accepting anyway.
type dumCommand struct {
	Seq   int64   `json:"q"`
	Dt    float64 `json:"dt"`
	MX    float64 `json:"mx"`
	MY    float64 `json:"my"`
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
}

// dumInput is one client→server input frame.
type dumInput struct {
	T    string       `json:"t"`
	Seen int64        `json:"k"`
	Cmds []dumCommand `json:"cmds"`
}

// dumSnapshot is the part of a snapshot a driving test reads. Typed rather than
// the map[string]any the assertions above use, because every field of it is a
// number this one does arithmetic on.
type dumSnapshot struct {
	T    string `json:"t"`
	Tick int64  `json:"k"`
	Ack  int64  `json:"ack"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

// dumWire is the browser's send loop, reduced to the part the arena can tell
// apart: a monotonic per-command sequence, the commands not yet acknowledged,
// and the last snapshot drawn. Everything the real client additionally does with
// a frame — prediction, replay, interpolation — is client-side and invisible
// from the server's end of the socket, so reproducing it here would only be
// reproducing a second implementation of it.
type dumWire struct {
	conn   *websocket.Conn
	frames <-chan []byte

	// now is the clock handed to the simulation loop, advanced by one SimStep
	// per tick because that is what the production ticker does. ticks counts
	// them, and is therefore also the arena's own tick number: nothing else in
	// the test fires one.
	now   time.Time
	ticks int64

	seq     int64
	unacked []dumCommand
	// ack is the highest acknowledgement any snapshot has carried, and seen is
	// the tick of the last one — the number the server derives round trip from.
	ack  int64
	seen int64
}

func newDumWire(conn *websocket.Conn, frames <-chan []byte) *dumWire {
	return &dumWire{conn: conn, frames: frames, now: time.Unix(0, 0)}
}

// send writes one input frame: n fresh sub-steps of proto, preceded by the tail
// of everything still unacknowledged, which is buildInputFrame's shape exactly.
//
// The tail is capped at RedundantCommands and the fresh commands are never in
// it, because parseInput keeps the FIRST MaxCommandsPerFrame+RedundantCommands
// of a frame and drops the rest — so a client that sent a longer tail would have
// its own new input truncated away, which is a different bug wearing this one's
// symptoms.
func (w *dumWire) send(t *testing.T, n int, proto dumCommand) {
	t.Helper()
	fresh := make([]dumCommand, 0, n)
	for i := 0; i < n; i++ {
		w.seq++
		c := proto
		c.Seq = w.seq
		fresh = append(fresh, c)
	}
	tail := w.unacked
	if len(tail) > gamevanyadum.RedundantCommands {
		tail = tail[len(tail)-gamevanyadum.RedundantCommands:]
	}
	msg, err := json.Marshal(dumInput{
		T:    gamevanyadum.TypeInput,
		Seen: w.seen,
		Cmds: append(append([]dumCommand{}, tail...), fresh...),
	})
	if err != nil {
		t.Fatalf("marshal input frame: %v", err)
	}
	if err := w.conn.Write(context.Background(), websocket.MessageText, msg); err != nil {
		t.Fatalf("send input frame: %v", err)
	}
	w.unacked = append(w.unacked, fresh...)
}

// step fires one simulation tick.
func (w *dumWire) step(t *testing.T, tick chan time.Time) {
	t.Helper()
	select {
	case tick <- w.now:
		w.now = w.now.Add(gamevanyadum.SimStep)
		w.ticks++
	case <-time.After(10 * time.Second):
		t.Fatal("the simulation loop stopped taking ticks")
	}
}

// attach sends a hello and waits for the ready it is answered with, folding in
// any snapshot that arrives on the way.
//
// It is the handshake, and it is ALSO this file's delivery barrier. One
// connection's messages are read in order on one read pump, so a ready proves
// that everything written before the hello has already reached the arena. Without
// it the gap between Write returning and the read pump enqueueing is settled by
// the goroutine scheduler, and a test that fires a tick into that gap measures a
// cadence it did not choose — which is how a phase-sensitive test comes to pass
// on one machine and fail on another.
//
// No tick is fired while waiting, deliberately. An idle tick is not free to the
// occupant it ticks: each one banks another SimStep of unspent time budget, up
// to TimeBudgetCap's half second, and an occupant holding half a second of credit
// swallows a whole backlog in a single step — which is precisely the condition
// the walk above is built to create.
func (w *dumWire) attach(t *testing.T) {
	t.Helper()
	if err := w.conn.Write(context.Background(), websocket.MessageText,
		[]byte(`{"t":"`+gamevanyadum.TypeHello+`"}`)); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw, ok := <-w.frames:
			if !ok {
				t.Fatal("the socket closed before the hello was answered")
			}
			if _, isSnap := w.apply(raw); isSnap {
				continue
			}
			var f struct {
				T string `json:"t"`
			}
			if json.Unmarshal(raw, &f) == nil && f.T == gamevanyadum.TypeReady {
				return
			}
		case <-deadline:
			t.Fatal("no ready frame arrived")
		}
	}
}

// draw folds in snapshots until one describes the last tick this test fired, and
// returns it — so the acknowledgement the next frame is composed against is as
// current as the world it is answering. That is what the browser has, and it is
// what makes the redundancy tail this test sends the tail a browser would.
//
// It waits on a tick already fired and never fires one itself; see attach on why
// an idle tick is not free.
func (w *dumWire) draw(t *testing.T) dumSnapshot {
	t.Helper()
	want := w.ticks
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw, ok := <-w.frames:
			if !ok {
				t.Fatal("the socket closed mid-walk")
			}
			if s, isSnap := w.apply(raw); isSnap && s.Tick >= want {
				return s
			}
		case <-deadline:
			t.Fatalf("no snapshot for tick %d arrived", want)
		}
	}
}

// apply reads one delivered frame, reporting the snapshot it was — anything
// else on this socket is a ready or an over, and neither says anything about
// what has been simulated.
func (w *dumWire) apply(raw []byte) (dumSnapshot, bool) {
	var s dumSnapshot
	if json.Unmarshal(raw, &s) != nil || s.T != gamevanyadum.TypeSnapshot {
		return dumSnapshot{}, false
	}
	w.seen = s.Tick
	if s.Ack > w.ack {
		w.ack = s.Ack
	}
	kept := w.unacked[:0]
	for _, c := range w.unacked {
		if c.Seq > w.ack {
			kept = append(kept, c)
		}
	}
	w.unacked = kept
	return s, true
}

// pumpUntil advances the simulation, ONE TICK PER ROUND TRIP, until a snapshot
// satisfies done — and returns that snapshot. why names what was being waited
// for, so a timeout says which condition never arrived rather than merely that
// something did not happen.
//
// The one-at-a-time part is not tidiness. A loop that offers the tick channel and
// the frame channel to the same select fires as many ticks as the scheduler will
// accept, which on a busy machine is hundreds before the first snapshot is read —
// and every tick publishes a frame, so the connection's send buffer overflows and
// the hub evicts the socket as a slow consumer (internal/realtime/hub.go). It
// presents as a socket that closes mid-test on a loaded runner and nowhere else.
//
// Everything an assertion needs is read from one frame, deliberately: asking for
// the acknowledgement and then asking again for the position is two round trips
// through a loop that moves twenty times a second, and the answer to the second
// question describes a different world from the answer to the first.
//
// Bounded by a DEADLINE and never by a count of ticks: how long a tick takes is a
// property of the machine the suite is running on, so a count that is generous
// here runs out mid-convergence on a loaded runner and reports the wrong reason.
func (w *dumWire) pumpUntil(t *testing.T, tick chan time.Time, why string, done func(dumSnapshot) bool) dumSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		w.step(t, tick)
		if s := w.draw(t); done(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s and it never came; %d commands acknowledged", why, w.ack)
		}
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
