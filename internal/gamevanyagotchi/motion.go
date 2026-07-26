package gamevanyagotchi

import (
	"math"
	"time"
)

// Everything that moves, and the one rule they all obey: POSITION IS A FUNCTION
// OF ABSOLUTE TIME, never an accumulation of steps.
//
// An NPC's place is `pattern(params, now − epoch)`. A player's place is a point
// along a walk that started at a known instant. Neither integrates a velocity,
// and that is the load-bearing property rather than a stylistic one: because the
// answer depends only on `now`, a tick that is late, early, skipped, duplicated
// or served to a client that has just reconnected still produces the correct
// world. A GC pause costs nothing. If instead each tick advanced everything by a
// velocity, every hiccup would permanently drift the yard, and two players would
// slowly stop seeing the same place.
//
// It is also why NPCs need no rows and no accounts. There is nothing about one
// to store: it is catalogue plus arithmetic, so adding one is a Go-file change
// with no migration and — because the client renders whatever entities it is
// sent — no client deploy either.
//
// The same evaluator serves the player's walk, which is what makes distance
// mean something. Before it, the far side of the plane was 220 ms away and the
// beer store could not be a race to arrive.

// PatternKey names a way of moving.
type PatternKey = string

// The patterns. Two of them arrive together, which is what earns the table
// below: with one, a map would be a map with a single key and a function would
// have done.
const (
	// PatternIdle does not move. The beer vendor stands at his crate.
	PatternIdle PatternKey = "idle"
	// PatternWander ambles about a rectangle — Тунг Тунг Сахур, who is going
	// nowhere in particular.
	PatternWander PatternKey = "wander"
	// PatternPatrol walks a fixed route with a pause at each corner — Балерина
	// Каппучина, whose whole joke is a repeating figure.
	PatternPatrol PatternKey = "patrol"
)

// MotionParams is every number any pattern needs.
//
// One struct rather than a per-pattern type, because a pattern is chosen by a
// catalogue string and the alternative is either an interface per NPC or a JSON
// blob — one of which is the abstraction this design refuses, and the other is
// the untyped column it refuses. Unused fields for a given pattern are simply
// zero, and the pattern's own doc says which it reads.
type MotionParams struct {
	// Home is the centre a wanderer wanders about, and where an idler stands.
	Home Point `json:"home"`
	// Spread is how far a wanderer strays from Home on each axis.
	Spread Point `json:"spread"`
	// Period is how long a full cycle takes. For a patrol it is the whole
	// circuit including the pauses.
	Period time.Duration `json:"period"`
	// Route is the polyline a patroller walks, in order, returning to the first
	// point. Ignored by the other patterns.
	Route []Point `json:"route"`
	// Phase offsets this entity within its cycle so that two NPCs sharing a
	// pattern do not move in lockstep.
	Phase float64 `json:"phase"`
}

// motion is a pattern's evaluator: where this thing is, `elapsed` into the
// world's life.
type motion func(p MotionParams, elapsed time.Duration) Point

// patterns is the table. Adding a way of moving is one function and one entry;
// adding an NPC that moves an existing way is a catalogue entry and no code at
// all, which is what keeps N NPCs × M patterns costing N + M.
var patterns = map[PatternKey]motion{
	PatternIdle:   idleAt,
	PatternWander: wanderAt,
	PatternPatrol: patrolAt,
}

// idleAt stands still.
func idleAt(p MotionParams, _ time.Duration) Point {
	return clampPoint(p.Home)
}

// wanderAt sums two incommensurable sinusoids per axis.
//
// Incommensurable — the ratio is irrational — so the path never repeats exactly
// and reads as aimless rather than as a machine on a loop. Bounded by Spread
// about Home, O(1), and stateless: no destination is ever chosen, so there is
// nothing to remember.
func wanderAt(p MotionParams, elapsed time.Duration) Point {
	if p.Period <= 0 {
		return clampPoint(p.Home)
	}
	t := elapsed.Seconds()/p.Period.Seconds() + p.Phase
	const golden = 1.618033988749895
	x := math.Sin(2*math.Pi*t) + 0.5*math.Sin(2*math.Pi*golden*t)
	y := math.Cos(2*math.Pi*t*golden) + 0.5*math.Cos(2*math.Pi*t)
	// The two sums span ±1.5, so halve them to keep the excursion inside Spread.
	return clampPoint(Point{
		X: p.Home.X + p.Spread.X*x/1.5,
		Y: p.Home.Y + p.Spread.Y*y/1.5,
	})
}

