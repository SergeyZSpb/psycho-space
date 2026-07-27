package gamevanyagotchi

import (
	"errors"
	"testing"
	"time"
)

// The fold core. What is worth testing here is not the arithmetic — decay_test
// covers that — but the four properties the event model rests on: that live and
// replay are the same function, that neither mutates its input, that order is
// meaningful, and that decay happens BEFORE a verb rather than after it.

// eventEpoch is a fixed instant. Every time in this file is derived from it, so
// nothing here reads a clock and nothing can flake on one.
var eventEpoch = time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)

// freshPet is a snapshot as the catalogue starts one.
func freshPet(at time.Time) Snapshot { return genesis(at) }

// pairOf reads one stored pair out of a snapshot. The as_of half matters as
// often as the value does here: a write is only correct if every row shares one
// instant, and that is invisible in a value.
func pairOf(t *testing.T, s Snapshot, key string) StatRow {
	t.Helper()
	row, ok := s.Rows[key]
	if !ok {
		t.Fatalf("the snapshot has no %q row: %+v", key, s.Rows)
	}
	return row
}

// valueOf reads one stat's value out of a snapshot.
func valueOf(t *testing.T, s Snapshot, key string) float64 {
	t.Helper()
	return pairOf(t, s, key).Value
}

// playedPet is a Ваня who has been alive a while and then died: every ordinary
// stat parked away from its catalogue start, the fatal one on its floor, and the
// lifetime counters holding tallies of their own.
//
// It is the fixture the reset is asserted against, and every part of it is
// chosen so that a reset which did nothing could not pass. A stat already
// standing on its start would be indistinguishable from one that was put back
// there, so the helper fails rather than hand back a value the catalogue happens
// to start at; and the counters hold numbers that are neither their start nor
// each other's, so a reset that cleared them — or that copied one over another —
// is visible rather than arithmetic that happens to agree.
func playedPet(t *testing.T) Snapshot {
	t.Helper()
	s := freshPet(eventEpoch)
	tally := 3.0
	for _, def := range Content().Stats {
		v := def.Max
		if def.Counter {
			tally += 4
			v = tally
		}
		if def.Key == StatHP {
			// The fatal stat on its floor, which is what makes him dead — and
			// what makes "the reset lifted him" a claim with content.
			v = def.Min
		}
		if v == def.Start {
			t.Fatalf("%q is parked at %v, which is also its catalogue start: a reset that did nothing at all would pass every assertion below", def.Key, v)
		}
		s.Rows[def.Key] = StatRow{Key: def.Key, Value: v, AsOf: eventEpoch}
	}
	return s
}

// TestOneActionIsAFoldOfOne is THE property the whole design rests on: the live
// path and a replay are not two implementations kept in agreement, they are the
// same function. If this ever fails, the event log has stopped being a truthful
// record of how a pet got where it is.
func TestOneActionIsAFoldOfOne(t *testing.T) {
	start := freshPet(eventEpoch)
	e := Event{Seq: 1, Verb: ActionDrink, At: eventEpoch.Add(3 * time.Hour)}

	direct, err := apply(start, e)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	folded := fold(start, []Event{e})

	for key := range direct.Rows {
		if got, want := folded.Rows[key], direct.Rows[key]; got != want {
			t.Fatalf("%q differs: fold gave %+v, apply gave %+v — the two paths have diverged, which is the one thing this model may not do",
				key, got, want)
		}
	}
	if len(folded.Rows) != len(direct.Rows) {
		t.Fatalf("fold produced %d rows and apply %d", len(folded.Rows), len(direct.Rows))
	}
}

// TestApplyAndAdvanceLeaveTheirInputAlone. The live path keeps the "before"
// snapshot to decide whether a verb was allowed and whether a death should be
// cleared, so a function that mutated its accumulator would change the answer
// underneath the question.
func TestApplyAndAdvanceLeaveTheirInputAlone(t *testing.T) {
	start := freshPet(eventEpoch)
	beforeHP := valueOf(t, start, StatHP)
	beforeAsOf := start.Rows[StatHP].AsOf

	out, err := apply(start, Event{Seq: 1, Verb: ActionDrink, At: eventEpoch.Add(5 * time.Hour)})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := valueOf(t, start, StatHP); got != beforeHP {
		t.Fatalf("apply moved the input's hp from %v to %v", beforeHP, got)
	}
	if got := start.Rows[StatHP].AsOf; !got.Equal(beforeAsOf) {
		t.Fatalf("apply re-stamped the input's as_of")
	}

	// And the returned map is its own, so a caller writing to the result cannot
	// reach back into the snapshot it came from.
	out.Rows[StatHP] = StatRow{Key: StatHP, Value: -999, AsOf: eventEpoch}
	if got := valueOf(t, start, StatHP); got != beforeHP {
		t.Fatalf("the two snapshots share a map: writing the result changed the input to %v", got)
	}

	_ = advance(start, eventEpoch.Add(time.Hour))
	if got := valueOf(t, start, StatHP); got != beforeHP {
		t.Fatalf("advance moved the input's hp to %v", got)
	}
}

