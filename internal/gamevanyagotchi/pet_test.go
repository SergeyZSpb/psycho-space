package gamevanyagotchi

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
)

// The durable half, driven against an in-memory repository.
//
// Everything interesting about it is decided in the service rather than in SQL:
// which stats a response carries and in what order, when a death is written down
// and what instant it carries, what a missing row does, what an action re-stamps.
// The integration suite proves the queries; this file proves the decisions, and
// it does so without a container so it can run on every commit.
//
// One of those decisions is now load-bearing in a way it was not before. Health
// is a consequence of the other stats rather than a chore of its own, and the
// arithmetic that makes that exact requires every write to re-stamp EVERY row at
// one instant. Nothing in a response reveals whether that happened, so the tests
// that guard it assert on what the fake repository was actually handed.

// testAccount is the caller every test here acts as. One account is one Ваня, so
// nothing in this file needs a second.
const testAccount = "acct-1"

// petBorn is the creation time the fake stamps on a pet. Fixed so a failure
// reads the same on every run; nothing under test depends on its value.
var petBorn = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory Repository that mirrors the SQL's semantics where
// they matter: SeedStats leaves an existing row alone (ON CONFLICT DO NOTHING),
// WriteStats upserts every row it is given, and MarkDied writes only while
// died_at is null — which is the guard that makes a death recorded exactly once
// however many readers observe it. It also records what it was called with,
// because "which rows did one action write, and at what instant" is half of what
// the tests below are asserting.
type fakeRepo struct {
	pet  *Pet
	rows []StatRow

	ensured int
	seeded  int
	revived int
	// statReads counts calls to Stats, which is how a test says "and the plane
	// drew that without asking the database again": the cache exists precisely
	// so the broadcast never reads, and a cache that quietly re-read would be
	// invisible in every frame it produced.
	statReads int
	// writes counts calls to WriteStats; written keeps the batch each call was
	// handed. The batch is the interesting part: the coupled decay is only exact
	// while every stat is re-stamped together, and a response cannot show that.
	writes  int
	written [][]StatRow
	// appended is the pet's event log, in the order the fake was handed it.
	// The verbs AND their order are what a test checks: a batch is folded in
	// sequence, so drink-then-relieve is a different pet from the reverse, and
	// the log is what a replay would read back.
	appended []Event
	// AppendErr, when set, fails the append — so a test can prove the snapshot
	// and the log are written together and neither survives the other failing.
	appendErr error

	// markDiedCalls counts every call; died records the instant of the calls that
	// actually wrote. Both are needed: a second read must not even ask.
	markDiedCalls int
	died          []time.Time

	// mu guards positions and sleepReads ALONE, and that narrowness is the point
	// rather than an oversight. Every other field here is touched only from the
	// pet path, which is one goroutine per test; a position is the one thing
	// production writes from a goroutine of its own — the writer Service.Run
	// starts — so it is the one field a test could ever see appended to
	// concurrently.
	mu sync.Mutex
	// positions records every SavePosition call, in order. A departure leaves no
	// other trace, so this is what "he was written down where he was standing"
	// is asserted against.
	positions []savedPosition
	// sleepReads counts calls to SleepingPets. Guarded by the same lock as
	// positions because the only paths that could ever call it are the plane's,
	// which run on goroutines of their own.
	sleepReads int
	// sleeping, when set, is what SleepingPets answers with: the yard as the
	// database knows it, which is generally several pets belonging to nobody in
	// particular rather than the caller's own. Unset, the single pet above stands
	// in for it, which is all the pet path's own tests need.
	sleeping []Pet
	// sleepErr fails the query, so a test can prove the load is retried rather
	// than spending its one chance on a database blip.
	sleepErr error
}

// savedPosition is one SavePosition call, exactly as it was made.
type savedPosition struct {
	accountID string
	at        Point
	seen      time.Time
}

var _ Repository = (*fakeRepo)(nil)

func (f *fakeRepo) EnsurePet(_ context.Context, _ db.DBTX, accountID, skinKey, locationKey string) (Pet, error) {
	f.ensured++
	if f.pet == nil {
		f.pet = &Pet{
			ID:          "pet-" + accountID,
			AccountID:   accountID,
			SkinKey:     skinKey,
			LocationKey: locationKey,
			CreatedAt:   petBorn,
		}
	}
	return *f.pet, nil
}

// FindPet reads without creating, which is what the plane does: opening a
// socket must not conjure a pet for somebody who has never opened the game. The
// account id is honoured rather than ignored, so a test with two players on the
// plane cannot accidentally hand both of them the same Ваня.
func (f *fakeRepo) FindPet(_ context.Context, _ db.DBTX, accountID string) (Pet, bool, error) {
	if f.pet == nil || f.pet.AccountID != accountID {
		return Pet{}, false, nil
	}
	return *f.pet, true, nil
}

// SleepingPets returns the pets that have stood somewhere, newest first, up to
// limit — the query that is meant to refill the yard with sleeping Ваняs after a
// restart, since the position map dies with the process.
//
// It mirrors the SQL's two filters: a pet with no x/y has never stood anywhere
// and is not lying in the yard, and the order is by last_seen_at descending so
// the cap keeps the people who were here most recently rather than an arbitrary
// handful. sleepReads counts the calls, because "read ONCE per process, lazily,
// on the first hello" is a property of WHEN it is called rather than of what it
// returns — and it is asserted in display_test.go, against ensureSleepers.
func (f *fakeRepo) SleepingPets(_ context.Context, _ db.DBTX, limit int) ([]Pet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleepReads++
	if f.sleepErr != nil {
		return nil, f.sleepErr
	}
	pets := f.sleeping
	if pets == nil && f.pet != nil {
		pets = []Pet{*f.pet}
	}
	out := make([]Pet, 0, len(pets))
	for _, p := range pets {
		// The SQL's own filter: a pet with no x/y has never stood anywhere and is
		// not lying in the yard.
		if p.X == nil || p.Y == nil {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, p)
	}
	return out, nil
}

// sleepingReads is how many times the yard has been read out of the database.
func (f *fakeRepo) sleepingReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sleepReads
}

// SavePosition records a departure. Nothing here reads it back — the pet path
// never asks where anybody is standing — so recording the call IS the behaviour
// under test.
func (f *fakeRepo) SavePosition(_ context.Context, _ db.DBTX, accountID string, at Point, seen time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.positions = append(f.positions, savedPosition{accountID: accountID, at: at, seen: seen})
	return nil
}

