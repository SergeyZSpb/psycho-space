package gamevanyadum

import (
	"math"
	"testing"

	"github.com/google/uuid"
)

// The нейрослоп: how it chooses, how it walks, how it is spawned, what it costs
// to be reached by one, and what it is worth to shoot one.
//
// The fixtures are world_test.go's, deliberately — `worldIn`, `joinTo`, `stand`,
// the stopwatch and `shot` are the same harness every rule about the building is
// already written against, and a слоп tested in a world of its own would be a
// creature proved to work somewhere nobody plays. What this file adds is the one
// thing those fixtures deliberately suppress: a spawner that is allowed to run.

// slopsFrom lets the spawner off its leash, from this tick onwards. It is the
// deliberate undoing of noSlops, which every other fixture in the package
// applies; see that function for why the leash is a deadline rather than a flag.
func slopsFrom(w *World, tick int64) { w.slopSpawnAt = tick }

// putSlop stands a нейрослоп somewhere by hand, deriving its room from its
// position exactly as the resolver would — the same shape, and for the same
// reason, as `stand` does for a man. A test about walking, shooting or touching
// must not also be a test of where the spawner happened to put something.
func putSlop(t *testing.T, w *World, id int, at Vec2) *Slop {
	t.Helper()
	sector := w.Level.SectorAt(at)
	if sector < 0 {
		t.Fatalf("this test wants a слоп at %v, which is outside the building", at)
	}
	s := &Slop{ID: id, Pos: at, Sector: sector, Health: SlopHealth}
	w.slops[id] = s
	return s
}

// liveSlops is how many нейрослопы are in the building.
func liveSlops(w *World) int {
	n := 0
	for _, s := range w.slops {
		if s != nil {
			n++
		}
	}
	return n
}

// --- choosing, and walking --------------------------------------------------

func TestASlopWalksAtTheNearestManAndKeepsWalkingWhenSomebodyNearerArrives(t *testing.T) {
	// The whole of the AI, stated as a test: the nearest man, recomputed every
	// tick. Recomputed rather than committed to, which is why there is no target
	// field on the creature — a stored choice would be a second answer to a
	// question the positions already answer, kept in step by hand.
	w := worldIn(t, room(0, 0, 40, 40, 0))
	sw := newStopwatch()
	near := joinTo(t, w, Vec2{X: 10, Y: 20}, 0)
	joinTo(t, w, Vec2{X: 35, Y: 20}, 0)
	s := putSlop(t, w, 0, Vec2{X: 20, Y: 20})

	sw.run(w, 10)
	if s.Pos.X >= 20 {
		t.Fatalf("it walked to x=%.2f, away from the man at x=10 and towards the one at x=35", s.Pos.X)
	}

	// The near man walks away past the far one, and the creature turns round —
	// which it can only do if the choice is made afresh rather than remembered.
	stand(t, w, near, Vec2{X: 39, Y: 20}, 0)
	was := s.Pos.X
	sw.run(w, 10)
	if s.Pos.X <= was {
		t.Fatalf("the nearest man moved and it kept walking at where he used to be: x %.2f → %.2f", was, s.Pos.X)
	}
}

func TestATieIsSettledByTheStableOrderAndNotByTheMap(t *testing.T) {
	// Two men at exactly the same distance is not a hypothetical in a building
	// this size, and the answer must not be whichever way the runtime tossed the
	// coin — the whole simulation is tested by replaying a transcript, so a target
	// chosen by map order is a world that stops reproducing.
	//
	// Asserted over nearestPrey rather than through the world, because what is
	// under test is the tie-break rule itself: the caller's order decides, and
	// stepSlops builds the list in keys' order.
	first := prey{Pos: Vec2{X: 0, Y: 10}}
	second := prey{Pos: Vec2{X: 20, Y: 10}}
	for i := 0; i < 50; i++ {
		got, ok := nearestPrey(Vec2{X: 10, Y: 10}, []prey{first, second})
		if !ok || got != first {
			t.Fatalf("an exact tie answered %+v on attempt %d, expected the first of the list", got, i)
		}
	}
}

func TestASlopFindsItsWayIntoTheNextRoomThroughTheDoorway(t *testing.T) {
	// The заброшка is rooms joined by doorways, so a creature that walks straight
	// at you walks into a wall and stays there. Three rooms in a line, the man in
	// the last of them, the слоп in the first: it has to leave through two
	// openings to arrive, and the only thing telling it where they are is the
	// route table.
	l := threeRooms()
	w := worldIn(t, l)
	sw := newStopwatch()
	victim := joinTo(t, w, Vec2{X: 25, Y: 5}, 0)
	// A corner of the first room, so the straight line to him crosses solid wall
	// rather than happening to line up with both openings.
	s := putSlop(t, w, 0, Vec2{X: 2, Y: 9})

	// Bounded by simulated time rather than by a count of anything: twelve seconds
	// is comfortably more than the walk takes at SlopSpeed and comfortably less
	// than an infinite loop.
	at := w.Occupant(victim).State.Pos
	reached := false
	for i := 0; i < 12*SimHz && !reached; i++ {
		sw.tick(w)
		reached = math.Hypot(s.Pos.X-at.X, s.Pos.Y-at.Y) <= SlopReach
	}
	if !reached {
		t.Fatalf("it got as far as sector %d at %v, %.2f m short of him",
			s.Sector, s.Pos, math.Hypot(s.Pos.X-at.X, s.Pos.Y-at.Y))
	}
}

