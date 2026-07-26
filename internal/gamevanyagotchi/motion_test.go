package gamevanyagotchi

import (
	"math"
	"testing"
	"time"
)

// The arithmetic the whole yard rests on.
//
// One rule governs motion.go — POSITION IS A FUNCTION OF ABSOLUTE TIME, never an
// accumulation of steps — and almost every property below is a consequence of
// it. That rule is what makes a tick that is late, early, skipped or duplicated
// harmless, and it is not something a reader can confirm by looking: an
// implementation that quietly remembered its last answer would produce a plane
// that looks perfectly plausible and drifts apart between two players over an
// afternoon. So it is asserted directly, by asking the same question twice with
// a hundred other questions in between.
//
// Nothing here needs a service, a socket or a clock seam: every function in
// motion.go is pure, which is precisely why the design is worth having and why
// this file is a table of instants rather than a harness.

// Fixtures the tests below share. They are deliberately NOT the catalogue's own
// characters — content.go's numbers are meant to be moved by feel, and a test
// pinned to them would report every retune as a regression. The catalogue's
// entries are checked for their own invariants in content_test.go.
var (
	// A wanderer whose box sits comfortably inside the plane, so an excursion
	// outside Spread is a real one rather than something the clamp swallowed.
	testWander = MotionParams{
		Home:   Point{X: 0.5, Y: 0.5},
		Spread: Point{X: 0.3, Y: 0.2},
		Period: 90 * time.Second,
	}
	// A four-cornered patrol with no phase offset, so a corner is reached at a
	// fraction of the period a test can work out on paper.
	testPatrol = MotionParams{
		Home:   Point{X: 0.5, Y: 0.5},
		Period: 40 * time.Second,
		Route: []Point{
			{X: 0.10, Y: 0.20},
			{X: 0.70, Y: 0.25},
			{X: 0.80, Y: 0.90},
			{X: 0.15, Y: 0.85},
		},
	}
	// An idler standing somewhere that is not the spawn, so "he is at Home" and
	// "the clamp gave up and returned the spawn" cannot be confused.
	testIdle = MotionParams{Home: Point{X: 0.25, Y: 0.75}}
)

// sweepOfInstants is the range of elapsed times every pattern is asked about.
//
// It is deliberately not a tidy ramp. Negative values are reachable in
// production — worldEpoch is a fixed date and a box whose clock is a minute
// behind it evaluates every character before the world began — and the huge ones
// are what a process that is still running in a century would ask for. The prime
// -ish steps are there so a sample never lands only on the neat fractions of a
// period, which is exactly where a broken pattern would look fine.
func sweepOfInstants() []time.Duration {
	out := []time.Duration{
		time.Duration(math.MinInt64), -365 * 24 * time.Hour, -97 * time.Second, -time.Nanosecond,
		0, time.Nanosecond, time.Millisecond, 997 * time.Millisecond,
		time.Second, 13 * time.Second, 41 * time.Second, 97 * time.Second,
		time.Hour, 24 * time.Hour, 365 * 24 * time.Hour, time.Duration(math.MaxInt64),
	}
	// Plus a fine ramp across a couple of the fixture periods, so the sweep also
	// covers the ordinary case densely rather than only its extremes.
	for i := 0; i < 500; i++ {
		out = append(out, time.Duration(i)*373*time.Millisecond)
	}
	return out
}

// onThePlane fails unless p is a position a client could actually draw.
//
// The plane is 0..1 on both axes and nothing else is renderable: a NaN puts a
// CSS custom property into a state the browser resolves to nothing, and a
// coordinate outside the unit square puts a character through the fence. Both
// are the clamp's job, and both are silent if it stops doing it.
func onThePlane(t *testing.T, p Point, what string) {
	t.Helper()
	if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
		t.Fatalf("%s is at (%v,%v), which is not a number; the clamp is meant to refuse these outright", what, p.X, p.Y)
	}
	if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
		t.Fatalf("%s is at (%v,%v), off the plane; a character with silly parameters walks into the fence, not off the screen",
			what, p.X, p.Y)
	}
}

// samePoint reports whether two positions are the same to within the last bit or
// two of a float64.
//
// Positions arrive by interpolation — from + (to − from) × f — and IEEE-754 does
// not promise that lands bit-identically on `to` even when f is exactly 1. An
// exact comparison would therefore be asserting a property of the floating-point
// unit rather than of the game.
func samePoint(a, b Point) bool { return math.Abs(a.X-b.X) < 1e-9 && math.Abs(a.Y-b.Y) < 1e-9 }