// saved returns every position written down so far, in the order it was written.
func (f *fakeRepo) saved() []savedPosition {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]savedPosition(nil), f.positions...)
}

func (f *fakeRepo) Stats(_ context.Context, _ db.DBTX, _ string) ([]StatRow, error) {
	f.statReads++
	return append([]StatRow(nil), f.rows...), nil
}

func (f *fakeRepo) SeedStats(_ context.Context, _ db.DBTX, _ string, rows []StatRow) error {
	f.seeded++
	for _, r := range rows {
		if _, ok := f.row(r.Key); ok {
			continue // ON CONFLICT DO NOTHING: the row that is there wins.
		}
		f.rows = append(f.rows, r)
	}
	return nil
}

func (f *fakeRepo) WriteStats(_ context.Context, _ db.DBTX, _ string, rows []StatRow) error {
	f.writes++
	// Copied, not aliased: the caller owns the slice it passed and a test that
	// compared against a batch the service later reused would prove nothing.
	f.written = append(f.written, append([]StatRow(nil), rows...))
	for _, r := range rows {
		if !f.upsert(r) {
			f.rows = append(f.rows, r)
		}
	}
	return nil
}

// upsert overwrites an existing row in place, reporting whether it found one.
func (f *fakeRepo) upsert(r StatRow) bool {
	for i := range f.rows {
		if f.rows[i].Key == r.Key {
			f.rows[i].Value, f.rows[i].AsOf = r.Value, r.AsOf
			return true
		}
	}
	return false
}

func (f *fakeRepo) MarkDied(_ context.Context, _ db.DBTX, _ string, at time.Time) (bool, error) {
	f.markDiedCalls++
	if f.pet == nil || f.pet.DiedAt != nil {
		return false, nil // the `died_at IS NULL` guard
	}
	when := at
	f.pet.DiedAt = &when
	f.died = append(f.died, at)
	return true, nil
}

func (f *fakeRepo) Revive(_ context.Context, _ db.DBTX, _ string) error {
	f.revived++
	if f.pet != nil {
		f.pet.DiedAt = nil
	}
	return nil
}

// row returns a stored row by key, as it is actually written down.
func (f *fakeRepo) row(key string) (StatRow, bool) {
	for _, r := range f.rows {
		if r.Key == key {
			return r, true
		}
	}
	return StatRow{}, false
}

// lastBatch is the set of rows the most recent action wrote, failing if there
// was not exactly one.
func (f *fakeRepo) lastBatch(t *testing.T) []StatRow {
	t.Helper()
	if len(f.written) != 1 {
		t.Fatalf("%d calls to WriteStats; want exactly one for one action", len(f.written))
	}
	return f.written[0]
}

// petService builds the durable half with neither transport nor pool. The two
// halves of Service never meet — nothing on the pet path touches the position
// map and nothing on the plane path touches storage — so both are genuinely
// unused here rather than being stubbed out.
func petService(repo Repository) *Service { return NewService(nil, "yard", nil, repo) }

// playedFor builds a repository already holding a pet and the rows it was left
// with: the shape of an account that has been playing for a while, which is the
// only shape in which decay, death and seeding gaps are observable.
func playedFor(rows ...StatRow) *fakeRepo {
	return &fakeRepo{
		pet: &Pet{
			ID:          "pet-existing",
			AccountID:   testAccount,
			SkinKey:     SkinVanya,
			LocationKey: LocationYard,
			CreatedAt:   petBorn,
		},
		// COPIED, not aliased. The variadic slice belongs to the caller, and a
		// test that later compares "what I set up" against "what came back"
		// would otherwise be comparing the post-action rows with themselves —
		// the fake would have written straight through into the fixture. A real
		// repository cannot reach back into a caller's memory, so neither should
		// this one.
		rows: append([]StatRow(nil), rows...),
	}
}