func TestNoSlopEverLeavesTheBuilding(t *testing.T) {
	// The rule that makes the walk safe to leave running for ever. A слоп is moved
	// by the player's own resolver (sim.go, resolve), which refuses any step that
	// lands outside every room — so the worst a bad route can do is grind on a
	// wall. Swept over enough generated buildings that a doorway the pathing
	// mishandles would have to be vanishingly rare to escape it.
	for seed := int64(1); seed <= 60; seed++ {
		w := NewWorld(uuid.New(), seed)
		sw := newStopwatch()
		acc := joinTo(t, w, w.Level.Spawn, 0)
		slopsFrom(w, 0)

		for i := 0; i < 400; i++ {
			// The man walks too, so the creature is chasing a moving target through
			// doorways rather than converging on a fixed point.
			o := w.Occupant(acc)
			w.Enqueue(acc, &ParsedInput{Cmds: []Command{{
				Seq: int64(i) + 1, Dt: subStep,
				MX:  math.Sin(float64(i) / 7),
				MY:  math.Cos(float64(i) / 11),
				Yaw: o.State.Yaw,
			}}})
			sw.tick(w)

			for _, s := range w.slops {
				if s == nil {
					continue
				}
				if got := w.Level.SectorAt(s.Pos); got < 0 {
					t.Fatalf("seed %d tick %d: слоп %d walked out of the building, to %v", seed, i, s.ID, s.Pos)
				}
				if s.Sector != w.Level.SectorAt(s.Pos) {
					t.Fatalf("seed %d tick %d: слоп %d is at %v in sector %d, which is sector %d",
						seed, i, s.ID, s.Pos, s.Sector, w.Level.SectorAt(s.Pos))
				}
			}
		}
	}
}

func TestOnEveryGeneratedBuildingASlopReachesAManWhoStandsStill(t *testing.T) {
	// THE OTHER HALF OF THE SWEEP ABOVE, and the half that says the walk WORKS.
	// "It never left the building" is passed perfectly by a creature grinding on a
	// wall for the rest of the match, so on its own it pins the safety property and
	// nothing about the pathing. Arrival is otherwise asserted only on a hand-built
	// corridor of three rectangles, which exercises none of the shapes the
	// generator actually produces — the room reached through a corner doorway, the
	// long thin one, the loop back round a courtyard.
	//
	// THE MAN STANDS STILL, so what is measured is the route table and not his
	// walking into something. He is at the spawn, and the spawner deliberately
	// starts every слоп in a room he cannot see into, so the whole of the walk is
	// under test.
	//
	// BOUNDED BY SIMULATED TIME, which is exact rather than a tolerance: this
	// world's clock IS the tick, so the bound is a statement about the game and not
	// about how loaded the machine running it is. Twenty seconds against a measured
	// worst case of about twelve — logged below, so a change that makes the walk
	// much slower without breaking it is visible rather than merely still passing.
	const deadline = 20 * SimHz
	worst := 0
	for seed := int64(1); seed <= 60; seed++ {
		w := NewWorld(uuid.New(), seed)
		sw := newStopwatch()
		acc := joinTo(t, w, w.Level.Spawn, 0)
		slopsFrom(w, 0)

		reached := -1
		for i := 0; i < deadline && reached < 0; i++ {
			sw.tick(w)
			if w.Occupant(acc).State.Health < StartHealth {
				reached = i + 1
			}
		}
		if reached < 0 {
			t.Fatalf("seed %d: a man stood still at the spawn for %d seconds and nothing ever reached him — %d слопы in the building, at %v",
				seed, deadline/SimHz, liveSlops(w), slopPositions(w))
		}
		if reached > worst {
			worst = reached
		}
	}
	t.Logf("the slowest of 60 generated buildings took %.1f s to put something on a man standing still", float64(worst)/SimHz)
}

// slopPositions is where everything in the building is standing, for a failure
// message that has to explain what a creature was doing instead of arriving.
func slopPositions(w *World) []Vec2 {
	var out []Vec2
	for _, s := range w.slops {
		if s != nil {
			out = append(out, s.Pos)
		}
	}
	return out
}

// --- keeping out of each other's way ----------------------------------------

