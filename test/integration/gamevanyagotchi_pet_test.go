//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
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
// anything that ticks, the moment of death is the derived instant rather than
// the moment somebody looked, and the world-object table's singleton invariant
// is enforced by an index rather than by a rule written in Go. None of that can
// be proved with a fake repository, which is why these live here and not in the
// package's unit tests.
//
// UID namespace: 72xx. 71xx belongs to the realtime tests in
// gamevanyagotchi_test.go, and the database is shared across the whole package.

// petStatEpsilon is the tolerance for a decayed value in these tests.
//
// The decay is exact — it is one subtraction over a stored timestamp — so the
// only slack needed is the wall-clock gap between the SQL now() that backdates a
// row and the server's own now() a few milliseconds later. At the hp rate of 3
// per hour, 0.5 is ten minutes of clock skew: far more than any test run can
// accumulate, and far tighter than any real bug would hide inside.
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

// petBackdate pushes a stat's as_of into the past, in the database's own clock.
//
// This is how time is travelled in these tests: there is nothing to advance,
// because nothing ticks — the whole decay engine is (value, as_of) read against
// now(), so moving as_of backwards is indistinguishable from having been away.
// The interval is built in SQL rather than in Go so the test never has to assume
// the container's clock agrees with the test process's.
func petBackdate(t *testing.T, petIDs, statKey string, d time.Duration) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE game_vanyagotchi_pet_stats
		    SET as_of = now() - make_interval(secs => $3)
		  WHERE pet_id = $1::uuid AND stat_key = $2`,
		petIDs, statKey, d.Seconds()); err != nil {
		t.Fatalf("backdate %s by %s: %v", statKey, d, err)
	}
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

// petStateValue plucks one decayed stat out of a /state response.
func petStateValue(t *testing.T, body map[string]any, statKey string) float64 {
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
		if row["key"] == statKey {
			v, ok := row["value"].(float64)
			if !ok {
				t.Fatalf("stat %s has no numeric value: %v", statKey, row)
			}
			return v
		}
	}
	t.Fatalf("stat %s missing from state: %v", statKey, body)
	return 0
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
		{http.MethodPost, "/api/game-vanyagotchi/actions/heal"},
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
	s, body := doJSON(t, cli, http.MethodPost, app.URL+"/api/game-vanyagotchi/actions/heal", nil)
	if s != http.StatusForbidden || body["error"] != "not_approved" {
		t.Fatalf("POST heal: status=%d body=%v; want 403 not_approved", s, body)
	}
}

// TestVanyagotchiPetConfigServesTheWholeCatalogue confirms the client can render
// the game knowing nothing but this response.
//
// That is the property the catalogue exists for: every key, label, rate and
// bound is content served from the backend, so adding a stat or an action is a
// backend-only change with no migration and no client deploy. A default that is
// not present in its own list would break a fresh pet's first render, which is
// why the two defaults are checked against the lists rather than merely for
// being non-empty.
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
	if !petHas(stats, gamevanyagotchi.StatHP) || !petHas(stats, gamevanyagotchi.StatBladder) {
		t.Fatalf("stats = %v, want both %q and %q", stats, gamevanyagotchi.StatHP, gamevanyagotchi.StatBladder)
	}
	if actions := petKeys(t, cfg, "actions"); !petHas(actions, gamevanyagotchi.ActionHeal) {
		t.Fatalf("actions = %v, want %q", actions, gamevanyagotchi.ActionHeal)
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
// testable at all. Both directions are checked in one test on purpose: hp drains
// and bladder FILLS, from one signed rate and one expression, and a sign error
// that made the bladder empty itself overnight would otherwise be invisible.
func TestVanyagotchiPetStatsDecayFromTheStoredInstant(t *testing.T) {
	app := petApp(t)
	cli := loginAs(t, app.URL, "7205", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7205"))

	const away = 10 * time.Hour
	petBackdate(t, id, gamevanyagotchi.StatHP, away)
	petBackdate(t, id, gamevanyagotchi.StatBladder, away)

	s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
	if s != http.StatusOK {
		t.Fatalf("state after backdating: status=%d body=%v", s, state)
	}

	hpDef, ok := gamevanyagotchi.StatByKey(gamevanyagotchi.StatHP)
	if !ok {
		t.Fatal("hp has left the catalogue")
	}
	bladderDef, ok := gamevanyagotchi.StatByKey(gamevanyagotchi.StatBladder)
	if !ok {
		t.Fatal("bladder has left the catalogue")
	}

	hp := petStateValue(t, state, gamevanyagotchi.StatHP)
	wantHP := hpDef.Start - hpDef.DecayPerHour*away.Hours()
	if hp >= hpDef.Start {
		t.Fatalf("hp = %.4f after %s away, want materially below the start %.1f", hp, away, hpDef.Start)
	}
	petNear(t, fmt.Sprintf("hp after %s away", away), hp, wantHP)

	bladder := petStateValue(t, state, gamevanyagotchi.StatBladder)
	wantBladder := bladderDef.Start - bladderDef.DecayPerHour*away.Hours()
	if bladder <= bladderDef.Start {
		t.Fatalf("bladder = %.4f after %s away, want materially above the start %.1f — it fills, it does not drain",
			bladder, away, bladderDef.Start)
	}
	petNear(t, fmt.Sprintf("bladder after %s away", away), bladder, wantBladder)

	// The stored pair is untouched by a read: the decay is computed, never
	// accumulated. If a read ever started writing the decayed value back, an
	// absence would compound instead of being linear.
	value, _ := petStoredStat(t, id, gamevanyagotchi.StatHP)
	if value != hpDef.Start {
		t.Fatalf("stored hp = %v after a read, want the untouched start %v — reads must not write the decay back",
			value, hpDef.Start)
	}
	if state["alive"] != true {
		t.Fatalf("alive = %v after %s away, want true — hp is still above zero", state["alive"], away)
	}
}

// TestVanyagotchiPetDeathIsRecordedAtTheDerivedInstant is the property the whole
// lazy design turns on.
//
// He did not die when somebody looked; he died when hp reached zero, and because
// the decay is linear that instant is derivable exactly from (value, as_of).
// Recording "now" instead would be wrong by however long nobody opened the game
// — here, sixteen hours — and would make the recorded moment depend on who
// polled rather than on what happened. The second read then has to leave the
// record alone, which is what "materialised exactly once" means: the first
// observer writes the fact, everyone after that reports it.
func TestVanyagotchiPetDeathIsRecordedAtTheDerivedInstant(t *testing.T) {
	app := petApp(t)
	cli := loginAs(t, app.URL, "7206", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7206"))

	hpDef, ok := gamevanyagotchi.StatByKey(gamevanyagotchi.StatHP)
	if !ok {
		t.Fatal("hp has left the catalogue")
	}
	// Long enough that hp hit zero well before now: 50 hours at 3/hour kills a
	// full Ваня after 33h20m, so the death is ~16h40m in the past.
	const away = 50 * time.Hour
	petBackdate(t, id, gamevanyagotchi.StatHP, away)

	s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil)
	if s != http.StatusOK {
		t.Fatalf("state: status=%d body=%v", s, state)
	}
	if state["alive"] != false {
		t.Fatalf("alive = %v after %s away, want false", state["alive"], away)
	}
	petNear(t, "hp at death", petStateValue(t, state, gamevanyagotchi.StatHP), hpDef.Min)

	value, asOf := petStoredStat(t, id, gamevanyagotchi.StatHP)
	// as_of + (value − Min) / rate: the instant the stored pair reaches the
	// fatal floor, computed from the row rather than from any clock.
	wantDeath := asOf.Add(time.Duration((value - hpDef.Min) / hpDef.DecayPerHour * float64(time.Hour)))

	first := petDiedAt(t, id)
	if first == nil {
		t.Fatal("died_at is NULL after the read that observed the death")
	}
	if d := first.Sub(wantDeath); d > time.Second || d < -time.Second {
		t.Fatalf("died_at = %s, want the derived instant %s (off by %s)", first.UTC(), wantDeath.UTC(), d)
	}
	// Stated separately from the tolerance above so the failure reads plainly if
	// somebody ever records the moment of the read: that is the whole bug this
	// test exists for, and it would land the death ~16h40m too late.
	if late := time.Since(*first); late < 10*time.Hour {
		t.Fatalf("died_at is only %s ago — it looks like the moment of the read, not the moment hp reached zero", late)
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

// TestVanyagotchiPetHealRevivesADeadPetAndClampsAtTheMaximum confirms death is a
// fright rather than an ending, and that the cure cannot overshoot.
//
// Both halves matter. Reviving is a deliberate design decision — an irreversible
// loss in a friend group is how a player leaves for good — so the action that
// restores hp is also the way back, and the death record has to be cleared
// rather than merely ignored, otherwise every later read would rediscover a
// death that is over. Clamping matters because an action is the only thing that
// ever raises a stat: an unclamped one would park hp above its ceiling and buy
// hours of invulnerability the catalogue never granted.
func TestVanyagotchiPetHealRevivesADeadPetAndClampsAtTheMaximum(t *testing.T) {
	app := petApp(t)
	cli := loginAs(t, app.URL, "7207", "user")

	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("create: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7207"))
	hpDef, ok := gamevanyagotchi.StatByKey(gamevanyagotchi.StatHP)
	if !ok {
		t.Fatal("hp has left the catalogue")
	}

	petBackdate(t, id, gamevanyagotchi.StatHP, 50*time.Hour)
	if s, state := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK || state["alive"] != false {
		t.Fatalf("state before healing: status=%d alive=%v; want 200 and dead", s, state["alive"])
	}

	s, state := doJSON(t, cli, http.MethodPost, app.URL+"/api/game-vanyagotchi/actions/heal", nil)
	if s != http.StatusOK {
		t.Fatalf("heal a dead pet: status=%d body=%v; want 200 — heal is the way back", s, state)
	}
	if state["alive"] != true {
		t.Fatalf("alive = %v after healing, want true", state["alive"])
	}
	if hp := petStateValue(t, state, gamevanyagotchi.StatHP); hp <= hpDef.Min {
		t.Fatalf("hp = %.4f after healing, want above the fatal floor %.1f", hp, hpDef.Min)
	}
	if at := petDiedAt(t, id); at != nil {
		t.Fatalf("stored died_at = %v after a revive, want NULL — a stale record would re-kill him on the next read", at)
	}

	// Heal until the ceiling, then once more. Bounded rather than counted, so a
	// retuned delta in the catalogue does not turn this into a false failure.
	hp := petStateValue(t, state, gamevanyagotchi.StatHP)
	for i := 0; hp < hpDef.Max && i < 10; i++ {
		var body map[string]any
		if s, body = doJSON(t, cli, http.MethodPost, app.URL+"/api/game-vanyagotchi/actions/heal", nil); s != http.StatusOK {
			t.Fatalf("heal %d: status=%d body=%v", i, s, body)
		}
		hp = petStateValue(t, body, gamevanyagotchi.StatHP)
		if hp > hpDef.Max {
			t.Fatalf("hp = %.4f after heal %d, want no more than the catalogue max %.1f", hp, i, hpDef.Max)
		}
	}
	petNear(t, "hp at the ceiling", hp, hpDef.Max)

	// One more at the ceiling: it must land, and it must change nothing.
	s, state = doJSON(t, cli, http.MethodPost, app.URL+"/api/game-vanyagotchi/actions/heal", nil)
	if s != http.StatusOK {
		t.Fatalf("heal at the ceiling: status=%d body=%v", s, state)
	}
	if hp := petStateValue(t, state, gamevanyagotchi.StatHP); hp > hpDef.Max {
		t.Fatalf("hp = %.4f after healing at the ceiling, want exactly %.1f", hp, hpDef.Max)
	}
	if value, _ := petStoredStat(t, id, gamevanyagotchi.StatHP); value > hpDef.Max {
		t.Fatalf("stored hp = %v, want no more than %v — the clamp must reach the row, not just the response", value, hpDef.Max)
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

	s, body := doJSON(t, cli, http.MethodPost, app.URL+"/api/game-vanyagotchi/actions/подкормить", nil)
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
// player heals.
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

	// Give Alice's pet room to be healed, so her action is a visible change
	// rather than a clamp that would look identical to doing nothing.
	petBackdate(t, aliceID, gamevanyagotchi.StatHP, 20*time.Hour)
	beforeValue, beforeAsOf := petStoredStat(t, bobID, gamevanyagotchi.StatHP)

	s, state := doJSON(t, alice, http.MethodPost, app.URL+"/api/game-vanyagotchi/actions/heal", nil)
	if s != http.StatusOK {
		t.Fatalf("alice heal: status=%d body=%v", s, state)
	}
	if id, _ := state["pet"].(map[string]any)["id"].(string); id != aliceID {
		t.Fatalf("alice's heal answered with pet %s, want %s", id, aliceID)
	}

	afterValue, afterAsOf := petStoredStat(t, bobID, gamevanyagotchi.StatHP)
	if afterValue != beforeValue || !afterAsOf.Equal(beforeAsOf) {
		t.Fatalf("bob's hp row moved from (%v, %s) to (%v, %s) because alice healed",
			beforeValue, beforeAsOf, afterValue, afterAsOf)
	}
	if _, aliceAsOf := petStoredStat(t, aliceID, gamevanyagotchi.StatHP); !aliceAsOf.After(beforeAsOf.Add(-time.Hour)) {
		t.Fatalf("alice's hp row was not rewritten by her own heal (as_of %s)", aliceAsOf)
	}
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
