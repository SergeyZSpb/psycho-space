package gamevanyagotchi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
)

// The world half: the durable things standing in the yard.
//
// Two properties are worth more than everything else in this file, and neither
// of them is visible in a frame. A deposit is written WHERE THE SERVER BELIEVES
// HE IS STANDING — the verb frame carries no coordinate, so there is nothing to
// forge — and it is written in the SAME TRANSACTION as the stats and the events
// that explain it, so a batch that fails leaves nothing behind. Both are
// asserted against what the fake repository was actually handed, because a
// response says nothing about either.
//
// The third is the constraint the whole design is shaped by: THE TICK READS
// NOTHING. The plane renders world objects from a cache filled at human-paced
// moments, so expiry has to be arithmetic over what is already held rather than
// a query — and a broadcast that quietly re-read would look identical in every
// frame it produced. It is asserted by counting the reads, which is the only
// place that rule can be broken visibly.

// mustObjectKind is the catalogue entry a test is reasoning about, fetched
// rather than written down for the same reason mustStat and mustAction are: the
// art key, the label and the lifetime are content, and a test that pinned them
// would report every retune as a regression.
func mustObjectKind(t *testing.T, key string) ObjectKind {
	t.Helper()
	k, ok := ObjectKindByKey(key)
	if !ok {
		t.Fatalf("the catalogue has no object kind %q", key)
	}
	return k
}

// leavingKind is the kind a verb puts on the ground, failing if the catalogue
// says that verb leaves nothing at all — which would make the test below a test
// of nothing rather than a failing one.
func leavingKind(t *testing.T, verb string) ObjectKind {
	t.Helper()
	action := mustAction(t, verb)
	if action.Leaves == "" {
		t.Fatalf("the catalogue says %q leaves nothing behind; this test is reasoning about a rule the game no longer has", verb)
	}
	return mustObjectKind(t, action.Leaves)
}

// propsOf is every entity in a frame that is a thing rather than somebody.
//
// Found by the published id, which is the only thing that distinguishes them on
// the wire — deliberately, because the client resolves them exactly as it does a
// pet and holds no notion that world objects exist.
func propsOf(r Roster) []Peer {
	var out []Peer
	for _, p := range r.Peers {
		if strings.HasPrefix(p.ID, "obj-") {
			out = append(out, p)
		}
	}
	return out
}

// anObject is one row as the database would hand it back: a uuid-shaped id whose
// first twelve characters are unique to it, a kind, a place and an expiry.
func anObject(n int, kind string, at Point, expires time.Time) WorldObject {
	return anObjectIn(n, kind, LocationYard, at, expires)
}

// anObjectIn is the same row standing somewhere other than the yard.
//
// The location is a field on the row rather than a parameter of the read now
// that the cache holds all five places at once — so a fixture has to say which
// one it means, and «the yard» is only the common answer rather than the only
// one.
func anObjectIn(n int, kind, locationKey string, at Point, expires time.Time) WorldObject {
	when := expires
	return WorldObject{
		ID:          fmt.Sprintf("%012d-4a1e-b0c2-%012d", n, n),
		Kind:        kind,
		LocationKey: locationKey,
		At:          at,
		ExpiresAt:   &when,
	}
}

// atTheStore stands the beer crate in the yard and puts an account beside it.
//
// TWO FACTS RATHER THAN ONE, because the arrival gate reads both: where the
// crate is, out of the world cache the frame is built from, and where he is, out
// of the placement map. It is the fixture every test of drinking now needs, and
// that is the rule change rather than a testing inconvenience — beer comes out of
// a crate, so a Ваня who is not at the crate cannot have any.
//
// Written straight into the service because the ordinary route to either half —
// a hello that reads the database, then a tap and two ticks to walk him over —
// would be a paragraph of setup in tests whose subject starts once he has
// arrived. Call it AFTER any svc.load, which replaces the world cache wholesale.
//
// `left` is what the CACHE says is in the crate; what a draw actually gets is the
// fake repository's own count. The two are deliberately independent, so a test
// can arrange the case that matters — a frame that says there is beer, and a
// crate somebody else has just emptied.
func atTheStore(t *testing.T, svc *Service, repo *fakeRepo, accountID string, left int) {
	t.Helper()
	standAt(svc, accountID, *crateInTheYard(t, svc, repo, left).At)
}

// crateInTheYard puts the beer crate into the world — into the cache the frame
// and the gate read, AND into what the repository hands back — and returns its
// catalogue entry, without placing anybody.
//
// Both halves, because a verb that changes the world refreshes the cache from
// the repository afterwards. A fixture that wrote only the cache would give the
// first drink a crate and the second one an empty yard, which is a test failing
// for a reason that has nothing to do with its subject.
//
// Separate from atTheStore because half the tests of the gate are about somebody
// who is NOT at the crate, and "there is a crate" and "he is beside it" are the
// two independent facts the gate reads.
func crateInTheYard(t *testing.T, svc *Service, repo *fakeRepo, left int) ObjectKind {
	t.Helper()
	kind := mustObjectKind(t, KindCrate)
	if kind.At == nil {
		t.Fatalf("the catalogue no longer gives %q a pitch of its own; there is nothing for the arrival gate to measure against", kind.Key)
	}
	crate := aCrate(t, 42, left)
	repo.objects = append(repo.objects, crate)
	svc.mu.Lock()
	svc.world = append(svc.world, crate)
	svc.mu.Unlock()
	return kind
}

// aCrate is the beer store as the database hands it back: standing on the
// catalogue's own pitch, holding however much the test wants left in it, and
// never expiring — a crate ends when it is empty rather than when it times out.
func aCrate(t *testing.T, n int, left int) WorldObject {
	t.Helper()
	kind := mustObjectKind(t, KindCrate)
	if kind.At == nil {
		t.Fatalf("the catalogue no longer gives %q a pitch of its own", kind.Key)
	}
	o := anObject(n, kind.Key, *kind.At, time.Time{})
	o.ExpiresAt = nil
	o.Remaining = &left
	return o
}

// aLostKey is the singleton key as the database hands it back.
//
// No expiry, and that is the catalogue's rule rather than this helper's
// convenience: a hunt ends when somebody wins it, not when it times out, because
// timing one out would need the background timer this design does not have.
func aLostKey(n int, at Point) WorldObject {
	o := anObject(n, KindKey, at, time.Time{})
	o.ExpiresAt = nil
	return o
}

// aHotspot is one of a location's published places, by index.
//
// By index rather than by key, for the reason mustStat and mustObjectKind are
// looked up rather than written down: the five places in the yard are content
// somebody is meant to move by feel, and a test naming «куст» would break the
// day somebody renamed a bush. What the tests below need is "a place" and "a
// different place", which is what an index gives.
func aHotspot(t *testing.T, locationKey string, i int) Hotspot {
	t.Helper()
	loc, ok := LocationByKey(locationKey)
	if !ok {
		t.Fatalf("the catalogue has no location %q", locationKey)
	}
	if i >= len(loc.Hotspots) {
		t.Fatalf("%q publishes %d hotspots and this test wants a %s; a search needs somewhere to be wrong as well as somewhere to be right",
			locationKey, len(loc.Hotspots), []string{"first", "second", "third"}[min(i, 2)])
	}
	return loc.Hotspots[i]
}

// theKeyIsIn hides the lost key at a hotspot, without placing anybody.
//
// BOTH HALVES, exactly as crateInTheYard writes both: into the cache the gate
// reads, and into what the repository hands back — because a verb that changes
// the world reloads the cache from the repository afterwards, and a fixture that
// wrote only the cache would give the first search a key and the second one an
// empty yard.
//
// Separate from searchingIn because half the tests of the gate are about somebody
// standing in the WRONG place, and "the key is here" and "he is there" are the
// two independent facts a search is judged on.
func theKeyIsIn(t *testing.T, svc *Service, repo *fakeRepo, spot Hotspot) WorldObject {
	t.Helper()
	key := mustObjectKind(t, KindKey)
	if !key.Hidden {
		t.Fatalf("the catalogue no longer marks %q hidden; there would be nothing to search for", key.Key)
	}
	lost := aLostKey(7, spot.At)
	repo.objects = append(repo.objects, lost)
	svc.mu.Lock()
	svc.world = append(svc.world, lost)
	svc.mu.Unlock()
	return lost
}

// searchingIn is the fixture every claim now needs: a key hidden in a place, and
// somebody standing in that place. It returns the spot key his frame has to name.
//
// That it takes three lines rather than none IS the rule change: «искать ключи»
// used to be pressable from anywhere against a key drawn on the plane for
// everybody to see, so a test needed no position and no place at all.
func searchingIn(t *testing.T, svc *Service, repo *fakeRepo, accountID string, spot Hotspot) string {
	t.Helper()
	theKeyIsIn(t, svc, repo, spot)
	standAt(svc, accountID, spot.At)
	return spot.Key
}

// readyFor puts an account wherever a verb's own gates require him to be, and
// returns the spot its frame has to name.
//
// It exists for the two tests that press EVERY verb in the catalogue to assert
// something that is not about the gates — the shared re-stamp, and the contest
// routing. Both would otherwise have to carry a switch over verbs that somebody
// has to remember to extend, which is the failure where a new verb silently stops
// being covered. This reads the gates off the catalogue instead, so a third gated
// verb joins those loops with no edit here.
func readyFor(t *testing.T, svc *Service, repo *fakeRepo, accountID string, action Action) string {
	t.Helper()
	if action.NeedsNear != "" {
		// The one kind anything is gated on standing beside is the crate, and it
		// is the crate this stands him at. A second such kind would want this to
		// read the kind's own `At` — which is exactly what atTheStore already
		// does one level down.
		atTheStore(t, svc, repo, accountID, crateStock)
	}
	def, ok := ObjectKindByKey(action.Contests)
	if !ok || !def.Hidden {
		return ""
	}
	// A searching verb wants him somewhere else entirely — a hotspot is nowhere
	// near the beer store, by design — so this overrides any placement above. No
	// verb is gated on both today, and one that were would be a rule nobody could
	// satisfy rather than a fixture problem.
	return searchingIn(t, svc, repo, accountID, aHotspot(t, LocationYard, 0))
}

// TestADepositIsLeftWhereTheServerBelievesHeIsStanding is the whole of what a
// verb writes into the world, driven down the one path a client actually has.
//
// The frame below carries coordinates, and they are ignored — that is the point
// of driving this through the socket rather than through Do. The client sends a
// verb and never a place, so the deposit lands where the yard says he is
// standing, and a hostile client cannot decorate somebody else's corner of the
// plane from across it. Everything else asserted here comes off the catalogue:
// the kind, its lifetime, and the singleton flag the database's invariant is
// predicated on.
func TestADepositIsLeftWhereTheServerBelievesHeIsStanding(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	// With a bladder to empty, because the catalogue gates the verb on one and a
	// pet nobody has played with starts empty. Seeded rather than filled by
	// drinking first: what this test is about is where the deposit LANDS, and a
	// beer in front of it would be a second verb in the log to explain.
	repo := playedFor(enoughFor(t, ActionRelieve, epoch))
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	// A tick first, so the tap below is a journey measured against a clock the
	// test owns rather than a teleport, and so the verb has that same instant to
	// be stamped with.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	where := Point{X: 0.62, Y: 0.41}
	w := tap(t, svc, member("1"), where)
	arrived := afterArriving(w)
	if err := svc.broadcast(context.Background(), arrived); err != nil {
		t.Fatalf("broadcast after the walk: %v", err)
	}

	// And now he relieves himself — in a frame that also claims a position, the
	// far corner of the plane, which nothing reads.
	//
	// Pressed until it lands, because the verb carries a FailChance and a press
	// that loses its nerve writes nothing at all. Driven through the SOCKET here
	// rather than through Do, which is the whole point of this test — so both of
	// the inbound path's own bounds have to be respected: a tick before each
	// press, because the tick's instant is what a verb is stamped with and two
	// presses inside one tick would be the same roll refused twice, and a full
	// verbInterval between them, because the rate limiter drops the second batch
	// in a second without a word.
	const attempts = 200
	pressedAt := arrived
	for i := 0; i < attempts && len(repo.appended) == 0; i++ {
		pressedAt = arrived.Add(time.Duration(i) * verbInterval)
		if err := svc.broadcast(context.Background(), pressedAt); err != nil {
			t.Fatalf("broadcast before pressing: %v", err)
		}
		svc.HandleInbound(context.Background(), member("1"), testRoom,
			fmt.Appendf(nil, `{"t":%q,"verbs":[%q],"x":0.05,"y":0.95}`, TypeDo, ActionRelieve))
	}

	if len(repo.appended) != 1 {
		t.Fatalf("%d events were recorded for one verb; the verb never applied at all, so nothing below is about the deposit: %+v",
			len(repo.appended), repo.appended)
	}
	got := repo.insertedObjects()
	if len(got) != 1 {
		t.Fatalf("%d objects were written for one %s; want exactly one deposit: %+v", len(got), ActionRelieve, got)
	}
	left := got[0]

	if !samePoint(left.at, where) {
		t.Errorf("the deposit was written at (%v,%v); want where the yard believes he is standing, (%v,%v)",
			left.at.X, left.at.Y, where.X, where.Y)
	}
	if samePoint(left.at, Point{X: 0.05, Y: 0.95}) {
		t.Error("the deposit landed on the coordinates the frame claimed; the verb path must read nothing from the payload but the verbs")
	}
	if left.kind != kind.Key {
		t.Errorf("the deposit is of kind %q; want the catalogue's %q", left.kind, kind.Key)
	}
	if left.locationKey != repo.pet.LocationKey {
		t.Errorf("the deposit was left in %q; want the location his pet is in, %q", left.locationKey, repo.pet.LocationKey)
	}
	if left.owner != testAccount {
		t.Errorf("the deposit is owned by %q; want the account that pressed the verb, %q", left.owner, testAccount)
	}
	if left.singleton != kind.Singleton {
		t.Errorf("the deposit was written with singleton=%v; want the catalogue's %v — the column is what the database's "+
			"at-most-one-active index is predicated on, and getting it wrong here silently forbids the second player",
			left.singleton, kind.Singleton)
	}
	if left.expires == nil {
		t.Fatalf("the deposit has no expiry; the catalogue gives %q a lifetime of %s", kind.Key, kind.Lifetime)
	}
	// Measured from the instant the verb was stamped with, which is the tick's —
	// this game has one clock, and a deposit that expired against another would
	// disappear from the plane at a moment nothing else agrees with.
	if want := pressedAt.Add(kind.Lifetime); !left.expires.Equal(want) {
		t.Errorf("the deposit expires at %s; want %s, the verb's own instant plus the catalogue's lifetime %s",
			left.expires.UTC(), want.UTC(), kind.Lifetime)
	}
}

