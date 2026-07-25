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

// petBuildApp builds the app with «Ванягоччи» wired to the real pool.
//
// Its own builder rather than the package's shared one because this file is
// only interested in the durable half of the game: the transport is nil, no hub
// runs, and nothing here publishes a roster. The pet path never touches the
// transport, which is precisely the separation the service's own doc comment
// claims — so passing nil is a small assertion of that in itself.
func petBuildApp(vkBaseURL string) http.Handler {
	cfg := config.Config{
		Env: "dev",
		VK:  config.VK{AppID: "app-1", ServiceToken: "svc", RedirectURI: vkRedirect, BaseURL: vkBaseURL},
	}
	h := httpapi.NewServer(httpapi.Deps{
		Config:          cfg,
		Pool:            pool,
		WebFS:           fstest.MapFS{"index.html": {Data: []byte("<html>psycho</html>")}},
		VK:              vk.New(vkBaseURL, "app-1", "svc", vkRedirect),
		Accounts:        newAccountService(),
		Sessions:        session.NewManager(pool, key(3), time.Hour, false),
		GameVanyagotchi: gamevanyagotchi.NewService(nil, httpapi.DefaultRoom, pool, gamevanyagotchi.NewPostgresRepository()),
		Settings:        settings.NewService(pool),
	}).Handler()
	return observability.WrapHandler(h, "http.server")
}

// petApp starts a server for one test and returns it with its base URL.
func petApp(t *testing.T) *httptest.Server {
	t.Helper()
	vkSrv := fakeVKDynamic()
	t.Cleanup(vkSrv.Close)
	app := httptest.NewServer(petBuildApp(vkSrv.URL))
	t.Cleanup(app.Close)
	return app
}

