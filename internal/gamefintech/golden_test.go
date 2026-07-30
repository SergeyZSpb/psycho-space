package gamefintech

import (
	"encoding/json"
	"flag"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// The conformance vectors that pin the TypeScript port of the simulation.
//
// WHY THIS FILE EXISTS. Client-side prediction means the browser runs the same
// movement, the same ramp and the same dash the server does, so `Sanitise` and
// `Step` now exist twice — in Go and in TypeScript (web/src/lib/fintechStep.ts).
// That is a second implementation of one rule, which this project's rules tell
// you not to build, and it is accepted here only because prediction is
// impossible without it. What makes it SAFE rather than merely permitted is
// this: the Go implementation emits a trace, the TypeScript one has to reproduce
// it, and the two diverging is a red test in the ordinary gate rather than a
// player watching their own salary jump backwards.
//
// THE TRACE IS OF Step(Sanitise(cmd)), NOT OF Step ALONE. That is the pipeline
// the client actually runs — it clamps its own input before predicting with it,
// because the server clamps before simulating and a prediction made from an
// unclamped command diverges on the first frame. So the port reproduces both
// functions or neither.
//
// Regenerate with:
//
//	./dev.sh test -run TestGoldenVectors ./internal/gamefintech/ -update
//
// Doing so deliberately breaks the TypeScript test until the port is brought
// back into line. That is the point: a change to the feel of this game is a
// change to two files, and this is what refuses to let it be a change to one.

var updateGolden = flag.Bool("update", false, "rewrite the golden vectors from the current Step")

// goldenPath is where the vectors live. Under testdata/ so `go build` ignores
// them, and read by the vitest suite through a relative path.
func goldenPath() string { return filepath.Join("testdata", "step_vectors.json") }

type goldenFile struct {
	// Note is for a human who opens the file wondering what it is.
	Note      string          `json:"note"`
	Constants goldenConstants `json:"constants"`
	Cases     []goldenCase    `json:"cases"`
}

// goldenConstants are the numbers the port must be using. They are in the file
// so that a mismatch reports "your ramp is six seconds and mine is eight" rather
// than a thousand failing salaries.
type goldenConstants struct {
	OfficeW        float64 `json:"office_w"`
	OfficeH        float64 `json:"office_h"`
	PlayerRadius   float64 `json:"player_radius"`
	WalkSpeed      float64 `json:"walk_speed"`
	DashSpeed      float64 `json:"dash_speed"`
	DashSeconds    float64 `json:"dash_seconds"`
	DashCooldown   float64 `json:"dash_cooldown"`
	IdleThreshold  float64 `json:"idle_threshold"`
	BasePerSecond  float64 `json:"base_per_second"`
	RampSeconds    float64 `json:"ramp_seconds"`
	MaxMultiplier  float64 `json:"max_multiplier"`
	GraceSeconds   float64 `json:"grace_seconds"`
	SlowFactor     float64 `json:"slow_factor"`
	SlowSeconds    float64 `json:"slow_seconds"`
	MaxStepSeconds float64 `json:"max_step_seconds"`
}

// goldenCase is one scenario: a start state, the furniture it runs against, the
// raw commands, and what the simulation did with them.
type goldenCase struct {
	Name  string       `json:"name"`
	Desks []Rect       `json:"desks"`
	Start goldenPlayer `json:"start"`
	// Commands are RAW — as a client would send them. Sanitise them before
	// stepping, exactly as Office.Enqueue does.
	Commands []goldenCmd `json:"commands"`
	// Trace is the player after each command, one entry per command.
	Trace []goldenPlayer `json:"trace"`
}

type goldenCmd struct {
	Dt   float64 `json:"dt"`
	MX   float64 `json:"mx"`
	MY   float64 `json:"my"`
	Dash bool    `json:"d"`
}

// goldenPlayer is the whole of Player, flattened. Every field is compared, not
// just the position: the salary and the streak are as much of this game as the
// coordinates are, and a port that moved correctly while paying wrongly would be
// the worst kind of green.
type goldenPlayer struct {
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Salary       float64 `json:"salary"`
	Streak       float64 `json:"streak"`
	MoveGrace    float64 `json:"move_grace"`
	DashLeft     float64 `json:"dash_left"`
	DashDX       float64 `json:"dash_dx"`
	DashDY       float64 `json:"dash_dy"`
	DashCooldown float64 `json:"dash_cooldown"`
	SlowLeft     float64 `json:"slow_left"`
	Alive        bool    `json:"alive"`
}

func toGolden(p Player) goldenPlayer {
	return goldenPlayer{
		X: round9(p.Pos.X), Y: round9(p.Pos.Y),
		Salary: round9(p.Salary), Streak: round9(p.Streak), MoveGrace: round9(p.MoveGrace),
		DashLeft: round9(p.DashLeft), DashDX: round9(p.DashDX), DashDY: round9(p.DashDY),
		DashCooldown: round9(p.DashCooldown), SlowLeft: round9(p.SlowLeft), Alive: p.Alive,
	}
}

// round9 trims the last few bits of float noise before a number is written down.
//
// It is a FILE SIZE decision, not a correctness one: `6.1000000000000005` costs
// eighteen characters where `6.1` costs three, and this file carries about
// eleven thousand of them. Both sides of the Go comparison round identically, so
// it stays exact here; the TypeScript side compares with a 1e-6 tolerance, which
// is three orders of magnitude looser than what is discarded.
func round9(v float64) float64 { return math.Round(v*1e9) / 1e9 }

// run steps a case and fills in its trace.
func (c *goldenCase) run(start Player) {
	c.Start = toGolden(start)
	p := start
	for _, raw := range c.Commands {
		p = Step(c.Desks, p, Sanitise(Command{Dt: raw.Dt, MX: raw.MX, MY: raw.MY, Dash: raw.Dash}))
		c.Trace = append(c.Trace, toGolden(p))
	}
}

// still, walking and dashing build command runs.
func still(n int) []goldenCmd {
	out := make([]goldenCmd, n)
	for i := range out {
		out[i] = goldenCmd{Dt: testDt}
	}
	return out
}

func walking(n int, mx, my float64) []goldenCmd {
	out := make([]goldenCmd, n)
	for i := range out {
		out[i] = goldenCmd{Dt: testDt, MX: mx, MY: my}
	}
	return out
}

// buildGolden runs every case through the real simulation and returns the file.
//
// The cases are chosen to exercise the parts of the port most likely to be got
// wrong, rather than to be many: the ramp, both sides of the grace window, the
// whole dash cycle including a refusal, furniture, the walls, the diagonal, a
// hostile frame, and one long pseudo-random wander that finds the corners
// nobody thought of.
func buildGolden() goldenFile {
	f := goldenFile{
		Note: "Generated by TestGoldenVectors in internal/gamefintech. " +
			"Pins the TypeScript port (web/src/lib/fintechStep.ts) to the Go original. " +
			"Each command is RAW: apply sanitise() and then step() with the case's desks, " +
			"and compare the whole player against the trace entry (tolerance 1e-6). " +
			"Regenerate with: ./dev.sh test -run TestGoldenVectors ./internal/gamefintech/ -update",
		Constants: goldenConstants{
			OfficeW: OfficeW, OfficeH: OfficeH, PlayerRadius: PlayerRadius,
			WalkSpeed: WalkSpeed, DashSpeed: DashSpeed,
			DashSeconds: DashSeconds, DashCooldown: DashCooldown,
			IdleThreshold: IdleThreshold,
			BasePerSecond: BasePerSecond, RampSeconds: RampSeconds,
			MaxMultiplier: MaxMultiplier, GraceSeconds: GraceSeconds,
			MaxStepSeconds: MaxStepSeconds,
			SlowFactor:     SlowFactor,
			SlowSeconds:    SlowSeconds,
		},
	}

	// Standing perfectly still for eight seconds: the ramp climbing to the cap
	// and then staying there while the money keeps coming.
	{
		c := goldenCase{Name: "idle-ramp", Desks: Desks, Commands: still(8 * SimHz)}
		c.run(atSpawn())
		f.Cases = append(f.Cases, c)
	}

	// Both sides of the grace window, from a fully ramped start: a quarter of a
	// second of walking costs nothing, four tenths costs everything.
	{
		var cmds []goldenCmd
		cmds = append(cmds, still(int(RampSeconds*SimHz))...)
		cmds = append(cmds, walking(5, 1, 0)...) // 0.25 s — inside
		cmds = append(cmds, still(20)...)
		cmds = append(cmds, walking(8, 1, 0)...) // 0.40 s — past
		cmds = append(cmds, still(40)...)
		c := goldenCase{Name: "grace-window", Desks: Desks, Commands: cmds}
		c.run(atSpawn())
		f.Cases = append(f.Cases, c)
	}

	// The whole dash cycle: a ramped player dashes (preserving the streak and
	// earning nothing), asks again inside the cooldown and is refused, waits it
	// out, and dashes again.
	{
		var cmds []goldenCmd
		cmds = append(cmds, still(int(RampSeconds*SimHz))...)
		cmds = append(cmds, goldenCmd{Dt: testDt, MX: 0, MY: -1, Dash: true})
		cmds = append(cmds, walking(20, 0, -1)...)
		cmds = append(cmds, goldenCmd{Dt: testDt, MX: 0, MY: -1, Dash: true}) // refused
		cmds = append(cmds, still(int(DashCooldown*SimHz)+2)...)
		cmds = append(cmds, goldenCmd{Dt: testDt, MX: 0, MY: -1, Dash: true}) // granted
		cmds = append(cmds, walking(10, 0, -1)...)
		c := goldenCase{Name: "dash-cycle", Desks: Desks, Commands: cmds}
		c.run(atSpawn())
		f.Cases = append(f.Cases, c)
	}

	// Straight into the side of a desk, then along it, then round the end: the
	// push-out and the slide.
	{
		start := atSpawn()
		start.Pos = Vec2{X: 8, Y: 3.6}
		var cmds []goldenCmd
		cmds = append(cmds, walking(40, -1, 0)...)
		cmds = append(cmds, walking(40, 0, -1)...)
		cmds = append(cmds, walking(40, -1, -1)...)
		c := goldenCase{Name: "desk-push-out", Desks: Desks, Commands: cmds}
		c.run(start)
		f.Cases = append(f.Cases, c)
	}

	// Into a corner and held there: the floor clamp, on both axes at once.
	{
		start := atSpawn()
		start.Pos = Vec2{X: 8, Y: 18}
		var cmds []goldenCmd
		cmds = append(cmds, walking(60, -1, 1)...)
		cmds = append(cmds, walking(60, 1, 1)...)
		c := goldenCase{Name: "floor-clamp", Desks: Desks, Commands: cmds}
		c.run(start)
		f.Cases = append(f.Cases, c)
	}

	// SLOWED, then not. Claude Code has landed on somebody, so the walk is
	// multiplied for a few seconds and then is not — and the DASH inside that window
	// must be untouched, which is the whole asymmetry of the punishment. The case
	// runs long enough to cover the slow expiring, so the port has to get both the
	// multiplied speed AND the moment it stops being multiplied.
	{
		start := atSpawn()
		start.SlowLeft = SlowSeconds
		var cmds []goldenCmd
		cmds = append(cmds, walking(30, 0, -1)...)
		// A dash while slowed: full distance, and it must not be scaled.
		cmds = append(cmds, goldenCmd{Dt: SimStep.Seconds(), MX: 1, MY: 0, Dash: true})
		cmds = append(cmds, walking(20, 1, 0)...)
		// Past the end of the slow, at ordinary speed again.
		cmds = append(cmds, walking(80, 1, 0)...)
		c := goldenCase{Name: "slowed-walk", Desks: Desks, Commands: cmds}
		c.run(start)
		f.Cases = append(f.Cases, c)
	}

	// A diagonal must not be faster than a straight line, and a half-pressed
	// stick must stay analogue. Both are Sanitise's business, which is why the
	// vectors carry raw commands.
	{
		var cmds []goldenCmd
		cmds = append(cmds, walking(20, 1, 1)...)
		cmds = append(cmds, walking(20, 0.5, 0)...)
		cmds = append(cmds, walking(20, 0.3, -0.4)...)
		c := goldenCase{Name: "diagonal-and-analogue", Desks: Desks, Commands: cmds}
		c.run(atSpawn())
		f.Cases = append(f.Cases, c)
	}

	// Hostile input. Both ends must clamp identically, or a client predicts one
	// thing from a command the server reads as another and the correction never
	// settles.
	{
		c := goldenCase{Name: "hostile-clamping", Desks: Desks, Commands: []goldenCmd{
			{Dt: 1e6, MY: 1e6},
			{Dt: -5, MY: 1},
			{Dt: testDt, MX: 50, MY: -50},
			{Dt: 100, MX: 0.5, MY: 0.5, Dash: true},
			{Dt: 0.5, MY: 1},
			{Dt: testDt},
		}}
		c.run(atSpawn())
		f.Cases = append(f.Cases, c)
	}

	// A long pseudo-random wander, generated here so the file carries the
	// commands and the port needs no random source of its own. Three hundred steps
	// through a real office will press into desk corners, run the walls and dash
	// at arbitrary moments.
	{
		r := rand.New(rand.NewPCG(20260729, 4))
		cmds := make([]goldenCmd, 0, 300)
		for i := 0; i < 300; i++ {
			cmds = append(cmds, goldenCmd{
				Dt:   testDt,
				MX:   float64(int(r.Float64()*200-100)) / 100,
				MY:   float64(int(r.Float64()*200-100)) / 100,
				Dash: r.IntN(40) == 0,
			})
		}
		c := goldenCase{Name: "wander", Desks: Desks, Commands: cmds}
		c.run(atSpawn())
		f.Cases = append(f.Cases, c)
	}

	return f
}

// TestGoldenVectors asserts that the committed vectors still describe what the
// simulation does — and rewrites them under -update.
//
// So a change to the feel fails HERE first, loudly, telling you the file is
// stale; you regenerate; and then the TypeScript conformance test fails until
// the port is brought back into line. Two gates, in a deliberate order.
func TestGoldenVectors(t *testing.T) {
	got := buildGolden()
	path := goldenPath()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		// Compact, not indented: this is a machine artefact read by a test in
		// another language, and indentation trebles a file that already lands in
		// every diff that touches the simulation.
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s — the TypeScript conformance test will now fail until the port is updated", path)
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — generate it with: ./dev.sh test -run TestGoldenVectors ./internal/gamefintech/ -update", err)
	}
	var want goldenFile
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	if want.Constants != got.Constants {
		t.Fatalf("constants moved: golden %+v, current %+v — regenerate with -update", want.Constants, got.Constants)
	}
	if len(want.Cases) != len(got.Cases) {
		t.Fatalf("golden has %d cases, current build has %d — regenerate with -update", len(want.Cases), len(got.Cases))
	}
	for i := range got.Cases {
		g, w := got.Cases[i], want.Cases[i]
		if g.Name != w.Name {
			t.Fatalf("case %d is now %q, golden says %q — regenerate with -update", i, g.Name, w.Name)
		}
		if len(g.Trace) != len(w.Trace) {
			t.Fatalf("case %q: trace lengths differ (%d vs %d)", g.Name, len(g.Trace), len(w.Trace))
		}
		for j := range g.Trace {
			// Exact, on this side: it is the same binary that produced the file.
			// The tolerance belongs on the TypeScript side, where a different
			// runtime's arithmetic may differ in the last bit.
			if g.Trace[j] != w.Trace[j] {
				t.Fatalf("case %q step %d: the simulation now produces %+v, golden says %+v — regenerate with -update",
					g.Name, j, g.Trace[j], w.Trace[j])
			}
		}
	}
}

