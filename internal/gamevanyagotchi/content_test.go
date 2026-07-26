package gamevanyagotchi

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

// The catalogue is data, and data has no compiler.
//
// Every property in this file is one that a typo in content.go breaks in
// production rather than at build time: a duplicated key, an action pointing at
// a stat that was renamed, a default skin nothing resolves to, a fatal stat that
// cannot actually kill. The whole point of the catalogue is that content changes
// without a client deploy and without a migration — which means these checks are
// the only gate a content change passes through at all.
//
// Since health became a CONSEQUENCE rather than a chore, the catalogue also
// carries the coupling — which stat drives which, from what threshold, at what
// extra rate — and the closed-form decay in decay.go is only exact while that
// coupling keeps a particular shape. Those structural conditions are content
// too, so they are checked here, next to the numbers that can break them, rather
// than being left as a paragraph of prose nobody re-reads before retuning a rate.
//
// Note that neither Stat nor Action is comparable with == any more: one holds a
// slice of penalties and the other a slice of effects. Comparisons against a
// zero value therefore go through reflect.DeepEqual.

// TestTheCatalogueIsPopulated is first because everything below it would pass
// vacuously against an empty catalogue: a range over nothing asserts nothing,
// and the suite would go green describing a game with no content in it.
func TestTheCatalogueIsPopulated(t *testing.T) {
	c := Content()
	for _, group := range []struct {
		what string
		n    int
	}{
		{"stats", len(c.Stats)},
		{"actions", len(c.Actions)},
		{"skins", len(c.Skins)},
		{"locations", len(c.Locations)},
	} {
		if group.n == 0 {
			t.Errorf("the catalogue has no %s", group.what)
		}
	}
	if c.GameKey != GameKey {
		t.Errorf("catalogue game key = %q; want %q — the shared asset store is keyed on it", c.GameKey, GameKey)
	}
}

// TestEveryKindOfKeyIsUnique guards the lookups. StatByKey and ActionByKey
// return the FIRST match, so a duplicated key does not fail loudly: it makes the
// second entry unreachable content, and — worse for an action — can silently
// resolve a verb onto the wrong stat.
func TestEveryKindOfKeyIsUnique(t *testing.T) {
	c := Content()
	statKeys := make([]string, 0, len(c.Stats))
	for _, s := range c.Stats {
		statKeys = append(statKeys, s.Key)
	}
	actionKeys := make([]string, 0, len(c.Actions))
	for _, a := range c.Actions {
		actionKeys = append(actionKeys, a.Key)
	}
	skinKeys := make([]string, 0, len(c.Skins))
	for _, s := range c.Skins {
		skinKeys = append(skinKeys, s.Key)
	}
	locationKeys := make([]string, 0, len(c.Locations))
	for _, l := range c.Locations {
		locationKeys = append(locationKeys, l.Key)
	}

	for _, group := range []struct {
		what string
		keys []string
	}{
		{"stat", statKeys},
		{"action", actionKeys},
		{"skin", skinKeys},
		{"location", locationKeys},
	} {
		seen := make(map[string]bool, len(group.keys))
		for _, k := range group.keys {
			if k == "" {
				t.Errorf("a %s has an empty key; nothing can address it", group.what)
			}
			if seen[k] {
				t.Errorf("%s key %q appears twice; the second entry is unreachable and a lookup may resolve onto the wrong one", group.what, k)
			}
			seen[k] = true
		}
	}
}

// TestEveryStatHasAUsableRange checks the three numbers a bar is drawn from. A
// stat whose start sits outside its own bounds is created already clamped —
// which for a fatal stat is the difference between a new pet and a dead one —
// and a warning threshold outside them is a warning that is either always or
// never showing.
func TestEveryStatHasAUsableRange(t *testing.T) {
	for _, s := range Content().Stats {
		t.Run(s.Key, func(t *testing.T) {
			if s.Min >= s.Max {
				t.Fatalf("min %v is not below max %v; the bar has no width and the decay has nowhere to go", s.Min, s.Max)
			}
			if s.Start < s.Min || s.Start > s.Max {
				t.Errorf("start %v is outside [%v, %v]; a new pet is created already clamped", s.Start, s.Min, s.Max)
			}
			if s.WarnAt < s.Min || s.WarnAt > s.Max {
				t.Errorf("warn_at %v is outside [%v, %v]; the warning is either always or never on", s.WarnAt, s.Min, s.Max)
			}
		})
	}
}