// statOf returns one stat from a response, failing if the client would not have
// received it at all.
func statOf(t *testing.T, st State, key string) StatValue {
	t.Helper()
	for _, v := range st.Stats {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("the response carries no %q: %+v", key, st.Stats)
	return StatValue{}
}

// rowOf returns one row from a written batch, failing if the batch left it out.
func rowOf(t *testing.T, batch []StatRow, key string) StatRow {
	t.Helper()
	for _, r := range batch {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("the batch carries no %q: %+v", key, batch)
	return StatRow{}
}

// nearlyStat fails unless got is want, allowing for the fraction of a point the
// stat decays between the test reading the clock and the service reading its
// own. A twentieth of a point is a handful of seconds of scheduling delay even
// at the fastest drain the catalogue can produce, and two orders of magnitude
// below any difference these tests are looking for.
func nearlyStat(t *testing.T, got, want float64, what string) {
	t.Helper()
	const slack = 0.05
	if math.IsNaN(got) || math.Abs(got-want) > slack {
		t.Fatalf("%s = %v; want %v (±%v)", what, got, want, slack)
	}
}

// mustStat is the catalogue entry a test is reasoning about. Fetched rather than
// hardcoded because the rates in content.go are meant to be moved by feel, and a
// test that pinned them would make every tuning change look like a regression.
func mustStat(t *testing.T, key string) Stat {
	t.Helper()
	s, ok := StatByKey(key)
	if !ok {
		t.Fatalf("the catalogue has no stat %q", key)
	}
	return s
}

// mustAction is the same for a verb.
func mustAction(t *testing.T, key string) Action {
	t.Helper()
	a, ok := ActionByKey(key)
	if !ok {
		t.Fatalf("the catalogue has no action %q", key)
	}
	return a
}

// effectOn is what an action moves one stat by, zero if it does not touch it.
func effectOn(a Action, statKey string) float64 {
	var delta float64
	for _, e := range a.Effects {
		if e.StatKey == statKey {
			delta += e.Delta
		}
	}
	return delta
}

// penaltyOn is health's penalty driven by one stat.
func penaltyOn(t *testing.T, driverKey string) Penalty {
	t.Helper()
	for _, p := range mustStat(t, StatHP).Penalties {
		if p.WhenKey == driverKey {
			return p
		}
	}
	t.Fatalf("health is not penalised by %q; the test is reasoning about coupling the catalogue no longer has", driverKey)
	return Penalty{}
}

// calmNeeds returns a stored row for every stat that drives health, parked at
// the end of its range furthest from the threshold that hurts and stamped at
// asOf.
//
// It is how a test says "this one is about the plain decay". With every driver
// as far from its threshold as the content allows, no penalty switches on for
// the next `quiet` hours, so health falls at its base rate alone and the
// expected numbers are one multiplication. The helper fails rather than handing
// back a set that would not stay quiet that long — a retuned catalogue has to
// break the test loudly instead of moving the arithmetic underneath it.
func calmNeeds(t *testing.T, asOf time.Time, quiet float64) []StatRow {
	t.Helper()
	penalties := mustStat(t, StatHP).Penalties
	rows := make([]StatRow, 0, len(penalties))
	for _, p := range penalties {
		def := mustStat(t, p.WhenKey)
		// The far end: a driver that hurts when it is LOW starts full, and one
		// that hurts when it is HIGH starts empty.
		start := def.Max
		if p.Above {
			start = def.Min
		}
		if onset := (start - p.Threshold) / def.DecayPerHour; onset <= quiet {
			t.Fatalf("%q reaches the threshold that hurts %v hours after a full reset, inside the %v-hour window this test needs quiet",
				def.Key, onset, quiet)
		}
		rows = append(rows, StatRow{Key: def.Key, Value: start, AsOf: asOf})
	}
	return rows
}

// requireNoPenaltyWithin fails unless every driver in `rows` stays clear of the
// threshold that hurts for `quiet` hours after its own as_of.
//
// Used where a test picks its own driver values rather than the calm ones, so
// the base-rate expectation it goes on to assert is stated as a precondition
// instead of being assumed.
func requireNoPenaltyWithin(t *testing.T, rows []StatRow, quiet float64) {
	t.Helper()
	for _, p := range mustStat(t, StatHP).Penalties {
		def := mustStat(t, p.WhenKey)
		found := false
		for _, r := range rows {
			if r.Key != p.WhenKey {
				continue
			}
			found = true
			// Signed, so one expression covers a driver that falls to a low
			// threshold and one that fills to a high one. Zero or less means it
			// is already past the threshold.
			if onset := (r.Value - p.Threshold) / def.DecayPerHour; onset <= quiet {
				t.Fatalf("%q at %v crosses the threshold that hurts after %v hours, inside the %v-hour window this test needs quiet",
					def.Key, r.Value, onset, quiet)
			}
		}
		if !found {
			t.Fatalf("no row for %q, which drives health; this test cannot claim the base rate alone applies", p.WhenKey)
		}
	}
}

// TestTheFirstReadCreatesThePetAndSeedsEveryStat covers the whole of a new
// account's first request. There is no registration step in this game — opening
// it IS the registration — so if this path does not both create the pet and give
// it a full set of stats, a new player's first screen is empty.
func TestTheFirstReadCreatesThePetAndSeedsEveryStat(t *testing.T) {
	repo := &fakeRepo{}
	svc := petService(repo)

	st, err := svc.State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	if repo.pet == nil || st.Pet.ID == "" {
		t.Fatalf("no pet was created: %+v", st.Pet)
	}
	c := Content()
	if st.Pet.SkinKey != c.DefaultSkin || st.Pet.LocationKey != c.DefaultLocation {
		t.Errorf("pet created as (%q, %q); want the catalogue defaults (%q, %q)",
			st.Pet.SkinKey, st.Pet.LocationKey, c.DefaultSkin, c.DefaultLocation)
	}
	if !st.Alive || st.Pet.DiedAt != nil {
		t.Errorf("a pet was born dead: alive=%v died_at=%v", st.Alive, st.Pet.DiedAt)
	}
	if st.ServerNow.IsZero() {
		t.Error("server_now is the zero time; the client corrects its own clock against it")
	}

	// Every catalogue stat, at its starting value, in catalogue order — the
	// order is the display order and is content too.
	if len(st.Stats) != len(c.Stats) {
		t.Fatalf("response carries %d stats; want all %d of them: %+v", len(st.Stats), len(c.Stats), st.Stats)
	}
	for i, def := range c.Stats {
		if st.Stats[i].Key != def.Key {
			t.Fatalf("stat %d is %q; want %q — the response is not in catalogue order", i, st.Stats[i].Key, def.Key)
		}
		if st.Stats[i].Value != def.Start {
			t.Errorf("%q seeded at %v; want its start %v", def.Key, st.Stats[i].Value, def.Start)
		}
		// The rate the client interpolates with. It is the base rate on a fresh
		// pet because nothing has gone wrong yet, and sending zero — or nothing —
		// would leave every bar frozen between fetches.
		if st.Stats[i].RatePerHour != def.DecayPerHour {
			t.Errorf("%q reports a rate of %v; want its base %v on a pet with no unmet needs",
				def.Key, st.Stats[i].RatePerHour, def.DecayPerHour)
		}
	}

	// And a second read seeds nothing: the rows are there now. A repository that
	// re-seeded would quietly restore every stat to full on every request.
	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("second State: %v", err)
	}
	if repo.seeded != 1 {
		t.Errorf("SeedStats called %d times across two reads; want once", repo.seeded)
	}
}

// TestAStatStoredInThePastReadsBackDecayed is offline progression, which is not
// a feature anybody built: it is what the stored pair already means. All three
// directions are asserted in one read because they share the one expression — a
// stat that fills is a negative rate and nothing else — and the window is short
// enough that no need has gone unmet, so this stays a test of the plain decay.
func TestAStatStoredInThePastReadsBackDecayed(t *testing.T) {
	const away = 2.0 // hours
	stored := time.Now().UTC().Add(-time.Duration(away * float64(time.Hour)))
	hpDef := mustStat(t, StatHP)

	rows := append([]StatRow{{Key: StatHP, Value: hpDef.Max, AsOf: stored}}, calmNeeds(t, stored, away)...)
	requireNoPenaltyWithin(t, rows, away)
	repo := playedFor(rows...)

	st, err := petService(repo).State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	// The expectation is re-derived from each definition rather than taken from
	// Stat.At, so this fails if the service reads a pair with the wrong sign, the
	// wrong unit, or not at all.
	for _, r := range rows {
		def := mustStat(t, r.Key)
		got := statOf(t, st, r.Key)
		nearlyStat(t, got.Value, def.Clamp(r.Value-def.DecayPerHour*away), r.Key+" after two hours away")
		if !got.AsOf.Equal(stored) {
			// The pair the value was decayed FROM is sent alongside it, so the
			// client can keep the bar moving between fetches without asking again.
			t.Errorf("%s as_of = %v; want the stored instant %v", r.Key, got.AsOf, stored)
		}
	}
	if hp := statOf(t, st, StatHP).Value; hp >= hpDef.Max {
		t.Errorf("hp did not move at all in two hours: %v", hp)
	}
	bladderDef := mustStat(t, StatBladder)
	if bladder := statOf(t, st, StatBladder).Value; bladder <= bladderDef.Min {
		t.Errorf("bladder did not fill at all in two hours: %v", bladder)
	}

	// Reading is not writing: a read that re-stamped as_of would hand every
	// player a free pause by opening the app.
	if repo.writes != 0 {
		t.Errorf("a plain read wrote %d batches of stat rows; want none", repo.writes)
	}
	row, _ := repo.row(StatHP)
	if row.Value != hpDef.Max || !row.AsOf.Equal(stored) {
		t.Errorf("the stored pair changed on a read: %+v", row)
	}
}

// TestAnUnmetNeedMakesHealthFallFasterThanItsOwnRate is the coupling as the
// player experiences it, through the one path that actually serves it.
//
// Health is a consequence rather than a chore: it barely rots on its own, and
// what kills him is a need left unmet. A service that read hp with the uncoupled
// arithmetic would answer with a healthier pet than the game's own rules
// describe — and would then also derive the wrong moment of death — so the
// difference between the two readings is asserted directly rather than left to a
// tolerance.
func TestAnUnmetNeedMakesHealthFallFasterThanItsOwnRate(t *testing.T) {
	const away = 4.0 // hours
	hpDef := mustStat(t, StatHP)
	bladderDef := mustStat(t, StatBladder)
	full := penaltyOn(t, StatBladder)
	stored := time.Now().UTC().Add(-time.Duration(away * float64(time.Hour)))

	// The bladder has been over its threshold for the whole window, so the
	// penalty is on from the first instant and the accrued damage is the whole
	// window's worth. The beer is parked at the far end of its own range, so only
	// one of the two penalties is in play and the expected number stays legible.
	rows := []StatRow{
		{Key: StatHP, Value: hpDef.Max, AsOf: stored},
		{Key: StatBladder, Value: full.Threshold + (bladderDef.Max-full.Threshold)/2, AsOf: stored},
	}
	for _, r := range calmNeeds(t, stored, away) {
		if r.Key != StatBladder {
			rows = append(rows, r)
		}
	}

	st, err := petService(playedFor(rows...)).State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	hp := statOf(t, st, StatHP)
	baseOnly := hpDef.Max - hpDef.DecayPerHour*away
	want := baseOnly - full.RatePerHour*away
	nearlyStat(t, hp.Value, want, "hp after four hours with a full bladder")
	if hp.Value >= baseOnly {
		t.Fatalf("hp = %v, which is what the base rate alone would give (%v); the unmet need cost him nothing",
			hp.Value, baseOnly)
	}

	// And the rate the client interpolates with is the coupled one, or the bar
	// would creep at the wrong speed until the next fetch corrected it.
	nearlyStat(t, hp.RatePerHour, hpDef.DecayPerHour+full.RatePerHour, "the hp rate reported to the client")
}

// TestAVerbThatIsNotInTheCatalogueIsRejectedAndTouchesNothing covers the stale
// or probing client. It has to be a clean refusal, and — because the catalogue
// lookup happens before anything else — it must not create a pet or write a row
// for an account that only ever sent nonsense.
func TestAVerbThatIsNotInTheCatalogueIsRejectedAndTouchesNothing(t *testing.T) {
	for _, key := range []string{"", "sudo-drink", StatHP, "DRINK", ActionDrink + " "} {
		t.Run("action "+key, func(t *testing.T) {
			repo := &fakeRepo{}
			if _, err := petService(repo).Act(context.Background(), testAccount, key); !errors.Is(err, ErrUnknownAction) {
				t.Fatalf("Act(%q) error = %v; want ErrUnknownAction", key, err)
			}
			if repo.ensured != 0 || repo.writes != 0 || repo.seeded != 0 {
				t.Fatalf("a rejected verb still touched storage: ensured=%d writes=%d seeded=%d",
					repo.ensured, repo.writes, repo.seeded)
			}
		})
	}
}

// TestActAppliesEveryEffectOfAMultiStatAction is the action loop itself.
//
// The client sends a verb and never a value, so the server's own arithmetic is
// the only thing standing between "a drink helped" and "a client set its hp to a
// thousand". An action moves a LIST of stats now — drinking tops him up, cheers
// him up and fills his bladder, which is the whole joke and the reason the
// second verb has anything to do — so every effect has to land, each clamped
// against its OWN stat's bounds rather than against the first one's.
func TestActAppliesEveryEffectOfAMultiStatAction(t *testing.T) {
	drink := mustAction(t, ActionDrink)
	if len(drink.Effects) < 2 {
		t.Fatalf("%q moves %d stats; this test exists for the multi-effect case", drink.Key, len(drink.Effects))
	}

	for _, tc := range []struct {
		name   string
		ageHrs float64
		// stored is the value each stat starts at, as a fraction of its own
		// range. Fractions rather than numbers so the case still means
		// "mid-range" or "nearly full" after a retune. `high` names the stats
		// pushed near their ceiling; everything else sits mid-range.
		//
		// Per-stat rather than one fraction for all, because the DRIVERS have to
		// stay quiet: this test is about effects landing and clamping, and a
		// bladder parked at 95% is already past the threshold that hurts, which
		// would fold a penalty into every expectation below. requireNoPenaltyWithin
		// enforces that rather than trusting it.
		high map[string]bool
	}{
		{name: "an ordinary round", ageHrs: 1},
		// Close enough to the top that at least one effect must clamp: "did
		// nothing at all" and "stopped at the ceiling" have to be
		// distinguishable. Beer is the one pushed up, because a drink adds most
		// to it — and a full beer is a QUIET driver, so the case stays legal.
		{name: "a round that overflows a ceiling", ageHrs: 0, high: map[string]bool{StatBeer: true, StatHP: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asOf := time.Now().UTC().Add(-time.Duration(tc.ageHrs * float64(time.Hour)))

			rows := make([]StatRow, 0, len(Content().Stats))
			for _, def := range Content().Stats {
				frac := 0.4
				if tc.high[def.Key] {
					frac = 0.95
				}
				rows = append(rows, StatRow{Key: def.Key, Value: def.Min + (def.Max-def.Min)*frac, AsOf: asOf})
			}
			requireNoPenaltyWithin(t, rows, tc.ageHrs)
			repo := playedFor(rows...)

			before := time.Now().UTC()
			st, err := petService(repo).Act(context.Background(), testAccount, ActionDrink)
			after := time.Now().UTC()
			if err != nil {
				t.Fatalf("Act: %v", err)
			}

			clamped := 0
			for _, r := range rows {
				def := mustStat(t, r.Key)
				// The delta lands on the DECAYED value, not on the stored one, and
				// the sum is clamped.
				raw := r.Value - def.DecayPerHour*tc.ageHrs + effectOn(drink, r.Key)
				want := def.Clamp(raw)
				if raw != want {
					clamped++
				}

				nearlyStat(t, statOf(t, st, r.Key).Value, want, r.Key+" in the response")
				stored, found := repo.row(r.Key)
				if !found {
					t.Fatalf("the action wrote no %q row at all", r.Key)
				}
				nearlyStat(t, stored.Value, want, "the stored "+r.Key+" value")
				if stored.Value < def.Min || stored.Value > def.Max {
					t.Errorf("the stored %q is %v, outside [%v, %v]; the delta was not clamped",
						r.Key, stored.Value, def.Min, def.Max)
				}
				// Re-stamped to the instant of the action, which is what stops the
				// next read decaying the new value from the old as_of and charging
				// the player twice for the same hours.
				if stored.AsOf.Before(before) || stored.AsOf.After(after) {
					t.Errorf("%q as_of = %v; want the instant of the action, within [%v, %v]",
						r.Key, stored.AsOf, before, after)
				}
			}

			if len(tc.high) > 0 && clamped == 0 {
				t.Fatalf("nothing clamped even with %v near the ceiling; this case is no longer testing the ceiling", tc.high)
			}
			// One action, one write. The batch is what the next test is about.
			if repo.writes != 1 {
				t.Errorf("WriteStats called %d times for one action; want once", repo.writes)
			}
		})
	}
}

// TestEveryActionRestampsEveryStatAtOneInstant is the invariant the whole
// coupled decay rests on, and it is invisible from a response.
//
// Health's drain is a function of the other stats' trajectories, so integrating
// it over a window needs every driver's history across that whole window. If an
// action wrote only the rows it moved, the drivers would keep an older as_of and
// the stretch between the two instants would be history nobody can reconstruct —
// which is not a rounding error but silently erased damage. Asserted against
// what the repository was handed, because the response looks identical either
// way.
func TestEveryActionRestampsEveryStatAtOneInstant(t *testing.T) {
	for _, action := range Content().Actions {
		t.Run(action.Key, func(t *testing.T) {
			asOf := time.Now().UTC().Add(-time.Hour)
			rows := make([]StatRow, 0, len(Content().Stats))
			for _, def := range Content().Stats {
				rows = append(rows, StatRow{Key: def.Key, Value: def.Min + (def.Max-def.Min)/2, AsOf: asOf})
			}
			repo := playedFor(rows...)

			before := time.Now().UTC()
			if _, err := petService(repo).Act(context.Background(), testAccount, action.Key); err != nil {
				t.Fatalf("Act(%q): %v", action.Key, err)
			}
			after := time.Now().UTC()

			batch := repo.lastBatch(t)
			if len(batch) != len(Content().Stats) {
				t.Fatalf("the action wrote %d rows; want all %d stats, including the ones it does not move: %+v",
					len(batch), len(Content().Stats), batch)
			}
			stamp := batch[0].AsOf
			for _, def := range Content().Stats {
				r := rowOf(t, batch, def.Key)
				if !r.AsOf.Equal(stamp) {
					t.Errorf("%q was written at %v and %q at %v; every row must share one instant",
						batch[0].Key, stamp, def.Key, r.AsOf)
				}
			}
			if stamp.Before(before) || stamp.After(after) {
				t.Errorf("the shared as_of is %v; want the instant of the action, within [%v, %v]", stamp, before, after)
			}
			// Including the ones this verb has nothing to do with: a stat with no
			// effect is re-stamped at its DECAYED value, not left where it was.
			for _, def := range Content().Stats {
				if effectOn(action, def.Key) != 0 {
					continue
				}
				stored := rowOf(t, batch, def.Key)
				nearlyStat(t, stored.Value, def.Clamp(def.Min+(def.Max-def.Min)/2-def.DecayPerHour*1),
					def.Key+", untouched by "+action.Key+", re-stamped at its decayed value")
			}
		})
	}
}

// TestRelievingHimDoesNotEraseTheDamageAFullBladderAlreadyDid is the regression
// that the re-stamp invariant exists to prevent, expressed as behaviour.
//
// He has been uncomfortable all morning, and that has been costing him health
// the whole time. Emptying the bladder must stop the damage, not undo it: if the
// action wrote only the bladder row, the next read would re-derive health from a
// pair which says the bladder was never full, and the morning would simply
// vanish. That is the bug the whole "write every stat at one instant" rule is
// there to make impossible, and this is what it looks like from outside.
func TestRelievingHimDoesNotEraseTheDamageAFullBladderAlreadyDid(t *testing.T) {
	const away = 4.0 // hours of discomfort
	hpDef, bladderDef := mustStat(t, StatHP), mustStat(t, StatBladder)
	full := penaltyOn(t, StatBladder)
	relieve := mustAction(t, ActionRelieve)
	asOf := time.Now().UTC().Add(-time.Duration(away * float64(time.Hour)))

	rows := []StatRow{
		{Key: StatHP, Value: hpDef.Max, AsOf: asOf},
		// Already over the threshold when the window opens, and it only fills
		// further, so the penalty is on for every one of those hours.
		{Key: StatBladder, Value: full.Threshold + (bladderDef.Max-full.Threshold)/2, AsOf: asOf},
	}
	for _, r := range calmNeeds(t, asOf, away) {
		if r.Key != StatBladder {
			rows = append(rows, r)
		}
	}
	repo := playedFor(rows...)

	st, err := petService(repo).Act(context.Background(), testAccount, ActionRelieve)
	if err != nil {
		t.Fatalf("Act(%q): %v", ActionRelieve, err)
	}

	// The bladder is reset. A delta larger than the whole scale plus the clamp is
	// how "reset" is expressed, so it lands exactly on the floor.
	if delta := effectOn(relieve, StatBladder); delta > -(bladderDef.Max - bladderDef.Min) {
		t.Fatalf("%q moves the bladder by %v, which is less than its whole range; it is no longer a reset",
			ActionRelieve, delta)
	}
	nearlyStat(t, statOf(t, st, StatBladder).Value, bladderDef.Min, "the bladder after relief")
	stored, _ := repo.row(StatBladder)
	nearlyStat(t, stored.Value, bladderDef.Min, "the stored bladder after relief")

	// And the damage is still there. Both the response and the row: the response
	// is what the player sees now, the row is what the next read will decay from.
	baseOnly := hpDef.Max - hpDef.DecayPerHour*away
	want := baseOnly - full.RatePerHour*away
	nearlyStat(t, statOf(t, st, StatHP).Value, want, "hp after relieving himself")
	storedHP, _ := repo.row(StatHP)
	nearlyStat(t, storedHP.Value, want, "the stored hp after relieving himself")
	if storedHP.Value >= baseOnly {
		t.Fatalf("stored hp = %v, which is what the base rate alone would give (%v): the morning's damage was erased by the reset",
			storedHP.Value, baseOnly)
	}

	// The stat the action never mentions was re-stamped too, which is the
	// mechanism the paragraph above depends on.
	if hpRow := rowOf(t, repo.lastBatch(t), StatHP); !hpRow.AsOf.Equal(stored.AsOf) {
		t.Errorf("hp was written at %v and the bladder at %v; a driver and its consequence must share one instant",
			hpRow.AsOf, stored.AsOf)
	}
}

// TestADeathIsRecordedOnceAtTheMomentItHappened is the property the whole
// died_at design exists for. A read arriving hours late has to record the moment
// he DIED rather than the moment somebody looked — that instant is derivable
// from the stored pairs alone, which is what makes it identical for every reader
// and therefore safe to write without a lock.
func TestADeathIsRecordedOnceAtTheMomentItHappened(t *testing.T) {
	const (
		away = 8.0 // hours since anybody looked
		left = 4.0 // hours of health there were in the stored pair
	)
	hpDef := mustStat(t, StatHP)
	now := time.Now().UTC()
	asOf := now.Add(-time.Duration(away * float64(time.Hour)))

	// Every need parked at the far end of its range, so this stays a test about
	// the derived instant rather than about the piecewise walk — that one lives
	// in decay_test.go, where it can be arithmetic instead of a service.
	rows := append([]StatRow{{Key: StatHP, Value: hpDef.Min + hpDef.DecayPerHour*left, AsOf: asOf}},
		calmNeeds(t, asOf, away)...)
	repo := playedFor(rows...)
	svc := petService(repo)

	first, err := svc.State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if first.Alive || first.Pet.DiedAt == nil {
		t.Fatalf("a pet whose hp ran out hours ago read back alive: %+v", first.Pet)
	}
	nearlyStat(t, statOf(t, first, StatHP).Value, hpDef.Min, "hp of a dead pet")

	// The instant, not the observation. Derived from (value, as_of) alone, so it
	// is four hours in the past however long it took anybody to notice.
	if len(repo.died) != 1 {
		t.Fatalf("%d deaths written; want exactly one", len(repo.died))
	}
	wantDied := asOf.Add(time.Duration(left * float64(time.Hour)))
	if d := repo.died[0].Sub(wantDied); d < -time.Second || d > time.Second {
		t.Errorf("death recorded at %v; want the derived instant %v (off by %v)", repo.died[0], wantDied, d)
	}
	if !repo.died[0].Before(now.Add(-time.Duration((away - left - 1) * float64(time.Hour)))) {
		t.Errorf("death recorded at %v, which is when somebody looked rather than when it happened", repo.died[0])
	}

	// A second read must not even ask. The `died_at IS NULL` guard would make a
	// second write a no-op anyway, but a read that keeps calling it is a read
	// that would overwrite the truthful instant the moment that guard moved.
	second, err := svc.State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("second State: %v", err)
	}
	if repo.markDiedCalls != 1 {
		t.Errorf("MarkDied called %d times across two reads; want once", repo.markDiedCalls)
	}
	if second.Alive || second.Pet.DiedAt == nil || !second.Pet.DiedAt.Equal(*first.Pet.DiedAt) {
		t.Errorf("the second read reports a different death: %+v vs %+v", second.Pet, first.Pet)
	}
}

