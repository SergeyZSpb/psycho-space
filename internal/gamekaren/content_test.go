package gamekaren

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// The catalogue is content, so these tests are about the office being a room a
// game can happen in — not about arithmetic.

// clearance is the widest disc the resolver has to push out of furniture.
var clearance = math.Max(PlayerRadius, BossRadius)

func TestNoTwoDesksOverlap(t *testing.T) {
	for i := range Desks {
		for j := i + 1; j < len(Desks); j++ {
			a, b := Desks[i], Desks[j]
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Fatalf("desks %d and %d overlap: %+v %+v", i, j, a, b)
			}
		}
	}
}

func TestEveryDeskIsClearOfTheWalls(t *testing.T) {
	// THIS IS WHAT LETS Step's RESOLVER BE A SINGLE PASS. The player is clamped
	// to the floor and then pushed out of each desk in turn; if a desk touched a
	// wall, that push could put somebody outside the room and nothing would
	// clamp them back. A generated level would need an iterative resolver
	// instead — this one buys the simplicity with a layout rule.
	for i, d := range Desks {
		if d.X < clearance || d.Y < clearance ||
			d.X+d.W > OfficeW-clearance || d.Y+d.H > OfficeH-clearance {
			t.Fatalf("desk %d (%+v) is within %v of a wall", i, d, clearance)
		}
	}
}

func TestNoTwoDesksAreCloserThanADiameter(t *testing.T) {
	// The other half of the same bargain: one pass per desk is only safe while
	// being pushed out of one desk cannot put you inside another.
	gap := 2 * clearance
	for i := range Desks {
		for j := i + 1; j < len(Desks); j++ {
			a, b := Desks[i], Desks[j]
			dx := math.Max(0, math.Max(b.X-(a.X+a.W), a.X-(b.X+b.W)))
			dy := math.Max(0, math.Max(b.Y-(a.Y+a.H), a.Y-(b.Y+b.H)))
			if math.Hypot(dx, dy) <= gap {
				t.Fatalf("desks %d and %d are %v apart, which is not more than %v",
					i, j, math.Hypot(dx, dy), gap)
			}
		}
	}
}

func TestBothSpawnsAreOnTheFloorAndOutOfTheFurniture(t *testing.T) {
	for _, tc := range []struct {
		name string
		pos  Vec2
		r    float64
	}{
		{"the player", Vec2{X: PlayerSpawnX, Y: PlayerSpawnY}, PlayerRadius},
		{"the bald man", Vec2{X: BossSpawnX, Y: BossSpawnY}, BossRadius},
	} {
		if tc.pos.X < tc.r || tc.pos.X > OfficeW-tc.r || tc.pos.Y < tc.r || tc.pos.Y > OfficeH-tc.r {
			t.Fatalf("%s spawns outside the floor at %+v", tc.name, tc.pos)
		}
		for i, d := range Desks {
			if insideRect(d, tc.pos, tc.r) {
				t.Fatalf("%s spawns inside desk %d", tc.name, i)
			}
		}
	}
}

func TestHeSpawnsFurtherAwayThanHeCanBeSeenSmiling(t *testing.T) {
	// Otherwise the shift is over before you have read anything, and the grin —
	// the only readout of how much trouble you are in — starts saturated.
	d := math.Hypot(BossSpawnX-PlayerSpawnX, BossSpawnY-PlayerSpawnY)
	if d <= GrinRange {
		t.Fatalf("he spawns %v away and starts smiling at %v", d, GrinRange)
	}
	if Caught(NewBoss(), Vec2{X: PlayerSpawnX, Y: PlayerSpawnY}) {
		t.Fatal("he has already caught you at the spawn")
	}
}

func TestEveryCauseTheCodeCanProduceHasAnEnding(t *testing.T) {
	// The over screen renders the catalogue's title and sub for whatever cause
	// the simulation wrote, so a cause with no ending is a blank screen.
	//
	// The lookup lives here rather than as an exported helper: the server never
	// needs one — it writes the cause and the client renders it — so a
	// findEnding in the package would be a function with no caller.
	find := func(key string) (Ending, bool) {
		for _, e := range Endings {
			if e.Key == key {
				return e, true
			}
		}
		return Ending{}, false
	}
	for _, cause := range []string{CausePromoted, CauseLeft} {
		e, ok := find(cause)
		if !ok {
			t.Fatalf("nothing in the catalogue describes the ending %q", cause)
		}
		if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Sub) == "" {
			t.Fatalf("the ending %q renders as a blank screen: %+v", cause, e)
		}
	}
	if _, ok := find("exposed"); ok {
		t.Fatal("iteration 4's ending has arrived early — is the simulation writing it?")
	}
}

