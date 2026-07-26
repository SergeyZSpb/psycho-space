package gamevanyagotchi

import (
	"time"
)

// The event model: what a player did, and the one function that interprets it.
//
// A pet's state used to exist only as its stat rows, overwritten by every
// action. That answered "what is he now" and nothing else — an action left no
// trace once folded into a number, so a retuned constant could not be applied
// retroactively and a real pet's history could not be replayed to reproduce a
// bug. The event log (migrations/009) is the missing half.
//
// THE WHOLE POINT IS THAT THERE IS ONE FUNCTION.
//
// `apply` interprets exactly one event against a snapshot. The live path calls
// it with the verb a player just pressed; a replay calls it in a loop over
// history. A single action is `fold` over a one-element slice, so the two paths
// are not "kept in agreement" — they are the same code, and cannot diverge.
// That property is the reason this file exists at all; the moment a second
// implementation of the arithmetic appears, the log stops being trustworthy.
//
// THE SNAPSHOT REMAINS AUTHORITATIVE FOR A READ. The stat rows in
// game_vanyagotchi_pet_stats are the snapshot, written in the same transaction
// as the events that produced them. So reading a pet is still one indexed query
// and one subtraction however long its history, and a fold is only needed by a
// replay. Persistence is therefore a policy question rather than a correctness
// one — write-through today, and a cheaper cadence later is a change here and
// nowhere else.
//
// NOTHING HERE READS A CLOCK. Every function takes the instant it works
// against, because the whole model is integrated against timestamps and a
// function that fetched its own `now` could not be replayed.

// Event is one thing a player did, at the instant the SERVER accepted it.
//
// The client never supplies `At`. It would be forgeable, and time is the single
// value this entire model rests on — a drink backdated six hours would rewrite
// the decay that followed it. `Seq` orders a pet's events totally, which `At`
// cannot: a batch of verbs sent in one frame shares an instant, and
// drink-then-relieve is a different pet from relieve-then-drink.
type Event struct {
	Seq  int64
	Verb string
	At   time.Time
}

// Snapshot is every stat of one pet, plus whether a death has been recorded.
//
// The accumulator a fold carries. `Rows` is keyed by stat key and is the same
// shape the read path already builds, so a snapshot is exactly what the
// database already stores rather than a parallel representation of it.
type Snapshot struct {
	Rows   map[string]StatRow
	DiedAt *time.Time
}

// clone copies a snapshot, so apply and advance never mutate their input.
//
// Purity is not decoration here: a fold that mutated its accumulator would make
// a caller's "before" value change under it, and the live path deliberately
// keeps the previous state to decide whether an action was allowed.
func (s Snapshot) clone() Snapshot {
	rows := make(map[string]StatRow, len(s.Rows))
	for k, v := range s.Rows {
		rows[k] = v
	}
	out := Snapshot{Rows: rows}
	if s.DiedAt != nil {
		at := *s.DiedAt
		out.DiedAt = &at
	}
	return out
}

// Alive reports whether no death has been recorded.
func (s Snapshot) Alive() bool { return s.DiedAt == nil }

// advance moves every stat forward to `to` and records a death if one happened
// on the way.
//
// This is the read path in one function, and it is also the first half of
// `apply` — which is what makes an action and a plain read agree about the world
// by construction rather than by care.
//
// A death is materialised at the instant DERIVED from the pairs, never at `to`.
// Two readers observing the same death hours apart therefore compute the
// identical timestamp, which is what makes losing the race to write it harmless.
func advance(s Snapshot, to time.Time) Snapshot {
	next := s.clone()
	// Every stat is re-stamped to one shared instant. That is condition 3 of
	// ADR-040 and the one that silently corrupts the arithmetic if skipped:
	// hp is integrated against the other stats' trajectories, so a window whose
	// start predates a driver's own as_of has a stretch of history nobody can
	// reconstruct.
	drivers := s.Rows
	var deadAt time.Time
	dead := false
	for key, row := range s.Rows {
		def, ok := StatByKey(key)
		if !ok {
			// A stored row whose key has left the catalogue cannot be evaluated
			// and is carried untouched rather than dropped — deleting state on
			// a content edit is not something a read should do.
			continue
		}
		next.Rows[key] = StatRow{Key: key, Value: def.AtWith(row.Value, row.AsOf, to, drivers), AsOf: to}
		if !def.Dead(row.Value, row.AsOf, to, drivers) {
			continue
		}
		if at, ok := def.DeadAtWith(row.Value, row.AsOf, drivers); ok && (!dead || at.Before(deadAt)) {
			deadAt, dead = at, true
		}
	}
	if dead && next.Alive() {
		at := deadAt
		next.DiedAt = &at
	}
	return next
}

