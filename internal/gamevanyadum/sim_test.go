package gamevanyadum

import (
	"math"
	"reflect"
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
	// THE TRIGGER IS IN THE TRANSCRIPT, and it is the part that could go wrong in
	// a new way. Position is a function of the command alone; the gun is a
	// function of the command AND of how much of a countdown was left, so a
	// transcript that fires is the one that would catch a timer picking up
	// anything ambient. Two hundred steps of it empty the gun, which is what
	// carries the reload branch into the run as well.
	l := Generate(7)
	cmds := []Command{
		{Dt: dt, MY: 1, Yaw: 0, Fire: true},
		{Dt: dt, MY: 1, MX: 0.3, Yaw: 0.4},
		{Dt: dt, MY: -1, Yaw: 2.1, Fire: true},
		{Dt: dt, MX: -1, Yaw: 5.9, Fire: true},
	}
	run := func() (Player, bool) {
		p := NewPlayer(l)
		reloaded := false
		for i := 0; i < 200; i++ {
			p = Step(l, p, cmds[i%len(cmds)])
			reloaded = reloaded || p.ReloadLeft > 0
		}
		return p, reloaded
	}
	a, reloaded := run()
	b, _ := run()
	if a.Pos != b.Pos || a.Sector != b.Sector || a.Yaw != b.Yaw {
		t.Fatalf("two identical runs diverged: %+v vs %+v", a, b)
	}
	if a.Loaded != b.Loaded || a.CooldownLeft != b.CooldownLeft || a.ReloadLeft != b.ReloadLeft {
		t.Fatalf("two identical runs left different guns: %+v vs %+v", a, b)
	}
	// And the transcript really did reach the reload, or the paragraph above is
	// describing a branch this test never enters.
	if !reloaded {
		t.Fatal("two hundred steps of firing never reloaded; the transcript proves less than it claims")
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

// --- the gun ----------------------------------------------------------------
//
// The обрез is the first thing on Player that is READ-MODIFY-WRITE: a countdown
// is decremented where everything above it is replaced. So these tests are about
// two different things at once — what the weapon does, and the arithmetic
// property that decides the client's replay base. Applying one command to one
// state twice moves a countdown twice, so the predictor rebuilds from the
// SNAPSHOT's gun and never from its own memory of it (ADR-058, and the Player
// doc in sim.go).

// fireAt is a trigger pull that changes nothing else: the player's own angles,
// no movement, one sub-step of time. It is what somebody standing perfectly
// still and tapping the screen actually sends.
func fireAt(p Player, seconds float64) Command {
	return Command{Dt: seconds, Yaw: p.Yaw, Pitch: p.Pitch, Fire: true}
}

// holdStill is the same command with the trigger released.
func holdStill(p Player, seconds float64) Command {
	return Command{Dt: seconds, Yaw: p.Yaw, Pitch: p.Pitch}
}

// armed is somebody standing in the middle of a room with a full gun.
//
// IT TAKES NO AMMUNITION, and there is nothing to give it: the обрез reloads
// itself for nothing (content.go, ReloadSeconds) and what a man is carrying is
// not on the state Step reads at all (world.go, Occupant.bag). So every gun test
// below starts from exactly one thing — a full обрез — which is the only pocket
// content the simulation has ever been able to tell apart.
func armed() (*Level, Player) {
	l := room(0, 0, 20, 20, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 10, Y: 10}
	return l, p
}

func TestTheCadenceRefusesASecondShotInsideItAndAllowsOneAfter(t *testing.T) {
	// The rule the whole weapon hangs off. Without it a client that samples at
	// forty a second empties both barrels in 50 ms and reloads for ever, which is
	// not a fire rate, it is the absence of one.
	l, p := armed()

	fired := Step(l, p, fireAt(p, dt))
	if fired.Loaded != Barrels-1 {
		t.Fatalf("a shot left %d barrels loaded, expected %d", fired.Loaded, Barrels-1)
	}
	if math.Abs(fired.CooldownLeft-FireCooldownSeconds) > 1e-9 {
		t.Fatalf("the cadence started at %.4f s, the catalogue says %.4f", fired.CooldownLeft, FireCooldownSeconds)
	}

	// Now hold the trigger down. Every step inside the cadence must be refused,
	// and the first one outside it must not be.
	//
	// THE TIME IS ASSERTED AND THE STEP COUNT IS NOT, deliberately. Whether
	// 0.35 minus seven 0.05s lands on exactly zero or a hair above it is a
	// property of IEEE754 rather than of the gun, and a test that pinned the
	// count would fail on a retune for a reason that has nothing to do with what
	// it is named for.
	p = fired
	elapsed, shotAfter := 0.0, -1.0
	for i := 0; i < 2*SimHz && shotAfter < 0; i++ {
		busy := p.CooldownLeft > 0
		p = Step(l, p, fireAt(p, dt))
		elapsed += dt
		switch {
		case p.Loaded == Barrels-2:
			shotAfter = elapsed
		case !busy:
			t.Fatalf("the cadence had run out and a held trigger was still refused, %.3f s in", elapsed)
		}
	}
	if shotAfter < 0 {
		t.Fatal("the second barrel never fired, however long the trigger was held")
	}
	if math.Abs(shotAfter-FireCooldownSeconds) > dt+1e-9 {
		t.Fatalf("the second shot came %.4f s after the first; the cadence is %.4f", shotAfter, FireCooldownSeconds)
	}
}

func TestAnEmptyGunReloadsItselfOutOfEmptyPockets(t *testing.T) {
	// AMMUNITION IS INFINITE, and this is the whole of what that means: a man with
	// nothing in his pockets pulls the trigger on an empty gun and gets a reload,
	// every time, for as long as he keeps asking. What he does not get is the shot
	// — the ReloadSeconds of standing there unable to answer is the only thing a
	// reload has ever really charged, and it is untouched.
	l, p := armed()
	p.Loaded = 0

	started := Step(l, p, fireAt(p, dt))
	if started.ReloadLeft != ReloadSeconds {
		t.Fatalf("a pull on an empty gun left %.4f s of reload, expected %.4f",
			started.ReloadLeft, ReloadSeconds)
	}
	if started.Loaded != 0 {
		t.Fatalf("the pull that started the reload also put %d barrels in the gun", started.Loaded)
	}

	// And it is not a one-off that some starting state paid for: run the gun dry
	// twice over, holding the trigger the whole way, and the second empty gun
	// answers exactly as the first did.
	reloads := 0
	for i := 0; i < 10*SimHz; i++ {
		before := p.ReloadLeft
		p = Step(l, p, fireAt(p, dt))
		if before == 0 && p.ReloadLeft > 0 {
			reloads++
		}
	}
	if reloads < 2 {
		t.Fatalf("ten seconds of a held trigger produced %d reloads, so nothing here is repeating", reloads)
	}
}

func TestAReloadTakesWhatTheCatalogueSaysAndNoLess(t *testing.T) {
	// A reload that finished early would be a reload that is not the cost it is
	// described as, and the cheatsheet on the splash screen is generated from
	// exactly the number this asserts against.
	l, p := armed()
	p.Loaded = 0

	p = Step(l, p, fireAt(p, dt))
	if p.ReloadLeft != ReloadSeconds {
		t.Fatalf("the reload started at %.4f s, the catalogue says %.4f", p.ReloadLeft, ReloadSeconds)
	}

	elapsed, done := 0.0, -1.0
	for i := 0; i < 4*SimHz && done < 0; i++ {
		p = Step(l, p, fireAt(p, dt))
		elapsed += dt
		if p.Loaded > 0 {
			done = elapsed
		}
	}
	if done < 0 {
		t.Fatal("the reload never finished")
	}
	if done < ReloadSeconds-1e-9 {
		t.Fatalf("the barrels came back after %.4f s; the reload is %.4f", done, ReloadSeconds)
	}
	if done > ReloadSeconds+dt+1e-9 {
		t.Fatalf("the reload took %.4f s, more than the %.4f it is sold as", done, ReloadSeconds)
	}
	// And it fills the gun rather than putting one shell in it — the trigger was
	// held throughout, so one barrel is already gone by the time this is read.
	if p.Loaded != Barrels-1 {
		t.Fatalf("a finished reload left %d barrels with the trigger held, expected %d", p.Loaded, Barrels-1)
	}
}

func TestAGrantedShotSpendsABarrelAndNothingElse(t *testing.T) {
	// This iteration deliberately stops here: the ray, the damage, the death and
	// the respawn are the next one, and splitting them off is what makes the
	// input protocol and the first read-modify-write state on Player debuggable
	// without a hit test in the picture. If this ever fails, something started
	// hitting things early.
	l, p := armed()
	p.Yaw, p.Pitch = 1.1, -0.4

	got := Step(l, p, fireAt(p, dt))
	if got.Pos != p.Pos || got.Sector != p.Sector {
		t.Fatalf("firing moved him from %+v to %+v", p.Pos, got.Pos)
	}
	if got.Health != p.Health {
		t.Fatalf("firing changed his own health to %d", got.Health)
	}
	if got.Yaw != p.Yaw || got.Pitch != p.Pitch {
		t.Fatalf("firing moved the view to %.3f/%.3f", got.Yaw, got.Pitch)
	}
}

func TestTheGunRunsWhileStandingPerfectlyStill(t *testing.T) {
	// The ordering inside Step, and it is load-bearing: the movement half returns
	// early when both axes are zero, so a gun folded in after that return would
	// cool down only while its owner was walking. Standing still is the state
	// anybody is in while aiming at something.
	l, p := armed()
	fired := Step(l, p, fireAt(p, dt))

	got := Step(l, fired, holdStill(fired, dt))
	if got.Pos != fired.Pos {
		t.Fatalf("a command with no axes in it moved him to %+v", got.Pos)
	}
	if want := FireCooldownSeconds - dt; math.Abs(got.CooldownLeft-want) > 1e-9 {
		t.Fatalf("standing still left the cadence at %.4f, expected %.4f", got.CooldownLeft, want)
	}
}

func TestTheGunIsOnlyEverBusyForOneReason(t *testing.T) {
	// The invariant the wire budget is measured on: a cadence and a reload are
	// never both running, so the widest frame this game can send carries the
	// barrel count and ONE timer (message_test.go). It is a property of the
	// trigger rule rather than an accident — a pull is refused while either timer
	// is up, and nothing else starts one — and this is the sweep that says so
	// over a long transcript rather than over a single path.
	l := Generate(23)
	p := NewPlayer(l)
	yaw := 0.0
	for i := 0; i < 4000; i++ {
		// A deterministic wander with the trigger pulled on an irregular beat, so
		// the transcript crosses cadence and reload boundaries at every phase
		// rather than at one.
		yaw += 0.37
		p = Step(l, p, Command{Dt: dt, MY: float64(i % 2), Yaw: yaw, Fire: i%7 < 3})
		if p.CooldownLeft > 0 && p.ReloadLeft > 0 {
			t.Fatalf("step %d: the gun is cooling (%.3f) and reloading (%.3f) at once", i, p.CooldownLeft, p.ReloadLeft)
		}
		if p.Loaded < 0 || p.Loaded > Barrels {
			t.Fatalf("step %d: %d barrels loaded", i, p.Loaded)
		}
		if p.CooldownLeft > FireCooldownSeconds+1e-9 || p.ReloadLeft > ReloadSeconds+1e-9 {
			t.Fatalf("step %d: a timer above what the catalogue sets it to: %+v", i, p)
		}
	}
}

func TestSteppingAPlayerCannotReachThroughToTheCaller(t *testing.T) {
	// THE HAZARD THAT USED TO LIVE ON THE BAG, WIDENED TO THE WHOLE STRUCT. Step
	// takes a Player BY VALUE, which copies every field except the contents of a
	// reference one — so a map or a slice on Player is a channel back into
	// whatever the caller is holding, and the client replays every pending command
	// on top of each snapshot it reconciles against (ADR-058). A write through one
	// would land once per REPLAY rather than once per command, which is a quantity
	// draining at the round-trip rate rather than at the rate the game charges.
	//
	// The bag was the one such field and it is gone (world.go, Occupant.bag), so
	// the invariant is now stronger than "the reload takes nothing": there is
	// NOTHING on this struct a step could reach through, and this is what says so
	// for every field that will ever be added rather than for the one that made
	// the point. A reviewer who needs shared state on Player has to delete this
	// test deliberately.
	for i, f := range reflect.VisibleFields(reflect.TypeOf(Player{})) {
		switch f.Type.Kind() {
		case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Chan, reflect.Func:
			t.Fatalf("field %d, Player.%s, is a %s: Step takes a Player by value, so writing through it would move the caller's own state",
				i, f.Name, f.Type.Kind())
		}
	}

	// And the behaviour that guards: the same command run twice from the same
	// base produces the same reload, rather than a second one that started from
	// something the first left behind.
	l, p := armed()
	p.Loaded = 0

	first := Step(l, p, fireAt(p, dt))
	second := Step(l, p, fireAt(p, dt))
	if first != second {
		t.Fatalf("two runs of one command from one base disagree: %+v and %+v", first, second)
	}
	if first.ReloadLeft != ReloadSeconds {
		t.Fatalf("the reload started at %.4f s, the catalogue says %.4f", first.ReloadLeft, ReloadSeconds)
	}
}

func TestReplayingACommandDecrementsTheCooldownAgain(t *testing.T) {
	// NOT A DEFECT — the property the client's predictor has to be built around,
	// stated on the server where it can be proved.
	//
	// Everything else on Player is REPLACED: reconciliation overwrites the
	// position and the sector from the snapshot and takes the angles from the
	// client, so replaying a pending command on top of an authoritative state
	// produces the same answer however many times it runs. A countdown does not
	// work that way. Apply it twice and it is decremented twice.
	//
	// WHICH IS WHY THE PREDICTOR'S REPLAY BASE IS THE SNAPSHOT'S GUN. It drops
	// every command the snapshot has acknowledged before replaying anything, so
	// the frame it rebuilds from is exactly the state as it stood before the
	// oldest command still pending — and replaying that list there lands each
	// one once. The base that would be wrong is the client's own predicted
	// player, which already holds their effects: every reconcile would then run
	// the cooldown down a second time and the gun would read ready long before
	// it was. That is ADR-058's rule stated plainly — every predicted duration
	// comes off the frame on every reconcile — and it is the mutation five of
	// the port's specs fail on.
	//
	// Until this iteration the property was a provable no-op, because there was
	// nothing on Player a second application could disagree about. This is what
	// stops it being one.
	l, p := armed()
	fired := Step(l, p, fireAt(p, dt))
	c := holdStill(fired, dt)

	once := Step(l, fired, c)
	twice := Step(l, once, c)
	if math.Abs((once.CooldownLeft-twice.CooldownLeft)-dt) > 1e-9 {
		t.Fatalf("applying the same command twice moved the cadence by %.4f, one step is %.4f",
			once.CooldownLeft-twice.CooldownLeft, dt)
	}

	// Where the position, applied to the same starting state twice, lands in the
	// same place both times — which is the whole reason the hazard is new.
	walk := Command{Dt: dt, MY: 1, Yaw: 0}
	if a, b := Step(l, p, walk), Step(l, p, walk); a.Pos != b.Pos {
		t.Fatalf("two runs of one movement command disagree: %+v vs %+v", a.Pos, b.Pos)
	}
}

// --- the шприц ---------------------------------------------------------------

// injecting is somebody standing in the middle of a room, hurt by `hurt`, with a
// full ampoule running. It is the state collect leaves a man in the tick after he
// walks over one (world.go).
func injecting(hurt int) (*Level, Player) {
	l, p := armed()
	p.Health = MaxHealth - hurt
	p.InjectLeft = SyringeSeconds
	return l, p
}

// walkHard is a command with the stick fully forward, which is what a rooted man
// is sending while he waits: he does not know he is rooted until the frame comes
// back saying so, and his thumb is already down.
func walkHard(p Player, seconds float64) Command {
	return Command{Dt: seconds, MX: 0.5, MY: 1, Yaw: p.Yaw, Pitch: p.Pitch}
}

func TestTheAmpouleRunsDownHealsAndFinishes(t *testing.T) {
	// The whole of the verb, in one transcript: the countdown falls, the health
	// climbs while it falls, and both land exactly where the catalogue says.
	l, p := injecting(SyringeHeal)
	start := p.Health

	// Sub-steps that do NOT divide the duration, so the last one lands past the
	// end rather than exactly on it — which is where an implementation that
	// subtracted without clamping would go negative and heal past the ampoule.
	const sub = 0.03
	last := p.Health
	for p.InjectLeft > 0 {
		next := Step(l, p, holdStill(p, sub))
		if next.Health < last {
			t.Fatalf("health went DOWN during an injection: %d then %d", last, next.Health)
		}
		if next.InjectLeft >= p.InjectLeft {
			t.Fatalf("the countdown did not move: %.4f then %.4f", p.InjectLeft, next.InjectLeft)
		}
		p, last = next, next.Health
	}
	if p.InjectLeft != 0 {
		t.Fatalf("the ampoule finished at %.6f rather than at zero", p.InjectLeft)
	}
	if got := p.Health - start; got != SyringeHeal {
		t.Fatalf("one ampoule delivered %d, the catalogue says %d", got, SyringeHeal)
	}

	// And it stays finished: a step after the end is an ordinary step, so nothing
	// goes on climbing.
	if after := Step(l, p, holdStill(p, sub)); after.Health != p.Health {
		t.Fatalf("health moved to %d after the ampoule was empty", after.Health)
	}
}

func TestTheAmpouleDoesNotOverheal(t *testing.T) {
	// A man 6 short of full against an ampoule holding SyringeHeal. The surplus is
	// lost rather than banked, AND THE TIME IS STILL SPENT — the ampoule is spent
	// on the clock rather than on the health, which is what makes injecting at
	// nearly full a bad trade instead of a free top-up (content.go).
	const short = 6
	l, p := injecting(short)

	filled := false
	for i := 0; p.InjectLeft > 0 && i < 1000; i++ {
		p = Step(l, p, walkHard(p, 0.03))
		if p.Health > MaxHealth {
			t.Fatalf("health reached %d, the cap is %d", p.Health, MaxHealth)
		}
		if p.Health == MaxHealth && !filled {
			filled = true
			// Full, and still rooted: the rest of the duration is owed whatever the
			// bar says.
			if p.InjectLeft <= 0 {
				t.Fatal("the ampoule ended the moment the bar filled, so the overheal costs nothing")
			}
		}
	}
	if !filled {
		t.Fatalf("an ampoule of %d on a man %d short never filled him", SyringeHeal, short)
	}
	if p.Health != MaxHealth {
		t.Fatalf("finished at %d rather than at the cap of %d", p.Health, MaxHealth)
	}
}

func TestAManWithANeedleInHisArmCannotWalkAndCannotFire(t *testing.T) {
	// The window, which is the whole reason the heal is not instant. He is sending
	// everything he has — full stick, trigger held — and none of it lands until the
	// countdown reaches zero.
	l, p := injecting(SyringeHeal)
	where, loaded := p.Pos, p.Loaded

	// Enough steps to outlast SyringeSeconds and then some, at a sub-step that
	// does not divide it — so the window expires in the middle of one rather than
	// on a boundary.
	//
	// THE ASSERTION IS ON THE STATE AFTER EACH STEP AND NOT BEFORE IT, because the
	// step that EMPTIES the ampoule is deliberately a step he can use (see
	// TestTheStepAnAmpouleEndsOnIsAStepHeCanUse). So the loop stops the moment the
	// countdown reaches zero, and everything before that is the window.
	done := false
	for i := 0; i < 200 && !done; i++ {
		p = Step(l, p, Command{Dt: 0.03, MX: 0.5, MY: 1, Yaw: 0.4, Fire: true})
		if p.InjectLeft == 0 {
			done = true
			break
		}
		if p.Pos != where {
			t.Fatalf("step %d: he moved to %+v with an ampoule running", i, p.Pos)
		}
		if p.Loaded != loaded {
			t.Fatalf("step %d: a barrel went while an ampoule was running", i)
		}
		if p.CooldownLeft != 0 || p.ReloadLeft != 0 {
			t.Fatalf("step %d: the gun started something (cd %.3f, reload %.3f)", i, p.CooldownLeft, p.ReloadLeft)
		}
	}
	if !done {
		t.Fatalf("the ampoule is still running after 6 s of steps: %.3f left", p.InjectLeft)
	}

	// The yaw landed throughout, because the camera is the client's and taking it
	// away is the one thing a first-person game may not do.
	if math.Abs(p.Yaw-0.4) > 1e-9 {
		t.Fatalf("his view is at %.4f rather than where he pointed it", p.Yaw)
	}

	// And it really has ended: the step that emptied it already walked and fired.
	if p.Pos == where {
		t.Fatal("he is still rooted on the step the ampoule emptied")
	}
	if p.Loaded != loaded-1 {
		t.Fatalf("the pull on the step the ampoule emptied left %d barrels, expected %d", p.Loaded, loaded-1)
	}
}

func TestTheStepAnAmpouleEndsOnIsAStepHeCanUse(t *testing.T) {
	// The same ordering rule the gun already has: the timer is advanced and THEN
	// the trigger is read, so a shot asked for on the step an injection finishes
	// is honoured rather than refused by a state the arm has just left. At 20 Hz
	// the difference is a tap eaten for no reason a player can see.
	l, p := injecting(SyringeHeal)
	p.InjectLeft = dt // exactly one sub-step left

	end := Step(l, p, Command{Dt: dt, MY: 1, Yaw: 0, Fire: true})
	if end.InjectLeft != 0 {
		t.Fatalf("the ampoule has %.4f left after the step that should have emptied it", end.InjectLeft)
	}
	if end.Loaded != Barrels-1 {
		t.Fatal("the pull on the step the ampoule emptied was refused")
	}
	if end.Pos == p.Pos {
		t.Fatal("he did not move on the step the ampoule emptied")
	}
}

func TestReplayingACommandAdvancesTheAmpouleAgain(t *testing.T) {
	// The ampoule is a COUNTDOWN, so it has the same reconciliation property the
	// gun's timers have and needs the same replay base: the snapshot's, never the
	// client's own predicted player (ADR-058, and the Player doc in sim.go). A
	// predictor that replayed pending commands on top of its own state would burn
	// the injection at the round-trip rate — the man would be walking and shooting
	// in the browser while the server still had him rooted, which is the single
	// most visible mistake this iteration can produce.
	l, p := injecting(SyringeHeal)
	c := holdStill(p, dt)

	once := Step(l, p, c)
	twice := Step(l, once, c)
	if math.Abs((once.InjectLeft-twice.InjectLeft)-dt) > 1e-9 {
		t.Fatalf("applying the same command twice moved the ampoule by %.4f, one step is %.4f",
			once.InjectLeft-twice.InjectLeft, dt)
	}

	// And the health follows the countdown rather than a sum of its own history,
	// which is what makes replaying from a snapshot land on the server's number:
	// two different routes to the same remaining time produce the same health.
	direct := Step(l, p, holdStill(p, 2*dt))
	if math.Abs(direct.InjectLeft-twice.InjectLeft) > 1e-9 {
		t.Fatalf("two sub-steps left %.6f and one double step left %.6f", twice.InjectLeft, direct.InjectLeft)
	}
	if direct.Health != twice.Health {
		t.Fatalf("the same remaining time produced %d health one way and %d the other", direct.Health, twice.Health)
	}
}

func TestADeadManHasNoAmpouleRunning(t *testing.T) {
	// Step returns at once for a man with no health, so an ampoule that somehow
	// survived his death would sit on him at whatever it read — and `dn` on the
	// wire carries the injection or the respawn depending on exactly that health
	// (message.go, Snapshot.Down). The world is what clears it (world.go, hurt);
	// this is the assertion that Step does not run one on a corpse if it ever did.
	l, p := injecting(SyringeHeal)
	p.Health = 0
	before := p

	after := Step(l, p, Command{Dt: dt, MX: 1, MY: 1, Yaw: 1, Fire: true})
	if after.InjectLeft != before.InjectLeft {
		t.Fatalf("the ampoule moved on a man with no health: %.4f then %.4f", before.InjectLeft, after.InjectLeft)
	}
	if after.Health != 0 {
		t.Fatalf("a corpse healed to %d", after.Health)
	}
}

func TestAnAmpouleKeepsTheIdleFillRunning(t *testing.T) {
	// A man being injected is standing PERFECTLY STILL and sending nothing at all
	// (web/src/lib/vanyadumInput.ts, `due`), so the world's idle fill is the only
	// thing advancing him — and the fill runs only while something on him is
	// counting down. A timer missing from `ticking` is a timer that stops the
	// moment its owner stops moving, which for this one is every tick of it.
	var p Player
	p.InjectLeft = SyringeSeconds
	if !p.ticking() {
		t.Fatal("a running ampoule does not count as ticking, so the idle fill will not advance it")
	}
	p.InjectLeft = 0
	if p.ticking() {
		t.Fatal("an empty ampoule keeps the idle fill running, which taxes standing still for nothing")
	}
}

func TestTheAmpouleIsSomethingTheBuildingActuallyScatters(t *testing.T) {
	// The injection's constants are the SIMULATION's and the thing that starts one
	// is a CATALOGUE entry, so the two have to keep pointing at each other: a
	// catalogue with no medicine in it means nothing can ever start an injection —
	// silently, because a building simply would not scatter any.
	for _, k := range Pickups {
		if k.Heals <= 0 {
			continue
		}
		if k.InjectSeconds <= 0 {
			t.Fatalf("%q heals %d over %.2f s, so it heals instantly and the window this iteration is about does not exist",
				k.Key, k.Heals, k.InjectSeconds)
		}
		if k.Grants != "" {
			t.Fatalf("%q both heals and grants %q; medicine is used rather than carried, so a bag entry would be a counter nobody can ever look at",
				k.Key, k.Grants)
		}
		return
	}
	t.Fatal("nothing in the catalogue heals, so no ampoule can ever be started")
}

func TestEveryPickupKindIsWorthWalkingOver(t *testing.T) {
	// THE INVARIANT THAT REPLACED A RUNTIME GUARD, and it is worth saying which
	// one. The collection loop used to clamp a counter to a ceiling and then check
	// whether anything had actually been gained — after it had already stamped the
	// respawn deadline, so a man at the ceiling destroyed the bottle for
	// PickupRespawn, got nothing, and took it from the other two occupants as
	// well, since that clock is the building's rather than his.
	//
	// The ceiling is gone (content.go, PickupKind) and with it the only way the
	// generic branch could decline, so the branch consumes unconditionally and
	// world.go says so. That is only true while every kind actually hands
	// something over, which is a property of the CATALOGUE and therefore belongs
	// here: a kind granting nothing would silently reintroduce exactly the state
	// the loop no longer checks for.
	for _, k := range Pickups {
		heals := k.Heals > 0
		grants := k.Grants != "" && k.Amount > 0
		if heals == grants {
			t.Fatalf("%q heals %d and grants %d of %q: a kind does exactly one of the two, "+
				"or walking over it consumes it for %s and hands the player nothing",
				k.Key, k.Heals, k.Amount, k.Grants, PickupRespawn)
		}
	}
}