// TestADeadPetTakesTheActionThatRevivesAndRefusesTheOneThatDoesNot covers both
// halves of Act's death guard, which are now both reachable through the shipped
// catalogue.
//
// Death here is a fright rather than an ending — an irreversible loss in a
// fifteen-person friend group is how a player leaves for good — so beer brings
// him round and what a death costs is the scare plus whatever decayed while
// nobody was looking. The refusal is the other half and it is not decoration: a
// dead Ваня does not go to the toilet, which is what makes ErrPetDead, the 409 it
// becomes, and the screen that says what to do instead all real rather than
// theoretical. A refusal must also write nothing at all, or a corpse would keep
// having its stats re-stamped by a verb it just declined.
func TestADeadPetTakesTheActionThatRevivesAndRefusesTheOneThatDoesNot(t *testing.T) {
	hpDef := mustStat(t, StatHP)

	// deadPet is a pet whose hp ran out an hour ago, rebuilt per case so each one
	// drives a fresh death.
	deadPet := func(t *testing.T) *fakeRepo {
		t.Helper()
		asOf := time.Now().UTC().Add(-time.Hour)
		return playedFor(append([]StatRow{{Key: StatHP, Value: hpDef.Min, AsOf: asOf}},
			calmNeeds(t, asOf, 1)...)...)
	}

	var revived, refused int
	for _, action := range Content().Actions {
		t.Run(action.Key, func(t *testing.T) {
			repo := deadPet(t)
			st, err := petService(repo).Act(context.Background(), testAccount, action.Key)

			if !action.RevivesFatal {
				refused++
				if !errors.Is(err, ErrPetDead) {
					t.Fatalf("Act(%q) on a dead pet error = %v; want ErrPetDead", action.Key, err)
				}
				if repo.writes != 0 {
					t.Errorf("%q wrote %d batches of stat rows on a dead pet; want none", action.Key, repo.writes)
				}
				if repo.revived != 0 {
					t.Errorf("%q revived a pet it was not allowed to touch", action.Key)
				}
				if repo.pet.DiedAt == nil {
					t.Error("the death was cleared by an action that was refused")
				}
				return
			}

			revived++
			if err != nil {
				t.Fatalf("Act(%q) on a dead pet = %v; an action that revives must be allowed through", action.Key, err)
			}
			if !st.Alive || st.Pet.DiedAt != nil {
				t.Fatalf("he is still dead after an action that revives him: alive=%v died_at=%v", st.Alive, st.Pet.DiedAt)
			}
			if repo.revived != 1 {
				t.Errorf("Revive called %d times; want once", repo.revived)
			}
			if repo.pet.DiedAt != nil {
				t.Errorf("the death is still stored after a revive: %v", repo.pet.DiedAt)
			}
			nearlyStat(t, statOf(t, st, StatHP).Value,
				hpDef.Clamp(hpDef.Min+effectOn(action, StatHP)), "hp after being brought round")
		})
	}

	// Both branches have to be exercised by the catalogue, or this test is half a
	// test wearing a loop. content_test.go asserts the same thing about the
	// content; this asserts it about what actually ran here.
	if revived == 0 || refused == 0 {
		t.Fatalf("%d actions revived and %d were refused; the guard needs both to be reachable", revived, refused)
	}
}

