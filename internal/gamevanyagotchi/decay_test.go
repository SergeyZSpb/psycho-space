package gamevanyagotchi

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

// The decay is four pure functions over a stored (value, as_of) pair, which is
// why every property below is reachable without a clock, a database or a
// service. Nothing in this file reads time.Now() or sleeps: every instant is
// derived from decayEpoch, so a failure means the arithmetic is wrong and never
// that the machine was busy.

// decayEpoch is the arbitrary instant every stored pair in this file is stamped
// with. Fixed rather than time.Now() so a failure reads identically on every
// run, and far enough from the zero time that stepping backwards from it is
// still a sane timestamp.
var decayEpoch = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

// hoursAfter is the instant h hours after the epoch, negative for before it.
// Fractional on purpose: the decay is continuous, and a suite that only ever
// stepped in whole hours would not notice an implementation that quietly
// rounded to one.
func hoursAfter(h float64) time.Time {
	return decayEpoch.Add(time.Duration(h * float64(time.Hour)))
}

// The fixtures are shaped like the catalogue's stats rather than being the
// catalogue's stats. The rates in content.go are explicitly meant to be moved by
// feel, and a suite that pinned them would make every tuning change look like a
// regression — so these hold the SHAPES that matter (a drain that kills, a fill,
// a rate of zero) at numbers chosen to make the arithmetic checkable by eye.
var (
	// statDrains is hp's shape: it falls towards a floor that kills.
	statDrains = Stat{Key: "drains", Min: 0, Max: 100, Start: 100, DecayPerHour: 3, GoodHigh: true, WarnAt: 30, Fatal: true}
	// statFills is bladder's shape: the SAME expression with a negative rate,
	// which is the whole reason there is one subtraction rather than a direction
	// flag and two code paths.
	statFills = Stat{Key: "fills", Min: 0, Max: 100, Start: 0, DecayPerHour: -5, WarnAt: 70}
	// statFrozen only moves when an action moves it — a lifetime counter. Fatal
	// too, so "can it ever reach the floor" is a question about the rate alone.
	statFrozen = Stat{Key: "frozen", Min: 0, Max: 100, Start: 50, DecayPerHour: 0, Fatal: true}
	// statRises is fatal and moves AWAY from its fatal floor, which is the other
	// way a death can be unreachable.
	statRises = Stat{Key: "rises", Min: 0, Max: 100, Start: 50, DecayPerHour: -1, Fatal: true}
	// statChores drains like hp but cannot kill — the shape every stat added
	// after the first one will have. It is the only fixture that separates "is
	// heading for its floor" from "is fatal", which is exactly the distinction
	// DeadAt and Dead both turn on.
	statChores = Stat{Key: "chores", Min: 0, Max: 100, Start: 50, DecayPerHour: 2, WarnAt: 20}
	// statGlacial drains so slowly that the interval to its death does not fit in
	// a time.Duration — about 10^32 hours.
	statGlacial = Stat{Key: "glacial", Min: 0, Max: 100, Start: 100, DecayPerHour: 1e-30, Fatal: true}
)

// nearlyEqual fails unless got is want to a tolerance far below anything the
// game can express. Used where the model's arithmetic is exact but the floating
// point need not be, bit for bit.
func nearlyEqual(t *testing.T, got, want float64, what string) {
	t.Helper()
	const tolerance = 1e-9
	if math.IsNaN(got) || math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %v; want %v", what, got, want)
	}
}

// TestAtDecaysByExactlyRateTimesElapsedHours is the centre of the whole design.
// Nothing ticks, so "how much has changed since" is one multiplication — and if
// that multiplication is wrong then every stat in the game is wrong, in a way no
// other test would notice because there is no second implementation to disagree
// with it.
func TestAtDecaysByExactlyRateTimesElapsedHours(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stat  Stat
		value float64
		now   time.Time
		want  float64
	}{
		{name: "two hours of a drain", stat: statDrains, value: 100, now: hoursAfter(2), want: 94},
		{name: "half an hour of the same drain", stat: statDrains, value: 100, now: hoursAfter(0.5), want: 98.5},
		{name: "a negative rate fills instead of draining", stat: statFills, value: 0, now: hoursAfter(2), want: 10},
		{name: "filling stops at the ceiling", stat: statFills, value: 90, now: hoursAfter(4), want: 100},
		{name: "draining stops at the floor", stat: statDrains, value: 10, now: hoursAfter(5), want: 0},
		{name: "a rate of zero never moves on its own", stat: statFrozen, value: 50, now: hoursAfter(1000), want: 50},
		{name: "no elapsed time is no decay", stat: statDrains, value: 100, now: decayEpoch, want: 100},
		// A host whose clock is corrected backwards must not wind a value back
		// UP: "this was true at a moment that has not arrived" honestly means
		// nothing has decayed yet, and inventing state is the worse answer.
		{name: "a clock that ran backwards leaves the stored value alone", stat: statDrains, value: 40, now: hoursAfter(-3), want: 40},
		{name: "and still clamps a stored value that is out of range", stat: statDrains, value: 150, now: hoursAfter(-1), want: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nearlyEqual(t, tc.stat.At(tc.value, decayEpoch, tc.now), tc.want, "At")
		})
	}
}