// TestOrderInsideABatchIsMeaningful. Drinking fills the bladder by 25 and
// relieving empties it, so the two orders end somewhere different — which is
// exactly why the log is ordered by seq rather than by time, a batch sharing one
// instant.
//
// He starts with a bladder rather than the catalogue's empty one, and that is
// load-bearing rather than tidiness. «покакать» is gated on having something to
// go for, so against a fresh pet the relieve-first order would be REFUSED and
// skipped by the fold — the two orders would still end somewhere different, and
// this test would go on passing while asserting the gate instead of the ordering.
// The last assertion states the difference exactly for the same reason: it is the
// one that fails when a verb silently stops applying.
func TestOrderInsideABatchIsMeaningful(t *testing.T) {
	start := freshPet(eventEpoch)
	full := enoughFor(t, ActionRelieve, eventEpoch)
	start.Rows[full.Key] = full
	at := eventEpoch.Add(time.Hour)

	drinkThenRelieve := fold(start, []Event{
		{Seq: 1, Verb: ActionDrink, At: at},
		{Seq: 2, Verb: ActionRelieve, At: at},
	})
	relieveThenDrink := fold(start, []Event{
		{Seq: 1, Verb: ActionRelieve, At: at},
		{Seq: 2, Verb: ActionDrink, At: at},
	})

	emptied := valueOf(t, drinkThenRelieve, StatBladder)
	filled := valueOf(t, relieveThenDrink, StatBladder)
	if emptied == filled {
		t.Fatalf("both orders left the bladder at %v; if order stopped mattering, seq is doing nothing and a replay may reorder a batch freely", emptied)
	}
	if emptied != 0 {
		t.Fatalf("relieving last left the bladder at %v; want its floor", emptied)
	}
	// Emptied and then refilled by exactly the drink: any other number means one
	// of the two verbs did not land, which is what a gate refusing the first one
	// looks like from here.
	if want := effectOn(mustAction(t, ActionDrink), StatBladder); filled != want {
		t.Fatalf("relieving first left the bladder at %v; want %v, the floor plus the drink's own delta — both verbs have to apply or the orders differ for the wrong reason",
			filled, want)
	}
}

// TestDecayHappensBeforeTheVerb is the subtlest thing in the file and the one
// that would fail silently. The damage an unmet need did BEFORE it was met has
// to be integrated against the window in which it was unmet — applying the
// effect first would evaluate that window from a pair claiming it never
// happened. This is ADR-040's third condition seen from the fold's side.
func TestDecayHappensBeforeTheVerb(t *testing.T) {
	start := freshPet(eventEpoch)

	// The same two verbs, once back to back and once with a long gap between
	// them. The gap is when beer runs dry and starts costing health.
	together := fold(start, []Event{
		{Seq: 1, Verb: ActionDrink, At: eventEpoch},
		{Seq: 2, Verb: ActionDrink, At: eventEpoch},
	})
	apart := fold(start, []Event{
		{Seq: 1, Verb: ActionDrink, At: eventEpoch},
		{Seq: 2, Verb: ActionDrink, At: eventEpoch.Add(30 * time.Hour)},
	})

	if valueOf(t, apart, StatHP) >= valueOf(t, together, StatHP) {
		t.Fatalf("thirty hours of neglect cost nothing: hp %v apart vs %v together — the decay between events is not being integrated",
			valueOf(t, apart, StatHP), valueOf(t, together, StatHP))
	}
}

// TestAFoldSkipsWhatItCannotApply. A replay must reproduce history, and an
// event that changed nothing when it happened must change nothing now — which
// matters most after a retune, when a verb legal against the old catalogue may
// not be against the new one.
func TestAFoldSkipsWhatItCannotApply(t *testing.T) {
	start := freshPet(eventEpoch)
	at := eventEpoch.Add(time.Hour)

	got := fold(start, []Event{
		{Seq: 1, Verb: "sudo-heal", At: at},
		{Seq: 2, Verb: ActionDrink, At: at},
	})
	only := fold(start, []Event{{Seq: 1, Verb: ActionDrink, At: at}})

	if valueOf(t, got, StatBeer) != valueOf(t, only, StatBeer) {
		t.Fatalf("the unknown verb was not skipped cleanly: beer %v with it, %v without",
			valueOf(t, got, StatBeer), valueOf(t, only, StatBeer))
	}
}