func TestTwoSlopsInOnePlaceAreSeparatedAndTheLowerIdDoesNotMove(t *testing.T) {
	// They converge by construction — same target, same table, same speed — so
	// this is the ordinary case rather than an edge one. What it must produce is
	// two figures rather than one, and it must produce it the same way every time:
	// the lower id never yields, so nothing about the первый слоп's arrival depends
	// on a second one having walked into it.
	//
	// walk runs that fixture with the second слоп in it and without, which is the
	// only way to state the claim the design actually rests on: a shot resolved at
	// the первый слоп, the tick it lands on and everything the rewind ring recorded
	// about it must not move because something else walked into it. It is true by
	// construction today — the loop only ever pushes the higher id — and a rewrite
	// that split the overlap between the pair would leave every other assertion
	// here green.
	walk := func(pair bool) (first, second Vec2) {
		w := worldIn(t, room(0, 0, 20, 20, 0))
		sw := newStopwatch()
		joinTo(t, w, Vec2{X: 18, Y: 10}, 0)
		one := putSlop(t, w, 0, Vec2{X: 5, Y: 10})
		var two *Slop
		if pair {
			two = putSlop(t, w, 1, Vec2{X: 5.05, Y: 10})
		}
		sw.run(w, 20)
		if two != nil {
			second = two.Pos
		}
		return one.Pos, second
	}

	first, second := walk(true)
	if got := math.Hypot(first.X-second.X, first.Y-second.Y); got < SlopReach-1e-9 {
		t.Fatalf("two слопы walking at the same man ended up %.3f m apart, closer than %.3f", got, SlopReach)
	}
	// And the one that yielded is still arriving: pushed sideways rather than
	// backwards, so it has not been made permanently the other's shadow.
	if second.X <= 5.05 {
		t.Fatalf("the слоп that gave way was pushed backwards: x %.2f, started at 5.05", second.X)
	}
	// Exactly where it would have been alone, to the last bit: an approximate
	// answer here would be a walk that the pair perturbs by a little, which is the
	// same defect with a smaller number on it.
	if alone, _ := walk(false); first != alone {
		t.Fatalf("the слоп that never yields ended at %v beside another and at %v on its own", first, alone)
	}
}

func TestTwoSlopsExactlyOnTopOfEachOtherAreStillSeparated(t *testing.T) {
	// The degenerate case, which is the one with no direction in it: coincident
	// discs have no line between them, so a naive push divides by zero and sends
	// one of them to infinity. The fallback is a FIXED axis rather than a draw,
	// because this runs twenty times a second and every process replaying the same
	// building has to agree about where they went.
	l := room(0, 0, 20, 20, 0)
	same := Vec2{X: 10, Y: 10}
	moved := separate(l, Slop{ID: 1, Pos: same, Sector: 0}, Slop{ID: 0, Pos: same, Sector: 0})
	if math.IsNaN(moved.Pos.X) || math.IsNaN(moved.Pos.Y) || math.IsInf(moved.Pos.X, 0) || math.IsInf(moved.Pos.Y, 0) {
		t.Fatalf("two coincident слопы separated to %v", moved.Pos)
	}
	if got := math.Hypot(moved.Pos.X-same.X, moved.Pos.Y-same.Y); math.Abs(got-SlopReach) > 1e-9 {
		t.Fatalf("it was pushed %.3f m clear, expected exactly %.3f", got, SlopReach)
	}
	// Twice, so the answer is the axis and not a coin.
	again := separate(l, Slop{ID: 1, Pos: same, Sector: 0}, Slop{ID: 0, Pos: same, Sector: 0})
	if again.Pos != moved.Pos {
		t.Fatalf("the same degenerate pair separated to %v and then to %v", moved.Pos, again.Pos)
	}
}

// --- the spawner ------------------------------------------------------------

func TestTheSpawnerFillsTheBuildingAndThenStops(t *testing.T) {
	// A population-keeping spawner rather than a generator that scatters them
	// once: nothing here ends, so a building stocked at generation time is one that
	// is permanently cleared the first time somebody tours it with a full gun.
	// What it must NOT do is keep going.
	w, _ := newTestWorld(t, 7)
	sw := newStopwatch()
	slopsFrom(w, 0)

	// Long enough to fill it several times over at SlopSpawnInterval.
	sw.run(w, int(slopSpawnTicks)*(SlopPopulation+3))
	if got := liveSlops(w); got != SlopPopulation {
		t.Fatalf("the building holds %d слопы, expected exactly %d", got, SlopPopulation)
	}
}

func TestTheBuildingIsQuietForOneIntervalBeforeTheFirstSlop(t *testing.T) {
	// Nothing materialises on the tick a socket says hello: the client is still
	// building its meshes, and being hurt by something it has not drawn yet is the
	// worst first impression this game could make. The deadline starts full, which
	// is what makes the first слоп cost the same wait as every one after it.
	w, _ := newTestWorld(t, 7)
	// Not slopsFrom: this is the production leash, and the point of the test.
	w.slopSpawnAt = slopSpawnTicks
	sw := newStopwatch()

	sw.run(w, int(slopSpawnTicks)-1)
	if got := liveSlops(w); got != 0 {
		t.Fatalf("%d слопы appeared before the first interval was up", got)
	}
	sw.tick(w)
	if got := liveSlops(w); got != 1 {
		t.Fatalf("the interval passed and %d слопы appeared, expected exactly one", got)
	}
}