// TestEveryPatternIsAPureFunctionOfTheClock is the property the entire design
// rests on, and the one that cannot be seen by reading the code once.
//
// Because a position depends only on the instant, a tick that is late, early,
// skipped or duplicated produces the same correct world, a GC pause costs
// nothing, and a client that has just reconnected is shown exactly what everybody
// else is looking at. An implementation that accumulated — a velocity applied per
// tick, a remembered destination, a cached last answer — would satisfy every
// other test in this file while drifting the yard apart over an afternoon, with
// nothing able to notice.
//
// So the same instant is asked about twice with a hundred other instants asked in
// between, and the answers are compared bit for bit. Nothing but genuine
// statelessness passes that.
func TestEveryPatternIsAPureFunctionOfTheClock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    PatternKey
		params MotionParams
	}{
		{name: "idle", key: PatternIdle, params: testIdle},
		{name: "wander", key: PatternWander, params: testWander},
		{name: "patrol", key: PatternPatrol, params: testPatrol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instants := sweepOfInstants()
			first := make([]Point, len(instants))
			for i, d := range instants {
				first[i] = evaluate(tc.key, tc.params, d)
			}

			// Asked again immediately: the plainest form of the property.
			for i, d := range instants {
				if got := evaluate(tc.key, tc.params, d); got != first[i] {
					t.Fatalf("evaluating %v twice in a row gave (%v,%v) then (%v,%v); the answer depends on something other than the clock",
						d, first[i].X, first[i].Y, got.X, got.Y)
				}
			}

			// Asked again after the evaluator has been all over the timeline —
			// backwards, forwards, and past the ends. This is the tick that was
			// skipped, and the tick that arrived twice.
			for i, d := range instants {
				for _, other := range instants {
					_ = evaluate(tc.key, tc.params, other)
				}
				if got := evaluate(tc.key, tc.params, d); got != first[i] {
					t.Fatalf("evaluating %v after %d other instants gave (%v,%v); it first gave (%v,%v) — the pattern is remembering something",
						d, len(instants), got.X, got.Y, first[i].X, first[i].Y)
				}
				if i > 20 {
					break // twenty full passes is enough; the rest is the same claim.
				}
			}
		})
	}
}

