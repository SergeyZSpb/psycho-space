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
func TestOrderInsideABatchIsMeaningful(t *testing.T) {
	start := freshPet(eventEpoch)
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

	relieved := fold(start, []Event{{Seq: 1, Verb: ActionRelieve, At: at}})
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