func TestKillingOneBuysAWholeIntervalOfQuiet(t *testing.T) {
	// The replacement rate IS the difficulty, so clearing a room has to be worth
	// something. The deadline is pushed forward on every tick the building is
	// full, which is what stops a kill being answered by an instant refill from a
	// timer that had been counting down under a building that needed nothing.
	w, _ := newTestWorld(t, 7)
	sw := newStopwatch()
	slopsFrom(w, 0)
	sw.run(w, int(slopSpawnTicks)*SlopPopulation+1)
	if liveSlops(w) != SlopPopulation {
		t.Fatalf("the fixture wanted a full building, got %d слопы", liveSlops(w))
	}

	w.killSlop(0)
	sw.run(w, int(slopSpawnTicks)-2)
	if got := liveSlops(w); got != SlopPopulation-1 {
		t.Fatalf("a replacement arrived before the interval was up: %d слопы", got)
	}
	sw.run(w, 3)
	if got := liveSlops(w); got != SlopPopulation {
		t.Fatalf("the interval passed and the building holds %d слопы", got)
	}
}

func TestNothingSpawnsIntoAnEmptyBuilding(t *testing.T) {
	// A слоп with nobody to walk at is a creature standing in a room nobody is in,
	// holding one of SlopPopulation places, in a world that is about to be torn
	// down. Making the spawner refuse an empty building is what makes "no leak
	// when everybody leaves" a property of this function rather than of the
	// service happening to stop ticking it.
	w := NewWorld(uuid.New(), 7)
	slopsFrom(w, 0)
	sw := newStopwatch()

	sw.run(w, int(slopSpawnTicks)*3)
	if got := liveSlops(w); got != 0 {
		t.Fatalf("%d слопы appeared in a building with nobody in it", got)
	}

	// And the moment somebody walks in, they arrive.
	joinTo(t, w, w.Level.Spawn, 0)
	sw.tick(w)
	if got := liveSlops(w); got == 0 {
		t.Fatal("somebody walked in and nothing came for him")
	}
}

func TestASlopAppearsWhereNobodyIsLookingAtTheTime(t *testing.T) {
	// Everything else in this world arrives by walking through a doorway, and a
	// creature that materialises in the room you are standing in is one you could
	// not have avoided and cannot explain. The visible set is already the answer to
	// "which rooms is somebody looking at", so the spawner reuses it.
	//
	// Swept over seeds, because which rooms are adjacent to the spawn is a property
	// of the building rather than of the rule.
	for seed := int64(1); seed <= 40; seed++ {
		w := NewWorld(uuid.New(), seed)
		sw := newStopwatch()
		acc := joinTo(t, w, w.Level.Spawn, 0)
		slopsFrom(w, 0)
		sw.tick(w)

		from := w.Occupant(acc).State.Sector
		for _, s := range w.slops {
			if s == nil {
				continue
			}
			if w.canSee(from, s.Sector) {
				t.Fatalf("seed %d: a слоп appeared in sector %d, which the man in sector %d can see into",
					seed, s.Sector, from)
			}
		}
	}
}

// --- arriving -----------------------------------------------------------------

func TestBeingReachedCostsHealthOnItsOwnClock(t *testing.T) {
	// Contact damage, and the cooldown is the whole of what makes it survivable: a
	// creature overlapping you at the tick rate would be twenty hits a second, so
	// walking into one would be indistinguishable from walking into a wall that
	// kills you. With the cooldown it is a warning first and a death fourth.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	victim := joinTo(t, w, Vec2{X: 10, Y: 10}, 0)
	putSlop(t, w, 0, Vec2{X: 10.1, Y: 10})

	sw.tick(w)
	if got := w.Occupant(victim).State.Health; got != StartHealth-SlopDamage {
		t.Fatalf("being reached left him on %d of %d", got, StartHealth)
	}
	// The room is told, on the same field a shot uses: being hurt by something
	// moves nobody, so there is nothing on the frame to derive it from.
	if !w.Occupant(victim).hitThisTick(w.Tick) {
		t.Fatal("a man walked into by a слоп was not marked as hit")
	}

	// And nothing more for the rest of the interval, however many ticks it holds.
	sw.run(w, int(slopTouchTicks)-2)
	if got := w.Occupant(victim).State.Health; got != StartHealth-SlopDamage {
		t.Fatalf("it hit him again inside its own cooldown: %d health", got)
	}
	sw.run(w, 2)
	if got := w.Occupant(victim).State.Health; got != StartHealth-2*SlopDamage {
		t.Fatalf("the cooldown expired and the second touch left him on %d", got)
	}
}