// TestARevivalNeedsTheActionToLiftHimOffTheFloor covers the second condition on
// the way back.
//
// Being ALLOWED on a corpse and actually reviving one are different things: an
// action permitted on a dead pet that failed to move the stat which killed him
// has not brought anybody round, and clearing died_at anyway would leave a pet
// reported alive whose health is still zero — dead again on the very next read,
// with the recorded moment of death now a lie.
//
// The guard is exercised directly rather than through Act, and deliberately not
// by adding a catalogue entry that exists only for tests: content.go is
// production content. Whether the shipped numbers can reach this branch end to
// end is a separate question, and it is asked below rather than assumed.
func TestARevivalNeedsTheActionToLiftHimOffTheFloor(t *testing.T) {
	hpDef := mustStat(t, StatHP)

	if revives([]StatRow{{Key: StatHP, Value: hpDef.Min}}) {
		t.Error("rows leaving the fatal stat on its floor were treated as a revival")
	}
	if !revives([]StatRow{{Key: StatHP, Value: hpDef.Min + 1}}) {
		t.Error("rows lifting the fatal stat off its floor were not treated as a revival")
	}
	// A non-fatal stat on its floor is not a death and must not block one being
	// undone — the bladder sits on its floor every time he relieves himself.
	if !revives([]StatRow{{Key: StatBladder, Value: mustStat(t, StatBladder).Min}, {Key: StatHP, Value: hpDef.Min + 1}}) {
		t.Error("a non-fatal stat resting on its floor was mistaken for a death")
	}
	// A key the catalogue no longer defines cannot decide the question either
	// way: it is unrenderable, so it is ignored, the same rule the read path uses.
	if !revives([]StatRow{{Key: "mood", Value: 0}}) {
		t.Error("a stored row the catalogue does not define blocked a revival")
	}

	// Reachability, stated rather than assumed. With the shipped numbers every
	// reviving action raises hp well clear of its floor, so Act's `revives`
	// refusal cannot currently be produced end to end; the guard is unit-tested
	// above instead of being faked with test-only content.
	for _, a := range Content().Actions {
		if !a.RevivesFatal {
			continue
		}
		if hpDef.Clamp(hpDef.Min+effectOn(a, StatHP)) <= hpDef.Min {
			t.Logf("%q revives but lifts hp to %v, so Act's `revives` refusal is now reachable end to end and deserves a service-level test",
				a.Key, hpDef.Clamp(hpDef.Min+effectOn(a, StatHP)))
		}
	}
}