// apply interprets ONE event against a snapshot, and is the only place a verb
// means anything.
//
// It returns the snapshot unchanged alongside an error when the verb could not
// be applied. A replay skips those: an event that cannot apply produced no state
// change, which is exactly what its absence from the result represents. The live
// path surfaces the error instead, because a player who pressed a button is owed
// an explanation even though the wire owes them no reply — see Service.Do, which
// turns it into a balloon over their own Ваня.
func apply(s Snapshot, e Event) (Snapshot, error) {
	action, ok := ActionByKey(e.Verb)
	if !ok {
		return s, ErrUnknownAction
	}
	for _, effect := range action.Effects {
		if _, ok := StatByKey(effect.StatKey); !ok {
			// A catalogue disagreeing with itself: an action moving a stat that
			// has been removed. Refused rather than partially applied, because
			// a button that appears to work and does not is worse than one that
			// says no.
			return s, ErrUnknownStat
		}
	}

	// Decay first, then the verb. The order is the whole of the coupling: the
	// damage a full bladder did BEFORE it was emptied has to be integrated
	// against the window in which it was full, and applying the effect first
	// would evaluate that window from a pair saying it never was.
	next := advance(s, e.At)
	if !next.Alive() && !action.RevivesFatal {
		return s, ErrPetDead
	}

	// A RESET IGNORES THE DELTAS, because coming back from the dead is coming
	// back as a new Ваня rather than as the old one plus a number. Every stat
	// goes to its catalogue Start — except a lifetime counter, which is exempt
	// for the obvious reason: a total that death set back to nought would not be
	// a lifetime total, and «выпито пива: 0» after forty beers is a lie about
	// the past rather than a fresh beginning.
	//
	// Still a pure function of (Snapshot, Event): the numbers come from the
	// catalogue, which is compiled in, so a replay reads exactly what the live
	// path read. Retuning a Start therefore retunes what a replay says a
	// revival did, which is the same retro-tuning property every other constant
	// here has.
	if action.StartsOver {
		for key := range next.Rows {
			def, ok := StatByKey(key)
			// Keyed on the FLAG, not on the rate. The two coincide in today's
			// catalogue and that is exactly why it matters: inferring "this is a
			// tally" from "its rate is nought" is the client-side mistake this
			// project refuses everywhere else, and it would silently reset the
			// first counter somebody gave a rate to.
			if !ok || def.Counter {
				continue
			}
			next.Rows[key] = StatRow{Key: key, Value: def.Clamp(def.Start), AsOf: e.At}
		}
		next.DiedAt = nil
		return next, nil
	}

	for _, effect := range action.Effects {
		row, ok := next.Rows[effect.StatKey]
		if !ok {
			// The pet has no row for a stat the action moves — a stat added to
			// the catalogue since this pet was last read. Seeding is the read
			// path's job and has already run by the time a verb arrives, so
			// this is unreachable in the live path and merely skipped in a
			// replay.
			continue
		}
		def, _ := StatByKey(effect.StatKey)
		next.Rows[effect.StatKey] = StatRow{
			Key:   effect.StatKey,
			Value: def.Clamp(row.Value + effect.Delta),
			AsOf:  e.At,
		}
	}

	// Bringing him round, and only if the verb actually lifted the fatal stat
	// off its floor: an action allowed on a corpse that failed to move the thing
	// which killed him has not revived anybody.
	if !next.Alive() && action.RevivesFatal && alive(next.Rows) {
		next.DiedAt = nil
	}
	return next, nil
}

// fold applies a whole history, in order.
//
// A single action is this with a one-element slice — which is the point, and the
// reason there is no separate "apply one" path to keep honest. An event that
// cannot be applied is SKIPPED rather than fatal: it changed nothing when it
// happened, so it must change nothing when replayed. That matters most after a
// retune, when a verb that was legal against the old catalogue may not be
// against the new one.
func fold(s Snapshot, events []Event) Snapshot {
	for _, e := range events {
		next, err := apply(s, e)
		if err != nil {
			continue
		}
		s = next
	}
	return s
}

// alive reports whether every fatal stat sits above its floor.
func alive(rows map[string]StatRow) bool {
	for key, row := range rows {
		if def, ok := StatByKey(key); ok && def.Fatal && row.Value <= def.Min {
			return false
		}
	}
	return true
}

// snapshotOf builds the fold's accumulator from what the database holds.
func snapshotOf(rows map[string]StatRow, diedAt *time.Time) Snapshot {
	return Snapshot{Rows: rows, DiedAt: diedAt}.clone()
}

// rowsAt flattens a snapshot back into the rows a write takes, in catalogue
// order so a write is deterministic rather than map-ordered.
func rowsAt(s Snapshot) []StatRow {
	out := make([]StatRow, 0, len(s.Rows))
	for _, def := range catalogue.Stats {
		if row, ok := s.Rows[def.Key]; ok {
			out = append(out, row)
		}
	}
	// Anything stored whose key has left the catalogue keeps its row rather
	// than being silently deleted by the next write.
	for key, row := range s.Rows {
		if _, ok := StatByKey(key); !ok {
			out = append(out, row)
		}
	}
	return out
}

// genesis is a pet as the catalogue says one starts: every stat at its Start
// value, stamped at the instant the pet was created.
//
// The fold's origin. A pet's rows are its CURRENT snapshot rather than its
// first, so a replay cannot begin from them — it begins here and walks the
// whole history forward.
func genesis(at time.Time) Snapshot {
	rows := make(map[string]StatRow, len(catalogue.Stats))
	for _, def := range catalogue.Stats {
		rows[def.Key] = StatRow{Key: def.Key, Value: def.Start, AsOf: at}
	}
	return Snapshot{Rows: rows}
}
