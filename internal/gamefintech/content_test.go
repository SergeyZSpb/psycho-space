package gamefintech

import (
	"encoding/json"
	"math"
	"slices"
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
		`"boss_lines"`, `"personas"`, `"claude_lines"`, `"npcs"`, `"max_occupants"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("the served catalogue has no %s: %s", key, raw)
		}
	}
}

func TestTheGameKeyDidNotMoveWithTheRename(t *testing.T) {
	// THE LITERAL, not a comparison against itself.
	//
	// The game was «СИМУЛЯТОР КАРЕНА» and is now «СИМУЛЯТОР ФИНТЕХА», and the
	// package, the table, the routes, the room and the frame types all moved with
	// it. This value did not, because a game_key is DATA: it is what art blobs in
	// the shared (game_key, art_key) store are keyed on, so changing it orphans
	// every one of them. Nothing is uploaded under it today, which is exactly why
	// the temptation to tidy it exists.
	//
	// Every other assertion about it in the codebase compares GameKey to GameKey
	// and is therefore tautological — it passes whatever the constant says. That
	// left one Playwright line in the full-stack suite as the only real guard, and
	// the rename's own sweep rewrote THAT to 'fintech' too. This test is the guard
	// that a mechanical sweep cannot satisfy by rewriting both sides.
	if GameKey != "karen" {
		t.Fatalf("GameKey = %q, want %q — a game_key value is data, not a name; see migrations/014", GameKey, "karen")
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
	if PlayerLines[0] != "Я КАРЕН" {
		t.Fatalf("an absent `p` means PlayerLines[0], which is %q", PlayerLines[0])
	}
	// And every index the two selectors can return has to exist, or a balloon is
	// a blank rectangle over somebody's head.
	if len(PlayerLines) < 3 {
		t.Fatalf("PlayerLine returns up to 2 and there are %d lines", len(PlayerLines))
	}
	if len(BossLines) < 2 {
		t.Fatalf("BossLine returns 1.. and there are %d lines", len(BossLines))
	}
	for name, pool := range map[string][]string{"BossLines": BossLines, "PlayerLines": PlayerLines, "ClaudeLines": ClaudeLines} {
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

func TestWhatFintechIsSaying(t *testing.T) {
	// The STATE picks the run and the TICK picks the line inside it, so what is
	// asserted here is which RUN each state lands in — never a fixed index,
	// because adding a line to any run would then be a broken test rather than
	// more of the same joke.
	stillRun := func(i int) bool { return i >= 0 && i < len(stillLines) }
	movingRun := func(i int) bool {
		return i >= len(stillLines) && i < len(stillLines)+len(movingLines)
	}
	dashRun := func(i int) bool { return i >= len(stillLines)+len(movingLines) && i < len(PlayerLines) }

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
	for tick := uint64(0); tick < PlayerSlot*uint64(len(PlayerLines))*4; tick += PlayerSlot {
		if got := PlayerLine(still, 0, tick); !stillRun(got) {
			t.Fatalf("tick %d: standing still says %d (%q), which is not in the still run", tick, got, PlayerLines[got])
		}
		if got := PlayerLine(moving, 0, tick); got != introLine && !movingRun(got) {
			t.Fatalf("tick %d: moving says %d (%q), which is neither the introduction nor in the moving run", tick, got, PlayerLines[got])
		}
		if got := PlayerLine(dashing, 0, tick); got != introLine && !dashRun(got) {
			t.Fatalf("tick %d: dashing says %d (%q), which is neither the introduction nor in the dash run", tick, got, PlayerLines[got])
		}
		if PlayerLine(moving, 0, tick) == introLine {
			intros++
		}
	}
	// And it really does come round, rather than being theoretically possible.
	if intros == 0 {
		t.Fatal("a walking Карен never once said who he was")
	}
	// He HOLDS a line for a whole slot rather than flickering, and then moves on.
	if PlayerLine(still, 0, PlayerSlot-1) != PlayerLine(still, 0, 0) {
		t.Fatal("the line changed inside one slot")
	}
	if len(stillLines) > 1 && PlayerLine(still, 0, PlayerSlot) == PlayerLine(still, 0, 0) {
		t.Fatal("the line did not change after a whole slot")
	}
}

// A balloon holds TWO lines and clips what will not fit, so a sentence somebody
// could not resist is a sentence cut off over the office. Both pools are
// catalogue data and adding to them is meant to be cheap, which is exactly why
// the bound belongs in a test rather than in somebody's memory.
//
// This number is a MEASUREMENT of the CSS and moves only with it. It was 32 while
// `.fintech-say` was a single `white-space: nowrap` row at 0.62rem, which is under a
// short Russian sentence — the pools stayed blunt because of it. The balloon is
// now two rows at 0.54rem inside `max-width: 160px`: bold uppercase Cyrillic with
// `letter-spacing: 0.02em` costs about 0.6 × the font size a rune, so ~5.2px a
// rune holds ~30 on a row and ~61 over two. The bound sits at 48 rather than 61
// because the wrap is by WORD — a sentence whose split lands badly needs the
// slack, and the `line-clamp` ellipsis is the backstop for a mistake rather than
// the budget being spent.
func TestNobodySaysMoreThanFitsOnAPhone(t *testing.T) {
	const most = 48 // runes; two rows of ~30 inside 160 px, with room for a bad split
	for name, pool := range map[string][]string{"BossLines": BossLines, "PlayerLines": PlayerLines, "ClaudeLines": ClaudeLines} {
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
	if BossSlot != twoSeconds || PlayerSlot != twoSeconds {
		t.Fatalf("the cadence is boss %d / fintech %d ticks, want %d (2 s at %d Hz)",
			BossSlot, PlayerSlot, twoSeconds, SimHz)
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
		{"a Карен standing perfectly still", func(tk uint64) int { return PlayerLine(still, 0, tk) }},
		{"a Карен who has just moved", func(tk uint64) int { return PlayerLine(moving, 0, tk) }},
		{"a Карен mid-dash", func(tk uint64) int { return PlayerLine(dashing, 0, tk) }},
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
		{"a Карен standing still", func(sl uint64) int { return PlayerLine(atSpawn(), 0, sl*PlayerSlot) }},
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

func TestEveryBottleSpotIsSomewhereYouCanStand(t *testing.T) {
	// A bottle is a place you WALK TO, so every spot has to be reachable: on the
	// floor and not inside a desk. One in the furniture is a bottle nobody can
	// ever have, and since it MOVES, one bad spot is a mechanic that dies for ten
	// seconds at a time rather than obviously.
	if len(BottleSpots) < 2 {
		t.Fatal("a bottle that cannot move somewhere else is not a bottle that moves")
	}
	for i, at := range BottleSpots {
		if at.X < PlayerRadius || at.X > OfficeW-PlayerRadius ||
			at.Y < PlayerRadius || at.Y > OfficeH-PlayerRadius {
			t.Fatalf("bottle spot %d is off the floor at %+v", i, at)
		}
		for d, desk := range Desks {
			if insideDesk(desk, at, PlayerRadius) {
				t.Fatalf("bottle spot %d is inside desk %d", i, d)
			}
		}
		for j := i + 1; j < len(BottleSpots); j++ {
			if math.Hypot(at.X-BottleSpots[j].X, at.Y-BottleSpots[j].Y) < 2 {
				t.Fatalf("spots %d and %d are close enough to be the same spot", i, j)
			}
		}
	}
}

func TestSomethingHappeningToHimInterruptsWhatHeWasSaying(t *testing.T) {
	// OWNER-DIRECTED. The two-second rotation is the office idling; being bought
	// a drink or being pointed at somebody else are EVENTS, and an event that
	// waited politely for the next slot would not read as one. So the run changes
	// the instant the state does — mid-slot — and the line changes with it.
	const mid = BossSlot / 3 // deliberately NOT on a slot boundary

	idle := BossSays(BossIdle, 1, mid, false)
	drunk := BossSays(BossDrunk, 1, mid, false)
	redirected := BossSays(BossRedirected, 1, mid, false)

	if drunk == idle {
		t.Fatal("getting drunk did not interrupt what he was saying")
	}
	if redirected == idle {
		t.Fatal("being pointed at somebody else did not interrupt what he was saying")
	}
	if drunk == redirected {
		t.Fatal("he says the same thing drunk as when he has been redirected")
	}

	// Each event speaks from its OWN run, so the words fit what happened.
	base := len(bossFar) + len(bossClosing)
	if drunk < base || drunk >= base+len(bossDrunkLines) {
		t.Fatalf("drunk he says index %d, outside his drink lines", drunk)
	}
	if redirected < base+len(bossDrunkLines) || redirected >= len(BossLines) {
		t.Fatalf("redirected he says index %d, outside those lines", redirected)
	}

	// And inside a run it is still the two-second, out-of-order rotation — an
	// event interrupts the cadence, it does not replace it with one sentence.
	held := BossSays(BossDrunk, 1, 0, false)
	for tick := uint64(1); tick < BossSlot; tick++ {
		if got := BossSays(BossDrunk, 1, tick, false); got != held {
			t.Fatalf("drunk, he changed line at tick %d inside the slot", tick)
		}
	}
	if BossSays(BossDrunk, 1, BossSlot, false) == held {
		t.Fatal("drunk, he holds one sentence for the whole binge")
	}

	// Drunk outranks redirected: a man who is both is funnier on the drink lines.
	if BossSays(BossDrunk, 1, mid, false) == BossSays(BossRedirected, 1, mid, false) {
		t.Fatal("the two states are indistinguishable")
	}
}

func TestTheServedGeometryUsesTheKeysTheClientReads(t *testing.T) {
	// THE BUG THIS EXISTS FOR, and it shipped. Vec2 had no JSON tags, so the
	// catalogue served `{"X":2.2,"Y":6}` while the browser read `at.x` — which is
	// `undefined`, then NaN, and `toPlane` clamps a NaN to zero. The bottle was
	// therefore drawn in the top-left corner of the office rather than where the
	// server was checking for it, so it could never be picked up, and because it
	// is only ever replaced after somebody drinks it, it never moved either. One
	// missing struct tag, three symptoms.
	//
	// The layout suite could not catch it: its stub is hand-written in the shape
	// the CLIENT expects, so the stub and the server disagreed and both were
	// self-consistent. Checking a served payload against the names the client
	// actually reads is the only thing that closes that gap.
	raw, err := json.Marshal(BuildConfig())
	if err != nil {
		t.Fatal(err)
	}
	var served struct {
		Bottle struct {
			Spots []struct {
				X *float64 `json:"x"`
				Y *float64 `json:"y"`
			} `json:"spots"`
		} `json:"bottle"`
		Office struct {
			Desks []struct {
				X *float64 `json:"x"`
				W *float64 `json:"w"`
			} `json:"desks"`
		} `json:"office"`
	}
	if err := json.Unmarshal(raw, &served); err != nil {
		t.Fatal(err)
	}
	if len(served.Bottle.Spots) != len(BottleSpots) {
		t.Fatalf("the client reads %d bottle spots, the catalogue has %d",
			len(served.Bottle.Spots), len(BottleSpots))
	}
	for i, spot := range served.Bottle.Spots {
		if spot.X == nil || spot.Y == nil {
			t.Fatalf("bottle spot %d has no x/y the client can read: %s", i, raw)
		}
		if *spot.X != BottleSpots[i].X || *spot.Y != BottleSpots[i].Y {
			t.Fatalf("bottle spot %d reads (%v,%v), the catalogue says %+v",
				i, *spot.X, *spot.Y, BottleSpots[i])
		}
	}
	for i, d := range served.Office.Desks {
		if d.X == nil || d.W == nil {
			t.Fatalf("desk %d has no x/w the client can read: %s", i, raw)
		}
	}
}

func TestEveryPersonaIntroducesItselfAsItself(t *testing.T) {
	// THE STRING BEHIND THE INDEX, not index-against-index arithmetic. A test that
	// recomputes a base with the same expression the code uses is wrong identically
	// on both sides and passes, which is the failure mode append-only ordering
	// exists to avoid — so every claim here resolves to the actual line.
	if len(Personas) != len(personaIntros)+1 {
		t.Fatalf("%d personas but %d intros after Карен", len(Personas), len(personaIntros))
	}
	if PlayerLines[IntroLineFor(0)] != "Я КАРЕН" {
		t.Fatalf("persona 0 introduces itself as %q, want «Я КАРЕН»", PlayerLines[IntroLineFor(0)])
	}
	for i := 1; i < len(Personas); i++ {
		got := PlayerLines[IntroLineFor(i)]
		want := personaIntros[i-1]
		if got != want {
			t.Fatalf("persona %d (%s) introduces itself as %q, want %q", i, Personas[i], got, want)
		}
		if !strings.Contains(strings.ToUpper(got), strings.ToUpper(Personas[i])) {
			t.Fatalf("persona %d is %s but says %q", i, Personas[i], got)
		}
	}
}

func TestKarenIsStillFirstInTheFlatPool(t *testing.T) {
	// THREE SEPARATE CONTRACTS AGREE ONLY WHILE HE IS. An omitted persona on the
	// wire means zero; `introLine` is zero; and `PlayerLines[0]` is his line. The
	// full-stack suite reads the first element of the served array, and the client
	// draws index 0 for anybody who has said nothing yet. Reordering `Personas` or
	// prepending to any run is a wire change, and this is the test that says so.
	if Personas[0] != "Карен" {
		t.Fatalf("Personas[0] = %q, want Карен", Personas[0])
	}
	if introLine != 0 || IntroLineFor(0) != 0 {
		t.Fatalf("introLine = %d, IntroLineFor(0) = %d, want 0 and 0", introLine, IntroLineFor(0))
	}
	if PlayerLines[0] != stillLines[0] {
		t.Fatalf("the flat pool does not start with the still run: %q vs %q", PlayerLines[0], stillLines[0])
	}
}

func TestAnUnknownPersonaFallsBackToKaren(t *testing.T) {
	// The safe direction: a frame that could not be read must not silently point at
	// a different colleague's name.
	for _, p := range []int{-1, len(Personas), 99} {
		if got := IntroLineFor(p); got != introLine {
			t.Fatalf("persona %d answers intro %d, want %d", p, got, introLine)
		}
	}
}

func TestAppendingPersonasDidNotMoveTheRedirectLine(t *testing.T) {
	// `RedirectLine` is derived from the lengths of the three runs before it, and
	// the new run is appended AFTER the redirect run — so it cannot move. Pinned
	// against the string, because that is the thing the client joins on.
	if PlayerLines[RedirectLine] != redirectLines[0] {
		t.Fatalf("RedirectLine points at %q, want %q", PlayerLines[RedirectLine], redirectLines[0])
	}
	if personaIntroBase <= RedirectLine {
		t.Fatalf("the persona intros start at %d, which is not after the redirect run at %d", personaIntroBase, RedirectLine)
	}
}

func TestEveryHookahSpotIsSomewhereYouCanActuallyStand(t *testing.T) {
	for i, at := range HookahSpots {
		if at.X < PlayerRadius || at.X > OfficeW-PlayerRadius ||
			at.Y < PlayerRadius || at.Y > OfficeH-PlayerRadius {
			t.Fatalf("hookah spot %d (%+v) is not on the floor", i, at)
		}
		for j, d := range Desks {
			// Reachable means a player disc can sit ON it, not merely near it: a
			// spot inside a desk would be a prop you walk at forever while the
			// collision resolver pushes you out.
			if insideDesk(d, at, PlayerRadius) {
				t.Fatalf("hookah spot %d (%+v) is inside desk %d", i, at, j)
			}
		}
	}
}

func TestNoHookahSharesATileWithABottle(t *testing.T) {
	// THE CROSS-LIST RULE, and it is the one this game had no need of before.
	// `drawBottleSpot` and `drawHookahSpot` each avoid only their OWN previous
	// index, so nothing else stops the two props landing on the same tile — and one
	// walk collecting both would hand a player a drunk лысый AND ten seconds of
	// being uncatchable for a single lost streak.
	const apart = 2.0
	for i, h := range HookahSpots {
		for j, b := range BottleSpots {
			if d := math.Hypot(h.X-b.X, h.Y-b.Y); d < apart {
				t.Fatalf("hookah spot %d and bottle spot %d are %.2f m apart, want at least %.1f", i, j, d, apart)
			}
		}
	}
}

func TestTheHookahMovesFarEnoughToBeADifferentWalk(t *testing.T) {
	// A prop that reappeared next to where it was would make the walk free, which
	// is the whole price of the mechanic.
	if len(HookahSpots) < 2 {
		t.Fatal("one spot is not a wander")
	}
	const apart = 3.0
	for i := range HookahSpots {
		for j := i + 1; j < len(HookahSpots); j++ {
			if d := math.Hypot(HookahSpots[i].X-HookahSpots[j].X, HookahSpots[i].Y-HookahSpots[j].Y); d < apart {
				t.Fatalf("hookah spots %d and %d are only %.2f m apart", i, j, d)
			}
		}
	}
}

func TestHeOnlyNamesTheManWhoCanHearHisName(t *testing.T) {
	// The templated line is the one that says «ГДЕ {}», and only the client that
	// vanished can fill it in — a persona is never sent for anybody else. So the
	// office sends it to that occupant and nobody else, and every other screen gets
	// a line from the same run that names nobody.
	var namedForMine, namedForOthers bool
	for tick := uint64(0); tick < BossSlot*uint64(len(bossLostLines))*8; tick += BossSlot {
		if strings.Contains(BossLines[BossSays(BossLost, 0, tick, true)], NamePlaceholder) {
			namedForMine = true
		}
		if strings.Contains(BossLines[BossSays(BossLost, 0, tick, false)], NamePlaceholder) {
			namedForOthers = true
		}
	}
	if !namedForMine {
		t.Fatal("the man who vanished is never named to himself, so the templated line is unreachable")
	}
	if namedForOthers {
		t.Fatal("a colleague's screen was sent a line with a placeholder it cannot fill")
	}
}

func TestLosingHisTargetIsItsOwnRunAndDidNotMoveTheOthers(t *testing.T) {
	// Appended last, so every base that existed before it is untouched. Pinned
	// against the STRINGS, because index-against-index arithmetic is wrong
	// identically on both sides when a base goes stale.
	lost := BossSays(BossLost, 0, 0, true)
	if !strings.Contains(strings.Join(bossLostLines, "|"), BossLines[lost]) {
		t.Fatalf("lost he says %q, which is not one of his lost lines", BossLines[lost])
	}
	if BossLines[bossLostBase] != bossLostLines[0] {
		t.Fatalf("the lost run starts at %q, want %q", BossLines[bossLostBase], bossLostLines[0])
	}
	// And the runs before it still start where they did.
	if BossLines[0] != bossFar[0] {
		t.Fatalf("the flat pool no longer starts with the far run: %q", BossLines[0])
	}
	drunk := BossSays(BossDrunk, 1, BossSlot, false)
	if BossLines[drunk] != bossDrunkLines[0] && !strings.Contains(strings.Join(bossDrunkLines, "|"), BossLines[drunk]) {
		t.Fatalf("drunk he says %q, which is not one of his drink lines", BossLines[drunk])
	}
}

func TestNoHookahIsBaitBesideTheBaldMan(t *testing.T) {
	// A spot next to his spawn is not a reprieve, it is bait: a shift opens with the
	// player at the far end of the room, so walking to it means walking into the one
	// thing that ends the shift. The first version of this list put one at (8, 19.0),
	// a metre and a half from him, and the integration test that walks a real player
	// to a real кальян died on it every time.
	const clear = 6.0
	for i, at := range HookahSpots {
		if d := math.Hypot(at.X-BossSpawnX, at.Y-BossSpawnY); d < clear {
			t.Fatalf("hookah spot %d (%+v) is %.2f m from his spawn, want at least %.1f", i, at, d, clear)
		}
	}
}

func TestASlowedWalkStillOutrunsThem(t *testing.T) {
	// THE OWNER'S CONSTRAINT, AS ARITHMETIC: «-20% movement speed (it should match
	// bosses movement speed so that its not instalooseable)». Both halves are
	// checked here, because both are what makes Claude Code a cost rather than an
	// ending.
	if ChaserSpeed != BossSpeed {
		t.Fatalf("Claude walks at %v and the лысый at %v — they are meant to match", ChaserSpeed, BossSpeed)
	}
	slowed := WalkSpeed * SlowFactor
	if slowed <= BossSpeed {
		t.Fatalf("a slowed walk is %v against their %v, so being caught is being caught", slowed, BossSpeed)
	}
	// AND IT MUST NOT STACK, which is the reason the office assigns rather than
	// accumulates. Two applications would leave a walk barely above their speed and
	// three would put it below — at which point the game is unwinnable and the test
	// above would still pass, because it only ever measures one.
	if twice := WalkSpeed * SlowFactor * SlowFactor; twice > BossSpeed {
		t.Logf("two applications would leave %.3f m/s against %.1f — close enough that "+
			"the non-stacking rule is what keeps this game winnable", twice, BossSpeed)
	}
	if thrice := WalkSpeed * SlowFactor * SlowFactor * SlowFactor; thrice >= BossSpeed {
		t.Fatalf("three applications leave %v, which is still an escape — this test has "+
			"stopped measuring anything", thrice)
	}
}

func TestTheDashIsNotSlowed(t *testing.T) {
	// The dash is the answer to being caught, so a slowed dash would take away the
	// way out of the punishment. Measured through Step rather than asserted about
	// the constant, because the branch order in `Step` is what decides it.
	p := atSpawn()
	p.SlowLeft = SlowSeconds
	dashed := Step(nil, p, Sanitise(Command{Dt: SimStep.Seconds(), MX: 1, Dash: true}))
	fast := Step(nil, atSpawn(), Sanitise(Command{Dt: SimStep.Seconds(), MX: 1, Dash: true}))
	if math.Abs((dashed.Pos.X-p.Pos.X)-(fast.Pos.X-atSpawn().Pos.X)) > 1e-9 {
		t.Fatalf("a slowed dash covered %v where an unslowed one covered %v",
			dashed.Pos.X-p.Pos.X, fast.Pos.X-atSpawn().Pos.X)
	}
}

func TestASlowedWalkIsActuallySlower(t *testing.T) {
	// And the walk IS multiplied, or the whole mechanic is decorative.
	p := atSpawn()
	p.SlowLeft = SlowSeconds
	slow := Step(nil, p, Sanitise(Command{Dt: SimStep.Seconds(), MX: 1}))
	full := Step(nil, atSpawn(), Sanitise(Command{Dt: SimStep.Seconds(), MX: 1}))
	moved, wanted := slow.Pos.X-p.Pos.X, full.Pos.X-atSpawn().Pos.X
	if math.Abs(moved-wanted*SlowFactor) > 1e-9 {
		t.Fatalf("a slowed step moved %v, want %v", moved, wanted*SlowFactor)
	}
}

func TestClaudeSaysEveryLineTheOwnerWrote(t *testing.T) {
	// The copy is the owner's and it is verbatim. One of his lines was 55 runes
	// against the 48 the balloon holds, so it ships as two entries rather than being
	// trimmed — both halves are here, and this is what says so.
	for _, want := range []string{
		"УВИЖУ КОДЕКС — ВЫЕБУ",
		"ТЫ ЧЁ СУКА КОДЕКС ПОСТАВИЛ",
		"В КОНСОЛИ НЕ МОЖЕШЬ? ЕБЛАН?",
		"GUI НУЖЕН? А МОЖЕТ ХУЙ ЗА ЩЕКУ?",
		"УЁБОК ПОДКЛЮЧИ СКИЛЛ",
		"КОДЕКС-ПАРАША ВЫЕБУ МАМАШУ",
		"THIS SEEMS LIKE A SMOKING CUM",
		"YOU ARE ABSOLUTELY RIGHT, FAGGOT",
		"ПИДОР, ПИШИ НОРМАЛЬНЫЙ ПРОМПТ",
	} {
		if !slices.Contains(ClaudeLines, want) {
			t.Fatalf("Claude no longer says %q", want)
		}
	}
	if ClaudeLines[0] != "Я КЛОД" {
		t.Fatalf("index 0 is %q, and an omitted index has to be the introduction", ClaudeLines[0])
	}
}

func TestBothNonPlayersHaveTheOwnersCopy(t *testing.T) {
	// Their lines are the owner's, and each has his OWN pool — folding them together
	// would let one man say the other's introduction.
	if len(NPCCast) != 2 {
		t.Fatalf("the cast is %d, want Серега and Тёма", len(NPCCast))
	}
	for _, want := range []string{"ХУЙНЯ, ПЕРЕДЕЛЫВАЙ", "ТЫ ТУПО КОДЕКСШЛАК ЗАЛИЛ?", "ДА Я ХУЕМ БОЛЬШЕ ДЕЛАЮ"} {
		found := false
		for _, k := range NPCCast {
			if slices.Contains(k.Lines, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("nobody says %q any more", want)
		}
	}
	if !slices.Contains(NPCCast[0].Lines, "А РЕВЬЮВИТЬ ОЧКО СЕМА АЛЬМАНА БУДЕТ?") {
		t.Fatal("Серега no longer asks who is reviewing")
	}
	for i, k := range NPCCast {
		if k.Key == "" || k.Name == "" {
			t.Fatalf("NPC %d has no key or no name: %+v", i, k)
		}
		if len(k.Lines) == 0 {
			t.Fatalf("%s has nothing to say", k.Name)
		}
		// Index 0 is the introduction, as in every pool here — an omitted index on the
		// frame means zero.
		if !strings.Contains(strings.ToUpper(k.Lines[0]), strings.ToUpper(k.Name)) {
			t.Fatalf("%s's index 0 is %q, which is not an introduction", k.Name, k.Lines[0])
		}
	}
}

func TestNobodySaysMoreThanFitsOnAPhoneIncludingThem(t *testing.T) {
	// The same 48-rune bound the other pools live under — a measurement of the
	// two-row balloon, not a taste. Their pools are separate arrays, so they escape
	// the map the other invariants iterate unless something checks them here.
	const most = 48
	for _, k := range NPCCast {
		for i, line := range k.Lines {
			if n := len([]rune(line)); n > most {
				t.Fatalf("%s's line %d is %d runes: %q", k.Name, i, n, line)
			}
			if line == "" {
				t.Fatalf("%s's line %d is empty", k.Name, i)
			}
		}
	}
}

func TestTheNonPlayersSpawnSomewhereTheyCanStand(t *testing.T) {
	for _, k := range NPCCast {
		at := k.Spawn
		if at.X < PlayerRadius || at.X > OfficeW-PlayerRadius ||
			at.Y < PlayerRadius || at.Y > OfficeH-PlayerRadius {
			t.Fatalf("%s spawns off the floor at %+v", k.Name, at)
		}
		for i, d := range Desks {
			if insideDesk(d, at, PlayerRadius) {
				t.Fatalf("%s spawns inside desk %d", k.Name, i)
			}
		}
	}
}

func TestTheyAmbleSlowerThanAnybodyLookingForSomebody(t *testing.T) {
	// So they never read as chasing anyone, which is the whole of what they are for.
	if NPCSpeed >= BossSpeed || NPCSpeed >= ChaserSpeed {
		t.Fatalf("they amble at %v against %v and %v", NPCSpeed, BossSpeed, ChaserSpeed)
	}
	if NPCSpeed >= WalkSpeed*SlowFactor {
		t.Fatalf("they amble at %v, which a slowed player at %v cannot outwalk", NPCSpeed, WalkSpeed*SlowFactor)
	}
}
