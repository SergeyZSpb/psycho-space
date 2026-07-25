package gamevanyagotchi

import (
	"math"
	"time"
)

// The decay engine, and it is four small pure functions rather than a system.
//
// A stat is stored as the pair (value, as_of) and its current value is computed
// on read:
//
//	current = clamp(value − DecayPerHour × hoursSince(as_of), Min, Max)
//
// Nothing ticks. There is no cron, no per-pet goroutine, no scheduler and no
// background timer — and offline progression is therefore not a feature anybody
// had to build, it is what that expression already means. Reading a value after
// a month away costs exactly what reading it a second later costs, because the
// cost is one subtraction either way.
//
// Two properties are load-bearing and worth not breaking:
//
// It is EXACT, not an approximation of ticking. Linear decay evaluated at an
// instant produces precisely what a continuous simulation would have produced,
// so there is no divergence between "was away" and "was watching" and therefore
// nothing to exploit by choosing when to look. That safety is a property of
// LINEARITY, not of the pattern. The moment a rate depends on another decaying
// value — compounding, or one stat draining another — the closed form becomes an
// approximation, and the SIGN of its error decides whether being absent beats
// playing. If that is ever wanted, derive the closed form from the continuous
// model and check the error's direction deliberately; do not just multiply.
//
// Server time is the only clock. `now` is the server's and `as_of` is a column,
// so a device with a wound-forward clock changes nothing at all.

// At returns the stat's current value, given the stored pair.
//
// A now before as_of yields the stored value untouched rather than running the
// decay backwards. That happens with clock adjustment on the host, and the
// honest reading of "this was true at a moment that has not arrived" is that
// nothing has decayed yet — winding a value back up would be inventing state.
func (s Stat) At(value float64, asOf, now time.Time) float64 {
	elapsed := now.Sub(asOf)
	if elapsed <= 0 {
		return s.Clamp(value)
	}
	return s.Clamp(value - s.DecayPerHour*(float64(elapsed)/hour))
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

// DeadAt returns the instant this stat reaches Min from the stored pair, and
// whether it ever does.
//
// It answers a question a wall clock cannot: the moment he died, rather than the
// moment somebody happened to look. Because the decay is linear the instant is
// derivable exactly — `as_of + (value − Min) / rate` — which is what lets the
// death be recorded truthfully by a read that arrives hours late.
//
// False for a stat that is not fatal, that does not drain, or whose death lies
// so far away that the interval will not fit in a time.Duration. That last case
// is a rate so near zero that "he never dies" is the correct answer anyway, and
// it is checked rather than assumed because the conversion would otherwise wrap
// to a negative duration and report a death in the past.
func (s Stat) DeadAt(value float64, asOf time.Time) (time.Time, bool) {
	if !s.Fatal || s.DecayPerHour <= 0 {
		return time.Time{}, false
	}
	v := s.Clamp(value)
	if v <= s.Min {
		return asOf, true
	}
	ns := ((v - s.Min) / s.DecayPerHour) * hour
	if ns > float64(math.MaxInt64) {
		return time.Time{}, false
	}
	return asOf.Add(time.Duration(ns)), true
}

// Dead reports whether this stat is at its fatal floor at now.
func (s Stat) Dead(value float64, asOf, now time.Time) bool {
	return s.Fatal && s.At(value, asOf, now) <= s.Min
}