// TestClampKeepsAValueInsideItsBounds pins both halves of the guard: the
// ordinary bounds, and the non-finite fallback. The second matters more than it
// looks — see TestAClampedNonFiniteValueStillMarshals for what a NaN reaching
// the response would cost.
func TestClampKeepsAValueInsideItsBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stat  Stat
		value float64
		want  float64
	}{
		{name: "inside the range is untouched", stat: statDrains, value: 42, want: 42},
		{name: "below the floor becomes the floor", stat: statDrains, value: -17, want: 0},
		{name: "above the ceiling becomes the ceiling", stat: statDrains, value: 250, want: 100},
		{name: "the floor itself is inside", stat: statDrains, value: 0, want: 0},
		{name: "the ceiling itself is inside", stat: statDrains, value: 100, want: 100},
		{name: "a NaN falls back to the start", stat: statDrains, value: math.NaN(), want: 100},
		{name: "so does a positive infinity", stat: statFills, value: math.Inf(1), want: 0},
		{name: "and a negative one", stat: statFills, value: math.Inf(-1), want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exact comparison, and an explicit NaN check with it: NaN fails every
			// ordinary comparison silently, so a tolerance-based assertion would
			// pass for exactly the value this test exists to catch.
			got := tc.stat.Clamp(tc.value)
			if math.IsNaN(got) || got != tc.want {
				t.Fatalf("Clamp(%v) = %v; want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestAClampedNonFiniteValueStillMarshals is why the non-finite guard is there
// at all rather than being belt-and-braces. encoding/json refuses to marshal a
// NaN, so one bad row would turn the state endpoint into a 500 for that account
// — every stat gone and the game unplayable — rather than into one odd-looking
// bar. This is the assertion that the fallback actually buys that back.
func TestAClampedNonFiniteValueStillMarshals(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		v := StatValue{Key: statDrains.Key, Value: statDrains.Clamp(bad), AsOf: decayEpoch}
		if _, err := json.Marshal(v); err != nil {
			t.Fatalf("a stat clamped from %v does not marshal: %v", bad, err)
		}
	}
}

// TestDeadAtDerivesTheMomentRatherThanTheMomentSomebodyLooked covers the
// function that lets a death be recorded truthfully by a read arriving hours
// late. It answers a question a wall clock cannot, and every "false" row below
// is a case where answering with a plausible-looking instant would be worse than
// answering with nothing.
func TestDeadAtDerivesTheMomentRatherThanTheMomentSomebodyLooked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stat   Stat
		value  float64
		want   time.Time
		wantOK bool
	}{
		{name: "value over rate hours away", stat: statDrains, value: 90, want: hoursAfter(30), wantOK: true},
		{name: "nearly empty is nearly there", stat: statDrains, value: 6, want: hoursAfter(2), wantOK: true},
		{name: "already at the floor died at as_of", stat: statDrains, value: 0, want: decayEpoch, wantOK: true},
		{name: "below the floor is not a death in the future", stat: statDrains, value: -5, want: decayEpoch, wantOK: true},
		{name: "a stat that cannot kill never reports one", stat: statFills, value: 0},
		{name: "not even one heading straight for its floor", stat: statChores, value: 50},
		{name: "a fatal stat that does not drain never gets there", stat: statFrozen, value: 50},
		{name: "nor does one moving away from its floor", stat: statRises, value: 50},
		// The overflow case. Without the check the conversion wraps to a negative
		// duration and reports a death in the PAST, which the service would then
		// write down — a pet killed by a rate that means "never".
		{name: "a rate so small the interval will not fit in a Duration", stat: statGlacial, value: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.stat.DeadAt(tc.value, decayEpoch)
			if ok != tc.wantOK {
				t.Fatalf("DeadAt(%v) ok = %v; want %v", tc.value, ok, tc.wantOK)
			}
			if !ok {
				if !got.IsZero() {
					t.Fatalf("DeadAt reported no death but handed back %v; a caller that ignores ok would record it", got)
				}
				return
			}
			if !got.Equal(tc.want) {
				t.Fatalf("DeadAt(%v) = %v; want %v", tc.value, got, tc.want)
			}
			// The instant it names has to be the instant the stat is actually at
			// its floor, or the death would be recorded at a moment the pet was
			// still alive by the game's own arithmetic.
			if v := tc.stat.At(tc.value, decayEpoch, got); v > tc.stat.Min {
				t.Fatalf("at the derived instant the stat still reads %v, above its floor %v", v, tc.stat.Min)
			}
		})
	}
}