// TestEveryPatternStaysOnThePlaneWhateverItIsGiven covers the half of the
// catalogue that has no compiler.
//
// Params are hand-written content: a spread of 40, a period somebody typed as
// negative, a patrol whose route was emptied while the entry was being edited, a
// pattern key renamed in the table and not at the call site. None of those is a
// build error and none of them should be able to put a character off the screen,
// into a NaN, or into a panic — a broken content entry is a character standing
// somewhere silly, which is legible, rather than a plane that fails to render.
func TestEveryPatternStaysOnThePlaneWhateverItIsGiven(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	for _, tc := range []struct {
		name   string
		key    PatternKey
		params MotionParams
	}{
		{name: "the ordinary idler", key: PatternIdle, params: testIdle},
		{name: "the ordinary wanderer", key: PatternWander, params: testWander},
		{name: "the ordinary patrol", key: PatternPatrol, params: testPatrol},
		{
			name:   "a wanderer with an absurd spread",
			key:    PatternWander,
			params: MotionParams{Home: Point{X: 0.5, Y: 0.5}, Spread: Point{X: 1e6, Y: 1e6}, Period: time.Minute},
		},
		{
			name:   "a wanderer with a negative period",
			key:    PatternWander,
			params: MotionParams{Home: Point{X: 0.4, Y: 0.6}, Spread: Point{X: 0.2, Y: 0.2}, Period: -5 * time.Second},
		},
		{
			name:   "a wanderer with no period at all",
			key:    PatternWander,
			params: MotionParams{Home: Point{X: 0.4, Y: 0.6}, Spread: Point{X: 0.2, Y: 0.2}},
		},
		{
			name:   "a wanderer whose home is off the plane",
			key:    PatternWander,
			params: MotionParams{Home: Point{X: -3, Y: 7}, Spread: Point{X: 0.2, Y: 0.2}, Period: time.Minute},
		},
		{
			name:   "a wanderer whose home is not a number",
			key:    PatternWander,
			params: MotionParams{Home: Point{X: nan, Y: 0.5}, Spread: Point{X: 0.2, Y: 0.2}, Period: time.Minute},
		},
		{
			name:   "a wanderer whose spread is not a number",
			key:    PatternWander,
			params: MotionParams{Home: Point{X: 0.5, Y: 0.5}, Spread: Point{X: inf, Y: nan}, Period: time.Minute},
		},
		{
			name:   "a patrol with an empty route",
			key:    PatternPatrol,
			params: MotionParams{Home: Point{X: 0.3, Y: 0.3}, Period: time.Minute},
		},
		{
			name:   "a patrol with a single point",
			key:    PatternPatrol,
			params: MotionParams{Home: Point{X: 0.3, Y: 0.3}, Period: time.Minute, Route: []Point{{X: 0.9, Y: 0.1}}},
		},
		{
			name: "a patrol with no period",
			key:  PatternPatrol,
			params: MotionParams{
				Home:  Point{X: 0.3, Y: 0.3},
				Route: []Point{{X: 0.1, Y: 0.1}, {X: 0.9, Y: 0.9}},
			},
		},
		{
			name: "a patrol whose route leaves the plane",
			key:  PatternPatrol,
			params: MotionParams{
				Home:   Point{X: 0.3, Y: 0.3},
				Period: 30 * time.Second,
				Route:  []Point{{X: -5, Y: 0.1}, {X: 0.5, Y: 40}, {X: 0.2, Y: -0.2}},
			},
		},
		{
			name: "a patrol whose route is not numbers",
			key:  PatternPatrol,
			params: MotionParams{
				Home:   Point{X: 0.3, Y: 0.3},
				Period: 30 * time.Second,
				Route:  []Point{{X: nan, Y: 0.1}, {X: 0.5, Y: inf}},
			},
		},
		{
			name:   "an idler standing nowhere real",
			key:    PatternIdle,
			params: MotionParams{Home: Point{X: inf, Y: -inf}},
		},
		{
			name:   "a pattern key nobody wrote",
			key:    "no-such-pattern",
			params: testWander,
		},
		{
			name:   "no pattern key at all",
			key:    "",
			params: MotionParams{Home: Point{X: 0.6, Y: 0.4}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range sweepOfInstants() {
				onThePlane(t, evaluate(tc.key, tc.params, d), tc.name+" at "+d.String())
			}
		})
	}
}

// TestAnUnknownPatternParksTheCharacterAtHome pins the shape of that failure
// rather than only its safety.
//
// A catalogue naming a pattern nobody wrote is a content bug, and the honest
// rendering of one is a character standing still at the place the entry says he
// belongs — not a missing character, not a crashed tick, and not the middle of
// the yard, which would be indistinguishable from a real position.
func TestAnUnknownPatternParksTheCharacterAtHome(t *testing.T) {
	params := MotionParams{Home: Point{X: 0.2, Y: 0.9}, Spread: Point{X: 0.3, Y: 0.3}, Period: time.Minute}
	if params.Home == spawn {
		t.Fatal("this fixture stands at the spawn, so parking at Home and falling back to the spawn would look the same")
	}
	for _, d := range sweepOfInstants() {
		if got := evaluate("не-существует", params, d); got != params.Home {
			t.Fatalf("an unknown pattern put the character at (%v,%v) after %v; want his home (%v,%v)",
				got.X, got.Y, d, params.Home.X, params.Home.Y)
		}
	}
}

// TestAnIdlerNeverMoves is the boring one, and it is the guard against the
// pattern table being wired up wrongly: the beer vendor standing at his crate is
// what the whole yard is arranged around, and an idler that drifted by a
// thousandth an hour would be noticed by nobody until his stall was in the road.
func TestAnIdlerNeverMoves(t *testing.T) {
	for _, d := range sweepOfInstants() {
		if got := evaluate(PatternIdle, testIdle, d); got != testIdle.Home {
			t.Fatalf("the idler is at (%v,%v) after %v; want his home (%v,%v) at every instant there has ever been",
				got.X, got.Y, d, testIdle.Home.X, testIdle.Home.Y)
		}
	}
}

