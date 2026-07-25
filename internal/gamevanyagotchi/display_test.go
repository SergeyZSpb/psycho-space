package gamevanyagotchi

import (
	"testing"
	"time"
)

// The pose: the one thing about an entity on the plane that is worked out
// rather than stored.
//
// It is a pure function of a cached entry and an instant, so it is driven
// directly here rather than through a broadcast — every rejection and every
// verdict is a table row instead of something that needs a transport to reach.
//
// The last test in this file is the one that matters most. What the cache holds
// is the raw (value, as_of) pairs and NOT the pose, so that a cached entry stays
// correct as time passes; if somebody ever "optimises" that by caching the pose
// itself, every test above still passes and only that one fails.

// cached builds a display entry holding one row per named stat, all stamped at
// asOf.
//
// Stamped together on purpose: the coupled decay is only defined while every
// pair shares one instant, which is the same invariant every write in this game
// upholds. A case that wants no decay at all passes the instant it evaluates at.
func cached(asOf time.Time, values map[string]float64) display {
	stats := make(map[string]StatRow, len(values))
	for key, v := range values {
		stats[key] = StatRow{Key: key, Value: v, AsOf: asOf}
	}
	return display{skinKey: SkinVanya, stats: stats}
}

// afterHours offsets an instant by a fractional number of hours, which is the
// unit every rate in the catalogue is expressed in.
func afterHours(t time.Time, h float64) time.Time {
	return t.Add(time.Duration(h * float64(time.Hour)))
}

// TestThePoseIsDecidedByTheFatalStatAndByARecordedDeath is the derivation in
// one table.
//
// Every value is expressed against the catalogue's own thresholds rather than
// written down, because the rates and bounds in content.go are meant to be moved
// by feel — a case pinned to the number 30 would report every retune as a
// regression. Each row is evaluated at the instant its pairs were stamped, so
// nothing has decayed and the row is about the verdict alone.
func TestThePoseIsDecidedByTheFatalStatAndByARecordedDeath(t *testing.T) {
	hp := mustStat(t, StatHP)
	beer := mustStat(t, StatBeer)
	bladder := mustStat(t, StatBladder)
	if !hp.Fatal {
		t.Fatalf("%q is no longer fatal; this table is reasoning about a stat that can kill him", hp.Key)
	}
	if beer.Fatal || bladder.Fatal {
		t.Fatal("a need has become fatal; the case below about a non-fatal stat in trouble no longer says anything")
	}

	// Mid-way into and mid-way clear of the warning range, so both stay well
	// away from the boundary and from the floor whatever the catalogue says.
	troubled := (hp.Min + hp.WarnAt) / 2
	healthy := (hp.WarnAt + hp.Max) / 2
	if !hp.Troubled(troubled) || hp.Troubled(healthy) || troubled <= hp.Min {
		t.Fatalf("with the current catalogue %v is not inside the warning range and %v is not clear of it; the table below would prove nothing",
			troubled, healthy)
	}
	// The two needs at the ends that read as trouble — an empty beer and a full
	// bladder — which is what the "a need in trouble is not a pose" case needs.
	if !beer.Troubled(beer.Min) || !bladder.Troubled(bladder.Max) {
		t.Fatal("neither need reads as trouble at the end of its own scale; the case below cannot say a non-fatal stat is ignored")
	}

	died := epoch.Add(-time.Hour)

	for _, tc := range []struct {
		name  string
		entry display
		want  string
		// why is the reason this row exists, printed on failure so a broken
		// case reads as a broken property rather than as a wrong string.
		why string
	}{
		{
			name:  "a recorded death outranks the numbers",
			entry: withDeath(cached(epoch, map[string]float64{StatHP: hp.Max}), died),
			want:  PoseDead,
			why:   "a pet whose death has been written down is dead however healthy the stored pairs look — the record is the fact",
		},
		{
			name:  "a fatal stat resting on its floor is dead before anybody writes it down",
			entry: cached(epoch, map[string]float64{StatHP: hp.Min}),
			want:  PoseDead,
			why:   "recording a death is a write, and writes belong to the read path; the plane must not wait for a database row to tell the truth",
		},
		{
			name:  "a fatal stat inside its warning range is looking rough",
			entry: cached(epoch, map[string]float64{StatHP: troubled}),
			want:  PosePoorly,
			why:   "the pose uses the catalogue's own warning threshold, so an amber bar and a rough-looking Ваня are one moment rather than two numbers that drift apart",
		},
		{
			name:  "a fatal stat clear of its warning range is fine",
			entry: cached(epoch, map[string]float64{StatHP: healthy}),
			want:  PoseFine,
			why:   "the ordinary case, and the one every other row has to be distinguishable from",
		},
		{
			name: "a need in trouble is not a pose",
			entry: cached(epoch, map[string]float64{
				StatHP: healthy, StatBeer: beer.Min, StatBladder: bladder.Max,
			}),
			want: PoseFine,
			why:  "only a FATAL stat decides how he is drawn; if any stat in trouble did, every stat added later would silently change how he looks",
		},
		{
			name:  "an account with nothing cached at all is fine",
			entry: display{},
			want:  PoseFine,
			why:   "a player whose pet has never been read still has to be drawn, and drawing them as dying would be a lie about somebody who has just arrived",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.pose(epoch); got != tc.want {
				t.Fatalf("pose = %q, want %q — %s", got, tc.want, tc.why)
			}
		})
	}
}