func TestTheConfigCarriesEveryFieldTheClientIsWrittenAgainst(t *testing.T) {
	// The splash screen's rules cheatsheet is GENERATED from this payload rather
	// than typed out, so every number below is load-bearing: dropping one turns
	// a rule into a blank line, and renaming one is a client change.
	raw, err := json.Marshal(BuildConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"game_key"`, `"title"`,
		`"office"`, `"w"`, `"h"`, `"desks"`, `"player_radius"`, `"boss_radius"`,
		`"money"`, `"base_per_second"`, `"ramp_seconds"`, `"max_multiplier"`, `"grace_ms"`,
		`"move"`, `"walk_speed"`, `"dash_speed"`, `"dash_ms"`, `"dash_cooldown_ms"`,
		`"input_hz"`, `"max_commands"`,
		`"boss"`, `"speed"`, `"catch_radius"`, `"grin_range"`,
		`"sim"`, `"hz"`, `"snapshot_hz"`,
		`"endings"`, `"key"`, `"sub"`,
		`"boss_lines"`, `"max_occupants"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("the served catalogue has no %s: %s", key, raw)
		}
	}
}

func TestTheConfigAgreesWithTheSimulation(t *testing.T) {
	// One catalogue, one set of numbers. A config that published a walk speed
	// the simulation did not use would make the cheatsheet a lie and prediction
	// diverge on the first step.
	c := BuildConfig()
	if c.GameKey != GameKey || c.Title != Title {
		t.Fatalf("the catalogue does not know what game it is: %+v", c)
	}
	if c.Office.W != OfficeW || c.Office.H != OfficeH || len(c.Office.Desks) != len(Desks) {
		t.Fatalf("the published office is not the simulated one: %+v", c.Office)
	}
	if c.Move.WalkSpeed != WalkSpeed || c.Move.DashSpeed != DashSpeed {
		t.Fatalf("the published speeds are not the simulated ones: %+v", c.Move)
	}
	if c.Move.DashMs != int(DashSeconds*1000) || c.Move.DashCooldownMs != int(DashCooldown*1000) {
		t.Fatalf("the published dash is not the simulated one: %+v", c.Move)
	}
	if c.Money.GraceMs != int(GraceSeconds*1000) {
		t.Fatalf("the published grace window is %v ms, the simulation uses %v s", c.Money.GraceMs, GraceSeconds)
	}
	if c.Sim.Hz != SimHz || c.Sim.SnapshotHz != SimHz/SnapshotEvery {
		t.Fatalf("the published rates are not the simulated ones: %+v", c.Sim)
	}
	if c.MaxOccupants != MaxOccupants {
		t.Fatalf("the published occupancy is %d, the office allows %d", c.MaxOccupants, MaxOccupants)
	}
}

func TestTheCatalogueIsCopiedRatherThanShared(t *testing.T) {
	// BuildConfig is called per request and the result is serialised by a
	// handler. Handing out the package's own slices would let one caller's
	// decoration — the art keys the HTTP layer adds — leak into everybody
	// else's office.
	c := BuildConfig()
	c.Office.Desks[0].X = -999
	c.Endings[0].Title = "нет"
	c.BossLines[0] = "нет"
	if Desks[0].X == -999 || Endings[0].Title == "нет" || BossLines[0] == "нет" {
		t.Fatal("mutating a served config changed the catalogue")
	}
}

func TestEveryBossLineIsSomethingHeWouldSay(t *testing.T) {
	// A cheap shape check, and the one that catches an empty string sneaking
	// into a balloon.
	if len(BossLines) == 0 {
		t.Fatal("he has nothing to say")
	}
	for i, l := range BossLines {
		if strings.TrimSpace(l) == "" {
			t.Fatalf("boss line %d is blank", i)
		}
	}
}