// TestAWandererActuallyWandersAndStaysNearHome is both halves of "ambles about",
// and they fail for opposite reasons.
//
// A wanderer that does not move is a character the pattern table describes and
// does not deliver — and it would pass every determinism and every stays-on-the
// -plane test in this file, because a constant is the most deterministic thing
// there is. A wanderer that strays further than its Spread is worse than useless:
// Spread is how content says "he belongs in this part of the yard", so an
// excursion past it walks him through the beer stall, and the plane's clamp would
// hide it by pinning him to an edge.
func TestAWandererActuallyWandersAndStaysNearHome(t *testing.T) {
	const samples = 2000
	step := testWander.Period / samples

	var moved bool
	var farthestX, farthestY float64
	first := evaluate(PatternWander, testWander, 0)
	for i := 0; i < samples; i++ {
		at := evaluate(PatternWander, testWander, time.Duration(i)*step)
		onThePlane(t, at, "the wanderer")
		if at != first {
			moved = true
		}
		dx, dy := math.Abs(at.X-testWander.Home.X), math.Abs(at.Y-testWander.Home.Y)
		farthestX, farthestY = math.Max(farthestX, dx), math.Max(farthestY, dy)
	}

	if !moved {
		t.Fatal("the wanderer stood still for a whole period; a constant satisfies every other property in this file and is not a character ambling about")
	}
	if farthestX > testWander.Spread.X+1e-9 || farthestY > testWander.Spread.Y+1e-9 {
		t.Fatalf("the wanderer strayed (%v,%v) from home; Spread is (%v,%v) and is how content says which part of the yard he belongs in",
			farthestX, farthestY, testWander.Spread.X, testWander.Spread.Y)
	}
	// And he uses the room he is given: a wanderer that twitched by a thousandth
	// would satisfy both checks above while looking like an idler on screen.
	if farthestX < testWander.Spread.X/2 || farthestY < testWander.Spread.Y/2 {
		t.Fatalf("the wanderer got no further than (%v,%v) from home in a whole period; his spread is (%v,%v), so he is barely moving",
			farthestX, farthestY, testWander.Spread.X, testWander.Spread.Y)
	}
}

// TestAPatrolVisitsEveryCornerAndPausesAtEachOne is the joke the pattern exists
// for: a ballerina doing the same four steps forever.
//
// Two properties, and the pause is the one worth the trouble. Without it a patrol
// slides around its route on rails, which reads as machinery rather than as
// somebody walking; with it, the character arrives, stands for a moment, and goes
// on. The dwell is a fraction inside patrolAt rather than a catalogue number, so
// this test detects it generically — a run of consecutive samples that are the
// same point — instead of computing where it ought to be from the constant.
func TestAPatrolVisitsEveryCornerAndPausesAtEachOne(t *testing.T) {
	if len(testPatrol.Route) < 2 {
		t.Fatal("this fixture has fewer than two corners; there is no circuit to walk")
	}
	const samples = 4000
	step := testPatrol.Period / samples

	at := make([]Point, samples)
	for i := range at {
		at[i] = evaluate(PatternPatrol, testPatrol, time.Duration(i)*step)
		onThePlane(t, at[i], "the patrol")
	}

	for k, corner := range testPatrol.Route {
		visited, paused := false, false
		for i := 0; i < len(at)-1; i++ {
			if !samePoint(at[i], corner) {
				continue
			}
			visited = true
			// Two consecutive samples in the same place. The character covers
			// roughly a hundredth of a leg between samples when he is walking, so
			// standing still for a whole sample is a pause and not rounding.
			if at[i] == at[i+1] {
				paused = true
				break
			}
		}
		if !visited {
			t.Errorf("corner %d (%v,%v) is never reached in a whole circuit; the route describes a place the character does not go",
				k, corner.X, corner.Y)
			continue
		}
		if !paused {
			t.Errorf("corner %d (%v,%v) is passed through without stopping; the pause is what makes a patrol read as somebody walking rather than something on rails",
				k, corner.X, corner.Y)
		}
	}

	// And it is a circuit rather than a place: a patrol that never left its first
	// corner would satisfy "visited" for that corner and nothing else, but the
	// check above only errors, so this is the guard that the whole thing moves.
	if at[0] == at[len(at)/3] && at[0] == at[2*len(at)/3] {
		t.Fatal("the patrol is in the same place a third and two thirds of the way round; it is not walking anywhere")
	}
}