// petActionURL builds the path for one catalogue verb, from the verb's own key.
//
// The client resolves actions against the served config rather than hardcoding
// them, and these tests do the same — so renaming a verb is one edit in the
// catalogue and nothing here has to be found and changed.
func petActionURL(base, action string) string {
	return base + "/api/game-vanyagotchi/actions/" + action
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
	app := petApp(t)
	anon := &http.Client{}

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/game-vanyagotchi/config"},
		{http.MethodGet, "/api/game-vanyagotchi/state"},
		{http.MethodPost, "/api/game-vanyagotchi/actions/" + gamevanyagotchi.ActionDrink},
		{http.MethodPost, "/api/game-vanyagotchi/actions/" + gamevanyagotchi.ActionRelieve},
	} {
		s, body := doJSON(t, anon, tc.method, app.URL+tc.path, nil)
		if s != http.StatusUnauthorized || body["error"] != "unauthorized" {
			t.Fatalf("%s %s: status=%d body=%v; want 401 unauthorized", tc.method, tc.path, s, body)
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
	app := petApp(t)
	cli := loginAs(t, app.URL, "7210", "user")
	setRoleStatus(t, accountIDByUID(t, "7210"), "user", "pending")

	for _, path := range []string{"/api/game-vanyagotchi/config", "/api/game-vanyagotchi/state"} {
		s, body := doJSON(t, cli, http.MethodGet, app.URL+path, nil)
		if s != http.StatusForbidden || body["error"] != "not_approved" {
			t.Fatalf("GET %s: status=%d body=%v; want 403 not_approved", path, s, body)
		}
	}
	for _, action := range []string{gamevanyagotchi.ActionDrink, gamevanyagotchi.ActionRelieve} {
		s, body := doJSON(t, cli, http.MethodPost, petActionURL(app.URL, action), nil)
		if s != http.StatusForbidden || body["error"] != "not_approved" {
			t.Fatalf("POST %s: status=%d body=%v; want 403 not_approved", action, s, body)
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
	app := petApp(t)
	cli := loginAs(t, app.URL, "7201", "user")

	s, cfg := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/config", nil)
	if s != http.StatusOK {
		t.Fatalf("config: status=%d body=%v", s, cfg)
	}
	if cfg["game_key"] != gamevanyagotchi.GameKey {
		t.Fatalf("game_key = %v, want %q", cfg["game_key"], gamevanyagotchi.GameKey)
	}

	stats := petKeys(t, cfg, "stats")
	for _, want := range []string{gamevanyagotchi.StatHP, gamevanyagotchi.StatBeer, gamevanyagotchi.StatBladder} {
		if !petHas(stats, want) {
			t.Fatalf("stats = %v, want it to include %q", stats, want)
		}
	}
	actions := petKeys(t, cfg, "actions")
	for _, want := range []string{gamevanyagotchi.ActionDrink, gamevanyagotchi.ActionRelieve} {
		if !petHas(actions, want) {
			t.Fatalf("actions = %v, want it to include %q", actions, want)
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
	app := petApp(t)
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
	app := petApp(t)
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
	app := petApp(t)
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
	app := petApp(t)
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
	app := petApp(t)
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
	app := petApp(t)
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
	app := petApp(t)
	cli := loginAs(t, app.URL, "7230", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7230"))

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

	s, state := doJSON(t, cli, http.MethodPost, petActionURL(app.URL, drink.Key), nil)
	if s != http.StatusOK {
		t.Fatalf("%s: status=%d body=%v", drink.Key, s, state)
	}
	if state["alive"] != true {
		t.Fatalf("alive = %v after a drink, want true", state["alive"])
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
		petNear(t, "responded "+e.StatKey+" after a drink", petStateValue(t, state, e.StatKey), want)
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
	app := petApp(t)
	cli := loginAs(t, app.URL, "7231", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7231"))

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

	s, state := doJSON(t, cli, http.MethodPost, petActionURL(app.URL, relieve.Key), nil)
	if s != http.StatusOK {
		t.Fatalf("%s: status=%d body=%v", relieve.Key, s, state)
	}

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

// TestVanyagotchiPetRelieveIsRefusedOnADeadPetAndDrinkIsNot pins the refusal
// that makes death mean anything at all.
//
// A dead Ваня does not go to the toilet. Only a verb the catalogue marks as
// reviving is allowed through, which is what turns "he is dead" from a label
// into a state the player has to act their way out of — and it has to be a
// refusal rather than a no-op, because a button that appears to work and changes
// nothing is how a player concludes the game is broken. 409 rather than 422: the
// request is perfectly well formed and would succeed against a living pet; what
// is wrong is the state of the world, and the remedy is a different action. The
// refusal must also be total — no stat may be re-stamped on the way out, since a
// rejected action that silently reset the clock would hand out free hours. Both
// halves of the guard are exercised in one test on purpose: an implementation
// that refused everything would pass the first half alone and leave the game
// unwinnable.
func TestVanyagotchiPetRelieveIsRefusedOnADeadPetAndDrinkIsNot(t *testing.T) {
	app := petApp(t)
	cli := loginAs(t, app.URL, "7232", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7232"))

	hpDef := petStat(t, gamevanyagotchi.StatHP)
	relieve := petAction(t, gamevanyagotchi.ActionRelieve)
	drink := petAction(t, gamevanyagotchi.ActionDrink)
	if relieve.RevivesFatal || !drink.RevivesFatal {
		t.Fatalf("the catalogue says %s revives=%v and %s revives=%v; this test needs one of each",
			relieve.Key, relieve.RevivesFatal, drink.Key, drink.RevivesFatal)
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

	s, body := doJSON(t, cli, http.MethodPost, petActionURL(app.URL, relieve.Key), nil)
	if s != http.StatusConflict || body["error"] != "pet_dead" {
		t.Fatalf("%s on a dead pet: status=%d body=%v; want 409 pet_dead", relieve.Key, s, body)
	}
	if at := petDiedAt(t, id); at == nil || !at.Equal(*died) {
		t.Fatalf("died_at = %v after a refused %s, want the recorded %v — a refusal must not touch the pet", at, relieve.Key, died)
	}
	if at := petSharedAsOf(t, id); !at.Equal(asOfBefore) {
		t.Fatalf("the stats were re-stamped from %s to %s by a refused %s — a rejected action must write nothing at all",
			asOfBefore.UTC(), at.UTC(), relieve.Key)
	}

	s, state := doJSON(t, cli, http.MethodPost, petActionURL(app.URL, drink.Key), nil)
	if s != http.StatusOK {
		t.Fatalf("%s on the same dead pet: status=%d body=%v; want 200 — this is the way back", drink.Key, s, state)
	}
	if state["alive"] != true {
		t.Fatalf("alive = %v after a %s, want true", state["alive"], drink.Key)
	}
	if hp := petStateValue(t, state, hpDef.Key); hp <= hpDef.Min {
		t.Fatalf("hp = %.4f after a %s, want above the fatal floor %.1f", hp, drink.Key, hpDef.Min)
	}
	if at := petDiedAt(t, id); at != nil {
		t.Fatalf("stored died_at = %v after a revive, want NULL — a stale record would re-kill him on the next read", at)
	}
}

// TestVanyagotchiPetDrinkRevivesADeadPetAndClampsAtTheMaximum confirms death is
// a fright rather than an ending, and that the cure cannot overshoot.
//
// Both halves matter. Reviving is a deliberate design decision — an irreversible
// loss in a friend group is how a player leaves for good — so the verb that
// tops him up is also the way back, and the death record has to be cleared
// rather than merely ignored, otherwise every later read would rediscover a
// death that is over. Clamping matters because an action is the only thing that
// ever raises a stat: an unclamped one would park health above its ceiling and
// buy hours of invulnerability the catalogue never granted. The same loop proves
// the clamp in both directions of usefulness, because the drink that fills him
// up also fills his bladder — a stat whose ceiling is the bad end of its scale.
func TestVanyagotchiPetDrinkRevivesADeadPetAndClampsAtTheMaximum(t *testing.T) {
	app := petApp(t)
	cli := loginAs(t, app.URL, "7207", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7207"))
	hpDef := petStat(t, gamevanyagotchi.StatHP)
	bladderDef := petStat(t, gamevanyagotchi.StatBladder)
	drink := petAction(t, gamevanyagotchi.ActionDrink)

	petBackdateAll(t, id, petHours(2*petFatalHours(t, hpDef)))
	if s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK || state["alive"] != false {
		t.Fatalf("state before drinking: status=%d alive=%v; want 200 and dead", s, state["alive"])
	}

	s, state := doJSON(t, cli, http.MethodPost, petActionURL(app.URL, drink.Key), nil)
	if s != http.StatusOK {
		t.Fatalf("%s on a dead pet: status=%d body=%v; want 200 — this is the way back", drink.Key, s, state)
	}
	if state["alive"] != true {
		t.Fatalf("alive = %v after a %s, want true", state["alive"], drink.Key)
	}
	if hp := petStateValue(t, state, hpDef.Key); hp <= hpDef.Min {
		t.Fatalf("hp = %.4f after a %s, want above the fatal floor %.1f", hp, drink.Key, hpDef.Min)
	}
	if at := petDiedAt(t, id); at != nil {
		t.Fatalf("stored died_at = %v after a revive, want NULL — a stale record would re-kill him on the next read", at)
	}

	// Drink until the ceiling, then once more. Bounded rather than counted, so a
	// retuned delta in the catalogue does not turn this into a false failure.
	hp := petStateValue(t, state, hpDef.Key)
	for i := 0; hp < hpDef.Max && i < 20; i++ {
		var body map[string]any
		if s, body = doJSON(t, cli, http.MethodPost, petActionURL(app.URL, drink.Key), nil); s != http.StatusOK {
			t.Fatalf("%s %d: status=%d body=%v", drink.Key, i, s, body)
		}
		hp = petStateValue(t, body, hpDef.Key)
		if hp > hpDef.Max {
			t.Fatalf("hp = %.4f after %s %d, want no more than the catalogue max %.1f", hp, drink.Key, i, hpDef.Max)
		}
		if bladder := petStateValue(t, body, bladderDef.Key); bladder > bladderDef.Max {
			t.Fatalf("bladder = %.4f after %s %d, want no more than the catalogue max %.1f", bladder, drink.Key, i, bladderDef.Max)
		}
	}
	petNear(t, "hp at the ceiling", hp, hpDef.Max)

	// One more at the ceiling: it must land, and it must change nothing.
	s, state = doJSON(t, cli, http.MethodPost, petActionURL(app.URL, drink.Key), nil)
	if s != http.StatusOK {
		t.Fatalf("%s at the ceiling: status=%d body=%v", drink.Key, s, state)
	}
	if hp := petStateValue(t, state, hpDef.Key); hp > hpDef.Max {
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

// TestVanyagotchiPetRejectsAnActionOutsideTheCatalogue confirms the allowlist is
// the catalogue itself.
//
// The verb is a path segment, so this endpoint is reachable with any string at
// all; what makes that safe is that an unknown one is refused rather than
// ignored. 404 with a stable code, because a client asking for it is either
// stale or probing and both deserve the same answer.
func TestVanyagotchiPetRejectsAnActionOutsideTheCatalogue(t *testing.T) {
	app := petApp(t)
	cli := loginAs(t, app.URL, "7208", "user")

	s, body := doJSON(t, cli, http.MethodPost, petActionURL(app.URL, "подкормить"), nil)
	if s != http.StatusNotFound || body["error"] != "unknown_action" {
		t.Fatalf("unknown action: status=%d body=%v; want 404 unknown_action", s, body)
	}
	if body["trace_id"] == "" {
		t.Fatal("error envelope carries no trace_id")
	}
}

// TestVanyagotchiPetsAreIsolatedPerAccount confirms one player's pet is one
// player's.
//
// Everything in the durable half is keyed on the caller's account id taken from
// the session, never from the request, so this is really a check that no query
// lost its account predicate — the failure mode being one shared Ваня that every
// player feeds. It matters more now than it did: an action re-stamps every stat
// row it can reach, so a missing predicate would not merely read somebody else's
// pet, it would rewrite it.
func TestVanyagotchiPetsAreIsolatedPerAccount(t *testing.T) {
	app := petApp(t)
	alice := loginAs(t, app.URL, "7220", "user")
	bob := loginAs(t, app.URL, "7221", "user")

	for name, cli := range map[string]*http.Client{"alice": alice, "bob": bob} {
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("%s create: status=%d body=%v", name, s, body)
		}
	}
	aliceID := petID(t, accountIDByUID(t, "7220"))
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

	s, state := doJSON(t, alice, http.MethodPost, petActionURL(app.URL, drink.Key), nil)
	if s != http.StatusOK {
		t.Fatalf("alice's %s: status=%d body=%v", drink.Key, s, state)
	}
	if id, _ := state["pet"].(map[string]any)["id"].(string); id != aliceID {
		t.Fatalf("alice's %s answered with pet %s, want %s", drink.Key, id, aliceID)
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
