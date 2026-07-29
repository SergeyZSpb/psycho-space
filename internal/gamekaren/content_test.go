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
			if insideDesk(d, tc.pos, tc.r) {
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
	//
	// «Я КАРЕН» IS ALLOWED IN EVERY RUN, which is the one thing that changed
	// here: the introduction is interjected periodically whatever you are doing,
	// on its own hashed schedule, so it lands out of order rather than as a
	// metronome. Everything ELSE a state says still has to come from that state's
	// own run — a walking Карен must not borrow a standing one's line.
	intros := 0
	for tick := uint64(0); tick < KarenSlot*uint64(len(KarenLines))*4; tick += KarenSlot {
		if got := KarenLine(still, tick); !stillRun(got) {
			t.Fatalf("tick %d: standing still says %d (%q), which is not in the still run", tick, got, KarenLines[got])
		}
		if got := KarenLine(moving, tick); got != introLine && !movingRun(got) {
			t.Fatalf("tick %d: moving says %d (%q), which is neither the introduction nor in the moving run", tick, got, KarenLines[got])
		}
		if got := KarenLine(dashing, tick); got != introLine && !dashRun(got) {
			t.Fatalf("tick %d: dashing says %d (%q), which is neither the introduction nor in the dash run", tick, got, KarenLines[got])
		}
		if KarenLine(moving, tick) == introLine {
			intros++
		}
	}
	// And it really does come round, rather than being theoretically possible.
	if intros == 0 {
		t.Fatal("a walking Карен never once said who he was")
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
	// FAR AWAY HE ROTATES TOO, which he did not used to: he said «Я ЛЫСЫЙ» and
	// nothing else until his grin started, and that is most of a shift with the
	// balloon over the only other figure on the plane held still.
	first := BossLine(0, 0)
	if got := BossLine(0, BossSlot-1); got != first {
		t.Fatalf("he changed line inside one slot while far: %d then %d", first, got)
	}
	if got := BossLine(0, BossSlot); got == first {
		t.Fatalf("a whole slot passed at a distance and he is still on %d", got)
	}
	// And what he says while far is always one of the far lines — which include
	// the introduction at index 0, since that is where he introduces himself.
	for tick := uint64(0); tick < BossSlot*uint64(len(BossLines))*4; tick += BossSlot {
		if got := BossLine(0, tick); got < 0 || got >= len(bossFar) {
			t.Fatalf("at tick %d, far away, he says index %d, outside the %d far lines", tick, got, len(bossFar))
		}
	}
	// The threshold is what picks the RUN, so just below it he is still on small
	// talk — whatever the slot happens to have chosen — and not yet on the
	// afternoon-enders.
	if got := BossLine(BossQuiet-0.001, 0); got >= len(bossFar) {
		t.Fatalf("just below the grin threshold he says %d, already a closing line", got)
	}
	// He HOLDS a line for a whole slot rather than flickering, and then moves on.
	closing := BossLine(1, 0)
	if got := BossLine(1, BossSlot-1); got != closing {
		t.Fatalf("he changed line inside one slot: %d then %d", closing, got)
	}
	if got := BossLine(1, BossSlot); got == closing {
		t.Fatalf("he is still on %d after a whole slot", got)
	}
	// And he wraps rather than running off the end of the pool.
	// Never small talk while he is close — what he says across the room is not a
	// threat, and hearing it with his face in yours would break the joke. The
	// INTRODUCTION is the one exception, interjected in every state so «Я ЛЫСЫЙ»
	// comes round out of order rather than only from a distance.
	saidWhoHeIs := 0
	for tick := uint64(0); tick < BossSlot*uint64(len(BossLines))*4; tick += BossSlot {
		got := BossLine(1, tick)
		if got == introLine {
			saidWhoHeIs++
			continue
		}
		if got < len(bossFar) || got >= len(BossLines) {
			t.Fatalf("at tick %d he says index %d, which is small talk while standing over you", tick, got)
		}
	}
	if saidWhoHeIs == 0 {
		t.Fatal("he never once introduced himself while closing")
	}
}

func TestEveryBalloonOnThePlaneRotatesEveryTwoSeconds(t *testing.T) {
	// OWNER-DIRECTED, and it is one rule for everybody: a line is held for at
	// least two seconds and then it changes. Both halves matter — a balloon that
	// flickered would be unreadable on a phone, and one that never changed is
	// the office looking like a photograph.
	//
	// The bald man's IDLE state is the one this exists to pin. He used to say a
	// single sentence until his grin started, which is most of a shift, so the
	// balloon over the only other figure on the plane sat frozen while everything
	// else moved.
	const twoSeconds = 2 * SimHz
	if BossSlot != twoSeconds || KarenSlot != twoSeconds {
		t.Fatalf("the cadence is boss %d / karen %d ticks, want %d (2 s at %d Hz)",
			BossSlot, KarenSlot, twoSeconds, SimHz)
	}

	still := atSpawn() // perfectly idle: the state this game is played in
	moving := atSpawn()
	moving.MoveGrace = 0.1
	dashing := atSpawn()
	dashing.DashLeft = 0.1

	for _, tc := range []struct {
		name string
		line func(tick uint64) int
	}{
		{"the bald man, far away", func(tk uint64) int { return BossLine(0, tk) }},
		{"the bald man, closing", func(tk uint64) int { return BossLine(1, tk) }},
		{"a Карен standing perfectly still", func(tk uint64) int { return KarenLine(still, tk) }},
		{"a Карен who has just moved", func(tk uint64) int { return KarenLine(moving, tk) }},
		{"a Карен mid-dash", func(tk uint64) int { return KarenLine(dashing, tk) }},
	} {
		// Held for the whole slot...
		first := tc.line(0)
		for tick := uint64(1); tick < twoSeconds; tick++ {
			if got := tc.line(tick); got != first {
				t.Fatalf("%s changed line at tick %d, inside the two-second slot", tc.name, tick)
			}
		}
		// ...and then moved on.
		if got := tc.line(twoSeconds); got == first {
			t.Fatalf("%s is still saying %d after a full two seconds", tc.name, got)
		}
		// And it keeps moving rather than settling: over a dozen slots it says
		// more than one thing, which is what "rotates" means.
		seen := map[int]bool{}
		for slot := uint64(0); slot < 12; slot++ {
			seen[tc.line(slot*twoSeconds)] = true
		}
		if len(seen) < 2 {
			t.Fatalf("%s said the same thing for twelve slots", tc.name)
		}
	}
}

func TestNobodyRecitesThePoolInOrder(t *testing.T) {
	// OWNER-DIRECTED: the introduction comes round periodically and OUT OF
	// ORDER. Two claims, and the second is why this is a hash and not a modulus.
	//
	// It is a property test rather than a pinned sequence on purpose — pinning
	// the exact order would make the hash a golden file and any retune of the
	// pools a diff nobody can read.
	const slots = 400

	for _, tc := range []struct {
		name string
		line func(slot uint64) int
	}{
		{"a Карен standing still", func(sl uint64) int { return KarenLine(atSpawn(), sl*KarenSlot) }},
		{"the bald man closing", func(sl uint64) int { return BossLine(1, sl*BossSlot) }},
	} {
		seq := make([]int, slots)
		for i := range seq {
			seq[i] = tc.line(uint64(i))
		}

		// (1) THE INTRODUCTION COMES ROUND — often enough to be noticed, rarely
		// enough to still be an interjection. IntroEvery is 5, so about a fifth;
		// the bars are loose because a hash is not a quota.
		intros := 0
		for _, v := range seq {
			if v == introLine {
				intros++
			}
		}
		if intros < slots/12 || intros > slots/2 {
			t.Fatalf("%s said who he was %d times in %d slots", tc.name, intros, slots)
		}

		// (2) IT IS NOT A METRONOME. A fixed period would put every introduction
		// the same distance from the last; a hash does not.
		gaps := map[int]bool{}
		last := -1
		for i, v := range seq {
			if v != introLine {
				continue
			}
			if last >= 0 {
				gaps[i-last] = true
			}
			last = i
		}
		if len(gaps) < 3 {
			t.Fatalf("%s introduced himself on a fixed rhythm: gaps %v", tc.name, gaps)
		}

		// (3) AND THE REST IS NOT RECITED IN ORDER EITHER. A `slot %% n` walk
		// steps by exactly +1 every time; a hash almost never does.
		ascending := 0
		for i := 1; i < len(seq); i++ {
			if seq[i] == seq[i-1]+1 {
				ascending++
			}
		}
		if ascending > len(seq)/3 {
			t.Fatalf("%s walked the pool in order %d times out of %d", tc.name, ascending, len(seq))
		}

		// (4) AND ALMOST NEVER THE SAME THING TWICE RUNNING, which reads as
		// frozen and is what the cadence exists to avoid.
		//
		// ALMOST, stated rather than claimed away. The repair looks one slot back
		// at the UNREPAIRED pick, so a run of three colliding hashes can still
		// slip a repeat through — genuinely eliminating it needs the sequence to
		// know its own history, which would cost the property that makes all of
		// this work: that every viewer computes the same words for the same
		// instant from the clock alone, with nothing stored and nothing sent.
		// A doubled slot is a line held four seconds instead of two, which is
		// invisible; the bar below is what would catch the repair being broken
		// altogether, as it was once — for the still run `base` IS the
		// introduction, so repairing a repeated introduction returned it again.
		repeats := 0
		for i := 1; i < len(seq); i++ {
			if seq[i] == seq[i-1] {
				repeats++
			}
		}
		if repeats > len(seq)/20 {
			t.Fatalf("%s repeated itself %d times in %d slots", tc.name, repeats, len(seq))
		}
	}
}