// TestADepositFromSomebodyTheYardHasNeverPlacedLandsAtTheEntrance is the
// fallback the position lookup carries, and the reason it is a fallback rather
// than a refusal.
//
// A verb can arrive from an account with a session and no socket — the HTTP path
// exists, and a client can act before it has ever been drawn — and refusing it
// over a detail the player cannot see would be a worse answer than putting the
// deposit at the door.
func TestADepositFromSomebodyTheYardHasNeverPlacedLandsAtTheEntrance(t *testing.T) {
	// A bladder to empty, for the reason given above: the verb is gated on one,
	// and this test is about the fallback position rather than about the gate.
	repo := playedFor(enoughFor(t, ActionRelieve, epoch))
	svc := planeService(&fakeTransport{}, repo)

	doUntilItLands(t, svc, testAccount, []string{ActionRelieve}, "", at(0))

	got := repo.insertedObjects()
	if len(got) != 1 {
		t.Fatalf("%d objects were written for one %s: %+v", len(got), ActionRelieve, got)
	}
	if !samePoint(got[0].at, spawn) {
		t.Errorf("the deposit of somebody who has never been placed was written at (%v,%v); want the spawn point (%v,%v)",
			got[0].at.X, got[0].at.Y, spawn.X, spawn.Y)
	}
}

// TestTheDepositTheStatsAndTheEventsAreOneWrite is the atomicity, proved from
// the far end: the deposit is the LAST statement in the transaction, so failing
// it is the only way to ask whether the two before it can survive on their own.
//
// A deposit that outlived a rolled-back batch would be a thing in the world that
// no event explains and no snapshot accounts for, and nothing would ever notice
// — no read reconciles the world against the log. The reverse failure is worse
// and quieter: stats and events that committed while the deposit was refused
// would be a Ваня who relieved himself with nothing to show for it.
func TestTheDepositTheStatsAndTheEventsAreOneWrite(t *testing.T) {
	refused := errors.New("the world objects table said no")
	// A bladder to empty: the verb whose atomicity is under test is gated on one,
	// and a batch refused before it reached the transaction would prove nothing
	// about what the transaction takes back out.
	repo := playedFor(enoughFor(t, ActionRelieve, epoch))
	// A handle that can actually begin one. Service.inTx takes a transaction only
	// when the pool it was given supports it, so a test driving this with a fake
	// that cannot would watch three writes land one after another and stay landed
	// — and would prove precisely nothing.
	pool := &txPool{}
	svc := NewService(nil, testRoom, pool, repo, nil)

	// Materialised first, so the rows this test compares against are the ones a
	// player would already have rather than the seeding the verb would do.
	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}
	before := append([]StatRow(nil), repo.rows...)
	if len(before) == 0 {
		t.Fatal("the pet has no stat rows at all; a rollback of nothing would pass this test whatever the service did")
	}

	repo.insertErr = refused
	// Pressed past the nerves, because this test is about the OTHER refusal: a
	// press that lost its nerve never reaches the insert that is rigged to fail.
	_, _, err := pressPastNerves(t, svc, testAccount, []string{ActionRelieve}, "", at(0))
	if !errors.Is(err, refused) {
		t.Fatalf("Do(%s) answered err=%v; want the insert's own failure surfaced to the caller", ActionRelieve, err)
	}

	// It was attempted, and it was attempted inside the transaction: without both
	// halves the assertions below would also pass on a service that never tried
	// to write a deposit at all.
	if n := len(repo.insertedObjects()); n != 1 {
		t.Fatalf("%d deposits were attempted; want exactly the one that failed", n)
	}
	if repo.writes != 1 {
		t.Fatalf("WriteStats was called %d times; want the one call whose effect has to come back out", repo.writes)
	}
	if len(pool.begun) != 1 {
		t.Fatalf("%d transactions were begun for one batch; want exactly 1", len(pool.begun))
	}
	if pool.begun[0].committed {
		t.Error("the transaction was committed even though a statement in it failed")
	}

	// And nothing durable is left over.
	if n := len(repo.appended); n != 0 {
		t.Errorf("%d events survived the failed batch; the log and the deposit are written together or not at all: %+v",
			n, repo.appended)
	}
	if len(repo.rows) != len(before) {
		t.Fatalf("the pet has %d stat rows after the failed batch; want the %d it had before", len(repo.rows), len(before))
	}
	for _, want := range before {
		got, ok := repo.row(want.Key)
		if !ok {
			t.Errorf("%q has no row after the failed batch", want.Key)
			continue
		}
		if got.Value != want.Value || !got.AsOf.Equal(want.AsOf) {
			t.Errorf("%q is (%v, %s) after the failed batch; want the (%v, %s) it was before — the snapshot committed without the deposit",
				want.Key, got.Value, got.AsOf.UTC(), want.Value, want.AsOf.UTC())
		}
	}
}

// TestAVerbThatTouchesNothingInTheWorldLeavesTheCacheAlone is the other side of
// the catalogue lookup, and the reason it names one verb rather than two.
//
// The service reads `Leaves` and `Contests` off the catalogue rather than
// switching on a verb key, so a verb carrying neither has to do two things: put
// nothing on the ground, and leave the world cache alone. The second is the one
// that would rot silently — a verb that refreshed the cache anyway would be a
// database read on every button press for a table it had not touched, and every
// frame it produced would look identical.
//
// «восстать из мертвых» is the verb that qualifies, and drinking is deliberately
// NOT: it draws from the crate, which is a change to the world, so it is
// expected to re-read exactly once. Asserting both in one place is what stops
// the rule being read as "verbs do not touch the world" when it is really "the
// catalogue says which do".
func TestAVerbThatTouchesNothingInTheWorldLeavesTheCacheAlone(t *testing.T) {
	quiet := mustAction(t, ActionRevive)
	if quiet.Leaves != "" || quiet.Contests != "" {
		t.Fatalf("the catalogue now says %q leaves %q and contests %q; this test needs a verb that does neither",
			quiet.Key, quiet.Leaves, quiet.Contests)
	}
	if busy := mustAction(t, ActionDrink); busy.Contests == "" {
		t.Fatalf("the catalogue no longer has %q contest anything; the second half of this test is about a rule the game does not have", busy.Key)
	}

	repo := playedFor()
	svc := planeService(&fakeTransport{}, repo)
	atTheStore(t, svc, repo, testAccount, crateStock)

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionRevive}, "", at(0)); err != nil {
		t.Fatalf("Do(%s): %v", ActionRevive, err)
	}
	if len(repo.appended) != 1 {
		t.Fatalf("%d events were recorded for one verb; it never applied, so this test says nothing about what it touched", len(repo.appended))
	}
	if got := repo.insertedObjects(); len(got) != 0 {
		t.Errorf("%d objects were left behind by a verb the catalogue says leaves nothing: %+v", len(got), got)
	}
	if n := repo.worldObjectReads(); n != 0 {
		t.Errorf("the world was read %d times by a verb that cannot have changed it; want none", n)
	}

	// And the contrast: a drink DOES change the world, so it refreshes what the
	// plane renders from — once, on its own goroutine, which is human-paced.
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", at(1)); err != nil {
		t.Fatalf("Do(%s): %v", ActionDrink, err)
	}
	if n := repo.worldObjectReads(); n != 1 {
		t.Errorf("the world was read %d times by a verb that drew a beer out of the crate; want exactly 1 — the cache the tick renders from is now out of date", n)
	}
	if got := repo.insertedObjects(); len(got) != 0 {
		t.Errorf("%d objects were written by a drink that did not empty the crate; a replacement is only stood up once the last one is gone: %+v", len(got), got)
	}
}

// TestAnObjectIsDrawnAsAnOrdinaryEntity is the whole of the client's contract
// with the world.
//
// It arrives in the roster looking exactly like a pet or an NPC — an id, a
// place, an art key, a pose — so the browser resolves its picture against the
// catalogue the same way it resolves everybody else's and holds no `switch
// (kind)` of its own. That is what makes a new kind of thing a backend change.
//
// The id is truncated, and that is asserted rather than assumed: a uuid is
// thirty-six characters travelling in every frame five times a second to every
// viewer, and the twelve here are the same length a player's pseudonym already
// uses.
func TestAnObjectIsDrawnAsAnOrdinaryEntity(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	lies := Point{X: 0.31, Y: 0.72}
	object := anObject(1, kind.Key, lies, at(30))

	repo := playedFor()
	repo.objects = []WorldObject{object}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	// A hello: the human-paced moment that is allowed to read the world.
	svc.load(context.Background(), testAccount)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]

	want := "obj-" + object.ID[:pseudonymChars]
	p, ok := peerByID(f, want)
	if !ok {
		t.Fatalf("the yard holds an object and no entity in the frame is drawn as %q: %+v", want, f.Peers)
	}
	standingAt(t, p, lies, "the object read out of the database")
	if p.Art != kind.Art {
		t.Errorf("the object is drawn as %q; want the catalogue's art for its kind, %q", p.Art, kind.Art)
	}
	if p.Label != kind.Label {
		t.Errorf("the object is labelled %q; want the catalogue's %q", p.Label, kind.Label)
	}
	if p.Pose != PoseFine {
		t.Errorf("the object is posed %q; want %q — a thing on the ground is not in any trouble", p.Pose, PoseFine)
	}
	if _, full := peerByID(f, "obj-"+object.ID); full {
		t.Errorf("the whole %d-character id travelled on the frame; it is truncated on purpose", len(object.ID))
	}
}

// TestAnExpiredObjectIsNotDrawnAndNobodyIsAskedAboutIt is the payoff of holding
// the expiry rather than the answer.
//
// Nothing sweeps this table and nothing re-reads it on a tick, so a deposit
// stops being drawn because the instant the frame is being rendered at is past
// the expiry the cache already holds — not because anybody asked the database
// whether it should be. Two consequences follow, and both are asserted: it
// disappears from every screen at the same moment, and it costs no query to do
// so.
func TestAnExpiredObjectIsNotDrawnAndNobodyIsAskedAboutIt(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	lasting := anObject(1, kind.Key, Point{X: 0.2, Y: 0.2}, at(10))
	fading := anObject(2, kind.Key, Point{X: 0.8, Y: 0.8}, at(1))

	repo := playedFor()
	repo.objects = []WorldObject{lasting, fading}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	svc.load(context.Background(), testAccount)
	reads := repo.worldObjectReads()
	if reads != 1 {
		t.Fatalf("a hello read the world %d times; want exactly 1", reads)
	}

	// While both are still standing.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast before the expiry: %v", err)
	}
	if n := len(propsOf(tr.frames()[0])); n != 2 {
		t.Fatalf("%d objects were drawn before either had expired; want both", n)
	}

	// And after one of them has run out — the same cache, a later instant.
	if err := svc.broadcast(context.Background(), at(2)); err != nil {
		t.Fatalf("broadcast after the expiry: %v", err)
	}
	f := tr.frames()[1]
	if _, ok := peerByID(f, "obj-"+fading.ID[:pseudonymChars]); ok {
		t.Error("an object whose expiry has passed is still being drawn; the tick's own instant is what decides it")
	}
	if _, ok := peerByID(f, "obj-"+lasting.ID[:pseudonymChars]); !ok {
		t.Error("the object that has not expired was dropped along with the one that had")
	}
	if n := repo.worldObjectReads(); n != reads {
		t.Errorf("the world was read %d times to work out that a deposit had expired; want the %d it stood at before the ticks — expiry is arithmetic over what is already held",
			n, reads)
	}
}

// TestAnObjectWhoseKindHasLeftTheCatalogueIsNotDrawn is what a row of retired
// content does on the read path.
//
// `kind` is text validated in Go rather than an enum, precisely so that adding
// one costs no migration — the price is rows whose kind the catalogue no longer
// defines, and the answer is the same one a stored stat key already gets: it is
// unrenderable, so it is skipped. Not drawn as a placeholder, because unlike a
// pet there is no person behind it who would otherwise vanish from the yard —
// and not fatal to the rest of the frame either, which is the half a loop with
// an early return would get wrong.
func TestAnObjectWhoseKindHasLeftTheCatalogueIsNotDrawn(t *testing.T) {
	const retired = "pettest_retired_kind"
	if _, ok := ObjectKindByKey(retired); ok {
		t.Fatalf("the catalogue now defines %q; this test needs a kind it does not know", retired)
	}
	kind := leavingKind(t, ActionRelieve)
	unknown := anObject(1, retired, Point{X: 0.4, Y: 0.4}, at(10))
	known := anObject(2, kind.Key, Point{X: 0.6, Y: 0.6}, at(10))

	repo := playedFor()
	repo.objects = []WorldObject{unknown, known}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	svc.load(context.Background(), testAccount)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]

	if _, ok := peerByID(f, "obj-"+unknown.ID[:pseudonymChars]); ok {
		t.Errorf("a row of kind %q was drawn; the catalogue cannot say what it looks like, so there is nothing to draw", retired)
	}
	if _, ok := peerByID(f, "obj-"+known.ID[:pseudonymChars]); !ok {
		t.Error("the object of a kind the catalogue does know was dropped as well; one unrenderable row must not cost the yard its furniture")
	}
}

// TestTheHeadCountCountsPeopleAndNotTheThingsLyingAboutInTheYard is the
// assertion a future edit is most likely to break.
//
// `here` is what the client shows as "how many are in the yard", and every
// entity appended after it — the sleepers, the NPCs, and now the objects — is
// deliberately outside it. A deposit is not somebody who is here, and moving one
// `append` above the count would make the yard look busy because a Ваня went to
// the toilet.
func TestTheHeadCountCountsPeopleAndNotTheThingsLyingAboutInTheYard(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	repo := playedFor()
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	// The same one person, with the yard empty and then strewn with deposits.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast with an empty yard: %v", err)
	}
	bare := tr.frames()[0]

	repo.objects = []WorldObject{
		anObject(1, kind.Key, Point{X: 0.1, Y: 0.1}, at(10)),
		anObject(2, kind.Key, Point{X: 0.2, Y: 0.2}, at(10)),
		anObject(3, kind.Key, Point{X: 0.3, Y: 0.3}, at(10)),
	}
	svc.load(context.Background(), testAccount)
	if err := svc.broadcast(context.Background(), at(1)); err != nil {
		t.Fatalf("broadcast with a strewn yard: %v", err)
	}
	strewn := tr.frames()[1]

	if n := len(propsOf(strewn)); n != 3 {
		t.Fatalf("%d objects were drawn; want the 3 in the yard, or this test is not comparing anything", n)
	}
	if inTheYard(strewn) != 1 {
		t.Errorf("the frame says %d are in the yard while one person stands among three deposits; want 1", inTheYard(strewn))
	}
	if inTheYard(strewn) != inTheYard(bare) {
		t.Errorf("the head count went from %d to %d because three things were left on the ground", inTheYard(bare), inTheYard(strewn))
	}
}