// TestAStoredStatTheCatalogueNoLongerDefinesIsLeftOut covers retiring a stat.
// The client resolves every key against the config, so a key the config does not
// mention is unrenderable — and shipping it anyway would put a value on screen
// that only content can give meaning to. The row itself stays: a read must not
// destroy data it merely cannot draw.
func TestAStoredStatTheCatalogueNoLongerDefinesIsLeftOut(t *testing.T) {
	const retired = "mood"
	now := time.Now().UTC()
	rows := append([]StatRow{
		{Key: StatHP, Value: 80, AsOf: now},
		{Key: retired, Value: 50, AsOf: now},
	}, calmNeeds(t, now, 0)...)
	repo := playedFor(rows...)

	st, err := petService(repo).State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	for _, v := range st.Stats {
		if v.Key == retired {
			t.Fatalf("a stat the catalogue does not define was sent to a client that cannot render it: %+v", v)
		}
	}
	if len(st.Stats) != len(Content().Stats) {
		t.Fatalf("response carries %d stats; want the catalogue's %d: %+v", len(st.Stats), len(Content().Stats), st.Stats)
	}
	if _, ok := repo.row(retired); !ok {
		t.Error("the retired row was deleted; a read must not destroy data it merely cannot draw")
	}
}

// TestAStatAddedAfterAPetExistsIsSeededOnTheNextRead is what makes "adding a
// stat is a catalogue entry" true for the pets that already exist, rather than
// only for the ones created afterwards. No migration backfills anything: the
// first read that notices a gap fills it.
func TestAStatAddedAfterAPetExistsIsSeededOnTheNextRead(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	const age = 1.0 // hours
	asOf := time.Now().UTC().Add(-time.Duration(age * float64(time.Hour)))
	stored := hpDef.Min + 50

	// One row only: a pet created before the other stats were content.
	repo := playedFor(StatRow{Key: StatHP, Value: stored, AsOf: asOf})

	st, err := petService(repo).State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	for _, def := range Content().Stats {
		if def.Key == StatHP {
			continue
		}
		if got := statOf(t, st, def.Key).Value; got != def.Start {
			t.Errorf("the newly seeded %q reads %v; want its start %v", def.Key, got, def.Start)
		}
		if _, ok := repo.row(def.Key); !ok {
			t.Errorf("the missing stat %q was not written down; every later read would seed it again", def.Key)
		}
	}

	// And the row that was already there is untouched. Seeding is ON CONFLICT DO
	// NOTHING rather than a write, or adding a stat to the catalogue would reset
	// every existing pet's hp to full.
	row, _ := repo.row(StatHP)
	if row.Value != stored || !row.AsOf.Equal(asOf) {
		t.Errorf("seeding rewrote the existing row: %+v; want (%v, %v)", row, stored, asOf)
	}
	// A stat seeded at THIS instant cannot have crossed anything yet, so the
	// pre-existing hp has only its base rate applied to it.
	nearlyStat(t, statOf(t, st, StatHP).Value, stored-hpDef.DecayPerHour*age, "the pre-existing hp")
}