// TestEveryActionMovesStatsThatExist is the catalogue agreeing with itself.
// An action naming a stat that has been renamed or removed is a button that
// appears to work: Act answers ErrUnknownStat, which is a 500 rather than
// anything the player did wrong.
//
// An action moves a LIST of stats now — drinking tops him up, cheers him up and
// fills his bladder — so every entry in that list has to resolve, not just the
// first one. An effect whose key has gone would otherwise be an action that is
// half a verb, failing only for the players who press it.
func TestEveryActionMovesStatsThatExist(t *testing.T) {
	for _, a := range Content().Actions {
		t.Run(a.Key, func(t *testing.T) {
			if len(a.Effects) == 0 {
				t.Fatal("the action moves nothing at all; pressing it would be a no-op the player is invited to try")
			}
			for _, e := range a.Effects {
				s, ok := StatByKey(e.StatKey)
				if !ok {
					t.Fatalf("action moves stat %q, which is not in the catalogue", e.StatKey)
				}
				if e.Delta == 0 {
					t.Errorf("effect on %q has a delta of zero; it is content that does nothing", e.StatKey)
				}
				if got := s.Clamp(s.Start + e.Delta); got < s.Min || got > s.Max {
					t.Fatalf("applying delta %v to a fresh %q lands at %v, outside [%v, %v]", e.Delta, s.Key, got, s.Min, s.Max)
				}
			}
		})
	}
}

// TestEveryPenaltyNamesAStatAndAThresholdInsideItsRange is the first of the
// coupling checks, and it is the one a rename breaks silently.
//
// A penalty whose driver is not in the catalogue is evaluated as "no penalty at
// all" by design — decay.go refuses to guess at a stat it cannot read — so
// retiring a stat that something depends on does not fail, it quietly makes the
// game easier and nobody finds out. A threshold outside the driver's own bounds
// is the same defect wearing a number: either it is satisfied from the moment a
// pet is born, or it can never be satisfied at all.
func TestEveryPenaltyNamesAStatAndAThresholdInsideItsRange(t *testing.T) {
	for _, s := range Content().Stats {
		for _, p := range s.Penalties {
			t.Run(s.Key+" penalised by "+p.WhenKey, func(t *testing.T) {
				driver, ok := StatByKey(p.WhenKey)
				if !ok {
					t.Fatalf("driver %q is not in the catalogue; the penalty is silently never applied", p.WhenKey)
				}
				if p.Threshold < driver.Min || p.Threshold > driver.Max {
					t.Errorf("threshold %v is outside %q's range [%v, %v]; it fires either always or never",
						p.Threshold, driver.Key, driver.Min, driver.Max)
				}
				if p.RatePerHour <= 0 {
					t.Errorf("the penalty adds %v/hour to the drain; a penalty that does not hurt is content that does nothing",
						p.RatePerHour)
				}
			})
		}
	}
}

// TestTheDependencyGraphIsOneLayerDeep is the property the whole exactness
// argument rests on, so it is worth failing loudly for.
//
// decay.go integrates a penalised stat by reading each driver's own trajectory
// across the window — with At, which applies no coupling. That is correct only
// while a driver is never itself penalised: the moment a stat both has penalties
// and drives another one, the drivers' trajectories are no longer knowable
// without solving a system, the closed form silently becomes an approximation,
// and the sign of its error decides whether being absent beats playing. Nothing
// in the type system prevents that catalogue entry. This test does.
func TestTheDependencyGraphIsOneLayerDeep(t *testing.T) {
	stats := Content().Stats

	penalised := make(map[string]bool, len(stats))
	for _, s := range stats {
		if len(s.Penalties) > 0 {
			penalised[s.Key] = true
		}
	}

	for _, s := range stats {
		for _, p := range s.Penalties {
			if penalised[p.WhenKey] {
				t.Errorf("%q is driven by %q, which is itself penalised: the dependency graph is two layers deep, "+
					"so the decay in decay.go is no longer exact and needs its own derivation before this ships",
					s.Key, p.WhenKey)
			}
		}
	}
	// Guarding against the vacuous pass: a catalogue with no coupling in it at
	// all satisfies the loop above while describing a different game from the
	// one the design and the decay engine were built for.
	if len(penalised) == 0 {
		t.Fatal("no stat has any penalties; health is supposed to be a consequence of the needs, not a chore of its own")
	}
}