// TestTheTickNeverReadsTheWorld is the constraint the entire design of world.go
// is shaped by, and the only place it can be observed.
//
// The broadcast runs five times a second for as long as anybody is connected, so
// a query on it is a query per viewer per tick against a box that also serves the
// site. The world is therefore read at human-paced moments alone — a client
// saying hello, and a verb that changed it — and however many frames go out in
// between, the count must not move. A tick that quietly re-read would produce
// identical frames and be invisible everywhere else.
func TestTheTickNeverReadsTheWorld(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	// A bladder to empty, so the verb below really is one of the two human-paced
	// moments that read the world rather than a refusal that reads nothing.
	repo := playedFor(enoughFor(t, ActionRelieve, epoch))
	repo.objects = []WorldObject{anObject(1, kind.Key, Point{X: 0.5, Y: 0.5}, at(60))}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	// The two moments that ARE allowed to read: a hello, and a verb that left
	// something behind.
	svc.load(context.Background(), testAccount)
	doUntilItLands(t, svc, testAccount, []string{ActionRelieve}, "", at(0))
	const human = 2
	if n := repo.worldObjectReads(); n != human {
		t.Fatalf("a hello and a verb read the world %d times; want %d — one each", n, human)
	}

	// And now a couple of minutes of broadcasts at the real rate.
	const ticks = 60
	for i := 1; i <= ticks; i++ {
		if err := svc.broadcast(context.Background(), at(float64(i)*0.2)); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
	}
	if n := len(tr.frames()); n != ticks {
		t.Fatalf("%d frames were published across %d ticks; the ticks under test did not happen", n, ticks)
	}
	if n := repo.worldObjectReads(); n != human {
		t.Errorf("the world was read %d times across %d broadcasts; want the %d the human-paced moments made and not one more",
			n, ticks, human)
	}
}

// TestWorldLimitBoundsWhatTheFrameCarries is a bandwidth bound rather than a
// tidiness one, AND — since the four locations arrived — a starvation bound as
// well.
//
// Every live object is an entity in a frame sent five times a second to every
// viewer, so the cost is bytes × rate × objects × viewers and the only term this
// game controls is the third. The cap is applied where the rows are fetched — so
// the bound holds however enthusiastically the yard behaves, and holds without
// the render path having to remember to enforce it.
//
// THE SECOND HALF IS WHY IT IS PER LOCATION. A single world-wide budget is one
// five places compete for, so a busy evening in the yard would spend it and
// every other location would come back empty — including whichever one is
// holding the key, which would end the hunt with nothing failing anywhere. The
// case below fills двор past its allowance and then asserts that кусты still has
// everything standing in it.
func TestWorldLimitBoundsWhatTheFrameCarries(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	repo := playedFor()
	for i := 0; i < worldLimitPerLocation+8; i++ {
		// Spread across the plane, and each with an id of its own: two objects
		// sharing the first twelve characters would collapse into one entity on
		// the wire and make this count wrong for the wrong reason.
		at := Point{X: float64(i%8) / 8, Y: float64(i%5) / 5}
		repo.objects = append(repo.objects, anObject(i, kind.Key, at, epoch.Add(time.Hour)))
	}
	// And two in another location, which is well inside its own allowance.
	const elsewhere = 2
	for i := 0; i < elsewhere; i++ {
		at := Point{X: 0.3 + float64(i)/10, Y: 0.4}
		repo.objects = append(repo.objects,
			anObjectIn(1000+i, kind.Key, LocationKusty, at, epoch.Add(time.Hour)))
	}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	svc.load(context.Background(), testAccount)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	if got := repo.worldObjectLimits(); len(got) != 1 || got[0] != worldLimitPerLocation {
		t.Errorf("the world was read with limits %v; want exactly one read capped at worldLimitPerLocation (%d)",
			got, worldLimitPerLocation)
	}
	drawn := map[string]int{}
	for _, p := range propsOf(tr.frames()[0]) {
		drawn[locationOr(p.Loc)]++
	}
	if drawn[LocationYard] != worldLimitPerLocation {
		t.Errorf("%d objects were drawn in the yard out of the %d standing in it; want no more than worldLimitPerLocation (%d)",
			drawn[LocationYard], worldLimitPerLocation+8, worldLimitPerLocation)
	}
	if drawn[LocationKusty] != elsewhere {
		t.Errorf("%d objects were drawn in %q; want all %d of them — a busy yard must not spend another location's allowance",
			drawn[LocationKusty], LocationKusty, elsewhere)
	}
}

// ---------------------------------------------------------------------------
// The key hunt: the first verb whose outcome another player can take away.
//
// One key is lost in the yard, everybody may look, and exactly one person finds
// it. WHICH one is not decided here and cannot be — it is a conditional UPDATE
// against a partial unique index, and it is proved against a real PostgreSQL in
// test/integration. What is decided here is everything built on top of that
// answer, and all of it hangs on one placement: THE CLAIM IS INSIDE THE
// TRANSACTION. A player who loses has his whole batch rolled back, which is how
// "losing costs nothing" falls out for free instead of being a special case
// somebody has to remember to write.
//
// So most of what follows is about what is NOT written, and a response cannot
// show that: a refused batch and a batch nobody ever sent answer identically.
// The assertions are against what the fake repository was handed and what it was
// left holding afterwards.
// ---------------------------------------------------------------------------

// TestOnlyTheWinnerOfTheKeyHasAnythingWrittenDownAtAll is the most valuable pair
// of cases in this file, and the two halves are one test on purpose: what makes
// the mechanic fair is not that the winner is rewarded but that everybody else is
// charged nothing whatsoever.
//
// Winning writes the tally, the event and the key that replaces the one just
// found. Losing writes NOTHING — no stat, no event, no counter, not even the
// re-stamp every other verb performs — because the claim runs in the same
// transaction as the snapshot and returning ErrClaimLost from it takes the rest
// back out with it. An implementation that claimed first and wrote afterwards
// would satisfy every assertion about the winner and quietly charge each loser a
// re-stamped as_of, which is silently erased damage: the whole coupled decay
// rests on those instants.
func TestOnlyTheWinnerOfTheKeyHasAnythingWrittenDownAtAll(t *testing.T) {
	claim := mustAction(t, ActionClaim)
	tally := effectOn(claim, StatKeysFound)
	if tally <= 0 {
		t.Fatalf("the catalogue says %q moves %q by %v; this test is reasoning about a verb that counts something",
			claim.Key, StatKeysFound, tally)
	}
	kind := mustObjectKind(t, KindKey)
	if !kind.Singleton {
		t.Fatalf("the catalogue no longer marks %q a singleton; there would be nothing for two players to contest", kind.Key)
	}
	if kind.Lifetime != 0 {
		t.Fatalf("the catalogue gives %q a lifetime of %s; a hunt that timed out would need the timer this design does not have",
			kind.Key, kind.Lifetime)
	}

	for _, tc := range []struct {
		name string
		won  bool
	}{
		{name: "the one who found them", won: true},
		{name: "everybody who did not", won: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			// A handle that can actually begin a transaction. Service.inTx takes one
			// only when the pool it was given supports it, so a test driving this
			// with a fake that cannot would watch the writes land one after another
			// and stay landed however the claim went — and would prove nothing.
			pool := &txPool{}
			svc := NewService(&fakeTransport{}, testRoom, pool, repo, nil)

			// Materialised first, so the rows compared against below are the ones a
			// player would already have. Seeding happens on the read path, outside
			// the transaction, and would otherwise look like something the batch
			// wrote and failed to take back.
			if _, err := svc.State(context.Background(), testAccount); err != nil {
				t.Fatalf("State: %v", err)
			}
			before := append([]StatRow(nil), repo.rows...)
			if len(before) == 0 {
				t.Fatal("the pet has no stat rows at all; a rollback of nothing would pass this test whatever the service did")
			}
			found, ok := repo.row(StatKeysFound)
			if !ok {
				t.Fatalf("the pet has no %q row; the counter this verb moves has not been materialised", StatKeysFound)
			}

			repo.claimWon = tc.won
			// A key in a place, and him standing in it — so what this case is
			// about is the outcome of the RACE rather than the search that got
			// him to it, which has its own tests below.
			spot := searchingIn(t, svc, repo, testAccount, aHotspot(t, LocationYard, 0))
			st, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, spot, at(0))

			// The race was actually run, for the right kind, in the location his pet
			// is in and in his own name. Without this, everything below would also
			// pass on a service that never contested anything at all.
			claims := repo.attempts()
			if len(claims) != 1 {
				t.Fatalf("%d claims were attempted for one %q; want exactly 1: %+v", len(claims), ActionClaim, claims)
			}
			if claims[0].Kind != kind.Key || claims[0].Location != repo.pet.LocationKey || claims[0].Account != testAccount {
				t.Errorf("the claim was made as %+v; want kind %q in %q for %q",
					claims[0], kind.Key, repo.pet.LocationKey, testAccount)
			}
			if !claims[0].At.Equal(at(0)) {
				t.Errorf("the claim is stamped %s; want the instant the verb was accepted at, %s — this game has one clock",
					claims[0].At.UTC(), at(0).UTC())
			}
			if len(pool.begun) != 1 {
				t.Fatalf("%d transactions were begun for one batch; want exactly 1", len(pool.begun))
			}

			if !tc.won {
				if !errors.Is(err, ErrClaimLost) {
					t.Fatalf("Do(%q) after losing the race = %v; want ErrClaimLost", ActionClaim, err)
				}
				if pool.begun[0].committed {
					t.Error("the transaction was committed for somebody who lost the race; the refusal has to take the whole batch back out")
				}
				if n := len(repo.appended); n != 0 {
					t.Errorf("%d events survived a lost claim; looking for the keys and not finding them is not something that happened to the pet: %+v",
						n, repo.appended)
				}
				if got := repo.insertedObjects(); len(got) != 0 {
					t.Errorf("%d world objects were written by a lost claim; only the winner starts the next hunt: %+v", len(got), got)
				}
				if held := repo.heldBy(kind.Key); held != "" {
					t.Errorf("the key is held by %q after a lost race; a claim that changed no rows must leave it lost", held)
				}
				now, ok := repo.row(StatKeysFound)
				if !ok || now.Value != found.Value {
					t.Errorf("the tally of somebody who lost went from %v to %v; losing costs nothing, and it costs nothing because the batch is rolled back",
						found.Value, now.Value)
				}
				if len(repo.rows) != len(before) {
					t.Fatalf("the pet has %d stat rows after a lost claim; want the %d it had before", len(repo.rows), len(before))
				}
				for _, want := range before {
					got, ok := repo.row(want.Key)
					if !ok {
						t.Errorf("%q has no row after a lost claim", want.Key)
						continue
					}
					if got.Value != want.Value || !got.AsOf.Equal(want.AsOf) {
						t.Errorf("%q is (%v, %s) after a lost claim; want the (%v, %s) it was before — the loser was charged a re-stamp",
							want.Key, got.Value, got.AsOf.UTC(), want.Value, want.AsOf.UTC())
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("Do(%q) after winning the race: %v", ActionClaim, err)
			}
			if !pool.begun[0].committed {
				t.Error("the winner's transaction was never committed")
			}
			if held := repo.heldBy(kind.Key); held != testAccount {
				t.Errorf("the key is held by %q after he won it; want %q", held, testAccount)
			}
			if n := len(repo.appended); n != 1 || repo.appended[0].Verb != ActionClaim {
				t.Fatalf("the winner's log holds %+v; want the one %q that was accepted", repo.appended, ActionClaim)
			}
			if repo.writes != 1 {
				t.Errorf("WriteStats was called %d times for one accepted verb; want once", repo.writes)
			}

			// The tally, in the answer and in the row the next read will decay from.
			want := found.Value + tally
			nearlyStat(t, statOf(t, st, StatKeysFound).Value, want, "the winner's "+StatKeysFound+" in the response")
			stored, ok := repo.row(StatKeysFound)
			if !ok {
				t.Fatalf("the winner has no stored %q row at all", StatKeysFound)
			}
			nearlyStat(t, stored.Value, want, "the winner's stored "+StatKeysFound)

			// And the next hunt, started in the same breath — so the frame after this
			// one already carries a fresh key rather than an empty yard somebody has
			// to be told about.
			got := repo.insertedObjects()
			if len(got) != 1 {
				t.Fatalf("%d world objects were written by a winning claim; want exactly the key that replaces the one he found: %+v", len(got), got)
			}
			next := got[0]
			if next.kind != kind.Key {
				t.Errorf("the replacement is of kind %q; want the catalogue's %q", next.kind, kind.Key)
			}
			// SOMEWHERE THE CATALOGUE KNOWS, and deliberately not «where he was
			// standing». A key names no location, so each fresh one is hidden in a
			// place drawn at random — which is what makes finding one tell you
			// nothing about where the next is. Reusing the finder's own location
			// would be the quiet bug: every key after the first would appear
			// wherever the last was found, so the hunt would settle into one place
			// and the other four would never hold anything again.
			if _, known := LocationByKey(next.locationKey); !known {
				t.Errorf("the replacement was hidden in %q, which is not a location in the catalogue; no search anywhere could ever find it",
					next.locationKey)
			}
			if next.singleton != kind.Singleton {
				t.Errorf("the replacement was written with singleton=%v; want the catalogue's %v — that column is what the database's "+
					"at-most-one-active index is predicated on, and a false one lets the world hold two keys",
					next.singleton, kind.Singleton)
			}
			if next.owner != "" {
				t.Errorf("the replacement is owned by %q; a key is lost by the world rather than dropped by a player", next.owner)
			}
			if next.expires != nil {
				t.Errorf("the replacement expires at %s; a hunt ends when it is won, and nothing here has a timer", next.expires.UTC())
			}
		})
	}
}

// TestTheClaimAndTheKeyThatReplacesItAreOneWrite is the other half of the
// atomicity, seen from the far end.
//
// The replacement is the LAST statement of the transaction, so failing it is the
// only way to ask whether the claim before it can survive on its own. It must
// not, and the failure it would otherwise cause is permanent rather than
// cosmetic: a key marked claimed while its replacement was refused is a yard with
// no key in it and no mechanism that will ever start another, because the next
// hunt is started by a hello finding none — and it would find the claimed one
// gone rather than missing. One transient insert failure would end the game's
// only contested verb for ever, in silence.
func TestTheClaimAndTheKeyThatReplacesItAreOneWrite(t *testing.T) {
	refused := errors.New("the world objects table said no")
	kind := mustObjectKind(t, KindKey)
	repo := playedFor()
	pool := &txPool{}
	svc := NewService(&fakeTransport{}, testRoom, pool, repo, nil)

	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}
	before := append([]StatRow(nil), repo.rows...)
	if len(before) == 0 {
		t.Fatal("the pet has no stat rows at all; a rollback of nothing would pass this test whatever the service did")
	}

	repo.claimWon = true
	spot := searchingIn(t, svc, repo, testAccount, aHotspot(t, LocationYard, 0))
	repo.insertErr = refused

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, spot, at(0)); !errors.Is(err, refused) {
		t.Fatalf("Do(%q) answered err=%v; want the insert's own failure surfaced to the caller", ActionClaim, err)
	}

	// He won it, and the replacement was attempted: without both halves the
	// assertion below would also pass on a service that never claimed anything.
	if n := len(repo.attempts()); n != 1 {
		t.Fatalf("%d claims were attempted; want the one that was won", n)
	}
	if n := len(repo.insertedObjects()); n != 1 {
		t.Fatalf("%d replacements were attempted; want exactly the one that failed", n)
	}
	if len(pool.begun) != 1 {
		t.Fatalf("%d transactions were begun for one batch; want exactly 1", len(pool.begun))
	}
	if pool.begun[0].committed {
		t.Error("the transaction was committed even though the statement that hides the next key failed")
	}

	// THE ASSERTION THIS TEST EXISTS FOR: the key is lost again. The UPDATE that
	// marked it claimed really ran, and the rollback really put it back.
	if held := repo.heldBy(kind.Key); held != "" {
		t.Errorf("the key is still held by %q after the batch that claimed it failed; the claim and its replacement are one write, or the hunt ends for ever",
			held)
	}

	// And nothing else is left over either.
	if n := len(repo.appended); n != 0 {
		t.Errorf("%d events survived the failed batch: %+v", n, repo.appended)
	}
	if len(repo.rows) != len(before) {
		t.Fatalf("the pet has %d stat rows after the failed batch; want the %d it had before", len(repo.rows), len(before))
	}
	for _, want := range before {
		got, ok := repo.row(want.Key)
		if !ok {
			t.Errorf("%q has no row after the failed batch", want.Key)
			continue
		}
		if got.Value != want.Value || !got.AsOf.Equal(want.AsOf) {
			t.Errorf("%q is (%v, %s) after the failed batch; want the (%v, %s) it was before — the snapshot committed without the replacement",
				want.Key, got.Value, got.AsOf.UTC(), want.Value, want.AsOf.UTC())
		}
	}
}

