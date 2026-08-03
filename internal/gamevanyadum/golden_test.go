package gamevanyadum

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The conformance vectors that pin the TypeScript port of Step.
//
// WHY THIS FILE EXISTS. Client-side prediction means the browser runs the same
// movement and collision the server does, so `Step` now exists twice — in Go
// and in TypeScript. That is a second implementation of one rule, which this
// project's rules tell you not to build, and it is accepted here only because
// prediction is impossible without it (ADR-052). What makes it *safe* rather
// than merely permitted is this: the Go implementation emits a trace, the
// TypeScript one has to reproduce it, and the two diverging is a red test in
// the ordinary gate rather than a player walking through a wall another player
// can see.
//
// The file carries the LEVEL as well as the trace, because the client is never
// allowed to generate a level (ADR-050) — so the vectors hand it the same
// sector graph the server would have sent, and the port is tested on exactly
// the input it will see in production.
//
// Regenerate with:
//
//	./dev.sh test -run TestGoldenVectors ./internal/gamevanyadum/ -update
//
// Doing so deliberately breaks the TypeScript test until the port is brought
// back into line. That is the point: a change to movement is a change to two
// files, and this is what refuses to let it be a change to one.

var updateGolden = flag.Bool("update", false, "rewrite the golden vectors from the current Step")

// goldenPath is where the vectors live. Under testdata/ so `go build` ignores
// it, and read by the vitest suite through a relative path.
func goldenPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "step_vectors.json")
}

type goldenFile struct {
	// Note is for a human who opens the file wondering what it is.
	Note      string          `json:"note"`
	Constants goldenConstants `json:"constants"`
	Cases     []goldenCase    `json:"cases"`
}

// goldenConstants are the numbers the port must be using. They are in the file
// so that a mismatch reports "your walk speed is wrong" rather than a hundred
// failing positions.
type goldenConstants struct {
	WalkSpeed       float64 `json:"walk_speed"`
	Radius          float64 `json:"radius"`
	MaxStep         float64 `json:"max_step"`
	MaxPitch        float64 `json:"max_pitch"`
	MaxStepSeconds  float64 `json:"max_step_seconds"`
	CollisionPasses int     `json:"collision_passes"`
	// The gun. A port running a different cadence would fail every firing
	// assertion in the trace, and this is what makes the report say which number
	// is wrong instead.
	Barrels             int     `json:"barrels"`
	FireCooldownSeconds float64 `json:"fire_cooldown_seconds"`
	ReloadSeconds       float64 `json:"reload_seconds"`
	// The ampoule, on the same terms: a port healing by a different amount or
	// over a different time would fail every point of the injection case, and
	// this is what makes the report say which of the three numbers is wrong. Full
	// health is here because it is the cap the heal is clamped to, so a port that
	// had it wrong would overheal instead of stopping.
	MaxHealth      int     `json:"max_health"`
	SyringeHeal    int     `json:"syringe_heal"`
	SyringeSeconds float64 `json:"syringe_seconds"`
}

type goldenCase struct {
	Name  string `json:"name"`
	Level *Level `json:"level"`
	// Start is where the walk begins, so a case can be aimed at a doorway
	// rather than starting wherever the generator put the spawn.
	Start  Vec2 `json:"start"`
	Sector int  `json:"sector"`
	// Down starts the case with the player on the floor — health at zero — and
	// StartProtect starts him with that many seconds of spawn protection.
	//
	// A BOOL RATHER THAN A HEALTH VALUE, because zero is the interesting state
	// and an omitted number would have to mean the opposite of what it says. The
	// port reads this as "set health to nothing"; what happens next is the whole
	// of the case.
	Down         bool    `json:"down,omitempty"`
	StartProtect float64 `json:"start_protect,omitempty"`
	// StartHurt is how much health has already been taken off him when the case
	// begins, and StartInject is how many seconds of an ampoule are already
	// running.
	//
	// HURT RATHER THAN A HEALTH VALUE, because zero has to mean something and
	// "unhurt" is the only reading that does not collide with Down above — a
	// start_health of 0 would be indistinguishable from an absent field on a man
	// at full health, and would mean the opposite of it.
	StartHurt   int           `json:"start_hurt,omitempty"`
	StartInject float64       `json:"start_inject,omitempty"`
	Commands    []goldenCmd   `json:"commands"`
	Trace       []goldenPoint `json:"trace"`
}

