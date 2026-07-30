package gamefintech

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestHeClosesOnTheNearestTarget(t *testing.T) {
	b := Boss{Pos: Vec2{X: 8, Y: 11}}
	near := Vec2{X: 8, Y: 9}
	far := Vec2{X: 8, Y: 1}

	got := StepBoss(nil, b, []Vec2{far, near}, testDt, 0)
	if got.Pos.Y >= b.Pos.Y {
		t.Fatalf("he walked away from both of them: %v → %v", b.Pos.Y, got.Pos.Y)
	}
	if want := 11 - BossSpeed*testDt; math.Abs(got.Pos.Y-want) > 1e-9 {
		t.Fatalf("he moved to %v, want %v — that is not BossSpeed towards the nearer one", got.Pos.Y, want)
	}
}

func TestATieIsBrokenByTheCallersOrdering(t *testing.T) {
	// He must not pick a victim by map iteration order, or the same office would
	// play differently in two processes. Office.Advance builds the slice in
	// ascending account order; here the property is simply "the first of the
	// equally close wins, every time".
	b := Boss{Pos: Vec2{X: 8, Y: 11}}
	left := Vec2{X: 4, Y: 11}
	right := Vec2{X: 12, Y: 11}

	for i := 0; i < 50; i++ {
		if got := StepBoss(nil, b, []Vec2{left, right}, testDt, 0); got.Pos.X >= b.Pos.X {
			t.Fatalf("run %d: he went right (%v) when the left target was listed first", i, got.Pos.X)
		}
		if got := StepBoss(nil, b, []Vec2{right, left}, testDt, 0); got.Pos.X <= b.Pos.X {
			t.Fatalf("run %d: he went left (%v) when the right target was listed first", i, got.Pos.X)
		}
	}
}

func TestHeNeverEndsInsideADeskOrOutsideTheFloor(t *testing.T) {
	// He does not path-find, so he walks straight into furniture constantly.
	// Sliding along it is the intended behaviour; standing in it is not.
	r := rand.New(rand.NewPCG(3, 4))
	b := NewBoss()
	for i := 0; i < 5000; i++ {
		target := Vec2{X: r.Float64() * OfficeW, Y: r.Float64() * OfficeH}
		b = StepBoss(Desks, b, []Vec2{target}, testDt, 0)
		if b.Pos.X < BossRadius-1e-9 || b.Pos.X > OfficeW-BossRadius+1e-9 ||
			b.Pos.Y < BossRadius-1e-9 || b.Pos.Y > OfficeH-BossRadius+1e-9 {
			t.Fatalf("step %d put him outside the floor at %+v", i, b.Pos)
		}
		for j, d := range Desks {
			if insideDesk(d, b.Pos, BossRadius) {
				t.Fatalf("step %d put him inside desk %d at %+v", i, j, b.Pos)
			}
		}
	}
}

func TestTheGrinIsZeroAtRangeAndOneOnContact(t *testing.T) {
	if got := Grin(GrinRange); got != 0 {
		t.Fatalf("at exactly GrinRange he is smiling %v", got)
	}
	if got := Grin(GrinRange * 3); got != 0 {
		t.Fatalf("from across the office he is smiling %v", got)
	}
	if got := Grin(0); got != 1 {
		t.Fatalf("on contact he is smiling %v", got)
	}
	if got := Grin(GrinRange / 2); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("halfway he is smiling %v", got)
	}
}

func TestTheGrinIsRecomputedEveryStep(t *testing.T) {
	// It is the only readout of how much trouble you are in, so it has to
	// describe where he ended up rather than where he started.
	b := Boss{Pos: Vec2{X: 8, Y: 20}}
	target := Vec2{X: 8, Y: 8}
	if b.Grin != 0 {
		t.Fatal("he started out pleased")
	}
	for i := 0; i < 200; i++ {
		next := StepBoss(nil, b, []Vec2{target}, testDt, 0)
		if next.Grin < b.Grin {
			t.Fatalf("step %d: the grin went down while he was closing", i)
		}
		b = next
	}
	// Within floating-point slack rather than exactly: the grin is 1 − dist/range
	// clamped, and whether the last step lands exactly on top of the target
	// depends on the speed dividing the distance evenly. It does not have to.
	if 1-b.Grin > 1e-9 {
		t.Fatalf("having arrived, he is only smiling %v", b.Grin)
	}
}