// TestEveryPenaltyIsDrivenByAStatThatCanActuallyReachIt covers the other way a
// penalty becomes decoration.
//
// A penalty is a SUFFIX of the window, described by a single onset instant, and
// that is only true because a driver moves monotonically towards its threshold
// or away from it forever. A driver heading the wrong way never crosses, so
// decay.go correctly applies nothing — which means a catalogue entry pairing
// "hurt him while the beer is empty" with a beer that refills on its own is not
// an error anywhere, just a rule that never fires. Almost certainly a content
// bug, and invisible without this.
func TestEveryPenaltyIsDrivenByAStatThatCanActuallyReachIt(t *testing.T) {
	for _, s := range Content().Stats {
		for _, p := range s.Penalties {
			t.Run(s.Key+" penalised by "+p.WhenKey, func(t *testing.T) {
				driver, ok := StatByKey(p.WhenKey)
				if !ok {
					t.Skipf("driver %q is not in the catalogue; covered by its own test", p.WhenKey)
				}
				// DecayPerHour is signed: positive falls towards Min, negative
				// rises towards Max. A driver already past the threshold at its
				// starting value qualifies whatever it does next, because the
				// penalty is on from the pet's first hour.
				switch {
				case p.Above && driver.DecayPerHour < 0, p.Above && driver.Start >= p.Threshold:
				case !p.Above && driver.DecayPerHour > 0, !p.Above && driver.Start <= p.Threshold:
				default:
					t.Errorf("%q is meant to hurt while %q is %s %v, but %q starts at %v and moves at %v/hour: "+
						"it can never get there, so the penalty is content that never fires",
						s.Key, driver.Key, aboveOrBelow(p.Above), p.Threshold, driver.Key, driver.Start, driver.DecayPerHour)
				}
			})
		}
	}
}

// aboveOrBelow renders a penalty's direction for a failure message.
func aboveOrBelow(above bool) string {
	if above {
		return "at or above"
	}
	return "at or below"
}

// TestAWarningColourMeansItIsCostingHimHealth pins a deliberate coincidence of
// two numbers that are free to drift apart.
//
// A stat's WarnAt is what turns its bar amber, and a penalty's Threshold is
// where that stat starts costing him health. They are set to the same value on
// purpose, so the warning colour carries information rather than being
// decoration: the moment the beer bar goes amber is the moment the empty beer
// begins killing him. Retune one and forget the other and the game still works,
// which is exactly why nothing but a test notices.
func TestAWarningColourMeansItIsCostingHimHealth(t *testing.T) {
	stats := Content().Stats
	for _, s := range stats {
		for _, p := range s.Penalties {
			driver, ok := StatByKey(p.WhenKey)
			if !ok {
				continue // covered by TestEveryPenaltyNamesAStatAndAThresholdInsideItsRange
			}
			if driver.WarnAt != p.Threshold {
				t.Errorf("%q warns at %v but starts costing %q health at %v; the amber bar would mean nothing",
					driver.Key, driver.WarnAt, s.Key, p.Threshold)
			}
		}
	}
}

// TestTheDefaultsAPetIsCreatedWithResolve covers the two keys every pet is
// stamped with on creation. A default that resolves to nothing is a pet the
// client cannot draw and a location the plane cannot place — for every account,
// from its very first read.
func TestTheDefaultsAPetIsCreatedWithResolve(t *testing.T) {
	c := Content()

	skin := false
	for _, s := range c.Skins {
		if s.Key == c.DefaultSkin {
			skin = true
		}
	}
	if !skin {
		t.Errorf("default skin %q is in no skin in the catalogue", c.DefaultSkin)
	}

	location := false
	for _, l := range c.Locations {
		if l.Key == c.DefaultLocation {
			location = true
		}
	}
	if !location {
		t.Errorf("default location %q is in no location in the catalogue", c.DefaultLocation)
	}
}

// TestExactlyOneStatIsFatalAndItActuallyDrains pins the design's statement that
// there is one timer at the centre of the game.
//
// The service supports several — it records the EARLIEST derived death among
// them — so a second fatal stat is a decision rather than a bug, and this test
// failing is how that decision gets made deliberately instead of by editing a
// bool. The rate check is the sharper half: a fatal stat that does not drain can
// never reach its floor, so the death loop would be content that exists and
// never fires.
func TestExactlyOneStatIsFatalAndItActuallyDrains(t *testing.T) {
	var fatal []Stat
	for _, s := range Content().Stats {
		if s.Fatal {
			fatal = append(fatal, s)
		}
	}
	if len(fatal) != 1 {
		t.Fatalf("%d fatal stats; the design says exactly one — adding another is a deliberate change, not a flag flip", len(fatal))
	}
	if fatal[0].DecayPerHour <= 0 {
		t.Fatalf("the fatal stat %q moves at %v/hour, so it can never reach its floor and nothing can ever kill him",
			fatal[0].Key, fatal[0].DecayPerHour)
	}
}