// TestFindingTheKeysPaintsTheYardAndThenStopsPainting is the entire loser
// effect, and the whole of the winner's reward beyond the tally: one happy face,
// every other face sad, for a few seconds.
//
// Three properties, and the two that would rot silently are the second and the
// third. It is COSMETIC — nothing about it is written down — so it has to expire
// by arithmetic against the tick's own clock rather than by anything clearing it,
// exactly as a balloon does. And it never outranks death: the loser's face is put
// on everybody who is in the yard rather than only on the people who were
// looking, so a corpse who happened to be standing there would be drawn sadly
// alive, which would be funny once and wrong every time afterwards. A dead winner
// is not the same problem and cannot happen — a corpse's claim is refused before
// any of this — so the sad case is the only one there is.
func TestFindingTheKeysPaintsTheYardAndThenStopsPainting(t *testing.T) {
	hp := mustStat(t, StatHP)
	winner, bystander, corpse := member("1"), member("2"), member("3")
	if winner.AccountID != testAccount {
		t.Fatalf("the winner's connection belongs to %q and the fake repository holds the pet of %q; the verb below would act for somebody else",
			winner.AccountID, testAccount)
	}

	repo := playedFor()
	tr := &fakeTransport{}
	tr.setMembers(winner, bystander, corpse)
	svc := planeService(tr, repo)

	// A death the plane already knows about, written straight into the cache: the
	// alternative is a second pet in a fake repository that holds one, which would
	// be a fixture about the fake rather than about the mood.
	died := at(0).Add(-time.Hour)
	svc.mu.Lock()
	svc.display[corpse.AccountID] = withDeath(cached(at(0), map[string]float64{StatHP: hp.Min}), died)
	svc.mu.Unlock()

	// One tick, so all three have a placement. A mood is hung on one, and somebody
	// the yard has never placed has nowhere to wear a face.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	repo.claimWon = true
	// After the tick, because standing him in the spot he is about to search is
	// the last thing the winner needs and the broadcast above is what gave the
	// other two somewhere to wear a face.
	spot := searchingIn(t, svc, repo, testAccount, aHotspot(t, LocationYard, 0))
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, spot, at(0)); err != nil {
		t.Fatalf("Do(%q): %v", ActionClaim, err)
	}

	for _, tc := range []struct {
		name string
		when time.Time
		want map[realtime.Member]string
		why  string
	}{
		{
			name: "while it is still on their faces",
			when: at(0).Add(moodFor / 2),
			want: map[realtime.Member]string{winner: PoseHappy, bystander: PoseSad, corpse: PoseDead},
			why:  "winning and losing move no stat, so the face IS the outcome — and a corpse does not get one",
		},
		{
			name: "once the moment has passed",
			when: at(0).Add(moodFor),
			want: map[realtime.Member]string{winner: PoseFine, bystander: PoseFine, corpse: PoseDead},
			why:  "it expires by comparison against the tick's clock, so nothing schedules its removal and nothing has to be cleaned up",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.broadcast(context.Background(), tc.when); err != nil {
				t.Fatalf("broadcast: %v", err)
			}
			frames := tr.frames()
			f := frames[len(frames)-1]
			for who, want := range tc.want {
				p, ok := peerOf(svc, f, who.AccountID)
				if !ok {
					t.Fatalf("%s is not in the frame at all: %+v", who.ConnID, f)
				}
				if p.Pose != want {
					t.Errorf("%s is drawn %q %s; want %q — %s", who.ConnID, p.Pose, tc.name, want, tc.why)
				}
			}
			// Stated separately as well as in the table, because it is the one that
			// has to hold for every instant rather than for these two.
			dead, _ := peerOf(svc, f, corpse.AccountID)
			if dead.Pose == PoseHappy || dead.Pose == PoseSad {
				t.Errorf("a dead Ваня is drawn %q; he is not in a position to have feelings about who found the keys", dead.Pose)
			}
		})
	}
}

// TestAHelloStandsUpEverySingletonTheYardIsMissingAndLeavesTheRestAlone is how
// the yard is never without its key or its beer, with nothing anywhere holding a
// timer.
//
// It runs at a hello — a fresh socket, human-paced — and never on the tick, and
// each insert is the identical statement the exhausting write uses for the
// replacement, against the identical partial index. So a cold start and a
// restart nobody won are one mechanism rather than two that have to be kept in
// agreement.
//
// It iterates the CATALOGUE rather than naming the two kinds, which is the
// property under test as much as the counting is: the crate arrived as an entry
// and this loop did not change, so the third singleton will not need it to
// either. And the count is an assertion in its own right — an implementation
// that inserted unconditionally and let the index swallow the extra would look
// correct in every frame it produced while filling the table with refused
// writes, one per visitor.
func TestAHelloStandsUpEverySingletonTheYardIsMissingAndLeavesTheRestAlone(t *testing.T) {
	var singletons []ObjectKind
	for _, def := range Content().ObjectKinds {
		if def.Singleton {
			singletons = append(singletons, def)
		}
	}
	if len(singletons) < 2 {
		t.Fatalf("the catalogue holds %d singleton kinds; this test is about a loop over them and needs more than one to be one",
			len(singletons))
	}
	all := map[string]string{}
	var everyKey []string
	for _, def := range singletons {
		all[def.Key] = "000000000009-4a1e-" + def.Key
		everyKey = append(everyKey, def.Key)
	}

	for _, tc := range []struct {
		name    string
		running map[string]string
		want    []string
		why     string
	}{
		{
			name: "a world with nothing in it at all", running: nil, want: everyKey,
			why: "somebody arriving to a yard nobody has ever played in has to find a hunt to join and a crate to drink from",
		},
		{
			name: "a world that already has everything", running: all, want: nil,
			why: "every hello would otherwise write rows the index refuses, which is a table full of nothing and a wasted write per visitor",
		},
		{
			name:    "a world missing exactly one of them",
			running: map[string]string{KindKey: all[KindKey]},
			want:    []string{KindCrate},
			why:     "the two are independent — somebody drank the last beer while the keys were still lost, and only the crate has to come back",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			repo.active = tc.running
			svc := planeService(&fakeTransport{}, repo)

			svc.load(context.Background(), testAccount)

			if n, want := repo.singletonReads(), len(singletons); n != want {
				t.Fatalf("one hello asked %d times whether a singleton was standing; want %d — once per singleton kind in the catalogue", n, want)
			}
			got := repo.insertedObjects()
			if len(got) != len(tc.want) {
				t.Fatalf("%d objects were stood up by one hello into %s; want %d (%v) — %s",
					len(got), tc.name, len(tc.want), tc.want, tc.why)
			}
			for _, want := range tc.want {
				started, ok := insertedOf(got, want)
				if !ok {
					t.Fatalf("nothing of kind %q was stood up into %s; want one — %s", want, tc.name, tc.why)
				}
				assertSpawned(t, started, mustObjectKind(t, want))
			}
		})
	}
}

// insertedOf picks the one insert of a kind out of a batch of them.
func insertedOf(got []insertedObject, kind string) (insertedObject, bool) {
	for _, o := range got {
		if o.kind == kind {
			return o, true
		}
	}
	return insertedObject{}, false
}

// assertSpawned checks one world-the-world spawned object against the catalogue
// entry it was supposed to be built from.
//
// Shared by the hello above and by the replacement a winning claim or an
// emptying draw stands up, because the two paths write the identical statement
// and the whole point is that they cannot disagree. Every expectation comes off
// the catalogue rather than being written down, so a retune is not reported as a
// regression — what is pinned is that the row AGREES with the content.
func assertSpawned(t *testing.T, got insertedObject, def ObjectKind) {
	t.Helper()
	// WHERE IT WENT IS THE CATALOGUE'S ANSWER, and there are two of them. A kind
	// that names a location is pinned to it — the crate is always in the yard, so
	// a player can be told to walk to the beer. A kind that names none may be in
	// any of the five, which is the key: the location is drawn with crypto/rand
	// every time one is hidden, so the only assertion that can be made is that it
	// is somewhere the catalogue knows about. A spawn into a location that is not
	// in the catalogue would be a key nobody could ever reach, because a search is
	// judged against the hotspots of the searcher's own location.
	if def.Location != "" && got.locationKey != def.Location {
		t.Errorf("%q was stood up in %q; the catalogue pins it to %q, and a shop that wandered would make the walk to it a matter of luck",
			def.Key, got.locationKey, def.Location)
	}
	if _, known := LocationByKey(got.locationKey); !known {
		t.Errorf("%q was stood up in %q, which is not a location in the catalogue; nothing could ever be searched there",
			def.Key, got.locationKey)
	}
	if got.singleton != def.Singleton {
		t.Errorf("%q was stood up with singleton=%v; want the catalogue's %v, which is what the at-most-one-active index is predicated on",
			def.Key, got.singleton, def.Singleton)
	}
	if got.owner != "" {
		t.Errorf("%q was stood up by %q; the world puts these out, and nobody in particular owns one", def.Key, got.owner)
	}
	if got.expires != nil {
		t.Errorf("%q expires at %s; both of these end when they are used up rather than when they time out, and a timeout would need the timer this design has not got",
			def.Key, got.expires.UTC())
	}
	if got.at.X < 0 || got.at.X > 1 || got.at.Y < 0 || got.at.Y > 1 {
		t.Errorf("%q was put at (%v,%v), off the plane; the column itself CHECKs 0..1, so that insert would be refused outright",
			def.Key, got.at.X, got.at.Y)
	}
	// A kind with a pitch stands ON it, and one without is hidden somewhere the
	// catalogue does not name. That nil is the entire difference between a shop
	// and a lost key, and it is read in one place — a spawn that ignored it would
	// put the beer store somewhere new on every restart.
	if def.At != nil && !samePoint(got.at, *def.At) {
		t.Errorf("%q was put at (%v,%v); want the catalogue's own pitch (%v,%v) — a shop that moved would make the walk to it a matter of luck",
			def.Key, got.at.X, got.at.Y, def.At.X, def.At.Y)
	}
	switch def.Contest {
	case ContestStock:
		if got.remaining == nil {
			t.Fatalf("%q was stood up with a NULL remaining; the catalogue says it is drawn down, and a null there is a crate nothing can ever take from", def.Key)
		}
		if *got.remaining != def.Stock {
			t.Errorf("%q was stood up holding %d; want the catalogue's %d", def.Key, *got.remaining, def.Stock)
		}
	default:
		if got.remaining != nil {
			t.Errorf("%q was stood up with remaining=%d; nothing draws from it, and a count there is a number no statement would ever move",
				def.Key, *got.remaining)
		}
	}
}

// TestTheFrameNamesTheKeyThatIsLostAndAsksNobodyWhichOneItIs is the hunt as the
// client receives it, and the constraint that decides how it is published.
//
// It rides the idempotent full-state frame as STATE rather than arriving as an
// announcement, and that distinction has teeth: a one-shot «ключи снова
// потерялись» is exactly what somebody who opened the app thirty seconds later
// misses. An id that changes is what a client watches, so joining at any instant
// shows the world as it is.
//
// And it costs no query, which is the same rule the rest of world.go is shaped by
// — the tick renders from the cache a hello filled. Asserted by counting reads
// across a couple of minutes of broadcasts at the real rate, because a tick that
// quietly re-read would produce identical frames and be invisible everywhere else.
func TestTheFrameNamesTheKeyThatIsLostAndAsksNobodyWhichOneItIs(t *testing.T) {
	relief := leavingKind(t, ActionRelieve)
	lost := aLostKey(1, Point{X: 0.4, Y: 0.6})

	for _, tc := range []struct {
		name    string
		objects []WorldObject
		running map[string]string
		want    string
		why     string
	}{
		{
			name:    "a key lying in the yard",
			objects: []WorldObject{lost, anObject(2, relief.Key, Point{X: 0.2, Y: 0.2}, at(60))},
			running: map[string]string{KindKey: lost.ID},
			want:    lost.ID[:pseudonymChars],
			why:     "twelve characters, the same truncation every other id on this frame uses, for a field that changes a few times an evening",
		},
		{
			name:    "a yard with nothing in it but a deposit",
			objects: []WorldObject{anObject(2, relief.Key, Point{X: 0.2, Y: 0.2}, at(60))},
			want:    "",
			why:     "empty is a state the yard should not stay in for long, and the next hello is what ends it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			repo.objects = tc.objects
			repo.active = tc.running
			tr := &fakeTransport{}
			tr.setMembers(member("1"))
			svc := planeService(tr, repo)

			// The one human-paced moment allowed to read any of this.
			svc.load(context.Background(), testAccount)
			world, hunts := repo.worldObjectReads(), repo.singletonReads()

			const ticks = 60
			for i := 1; i <= ticks; i++ {
				if err := svc.broadcast(context.Background(), at(float64(i)*0.2)); err != nil {
					t.Fatalf("broadcast %d: %v", i, err)
				}
			}

			frames := tr.frames()
			if len(frames) != ticks {
				t.Fatalf("%d frames were published across %d ticks; the ticks under test did not happen", len(frames), ticks)
			}
			for i, f := range frames {
				if f.Hunt != tc.want {
					t.Fatalf("frame %d names the hunt %q; want %q — %s", i, f.Hunt, tc.want, tc.why)
				}
			}
			if n := repo.worldObjectReads(); n != world {
				t.Errorf("the world was read %d times across %d broadcasts; want the %d the hello made and not one more", n, ticks, world)
			}
			if n := repo.singletonReads(); n != hunts {
				t.Errorf("the database was asked which hunt is running %d times across %d broadcasts; want the %d from the hello — the id comes out of the cache",
					n, ticks, hunts)
			}
		})
	}
}