func TestCaughtFiresWhenTheDiscsTouch(t *testing.T) {
	const reach = CatchRadius + PlayerRadius
	b := Boss{Pos: Vec2{X: 8, Y: 8}}
	if !Caught(b.Pos, Vec2{X: 8 + reach, Y: 8}) {
		t.Fatalf("at exactly %v he had not caught anybody", reach)
	}
	if !Caught(b.Pos, Vec2{X: 8, Y: 8}) {
		t.Fatal("standing on him is not being caught")
	}
	if Caught(b.Pos, Vec2{X: 8 + reach + 0.01, Y: 8}) {
		t.Fatalf("he caught somebody a centimetre outside %v", reach)
	}
}

func TestWithNobodyToChaseHeGoesHomeAndStops(t *testing.T) {
	// The office is never empty of a boss; an office empty of PEOPLE is torn
	// down entirely. This is what he does in between.
	b := Boss{Pos: Vec2{X: 2, Y: 4}}
	for i := 0; i < 1000; i++ {
		b = StepBoss(Desks, b, nil, testDt, 0)
	}
	if math.Abs(b.Pos.X-BossSpawnX) > 1e-6 || math.Abs(b.Pos.Y-BossSpawnY) > 1e-6 {
		t.Fatalf("he ended up at %+v rather than his spawn", b.Pos)
	}
	if b.Grin != 0 {
		t.Fatalf("standing alone at the far wall he is smiling %v", b.Grin)
	}
	// And he stays there rather than jittering around it.
	settled := b
	for i := 0; i < 10; i++ {
		b = StepBoss(Desks, b, nil, testDt, 0)
	}
	if b.Pos != settled.Pos {
		t.Fatalf("he drifted from %+v to %+v with nothing to chase", settled.Pos, b.Pos)
	}
}

func TestHeCannotOutrunAWalk(t *testing.T) {
	// The tuning that makes the game playable at all: he is slower than you, so
	// being caught is always a decision you made.
	if BossSpeed >= WalkSpeed {
		t.Fatalf("he walks at %v and you walk at %v — you cannot get away", BossSpeed, WalkSpeed)
	}
	if DashSpeed <= WalkSpeed {
		t.Fatalf("the dash (%v) is not faster than a walk (%v)", DashSpeed, WalkSpeed)
	}
}

func TestADrunkBaldManIsSlowerButStillComing(t *testing.T) {
	// STAGGERING IS NOT STOPPING, and that is the whole character: a boss who
	// freezes is a boss you ignore, and the joke is that he is delighted AND now
	// also drunk. Sober covers more ground than drunk; drunk still covers some.
	target := []Vec2{{X: BossSpawnX, Y: 2}}
	sober := StepBoss(Desks, NewBoss(), target, 1, 0)
	drunkStart := NewBoss()
	drunkStart.Drunk = DrunkSeconds
	drunk := StepBoss(Desks, drunkStart, target, 1, 0)

	soberGap := BossSpawnY - sober.Pos.Y
	drunkGap := BossSpawnY - drunk.Pos.Y
	if drunkGap <= 0 {
		t.Fatalf("drunk, he moved %v towards his target — he has stopped or gone backwards", drunkGap)
	}
	if drunkGap >= soberGap {
		t.Fatalf("drunk he covered %v and sober %v", drunkGap, soberGap)
	}
}

func TestHeWeavesWhileDrunkRatherThanWalkingAStraightLine(t *testing.T) {
	// The wobble is on the HEADING, so the same second of pursuit from the same
	// place lands somewhere different depending on where in the weave he is.
	target := []Vec2{{X: BossSpawnX, Y: 2}}
	start := NewBoss()
	start.Drunk = DrunkSeconds
	seen := map[int]bool{}
	for i := 0; i < 8; i++ {
		// A quarter of the wobble period apart, so the samples are spread across
		// it rather than landing on the same phase.
		at := float64(i) / (4 * DrunkWobbleHz)
		got := StepBoss(Desks, start, target, 0.5, at)
		seen[int(got.Pos.X*1000)] = true
	}
	if len(seen) < 3 {
		t.Fatalf("he walked the same line from every phase of the weave: %d distinct", len(seen))
	}
}

