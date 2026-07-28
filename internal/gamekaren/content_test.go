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