// TestAKeyIsHiddenAtOneOfTheLocationsOwnHotspots is the whole of where a key
// goes, and it replaces the draw-a-random-point placement it supersedes.
//
// A key used to be hidden anywhere on the plane and DRAWN there, which made
// «искать ключи» a race to press a button somebody could already see the answer
// to. It is now hidden at one of the five places the location publishes — never
// between them, never at a coordinate no hotspot occupies — because the whole
// mechanic is that a player picks one of those five and walks to it. A key
// somewhere else is a key nobody can name.
//
// Two properties, and both would fail silently. It must land ON a hotspot,
// exactly, because that is what `searched` resolves against. And it must move: a
// key that always landed in the same place would be found by whoever noticed
// first, every round, for ever. Neither the number of hotspots nor their
// coordinates are written down here — they are content somebody is meant to move
// by feel, and a test carrying them would report every retune as a regression.
func TestAKeyIsHiddenAtOneOfTheLocationsOwnHotspots(t *testing.T) {
	yard, ok := LocationByKey(LocationYard)
	if !ok {
		t.Fatalf("the catalogue has no location %q at all", LocationYard)
	}
	if len(yard.Hotspots) < 2 {
		t.Fatalf("%q publishes %d hotspots; a search needs somewhere to be wrong as well as somewhere to be right",
			LocationYard, len(yard.Hotspots))
	}
	key := mustObjectKind(t, KindKey)
	if !key.Hidden {
		t.Fatalf("the catalogue no longer marks %q hidden; it would be drawn on the plane and there would be nothing to search for", key.Key)
	}
	if key.At != nil {
		t.Fatalf("the catalogue gives %q a pitch of its own at %+v; a key that stands somewhere in particular is a shop", key.Key, *key.At)
	}

	// Enough draws that every hotspot is hit with overwhelming probability — at
	// five places, the chance of missing one across this many is far below any
	// number worth writing down.
	const draws = 2000
	seen := map[string]int{}
	for i := 0; i < draws; i++ {
		p := hideIn(LocationYard)
		if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
			t.Fatalf("draw %d hid the key at (%v,%v); the world-object table CHECKs both axes into 0..1, so that row would be refused outright",
				i, p.X, p.Y)
		}
		spot, ok := hotspotAtPoint(yard, p)
		if !ok {
			t.Fatalf("draw %d hid the key at (%v,%v), which is not any of %q's hotspots; a key nobody can name is a hunt nobody can win",
				i, p.X, p.Y, LocationYard)
		}
		seen[spot.Key]++
	}
	for _, h := range yard.Hotspots {
		if seen[h.Key] == 0 {
			t.Errorf("%q never held the key across %d draws; every published place has to be worth searching, or the list is longer than the game",
				h.Key, draws)
		}
	}
}

// hotspotAtPoint finds the hotspot a point is exactly on, which is what a spawn
// is required to produce.
//
// Exact rather than nearest, deliberately: `nearestHotspot` is total by design
// and would answer for any point on the plane, so using it here would let a key
// hidden between two bushes pass a test whose whole subject is that it cannot be.
func hotspotAtPoint(loc Location, at Point) (Hotspot, bool) {
	for _, h := range loc.Hotspots {
		if h.At == at {
			return h, true
		}
	}
	return Hotspot{}, false
}

// ---------------------------------------------------------------------------
// The beer store: the first verb that is about WHERE HE IS STANDING, and the
// second discipline the world objects are contested under.
//
// Two rules arrived together and they refuse for different reasons on purpose.
// You can be at the crate and find it empty, or hold the whole yard's beer at
// arm's length and be too far to reach it — and telling the player which is the
// difference between walking over and waiting.
//
// The arrival gate lives in Service.Do and NOT in `apply`, which is the whole of
// what makes it safe: `apply` is what a replay folds over a history, so a rule
// about where somebody was standing cannot be one of its inputs. That property
// is asserted in event_test.go, from the far side.
// ---------------------------------------------------------------------------

// TestDrinkingIsRefusedFromAcrossTheYardAndAllowedBesideTheCrate is the movement
// gate, in both directions and at its edge.
//
// It is decided from the IN-MEMORY placement against the crate in the IN-MEMORY
// world, which is the pairing that matters: the same cache the frame's store
// block is built from, so the button the client greys out and the verb the
// server refuses are looking at one number rather than two that can drift apart.
//
// The refusal costs nothing, and that half is asserted against what the fake was
// handed rather than against the answer — a refused batch and a batch nobody
// sent look identical from outside, so the only place the difference is visible
// is the stat rows, the log, and whether anybody drew from the crate at all.
func TestDrinkingIsRefusedFromAcrossTheYardAndAllowedBesideTheCrate(t *testing.T) {
	drink := mustAction(t, ActionDrink)
	if drink.NeedsNear == "" {
		t.Fatalf("the catalogue no longer gates %q on standing near anything; this test is about a rule the game does not have", drink.Key)
	}
	crate := mustObjectKind(t, drink.NeedsNear)
	if crate.At == nil {
		t.Fatalf("%q has no pitch; there is nothing to measure a distance to", crate.Key)
	}
	// Just inside and just outside the threshold, expressed as fractions of it so
	// the case still means "barely there" and "barely not" after a retune. On one
	// axis, so the distance IS the offset and no test has to redo the hypotenuse.
	near := Point{X: crate.At.X - arriveWithin*0.9, Y: crate.At.Y}
	beyond := Point{X: crate.At.X - arriveWithin*1.1, Y: crate.At.Y}

	for _, tc := range []struct {
		name  string
		stand func(svc *Service)
		want  error
		why   string
	}{
		{
			name:  "standing right at it",
			stand: func(svc *Service) { standAt(svc, testAccount, *crate.At) },
			why:   "the ordinary case: he walked over and pressed the button",
		},
		{
			name:  "a step away, inside the threshold",
			stand: func(svc *Service) { standAt(svc, testAccount, near) },
			why:   "beside it is a radius rather than a coordinate, or arriving would be a pixel-hunt on a phone",
		},
		{
			name:  "a step further, outside it",
			stand: func(svc *Service) { standAt(svc, testAccount, beyond) },
			want:  ErrTooFar,
			why:   "the threshold has to actually refuse somebody, or the walk to the store means nothing",
		},
		{
			name:  "across the yard",
			stand: func(svc *Service) { standAt(svc, testAccount, Point{X: 1 - crate.At.X, Y: 1 - crate.At.Y}) },
			want:  ErrTooFar,
			why:   "beer comes out of the crate, so a Ваня nowhere near it cannot have any",
		},
		{
			name:  "somebody the yard has never placed at all",
			stand: func(*Service) {},
			want:  ErrTooFar,
			why:   "an unplaced account falls back to the entrance, which is a walk away — refusing is right, and it is why the crate is inside one walk of the door",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			svc := planeService(&fakeTransport{}, repo)
			crateInTheYard(t, svc, repo, crateStock)
			tc.stand(svc)

			// Materialised first, so the rows compared against below are the ones
			// a player would already have. Seeding happens on the read path, which
			// a refusal still goes through — the gate is a rule about a verb, not
			// about whether the pet exists.
			if _, err := svc.State(context.Background(), testAccount); err != nil {
				t.Fatalf("State: %v", err)
			}
			before := append([]StatRow(nil), repo.rows...)
			if len(before) == 0 {
				t.Fatal("the pet has no stat rows at all; a comparison against nothing would pass whatever the service did")
			}
			writes := repo.writes

			_, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", at(0))

			if tc.want == nil {
				if err != nil {
					t.Fatalf("Do(%s) while %s = %v; want it allowed — %s", ActionDrink, tc.name, err, tc.why)
				}
				if n := len(repo.drawn()); n != 1 {
					t.Errorf("%d beers were drawn out of the crate for one accepted drink; want exactly 1", n)
				}
				return
			}

			if !errors.Is(err, tc.want) {
				t.Fatalf("Do(%s) while %s = %v; want %v — %s", ActionDrink, tc.name, err, tc.want, tc.why)
			}
			// AND IT COST HIM NOTHING. Not a stat, not an event, not a beer, and
			// not the re-stamp every accepted verb performs — that last one is the
			// finest-grained way of saying "nothing was written", because a
			// rolled-back batch and a batch that wrote the same numbers again are
			// otherwise indistinguishable.
			if n := len(repo.drawn()); n != 0 {
				t.Errorf("%d beers were drawn for a refused drink; the gate runs before the transaction, so the crate must not have been touched", n)
			}
			if n := len(repo.appended); n != 0 {
				t.Errorf("%d events survived a refused drink; being too far from the crate is not something that happened to the pet: %+v", n, repo.appended)
			}
			if repo.writes != writes {
				t.Errorf("WriteStats was called %d times for a refused drink; want the %d it stood at before", repo.writes, writes)
			}
			for _, want := range before {
				got, ok := repo.row(want.Key)
				if !ok {
					t.Errorf("%q has no row after a refused drink", want.Key)
					continue
				}
				if got.Value != want.Value || !got.AsOf.Equal(want.AsOf) {
					t.Errorf("%q is (%v, %s) after a refused drink; want the (%v, %s) it was before — a refusal charged him a re-stamp",
						want.Key, got.Value, got.AsOf.UTC(), want.Value, want.AsOf.UTC())
				}
			}
		})
	}
}

// TestTheGateReadsWhereHeHasGotToAndNotWhereHeSetOff is what makes the walk to
// the store a walk rather than a teleport with extra steps.
//
// A journey is evaluated at an instant, so the gate has to ask at `at` — the
// moment the batch is folded — rather than at the moment the tap was accepted.
// An implementation that read the walk's destination would let him drink the
// instant he pressed "go", and one that read its origin would refuse him after he
// had plainly arrived; both would look right in a screenshot.
func TestTheGateReadsWhereHeHasGotToAndNotWhereHeSetOff(t *testing.T) {
	crate := mustObjectKind(t, KindCrate)
	if crate.At == nil {
		t.Fatalf("%q has no pitch; there is nothing to walk to", crate.Key)
	}
	// As long a walk as can never be refused, ending at the crate — so this test
	// is about the clock rather than about luck, and its halfway point is
	// comfortably outside the arrival threshold.
	from := Point{X: crate.At.X - tiredFrom*0.95, Y: crate.At.Y}

	repo := playedFor()
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)
	crateInTheYard(t, svc, repo, crateStock)
	standAt(svc, testAccount, from)

	// A tick, so the tap below is a journey measured against a clock this test
	// owns rather than a teleport.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	w := tap(t, svc, member("1"), *crate.At)

	// Halfway there, which is still further than the threshold.
	midway := partWayThrough(w)
	if d := distance(w.at(midway), *crate.At); d <= arriveWithin {
		t.Fatalf("halfway along a walk of %v he is already within %v of the crate; this test needs a journey whose middle is genuinely too far away",
			distance(w.from, w.to), arriveWithin)
	}
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", midway); !errors.Is(err, ErrTooFar) {
		t.Fatalf("Do(%s) halfway to the crate = %v; want ErrTooFar — he is still walking, and where he is GOING is not where he is",
			ActionDrink, err)
	}

	// And once he is there.
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", afterArriving(w)); err != nil {
		t.Fatalf("Do(%s) after arriving at the crate: %v; the gate is reading something other than his position at the instant of the verb",
			ActionDrink, err)
	}
}

// TestTheContestFieldRoutesEachVerbToItsOwnDiscipline is the seam this iteration
// earned, asserted where it can actually be wrong.
//
// The verb names a kind, the kind names a discipline, and the service switches
// on the discipline alone. What that has to buy is that each verb reaches its OWN
// statement and only its own: a drink that went through ClaimSingleton would win
// the whole crate at once, and a claim that went through DrawFromStock would
// hand the keys to everybody in turn. Both would still look like a working
// button.
//
// The key's own behaviour is asserted here too, unchanged, because moving it off
// the inline `if` and onto the catalogue is a refactor and the point of a
// refactor is that nothing moved.
func TestTheContestFieldRoutesEachVerbToItsOwnDiscipline(t *testing.T) {
	for _, tc := range []struct {
		verb       string
		discipline ContestKind
		claims     int
		draws      int
	}{
		{verb: ActionClaim, discipline: ContestSingleWinner, claims: 1, draws: 0},
		{verb: ActionDrink, discipline: ContestStock, claims: 0, draws: 1},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			action := mustAction(t, tc.verb)
			kind := mustObjectKind(t, action.Contests)
			if kind.Contest != tc.discipline {
				t.Fatalf("%q contests %q, which the catalogue settles by %q; this case is about %q",
					tc.verb, kind.Key, kind.Contest, tc.discipline)
			}

			repo := playedFor()
			repo.claimWon = true
			svc := contestedService(repo)
			spot := readyFor(t, svc, repo, testAccount, action)

			if _, err := svc.Do(context.Background(), testAccount, []string{tc.verb}, spot, at(0)); err != nil {
				t.Fatalf("Do(%q): %v", tc.verb, err)
			}

			if n := len(repo.attempts()); n != tc.claims {
				t.Errorf("%q made %d all-or-nothing claims; want %d", tc.verb, n, tc.claims)
			}
			if n := len(repo.drawn()); n != tc.draws {
				t.Errorf("%q made %d draws from stock; want %d", tc.verb, n, tc.draws)
			}
			// And whichever statement it did reach was told the right thing: the
			// kind off the catalogue, the location his pet is in, and the instant
			// the verb was accepted at, because this game has one clock.
			switch tc.discipline {
			case ContestSingleWinner:
				got := repo.attempts()[0]
				if got.Kind != kind.Key || got.Location != repo.pet.LocationKey || got.Account != testAccount {
					t.Errorf("the claim was made as %+v; want kind %q in %q for %q", got, kind.Key, repo.pet.LocationKey, testAccount)
				}
				if !got.At.Equal(at(0)) {
					t.Errorf("the claim is stamped %s; want the instant the verb was accepted at, %s", got.At.UTC(), at(0).UTC())
				}
			case ContestStock:
				got := repo.drawn()[0]
				if got.Kind != kind.Key || got.Location != repo.pet.LocationKey {
					t.Errorf("the draw was made as %+v; want kind %q in %q", got, kind.Key, repo.pet.LocationKey)
				}
				if !got.At.Equal(at(0)) {
					t.Errorf("the draw is stamped %s; want the instant the verb was accepted at, %s", got.At.UTC(), at(0).UTC())
				}
			}
		})
	}
}