// TestEveryVerbButTheRevivalIsRefusedOnACorpseAndChangesNothing.
//
// A dead Ваня cannot go to the toilet, and — this is the part that changed — he
// cannot have a beer either. Drinking used to carry RevivesFatal, which made
// dying almost invisible: the verb a player presses anyway quietly undid it, so
// the one moment the game is about had no moment. The way back is a verb of its
// own now, and everything else is refused.
//
// Driven off the catalogue rather than naming the toilet, so a verb that
// acquires the flag by accident is caught here rather than by a player
// discovering death has stopped meaning anything. The refusal also has to leave
// the snapshot byte-identical: the live path relies on that to write nothing at
// all when a batch is refused, and a rejected verb that silently re-stamped the
// rows would hand out free hours.
func TestEveryVerbButTheRevivalIsRefusedOnACorpseAndChangesNothing(t *testing.T) {
	// Named outright, because it is the specific thing this test exists to hold:
	// beer is beer now, and if it ever revives again every assertion below would
	// simply skip it.
	if beer := mustAction(t, ActionDrink); beer.RevivesFatal {
		t.Fatalf("%q revives a corpse again; death is meant to have a verb of its own, and a drink that undoes one makes dying unnoticeable", beer.Key)
	}

	refused := 0
	for _, action := range Content().Actions {
		if action.RevivesFatal {
			continue
		}
		refused++
		t.Run(action.Key, func(t *testing.T) {
			dead := playedPet(t)
			out, err := apply(dead, Event{Seq: 1, Verb: action.Key, At: eventEpoch.Add(time.Minute)})
			if !errors.Is(err, ErrPetDead) {
				t.Fatalf("a dead Ваня answered %q with %v; want ErrPetDead", action.Key, err)
			}
			for _, def := range Content().Stats {
				before, after := pairOf(t, dead, def.Key), pairOf(t, out, def.Key)
				if after.Value != before.Value || !after.AsOf.Equal(before.AsOf) {
					t.Fatalf("the refused %q moved %q from %+v to %+v", action.Key, def.Key, before, after)
				}
			}
		})
	}
	if refused == 0 {
		t.Fatal("every action in the catalogue revives, so this test asserts nothing and ErrPetDead is unreachable")
	}
}

// ---------------------------------------------------------------------------
// The catalogue's own precondition: a verb that needs one of his numbers to be
// further along before it means anything.
//
// It is enforced in `apply` rather than in the service, which is what makes it
// hold for a replay as well as for a press — and what stops a client that decided
// not to grey its button out from having a rule of its own. Everything below is
// therefore about the one `if`, and the two halves of it that would rot quietly:
// WHICH side of the comparison the threshold falls on, and WHICH value it is
// compared against.
// ---------------------------------------------------------------------------