// withDeath records a death on a cache entry.
func withDeath(d display, at time.Time) display {
	d.diedAt = &at
	return d
}

// TestAPoseCachedOnceGoesOnGettingWorseByItself is the property that makes
// caching the PAIRS rather than the pose correct, and it is the one that would
// rot silently.
//
// The cache is filled when a client says hello and when its owner acts over
// HTTP, and a socket left open all afternoon sees neither. A pose frozen at the
// moment it was cached would therefore show a healthy Ваня who had been dying
// since lunchtime — and nothing would notice, because every frame would keep
// arriving and keep looking plausible. Deriving it on each tick costs one
// subtraction per stat and stays correct indefinitely with nobody refreshing
// anything.
//
// ONLY the fatal stat is cached here. That is deliberate: with no drivers in the
// entry no penalty can switch on, so the fall is the catalogue's base rate alone
// and each instant below is a single division. The coupled arithmetic has its
// own tests in decay_test.go; what is under test here is that the POSE moves
// with the clock at all.
func TestAPoseCachedOnceGoesOnGettingWorseByItself(t *testing.T) {
	hp := mustStat(t, StatHP)
	if hp.DecayPerHour <= 0 {
		t.Fatalf("%q no longer falls on its own (%v an hour); nothing here would ever get worse", hp.Key, hp.DecayPerHour)
	}
	entry := cached(epoch, map[string]float64{StatHP: hp.Max})

	// Derived from the catalogue, and derived here rather than asked of the
	// decay engine: an expectation the implementation computed would agree with
	// it whatever it did.
	toWarning := (hp.Max - hp.WarnAt) / hp.DecayPerHour
	toFloor := (hp.Max - hp.Min) / hp.DecayPerHour
	if !(toWarning > 0 && toFloor > toWarning) {
		t.Fatalf("from full, %q reaches its warning range after %vh and its floor after %vh; this test needs the first strictly before the second",
			hp.Key, toWarning, toFloor)
	}

	for _, tc := range []struct {
		name string
		when time.Time
		want string
	}{
		{name: "the instant it was cached", when: epoch, want: PoseFine},
		{name: "with the warning range still ahead of him", when: afterHours(epoch, toWarning/2), want: PoseFine},
		{name: "once he has fallen into it", when: afterHours(epoch, (toWarning+toFloor)/2), want: PosePoorly},
		{name: "once he has run out entirely", when: afterHours(epoch, toFloor+1), want: PoseDead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The SAME entry every time — never refreshed, never re-read. The
			// clock is the only thing that moves.
			if got := entry.pose(tc.when); got != tc.want {
				t.Fatalf("pose %s = %q, want %q — the pose is derived from the cached pairs, so it has to change with nothing but the passage of time",
					tc.name, got, tc.want)
			}
		})
	}
}

// TestTheSkinFallsBackToTheCatalogueDefault covers the account the plane knows
// nothing about yet: a client that has connected but not said hello, or one
// whose pet has never been read. It still has to be drawn as something, because
// publishing no entity at all would make a player invisible to the yard for want
// of a lookup.
func TestTheSkinFallsBackToTheCatalogueDefault(t *testing.T) {
	if got := (display{}).skin(); got != Content().DefaultSkin {
		t.Fatalf("an empty cache entry draws as %q, want the catalogue default %q", got, Content().DefaultSkin)
	}
	// And a skin that IS cached is published unchanged, or the fallback would be
	// the only skin this game ever has.
	const other = "какой-то-другой"
	if got := (display{skinKey: other}).skin(); got != other {
		t.Fatalf("a cached skin was published as %q, want %q", got, other)
	}
}
