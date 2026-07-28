package gamevanyadum

import (
	"math"
	"testing"
)

// The simulation is tested against HAND-BUILT levels, not generated ones,
// wherever the claim is about a specific piece of geometry — a wall in a known
// place is the only way to say "he should stop here" and mean it. The generated
// levels come back at the bottom of the file, for the claims that are about
// every level rather than about one.

// room builds a single square room with no way out, walls derived exactly as
// the generator derives them.
func room(minX, minY, maxX, maxY, floorZ float64) *Level {
	l := &Level{Sectors: []Sector{{
		ID: 0, MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY,
		FloorZ: floorZ, CeilZ: floorZ + CeilingHeight,
	}}}
	l.Walls = buildWalls(l)
	l.Spawn = l.Sectors[0].Center()
	return l
}

// twoRooms builds a pair side by side, sharing the line x = 10, with a doorway
// through the middle of it. rightFloor is the floor of the right-hand room, so a
// test can put a step — or a ledge — in the doorway.
func twoRooms(rightFloor float64) *Level {
	l := &Level{
		Sectors: []Sector{
			{ID: 0, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, FloorZ: 0, CeilZ: CeilingHeight},
			{ID: 1, MinX: 10, MinY: 0, MaxX: 20, MaxY: 10, FloorZ: rightFloor, CeilZ: rightFloor + CeilingHeight},
		},
		Portals: []Portal{{A: 0, B: 1, Vertical: true, At: 10, Lo: 4, Hi: 6}},
	}
	l.Walls = buildWalls(l)
	return l
}

// walk runs n identical steps, which is what the arena does with a batch of
// commands. dt is one simulation step.
func walk(l *Level, p Player, c Command, n int) Player {
	for i := 0; i < n; i++ {
		p = Step(l, p, c)
	}
	return p
}

const dt = 1.0 / SimHz

func TestYawZeroWalksTowardsPositiveY(t *testing.T) {
	// The convention has to be pinned somewhere, because the client's camera
	// shares it and the two disagreeing looks like broken controls rather than
	// like a broken transform. Yaw zero faces +Y; strafing right is +X.
	l := room(0, 0, 20, 20, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 10, Y: 10}

	fwd := walk(l, p, Command{Dt: dt, MY: 1, Yaw: 0}, SimHz) // one second
	if fwd.Pos.Y <= 10.5 || math.Abs(fwd.Pos.X-10) > 1e-9 {
		t.Fatalf("walking forward at yaw 0 should go +Y and nothing else, got %+v", fwd.Pos)
	}
	if got := fwd.Pos.Y - 10; math.Abs(got-WalkSpeed) > 0.05 {
		t.Fatalf("one second of walking covered %.2f m, WalkSpeed is %.2f", got, WalkSpeed)
	}

	right := walk(l, p, Command{Dt: dt, MX: 1, Yaw: 0}, SimHz)
	if right.Pos.X <= 10.5 || math.Abs(right.Pos.Y-10) > 1e-9 {
		t.Fatalf("strafing right at yaw 0 should go +X and nothing else, got %+v", right.Pos)
	}
}

func TestDiagonalIsNotFaster(t *testing.T) {
	// The oldest bug in first-person movement. Without normalisation, holding
	// forward and right is 1.41 times the speed of holding forward — which is
	// not a feel problem, it is a movement exploit that every player finds.
	l := room(0, 0, 40, 40, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 5, Y: 5}

	straight := walk(l, p, Command{Dt: dt, MY: 1, Yaw: 0}, SimHz)
	diagonal := walk(l, p, Command{Dt: dt, MY: 1, MX: 1, Yaw: 0}, SimHz)

	sd := math.Hypot(straight.Pos.X-5, straight.Pos.Y-5)
	dd := math.Hypot(diagonal.Pos.X-5, diagonal.Pos.Y-5)
	if math.Abs(sd-dd) > 0.01 {
		t.Fatalf("diagonal covered %.3f m, straight covered %.3f m", dd, sd)
	}
}

