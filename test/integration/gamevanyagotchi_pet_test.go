//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/gamevanyagotchi"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/settings"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/jackc/pgx/v5/pgconn"
)

// The durable half of «Ванягоччи», against a real PostgreSQL.
//
// Everything this file guards is a property the database owns rather than the
// Go code: the pet is created exactly once however many tabs ask for it, the
// decay is computed from stored (value, as_of) pairs rather than accumulated by
// anything that ticks, an action re-stamps every pair at one shared instant, the
// moment of death is the derived instant rather than the moment somebody looked,
// and the world-object table's singleton invariant is enforced by an index
// rather than by a rule written in Go. None of that can be proved with a fake
// repository, which is why these live here and not in the package's unit tests.
//
// EVERY EXPECTATION IS DERIVED FROM THE CATALOGUE, NEVER WRITTEN DOWN. The rates
// in content.go are explicitly meant to be moved by feel, so a test that pinned
// literals would report every tuning change as a regression and would be edited
// until it stopped meaning anything. Neither is any expectation obtained by
// calling the decay engine: the arithmetic below — a penalty's onset, a value
// after an absence, the instant health reaches its floor — is derived here from
// the catalogue's own fields, because a test that asked the implementation what
// it thought would agree with itself whatever it computed.
//
// UID namespace: 72xx. 71xx and 73xx belong to the realtime tests in
// gamevanyagotchi_test.go, and the database is shared across the whole package.
//
// A handful of the helpers below read the pets table on the PLANE's behalf —
// where a pet was last standing, and whether anything about it has been written
// at all. They live here rather than next to those tests because this is the
// file that owns raw SQL against this game's tables, and because the plane's own
// file is about frames on a socket.

// petStatEpsilon is the tolerance for a decayed value in these tests.
//
// The decay is exact — one subtraction over a stored timestamp, plus one more
// for each unmet need — so the only slack needed is the wall-clock gap between
// the SQL now() that backdates a row and the server's own now() a few
// milliseconds later. Even at the fastest drain the catalogue can produce (the
// base rate with every penalty switched on, thirteen an hour today) 0.5 is over
// two minutes of clock skew: far more than any test run can accumulate, and far
// tighter than any real bug would hide inside.
const petStatEpsilon = 0.5

// petBuildApp builds the app with «Ванягоччи» wired to the real pool, and hands
// back the game alongside the handler that serves it.
//
// Its own builder rather than the package's shared one because this file is
// only interested in the durable half of the game: the transport is nil, no hub
// runs, and nothing here publishes a roster. The pet path never touches the
// transport, which is precisely the separation the service's own doc comment
// claims — so passing nil is a small assertion of that in itself.
//
// The service itself comes back because verbs travel over the socket now, and
// these tests are about what a verb does to a pet in a real Postgres rather than
// about how it arrived — so they call Service.Do, which is the single funnel the
// socket handler itself goes through. Driving a websocket here would test the
// transport twice and the durable half not at all.
//
// The transport is a parameter because ONE verb broke the separation that
// paragraph describes. Winning a contested claim paints the yard — the winner's
// Ваня is drawn happy and everybody else's sad — and that reads the room off the
// transport, on the pet path, after the transaction has committed. So a test that
// presses that verb has to hand over something to read the room from; every other
// test in this file still passes nil, which keeps stating the separation for the
// rest of the durable half.
func petBuildApp(vkBaseURL string, transport gamevanyagotchi.Transport) (http.Handler, *gamevanyagotchi.Service) {
	cfg := config.Config{
		Env: "dev",
		VK:  config.VK{AppID: "app-1", ServiceToken: "svc", RedirectURI: vkRedirect, BaseURL: vkBaseURL},
	}
	game := gamevanyagotchi.NewService(transport, httpapi.DefaultRoom, pool, gamevanyagotchi.NewPostgresRepository(), newAccountService())
	h := httpapi.NewServer(httpapi.Deps{
		Config:          cfg,
		Pool:            pool,
		WebFS:           fstest.MapFS{"index.html": {Data: []byte("<html>psycho</html>")}},
		VK:              vk.New(vkBaseURL, "app-1", "svc", vkRedirect),
		Accounts:        newAccountService(),
		Sessions:        session.NewManager(pool, key(3), time.Hour, false),
		GameVanyagotchi: game,
		Settings:        settings.NewService(pool),
	}).Handler()
	return observability.WrapHandler(h, "http.server"), game
}

// petApp starts a server for one test and returns it with the game it serves. A
// test that only reads takes the server alone; a test that presses a verb needs
// both.
func petApp(t *testing.T) (*httptest.Server, *gamevanyagotchi.Service) {
	t.Helper()
	vkSrv := fakeVKDynamic()
	t.Cleanup(vkSrv.Close)
	h, game := petBuildApp(vkSrv.URL, petYardTransport{})
	app := httptest.NewServer(h)
	t.Cleanup(app.Close)
	return app, game
}

// petYardTransport is the hub as this file needs it, and nothing more.
//
// It is deliberately inert, and it used to be optional: for a long time the
// durable half of the game touched no transport at all, so most tests here were
// built with none. Two things now reach one on the pet's own path — a won claim
// paints every face in the yard, and a hello answers the connection that sent it
// — and both are in-memory, cosmetic or unicast, with unit tests of their own in
// internal/gamevanyagotchi. Nothing this file asserts is about either.
//
// Reporting an empty room is therefore the honest answer rather than a stub:
// nobody has a socket open here, and the service under test has no hub running.
type petYardTransport struct{}

func (petYardTransport) Publish(context.Context, string, []byte) error { return nil }

func (petYardTransport) PublishTo(context.Context, string, []byte) error { return nil }

func (petYardTransport) Members(context.Context, string) ([]realtime.Member, error) {
	return nil, nil
}

// petObjectKind is the catalogue entry a test is reasoning about, fetched rather
// than written down for the same reason petAction and petStat are.
func petObjectKind(t *testing.T, key string) gamevanyagotchi.ObjectKind {
	t.Helper()
	k, ok := gamevanyagotchi.ObjectKindByKey(key)
	if !ok {
		t.Fatalf("the catalogue has no object kind %q", key)
	}
	return k
}

// petStandAtTheBeerStore puts an account at the crate, with a crate to be at.
//
// THE FIXTURE EVERY TEST THAT DRINKS NOW NEEDS, and that is the rule change
// rather than a testing inconvenience: beer comes out of a crate, so a Ваня who
// is not standing at one cannot have any. It drives the two real client frames
// rather than reaching into the service, because both of them are the honest
// path and neither needs a socket.
//
// The hello is the human-paced moment the yard reads the world, and the one that
// stands a crate up when the world has none — so this also guarantees there is
// beer to draw, whatever an earlier test drank. The tap is a teleport rather
// than a walk, and deliberately: no broadcast has ever run in this suite, so the
// yard has no clock to measure a journey against and puts him straight where he
// asked. The walk has its own tests in internal/gamevanyagotchi; what this file
// needs is somebody standing in the right place.
func petStandAtTheBeerStore(t *testing.T, game *gamevanyagotchi.Service, accountID string) {
	t.Helper()
	crate := petObjectKind(t, gamevanyagotchi.KindCrate)
	if crate.At == nil {
		t.Fatalf("the catalogue no longer gives %q a pitch of its own; there is nowhere to stand", crate.Key)
	}
	m := realtime.Member{ConnID: "conn-" + accountID, AccountID: accountID}
	game.HandleInbound(context.Background(), m, httpapi.DefaultRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	game.HandleInbound(context.Background(), m, httpapi.DefaultRoom,
		fmt.Appendf(nil, `{"t":"vanyagotchi_move","x":%v,"y":%v}`, crate.At.X, crate.At.Y))
}

// petDo presses a batch of verbs the way an inbound socket frame does, and fails
// the test on a refusal.
//
// The instant is passed because Do takes one: the read, the fold, the write and
// the answer then all happen at a single moment, and that moment is what every
// backdated as_of in this file is measured against. A verb that took its own
// clock somewhere inside would make the arithmetic below true only by accident.
//
// What this deliberately does NOT reproduce is the wrapper handleVerbs puts
// around the same call. The per-account one-verb-a-second bound (allowVerb) and
// the line over his head (Say) are properties of the plane in memory rather than
// of the pet in Postgres, they have their own unit tests in
// internal/gamevanyagotchi/service_test.go, and reproducing them here would make
// every test below wait a second between presses to assert nothing new.
func petDo(t *testing.T, game *gamevanyagotchi.Service, accountID string, verbs ...string) gamevanyagotchi.State {
	t.Helper()
	st, err := game.Do(context.Background(), accountID, verbs, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("Do(%v) for account %s: %v", verbs, accountID, err)
	}
	return st
}

// petValue plucks one decayed stat value out of the state a verb answered with.
//
// A sibling of petStateValue rather than a replacement, because there are
// genuinely two shapes here and neither is the other's leftover: a READ still
// goes over HTTP and arrives as a map[string]any, while a verb now comes back as
// the typed State that Do returns.
func petValue(t *testing.T, st gamevanyagotchi.State, statKey string) float64 {
	t.Helper()
	for _, v := range st.Stats {
		if v.Key == statKey {
			return v.Value
		}
	}
	t.Fatalf("stat %s is missing from the state a verb answered with: %+v", statKey, st.Stats)
	return 0
}

// petRowCount counts an account's pet rows, INCLUDING any soft-deleted one.
//
// Deliberately unfiltered: the invariant under test is "one INSERT ever won",
// and filtering on deleted_at IS NULL would hide a second row the moment the
// game learns to retire a pet.
func petRowCount(t *testing.T, accountID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM game_vanyagotchi_pets WHERE account_id = $1::uuid`, accountID).Scan(&n); err != nil {
		t.Fatalf("count pets for %s: %v", accountID, err)
	}
	return n
}

// petID reads the account's living pet id.
func petID(t *testing.T, accountID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM game_vanyagotchi_pets WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountID).Scan(&id); err != nil {
		t.Fatalf("pet id for account %s: %v", accountID, err)
	}
	return id
}

// petStatRowCount counts a pet's stored stat rows for one key.
func petStatRowCount(t *testing.T, petIDs, statKey string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM game_vanyagotchi_pet_stats WHERE pet_id = $1::uuid AND stat_key = $2`,
		petIDs, statKey).Scan(&n); err != nil {
		t.Fatalf("count stat rows (%s): %v", statKey, err)
	}
	return n
}

// petStoredStat reads the stored pair the decay is computed from.
func petStoredStat(t *testing.T, petIDs, statKey string) (float64, time.Time) {
	t.Helper()
	var value float64
	var asOf time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT value, as_of FROM game_vanyagotchi_pet_stats
		  WHERE pet_id = $1::uuid AND stat_key = $2 AND deleted_at IS NULL`,
		petIDs, statKey).Scan(&value, &asOf); err != nil {
		t.Fatalf("stored stat %s: %v", statKey, err)
	}
	return value, asOf
}

// petBackdateAll pushes EVERY stat row of a pet into the past, in ONE statement.
//
// This is how time is travelled in these tests: there is nothing to advance,
// because nothing ticks — the whole decay engine is (value, as_of) read against
// now(), so moving as_of backwards is indistinguishable from having been away.
// The interval is built in SQL rather than in Go so the test never has to assume
// the container's clock agrees with the test process's.
//
// One statement, and that is the point rather than an optimisation. Health's
// drain is a function of the other stats' trajectories, so the arithmetic is
// only defined while every pair shares one as_of; backdating a row at a time
// would give each its own now() and quietly blur the very invariant these tests
// exist to hold. The row count is checked for the same reason — a pet that has
// not been materialised yet would be silently backdated by nothing at all.
func petBackdateAll(t *testing.T, petIDs string, d time.Duration) {
	t.Helper()
	tag, err := pool.Exec(context.Background(),
		`UPDATE game_vanyagotchi_pet_stats
		    SET as_of = now() - make_interval(secs => $2)
		  WHERE pet_id = $1::uuid`,
		petIDs, d.Seconds())
	if err != nil {
		t.Fatalf("backdate every stat by %s: %v", d, err)
	}
	if got, want := int(tag.RowsAffected()), len(gamevanyagotchi.Stats()); got != want {
		t.Fatalf("backdated %d stat rows, want all %d — the pet was not materialised, or a stat has no row", got, want)
	}
}

// petSharedAsOf asserts every catalogue stat's stored row carries one and the
// same instant, and returns it.
//
// The single assertion the coupled decay rests on. A write that re-stamps only
// the rows it moved leaves the others anchored in the past, and health's drain
// is then integrated over a window in which a driver's history is unknown —
// which does not fail loudly, it silently forgets damage.
func petSharedAsOf(t *testing.T, petIDs string) time.Time {
	t.Helper()
	var shared time.Time
	var sharedKey string
	for _, def := range gamevanyagotchi.Stats() {
		_, asOf := petStoredStat(t, petIDs, def.Key)
		if sharedKey == "" {
			shared, sharedKey = asOf, def.Key
			continue
		}
		if !asOf.Equal(shared) {
			t.Fatalf("%s is stamped %s but %s is stamped %s — every stat must share one as_of or the coupled decay has a window it cannot reconstruct",
				def.Key, asOf.UTC(), sharedKey, shared.UTC())
		}
	}
	return shared
}

// petRecent fails unless an instant is the one a write just stamped.
func petRecent(t *testing.T, what string, ts time.Time) {
	t.Helper()
	if age := time.Since(ts); age < -time.Second || age > time.Minute {
		t.Fatalf("%s is stamped %s (%s ago), want the instant of the write", what, ts.UTC(), age)
	}
}

// petStanding reads where a pet was last written down, and when.
//
// All three are NULL until a departure has been written: a position is presence,
// and until somebody has actually left there is nothing durable to say about it.
// Returned as pointers rather than zero values because "at the top-left corner
// at the zero time" and "never stood anywhere" are different answers and only
// one of them means the write happened.
func petStanding(t *testing.T, petIDs string) (*float64, *float64, *time.Time) {
	t.Helper()
	var x, y *float64
	var seen *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT x, y, last_seen_at FROM game_vanyagotchi_pets WHERE id = $1::uuid`,
		petIDs).Scan(&x, &y, &seen); err != nil {
		t.Fatalf("standing position of %s: %v", petIDs, err)
	}
	return x, y, seen
}

// petWaitStanding waits until a departure has been written down and returns it.
//
// A poll rather than a wait on anything, because the write is deliberately off
// the broadcast's own path: the tick notices somebody has gone and says so down
// a channel, and a goroutine of the game's does the writing. There is nothing
// from outside the process to synchronise with, so the condition itself is what
// is waited on — never a fixed sleep, which would be slower and would still be a
// race.
func petWaitStanding(t *testing.T, petIDs string) (float64, float64, time.Time) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		x, y, seen := petStanding(t, petIDs)
		if x != nil && y != nil && seen != nil {
			return *x, *y, *seen
		}
		if time.Now().After(deadline) {
			t.Fatalf("pet %s still has no position written down; a departure was never persisted", petIDs)
		}
		// Nothing inside the process is observable from out here to wait on, so
		// the condition itself is what is polled. The pause is BETWEEN polls
		// rather than instead of them: the loop returns the instant the row
		// appears.
		<-time.After(20 * time.Millisecond)
	}
}

// petSetName names a pet directly.
//
// There is no endpoint for it yet — naming happens in a dialog the SPA has not
// grown — so the row is written here rather than through a flow that does not
// exist. Direct setup rather than a test-only endpoint: production code carries
// no path that exists for a test's benefit.
func petSetName(t *testing.T, petIDs, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE game_vanyagotchi_pets SET name = $2 WHERE id = $1::uuid`, petIDs, name); err != nil {
		t.Fatalf("name pet %s: %v", petIDs, err)
	}
}

// petTouchedAt returns the most recent updated_at across a pet's own row and
// every one of its stat rows — one number standing for "has anything about this
// pet been written".
//
// Every write in this game's repository sets updated_at to now(), so a value
// that has not moved is a pet nothing has touched. That is a proxy rather than a
// proof, and a deliberate one: counting statements through pg_stat would be more
// literal, far more fragile, and would still be answering the same question.
func petTouchedAt(t *testing.T, petIDs string) time.Time {
	t.Helper()
	var at time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT greatest(
		          (SELECT updated_at FROM game_vanyagotchi_pets WHERE id = $1::uuid),
		          (SELECT max(updated_at) FROM game_vanyagotchi_pet_stats WHERE pet_id = $1::uuid))`,
		petIDs).Scan(&at); err != nil {
		t.Fatalf("last write to pet %s: %v", petIDs, err)
	}
	return at
}