// TestEveryPassageFitsTheWidestThingThatMustUseIt pins the level design.
//
// The gaps between the furniture ARE the level: they are what makes a desk cover
// you use rather than something you catch on. The first layout had 2.0 m aisles,
// which is 1.2 m of daylight once the лысый's width is removed — and it was
// tuned before the player's speed was, so at 6.4 m/s you cross it in under a
// fifth of a second and clipping a desk mid-dodge is likelier than choosing to.
//
// Measured against the BOSS rather than the player: he is the wider of the two,
// and a gap he cannot follow you through is a gap that changes the game.
func TestEveryPassageFitsTheWidestThingThatMustUseIt(t *testing.T) {
	// Daylight left after the widest occupant, in metres. Room to steer, not just
	// room to exist.
	const wantDaylight = 1.5
	widest := 2 * math.Max(PlayerRadius, BossRadius)

	check := func(what string, gap float64) {
		t.Helper()
		if got := gap - widest; got < wantDaylight {
			t.Errorf("%s is %.2f m, leaving %.2f m of daylight for a %.2f m body, want %.2f m",
				what, gap, got, widest, wantDaylight)
		}
	}

	left, right := math.Inf(1), math.Inf(1)
	for _, d := range Desks {
		left = math.Min(left, d.X)
		right = math.Min(right, OfficeW-(d.X+d.W))
	}
	check("the left aisle", left)
	check("the right aisle", right)

	// The central lane: from the right edge of the left column to the left edge
	// of the right column.
	var leftEdge, rightEdge float64
	for _, d := range Desks {
		if d.X < OfficeW/2 {
			leftEdge = math.Max(leftEdge, d.X+d.W)
		}
	}
	rightEdge = OfficeW
	for _, d := range Desks {
		if d.X >= OfficeW/2 {
			rightEdge = math.Min(rightEdge, d.X)
		}
	}
	check("the central lane", rightEdge-leftEdge)

	// And between the rows, for every pair in the same column.
	for i, a := range Desks {
		for j, b := range Desks {
			if i >= j || a.X != b.X {
				continue
			}
			lo, hi := a, b
			if lo.Y > hi.Y {
				lo, hi = hi, lo
			}
			if gap := hi.Y - (lo.Y + lo.H); gap > 0 && gap < OfficeH/2 {
				check("the gap between two desks in a column", gap)
			}
		}
	}
}

// The balloons. Index 0 of each pool is what an omitted `p` means, so the first
// line of each is a contract rather than a preference.
func TestTheDefaultLinesAreFirst(t *testing.T) {
	if BossLines[0] != "Я ЛЫСЫЙ" {
		t.Fatalf("an absent `p` means BossLines[0], which is %q", BossLines[0])
	}
	if KarenLines[0] != "Я КАРЕН" {
		t.Fatalf("an absent `p` means KarenLines[0], which is %q", KarenLines[0])
	}
	// And every index the two selectors can return has to exist, or a balloon is
	// a blank rectangle over somebody's head.
	if len(KarenLines) < 3 {
		t.Fatalf("KarenLine returns up to 2 and there are %d lines", len(KarenLines))
	}
	if len(BossLines) < 2 {
		t.Fatalf("BossLine returns 1.. and there are %d lines", len(BossLines))
	}
	for name, pool := range map[string][]string{"BossLines": BossLines, "KarenLines": KarenLines} {
		seen := map[string]bool{}
		for i, line := range pool {
			if line == "" {
				t.Fatalf("%s[%d] is empty", name, i)
			}
			if seen[line] {
				t.Fatalf("%s says %q twice, so an index no longer identifies a line", name, line)
			}
			seen[line] = true
		}
	}
}