// TestDeathIsRecoverableAndTheRefusalIsReachable pins both halves of the
// revival rule, and they fail for opposite reasons.
//
// Death in this game is deliberately recoverable — an irreversible loss in a
// fifteen-person friend group is how a player leaves permanently — so a
// catalogue in which no action revives is a dead end that would reach a player
// before anyone noticed. The other half is subtler: if EVERY action revived,
// Act's death guard could never fire, ErrPetDead would be unreachable, and the
// 409 the client is written to handle would be dead code that nothing proves
// works. A dead Ваня not being able to go to the toilet is what makes that path
// real.
func TestDeathIsRecoverableAndTheRefusalIsReachable(t *testing.T) {
	var revives, refuses []string
	for _, a := range Content().Actions {
		if a.RevivesFatal {
			revives = append(revives, a.Key)
			continue
		}
		refuses = append(refuses, a.Key)
	}
	if len(revives) == 0 {
		t.Error("no action sets revives_fatal: a dead pet would be unrecoverable and the account would be finished with the game")
	}
	if len(refuses) == 0 {
		t.Error("every action sets revives_fatal: Act's death guard can never fire, so ErrPetDead and the 409 it becomes are unreachable")
	}
}

// TestContentHandsOutACopy is the defect the first game learned by having it.
// The config handler decorates what Content returns — filling in art URLs for
// skins that have an uploaded blob — so a shared backing array would let one
// request write through into the package's own catalogue and leave every later
// request looking at a mutated one, for the life of the process.
func TestContentHandsOutACopy(t *testing.T) {
	// Captured as plain values rather than kept as a second Config. A Config
	// held across the mutation would be a VIEW of whatever array Content handed
	// out — so if that array were the catalogue's own, the "before" copy would
	// report the mutation too and the comparisons below would pass while the
	// catalogue was being corrupted underneath them.
	statLabel := Content().Stats[0].Label
	actionLabel := Content().Actions[0].Label
	skinImage := Content().Skins[0].Image
	locationLabel := Content().Locations[0].Label

	c := Content()
	c.Stats[0].Label = "mutated"
	c.Actions[0].Label = "mutated"
	c.Skins[0].Image = "mutated"
	c.Locations[0].Label = "mutated"

	after := Content()
	if after.Stats[0].Label != statLabel {
		t.Errorf("stat label is now %q; a caller's edit reached the catalogue", after.Stats[0].Label)
	}
	if after.Actions[0].Label != actionLabel {
		t.Errorf("action label is now %q; a caller's edit reached the catalogue", after.Actions[0].Label)
	}
	if after.Skins[0].Image != skinImage {
		t.Errorf("skin image is now %q; a caller's edit reached the catalogue", after.Skins[0].Image)
	}
	if after.Locations[0].Label != locationLabel {
		t.Errorf("location label is now %q; a caller's edit reached the catalogue", after.Locations[0].Label)
	}

	// Stats() is the same handout by a shorter route and needs the same care.
	s := Stats()
	s[0].Key = "mutated"
	if Stats()[0].Key == "mutated" {
		t.Error("Stats() shares its backing array with the catalogue")
	}
}

// TestALookupFindsWhatIsThereAndMissesWhatIsNot pins both directions on
// purpose. A lookup that always missed would pass every "unknown key is
// rejected" assertion in the suite while making the whole catalogue
// unreachable, so the positive half is what stops this test being green and
// worthless.
func TestALookupFindsWhatIsThereAndMissesWhatIsNot(t *testing.T) {
	for _, key := range []string{StatHP, StatBeer, StatBladder} {
		if s, ok := StatByKey(key); !ok || s.Key != key {
			t.Fatalf("StatByKey(%q) = (%+v, %v); want the catalogue's own entry", key, s, ok)
		}
	}
	for _, key := range []string{ActionDrink, ActionRelieve} {
		if a, ok := ActionByKey(key); !ok || a.Key != key {
			t.Fatalf("ActionByKey(%q) = (%+v, %v); want the catalogue's own entry", key, a, ok)
		}
	}

	// A client asking for one of these is either stale or probing; both have to
	// be a clean miss rather than a zero-valued entry that looks real. The
	// near-misses are the interesting rows — a lookup that trimmed or folded case
	// would resolve a key the config never published. Compared with DeepEqual
	// rather than ==: both types now hold a slice and are no longer comparable.
	for _, key := range []string{"", "no-such-key", " " + StatHP, "HP", ActionDrink} {
		if s, ok := StatByKey(key); ok {
			t.Errorf("StatByKey(%q) resolved to %+v; want a miss", key, s)
		} else if !reflect.DeepEqual(s, Stat{}) {
			t.Errorf("StatByKey(%q) missed but returned %+v; want the zero value", key, s)
		}
	}
	for _, key := range []string{"", "no-such-key", " " + ActionDrink, "DRINK", StatHP} {
		if a, ok := ActionByKey(key); ok {
			t.Errorf("ActionByKey(%q) resolved to %+v; want a miss", key, a)
		} else if !reflect.DeepEqual(a, Action{}) {
			t.Errorf("ActionByKey(%q) missed but returned %+v; want the zero value", key, a)
		}
	}
}