func TestASlopBetweenTwoPeopleHitsTheNearerOfThem(t *testing.T) {
	// A creature standing between two men who are both in reach used to hurt
	// whichever of them held the lexicographically smaller uuid, and to go on doing
	// it for the life of both accounts — a name never changes. That is precisely
	// what turnOrder exists to prevent, and what it calls a coin that never gets
	// tossed rather than one landing badly.
	//
	// NEAREST RATHER THAN A ROTATION, because it needs no clock to be fair: it is a
	// property of where the two of them are standing, and it is what anybody
	// watching a creature arrive would expect it to do.
	//
	// ASSERTED OVER touch AND NOT OVER A TICK. A tick walks the слоп at the nearest
	// man before anything lands, and 0.16 m of that takes the other one out of
	// reach — so a fixture driven by the clock would be asserting the pathing and
	// could not put two people in reach at once at all. The list is built exactly
	// as stepSlops builds it, in keys' order, which is the order the rule this
	// replaces would have taken the first of.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	a := joinTo(t, w, Vec2{X: 10.3, Y: 10}, 0)
	b := joinTo(t, w, Vec2{X: 9.4, Y: 10}, 0)
	s := putSlop(t, w, 0, Vec2{X: 10, Y: 10})
	victims := make([]*Occupant, 0, 2)
	for _, id := range w.keys() {
		victims = append(victims, w.Occupant(id))
	}

	w.touch(s, victims)
	if got := w.Occupant(a).State.Health; got != StartHealth-SlopDamage {
		t.Fatalf("the man 0.3 m away is on %d of %d", got, StartHealth)
	}
	if got := w.Occupant(b).State.Health; got != StartHealth {
		t.Fatalf("the man 0.6 m away, behind the nearer one, is on %d of %d", got, StartHealth)
	}

	// And now the same two accounts the other way round, which is what makes this a
	// test of the rule rather than of the draw: whichever of them holds the smaller
	// uuid, exactly one of these two halves is the arrangement a fixed order gets
	// wrong.
	stand(t, w, a, Vec2{X: 9.4, Y: 10}, 0)
	stand(t, w, b, Vec2{X: 10.3, Y: 10}, 0)
	// The cooldown belongs to the слоп and is not what this test is about; it has
	// its own (TestBeingReachedCostsHealthOnItsOwnClock).
	s.touchAt = 0

	w.touch(s, victims)
	if got := w.Occupant(b).State.Health; got != StartHealth-SlopDamage {
		t.Fatalf("they swapped places and the man now 0.3 m away is on %d of %d", got, StartHealth)
	}
	if got := w.Occupant(a).State.Health; got != StartHealth-SlopDamage {
		t.Fatalf("the man now 0.6 m away took a second touch as well: %d of %d", got, StartHealth)
	}
}

func TestFourTouchesIsADeathAndNobodyIsBlamedForIt(t *testing.T) {
	// The death loop is the one the обрез already built — DownTime, the spawn, the
	// protection, all of it — and what differs is only the accounting: a слоп
	// arriving is nobody's fault, so no betrayal is recorded and nothing is added
	// to anybody's kills.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	victim := joinTo(t, w, Vec2{X: 10, Y: 10}, 0)
	putSlop(t, w, 0, Vec2{X: 10.1, Y: 10})

	for i := 0; i < StartHealth/SlopDamage; i++ {
		sw.run(w, int(slopTouchTicks))
	}
	o := w.Occupant(victim)
	if o.State.Health != 0 {
		t.Fatalf("%d touches left him on %d health", StartHealth/SlopDamage, o.State.Health)
	}
	if o.deaths != 1 {
		t.Fatalf("a man killed by a слоп has %d deaths", o.deaths)
	}
	if o.betrayals != 0 || o.kills != 0 {
		t.Fatalf("being killed by a слоп scored him %d betrayals and %d kills", o.betrayals, o.kills)
	}
}

func TestNothingIsExchangedThroughConcrete(t *testing.T) {
	// THE FIXTURE IS THE WORST CASE AND IT IS NOT CONTRIVED: a man and a слоп flush
	// against opposite sides of the solid part of a shared wall. Each stands
	// PlayerRadius clear of the concrete, which is where the resolver keeps
	// everything (sim.go, pushOut), so the pair are EXACTLY SlopReach apart —
	// which is what a plain distance test calls a touch. The property held before there was a wall test only because
	// two floating-point coordinates had to coincide to about 3e-8 for the walk to
	// leave them level, and raising SlopReach by a tenth of a metre because it felt
	// better would have started hurting people through walls with nothing failing.
	//
	// The doorway is at y ∈ [4, 6] and this is at y = 2, so the line between them
	// crosses concrete rather than the opening.
	w := worldIn(t, twoRooms(0))
	victim := joinTo(t, w, Vec2{X: 10 - PlayerRadius, Y: 2}, 0)
	s := putSlop(t, w, 0, Vec2{X: 10 + PlayerRadius, Y: 2})
	o := w.Occupant(victim)

	if got := math.Hypot(s.Pos.X-o.State.Pos.X, s.Pos.Y-o.State.Pos.Y); got > SlopReach {
		t.Fatalf("the fixture stood them %.6f m apart, further than the %.6f this test is about", got, SlopReach)
	}
	if _, ok := touches(w.Level, s.Pos, o.State.Pos); ok {
		t.Fatal("a слоп reached a man through a solid wall")
	}
	// And the guard is wired into the thing that hurts people rather than merely
	// available to it. Called directly because a tick would walk the creature off
	// the line first — it leaves for the doorway immediately — and the case under
	// test is the instant before it does.
	w.touch(s, []*Occupant{o})
	if got := o.State.Health; got != StartHealth {
		t.Fatalf("contact through the wall left him on %d of %d", got, StartHealth)
	}

	// The other direction is not re-asserted here, deliberately: it is the very
	// same wall sweep, and it already has its own test over the barrel
	// (hit_test.go, TestConcreteStopsTheShot). Flush against the wall is in any
	// case the one geometry that cannot state it — a disc PlayerRadius clear of the
	// concrete is TANGENT to it, so the shot enters the body at the wall plane to
	// within a rounding error, and an assertion there would be pinning which way a
	// tie happened to fall.
	//
	// What this test owns is that contact damage is now leashed by that sweep too.
	// And that it is a leash rather than contact quietly switched off: the way in
	// is the doorway, and it takes about two seconds to walk round to him through
	// it.
	sw := newStopwatch()
	for i := 0; i < 5*SimHz && w.Occupant(victim).State.Health == StartHealth; i++ {
		sw.tick(w)
	}
	if got := w.Occupant(victim).State.Health; got != StartHealth-SlopDamage {
		t.Fatalf("it had five seconds to walk round through the doorway and left him on %d of %d", got, StartHealth)
	}
}