func TestTheGoldenVectorsCoverWhatTheyClaimTo(t *testing.T) {
	// A vector file that exercised only the happy path would be a green test
	// that proves nothing. These are the properties the port is most likely to
	// get wrong, asserted against the generated trace so the file cannot quietly
	// stop covering them.
	f := buildGolden()
	byName := map[string]goldenCase{}
	for _, c := range f.Cases {
		byName[c.Name] = c
	}

	ramp := byName["idle-ramp"]
	if last := ramp.Trace[len(ramp.Trace)-1]; last.Streak < RampSeconds || last.Salary <= 0 {
		t.Fatalf("the idle case never reached the cap: %+v", last)
	}

	grace := byName["grace-window"]
	var kept, lost bool
	for i := 1; i < len(grace.Trace); i++ {
		if grace.Trace[i].Streak == 0 && grace.Trace[i-1].Streak > 0 {
			lost = true
		}
		if grace.Trace[i].MoveGrace > 0 && grace.Trace[i].Streak == grace.Trace[i-1].Streak &&
			grace.Trace[i].Streak > 0 {
			kept = true
		}
	}
	if !kept || !lost {
		t.Fatalf("the grace case does not show both sides of the window (kept=%v lost=%v)", kept, lost)
	}

	dash := byName["dash-cycle"]
	var dashed, refused int
	for i := 1; i < len(dash.Trace); i++ {
		if dash.Trace[i].DashLeft > 0 && dash.Trace[i-1].DashLeft == 0 {
			dashed++
		}
		if dash.Trace[i].DashCooldown > 0 && dash.Trace[i].DashCooldown < dash.Trace[i-1].DashCooldown {
			refused++ // the cooldown ran down rather than being restarted
		}
	}
	if dashed != 2 {
		t.Fatalf("the dash case starts %d dashes, want 2 (one granted, one after the cooldown)", dashed)
	}
	if refused == 0 {
		t.Fatal("the dash case never shows a request inside the cooldown")
	}

	wander := byName["wander"]
	for i, p := range wander.Trace {
		if p.X < PlayerRadius-1e-9 || p.X > OfficeW-PlayerRadius+1e-9 ||
			p.Y < PlayerRadius-1e-9 || p.Y > OfficeH-PlayerRadius+1e-9 {
			t.Fatalf("the wander left the floor at step %d: %+v", i, p)
		}
	}
}