// TestTheCrateIsReplacedOnlyByTheDrawThatEmptiesIt is the difference between the
// two disciplines stated as behaviour rather than as a field.
//
// An all-or-nothing claim exhausts the thing it wins, so the replacement follows
// every win. A crate is used up GRADUALLY, so a replacement after every draw
// would stand up a fresh crate per beer — six crates for six drinks, each of
// them refused by the partial index except the first, and the yard would be
// permanently full while looking exactly right.
func TestTheCrateIsReplacedOnlyByTheDrawThatEmptiesIt(t *testing.T) {
	crate := mustObjectKind(t, KindCrate)
	repo := playedFor()
	// Two left, so the test watches an ordinary draw and the last one.
	repo.stock = map[string]int{crate.Key: 2}
	svc := planeService(&fakeTransport{}, repo)
	atTheStore(t, svc, repo, testAccount, 2)

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", at(0)); err != nil {
		t.Fatalf("the first drink: %v", err)
	}
	if left := repo.leftIn(crate.Key); left != 1 {
		t.Fatalf("the crate holds %d after one drink out of two; want 1", left)
	}
	if got := repo.insertedObjects(); len(got) != 0 {
		t.Fatalf("%d crates were stood up by a draw that left one behind; the replacement belongs to the draw that empties it: %+v", len(got), got)
	}

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", at(1)); err != nil {
		t.Fatalf("the drink that empties it: %v", err)
	}
	if left := repo.leftIn(crate.Key); left != 0 {
		t.Fatalf("the crate holds %d after both drinks; want 0", left)
	}
	got := repo.insertedObjects()
	if len(got) != 1 {
		t.Fatalf("%d crates were stood up by the draw that emptied the last one; want exactly the replacement: %+v", len(got), got)
	}
	// A FULL one, in the same place, owned by nobody — another crate was put out,
	// rather than somebody putting the empty one back.
	assertSpawned(t, got[0], crate)
}

// TestTheDrawAndTheCrateThatReplacesItAreOneWrite is the atomicity, seen from the
// far end — the same shape the key's replacement is proved by, and for a failure
// that is worse here.
//
// The replacement is the LAST statement of the transaction, so failing it is the
// only way to ask whether the draw before it can survive on its own. It must
// not: a crate marked exhausted whose replacement was refused is a yard with no
// beer in it and no mechanism that would ever bring more, because the next hello
// starts one only when it finds none standing — and it would find the exhausted
// one gone rather than missing. One transient insert failure would end drinking
// for ever, in silence.
func TestTheDrawAndTheCrateThatReplacesItAreOneWrite(t *testing.T) {
	refused := errors.New("the world objects table said no")
	crate := mustObjectKind(t, KindCrate)

	repo := playedFor()
	repo.stock = map[string]int{crate.Key: 1}
	// A handle that can actually begin a transaction. Service.inTx takes one only
	// when the pool it was given supports it, so a test driving this with a fake
	// that cannot would watch the writes land one after another and stay landed
	// however the insert ended — and would prove nothing.
	pool := &txPool{}
	svc := NewService(&fakeTransport{}, testRoom, pool, repo, nil)
	atTheStore(t, svc, repo, testAccount, 1)

	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}
	before := append([]StatRow(nil), repo.rows...)
	if len(before) == 0 {
		t.Fatal("the pet has no stat rows at all; a rollback of nothing would pass this test whatever the service did")
	}
	repo.insertErr = refused

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", at(0)); !errors.Is(err, refused) {
		t.Fatalf("Do(%s) answered err=%v; want the insert's own failure surfaced to the caller", ActionDrink, err)
	}

	// He drew the last beer and the replacement was attempted: without both
	// halves the assertion below would also pass on a service that never drew
	// anything.
	if n := len(repo.drawn()); n != 1 {
		t.Fatalf("%d draws were made; want the one that emptied the crate", n)
	}
	if n := len(repo.insertedObjects()); n != 1 {
		t.Fatalf("%d replacements were attempted; want exactly the one that failed", n)
	}
	if len(pool.begun) != 1 {
		t.Fatalf("%d transactions were begun for one batch; want exactly 1", len(pool.begun))
	}
	if pool.begun[0].committed {
		t.Error("the transaction was committed even though the statement that stands up the next crate failed")
	}

	// THE ASSERTION THIS TEST EXISTS FOR: the beer is back. The UPDATE that took
	// it really ran, and the rollback really put it back.
	if left := repo.leftIn(crate.Key); left != 1 {
		t.Errorf("the crate holds %d after the batch that emptied it failed; the draw and its replacement are one write, or the yard runs dry for ever", left)
	}
	if n := len(repo.appended); n != 0 {
		t.Errorf("%d events survived the failed batch: %+v", n, repo.appended)
	}
	for _, want := range before {
		got, ok := repo.row(want.Key)
		if !ok {
			t.Errorf("%q has no row after the failed batch", want.Key)
			continue
		}
		if got.Value != want.Value || !got.AsOf.Equal(want.AsOf) {
			t.Errorf("%q is (%v, %s) after the failed batch; want the (%v, %s) it was before — the snapshot committed without the replacement",
				want.Key, got.Value, got.AsOf.UTC(), want.Value, want.AsOf.UTC())
		}
	}
}

// TestAnEmptyCrateRefusesTheDrinkAndCostsHimNothing is the second refusal, and
// the reason there are two of them.
//
// The client greys the button when the frame says nothing is left, so a player
// should meet this rarely — but a count on a frame is always a moment out of
// date, and a greyed button is a suggestion. What matters is that being refused
// by the crate is exactly as free as being refused by the gate: the draw is
// inside the transaction, so returning from it takes the snapshot and the log
// back out with it.
func TestAnEmptyCrateRefusesTheDrinkAndCostsHimNothing(t *testing.T) {
	crate := mustObjectKind(t, KindCrate)
	repo := playedFor()
	// Empty, which the fake only ever is when a test says so — a crate is stood
	// up full and replaced the instant it empties, so this is the exceptional
	// state rather than the default.
	repo.stock = map[string]int{crate.Key: 0}
	pool := &txPool{}
	svc := NewService(&fakeTransport{}, testRoom, pool, repo, nil)
	// The CACHE still says there is beer, which is the case worth arranging: the
	// frame the player acted on was a moment out of date, and the database is
	// what decides.
	atTheStore(t, svc, repo, testAccount, 1)

	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}
	before := append([]StatRow(nil), repo.rows...)
	if len(before) == 0 {
		t.Fatal("the pet has no stat rows at all; a rollback of nothing would pass this test whatever the service did")
	}

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionDrink}, "", at(0)); !errors.Is(err, ErrOutOfStock) {
		t.Fatalf("Do(%s) at an empty crate = %v; want ErrOutOfStock — and NOT ErrTooFar, which would send him walking to a crate he is already standing at",
			ActionDrink, err)
	}
	if n := len(repo.drawn()); n != 1 {
		t.Fatalf("%d draws were attempted; want the one that found nothing — the database decides this, not the cached count", n)
	}
	if pool.begun[0].committed {
		t.Error("the transaction was committed for somebody who found the crate empty; the refusal has to take the whole batch back out")
	}
	if n := len(repo.appended); n != 0 {
		t.Errorf("%d events survived a refused drink; reaching for a beer that was not there is not something that happened to the pet: %+v", n, repo.appended)
	}
	if got := repo.insertedObjects(); len(got) != 0 {
		t.Errorf("%d crates were stood up by a draw that got nothing; only the draw that EMPTIES one brings another: %+v", len(got), got)
	}
	for _, want := range before {
		got, ok := repo.row(want.Key)
		if !ok {
			t.Errorf("%q has no row after the refused drink", want.Key)
			continue
		}
		if got.Value != want.Value || !got.AsOf.Equal(want.AsOf) {
			t.Errorf("%q is (%v, %s) after a refused drink; want the (%v, %s) it was before — the loser was charged a re-stamp",
				want.Key, got.Value, got.AsOf.UTC(), want.Value, want.AsOf.UTC())
		}
	}
}

// TestTheFrameCarriesTheStoreOnceAndAsksNobodyWhatIsLeft is the store as the
// client receives it, and the two constraints that decide its shape.
//
// It is ONE SHARED FIELD rather than one per peer, and the arithmetic is the
// reason: at roughly 36 bytes it costs 180 B/s to each phone however busy the
// yard is, where hanging it off every entity would be that times the entity
// count — 4.3 KB/s at the two dozen the world cap allows, to say the same number
// two dozen times. The count is asserted against the RAW frame, because a
// decoded one cannot tell the difference.
//
// And it costs no query, which is the rule the whole of world.go is shaped by:
// the tick renders from the cache a hello filled. Asserted by counting reads
// across a couple of minutes of broadcasts at the real rate, because a tick that
// quietly re-read would produce identical frames and be invisible everywhere
// else.
func TestTheFrameCarriesTheStoreOnceAndAsksNobodyWhatIsLeft(t *testing.T) {
	crate := mustObjectKind(t, KindCrate)
	relief := leavingKind(t, ActionRelieve)

	for _, tc := range []struct {
		name    string
		objects []WorldObject
		want    *Store
		why     string
	}{
		{
			name:    "a crate with beer in it",
			objects: []WorldObject{aCrate(t, 1, 3), anObject(2, relief.Key, Point{X: 0.2, Y: 0.2}, at(60))},
			want:    &Store{X: crate.At.X, Y: crate.At.Y, Left: 3},
			why:     "where it is and how much is in it are the two things a client needs to grey the button, and it holds no kind key to find either out for itself",
		},
		{
			name:    "a yard with nothing in it but a deposit",
			objects: []WorldObject{anObject(2, relief.Key, Point{X: 0.2, Y: 0.2}, at(60))},
			want:    nil,
			why:     "absent rather than empty: the draw that empties a crate stands up another in the same transaction, so nought is not a state a published frame can be in",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			repo.objects = tc.objects
			tr := &fakeTransport{}
			tr.setMembers(member("1"))
			svc := planeService(tr, repo)

			// The one human-paced moment allowed to read any of this.
			svc.load(context.Background(), testAccount)
			reads := repo.worldObjectReads()

			const ticks = 60
			for i := 1; i <= ticks; i++ {
				if err := svc.broadcast(context.Background(), at(float64(i)*0.2)); err != nil {
					t.Fatalf("broadcast %d: %v", i, err)
				}
			}

			frames := tr.frames()
			if len(frames) != ticks {
				t.Fatalf("%d frames were published across %d ticks; the ticks under test did not happen", len(frames), ticks)
			}
			for i, f := range frames {
				switch {
				case tc.want == nil && f.Store != nil:
					t.Fatalf("frame %d carries a store %+v; want none — %s", i, *f.Store, tc.why)
				case tc.want == nil:
				case f.Store == nil:
					t.Fatalf("frame %d carries no store at all; want %+v — %s", i, *tc.want, tc.why)
				case *f.Store != *tc.want:
					t.Fatalf("frame %d says the store is %+v; want %+v — %s", i, *f.Store, *tc.want, tc.why)
				}
			}
			for i, raw := range tr.rawFrames() {
				if n := strings.Count(string(raw), `"store"`); n > 1 {
					t.Fatalf("frame %d names the store %d times; it is one fact about the world and is carried once, or it is the entity count times over on every tick",
						i, n)
				}
			}
			if n := repo.worldObjectReads(); n != reads {
				t.Errorf("the world was read %d times across %d broadcasts; want the %d the hello made and not one more — what is left comes out of the cache",
					n, ticks, reads)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The key hunt as a SEARCH — «искать ключи» stops being a button and becomes a
// place you have to name and walk to.
//
// It supersedes the press-anywhere claim entirely, and the tests below are
// written to fail if any of it comes back. Three things changed at once and each
// one is load-bearing: the key is no longer DRAWN, so nobody can see where it is;
// the claim carries a SPOT, which is the first inbound payload the server has to
// judge rather than clamp; and the claim is refused unless he is actually
// STANDING in that spot, which makes this the second user of the arrival gate I9
// built rather than a mechanism of its own.
//
// The ordering of the checks is a security property rather than a style, and
// TestARefusalNeverSaysWhereTheKeyIs is the test that holds it: reach is decided
// before the answer is looked at, so a refusal cannot be used as an oracle.
// ---------------------------------------------------------------------------

// TestTheKeyIsNeverDrawnOnThePlane is the deletion this iteration is built on,
// asserted from the only side that can see it.
//
// The key used to be an ordinary entity in the roster, which meant every client
// was told exactly where it was five times a second and «искать ключи» was a race
// to press a button pointing at a visible dot. It is now absent from the frame
// entirely — not obscured, not sent with a flag, absent — so a hostile client
// reading every byte it will ever receive still does not know where the keys are.
//
// What must NOT disappear with it is the hunt's id: a hunt is STATE a late joiner
// has to be able to see, and a yard that stopped saying one was running would
// leave somebody who opened the app thirty seconds ago with nothing to join.
func TestTheKeyIsNeverDrawnOnThePlane(t *testing.T) {
	key := mustObjectKind(t, KindKey)
	if !key.Hidden {
		t.Fatalf("the catalogue no longer marks %q hidden; this test is about a rule the game does not have", key.Key)
	}
	relief := mustObjectKind(t, KindRelief)
	if relief.Hidden {
		t.Fatalf("the catalogue marks %q hidden too; this test needs a visible object to prove the frame is not simply empty", relief.Key)
	}

	spot := aHotspot(t, LocationYard, 0)
	lost := aLostKey(1, spot.At)
	repo := playedFor()
	repo.objects = []WorldObject{lost, anObject(2, relief.Key, Point{X: 0.2, Y: 0.2}, at(600))}
	repo.active = map[string]string{KindKey: lost.ID}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)
	svc.load(context.Background(), testAccount)

	if err := svc.broadcast(context.Background(), at(1)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	if len(frames) != 1 {
		t.Fatalf("%d frames were published for one tick", len(frames))
	}
	frame := frames[0]

	props := propsOf(frame)
	if len(props) != 1 {
		t.Fatalf("the frame carries %d objects; want the one deposit and NOT the key: %+v", len(props), props)
	}
	if props[0].Art == key.Art {
		t.Errorf("the frame draws the key: %+v — everybody can see where it is, and «искать ключи» is a race to press rather than a search", props[0])
	}
	if props[0].ID == "obj-"+shortID(lost.ID) {
		t.Errorf("the key is in the roster under its own id %q; hiding it means it is not an entity at all", props[0].ID)
	}
	// Nothing anywhere in the frame stands where the key is, which is the strong
	// form: not merely "no entity is labelled key" but "no coordinate leaks it".
	for _, p := range frame.Peers {
		if samePoint(Point{X: p.X, Y: p.Y}, spot.At) {
			t.Errorf("entity %q is standing exactly where the key is hidden, (%v,%v); a client that noticed would have the answer",
				p.ID, p.X, p.Y)
		}
	}

	// And the hunt is still announced as state, because a late joiner has to be
	// able to take part in one that is already running.
	if frame.Hunt != shortID(lost.ID) {
		t.Errorf("the frame names the hunt %q; want %q — hiding the key must not hide that a hunt is running, which is state a late joiner needs",
			frame.Hunt, shortID(lost.ID))
	}
}

// TestASearchIsRefusedUnlessHeNamedAPlaceAndWalkedToIt is the whole of the gate,
// in every way it says no, and it is also the deletion test for the
// press-anywhere claim: the first case below is exactly the frame I8c accepted.
//
// Each refusal asks the player for a different thing — name a place, walk to it,
// try somewhere else — which is why there are three sentences rather than one.
// And every one of them WRITES NOTHING: no claim is attempted, no event is
// appended, no stat is re-stamped. That last part matters more than it looks,
// because a re-stamp on a refused batch is silently erased damage rather than a
// cosmetic blemish.
func TestASearchIsRefusedUnlessHeNamedAPlaceAndWalkedToIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spot  func(t *testing.T) string
		stand func(t *testing.T, svc *Service, hidden Hotspot)
		want  error
		why   string
	}{
		{
			name:  "no place named at all",
			spot:  func(*testing.T) string { return "" },
			stand: func(_ *testing.T, svc *Service, hidden Hotspot) { standAt(svc, testAccount, hidden.At) },
			want:  ErrNoSpot,
			why:   "THE PRESS-ANYWHERE CLAIM, exactly as it used to arrive — a bare {\"verbs\":[\"claim\"]} — and it must not work even from on top of the key",
		},
		{
			name:  "a place no location has",
			spot:  func(*testing.T) string { return "под-луной" },
			stand: func(_ *testing.T, svc *Service, hidden Hotspot) { standAt(svc, testAccount, hidden.At) },
			want:  ErrNoSpot,
			why:   "an arbitrary string off the wire buys nothing: it is resolved against the catalogue before anything else happens",
		},
		{
			name: "a place in another location",
			spot: func(t *testing.T) string {
				// Composed rather than written down, so this stays a test about
				// the location predicate rather than about a name.
				return aHotspot(t, LocationYard, 0).Key + "-в-лесу"
			},
			stand: func(_ *testing.T, svc *Service, hidden Hotspot) { standAt(svc, testAccount, hidden.At) },
			want:  ErrNoSpot,
			why:   "the lookup is by (location, spot) from the start, which is what the four locations after this one reuse",
		},
		{
			name: "the right place, from across the yard",
			spot: func(t *testing.T) string { return aHotspot(t, LocationYard, 0).Key },
			stand: func(t *testing.T, svc *Service, _ Hotspot) {
				standAt(svc, testAccount, aHotspot(t, LocationYard, 1).At)
			},
			want: ErrTooFar,
			why:  "the client announcing an arrival is a REQUEST, never a fact; the server measures its own placement",
		},
		{
			name: "a place he is standing in, with nothing in it",
			spot: func(t *testing.T) string { return aHotspot(t, LocationYard, 1).Key },
			stand: func(t *testing.T, svc *Service, _ Hotspot) {
				standAt(svc, testAccount, aHotspot(t, LocationYard, 1).At)
			},
			want: ErrNothingHere,
			why:  "he asked properly and looked properly; this is the only refusal of the three that is a move in the game rather than a correction",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			pool := &txPool{}
			svc := NewService(&fakeTransport{}, testRoom, pool, repo, nil)
			if _, err := svc.State(context.Background(), testAccount); err != nil {
				t.Fatalf("State: %v", err)
			}
			before := append([]StatRow(nil), repo.rows...)
			if len(before) == 0 {
				t.Fatal("the pet has no stat rows at all; a rollback of nothing would pass this test whatever the service did")
			}

			// The key is in the FIRST hotspot in every case, and the fake would
			// hand it to anybody who got as far as asking — so every refusal below
			// is the gate's and not the database's.
			hidden := aHotspot(t, LocationYard, 0)
			theKeyIsIn(t, svc, repo, hidden)
			repo.claimWon = true
			tc.stand(t, svc, hidden)

			_, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, tc.spot(t), at(0))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Do(%q, spot=%q) = %v; want %v — %s", ActionClaim, tc.spot(t), err, tc.want, tc.why)
			}

			// AND NOTHING HAPPENED. The gate runs before the transaction, so this
			// is stronger than a rollback: there was never anything to take back.
			if n := len(repo.attempts()); n != 0 {
				t.Errorf("%d claims reached the database on a refused search; the gate decides before anything is contested: %+v", n, repo.attempts())
			}
			if n := len(pool.begun); n != 0 {
				t.Errorf("%d transactions were begun for a search that was refused before one was needed", n)
			}
			if n := len(repo.appended); n != 0 {
				t.Errorf("%d events survived a refused search; looking in the wrong place is not something that happened to the pet: %+v", n, repo.appended)
			}
			if n := len(repo.insertedObjects()); n != 0 {
				t.Errorf("%d world objects were written by a refused search; only a winner starts the next hunt: %+v", n, repo.insertedObjects())
			}
			if len(repo.rows) != len(before) {
				t.Fatalf("the pet has %d stat rows after a refused search; want the %d it had before", len(repo.rows), len(before))
			}
			for _, want := range before {
				got, ok := repo.row(want.Key)
				if !ok {
					t.Errorf("%q has no row after a refused search", want.Key)
					continue
				}
				if got.Value != want.Value || !got.AsOf.Equal(want.AsOf) {
					t.Errorf("%q is (%v, %s) after a refused search; want the (%v, %s) it was before — a refusal charged him a re-stamp, which is silently erased damage",
						want.Key, got.Value, got.AsOf.UTC(), want.Value, want.AsOf.UTC())
				}
			}
		})
	}
}