func TestASlopDoesNotTouchAManInsideHisSpawnProtection(t *testing.T) {
	// Both halves of the protection or it is not a protection. There is one spawn
	// point that everybody knows about, and a creature that could kill a man on it
	// the instant he got up would make the two seconds in which he also cannot
	// shoot the worst two seconds in the game.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	victim := walkIn(t, w, Vec2{X: 10, Y: 10}, 0) // walkIn, so he keeps the grant
	putSlop(t, w, 0, Vec2{X: 10.1, Y: 10})

	sw.run(w, int(protectTicks)-2)
	if got := w.Occupant(victim).State.Health; got != StartHealth {
		t.Fatalf("a protected man standing inside a слоп is on %d health", got)
	}
	// And the moment it lapses, it lands.
	sw.run(w, 3)
	if got := w.Occupant(victim).State.Health; got != StartHealth-SlopDamage {
		t.Fatalf("the protection expired and he is still on %d health", got)
	}
}

func TestASlopIgnoresAManOnTheFloor(t *testing.T) {
	// A corpse is not a target, which is the same rule the hit test already
	// applies. Without it a слоп would stand on a dead man for DownTime and be
	// waiting exactly where he gets up — except he gets up at the spawn, so what it
	// would actually do is guard a body nobody is coming back to.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	dead := joinTo(t, w, Vec2{X: 10, Y: 10}, 0)
	joinTo(t, w, Vec2{X: 18, Y: 10}, 0)
	w.hurt(w.Occupant(dead), StartHealth)
	s := putSlop(t, w, 0, Vec2{X: 5, Y: 10})

	sw.run(w, 10)
	if s.Pos.X <= 5 {
		t.Fatalf("it walked to x=%.2f, towards the corpse at x=10 rather than the man at x=18", s.Pos.X)
	}
}

// --- being shot ---------------------------------------------------------------

func TestASlopDiesToOneBarrelBecauseNothingOnTheWireCouldSayOtherwise(t *testing.T) {
	// THE CONSTANT AND THE WIRE ARE ONE DECISION. A слоп is drawn from a position
	// and nothing else (message.go, Foe) — no health, no state field — so the only
	// acknowledgement a hit can have is the creature ceasing to be on the frame.
	// That is the truth only while one barrel is the whole of it: raise SlopHealth
	// above BarrelDamage and shooting one becomes an action with no acknowledgement
	// at all, which this project treats as unfinished, and the field that would fix
	// it costs a place in the building (MaxOccupants).
	//
	// Asserted over the constants first, because that is the relationship, and then
	// through the world, because a comment is not a gate.
	if SlopHealth > BarrelDamage {
		t.Fatalf("SlopHealth is %d against a barrel's %d: a слоп now survives a hit that nothing on the frame can report",
			SlopHealth, BarrelDamage)
	}

	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	arm(w, shooter, Barrels, 0)
	stand(t, w, shooter, Vec2{X: 5, Y: 10}, eastward)
	putSlop(t, w, 0, Vec2{X: 9, Y: 10})
	sw.run(w, 3)

	shot(w, sw, shooter, 1)
	if w.slops[0] != nil {
		t.Fatalf("one barrel left it on %d health", w.slops[0].Health)
	}
	// And it is gone from the frame on the very tick it died, which is the whole
	// of the acknowledgement.
	if got := mustSnapshot(t, w, shooter).Slops; len(got) != 0 {
		t.Fatalf("a слоп killed on this tick is still on the frame: %+v", got)
	}
}

