package gamevanyagotchi

import (
	"context"
	"errors"
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

// ---------------------------------------------------------------------------
// Furnishing the yard: the one query that makes a restart survivable.
//
// The position map dies with the process, so without this a deploy would empty
// the yard of everybody asleep in it and the first visitor afterwards would find
// a bare field — which is the exact thing the sleepers exist to prevent. It is
// also the one place the plane is allowed to read the database, so WHEN it reads
// matters as much as what it reads.
// ---------------------------------------------------------------------------

// sleepingPet is a pet the database says is lying in the yard.
func sleepingPet(accountID string, at Point, name string) Pet {
	x, y := at.X, at.Y
	p := Pet{ID: "pet-" + accountID, AccountID: accountID, SkinKey: SkinVanya, LocationKey: LocationYard, X: &x, Y: &y}
	if name != "" {
		p.Name = &name
	}
	return p
}

// TestTheYardIsFurnishedOnceAndThenLeftAlone is the shape of the read as much as
// its result: ONE query per process, triggered by a human saying hello.
//
// Not a timer, and above all not the broadcast — the tick is a render step that
// reads nothing, and a query there would be every player five times a second
// against a box that also serves the site. After the one read the cache is kept
// up by the events that actually change it: somebody connects, somebody acts,
// somebody leaves.
func TestTheYardIsFurnishedOnceAndThenLeftAlone(t *testing.T) {
	lay := Point{X: 0.2, Y: 0.9}
	repo := &fakeRepo{sleeping: []Pet{
		sleepingPet("acct-asleep", lay, "Спящий"),
		// Never stood anywhere: NULL is not a position, and laying him down at the
		// origin would put him in the corner of the plane rather than nowhere.
		{ID: "pet-new", AccountID: "acct-never-stood", SkinKey: SkinVanya},
	}}
	tr := &fakeTransport{}
	tr.setMembers(member("visitor"))
	svc := planeService(tr, repo)

	svc.load(context.Background(), accountOf("visitor"))
	if n := repo.sleepingReads(); n != 1 {
		t.Fatalf("the yard was read %d times for one hello; want exactly 1", n)
	}

	// A second hello, and a third: the yard is furniture, and it does not need
	// re-reading every time somebody walks in.
	svc.load(context.Background(), accountOf("visitor"))
	svc.load(context.Background(), "acct-somebody-else")
	if n := repo.sleepingReads(); n != 1 {
		t.Fatalf("the yard was read %d times across three hellos; the read is once per process", n)
	}

	// The sleeper is in the yard, where the database said he was, drawn asleep
	// and labelled — and he is not counted as a person, because he is not one.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]
	p, ok := peerOf(svc, f, "acct-asleep")
	if !ok {
		t.Fatalf("the pet the database says is lying in the yard is not in the frame: %+v", f)
	}
	standingAt(t, p, lay, "the sleeper read out of the database")
	if p.Pose != PoseAsleep {
		t.Errorf("he is drawn as %q; want %q", p.Pose, PoseAsleep)
	}
	if p.Label != "Спящий" || p.Art != SkinVanya {
		t.Errorf("he is drawn as %+v; want the name and skin his row carries", p)
	}
	if f.Here != 1 {
		t.Fatalf("the frame says %d people are in the yard; only the visitor has a socket open", f.Here)
	}
	if _, ok := peerOf(svc, f, "acct-never-stood"); ok {
		t.Fatal("a pet that has never stood anywhere was laid down in the yard; NULL is not a position")
	}

	// And what was read is not written straight back out. The placement is marked
	// as already saved, so the departure of somebody who never actually connected
	// costs no UPDATE saying exactly what the SELECT just said.
	svc.mu.Lock()
	held := svc.pos["acct-asleep"]
	svc.mu.Unlock()
	if !held.saved {
		t.Error("the loaded position is not marked as saved; the next tick would queue a write to store what it had just read")
	}
	if held.provisional {
		t.Error("the loaded position is marked provisional; it came out of the database, and a spawn point must not be allowed to replace it")
	}
	if n := len(queued(svc)); n != 0 {
		t.Fatalf("%d writes were queued by reading the yard; furnishing it is a read", n)
	}
}

// TestAFailedFurnishingIsTriedAgain is why the once-only flag is a bool rather
// than a sync.Once.
//
// A Once would spend its single chance on a database blip and leave the yard
// bare until the next deploy — which, for a feature whose entire job is that the
// field is never empty, is the failure mode that matters. Nobody would notice
// either: an empty yard looks exactly like an evening when nobody was playing.
func TestAFailedFurnishingIsTriedAgain(t *testing.T) {
	lay := Point{X: 0.6, Y: 0.15}
	repo := &fakeRepo{
		sleeping: []Pet{sleepingPet("acct-asleep", lay, "")},
		sleepErr: errors.New("the database is having a moment"),
	}
	tr := &fakeTransport{}
	tr.setMembers(member("visitor"))
	svc := planeService(tr, repo)

	svc.load(context.Background(), accountOf("visitor"))
	svc.mu.Lock()
	_, furnished := svc.pos["acct-asleep"]
	svc.mu.Unlock()
	if furnished {
		t.Fatal("a failed read furnished the yard anyway")
	}

	// The next person through the door tries again, and this time it works.
	repo.mu.Lock()
	repo.sleepErr = nil
	repo.mu.Unlock()
	svc.load(context.Background(), accountOf("visitor"))
	if n := repo.sleepingReads(); n != 2 {
		t.Fatalf("the yard was read %d times across a failure and a retry; want 2", n)
	}

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	p, ok := peerOf(svc, tr.frames()[0], "acct-asleep")
	if !ok {
		t.Fatal("the yard is still bare after a successful retry")
	}
	standingAt(t, p, lay, "the sleeper read on the second attempt")

	// And having succeeded, it stops asking.
	svc.load(context.Background(), accountOf("visitor"))
	if n := repo.sleepingReads(); n != 2 {
		t.Fatalf("the yard was read %d times after a successful load; it is once per process from then on", n)
	}
}

// TestFurnishingTheYardNeverDisplacesSomebodyWhoIsAlreadyInIt is the ordering
// this read has to survive.
//
// A hello can arrive at any moment, including while its sender is already
// standing somewhere he walked to — the hub registers a connection before the
// hello, and a reconnect is a hello too. The database's copy is older news than
// the map's in both cases, and applying it would drag a live player back to
// wherever he was when he last left, which is the exact teleport the whole grace
// period exists to prevent.
func TestFurnishingTheYardNeverDisplacesSomebodyWhoIsAlreadyInIt(t *testing.T) {
	stored := Point{X: 0.1, Y: 0.1}
	repo := &fakeRepo{sleeping: []Pet{sleepingPet(accountOf("here"), stored, "")}}
	tr := &fakeTransport{}
	tr.setMembers(member("here"))
	svc := planeService(tr, repo)

	// He is in the yard and has walked somewhere of his own.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	walked := Point{X: 0.65, Y: 0.55}
	w := tap(t, svc, member("here"), walked)
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// Now the yard is furnished — and the database still thinks he is in the
	// corner where he left off last week.
	svc.load(context.Background(), accountOf("here"))
	if err := svc.broadcast(context.Background(), afterArriving(w).Add(time.Second)); err != nil {
		t.Fatalf("broadcast after the load: %v", err)
	}

	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], accountOf("here"))
	if !ok {
		t.Fatalf("the player vanished when the yard was furnished: %+v", frames[len(frames)-1])
	}
	standingAt(t, p, walked, "the player who was already standing somewhere")
}