func TestWhatKarenIsSaying(t *testing.T) {
	// The STATE picks the run and the TICK picks the line inside it, so what is
	// asserted here is which RUN each state lands in — never a fixed index,
	// because adding a line to any run would then be a broken test rather than
	// more of the same joke.
	stillRun := func(i int) bool { return i >= 0 && i < len(karenStill) }
	movingRun := func(i int) bool {
		return i >= len(karenStill) && i < len(karenStill)+len(karenMoving)
	}
	dashRun := func(i int) bool { return i >= len(karenStill)+len(karenMoving) && i < len(KarenLines) }

	still := Player{}
	moving := Player{MoveGrace: 0.05}
	// A dash outranks moving: it IS moving, and it is the one movement that
	// costs nothing, so saying the same thing as an ordinary walk would be a lie
	// about the rule the whole game rests on.
	dashing := Player{MoveGrace: 0.05, DashLeft: 0.1}

	// Across enough ticks to walk every run right round and wrap.
	for tick := uint64(0); tick < KarenSlot*uint64(len(KarenLines))*2; tick += KarenSlot {
		if got := KarenLine(still, tick); !stillRun(got) {
			t.Fatalf("tick %d: standing still says %d (%q), which is not in the still run", tick, got, KarenLines[got])
		}
		if got := KarenLine(moving, tick); !movingRun(got) {
			t.Fatalf("tick %d: moving says %d (%q), which is not in the moving run", tick, got, KarenLines[got])
		}
		if got := KarenLine(dashing, tick); !dashRun(got) {
			t.Fatalf("tick %d: dashing says %d (%q), which is not in the dash run", tick, got, KarenLines[got])
		}
	}
	// Standing still at tick zero is the default, which is what an absent index
	// on the wire means.
	if got := KarenLine(still, 0); got != 0 {
		t.Fatalf("a fresh still player says %d, want the default", got)
	}
	// He HOLDS a line for a whole slot rather than flickering, and then moves on.
	if KarenLine(still, KarenSlot-1) != KarenLine(still, 0) {
		t.Fatal("the line changed inside one slot")
	}
	if len(karenStill) > 1 && KarenLine(still, KarenSlot) == KarenLine(still, 0) {
		t.Fatal("the line did not change after a whole slot")
	}
}

// A balloon holds TWO lines and clips what will not fit, so a sentence somebody
// could not resist is a sentence cut off over the office. Both pools are
// catalogue data and adding to them is meant to be cheap, which is exactly why
// the bound belongs in a test rather than in somebody's memory.
//
// This number is a MEASUREMENT of the CSS and moves only with it. It was 32 while
// `.karen-say` was a single `white-space: nowrap` row at 0.62rem, which is under a
// short Russian sentence — the pools stayed blunt because of it. The balloon is
// now two rows at 0.54rem inside `max-width: 160px`: bold uppercase Cyrillic with
// `letter-spacing: 0.02em` costs about 0.6 × the font size a rune, so ~5.2px a
// rune holds ~30 on a row and ~61 over two. The bound sits at 48 rather than 61
// because the wrap is by WORD — a sentence whose split lands badly needs the
// slack, and the `line-clamp` ellipsis is the backstop for a mistake rather than
// the budget being spent.
func TestNobodySaysMoreThanFitsOnAPhone(t *testing.T) {
	const most = 48 // runes; two rows of ~30 inside 160 px, with room for a bad split
	for name, pool := range map[string][]string{"BossLines": BossLines, "KarenLines": KarenLines} {
		for i, line := range pool {
			if n := len([]rune(line)); n > most {
				t.Errorf("%s[%d] is %d characters and the balloon holds %d: %q", name, i, n, most, line)
			}
		}
	}
}

func TestWhatTheBaldManIsSaying(t *testing.T) {
	// Far away he introduces himself, and does so at every tick — the tick must
	// not leak into the quiet answer, or he would mutter to himself across the
	// room.
	for _, tick := range []uint64{0, 1, 49, 50, 51, 12345} {
		if got := BossLine(0, tick); got != 0 {
			t.Fatalf("at grin 0, tick %d, he says %d", tick, got)
		}
	}
	if got := BossLine(BossQuiet-0.001, 0); got != 0 {
		t.Fatalf("just below the grin threshold he says %d", got)
	}
	// Close enough to smile, and he starts working through the afternoon.
	first := BossLine(BossQuiet, 0)
	if first != 1 {
		t.Fatalf("the first thing he says on arrival is %d", first)
	}
	// He HOLDS a line for a whole slot rather than flickering, and then moves on.
	if got := BossLine(1, BossSlot-1); got != first {
		t.Fatalf("he changed line inside one slot: %d then %d", first, got)
	}
	if got := BossLine(1, BossSlot); got == first {
		t.Fatalf("he is still on %d after a whole slot", got)
	}
	// And he wraps rather than running off the end of the pool.
	for _, tick := range []uint64{0, BossSlot * 7, BossSlot * 8, BossSlot * 999} {
		got := BossLine(1, tick)
		if got < 1 || got >= len(BossLines) {
			t.Fatalf("at tick %d he says index %d, out of %d lines", tick, got, len(BossLines))
		}
	}
	// Never the default while he is close: index 0 is who he is, not a threat.
	for tick := uint64(0); tick < BossSlot*uint64(len(BossLines))*2; tick += BossSlot {
		if BossLine(1, tick) == 0 {
			t.Fatalf("he introduced himself at tick %d while standing over you", tick)
		}
	}
}