type goldenCmd struct {
	Dt    float64 `json:"dt"`
	MX    float64 `json:"mx"`
	MY    float64 `json:"my"`
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
	// Fire is omitted when it was not pulled, exactly as it is on the wire —
	// which keeps six hundred steps of walking from carrying six hundred copies
	// of a field that is false in all of them.
	Fire bool `json:"f,omitempty"`
}

// goldenPoint is the player after one command: where he is, and what state his
// gun is in.
//
// THE GUN IS ON EVERY POINT AND NOT ONLY ON THE FIRING CASE. It costs six bytes
// a step on the walking transcripts — where it never changes — and what those
// six bytes buy is the assertion that walking does NOT change it. A port that
// ran a cooldown down on a step with no trigger in it would pass a firing-only
// trace and fail here, which is the right way round.
type goldenPoint struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Sector int     `json:"s"`
	Loaded int     `json:"b"`
	// Seconds, unquantised: this is a conformance artefact rather than a wire
	// frame, and the thing being pinned IS the float arithmetic. The wire's
	// millisecond rounding happens later and on one side only.
	Cooldown float64 `json:"cd,omitempty"`
	Reload   float64 `json:"rl,omitempty"`
	// Protected is seconds of spawn protection left, and it is omitted at zero —
	// which is every step of every case except the one that is about it. The
	// countdown is on the point rather than left to be inferred from a refused
	// trigger so that a port whose arithmetic drifts reports "your protection is
	// wrong" instead of "your gun is wrong".
	Protected float64 `json:"pr,omitempty"`
	// Inject is seconds of ampoule left, on exactly those terms.
	Inject float64 `json:"ij,omitempty"`
	// Health is what is left of him, and it is NOT omitted at zero: zero is a man
	// on the floor, which is the state one whole case is about, and an absent
	// field cannot say it.
	//
	// IT USED TO BE ABSENT ALTOGETHER, on the grounds that Step never changed it —
	// damage belongs to the world, which the browser does not run. The шприц ended
	// that: an injection is advanced INSIDE Step, and the health it delivers is
	// derived from the countdown rather than accumulated, so the browser produces
	// the number too and a port that rounded it differently would drift from the
	// server by a hit point every few frames. Nine bytes a step, which buys the
	// assertion that walking, firing and dying still do NOT move it.
	Health int `json:"hp"`
}

// pointOf records everything the port has to reproduce about one step.
//
// EVERY PREDICTED QUANTITY BELONGS HERE, and health is the one that had to be
// argued about twice. It was left out while Step could not change it; the
// ampoule made it a product of Step (sim.go, stepInject), so it is on the point
// like everything else — and carrying it on the cases that do NOT inject is what
// asserts that nothing else in the simulation touches it.
//
// THE BAG IS NOT A PREDICTED QUANTITY, which is what took it off this struct.
// It rode every point while a reload spent a bottle; ammunition is infinite now,
// and the bag is not on Player at all any more (world.go, Occupant.bag) — so
// there is nothing here for a port to reproduce and nothing for a column to echo.
func pointOf(p Player) goldenPoint {
	return goldenPoint{
		X: p.Pos.X, Y: p.Pos.Y, Sector: p.Sector,
		Loaded: p.Loaded, Cooldown: p.CooldownLeft, Reload: p.ReloadLeft,
		Protected: p.ProtectedLeft,
		Inject:    p.InjectLeft,
		Health:    p.Health,
	}
}