// TestSearchingTheRightPlaceFindsTheKeysAndTheWrongOneDoesNot is the pair, driven
// against one fixture so the only difference between them is where he looked.
//
// A test of the winning half alone would pass on a service that ignored the spot
// entirely, which is exactly the regression this iteration exists to prevent.
func TestSearchingTheRightPlaceFindsTheKeysAndTheWrongOneDoesNot(t *testing.T) {
	tally := effectOn(mustAction(t, ActionClaim), StatKeysFound)
	if tally <= 0 {
		t.Fatalf("the catalogue says %q moves %q by %v; there would be nothing for a winner to gain", ActionClaim, StatKeysFound, tally)
	}
	right, wrong := aHotspot(t, LocationYard, 0), aHotspot(t, LocationYard, 1)

	for _, tc := range []struct {
		name string
		spot Hotspot
		wins bool
	}{
		{name: "where they are", spot: right, wins: true},
		{name: "where they are not", spot: wrong, wins: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			svc := NewService(&fakeTransport{}, testRoom, &txPool{}, repo, nil)
			if _, err := svc.State(context.Background(), testAccount); err != nil {
				t.Fatalf("State: %v", err)
			}
			found, ok := repo.row(StatKeysFound)
			if !ok {
				t.Fatalf("the pet has no %q row; the counter this verb moves has not been materialised", StatKeysFound)
			}

			theKeyIsIn(t, svc, repo, right)
			repo.claimWon = true
			// He walks to whichever one this case is about, so the ONLY variable
			// between the two runs is the place he searched.
			standAt(svc, testAccount, tc.spot.At)

			st, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, tc.spot.Key, at(0))
			if !tc.wins {
				if !errors.Is(err, ErrNothingHere) {
					t.Fatalf("Do(%q) in %q = %v; want ErrNothingHere — the keys are in %q", ActionClaim, tc.spot.Key, err, right.Key)
				}
				now, ok := repo.row(StatKeysFound)
				if !ok || now.Value != found.Value {
					t.Errorf("his %q went from %v to %v on a search that found nothing", StatKeysFound, found.Value, now.Value)
				}
				return
			}
			if err != nil {
				t.Fatalf("Do(%q) in %q, which is where they are: %v", ActionClaim, tc.spot.Key, err)
			}
			nearlyStat(t, statOf(t, st, StatKeysFound).Value, found.Value+tally, "the finder's "+StatKeysFound)
			if held := repo.heldBy(KindKey); held != testAccount {
				t.Errorf("the key is held by %q after he searched the place it was in; want %q", held, testAccount)
			}
		})
	}
}

// TestARefusalNeverSaysWhereTheKeyIs is the security property of the gate, and
// the reason its three checks are in the order they are in.
//
// Reach is decided BEFORE the answer is looked at. So a player standing in the
// middle of the yard, tapping every hotspot in turn, gets the identical refusal
// for all of them — including the one holding the key. Reverse those two checks
// and the refusal becomes an oracle: the spot that answered differently is the
// answer, and the whole game is one loop in a browser console.
//
// It is asserted as "every refusal is the SAME", which is the property that
// matters, rather than as "the right one says далековато" — a future third
// refusal that leaked would break this test whatever it was called.
func TestARefusalNeverSaysWhereTheKeyIs(t *testing.T) {
	yard, ok := LocationByKey(LocationYard)
	if !ok {
		t.Fatalf("the catalogue has no location %q", LocationYard)
	}
	if len(yard.Hotspots) < 2 {
		t.Fatalf("%q publishes %d hotspots; with one there is nothing to compare a refusal against", LocationYard, len(yard.Hotspots))
	}

	// Somewhere genuinely away from all of them, so no tap below is ever within
	// reach and the only thing that could distinguish the answers is the key.
	var far Point
	for _, candidate := range []Point{spawn, {X: 0.5, Y: 0.5}, {X: 0.5, Y: 0.42}} {
		ok := true
		for _, h := range yard.Hotspots {
			if distance(candidate, h.At) <= arriveWithin {
				ok = false
			}
		}
		if ok {
			far = candidate
			break
		}
	}
	if far == (Point{}) {
		t.Fatal("every candidate standing place is within arriving distance of some hotspot; there is nowhere to be too far from all of them")
	}

	for i := range yard.Hotspots {
		t.Run(fmt.Sprintf("the key is in hotspot %d", i), func(t *testing.T) {
			answers := map[string]string{}
			for _, asked := range yard.Hotspots {
				repo := playedFor()
				svc := NewService(&fakeTransport{}, testRoom, &txPool{}, repo, nil)
				theKeyIsIn(t, svc, repo, yard.Hotspots[i])
				repo.claimWon = true
				standAt(svc, testAccount, far)

				_, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, asked.Key, at(0))
				if err == nil {
					t.Fatalf("searching %q from across the yard was ALLOWED; the arrival gate is not being applied at all", asked.Key)
				}
				answers[asked.Key] = refusalLine(context.Background(), err)
			}
			var first string
			for _, line := range answers {
				if first == "" {
					first = line
					continue
				}
				if line != first {
					t.Fatalf("the yard answers %+v when the key is in %q; a refusal that varies with the answer IS the answer, and the game becomes one loop in a console",
						answers, yard.Hotspots[i].Key)
				}
			}
		})
	}
}

// TestOneCorrectSearchWinsOneKeyHoweverManyClaimsAreInTheFrame closes the hole a
// batch would otherwise open, and it is the case a hostile client reaches first.
//
// The gate reads the world CACHE, which is not refreshed inside a batch — so
// without the one-search-per-frame rule, `{"verbs":["claim",…×8],"spot":"куст"}`
// would pass the same cached key eight times off one correct search, and each
// claim after the first would take the REPLACEMENT the one before it stood up:
// a key hidden somewhere he has never looked. Eight keys for the price of one
// walk, and the log would record eight searches that genuinely happened.
//
// The second claim is refused as an empty place, which is exactly what it is —
// he already took what was there — and the refusal takes the whole batch with it,
// so a client trying this gets nothing rather than getting the first one free.
func TestOneCorrectSearchWinsOneKeyHoweverManyClaimsAreInTheFrame(t *testing.T) {
	spot := aHotspot(t, LocationYard, 0)
	repo := playedFor()
	pool := &txPool{}
	svc := NewService(&fakeTransport{}, testRoom, pool, repo, nil)
	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}
	found, ok := repo.row(StatKeysFound)
	if !ok {
		t.Fatalf("the pet has no %q row", StatKeysFound)
	}

	searchingIn(t, svc, repo, testAccount, spot)
	repo.claimWon = true

	batch := make([]string, maxBatch)
	for i := range batch {
		batch[i] = ActionClaim
	}
	if _, err := svc.Do(context.Background(), testAccount, batch, spot.Key, at(0)); !errors.Is(err, ErrNothingHere) {
		t.Fatalf("Do(%d × %q, one spot) = %v; want ErrNothingHere on the second — he already took what was in there",
			len(batch), ActionClaim, err)
	}

	if n := len(repo.attempts()); n != 0 {
		t.Errorf("%d claims reached the database; the batch is refused before the transaction, so none of them should have: %+v", n, repo.attempts())
	}
	if n := len(pool.begun); n != 0 {
		t.Errorf("%d transactions were begun for a batch refused before one was needed", n)
	}
	if now, ok := repo.row(StatKeysFound); !ok || now.Value != found.Value {
		t.Errorf("his %q went from %v to %v on a batch that was refused whole; a refused batch writes nothing at all", StatKeysFound, found.Value, now.Value)
	}

	// And the honest single search still works, from the same fixture — so the
	// rule above bounds the batch rather than breaking the verb.
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, spot.Key, at(1)); err != nil {
		t.Fatalf("one %q in the place it is in: %v; the batch rule must not cost the ordinary search", ActionClaim, err)
	}
}

// TestTheKeyThatReplacesAFoundOneIsHiddenAtAHotspotToo is the property that keeps
// the hunt going, and the one that would rot silently.
//
// The replacement is inserted inside the winner's own transaction, from
// `placeFor`. If it landed anywhere else on the plane — a random point, the
// origin, the spawn — the NEXT search could never be judged: no spot key would
// resolve to it, so every player would be told «тут пусто» everywhere, for ever,
// with a hunt id riding the frame the whole time saying one was running.
//
// IT IS CHECKED AGAINST THE LOCATION IT ACTUALLY WENT TO, which is the half the
// four locations added. A key names no location, so the replacement is hidden in
// one drawn at random — and the rule is that it is at a hotspot of THAT place,
// not of the one the finder was standing in. Checking it against двор would have
// been a test that passed one time in five for the wrong reason.
func TestTheKeyThatReplacesAFoundOneIsHiddenAtAHotspotToo(t *testing.T) {
	spot := aHotspot(t, LocationYard, 0)

	repo := playedFor()
	svc := NewService(&fakeTransport{}, testRoom, &txPool{}, repo, nil)
	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}
	searchingIn(t, svc, repo, testAccount, spot)
	repo.claimWon = true

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, spot.Key, at(0)); err != nil {
		t.Fatalf("Do(%q): %v", ActionClaim, err)
	}

	inserted := repo.insertedObjects()
	if len(inserted) != 1 {
		t.Fatalf("%d world objects were written by a winning claim; want the replacement key: %+v", len(inserted), inserted)
	}
	next := inserted[0]
	if next.kind != KindKey {
		t.Fatalf("the replacement is of kind %q; want %q", next.kind, KindKey)
	}
	where, ok := LocationByKey(next.locationKey)
	if !ok {
		t.Fatalf("the replacement was hidden in %q, which is not a location in the catalogue; nothing could ever be searched there", next.locationKey)
	}
	if _, ok := hotspotAtPoint(where, next.at); !ok {
		t.Fatalf("the replacement was hidden at (%v,%v), which is not any of %q's hotspots; no spot key would ever resolve to it and every later search would answer «тут пусто» for ever",
			next.at.X, next.at.Y, where.Key)
	}
}