// gatedActions is every verb the catalogue gates on a stat.
//
// Discovered rather than named, so a second gated verb is covered by the tests
// below on the day it is added, and so a catalogue that lost the rule altogether
// fails loudly instead of running three tests over an empty list.
func gatedActions(t *testing.T) []Action {
	t.Helper()
	var out []Action
	for _, a := range Content().Actions {
		if a.NeedsStat != "" {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		t.Fatal("no verb in the catalogue is gated on a stat of its own; ErrNotYet is unreachable and every assertion below is about a rule the game no longer has")
	}
	return out
}

// TestAGatedVerbIsRefusedShortOfWhatItNeedsAndAllowedExactlyOnIt is the
// precondition including its BOUNDARY, which is the half nothing else would
// catch.
//
// The server compares `>=`, so the threshold itself is allowed: a bladder holding
// exactly what the catalogue asks for is one he may empty. Off by one either way
// is invisible in every other test here — the value is a float that in practice
// is never sitting exactly on the line — and it is precisely what a later edit
// gets wrong, so it is asserted on the line and a hair to each side of it.
//
// Every event is stamped at the row's own as_of, so nothing decays between the
// value being set up and the gate reading it and the three cases differ by the
// hair and by nothing else. What the gate does with a value that has MOVED since
// is the next test's subject.
func TestAGatedVerbIsRefusedShortOfWhatItNeedsAndAllowedExactlyOnIt(t *testing.T) {
	// A hair, in the units of a 0..100 scale: far finer than anything the
	// catalogue is tuned in, and far coarser than the precision a float64 loses
	// at these magnitudes.
	const hair = 0.001

	for _, action := range gatedActions(t) {
		def := mustStat(t, action.NeedsStat)
		for _, tc := range []struct {
			name    string
			held    float64
			allowed bool
		}{
			{name: "a hair short of it", held: action.NeedsAtLeast - hair, allowed: false},
			{name: "exactly on it", held: action.NeedsAtLeast, allowed: true},
			{name: "a hair past it", held: action.NeedsAtLeast + hair, allowed: true},
		} {
			t.Run(action.Key+" with "+tc.name, func(t *testing.T) {
				start := freshPet(eventEpoch)
				start.Rows[def.Key] = StatRow{Key: def.Key, Value: def.Clamp(tc.held), AsOf: eventEpoch}
				if got := valueOf(t, start, def.Key); got != tc.held {
					t.Fatalf("%q cannot hold %v — it clamped to %v — so this case is not the one it says it is", def.Key, tc.held, got)
				}

				out, err := apply(start, Event{Seq: 1, Verb: action.Key, At: eventEpoch})
				if tc.allowed {
					if err != nil {
						t.Fatalf("%q with %v of %q was refused with %v; the catalogue asks for at least %v and the comparison is >=, so the threshold itself is allowed",
							action.Key, tc.held, def.Key, err, action.NeedsAtLeast)
					}
					return
				}
				if !errors.Is(err, ErrNotYet) {
					t.Fatalf("%q with %v of %q answered %v; want ErrNotYet, because the catalogue asks for %v",
						action.Key, tc.held, def.Key, err, action.NeedsAtLeast)
				}
				// And the snapshot came back untouched. The live path writes
				// whatever apply hands it, so a refusal that re-stamped the rows
				// would hand out free hours of decay to anybody who pressed a
				// button the client had already greyed out.
				for _, s := range Content().Stats {
					was, now := pairOf(t, start, s.Key), pairOf(t, out, s.Key)
					if now.Value != was.Value || !now.AsOf.Equal(was.AsOf) {
						t.Fatalf("the refused %q moved %q from %+v to %+v", action.Key, s.Key, was, now)
					}
				}
			})
		}
	}
}

// TestAGateReadsTheValueAtTheInstantOfTheVerbAndNotTheOneWrittenDown is the most
// valuable assertion in this file, because the wrong implementation passes every
// other one.
//
// A gate that read the STORED number would be right whenever nothing had moved
// since it was written — which is every test that sets a row up and immediately
// presses the button — and wrong exactly when somebody has been away, which is
// most of a real evening. The bladder a player is looking at is the decayed one,
// so that is the one the rule is about: a Ваня who filled up overnight may go to
// the bushes the moment his owner opens the app, without a drink first to make
// the row on disk agree with him.
func TestAGateReadsTheValueAtTheInstantOfTheVerbAndNotTheOneWrittenDown(t *testing.T) {
	for _, action := range gatedActions(t) {
		def := mustStat(t, action.NeedsStat)
		t.Run(action.Key, func(t *testing.T) {
			// A filling stat carries a negative rate — see Stat.DecayPerHour —
			// so this is how much of it an hour of being away is worth. It has to
			// be positive for the case to exist at all: a stat that only ever
			// drains never comes WITHIN reach of a gate by itself, and there
			// would be nothing to wait for.
			filling := -def.DecayPerHour
			if filling <= 0 {
				t.Fatalf("%q moves by %v an hour, so no amount of being away brings %q within reach; this case cannot be set up against the current catalogue",
					def.Key, def.DecayPerHour, action.Key)
			}
			if action.NeedsAtLeast <= def.Min {
				t.Fatalf("%q asks for %v of %q, which is at or below its floor %v; the gate could never refuse anything",
					action.Key, action.NeedsAtLeast, def.Key, def.Min)
			}
			// The stored pair says empty, and stays that way — a read never
			// re-stamps — so the only thing that changes between the two cases
			// below is how long the pet has been left alone.
			empty := StatRow{Key: def.Key, Value: def.Min, AsOf: eventEpoch}
			start := freshPet(eventEpoch)
			start.Rows[empty.Key] = empty
			full := time.Duration((action.NeedsAtLeast - def.Min) / filling * hour)

			// Nine tenths of the way there: still short, and refused on a value
			// that is nowhere in the snapshot it was computed from.
			short := eventEpoch.Add(full * 9 / 10)
			if _, err := apply(start, Event{Seq: 1, Verb: action.Key, At: short}); !errors.Is(err, ErrNotYet) {
				t.Fatalf("%q after %s of filling answered %v; want ErrNotYet — %v an hour has not yet carried %q from %v to the %v the catalogue asks for",
					action.Key, full*9/10, err, filling, def.Key, def.Min, action.NeedsAtLeast)
			}

			// And a little past it. THE ASSERTION THIS TEST EXISTS FOR: the row
			// on disk is unchanged and still says he is empty, and the verb goes
			// through anyway, because what the gate reads is the value at the
			// instant of the press.
			ready := eventEpoch.Add(full * 11 / 10)
			if _, err := apply(start, Event{Seq: 1, Verb: action.Key, At: ready}); err != nil {
				t.Fatalf("%q after %s of filling was refused with %v; the stored row still reads %v of %q, and a gate that believed it would refuse a player who is plainly full",
					action.Key, full*11/10, err, def.Min, def.Key)
			}
			if stored := valueOf(t, start, def.Key); stored >= action.NeedsAtLeast {
				t.Fatalf("the stored %q is %v, already past the %v the gate asks for; the case above proves nothing unless the row on disk disagrees with the answer",
					def.Key, stored, action.NeedsAtLeast)
			}
		})
	}
}

// TestRelievingHimselfTwiceInOneBreathIsRefusedTheSecondTime is the same rule in
// the falling direction, reached through the fold rather than through the clock.
//
// It has to be, and that is a fact about the content rather than a shortcut: the
// only stat the catalogue gates a verb on is one that FILLS, so there is no
// amount of waiting that carries it back down below the threshold. What a batch
// can do is spend it inside a single instant — and it asks the identical question
// of the identical line of code. The second press is judged against what the
// first one LEFT, not against the row both of them started from, which still says
// he was full.
func TestRelievingHimselfTwiceInOneBreathIsRefusedTheSecondTime(t *testing.T) {
	relieve := mustAction(t, ActionRelieve)
	if relieve.NeedsStat == "" {
		t.Fatalf("the catalogue no longer gates %q on a stat; this test is about a rule the game does not have", relieve.Key)
	}
	if effectOn(relieve, relieve.NeedsStat) >= 0 {
		t.Fatalf("%q no longer spends the %q it needs (it moves it by %v); pressing it twice would not be refused for the reason this test names",
			relieve.Key, relieve.NeedsStat, effectOn(relieve, relieve.NeedsStat))
	}

	full := enoughFor(t, ActionRelieve, eventEpoch)
	start := freshPet(eventEpoch)
	start.Rows[full.Key] = full

	once, err := apply(start, Event{Seq: 1, Verb: relieve.Key, At: eventEpoch})
	if err != nil {
		t.Fatalf("the first %q with %v of %q was refused with %v; the fixture is not set up for what this test is about",
			relieve.Key, full.Value, full.Key, err)
	}
	if _, err := apply(once, Event{Seq: 2, Verb: relieve.Key, At: eventEpoch}); !errors.Is(err, ErrNotYet) {
		t.Fatalf("the second %q in the same instant answered %v; want ErrNotYet — nothing decayed between the two, so the only thing that can refuse it is what the first one left behind",
			relieve.Key, err)
	}
}

// TestAVerbWithNoPreconditionOfItsOwnIsAlwaysAvailable is the other side of the
// catalogue lookup, and it is what stops the gate becoming a rule about the PET.
//
// A precondition belongs to the verb that carries one. Implemented as "is this
// Ваня in a fit state to do things" it would refuse the lot of them, and a new
// player's first screen — every stat on its catalogue start, every counter at
// nought — would open with no button he is allowed to press.
func TestAVerbWithNoPreconditionOfItsOwnIsAlwaysAvailable(t *testing.T) {
	start := freshPet(eventEpoch)
	at := eventEpoch.Add(time.Minute)

	free := 0
	for _, action := range Content().Actions {
		if action.NeedsStat != "" {
			continue
		}
		free++
		t.Run(action.Key, func(t *testing.T) {
			if _, err := apply(start, Event{Seq: 1, Verb: action.Key, At: at}); err != nil {
				t.Fatalf("%q on a pet holding nothing but its starting values was refused with %v; the catalogue gives it no precondition to fail",
					action.Key, err)
			}
		})
	}
	if free == 0 {
		t.Fatal("every verb in the catalogue is gated on something, so a new player's first screen has no button he may press at all")
	}

	// And on the SAME pet the gated ones are refused, which is what gives the
	// loop above its teeth: a gate that had quietly stopped being enforced would
	// satisfy every line of it.
	for _, action := range gatedActions(t) {
		if _, err := apply(start, Event{Seq: 1, Verb: action.Key, At: at}); !errors.Is(err, ErrNotYet) {
			t.Fatalf("%q on a pet holding nothing answered %v; want ErrNotYet, or the freedom asserted above is not freedom from anything",
				action.Key, err)
		}
	}
}

// TestDeathIsRecordedAtTheDerivedInstant. Not at the moment somebody looked —
// which is what lets two readers hours apart compute the same timestamp, and
// what makes losing the race to write it harmless.
func TestDeathIsRecordedAtTheDerivedInstant(t *testing.T) {
	start := freshPet(eventEpoch)
	late := eventEpoch.Add(365 * 24 * time.Hour)

	got := advance(start, late)
	if got.Alive() {
		t.Fatal("a year of neglect left him alive")
	}
	if !got.DiedAt.Before(late) {
		t.Fatalf("died_at is %v, the instant we looked; want the derived moment, long before it", got.DiedAt)
	}

	// And looking again does not move it.
	again := advance(got, late.Add(24*time.Hour))
	if !again.DiedAt.Equal(*got.DiedAt) {
		t.Fatalf("a second read moved the death from %v to %v", got.DiedAt, again.DiedAt)
	}
}

// TestRevivingClearsTheDeathAndTheResetIsWhatLiftsHim.
//
// The verb is allowed past the death guard because the catalogue marks it
// RevivesFatal, and it clears the death because StartsOver writes every ordinary
// stat back to its catalogue Start.
//
// "Only when it lifts him" used to be the other half of this test, against beer:
// an action ALLOWED on a corpse that failed to move the stat which killed him
// has revived nobody, and clearing died_at anyway would report a pet alive whose
// health is still zero — dead again on the very next read, with the recorded
// moment of death now a lie. A reset always lifts him, so that condition is no
// longer a question about the verb. What it becomes is a question about the
// CONTENT, and that is what is pinned here: the fatal stat's Start has to sit
// above its own floor, or the reset would land him straight back where he was.
// The delta-shaped revival is still guarded — apply's own `alive(next.Rows)`
// check on the way out — it is simply no longer reachable through any shipped
// verb, since the only verb that revives returns before ever getting there.
func TestRevivingClearsTheDeathAndTheResetIsWhatLiftsHim(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	if hpDef.Start <= hpDef.Min {
		t.Fatalf("the fatal stat starts at %v, which is not above its floor %v: a reset would hand back a pet that is dead again the instant anybody reads it",
			hpDef.Start, hpDef.Min)
	}

	dead := playedPet(t)
	at := eventEpoch.Add(time.Minute)

	got, err := apply(dead, Event{Seq: 1, Verb: ActionRevive, At: at})
	if err != nil {
		t.Fatalf("the reviving verb was refused: %v", err)
	}
	if !got.Alive() {
		t.Fatalf("%q did not bring him round: hp %v", ActionRevive, valueOf(t, got, StatHP))
	}
	if got := valueOf(t, got, StatHP); got != hpDef.Clamp(hpDef.Start) {
		t.Fatalf("hp after the revival = %v; want the catalogue start %v", got, hpDef.Clamp(hpDef.Start))
	}
}

// TestAResetPutsEveryOrdinaryStatBackToItsCatalogueStart is the reset itself,
// and it is a different thing from an effect rather than a large one.
//
// Coming back from the dead is coming back as a NEW Ваня — hp 65, beer 60, an
// empty bladder — and none of those is reachable by adding a fixed amount to
// whatever he happened to die holding: a delta big enough to clamp gets you the
// stat's bound, not its start. So the verb carries no deltas at all and apply
// writes the catalogue's own numbers, which is also what keeps a replay honest —
// the values come from the compiled-in catalogue, so retuning a Start retunes
// what a replay says a revival did.
//
// Every stat, not just the one that killed him: the reset is total, and a stat
// left holding its dying value would be the old Ваня wearing a new one's health.
// The shared instant is asserted alongside, because the coupled decay is only
// exact while every pair carries one as_of.
func TestAResetPutsEveryOrdinaryStatBackToItsCatalogueStart(t *testing.T) {
	dead := playedPet(t)
	at := eventEpoch.Add(time.Minute)

	got, err := apply(dead, Event{Seq: 1, Verb: ActionRevive, At: at})
	if err != nil {
		t.Fatalf("apply(%q): %v", ActionRevive, err)
	}
	if !got.Alive() {
		t.Fatal("the death survived a reset")
	}

	ordinary := 0
	for _, def := range Content().Stats {
		if def.Counter {
			continue // exempt on purpose — see TestAResetLeavesTheLifetimeCountersAlone
		}
		ordinary++
		row := pairOf(t, got, def.Key)
		if want := def.Clamp(def.Start); row.Value != want {
			t.Errorf("%q is %v after a reset; want its catalogue start %v — a revival is not the old Ваня plus a number",
				def.Key, row.Value, want)
		}
		if !row.AsOf.Equal(at) {
			t.Errorf("%q is stamped %v after a reset; want the instant of the verb, %v — every row must share one as_of or the coupled decay has a window it cannot reconstruct",
				def.Key, row.AsOf, at)
		}
	}
	if ordinary == 0 {
		t.Fatal("every stat in the catalogue is a counter, so this test asserts nothing about the reset")
	}
}

// TestAResetLeavesTheLifetimeCountersAlone is the subtle half, and the one a
// later edit is most likely to break by tidying the exemption away.
//
// A total that death set back to nought would not be a total: «выпито пива: 0»
// after forty beers is a lie about the past rather than a fresh beginning. The
// counters are the one part of a pet that survives him, which is what makes them
// worth anything at all as a record — so the reset skips them deliberately, and
// this says so where a reader of the loop would otherwise have to infer it.
//
// Exempt from the RESET is not exempt from the WRITE: a counter is still
// re-stamped with everything else, because a row left at an older as_of breaks
// the one-instant invariant the decay of the stats around it depends on.
func TestAResetLeavesTheLifetimeCountersAlone(t *testing.T) {
	dead := playedPet(t)
	at := eventEpoch.Add(time.Minute)

	got, err := apply(dead, Event{Seq: 1, Verb: ActionRevive, At: at})
	if err != nil {
		t.Fatalf("apply(%q): %v", ActionRevive, err)
	}

	counters := 0
	for _, def := range Content().Stats {
		if !def.Counter {
			continue
		}
		counters++
		before, after := pairOf(t, dead, def.Key), pairOf(t, got, def.Key)
		if after.Value != before.Value {
			t.Errorf("%q went from %v to %v across a revival; a lifetime tally that a death reset would not be a lifetime tally",
				def.Key, before.Value, after.Value)
		}
		if !after.AsOf.Equal(at) {
			t.Errorf("%q is stamped %v after a revival; want the instant of the verb, %v — being exempt from the reset is not being exempt from the write",
				def.Key, after.AsOf, at)
		}
	}
	if counters == 0 {
		t.Fatal("the catalogue has no lifetime counters, so this test asserts nothing")
	}
}

// TestTheCountersCountWhatTheVerbsDid is the whole mechanism behind a lifetime
// tally, which is that there isn't one.
//
// A counter is an ordinary stat whose rate is nought, so counting is an effect
// like any other and needs no code, no table and no migration of its own — which
// is what lets a record be added to this game by editing the catalogue. The two
// halves below are both worth stating: the tally goes up once per press, and it
// does not move on its own however long nobody plays, which is the property that
// distinguishes a counter from a bar that happens to be full.
func TestTheCountersCountWhatTheVerbsDid(t *testing.T) {
	start := freshPet(eventEpoch)
	at := eventEpoch.Add(time.Hour)

	// Two rounds in one batch, which is also the general case: a batch is folded
	// in order against one snapshot, so the second drink counts the first one's
	// result rather than the fresh pet's.
	rounds := fold(start, []Event{
		{Seq: 1, Verb: ActionDrink, At: at},
		{Seq: 2, Verb: ActionDrink, At: at},
	})
	if got := valueOf(t, rounds, StatBeersDrunk); got != 2 {
		t.Errorf("%q = %v after two drinks; want 2 — a counter moves by an effect, so a press that failed to land is invisible in every other bar",
			StatBeersDrunk, got)
	}
	if got := valueOf(t, rounds, StatShitsTaken); got != 0 {
		t.Errorf("%q = %v after two drinks and no visit to the bushes; want 0 — the two tallies are being moved by the same effect",
			StatShitsTaken, got)
	}

	// A drink first, because going to the bushes now needs something to go for:
	// the catalogue gates «покакать» on the bladder, and a fresh pet's is empty.
	// The drink is what fills it, which is the loop the two verbs make.
	relieved := fold(start, []Event{
		{Seq: 1, Verb: ActionDrink, At: at},
		{Seq: 2, Verb: ActionRelieve, At: at},
	})
	if got := valueOf(t, relieved, StatShitsTaken); got != 1 {
		t.Errorf("%q = %v after one visit to the bushes; want 1", StatShitsTaken, got)
	}

	// Three weeks of nobody playing. Every bar he has moves — he is long dead by
	// then — and the tally does not, because its rate is nought and nothing but a
	// verb ever touches it.
	later := advance(rounds, at.Add(3*7*24*time.Hour))
	if got := valueOf(t, later, StatBeersDrunk); got != 2 {
		t.Errorf("%q = %v after three weeks away; want the 2 it was left at — a tally that decays is a bar wearing a label", StatBeersDrunk, got)
	}
}

// TestARowWhoseKeyLeftTheCatalogueIsCarriedNotDropped. Deleting somebody's state
// because content changed is not a thing a read may do.
func TestARowWhoseKeyLeftTheCatalogueIsCarriedNotDropped(t *testing.T) {
	s := freshPet(eventEpoch)
	s.Rows["mood"] = StatRow{Key: "mood", Value: 42, AsOf: eventEpoch}

	got := advance(s, eventEpoch.Add(time.Hour))
	if row, ok := got.Rows["mood"]; !ok || row.Value != 42 {
		t.Fatalf("the orphan row was altered or dropped: %+v", got.Rows["mood"])
	}
	found := false
	for _, r := range rowsAt(got) {
		if r.Key == "mood" {
			found = true
		}
	}
	if !found {
		t.Fatal("the orphan row is missing from rowsAt, so the next write would delete it")
	}
}

// TestRowsAtIsDeterministicAndInCatalogueOrder. Map iteration is randomised, so
// a write built straight from the map would order its arrays differently every
// call — which turns a diff of two writes into noise.
func TestRowsAtIsDeterministicAndInCatalogueOrder(t *testing.T) {
	s := freshPet(eventEpoch)
	want := make([]string, 0, len(catalogue.Stats))
	for _, def := range catalogue.Stats {
		want = append(want, def.Key)
	}
	for i := 0; i < 50; i++ {
		got := rowsAt(s)
		if len(got) != len(want) {
			t.Fatalf("rowsAt returned %d rows; want %d", len(got), len(want))
		}
		for j, r := range got {
			if r.Key != want[j] {
				t.Fatalf("run %d: position %d is %q; want catalogue order %q", i, j, r.Key, want[j])
			}
		}
	}
}

// TestApplyKnowsNothingAboutWhereAnybodyIsStandingOrWhatIsLeftInTheCrate is the
// boundary that keeps a replay possible at all, asserted from the side that can
// break it.
//
// Drinking is now gated twice — he has to be AT the crate, and there has to be
// beer in it — and neither of those rules is here. `apply` interprets one event
// against a snapshot and nothing else, so a fold over a history has no idea
// where anybody was standing last March and does not have to: the log records
// that the drink HAPPENED, and the gates recorded at the time are what let it
// happen. Both live in Service.Do.
//
// Putting either inside `apply` would be the mistake, and it would look
// reasonable: the rules belong to the verb. What it would cost is the whole
// point of the log — a replay would need a position nobody stored and a crate
// count that has since moved, which means a database read inside a pure fold and
// an answer that changes every time it is asked.
//
// So this drives the fold with NO service, NO position map, NO world and NO
// repository at all, which is the strongest available statement that it needs
// none of them: a package-level function over two values.
func TestApplyKnowsNothingAboutWhereAnybodyIsStandingOrWhatIsLeftInTheCrate(t *testing.T) {
	drink := mustAction(t, ActionDrink)
	if drink.NeedsNear == "" && drink.Contests == "" {
		t.Fatalf("the catalogue no longer gates %q on the world at all; this test is guarding a boundary the game does not have", drink.Key)
	}
	moves := effectOn(drink, StatBeer)
	if moves <= 0 {
		t.Fatalf("the catalogue says %q moves %q by %v; this test needs a verb whose effect is visible", drink.Key, StatBeer, moves)
	}

	start := freshPet(eventEpoch)
	before := valueOf(t, start, StatBeer)

	out, err := apply(start, Event{Seq: 1, Verb: ActionDrink, At: eventEpoch})
	if err != nil {
		t.Fatalf("apply(%s) = %v; a replay must be able to apply a drink with nobody standing anywhere and no crate in sight", ActionDrink, err)
	}
	if got, want := valueOf(t, out, StatBeer), mustStat(t, StatBeer).Clamp(before+moves); got != want {
		t.Errorf("%q = %v after a replayed drink; want %v — the verb applied, so it has to have applied fully", StatBeer, got, want)
	}

	// And a whole history of them, which is what a replay actually does: an
	// arrival gate hiding in here would refuse every drink but the ones somebody
	// could prove, and a stock check would refuse them all after the seventh.
	history := make([]Event, 0, crateStock+4)
	for i := 0; i < crateStock+4; i++ {
		history = append(history, Event{Seq: int64(i + 1), Verb: ActionDrink, At: eventEpoch.Add(time.Duration(i) * time.Hour)})
	}
	folded := fold(freshPet(eventEpoch), history)
	if got := valueOf(t, folded, StatBeersDrunk); got != float64(len(history)) {
		t.Errorf("%q = %v after replaying %d drinks; want %d — `fold` SKIPS what it cannot apply, so a gate in here would be silently dropped events rather than a failure",
			StatBeersDrunk, got, len(history), len(history))
	}
}
