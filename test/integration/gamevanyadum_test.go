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
)

// «ВАНЯДУМ» end to end: a real HTTP server, real WebSockets, a real PostgreSQL,
// and the game's own simulation loop driven by a channel this test fires.
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
//
// The tick channel is UNBUFFERED: a send returns only once the simulation has
// taken it, so "the world advanced" is something this file knows rather than
// hopes.
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

// fetchWorld reads the заброшка over HTTP: which building it is and the whole
// geometry to build meshes from. It is the only place the level is ever sent.
func fetchWorld(t *testing.T, cli *http.Client, base string) (string, *gamevanyadum.Level) {
	t.Helper()
	resp, err := cli.Get(base + "/api/game-vanyadum/world")
	if err != nil {
		t.Fatalf("get world: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get world: status %d", resp.StatusCode)
	}
	var out struct {
		WorldID string              `json:"world_id"`
		Seed    int64               `json:"seed"`
		Level   *gamevanyadum.Level `json:"level"`
		Room    string              `json:"room"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode world: %v", err)
	}
	if out.Level == nil {
		t.Fatal("the world came back without a level in it")
	}
	if out.Seed != out.Level.Seed {
		t.Fatalf("the response says seed %d and the level says %d", out.Seed, out.Level.Seed)
	}
	if out.Room != gamevanyadum.Room {
		t.Fatalf("the world names room %q", out.Room)
	}
	return out.WorldID, out.Level
}

// dumClock is the instant the tick carries, shared by every socket in a test
// because the tick channel is.
//
// BASED AT time.Now() AND NEVER AT A FIXED EPOCH: the hello is the join and it
// stamps the occupant's arrival from the wall clock, exactly as production does
// where the injected tick IS a time.Ticker. A clock starting in 1970 would give
// every occupant a visit that began fifty years after it ended.
type dumClock struct {
	now   time.Time
	ticks int64
}

func newDumClock() *dumClock { return &dumClock{now: time.Now()} }

// step fires one tick and advances the clock by one simulation step, which is
// what the production ticker does.
func (c *dumClock) step(t *testing.T, tick chan time.Time) {
	t.Helper()
	select {
	case tick <- c.now:
		c.now = c.now.Add(gamevanyadum.SimStep)
		c.ticks++
	case <-time.After(10 * time.Second):
		t.Fatal("the simulation loop stopped taking ticks")
	}
}

// jump moves the clock forward without ticking, which is how a test reaches the
// far side of the abandon grace without waiting two real minutes for it.
func (c *dumClock) jump(d time.Duration) { c.now = c.now.Add(d) }

// waitForFrame pumps the simulation until a frame of the given type arrives.
// It advances the world rather than waiting for it, which is what keeps this
// file free of sleeps.
func waitForFrame(t *testing.T, frames <-chan []byte, tick chan time.Time, clk *dumClock, want string) map[string]any {
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
	for {
		at := clk.now
		select {
		case raw := <-frames:
			var f map[string]any
			if err := json.Unmarshal(raw, &f); err != nil {
				continue
			}
			if f["t"] == want {
				return f
			}
		case tick <- at:
			clk.now = clk.now.Add(gamevanyadum.SimStep)
			clk.ticks++
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
		World    map[string]float64 `json:"world"`
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
	// And the building's own two rules, both of which a player has to know before
	// walking in: how many people fit, and how long a thing takes to come back.
	if cfg.World["max_occupants"] != gamevanyadum.MaxOccupants {
		t.Fatalf("the catalogue says %v people fit, the building holds %d",
			cfg.World["max_occupants"], gamevanyadum.MaxOccupants)
	}
	if cfg.World["respawn_seconds"] != gamevanyadum.PickupRespawn.Seconds() {
		t.Fatalf("the catalogue says things come back after %vs, the world says %v",
			cfg.World["respawn_seconds"], gamevanyadum.PickupRespawn)
	}
}

func TestVanyadumEverybodyIsSentTheSameBuilding(t *testing.T) {
	// One заброшка for the whole process. Two people fetching it get the same
	// world id and the same geometry, which is what makes "we are in this
	// together" true rather than a claim.
	app, _, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "910002", "user")
	cliB := loginAs(t, srv.URL, "910003", "user")

	idA, levelA := fetchWorld(t, cliA, srv.URL)
	idB, levelB := fetchWorld(t, cliB, srv.URL)

	if idA != idB {
		t.Fatalf("two people were sent two buildings: %s and %s", idA, idB)
	}
	if levelA.Seed != levelB.Seed {
		t.Fatalf("same world id, different seeds: %d and %d", levelA.Seed, levelB.Seed)
	}
	// The level is sent ONCE, here, and never on a snapshot — which is the
	// single most expensive mistake available in this game.
	if len(levelA.Sectors) < 2 {
		t.Fatalf("a заброшка with %d rooms is not one", len(levelA.Sectors))
	}
	if len(levelA.Walls) == 0 || len(levelA.Pickups) == 0 {
		t.Fatalf("the level has %d walls and %d pickups", len(levelA.Walls), len(levelA.Pickups))
	}
}

func TestVanyadumTheSocketSimulatesAndAnswersWithSnapshots(t *testing.T) {
	app, tick, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910005", "user")
	clk := newDumClock()

	worldID, _ := fetchWorld(t, cli, srv.URL)
	conn, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)

	ctx := context.Background()
	// THE HELLO IS THE JOIN. There is no start endpoint: the room already carries
	// an authenticated account, so being in the room is being in the building.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"vanyadum_hello"}`)); err != nil {
		t.Fatal(err)
	}
	ready := waitForFrame(t, frames, tick, clk, "vanyadum_ready")
	if ready["world_id"] != worldID {
		t.Fatalf("let into %v, fetched the geometry of %v", ready["world_id"], worldID)
	}

	first := waitForFrame(t, frames, tick, clk, "vanyadum_snap")
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
			clk.step(t, tick)
		}
	}

	// Read PAST the snapshots already queued: the frame channel is buffered, so
	// the next one out of it describes a tick from before the walking started
	// and would compare equal for reasons that have nothing to do with the
	// simulation. Keep pumping until the position actually differs.
	var moved map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for moved == nil && time.Now().Before(deadline) {
		at := clk.now
		select {
		case raw := <-frames:
			var f map[string]any
			if json.Unmarshal(raw, &f) == nil && f["t"] == "vanyadum_snap" &&
				(f["x"] != startX || f["y"] != startY) {
				moved = f
			}
		case tick <- at:
			clk.now = clk.now.Add(gamevanyadum.SimStep)
			clk.ticks++
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
	if k, _ := moved["k"].(float64); k < 1 {
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
	// while the world drops the repeats. Dropping them by the last APPLIED
	// sequence drops the repeats of commands already stepped and accepts the
	// repeats of commands still WAITING — so the redundancy window buys
	// simulation instead of insurance, and the player is dragged forward while
	// walking and keeps walking after he lets go.
	//
	// The world's own tests pin the queue arithmetic. This one pins that nothing
	// between the browser and that queue undoes it: the real frame, the real
	// per-frame cap in parseInput, the real socket, the real service loop.
	app, tick, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910008", "user")
	clk := newDumClock()

	// Where he starts and how much room he has, taken from the level rather than
	// from a snapshot. A level is a pure function of its seed and is never
	// written to after it is generated, so this is the same geometry the
	// simulation is using — and the walk has to be measured from a position no
	// rounding has touched.
	_, level := fetchWorld(t, cli, srv.URL)
	spawn := level.Spawn
	room := level.Sectors[level.SpawnSector]

	conn, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	wire := newDumWire(conn, readFrames(t, conn))

	wire.attach(t)

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
	// pickup in the spawn room, so nothing here can collect anything underneath
	// the assertion either. Checked rather than assumed, because retuning RoomMin
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
		wire.attach(t) // the barrier: the frame is in the world before a tick fires
		ticks := 2
		if w == 0 {
			ticks = 1
		}
		for i := 0; i < ticks; i++ {
			clk.step(t, tick)
		}
		wire.draw(t, clk)
	}

	// The drive granted 2*windows-1 ticks against windows*100 ms of input, so the
	// world cannot have caught up here — and if a retune ever makes it catch up,
	// every frame lands on an empty queue and this test quietly stops testing
	// anything.
	if wire.ack >= total {
		t.Fatalf("the world acknowledged all %d commands during the walk, so no frame ever landed on a queue with anything in it", total)
	}

	// Now let it finish what it was asked for.
	acked := wire.pumpUntil(t, tick, clk, "every command acknowledged",
		func(s dumSnapshot) bool { return s.Ack >= total })

	// And then keep ticking with nothing left to send, which is what letting go
	// of the controls looks like from here. The queue holds at most maxPending
	// commands of subStep each — the expression is world.go's, mirrored because
	// the constant is unexported — so this many ticks empties whatever the
	// acknowledgement did not account for. Arithmetic on a bounded queue rather
	// than a wait on anything that could be slow.
	const queueCap = 4 * (gamevanyadum.MaxCommandsPerFrame + gamevanyadum.RedundantCommands)
	settleTicks := int64(math.Ceil(queueCap * subStep * gamevanyadum.SimHz))
	settled := wire.pumpUntil(t, tick, clk, "the walk to settle",
		func(s dumSnapshot) bool { return s.Tick >= acked.Tick+settleTicks })

	// THE ACK IS A PROMISE THAT THERE IS NOTHING LEFT. A client drops everything
	// at or below it and stops predicting it, so movement produced after the
	// input that caused it was acknowledged is movement the client has no record
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

func TestVanyadumTwoPeopleShareOneBuildingAndItsBeer(t *testing.T) {
	// THE WHOLE ITERATION, END TO END. Two accounts, two real sockets, one
	// заброшка, over a real Postgres: they see each other move, the bottle one of
	// them drinks disappears from the other's world too, it comes back on the
	// tick the catalogue says it will, and walking away writes one visit each.
	app, tick, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cliA := loginAs(t, srv.URL, "910010", "user")
	cliB := loginAs(t, srv.URL, "910011", "user")
	clk := newDumClock()

	worldID, level := fetchWorld(t, cliA, srv.URL)

	connA, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cliA, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cliB, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer connB.CloseNow()

	a := newDumWire(connA, readFrames(t, connA))
	b := newDumWire(connB, readFrames(t, connB))
	room := []*dumWire{a, b}

	// Both walk in, and both are let into the SAME building — which is what the
	// world id on the ready frame is for.
	for _, w := range room {
		if got := w.attach(t); got != worldID {
			t.Fatalf("let into %s, the geometry everybody fetched is %s", got, worldID)
		}
	}

	// A few ticks so both of them are in a frame, then each must be able to see
	// the other. A peer is named by a twelve-character handle and never by an
	// account id (ADR-037).
	deadline := time.Now().Add(20 * time.Second)
	for (len(a.last.Peers) == 0 || len(b.last.Peers) == 0) && time.Now().Before(deadline) {
		dumPump(t, tick, clk, room...)
	}
	if len(a.last.Peers) == 0 || len(b.last.Peers) == 0 {
		t.Fatalf("a sees %d peers and b sees %d; they are not in the same building",
			len(a.last.Peers), len(b.last.Peers))
	}
	if a.last.Peers[0].ID == b.last.Peers[0].ID {
		t.Fatalf("both frames name the peer %q — somebody is looking at himself", a.last.Peers[0].ID)
	}
	for who, s := range map[string]dumSnapshot{"a": a.last, "b": b.last} {
		if len(s.Peers[0].ID) != 12 {
			t.Fatalf("%s's neighbour is called %q, which is not a handle", who, s.Peers[0].ID)
		}
	}

	// A walks somewhere. B must SEE him move — a peer whose position never
	// changes is a figure drawn once, which is not the same thing as a shared
	// world.
	target := level.Pickups[0]
	before := b.last.Peers[0]
	dumWalkTo(t, tick, clk, room, a, dumRouteTo(t, level, level.SpawnSector, target.Sector, target.Pos))
	after := b.peerNamed(t, before.ID)
	if moved := math.Hypot(float64(after.X-before.X), float64(after.Y-before.Y)) / 100; moved < 1 {
		t.Fatalf("a crossed the заброшка and b saw him move %.2f m", moved)
	}

	// And the bottle he walked over is gone from BOTH their worlds. The mask is
	// idempotent full state — bit i is the pickup at index i of the level — so
	// this is the same field for everybody rather than a per-player view of it.
	const bit = uint32(1) << 0
	dumWaitFor(t, tick, clk, room, "the beer to be gone for both of them",
		func() bool { return a.last.Left&bit == 0 && b.last.Left&bit == 0 })

	// Then it comes back, on the tick the catalogue promised and not before. The
	// respawn rides the mask and nothing else: there is no event for it, because
	// the frame already says so twenty times a second.
	//
	// A walks away first, or he would take it again on the very tick it returned
	// and the bit would never be seen set.
	dumWalkTo(t, tick, clk, room, a, dumRouteTo(t, level, target.Sector, level.SpawnSector, level.Spawn))
	dumWaitFor(t, tick, clk, room, "the beer to come back for both of them",
		func() bool { return a.last.Left&bit != 0 && b.last.Left&bit != 0 })

	// And then everybody goes home. Closing the socket is the only way out of the
	// building — there is no quit button, because nothing here ends — so what
	// records the visit is the abandon grace expiring with nobody connected.
	if err := connA.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close a: %v", err)
	}
	if err := connB.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close b: %v", err)
	}
	clk.jump(gamevanyadum.AbandonGrace + time.Second)

	for _, uid := range []string{"910010", "910011"} {
		waitForVisits(t, tick, clk, uid, 1)
	}
	// Exactly one each, and not one per tick after the grace: the occupant is
	// taken out of the building by the same tick that queues the row.
	for i := 0; i < 20; i++ {
		clk.step(t, tick)
	}
	for _, uid := range []string{"910010", "910011"} {
		if n := visitRowCount(t, uid); n != 1 {
			t.Fatalf("%s left once and has %d visits", uid, n)
		}
	}

	// The row says which building they were in, which is the only thing anybody
	// has ever wanted to ask of it.
	var seed int64
	if err := pool.QueryRow(context.Background(),
		`SELECT seed FROM game_vanyadum_visits WHERE account_id = $1::uuid`,
		accountIDByUID(t, "910010")).Scan(&seed); err != nil {
		t.Fatalf("read the visit: %v", err)
	}
	if seed != level.Seed {
		t.Fatalf("the visit records building %d, they were in %d", seed, level.Seed)
	}
}