// TestAPatrolIsTheSameCircuitEveryTimeRound is the closed-form claim stated as
// something a reader can check: the position a whole period later is the position
// now.
//
// It matters because the leg is indexed by arithmetic on the clock rather than by
// walking the polyline. An off-by-one in that indexing — a leg that wraps to the
// wrong corner, a cycle that does not close — shows up as a character who
// gradually falls out of step with himself, which nobody would spot by watching.
func TestAPatrolIsTheSameCircuitEveryTimeRound(t *testing.T) {
	for _, d := range []time.Duration{0, time.Second, 7 * time.Second, 19*time.Second + 500*time.Millisecond} {
		now := evaluate(PatternPatrol, testPatrol, d)
		for _, rounds := range []int{1, 2, 17, 1000} {
			later := evaluate(PatternPatrol, testPatrol, d+time.Duration(rounds)*testPatrol.Period)
			if !samePoint(now, later) {
				t.Fatalf("%v into the circuit he is at (%v,%v), but %d circuits later at (%v,%v); the cycle does not close",
					d, now.X, now.Y, rounds, later.X, later.Y)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The player's walk, on the same principle.
// ---------------------------------------------------------------------------

// walkFixture is a journey with numbers chosen so the arithmetic is legible: the
// straight line between the two points is exactly one plane width, so it takes
// exactly 1/walkSpeed seconds and every fraction of it is a round number.
func walkFixture(startedAt time.Time, stopAt float64) walk {
	return walk{
		from:      Point{X: 0.1, Y: 0.2},
		to:        Point{X: 0.9, Y: 0.8},
		startedAt: startedAt,
		stopAt:    stopAt,
	}
}

// walkStart is the instant every walk fixture below begins. Fixed, so a failure
// reads the same on every run.
var walkStart = time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)

// crossing is how long the fixture's journey takes at walkSpeed.
var crossing = time.Duration(distance(Point{X: 0.1, Y: 0.2}, Point{X: 0.9, Y: 0.8}) / walkSpeed * float64(time.Second))

// TestAWalkIsWhereHeIsAtEveryInstantAlongIt is the walk stated as a table of
// instants, which is the whole of what the plane asks of it.
//
// The row before the start is not a curiosity. The broadcast's clock is injected
// and a tap's clock is time.Now(), so a tick genuinely can carry an instant
// earlier than the walk it is evaluating — and the honest answer there is "he has
// not set off yet", not a negative fraction of a journey.
func TestAWalkIsWhereHeIsAtEveryInstantAlongIt(t *testing.T) {
	w := walkFixture(walkStart, 1)
	mid := Point{X: (w.from.X + w.to.X) / 2, Y: (w.from.Y + w.to.Y) / 2}

	for _, tc := range []struct {
		name     string
		when     time.Time
		want     Point
		progress float64
		arrived  bool
		why      string
	}{
		{
			name: "before he was even asked", when: walkStart.Add(-time.Hour),
			want: w.from, progress: 0, arrived: false,
			why: "the tick's clock and the tap's clock are two different clocks; a tick from before the tap must not run the journey backwards",
		},
		{
			name: "the instant of the tap", when: walkStart,
			want: w.from, progress: 0, arrived: false,
			why: "a tap is a journey, not a teleport: at the moment it is accepted he is still where he was standing",
		},
		{
			name: "a quarter of the way", when: walkStart.Add(crossing / 4),
			want: lerp(w.from, w.to, 0.25), progress: 0.25, arrived: false,
			why: "progress is linear in time, which is what makes distance mean something",
		},
		{
			name: "halfway", when: walkStart.Add(crossing / 2),
			want: mid, progress: 0.5, arrived: false,
			why: "the midpoint in time is the midpoint in space",
		},
		{
			name: "the moment he arrives", when: walkStart.Add(crossing),
			want: w.to, progress: 1, arrived: true,
			why: "the journey is over exactly when the distance divided by the speed says it is",
		},
		{
			name: "an hour after he arrives", when: walkStart.Add(time.Hour),
			want: w.to, progress: 1, arrived: true,
			why: "a finished walk is simply standing somewhere; it does not keep going",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.at(tc.when); !samePoint(got, tc.want) {
				t.Errorf("at %s he is at (%v,%v); want (%v,%v) — %s", tc.name, got.X, got.Y, tc.want.X, tc.want.Y, tc.why)
			}
			if got := w.progress(tc.when); math.Abs(got-tc.progress) > 1e-9 {
				t.Errorf("progress %s = %v; want %v — %s", tc.name, got, tc.progress, tc.why)
			}
			if got := w.arrived(tc.when); got != tc.arrived {
				t.Errorf("arrived %s = %v; want %v — %s", tc.name, got, tc.arrived, tc.why)
			}
		})
	}

	// arrived and at have to agree, or the yard would draw somebody walking who
	// the rest of the service believes has stopped. Checked across the journey
	// rather than at the two ends, because an off-by-one at the boundary is
	// exactly the kind of disagreement that only shows up on one frame in a
	// hundred.
	for i := 0; i <= 100; i++ {
		when := walkStart.Add(time.Duration(i) * crossing / 100)
		if w.arrived(when) != samePoint(w.at(when), w.to) {
			t.Fatalf("%d%% of the way along he is at (%v,%v) and arrived reports %v; the two disagree",
				i, w.at(when).X, w.at(when).Y, w.arrived(when))
		}
	}
}

// TestAWalkHeGivesUpOnStopsShortAndStaysThere is the tiredness the whole feature
// exists for, seen from the arithmetic rather than from the roster.
//
// He must stop at the fraction the server decided and STAY there — not creep on,
// not snap to the destination a frame later, not start again. Everybody is
// watching the same walk, so a Ваня who kept moving after giving up would sit
// down in a different place on every screen.
func TestAWalkHeGivesUpOnStopsShortAndStaysThere(t *testing.T) {
	const stopAt = 0.4
	w := walkFixture(walkStart, stopAt)
	sat := lerp(w.from, w.to, stopAt)
	stopped := walkStart.Add(time.Duration(stopAt * float64(crossing)))

	if !w.stoppedAt().Equal(stopped) {
		t.Fatalf("he stopped at %v; want %v — the instant is the distance he covered divided by the speed",
			w.stoppedAt().UTC(), stopped.UTC())
	}
	// The boundary itself is deliberately approached from a millisecond either
	// side rather than asserted to the nanosecond. stoppedAt is a float division
	// truncated to whole nanoseconds, so whether the very instant it names counts
	// as arrived depends on the last bit of the distance — which is a property of
	// the floating-point unit, not of the game, and is invisible against a tick
	// that comes round every 200 ms.

	// Still walking right up to the moment he sits down.
	for _, before := range []time.Duration{0, crossing / 8, crossing/4 - time.Millisecond} {
		when := walkStart.Add(before)
		if w.arrived(when) {
			t.Fatalf("%v in he has already given up; he does not stop until %v", before, stopped.UTC())
		}
		if samePoint(w.at(when), sat) && before > 0 {
			t.Fatalf("%v in he is already sitting at (%v,%v); the journey up to that point is a walk like any other", before, sat.X, sat.Y)
		}
	}
	// And from the moment he stops, forever.
	for _, after := range []time.Duration{time.Millisecond, tiredFor, time.Hour, 365 * 24 * time.Hour} {
		when := stopped.Add(after)
		if got := w.at(when); !samePoint(got, sat) {
			t.Fatalf("%v after giving up he is at (%v,%v); want the spot he sat down in (%v,%v)", after, got.X, got.Y, sat.X, sat.Y)
		}
		if !w.arrived(when) {
			t.Fatalf("%v after giving up the journey is still reported as running", after)
		}
	}
	// He must not reach the destination — a stopAt that was ignored would land him
	// there and every other assertion above would still hold.
	if samePoint(sat, w.to) {
		t.Fatal("giving up put him exactly where he was heading; stopAt did nothing")
	}
}

// TestHeSaysHeIsTiredOnlyWhileHeIsSittingThere pins the window «устал» is shown
// for, which is derived rather than timed.
//
// There is no timer and nothing to clean up: the line stops being true because
// the arithmetic says so. That is worth a test of its own precisely because
// nothing else in the system would notice it never stopping — a Ваня permanently
// announcing that he is tired looks like content, not like a bug.
func TestHeSaysHeIsTiredOnlyWhileHeIsSittingThere(t *testing.T) {
	const stopAt = 0.6
	w := walkFixture(walkStart, stopAt)
	stopped := w.stoppedAt()

	for _, tc := range []struct {
		name string
		when time.Time
		want bool
		why  string
	}{
		{name: "on the way", when: walkStart.Add(crossing / 4), want: false,
			why: "he has not given up yet; he is walking"},
		{name: "a moment before he stops", when: stopped.Add(-time.Millisecond), want: false,
			why: "the line belongs to the sitting down, not to the journey"},
		// A millisecond after rather than exactly on: stoppedAt is a float
		// division truncated to whole nanoseconds, so the instant it names sits on
		// the last bit of the distance. Asserting on it would be asserting a
		// property of the floating-point unit, and the tick comes round every
		// 200 ms in any case.
		{name: "the moment he stops", when: stopped.Add(time.Millisecond), want: true,
			why: "this is the whole point: a limitation turned into content"},
		{name: "most of the way through the breather", when: stopped.Add(tiredFor / 2), want: true,
			why: "he is still sitting there"},
		{name: "the moment the breather ends", when: stopped.Add(tiredFor), want: false,
			why: "derived, so it needs no timer and no cleanup — it simply stops being true"},
		{name: "long afterwards", when: stopped.Add(time.Hour), want: false,
			why: "a Ваня permanently announcing that he is tired reads as content rather than as a bug"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.gaveUp(tc.when); got != tc.want {
				t.Fatalf("gaveUp %s = %v, want %v — %s", tc.name, got, tc.want, tc.why)
			}
		})
	}

	// A walk he completed never says it, at any instant at all. Without this the
	// window above could be satisfied by a walk that reported tiredness whenever
	// it had just finished, which is every arrival.
	done := walkFixture(walkStart, 1)
	for _, d := range []time.Duration{-time.Hour, 0, crossing / 2, crossing, crossing + tiredFor/2, time.Hour} {
		if done.gaveUp(walkStart.Add(d)) {
			t.Fatalf("a walk he finished claims he gave up, %v in", d)
		}
	}
}

// TestAJourneyToWhereHeAlreadyStandsDividesByNothing is the degenerate case, and
// it is reachable in the ordinary way: tapping the spot you are already standing
// on, or a placement built by standing().
//
// The whole of the walk's arithmetic is "distance divided by speed", so a
// distance of zero is where an unguarded implementation produces a NaN — and a
// NaN position is not a wrong place, it is a character the client cannot draw at
// all.
func TestAJourneyToWhereHeAlreadyStandsDividesByNothing(t *testing.T) {
	here := Point{X: 0.3, Y: 0.7}
	for _, tc := range []struct {
		name string
		w    walk
	}{
		{name: "standing", w: standing(here)},
		{name: "a tap on the spot he is on", w: walk{from: here, to: here, startedAt: walkStart, stopAt: 1}},
		{name: "one he would have given up on", w: walk{from: here, to: here, startedAt: walkStart, stopAt: 0.5}},
		{name: "the zero value", w: walk{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range []time.Duration{-time.Hour, 0, time.Millisecond, time.Hour, 365 * 24 * time.Hour} {
				when := walkStart.Add(d)
				at := tc.w.at(when)
				onThePlane(t, at, tc.name)
				if p := tc.w.progress(when); math.IsNaN(p) || math.IsInf(p, 0) {
					t.Fatalf("progress %v in is %v; a journey of no length must not divide by its own length", d, p)
				}
				if tc.w.stopAt >= 1 && tc.w.gaveUp(when) {
					t.Fatalf("%v in, a completed journey of no length claims he gave up", d)
				}
			}
			// He is exactly where he was: a zero-length journey moves nobody.
			if tc.name != "the zero value" && tc.w.at(walkStart.Add(time.Hour)) != here {
				t.Fatalf("a journey to where he already stands moved him to (%v,%v)",
					tc.w.at(walkStart.Add(time.Hour)).X, tc.w.at(walkStart.Add(time.Hour)).Y)
			}
			// And the instant he "stopped" is the instant he started, which is what
			// keeps stoppedAt out of the NaN business too.
			if !tc.w.stoppedAt().Equal(tc.w.startedAt) {
				t.Fatalf("stoppedAt = %v; want the instant he started, %v", tc.w.stoppedAt(), tc.w.startedAt)
			}
		})
	}
	// NOTE for whoever reads this next: arrived() is false for a zero-length walk
	// with stopAt 1, because progress is defined as 0 there and 0 >= 1 is false.
	// It has no consequence today — gaveUp is arrived's only caller and it is
	// gated on stopAt < 1 first — so it is deliberately not asserted either way
	// rather than being pinned as if it were intended.
}

// TestTheGeometryUnderneathAgreesAboutThePlane covers the three helpers every
// position in this game passes through, at the inputs that would otherwise reach
// them unnoticed.
//
// clampPoint is the one that matters most: it is the last thing between a
// computed position and a CSS custom property, and its refusal of non-finite
// values is a deliberate choice rather than an oversight — a NaN is not an
// out-of-range position, it is a broken client or a broken content entry, and
// mapping it onto an edge of the plane would hide both.
func TestTheGeometryUnderneathAgreesAboutThePlane(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)

	t.Run("distance", func(t *testing.T) {
		here := Point{X: 0.25, Y: 0.75}
		if d := distance(here, here); d != 0 {
			t.Errorf("the distance from a place to itself is %v; want 0", d)
		}
		// The corner-to-corner span is what the tiredness roll is scaled against,
		// so the constant and the function have to agree about it.
		if d := distance(Point{X: 0, Y: 0}, Point{X: 1, Y: 1}); d != maxDistance {
			t.Errorf("corner to corner is %v but maxDistance is %v; the tiredness roll is scaled against a length the plane does not have",
				d, maxDistance)
		}
		a, b := Point{X: 0.1, Y: 0.9}, Point{X: 0.8, Y: 0.2}
		if distance(a, b) != distance(b, a) {
			t.Errorf("distance is not symmetric: %v one way and %v the other", distance(a, b), distance(b, a))
		}
		if d := distance(Point{X: 0, Y: 0}, Point{X: 0.6, Y: 0.8}); math.Abs(d-1) > 1e-12 {
			t.Errorf("the 3-4-5 triangle came out at %v; want 1", d)
		}
	})

	t.Run("lerp", func(t *testing.T) {
		a, b := Point{X: 0.2, Y: 0.4}, Point{X: 0.8, Y: 0.6}
		if got := lerp(a, b, 0); got != a {
			t.Errorf("no fraction of the way is (%v,%v); want the start (%v,%v)", got.X, got.Y, a.X, a.Y)
		}
		if got := lerp(a, b, 1); !samePoint(got, b) {
			t.Errorf("all the way is (%v,%v); want the end (%v,%v)", got.X, got.Y, b.X, b.Y)
		}
		if got := lerp(a, b, 0.5); !samePoint(got, Point{X: 0.5, Y: 0.5}) {
			t.Errorf("halfway is (%v,%v); want (0.5,0.5)", got.X, got.Y)
		}
		// Past the end, and back before the start: clamped onto the plane rather
		// than onto the segment, which is the honest reading — the plane is the
		// only thing that is actually a boundary.
		onThePlane(t, lerp(a, b, 4), "a fraction past the end")
		onThePlane(t, lerp(a, b, -4), "a fraction before the start")
		// And a fraction that is not a number cannot produce a position that is
		// not a number.
		onThePlane(t, lerp(a, b, nan), "an unusable fraction")
		onThePlane(t, lerp(Point{X: nan, Y: inf}, b, 0.5), "a journey from nowhere")
	})

	t.Run("clampPoint", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			in   Point
			want Point
		}{
			{name: "already on the plane", in: Point{X: 0.3, Y: 0.6}, want: Point{X: 0.3, Y: 0.6}},
			{name: "the corners themselves", in: Point{X: 0, Y: 1}, want: Point{X: 0, Y: 1}},
			{name: "past the far edge", in: Point{X: 4, Y: 9}, want: Point{X: 1, Y: 1}},
			{name: "behind the origin", in: Point{X: -2, Y: -0.001}, want: Point{X: 0, Y: 0}},
			{name: "one axis out", in: Point{X: 0.5, Y: 12}, want: Point{X: 0.5, Y: 1}},
			{name: "not a number", in: Point{X: nan, Y: 0.5}, want: spawn},
			{name: "infinite", in: Point{X: 0.5, Y: inf}, want: spawn},
			{name: "negative infinity", in: Point{X: -inf, Y: -inf}, want: spawn},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := clampPoint(tc.in); got != tc.want {
					t.Fatalf("clampPoint(%v,%v) = (%v,%v); want (%v,%v)",
						tc.in.X, tc.in.Y, got.X, got.Y, tc.want.X, tc.want.Y)
				}
			})
		}
	})
}