// patrolAt walks a closed route, pausing at each corner.
//
// The leg is indexed by `floor(t / legDuration)`, so this is O(1) rather than a
// walk along the polyline — and, like everything here, a pure function of the
// clock, so nobody has to remember which leg an NPC was on.
func patrolAt(p MotionParams, elapsed time.Duration) Point {
	if len(p.Route) == 0 {
		return clampPoint(p.Home)
	}
	if len(p.Route) == 1 || p.Period <= 0 {
		return clampPoint(p.Route[0])
	}

	legs := float64(len(p.Route))
	// Where we are in the circuit, 0..1, offset by this entity's phase.
	cycle := math.Mod(elapsed.Seconds()/p.Period.Seconds()+p.Phase, 1)
	if cycle < 0 {
		cycle++
	}
	pos := cycle * legs
	if math.IsNaN(pos) || math.IsInf(pos, 0) {
		// A non-finite Phase makes `int(pos)` implementation-defined — a large
		// negative on amd64 — and the index below would then panic rather than
		// clamp. Catalogue input only, so unreachable from a client, but it is
		// the one absurd parameter that crashed instead of parking the character
		// somewhere sensible, and a crash in the broadcast loop is the whole
		// yard rather than one entity.
		return clampPoint(p.Route[0])
	}
	leg := int(pos)
	if leg < 0 {
		leg = 0
	}
	if leg >= len(p.Route) {
		leg = len(p.Route) - 1
	}
	progress := pos - float64(leg)

	// The last fifth of every leg is a pause at the corner, which is what makes
	// a patrol read as deliberate rather than as something sliding on rails.
	const dwell = 0.2
	if progress > 1-dwell {
		progress = 1
	} else {
		progress /= 1 - dwell
	}

	from := p.Route[leg]
	to := p.Route[(leg+1)%len(p.Route)]
	return clampPoint(lerp(from, to, progress))
}

// evaluate places an entity moving under a named pattern.
//
// An unknown pattern key parks it at Home rather than failing: a catalogue that
// names a pattern nobody wrote is a content bug, and the honest rendering of it
// is a character standing still, not a missing character or a crashed tick.
func evaluate(key PatternKey, p MotionParams, elapsed time.Duration) Point {
	if fn, ok := patterns[key]; ok {
		return fn(p, elapsed)
	}
	return clampPoint(p.Home)
}

// ---------------------------------------------------------------------------
// The player's walk, on the same principle.
// ---------------------------------------------------------------------------

// walk is a journey across the plane: where from, where to, when it started,
// and how much of it he actually manages.
//
// Stored in memory as part of a placement, and evaluated on the broadcast tick
// exactly as an NPC's pattern is. A tap while already walking retargets from the
// CURRENT interpolated position rather than from the old destination, so the
// yard feels responsive instead of queued.
type walk struct {
	from      Point
	to        Point
	startedAt time.Time
	// stopAt is the fraction of the journey he completes before sitting down.
	// 1 means he arrives. Decided once, at accept time, by the server — see
	// (*Service).planWalk — so that everybody watches him give up in the same
	// spot at the same moment.
	stopAt float64
	// say is the complaint he makes when he gives up, empty when he arrives.
	//
	// Carried on the walk rather than picked when the frame is built, for the
	// same reason stopAt is: it is one event, so it gets one answer. Re-picking
	// it per tick would riffle through the whole phrase pool at 5 Hz while he
	// sat there catching his breath.
	say string
}

// standing is a walk that is already over: he is simply here.
func standing(at Point) walk {
	return walk{from: at, to: at, stopAt: 1}
}

// at returns where he is now.
func (w walk) at(now time.Time) Point {
	return lerp(w.from, w.to, w.progress(now))
}

// progress is how far along the journey he is, 0..1, capped by stopAt.
func (w walk) progress(now time.Time) float64 {
	if w.stopAt <= 0 {
		return 0
	}
	dist := distance(w.from, w.to)
	if dist <= 0 {
		// A journey of no length is already over, so it is complete rather than
		// unstarted. This used to return 0, which made `arrived` false for
		// anybody merely standing still — harmless while gaveUp was arrived's
		// only caller, because gaveUp checks stopAt < 1 first, and a real bug
		// the moment anything asked "is he still?": the idle chatter went to
		// nobody, since a Ваня who has not yet walked has exactly this walk.
		return w.stopAt
	}
	elapsed := now.Sub(w.startedAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return math.Min(w.stopAt, elapsed*walkSpeed/dist)
}

// arrived reports whether the journey is over, whether or not he got there.
func (w walk) arrived(now time.Time) bool {
	return w.progress(now) >= w.stopAt
}

// gaveUp reports whether he stopped short and is still sitting there getting his
// breath back. Derived, so it needs no timer and no cleanup — it simply stops
// being true.
func (w walk) gaveUp(now time.Time) bool {
	return w.stopAt < 1 && w.arrived(now) && now.Sub(w.stoppedAt()) < tiredFor
}

// stoppedAt is the instant he stopped moving.
func (w walk) stoppedAt() time.Time {
	dist := distance(w.from, w.to)
	if dist <= 0 {
		return w.startedAt
	}
	return w.startedAt.Add(time.Duration(dist * w.stopAt / walkSpeed * float64(time.Second)))
}

// lerp is the point a fraction of the way from a to b.
func lerp(a, b Point, f float64) Point {
	return clampPoint(Point{X: a.X + (b.X-a.X)*f, Y: a.Y + (b.Y-a.Y)*f})
}

// distance is the straight line between two places on the plane, in plane
// widths — the same unit walkSpeed is expressed in.
func distance(a, b Point) float64 {
	return math.Hypot(b.X-a.X, b.Y-a.Y)
}

// clampPoint keeps a computed position on the plane. A pattern with silly
// parameters walks its character into the fence rather than off the screen.
func clampPoint(p Point) Point {
	x, okX := clampUnit(p.X)
	y, okY := clampUnit(p.Y)
	if !okX || !okY {
		return spawn
	}
	return Point{X: x, Y: y}
}