func TestVanyadumMyVisitsReadsBackWhatWasWritten(t *testing.T) {
	// The splash screen's list, and the reason it exists: it makes "the visit was
	// written" something a person can check in production without opening a
	// database.
	app, tick, _ := buildAppVanyadum(t, dumVK(t))
	srv := httptest.NewServer(app)
	defer srv.Close()
	cli := loginAs(t, srv.URL, "910012", "user")
	clk := newDumClock()

	_, level := fetchWorld(t, cli, srv.URL)
	conn, _, err := dialVanyadum(t, srv.URL, cookieHeader(t, cli, srv.URL), gamevanyadum.Room)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	w := newDumWire(conn, readFrames(t, conn))
	w.attach(t)

	// Long enough to be worth recording, driven on the tick's own clock rather
	// than on a wall-clock wait.
	for i := 0; i < 5; i++ {
		dumPump(t, tick, clk, w)
	}
	clk.jump(30 * time.Second)
	dumPump(t, tick, clk, w)

	if err := conn.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close: %v", err)
	}
	clk.jump(gamevanyadum.AbandonGrace + time.Second)
	waitForVisits(t, tick, clk, "910012", 1)

	resp, err := cli.Get(srv.URL + "/api/game-vanyadum/visits/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var mine struct {
		Visits []struct {
			Seed     int64     `json:"seed"`
			Seconds  int       `json:"seconds"`
			Beer     int       `json:"beer"`
			JoinedAt time.Time `json:"joined_at"`
		} `json:"visits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mine); err != nil {
		t.Fatal(err)
	}
	if len(mine.Visits) != 1 {
		t.Fatalf("my visits reads back %d rows", len(mine.Visits))
	}
	v := mine.Visits[0]
	if v.Seed != level.Seed {
		t.Fatalf("the visit names building %d, the world was %d", v.Seed, level.Seed)
	}
	// Thirty seconds of clock passed with the socket connected, and the two
	// minutes of grace after it must not be in this number.
	if v.Seconds < 25 || v.Seconds > 40 {
		t.Fatalf("the visit records %d seconds; about thirty were spent in the building", v.Seconds)
	}
	if v.JoinedAt.IsZero() {
		t.Fatal("the visit does not say when it started")
	}
}

func TestVanyadumTheVisitsTableReplacedTheRunsTable(t *testing.T) {
	// The migration, asserted rather than assumed. A run had an objective and a
	// result; a visit has neither, so the old table is dropped in the same change
	// that adds the new one — this codebase does not carry two ways of storing
	// the same thing while somebody works out which is live.
	var visits, runs *string
	if err := pool.QueryRow(context.Background(),
		`SELECT to_regclass('public.game_vanyadum_visits')::text,
		        to_regclass('public.game_vanyadum_runs')::text`).Scan(&visits, &runs); err != nil {
		t.Fatalf("look up the tables: %v", err)
	}
	if visits == nil {
		t.Fatal("game_vanyadum_visits does not exist")
	}
	if runs != nil {
		t.Fatalf("game_vanyadum_runs is still there as %q", *runs)
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

// dumPeer is one other person in the building, as the frame names him.
type dumPeer struct {
	ID string `json:"i"`
	X  int    `json:"x"`
	Y  int    `json:"y"`
}

// dumSnapshot is the part of a snapshot a driving test reads. Typed rather than
// the map[string]any the assertions above use, because every field of it is
// something this file does arithmetic or comparisons on.
type dumSnapshot struct {
	T     string    `json:"t"`
	Tick  int64     `json:"k"`
	Ack   int64     `json:"ack"`
	X     int       `json:"x"`
	Y     int       `json:"y"`
	Left  uint32    `json:"pk"`
	Peers []dumPeer `json:"p"`
}

// dumWire is the browser's send loop, reduced to the part the world can tell
// apart: a monotonic per-command sequence, the commands not yet acknowledged,
// and the last snapshot drawn. Everything the real client additionally does with
// a frame — prediction, replay, interpolation — is client-side and invisible
// from the server's end of the socket, so reproducing it here would only be
// reproducing a second implementation of it.
type dumWire struct {
	conn   *websocket.Conn
	frames <-chan []byte

	seq     int64
	unacked []dumCommand
	// ack is the highest acknowledgement any snapshot has carried, and seen is
	// the tick of the last one — the number the server derives round trip from.
	ack  int64
	seen int64
	// last is the newest snapshot folded in, which is what the steering below
	// aims from.
	last dumSnapshot
	// lastSend is when a frame was last written, and it is what keeps a driving
	// test inside the socket's own rate limit — see dumSendGap.
	lastSend time.Time
}

func newDumWire(conn *websocket.Conn, frames <-chan []byte) *dumWire {
	return &dumWire{conn: conn, frames: frames}
}

// dumSendGap is the shortest interval this file will put between two input
// frames on one socket.
//
// THE SOCKET'S LIMIT IS TEN MESSAGES A SECOND with a burst of twenty
// (internal/realtime/conn.go), and a driving test fires ticks as fast as the
// scheduler will take them — so a steering loop that wrote whenever it had
// something to say would exhaust the burst within milliseconds and be
// disconnected. That presents as "no frame arrived" rather than as a rate-limit
// error, which is why the pacing is here rather than discovered.
//
// It is enforced by SKIPPING a send, never by sleeping: the loop goes on firing
// ticks, which is real work, and the next pass writes once enough of them have
// gone by. Nothing in this file waits on the clock to synchronise.
const dumSendGap = 120 * time.Millisecond

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
	w.sendExactly(t, fresh)
}

// sendExactly writes the commands it is given, stamping each with the next
// sequence number, and is what the steering loop uses when the sub-steps are not
// all the same length.
func (w *dumWire) sendExactly(t *testing.T, fresh []dumCommand) {
	t.Helper()
	for i := range fresh {
		if fresh[i].Seq == 0 {
			w.seq++
			fresh[i].Seq = w.seq
		}
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
	w.lastSend = time.Now()
}

// attach sends a hello and returns the world id it is answered with, folding in
// any snapshot that arrives on the way.
//
// It is the JOIN — there is no start endpoint — and it is ALSO this file's
// delivery barrier. One connection's messages are read in order on one read
// pump, so a ready proves that everything written before the hello has already
// reached the world. Without it the gap between Write returning and the read
// pump enqueueing is settled by the goroutine scheduler, and a test that fires a
// tick into that gap measures a cadence it did not choose — which is how a
// phase-sensitive test comes to pass on one machine and fail on another.
//
// No tick is fired while waiting, deliberately. An idle tick is not free to the
// occupant it ticks: each one banks another SimStep of unspent time budget, up
// to TimeBudgetCap's half second, and an occupant holding half a second of credit
// swallows a whole backlog in a single step — which is precisely the condition
// the redundancy walk above is built to create.
func (w *dumWire) attach(t *testing.T) string {
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
				T       string `json:"t"`
				WorldID string `json:"world_id"`
			}
			if json.Unmarshal(raw, &f) != nil {
				continue
			}
			switch f.T {
			case gamevanyadum.TypeReady:
				return f.WorldID
			case gamevanyadum.TypeFull:
				t.Fatal("the заброшка refused this socket as full")
			}
		case <-deadline:
			t.Fatal("no ready frame arrived")
		}
	}
}

// draw folds in snapshots until one describes the last tick that was fired, and
// returns it — so the acknowledgement the next frame is composed against is as
// current as the world it is answering. That is what the browser has, and it is
// what makes the redundancy tail this file sends the tail a browser would.
//
// It waits on a tick already fired and never fires one itself; see attach on why
// an idle tick is not free.
func (w *dumWire) draw(t *testing.T, clk *dumClock) dumSnapshot {
	t.Helper()
	want := clk.ticks
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

// peerNamed is the entry for one handle on this socket's newest frame.
func (w *dumWire) peerNamed(t *testing.T, id string) dumPeer {
	t.Helper()
	for _, p := range w.last.Peers {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("this frame does not mention %q at all: %+v", id, w.last.Peers)
	return dumPeer{}
}

// apply reads one delivered frame, reporting the snapshot it was — anything
// else on this socket is a ready or a refusal, and neither says anything about
// what has been simulated.
func (w *dumWire) apply(raw []byte) (dumSnapshot, bool) {
	var s dumSnapshot
	if json.Unmarshal(raw, &s) != nil || s.T != gamevanyadum.TypeSnapshot {
		return dumSnapshot{}, false
	}
	w.seen = s.Tick
	w.last = s
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
func (w *dumWire) pumpUntil(t *testing.T, tick chan time.Time, clk *dumClock, why string, done func(dumSnapshot) bool) dumSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		clk.step(t, tick)
		if s := w.draw(t, clk); done(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s and it never came; %d commands acknowledged", why, w.ack)
		}
	}
}

// dumPump fires one tick and reads the frame it produced on EVERY socket.
//
// Every socket, on every tick, for the reason spelled out on pumpUntil: each
// tick publishes to each connection, so a loop that ticked without reading fills
// the send buffers and the hub evicts the sockets as slow consumers. With two
// people in the building that arrives twice as fast.
func dumPump(t *testing.T, tick chan time.Time, clk *dumClock, room ...*dumWire) {
	t.Helper()
	clk.step(t, tick)
	for _, w := range room {
		w.draw(t, clk)
	}
}

// dumWaitFor pumps the whole room until a condition over its newest frames
// holds. Bounded by a DEADLINE rather than by a count of ticks, because how long
// a tick takes is a property of the machine and not of the game.
func dumWaitFor(t *testing.T, tick chan time.Time, clk *dumClock, room []*dumWire, why string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s and it never came", why)
		}
		dumPump(t, tick, clk, room...)
	}
}

// dumRouteTo is the sequence of points to walk through to get from one sector to a
// spot in another: the midpoint of every doorway on the way, and then the spot
// itself.
//
// THE PORTAL GRAPH IS A TREE. Every room is attached to one that already exists
// (level.go), so there is exactly one path between any two rooms and a
// breadth-first search finds it. Walking a straight line between consecutive
// waypoints stays inside the geometry, because every room is a rectangle — hence
// convex — and a doorway's midpoint lies on the boundary of exactly the two
// rooms it joins.
//
// A doorway is DoorWidth wide against a player radius of PlayerRadius, so its
// midpoint is somewhere the player genuinely fits: the nearest jamb is half the
// doorway away, which is comfortably more than his radius.
func dumRouteTo(t *testing.T, l *gamevanyadum.Level, from, to int, at gamevanyadum.Vec2) []gamevanyadum.Vec2 {
	t.Helper()
	if from == to {
		return []gamevanyadum.Vec2{at}
	}
	// via[s] is the portal that reaches sector s, and back[s] is where it was
	// reached from.
	via := map[int]int{}
	back := map[int]int{from: from}
	queue := []int{from}
	for len(queue) > 0 && back[to] == 0 && to != from {
		cur := queue[0]
		queue = queue[1:]
		for i, p := range l.Portals {
			var next int
			switch cur {
			case p.A:
				next = p.B
			case p.B:
				next = p.A
			default:
				continue
			}
			if _, seen := back[next]; seen {
				continue
			}
			via[next], back[next] = i, cur
			queue = append(queue, next)
		}
		if _, done := back[to]; done {
			break
		}
	}
	if _, ok := back[to]; !ok {
		t.Fatalf("no way from sector %d to sector %d; the portal graph is not a tree", from, to)
	}

	var doors []gamevanyadum.Vec2
	for s := to; s != from; s = back[s] {
		doors = append(doors, dumPortalMid(l.Portals[via[s]]))
	}
	// Collected from the destination backwards, so it goes on the wire the other
	// way round.
	out := make([]gamevanyadum.Vec2, 0, len(doors)+1)
	for i := len(doors) - 1; i >= 0; i-- {
		out = append(out, doors[i])
	}
	return append(out, at)
}

// dumPortalMid is the middle of a doorway, which is the point a player walks
// through it at.
func dumPortalMid(p gamevanyadum.Portal) gamevanyadum.Vec2 {
	if p.Vertical {
		return gamevanyadum.Vec2{X: p.At, Y: (p.Lo + p.Hi) / 2}
	}
	return gamevanyadum.Vec2{X: (p.Lo + p.Hi) / 2, Y: p.At}
}

// dumArrived is how close counts as having reached a waypoint. Generous against
// PickupReach's 0.9 m, and generous against a doorway's half-width, so a player
// nudged sideways by the collision resolver still counts as through.
const dumArrived = 0.5

// dumWalkTo steers one socket along a route, over the real wire, until it has
// reached the last waypoint.
//
// The whole room is pumped throughout, because every tick publishes to every
// connection and an unread socket is an evicted one. Bounded by a deadline.
//
// Each frame carries up to MaxCommandsPerFrame sub-steps of up to MaxStepSeconds
// — four metres of walking in one message — which is what makes a walk across
// the заброшка affordable inside the socket's ten-messages-a-second budget. The
// last sub-step of a batch is cut to the distance that is actually left, so the
// player lands on the waypoint rather than past it.
func dumWalkTo(t *testing.T, tick chan time.Time, clk *dumClock, room []*dumWire, who *dumWire, route []gamevanyadum.Vec2) {
	t.Helper()
	step := gamevanyadum.WalkSpeed * gamevanyadum.MaxStepSeconds
	for n, at := range route {
		deadline := time.Now().Add(30 * time.Second)
		for {
			dx := at.X - float64(who.last.X)/100
			dy := at.Y - float64(who.last.Y)/100
			left := math.Hypot(dx, dy)
			if left <= dumArrived {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("waypoint %d of %d is %.2f m away and he stopped getting closer", n+1, len(route), left)
			}
			// Only when the previous batch has been fully folded in, so the aim
			// is taken from where he actually is — and only as often as the
			// socket's rate limit allows.
			if who.ack >= who.seq && time.Since(who.lastSend) >= dumSendGap {
				yaw := math.Atan2(dx/left, dy/left) // yaw zero looks along +Y
				var batch []dumCommand
				for remaining := left; remaining > 1e-6 && len(batch) < gamevanyadum.MaxCommandsPerFrame; {
					d := math.Min(step, remaining)
					batch = append(batch, dumCommand{Dt: d / gamevanyadum.WalkSpeed, MY: 1, Yaw: yaw})
					remaining -= d
				}
				who.sendExactly(t, batch)
			}
			dumPump(t, tick, clk, room...)
		}
	}
}

// visitRowCount counts an account's rows in the game's one table.
func visitRowCount(t *testing.T, uid string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM game_vanyadum_visits WHERE account_id = $1::uuid`,
		accountIDByUID(t, uid)).Scan(&n)
	if err != nil {
		t.Fatalf("count visits: %v", err)
	}
	return n
}

// waitForVisits pumps the simulation until an account has the expected number of
// recorded visits.
//
// The write is queued by the tick and performed by the service's own writer
// goroutine, so the row appears a moment after the tick that caused it — which
// is why this both TICKS and re-reads, bounded by a deadline rather than by a
// number of attempts.
func waitForVisits(t *testing.T, tick chan time.Time, clk *dumClock, uid string, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if n := visitRowCount(t, uid); n >= want {
			if n != want {
				t.Fatalf("%s has %d visits, expected %d", uid, n, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached %d recorded visits", uid, want)
		}
		clk.step(t, tick)
	}
}
