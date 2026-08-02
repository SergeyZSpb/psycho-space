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
	// THE TRIGGER IS IN THE TRANSCRIPT, and it is the part that could go wrong in
	// a new way. Position is a function of the command alone; the gun is a
	// function of the command AND of how much of a countdown was left, so a
	// transcript that fires is the one that would catch a timer picking up
	// anything ambient. Beer is in the bag so the reload branch is reached too.
	l := Generate(7)
	cmds := []Command{
		{Dt: dt, MY: 1, Yaw: 0, Fire: true},
		{Dt: dt, MY: 1, MX: 0.3, Yaw: 0.4},
		{Dt: dt, MY: -1, Yaw: 2.1, Fire: true},
		{Dt: dt, MX: -1, Yaw: 5.9, Fire: true},
	}
	run := func() Player {
		p := NewPlayer(l)
		p.Counters[AmmoCounter] = 3
		for i := 0; i < 200; i++ {
			p = Step(l, p, cmds[i%len(cmds)])
		}
		return p
	}
	a, b := run(), run()
	if a.Pos != b.Pos || a.Sector != b.Sector || a.Yaw != b.Yaw {
		t.Fatalf("two identical runs diverged: %+v vs %+v", a, b)
	}
	if a.Loaded != b.Loaded || a.CooldownLeft != b.CooldownLeft || a.ReloadLeft != b.ReloadLeft {
		t.Fatalf("two identical runs left different guns: %+v vs %+v", a, b)
	}
	if a.Counters[AmmoCounter] != b.Counters[AmmoCounter] {
		t.Fatalf("two identical runs spent different amounts of beer: %v vs %v", a.Counters, b.Counters)
	}
	// And the transcript really did reach the reload, or the paragraph above is
	// describing a branch this test never enters.
	if a.Counters[AmmoCounter] == 3 {
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

// armed is somebody standing in the middle of a room with a full gun and n
// bottles in his pockets.
func armed(ammo int) (*Level, Player) {
	l := room(0, 0, 20, 20, 0)
	p := NewPlayer(l)
	p.Pos = Vec2{X: 10, Y: 10}
	if ammo > 0 {
		p.Counters[AmmoCounter] = ammo
	}
	return l, p
}

func TestTheCadenceRefusesASecondShotInsideItAndAllowsOneAfter(t *testing.T) {
	// The rule the whole weapon hangs off. Without it a client that samples at
	// forty a second empties both barrels in 50 ms and reloads for ever, which is
	// not a fire rate, it is the absence of one.
	l, p := armed(0)

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

func TestAnEmptyGunWithNothingToLoadCannotFire(t *testing.T) {
	// What makes the bottles worth walking to. A player with no beer is not
	// slowed down or made weaker, he is disarmed — which is the only version of
	// scarcity that survives a game where nothing else is spent.
	l, p := armed(0)
	p.Loaded = 0

	dry := p
	for i := 0; i < SimHz; i++ {
		dry = Step(l, dry, fireAt(dry, dt))
	}
	if dry.Loaded != 0 || dry.ReloadLeft != 0 || dry.CooldownLeft != 0 {
		t.Fatalf("a second of pulling a dry trigger produced %+v", dry)
	}
	if len(dry.Counters) != 0 {
		t.Fatalf("something was spent out of an empty bag: %v", dry.Counters)
	}

	// One bottle is the whole difference.
	p.Counters[AmmoCounter] = ReloadCost
	if wet := Step(l, p, fireAt(p, dt)); wet.ReloadLeft != ReloadSeconds {
		t.Fatalf("the same pull with a bottle in the bag left %.4f s of reload", wet.ReloadLeft)
	}
}

func TestAReloadTakesWhatTheCatalogueSaysAndNoLess(t *testing.T) {
	// A reload that finished early would be a reload that is not the cost it is
	// described as, and the cheatsheet on the splash screen is generated from
	// exactly the number this asserts against.
	l, p := armed(ReloadCost)
	p.Loaded = 0

	p = Step(l, p, fireAt(p, dt))
	if p.ReloadLeft != ReloadSeconds {
		t.Fatalf("the reload started at %.4f s, the catalogue says %.4f", p.ReloadLeft, ReloadSeconds)
	}
	if got := p.Counters[AmmoCounter]; got != 0 {
		t.Fatalf("the bottle was not spent when the reload started: %d left", got)
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
	l, p := armed(2)
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
	if got.Counters[AmmoCounter] != 2 {
		t.Fatalf("a shot spent beer as well as a barrel: %v", got.Counters)
	}
}

func TestTheGunRunsWhileStandingPerfectlyStill(t *testing.T) {
	// The ordering inside Step, and it is load-bearing: the movement half returns
	// early when both axes are zero, so a gun folded in after that return would
	// cool down only while its owner was walking. Standing still is the state
	// anybody is in while aiming at something.
	l, p := armed(0)
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
	p.Counters[AmmoCounter] = 9
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
		if n := p.Counters[AmmoCounter]; n < 0 {
			t.Fatalf("step %d: %d bottles in the bag", i, n)
		}
		if p.CooldownLeft > FireCooldownSeconds+1e-9 || p.ReloadLeft > ReloadSeconds+1e-9 {
			t.Fatalf("step %d: a timer above what the catalogue sets it to: %+v", i, p)
		}
	}
}

func TestReloadingDoesNotWriteToTheCallersBag(t *testing.T) {
	// Step takes a Player BY VALUE and a map is not copied by value, so the
	// obvious decrement would write through to whatever the caller is holding.
	// That is not a stylistic point here: the client replays every pending
	// command on top of each snapshot (ADR-058), so an in-place decrement would
	// spend a bottle once per REPLAY instead of once per command, and a player's
	// beer would drain at the round-trip rate.
	l, p := armed(2)
	p.Loaded = 0

	first := Step(l, p, fireAt(p, dt))
	second := Step(l, p, fireAt(p, dt))

	if got := p.Counters[AmmoCounter]; got != 2 {
		t.Fatalf("Step wrote through to the caller's bag: %d bottles left of 2", got)
	}
	if first.Counters[AmmoCounter] != 2-ReloadCost || second.Counters[AmmoCounter] != 2-ReloadCost {
		t.Fatalf("two runs of one command spent differently: %v and %v", first.Counters, second.Counters)
	}
	// And the copy is a copy, not a second reference to the same map.
	first.Counters[AmmoCounter] = 99
	if second.Counters[AmmoCounter] == 99 {
		t.Fatal("two results share one bag")
	}
}

func TestTheBagStopsMentioningWhatIsGone(t *testing.T) {
	// Prefer omitting to sending empty, applied to the bag rather than to a
	// field: `"c":{"beer":0}` would ride the snapshot at 20 Hz and the standings
	// at 1 Hz for as long as its owner stayed in the building, to say he is
	// carrying nothing. Both frames guard on the bag being non-empty, so
	// deleting the last bottle is what makes the guard fire.
	l, p := armed(ReloadCost)
	p.Loaded = 0

	spent := Step(l, p, fireAt(p, dt))
	if len(spent.Counters) != 0 {
		t.Fatalf("the bag still names something after the last of it was spent: %v", spent.Counters)
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
	l, p := armed(0)
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

func TestTheAmmunitionIsSomethingTheBuildingActuallyScatters(t *testing.T) {
	// The gun spends a counter, and a counter is only ever filled by a pickup's
	// Grants. If the two ever stop naming the same thing, the gun becomes
	// impossible to reload and nothing else in the package would say so — the
	// simulation would simply refuse every trigger pull on an empty gun, for
	// ever, in silence.
	for _, k := range Pickups {
		if k.Grants == AmmoCounter {
			if k.Max < ReloadCost {
				t.Fatalf("%q caps at %d and a reload costs %d, so a full bag cannot load the gun",
					k.Key, k.Max, ReloadCost)
			}
			return
		}
	}
	t.Fatalf("nothing in the catalogue grants %q, so the gun can never be reloaded", AmmoCounter)
}