func TestHalfPressedStickIsSlower(t *testing.T) {
	// The other half of the same rule: normalising unconditionally would make a
	// gentle push on a touch stick move at full speed, which is what turns an
	// analogue control into a digital one.
	l := room(0, 0, 40, 40, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 5, Y: 5}

	full := walk(l, p, Command{Dt: dt, MY: 1, Yaw: 0}, SimHz)
	half := walk(l, p, Command{Dt: dt, MY: 0.5, Yaw: 0}, SimHz)
	if got := (half.Pos.Y - 5) / (full.Pos.Y - 5); math.Abs(got-0.5) > 0.01 {
		t.Fatalf("half a stick moved %.2f of the distance, expected half", got)
	}
}

func TestWallStopsHimAtHisOwnRadius(t *testing.T) {
	l := room(0, 0, 10, 10, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 5, Y: 5}

	// Far more walking than the room is deep.
	end := walk(l, p, Command{Dt: dt, MY: 1, Yaw: 0}, 4*SimHz)
	if want := 10 - PlayerRadius; math.Abs(end.Pos.Y-want) > 1e-6 {
		t.Fatalf("stopped at y=%.4f, expected %.4f (the wall minus his radius)", end.Pos.Y, want)
	}
	if l.SectorAt(end.Pos) < 0 {
		t.Fatal("ended up outside the room")
	}
}

func TestHeSlidesAlongAWallRatherThanSticking(t *testing.T) {
	// Sliding is not a feature that was written; it falls out of pushing the
	// disc out of the wall it overlaps. This test is what says so — and what
	// would fail if somebody replaced the resolver with one that refuses a
	// blocked move outright, which feels like walking into glue.
	l := room(0, 0, 20, 20, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 5, Y: 19}

	// Facing 45°, so half the movement is into the far wall and half is along it.
	end := walk(l, p, Command{Dt: dt, MY: 1, Yaw: math.Pi / 4}, SimHz)
	if math.Abs(end.Pos.Y-(20-PlayerRadius)) > 1e-6 {
		t.Fatalf("should be pressed against the wall, y=%.4f", end.Pos.Y)
	}
	if end.Pos.X-5 < 2 {
		t.Fatalf("should have slid along it, moved only %.2f m in x", end.Pos.X-5)
	}
}

func TestHeCannotWalkThroughASolidSharedWall(t *testing.T) {
	l := twoRooms(0)
	p := NewPlayer(l)
	// Level with the lower half of the shared wall, well away from the doorway.
	p.Pos = Vec2{X: 9, Y: 1}
	p.Sector = 0

	end := walk(l, p, Command{Dt: dt, MY: 1, Yaw: math.Pi / 2}, 2*SimHz) // due +X
	if end.Sector != 0 {
		t.Fatalf("walked into sector %d through a solid wall", end.Sector)
	}
	if want := 10 - PlayerRadius; math.Abs(end.Pos.X-want) > 1e-6 {
		t.Fatalf("stopped at x=%.4f, expected %.4f", end.Pos.X, want)
	}
}

func TestHeWalksThroughTheDoorway(t *testing.T) {
	l := twoRooms(0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 9, Y: 5} // lined up with the opening at y 4..6
	p.Sector = 0

	end := walk(l, p, Command{Dt: dt, MY: 1, Yaw: math.Pi / 2}, 2*SimHz)
	if end.Sector != 1 {
		t.Fatalf("still in sector %d, expected to be through the door", end.Sector)
	}
	if end.Pos.X <= 10 {
		t.Fatalf("did not cross the threshold, x=%.4f", end.Pos.X)
	}
}

func TestHeStepsUpASmallRiseAndNotABigOne(t *testing.T) {
	// Stairs work because a small rise is simply walked up, with no jump and no
	// vertical velocity anywhere in the model. The upper bound is what stops a
	// generated ledge becoming a free lift.
	up := twoRooms(MaxStep - 0.05)
	p := NewPlayer(up)
	p.Pos, p.Sector = Vec2{X: 9, Y: 5}, 0
	if end := walk(up, p, Command{Dt: dt, MY: 1, Yaw: math.Pi / 2}, 2*SimHz); end.Sector != 1 {
		t.Fatalf("should have stepped up %.2f m, still in sector %d", MaxStep-0.05, end.Sector)
	}

	ledge := twoRooms(MaxStep + 0.5)
	p = NewPlayer(ledge)
	p.Pos, p.Sector = Vec2{X: 9, Y: 5}, 0
	if end := walk(ledge, p, Command{Dt: dt, MY: 1, Yaw: math.Pi / 2}, 2*SimHz); end.Sector != 0 {
		t.Fatalf("climbed a %.2f m ledge with MaxStep %.2f", MaxStep+0.5, MaxStep)
	}
}

