package gamevanyagotchi

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// The durable half, driven against an in-memory repository.
//
// Everything interesting about it is decided in the service rather than in SQL:
// which stats a response carries and in what order, when a death is written down
// and what instant it carries, what a missing row does, what an action re-stamps.
// The integration suite proves the queries; this file proves the decisions, and
// it does so without a container so it can run on every commit.

// testAccount is the caller every test here acts as. One account is one Ваня, so
// nothing in this file needs a second.
const testAccount = "acct-1"

// petBorn is the creation time the fake stamps on a pet. Fixed so a failure
// reads the same on every run; nothing under test depends on its value.
var petBorn = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory Repository that mirrors the SQL's semantics where
// they matter: SeedStats leaves an existing row alone (ON CONFLICT DO NOTHING),
// SetStat upserts, and MarkDied writes only while died_at is null — which is the
// guard that makes a death recorded exactly once however many readers observe
// it. It also counts calls, because "how many times was this called" is half of
// what the tests below are asserting.
type fakeRepo struct {
	pet  *Pet
	rows []StatRow

	ensured int
	seeded  int
	sets    int
	revived int
	// markDiedCalls counts every call; died records the instant of the calls that
	// actually wrote. Both are needed: a second read must not even ask.
	markDiedCalls int
	died          []time.Time
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

func (f *fakeRepo) Stats(_ context.Context, _ db.DBTX, _ string) ([]StatRow, error) {
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

func (f *fakeRepo) SetStat(_ context.Context, _ db.DBTX, _, statKey string, value float64, asOf time.Time) error {
	f.sets++
	for i := range f.rows {
		if f.rows[i].Key == statKey {
			f.rows[i].Value, f.rows[i].AsOf = value, asOf
			return nil
		}
	}
	f.rows = append(f.rows, StatRow{Key: statKey, Value: value, AsOf: asOf})
	return nil
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
		rows: rows,
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

// nearlyStat fails unless got is want, allowing for the fraction of a point the
// stat decays between the test reading the clock and the service reading its
// own. A twentieth of a point is about a minute of scheduling delay at the
// catalogue's shipped rates, and two orders of magnitude below any difference
// these tests are looking for.
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
// a feature anybody built: it is what the stored pair already means. Both
// directions are asserted in one read because they share the one expression — a
// stat that fills is a negative rate and nothing else.
func TestAStatStoredInThePastReadsBackDecayed(t *testing.T) {
	const away = 2.0 // hours
	stored := time.Now().UTC().Add(-time.Duration(away * float64(time.Hour)))
	hpDef, bladderDef := mustStat(t, StatHP), mustStat(t, StatBladder)

	repo := playedFor(
		StatRow{Key: StatHP, Value: hpDef.Max, AsOf: stored},
		StatRow{Key: StatBladder, Value: bladderDef.Min, AsOf: stored},
	)
	st, err := petService(repo).State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	hp, bladder := statOf(t, st, StatHP), statOf(t, st, StatBladder)
	// The expectation is re-derived from the definition rather than taken from
	// Stat.At, so this fails if the service reads the pair with the wrong sign,
	// the wrong unit, or not at all.
	nearlyStat(t, hp.Value, hpDef.Max-hpDef.DecayPerHour*away, "hp after two hours away")
	nearlyStat(t, bladder.Value, bladderDef.Min-bladderDef.DecayPerHour*away, "bladder after two hours away")
	if hp.Value >= hpDef.Max {
		t.Errorf("hp did not move at all in two hours: %v", hp.Value)
	}
	if bladder.Value <= bladderDef.Min {
		t.Errorf("bladder did not fill at all in two hours: %v", bladder.Value)
	}

	// The pair the value was decayed FROM is sent alongside it, so the client can
	// keep the bar moving between fetches without asking again.
	if !hp.AsOf.Equal(stored) {
		t.Errorf("hp as_of = %v; want the stored instant %v", hp.AsOf, stored)
	}

	// Reading is not writing: a read that re-stamped as_of would hand every
	// player a free pause by opening the app.
	if repo.sets != 0 {
		t.Errorf("a plain read wrote %d stat rows; want none", repo.sets)
	}
	row, _ := repo.row(StatHP)
	if row.Value != hpDef.Max || !row.AsOf.Equal(stored) {
		t.Errorf("the stored pair changed on a read: %+v", row)
	}
}

// TestAVerbThatIsNotInTheCatalogueIsRejectedAndTouchesNothing covers the stale
// or probing client. It has to be a clean refusal, and — because the catalogue
// lookup happens before anything else — it must not create a pet or write a row
// for an account that only ever sent nonsense.
func TestAVerbThatIsNotInTheCatalogueIsRejectedAndTouchesNothing(t *testing.T) {
	for _, key := range []string{"", "sudo-heal", StatHP, "HEAL"} {
		t.Run("action "+key, func(t *testing.T) {
			repo := &fakeRepo{}
			if _, err := petService(repo).Act(context.Background(), testAccount, key); !errors.Is(err, ErrUnknownAction) {
				t.Fatalf("Act(%q) error = %v; want ErrUnknownAction", key, err)
			}
			if repo.ensured != 0 || repo.sets != 0 || repo.seeded != 0 {
				t.Fatalf("a rejected verb still touched storage: ensured=%d sets=%d seeded=%d",
					repo.ensured, repo.sets, repo.seeded)
			}
		})
	}
}

// TestActAppliesTheDeltaAndRestampsTheStat is the action loop itself. The client
// sends a verb and never a value, so the server's own arithmetic is the only
// thing standing between "a dose helped" and "a client set its hp to a thousand"
// — and the re-stamp is what makes the next read decay from the new value rather
// than from the old one.
func TestActAppliesTheDeltaAndRestampsTheStat(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	heal, ok := ActionByKey(ActionHeal)
	if !ok {
		t.Fatalf("the catalogue has no %q action", ActionHeal)
	}

	for _, tc := range []struct {
		name   string
		stored float64
		ageHrs float64
	}{
		{name: "an ordinary dose", stored: hpDef.Min + 40, ageHrs: 1},
		// A point below the ceiling, so "did nothing at all" is distinguishable
		// from "clamped": the value has to move, and it has to stop at the top.
		{name: "a dose that would overflow the ceiling", stored: hpDef.Max - 1, ageHrs: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asOf := time.Now().UTC().Add(-time.Duration(tc.ageHrs * float64(time.Hour)))
			repo := playedFor(
				StatRow{Key: StatHP, Value: tc.stored, AsOf: asOf},
				StatRow{Key: StatBladder, Value: 10, AsOf: asOf},
			)

			// The delta lands on the DECAYED value, not on the stored one, and
			// the sum is clamped — the ceiling case is the second row above.
			raw := tc.stored - hpDef.DecayPerHour*tc.ageHrs + heal.Delta
			want := math.Min(hpDef.Max, math.Max(hpDef.Min, raw))

			before := time.Now().UTC()
			st, err := petService(repo).Act(context.Background(), testAccount, ActionHeal)
			after := time.Now().UTC()
			if err != nil {
				t.Fatalf("Act: %v", err)
			}

			nearlyStat(t, statOf(t, st, StatHP).Value, want, "hp in the response")
			row, found := repo.row(StatHP)
			if !found {
				t.Fatal("the action wrote no hp row at all")
			}
			nearlyStat(t, row.Value, want, "the stored hp value")
			if raw > hpDef.Max && row.Value > hpDef.Max {
				t.Errorf("the stored value %v is above the ceiling %v; the delta was not clamped", row.Value, hpDef.Max)
			}

			// Re-stamped to the instant of the action, which is what stops the
			// next read decaying the new value from the old as_of and charging
			// the player twice for the same hours.
			if row.AsOf.Before(before) || row.AsOf.After(after) {
				t.Errorf("as_of = %v; want the instant of the action, within [%v, %v]", row.AsOf, before, after)
			}

			// One action moves one stat.
			if repo.sets != 1 {
				t.Errorf("SetStat called %d times for one action; want once", repo.sets)
			}
			if b, _ := repo.row(StatBladder); b.Value != 10 || !b.AsOf.Equal(asOf) {
				t.Errorf("healing also moved the bladder row: %+v", b)
			}
		})
	}
}

// TestADeathIsRecordedOnceAtTheMomentItHappened is the property the whole
// died_at design exists for. A read arriving hours late has to record the moment
// he DIED rather than the moment somebody looked — that instant is derivable
// from the stored pair alone, which is what makes it identical for every reader
// and therefore safe to write without a lock.
func TestADeathIsRecordedOnceAtTheMomentItHappened(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	now := time.Now().UTC()
	// Stored twenty hours ago with exactly ten hours of hp left in it: he has
	// been dead for ten hours and nobody has looked since.
	asOf := now.Add(-20 * time.Hour)
	repo := playedFor(
		StatRow{Key: StatHP, Value: hpDef.Min + hpDef.DecayPerHour*10, AsOf: asOf},
		StatRow{Key: StatBladder, Value: 50, AsOf: asOf},
	)
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
	// is ten hours in the past however long it took anybody to notice.
	if len(repo.died) != 1 {
		t.Fatalf("%d deaths written; want exactly one", len(repo.died))
	}
	wantDied := asOf.Add(10 * time.Hour)
	if d := repo.died[0].Sub(wantDied); d < -time.Second || d > time.Second {
		t.Errorf("death recorded at %v; want the derived instant %v (off by %v)", repo.died[0], wantDied, d)
	}
	if !repo.died[0].Before(now.Add(-9 * time.Hour)) {
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

// TestHealingBringsADeadPetBack is why death here is a fright rather than an
// ending. An irreversible loss in a friend group is how a player leaves for
// good; what a death actually costs is the scare and whatever decayed while
// nobody was looking.
func TestHealingBringsADeadPetBack(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	heal, _ := ActionByKey(ActionHeal)
	asOf := time.Now().UTC().Add(-time.Hour)
	repo := playedFor(
		StatRow{Key: StatHP, Value: hpDef.Min, AsOf: asOf},
		StatRow{Key: StatBladder, Value: 20, AsOf: asOf},
	)
	svc := petService(repo)

	dead, err := svc.State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if dead.Alive {
		t.Fatal("a pet at the fatal floor read back alive")
	}

	st, err := svc.Act(context.Background(), testAccount, ActionHeal)
	if err != nil {
		t.Fatalf("Act(%q) on a dead pet: %v", ActionHeal, err)
	}
	if !st.Alive || st.Pet.DiedAt != nil {
		t.Fatalf("he is still dead after the one action that revives him: alive=%v died_at=%v", st.Alive, st.Pet.DiedAt)
	}
	if repo.revived != 1 {
		t.Errorf("Revive called %d times; want once", repo.revived)
	}
	nearlyStat(t, statOf(t, st, StatHP).Value,
		math.Min(hpDef.Max, hpDef.Min+heal.Delta), "hp after being brought round")
	if repo.pet.DiedAt != nil {
		t.Errorf("the death is still stored after a revive: %v", repo.pet.DiedAt)
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
	repo := playedFor(
		StatRow{Key: StatHP, Value: 80, AsOf: now},
		StatRow{Key: StatBladder, Value: 10, AsOf: now},
		StatRow{Key: retired, Value: 50, AsOf: now},
	)

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
	hpDef, bladderDef := mustStat(t, StatHP), mustStat(t, StatBladder)
	const age = 1.0 // hours
	asOf := time.Now().UTC().Add(-time.Duration(age * float64(time.Hour)))
	stored := hpDef.Min + 50

	// One row only: a pet created before the second stat was content.
	repo := playedFor(StatRow{Key: StatHP, Value: stored, AsOf: asOf})

	st, err := petService(repo).State(context.Background(), testAccount)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	if got := statOf(t, st, StatBladder).Value; got != bladderDef.Start {
		t.Errorf("the newly seeded %q reads %v; want its start %v", StatBladder, got, bladderDef.Start)
	}
	if _, ok := repo.row(StatBladder); !ok {
		t.Error("the missing stat was not written down; every later read would seed it again")
	}

	// And the row that was already there is untouched. Seeding is ON CONFLICT DO
	// NOTHING rather than a write, or adding a stat to the catalogue would reset
	// every existing pet's hp to full.
	row, _ := repo.row(StatHP)
	if row.Value != stored || !row.AsOf.Equal(asOf) {
		t.Errorf("seeding rewrote the existing row: %+v; want (%v, %v)", row, stored, asOf)
	}
	nearlyStat(t, statOf(t, st, StatHP).Value, stored-hpDef.DecayPerHour*age, "the pre-existing hp")
}

// TestADeadPetAcceptsOnlyAnActionThatCanRevive covers Act's death guard.
//
// Half of that guard is UNREACHABLE through the shipped catalogue: every action
// in content.go sets RevivesFatal, so `!before.Alive && !action.RevivesFatal`
// cannot be true today and ErrPetDead cannot currently be produced. A test-only
// action is deliberately not added to the catalogue to reach it — content.go is
// production content and production content does not carry test fixtures. So
// what is asserted unconditionally is the reachable half, and the loop below
// arms itself the moment the catalogue gains an action that does not revive.
func TestADeadPetAcceptsOnlyAnActionThatCanRevive(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	// deadPet is a pet whose hp ran out an hour ago, rebuilt per case so each
	// one drives a fresh death.
	deadPet := func() *fakeRepo {
		asOf := time.Now().UTC().Add(-time.Hour)
		return playedFor(
			StatRow{Key: StatHP, Value: hpDef.Min, AsOf: asOf},
			StatRow{Key: StatBladder, Value: 20, AsOf: asOf},
		)
	}

	// The reachable half: the action that revives is allowed through.
	repo := deadPet()
	st, err := petService(repo).Act(context.Background(), testAccount, ActionHeal)
	if err != nil {
		t.Fatalf("Act(%q) on a dead pet = %v; the one action that revives must be allowed", ActionHeal, err)
	}
	if !st.Alive {
		t.Fatal("the reviving action was accepted but he is still dead")
	}

	// The other half, for whenever it becomes reachable.
	armed := false
	for _, a := range Content().Actions {
		if a.RevivesFatal {
			continue
		}
		armed = true
		t.Run(a.Key+" is refused on a dead pet", func(t *testing.T) {
			r := deadPet()
			if _, err := petService(r).Act(context.Background(), testAccount, a.Key); !errors.Is(err, ErrPetDead) {
				t.Fatalf("Act(%q) on a dead pet error = %v; want ErrPetDead", a.Key, err)
			}
			if r.sets != 0 {
				t.Errorf("%q wrote %d stat rows on a dead pet; want none", a.Key, r.sets)
			}
		})
	}
	if !armed {
		t.Logf("every catalogue action revives, so ErrPetDead is unreachable by construction today; "+
			"this test starts exercising it as soon as an action has revives_fatal=false (%d actions checked)",
			len(Content().Actions))
	}
}