func TestKillingASlopScoresAndShootingAFriendStillDoesNot(t *testing.T) {
	// The joke, as a test. Same gun, same ray, same damage; the building simply
	// has an opinion about which one you pointed it at — and the two columns on
	// the standings are where it says so.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	friend := joinTo(t, w, Vec2{X: 9, Y: 16}, 0)
	arm(w, shooter, Barrels, 9)
	stand(t, w, shooter, Vec2{X: 5, Y: 10}, eastward)
	putSlop(t, w, 0, Vec2{X: 9, Y: 10})
	sw.run(w, 3)

	shot(w, sw, shooter, 1)
	o := w.Occupant(shooter)
	if o.kills != 1 || o.betrayals != 0 {
		t.Fatalf("shooting a слоп scored %d kills and %d betrayals", o.kills, o.betrayals)
	}

	// Now the friend, on the same line, and on the half-bar that makes one barrel
	// the end of him — a betrayal is a friend PUT ON THE FLOOR rather than a
	// friend hit, and a full gun is exactly one kill (content.go, BarrelDamage).
	stand(t, w, friend, Vec2{X: 9, Y: 10}, 0)
	w.Occupant(friend).State.Health = BarrelDamage
	sw.run(w, int(FireCooldownSeconds*SimHz)+1)
	shot(w, sw, shooter, 2)
	if o.kills != 1 {
		t.Fatalf("shooting a friend moved the kill count to %d", o.kills)
	}
	if o.betrayals != 1 {
		t.Fatalf("shooting a friend scored %d betrayals", o.betrayals)
	}

	// And the board publishes both, separately.
	rows := w.Standings(epoch).Rows
	var mine StandingsRow
	for _, r := range rows {
		if r.Slot == o.Slot {
			mine = r
		}
	}
	if mine.Kills != 1 || mine.Betrayals != 1 {
		t.Fatalf("the standings row reads kills=%d betrayals=%d", mine.Kills, mine.Betrayals)
	}
}

func TestASlopIsShotWhereTheShooterSawItAndNotWhereItIsNow(t *testing.T) {
	// A слоп is server-owned and unpredicted, and it is drawn interpolated in the
	// recent past exactly as a peer is — so the same rewind is the only honest way
	// to resolve a shot at one. There is no second hit path: the ray, the ring and
	// the visibility filter are the ones the обрез already had.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	arm(w, shooter, Barrels, 0)
	stand(t, w, shooter, Vec2{X: 5, Y: 10}, eastward)
	onTheLine := putSlop(t, w, 0, Vec2{X: 9, Y: 10})
	offTheLine := putSlop(t, w, 1, Vec2{X: 9, Y: 16})
	sw.run(w, 6) // enough recorded past to rewind into

	// They swap places, and the shot goes off before a single frame of the new
	// world has been drawn on anybody's screen. Placed rather than walked, because
	// what is under test is the rewind and not the pathing.
	onTheLine.Pos, offTheLine.Pos = Vec2{X: 9, Y: 16}, Vec2{X: 9, Y: 10}
	shot(w, sw, shooter, 1)

	if w.slops[0] != nil {
		t.Fatal("the слоп the shooter was actually looking at survived")
	}
	if w.slops[1] == nil {
		t.Fatal("the слоп that had just stepped into the line — and was drawn nowhere near it — was killed")
	}
}

func TestANewSlopIsNotShotWhereTheLastOneDied(t *testing.T) {
	// THE FAILURE LAG COMPENSATION MUST NEVER PRODUCE, in its second form. The
	// rewind ring is keyed by the wire's own names, and a слоп's id is handed to
	// the next creature the moment it dies — so everything the ring recorded about
	// the last one describes the new one for the whole of RewindMax, unless the
	// ring is purged when the id is freed (history.forget, world.go killSlop).
	//
	// It is worse here than it is for a man, and that is why it has its own test:
	// a place in the building comes free when somebody's connection has been gone
	// for two minutes, where a слоп's id comes free the instant somebody shoots it.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	arm(w, shooter, Barrels, 9)
	stand(t, w, shooter, Vec2{X: 5, Y: 10}, eastward)
	putSlop(t, w, 0, Vec2{X: 9, Y: 10})
	sw.run(w, 6)

	shot(w, sw, shooter, 1)
	if w.slops[0] != nil {
		t.Fatal("the fixture wanted the first слоп dead")
	}

	// The id is handed straight back out, to a creature standing nowhere near the
	// line — and the shooter fires down the same line again.
	putSlop(t, w, 0, Vec2{X: 9, Y: 18})
	sw.run(w, int(FireCooldownSeconds*SimHz)+1)
	shot(w, sw, shooter, 2)

	if w.slops[0] == nil {
		t.Fatal("the new слоп was shot on a line it was never on, where its predecessor died")
	}
}

func TestYouCannotShootASlopYouWereNeverSent(t *testing.T) {
	// The filter is load-bearing rather than merely economical, and it has to be
	// the same one for both kinds of target. The geometry would happily carry a
	// shot through two lined-up doorways into a room the approximation does not
	// consider visible — and the creature standing there was never on the shooter's
	// screen, so it would be a kill nobody could see happen.
	w := worldIn(t, threeRooms())
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 5}, eastward)
	arm(w, shooter, Barrels, 0)
	stand(t, w, shooter, Vec2{X: 5, Y: 5}, eastward)
	putSlop(t, w, 0, Vec2{X: 25, Y: 5}) // two doorways away, straight down the line
	sw.run(w, 3)

	// It is not on his frame …
	if got := mustSnapshot(t, w, shooter).Slops; len(got) != 0 {
		t.Fatalf("a слоп two rooms away was sent anyway: %+v", got)
	}
	// … and so it is not a target either.
	shot(w, sw, shooter, 1)
	if w.slops[0] == nil {
		t.Fatal("a слоп the shooter was never sent was killed through two doorways")
	}
}

