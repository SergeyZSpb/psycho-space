package gamekaren

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestHeClosesOnTheNearestTarget(t *testing.T) {
	b := Boss{Pos: Vec2{X: 8, Y: 11}}
	near := Vec2{X: 8, Y: 9}
	far := Vec2{X: 8, Y: 1}

	got := StepBoss(nil, b, []Vec2{far, near}, testDt)
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
		if got := StepBoss(nil, b, []Vec2{left, right}, testDt); got.Pos.X >= b.Pos.X {
			t.Fatalf("run %d: he went right (%v) when the left target was listed first", i, got.Pos.X)
		}
		if got := StepBoss(nil, b, []Vec2{right, left}, testDt); got.Pos.X <= b.Pos.X {
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
		b = StepBoss(Desks, b, []Vec2{target}, testDt)
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
		next := StepBoss(nil, b, []Vec2{target}, testDt)
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
	if !Caught(b, Vec2{X: 8 + reach, Y: 8}) {
		t.Fatalf("at exactly %v he had not caught anybody", reach)
	}
	if !Caught(b, Vec2{X: 8, Y: 8}) {
		t.Fatal("standing on him is not being caught")
	}
	if Caught(b, Vec2{X: 8 + reach + 0.01, Y: 8}) {
		t.Fatalf("he caught somebody a centimetre outside %v", reach)
	}
}

func TestWithNobodyToChaseHeGoesHomeAndStops(t *testing.T) {
	// The office is never empty of a boss; an office empty of PEOPLE is torn
	// down entirely. This is what he does in between.
	b := Boss{Pos: Vec2{X: 2, Y: 4}}
	for i := 0; i < 1000; i++ {
		b = StepBoss(Desks, b, nil, testDt)
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
		b = StepBoss(Desks, b, nil, testDt)
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