// The beer store is carried as a STRUCTURE and never as an entity.
//
// It used to be both — an `obj-` entity in the roster AND the `store` block —
// which is the second path to one outcome CLAUDE.md forbids, and it is why the
// crate drew as a person-shaped circle: the client tells a thing from a person
// by the presence of `expires`, and a crate never expires. One representation is
// correcter, lets the client draw a shop with a count on it, and is about sixty
// bytes a frame per viewer cheaper.
func TestTheBeerStoreIsCarriedAsAStructureAndNotAsAnEntity(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)
	svc.load(context.Background(), accountOf("a"))
	atTheStore(t, svc, repo, accountOf("a"), 4)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	if len(frames) == 0 {
		t.Fatal("nothing was published")
	}
	frame := frames[len(frames)-1]

	// The block still says where it is and how much is in it — the client needs
	// both to draw the shop and to gate the button.
	if frame.Store == nil {
		t.Fatal("the frame carries no store at all, so the yard has no shop to draw")
	}
	if frame.Store.Left != 4 {
		t.Errorf("the store says %d left, want 4", frame.Store.Left)
	}
	kind := mustObjectKind(t, KindCrate)
	if kind.At == nil {
		t.Fatal("the crate has no pitch in the catalogue")
	}
	if frame.Store.X != kind.At.X || frame.Store.Y != kind.At.Y {
		t.Errorf("the store is at %v,%v, want the catalogue's %v", frame.Store.X, frame.Store.Y, *kind.At)
	}

	// And nothing is drawn for it. The crate is the only thing in the world here,
	// so any object entity at all is the duplicate this test exists to forbid.
	if drawn := propsOf(frame); len(drawn) != 0 {
		t.Errorf("the crate is still drawn as an entity as well as published as a store: %+v", drawn)
	}
}

// ---------------------------------------------------------------------------
// The four locations. One key in the world, five places to look for it.
// ---------------------------------------------------------------------------

// TestTheKeyIsHiddenSomewhereAndTheOtherPlacesAreEmpty is the whole of what the
// four locations changed about the hunt.
//
// One key exists in the world — the partial unique index is on `kind` alone — so
// four of the five places are holding nothing at all, and a player standing in
// one of those four gets «тут пусто» however correctly he searches. That is the
// game rather than a failure: there is no way to be told where the key is, so
// walking somewhere else and looking again is the only move.
//
// THE REFUSAL IS THE SAME ONE A WRONG HOTSPOT GETS, and that is the security
// property rather than a tidiness one. Anything that distinguished «wrong spot»
// from «wrong location» would narrow five places to one for the price of a tap,
// which is the entire answer given away by a refusal.
func TestTheKeyIsHiddenSomewhereAndTheOtherPlacesAreEmpty(t *testing.T) {
	here := aHotspot(t, LocationYard, 0)
	there := aHotspot(t, LocationKusty, 0)

	repo := playedFor()
	svc := NewService(&fakeTransport{}, testRoom, &txPool{}, repo, nil)
	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}

	// The key is in кусты, and he is standing at the matching spot — in the yard,
	// where his pet still is. Same spot index, same coordinates, different place.
	lost := aLostKey(7, there.At)
	lost.LocationKey = LocationKusty
	repo.objects = append(repo.objects, lost)
	svc.mu.Lock()
	svc.world = append(svc.world, lost)
	svc.mu.Unlock()
	standAt(svc, testAccount, here.At)

	_, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, here.Key, at(0))
	if !errors.Is(err, ErrNothingHere) {
		t.Fatalf("searching %q in the yard while the key is in %q = %v; want %v — a key in another location must not be findable from this one, and the refusal must be the ordinary empty-place one",
			here.Key, LocationKusty, err, ErrNothingHere)
	}
	if got := repo.attempts(); len(got) != 0 {
		t.Errorf("a search in the wrong location reached the database %d times: %+v; the gate is decided in memory and a refused batch writes nothing at all",
			len(got), got)
	}

	// And the same search, once he is actually in кусты, finds them. Nothing else
	// about the fixture changes — same spot key, same coordinates, same hotspot
	// index — so the location is the only thing under test.
	repo.pet.LocationKey = LocationKusty
	repo.claimWon = true
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, there.Key, at(1)); err != nil {
		t.Fatalf("searching %q from inside %q: %v; the identical search must succeed once his pet is in the location holding the key",
			there.Key, LocationKusty, err)
	}
}

// TestAFreshKeyGoesAnywhereAndTheCrateNeverLeavesTheYard is the two answers
// `locationFor` gives, and the reason the field has two of them.
//
// A kind naming no location is scattered: hidden in a place drawn with
// crypto/rand each time, so finding one tells you nothing about where the next
// will be. A kind naming one is pinned: the beer store is in двор every time it
// is stood up, because a shop that wandered between five places would be a
// second key hunt wearing an apron and no player could ever be told to walk to
// it.
//
// The spread is asserted rather than the distribution. A hundred draws over five
// locations lands in every one of them with overwhelming probability, and a
// tighter claim would be a statistical test in a suite that has to be green every
// time.
func TestAFreshKeyGoesAnywhereAndTheCrateNeverLeavesTheYard(t *testing.T) {
	key := mustObjectKind(t, KindKey)
	if key.Location != "" {
		t.Fatalf("the catalogue now pins %q to %q; this test is about a kind that may be anywhere", key.Key, key.Location)
	}
	crate := mustObjectKind(t, KindCrate)
	if crate.Location == "" {
		t.Fatalf("the catalogue no longer pins %q anywhere; the store would move and the walk to it would be a matter of luck", crate.Key)
	}

	seen := map[string]int{}
	const draws = 100
	for i := 0; i < draws; i++ {
		where := locationFor(key)
		if _, ok := LocationByKey(where); !ok {
			t.Fatalf("a fresh %q was sent to %q, which is not a location in the catalogue; nothing could ever be searched there", key.Key, where)
		}
		seen[where]++
		if got := locationFor(crate); got != crate.Location {
			t.Fatalf("a fresh %q was sent to %q; want the catalogue's %q every single time", crate.Key, got, crate.Location)
		}
	}
	if len(seen) != len(Content().Locations) {
		t.Errorf("%d draws reached %d of the %d locations (%v); a place the key can never be hidden in is a place nobody would ever have a reason to visit",
			draws, len(seen), len(Content().Locations), seen)
	}
}

// TestThingsInOtherLocationsStillRideTheFrameCarryingTheirOwnPlace.
//
// Locations are NOT realtime rooms, so there is one frame for the whole world and
// the client draws the entities whose `loc` matches its own. Everything therefore
// has to be on it, and everything has to say where it is — a prop that arrived
// without a location would be drawn in every location at once, which for a relief
// deposit means the same куча in five places.
//
// THE DEFAULT IS OMITTED AND THAT IS THE POINT OF THE FIELD'S COST. `loc` is
// eighteen bytes on an entity, on a frame sent five times a second forever, so
// paying it for the yard — where a pet is created, the store stands and the
// sleepers lie — would be paying it for nearly every entity of nearly every
// frame. This asserts both halves at once: absent in двор, present anywhere else.
func TestThingsInOtherLocationsStillRideTheFrameCarryingTheirOwnPlace(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	repo := playedFor()
	repo.objects = []WorldObject{
		anObject(1, kind.Key, Point{X: 0.2, Y: 0.3}, epoch.Add(time.Hour)),
		anObjectIn(2, kind.Key, LocationLes, Point{X: 0.4, Y: 0.5}, epoch.Add(time.Hour)),
		anObjectIn(3, kind.Key, LocationZabroshka, Point{X: 0.6, Y: 0.7}, epoch.Add(time.Hour)),
	}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	svc.load(context.Background(), testAccount)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	props := propsOf(tr.frames()[0])
	if len(props) != len(repo.objects) {
		t.Fatalf("%d objects were drawn out of the %d standing in three locations; want all of them — one frame carries the whole world",
			len(props), len(repo.objects))
	}
	got := map[string]bool{}
	for _, p := range props {
		got[p.Loc] = true
	}
	for _, want := range []string{"", LocationLes, LocationZabroshka} {
		if !got[want] {
			t.Errorf("no entity on the frame carries loc=%q; the frame carries %v — a prop with the wrong location is drawn in the wrong place, and one with none is drawn in all five",
				want, got)
		}
	}
	// The yard's own is ABSENT rather than spelled out, which is the eighteen
	// bytes per entity per tick per viewer this field is allowed to cost.
	for _, p := range props {
		if p.Loc == LocationYard {
			t.Errorf("an entity in the yard published loc=%q; the default is omitted, and it is on nearly every entity of nearly every frame", p.Loc)
		}
	}
}

// TestTheStoreIsCarriedWithTheLocationItStandsIn.
//
// One frame goes to the whole world, so «where the shop is» stopped being a pair
// of coordinates and became a place plus a pair of coordinates. Without the
// place, a Ваня in лес would be shown a store at (0.82, 0.22) of лес — a shop
// that is not there, inviting him to walk to a crate he cannot see and be refused
// when he presses. The client compares it against its own location and draws
// nothing when they differ; the server refuses regardless, because a greyed
// button is a suggestion.
func TestTheStoreIsCarriedWithTheLocationItStandsIn(t *testing.T) {
	repo := playedFor()
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)
	crateInTheYard(t, svc, repo, crateStock)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	store := tr.frames()[0].Store
	if store == nil {
		t.Fatal("the frame carries no store at all while a crate is standing in the yard")
	}
	// Omitted, because the crate is pinned to the default location — so the field
	// costs nothing today and is what keeps it free to move the shop.
	if store.Loc != "" {
		t.Errorf("the store publishes loc=%q while it stands in the default location; the whole convention is that the common case is absent", store.Loc)
	}

	// And a crate somewhere else says so, which is the case the field exists for.
	elsewhere := aCrate(t, 43, crateStock)
	elsewhere.LocationKey = LocationZabroshka
	svc.mu.Lock()
	svc.world = []WorldObject{elsewhere}
	svc.mu.Unlock()
	if err := svc.broadcast(context.Background(), at(1)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	moved := tr.frames()[1].Store
	if moved == nil {
		t.Fatal("the frame carries no store while a crate is standing in заброшка")
	}
	if moved.Loc != LocationZabroshka {
		t.Errorf("the store publishes loc=%q while its crate stands in %q; a client looking at the yard would draw a shop that is not there",
			moved.Loc, LocationZabroshka)
	}
}

// TestBeerCannotBeDrunkFromAnotherLocation is the arrival gate's second
// dimension, and it is a rule that would have been broken in SILENCE.
//
// Coordinates are normalised per location, so a Ваня standing at (0.82, 0.22) of
// лес is at the crate's exact pitch without being anywhere near the crate.
// Measuring distance alone would let him drink from a shop four places away —
// and the frame would agree, because it publishes one store for everybody.
func TestBeerCannotBeDrunkFromAnotherLocation(t *testing.T) {
	drink := mustAction(t, ActionDrink)
	if drink.NeedsNear == "" {
		t.Fatalf("the catalogue no longer gates %q on standing beside anything; this test is about a rule the game does not have", drink.Key)
	}
	repo := playedFor()
	svc := NewService(&fakeTransport{}, testRoom, &txPool{}, repo, nil)
	if _, err := svc.State(context.Background(), testAccount); err != nil {
		t.Fatalf("State: %v", err)
	}
	atTheStore(t, svc, repo, testAccount, crateStock)

	// From the yard, standing on the crate, it works — the fixture is honest.
	if _, err := svc.Do(context.Background(), testAccount, []string{drink.Key}, "", at(0)); err != nil {
		t.Fatalf("Do(%q) beside the crate in the yard: %v", drink.Key, err)
	}

	// He goes to лес and stands at the identical coordinates. Nothing about his
	// position changes; only the place he is in does.
	repo.pet.LocationKey = LocationLes
	_, err := svc.Do(context.Background(), testAccount, []string{drink.Key}, "", at(1))
	if !errors.Is(err, ErrTooFar) {
		t.Fatalf("Do(%q) from %q while standing on the yard's crate = %v; want %v — a crate in another location is not something you are beside, however close the two coordinates are",
			drink.Key, LocationLes, err, ErrTooFar)
	}
	if got := repo.drawn(); len(got) != 1 {
		t.Errorf("the crate was drawn from %d times; want exactly the one honest drink — a refused batch takes no beer at all", len(got))
	}
}

// TestTheSadFacesStayInTheLocationTheKeyWasFoundIn is the second half of the
// loser effect, and it is a rule the four locations created rather than one they
// merely extended.
//
// The room is the WHOLE WORLD — locations are deliberately not rooms — so the
// members list a claim reacts to spans every place at once. Unfiltered, a Ваня
// standing in лифт would go grey for four seconds because of something that
// happened in заброшка: he could not see the hotspot, could not see the winner,
// may not have looked for a key all evening, and would be given no explanation at
// all. A cosmetic effect with no local cause does not read as losing a race, it
// reads as the game being broken.
//
// The winner is unconditional, because there is one of him and the search gate
// proved where he was standing before any of this ran.
func TestTheSadFacesStayInTheLocationTheKeyWasFoundIn(t *testing.T) {
	winner, neighbour, faraway := member("1"), member("2"), member("3")
	if winner.AccountID != testAccount {
		t.Fatalf("the winner's connection belongs to %q and the fake repository holds the pet of %q; the verb below would act for somebody else",
			winner.AccountID, testAccount)
	}
	lift, ok := LocationByKey(LocationLift)
	if !ok {
		t.Fatalf("the catalogue has no location %q", LocationLift)
	}

	repo := playedFor()
	tr := &fakeTransport{}
	tr.setMembers(winner, neighbour, faraway)
	svc := planeService(tr, repo)

	// One tick, so all three have a placement — a mood is hung on one, and
	// somebody the yard has never placed has nowhere to wear a face.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	// And the third one leaves for лифт, through the production path so the
	// fixture cannot disagree with what a real goto does.
	svc.arrive(faraway.AccountID, lift)

	repo.claimWon = true
	spot := searchingIn(t, svc, repo, testAccount, aHotspot(t, LocationYard, 0))
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionClaim}, spot, at(0)); err != nil {
		t.Fatalf("Do(%q): %v", ActionClaim, err)
	}

	if err := svc.broadcast(context.Background(), at(0).Add(moodFor/2)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	f := frames[len(frames)-1]
	for _, tc := range []struct {
		who  realtime.Member
		want string
		why  string
	}{
		{who: winner, want: PoseHappy, why: "he found them, and he is in that location by construction"},
		{who: neighbour, want: PoseSad, why: "he was standing in the yard when somebody there won the race"},
		{
			who: faraway, want: PoseFine,
			why: "he is in another location entirely: he could not see the hotspot, could not see the winner, " +
				"and a face with no local cause reads as a broken game rather than as a lost race",
		},
	} {
		p, ok := peerOf(svc, f, tc.who.AccountID)
		if !ok {
			t.Fatalf("%s is not in the frame at all: %+v", tc.who.ConnID, f)
		}
		if p.Pose != tc.want {
			t.Errorf("%s is drawn %q after the claim; want %q — %s", tc.who.ConnID, p.Pose, tc.want, tc.why)
		}
	}
}