// planeMember is the caller as the transport reports it, for the tests below
// that ask what the plane would draw. One connection is enough: the roster is
// built per ACCOUNT, and how several connections of one account collapse into
// one entity is service_test.go's subject rather than this file's.
var planeMember = []realtime.Member{{ConnID: "c-1", AccountID: testAccount}}

// TestAnHTTPReadRefreshesWhatThePlaneDraws is the seam that keeps Postgres off
// the broadcast tick.
//
// The tick reads nothing, so the appearance it publishes is only ever as fresh
// as the last human-paced event that filled the cache — a hello on a new socket,
// or its owner acting over HTTP. This is the second of those, and it is the one
// that runs while the socket is already open: a player who renames his Ваня or
// drinks himself into trouble has to become visible to the yard without anybody
// reconnecting.
//
// Asserted through place, which is the real render path, rather than by reading
// the cache map — what matters is the frame the other players would receive.
func TestAnHTTPReadRefreshesWhatThePlaneDraws(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	name := "Ваня"
	asOf := time.Now().UTC()

	// Health inside its warning range, with both needs parked at the far end of
	// their own so nothing else is in play: the pose under test is the one the
	// fatal stat alone decides.
	rows := append([]StatRow{{Key: StatHP, Value: (hpDef.Min + hpDef.WarnAt) / 2, AsOf: asOf}},
		calmNeeds(t, asOf, 0)...)
	repo := playedFor(rows...)
	repo.pet.Name = &name
	svc := petService(repo)

	// Before the read the plane knows nothing about him, and still draws him:
	// catalogue default, no label, no trouble.
	// place answers with the entities AND how many of them are people, and one
	// connected account is one of each: the NPCs are appended by the broadcast
	// rather than here, and there is nobody asleep in the yard.
	before, here := svc.place(planeMember, asOf)
	if len(before) != 1 || here != 1 {
		t.Fatalf("an account with no pet read yet produced %d entities and a head count of %d; want 1 of each: %+v",
			len(before), here, before)
	}
	if before[0].Art != Content().DefaultSkin || before[0].Label != "" || before[0].Pose != PoseFine {
		t.Fatalf("before any read the plane drew %+v; want the catalogue default skin, no label and no trouble", before[0])
	}

	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}

	reads := repo.statReads
	after, here := svc.place(planeMember, asOf)
	if len(after) != 1 || here != 1 {
		t.Fatalf("the roster carries %d entities and a head count of %d after a read; want 1 of each: %+v",
			len(after), here, after)
	}
	got := after[0]
	if got.Art != repo.pet.SkinKey {
		t.Errorf("the plane draws %q; want the pet's own skin %q", got.Art, repo.pet.SkinKey)
	}
	if got.Label != name {
		t.Errorf("the plane labels him %q; want %q — a named Ваня is what stops the yard being anonymous dots", got.Label, name)
	}
	if got.Pose != PosePoorly {
		t.Errorf("the plane draws him %q; want %q — his health is inside the catalogue's own warning range", got.Pose, PosePoorly)
	}
	// And it drew all of that without asking the database anything. That is the
	// whole point of the cache: at five frames a second a query per tick would be
	// the traffic this design does not have.
	if repo.statReads != reads {
		t.Errorf("rendering the plane made %d further reads of the stored stats; want none", repo.statReads-reads)
	}
}

