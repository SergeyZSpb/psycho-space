package gamevanyagotchi

import (
	"math"
	"sort"
	"time"
)

// The decay engine: small pure functions, no system.
//
// A stat is stored as the pair (value, as_of) and its current value is computed
// on read:
//
//	current = clamp(value − DecayPerHour × hoursSince(as_of) − penalties, Min, Max)
//
// Nothing ticks. There is no cron, no per-pet goroutine, no scheduler and no
// background timer — and offline progression is therefore not a feature anybody
// had to build, it is what that expression already means. Reading a value after
// a month away costs exactly what reading it a second later costs.
//
// PENALTIES ARE THE COUPLING, AND THEY ARE THE DELICATE PART.
//
// Health does not simply rot: it falls faster while beer is empty and faster
// while the bladder is full. That makes hp's RATE depend on other decaying
// values — exactly the thing ADR-038 warned turns a closed form into an
// approximation, and one whose error sign decides whether being absent beats
// playing. It stays EXACT here, and only because three conditions hold together
// (ADR-040):
//
//  1. The coupling is ONE-DIRECTIONAL. Beer and bladder drive hp; nothing drives
//     them but time and the player. There is no feedback term to integrate.
//  2. Every driver is LINEAR AND MONOTONE between writes, so the instant it
//     crosses a threshold is solvable in closed form, and once it has crossed it
//     stays crossed. A penalty is therefore a SUFFIX of the window, described by
//     a single instant: its ONSET.
//  3. Every write re-stamps EVERY stat, so all pairs share one as_of and each
//     driver's trajectory is known across the whole integration window. Break
//     this and the maths silently loses damage: relieve yourself at noon and,
//     without the re-stamp, the morning's full bladder would be evaluated from
//     the post-reset pair and its damage would simply vanish.
//
// Given those three, hp is piecewise-linear with one breakpoint per penalty, and
// both its value and the instant it reaches zero are computed exactly rather
// than approximated. A future stat wanting a non-monotone driver, or a mutual
// dependency, satisfies none of this and needs its own derivation.

// Penalty raises a stat's drain while another stat sits in a bad range.
//
// It reads as a sentence: "hp loses another RatePerHour while WhenKey is at or
// above/below Threshold". Content rather than code — adding one is a catalogue
// entry, and no schema knows it exists.
type Penalty struct {
	// WhenKey is the driving stat.
	WhenKey string `json:"when_key"`
	// Threshold and Above together give the condition: at or above Threshold
	// when Above is set, at or below it otherwise.
	Threshold float64 `json:"threshold"`
	Above     bool    `json:"above"`
	// RatePerHour is added to the penalised stat's drain while it applies.
	RatePerHour float64 `json:"rate_per_hour"`
}

// holds reports whether a driver value satisfies the condition.
func (p Penalty) holds(v float64) bool {
	if p.Above {
		return v >= p.Threshold
	}
	return v <= p.Threshold
}

// onset returns the instant this penalty starts applying — never earlier than
// asOf — and whether it ever applies at all.
//
// A single instant is enough BECAUSE the driver is monotone between writes: it
// either already qualifies, or it is heading towards the threshold and will stay
// past it once it arrives, or it is heading away and never will. That is what
// makes a penalty a suffix rather than a set of intervals, and it is the whole
// reason the arithmetic stays closed-form.
func (p Penalty) onset(asOf time.Time, drivers map[string]StatRow) (time.Time, bool) {
	row, ok := drivers[p.WhenKey]
	if !ok {
		return time.Time{}, false
	}
	def, ok := StatByKey(p.WhenKey)
	if !ok {
		// A driver the catalogue no longer defines cannot be evaluated, so it
		// penalises nothing — the same rule the read path uses for a stored row
		// whose key has gone: unrenderable, therefore ignored.
		return time.Time{}, false
	}
	if p.holds(def.At(row.Value, row.AsOf, asOf)) {
		return asOf, true
	}
	// Not yet. Heading the wrong way — or static — means never. DecayPerHour is
	// signed, so a stat that FILLS has a negative rate.
	if p.Above && def.DecayPerHour >= 0 {
		return time.Time{}, false
	}
	if !p.Above && def.DecayPerHour <= 0 {
		return time.Time{}, false
	}
	at, ok := def.crossesAt(row.Value, row.AsOf, p.Threshold)
	if !ok || at.Before(asOf) {
		return time.Time{}, false
	}
	return at, true
}

// crossesAt returns the instant this stat passes through threshold, and whether
// it ever does. A crossing already in the past is returned as such; the caller
// decides whether that is meaningful.
func (s Stat) crossesAt(value float64, asOf time.Time, threshold float64) (time.Time, bool) {
	if s.DecayPerHour == 0 {
		return time.Time{}, false
	}
	return addHours(asOf, (s.Clamp(value)-threshold)/s.DecayPerHour)
}

// At returns the stat's current value with no coupling applied.
//
// Kept, and used deliberately: most stats have no penalties, and it is also how
// a DRIVER's own trajectory is read. A driver is never itself penalised — that
// is condition 1 above, and reading one any other way would reintroduce the
// feedback term the closed form does not have.
func (s Stat) At(value float64, asOf, now time.Time) float64 {
	return s.AtWith(value, asOf, now, nil)
}