// TestDeadIsTrueOnlyForAFatalStatAtItsFloor is the read-time half of the same
// rule. A stat sitting on a boundary is the interesting case in both directions:
// a fatal one at its floor is a death, and a non-fatal one at either bound is
// emphatically not.
func TestDeadIsTrueOnlyForAFatalStatAtItsFloor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stat  Stat
		value float64
		now   time.Time
		want  bool
	}{
		{name: "hours left to live", stat: statDrains, value: 100, now: hoursAfter(33), want: false},
		{name: "an hour later, gone", stat: statDrains, value: 100, now: hoursAfter(34), want: true},
		{name: "exactly at the floor counts as dead", stat: statDrains, value: 6, now: hoursAfter(2), want: true},
		{name: "a non-fatal stat on its floor is not a death", stat: statFills, value: 0, now: decayEpoch, want: false},
		{name: "nor is one that drained all the way to it", stat: statChores, value: 50, now: hoursAfter(30), want: false},
		{name: "nor is one pinned at its ceiling", stat: statFills, value: 100, now: hoursAfter(10), want: false},
		{name: "a fatal stat that does not drain lives forever", stat: statFrozen, value: 50, now: hoursAfter(1000), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.stat.Dead(tc.value, decayEpoch, tc.now); got != tc.want {
				t.Fatalf("Dead(%v, +%v) = %v; want %v", tc.value, tc.now.Sub(decayEpoch), got, tc.want)
			}
		})
	}
}

// TestSplittingTheIntervalChangesNothing is the property the entire no-tick
// design rests on, and the one worth guarding hardest.
//
// Because the decay is linear, evaluating it at an instant produces precisely
// what a continuous simulation would have produced — so a player who was away is
// in exactly the state a player who was watching would be in, and there is
// nothing to gain by choosing when to look. The moment a rate stops being a
// constant (compounding, or one stat draining another) the closed form becomes
// an approximation and this test is what will say so.
func TestSplittingTheIntervalChangesNothing(t *testing.T) {
	const total = 4.0
	for _, st := range []Stat{statDrains, statFills} {
		for _, split := range []float64{0.25, 1, 2.5, 3.999} {
			t.Run(fmt.Sprintf("%s split at %vh", st.Key, split), func(t *testing.T) {
				oneStep := st.At(st.Start, decayEpoch, hoursAfter(total))
				// The re-stamp an action performs: read, write the value down
				// against a new as_of, carry on decaying from there.
				mid := st.At(st.Start, decayEpoch, hoursAfter(split))
				twoSteps := st.At(mid, hoursAfter(split), hoursAfter(total))
				nearlyEqual(t, twoSteps, oneStep, "decayed in two steps")
			})
		}

		// And it does not drift over many of them. Somebody who opens the app
		// every hour re-stamps nothing, but the same shape appears whenever an
		// action does — and twenty-four of those must land where a single
		// day-long read lands, or playing often would be a different game from
		// playing rarely.
		t.Run(st.Key+" over 24 hourly steps", func(t *testing.T) {
			v := st.Start
			for h := 1; h <= 24; h++ {
				v = st.At(v, hoursAfter(float64(h-1)), hoursAfter(float64(h)))
			}
			nearlyEqual(t, v, st.At(st.Start, decayEpoch, hoursAfter(24)), "24 hourly steps")
		})
	}
}
