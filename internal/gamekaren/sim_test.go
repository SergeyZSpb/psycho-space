package gamekaren

import (
	"math"
	"math/rand/v2"
	"testing"
)

// The simulation is pure, so every test here is arithmetic: no office, no
// socket, no goroutine and no clock.

const testDt = 1.0 / SimHz

func TestWalkingMovesAtWalkSpeed(t *testing.T) {
	p := NewPlayer()
	p.Pos = Vec2{X: 8, Y: 8}
	got := Step(nil, p, Command{Dt: 0.5, MX: 1})
	if want := 8 + WalkSpeed*0.5; math.Abs(got.Pos.X-want) > 1e-9 {
		t.Fatalf("half a second of walking put him at %v, want %v", got.Pos.X, want)
	}
	if got.Pos.Y != 8 {
		t.Fatalf("walking along +X moved him in Y: %v", got.Pos.Y)
	}
}

func TestADiagonalIsNotFasterThanAStraightLine(t *testing.T) {
	// The stick is the only control on a phone, so a player holding it into a
	// corner must not outrun one holding it sideways. Sanitise is what
	// normalises it, which is why this test goes through Sanitise rather than
	// straight into Step — the office does the same.
	p := NewPlayer()
	p.Pos = Vec2{X: 8, Y: 8}

	straight := Step(nil, p, Sanitise(Command{Dt: 0.5, MX: 1}))
	diagonal := Step(nil, p, Sanitise(Command{Dt: 0.5, MX: 1, MY: 1}))

	ds := math.Hypot(straight.Pos.X-8, straight.Pos.Y-8)
	dd := math.Hypot(diagonal.Pos.X-8, diagonal.Pos.Y-8)
	if dd > ds+1e-9 {
		t.Fatalf("the diagonal covered %v against the straight line's %v", dd, ds)
	}
}

func TestAHalfPressedStickStaysAnalogue(t *testing.T) {
	// Normalising only when the vector is longer than one is what keeps a gentle
	// nudge gentle. A stick that snapped to full speed would make standing still
	// on a phone almost impossible, which is the entire game.
	p := NewPlayer()
	p.Pos = Vec2{X: 8, Y: 8}
	got := Step(nil, p, Sanitise(Command{Dt: MaxStepSeconds, MX: 0.5}))
	if want := 8 + WalkSpeed*0.5*MaxStepSeconds; math.Abs(got.Pos.X-want) > 1e-9 {
		t.Fatalf("a half-pressed stick moved him to %v, want %v", got.Pos.X, want)
	}
}

func TestNobodyEverLeavesTheFloorOrEndsInsideADesk(t *testing.T) {
	// Ten thousand hostile commands, seeded so a failure is reproducible. This is
	// the test that would catch a push-out that shoves somebody through a wall,
	// which is why content_test also pins the clearance that makes it impossible.
	r := rand.New(rand.NewPCG(1, 2))
	p := NewPlayer()
	for i := 0; i < 10000; i++ {
		c := Sanitise(Command{
			Dt:   r.Float64() * 2,   // beyond MaxStepSeconds on purpose
			MX:   r.Float64()*6 - 3, // beyond ±1 on purpose
			MY:   r.Float64()*6 - 3, //
			Dash: r.IntN(10) == 0,   //
		})
		p = Step(Desks, p, c)
		if p.Pos.X < PlayerRadius-1e-9 || p.Pos.X > OfficeW-PlayerRadius+1e-9 ||
			p.Pos.Y < PlayerRadius-1e-9 || p.Pos.Y > OfficeH-PlayerRadius+1e-9 {
			t.Fatalf("step %d left the floor at %+v", i, p.Pos)
		}
		for j, d := range Desks {
			if insideRect(d, p.Pos, PlayerRadius) {
				t.Fatalf("step %d ended inside desk %d at %+v", i, j, p.Pos)
			}
		}
	}
}

// insideRect reports whether a disc's centre is strictly inside a desk expanded
// by its radius — the condition the resolver exists to make false.
func insideRect(d Rect, p Vec2, r float64) bool {
	const eps = 1e-9
	return p.X > d.X-r+eps && p.X < d.X+d.W+r-eps &&
		p.Y > d.Y-r+eps && p.Y < d.Y+d.H+r-eps
}

func TestADeskIsPushedOutOfAlongTheShortestAxis(t *testing.T) {
	// Walking into the top edge of a desk puts you back on top of it, not around
	// the side. That is what makes furniture read as furniture rather than as an
	// invisible current.
	d := Desks[0]
	p := NewPlayer()
	p.Pos = Vec2{X: d.X + d.W/2, Y: d.Y - PlayerRadius - 0.01}
	got := Step([]Rect{d}, p, Command{Dt: 0.1, MY: 1})
	if math.Abs(got.Pos.Y-(d.Y-PlayerRadius)) > 1e-9 {
		t.Fatalf("pushed to Y=%v, want %v", got.Pos.Y, d.Y-PlayerRadius)
	}
	if math.Abs(got.Pos.X-(d.X+d.W/2)) > 1e-9 {
		t.Fatalf("the push moved him sideways to X=%v", got.Pos.X)
	}
}

func TestSanitiseClampsEveryAttackerControlledField(t *testing.T) {
	got := Sanitise(Command{Dt: 1000, MX: 50, MY: -50})
	if got.Dt != MaxStepSeconds {
		t.Fatalf("dt survived as %v", got.Dt)
	}
	if mag := math.Hypot(got.MX, got.MY); mag > 1+1e-9 {
		t.Fatalf("the movement vector survived at magnitude %v: %+v", mag, got)
	}

	if got := Sanitise(Command{Dt: -5}); got.Dt != 0 {
		t.Fatalf("a negative dt survived as %v", got.Dt)
	}

	nan := Sanitise(Command{Dt: math.NaN(), MX: math.NaN(), MY: math.Inf(1)})
	if math.IsNaN(nan.Dt) || math.IsNaN(nan.MX) || math.IsNaN(nan.MY) ||
		math.IsInf(nan.MX, 0) || math.IsInf(nan.MY, 0) {
		t.Fatalf("a NaN or an infinity reached the simulation: %+v", nan)
	}

	// The sequence and the dash request are not clamped — one is an opaque
	// counter and the other is a boolean the simulation judges for itself.
	if got := Sanitise(Command{Seq: 99, Dash: true, Dt: 0.05}); got.Seq != 99 || !got.Dash {
		t.Fatalf("sanitising rewrote intent: %+v", got)
	}
}

func TestStepIsDeterministic(t *testing.T) {
	// No clock, no RNG, no map iteration. If this ever fails, the TypeScript
	// port cannot exist and neither can the golden vectors.
	r := rand.New(rand.NewPCG(9, 9))
	cmds := make([]Command, 400)
	for i := range cmds {
		cmds[i] = Sanitise(Command{
			Dt:   testDt,
			MX:   r.Float64()*2 - 1,
			MY:   r.Float64()*2 - 1,
			Dash: r.IntN(20) == 0,
		})
	}
	run := func() Player {
		p := NewPlayer()
		for _, c := range cmds {
			p = Step(Desks, p, c)
		}
		return p
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("two identical runs diverged:\n%+v\n%+v", a, b)
	}
}

func TestStepDoesNotResurrectAnybody(t *testing.T) {
	// Alive is the office's business. Step carries it and never changes it,
	// which is what lets a caught player be stepped one last time on the tick
	// they are removed without coming back to life.
	p := NewPlayer()
	p.Alive = false
	if Step(Desks, p, Command{Dt: testDt, MX: 1}).Alive {
		t.Fatal("stepping a dead player brought him back")
	}
}