// AtWith returns the stat's current value including every penalty its drivers
// have switched on.
//
// A now before as_of yields the stored value untouched rather than running the
// decay backwards. That happens with clock adjustment on the host, and the
// honest reading of "this was true at a moment that has not arrived" is that
// nothing has decayed yet — winding a value back up would be inventing state.
func (s Stat) AtWith(value float64, asOf, now time.Time, drivers map[string]StatRow) float64 {
	if !now.After(asOf) {
		return s.Clamp(value)
	}
	drop := s.DecayPerHour * hoursBetween(asOf, now)
	for _, p := range s.Penalties {
		from, ok := p.onset(asOf, drivers)
		if !ok || !from.Before(now) {
			continue
		}
		drop += p.RatePerHour * hoursBetween(from, now)
	}
	return s.Clamp(value - drop)
}

// RateAt returns the drain the stat is suffering at now, per hour.
//
// Sent to the client so a bar can keep creeping between fetches without the
// browser owning a second copy of the coupling above — the mistake that would
// turn one piece of arithmetic into two that have to be kept honest. It is
// correct until the next onset, which is hours away, and every action answers
// with fresh server state regardless.
func (s Stat) RateAt(asOf, now time.Time, drivers map[string]StatRow) float64 {
	rate := s.DecayPerHour
	for _, p := range s.Penalties {
		if from, ok := p.onset(asOf, drivers); ok && !from.After(now) {
			rate += p.RatePerHour
		}
	}
	return rate
}

// Clamp forces v inside the stat's bounds.
//
// The non-finite guard is unreachable by construction — the only writers are the
// catalogue's own start values and clamped arithmetic over them — and it is here
// anyway because of where a NaN would surface if one ever did: encoding/json
// refuses to marshal one, so a single bad row would turn the whole state
// endpoint into a 500 rather than into one odd-looking bar. Falling back to the
// starting value keeps the game playable and keeps the failure visible.
func (s Stat) Clamp(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return s.Start
	}
	return math.Min(s.Max, math.Max(s.Min, v))
}

// DeadAt returns the instant this stat reaches Min with no coupling applied.
func (s Stat) DeadAt(value float64, asOf time.Time) (time.Time, bool) {
	return s.DeadAtWith(value, asOf, nil)
}

// DeadAtWith returns the instant this stat reaches Min, and whether it ever
// does, with every penalty taken into account.
//
// It answers a question a wall clock cannot: the moment he died, rather than the
// moment somebody happened to look — which is what lets a death be recorded
// truthfully by a read arriving hours late.
//
// With penalties the value is piecewise-linear rather than linear, so this walks
// the segments between onsets and finds the one the floor falls inside. Still
// exact, still O(penalties), still nothing stored.
//
// False for a stat that is not fatal, or one that never drains fast enough to
// arrive — including a rate so near zero that the interval will not fit in a
// time.Duration, which is checked rather than assumed because the conversion
// would otherwise wrap and report a death in the past.
func (s Stat) DeadAtWith(value float64, asOf time.Time, drivers map[string]StatRow) (time.Time, bool) {
	if !s.Fatal {
		return time.Time{}, false
	}
	v := s.Clamp(value)
	if v <= s.Min {
		return asOf, true
	}

	// Each onset is a step up in the drain; sorted, they cut the future into
	// segments of constant rate.
	type step struct {
		at   time.Time
		rate float64
	}
	steps := make([]step, 0, len(s.Penalties))
	for _, p := range s.Penalties {
		if from, ok := p.onset(asOf, drivers); ok {
			steps = append(steps, step{at: from, rate: p.RatePerHour})
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].at.Before(steps[j].at) })

	cur, rate, left := asOf, s.DecayPerHour, v-s.Min
	for _, st := range steps {
		if st.at.After(cur) {
			span := hoursBetween(cur, st.at)
			// A segment that is not draining cannot kill him, but time still
			// passes in it — so only the falling case consumes any of `left`.
			if rate > 0 {
				if need := left / rate; need <= span {
					return addHours(cur, need)
				}
				left -= rate * span
			}
			cur = st.at
		}
		rate += st.rate
	}
	if rate <= 0 {
		return time.Time{}, false
	}
	return addHours(cur, left/rate)
}

// Troubled reports whether a value sits in the range the catalogue calls
// trouble: below WarnAt when high is good, above it otherwise.
//
// One expression for both directions, and the threshold is content — so the
// amber bar on a screen and the rough-looking Ваня on the plane are decided by
// the same number rather than by two that can drift apart.
func (s Stat) Troubled(v float64) bool {
	// A lifetime tally has no bad end of the scale — «выпито пива: 0» is not a
	// problem and «выпито пива: 400» is not either — so it is never trouble and
	// WarnAt is never consulted on one.
	if s.Counter {
		return false
	}
	if s.GoodHigh {
		return v < s.WarnAt
	}
	return v > s.WarnAt
}

// Dead reports whether this stat is at its fatal floor at now, coupling
// included.
func (s Stat) Dead(value float64, asOf, now time.Time, drivers map[string]StatRow) bool {
	return s.Fatal && s.AtWith(value, asOf, now, drivers) <= s.Min
}

// hoursBetween is b − a, in hours.
func hoursBetween(a, b time.Time) float64 { return float64(b.Sub(a)) / hour }

// addHours offsets t, refusing an interval that will not fit in a time.Duration
// rather than wrapping it into a nonsense instant.
func addHours(t time.Time, h float64) (time.Time, bool) {
	ns := h * hour
	if math.IsNaN(ns) || math.Abs(ns) > float64(math.MaxInt64) {
		return time.Time{}, false
	}
	return t.Add(time.Duration(ns)), true
}
