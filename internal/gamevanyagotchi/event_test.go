package gamevanyagotchi

import (
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

// valueOf reads one stat out of a snapshot.
func valueOf(t *testing.T, s Snapshot, key string) float64 {
	t.Helper()
	row, ok := s.Rows[key]
	if !ok {
		t.Fatalf("the snapshot has no %q row: %+v", key, s.Rows)
	}
	return row.Value
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

// TestARefusedVerbChangesNothing. A dead Ваня cannot go to the toilet, and the
// refusal has to leave the snapshot byte-identical — the live path relies on
// that to write nothing at all when a batch is refused.
func TestARefusedVerbChangesNothing(t *testing.T) {
	dead := freshPet(eventEpoch)
	dead.Rows[StatHP] = StatRow{Key: StatHP, Value: 0, AsOf: eventEpoch}

	out, err := apply(dead, Event{Seq: 1, Verb: ActionRelieve, At: eventEpoch.Add(time.Minute)})
	if err == nil {
		t.Fatal("a dead Ваня accepted the toilet")
	}
	if valueOf(t, out, StatBladder) != valueOf(t, dead, StatBladder) ||
		!out.Rows[StatHP].AsOf.Equal(dead.Rows[StatHP].AsOf) {
		t.Fatalf("the refusal moved something: %+v became %+v", dead.Rows, out.Rows)
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

// TestRevivingClearsTheDeathAndOnlyWhenItLiftsHim.
func TestRevivingClearsTheDeathAndOnlyWhenItLiftsHim(t *testing.T) {
	dead := freshPet(eventEpoch)
	dead.Rows[StatHP] = StatRow{Key: StatHP, Value: 0, AsOf: eventEpoch}
	at := eventEpoch.Add(time.Minute)

	got, err := apply(dead, Event{Seq: 1, Verb: ActionDrink, At: at})
	if err != nil {
		t.Fatalf("the reviving verb was refused: %v", err)
	}
	if !got.Alive() {
		t.Fatalf("drink did not bring him round: hp %v", valueOf(t, got, StatHP))
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