// ---------------------------------------------------------------------------
// The yard's regulars.
//
// An NPC is the purest form of the thing this file exists to guard: he has no
// table, no row and no client code, so he is nothing but a catalogue entry and
// the arithmetic it names. There is correspondingly nothing between a typo here
// and a character who stands in the fence, never moves, or is drawn as a blank.
// ---------------------------------------------------------------------------

// TestEveryNPCIsAddressableAndDrawable covers the three fields that reach a
// screen. The key becomes the entity's id in the roster, so a duplicate would
// publish two entities the client cannot tell apart; the art is what it resolves
// against the catalogue exactly as it resolves a pet's skin, so an empty one is
// an entity with nothing to draw; and the label is how a player recognises him.
//
// A silent, nameless prop is imaginable — a crate, a bench — and if one is ever
// added, this test failing is how that becomes a decision rather than an
// oversight.
func TestEveryNPCIsAddressableAndDrawable(t *testing.T) {
	npcs := Content().NPCs
	if len(npcs) == 0 {
		t.Fatal("the catalogue has no regulars; the yard is an empty field and every check below asserts nothing")
	}
	seen := make(map[string]bool, len(npcs))
	for _, npc := range npcs {
		t.Run(npc.Key, func(t *testing.T) {
			if npc.Key == "" {
				t.Fatal("a regular has no key; nothing can address him and his roster id is the bare prefix")
			}
			if seen[npc.Key] {
				t.Fatalf("key %q appears twice; two entities would be published under one id and the client cannot tell them apart", npc.Key)
			}
			seen[npc.Key] = true
			if npc.Art == "" {
				t.Error("no art key; the client resolves this against the catalogue and has nothing to draw without it")
			}
			if npc.Label == "" {
				t.Error("no label; a regular nobody can name is indistinguishable from a player's unnamed Ваня")
			}
		})
	}
}

// TestEveryNPCMovesInAWayThatExists is the join between the two halves of the
// design — a character is art plus a pattern KEY — and it is the join a rename
// breaks silently.
//
// evaluate parks an unknown pattern at Home rather than failing, which is the
// right behaviour at runtime and the reason this has to be checked here: a
// catalogue naming a pattern nobody wrote produces a character standing
// perfectly still, in a game where standing perfectly still is also what one of
// the patterns does on purpose.
func TestEveryNPCMovesInAWayThatExists(t *testing.T) {
	for _, npc := range Content().NPCs {
		t.Run(npc.Key, func(t *testing.T) {
			if _, ok := patterns[npc.Pattern]; !ok {
				t.Fatalf("moves by pattern %q, which is not in the table; he would stand at his home for ever and nothing would say why", npc.Pattern)
			}
		})
	}
}