// --- the frame ----------------------------------------------------------------

func TestTheFrameCarriesTheSlopsYouCanSeeAndNothingElseAboutThem(t *testing.T) {
	// The whole of what a слоп costs a viewer: an id, two centimetre coordinates
	// and a room. Everything a peer also carries is either derivable from two
	// consecutive frames (its facing) or meaningless for this creature (the state
	// field), and each omission is a place in the building (MaxOccupants).
	w := worldIn(t, threeRooms())
	viewer := joinTo(t, w, Vec2{X: 5, Y: 5}, 0)
	here := putSlop(t, w, 0, Vec2{X: 6.5, Y: 5.25})
	putSlop(t, w, 1, Vec2{X: 25, Y: 5}) // two rooms away

	got := mustSnapshot(t, w, viewer).Slops
	if len(got) != 1 {
		t.Fatalf("the frame carries %d слопы, expected the one in the room: %+v", len(got), got)
	}
	want := Foe{ID: 0, X: cm(here.Pos.X), Y: cm(here.Pos.Y), Sector: here.Sector}
	if got[0] != want {
		t.Fatalf("the frame says %+v, expected %+v", got[0], want)
	}
}

func TestASlopInADoorwayIsHeldRatherThanStrobing(t *testing.T) {
	// A sector is derived from a position, so a creature standing in the doorway
	// between two rooms belongs to whichever of them the last sub-centimetre put it
	// in — and it crosses back and forth without walking anywhere. Without the hold
	// it would appear and disappear at the tick rate for everybody adjacent to one
	// of the two rooms and not the other.
	w := worldIn(t, threeRooms())
	sw := newStopwatch()
	viewer := joinTo(t, w, Vec2{X: 5, Y: 5}, 0) // sector 0
	s := putSlop(t, w, 0, Vec2{X: 15, Y: 5})    // sector 1, through the doorway
	sw.tick(w)

	if len(mustSnapshot(t, w, viewer).Slops) != 1 {
		t.Fatal("a слоп through the doorway was not sent")
	}
	// It steps into the third room, which the viewer cannot see into — and is held
	// for a moment rather than vanishing on the tick it crossed.
	s.Pos, s.Sector = Vec2{X: 25, Y: 5}, 2
	sw.tick(w)
	if len(mustSnapshot(t, w, viewer).Slops) != 1 {
		t.Fatal("it left the visible set and was dropped on the very first tick, with no hold at all")
	}
	sw.run(w, int(visibleHoldTicks))
	if got := mustSnapshot(t, w, viewer).Slops; len(got) != 0 {
		t.Fatalf("the hold expired and it is still on the frame: %+v", got)
	}
}

// --- and all of it, twice ------------------------------------------------------

func TestTheSameTranscriptProducesTheSameBuildingWithSlopsInIt(t *testing.T) {
	// The replay property, extended to cover everything this iteration added. Two
	// worlds from one seed, driven by one transcript of walking and firing, with
	// the spawner running and the слопы walking and being shot at — and the digest
	// hashes their positions, their health and the spawner's own deadline, so a tie
	// broken by map order anywhere in the new code fails here.
	digest := func() string {
		w := NewWorld(uuid.Nil, 23)
		sw := newStopwatch()
		var accs []string
		// Deterministic account ids, because keys' order is what settles a tie
		// between two men at the same distance and a random uuid would resolve it
		// differently on each of the two runs.
		for i := 0; i < MaxOccupants; i++ {
			acc := uuid.NewSHA1(uuid.Nil, []byte{byte(i)}).String()
			if _, ok := w.Join(acc, "pseudo", epoch); !ok {
				t.Fatal("the building refused an occupant this test needs")
			}
			settled(w, acc)
			accs = append(accs, acc)
		}
		slopsFrom(w, 0)

		for i := 0; i < 500; i++ {
			for n, acc := range accs {
				w.Enqueue(acc, &ParsedInput{Cmds: []Command{{
					Seq: int64(i) + 1, Dt: subStep,
					MX:  math.Sin(float64(i+n*3) / 5),
					MY:  math.Cos(float64(i+n*7) / 9),
					Yaw: float64(i+n) / 4,
					// Often enough that the gun, the reload and the kills are all in
					// the transcript rather than merely reachable from it.
					Fire: i%11 == 0,
				}}})
			}
			sw.tick(w)
		}
		return worldDigest(t, w, accs...)
	}
	if first, second := digest(), digest(); first != second {
		t.Fatalf("the same seed and the same transcript produced two buildings:\n%s\n%s", first, second)
	}
}