func TestHeSobersUp(t *testing.T) {
	b := NewBoss()
	b.Drunk = 0.1
	target := []Vec2{{X: BossSpawnX, Y: 2}}
	for i := 0; i < 5; i++ {
		b = StepBoss(Desks, b, target, SimStep.Seconds(), float64(i)*SimStep.Seconds())
	}
	if b.Drunk != 0 {
		t.Fatalf("he is still %v drunk", b.Drunk)
	}
}

// --- getting round a desk ---------------------------------------------------

func TestHeReachesSomebodyStandingBehindADesk(t *testing.T) {
	// THE DEFECT THIS FIXES, stated as the test that used to fail. Pure pursuit
	// grinds along a desk in whichever direction the push-out resolver happens to
	// send him, which has nothing to do with where he is trying to get — measured
	// at up to ninety seconds against a still player, in a game whose optimal
	// play is standing still.
	//
	// Behind the FIRST desk, on the far side from him, which is the shape of the
	// worst case: a straight line that is blocked for most of its length.
	d := Desks[0]
	behind := Vec2{X: d.X + d.W/2, Y: d.Y - PlayerRadius - 0.45}
	if clearLine(behind, NewBoss().Pos, BossRadius) {
		t.Skip("the catalogue moved: this point is no longer behind a desk")
	}

	o := NewOffice()
	if err := o.Join("a", "s1", "p-a", "", 0, epoch); err != nil {
		t.Fatal(err)
	}
	const patience = 30 * SimHz
	ticks := 0
	for ; ticks < patience; ticks++ {
		place(t, o, "a", behind)
		advance(o, 1)
		o.mu.Lock()
		_, still := o.occupants["a"]
		o.mu.Unlock()
		if !still {
			break
		}
	}
	if ticks >= patience {
		t.Fatalf("he never got round a desk to somebody standing %v away", behind)
	}
}

func TestHeWalksStraightWhenNothingIsInTheWay(t *testing.T) {
	// The fast path, and it is worth pinning: following grid cells across an
	// empty floor would move him in visible steps for no reason. With a clear
	// line he heads at the target itself.
	from := Vec2{X: BossSpawnX, Y: BossSpawnY}
	to := Vec2{X: BossSpawnX, Y: BossSpawnY - 3}
	if !clearLine(from, to, BossRadius) {
		t.Skip("the catalogue moved: this line is no longer clear")
	}
	if got := navAimAt(from, to); got != to {
		t.Fatalf("with a clear line he aimed at %+v rather than at the target %+v", got, to)
	}
}

func TestAPathStepIsSomewhereHeCanStand(t *testing.T) {
	// Whatever it hands back has to be a place his disc fits, or the resolver
	// spends the next tick pushing him out of the waypoint it was sent to.
	from := Vec2{X: BossSpawnX, Y: BossSpawnY}
	for _, to := range []Vec2{
		{X: 1, Y: 1}, {X: OfficeW - 1, Y: 1}, {X: 1, Y: OfficeH - 1},
		{X: Desks[0].X + Desks[0].W/2, Y: Desks[0].Y - 0.8},
	} {
		got := navAimAt(from, to)
		if got.X < 0 || got.X > OfficeW || got.Y < 0 || got.Y > OfficeH {
			t.Fatalf("heading for %+v he was sent off the floor to %+v", to, got)
		}
		if got == to {
			continue // the clear-line fast path
		}
		for i, d := range Desks {
			if insideDesk(d, got, BossRadius) {
				t.Fatalf("heading for %+v he was sent into desk %d at %+v", to, i, got)
			}
		}
	}
}

func TestTheRouteIsTheSameEveryTime(t *testing.T) {
	// Determinism is what every test of the chase rests on, and a flood fill is
	// exactly the sort of thing that stops being deterministic when somebody
	// reaches for a map.
	from := Vec2{X: BossSpawnX, Y: BossSpawnY}
	to := Vec2{X: Desks[0].X + Desks[0].W/2, Y: Desks[0].Y - 0.8}
	first := navAimAt(from, to)
	for i := 0; i < 50; i++ {
		if got := navAimAt(from, to); got != first {
			t.Fatalf("run %d routed him to %+v, the first run to %+v", i, got, first)
		}
	}
}