// TestEveryNPCsParametersSuitItsPattern checks the numbers each pattern actually
// reads, because MotionParams is one struct for all of them and an unused field
// is simply zero.
//
// That is the design's own trade: a pattern chosen by a catalogue string means
// the compiler cannot say a patroller needs a route. So this does. Each of these
// is a character who renders — no crash, no warning — and does nothing anybody
// meant him to do.
func TestEveryNPCsParametersSuitItsPattern(t *testing.T) {
	for _, npc := range Content().NPCs {
		t.Run(npc.Key, func(t *testing.T) {
			switch npc.Pattern {
			case PatternIdle:
				onPlane(t, npc.Params.Home, "his home")
			case PatternWander:
				if npc.Params.Period <= 0 {
					t.Fatalf("wanders on a period of %v; wanderAt parks him at home for anything not positive, so he would never move", npc.Params.Period)
				}
				if npc.Params.Spread.X == 0 && npc.Params.Spread.Y == 0 {
					t.Fatal("wanders with no spread at all; he is an idler with extra arithmetic")
				}
				onPlane(t, npc.Params.Home, "his home")
				// The box he ambles in has to fit on the plane. It is clamped if it
				// does not, so nothing breaks — he just spends part of his day
				// pressed against the fence, which reads as a stuck character.
				for _, corner := range []Point{
					{X: npc.Params.Home.X - npc.Params.Spread.X, Y: npc.Params.Home.Y - npc.Params.Spread.Y},
					{X: npc.Params.Home.X + npc.Params.Spread.X, Y: npc.Params.Home.Y + npc.Params.Spread.Y},
				} {
					onPlane(t, corner, "the corner of the box he wanders in")
				}
			case PatternPatrol:
				if len(npc.Params.Route) < 2 {
					t.Fatalf("patrols a route of %d points; patrolAt parks him on the first one, so his whole joke is standing still", len(npc.Params.Route))
				}
				if npc.Params.Period <= 0 {
					t.Fatalf("patrols on a period of %v; anything not positive parks him on the first point of the route", npc.Params.Period)
				}
				for i, p := range npc.Params.Route {
					onPlane(t, p, fmt.Sprintf("route point %d", i))
				}
			}
		})
	}
}

// TestEveryNPCStaysOnThePlaneAllDay is the catalogue's own version of the sweep
// in motion_test.go, and it is the one that would catch a retune rather than a
// bug.
//
// Numbers here are meant to be moved by feel, and a spread or a route nudged a
// little too far does not fail anywhere: the clamp catches it and the character
// stands against the fence looking stuck. A day's worth of instants either side
// of the world's epoch is enough to catch that — including the ones BEFORE it,
// which a box whose clock is a minute slow really does ask for.
func TestEveryNPCStaysOnThePlaneAllDay(t *testing.T) {
	const step = 977 * time.Millisecond // deliberately not a factor of any period
	for _, npc := range Content().NPCs {
		t.Run(npc.Key, func(t *testing.T) {
			for i := -2000; i < 2000; i++ {
				elapsed := time.Duration(i) * step
				at := evaluate(npc.Pattern, npc.Params, elapsed)
				onPlane(t, at, fmt.Sprintf("his position %v into the world", elapsed))
			}
		})
	}
}

// onPlane fails unless p is somewhere a client could actually draw: 0..1 on both
// axes, and a number at all.
func onPlane(t *testing.T, p Point, what string) {
	t.Helper()
	if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
		t.Fatalf("%s is (%v,%v), which is not a position", what, p.X, p.Y)
	}
	if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
		t.Errorf("%s is (%v,%v), off the plane — the clamp will pin him to the fence and he will read as stuck", what, p.X, p.Y)
	}
}

// TestTheWorldEpochIsAFixedInstant pins the one property of NPC motion that no
// other test can see, and that a browser test provably cannot.
//
// Every character's position is measured from this instant, so it has to be the
// SAME instant in every process. Derive it from process start — `time.Now()` in
// a var, which is the obvious and wrong way to write this — and the entire cast
// teleports on every deploy, several times a day, while every unit test keeps
// passing because they all measure against the same variable they are testing.
//
// A full-stack test was attempted for it and deliberately withdrawn: restarting
// the binary and comparing an NPC's position across the restart passed even
// against a server broken exactly this way, because reconnect latency varies by
// more than the difference being measured. That is the sort of test that is
// worse than none, so the property is pinned here instead — cheaply, and by
// construction.
func TestTheWorldEpochIsAFixedInstant(t *testing.T) {
	want := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	if !worldEpoch.Equal(want) {
		t.Fatalf("worldEpoch = %s; want the literal %s.\n"+
			"If this was changed on purpose, moving it teleports every NPC once and "+
			"then stays correct. If it now depends on time.Now(), it teleports them "+
			"on EVERY deploy and nothing else will tell you.", worldEpoch, want)
	}
	// And it is genuinely a constant rather than something recomputed: two reads
	// an instant apart are the same instant.
	first := worldEpoch
	time.Sleep(0) // yields; no wall-clock dependency, so this is not a sleep-to-wait
	if !worldEpoch.Equal(first) {
		t.Fatal("worldEpoch changed between two reads, so it is being recomputed rather than fixed")
	}
}
