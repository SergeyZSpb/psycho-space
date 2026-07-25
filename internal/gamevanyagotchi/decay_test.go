package gamevanyagotchi

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

// The decay is a handful of pure functions over a stored (value, as_of) pair,
// which is why every property below is reachable without a clock, a database or
// a service. Nothing in this file reads time.Now() or sleeps: every instant is
// derived from decayEpoch, so a failure means the arithmetic is wrong and never
// that the machine was busy.
//
// The file has two halves. The first is the UNCOUPLED decay — one rate, one
// subtraction — and it is the arithmetic every stat in the game rests on. The
// second is the COUPLING: health drains faster while the beer is empty and
// faster while the bladder is full, which makes hp's rate a function of other
// decaying values. That is precisely the shape which usually turns a closed form
// into an approximation, and the claim these tests defend is that here it does
// NOT: the value and the moment of death are computed exactly, not estimated.
// TestTheCoupledDecayIsExactRatherThanApproximate is the one that would catch
// somebody "simplifying" the onset arithmetic into something plausible.

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
	// A coupled row too, because Dead now takes the drivers: a stat that would
	// still be alive on its base rate alone is dead once the penalty is counted,
	// and a Dead that ignored its drivers would report the first of those.
	beer := fallingDriver(t, 5)
	coupled := coupledStat(1, beer.penalty)
	drivers := driversAt(beer.row)
	// Enough left to survive the base drain over the window, and not enough to
	// survive the penalty as well. Stated as a fixture check rather than as a
	// computed expectation, so a retuned catalogue turns this case into a loud
	// failure instead of quietly asserting nothing.
	const window = 20.0
	survivesBase := coupled.Min + coupled.DecayPerHour*window + 1
	killedByPenalty := coupled.DecayPerHour*window + beer.penalty.RatePerHour*(window-beer.onset)
	if beer.onset >= window || killedByPenalty <= survivesBase-coupled.Min {
		t.Fatalf("the fixture no longer separates the base drain from the coupled one: onset %vh, window %vh, "+
			"%v of drain against %v of value", beer.onset, window, killedByPenalty, survivesBase-coupled.Min)
	}

	for _, tc := range []struct {
		name    string
		stat    Stat
		value   float64
		now     time.Time
		drivers map[string]StatRow
		want    bool
	}{
		{name: "hours left to live", stat: statDrains, value: 100, now: hoursAfter(33), want: false},
		{name: "an hour later, gone", stat: statDrains, value: 100, now: hoursAfter(34), want: true},
		{name: "exactly at the floor counts as dead", stat: statDrains, value: 6, now: hoursAfter(2), want: true},
		{name: "a non-fatal stat on its floor is not a death", stat: statFills, value: 0, now: decayEpoch, want: false},
		{name: "nor is one that drained all the way to it", stat: statChores, value: 50, now: hoursAfter(30), want: false},
		{name: "nor is one pinned at its ceiling", stat: statFills, value: 100, now: hoursAfter(10), want: false},
		{name: "a fatal stat that does not drain lives forever", stat: statFrozen, value: 50, now: hoursAfter(1000), want: false},
		{
			name: "the base drain alone would have left him alive", stat: coupled,
			value: survivesBase, now: hoursAfter(window), drivers: nil, want: false,
		},
		{
			name: "an unmet need is what actually kills him", stat: coupled,
			value: survivesBase, now: hoursAfter(window), drivers: drivers, want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.stat.Dead(tc.value, decayEpoch, tc.now, tc.drivers); got != tc.want {
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

// ---------------------------------------------------------------------------
// The coupling.
//
// Health is not a chore of its own: it falls faster while the beer is empty and
// faster while the bladder is full, which makes its rate a function of two other
// decaying values. Everything below defends the claim that this stays EXACT — a
// player who was away is in precisely the state a player who was watching would
// be in — rather than becoming an estimate whose error sign decides whether
// being absent beats playing.
//
// One constraint shapes every fixture here. Penalty.onset resolves its driver's
// definition through StatByKey, against the real catalogue, so a driver must be
// a genuine catalogue key: a made-up one is evaluated as "no penalty at all",
// and a suite built on invented drivers would pass while asserting nothing. The
// helpers below therefore borrow the catalogue's own falling and rising stats
// and derive every threshold and onset from their shipped rates, so a tuning
// change moves the numbers without invalidating the properties.
// ---------------------------------------------------------------------------

// coupledDriver is one driver fixture: the stored row, the penalty it powers,
// and the hour — counted from decayEpoch — at which that penalty switches on.
type coupledDriver struct {
	row     StatRow
	penalty Penalty
	onset   float64
}

// fallingDriver builds a driver that starts above a low threshold and falls past
// it, so its penalty switches on part-way through a window rather than at the
// start of one. That is the interesting case: a penalty which was already on is
// indistinguishable from a bigger base rate.
func fallingDriver(t *testing.T, ratePerHour float64) coupledDriver {
	t.Helper()
	return driverFixture(t, StatBeer, 0.44, 0.20, false, ratePerHour)
}

// risingDriver is the same shape mirrored: a stat that FILLS towards a high
// threshold. It exists because one signed rate is meant to cover both
// directions, so a sign error in the onset arithmetic has to fail somewhere.
func risingDriver(t *testing.T, ratePerHour float64) coupledDriver {
	t.Helper()
	return driverFixture(t, StatBladder, 0.10, 0.80, true, ratePerHour)
}

// driverFixture places a driver at startFrac of its range with a threshold at
// thresholdFrac of it, and derives the hour at which it crosses.
//
// Fractions rather than literals so the fixture survives a retune: the onset
// moves with the catalogue's rate and every expectation is computed from it.
// What it will NOT survive is the driver reversing direction, which is checked
// here so that failure reads as "the catalogue changed shape" rather than as a
// wrong number three tests away.
func driverFixture(t *testing.T, key string, startFrac, thresholdFrac float64, above bool, ratePerHour float64) coupledDriver {
	t.Helper()
	def := mustStat(t, key)
	span := def.Max - def.Min
	start, threshold := def.Min+span*startFrac, def.Min+span*thresholdFrac
	if above && def.DecayPerHour >= 0 {
		t.Fatalf("%q moves at %v/hour and can never rise to %v; this fixture needs a filling driver", key, def.DecayPerHour, threshold)
	}
	if !above && def.DecayPerHour <= 0 {
		t.Fatalf("%q moves at %v/hour and can never fall to %v; this fixture needs a draining driver", key, def.DecayPerHour, threshold)
	}
	onset := (start - threshold) / def.DecayPerHour
	if onset <= 0 {
		t.Fatalf("%q starts at %v, already past the threshold %v; this fixture needs a penalty that switches on later", key, start, threshold)
	}
	return coupledDriver{
		row:     StatRow{Key: key, Value: start, AsOf: decayEpoch},
		penalty: Penalty{WhenKey: key, Threshold: threshold, Above: above, RatePerHour: ratePerHour},
		onset:   onset,
	}
}

// coupledStat is hp's shape: it barely rots on its own, and what actually kills
// it is other stats sitting in a bad range.
func coupledStat(base float64, penalties ...Penalty) Stat {
	return Stat{
		Key: "coupled", Min: 0, Max: 100, Start: 100,
		DecayPerHour: base, GoodHigh: true, WarnAt: 30, Fatal: true,
		Penalties: penalties,
	}
}

// driversAt collects rows into the map the coupled functions take.
func driversAt(rows ...StatRow) map[string]StatRow {
	m := make(map[string]StatRow, len(rows))
	for _, r := range rows {
		m[r.Key] = r
	}
	return m
}

// restamped is what an action does to the driver rows: read each one at `at`,
// write it back down against that instant, and carry on decaying from there.
//
// Read with At and never AtWith, which is the same rule the production read path
// follows: a driver is never itself penalised, so applying coupling to one would
// invent a feedback term the closed form does not have.
func restamped(t *testing.T, drivers map[string]StatRow, at time.Time) map[string]StatRow {
	t.Helper()
	out := make(map[string]StatRow, len(drivers))
	for k, r := range drivers {
		def, ok := StatByKey(k)
		if !ok {
			t.Fatalf("driver %q is not in the catalogue; the fixture is asserting nothing", k)
		}
		out[k] = StatRow{Key: k, Value: def.At(r.Value, r.AsOf, at), AsOf: at}
	}
	return out
}

// TestAPenaltyAppliesForASuffixOfTheWindowAndNeverForLess is the onset rule,
// observed through the two functions that expose it.
//
// A single instant describes a penalty completely — before it, nothing; from it
// onwards, the extra drain — and that is only sound while a driver moves towards
// its threshold monotonically or away from it forever. The four "never" rows
// matter as much as the two that fire: a penalty applied to a driver that is
// heading away, or to one the catalogue cannot resolve, would be damage invented
// out of a value nobody can reconstruct.
func TestAPenaltyAppliesForASuffixOfTheWindowAndNeverForLess(t *testing.T) {
	const (
		base    = 1.0
		penalty = 4.0
		// Past every onset below (the latest is 14h), and short enough that the
		// worst case — a penalty on from the first instant — still lands well
		// inside the stat's range. A window that clamped would be testing the
		// bounds rather than the coupling, which the guard at the end enforces.
		window = 16.0
	)
	beerDef, bladderDef := mustStat(t, StatBeer), mustStat(t, StatBladder)
	falling, rising := fallingDriver(t, penalty), risingDriver(t, penalty)

	// A driver that is already past its threshold when the window opens: the
	// penalty is on from the very first instant, with no crossing to solve for.
	alreadyEmpty := StatRow{Key: StatBeer, Value: falling.penalty.Threshold - 1, AsOf: decayEpoch}

	for _, tc := range []struct {
		name    string
		penalty Penalty
		drivers map[string]StatRow
		onset   float64
		applies bool
	}{
		{
			name:    "a driver already past its threshold penalises from the first instant",
			penalty: falling.penalty, drivers: driversAt(alreadyEmpty), onset: 0, applies: true,
		},
		{
			name:    "a driver heading for its threshold penalises from the exact crossing",
			penalty: falling.penalty, drivers: driversAt(falling.row), onset: falling.onset, applies: true,
		},
		{
			name:    "and the same holds for one that fills towards a high threshold",
			penalty: rising.penalty, drivers: driversAt(rising.row), onset: rising.onset, applies: true,
		},
		{
			// The catalogue has no stat with a rate of zero, so "static" cannot be
			// built as a driver at all — and it would take this same branch if it
			// could: the guard is `>= 0`, which is the one a static driver hits.
			name: "a driver heading away from its threshold never penalises",
			penalty: Penalty{
				WhenKey: StatBeer, Threshold: beerDef.Max - 1, Above: true, RatePerHour: penalty,
			},
			drivers: driversAt(StatRow{Key: StatBeer, Value: beerDef.Max - 10, AsOf: decayEpoch}),
		},
		{
			name: "nor does one filling away from a floor it has already left",
			penalty: Penalty{
				WhenKey: StatBladder, Threshold: bladderDef.Min + 1, Above: false, RatePerHour: penalty,
			},
			drivers: driversAt(StatRow{Key: StatBladder, Value: bladderDef.Min + 10, AsOf: decayEpoch}),
		},
		{
			name:    "a driver the caller did not pass penalises nothing",
			penalty: falling.penalty, drivers: driversAt(rising.row),
		},
		{
			name:    "and neither does one the caller passed but the catalogue does not define",
			penalty: Penalty{WhenKey: "mood", Threshold: 50, Above: false, RatePerHour: penalty},
			drivers: driversAt(StatRow{Key: "mood", Value: 0, AsOf: decayEpoch}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := coupledStat(base, tc.penalty)

			if !tc.applies {
				// Nothing, anywhere: not at the start, not deep into the window.
				for _, h := range []float64{0, window / 2, window, 10 * window} {
					nearlyEqual(t, st.RateAt(decayEpoch, hoursAfter(h), tc.drivers), base,
						fmt.Sprintf("the rate %vh in", h))
					nearlyEqual(t, st.AtWith(st.Start, decayEpoch, hoursAfter(h), tc.drivers),
						st.At(st.Start, decayEpoch, hoursAfter(h)), fmt.Sprintf("the value %vh in", h))
				}
				return
			}

			// A second either side of the crossing. A second is far finer than any
			// bug worth catching and far coarser than the nanosecond the instant is
			// actually computed to, so this pins the onset without being a test of
			// floating-point rounding.
			const second = 1.0 / 3600
			if tc.onset > 0 {
				nearlyEqual(t, st.RateAt(decayEpoch, hoursAfter(tc.onset-second), tc.drivers), base,
					"the rate a second before the crossing")
			} else {
				// An onset of zero is the window's own start, which is exact.
				nearlyEqual(t, st.RateAt(decayEpoch, decayEpoch, tc.drivers), base+tc.penalty.RatePerHour,
					"the rate at the instant the window opens")
			}
			nearlyEqual(t, st.RateAt(decayEpoch, hoursAfter(tc.onset+second), tc.drivers), base+tc.penalty.RatePerHour,
				"the rate a second after the crossing")

			// And the accrued damage is the penalty over the suffix, not over the
			// window — the difference between the two is the whole point.
			got := st.AtWith(st.Start, decayEpoch, hoursAfter(window), tc.drivers)
			want := st.Start - base*window - tc.penalty.RatePerHour*(window-tc.onset)
			// Checked BEFORE the comparison, so that a retune which pushes the
			// fixture out of range fails saying so, rather than as a baffling
			// "want -50" from a stat whose floor is zero.
			if want <= st.Min || want >= st.Max {
				t.Fatalf("the fixture clamps at %v, so this case would test the bounds rather than the coupling", want)
			}
			nearlyEqual(t, got, want, fmt.Sprintf("the value after %vh with the penalty on from %vh", window, tc.onset))
		})
	}
}

// TestAtWithIsAtWhenThereIsNothingToCoupleTo pins the two ways the coupled and
// uncoupled paths must agree.
//
// They are separate functions on purpose — a driver's own trajectory is read
// with At, because a driver is never itself penalised — so "the coupled one
// reduces to the plain one when there is no coupling" is the property that keeps
// them from drifting into two different decays.
func TestAtWithIsAtWhenThereIsNothingToCoupleTo(t *testing.T) {
	falling := fallingDriver(t, 6)
	plain := coupledStat(2)                    // no penalties at all
	coupled := coupledStat(2, falling.penalty) // penalties, but nothing to evaluate them against

	for _, h := range []float64{0, 0.5, 7, 40} {
		now := hoursAfter(h)
		nearlyEqual(t, plain.AtWith(plain.Start, decayEpoch, now, driversAt(falling.row)),
			plain.At(plain.Start, decayEpoch, now), fmt.Sprintf("a stat with no penalties after %vh", h))
		nearlyEqual(t, coupled.AtWith(coupled.Start, decayEpoch, now, nil),
			coupled.At(coupled.Start, decayEpoch, now), fmt.Sprintf("a penalised stat with no drivers after %vh", h))
	}
}

// TestTheCoupledDecayIsExactRatherThanApproximate is the centre of the coupling,
// and the test that earns the design its right to exist.
//
// Evaluating a whole window in one shot must produce precisely what a continuous
// simulation would have produced — otherwise a player who was away is in a
// different state from one who was watching, and the sign of the difference
// decides whether the winning move is to stop playing. The comparison is against
// a step simulation that re-stamps every row as it advances, which is exactly
// what a stream of actions does to a real pet. An implementation that applied a
// penalty to the whole window whenever it holds at the end, or from the start
// whenever it holds anywhere, would pass every other test in this file and fail
// this one by a wide margin.
func TestTheCoupledDecayIsExactRatherThanApproximate(t *testing.T) {
	const (
		base  = 0.5
		steps = 2400
		// Deliberately not a whole number of minutes and not a divisor of any
		// onset, so the crossings fall INSIDE steps rather than on their edges —
		// the partial-step case is the one an approximation gets wrong.
		step = 37 * time.Second
	)
	falling, rising := fallingDriver(t, 2), risingDriver(t, 3)
	st := coupledStat(base, falling.penalty, rising.penalty)
	drivers := driversAt(falling.row, rising.row)

	window := time.Duration(steps) * step
	hours := window.Hours()
	if falling.onset >= hours || rising.onset >= hours || falling.onset == rising.onset {
		t.Fatalf("the window of %vh does not contain both onsets (%vh, %vh) as distinct instants",
			hours, falling.onset, rising.onset)
	}

	// One shot: the whole window evaluated from the stored pair.
	oneShot := st.AtWith(st.Start, decayEpoch, decayEpoch.Add(window), drivers)

	// Many steps: read, write down, carry on — every row re-stamped together, the
	// way an action writes them.
	v, cur, sim := st.Start, decayEpoch, drivers
	for i := range steps {
		next := cur.Add(step)
		v = st.AtWith(v, cur, next, sim)
		sim = restamped(t, sim, next)
		cur = next
		if v <= st.Min || v >= st.Max {
			t.Fatalf("the simulated value hit a bound (%v) at step %d; the fixture is testing the clamp rather than the coupling", v, i)
		}
	}

	nearlyEqual(t, oneShot, v, fmt.Sprintf("%d steps of %s against one window of %s", steps, step, window))

	// And both agree with the closed form written out by hand, so a shared
	// mistake in the two paths above cannot pass unnoticed.
	want := st.Start - base*hours -
		falling.penalty.RatePerHour*(hours-falling.onset) -
		rising.penalty.RatePerHour*(hours-rising.onset)
	nearlyEqual(t, oneShot, want, "the value after the whole window")
}

// TestSplittingTheIntervalChangesNothingWithPenaltiesActive extends the
// no-tick property to the coupled case.
//
// Splitting a window is what an action does, and the re-stamp it performs writes
// every row at one instant. Do that either side of a penalty's onset and the
// result must be the value the unsplit window would have given — which is the
// same claim as the exactness test above, reached from the direction that
// actually happens in production. The split falling exactly ON an onset is the
// row that catches an off-by-one in whether the crossing instant is counted in
// the segment before it or the one after.
func TestSplittingTheIntervalChangesNothingWithPenaltiesActive(t *testing.T) {
	const (
		base  = 1.0
		total = 24.0
	)
	falling, rising := fallingDriver(t, 2), risingDriver(t, 3)
	st := coupledStat(base, falling.penalty, rising.penalty)
	drivers := driversAt(falling.row, rising.row)

	oneStep := st.AtWith(st.Start, decayEpoch, hoursAfter(total), drivers)

	for _, split := range []float64{
		falling.onset / 2,                     // before either penalty is on
		falling.onset,                         // exactly on the first onset
		(falling.onset + rising.onset) / 2,    // between the two
		rising.onset,                          // exactly on the second
		rising.onset + (total-rising.onset)/2, // with both already on
	} {
		t.Run(fmt.Sprintf("split at %.4gh", split), func(t *testing.T) {
			if split <= 0 || split >= total {
				t.Fatalf("the split at %vh is outside the window; the fixture no longer covers what it claims", split)
			}
			at := hoursAfter(split)
			mid := st.AtWith(st.Start, decayEpoch, at, drivers)
			twoSteps := st.AtWith(mid, at, hoursAfter(total), restamped(t, drivers, at))
			nearlyEqual(t, twoSteps, oneStep, "decayed in two steps across an onset")
		})
	}
}

// TestRateAtStepsUpOnceForEachPenaltyThatHasSwitchedOn covers the number the
// client is trusted with.
//
// The rate is sent so a bar can keep creeping between fetches without the
// browser owning a second copy of the coupling — the mistake that would turn one
// piece of arithmetic into two nobody keeps honest. It therefore has to be the
// rate the server is actually applying at that instant: base before anything has
// fired, base plus one after the first onset, base plus both after the second.
func TestRateAtStepsUpOnceForEachPenaltyThatHasSwitchedOn(t *testing.T) {
	const base = 1.0
	falling, rising := fallingDriver(t, 2), risingDriver(t, 3)
	st := coupledStat(base, falling.penalty, rising.penalty)
	drivers := driversAt(falling.row, rising.row)

	first, second := falling, rising
	if second.onset < first.onset {
		first, second = second, first
	}
	if first.onset == second.onset {
		t.Fatalf("both penalties switch on at %vh, so this test cannot tell one step from two", first.onset)
	}

	for _, tc := range []struct {
		name string
		at   float64
		want float64
	}{
		{name: "before anything has gone wrong", at: 0, want: base},
		{name: "still nothing, a moment before the first onset", at: first.onset - 1.0/3600, want: base},
		{name: "one need unmet", at: first.onset + 1.0/3600, want: base + first.penalty.RatePerHour},
		{name: "still one, just before the second", at: second.onset - 1.0/3600, want: base + first.penalty.RatePerHour},
		{name: "both unmet", at: second.onset + 1.0/3600, want: base + first.penalty.RatePerHour + second.penalty.RatePerHour},
		{name: "and it does not keep climbing afterwards", at: second.onset * 10, want: base + first.penalty.RatePerHour + second.penalty.RatePerHour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nearlyEqual(t, st.RateAt(decayEpoch, hoursAfter(tc.at), drivers), tc.want,
				fmt.Sprintf("the rate %.4gh in", tc.at))
		})
	}
}

// TestDeadAtWithWalksThePiecewiseDrainToTheExactInstant is the death half of the
// exactness claim.
//
// With penalties the value is piecewise-linear rather than linear, so the moment
// it reaches the floor is no longer one division: it is the segment the floor
// falls inside. Getting it wrong is not a rounding error — the recorded moment of
// death is written to the database by whichever read observes it, and every
// reader has to derive the identical instant or the write race stops being
// harmless. Each expectation below is worked out from first principles in the
// test rather than copied from the implementation, which is the only way this
// test can disagree with it.
func TestDeadAtWithWalksThePiecewiseDrainToTheExactInstant(t *testing.T) {
	const base = 1.0
	falling, rising := fallingDriver(t, 2), risingDriver(t, 3)
	drivers := driversAt(falling.row, rising.row)

	first, second := falling, rising
	if second.onset < first.onset {
		first, second = second, first
	}

	t.Run("the drain steps up once before he reaches the floor", func(t *testing.T) {
		st := coupledStat(base, first.penalty)
		// Enough to outlive the base drain up to the onset, and not much more.
		value := base*first.onset + 14
		left := value - st.Min - base*first.onset
		if left <= 0 {
			t.Fatalf("the fixture kills him before the onset at %vh; nothing steps up", first.onset)
		}
		want := hoursAfter(first.onset + left/(base+first.penalty.RatePerHour))

		got, ok := st.DeadAtWith(value, decayEpoch, drivers)
		if !ok {
			t.Fatal("no death reported for a stat that is draining towards its floor")
		}
		assertInstant(t, got, want, "the death after one rate change")
	})

	t.Run("and twice when both needs go unmet", func(t *testing.T) {
		st := coupledStat(base, first.penalty, second.penalty)
		rateAfterFirst := base + first.penalty.RatePerHour
		rateAfterSecond := rateAfterFirst + second.penalty.RatePerHour
		// Enough to survive both segments and die in the third.
		value := base*first.onset + rateAfterFirst*(second.onset-first.onset) + 13

		left := value - st.Min - base*first.onset
		if left <= 0 {
			t.Fatalf("the fixture kills him before the first onset at %vh", first.onset)
		}
		left -= rateAfterFirst * (second.onset - first.onset)
		if left <= 0 {
			t.Fatalf("the fixture kills him before the second onset at %vh; only one rate change is exercised", second.onset)
		}
		want := hoursAfter(second.onset + left/rateAfterSecond)

		got, ok := st.DeadAtWith(value, decayEpoch, drivers)
		if !ok {
			t.Fatal("no death reported for a stat that is draining towards its floor")
		}
		assertInstant(t, got, want, "the death after two rate changes")
	})

	t.Run("a death before the first onset is the plain base-rate division", func(t *testing.T) {
		st := coupledStat(base, first.penalty, second.penalty)
		// Half the value it would take to reach the first onset alive.
		value := st.Min + base*first.onset/2
		want := hoursAfter((value - st.Min) / base)
		if !want.Before(hoursAfter(first.onset)) {
			t.Fatalf("the fixture's death at %v is not before the first onset at %vh", want, first.onset)
		}

		got, ok := st.DeadAtWith(value, decayEpoch, drivers)
		if !ok {
			t.Fatal("no death reported for a stat that reaches its floor before anything else goes wrong")
		}
		assertInstant(t, got, want, "the death before any onset")
		// The penalties must not have been counted early: with them the instant
		// would be sooner, which is a death recorded before it happened.
		if !got.Equal(want) && got.Before(want) {
			t.Fatalf("death at %v is earlier than the base rate alone allows (%v)", got, want)
		}
	})

	t.Run("a stat whose every rate is at most zero never reaches its floor", func(t *testing.T) {
		// It rises on its own, and the one thing that hurts it exactly cancels
		// that out. Nothing after the onset is falling, so there is no instant to
		// report and reporting one anyway would kill a pet that is fine.
		rise := Penalty{
			WhenKey:     first.penalty.WhenKey,
			Threshold:   first.penalty.Threshold,
			Above:       first.penalty.Above,
			RatePerHour: 1,
		}
		st := coupledStat(-1, rise)
		got, ok := st.DeadAtWith(50, decayEpoch, drivers)
		if ok {
			t.Fatalf("DeadAtWith reported a death at %v for a stat that never falls", got)
		}
		if !got.IsZero() {
			t.Fatalf("DeadAtWith reported no death but handed back %v; a caller that ignores ok would record it", got)
		}
	})
}

// assertInstant fails unless got is want to within a second, which is finer than
// any behaviour the game expresses and coarser than the nanosecond truncation an
// exact instant is converted through.
func assertInstant(t *testing.T, got, want time.Time, what string) {
	t.Helper()
	if d := got.Sub(want); d > time.Second || d < -time.Second {
		t.Fatalf("%s = %v; want %v (off by %v)", what, got, want, d)
	}
}

// TestTheDerivedDeathIsTheInstantTheValueActuallyReachesTheFloor is the
// consistency check between the two coupled functions.
//
// They are separate derivations of the same piecewise line — one integrates it,
// the other solves it for a floor — so nothing but a test makes them agree. If
// they disagree, a pet is recorded as having died at a moment when the game's
// own arithmetic says he still had health, and the screen and the record tell
// different stories.
func TestTheDerivedDeathIsTheInstantTheValueActuallyReachesTheFloor(t *testing.T) {
	const base = 1.0
	falling, rising := fallingDriver(t, 2), risingDriver(t, 3)
	drivers := driversAt(falling.row, rising.row)

	for _, tc := range []struct {
		name  string
		stat  Stat
		value float64
	}{
		{name: "across one rate change", stat: coupledStat(base, falling.penalty), value: 40},
		{name: "across two", stat: coupledStat(base, falling.penalty, rising.penalty), value: 70},
		{name: "with no coupling at all", stat: coupledStat(base), value: 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, ok := tc.stat.DeadAtWith(tc.value, decayEpoch, drivers)
			if !ok {
				t.Fatal("no death reported for a stat draining towards its floor")
			}
			if !at.After(decayEpoch) {
				t.Fatalf("the death at %v is not in the future of the stored instant %v", at, decayEpoch)
			}

			// At the instant itself: on the floor.
			nearlyEqual(t, tc.stat.AtWith(tc.value, decayEpoch, at, drivers), tc.stat.Min,
				"the value at the derived instant")
			// A minute earlier: still alive. Without this the test would pass for
			// an implementation that reported a death far too early, since the
			// value is clamped at the floor for ever afterwards.
			if v := tc.stat.AtWith(tc.value, decayEpoch, at.Add(-time.Minute), drivers); v <= tc.stat.Min {
				t.Fatalf("a minute before the derived death the value is already %v; the instant is too late", v)
			}
			// And a second later he is dead by the read path's own reckoning, so
			// the two agree about the record as well as about the number.
			if !tc.stat.Dead(tc.value, decayEpoch, at.Add(time.Second), drivers) {
				t.Fatal("Dead is false a second after the instant DeadAtWith derived")
			}
		})
	}
}