// TestAReadThatRecordsADeathLeavesThePlaneShowingHimDead closes the one gap
// between the two halves of that refresh.
//
// A read both computes a death and writes it down, so the cache is filled twice
// on that path: once before the write and once after it. Only the second one
// carries the recorded instant — and without it the plane would hold an entry
// that says "alive, with these numbers" about a pet the database has just
// recorded as dead. He would still be DRAWN dead today, because his health is on
// its floor, but the two would disagree about why; the moment an action lifts
// him off that floor without the cache being told, the disagreement becomes a
// visible one.
func TestAReadThatRecordsADeathLeavesThePlaneShowingHimDead(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	asOf := time.Now().UTC().Add(-time.Hour)
	rows := append([]StatRow{{Key: StatHP, Value: hpDef.Min, AsOf: asOf}}, calmNeeds(t, asOf, 1)...)
	repo := playedFor(rows...)
	svc := petService(repo)

	st, err := svc.State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if st.Alive || st.Pet.DiedAt == nil {
		t.Fatalf("a pet whose health ran out an hour ago read back alive: %+v", st.Pet)
	}
	if len(repo.died) != 1 {
		t.Fatalf("%d deaths written; want exactly one — this test is about the cache the write leaves behind", len(repo.died))
	}

	svc.mu.Lock()
	entry, cached := svc.display[testAccount]
	svc.mu.Unlock()
	if !cached {
		t.Fatal("the read cached nothing at all for the account it just answered for")
	}
	if entry.diedAt == nil || !entry.diedAt.Equal(*st.Pet.DiedAt) {
		t.Fatalf("the plane's copy records the death as %v; want the instant just written down, %v — the cache was filled before the write and never refreshed",
			entry.diedAt, st.Pet.DiedAt)
	}

	peers, here := svc.place(planeMember, time.Now().UTC())
	if len(peers) != 1 || here != 1 {
		t.Fatalf("the roster carries %d entities and a head count of %d; want 1 of each: %+v", len(peers), here, peers)
	}
	if peers[0].Pose != PoseDead {
		t.Fatalf("the plane draws a dead Ваня as %q; want %q", peers[0].Pose, PoseDead)
	}
}

// AppendEvents records the batch, assigning sequence numbers the way the real
// table's statement does — from the current maximum, in the order given.
func (f *fakeRepo) AppendEvents(_ context.Context, _ db.DBTX, _ string, verbs []string, at time.Time) ([]Event, error) {
	if f.appendErr != nil {
		return nil, f.appendErr
	}
	out := make([]Event, 0, len(verbs))
	for _, verb := range verbs {
		e := Event{Seq: int64(len(f.appended) + 1), Verb: verb, At: at}
		f.appended = append(f.appended, e)
		out = append(out, e)
	}
	return out, nil
}

// Events replays what was appended, oldest first.
func (f *fakeRepo) Events(_ context.Context, _ db.DBTX, _ string) ([]Event, error) {
	return append([]Event(nil), f.appended...), nil
}