// buildGolden runs every case through the real Step and returns the file.
//
// The cases are chosen to exercise the parts of the port most likely to be got
// wrong, rather than to be many: a long pseudo-random wander through two
// generated buildings that will find corners nobody thought of, a doorway
// traversal with a step up, a hostile command that must be clamped identically
// on both ends, and the gun driven through every branch of its trigger.
func buildGolden() goldenFile {
	f := goldenFile{
		Note: "Generated by TestGoldenVectors in internal/gamevanyadum. " +
			"Pins the TypeScript port of Step (web/src/lib/vanyadumStep.ts) to the Go original: movement, collision and the gun. " +
			"Regenerate with: ./dev.sh test -run TestGoldenVectors ./internal/gamevanyadum/ -update",
		Constants: goldenConstants{
			WalkSpeed:           WalkSpeed,
			Radius:              PlayerRadius,
			MaxStep:             MaxStep,
			MaxPitch:            MaxPitch,
			MaxStepSeconds:      MaxStepSeconds,
			CollisionPasses:     collisionPasses,
			Barrels:             Barrels,
			FireCooldownSeconds: FireCooldownSeconds,
			ReloadSeconds:       ReloadSeconds,
			MaxHealth:           MaxHealth,
			SyringeHeal:         SyringeHeal,
			SyringeSeconds:      SyringeSeconds,
		},
	}

	const dt = 1.0 / SimHz

	// A generated level, walked in a slowly turning spiral. This is the case
	// that finds the corners: six hundred steps through a real заброшка will
	// press into jambs, slide along walls and cross doorways in both
	// directions.
	for _, seed := range []int64{7, 101} {
		l := Generate(seed)
		p := NewPlayer(l)
		c := goldenCase{
			Name:   "wander",
			Level:  l,
			Start:  p.Pos,
			Sector: p.Sector,
		}
		yaw := 0.0
		for i := 0; i < 300; i++ {
			yaw += 0.37
			cmd := goldenCmd{Dt: dt, MY: 1, Yaw: yaw}
			c.Commands = append(c.Commands, cmd)
			p = Step(l, p, Command{Dt: cmd.Dt, MX: cmd.MX, MY: cmd.MY, Yaw: cmd.Yaw})
			c.Trace = append(c.Trace, pointOf(p))
		}
		f.Cases = append(f.Cases, c)
	}

	// A hand-built pair of rooms with a step in the doorway, walked straight at
	// it. Deterministic, small, and the one case whose expected outcome a human
	// can check by reading it.
	{
		l := &Level{
			Sectors: []Sector{
				{ID: 0, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, FloorZ: 0, CeilZ: CeilingHeight},
				{ID: 1, MinX: 10, MinY: 0, MaxX: 20, MaxY: 10, FloorZ: 0.3, CeilZ: 0.3 + CeilingHeight},
			},
			Portals: []Portal{{A: 0, B: 1, Vertical: true, At: 10, Lo: 4, Hi: 6}},
		}
		l.Walls = buildWalls(l)
		p := NewPlayer(l)
		p.Pos, p.Sector = Vec2{X: 2, Y: 5}, 0
		c := goldenCase{Name: "doorway-and-step", Level: l, Start: p.Pos, Sector: p.Sector}
		for i := 0; i < 120; i++ {
			cmd := goldenCmd{Dt: dt, MY: 1, Yaw: math.Pi / 2} // due +X
			c.Commands = append(c.Commands, cmd)
			p = Step(l, p, Command{Dt: cmd.Dt, MY: cmd.MY, Yaw: cmd.Yaw})
			c.Trace = append(c.Trace, pointOf(p))
		}
		f.Cases = append(f.Cases, c)
	}

	// Hostile input. Both ends must clamp identically, or a client predicts one
	// thing from a command the server reads as another and the correction never
	// settles.
	//
	// THE LAST THREE PULL THE TRIGGER ON A CLAMPED STEP, which is the one
	// interaction the two cases above between them do not reach: the gun case
	// fires with ordinary sub-steps throughout, and everything here used to fire
	// not at all. A shot spends its barrel from the dt the CLAMP produced rather
	// than the dt that was sent, so a port that clamped movement but ran the gun
	// on the raw value would advance a cadence by a million seconds and be ready
	// again instantly — and nothing in this file would have said so. The
	// transcript is a shot, a pull refused with 0.15 s still on the clock
	// (0.35 − MaxStepSeconds), and a third that the expiry lets through.
	{
		l := &Level{Sectors: []Sector{{ID: 0, MinX: 0, MinY: 0, MaxX: 20, MaxY: 20, FloorZ: 0, CeilZ: CeilingHeight}}}
		l.Walls = buildWalls(l)
		p := NewPlayer(l)
		p.Pos, p.Sector = Vec2{X: 10, Y: 10}, 0
		c := goldenCase{Name: "hostile-clamping", Level: l, Start: p.Pos, Sector: p.Sector}
		hostile := []goldenCmd{
			{Dt: 1e6, MY: 1e6, Yaw: 0},
			{Dt: -5, MY: 1, Yaw: 1},
			{Dt: dt, MX: 50, MY: -50, Yaw: 100},
			{Dt: dt, MX: 0.5, MY: 0.5, Yaw: -3},
			{Dt: 0.5, MY: 1, Yaw: 2, Pitch: 99},
			{Dt: 1e6, MY: 1, Yaw: 2, Fire: true},
			{Dt: 1e9, MY: 1, Yaw: 2, Fire: true},
			{Dt: 1e9, MY: 1, Yaw: 2, Fire: true},
		}
		for _, cmd := range hostile {
			c.Commands = append(c.Commands, cmd)
			p = Step(l, p, Command{
				Dt: cmd.Dt, MX: cmd.MX, MY: cmd.MY, Yaw: cmd.Yaw, Pitch: cmd.Pitch, Fire: cmd.Fire,
			})
			c.Trace = append(c.Trace, pointOf(p))
		}
		f.Cases = append(f.Cases, c)
	}

	// The gun, driven through every branch it has: a shot, a trigger held down
	// inside the cadence and refused, the second barrel, an empty gun reloading
	// itself, the trigger refused for the whole of that, the barrels coming back —
	// and then the same thing a SECOND time, out of the same empty pockets.
	// Walking runs underneath the whole of it, because firing while moving is the
	// ordinary case and a port that folded the gun in after the standing-still
	// return would pass a stationary trace.
	//
	// THE PLAYER CARRIES NOTHING, AND THAT IS THE ASSERTION. He used to start with
	// one bottle so a reload could be exercised at all, and the transcript ended
	// with a dry click nobody could answer. A reload costs nothing now, so an empty
	// bag is the state every reload in this trace starts from — and a port that
	// kept any condition on the reload branch at all fires the first four shots
	// correctly and then stands there for ever.
	//
	// THE SUB-STEPS ARE MIXED ON PURPOSE. A cadence of 0.35 divides exactly by
	// the client's 0.025, so a transcript of nothing but those would land every
	// expiry on a clean boundary and pin nothing about the arithmetic. The 0.02
	// and 0.05 steps — both of which a real client emits, after a stall and at
	// the simulation's own rate — leave the countdown at values with no short
	// binary expansion, which is exactly where two languages would disagree if
	// either of them rounded.
	{
		l := &Level{Sectors: []Sector{{ID: 0, MinX: 0, MinY: 0, MaxX: 30, MaxY: 30, FloorZ: 0, CeilZ: CeilingHeight}}}
		l.Walls = buildWalls(l)
		p := NewPlayer(l)
		p.Pos, p.Sector = Vec2{X: 5, Y: 5}, 0
		c := goldenCase{Name: "gun", Level: l, Start: p.Pos, Sector: p.Sector}

		// step appends n commands of the given length, with the trigger held or
		// released and the player walking north-east throughout.
		step := func(n int, length float64, fire bool) {
			for i := 0; i < n; i++ {
				cmd := goldenCmd{Dt: length, MX: 0.5, MY: 1, Yaw: 0.8, Fire: fire}
				c.Commands = append(c.Commands, cmd)
				p = Step(l, p, Command{
					Dt: cmd.Dt, MX: cmd.MX, MY: cmd.MY, Yaw: cmd.Yaw, Pitch: cmd.Pitch, Fire: cmd.Fire,
				})
				c.Trace = append(c.Trace, pointOf(p))
			}
		}

		step(2, 0.025, false)  // walking up to it, both barrels loaded
		step(1, 0.025, true)   // the first barrel
		step(4, 0.025, true)   // the trigger held inside the cadence, refused
		step(9, 0.02, false)   // 0.35 − 4×0.025 − 9×0.02 leaves 0.07 on the clock
		step(2, 0.05, true)    // the first of these is still inside it; the second fires
		step(6, 0.025, true)   // empty, and inside the second barrel's cadence
		step(9, 0.02, true)    // the cadence expires mid-run and the reload starts, free
		step(30, 0.025, true)  // the trigger held through the reload, which refuses it
		step(34, 0.025, false) // and released, so the barrels come back and simply sit there
		step(4, 0.05, true)    // the barrels are back: a shot and the cadence after it
		step(20, 0.05, true)   // the second barrel, and then a SECOND reload out of nothing
		step(30, 0.025, true)  // held through that one too, and refused for all of it
		step(10, 0.05, false)  // released; the barrels come back a second time
		step(2, 0.05, true)    // and it fires again, which is what "infinite" means here
		f.Cases = append(f.Cases, c)
	}

	// A man on the floor, sent everything at once: full stick, the trigger held
	// down, and a look angle changing under him. NOTHING in the trace may move —
	// not the position, not the sector, not a barrel, not a timer.
	//
	// IT IS THE PORT'S HALF OF DYING. The world decides when he gets up (world.go,
	// rise) and the client has no business predicting that; what the client must
	// predict is that his own input does nothing until it happens. A port that
	// went on applying it would walk his corpse down the corridor and have it
	// snapped back twenty times a second.
	{
		l := &Level{Sectors: []Sector{{ID: 0, MinX: 0, MinY: 0, MaxX: 20, MaxY: 20, FloorZ: 0, CeilZ: CeilingHeight}}}
		l.Walls = buildWalls(l)
		p := NewPlayer(l)
		p.Pos, p.Sector = Vec2{X: 10, Y: 10}, 0
		p.Health = 0
		c := goldenCase{Name: "down", Level: l, Start: p.Pos, Sector: p.Sector, Down: true}
		for i := 0; i < 20; i++ {
			cmd := goldenCmd{Dt: dt, MX: 1, MY: 1, Yaw: float64(i) * 0.3, Pitch: 0.2, Fire: true}
			c.Commands = append(c.Commands, cmd)
			p = Step(l, p, Command{
				Dt: cmd.Dt, MX: cmd.MX, MY: cmd.MY, Yaw: cmd.Yaw, Pitch: cmd.Pitch, Fire: cmd.Fire,
			})
			c.Trace = append(c.Trace, pointOf(p))
		}
		f.Cases = append(f.Cases, c)
	}

	// Spawn protection: a man who has just got up, walking away from the spawn
	// with the trigger held down the whole time.
	//
	// TWO RULES IN ONE TRANSCRIPT, and they pull in opposite directions on
	// purpose. He MOVES — protection is not paralysis, and running is the whole
	// point of it — while every one of those trigger pulls is refused, so the
	// barrel count sits at Barrels until the countdown reaches zero and the very
	// next pull fires. The sub-step is 0.03 rather than the client's own 0.025, so
	// the window expires in the middle of a step and a port that only ever
	// compared against clean boundaries is caught.
	{
		l := &Level{Sectors: []Sector{{ID: 0, MinX: 0, MinY: 0, MaxX: 40, MaxY: 40, FloorZ: 0, CeilZ: CeilingHeight}}}
		l.Walls = buildWalls(l)
		p := NewPlayer(l)
		p.Pos, p.Sector = Vec2{X: 20, Y: 20}, 0
		p.ProtectedLeft = SpawnProtectSeconds
		c := goldenCase{
			Name: "spawn-protection", Level: l, Start: p.Pos, Sector: p.Sector,
			StartProtect: SpawnProtectSeconds,
		}
		for i := 0; i < 90; i++ {
			cmd := goldenCmd{Dt: 0.03, MY: 1, Yaw: 0.4, Fire: true}
			c.Commands = append(c.Commands, cmd)
			p = Step(l, p, Command{
				Dt: cmd.Dt, MX: cmd.MX, MY: cmd.MY, Yaw: cmd.Yaw, Pitch: cmd.Pitch, Fire: cmd.Fire,
			})
			c.Trace = append(c.Trace, pointOf(p))
		}
		f.Cases = append(f.Cases, c)
	}

	// The ampoule, twice: a wounded man who gets the whole of it, and a nearly
	// whole one who is rooted for the same time and given only what fits.
	//
	// THREE RULES IN ONE TRANSCRIPT, all of them running against a full stick and
	// a held trigger for every step. He does not MOVE — the position is the one he
	// started at until the countdown reaches zero, and then he walks; he does not
	// FIRE — the barrel count sits at Barrels through the whole injection and the
	// first pull after it is honoured; and his HEALTH climbs a hit point at a time
	// as the countdown falls, which is the number this file did not carry until
	// this iteration.
	//
	// THE SUB-STEP IS 0.03 AND NOT THE CLIENT'S 0.025, so the window expires in
	// the middle of a step rather than on a boundary — the same trick the spawn
	// protection case plays, and for the same reason: a port that only ever
	// compared against clean boundaries would pass and still be wrong.
	//
	// THE SECOND CASE IS THE OVERHEAL, and it is the one worth reading. He starts
	// 6 short of full against an ampoule holding SyringeHeal, so the heal is
	// exhausted against the cap a fifth of the way in — and the trace then shows
	// health FLAT at MaxHealth while the countdown goes on running and the stick
	// goes on being refused. That is the rule the catalogue chose: the ampoule is
	// spent on the clock rather than on the health, so injecting at nearly full is
	// a bad trade rather than a free top-up.
	for _, c := range []struct {
		name string
		hurt int
	}{
		{"syringe", SyringeHeal + 13},
		{"syringe-overheal", 6},
	} {
		l := &Level{Sectors: []Sector{{ID: 0, MinX: 0, MinY: 0, MaxX: 40, MaxY: 40, FloorZ: 0, CeilZ: CeilingHeight}}}
		l.Walls = buildWalls(l)
		p := NewPlayer(l)
		p.Pos, p.Sector = Vec2{X: 20, Y: 20}, 0
		p.Health = MaxHealth - c.hurt
		p.InjectLeft = SyringeSeconds
		g := goldenCase{
			Name: c.name, Level: l, Start: p.Pos, Sector: p.Sector,
			StartHurt: c.hurt, StartInject: SyringeSeconds,
		}
		// Long enough to outlast the injection and then some, so the tail of the
		// trace is a man walking and shooting again — which is the assertion that
		// the root and the refusal actually END.
		for i := 0; i < 110; i++ {
			cmd := goldenCmd{Dt: 0.03, MX: 0.5, MY: 1, Yaw: 0.4, Fire: true}
			g.Commands = append(g.Commands, cmd)
			p = Step(l, p, Command{
				Dt: cmd.Dt, MX: cmd.MX, MY: cmd.MY, Yaw: cmd.Yaw, Pitch: cmd.Pitch, Fire: cmd.Fire,
			})
			g.Trace = append(g.Trace, pointOf(p))
		}
		f.Cases = append(f.Cases, g)
	}

	return f
}

// TestGoldenVectors asserts that the committed vectors still describe what Step
// does — and rewrites them under -update.
//
// So a change to movement fails HERE first, loudly, telling you the file is
// stale; you regenerate; and then the TypeScript conformance test fails until
// the port is brought back into line. Two gates, in a deliberate order.
func TestGoldenVectors(t *testing.T) {
	got := buildGolden()
	path := goldenPath(t)

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		// Compact, not indented: this is a machine artefact read by a test in
		// another language, and indentation trebles a file that already lands
		// in every diff that touches movement.
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
		t.Fatalf("%v — generate it with: ./dev.sh test -run TestGoldenVectors ./internal/gamevanyadum/ -update", err)
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
		if len(g.Trace) != len(w.Trace) {
			t.Fatalf("case %q: trace lengths differ", g.Name)
		}
		for j := range g.Trace {
			// Exact, on this side: it is the same binary that produced the
			// file. The tolerance belongs on the TypeScript side, where a
			// different runtime's trigonometry may differ in the last bit.
			if g.Trace[j] != w.Trace[j] {
				t.Fatalf("case %q step %d: Step now produces %+v, golden says %+v — regenerate with -update",
					g.Name, j, g.Trace[j], w.Trace[j])
			}
		}
	}
}