func TestSanitiseClampsEverythingHostile(t *testing.T) {
	// Every field of a command is attacker-controlled. None of these is exotic:
	// a NaN is one malformed float away, and a huge dt is what a backgrounded
	// tab produces honestly.
	got := Command{
		Dt:    1e9,
		MX:    math.Inf(1),
		MY:    -500,
		Yaw:   math.NaN(),
		Pitch: 99,
	}.Sanitise()

	if got.Dt != MaxStepSeconds {
		t.Fatalf("dt not clamped: %v", got.Dt)
	}
	if got.MX != 1 || got.MY != -1 {
		t.Fatalf("movement axes not clamped: %v %v", got.MX, got.MY)
	}
	if got.Yaw != 0 {
		t.Fatalf("NaN yaw should become zero, got %v", got.Yaw)
	}
	if got.Pitch != MaxPitch {
		t.Fatalf("pitch not clamped: %v", got.Pitch)
	}
}

func TestAHostileCommandCannotTeleportOutOfTheLevel(t *testing.T) {
	// Sanitise is applied inside Step rather than at the edge, so there is no
	// path into the simulation that skips it. This is that promise, tested from
	// the outside.
	l := room(0, 0, 10, 10, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 5, Y: 5}

	end := Step(l, p, Command{Dt: 1e6, MY: 1e6, Yaw: 0})
	if l.SectorAt(end.Pos) < 0 {
		t.Fatalf("left the level: %+v", end.Pos)
	}
	if end.Pos.Y > 10 {
		t.Fatalf("walked through the far wall to y=%.2f", end.Pos.Y)
	}
}

func TestStepIsDeterministic(t *testing.T) {
	// The single most valuable test in this package. Everything else it buys is
	// downstream: table-testable movement, a replay that reproduces a run, and —
	// if the netcode ever climbs to client-side prediction — a TypeScript port
	// that can be pinned against golden vectors generated from here.
	l := Generate(7)
	cmds := []Command{
		{Dt: dt, MY: 1, Yaw: 0},
		{Dt: dt, MY: 1, MX: 0.3, Yaw: 0.4},
		{Dt: dt, MY: -1, Yaw: 2.1},
		{Dt: dt, MX: -1, Yaw: 5.9},
	}
	run := func() Player {
		p := NewPlayer(l)
		for i := 0; i < 200; i++ {
			p = Step(l, p, cmds[i%len(cmds)])
		}
		return p
	}
	a, b := run(), run()
	if a.Pos != b.Pos || a.Sector != b.Sector || a.Yaw != b.Yaw {
		t.Fatalf("two identical runs diverged: %+v vs %+v", a, b)
	}
}

func TestNoWalkEverLeavesAGeneratedLevel(t *testing.T) {
	// The sweep the hand-built cases cannot do: hundreds of levels, each walked
	// by a deterministic pseudo-random wanderer, asserting only that the player
	// is always somewhere. Escaping the level is the failure that turns into a
	// black screen and a player falling forever, and it is exactly the failure a
	// generator produces on the one shape nobody drew by hand.
	for seed := int64(0); seed < 80; seed++ {
		l := Generate(seed)
		p := NewPlayer(l)
		yaw := 0.0
		for i := 0; i < 600; i++ {
			// A cheap deterministic wander: turn by an irrational-ish amount so
			// the walk covers directions without repeating.
			yaw += 0.37
			p = Step(l, p, Command{Dt: dt, MY: 1, Yaw: yaw})
			if l.SectorAt(p.Pos) < 0 {
				t.Fatalf("seed %d step %d: escaped the level at %+v", seed, i, p.Pos)
			}
		}
	}
}

func TestEyeHeightFollowsTheFloorHeIsStandingOn(t *testing.T) {
	l := twoRooms(0.6)
	p := NewPlayer(l)
	p.Sector = 0
	if got := EyeZ(l, p); math.Abs(got-EyeHeight) > 1e-9 {
		t.Fatalf("eye at %.3f in the lower room, expected %.3f", got, EyeHeight)
	}
	p.Sector = 1
	if got := EyeZ(l, p); math.Abs(got-(0.6+EyeHeight)) > 1e-9 {
		t.Fatalf("eye at %.3f in the upper room, expected %.3f", got, 0.6+EyeHeight)
	}
}
