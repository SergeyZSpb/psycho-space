package gamevanyagotchi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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
	when := expires
	return WorldObject{
		ID:        fmt.Sprintf("%012d-4a1e-b0c2-%012d", n, n),
		Kind:      kind,
		At:        at,
		ExpiresAt: &when,
	}
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
	repo := playedFor()
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
	svc.HandleInbound(context.Background(), member("1"), testRoom,
		fmt.Appendf(nil, `{"t":%q,"verbs":[%q],"x":0.05,"y":0.95}`, TypeDo, ActionRelieve))

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
	if want := arrived.Add(kind.Lifetime); !left.expires.Equal(want) {
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
	repo := playedFor()
	svc := planeService(&fakeTransport{}, repo)

	if _, err := svc.Do(context.Background(), testAccount, []string{ActionRelieve}, at(0)); err != nil {
		t.Fatalf("Do(%s): %v", ActionRelieve, err)
	}

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
	repo := playedFor()
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
	_, err := svc.Do(context.Background(), testAccount, []string{ActionRelieve}, at(0))
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

// TestAVerbThatLeavesNothingWritesNothingToTheWorld is the other side of the
// catalogue lookup.
//
// Exactly one verb carries `Leaves` today, and the way that stays true is that
// the service reads the field rather than switching on a key — so a verb without
// one has to write nothing and, just as importantly, has to leave the world
// cache alone. A drink that refreshed it would be a database read on every
// button press for a table it did not touch.
func TestAVerbThatLeavesNothingWritesNothingToTheWorld(t *testing.T) {
	for _, verb := range []string{ActionDrink, ActionRevive} {
		if a := mustAction(t, verb); a.Leaves != "" {
			t.Fatalf("the catalogue now says %q leaves %q behind; this test is asserting the opposite of the content", verb, a.Leaves)
		}
	}

	repo := playedFor()
	svc := planeService(&fakeTransport{}, repo)

	for i, verb := range []string{ActionDrink, ActionRevive} {
		if _, err := svc.Do(context.Background(), testAccount, []string{verb}, at(float64(i))); err != nil {
			t.Fatalf("Do(%s): %v", verb, err)
		}
	}

	if len(repo.appended) != 2 {
		t.Fatalf("%d events were recorded for two verbs; neither applied, so this test says nothing about what they leave behind", len(repo.appended))
	}
	if got := repo.insertedObjects(); len(got) != 0 {
		t.Errorf("%d objects were left behind by verbs the catalogue says leave nothing: %+v", len(got), got)
	}
	if n := repo.worldObjectReads(); n != 0 {
		t.Errorf("the world was read %d times by verbs that cannot have changed it; want none", n)
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
	if strewn.Here != 1 {
		t.Errorf("the frame says %d are in the yard while one person stands among three deposits; want 1", strewn.Here)
	}
	if strewn.Here != bare.Here {
		t.Errorf("the head count went from %d to %d because three things were left on the ground", bare.Here, strewn.Here)
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
	repo := playedFor()
	repo.objects = []WorldObject{anObject(1, kind.Key, Point{X: 0.5, Y: 0.5}, at(60))}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	// The two moments that ARE allowed to read: a hello, and a verb that left
	// something behind.
	svc.load(context.Background(), testAccount)
	if _, err := svc.Do(context.Background(), testAccount, []string{ActionRelieve}, at(0)); err != nil {
		t.Fatalf("Do(%s): %v", ActionRelieve, err)
	}
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
// tidiness one.
//
// Every live object is an entity in a frame sent five times a second to every
// viewer, so the cost is bytes × rate × objects × viewers and the only term this
// game controls is the third. The cap is applied where the rows are fetched — so
// the bound holds however enthusiastically the yard behaves, and holds without
// the render path having to remember to enforce it.
func TestWorldLimitBoundsWhatTheFrameCarries(t *testing.T) {
	kind := leavingKind(t, ActionRelieve)
	repo := playedFor()
	for i := 0; i < worldLimit+8; i++ {
		// Spread across the plane, and each with an id of its own: two objects
		// sharing the first twelve characters would collapse into one entity on
		// the wire and make this count wrong for the wrong reason.
		at := Point{X: float64(i%8) / 8, Y: float64(i%5) / 5}
		repo.objects = append(repo.objects, anObject(i, kind.Key, at, epoch.Add(time.Hour)))
	}
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	svc.load(context.Background(), testAccount)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	if got := repo.worldObjectLimits(); len(got) != 1 || got[0] != worldLimit {
		t.Errorf("the world was read with limits %v; want exactly one read capped at worldLimit (%d)", got, worldLimit)
	}
	if n := len(propsOf(tr.frames()[0])); n != worldLimit {
		t.Errorf("%d objects were drawn out of the %d standing in the yard; want no more than worldLimit (%d)",
			n, len(repo.objects), worldLimit)
	}
}