// petDiedAt reads the recorded moment of death, nil while the pet is alive.
func petDiedAt(t *testing.T, petIDs string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT died_at FROM game_vanyagotchi_pets WHERE id = $1::uuid`, petIDs).Scan(&at); err != nil {
		t.Fatalf("died_at: %v", err)
	}
	return at
}

// petStateField plucks one numeric field of one stat out of a /state response.
func petStateField(t *testing.T, body map[string]any, statKey, field string) float64 {
	t.Helper()
	stats, ok := body["stats"].([]any)
	if !ok {
		t.Fatalf("state has no stats array: %v", body)
	}
	for _, s := range stats {
		row, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if row["key"] != statKey {
			continue
		}
		v, ok := row[field].(float64)
		if !ok {
			t.Fatalf("stat %s has no numeric %s: %v", statKey, field, row)
		}
		return v
	}
	t.Fatalf("stat %s missing from state: %v", statKey, body)
	return 0
}

// petStateValue plucks one decayed stat value out of a /state response.
func petStateValue(t *testing.T, body map[string]any, statKey string) float64 {
	t.Helper()
	return petStateField(t, body, statKey, "value")
}

// petStateRate plucks the drain a stat is currently suffering out of a /state
// response. Coupling included — it is generally not the catalogue's own rate.
func petStateRate(t *testing.T, body map[string]any, statKey string) float64 {
	t.Helper()
	return petStateField(t, body, statKey, "rate_per_hour")
}

// petNear fails unless got is within petStatEpsilon of want.
func petNear(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > petStatEpsilon {
		t.Fatalf("%s = %.4f, want %.4f (±%.2f)", what, got, want, petStatEpsilon)
	}
}

// petKeys collects the "key" field of every object in a config array, so a test
// can assert on the catalogue's contents without hardcoding its order.
func petKeys(t *testing.T, body map[string]any, field string) []string {
	t.Helper()
	raw, ok := body[field].([]any)
	if !ok {
		t.Fatalf("config has no %s array: %v", field, body)
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		row, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("%s entry is not an object: %v", field, e)
		}
		k, _ := row["key"].(string)
		out = append(out, k)
	}
	return out
}

func petHas(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The arithmetic, derived here rather than borrowed from the engine under test.
// ---------------------------------------------------------------------------

// petHours turns a count of hours into the duration the tests travel back by.
func petHours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

// petStat looks a stat up in the catalogue and fails the test if it has gone.
func petStat(t *testing.T, key string) gamevanyagotchi.Stat {
	t.Helper()
	def, ok := gamevanyagotchi.StatByKey(key)
	if !ok {
		t.Fatalf("stat %q has left the catalogue", key)
	}
	return def
}

// petAction looks an action up in the catalogue and fails the test if it has
// gone.
func petAction(t *testing.T, key string) gamevanyagotchi.Action {
	t.Helper()
	a, ok := gamevanyagotchi.ActionByKey(key)
	if !ok {
		t.Fatalf("action %q has left the catalogue", key)
	}
	return a
}

// petEffectOn is what one action moves one stat by, zero if it does not touch
// it. Summed rather than found, because nothing stops a verb naming a stat
// twice and the effects are applied in order.
func petEffectOn(a gamevanyagotchi.Action, statKey string) float64 {
	var delta float64
	for _, e := range a.Effects {
		if e.StatKey == statKey {
			delta += e.Delta
		}
	}
	return delta
}

// petClamp is the tests' own copy of the bounds check, so an expectation is
// never computed by the code it is meant to hold to account.
func petClamp(def gamevanyagotchi.Stat, v float64) float64 {
	return math.Min(def.Max, math.Max(def.Min, v))
}

// petPenaltyOn returns the penalty def suffers from one named driver.
func petPenaltyOn(t *testing.T, def gamevanyagotchi.Stat, driverKey string) gamevanyagotchi.Penalty {
	t.Helper()
	for _, p := range def.Penalties {
		if p.WhenKey == driverKey {
			return p
		}
	}
	t.Fatalf("%s carries no penalty driven by %s — the coupling these tests describe is gone", def.Key, driverKey)
	return gamevanyagotchi.Penalty{}
}

// petOnsetHours returns how long after a shared as_of a penalty begins to bite,
// given every stat stood at its catalogue start value at that instant.
//
// One instant is the whole answer because the driver is linear and monotone
// between writes: it either already qualifies, or it is heading towards the
// threshold and stays past it once it arrives. The rate is signed, so the same
// expression serves a need that drains and one that fills.
func petOnsetHours(t *testing.T, p gamevanyagotchi.Penalty) float64 {
	t.Helper()
	driver := petStat(t, p.WhenKey)
	holds := func(v float64) bool {
		if p.Above {
			return v >= p.Threshold
		}
		return v <= p.Threshold
	}
	if holds(driver.Start) {
		return 0
	}
	towards := (p.Above && driver.DecayPerHour < 0) || (!p.Above && driver.DecayPerHour > 0)
	if !towards {
		t.Fatalf("the penalty driven by %s never switches on from that stat's start value — these tests assume an untended pet eventually earns every penalty", p.WhenKey)
	}
	return (driver.Start - p.Threshold) / driver.DecayPerHour
}

// petFirstOnsetHours returns the earliest hour at which any of a stat's
// penalties switches on. A stat with no penalties never reaches one, and saying
// so with an infinity keeps the callers free of a special case.
func petFirstOnsetHours(t *testing.T, def gamevanyagotchi.Stat) float64 {
	t.Helper()
	first := math.Inf(1)
	for _, p := range def.Penalties {
		if h := petOnsetHours(t, p); h < first {
			first = h
		}
	}
	return first
}

// petValueAfter returns what a stat reads h hours after a shared as_of at which
// every stat stood at its catalogue start.
//
// The penalties apply as suffixes of the window — each from its own onset to the
// end — and the bounds are applied once at the end rather than per term, so a
// need that has been unmet long enough to bottom out its driver still costs
// exactly what it should.
func petValueAfter(t *testing.T, def gamevanyagotchi.Stat, h float64) float64 {
	t.Helper()
	v := def.Start - def.DecayPerHour*h
	for _, p := range def.Penalties {
		if onset := petOnsetHours(t, p); h > onset {
			v -= p.RatePerHour * (h - onset)
		}
	}
	return petClamp(def, v)
}

// petFatalHours returns how long a fatal stat takes to reach its floor from a
// shared as_of at the catalogue's starting values.
//
// With penalties the fall is piecewise-linear rather than linear: every onset is
// a step up in the drain, so the future is a run of constant-rate segments and
// the answer is the one the floor falls inside. Walked here from first
// principles because the instant this yields is the assertion — obtaining it
// from the engine would be asking the accused to write the verdict.
func petFatalHours(t *testing.T, def gamevanyagotchi.Stat) float64 {
	t.Helper()
	if !def.Fatal {
		t.Fatalf("stat %q cannot kill him, so it has no moment of death", def.Key)
	}
	type step struct{ at, rate float64 }
	steps := make([]step, 0, len(def.Penalties))
	for _, p := range def.Penalties {
		steps = append(steps, step{at: petOnsetHours(t, p), rate: p.RatePerHour})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].at < steps[j].at })

	at, rate, left := 0.0, def.DecayPerHour, def.Start-def.Min
	for _, s := range steps {
		if s.at > at {
			// A segment that is not draining cannot kill him, but time still
			// passes in it.
			if rate > 0 {
				if need := left / rate; need <= s.at-at {
					return at + need
				}
				left -= rate * (s.at - at)
			}
			at = s.at
		}
		rate += s.rate
	}
	if rate <= 0 {
		t.Fatalf("stat %q never drains fast enough to reach its floor", def.Key)
	}
	return at + left/rate
}

// ---------------------------------------------------------------------------
// The tests.
// ---------------------------------------------------------------------------

// TestVanyagotchiPetRoutesRejectACallerWithoutASession confirms every route in
// the group is behind requireAuth.
//
// Worth pinning per-route rather than trusting the r.Use in the route block: a
// handler moved out of the group by a later refactor would still answer, and a
// pet is durable per-account state — an unauthenticated caller reaching /state
// would be creating rows for whoever the handler thought they were.
func TestVanyagotchiPetRoutesRejectACallerWithoutASession(t *testing.T) {
	app, _ := petApp(t)
	anon := &http.Client{}

	// Both reads, and nothing else, because the group is two reads now: a verb's
	// gate is the socket handshake, and that gate is pinned in
	// test/integration/realtime_test.go — a pending account is refused the
	// upgrade, an expired session is refused, a blocked account is refused. So the
	// absence of a verb from this table is not verbs becoming ungated. What is
	// pinned HERE is narrower and still worth a test of its own: a pet is never
	// materialised for a caller the server cannot name.
	for _, path := range []string{
		"/api/game-vanyagotchi/config",
		"/api/game-vanyagotchi/state",
	} {
		s, body := doJSON(t, anon, http.MethodGet, app.URL+path, nil)
		if s != http.StatusUnauthorized || body["error"] != "unauthorized" {
			t.Fatalf("GET %s: status=%d body=%v; want 401 unauthorized", path, s, body)
		}
	}
}

// TestVanyagotchiPetRoutesRejectAnUnapprovedAccount confirms the allowlist gate
// reaches this game too.
//
// A live session is not enough anywhere in this application — approval is the
// other half of "authorized" — and a game added later is exactly the sort of
// route group that gets the session check and forgets the status one. The
// account is approved, then demoted to pending while its session stays valid,
// because that is the case the middleware actually has to catch: authorization
// is re-read on every request rather than frozen at login.
func TestVanyagotchiPetRoutesRejectAnUnapprovedAccount(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7210", "user")
	setRoleStatus(t, accountIDByUID(t, "7210"), "user", "pending")

	// The reads only, for the same reason as the test above: a verb no longer
	// arrives over HTTP, and the approval check a verb passes is the one at the
	// socket upgrade — asserted in test/integration/realtime_test.go, where a
	// pending account is refused the handshake outright.
	for _, path := range []string{"/api/game-vanyagotchi/config", "/api/game-vanyagotchi/state"} {
		s, body := doJSON(t, cli, http.MethodGet, app.URL+path, nil)
		if s != http.StatusForbidden || body["error"] != "not_approved" {
			t.Fatalf("GET %s: status=%d body=%v; want 403 not_approved", path, s, body)
		}
	}
}

// TestVanyagotchiActionRouteIsGone pins the ABSENCE of the HTTP verb route.
//
// A verb travels over the socket now (ADR-043) and the route that used to carry
// one is deleted. This is the same shape of test the `/api/game/*` alias removal
// left behind, and it exists because deleting something is not self-proving:
// nothing else in the suite fails if a future change quietly re-registers it,
// and a second way to press a button is exactly what the no-legacy rule forbids.
//
// IT HAS TO BE AUTHENTICATED, and that is the whole subtlety. The route group
// carries `requireAuth` as group middleware, so chi runs the middleware before
// it discovers there is no route — which means an anonymous request to a path
// that never existed and one to a path just deleted BOTH answer 401,
// indistinguishably. Only an approved caller gets far enough to be told 404. A
// version of this test without a session would pass against a fully restored
// route and prove nothing at all.
// EVERY VERB IN THE CATALOGUE, taken from the catalogue rather than listed. A
// hand-written list is a list that goes stale the next time a verb is added —
// «восстать из мертвых» was added and the list did not grow — and a route that
// answered for exactly the verb nobody remembered to name is the whole failure
// this test exists to prevent.
func TestVanyagotchiActionRouteIsGone(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7233", "user")

	actions := gamevanyagotchi.Content().Actions
	if len(actions) == 0 {
		t.Fatal("the catalogue has no actions, so this test asserts nothing about the deleted route")
	}
	for _, action := range actions {
		url := app.URL + "/api/game-vanyagotchi/actions/" + action.Key
		s, body := doJSON(t, cli, http.MethodPost, url, nil)
		if s != http.StatusNotFound {
			t.Fatalf("POST %s: status=%d body=%v; want 404 — the verb route is deleted and nothing may bring it back", url, s, body)
		}
	}
}

// TestVanyagotchiPetConfigServesTheWholeCatalogue confirms the client can render
// the game knowing nothing but this response.
//
// That is the property the catalogue exists for: every key, label, rate and
// bound is content served from the backend, so adding a stat or an action is a
// backend-only change with no migration and no client deploy. All three stats
// are checked by name because the game is now a causal story rather than a bar —
// the two needs the player acts on and the consequence they drive — and a client
// served only some of them would draw a health bar nobody could explain. A
// default that is not present in its own list would break a fresh pet's first
// render, which is why the two defaults are checked against the lists rather
// than merely for being non-empty.
func TestVanyagotchiPetConfigServesTheWholeCatalogue(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7201", "user")

	s, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/config", nil)
	if s != http.StatusOK {
		t.Fatalf("config: status=%d body=%v", s, cfg)
	}
	if cfg["game_key"] != gamevanyagotchi.GameKey {
		t.Fatalf("game_key = %v, want %q", cfg["game_key"], gamevanyagotchi.GameKey)
	}

	// The WHOLE catalogue, derived from the catalogue rather than named: a client
	// resolves every key against this response, so a stat or a verb the endpoint
	// leaves out is one the player has no way to see or press — and a hand-written
	// list here would have gone on passing while the two lifetime tallies and the
	// verb that undoes a death were missing from it.
	stats := petKeys(t, cfg, "stats")
	for _, def := range gamevanyagotchi.Stats() {
		if !petHas(stats, def.Key) {
			t.Fatalf("stats = %v, want it to include %q", stats, def.Key)
		}
	}
	actions := petKeys(t, cfg, "actions")
	for _, def := range gamevanyagotchi.Content().Actions {
		if !petHas(actions, def.Key) {
			t.Fatalf("actions = %v, want it to include %q", actions, def.Key)
		}
	}

	defaultSkin, _ := cfg["default_skin"].(string)
	if skins := petKeys(t, cfg, "skins"); defaultSkin == "" || !petHas(skins, defaultSkin) {
		t.Fatalf("default_skin %q is not in skins %v", defaultSkin, skins)
	}
	defaultLocation, _ := cfg["default_location"].(string)
	if locations := petKeys(t, cfg, "locations"); defaultLocation == "" || !petHas(locations, defaultLocation) {
		t.Fatalf("default_location %q is not in locations %v", defaultLocation, locations)
	}
}

// TestVanyagotchiPetIsCreatedByTheFirstStateRead confirms the lazy create.
//
// There is no "start a game" call in this game and there is deliberately no
// background job that provisions anything: the first read is what brings a pet
// into existence, seeded from the catalogue's starting values. The assertions go
// through raw SQL as well as the response, because "the API said sixty-five" and
// "a row exists saying sixty-five" are different claims and only the second one
// survives a restart.
func TestVanyagotchiPetIsCreatedByTheFirstStateRead(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7202", "user")
	account := accountIDByUID(t, "7202")

	if n := petRowCount(t, account); n != 0 {
		t.Fatalf("pets before the first read = %d, want 0", n)
	}

	s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
	if s != http.StatusOK {
		t.Fatalf("state: status=%d body=%v", s, state)
	}
	if state["alive"] != true {
		t.Fatalf("alive = %v, want true", state["alive"])
	}
	pet, ok := state["pet"].(map[string]any)
	if !ok {
		t.Fatalf("state has no pet: %v", state)
	}
	if pet["died_at"] != nil {
		t.Fatalf("died_at = %v on a fresh pet, want null", pet["died_at"])
	}
	// Seeded at each stat's catalogue START, whatever the catalogue currently
	// says. Against the literal numbers this would have to be edited every time
	// somebody tunes one — and the property under test is "a new pet is seeded
	// from the catalogue", not "health begins at sixty-five". That health starts
	// below its ceiling at all is asserted where it belongs, in content_test.go.
	for _, def := range gamevanyagotchi.Stats() {
		petNear(t, "fresh "+def.Key, petStateValue(t, state, def.Key), def.Start)
	}

	if n := petRowCount(t, account); n != 1 {
		t.Fatalf("pets after the first read = %d, want exactly 1", n)
	}
	id := petID(t, account)
	if pet["id"] != id {
		t.Fatalf("response pet id %v != stored %s", pet["id"], id)
	}
	if at := petDiedAt(t, id); at != nil {
		t.Fatalf("stored died_at = %v on a fresh pet, want NULL", at)
	}
	for _, def := range gamevanyagotchi.Stats() {
		value, asOf := petStoredStat(t, id, def.Key)
		if value != def.Start {
			t.Fatalf("stored %s = %v, want the catalogue start %v", def.Key, value, def.Start)
		}
		if asOf.IsZero() {
			t.Fatalf("stored %s has no as_of — the decay would have nothing to measure from", def.Key)
		}
	}
}

// TestVanyagotchiPetStateIsIdempotent confirms that reading is not creating.
//
// GET /state writes — it materialises the pet and, later, the death — so it is
// exactly the shape of endpoint that quietly accumulates rows. Three reads must
// leave one pet and one row per stat; anything else means the client's own
// polling is a slow leak.
func TestVanyagotchiPetStateIsIdempotent(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7203", "user")
	account := accountIDByUID(t, "7203")

	var firstID string
	for i := 1; i <= 3; i++ {
		s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
		if s != http.StatusOK {
			t.Fatalf("state read %d: status=%d body=%v", i, s, state)
		}
		id, _ := state["pet"].(map[string]any)["id"].(string)
		if firstID == "" {
			firstID = id
		}
		if id != firstID {
			t.Fatalf("state read %d returned pet %s, want the same pet %s", i, id, firstID)
		}
	}

	if n := petRowCount(t, account); n != 1 {
		t.Fatalf("pets after three reads = %d, want exactly 1", n)
	}
	for _, def := range gamevanyagotchi.Stats() {
		if n := petStatRowCount(t, firstID, def.Key); n != 1 {
			t.Fatalf("rows for stat %s after three reads = %d, want exactly 1", def.Key, n)
		}
	}
}

// TestVanyagotchiPetIsCreatedOnceUnderConcurrentFirstReads is why the partial
// unique index and the bare ON CONFLICT DO NOTHING both exist.
//
// Two tabs opening the game at the same instant is the ordinary case, not an
// exotic one, and the create is a read-then-insert with no lock around it. What
// keeps it correct is the database: the index makes a second living pet
// impossible, and DO NOTHING turns the loser of the race into a no-op instead of
// a 500. Both halves are needed — without the index the loser inserts a second
// pet, without DO NOTHING the loser gets a unique violation — and this test
// fails for either.
func TestVanyagotchiPetIsCreatedOnceUnderConcurrentFirstReads(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7204", "user")
	account := accountIDByUID(t, "7204")

	const readers = 8
	// A single client, so every request shares one cookie jar and one connection
	// pool — the two-tabs case rather than eight separate logins.
	var wg sync.WaitGroup
	statuses := make([]int, readers)
	ids := make([]string, readers)
	start := make(chan struct{})
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Released together, so the requests genuinely overlap instead of
			// being spread out by goroutine start-up.
			<-start
			s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
			statuses[i] = s
			if pet, ok := body["pet"].(map[string]any); ok {
				ids[i], _ = pet["id"].(string)
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, s := range statuses {
		if s != http.StatusOK {
			t.Fatalf("concurrent read %d: status=%d, want 200 — a lost create race must not surface as an error", i, s)
		}
		if ids[i] == "" || ids[i] != ids[0] {
			t.Fatalf("concurrent read %d returned pet %q, want the same pet as read 0 (%q)", i, ids[i], ids[0])
		}
	}
	if n := petRowCount(t, account); n != 1 {
		t.Fatalf("pets after %d concurrent first reads = %d, want exactly 1", readers, n)
	}
}

// TestVanyagotchiPetStatsDecayFromTheStoredInstant confirms the decay is real,
// lazy, and signed.
//
// Nothing ticks in this game, so "he got worse while you were away" is not a job
// that ran — it is what clamp(value − rate × elapsed) already means. Backdating
// as_of is therefore indistinguishable from an absence, which is what makes it
// testable at all. Every stat is checked by the one expression on purpose: the
// needs move in opposite directions, from one signed rate and no branch, and a
// sign error that made the bladder empty itself overnight would otherwise be
// invisible. The window stops short of the first onset so that what is measured
// here is the uncoupled property alone — the coupling has a test of its own,
// below, and a test that mixed the two would not say which had broken.
func TestVanyagotchiPetStatsDecayFromTheStoredInstant(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7205", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7205"))

	// Half way to the first unmet need, derived rather than written down, so a
	// retuned catalogue moves this window instead of failing against it.
	hpDef := petStat(t, gamevanyagotchi.StatHP)
	away := petHours(petFirstOnsetHours(t, hpDef) / 2)
	petBackdateAll(t, id, away)

	s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
	if s != http.StatusOK {
		t.Fatalf("state after backdating: status=%d body=%v", s, state)
	}

	for _, def := range gamevanyagotchi.Stats() {
		got := petStateValue(t, state, def.Key)
		switch {
		case def.DecayPerHour > 0 && got >= def.Start:
			t.Fatalf("%s = %.4f after %s away, want materially below its start %.1f — it drains", def.Key, got, away, def.Start)
		case def.DecayPerHour < 0 && got <= def.Start:
			t.Fatalf("%s = %.4f after %s away, want materially above its start %.1f — it fills, it does not drain", def.Key, got, away, def.Start)
		}
		petNear(t, fmt.Sprintf("%s after %s away", def.Key, away), got, petClamp(def, def.Start-def.DecayPerHour*away.Hours()))

		// The stored pair is untouched by a read: the decay is computed, never
		// accumulated. If a read ever started writing the decayed value back, an
		// absence would compound instead of being linear.
		if value, _ := petStoredStat(t, id, def.Key); value != def.Start {
			t.Fatalf("stored %s = %v after a read, want the untouched start %v — reads must not write the decay back",
				def.Key, value, def.Start)
		}
	}

	if state["alive"] != true {
		t.Fatalf("alive = %v after %s away, want true — hp is still above zero", state["alive"], away)
	}
}

// TestVanyagotchiPetHealthFallsFasterWhileANeedGoesUnmet is the coupling, end to
// end and through the database.
//
// Health is a consequence rather than a chore in this game: it barely rots on
// its own, and what actually kills him is an empty beer or a full bladder adding
// their own drain on top. That is the entire causal story the player is meant to
// read off the bars, so a build that stored and served the stats correctly but
// forgot to couple them would look completely healthy while quietly making the
// game pointless — the two bars you can press would no longer drive the one you
// cannot. The window is chosen to sit between the two onsets, so exactly one
// need is unmet and the expected damage is attributable rather than merely
// "lower than before"; the ordering is asserted rather than assumed, so a
// retuned catalogue that no longer has that shape says so instead of quietly
// testing nothing.
func TestVanyagotchiPetHealthFallsFasterWhileANeedGoesUnmet(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7233", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7233"))

	hpDef := petStat(t, gamevanyagotchi.StatHP)
	beerPenalty := petPenaltyOn(t, hpDef, gamevanyagotchi.StatBeer)
	bladderPenalty := petPenaltyOn(t, hpDef, gamevanyagotchi.StatBladder)
	beerOnset := petOnsetHours(t, beerPenalty)
	bladderOnset := petOnsetHours(t, bladderPenalty)
	if !(beerOnset < bladderOnset) {
		t.Fatalf("beer goes unmet at %.2fh and the bladder at %.2fh; this test needs the first strictly before the second", beerOnset, bladderOnset)
	}
	hours := (beerOnset + bladderOnset) / 2
	if fatal := petFatalHours(t, hpDef); hours >= fatal {
		t.Fatalf("the window (%.2fh) reaches past the moment he dies (%.2fh); this test needs him alive to read a health bar", hours, fatal)
	}
	away := petHours(hours)
	petBackdateAll(t, id, away)

	s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
	if s != http.StatusOK {
		t.Fatalf("state after backdating: status=%d body=%v", s, state)
	}
	if state["alive"] != true {
		t.Fatalf("alive = %v after %s away, want true", state["alive"], away)
	}

	// One need unmet and the other not, which is what makes the expected damage
	// a single term and the failure message able to name a culprit.
	beerDef := petStat(t, gamevanyagotchi.StatBeer)
	if beer := petStateValue(t, state, beerDef.Key); beer > beerPenalty.Threshold {
		t.Fatalf("beer = %.4f after %s away, want at or below %.1f — the window was chosen for a beer that has run dry", beer, away, beerPenalty.Threshold)
	}
	bladderDef := petStat(t, gamevanyagotchi.StatBladder)
	if bladder := petStateValue(t, state, bladderDef.Key); bladder >= bladderPenalty.Threshold {
		t.Fatalf("bladder = %.4f after %s away, want below %.1f — the window was chosen for a bladder that is not yet full", bladder, away, bladderPenalty.Threshold)
	}

	base := hpDef.Start - hpDef.DecayPerHour*hours
	damage := beerPenalty.RatePerHour * (hours - beerOnset)
	want := petClamp(hpDef, base-damage)
	if damage <= 4*petStatEpsilon {
		t.Fatalf("the unmet need only costs %.4f over this window, which is inside the tolerance — the test could not tell the coupling from noise", damage)
	}

	hp := petStateValue(t, state, hpDef.Key)
	// Stated separately from the tolerance below, and deliberately loudly: a
	// build that dropped the coupling altogether lands exactly on `base`, which
	// is a plausible-looking number, and the failure has to say why it is wrong.
	if hp >= base-damage/2 {
		t.Fatalf("hp = %.4f after %s away — the base rate alone explains %.4f, and %.2fh of an unmet %s should have taken it to %.4f; the coupling is not being applied",
			hp, away, base, hours-beerOnset, beerDef.Key, want)
	}
	petNear(t, fmt.Sprintf("hp after %s away with %s unmet", away, beerDef.Key), hp, want)

	// The client interpolates between fetches from the rate the server sends, so
	// the rate has to carry the coupling too — otherwise the bar would creep at
	// the harmless base rate and jump every time anybody looked.
	petNear(t, "the hp drain reported to the client", petStateRate(t, state, hpDef.Key), hpDef.DecayPerHour+beerPenalty.RatePerHour)
}

// TestVanyagotchiPetDeathIsRecordedAtTheDerivedInstant is the property the whole
// lazy design turns on.
//
// He did not die when somebody looked; he died when hp reached zero, and that
// instant is derivable exactly from the stored pairs. Recording "now" instead
// would be wrong by however long nobody opened the game and would make the
// recorded moment depend on who polled rather than on what happened. The
// derivation now spans a change of rate — health falls slowly, then faster once
// the beer runs dry, then faster again once the bladder fills — so this also
// pins that the walk across those segments is exact rather than an average.
// The whole pet is backdated, not just health: backdating health alone would
// leave both needs freshly met and health falling at its harmless base rate for
// days, which is a different scenario and a much duller one. The second read
// then has to leave the record alone, which is what "materialised exactly once"
// means: the first observer writes the fact, everyone after that reports it.
func TestVanyagotchiPetDeathIsRecordedAtTheDerivedInstant(t *testing.T) {
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7206", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7206"))

	hpDef := petStat(t, gamevanyagotchi.StatHP)
	fatal := petFatalHours(t, hpDef)
	// Three times over, so he died and then lay there twice as long again — the
	// gap between the two being exactly what a recorded "now" would get wrong.
	away := petHours(3 * fatal)
	petBackdateAll(t, id, away)

	s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
	if s != http.StatusOK {
		t.Fatalf("state: status=%d body=%v", s, state)
	}
	if state["alive"] != false {
		t.Fatalf("alive = %v after %s away, want false", state["alive"], away)
	}
	petNear(t, "hp at death", petStateValue(t, state, hpDef.Key), hpDef.Min)

	// The derivation's premise, asserted rather than assumed: every stat still
	// stands at its catalogue start, at one shared instant.
	for _, def := range gamevanyagotchi.Stats() {
		if value, _ := petStoredStat(t, id, def.Key); value != def.Start {
			t.Fatalf("stored %s = %v before any action, want the untouched start %v", def.Key, value, def.Start)
		}
	}
	asOf := petSharedAsOf(t, id)
	wantDeath := asOf.Add(petHours(fatal))

	first := petDiedAt(t, id)
	if first == nil {
		t.Fatal("died_at is NULL after the read that observed the death")
	}
	if d := first.Sub(wantDeath); d > time.Second || d < -time.Second {
		t.Fatalf("died_at = %s, want the derived instant %s (off by %s)", first.UTC(), wantDeath.UTC(), d)
	}
	// Stated separately from the tolerance above so the failure reads plainly if
	// somebody ever records the moment of the read: that is the whole bug this
	// test exists for, and it would land the death this much too late.
	unattended := away - petHours(fatal)
	if late := time.Since(*first); late < unattended-time.Minute {
		t.Fatalf("died_at is only %s ago, want about %s — it looks like the moment of the read, not the moment hp reached zero", late, unattended)
	}

	// Reading again must not restate the fact.
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("second state: status=%d body=%v", s, body)
	}
	second := petDiedAt(t, id)
	if second == nil || !second.Equal(*first) {
		t.Fatalf("died_at moved from %v to %v on a second read", first, second)
	}

	// And a recorded death is not recomputed. Rewriting the row to a different
	// instant and reading again pins both guards that make the write happen once
	// — the service's "only if he was alive" and the UPDATE's own
	// "died_at IS NULL" — since either alone would leave this value alone.
	sentinel := wantDeath.Add(-90 * time.Minute)
	if _, err := pool.Exec(context.Background(),
		`UPDATE game_vanyagotchi_pets SET died_at = $2 WHERE id = $1::uuid`, id, sentinel); err != nil {
		t.Fatalf("plant sentinel died_at: %v", err)
	}
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("third state: status=%d body=%v", s, body)
	}
	third := petDiedAt(t, id)
	if third == nil || !third.Equal(sentinel) {
		t.Fatalf("died_at = %v after a later read, want the already-recorded %v — the death was rewritten", third, sentinel)
	}
}

// TestVanyagotchiPetDrinkMovesThreeStatsAtOneInstant confirms one press moves
// everything the verb names, and stamps it all with the same moment.
//
// Drinking is not a single-stat button any more: it tops him up, cheers him up,
// and fills his bladder, which is the joke and also the reason the second verb
// has anything to do. Each effect is asserted against the catalogue's own delta
// applied to the decayed value, so a loop that stopped after the first effect —
// the obvious regression when one delta became a slice of them — is caught by
// the stats it silently skipped. The shared instant is asserted alongside,
// because the values are only meaningful if every pair was re-stamped together:
// health's drain is integrated over the other stats' trajectories, and rows
// stamped a few milliseconds apart are already a window nobody can reconstruct.
// The raw rows are read as well as the response, because "the API said eighty"
// and "a row says eighty" are different claims and only the second survives a
// restart.
func TestVanyagotchiPetDrinkMovesThreeStatsAtOneInstant(t *testing.T) {
	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7230", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7230")
	id := petID(t, account)

	drink := petAction(t, gamevanyagotchi.ActionDrink)
	if len(drink.Effects) < 2 {
		t.Fatalf("%s moves %d stat(s); the property under test is one press moving several at once", drink.Key, len(drink.Effects))
	}

	// Short of the first unmet need, so every stat has drifted somewhere in the
	// middle of its scale and each delta lands without clamping — the clamps are
	// pinned where they belong, in the tests that drive a stat to its bound.
	hpDef := petStat(t, gamevanyagotchi.StatHP)
	away := petHours(petFirstOnsetHours(t, hpDef) / 2)
	petBackdateAll(t, id, away)

	// At the crate, because beer now comes out of one. This test is about the
	// three stats a drink moves; the arrival gate has its own tests.
	petStandAtTheBeerStore(t, game, account)
	state := petDo(t, game, account, drink.Key)
	if !state.Alive {
		t.Fatalf("alive = %v after a drink, want true", state.Alive)
	}

	for _, e := range drink.Effects {
		def := petStat(t, e.StatKey)
		decayed := petValueAfter(t, def, away.Hours())
		want := petClamp(def, decayed+e.Delta)
		if math.Abs(want-decayed) <= petStatEpsilon {
			t.Fatalf("%s's %+.1f on %s is invisible at this window (%.4f either way) — the test would pass against a verb that did nothing",
				drink.Key, e.Delta, e.StatKey, decayed)
		}
		value, _ := petStoredStat(t, id, e.StatKey)
		petNear(t, "stored "+e.StatKey+" after a drink", value, want)
		petNear(t, "answered "+e.StatKey+" after a drink", petValue(t, state, e.StatKey), want)
	}

	petRecent(t, "the instant a drink stamped on every stat", petSharedAsOf(t, id))
}

// TestVanyagotchiPetRelieveEmptiesTheBladderAndRestampsEveryStat pins the two
// halves of the verb that closes the loop drinking opens.
//
// The reset is free: the catalogue sends the bladder down by more than the whole
// scale and the clamp lands it exactly on the floor, so "empty it" needs no
// mechanism of its own — and an action that overshot into a negative reading
// would be a bar drawn off the end of its track. The re-stamp is the part that
// is easy to get wrong and impossible to see: relieving himself moves one stat,
// so writing one stat is the obvious implementation, and it would leave health
// anchored at a pair that says the morning never happened. The damage an empty
// beer had already done would simply vanish at the moment of the next flush,
// which is a bug nobody would report because the game would merely feel kind.
func TestVanyagotchiPetRelieveEmptiesTheBladderAndRestampsEveryStat(t *testing.T) {
	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7231", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7231")
	id := petID(t, account)

	relieve := petAction(t, gamevanyagotchi.ActionRelieve)
	hpDef := petStat(t, gamevanyagotchi.StatHP)
	bladderDef := petStat(t, gamevanyagotchi.StatBladder)
	beerOnset := petOnsetHours(t, petPenaltyOn(t, hpDef, gamevanyagotchi.StatBeer))
	bladderOnset := petOnsetHours(t, petPenaltyOn(t, hpDef, gamevanyagotchi.StatBladder))
	if !(beerOnset < bladderOnset) {
		t.Fatalf("beer goes unmet at %.2fh and the bladder at %.2fh; this test needs the first strictly before the second", beerOnset, bladderOnset)
	}
	// Far enough in that health has already taken penalty damage, so the value
	// written back is one only the coupling explains — and near enough that he
	// is still alive to be relieved.
	hours := (beerOnset + bladderOnset) / 2
	if fatal := petFatalHours(t, hpDef); hours >= fatal {
		t.Fatalf("the window (%.2fh) reaches past the moment he dies (%.2fh); a corpse cannot go to the toilet", hours, fatal)
	}
	away := petHours(hours)
	petBackdateAll(t, id, away)

	// The state it answers with is not read here: every assertion below is raw
	// SQL, because what this test is about is what the write left in the table.
	petDo(t, game, account, relieve.Key)

	// Asserted first, because it is the invariant the values below only make
	// sense on top of, and because it names the regression outright when a write
	// touches one row and leaves the rest anchored in the past.
	petRecent(t, "the instant relieving himself stamped on every stat", petSharedAsOf(t, id))

	// Every stat, whether the verb names it or not: the ones it moves land on
	// the catalogue's delta, the ones it does not land on their decayed value —
	// and both are only correct if the whole set was written.
	for _, def := range gamevanyagotchi.Stats() {
		want := petValueAfter(t, def, hours)
		for _, e := range relieve.Effects {
			if e.StatKey == def.Key {
				want = petClamp(def, want+e.Delta)
			}
		}
		value, _ := petStoredStat(t, id, def.Key)
		petNear(t, "stored "+def.Key+" after relieving himself", value, want)
	}

	if value, _ := petStoredStat(t, id, bladderDef.Key); value != bladderDef.Min {
		t.Fatalf("stored %s = %v after relieving himself, want exactly its floor %v — the delta is larger than the whole scale and the clamp is what makes a reset free",
			bladderDef.Key, value, bladderDef.Min)
	}

	// The damage survived the write. Said plainly because the failure it guards
	// against restores health to a number that looks entirely reasonable: the
	// one the base rate alone would predict.
	base := hpDef.Start - hpDef.DecayPerHour*hours
	hp, _ := petStoredStat(t, id, hpDef.Key)
	if hp >= base-petStatEpsilon {
		t.Fatalf("stored hp = %.4f after relieving himself, want %.4f — the %.4f the base rate alone predicts means the hours of an empty beer were forgotten when the row was re-stamped",
			hp, petValueAfter(t, hpDef, hours), base)
	}
}

// TestVanyagotchiPetEveryVerbButTheRevivalIsRefusedOnADeadPet pins the refusal
// that makes death mean anything at all.
//
// A dead Ваня does not go to the toilet, and — the part that changed — he does
// not have a beer either. Drinking used to carry revives_fatal, which made dying
// very nearly invisible: the verb a player presses anyway quietly undid it. Death
// has a verb of its own now, and only the verb the catalogue marks as reviving is
// allowed through, which is what turns "he is dead" from a label into a state the
// player has to act their way out of. It has to be a refusal rather than a no-op,
// because a button that appears to work and changes nothing is how a player
// concludes the game is broken. The refusal is an error out of the one funnel
// every verb goes through; its player-facing form is a line over his head rather
// than a status code — refusalLine turns ErrPetDead into «он не встаёт», which is
// asserted in internal/gamevanyagotchi/service_test.go, where the plane can
// actually be read. The refusal must also be total — no stat may be re-stamped on
// the way out, since a rejected verb that silently reset the clock would hand out
// free hours. Both halves of the guard are exercised in one test on purpose: an
// implementation that refused everything would pass the first half alone and
// leave the game unwinnable.
//
// The refused verbs are taken from the catalogue rather than named, so a verb
// that quietly acquires the flag fails here instead of reaching a player who
// discovers that death has stopped costing anything.
func TestVanyagotchiPetEveryVerbButTheRevivalIsRefusedOnADeadPet(t *testing.T) {
	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7232", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7232")
	id := petID(t, account)

	hpDef := petStat(t, gamevanyagotchi.StatHP)
	revive := petAction(t, gamevanyagotchi.ActionRevive)
	if !revive.RevivesFatal {
		t.Fatalf("the catalogue says %s revives=%v; there is no way back and this account is finished with the game", revive.Key, revive.RevivesFatal)
	}
	// Named outright because it is the specific thing that changed: if beer ever
	// revives again the loop below would simply skip it, and this test would go
	// green over exactly the regression it was rewritten for.
	if drink := petAction(t, gamevanyagotchi.ActionDrink); drink.RevivesFatal {
		t.Fatalf("%s revives a corpse again; a death is meant to cost a deliberate press of %s rather than being undone by the verb the player was pressing anyway",
			drink.Key, revive.Key)
	}

	// Twice as long as it takes to die, so he is unambiguously gone.
	petBackdateAll(t, id, petHours(2*petFatalHours(t, hpDef)))
	if s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK || state["alive"] != false {
		t.Fatalf("state before acting: status=%d alive=%v; want 200 and dead", s, state["alive"])
	}
	died := petDiedAt(t, id)
	if died == nil {
		t.Fatal("died_at is NULL after the read that observed the death")
	}
	asOfBefore := petSharedAsOf(t, id)

	refused := 0
	for _, action := range gamevanyagotchi.Content().Actions {
		if action.RevivesFatal {
			continue
		}
		refused++
		if _, err := game.Do(context.Background(), account, []string{action.Key}, "", time.Now().UTC()); !errors.Is(err, gamevanyagotchi.ErrPetDead) {
			t.Fatalf("%s on a dead pet: err=%v; want ErrPetDead", action.Key, err)
		}
		if at := petDiedAt(t, id); at == nil || !at.Equal(*died) {
			t.Fatalf("died_at = %v after a refused %s, want the recorded %v — a refusal must not touch the pet", at, action.Key, died)
		}
		if at := petSharedAsOf(t, id); !at.Equal(asOfBefore) {
			t.Fatalf("the stats were re-stamped from %s to %s by a refused %s — a rejected action must write nothing at all",
				asOfBefore.UTC(), at.UTC(), action.Key)
		}
	}
	if refused == 0 {
		t.Fatal("every verb in the catalogue revives, so ErrPetDead is unreachable and death costs nothing")
	}

	// Back to back with no wait, which is legal precisely BECAUSE Do carries no
	// rate limit of its own: through handleVerbs the second press would be dropped
	// by verbInterval, and that bound belongs to the plane in memory rather than to
	// the pet in Postgres.
	state := petDo(t, game, account, revive.Key)
	if !state.Alive {
		t.Fatalf("alive = %v after a %s, want true", state.Alive, revive.Key)
	}
	if hp := petValue(t, state, hpDef.Key); hp <= hpDef.Min {
		t.Fatalf("hp = %.4f after a %s, want above the fatal floor %.1f", hp, revive.Key, hpDef.Min)
	}
	if at := petDiedAt(t, id); at != nil {
		t.Fatalf("stored died_at = %v after a revive, want NULL — a stale record would re-kill him on the next read", at)
	}
}

// TestVanyagotchiPetDrinkClampsAtTheMaximum confirms the cure cannot overshoot.
//
// An action is the only thing that ever raises a stat, so an unclamped one would
// park health above its ceiling and buy hours of invulnerability the catalogue
// never granted. The same loop proves the clamp in both directions of usefulness,
// because the drink that fills him up also fills his bladder — a stat whose
// ceiling is the bad end of its scale.
//
// It used to start by killing him, back when beer was also the way out of a
// death. That half now belongs to the verb that owns it and is asserted in
// TestVanyagotchiPetRevivingRestartsEveryStatAndKeepsTheTallies; what is left
// here is the clamp alone, on a pet who is merely run down rather than gone —
// which is also the state in which a drink is a visible change rather than a
// no-op against a ceiling.
func TestVanyagotchiPetDrinkClampsAtTheMaximum(t *testing.T) {
	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7207", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7207")
	id := petID(t, account)
	hpDef := petStat(t, gamevanyagotchi.StatHP)
	bladderDef := petStat(t, gamevanyagotchi.StatBladder)
	drink := petAction(t, gamevanyagotchi.ActionDrink)

	// Half way to the grave rather than a fixed number of hours, so he is
	// reliably still alive to do the drinking and reliably far enough down that
	// reaching the ceiling takes several rounds.
	petBackdateAll(t, id, petHours(petFatalHours(t, hpDef)/2))
	// Standing at the crate for every round of this, because a drink he cannot
	// reach would never get near a ceiling.
	petStandAtTheBeerStore(t, game, account)

	state := petDo(t, game, account, drink.Key)
	if !state.Alive {
		t.Fatalf("alive = %v after a %s, want true", state.Alive, drink.Key)
	}

	// Drink until the ceiling, then once more. Bounded rather than counted, so a
	// retuned delta in the catalogue does not turn this into a false failure.
	hp := petValue(t, state, hpDef.Key)
	for i := 0; hp < hpDef.Max && i < 20; i++ {
		round := petDo(t, game, account, drink.Key)
		hp = petValue(t, round, hpDef.Key)
		if hp > hpDef.Max {
			t.Fatalf("hp = %.4f after %s %d, want no more than the catalogue max %.1f", hp, drink.Key, i, hpDef.Max)
		}
		if bladder := petValue(t, round, bladderDef.Key); bladder > bladderDef.Max {
			t.Fatalf("bladder = %.4f after %s %d, want no more than the catalogue max %.1f", bladder, drink.Key, i, bladderDef.Max)
		}
	}
	petNear(t, "hp at the ceiling", hp, hpDef.Max)

	// One more at the ceiling: it must land, and it must change nothing.
	state = petDo(t, game, account, drink.Key)
	if hp := petValue(t, state, hpDef.Key); hp > hpDef.Max {
		t.Fatalf("hp = %.4f after drinking at the ceiling, want exactly %.1f", hp, hpDef.Max)
	}
	// The clamp must reach the row, not just the response — a stored value above
	// the ceiling would decay for hours before it came back into range.
	for _, def := range []gamevanyagotchi.Stat{hpDef, bladderDef} {
		if value, _ := petStoredStat(t, id, def.Key); value > def.Max {
			t.Fatalf("stored %s = %v, want no more than %v", def.Key, value, def.Max)
		}
	}
}

// TestVanyagotchiPetRevivingRestartsEveryStatAndKeepsTheTallies is the way back
// from a death, through a real database.
//
// Death is a fright rather than an ending — an irreversible loss in a friend
// group is how a player leaves for good — so «восстать из мертвых» exists and
// costs one press. What it does is not a large delta but a RESET: coming back
// means coming back as a new Ваня, and no amount added to whatever he died
// holding lands on the catalogue's starting values, because a delta big enough
// to clamp gets you the stat's bound rather than its start. The stored rows are
// what this asserts, not the response: "the API said sixty-five" and "a row says
// sixty-five" are different claims and only the second one survives a restart.
//
// AND THE TALLIES SURVIVE HIM, which is the subtle half and the one a later
// tidy-up would break. A lifetime total that a death set back to nought would
// not be a lifetime total — «выпито пива: 0» after a dozen beers is a lie about
// the past rather than a fresh beginning — so the counters are exempt from the
// reset while every bar is rewritten. They are stocked by actually playing
// first, because a counter still standing at its start would agree with a reset
// that cleared it and this test would prove nothing.
func TestVanyagotchiPetRevivingRestartsEveryStatAndKeepsTheTallies(t *testing.T) {
	// A transport, because the evening below now includes the CONTESTED verb and
	// winning one paints the yard — see petYardTransport.
	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7234", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7234")
	id := petID(t, account)

	hpDef := petStat(t, gamevanyagotchi.StatHP)
	drink := petAction(t, gamevanyagotchi.ActionDrink)
	relieve := petAction(t, gamevanyagotchi.ActionRelieve)
	claim := petAction(t, gamevanyagotchi.ActionClaim)
	revive := petAction(t, gamevanyagotchi.ActionRevive)

	// One tally is only moved by finding the keys, and finding them is contested:
	// there has to be a key lost in the yard before anybody can find one. He is
	// the only player here, so this hides one for him rather than waiting for a
	// hello to start a hunt.
	kind, ok := gamevanyagotchi.ObjectKindByKey(gamevanyagotchi.KindKey)
	if !ok {
		t.Fatalf("the catalogue has no object kind %q", gamevanyagotchi.KindKey)
	}
	// In a HOTSPOT, because finding the keys is now a search: he has to name the
	// place and be standing in it, and a key hidden anywhere else would resolve to
	// whichever spot happened to be nearest.
	spot := petYardHotspot(t, 0)
	petClearTheYardOf(t, gamevanyagotchi.KindKey)
	t.Cleanup(func() { petClearTheYardOf(t, gamevanyagotchi.KindKey) })
	if err := gamevanyagotchi.NewPostgresRepository().InsertWorldObject(context.Background(), pool,
		kind.Key, gamevanyagotchi.LocationYard, spot.At, "", kind.Singleton, nil, nil); err != nil {
		t.Fatalf("hide a key for him to find: %v", err)
	}

	// An evening's worth of playing: two rounds in one batch, a visit to the
	// bushes, and the keys turning up. Every tally the catalogue carries has to end
	// up above its start, or the preservation asserted at the end would be
	// indistinguishable from a reset — so that is checked rather than assumed.
	pressed := []gamevanyagotchi.Action{drink, drink, relieve, claim}
	// At the crate, since two of those four rounds are beer. Placed AFTER the key
	// is hidden on purpose: the hello inside this fixture starts a hunt when it
	// finds none running, and a key it hid first would swallow the one this test
	// wants him to find.
	petStandAtTheBeerStore(t, game, account)
	petDo(t, game, account, drink.Key, drink.Key)
	petDo(t, game, account, relieve.Key)
	// And then he walks to the place the keys are in, because looking for them is
	// a search now: the beer store is nowhere near a hotspot, deliberately.
	petSearchingIn(t, game, account, spot)
	petSearchIn(t, game, account, spot)

	tallies := make(map[string]float64)
	for _, def := range gamevanyagotchi.Stats() {
		if !def.Counter {
			continue
		}
		want := def.Start
		for _, a := range pressed {
			want += petEffectOn(a, def.Key)
		}
		if want <= def.Start {
			t.Fatalf("nothing pressed here moves the tally %s off its start %v; the reset below would have nothing to preserve", def.Key, def.Start)
		}
		value, _ := petStoredStat(t, id, def.Key)
		if value != want {
			t.Fatalf("stored %s = %v after %d verbs, want %v — a counter is a stat whose rate is nought, so counting is an effect and a press that failed to land shows up nowhere else",
				def.Key, value, len(pressed), want)
		}
		tallies[def.Key] = value
	}
	if len(tallies) == 0 {
		t.Fatal("the catalogue has no lifetime tallies, so half of this test asserts nothing")
	}

	// Long enough that he is unambiguously gone, measured from the catalogue
	// rather than written down. Three times over rather than twice, because he
	// starts this stretch topped up rather than at his starting values.
	petBackdateAll(t, id, petHours(3*petFatalHours(t, hpDef)))
	if s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK || state["alive"] != false {
		t.Fatalf("state before the revival: status=%d alive=%v; want 200 and dead", s, state["alive"])
	}
	if petDiedAt(t, id) == nil {
		t.Fatal("died_at is NULL after the read that observed the death")
	}

	state := petDo(t, game, account, revive.Key)
	if !state.Alive {
		t.Fatalf("alive = %v after %s, want true", state.Alive, revive.Key)
	}
	if at := petDiedAt(t, id); at != nil {
		t.Fatalf("stored died_at = %v after %s, want NULL — a stale record would re-kill him on the next read", at, revive.Key)
	}

	for _, def := range gamevanyagotchi.Stats() {
		value, _ := petStoredStat(t, id, def.Key)
		if def.Counter {
			if value != tallies[def.Key] {
				t.Fatalf("stored %s went from %v to %v across a revival, want it untouched — a total a death reset would not be a lifetime total",
					def.Key, tallies[def.Key], value)
			}
			continue
		}
		if value != def.Start {
			t.Fatalf("stored %s = %v after a revival, want the catalogue start %v — coming back from the dead is coming back as a new Ваня rather than as the old one plus a number",
				def.Key, value, def.Start)
		}
		petNear(t, "answered "+def.Key+" after a revival", petValue(t, state, def.Key), def.Start)
	}

	// One instant across the whole set, counters included: being exempt from the
	// reset is not being exempt from the write, and a row left at an older as_of
	// is a window the coupled decay cannot reconstruct.
	petRecent(t, "the instant the revival stamped on every stat", petSharedAsOf(t, id))
}

// TestVanyagotchiPetRejectsAnActionOutsideTheCatalogue confirms the allowlist is
// the catalogue itself.
//
// A verb is a string off the wire, so the funnel is reachable with anything at
// all; what makes that safe is that an unknown one is refused rather than
// ignored. And the refusal happens BEFORE storage is touched, which is the second
// half of this test: the catalogue lookup runs first precisely so a verb nobody
// has heard of cannot conjure a pet — with rows and seeded stats — for an account
// that has never opened the game. A hostile client would otherwise have a free
// write per frame.
func TestVanyagotchiPetRejectsAnActionOutsideTheCatalogue(t *testing.T) {
	app, game := petApp(t)
	// The login is what creates the account row; nothing here ever reads over
	// HTTP, so the client itself is not needed.
	loginAs(t, app.URL, "7208", "user")
	account := accountIDByUID(t, "7208")

	_, err := game.Do(context.Background(), account, []string{"подкормить"}, "", time.Now().UTC())
	if !errors.Is(err, gamevanyagotchi.ErrUnknownAction) {
		t.Fatalf("an unknown verb: err=%v; want ErrUnknownAction", err)
	}
	if n := petRowCount(t, account); n != 0 {
		t.Fatalf("pets after a verb outside the catalogue = %d, want 0 — the lookup runs before any storage is touched so that nonsense cannot materialise a pet", n)
	}
}

// TestVanyagotchiPetsAreIsolatedPerAccount confirms one player's pet is one
// player's.
//
// Every query in the durable half carries an account predicate, and this is the
// check that none of them lost it — the failure mode being one shared Ваня that
// every player feeds. It matters more now than it did: a verb re-stamps every
// stat row it can reach, so a missing predicate would not merely read somebody
// else's pet, it would rewrite it.
//
// The account id is an ARGUMENT here rather than something taken from a session,
// which is the honest shape of the funnel: Do is handed an account and trusts it.
// Where that argument comes from is proved elsewhere — the /state tests for the
// reads, and for a verb the realtime.Member the hub binds at the socket upgrade,
// which a frame cannot talk its way out of.
func TestVanyagotchiPetsAreIsolatedPerAccount(t *testing.T) {
	app, game := petApp(t)
	alice := loginAs(t, app.URL, "7220", "user")
	bob := loginAs(t, app.URL, "7221", "user")

	for name, cli := range map[string]*http.Client{"alice": alice, "bob": bob} {
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("%s create: status=%d body=%v", name, s, body)
		}
	}
	aliceAccount := accountIDByUID(t, "7220")
	aliceID := petID(t, aliceAccount)
	bobID := petID(t, accountIDByUID(t, "7221"))
	if aliceID == bobID {
		t.Fatalf("both accounts resolved to pet %s — the pet is not per-account", aliceID)
	}

	// Give Alice's pet room for a drink to be a visible change rather than a
	// clamp that would look identical to doing nothing — and half way to the
	// grave rather than a fixed number of hours, so she is reliably still alive
	// to do the drinking.
	hpDef := petStat(t, gamevanyagotchi.StatHP)
	drink := petAction(t, gamevanyagotchi.ActionDrink)
	petBackdateAll(t, aliceID, petHours(petFatalHours(t, hpDef)/2))
	beforeValue, beforeAsOf := petStoredStat(t, bobID, hpDef.Key)

	// Alice at the crate, and deliberately not Bob: what this test is about is
	// that her drink leaves his pet alone, and one of them standing somewhere is
	// no part of the other's pet.
	petStandAtTheBeerStore(t, game, aliceAccount)
	state := petDo(t, game, aliceAccount, drink.Key)
	if state.Pet.ID != aliceID {
		t.Fatalf("alice's %s answered with pet %s, want %s", drink.Key, state.Pet.ID, aliceID)
	}

	afterValue, afterAsOf := petStoredStat(t, bobID, hpDef.Key)
	if afterValue != beforeValue || !afterAsOf.Equal(beforeAsOf) {
		t.Fatalf("bob's hp row moved from (%v, %s) to (%v, %s) because alice had a drink",
			beforeValue, beforeAsOf, afterValue, afterAsOf)
	}
	petRecent(t, "alice's own stats after her drink", petSharedAsOf(t, aliceID))
}

// TestVanyagotchiWorldObjectSingletonIsADatabaseInvariant pins the DDL that
// three later mechanics depend on.
//
// Nothing writes this table yet, which is exactly why it is tested now: the
// migration is immutable once shipped, so the index predicate has to be right
// before the first key hunt or beer delivery is built on top of it. The
// invariant is "at most one ACTIVE event of a kind", and all three clauses of
// the predicate carry weight — `singleton` keeps relief deposits out of it (many
// are live at once and they are never exhausted), and `exhausted_at IS NULL` is
// what lets the next key be inserted the moment the last one is claimed. Written
// in raw SQL because the point is that the database refuses, not that some Go
// function declined to ask.
func TestVanyagotchiWorldObjectSingletonIsADatabaseInvariant(t *testing.T) {
	ctx := context.Background()
	// Kinds of this test's own, so the rows cannot collide with a mechanic that
	// starts writing real ones later.
	const (
		singletonKind = "pettest_key"
		manyKind      = "pettest_relief"
	)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx,
			`DELETE FROM game_vanyagotchi_world_objects WHERE kind IN ($1, $2)`, singletonKind, manyKind); err != nil {
			t.Fatalf("cleanup world objects: %v", err)
		}
	})

	insert := func(kind string, singleton bool, x, y float64) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO game_vanyagotchi_world_objects (kind, location_key, x, y, singleton)
			 VALUES ($1, $2, $3, $4, $5)`,
			kind, gamevanyagotchi.LocationYard, x, y, singleton)
		return err
	}
	isUniqueViolation := func(err error) bool {
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && pgErr.Code == "23505"
	}

	// One active key.
	if err := insert(singletonKind, true, 0.25, 0.25); err != nil {
		t.Fatalf("first singleton insert: %v", err)
	}
	// A second one, while the first is still active, must be refused by the
	// index rather than by anything in Go.
	err := insert(singletonKind, true, 0.75, 0.75)
	if !isUniqueViolation(err) {
		t.Fatalf("second singleton insert: err=%v; want a unique violation (23505)", err)
	}

	// Exhaust the first — claimed, drunk, whatever the mechanic calls it — and
	// the replacement becomes insertable. This is what makes "the next key spawns
	// as soon as this one is found" a single statement rather than a lock.
	if _, err := pool.Exec(ctx,
		`UPDATE game_vanyagotchi_world_objects SET exhausted_at = now() WHERE kind = $1 AND exhausted_at IS NULL`,
		singletonKind); err != nil {
		t.Fatalf("exhaust the first singleton: %v", err)
	}
	if err := insert(singletonKind, true, 0.75, 0.75); err != nil {
		t.Fatalf("replacement singleton insert after exhausting the first: %v", err)
	}

	// Non-singleton rows of one kind are unrestricted: two players relieving
	// themselves in the same yard is two deposits, not a conflict. This is the
	// clause an invariant keyed on `exhausted_at IS NULL` alone would have got
	// wrong.
	if err := insert(manyKind, false, 0.1, 0.1); err != nil {
		t.Fatalf("first non-singleton insert: %v", err)
	}
	if err := insert(manyKind, false, 0.2, 0.2); err != nil {
		t.Fatalf("second non-singleton insert: %v; want it accepted — only singletons are limited", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM game_vanyagotchi_world_objects WHERE kind = $1`, manyKind).Scan(&n); err != nil {
		t.Fatalf("count non-singletons: %v", err)
	}
	if n != 2 {
		t.Fatalf("non-singleton rows = %d, want 2", n)
	}
}

// petWorldObject is one row of the world-object table, read back with none of
// the plane's own filters.
//
// Unfiltered on purpose. The service's SELECT hides an expired, exhausted or
// deleted row and never returns `singleton` at all — and `singleton` is exactly
// what the assertion below turns on, because it is the column the database's
// at-most-one-active index is predicated on. What is under test here is what the
// INSERT actually wrote, not what a read would show of it.
type petWorldObject struct {
	id          string
	kind        string
	locationKey string
	x, y        float64
	singleton   bool
	expiresAt   *time.Time
	exhaustedAt *time.Time
}

// petWorldObjectsOwnedBy reads everything one account has left behind, oldest
// first.
func petWorldObjectsOwnedBy(t *testing.T, accountID string) []petWorldObject {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id::text, kind, location_key, x, y, singleton, expires_at, exhausted_at
		   FROM game_vanyagotchi_world_objects
		  WHERE owner_account_id = $1::uuid
		  ORDER BY created_at`,
		accountID)
	if err != nil {
		t.Fatalf("read the world objects owned by %s: %v", accountID, err)
	}
	defer rows.Close()

	var out []petWorldObject
	for rows.Next() {
		var o petWorldObject
		if err := rows.Scan(&o.id, &o.kind, &o.locationKey, &o.x, &o.y, &o.singleton, &o.expiresAt, &o.exhaustedAt); err != nil {
			t.Fatalf("scan a world object of %s: %v", accountID, err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the world objects owned by %s: %v", accountID, err)
	}
	return out
}

// petForgetWorldObjects deletes every deposit these accounts left.
//
// The suite shares ONE database and has no truncate step, so a test that writes
// into a table other tests read has to clean up after itself. Deposits are
// filtered on read rather than swept in production — nothing sweeps, by design —
// which means a row left here would still be standing in the yard when a later
// test looks at it, and the failure would land somewhere with nothing to do with
// the cause.
func petForgetWorldObjects(t *testing.T, accountIDs ...string) {
	t.Helper()
	for _, id := range accountIDs {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM game_vanyagotchi_world_objects WHERE owner_account_id = $1::uuid`, id); err != nil {
			t.Fatalf("clean up the world objects of %s: %v", id, err)
		}
	}
}

// petLocationEntry is where the catalogue says a pet stands on arriving.
//
// Derived rather than written down, like every other expectation in this file:
// the entry point is content, and a test that pinned (0.5, 0.5) would report
// moving the door as a regression.
func petLocationEntry(t *testing.T, key string) gamevanyagotchi.Point {
	t.Helper()
	for _, l := range gamevanyagotchi.Content().Locations {
		if l.Key == key {
			return l.Entry
		}
	}
	t.Fatalf("the catalogue has no location %q", key)
	return gamevanyagotchi.Point{}
}

// petAtPoint fails unless a stored position is the point it should be.
func petAtPoint(t *testing.T, what string, x, y float64, want gamevanyagotchi.Point) {
	t.Helper()
	// A double precision column round-trips a float64 exactly, so the tolerance
	// here is against arithmetic rather than against storage.
	if math.Abs(x-want.X) > 1e-9 || math.Abs(y-want.Y) > 1e-9 {
		t.Errorf("%s is at (%v,%v); want (%v,%v)", what, x, y, want.X, want.Y)
	}
}

// TestVanyagotchiRelievingHimselfLeavesARealDepositInTheWorld is the world half
// of a verb against the database that has to hold it.
//
// The row it writes is the first this table has ever had in production, and the
// single most valuable line below is the SECOND player's. The insert is
// `ON CONFLICT DO NOTHING` against a partial unique index, which means a kind
// wrongly marked `singleton` would not fail loudly — it would swallow the second
// deposit in silence while the verb still answered as though it had worked, and
// the game would quietly become one in which only one person at a time may
// relieve himself. That is the invariant migration 008's comment says the
// `singleton` column exists for, and it can only be proved where a real index is
// enforcing it.
//
// The rest is what a fake repository cannot say either: that the `expires_at`
// the TTL is filtered against is really written, that the deposit lands where
// the yard believes its owner is standing, and that the plane's own read gets
// the row back.
func TestVanyagotchiRelievingHimselfLeavesARealDepositInTheWorld(t *testing.T) {
	ctx := context.Background()
	relieve := petAction(t, gamevanyagotchi.ActionRelieve)
	if relieve.Leaves == "" {
		t.Fatalf("the catalogue says %q leaves nothing behind; this test is asserting a rule the game no longer has", relieve.Key)
	}
	kind, ok := gamevanyagotchi.ObjectKindByKey(relieve.Leaves)
	if !ok {
		t.Fatalf("%q leaves %q behind and the catalogue has no such object kind", relieve.Key, relieve.Leaves)
	}
	if kind.Singleton {
		t.Fatalf("the catalogue marks %q a singleton; the database would then allow exactly one of them in the whole world, and two players could not both use %q",
			kind.Key, relieve.Key)
	}
	if kind.Lifetime <= 0 {
		t.Fatalf("%q has no lifetime; the expiry asserted below is not a thing this kind has", kind.Key)
	}

	// A beer in front of every visit to the bushes, because the catalogue gates
	// «покакать» on the bladder and a pet nobody has played with starts empty.
	// Drinking first is the honest way to fill one — it is the loop the two verbs
	// make — and it costs this test nothing, because a drink leaves nothing behind
	// and every row counted below is a deposit.
	drink := petAction(t, gamevanyagotchi.ActionDrink)
	if petEffectOn(drink, relieve.NeedsStat) <= 0 {
		t.Fatalf("%q no longer fills %q, which %q needs; there is nothing to press before the bushes",
			drink.Key, relieve.NeedsStat, relieve.Key)
	}

	app, game := petApp(t)
	alice := loginAs(t, app.URL, "7240", "user")
	bob := loginAs(t, app.URL, "7241", "user")
	for name, cli := range map[string]*http.Client{"alice": alice, "bob": bob} {
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("%s create: status=%d body=%v", name, s, body)
		}
	}
	aliceAccount, bobAccount := accountIDByUID(t, "7240"), accountIDByUID(t, "7241")
	t.Cleanup(func() { petForgetWorldObjects(t, aliceAccount, bobAccount) })

	// Both at the crate, because the beer that fills a bladder now has to be
	// fetched from one. It also decides where the deposits land, which is
	// asserted below: the yard's opinion of where he is standing, and the yard
	// now has one.
	petStandAtTheBeerStore(t, game, aliceAccount)
	petStandAtTheBeerStore(t, game, bobAccount)

	pressed := time.Now().UTC()
	state := petDo(t, game, aliceAccount, drink.Key, relieve.Key)

	deposits := petWorldObjectsOwnedBy(t, aliceAccount)
	if len(deposits) != 1 {
		t.Fatalf("%d rows were written into the world for one %s; want exactly one deposit: %+v", len(deposits), relieve.Key, deposits)
	}
	left := deposits[0]

	if left.kind != kind.Key {
		t.Errorf("the row is of kind %q; want the catalogue's %q", left.kind, kind.Key)
	}
	if left.locationKey != state.Pet.LocationKey {
		t.Errorf("the deposit was left in %q; want the location his pet is in, %q", left.locationKey, state.Pet.LocationKey)
	}
	// AT THE CRATE, because that is where the fixture walked him and the deposit
	// is written where the SERVER believes he is standing — the verb frame carries
	// no coordinate, so there is nothing in it to forge. It used to be the
	// location's entry point, back when nobody in this file had ever been placed
	// on the plane at all; the beer store is what gave the tests a reason to walk
	// anybody anywhere.
	petAtPoint(t, "the deposit", left.x, left.y, *petObjectKind(t, gamevanyagotchi.KindCrate).At)
	if left.singleton {
		t.Errorf("the row was written with singleton=true; the index would then permit exactly one active %q in the whole world", kind.Key)
	}
	if left.exhaustedAt != nil {
		t.Errorf("the deposit was written already exhausted (%s); a deposit is never used up, it expires", left.exhaustedAt.UTC())
	}
	if left.expiresAt == nil {
		t.Fatalf("the deposit has no expires_at; the TTL is filtered on read, so a NULL here is a deposit that stands in the yard for ever")
	}
	// A minute of slack against the wall clock between the test reading it and
	// the verb reading its own, which is two orders of magnitude below the
	// lifetime under test.
	if drift := left.expiresAt.Sub(pressed.Add(kind.Lifetime)); drift < -time.Minute || drift > time.Minute {
		t.Errorf("the deposit expires at %s, %s away from the %s lifetime the catalogue gives %q",
			left.expiresAt.UTC(), drift, kind.Lifetime, kind.Key)
	}

	// THE ONE THAT MATTERS. A second player relieving himself is a second row,
	// and this is where a `singleton` gone wrong would show up as silence rather
	// than as an error.
	petDo(t, game, bobAccount, drink.Key, relieve.Key)
	his := petWorldObjectsOwnedBy(t, bobAccount)
	if len(his) != 1 {
		t.Fatalf("a second player used %q and the world holds %d of his deposits; want 1 — the insert is ON CONFLICT DO NOTHING, so a kind wrongly marked singleton loses this row in silence and the verb still answers as though it had worked",
			relieve.Key, len(his))
	}
	if again := petWorldObjectsOwnedBy(t, aliceAccount); len(again) != 1 || again[0].id != left.id {
		t.Fatalf("the first player's deposit is no longer the one and only thing he left: %+v", again)
	}

	// And both come back from the read the plane actually uses.
	live, err := gamevanyagotchi.NewPostgresRepository().LiveWorldObjects(ctx, pool, 200)
	if err != nil {
		t.Fatalf("LiveWorldObjects: %v", err)
	}
	standing := make(map[string]gamevanyagotchi.WorldObject, len(live))
	for _, o := range live {
		standing[o.ID] = o
	}
	for who, row := range map[string]petWorldObject{"the first player": left, "the second player": his[0]} {
		o, ok := standing[row.id]
		if !ok {
			t.Fatalf("%s's deposit is in the table and does not come back from the plane's own read of the yard: %+v", who, live)
		}
		if o.Kind != kind.Key {
			t.Errorf("%s's deposit reads back as kind %q; want %q", who, o.Kind, kind.Key)
		}
		petAtPoint(t, who+"'s deposit as the plane reads it", o.At.X, o.At.Y, gamevanyagotchi.Point{X: row.x, Y: row.y})
		if o.ExpiresAt == nil {
			t.Errorf("%s's deposit reads back with no expiry, so the plane would draw it for ever", who)
		}
	}
}

// TestVanyagotchiPetAGatedVerbIsRefusedUntilTheStatItNeedsIsThere is the
// catalogue's precondition against the tables that have to survive it.
//
// The unit tests prove the arithmetic; what only a real database can say is that
// a refused press leaves the three writes a successful one makes — the stat
// snapshot, the event log and the row in the world — with NOTHING in them, and
// that the value the gate judges him by is derived from `(value, as_of)` read
// against the server's own clock rather than taken off the row. Both halves are
// driven here, refused and then allowed, against one pet whose only change
// between the two is how long ago its rows say it was last touched.
//
// The re-stamp is the failure worth naming. A press that was refused but still
// wrote every row at the current instant would erase exactly the hours the player
// had been away — the hours that were about to make the verb legal — so the
// button would stay refused for ever and every bar would look entirely
// reasonable while it happened.
func TestVanyagotchiPetAGatedVerbIsRefusedUntilTheStatItNeedsIsThere(t *testing.T) {
	relieve := petAction(t, gamevanyagotchi.ActionRelieve)
	if relieve.NeedsStat == "" {
		t.Fatalf("the catalogue no longer gates %q on a stat of its own; this test is about a rule the game does not have", relieve.Key)
	}
	def := petStat(t, relieve.NeedsStat)
	// A filling stat carries a negative rate, so this is what an hour of being
	// away is worth, and `full` is how long an empty one takes to reach what the
	// verb asks for. Derived from the catalogue, so retuning either the rate or
	// the threshold moves both windows below with it rather than leaving this
	// test asserting against a number nobody updated.
	filling := -def.DecayPerHour
	if filling <= 0 {
		t.Fatalf("%s moves by %v an hour, so being away never brings %s within reach and neither half of this test exists",
			def.Key, def.DecayPerHour, relieve.Key)
	}
	if relieve.NeedsAtLeast <= def.Min {
		t.Fatalf("%s asks for %v of %s, at or below its floor %v; the gate could never refuse anything", relieve.Key, relieve.NeedsAtLeast, def.Key, def.Min)
	}
	full := (relieve.NeedsAtLeast - def.Min) / filling

	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7242", "user")
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7242")
	id := petID(t, account)
	t.Cleanup(func() { petForgetWorldObjects(t, account) })

	// A fresh pet's bladder is at its floor, so nine tenths of the way to the
	// threshold is still short of it — and short by a margin no rounding reaches.
	petBackdateAll(t, id, petHours(full*9/10))
	was := petSharedAsOf(t, id)
	before := make(map[string]float64, len(gamevanyagotchi.Stats()))
	for _, s := range gamevanyagotchi.Stats() {
		before[s.Key], _ = petStoredStat(t, id, s.Key)
	}
	if before[def.Key] != def.Min {
		t.Fatalf("the stored %s is %v rather than its floor %v; a pet that had already been played with would make the window above mean something else",
			def.Key, before[def.Key], def.Min)
	}
	events := petEventCount(t, id)

	_, err := game.Do(context.Background(), account, []string{relieve.Key}, "", time.Now().UTC())
	if !errors.Is(err, gamevanyagotchi.ErrNotYet) {
		t.Fatalf("%s after %.2f hours of filling answered %v; want ErrNotYet, because the catalogue asks for %v of %s",
			relieve.Key, full*9/10, err, relieve.NeedsAtLeast, def.Key)
	}

	// The three writes an accepted verb makes, and none of them happened.
	if n := petEventCount(t, id); n != events {
		t.Errorf("the log went from %d events to %d across a refused verb; a refusal is not something that happened to the pet", events, n)
	}
	if left := petWorldObjectsOwnedBy(t, account); len(left) != 0 {
		t.Errorf("%d rows were written into the world by a verb that was refused: %+v", len(left), left)
	}
	if now := petSharedAsOf(t, id); !now.Equal(was) {
		t.Fatalf("every stat was re-stamped from %s to %s by a refused verb; the hours it was refused FOR are the hours that write erases, so the button would never become legal",
			was.UTC(), now.UTC())
	}
	// Every stored value, not only the one the gate looked at: a refusal that
	// wrote a decayed snapshot back would be indistinguishable from one that
	// wrote nothing if all this checked was the bladder it declined to empty.
	for _, s := range gamevanyagotchi.Stats() {
		now, _ := petStoredStat(t, id, s.Key)
		if now != before[s.Key] {
			t.Errorf("the stored %s moved from %v to %v across a refused verb", s.Key, before[s.Key], now)
		}
	}

	// And now the same press, on the same pet, with nothing changed but the
	// instant its rows are stamped at.
	petBackdateAll(t, id, petHours(full*11/10))
	state, err := game.Do(context.Background(), account, []string{relieve.Key}, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("%s after %.2f hours of filling: %v — the stored %s still reads %v, and what the gate judges him by is the value he actually has",
			relieve.Key, full*11/10, err, def.Key, def.Min)
	}
	if !state.Alive {
		t.Fatalf("alive = %v after %s; the windows here are meant to keep him well clear of dying", state.Alive, relieve.Key)
	}
	if value, _ := petStoredStat(t, id, def.Key); value != def.Min {
		t.Errorf("stored %s = %v after %s, want its floor %v", def.Key, value, relieve.Key, def.Min)
	}
	if n := petEventCount(t, id); n != events+1 {
		t.Errorf("the log holds %d events after one accepted verb; want %d", n, events+1)
	}
	if left := petWorldObjectsOwnedBy(t, account); len(left) != 1 {
		t.Errorf("%d rows were written into the world by the accepted %s; want the one deposit: %+v", len(left), relieve.Key, left)
	}
	petRecent(t, "the instant the accepted verb stamped on every stat", petSharedAsOf(t, id))
}

// petContestedRow is one row of a contested kind, read back with none of the
// service's filters.
//
// Unfiltered on purpose: the columns these tests turn on — `claimed_by`,
// `remaining`, `exhausted_at` — are precisely the ones the plane's own SELECT
// hides or never returns, because a claimed key and an emptied crate are things
// that have stopped existing as far as the yard is concerned. What is under test
// is what the UPDATE actually wrote.
//
// One shape for both disciplines rather than one each, because the point of the
// Contest field is that they are the same row with different statements over it:
// a key leaves `remaining` NULL, a crate leaves `claimed_by` NULL, and both set
// `exhausted_at` when they are used up.
type petContestedRow struct {
	id          string
	claimedBy   *string
	claimedAt   *time.Time
	remaining   *int
	exhaustedAt *time.Time
}

// petContestedRowsOf reads every row of a kind ever put out ANYWHERE IN THE
// WORLD, oldest first — including the ones that have been used up, which is the
// whole point of reading it here rather than through the service.
//
// It used to take a location, and it stopped once there were five of them. Both
// contested kinds are singletons and the partial unique index is on `kind` alone,
// so "the active key" is a world-wide question — and a fresh key is hidden in a
// location drawn at random, which means a read scoped to двор would find the
// replacement four times in five... by not finding it, and report an ended hunt
// as a passing test.
func petContestedRowsOf(t *testing.T, kind string) []petContestedRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id::text, claimed_by::text, claimed_at, remaining, exhausted_at
		   FROM game_vanyagotchi_world_objects
		  WHERE kind = $1
		  ORDER BY created_at`,
		kind)
	if err != nil {
		t.Fatalf("read the %s rows: %v", kind, err)
	}
	defer rows.Close()

	var out []petContestedRow
	for rows.Next() {
		var k petContestedRow
		if err := rows.Scan(&k.id, &k.claimedBy, &k.claimedAt, &k.remaining, &k.exhaustedAt); err != nil {
			t.Fatalf("scan a %s row: %v", kind, err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the %s rows: %v", kind, err)
	}
	return out
}

// petLocationOf reads which location one world object is standing in.
//
// Straight out of the table for the reason petWorldObjectPoint is: what is under
// test is what was actually WRITTEN, and a replacement whose location the service
// chose correctly and then inserted from the wrong variable would look identical
// everywhere else.
func petLocationOf(t *testing.T, id string) string {
	t.Helper()
	var key string
	if err := pool.QueryRow(context.Background(),
		`SELECT location_key FROM game_vanyagotchi_world_objects WHERE id = $1::uuid`, id).Scan(&key); err != nil {
		t.Fatalf("read the location of world object %s: %v", id, err)
	}
	return key
}

// petClearTheYardOf removes every row of a kind there is.
//
// Called at the START of a test as well as from its cleanup, and the first of
// those is the one that matters. The key and the crate are both SINGLETONS: the
// database permits exactly one active row of each in the whole world, and every
// hello in this suite stands up whichever is missing — so one left standing by
// an earlier test would be the one this test's players raced for, and the row it
// puts out for them would be swallowed by `ON CONFLICT DO NOTHING` in silence.
// The suite shares one database and has no truncate step, which is why a test
// that writes a contended row owns both ends of its own state.
func petClearTheYardOf(t *testing.T, kind string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM game_vanyagotchi_world_objects WHERE kind = $1`, kind); err != nil {
		t.Fatalf("clear the yard of %s: %v", kind, err)
	}
}

// petWaitForBlockedStatements waits until n statements are queued behind a lock
// somebody else is holding.
//
// The barrier the race below is built on. Letting go of the key before both
// claims have actually reached their UPDATE would let the first through
// uncontended, and the second would then be claiming the key the first had just
// hidden — a different race, decided by scheduling, that says nothing about the
// invariant under test.
//
// Polled rather than waited on because there is nothing out here to wait on: the
// blocked statements are inside two other goroutines' transactions, and their
// only visible trace is Postgres's own lock table. Nothing else in this suite
// runs concurrently, so an ungranted lock is one of these two and nobody else.
func petWaitForBlockedStatements(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&blocked); err != nil {
			t.Fatalf("read the lock table: %v", err)
		}
		if blocked >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d claims ever queued behind the key's row lock; the rest never reached it", blocked, n)
		}
		// Between polls rather than instead of them, exactly as petWaitStanding
		// does: the loop returns the instant both are waiting.
		<-time.After(20 * time.Millisecond)
	}
}

// petEventCount is how many verbs a pet's history holds.
//
// The log is what a refused batch must not leave behind: an event that survived
// a rolled-back claim would be a thing the pet did with no state to show for it,
// and a replay would produce a Ваня the snapshot disagrees with.
func petEventCount(t *testing.T, petIDs string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM game_vanyagotchi_events WHERE pet_id = $1::uuid`, petIDs).Scan(&n); err != nil {
		t.Fatalf("count events for pet %s: %v", petIDs, err)
	}
	return n
}

// TestVanyagotchiExactlyOnePlayerFindsTheKey is the mechanic the whole
// world-object table was shaped for, and the one test in the suite that can prove
// it at all.
//
// TWO PLAYERS RACE AND ONE WINS, and nothing in Go decides which. The guard is
// `claimed_by IS NULL` inside a conditional UPDATE, so the second transaction
// blocks on the row the first one is holding, re-evaluates its own WHERE when
// that commits, and finds nothing to update. A fake repository cannot say
// anything about this: it would be a Go function deciding what a database
// constraint is supposed to decide, and it could agree with itself while the
// index was wrong.
//
// Four properties, and the last two are what make losing harmless. The winner is
// recorded on the row, exhausting it in the same statement. A FRESH key exists
// afterwards, hidden inside the winner's own transaction — so the hunt restarts
// without anything scheduling it, and the world is never left with none. The
// winner's tally moves. And the loser's pet is untouched down to the as_of on its
// stat rows: the claim is inside the transaction, so a lost race rolls back the
// snapshot and the events with it.
func TestVanyagotchiExactlyOnePlayerFindsTheKey(t *testing.T) {
	ctx := context.Background()
	claim := petAction(t, gamevanyagotchi.ActionClaim)
	tally := petEffectOn(claim, gamevanyagotchi.StatKeysFound)
	if tally <= 0 {
		t.Fatalf("the catalogue says %q moves %q by %v; this test is reasoning about a verb that counts something",
			claim.Key, gamevanyagotchi.StatKeysFound, tally)
	}
	kind, ok := gamevanyagotchi.ObjectKindByKey(gamevanyagotchi.KindKey)
	if !ok {
		t.Fatalf("the catalogue has no object kind %q", gamevanyagotchi.KindKey)
	}
	if !kind.Singleton {
		t.Fatalf("the catalogue no longer marks %q a singleton; the index would then allow any number of keys and there would be nothing to contest",
			kind.Key)
	}

	app, game := petApp(t)
	accounts := map[string]string{}
	pets := map[string]string{}
	for who, uid := range map[string]string{"alice": "7250", "bob": "7251"} {
		cli := loginAs(t, app.URL, uid, "user")
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("%s create: status=%d body=%v", who, s, body)
		}
		accounts[who] = accountIDByUID(t, uid)
		pets[who] = petID(t, accounts[who])
	}
	// The instant every stat of each pet is currently stamped with. A lost claim
	// must leave it exactly where it is, which is the finest-grained way of saying
	// "nothing at all was written" — a rolled-back batch and a batch that wrote
	// the same numbers again are otherwise indistinguishable.
	stamped := map[string]time.Time{
		"alice": petSharedAsOf(t, pets["alice"]),
		"bob":   petSharedAsOf(t, pets["bob"]),
	}

	petClearTheYardOf(t, gamevanyagotchi.KindKey)
	t.Cleanup(func() { petClearTheYardOf(t, gamevanyagotchi.KindKey) })

	// One key, hidden by hand IN A HOTSPOT. The service hides its own at a hello,
	// which is a socket and therefore this file's neighbour's subject; what this
	// test needs is a key whose id it knows. It has to be in a real hotspot rather
	// than merely somewhere, because a claim is now judged by asking which spot
	// the key's coordinates resolve to — a key in the middle of the yard would be
	// found by whichever hotspot happened to be nearest, which is a fixture that
	// works by accident.
	spot := petYardHotspot(t, 0)
	repo := gamevanyagotchi.NewPostgresRepository()
	if err := repo.InsertWorldObject(ctx, pool, kind.Key, gamevanyagotchi.LocationYard,
		spot.At, "", kind.Singleton, nil, nil); err != nil {
		t.Fatalf("hide the key: %v", err)
	}
	hidden := petContestedRowsOf(t, gamevanyagotchi.KindKey)
	if len(hidden) != 1 {
		t.Fatalf("%d keys are in the yard before the race; want exactly the one this test hid: %+v", len(hidden), hidden)
	}
	lost := hidden[0]

	// AND BOTH OF THEM STANDING IN IT. A search is refused unless the server's
	// own placement says he is at the spot he named, so a race for the key is now
	// a race between two people who have both already walked there — which is
	// what the mechanic is for. Done after the key is hidden, so the hello inside
	// finds a hunt already running and does not stand a second one up.
	for _, account := range accounts {
		petSearchingIn(t, game, account, spot)
	}

	// BOTH ON THE SAME KEY, and that is arranged rather than hoped for.
	//
	// The test takes the key's row lock first, so both claims reach their UPDATE
	// and queue behind it with their statement snapshots already taken; releasing
	// it lets exactly one of them through. Without the lock the two are only as
	// concurrent as goroutine scheduling happens to make them, and a claim that
	// starts AFTER the winner has committed is not contending for the same key at
	// all: by then the winner has hidden a fresh one, and the UPDATE names a KIND
	// rather than an id, so the latecomer finds the replacement and wins it. That
	// is the game working — the hunt restarted and somebody found the new key —
	// but as a test it would pass or fail by timing and say nothing about the
	// invariant. This is the contended case, deterministically.
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the transaction that holds the key still: %v", err)
	}
	// The failure path. The lock is released explicitly below, before anything is
	// asserted; this is what lets go of it when a Fatalf jumps out first.
	defer func() { _ = holder.Rollback(ctx) }()
	var pinned string
	if err := holder.QueryRow(ctx,
		`SELECT id::text FROM game_vanyagotchi_world_objects WHERE id = $1::uuid FOR UPDATE`, lost.id).Scan(&pinned); err != nil {
		t.Fatalf("hold the key's row still: %v", err)
	}

	type outcome struct {
		who string
		err error
	}
	results := make(chan outcome, len(accounts))
	var wg sync.WaitGroup
	for who, account := range accounts {
		wg.Add(1)
		go func(who, account string) {
			defer wg.Done()
			_, err := game.Do(ctx, account, []string{claim.Key}, spot.Key, time.Now().UTC())
			results <- outcome{who: who, err: err}
		}(who, account)
	}
	petWaitForBlockedStatements(t, len(accounts))
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("let go of the key: %v", err)
	}
	wg.Wait()
	close(results)

	var winner, loser string
	for r := range results {
		switch {
		case r.err == nil:
			if winner != "" {
				t.Fatalf("both %s and %s were told they had found the same key; the conditional UPDATE is not deciding this", winner, r.who)
			}
			winner = r.who
		case errors.Is(r.err, gamevanyagotchi.ErrClaimLost):
			if loser != "" {
				t.Fatalf("both %s and %s lost the race; the key was there and somebody had to find it", loser, r.who)
			}
			loser = r.who
		default:
			t.Fatalf("%s's claim failed for a reason that is not a lost race: %v", r.who, r.err)
		}
	}
	if winner == "" || loser == "" {
		t.Fatalf("the race produced winner=%q loser=%q; want exactly one of each", winner, loser)
	}
	t.Logf("%s found the key and %s did not", winner, loser)

	// The row itself: claimed by the winner, and used up by the same statement
	// that claimed it. Doing those in two statements would leave a window in
	// which the partial index still held the old row and the replacement below
	// would have been silently swallowed.
	keys := petContestedRowsOf(t, gamevanyagotchi.KindKey)
	if len(keys) != 2 {
		t.Fatalf("%d keys are in the yard after one was found; want the one that was claimed and the one that replaced it: %+v", len(keys), keys)
	}
	var claimed, fresh []petContestedRow
	for _, k := range keys {
		if k.exhaustedAt == nil {
			fresh = append(fresh, k)
			continue
		}
		claimed = append(claimed, k)
	}
	if len(claimed) != 1 || len(fresh) != 1 {
		t.Fatalf("%d keys are used up and %d are still lost; want one of each — two active keys is the invariant the partial unique index exists to hold: %+v",
			len(claimed), len(fresh), keys)
	}
	if claimed[0].id != lost.id {
		t.Errorf("the key that was used up is %s; want the one that was hidden, %s", claimed[0].id, lost.id)
	}
	if claimed[0].claimedBy == nil || *claimed[0].claimedBy != accounts[winner] {
		t.Errorf("the key is recorded as claimed by %v; want %s, who was told he found it", claimed[0].claimedBy, winner)
	}
	if claimed[0].claimedAt == nil {
		t.Error("the key was claimed with no claimed_at; the moment is written by the same UPDATE as the claimant")
	}

	// The restart. Hidden inside the winner's own transaction, so the very next
	// frame already carries a hunt rather than an empty yard somebody has to be
	// told about — and it is a NEW row, not the old one reopened.
	if fresh[0].id == lost.id {
		t.Fatal("the key that was found is still the active one; it was claimed without being exhausted")
	}
	if fresh[0].claimedBy != nil {
		t.Errorf("the replacement key is already claimed by %v", fresh[0].claimedBy)
	}
	id, running, err := repo.ActiveSingleton(ctx, pool, kind.Key)
	if err != nil {
		t.Fatalf("ActiveSingleton: %v", err)
	}
	if !running {
		t.Fatal("no hunt is running after one was won; the yard would stay empty until somebody said hello")
	}
	if id != fresh[0].id {
		t.Errorf("the running hunt is %s; want the replacement %s", id, fresh[0].id)
	}

	// The tally, and the whole of what losing costs.
	won, _ := petStoredStat(t, pets[winner], gamevanyagotchi.StatKeysFound)
	petNear(t, "the winner's stored "+gamevanyagotchi.StatKeysFound, won, tally)
	lostTally, _ := petStoredStat(t, pets[loser], gamevanyagotchi.StatKeysFound)
	petNear(t, "the loser's stored "+gamevanyagotchi.StatKeysFound, lostTally, 0)

	if n := petEventCount(t, pets[winner]); n != 1 {
		t.Errorf("the winner's history holds %d events; want the one %q he was allowed", n, claim.Key)
	}
	if n := petEventCount(t, pets[loser]); n != 0 {
		t.Errorf("the loser's history holds %d events; looking for the keys and not finding them is not something that happened to his pet", n)
	}
	if got := petSharedAsOf(t, pets[loser]); !got.Equal(stamped[loser]) {
		t.Errorf("the loser's stats are stamped %s; want the %s they carried before the race — the batch is rolled back, so not even the re-stamp survives",
			got.UTC(), stamped[loser].UTC())
	}
	petRecent(t, "the winner's stats after finding the key", petSharedAsOf(t, pets[winner]))
}

// TestVanyagotchiTheCrateCannotBeOversold is the Stock discipline's half of what
// the world-object table was shaped for, and the counterpart of the key race
// above.
//
// TWO PLAYERS, ONE BEER, AND NOTHING IN GO DECIDES WHICH OF THEM GETS IT. The
// guard is `remaining > 0` inside a conditional UPDATE, so the second
// transaction blocks on the row the first is holding, re-evaluates its own WHERE
// against the value the first committed, and finds nothing left to take. A fake
// repository cannot say anything about this: it would be a Go function deciding
// what a row lock decides, and it could agree with itself while the statement
// was wrong.
//
// Four properties, and the last two are what make losing harmless. Exactly one
// drink lands. The crate is left at nought and exhausted by the same statement
// that emptied it. A FRESH, FULL crate exists afterwards, stood up inside the
// winner's own transaction — so the store restocks without anything scheduling
// it, and the yard is never left dry. And the loser's pet is untouched down to
// the as_of on its stat rows: the draw is inside the transaction, so a lost race
// rolls back the snapshot and the events with it.
func TestVanyagotchiTheCrateCannotBeOversold(t *testing.T) {
	ctx := context.Background()
	drink := petAction(t, gamevanyagotchi.ActionDrink)
	crate := petObjectKind(t, gamevanyagotchi.KindCrate)
	if crate.Contest != gamevanyagotchi.ContestStock {
		t.Fatalf("the catalogue settles %q by %q; this test is about the stock discipline", crate.Key, crate.Contest)
	}
	if !crate.Singleton {
		t.Fatalf("the catalogue no longer marks %q a singleton; the UPDATE names a KIND rather than a row, so a second live crate would be decremented at the same time",
			crate.Key)
	}
	if drink.Contests != crate.Key {
		t.Fatalf("%q contests %q rather than %q; this test is racing the wrong verb", drink.Key, drink.Contests, crate.Key)
	}

	app, game := petApp(t)
	accounts := map[string]string{}
	pets := map[string]string{}
	for who, uid := range map[string]string{"alice": "7260", "bob": "7261"} {
		cli := loginAs(t, app.URL, uid, "user")
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("%s create: status=%d body=%v", who, s, body)
		}
		accounts[who] = accountIDByUID(t, uid)
		pets[who] = petID(t, accounts[who])
	}
	// The instant every stat of each pet is currently stamped with. A lost draw
	// must leave it exactly where it is, which is the finest-grained way of saying
	// "nothing at all was written" — a rolled-back batch and a batch that wrote
	// the same numbers again are otherwise indistinguishable.
	stamped := map[string]time.Time{
		"alice": petSharedAsOf(t, pets["alice"]),
		"bob":   petSharedAsOf(t, pets["bob"]),
	}

	petClearTheYardOf(t, crate.Key)
	t.Cleanup(func() { petClearTheYardOf(t, crate.Key) })

	// ONE BEER IN THE WHOLE WORLD, put out by hand. The service stands a full
	// crate up at a hello, which is its neighbour's subject; what this test needs
	// is a crate holding exactly one, so that two players pressing at once is a
	// race with a loser in it.
	one := 1
	repo := gamevanyagotchi.NewPostgresRepository()
	if err := repo.InsertWorldObject(ctx, pool, crate.Key, gamevanyagotchi.LocationYard,
		*crate.At, "", crate.Singleton, &one, nil); err != nil {
		t.Fatalf("put out a crate with one beer in it: %v", err)
	}
	standing := petContestedRowsOf(t, crate.Key)
	if len(standing) != 1 {
		t.Fatalf("%d crates are in the yard before the race; want exactly the one this test put out: %+v", len(standing), standing)
	}
	last := standing[0]

	// Both of them at it, and both of them told there is beer — the hello fills
	// the world cache the arrival gate reads, and the tap puts them at the crate.
	for _, account := range accounts {
		petStandAtTheBeerStore(t, game, account)
	}

	// BOTH ON THE SAME CRATE, and that is arranged rather than hoped for.
	//
	// The test takes the crate's row lock first, so both draws reach their UPDATE
	// and queue behind it with their statement snapshots already taken; releasing
	// it lets exactly one of them through. Without the lock the two are only as
	// concurrent as goroutine scheduling happens to make them, and a draw that
	// starts AFTER the winner has committed is not contending for the same crate
	// at all: by then the winner has stood a full one up, and the UPDATE names a
	// KIND rather than an id, so the latecomer finds the replacement and drinks
	// from it. That is the game working — the store restocked and somebody bought
	// a beer — but as a test it would pass or fail by timing and say nothing about
	// the invariant. This is the contended case, deterministically.
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the transaction that holds the crate still: %v", err)
	}
	// The failure path. The lock is released explicitly below, before anything is
	// asserted; this is what lets go of it when a Fatalf jumps out first.
	defer func() { _ = holder.Rollback(ctx) }()
	var pinned string
	if err := holder.QueryRow(ctx,
		`SELECT id::text FROM game_vanyagotchi_world_objects WHERE id = $1::uuid FOR UPDATE`, last.id).Scan(&pinned); err != nil {
		t.Fatalf("hold the crate's row still: %v", err)
	}

	type outcome struct {
		who string
		err error
	}
	results := make(chan outcome, len(accounts))
	var wg sync.WaitGroup
	for who, account := range accounts {
		wg.Add(1)
		go func(who, account string) {
			defer wg.Done()
			_, err := game.Do(ctx, account, []string{drink.Key}, "", time.Now().UTC())
			results <- outcome{who: who, err: err}
		}(who, account)
	}
	petWaitForBlockedStatements(t, len(accounts))
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("let go of the crate: %v", err)
	}
	wg.Wait()
	close(results)

	var winner, loser string
	for r := range results {
		switch {
		case r.err == nil:
			if winner != "" {
				t.Fatalf("both %s and %s got the last beer; the conditional UPDATE is not deciding this and the crate has been oversold", winner, r.who)
			}
			winner = r.who
		case errors.Is(r.err, gamevanyagotchi.ErrOutOfStock):
			if loser != "" {
				t.Fatalf("both %s and %s were told the crate was empty; there was a beer in it and somebody had to get it", loser, r.who)
			}
			loser = r.who
		default:
			t.Fatalf("%s's drink failed for a reason that is not an empty crate: %v", r.who, r.err)
		}
	}
	if winner == "" || loser == "" {
		t.Fatalf("the race produced winner=%q loser=%q; want exactly one of each", winner, loser)
	}
	t.Logf("%s got the last beer and %s did not", winner, loser)

	// The crate as the database left it: emptied and exhausted by the same
	// statement, and a full one standing beside it.
	crates := petContestedRowsOf(t, crate.Key)
	if len(crates) != 2 {
		t.Fatalf("%d crates are in the yard after the race; want the emptied one and its replacement: %+v", len(crates), crates)
	}
	emptied, replacement := crates[0], crates[1]
	if emptied.id != last.id {
		t.Fatalf("the first crate in the yard is %s; want the one this test put out, %s", emptied.id, last.id)
	}
	if emptied.remaining == nil || *emptied.remaining != 0 {
		t.Errorf("the crate that was drunk from holds %v; want 0 — the decrement and the guard are one statement, so it cannot go below and must not stop above",
			emptied.remaining)
	}
	if emptied.exhaustedAt == nil {
		t.Error("the emptied crate has no exhausted_at; it would stay in the partial unique index for ever, and no replacement could ever be inserted")
	}
	if replacement.exhaustedAt != nil {
		t.Errorf("the replacement crate is already exhausted (%s); the store would be dry from the moment it restocked", replacement.exhaustedAt.UTC())
	}
	if replacement.remaining == nil || *replacement.remaining != crate.Stock {
		t.Errorf("the replacement crate holds %v; want the catalogue's %d — a fresh crate was put out rather than the empty one being left",
			replacement.remaining, crate.Stock)
	}

	// The winner drank, and it is on his tally.
	beers := petEffectOn(drink, gamevanyagotchi.StatBeersDrunk)
	if beers <= 0 {
		t.Fatalf("the catalogue says %q moves %q by %v; this test is reasoning about a verb that counts something",
			drink.Key, gamevanyagotchi.StatBeersDrunk, beers)
	}
	if got := petStoredValue(t, pets[winner], gamevanyagotchi.StatBeersDrunk); got != beers {
		t.Errorf("the winner's %q is %v after his first beer; want %v", gamevanyagotchi.StatBeersDrunk, got, beers)
	}

	// AND THE LOSER WAS CHARGED NOTHING AT ALL — not a stat, not an event, and
	// not the re-stamp every accepted verb performs. The draw is inside the
	// transaction, so returning from it takes the whole batch back out.
	if got := petStoredValue(t, pets[loser], gamevanyagotchi.StatBeersDrunk); got != 0 {
		t.Errorf("the loser's %q is %v; want 0 — losing a race for the last beer costs nothing", gamevanyagotchi.StatBeersDrunk, got)
	}
	if n := petEventCount(t, pets[loser]); n != 0 {
		t.Errorf("%d events survived the loser's refused drink; reaching for a beer that was not there is not something that happened to his pet", n)
	}
	if now := petSharedAsOf(t, pets[loser]); !now.Equal(stamped[loser]) {
		t.Errorf("the loser's stats are stamped %s; want the %s they carried before the race — a rolled-back batch must not leave a re-stamp behind, which is silently erased damage",
			now.UTC(), stamped[loser].UTC())
	}
	if n := petEventCount(t, pets[winner]); n != 1 {
		t.Errorf("the winner's log holds %d events for one accepted drink; want 1", n)
	}
}

// TestVanyagotchiDrinkingIsRefusedFromAcrossTheYardAndWritesNothing is the
// arrival gate against a real database, which is the only place the "and writes
// nothing" half can be proved.
//
// The gate itself is decided entirely in memory — where the yard believes he is
// standing, against where the crate is in the world cache — and it has its
// exhaustive tests in internal/gamevanyagotchi. What this adds is the durable
// consequence: a refused drink must leave the stat rows, their shared as_of, the
// event log and the crate's own `remaining` exactly as they were, and a response
// cannot show any of that.
func TestVanyagotchiDrinkingIsRefusedFromAcrossTheYardAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	drink := petAction(t, gamevanyagotchi.ActionDrink)
	crate := petObjectKind(t, gamevanyagotchi.KindCrate)
	if drink.NeedsNear != crate.Key {
		t.Fatalf("the catalogue gates %q on being near %q rather than %q; this test is about a rule the game does not have",
			drink.Key, drink.NeedsNear, crate.Key)
	}

	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7262", "user")
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7262")
	id := petID(t, account)

	// At the store first — which is what fills the world cache and guarantees a
	// crate exists — and then walked away from it, to the opposite corner of the
	// plane. Walking away rather than never arriving is the case worth driving:
	// it proves the gate reads his position now rather than remembering that he
	// once qualified.
	petStandAtTheBeerStore(t, game, account)
	away := gamevanyagotchi.Point{X: 1 - crate.At.X, Y: 1 - crate.At.Y}
	game.HandleInbound(ctx, realtime.Member{ConnID: "conn-" + account, AccountID: account},
		httpapi.DefaultRoom, fmt.Appendf(nil, `{"t":"vanyagotchi_move","x":%v,"y":%v}`, away.X, away.Y))

	before := petSharedAsOf(t, id)
	beers := petStoredValue(t, id, gamevanyagotchi.StatBeersDrunk)
	events := petEventCount(t, id)
	stock := petLiveCrateStock(t)

	if _, err := game.Do(ctx, account, []string{drink.Key}, "", time.Now().UTC()); !errors.Is(err, gamevanyagotchi.ErrTooFar) {
		t.Fatalf("Do(%s) from across the yard = %v; want ErrTooFar", drink.Key, err)
	}

	if now := petSharedAsOf(t, id); !now.Equal(before) {
		t.Errorf("his stats are stamped %s after a refused drink; want the %s they carried before it — a refusal charged him a re-stamp, which is silently erased damage",
			now.UTC(), before.UTC())
	}
	if got := petStoredValue(t, id, gamevanyagotchi.StatBeersDrunk); got != beers {
		t.Errorf("his %q went from %v to %v on a drink he never got", gamevanyagotchi.StatBeersDrunk, beers, got)
	}
	if n := petEventCount(t, id); n != events {
		t.Errorf("his log holds %d events after a refused drink; want the %d it held before", n, events)
	}
	if now := petLiveCrateStock(t); now != stock {
		t.Errorf("the crate holds %d after a drink that was refused before the transaction; want the %d it held before — nobody took a beer", now, stock)
	}

	// And walking back is all it takes: the gate stores no answer, so there is
	// nothing about the refusal for him to undo.
	petStandAtTheBeerStore(t, game, account)
	if _, err := game.Do(ctx, account, []string{drink.Key}, "", time.Now().UTC()); err != nil {
		t.Fatalf("Do(%s) back at the crate: %v; the gate asks about now and remembers nothing", drink.Key, err)
	}
	if now := petLiveCrateStock(t); now != stock-1 {
		t.Errorf("the crate holds %d after one accepted drink; want %d", now, stock-1)
	}
}

// petLiveCrateStock is what the one active crate in the yard currently holds.
//
// Read through the plane's OWN query rather than by hand, because "the crate the
// world is drawing" and "some row of that kind" are different things the moment
// one has been exhausted — and the exhausted ones stay in the table for ever,
// since nothing sweeps.
func petLiveCrateStock(t *testing.T) int {
	t.Helper()
	live, err := gamevanyagotchi.NewPostgresRepository().LiveWorldObjects(context.Background(), pool, 200)
	if err != nil {
		t.Fatalf("read the yard: %v", err)
	}
	for _, o := range live {
		if o.Kind != gamevanyagotchi.KindCrate {
			continue
		}
		if o.Remaining == nil {
			t.Fatalf("the crate standing in the yard has a NULL remaining; nothing could ever draw from it")
		}
		return *o.Remaining
	}
	t.Fatal("there is no crate standing in the yard at all; a hello stands one up when it finds none, so this is a store that will never restock")
	return 0
}

// petStoredValue is one stat's stored value, undecayed. A counter never decays,
// so for the tallies this is also its current value.
func petStoredValue(t *testing.T, petIDs, statKey string) float64 {
	t.Helper()
	value, _ := petStoredStat(t, petIDs, statKey)
	return value
}

// ---------------------------------------------------------------------------
// «искать ключи» as a SEARCH, against a real database.
//
// The gate itself is decided entirely in memory — the spot against the
// catalogue, then his own placement against that spot — and it has its exhaustive
// tests in internal/gamevanyagotchi. What this file adds is the durable half,
// which is the half a response cannot show: a refused search must leave the stat
// rows, their shared as_of, the event log and the key's own row exactly as they
// were, and a winning one must have moved the key through PostgreSQL rather than
// through anything in Go.
// ---------------------------------------------------------------------------

// petYardHotspot is one of the yard's published places, by index.
//
// By index rather than by key, for the reason petAction and petObjectKind are
// looked up rather than written down: the places in the yard are content
// somebody is meant to move by feel, and a test naming «куст» would break the day
// somebody renamed a bush. What these tests need is "a place" and "a different
// place".
func petYardHotspot(t *testing.T, i int) gamevanyagotchi.Hotspot {
	t.Helper()
	yard, ok := gamevanyagotchi.LocationByKey(gamevanyagotchi.LocationYard)
	if !ok {
		t.Fatalf("the catalogue has no location %q", gamevanyagotchi.LocationYard)
	}
	if i >= len(yard.Hotspots) {
		t.Fatalf("%q publishes %d hotspots and this test wants index %d; a search needs somewhere to be wrong as well as somewhere to be right",
			gamevanyagotchi.LocationYard, len(yard.Hotspots), i)
	}
	return yard.Hotspots[i]
}

// petSearchingIn stands an account in a hotspot, with the yard loaded.
//
// The two real client frames rather than a reach into the service, exactly as
// petStandAtTheBeerStore does and for the same reasons. The hello is the
// human-paced moment the yard reads the world — which is what puts the key into
// the cache the gate reads, and stands one up if the world has none — and the tap
// is a teleport, because no broadcast has ever run in this suite so there is no
// clock to measure a journey against.
func petSearchingIn(t *testing.T, game *gamevanyagotchi.Service, accountID string, spot gamevanyagotchi.Hotspot) {
	t.Helper()
	m := realtime.Member{ConnID: "conn-" + accountID, AccountID: accountID}
	game.HandleInbound(context.Background(), m, httpapi.DefaultRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	game.HandleInbound(context.Background(), m, httpapi.DefaultRoom,
		fmt.Appendf(nil, `{"t":"vanyagotchi_move","x":%v,"y":%v}`, spot.At.X, spot.At.Y))
}

// TestVanyagotchiASearchThatIsRefusedWritesNothingAtAll is the durable half of
// every way the gate says no, including the frame «искать ключи» used to be.
//
// The first case is the deletion test for the press-anywhere claim: a bare
// `{"verbs":["claim"]}` with no spot is exactly what I8c accepted from anywhere in
// the yard, and it must now be refused even from on top of the key. The rest are
// the other three ways to ask badly. Every one of them has to leave the database
// as it found it — and "as it found it" includes the shared as_of, because a
// re-stamp on a refused batch is silently erased damage rather than a blemish.
func TestVanyagotchiASearchThatIsRefusedWritesNothingAtAll(t *testing.T) {
	ctx := context.Background()
	claim := petAction(t, gamevanyagotchi.ActionClaim)
	kind, ok := gamevanyagotchi.ObjectKindByKey(claim.Contests)
	if !ok || !kind.Hidden {
		t.Fatalf("%q contests %q, which is not a hidden kind; this test is about a mechanic the game does not have", claim.Key, claim.Contests)
	}

	here, elsewhere := petYardHotspot(t, 0), petYardHotspot(t, 1)

	for i, tc := range []struct {
		name  string
		spot  string
		stand gamevanyagotchi.Hotspot
		want  error
		why   string
	}{
		{
			name: "the press-anywhere claim, from on top of the key", spot: "", stand: here,
			want: gamevanyagotchi.ErrNoSpot,
			why:  "the frame I8c accepted — no spot at all — and it must not work even where the answer is",
		},
		{
			name: "a spot no location has", spot: "под-луной", stand: here,
			want: gamevanyagotchi.ErrNoSpot,
			why:  "an arbitrary string off the wire is resolved against the catalogue before anything else happens",
		},
		{
			name: "the right spot, without having walked to it", spot: here.Key, stand: elsewhere,
			want: gamevanyagotchi.ErrTooFar,
			why:  "the client announcing an arrival is a request, never a fact",
		},
		{
			name: "a spot he is standing in, with nothing in it", spot: elsewhere.Key, stand: elsewhere,
			want: gamevanyagotchi.ErrNothingHere,
			why:  "he asked properly and looked properly; this is the refusal that is a move in the game",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, game := petApp(t)
			uid := fmt.Sprintf("729%d", i)
			cli := loginAs(t, app.URL, uid, "user")
			if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
				t.Fatalf("create: status=%d body=%v", s, body)
			}
			account := accountIDByUID(t, uid)
			id := petID(t, account)

			petClearTheYardOf(t, kind.Key)
			t.Cleanup(func() { petClearTheYardOf(t, kind.Key) })
			repo := gamevanyagotchi.NewPostgresRepository()
			if err := repo.InsertWorldObject(ctx, pool, kind.Key, gamevanyagotchi.LocationYard,
				here.At, "", kind.Singleton, nil, nil); err != nil {
				t.Fatalf("hide the key: %v", err)
			}
			petSearchingIn(t, game, account, tc.stand)

			before := petSharedAsOf(t, id)
			found := petStoredValue(t, id, gamevanyagotchi.StatKeysFound)
			events := petEventCount(t, id)
			keys := petContestedRowsOf(t, kind.Key)

			if _, err := game.Do(ctx, account, []string{claim.Key}, tc.spot, time.Now().UTC()); !errors.Is(err, tc.want) {
				t.Fatalf("Do(%s, spot=%q) = %v; want %v — %s", claim.Key, tc.spot, err, tc.want, tc.why)
			}

			if now := petSharedAsOf(t, id); !now.Equal(before) {
				t.Errorf("his stats are stamped %s after a refused search; want the %s they carried before it — a refusal charged him a re-stamp, which is silently erased damage",
					now.UTC(), before.UTC())
			}
			if got := petStoredValue(t, id, gamevanyagotchi.StatKeysFound); got != found {
				t.Errorf("his %q went from %v to %v on a search that was refused", gamevanyagotchi.StatKeysFound, found, got)
			}
			if n := petEventCount(t, id); n != events {
				t.Errorf("his log holds %d events after a refused search; want the %d it held before — looking in the wrong place is not something that happened to his pet", n, events)
			}
			// AND THE KEY IS STILL LOST. Not merely unclaimed by him: the row must
			// be untouched, because a refused search that had exhausted it would
			// have ended the hunt with nothing to replace it.
			now := petContestedRowsOf(t, kind.Key)
			if len(now) != len(keys) {
				t.Fatalf("the yard holds %d keys after a refused search; want the %d it held before: %+v", len(now), len(keys), now)
			}
			for j := range now {
				if now[j].claimedBy != nil || now[j].exhaustedAt != nil {
					t.Errorf("key %s is claimed/exhausted after a refused search: %+v", now[j].id, now[j])
				}
			}
		})
	}
}

// TestVanyagotchiSearchingTheRightPlaceFindsTheKeysAndHidesTheNextOneInAnother is
// the winning half, and the property that keeps the game running afterwards.
//
// Two things are proved here that no unit test can. The claim really moved a row
// in PostgreSQL — claimed, exhausted, and the tally written down — and the
// REPLACEMENT it stood up in the same transaction is itself at a hotspot. That
// second half is the one that would rot silently: a replacement hidden anywhere
// else on the plane would resolve to no spot key at all, so every later search by
// every player would answer «тут пусто» for ever, while a hunt id rode the frame
// the whole time saying one was running.
func TestVanyagotchiSearchingTheRightPlaceFindsTheKeysAndHidesTheNextOneInAnother(t *testing.T) {
	ctx := context.Background()
	claim := petAction(t, gamevanyagotchi.ActionClaim)
	tally := petEffectOn(claim, gamevanyagotchi.StatKeysFound)
	if tally <= 0 {
		t.Fatalf("the catalogue says %q moves %q by %v; there would be nothing for a finder to gain", claim.Key, gamevanyagotchi.StatKeysFound, tally)
	}
	kind, ok := gamevanyagotchi.ObjectKindByKey(claim.Contests)
	if !ok || !kind.Hidden {
		t.Fatalf("%q contests %q, which is not a hidden kind", claim.Key, claim.Contests)
	}
	spot := petYardHotspot(t, 2)

	app, game := petApp(t)
	cli := loginAs(t, app.URL, "7295", "user")
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7295")
	id := petID(t, account)

	petClearTheYardOf(t, kind.Key)
	t.Cleanup(func() { petClearTheYardOf(t, kind.Key) })
	repo := gamevanyagotchi.NewPostgresRepository()
	if err := repo.InsertWorldObject(ctx, pool, kind.Key, gamevanyagotchi.LocationYard,
		spot.At, "", kind.Singleton, nil, nil); err != nil {
		t.Fatalf("hide the key: %v", err)
	}
	petSearchingIn(t, game, account, spot)

	found := petStoredValue(t, id, gamevanyagotchi.StatKeysFound)
	if _, err := game.Do(ctx, account, []string{claim.Key}, spot.Key, time.Now().UTC()); err != nil {
		t.Fatalf("Do(%s) in %q, which is where the keys are: %v", claim.Key, spot.Key, err)
	}

	if got, want := petStoredValue(t, id, gamevanyagotchi.StatKeysFound), found+tally; got != want {
		t.Errorf("his %q is %v after finding the keys; want %v", gamevanyagotchi.StatKeysFound, got, want)
	}
	if n := petEventCount(t, id); n != 1 {
		t.Errorf("his log holds %d events for one accepted search; want 1", n)
	}

	keys := petContestedRowsOf(t, kind.Key)
	if len(keys) != 2 {
		t.Fatalf("%d keys are in the yard after one was found; want the one that was claimed and the one that replaced it: %+v", len(keys), keys)
	}
	var fresh []petContestedRow
	for _, k := range keys {
		if k.exhaustedAt == nil {
			fresh = append(fresh, k)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("%d keys are still lost; the partial unique index permits exactly one: %+v", len(fresh), keys)
	}

	// THE ASSERTION THIS TEST EXISTS FOR: the next key is somewhere a player can
	// name. Read back out of Postgres rather than off the insert, because a
	// coordinate that survived the column's CHECK is not the same claim as a
	// coordinate that is one of the places the client is told about.
	//
	// AND IT IS JUDGED AGAINST THE LOCATION THE ROW ACTUALLY NAMES. A key names no
	// location in the catalogue, so a fresh one is hidden in one drawn with
	// crypto/rand — the whole point being that finding one tells you nothing about
	// where the next is. Checking it against двор would have been a test that
	// passed one time in five for the wrong reason.
	where, ok := gamevanyagotchi.LocationByKey(petLocationOf(t, fresh[0].id))
	if !ok {
		t.Fatalf("the replacement key was hidden in %q, which is not a location in the catalogue; nobody could ever search there",
			petLocationOf(t, fresh[0].id))
	}
	at, err := petWorldObjectPoint(t, fresh[0].id)
	if err != nil {
		t.Fatalf("read the replacement key's position: %v", err)
	}
	var landed string
	for _, h := range where.Hotspots {
		if h.At == at {
			landed = h.Key
		}
	}
	if landed == "" {
		t.Fatalf("the replacement key is at (%v,%v), which is not any of %q's hotspots; no spot key would ever resolve to it and every later search would answer «тут пусто» for ever",
			at.X, at.Y, where.Key)
	}
	t.Logf("the next key is in %q, in %q", landed, where.Key)
}

// petWorldObjectPoint reads where one world object is standing.
//
// By id and straight out of the table, because the question is what was actually
// WRITTEN: a replacement whose coordinates the service computed correctly and
// then inserted from the wrong variable would look identical everywhere else.
func petWorldObjectPoint(t *testing.T, id string) (gamevanyagotchi.Point, error) {
	t.Helper()
	var p gamevanyagotchi.Point
	err := pool.QueryRow(context.Background(),
		`SELECT x, y FROM game_vanyagotchi_world_objects WHERE id = $1::uuid`, id).Scan(&p.X, &p.Y)
	return p, err
}

// petSearchIn presses «искать ключи» naming a place, and fails the test on a
// refusal.
//
// The sibling of petDo for the one verb that now carries a payload the server
// has to judge. It exists rather than a spot parameter on petDo because every
// other verb in the catalogue sends none, and threading an empty string through
// forty call sites would say the opposite of what is true — a spot belongs to a
// search and to nothing else.
func petSearchIn(t *testing.T, game *gamevanyagotchi.Service, accountID string, spot gamevanyagotchi.Hotspot) gamevanyagotchi.State {
	t.Helper()
	st, err := game.Do(context.Background(), accountID,
		[]string{gamevanyagotchi.ActionClaim}, spot.Key, time.Now().UTC())
	if err != nil {
		t.Fatalf("searching %q for account %s: %v", spot.Key, accountID, err)
	}
	return st
}

// TestVanyagotchiTheWorldReadIsCappedPerLocationAndKeepsItsFixtures is the
// window function in LiveWorldObjects, against a real PostgreSQL — and it is the
// half no unit test can reach, because the fake repository deliberately does not
// reimplement the ordering.
//
// TWO PROPERTIES, and they fail in opposite directions. The cap is PER LOCATION,
// so a yard filled past its allowance cannot spend кусты's — a single world-wide
// `LIMIT` would have left the other four places empty on a busy evening,
// including whichever one holds the key, and the hunt would have ended with
// nothing failing anywhere. And singletons are ordered FIRST, so a pile of fresh
// deposits cannot push the beer crate out of the read: the crate is the oldest
// row in its location, because it is stood up once and everything else piles on
// afterwards, so newest-first alone would evict it and leave a yard where the
// store has vanished and nobody can drink.
func TestVanyagotchiTheWorldReadIsCappedPerLocationAndKeepsItsFixtures(t *testing.T) {
	ctx := context.Background()
	// Kinds AND locations of this test's own. `location_key` is plain text by
	// design, so a test can have places nothing else writes to — which is what
	// makes the counts below exact: the suite shares one database and every hello
	// in it stands the real key and the real crate up in двор, so a cap asserted
	// against the yard would be sharing its allowance with another test's fixture.
	const (
		fixtureKind = "pettest_fixture"
		litterKind  = "pettest_litter"
		busy        = "pettest_place_busy"
		quiet       = "pettest_place_quiet"
		perLocation = 3
	)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx,
			`DELETE FROM game_vanyagotchi_world_objects WHERE kind IN ($1, $2)`, fixtureKind, litterKind); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	})

	// The fixture goes down FIRST, so it is the oldest row in its location and
	// newest-first ordering alone would evict it.
	if _, err := pool.Exec(ctx,
		`INSERT INTO game_vanyagotchi_world_objects (kind, location_key, x, y, singleton)
		 VALUES ($1, $2, 0.5, 0.5, true)`, fixtureKind, busy); err != nil {
		t.Fatalf("stand the fixture up: %v", err)
	}
	// Then a pile in the busy place, well past its allowance, and a couple in the
	// quiet one.
	for i := 0; i < perLocation+4; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO game_vanyagotchi_world_objects (kind, location_key, x, y, singleton)
			 VALUES ($1, $2, $3, 0.5, false)`,
			litterKind, busy, float64(i)/10); err != nil {
			t.Fatalf("litter the busy place: %v", err)
		}
	}
	const elsewhere = 2
	for i := 0; i < elsewhere; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO game_vanyagotchi_world_objects (kind, location_key, x, y, singleton)
			 VALUES ($1, $2, $3, 0.5, false)`,
			litterKind, quiet, float64(i)/10); err != nil {
			t.Fatalf("litter the quiet place: %v", err)
		}
	}

	live, err := gamevanyagotchi.NewPostgresRepository().LiveWorldObjects(ctx, pool, perLocation)
	if err != nil {
		t.Fatalf("LiveWorldObjects: %v", err)
	}
	perPlace := map[string]int{}
	fixtureSurvived := false
	for _, o := range live {
		perPlace[o.LocationKey]++
		if o.Kind == fixtureKind {
			fixtureSurvived = true
		}
	}
	if perPlace[busy] != perLocation {
		t.Errorf("the busy place came back with %d rows against a cap of %d; the cap is what bounds the frame, and it has to hold however enthusiastically one location behaves",
			perPlace[busy], perLocation)
	}
	if perPlace[quiet] != elsewhere {
		t.Errorf("the quiet place came back with %d of its %d rows; a busy location must not be able to spend a neighbour's allowance",
			perPlace[quiet], elsewhere)
	}
	if !fixtureSurvived {
		t.Error("the singleton was evicted by a pile of newer rows; it is the oldest row in its location by construction, so ordering by age alone loses the beer crate and leaves a yard nobody can drink in")
	}
	// And every row comes back knowing where it is, which is what the store block
	// and the search gate both compare against.
	for _, o := range live {
		if o.LocationKey == "" {
			t.Errorf("world object %s came back with no location; the store would be drawn in every place at once and a key in лес would be claimable from двор", o.ID)
		}
	}
}

// TestVanyagotchiTheActiveSingletonIsWorldWide is the "one key, not one per
// location" ruling read straight off the index it rests on.
//
// The partial unique index is `(kind) WHERE singleton AND exhausted_at IS NULL`
// — on the kind ALONE — so a key hidden in кусты means there is no key to be
// stood up in двор, and a hello arriving in an empty-looking yard must not try.
// Asking per location would have been a question the index does not answer: four
// places would each have reported nothing, each would have inserted, and three of
// the four would have been refused in silence.
func TestVanyagotchiTheActiveSingletonIsWorldWide(t *testing.T) {
	ctx := context.Background()
	kind := petObjectKind(t, gamevanyagotchi.KindKey)
	if !kind.Singleton {
		t.Fatalf("the catalogue no longer marks %q a singleton; there is no world-wide invariant left to check", kind.Key)
	}
	petClearTheYardOf(t, kind.Key)
	t.Cleanup(func() { petClearTheYardOf(t, kind.Key) })

	repo := gamevanyagotchi.NewPostgresRepository()
	if _, running, err := repo.ActiveSingleton(ctx, pool, kind.Key); err != nil {
		t.Fatalf("ActiveSingleton on an empty world: %v", err)
	} else if running {
		t.Fatal("a hunt is reported as running in a world that has just been emptied of keys")
	}

	// Hidden somewhere that is emphatically not the yard.
	spot := gamevanyagotchi.Hotspot{}
	kusty, ok := gamevanyagotchi.LocationByKey(gamevanyagotchi.LocationKusty)
	if !ok || len(kusty.Hotspots) == 0 {
		t.Fatalf("the catalogue has no hiding places in %q", gamevanyagotchi.LocationKusty)
	}
	spot = kusty.Hotspots[0]
	if err := repo.InsertWorldObject(ctx, pool, kind.Key, kusty.Key, spot.At, "", kind.Singleton, nil, nil); err != nil {
		t.Fatalf("hide the key in %q: %v", kusty.Key, err)
	}

	id, running, err := repo.ActiveSingleton(ctx, pool, kind.Key)
	if err != nil {
		t.Fatalf("ActiveSingleton: %v", err)
	}
	if !running {
		t.Fatalf("no hunt is reported as running while a key is hidden in %q; every hello would try to stand a second one up and be refused by the index in silence", kusty.Key)
	}
	if got := petLocationOf(t, id); got != kusty.Key {
		t.Errorf("the running hunt's key is in %q; want the %q it was hidden in", got, kusty.Key)
	}

	// And the index agrees: a second one, anywhere at all, is refused.
	yard, ok := gamevanyagotchi.LocationByKey(gamevanyagotchi.LocationYard)
	if !ok || len(yard.Hotspots) == 0 {
		t.Fatalf("the catalogue has no hiding places in %q", gamevanyagotchi.LocationYard)
	}
	if err := repo.InsertWorldObject(ctx, pool, kind.Key, yard.Key, yard.Hotspots[0].At, "", kind.Singleton, nil, nil); err != nil {
		t.Fatalf("the second insert errored rather than being swallowed: %v; it is ON CONFLICT DO NOTHING and must be a silent no-op", err)
	}
	var active int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM game_vanyagotchi_world_objects
		  WHERE kind = $1 AND exhausted_at IS NULL AND deleted_at IS NULL`, kind.Key).Scan(&active); err != nil {
		t.Fatalf("count the active keys: %v", err)
	}
	if active != 1 {
		t.Errorf("%d keys are active in the world at once; the index is on the kind alone, so five locations must still mean one hunt", active)
	}
}

// TestVanyagotchiAPetsLocationIsWrittenWithoutTouchingItsPosition.
//
// The two have deliberately different write cadences and this is where that
// could go wrong quietly. A position is written on DEPARTURE, once per session;
// a location is written EAGERLY, the moment somebody goes somewhere, because
// coming back to the place you left is the whole of what it is for. If
// SetLocation also cleared x/y, a Ваня would arrive back after a restart at the
// origin — the corner of the plane — rather than where he was standing, and
// nothing in the frame would say why.
func TestVanyagotchiAPetsLocationIsWrittenWithoutTouchingItsPosition(t *testing.T) {
	ctx := context.Background()
	app, _ := petApp(t)
	cli := loginAs(t, app.URL, "7307", "user")
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	account := accountIDByUID(t, "7307")

	repo := gamevanyagotchi.NewPostgresRepository()
	where := gamevanyagotchi.Point{X: 0.31, Y: 0.79}
	seen := time.Now().UTC().Truncate(time.Millisecond)
	if err := repo.SavePosition(ctx, pool, account, where, seen); err != nil {
		t.Fatalf("SavePosition: %v", err)
	}
	if err := repo.SetLocation(ctx, pool, account, gamevanyagotchi.LocationZabroshka); err != nil {
		t.Fatalf("SetLocation: %v", err)
	}

	pet, ok, err := repo.FindPet(ctx, pool, account)
	if err != nil || !ok {
		t.Fatalf("FindPet: ok=%v err=%v", ok, err)
	}
	if pet.LocationKey != gamevanyagotchi.LocationZabroshka {
		t.Errorf("his row says he is in %q; want %q", pet.LocationKey, gamevanyagotchi.LocationZabroshka)
	}
	at, known := pet.Standing()
	if !known {
		t.Fatal("his position was cleared by writing his location; he would come back to the corner of the plane after a restart, and nothing would say why")
	}
	petAtPoint(t, "a pet who moved location after standing somewhere", at.X, at.Y, where)

	// And it moves again, idempotently, without a second row appearing anywhere.
	if err := repo.SetLocation(ctx, pool, account, gamevanyagotchi.LocationYard); err != nil {
		t.Fatalf("SetLocation back: %v", err)
	}
	pet, ok, err = repo.FindPet(ctx, pool, account)
	if err != nil || !ok {
		t.Fatalf("FindPet after moving back: ok=%v err=%v", ok, err)
	}
	if pet.LocationKey != gamevanyagotchi.LocationYard {
		t.Errorf("his row says he is in %q after going back to the yard; want %q", pet.LocationKey, gamevanyagotchi.LocationYard)
	}
	var pets int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM game_vanyagotchi_pets WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		account).Scan(&pets); err != nil {
		t.Fatalf("count his pets: %v", err)
	}
	if pets != 1 {
		t.Errorf("he has %d living pets after moving twice; want 1 — moving is an UPDATE and not an insert", pets)
	}
}
