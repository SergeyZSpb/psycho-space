package gamevanyagotchi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeTransport stands in for the hub. Every property of the broadcast worth
// asserting is reachable without a socket, which is what keeps these tests
// deterministic — the tick is a channel the test fires, so there is never a
// "wait for the next broadcast" sleep anywhere in this file.
type fakeTransport struct {
	mu         sync.Mutex
	members    []realtime.Member
	published  [][]byte
	unicast    []unicast
	membersErr error
	publishErr error
}

// unicast is one PublishTo call: which connection, and what it was sent.
type unicast struct {
	connID string
	msg    []byte
}

// epoch is the arbitrary instant the broadcast clock starts at in these tests.
// Fixed rather than time.Now() so a failure reads the same on every run, and far
// enough from the zero time that subtracting a grace period from it is still a
// sane timestamp.
var epoch = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

// at is the timestamp a broadcast is driven with, `mins` minutes after the
// epoch. Every test that does not care about the grace period passes at(0) and
// the position map simply never expires anything.
func at(mins float64) time.Time {
	return epoch.Add(time.Duration(mins * float64(time.Minute)))
}

func (f *fakeTransport) setMembers(ms ...realtime.Member) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members = ms
}

func (f *fakeTransport) Members(_ context.Context, _ string) ([]realtime.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.membersErr != nil {
		return nil, f.membersErr
	}
	return append([]realtime.Member(nil), f.members...), nil
}

func (f *fakeTransport) Publish(_ context.Context, _ string, msg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, append([]byte(nil), msg...))
	return nil
}

func (f *fakeTransport) PublishTo(_ context.Context, connID string, msg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.unicast = append(f.unicast, unicast{connID: connID, msg: append([]byte(nil), msg...)})
	return nil
}

// unicasts returns every PublishTo the service has made.
func (f *fakeTransport) unicasts() []unicast {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]unicast(nil), f.unicast...)
}

// youFor decodes the single "you" frame sent to one connection, and reports
// whether exactly one was sent to it.
func youFor(t *testing.T, tr *fakeTransport, connID string) (You, bool) {
	t.Helper()
	var got You
	n := 0
	for _, u := range tr.unicasts() {
		if u.connID != connID {
			continue
		}
		var y You
		if err := json.Unmarshal(u.msg, &y); err != nil || y.T != TypeYou {
			continue
		}
		got, n = y, n+1
	}
	return got, n == 1
}

// statesFor decodes every `vanyagotchi_state` frame sent to one connection.
//
// A list rather than youFor's exactly-one, because the two answers are counted
// differently: a hello is answered once, and a pet is pushed every time it
// changes.
func statesFor(t *testing.T, tr *fakeTransport, connID string) []State {
	t.Helper()
	var out []State
	for _, u := range tr.unicasts() {
		if u.connID != connID {
			continue
		}
		var f StateFrame
		if err := json.Unmarshal(u.msg, &f); err != nil || f.T != TypeStateFrame {
			continue
		}
		out = append(out, f.State)
	}
	return out
}

// rawFrames is every published frame as it went on the wire.
//
// Decoding loses the one thing a couple of assertions are about: how many TIMES
// something appears in a frame. A Roster with one Store and a Roster whose every
// peer also carried one decode identically, and the difference between them is
// kilobytes a second to somebody's phone.
func (f *fakeTransport) rawFrames() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.published...)
}

func (f *fakeTransport) frames() []Roster {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Roster, 0, len(f.published))
	for _, raw := range f.published {
		var r Roster
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// member is one connection belonging to an account of its own — the common case
// of one person on one device.
func member(id string) realtime.Member {
	return realtime.Member{ConnID: id, AccountID: accountOf(id)}
}

// accountOf is the account id "member" invents for a connection id.
func accountOf(connID string) string { return "acct-" + connID }

// conn is a further connection of an account that may already be present: the
// second device, which the roster has to collapse into one entity.
func conn(connID, accountID string) realtime.Member {
	return realtime.Member{ConnID: connID, AccountID: accountID}
}

// peerByID finds one entity in a frame by the id that was actually published.
func peerByID(r Roster, id string) (Peer, bool) {
	for _, p := range r.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return Peer{}, false
}

// peerOf finds the entity an account is drawn as. Nothing in a frame says
// "acct-a" — that is the whole point of the pseudonym — so a test has to derive
// the published handle the same way the server does.
func peerOf(svc *Service, r Roster, accountID string) (Peer, bool) {
	return peerByID(r, svc.pseudonym(accountID))
}

// inTheYard is how many people the frame says are standing in двор.
//
// `Here` became a map when the four locations arrived, and nearly every test in
// this package is about the yard because that is where a pet is created — so
// this reads the one entry those tests mean and leaves `r.Here` itself for the
// handful that are about several places at once. A location nobody is standing
// in has no entry at all, and a map answers nought for one, which is exactly
// what "nobody is in the yard" should read as.
func inTheYard(r Roster) int { return r.Here[LocationYard] }

const testRoom = "yard"

// ---------------------------------------------------------------------------
// A tap is a journey now, and that is what the helpers below are for.
//
// Three things follow from it. A position is no longer the point the tap named —
// it is where he has got to by the instant the frame is drawn — so a test has to
// say WHEN it is looking, and derive that instant from the walk rather than
// writing one down. A walk lands on its destination by interpolation, from +
// (to − from) × 1, which IEEE-754 does not promise is bit-identical to `to`, so
// positions are compared with a tolerance rather than with ==. And an ambitious
// tap may be refused outright, which is the subject of exactly one test and a
// flake in every other, so the rest keep their journeys short.
//
// The clock stays the plain fixed epoch above, and that works because the game
// has exactly ONE clock: a tap is stamped with the last tick's instant (see
// Service.lastTick), never with time.Now, so a test that owns the ticks owns the
// walks too. The one consequence worth remembering is that a tap arriving before
// any tick has no instant to be measured from and is applied as a teleport.
// ---------------------------------------------------------------------------

// longestWalk is the longest journey the plane allows: corner to corner at
// walkSpeed. A tick this far past a tap is past the end of whatever walk that
// tap started, whatever it asked for and whether or not he finished it.
var longestWalk = time.Duration(maxDistance / walkSpeed * float64(time.Second))

// afterArriving is an instant by which the journey w names is certainly over.
func afterArriving(w walk) time.Time { return w.startedAt.Add(longestWalk + time.Second) }

// atProgress is the instant at which w has covered the given fraction of its
// length. Derived from the distance and walkSpeed rather than from the walk's own
// arithmetic, so an expectation is not computed by the thing it is checking.
func atProgress(w walk, f float64) time.Time {
	return w.startedAt.Add(time.Duration(distance(w.from, w.to) * f / walkSpeed * float64(time.Second)))
}

// partWayThrough is the instant w is halfway along, in time and therefore in
// space.
func partWayThrough(w walk) time.Time { return atProgress(w, 0.5) }

// walkOf reads the journey an account is currently on, failing if the plane has
// never heard of it.
func walkOf(t *testing.T, svc *Service, accountID string) walk {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	p, ok := svc.pos[accountID]
	if !ok {
		t.Fatalf("the plane holds no placement for %q", accountID)
	}
	return p.walk
}

// tap moves an account the way a client does — an inbound frame on its own
// connection — and hands back the journey it started.
//
// It reads the walk back out of the map rather than constructing one, because
// both of the things a caller needs are decided inside the service and cannot be
// predicted from outside: where the journey STARTS (a tap retargets from the
// current interpolated position, not from the last destination) and whether he
// manages it at all.
//
// The second is why this insists on a short hop. planWalk decides tiredness at
// accept time by hashing (account, destination, instant) against a key
// crypto/rand mints per process, so a test cannot predict the outcome of an
// ambitious tap — and one that asserted arrival on one would fail a run in twenty
// for a reason that has nothing to do with what it was testing. A journey no
// longer than tiredFrom is never refused. A caller who asks for a longer one
// fails here, legibly, instead of flaking somewhere downstream.
func tap(t *testing.T, svc *Service, m realtime.Member, to Point) walk {
	t.Helper()
	svc.HandleInbound(context.Background(), m, testRoom,
		fmt.Appendf(nil, `{"t":%q,"x":%v,"y":%v}`, TypeMove, to.X, to.Y))
	w := walkOf(t, svc, m.AccountID)
	if w.to != to {
		t.Fatalf("the tap asked for (%v,%v) but the journey is heading for (%v,%v); the frame was rejected",
			to.X, to.Y, w.to.X, w.to.Y)
	}
	if d := distance(w.from, w.to); d > tiredFrom {
		t.Fatalf("this tap is %v across, past the %v at which he may sit down and give up: pick a nearer point, or the test flakes on the run where he does",
			d, tiredFrom)
	}
	if w.stopAt != 1 {
		t.Fatalf("a hop of %v was refused (stopAt %v); tiredFrom no longer means what this helper assumes",
			distance(w.from, w.to), w.stopAt)
	}
	return w
}

// tapUntilHeGivesUp taps the same ambitious destination until the server refuses
// one, and hands back the journey he sat down in the middle of.
//
// The roll cannot be predicted from outside — that is the point of it: planWalk
// hashes (account, destination, instant) against a per-process key, so nobody,
// client or test, can tell in advance which tap will be refused. What a test can
// do is ask repeatedly, since every tick carries an instant this service has not
// hashed before, and across the yard the chance of a refusal is high enough that
// a few hundred taps without one is not a flake anybody will ever see. It is also
// exactly what a player does when his Ваня sits down: he taps again.
func tapUntilHeGivesUp(t *testing.T, svc *Service, m realtime.Member, to Point) walk {
	t.Helper()
	const attempts = 400
	for i := 0; i < attempts; i++ {
		// A tick first: it is what gives the next tap an instant, and a different
		// one each time round.
		if err := svc.broadcast(context.Background(), at(0).Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		svc.HandleInbound(context.Background(), m, testRoom,
			fmt.Appendf(nil, `{"t":%q,"x":%v,"y":%v}`, TypeMove, to.X, to.Y))
		w := walkOf(t, svc, m.AccountID)
		if d := distance(w.from, w.to); d <= tiredFrom {
			t.Fatalf("this journey is %v across and can never be refused at all; a loop waiting for a refusal on it would never end", d)
		}
		if w.stopAt < 1 {
			return w
		}
	}
	t.Fatalf("%d taps across the whole yard were every one of them accepted; either the tiredness roll has been disconnected or this is the unluckiest run in the history of the game",
		attempts)
	return walk{}
}

// standAt plants an account in the yard already standing at p, with no journey
// in progress and nothing provisional about it.
//
// The state a test cannot otherwise reach in one step: a completed walk needs
// two ticks and a tap to produce, and half the tests below are about what happens
// to somebody who is simply STANDING there — a departure, a shutdown flush, a
// sleeper. It is the identical placement load() writes when it reads a stored
// position out of Postgres.
func standAt(svc *Service, accountID string, p Point) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	held := svc.pos[accountID]
	held.walk = standing(p)
	held.provisional = false
	svc.pos[accountID] = held
}

// standingAt fails unless a peer is where it should be, to within the last bit
// or two of a float64 — see the note about interpolation above.
func standingAt(t *testing.T, got Peer, want Point, what string) {
	t.Helper()
	if !samePoint(Point{X: got.X, Y: got.Y}, want) {
		t.Fatalf("%s is at (%v,%v); want (%v,%v)", what, got.X, got.Y, want.X, want.Y)
	}
}

// isTiredLine reports whether a balloon is one of the giving-up complaints.
//
// Tests assert on the CATEGORY rather than on a particular string: which line a
// given Ваня uses is a hash, so pinning one would be pinning the hash, and the
// pool is content that is expected to grow.
func isTiredLine(say string) bool {
	return slices.Contains(tiredSays, say)
}

// npcID is the handle the plane publishes one of the yard's regulars under. The
// prefix is the server's own convention; it is asserted against the wire in
// TestTheYardsRegularsAreInEveryFrame and derived from here everywhere else.
func npcID(key string) string { return "npc-" + key }

// withoutNPCs is the entities in a frame that are not the yard's furniture: the
// people in it, and anybody asleep in it.
func withoutNPCs(r Roster) []Peer {
	npcs := make(map[string]bool, len(catalogue.NPCs))
	for _, n := range catalogue.NPCs {
		npcs[npcID(n.Key)] = true
	}
	out := make([]Peer, 0, len(r.Peers))
	for _, p := range r.Peers {
		if !npcs[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

// TestBroadcastPlacesANewConnectionAtTheSpawn covers the first frame somebody
// appears in: they have never sent a move, and they still have to be somewhere,
// or a peer's client would have to invent a position for them.
func TestBroadcastPlacesANewConnectionAtTheSpawn(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	frames := tr.frames()
	if len(frames) != 1 {
		t.Fatalf("published %d frames; want 1", len(frames))
	}
	if frames[0].T != TypeRoster {
		t.Fatalf("frame type = %q; want %q", frames[0].T, TypeRoster)
	}
	p, ok := peerOf(svc, frames[0], accountOf("a"))
	if !ok {
		t.Fatalf("frame does not mention the connected peer: %+v", frames[0])
	}
	if p.X != spawn.X || p.Y != spawn.Y {
		t.Fatalf("peer at (%v,%v); want the spawn point (%v,%v)", p.X, p.Y, spawn.X, spawn.Y)
	}
}

// TestMoveShowsUpInTheNextFrame is the whole point of the iteration: what one
// player does has to become visible to the others.
func TestMoveShowsUpInTheNextFrame(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	// A tick before the tap. It is what puts him at the spawn, and it is what
	// gives the walk a clock to be measured from — a tap that arrives before the
	// plane has ever been drawn has no instant to start at and is applied as a
	// teleport instead.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	dest := Point{X: 0.6, Y: 0.75}
	w := tap(t, svc, member("a"), dest)
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	f := tr.frames()[1]
	if inTheYard(f) != 2 {
		t.Fatalf("the frame says %d people are in the yard; want both players: %+v", inTheYard(f), f)
	}
	moved, ok := peerOf(svc, f, accountOf("a"))
	if !ok {
		t.Fatalf("the player who moved is not in the frame: %+v", f)
	}
	standingAt(t, moved, dest, "the player who tapped")
	still, ok := peerOf(svc, f, accountOf("b"))
	if !ok {
		t.Fatalf("the player who did not move is not in the frame: %+v", f)
	}
	standingAt(t, still, spawn, "the player who did not tap")
}

// TestATapStartsAJourneyRatherThanATeleport is the change distance had to make
// to mean anything.
//
// Before it, the position WAS the tap: the server clamped it, broadcast it, and
// the far side of the yard was 220 ms away whatever the distance — which makes a
// race to arrive impossible and a beer delivery pointless. He now sets off at
// once, is somewhere in between while he walks, and arrives when the distance
// divided by the speed says he does. All three are asserted, because a walk that
// snapped to its destination on the first frame would still satisfy the last one.
func TestATapStartsAJourneyRatherThanATeleport(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	dest := Point{X: 0.85, Y: 0.6}
	w := tap(t, svc, member("a"), dest)
	if w.from != spawn {
		t.Fatalf("the journey starts at (%v,%v); want the spawn he was standing on (%v,%v)", w.from.X, w.from.Y, spawn.X, spawn.Y)
	}

	for _, tc := range []struct {
		name string
		when time.Time
		want Point
		why  string
	}{
		{
			name: "the instant he taps", when: w.startedAt, want: spawn,
			why: "a tap is a request to walk somewhere, and at the moment it is accepted he has not walked anywhere",
		},
		{
			name: "halfway", when: partWayThrough(w), want: lerp(spawn, dest, 0.5),
			why: "he is between the two places, which is the whole of what makes distance mean something",
		},
		{
			name: "once the walk is over", when: afterArriving(w), want: dest,
			why: "he does arrive; a journey that never ended would be worse than a teleport",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.broadcast(context.Background(), tc.when); err != nil {
				t.Fatalf("broadcast: %v", err)
			}
			frames := tr.frames()
			p, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
			if !ok {
				t.Fatalf("the walking player is not in the frame: %+v", frames[len(frames)-1])
			}
			standingAt(t, p, tc.want, tc.name+" — "+tc.why)
		})
	}
}

// TestATapMidJourneyRetargetsFromWhereHeActuallyIs is what makes the yard feel
// responsive rather than queued.
//
// Changing your mind halfway across has to start from where he has got to. The
// two wrong answers are both plausible implementations: carrying on from the old
// DESTINATION queues the second errand behind the first, and restarting from
// where the first journey BEGAN teleports him backwards before he sets off. Both
// look almost right on a screen, and both are ruled out here.
func TestATapMidJourneyRetargetsFromWhereHeActuallyIs(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	// Standing somewhere that is NOT the spawn, so "from where he is" and "from
	// the default" cannot look the same.
	stood := Point{X: 0.2, Y: 0.8}
	if stood == spawn {
		t.Fatal("this fixture stands on the spawn; a retarget and a reset would be indistinguishable")
	}
	standAt(svc, accountOf("a"), stood)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	first := tap(t, svc, member("a"), Point{X: 0.5, Y: 0.7})
	if first.from != stood {
		t.Fatalf("the first journey starts at (%v,%v); want where he was standing (%v,%v)",
			first.from.X, first.from.Y, stood.X, stood.Y)
	}

	// Halfway along, he changes his mind. The tick is what moves the clock the
	// second tap is stamped with.
	half := partWayThrough(first)
	if err := svc.broadcast(context.Background(), half); err != nil {
		t.Fatalf("broadcast halfway: %v", err)
	}
	second := tap(t, svc, member("a"), Point{X: 0.35, Y: 0.95})

	reached := first.at(half)
	if !samePoint(second.from, reached) {
		t.Fatalf("the second journey starts at (%v,%v); want where he had actually got to (%v,%v)",
			second.from.X, second.from.Y, reached.X, reached.Y)
	}
	if samePoint(second.from, first.to) {
		t.Fatal("the second journey starts where the first one was heading; a tap mid-errand queued behind it instead of replacing it")
	}
	if samePoint(second.from, stood) {
		t.Fatal("the second journey starts where the first one began; he was wound back to the start of a walk he had half finished")
	}

	// And the frame drawn at that instant shows him at that same point, so there
	// is no jump on the screen at the moment he is redirected.
	if err := svc.broadcast(context.Background(), second.startedAt); err != nil {
		t.Fatalf("broadcast at the moment of the second tap: %v", err)
	}
	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if !ok {
		t.Fatalf("the redirected player is not in the frame: %+v", frames[len(frames)-1])
	}
	standingAt(t, p, reached, "the redirected player")
}

// TestGivingUpIsDecidedOnceAndShownToEverybody is tiredness as the roster sees
// it, and the property that matters is that it is DECIDED ONCE.
//
// The roll happens at accept time, on the server, and becomes part of the walk.
// Re-rolling it per tick would have him sit down in a different place on every
// frame; rolling it on the client would put a different Ваня on every screen and
// hand the decision to the one party with a reason to cheat. Neither would look
// obviously wrong from one screen, which is exactly why it is pinned here.
func TestGivingUpIsDecidedOnceAndShownToEverybody(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("watching"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	w := tapUntilHeGivesUp(t, svc, member("a"), Point{X: 1, Y: 1})
	sat := lerp(w.from, w.to, w.stopAt)
	if samePoint(sat, w.to) {
		t.Fatal("he gave up exactly where he was heading; there is nothing to see")
	}

	// Evaluated again and again, the journey does not change under him: same
	// start, same destination, same fraction, same instant.
	for _, when := range []time.Time{w.startedAt, atProgress(w, w.stopAt/2), afterArriving(w), afterArriving(w).Add(time.Hour)} {
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		got := walkOf(t, svc, accountOf("a"))
		if got.stopAt != w.stopAt || got.from != w.from || got.to != w.to || !got.startedAt.Equal(w.startedAt) {
			t.Fatalf("the journey changed between ticks: %+v became %+v — the give-up is part of the walk, not something re-rolled per frame", w, got)
		}
	}

	// What the yard sees: still walking, then sitting and saying so, then sitting
	// and over it. He never reaches the place he was tapped towards.
	// The expectation is about the COMPLAINT, not about silence. Once he has his
	// breath back he is standing about, and a Ваня standing about is entitled to
	// mutter something unrelated — so the assertion after the window is that the
	// giving-up line is gone, not that his mouth is shut.
	for _, tc := range []struct {
		name  string
		when  time.Time
		want  Point
		tired bool
		why   string
	}{
		{
			name: "on the way", when: atProgress(w, w.stopAt/2), want: lerp(w.from, w.to, w.stopAt/2),
			why: "the walk up to the point he gives up is a walk like any other, and a walker says nothing at all",
		},
		{
			name: "the moment he sits down", when: w.stoppedAt().Add(time.Millisecond), want: sat, tired: true,
			why: "a speed cap alone reads as a tax; a Ваня who sits down halfway across the yard and says his leg is falling off IS the game",
		},
		{
			name: "once he has got his breath back", when: w.stoppedAt().Add(tiredFor), want: sat,
			why: "the line is derived from the clock, so it needs no timer and no cleanup — it simply stops being true",
		},
		{
			name: "an hour later", when: w.stoppedAt().Add(time.Hour), want: sat,
			why: "he stays where he sat down until somebody asks him to go somewhere else",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.broadcast(context.Background(), tc.when); err != nil {
				t.Fatalf("broadcast: %v", err)
			}
			frames := tr.frames()
			p, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
			if !ok {
				t.Fatalf("the tired player is not in the frame: %+v", frames[len(frames)-1])
			}
			standingAt(t, p, tc.want, tc.name+" — "+tc.why)
			switch {
			case tc.tired && p.Say != w.say:
				t.Fatalf("%s he says %q; want the complaint carried on the walk, %q — %s",
					tc.name, p.Say, w.say, tc.why)
			case tc.tired && !isTiredLine(p.Say):
				t.Fatalf("%s he says %q, which is not one of the giving-up lines — %s", tc.name, p.Say, tc.why)
			case tc.name == "on the way" && p.Say != "":
				t.Fatalf("on the way he says %q; a Ваня mid-walk says nothing — %s", p.Say, tc.why)
			case !tc.tired && isTiredLine(p.Say):
				t.Fatalf("%s he is still complaining (%q) — %s", tc.name, p.Say, tc.why)
			}
			// And the frame that carries it is the one everybody in the room gets,
			// which is what makes them all watch the same man sit down.
			if other, ok := peerOf(svc, frames[len(frames)-1], accountOf("watching")); !ok || other.Say != "" {
				t.Fatalf("the bystander is drawn as %+v; only the man who gave up says anything", other)
			}
		})
	}
}

// TestEveryFrameIsFullState pins the property the hub's backpressure depends on.
// The hub drops frames for a client that is behind, so a frame that described
// only what changed would leave that client rendering a world that no longer
// exists — undetectably. Nothing moves here, and the second frame must still
// describe everybody.
func TestEveryFrameIsFullState(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	for i := 0; i < 3; i++ {
		if err := svc.broadcast(context.Background(), at(0)); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
	}

	for i, f := range tr.frames() {
		// The people, not the entities: the yard's regulars are in every frame too
		// and are counted separately — see TestTheYardsRegularsAreInEveryFrame.
		if n := len(withoutNPCs(f)); n != 2 {
			t.Fatalf("frame %d carries %d people; every frame must carry all of them", i, n)
		}
		if inTheYard(f) != 2 {
			t.Fatalf("frame %d says %d are in the yard; want 2", i, inTheYard(f))
		}
		for _, who := range []string{"a", "b"} {
			if _, ok := peerOf(svc, f, accountOf(who)); !ok {
				t.Fatalf("frame %d leaves %s out; a frame is full state, not a description of what changed", i, who)
			}
		}
	}
}

// TestLeavingRemovesAPeerFromTheFrameAtOnce is the first half of a disconnect
// for somebody who had only one device: he stops being a person in the yard the
// moment his socket goes, with nothing having told the game he left.
//
// It is only the first half now. The position is REMEMBERED for a grace period,
// which is what makes a reload keep your place, and past that he does not vanish
// — he lies down and sleeps where he stood, which is
// TestAnAbsentPlayerLiesDownInTheYardWhereHeStood's subject.
// TestAnEntityOutlivesOneOfItsConnections is the multi-device case, where one
// socket closing must NOT remove anything.
func TestLeavingRemovesAPeerFromTheFrameAtOnce(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	w := tap(t, svc, member("a"), Point{X: 0.2, Y: 0.3})
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	tr.setMembers(member("b"))
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast after leave: %v", err)
	}

	f := tr.frames()[2]
	if _, ok := peerOf(svc, f, accountOf("a")); ok {
		t.Fatalf("a disconnected peer is still in the frame: %+v", f)
	}
	if inTheYard(f) != 1 {
		t.Fatalf("the frame says %d are in the yard; only one socket is still open", inTheYard(f))
	}
}

// TestAnEmptyRoomPublishesNothing keeps the common case free. Nobody is
// connected for most of the day, and publishing to an empty room five times a
// second would be pure waste — and would hide a genuine "why is this room
// empty" question behind traffic.
func TestAnEmptyRoomPublishesNothing(t *testing.T) {
	tr := &fakeTransport{}
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if n := len(tr.frames()); n != 0 {
		t.Fatalf("published %d frames into an empty room; want none", n)
	}
}

// TestAReloadKeepsYourPlace is the reason PositionGrace exists.
//
// Reloading the page closes the socket and opens a new one, so for a moment the
// account has no connections at all. The map used to be rebuilt from the live
// membership every tick, which dropped the position in that gap and put the
// player back in the middle of the yard — for a refresh, a tunnel, a lock
// screen, and every reconnect. Absence is not departure.
func TestAReloadKeepsYourPlace(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.4,"y":0.4}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// The socket goes away, and a couple of ticks pass with the yard empty.
	tr.setMembers()
	for _, when := range []time.Time{at(0.2), at(0.4)} {
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast into an empty room: %v", err)
		}
	}

	// The new socket arrives, well inside the grace.
	tr.setMembers(member("a"))
	if err := svc.broadcast(context.Background(), at(0.6)); err != nil {
		t.Fatalf("broadcast after the reload: %v", err)
	}

	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if !ok {
		t.Fatal("the reloaded player is missing from the roster")
	}
	if p.X != 0.4 || p.Y != 0.4 {
		t.Fatalf("peer at (%v,%v) after a reload; want where they were standing (0.4,0.4)", p.X, p.Y)
	}
}

// TestAnEmptyRoomForgetsAPlacementNobodyChose is what is left of the leak the
// grace period could introduce, now that a position outliving it is the FEATURE
// rather than the bug.
//
// Somebody who stood somewhere is kept: he is the sleeper in the yard, and the
// map is bounded by the number of pets rather than by traffic. Somebody who was
// merely SEEN — placed at the spawn by a tick, never a tap, never a hello — has
// nowhere to lie down and nothing worth remembering, and he is the one an
// ever-growing map would be made of: every socket that ever opened, for the life
// of the process.
func TestAnEmptyRoomForgetsAPlacementNobodyChose(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("chose"), member("drifted"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	// One of them goes and stands somewhere; the other never says a word.
	w := tap(t, svc, member("chose"), Point{X: 0.4, Y: 0.4})
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	gone := afterArriving(w)

	tr.setMembers()
	// One tick inside the grace: both still remembered, because a reload is
	// exactly this shape and neither of them has really left.
	if err := svc.broadcast(context.Background(), gone.Add(PositionGrace/2)); err != nil {
		t.Fatalf("broadcast inside the grace: %v", err)
	}
	svc.mu.Lock()
	held := len(svc.pos)
	svc.mu.Unlock()
	if held != 2 {
		t.Fatalf("%d positions held inside the grace; want both", held)
	}

	// One tick past it: the drifter is forgotten entirely, and the man who stood
	// somewhere is kept, because he is now lying in the yard asleep.
	if err := svc.broadcast(context.Background(), gone.Add(PositionGrace+time.Second)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}
	svc.mu.Lock()
	_, keptChoser := svc.pos[accountOf("chose")]
	_, keptDrifter := svc.pos[accountOf("drifted")]
	_, keptDrifterFace := svc.display[accountOf("drifted")]
	svc.mu.Unlock()
	if keptDrifter || keptDrifterFace {
		t.Fatal("a placement nobody chose survived the grace period; the map would then grow with every socket that ever opened")
	}
	if !keptChoser {
		t.Fatal("the position of somebody who actually stood somewhere was thrown away; he is what the yard is furnished with when nobody is in it")
	}
}

// TestComingBackAfterTheGraceWakesHimWhereHeWasLyingDown is the rule the
// sleepers changed.
//
// It used to be that a position past the grace was stale and the spawn was the
// honest answer. It is not stale any more — it is where his Ваня is asleep, and
// everybody else in the yard can see him lying there — so waking him up in the
// middle of the yard would teleport a body that was on screen a frame earlier.
// Coming back is now the same journey as any other: it starts where he is.
func TestComingBackAfterTheGraceWakesHimWhereHeWasLyingDown(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("watching"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	lay := Point{X: 0.8, Y: 0.2}
	if lay == spawn {
		t.Fatal("this fixture lies down on the spawn, so waking up in place and being reset would look the same")
	}
	w := tap(t, svc, member("a"), lay)
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	gone := afterArriving(w)

	tr.setMembers(member("watching"))
	if err := svc.broadcast(context.Background(), gone.Add(PositionGrace+time.Second)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}

	tr.setMembers(member("a"), member("watching"))
	if err := svc.broadcast(context.Background(), gone.Add(2*PositionGrace)); err != nil {
		t.Fatalf("broadcast on the return: %v", err)
	}

	frames := tr.frames()
	back := frames[len(frames)-1]
	p, ok := peerOf(svc, back, accountOf("a"))
	if !ok {
		t.Fatalf("the returning player is not in the frame: %+v", back)
	}
	standingAt(t, p, lay, "the player who came back")
	if p.Pose == PoseAsleep {
		t.Fatal("he is drawn asleep while his socket is open; waking up is what connecting means")
	}
	if inTheYard(back) != 2 {
		t.Fatalf("the frame says %d are in the yard; both sockets are open", inTheYard(back))
	}
	// And there is exactly one of him: an account that is both present and past
	// the grace must not be drawn twice, once awake and once asleep.
	n := 0
	for _, e := range back.Peers {
		if e.ID == svc.pseudonym(accountOf("a")) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the returning player appears %d times in the frame; want once: %+v", n, back)
	}
}

// TestRejectedFramesLeaveThePositionAlone is the substance of "the server owns
// truth". A malformed or out-of-protocol frame must not move anybody, and above
// all must not reset them to the spawn — a client could otherwise teleport a
// rival home by sending nonsense.
func TestRejectedFramesLeaveThePositionAlone(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.8,"y":0.2}`))
	for _, bad := range []string{
		`not json`,
		`{"t":"vanyagotchi_move"}`,
		`{"t":"vanyagotchi_move","x":0.1}`,
		`{"t":"something_else","x":0.1,"y":0.1}`,
		`{"t":"vanyagotchi_move","x":"0.1","y":0.1}`,
	} {
		svc.HandleInbound(context.Background(), member("a"), testRoom, []byte(bad))
	}

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	p, _ := peerOf(svc, tr.frames()[0], accountOf("a"))
	if p.X != 0.8 || p.Y != 0.2 {
		t.Fatalf("peer at (%v,%v) after bad frames; want the last good move (0.8,0.2)", p.X, p.Y)
	}
}

// TestFramesFromAnotherRoomAreIgnored matters because the handler is registered
// on the transport, not on a room: it is handed everything that arrives. A game
// acting on a frame from a room it does not own would be one realtime feature
// reaching into another.
func TestFramesFromAnotherRoomAreIgnored(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), "somewhere-else",
		[]byte(`{"t":"vanyagotchi_move","x":0.9,"y":0.9}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	p, _ := peerOf(svc, tr.frames()[0], accountOf("a"))
	if p.X != spawn.X || p.Y != spawn.Y {
		t.Fatalf("a frame from another room moved the peer to (%v,%v)", p.X, p.Y)
	}
}

// TestRunPublishesOnEachTick proves the loop is driven by the injected channel
// and nothing else — no internal timer, so a test never waits on wall-clock time
// and a production tick that is late produces the same frame.
func TestRunPublishesOnEachTick(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx, tick) }()

	for i := 1; i <= 3; i++ {
		tick <- time.Time{}
		waitForCount(t, tr, i)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
}

// TestRunStopsWhenTheHubIsGone covers the deploy path. The hub drains before the
// HTTP server on every restart, so this loop outlives it by a moment; it must
// stop rather than spin logging a failure five times a second until the process
// dies.
func TestRunStopsWhenTheHubIsGone(t *testing.T) {
	tr := &fakeTransport{membersErr: realtime.ErrHubClosed}
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	tick := make(chan time.Time, 1)
	tick <- time.Time{}
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(context.Background(), tick) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going after the hub reported itself closed")
	}
}

// TestRunSurvivesATransientPublishFailure is the other side of that: an ordinary
// error must not kill the loop, or one bad moment would silently end the game
// for everybody until the next deploy.
func TestRunSurvivesATransientPublishFailure(t *testing.T) {
	tr := &fakeTransport{publishErr: errors.New("transient")}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx, tick) }()

	// Two ticks accepted means the loop is still running after the first failed.
	for i := 0; i < 2; i++ {
		select {
		case tick <- time.Time{}:
		case <-done:
			t.Fatal("Run exited on a transient publish failure")
		case <-time.After(2 * time.Second):
			t.Fatal("Run stopped consuming ticks")
		}
	}
}

// TestConcurrentMovesAreSafe drives the actual production shape: HandleInbound
// runs on each connection's own read pump, so it is called concurrently while
// the broadcast loop reads the same map. Meaningful under -race, which the gate
// runs.
func TestConcurrentMovesAreSafe(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"), member("c"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				svc.HandleInbound(context.Background(), member(id), testRoom,
					[]byte(`{"t":"vanyagotchi_move","x":0.5,"y":0.5}`))
			}
		}(id)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := svc.broadcast(context.Background(), at(0)); err != nil {
				t.Errorf("broadcast: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

// TestTwoConnectionsOfOneAccountAreOneEntity is the bug this keying fixes:
// signing in on a second device used to put a second Ваня in the yard, because
// the roster was built per connection and the hub reports one Member per socket.
func TestTwoConnectionsOfOneAccountAreOneEntity(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(conn("phone", "acct-1"), conn("laptop", "acct-1"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	f := tr.frames()[0]
	if n := len(withoutNPCs(f)); n != 1 {
		t.Fatalf("two devices of one account produced %d entities; want 1: %+v", n, f)
	}
	if inTheYard(f) != 1 {
		t.Fatalf("the frame says %d people are in the yard; two devices are one person: %+v", inTheYard(f), f)
	}
	if _, ok := peerOf(svc, f, "acct-1"); !ok {
		t.Fatalf("the one entity is not the account's: %+v", f)
	}
}

// TestAMoveFromEitherDeviceMovesTheSameEntity is the other half of it. One Ваня
// means one position: whichever socket the tap arrives on, the same entity has
// to move, and there must still be only one of it afterwards.
func TestAMoveFromEitherDeviceMovesTheSameEntity(t *testing.T) {
	phone, laptop := conn("phone", "acct-1"), conn("laptop", "acct-1")
	for _, tc := range []struct {
		name   string
		sender realtime.Member
		x, y   float64
	}{
		// Both destinations are within tiredFrom of the spawn he starts from, so
		// neither tap can be refused — see the tap helper.
		{name: "from the phone", sender: phone, x: 0.25, y: 0.3},
		{name: "from the laptop", sender: laptop, x: 0.7, y: 0.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &fakeTransport{}
			tr.setMembers(phone, laptop)
			svc := NewService(tr, testRoom, nil, nil, nil, nil)

			if err := svc.broadcast(context.Background(), at(0)); err != nil {
				t.Fatalf("broadcast: %v", err)
			}
			w := tap(t, svc, tc.sender, Point{X: tc.x, Y: tc.y})
			if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
				t.Fatalf("broadcast: %v", err)
			}

			f := tr.frames()[1]
			if n := len(withoutNPCs(f)); n != 1 {
				t.Fatalf("frame carries %d entities that are not the yard's regulars; want 1: %+v", n, f)
			}
			p, ok := peerOf(svc, f, "acct-1")
			if !ok {
				t.Fatalf("the account is not on the plane: %+v", f)
			}
			standingAt(t, p, Point{X: tc.x, Y: tc.y}, "the entity both devices drive")
		})
	}
}

// TestAnEntityOutlivesOneOfItsConnections is the pruning property restated per
// account. Closing a laptop must not remove the Ваня the phone is still holding
// open — but closing the last socket must, and must drop its position with it,
// or the map grows for the life of the process.
func TestAnEntityOutlivesOneOfItsConnections(t *testing.T) {
	tr := &fakeTransport{}
	phone, laptop := conn("phone", "acct-1"), conn("laptop", "acct-1")
	tr.setMembers(phone, laptop)
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	stood := Point{X: 0.25, Y: 0.5}
	w := tap(t, svc, laptop, stood)
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// The laptop goes; the phone is still connected.
	tr.setMembers(phone)
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast after one connection left: %v", err)
	}
	p, ok := peerOf(svc, tr.frames()[2], "acct-1")
	if !ok {
		t.Fatalf("the account vanished while it still had a connection: %+v", tr.frames()[2])
	}
	// And it kept standing where the departed device had put it — the position
	// belongs to the account, not to the socket that last moved it.
	standingAt(t, p, stood, "the account whose laptop closed")

	// The phone goes too, and now nobody is driving it.
	tr.setMembers(conn("someone-else", "acct-2"))
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast after the last connection left: %v", err)
	}
	last := tr.frames()[3]
	if _, ok := peerOf(svc, last, "acct-1"); ok {
		t.Fatalf("an account with no connections is still on the plane: %+v", last)
	}
	if inTheYard(last) != 1 {
		t.Fatalf("the frame says %d are in the yard; only the stranger's socket is open", inTheYard(last))
	}
	// Off the plane as a PERSON at once. The placement itself outlives the grace
	// on purpose — he is asleep in the yard from then on, which is
	// TestAnAbsentPlayerLiesDownInTheYardWhereHeStood's subject — and what must
	// not outlive it is a placement nobody chose, which
	// TestAnEmptyRoomForgetsAPlacementNobodyChose covers.
}

// TestTwoAccountsAreStillTwoEntities is the guard against over-correcting. The
// fix collapses one account's connections; it must not collapse two people who
// happen to be in the room together.
func TestTwoAccountsAreStillTwoEntities(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(
		conn("a-phone", "acct-1"), conn("a-laptop", "acct-1"),
		conn("b-phone", "acct-2"),
	)
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	f := tr.frames()[0]
	if n := len(withoutNPCs(f)); n != 2 {
		t.Fatalf("three connections across two accounts produced %d entities; want 2: %+v", n, f)
	}
	if inTheYard(f) != 2 {
		t.Fatalf("the frame says %d people are in the yard; three sockets across two accounts is two people: %+v", inTheYard(f), f)
	}
	for _, acct := range []string{"acct-1", "acct-2"} {
		if _, ok := peerOf(svc, f, acct); !ok {
			t.Fatalf("%s is missing from the plane: %+v", acct, f)
		}
	}
}

// TestThePseudonymIsStableAndIsNotTheAccountID pins the identifier's contract in
// both directions. It has to be usable as an identity — the same for one account
// across its devices and across ticks, different between accounts — and it has
// to not be the account id, which is the durable cross-session handle this
// project declines to broadcast to everybody in a room.
func TestThePseudonymIsStableAndIsNotTheAccountID(t *testing.T) {
	svc := NewService(&fakeTransport{}, testRoom, nil, nil, nil, nil)
	const acct = "11111111-2222-3333-4444-555555555555"

	first := svc.pseudonym(acct)
	if first != svc.pseudonym(acct) {
		t.Fatal("the pseudonym changed between two calls for the same account")
	}
	if first == acct {
		t.Fatal("the pseudonym IS the account id")
	}
	if strings.Contains(first, acct) || strings.Contains(acct, first) {
		t.Fatalf("the pseudonym %q shares text with the account id", first)
	}
	if len(first) != pseudonymChars {
		t.Fatalf("pseudonym %q is %d chars; want %d", first, len(first), pseudonymChars)
	}
	if other := svc.pseudonym("66666666-7777-8888-9999-000000000000"); other == first {
		t.Fatal("two accounts share one pseudonym")
	}

	// A second process is a second key, so the same account is a different
	// entity there. That is the point of a per-process key: nothing published
	// before a restart can be correlated with anything published after it.
	if second := NewService(&fakeTransport{}, testRoom, nil, nil, nil, nil).pseudonym(acct); second == first {
		t.Fatal("two services produced the same pseudonym; the key is not per-process")
	}
}

// TestTheRosterNeverCarriesAnAccountID is the same rule enforced where it
// actually matters — on the bytes that leave the process. Deriving a pseudonym
// is no use if the frame also carries the thing it was derived from.
func TestTheRosterNeverCarriesAnAccountID(t *testing.T) {
	tr := &fakeTransport{}
	const acct = "11111111-2222-3333-4444-555555555555"
	tr.setMembers(conn("c-1", acct))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	tr.mu.Lock()
	raw := string(tr.published[0])
	tr.mu.Unlock()

	if strings.Contains(raw, acct) {
		t.Fatalf("the account id is on the wire: %s", raw)
	}
	if !strings.Contains(raw, svc.pseudonym(acct)) {
		t.Fatalf("the frame does not carry the account's pseudonym: %s", raw)
	}
}

// TestHelloIsAnsweredOnTheAskingConnectionOnly covers the handshake. The client
// cannot pick itself out of a roster that names everybody by an opaque handle,
// so it asks — and the answer is a unicast, because the pseudonym of one player
// is of no use to the rest of the room.
func TestHelloIsAnsweredOnTheAskingConnectionOnly(t *testing.T) {
	tr := &fakeTransport{}
	asker, bystander := conn("asker", "acct-1"), conn("bystander", "acct-2")
	tr.setMembers(asker, bystander)
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	svc.HandleInbound(context.Background(), asker, testRoom, []byte(`{"t":"vanyagotchi_hello"}`))

	got, ok := youFor(t, tr, "asker")
	if !ok {
		t.Fatalf("the asking connection got no single you frame: %+v", tr.unicasts())
	}
	if got.ID != svc.pseudonym("acct-1") {
		t.Fatalf("you.id = %q; want the asker's own pseudonym %q", got.ID, svc.pseudonym("acct-1"))
	}
	// And the identity it was told is the identity it is drawn under.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if _, found := peerByID(tr.frames()[0], got.ID); !found {
		t.Fatalf("the id the client was given is in no roster: %+v", tr.frames()[0])
	}
	// Nobody else hears about it.
	for _, u := range tr.unicasts() {
		if u.connID != "asker" {
			t.Fatalf("a you frame was sent to %q as well", u.connID)
		}
	}
}

// TestHelloFromASecondDeviceGetsTheSameID is what makes "highlight me" work on
// both devices at once: they are one entity, so they must be told the same id.
func TestHelloFromASecondDeviceGetsTheSameID(t *testing.T) {
	tr := &fakeTransport{}
	phone, laptop := conn("phone", "acct-1"), conn("laptop", "acct-1")
	tr.setMembers(phone, laptop)
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	svc.HandleInbound(context.Background(), phone, testRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	svc.HandleInbound(context.Background(), laptop, testRoom, []byte(`{"t":"vanyagotchi_hello"}`))

	onPhone, okP := youFor(t, tr, "phone")
	onLaptop, okL := youFor(t, tr, "laptop")
	if !okP || !okL {
		t.Fatalf("both devices must be answered exactly once: %+v", tr.unicasts())
	}
	if onPhone.ID != onLaptop.ID {
		t.Fatalf("the two devices were told different ids (%q, %q)", onPhone.ID, onLaptop.ID)
	}
}

// TestOnlyAHelloIsAnswered keeps the reply path narrow. Every other frame is
// silent by design — a move that drew a reply would let a client believe a
// position the server had not published yet, and an unknown frame that drew one
// would be an amplifier a client controls.
func TestOnlyAHelloIsAnswered(t *testing.T) {
	tr := &fakeTransport{}
	c := conn("c-1", "acct-1")
	tr.setMembers(c)
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	for _, payload := range []string{
		`{"t":"vanyagotchi_move","x":0.5,"y":0.5}`,
		`{"t":"vanyagotchi_you","id":"whatever"}`,
		`{"t":"vanyagotchi_shout"}`,
		`{"t":"bye","code":1001,"reason":"restart"}`,
		`not json`,
		``,
	} {
		svc.HandleInbound(context.Background(), c, testRoom, []byte(payload))
	}
	if n := len(tr.unicasts()); n != 0 {
		t.Fatalf("%d frames were sent in reply to messages that are not a hello: %+v", n, tr.unicasts())
	}

	// A hello from another room is somebody else's protocol, not ours.
	svc.HandleInbound(context.Background(), c, "somewhere-else", []byte(`{"t":"vanyagotchi_hello"}`))
	if n := len(tr.unicasts()); n != 0 {
		t.Fatalf("a hello from another room was answered: %+v", tr.unicasts())
	}
}

// ---------------------------------------------------------------------------
// Appearance: what stops the yard being a field of anonymous dots.
// ---------------------------------------------------------------------------

// TestEveryPeerCarriesItsArtAndPoseAndOnlyANamedPetALabel is the whole of the
// roster's appearance contract, in one frame.
//
// It goes to EVERYBODY, and that is the point: a world where each player can
// only see their own Ваня properly is two worlds rather than one shared one. The
// label is the one field that may be absent, because a pet is named in a dialog
// and is nil until then — publishing an empty string as a name would put a blank
// caption under an entity rather than none.
func TestEveryPeerCarriesItsArtAndPoseAndOnlyANamedPetALabel(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	name := "Ваня"

	tr := &fakeTransport{}
	tr.setMembers(member("named"), member("nameless"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	// Cached exactly as the two human-paced events fill it — a hello, or its
	// owner acting over HTTP. Which of the two put it there is not this test's
	// subject; that they are what the frame is built from is.
	svc.remember(accountOf("named"), Pet{SkinKey: SkinVanya, Name: &name},
		[]StatRow{{Key: StatHP, Value: (hpDef.Min + hpDef.WarnAt) / 2, AsOf: at(0)}})
	svc.remember(accountOf("nameless"), Pet{SkinKey: SkinVanya},
		[]StatRow{{Key: StatHP, Value: (hpDef.WarnAt + hpDef.Max) / 2, AsOf: at(0)}})

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]

	named, ok := peerOf(svc, f, accountOf("named"))
	if !ok {
		t.Fatalf("the named account is not on the plane: %+v", f)
	}
	if named.Art != SkinVanya {
		t.Errorf("art = %q; want the pet's own skin %q", named.Art, SkinVanya)
	}
	if named.Label != name {
		t.Errorf("label = %q; want %q", named.Label, name)
	}
	if named.Pose != PosePoorly {
		t.Errorf("pose = %q; want %q — his health is inside the catalogue's warning range", named.Pose, PosePoorly)
	}

	nameless, ok := peerOf(svc, f, accountOf("nameless"))
	if !ok {
		t.Fatalf("the unnamed account is not on the plane: %+v", f)
	}
	if nameless.Label != "" {
		t.Errorf("an unnamed pet was labelled %q; want no label at all", nameless.Label)
	}
	if nameless.Art != SkinVanya || nameless.Pose != PoseFine {
		t.Errorf("an unnamed but healthy pet was drawn as %+v; want the skin it has and %q", nameless, PoseFine)
	}
}

// TestAPlayerTheCacheKnowsNothingAboutIsStillDrawn is the rule that a missing
// lookup must never cost somebody their presence.
//
// A client that has connected but not yet said hello, or one whose owner has
// never opened the game over HTTP, has nothing cached at all. Rendering nothing
// for them would make a real person invisible to everyone else in the yard for
// the sake of a name — so they are drawn with the catalogue's default skin, no
// label, and no trouble.
func TestAPlayerTheCacheKnowsNothingAboutIsStillDrawn(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("stranger"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]
	p, ok := peerOf(svc, f, accountOf("stranger"))
	if !ok {
		t.Fatalf("an account with nothing cached was left out of the roster entirely: %+v", f)
	}
	if p.Art != Content().DefaultSkin {
		t.Errorf("art = %q; want the catalogue default %q", p.Art, Content().DefaultSkin)
	}
	if p.Label != "" {
		t.Errorf("label = %q; want none — nothing is known about this pet, not even that it has a name", p.Label)
	}
	if p.Pose != PoseFine {
		t.Errorf("pose = %q; want %q — an unknown pet must not be drawn as one that is dying", p.Pose, PoseFine)
	}
}

// ---------------------------------------------------------------------------
// Durable position: what makes a deploy leave the yard where it was.
// ---------------------------------------------------------------------------

// noPool stands in for the connection pool on the plane's durable path.
//
// The service does no durable work at all while its pool or its repository is
// nil — which is what lets every test above drive the plane with both of them
// unset — so a test of the durable half needs something non-nil to hand it. The
// fake repository ignores it entirely, and every method here panics rather than
// answering, so a query that somehow reached the pool fails loudly instead of
// quietly reading nothing.
type noPool struct{}

func (noPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("gamevanyagotchi: the plane reached the connection pool")
}

func (noPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("gamevanyagotchi: the plane reached the connection pool")
}

func (noPool) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("gamevanyagotchi: the plane reached the connection pool")
}

// planeService builds the plane with its durable half wired up.
//
// Run is deliberately never started by the tests below. Departures are queued by
// the broadcast and written by a goroutine of Run's, so driving broadcast
// directly and reading the queue makes every assertion here synchronous: no
// waiting, no polling, and no way for a passing test to be a race that happened
// to land the right way round.
func planeService(tr Transport, repo Repository) *Service {
	return NewService(tr, testRoom, noPool{}, repo, nil, nil)
}

// queued drains everything the plane has asked to have written down.
func queued(svc *Service) []positionSave {
	var out []positionSave
	for {
		select {
		case sv := <-svc.saves:
			out = append(out, sv)
		default:
			return out
		}
	}
}

// TestADepartureIsQueuedOnceHoweverLongItLasts is why placement carries a
// `saved` flag at all.
//
// Absence is not an event: there is no leave hook, so it is re-observed on every
// tick until the grace period runs out. Queueing the write each time would be
// five database writes a second for somebody who has already gone — the
// per-tick traffic this whole design exists to avoid — and every one after the
// first would say exactly what the first one said.
//
// Asserted on the queue rather than on the repository because the queue is the
// seam the broadcast itself touches. What is under test is what the TICK does;
// the writer behind it is a goroutine, and reaching past it would mean
// synchronising with something this assertion does not need.
func TestADepartureIsQueuedOnceHoweverLongItLasts(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.3}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// Gone, and noticed on every one of the next several ticks — all of them
	// well inside the grace, so the position is still being held.
	tr.setMembers()
	for i := 1; i <= 5; i++ {
		if err := svc.broadcast(context.Background(), at(float64(i)*0.1)); err != nil {
			t.Fatalf("broadcast %d after the departure: %v", i, err)
		}
	}

	saves := queued(svc)
	if len(saves) != 1 {
		t.Fatalf("%d writes queued for one departure observed over five ticks; want exactly 1", len(saves))
	}
	if saves[0].accountID != accountOf("a") {
		t.Errorf("queued a write for %q; want %q", saves[0].accountID, accountOf("a"))
	}
	if saves[0].at.X != 0.2 || saves[0].at.Y != 0.3 {
		t.Errorf("queued position (%v,%v); want where he was standing (0.2,0.3)", saves[0].at.X, saves[0].at.Y)
	}
	// The instant recorded is the last tick he was actually connected, not the
	// one that noticed he had gone — the first is when he was last seen and the
	// second is merely when somebody looked.
	if !saves[0].seen.Equal(at(0)) {
		t.Errorf("queued seen = %v; want the last tick he was connected, %v", saves[0].seen, at(0))
	}
}

// TestComingBackMakesTheNextDepartureANewOne is the other half of that flag. It
// suppresses repeats of ONE departure and must not suppress the next one, or a
// player who reloaded the page would keep his old resting place forever and
// every walk after the first reconnect would be lost on the way out.
func TestComingBackMakesTheNextDepartureANewOne(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.1,"y":0.1}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// Away — written down once.
	tr.setMembers()
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast while away: %v", err)
	}

	// Back inside the grace, and standing somewhere else by the time he goes
	// again.
	tr.setMembers(member("a"))
	if err := svc.broadcast(context.Background(), at(0.4)); err != nil {
		t.Fatalf("broadcast on the return: %v", err)
	}
	// His second move is a WALK — a tick has been published by now, so it has a
	// clock to be measured from — and the departure is written down only once he
	// has finished it. The destination is within tiredFrom of where he is
	// standing, so the walk cannot be refused half way; that outcome is real, and
	// it belongs to the test about it rather than to this one.
	walked := tap(t, svc, member("a"), Point{X: 0.4, Y: 0.35})
	if err := svc.broadcast(context.Background(), afterArriving(walked)); err != nil {
		t.Fatalf("broadcast after the second move: %v", err)
	}
	back := afterArriving(walked)

	tr.setMembers()
	if err := svc.broadcast(context.Background(), back.Add(time.Second)); err != nil {
		t.Fatalf("broadcast on the second departure: %v", err)
	}

	saves := queued(svc)
	if len(saves) != 2 {
		t.Fatalf("%d writes queued for two departures; want exactly 2: %+v", len(saves), saves)
	}
	second := saves[1]
	if !samePoint(second.at, walked.to) {
		t.Errorf("the second departure was written at (%v,%v); want where he was standing by then (%v,%v)",
			second.at.X, second.at.Y, walked.to.X, walked.to.Y)
	}
	if !second.seen.Equal(back) {
		t.Errorf("the second departure carries seen = %v; want the last tick of his second visit, %v — it is a new departure, not a repeat of the first",
			second.seen, back)
	}
}

// TestTheShutdownFlushWritesDownEverybodyStillStanding is the leg that only a
// deploy exercises.
//
// A restart is everybody leaving at once with no further tick to notice it, so
// without this the whole yard would come back standing in the middle — which is
// the exact failure durable position exists to fix, and the one that shows up
// nowhere but production.
func TestTheShutdownFlushWritesDownEverybodyStillStanding(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.4}`))
	svc.HandleInbound(context.Background(), member("b"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.7,"y":0.6}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	svc.flushPositions()

	saved := repo.saved()
	if len(saved) != 2 {
		t.Fatalf("the shutdown flush wrote %d positions; want one for each of the two players: %+v", len(saved), saved)
	}
	want := map[string]Point{
		accountOf("a"): {X: 0.2, Y: 0.4},
		accountOf("b"): {X: 0.7, Y: 0.6},
	}
	for _, sv := range saved {
		w, ok := want[sv.accountID]
		if !ok {
			t.Fatalf("the flush wrote a position for %q, who is not in the yard", sv.accountID)
		}
		if sv.at != w {
			t.Errorf("%q was written down at (%v,%v); want (%v,%v)", sv.accountID, sv.at.X, sv.at.Y, w.X, w.Y)
		}
		if !sv.seen.Equal(at(0)) {
			t.Errorf("%q was written down as last seen at %v; want the last tick, %v", sv.accountID, sv.seen, at(0))
		}
		delete(want, sv.accountID)
	}
	if len(want) != 0 {
		t.Fatalf("the flush left %v out entirely", want)
	}
}

// TestTheShutdownFlushDoesNotWriteADepartureDownTwice is the same flag seen from
// the other end.
//
// Somebody who left a moment before the restart is STILL held in memory — the
// grace period is what makes a reload keep your place — so the flush can see
// him. Writing him again would be a second write saying exactly what the queued
// one said, and it is the flush, arriving later, that would win: he would be
// recorded as last seen at the shutdown rather than at the moment he actually
// left.
func TestTheShutdownFlushDoesNotWriteADepartureDownTwice(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("staying"), member("leaving"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	// Both of them go and stand somewhere first, which is what makes their
	// positions worth writing down at all: a spawn point nobody chose is
	// provisional and is deliberately NOT persisted — see the test below.
	for _, who := range []string{"staying", "leaving"} {
		svc.HandleInbound(context.Background(), member(who), testRoom,
			[]byte(`{"t":"vanyagotchi_move","x":0.3,"y":0.7}`))
	}
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// One socket goes, and the tick queues that departure.
	tr.setMembers(member("staying"))
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast after the departure: %v", err)
	}
	if n := len(queued(svc)); n != 1 {
		t.Fatalf("%d writes queued for one departure; want 1", n)
	}
	svc.mu.Lock()
	_, held := svc.pos[accountOf("leaving")]
	svc.mu.Unlock()
	if !held {
		t.Fatal("the departed account was forgotten immediately; this test needs it still held, which is what makes the flush able to see it")
	}

	svc.flushPositions()

	saved := repo.saved()
	if len(saved) != 1 {
		t.Fatalf("the flush wrote %d positions; want 1 — only the player who is still standing there: %+v", len(saved), saved)
	}
	if saved[0].accountID != accountOf("staying") {
		t.Fatalf("the flush wrote down %q; want %q, since the other one's departure had already been written",
			saved[0].accountID, accountOf("staying"))
	}
}

// TestAHelloPutsAPetBackWhereItLastStood is the reconnect a deploy causes: the
// position map died with the old process, so the only thing that knows where
// anybody was standing is the pets table.
//
// The second half is the case the guard in load exists for. A reconnect INSIDE
// the grace is also a hello, and there the in-memory position is the newer
// truth — winding him back to the last one written down would undo the very
// thing the grace period is for.
func TestAHelloPutsAPetBackWhereItLastStood(t *testing.T) {
	stood := Point{X: 0.8, Y: 0.15}
	if stood == spawn {
		t.Fatal("the stored position is the spawn, so this test could not tell a restore from a default")
	}
	name := "Ваня"
	saved := time.Now().UTC().Add(-time.Hour)
	repo := &fakeRepo{
		pet: &Pet{
			ID: "pet-a", AccountID: accountOf("a"), Name: &name, SkinKey: SkinVanya,
			LocationKey: LocationYard, X: &stood.X, Y: &stood.Y, LastSeenAt: &saved,
		},
		rows: []StatRow{{Key: StatHP, Value: mustStat(t, StatHP).Max, AsOf: at(0)}},
	}
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	p, ok := peerOf(svc, tr.frames()[0], accountOf("a"))
	if !ok {
		t.Fatalf("the account that said hello is not on the plane: %+v", tr.frames()[0])
	}
	if p.X != stood.X || p.Y != stood.Y {
		t.Fatalf("peer at (%v,%v) on a fresh process; want where he was standing when he last left (%v,%v)",
			p.X, p.Y, stood.X, stood.Y)
	}
	// The same hello is what fills the cache, so the appearance arrives with the
	// position rather than a frame later.
	if p.Art != SkinVanya || p.Label != name {
		t.Errorf("peer drawn as %+v; want the skin and name the pet actually has", p)
	}

	// He walks somewhere else, and his socket drops and comes back inside the
	// grace. The hello must leave him where he is.
	walked := Point{X: 0.6, Y: 0.3}
	w := tap(t, svc, member("a"), walked)
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast once he has walked: %v", err)
	}
	tr.setMembers()
	if err := svc.broadcast(context.Background(), afterArriving(w).Add(time.Second)); err != nil {
		t.Fatalf("broadcast while the socket is away: %v", err)
	}
	tr.setMembers(member("a"))
	svc.HandleInbound(context.Background(), member("a"), testRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	if err := svc.broadcast(context.Background(), afterArriving(w).Add(2*time.Second)); err != nil {
		t.Fatalf("broadcast after the reconnect: %v", err)
	}

	frames := tr.frames()
	back, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if !ok {
		t.Fatalf("the reconnected account is missing from the roster: %+v", frames[len(frames)-1])
	}
	standingAt(t, back, walked,
		"the player who reconnected inside the grace — the stored position is older news than the walk he has just taken")
}

// waitForCount waits until the transport has seen n frames.
func waitForCount(t *testing.T, tr *fakeTransport, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(tr.frames()) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d published frames", n)
}

// TestAPositionNobodyChoseIsNotWrittenDown guards a data-loss bug that would be
// invisible until somebody complained.
//
// The hub registers a connection at the upgrade, BEFORE its client has said
// hello — so a tick can land in that gap and put a spawn point in the map for
// somebody whose stored position has not been read yet. If that provisional
// placement were persisted on departure, a player whose hello never arrived (an
// old cached client, a slow query, a dropped frame) would have the real position
// he left behind quietly overwritten with the middle of the yard. Nothing would
// error; he would just stop being where he was.
func TestAPositionNobodyChoseIsNotWrittenDown(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("drifter"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	// Seen by the plane, never moved, never said hello: placed at the spawn by
	// the broadcast alone.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	tr.setMembers()
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast after the departure: %v", err)
	}
	if n := len(queued(svc)); n != 0 {
		t.Fatalf("%d writes queued for a position nobody chose; want none", n)
	}

	// And the shutdown path agrees with the tick, which is the half that only
	// ever runs in production.
	svc.flushPositions()
	if saved := repo.saved(); len(saved) != 0 {
		t.Fatalf("the flush wrote %d provisional positions; want none: %+v", len(saved), saved)
	}
}

// TestAStoredPositionReplacesTheSpawnTheTickInvented is the other half of the
// same ordering problem, from the arriving side.
//
// A tick between the upgrade and the hello leaves a spawn point in the map. The
// stored position then arrives and MUST be allowed to replace it — treating the
// map as already-decided is what would make a returning Ваня teleport to the
// middle, which is the entire failure durable position exists to prevent.
func TestAStoredPositionReplacesTheSpawnTheTickInvented(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("returning"))
	acct := accountOf("returning")
	stood := Point{X: 0.15, Y: 0.85}
	repo := &fakeRepo{pet: &Pet{
		ID: "pet-1", AccountID: acct, SkinKey: SkinVanya, LocationKey: LocationYard,
		X: &stood.X, Y: &stood.Y,
	}}
	svc := planeService(tr, repo)

	// The gap: a frame is published before the client has said anything.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast before the hello: %v", err)
	}
	svc.HandleInbound(context.Background(), member("returning"), testRoom,
		[]byte(`{"t":"vanyagotchi_hello"}`))
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast after the hello: %v", err)
	}

	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], acct)
	if !ok {
		t.Fatal("the returning player is missing from the roster")
	}
	if p.X != stood.X || p.Y != stood.Y {
		t.Fatalf("returning peer at (%v,%v); want where he left off (%v,%v)", p.X, p.Y, stood.X, stood.Y)
	}
}

// TestRunWritesEverybodyDownOnTheWayOut is the shutdown path, driven through Run
// rather than by calling the flush directly — because calling it directly is
// exactly the thing that cannot fail.
//
// This is the half of durable position that ONLY happens in production. A deploy
// cancels the context the tick loop, the writer and every socket share; there is
// no further tick to notice that everybody has left, so without an explicit
// flush on the way out a restart would write nothing at all and the whole yard
// would come back standing in the middle. A test that only ever called
// flushPositions() would keep passing with that flush deleted from Run.
func TestRunWritesEverybodyDownOnTheWayOut(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("sleeper"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("sleeper"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.8}`))

	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		svc.Run(ctx, tick)
		close(done)
	}()

	// One frame, so the position is held and its owner is present.
	tick <- at(0)

	// The deploy.
	cancel()
	<-done

	saved := repo.saved()
	if len(saved) != 1 {
		t.Fatalf("shutdown wrote %d positions; want the one player standing in the yard: %+v", len(saved), saved)
	}
	if saved[0].accountID != accountOf("sleeper") {
		t.Fatalf("shutdown wrote down %q; want %q", saved[0].accountID, accountOf("sleeper"))
	}
	if saved[0].at.X != 0.2 || saved[0].at.Y != 0.8 {
		t.Fatalf("shutdown wrote (%v,%v); want where he was standing (0.2,0.8)", saved[0].at.X, saved[0].at.Y)
	}
}

// ---------------------------------------------------------------------------
// The yard's regulars: characters who are catalogue plus arithmetic, and who
// have no rows, no accounts and no placements.
// ---------------------------------------------------------------------------

// TestTheYardsRegularsAreInEveryFrame is what makes an NPC free.
//
// A character costs no migration and no client work because he is nothing but a
// catalogue entry and a closed-form position: the browser resolves his art
// exactly as it resolves a pet's skin, so to it he is one more entity in the
// roster. That is only true while the server actually publishes him in EVERY
// frame — a snapshot that left the furniture out on some ticks would flicker the
// whole cast, and the client has nothing to fill the gap with, because it is
// deliberately not told how anybody moves.
func TestTheYardsRegularsAreInEveryFrame(t *testing.T) {
	if len(catalogue.NPCs) == 0 {
		t.Fatal("the catalogue has no NPCs at all; the yard is an empty field and everything below asserts nothing")
	}
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	when := []time.Time{at(0), at(3), at(11)}
	for i, now := range when {
		if err := svc.broadcast(context.Background(), now); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
		f := tr.frames()[i]
		for _, npc := range catalogue.NPCs {
			p, ok := peerByID(f, npcID(npc.Key))
			if !ok {
				t.Fatalf("frame %d does not carry %q; the yard's regulars are in every frame or in none", i, npc.Key)
			}
			if p.Art != npc.Art || p.Label != npc.Label {
				t.Errorf("%q is drawn as %+v; want the catalogue's own art %q and label %q", npc.Key, p, npc.Art, npc.Label)
			}
			if p.Pose != PoseFine {
				t.Errorf("%q is drawn as %q; a character who is not a pet has no health to be poorly about", npc.Key, p.Pose)
			}
			if p.Say != "" {
				t.Errorf("%q says %q; nothing gives a regular a line yet", npc.Key, p.Say)
			}
			// The position is the pattern's own answer, evaluated from the FIXED
			// world epoch rather than from process start — which is what makes two
			// processes, and the same one after a deploy, agree about where he is.
			want := evaluate(npc.Pattern, npc.Params, now.Sub(worldEpoch))
			if p.X != want.X || p.Y != want.Y {
				t.Fatalf("frame %d puts %q at (%v,%v); his pattern says (%v,%v) at that instant",
					i, npc.Key, p.X, p.Y, want.X, want.Y)
			}
		}
	}

	// And they are alive rather than painted on: at instants minutes apart, at
	// least one of them has moved. A cast evaluated against a constant would
	// satisfy every assertion above.
	moves := false
	for _, npc := range catalogue.NPCs {
		if npc.Pattern != PatternIdle {
			moves = true
		}
	}
	if moves {
		stirred := false
		for _, npc := range catalogue.NPCs {
			first, _ := peerByID(tr.frames()[0], npcID(npc.Key))
			last, _ := peerByID(tr.frames()[len(when)-1], npcID(npc.Key))
			if first.X != last.X || first.Y != last.Y {
				stirred = true
			}
		}
		if !stirred {
			t.Fatal("no regular moved an inch over eleven minutes, though the catalogue says some of them wander; the cast is being evaluated against a clock that does not move")
		}
	}
}

// TestTheRegularsAreNotPeople is the other half of "they cost nothing", and it
// is the half that would break something real.
//
// They are not counted in the head count the client renders as «во дворе: N» —
// which is the whole reason that number is computed on the server rather than by
// counting entities in the browser. They acquire no placement, so nothing about
// them can be moved by a tap or expire with a grace period. And above all they
// are never queued for a write: an NPC has no account and no pet row, so a save
// keyed on one would be an UPDATE matching nothing, five times a second, forever.
func TestTheRegularsAreNotPeople(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]
	if inTheYard(f) != 1 {
		t.Fatalf("the frame says %d people are in the yard; one socket is open and the rest of the cast is furniture: %+v", inTheYard(f), f)
	}
	if len(f.Peers) != 1+len(catalogue.NPCs) {
		t.Fatalf("the frame carries %d entities; want the one player plus the %d regulars: %+v",
			len(f.Peers), len(catalogue.NPCs), f)
	}

	// Nothing about them is in the position map, before or after they have been
	// drawn a few times.
	w := tap(t, svc, member("a"), Point{X: 0.7, Y: 0.4})
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	svc.mu.Lock()
	held := make([]string, 0, len(svc.pos))
	for id := range svc.pos {
		held = append(held, id)
	}
	svc.mu.Unlock()
	if len(held) != 1 || held[0] != accountOf("a") {
		t.Fatalf("the plane holds placements for %v; want only the one real account", held)
	}

	// And when the player goes, only the player is written down.
	tr.setMembers(member("watching"))
	if err := svc.broadcast(context.Background(), afterArriving(w).Add(time.Second)); err != nil {
		t.Fatalf("broadcast after the departure: %v", err)
	}
	saves := queued(svc)
	if len(saves) != 1 {
		t.Fatalf("%d writes queued for one departure; want exactly 1: %+v", len(saves), saves)
	}
	for _, sv := range saves {
		if sv.accountID != accountOf("a") {
			t.Fatalf("a write was queued for %q, which is not a person's account", sv.accountID)
		}
	}
}

// ---------------------------------------------------------------------------
// The sleepers: what makes the yard a place rather than a menu.
//
// With five to thirty friends it is almost never occupied by two people at once,
// so without them a solo visit is an empty field — and filler characters would
// have been a worse answer than the real ones, who are all still lying about in
// it.
// ---------------------------------------------------------------------------

// TestAnAbsentPlayerLiesDownInTheYardWhereHeStood is the whole feature in one
// test, and the three stages have to be distinguishable.
//
// While he is connected he is a person. For the grace period after his socket
// goes he is nobody — absent, not a ghost, because a reload looks exactly like
// this and drawing him asleep for two minutes every time somebody refreshed the
// page would be worse than drawing nothing. Past it he is furniture: still in the
// frame, lying exactly where he stood, and NOT counted among the people in the
// yard.
func TestAnAbsentPlayerLiesDownInTheYardWhereHeStood(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("goes"), member("stays"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	stood := Point{X: 0.75, Y: 0.3}
	w := tap(t, svc, member("goes"), stood)
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	gone := afterArriving(w)

	tr.setMembers(member("stays"))
	if err := svc.broadcast(context.Background(), gone.Add(PositionGrace/2)); err != nil {
		t.Fatalf("broadcast inside the grace: %v", err)
	}
	inside := tr.frames()[2]
	if _, ok := peerOf(svc, inside, accountOf("goes")); ok {
		t.Fatalf("he is lying in the yard a minute after his socket dropped; a reload looks exactly like this: %+v", inside)
	}

	if err := svc.broadcast(context.Background(), gone.Add(PositionGrace+time.Second)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}
	past := tr.frames()[3]
	asleep, ok := peerOf(svc, past, accountOf("goes"))
	if !ok {
		t.Fatalf("nobody is asleep in the yard past the grace; the field is bare again: %+v", past)
	}
	standingAt(t, asleep, stood, "the sleeper")
	if asleep.Pose != PoseAsleep {
		t.Fatalf("he is drawn as %q; want %q — he is not here, he is asleep where he stood", asleep.Pose, PoseAsleep)
	}
	if asleep.Say != "" {
		t.Errorf("a sleeper says %q; nobody talks in their sleep", asleep.Say)
	}
	if inTheYard(past) != 1 {
		t.Fatalf("the frame says %d are in the yard; the count is PEOPLE, and he went home: %+v", inTheYard(past), past)
	}
	if n := len(withoutNPCs(past)); n != 2 {
		t.Fatalf("%d entities that are not regulars; want the one player and the one sleeper: %+v", n, past)
	}
}

// TestADeadSleeperIsDrawnDeadRatherThanAsleep is the one thing that outranks
// sleep.
//
// A dead Ваня drawn peacefully asleep would hide the single thing his owner
// needs to come back for, and death in this game is recoverable precisely so
// that somebody comes back and fixes it. The pose is derived from the cache the
// same way a present player's is, so it keeps working for somebody who has not
// been connected for a week.
func TestADeadSleeperIsDrawnDeadRatherThanAsleep(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("dead"), member("stays"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	died := at(-1)
	svc.remember(accountOf("dead"), Pet{SkinKey: SkinVanya, DiedAt: &died}, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	w := tap(t, svc, member("dead"), Point{X: 0.4, Y: 0.65})
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	gone := afterArriving(w)

	tr.setMembers(member("stays"))
	if err := svc.broadcast(context.Background(), gone.Add(PositionGrace+time.Second)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}

	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], accountOf("dead"))
	if !ok {
		t.Fatalf("the dead pet is not lying in the yard at all: %+v", frames[len(frames)-1])
	}
	if p.Pose != PoseDead {
		t.Fatalf("a dead Ваня whose owner has gone is drawn as %q; want %q — asleep would hide the one thing worth coming back for",
			p.Pose, PoseDead)
	}
}

// TestSomebodyWhoNeverStoodAnywhereDoesNotLieDown is the sleeper rule's other
// edge, and it is the one that would put a body in the yard that never had one.
//
// A tick can place an account at the spawn before its client has said hello —
// the hub registers a connection at the upgrade — so the map fills with
// positions nobody chose. Laying those down would furnish the yard with people
// who merely opened a socket once, at the exact middle of it, and would keep the
// map growing for the life of the process.
func TestSomebodyWhoNeverStoodAnywhereDoesNotLieDown(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("drifter"), member("stays"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	// Seen by the plane, never moved, never said hello.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	tr.setMembers(member("stays"))
	if err := svc.broadcast(context.Background(), at(0).Add(PositionGrace+time.Second)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}

	past := tr.frames()[1]
	if _, ok := peerOf(svc, past, accountOf("drifter")); ok {
		t.Fatalf("somebody who never stood anywhere is lying in the yard: %+v", past)
	}
	if n := len(withoutNPCs(past)); n != 1 {
		t.Fatalf("%d entities that are not regulars; want only the player who is still here: %+v", n, past)
	}
}

// TestTheYardShowsTheMostRecentSleepersAndNoMore bounds the frame.
//
// Without the cap every pet who ever played would be in every frame forever, so
// the roster would grow with the AGE of the game rather than with the size of
// the group — thirty players a second each carrying a hundred bodies. And the
// ones kept have to be the most recent, or the cap would fill the yard with
// whoever the map happened to iterate first and quietly hide the people who were
// actually here this afternoon.
func TestTheYardShowsTheMostRecentSleepersAndNoMore(t *testing.T) {
	tr := &fakeTransport{}
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	const extra = 5
	total := sleeperLimit + extra
	accounts := make([]string, total)
	for i := range accounts {
		accounts[i] = fmt.Sprintf("acct-sleeper-%03d", i)
		// Each of them stands somewhere of his own and is seen at an instant of
		// his own, oldest first, so "the newest thirty" is a set this test can
		// name.
		standAt(svc, accounts[i], Point{X: float64(i) / float64(2*total), Y: 0.5})
		tr.setMembers(conn(fmt.Sprintf("c-%03d", i), accounts[i]))
		if err := svc.broadcast(context.Background(), at(0).Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
	}

	// Everybody has gone but one person, who is there to be published to: the
	// yard tells nobody about anything when nobody is in it.
	watcher := conn("watcher", "acct-watcher")
	tr.setMembers(watcher)
	past := at(0).Add(time.Duration(total)*time.Second + PositionGrace + time.Second)
	if err := svc.broadcast(context.Background(), past); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}

	frames := tr.frames()
	last := frames[len(frames)-1]
	var sleepers []Peer
	for _, p := range withoutNPCs(last) {
		if p.ID == svc.pseudonym("acct-watcher") {
			continue
		}
		sleepers = append(sleepers, p)
	}
	if len(sleepers) != sleeperLimit {
		t.Fatalf("%d sleepers in the frame; the cap is %d and %d accounts are asleep", len(sleepers), sleeperLimit, total)
	}

	// The newest first, and the oldest not at all.
	for i, p := range sleepers {
		want := accounts[total-1-i]
		if p.ID != svc.pseudonym(want) {
			t.Fatalf("sleeper %d is %q; want %q — they are sorted newest first, so the cap keeps the people who were actually here recently",
				i, p.ID, want)
		}
		if p.Pose != PoseAsleep {
			t.Errorf("sleeper %d is drawn as %q; want %q", i, p.Pose, PoseAsleep)
		}
	}
	for _, dropped := range accounts[:extra] {
		if _, ok := peerOf(svc, last, dropped); ok {
			t.Fatalf("%q is in the frame; he is one of the %d oldest and the cap should have left him out", dropped, extra)
		}
	}
}

// TestAnEmptyYardPublishesNothingHoweverManyAreAsleepInIt keeps the common case
// free, and the sleepers are what made it worth restating.
//
// The yard is now never empty of ENTITIES — the regulars are always in it and
// the sleepers usually are — so a frame is never small. But nobody is connected
// for most of the day, and publishing a full roster into a room with no sockets
// in it five times a second is bandwidth spent on nobody, and it would hide a
// genuine "why is this room empty" question behind traffic.
func TestAnEmptyYardPublishesNothingHoweverManyAreAsleepInIt(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	w := tap(t, svc, member("a"), Point{X: 0.3, Y: 0.6})
	if err := svc.broadcast(context.Background(), afterArriving(w)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	published := len(tr.frames())

	// He goes, and the yard runs on with him asleep in it and nobody watching.
	tr.setMembers()
	gone := afterArriving(w)
	for i := 1; i <= 5; i++ {
		when := gone.Add(PositionGrace + time.Duration(i)*time.Second)
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast into the empty yard: %v", err)
		}
	}
	if n := len(tr.frames()); n != published {
		t.Fatalf("%d frames were published into a yard with nobody in it; want none beyond the %d from when somebody was", n-published, published)
	}

	// And the sleeper really is there to be published — the yard is quiet, not
	// empty, so this test is about who is listening rather than about who is in it.
	svc.mu.Lock()
	_, kept := svc.pos[accountOf("a")]
	svc.mu.Unlock()
	if !kept {
		t.Fatal("the sleeper was forgotten, so the assertion above is about an empty yard rather than about an unwatched one")
	}
}

// ---------------------------------------------------------------------------
// The yard has a voice.
//
// Two kinds of balloon, and they are decided in two different places for the
// same reason: one event gets one answer, and everybody has to get the same one.
// A giving-up line is chosen with the walk, at accept time, and carried on it.
// An idle remark is chosen by hashing (account, slot) — a pure function of
// absolute time, so it needs no timer, stores nothing, expires by arithmetic,
// and two people watching the same Ваня read the same words at the same moment.
// ---------------------------------------------------------------------------

// muttering is the first instant at or after `from` at which this account is
// saying something to itself, and what it says.
//
// A search rather than a fixture, because the phrase key is minted from
// crypto/rand per process: which slots produce a remark cannot be known from
// outside, only found. At idleChance a run of a few hundred silent slots is
// already beyond improbable, so the cap is a bug detector rather than a limit.
func muttering(t *testing.T, svc *Service, accountID string, from time.Time) (time.Time, string) {
	t.Helper()
	const slots = 2000
	for i := 0; i < slots; i++ {
		when := from.Add(time.Duration(i) * idlePeriod)
		if say := svc.idleSay(accountID, when); say != "" {
			return when, say
		}
	}
	t.Fatalf("%d consecutive slots produced no remark at a chance of %v; the muttering is unreachable rather than merely quiet",
		slots, idleChance)
	return time.Time{}, ""
}

// slotStart is an instant aligned to the start of an idle slot, an hour into the
// world. Alignment matters: every assertion below is about WHERE in a slot an
// instant falls, so a base at some arbitrary offset would make "past the window"
// mean something different each run.
func slotStart(t *testing.T) time.Time {
	t.Helper()
	base := worldEpoch.Add(time.Hour)
	if (base.Sub(worldEpoch))%idlePeriod != 0 {
		t.Fatalf("the base instant is not slot-aligned; idlePeriod %v no longer divides an hour and these tests measure from the wrong place", idlePeriod)
	}
	return base
}

// TestAВаняStandingAboutTalksToHimself is the ambient noise, and the whole point
// of it: a yard is mostly people doing nothing, so without this it is a still
// life. The remark has to reach the frame, come from the pool, and be the same
// one the arithmetic says it is.
func TestAВаняStandingAboutTalksToHimself(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	base := slotStart(t)
	if err := svc.broadcast(context.Background(), base); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	// He has never walked anywhere, which is the case that matters: this is what
	// almost everybody in the yard is doing almost all of the time.
	when, say := muttering(t, svc, accountOf("a"), base)

	if err := svc.broadcast(context.Background(), when); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if !ok {
		t.Fatalf("the muttering player is not in the frame: %+v", frames[len(frames)-1])
	}
	if p.Say != say {
		t.Fatalf("the frame carries %q but the arithmetic says %q; the balloon in the yard is not the one the server decided on", p.Say, say)
	}
	if !slices.Contains(idleSays, p.Say) {
		t.Fatalf("he says %q, which is in neither pool; a line reaching the yard from outside the catalogue is content nobody wrote", p.Say)
	}
	if isTiredLine(p.Say) {
		t.Fatalf("a Ваня who has not walked anywhere says %q, which is a complaint about walking", p.Say)
	}
}

// TestTheMutteringStopsWithoutAnythingStoppingIt pins the shape that makes it
// free: the remark is up for the first idleSayFor of its slot and then is not,
// and nothing anywhere had to remember to remove it.
func TestTheMutteringStopsWithoutAnythingStoppingIt(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)
	acct := accountOf("a")

	base := slotStart(t)
	when, say := muttering(t, svc, acct, base)

	for _, tc := range []struct {
		name string
		when time.Time
		want string
		why  string
	}{
		{
			name: "the moment it starts", when: when, want: say,
			why: "the slot's remark is up from its first instant",
		},
		{
			name: "the last instant it is up", when: when.Add(idleSayFor - time.Millisecond), want: say,
			why: "the same slot is the same remark throughout — it must not riffle through the pool at 5 Hz while it is shown",
		},
		{
			name: "the instant the window closes", when: when.Add(idleSayFor), want: "",
			why: "it expires by arithmetic, so there is no timer to fire and nothing to clean up",
		},
		{
			name: "later in the same slot", when: when.Add(idlePeriod - time.Millisecond), want: "",
			why: "at most one remark per slot, however often the frame is drawn",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := svc.idleSay(acct, tc.when); got != tc.want {
				t.Fatalf("he says %q; want %q — %s", got, tc.want, tc.why)
			}
		})
	}
}

// TestEverybodyHearsTheSameMutteringAtTheSameMoment is the property that makes
// this shared rather than decorative. Two clients are two evaluations of the
// same function, so the words and their timing agree without a message being
// sent to say so — which is exactly why the client is never allowed to pick one.
func TestEverybodyHearsTheSameMutteringAtTheSameMoment(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("watching"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	base := slotStart(t)
	when, say := muttering(t, svc, accountOf("a"), base)

	// Drawn twice at the same instant, as two clients reading one broadcast are.
	for i := 0; i < 2; i++ {
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
	}
	frames := tr.frames()
	first, ok := peerOf(svc, frames[len(frames)-2], accountOf("a"))
	if !ok {
		t.Fatal("the muttering player is missing from the first of the two frames")
	}
	second, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if !ok {
		t.Fatal("the muttering player is missing from the second of the two frames")
	}
	if first.Say != say || second.Say != say {
		t.Fatalf("the same instant produced %q and %q; want %q both times — a remark that differs between two readings of one moment is one the clients would disagree about",
			first.Say, second.Say, say)
	}
}

// TestOnlyAВаняWhoIsStandingStillSaysAnything is the gate, and each case is a
// separate reason.
func TestOnlyAВаняWhoIsStandingStillSaysAnything(t *testing.T) {
	base := slotStart(t)

	t.Run("a walker keeps his breath", func(t *testing.T) {
		tr := &fakeTransport{}
		tr.setMembers(member("a"))
		svc := NewService(tr, testRoom, nil, nil, nil, nil)
		acct := accountOf("a")

		// An instant at which he WOULD be muttering if he were standing there,
		// so this proves the walk suppresses it rather than that the slot is a
		// quiet one.
		when, say := muttering(t, svc, acct, base)
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		// A short hop, so it can never be refused and turn into a complaint.
		w := tap(t, svc, member("a"), Point{X: spawn.X + 0.2, Y: spawn.Y})
		mid := partWayThrough(w)
		if svc.idleSay(acct, mid) == "" {
			t.Skip("the instant half way along this walk is a quiet slot, so there is nothing to suppress")
		}
		if err := svc.broadcast(context.Background(), mid); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		frames := tr.frames()
		p, ok := peerOf(svc, frames[len(frames)-1], acct)
		if !ok {
			t.Fatal("the walker is not in the frame")
		}
		if p.Say != "" {
			t.Fatalf("mid-walk he says %q (the slot's line is %q); a Ваня narrating his own journey reads as a status message, and the joke is that this is not one",
				p.Say, say)
		}
	})

	t.Run("a dead Ваня has nothing to add", func(t *testing.T) {
		tr := &fakeTransport{}
		tr.setMembers(member("a"))
		svc := NewService(tr, testRoom, nil, nil, nil, nil)
		acct := accountOf("a")

		died := base.Add(-time.Hour)
		svc.remember(acct, Pet{SkinKey: SkinVanya, DiedAt: &died}, nil)

		when, say := muttering(t, svc, acct, base)
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		frames := tr.frames()
		p, ok := peerOf(svc, frames[len(frames)-1], acct)
		if !ok {
			t.Fatal("the dead player is not in the frame")
		}
		if p.Pose != PoseDead {
			t.Fatalf("pose is %q; this test asserts nothing unless he is actually dead", p.Pose)
		}
		if p.Say != "" {
			t.Fatalf("a dead Ваня says %q (the slot's line is %q)", p.Say, say)
		}
	})

	t.Run("a sleeper sleeps", func(t *testing.T) {
		tr := &fakeTransport{}
		tr.setMembers(member("a"))
		svc := NewService(tr, testRoom, nil, nil, nil, nil)
		acct := accountOf("a")

		if err := svc.broadcast(context.Background(), base); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		standAt(svc, acct, Point{X: 0.3, Y: 0.6})
		// He leaves, but somebody stays: an empty room publishes no frame at
		// all, so without a watcher the assertion below would read the last
		// frame from when he was still standing here awake.
		tr.setMembers(member("watching"))

		// Past the grace, so he is lying in the yard rather than merely absent —
		// and at an instant his slot would otherwise have him talking.
		when, say := muttering(t, svc, acct, base.Add(PositionGrace+idlePeriod))
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		frames := tr.frames()
		p, ok := peerOf(svc, frames[len(frames)-1], acct)
		if !ok {
			t.Fatalf("the sleeper is not in the frame: %+v", frames[len(frames)-1])
		}
		if p.Pose != PoseAsleep {
			t.Fatalf("pose is %q; this test asserts nothing unless he is asleep", p.Pose)
		}
		if p.Say != "" {
			t.Fatalf("a sleeping Ваня says %q (the slot's line is %q); he is not awake to say it", p.Say, say)
		}
	})

	t.Run("the regulars are silent", func(t *testing.T) {
		tr := &fakeTransport{}
		tr.setMembers(member("a"))
		svc := NewService(tr, testRoom, nil, nil, nil, nil)

		// A SWEEP rather than one instant, and that is the whole strength of
		// this case. Checking a single moment caught a talking NPC only about
		// half the time — a quarter chance per character per slot — so it passed
		// against a service deliberately broken to give the cast balloons.
		// Across this many slots every plausible key speaks dozens of times,
		// whichever handle an accidental implementation happened to hash.
		const slots = 150
		for i := 0; i < slots; i++ {
			when := base.Add(time.Duration(i) * idlePeriod)
			if err := svc.broadcast(context.Background(), when); err != nil {
				t.Fatalf("broadcast: %v", err)
			}
			frames := tr.frames()
			last := frames[len(frames)-1]
			for _, npc := range catalogue.NPCs {
				p, ok := peerByID(last, npcID(npc.Key))
				if !ok {
					t.Fatalf("%q is not in the frame", npc.Key)
				}
				if p.Say != "" {
					t.Fatalf("%q says %q at slot %d; the muttering is the players' and giving it to the cast would make the yard a chatroom",
						npc.Key, p.Say, i)
				}
			}
		}
		// And the sweep really did cover slots in which somebody talks, or a
		// silent cast proves nothing.
		talking := 0
		for i := 0; i < slots; i++ {
			if svc.idleSay(accountOf("a"), base.Add(time.Duration(i)*idlePeriod)) != "" {
				talking++
			}
		}
		if talking == 0 {
			t.Fatalf("none of the %d swept slots produces a remark even for a player, so the silence above is not evidence of anything", slots)
		}
	})
}

// TestTheComplaintIsChosenOnceAndCarriedOnTheWalk. The alternative — choosing it
// when the frame is built — would riffle through ten lines at 5 Hz for the four
// seconds he sat there, and two clients drawing the same moment could disagree.
func TestTheComplaintIsChosenOnceAndCarriedOnTheWalk(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	w := tapUntilHeGivesUp(t, svc, member("a"), Point{X: 1, Y: 1})
	if !isTiredLine(w.say) {
		t.Fatalf("the walk carries %q, which is not one of the giving-up lines", w.say)
	}

	// Every tick of the window he is sitting there says the same thing.
	sat := w.stoppedAt()
	for _, d := range []time.Duration{time.Millisecond, tiredFor / 4, tiredFor / 2, tiredFor - time.Millisecond} {
		when := sat.Add(d)
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
		frames := tr.frames()
		p, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
		if !ok {
			t.Fatal("the tired player is not in the frame")
		}
		if p.Say != w.say {
			t.Fatalf("%v after sitting down he says %q; want the line chosen with the walk, %q", d, p.Say, w.say)
		}
	}
}

// TestThePhrasePoolsAreDisjoint. Several tests read "he said a line from pool X"
// as evidence that X is what happened to him, which is only sound while no line
// is in two pools. A single word landing in both would make those tests quietly
// stop discriminating instead of failing.
//
// Three pools now: giving up on a walk, muttering while standing about, and
// losing his nerve. The third is the one that most needs the guarantee, because
// «я тут стесняюсь» is the ONLY evidence a test has that a press was refused for
// nerves rather than for any of the reasons that are the player's fault.
func TestThePhrasePoolsAreDisjoint(t *testing.T) {
	pools := map[string][]string{"tiredSays": tiredSays, "idleSays": idleSays, "shySays": shySays}
	for name, pool := range pools {
		if len(pool) == 0 {
			t.Fatalf("%s is empty, so every balloon assertion resting on it is vacuous", name)
		}
		seen := make(map[string]bool, len(pool))
		for _, line := range pool {
			if seen[line] {
				t.Fatalf("%q appears twice in %s, so it is twice as likely as every other line for no stated reason", line, name)
			}
			seen[line] = true
		}
	}
	for name, pool := range pools {
		for other, against := range pools {
			if name >= other {
				continue // each pair once, and never a pool against itself
			}
			for _, line := range pool {
				if slices.Contains(against, line) {
					t.Fatalf("%q is in both %s and %s; a balloon can no longer say which of the two happened to him", line, name, other)
				}
			}
		}
	}
}

// TestPickingALineCannotRunOffTheEndOfThePool. The draw is a float in a CLOSED
// interval, so exactly 1.0 is a value it can take — and int(1.0 × len) is one
// past the last line.
func TestPickingALineCannotRunOffTheEndOfThePool(t *testing.T) {
	pool := []string{"a", "b", "c"}
	for _, at := range []float64{0, 0.333, 0.5, 0.999, 1} {
		got := pickPhrase(pool, at)
		if !slices.Contains(pool, got) {
			t.Fatalf("a draw of %v picked %q, which is not in the pool", at, got)
		}
	}
	if got := pickPhrase(nil, 0.5); got != "" {
		t.Fatalf("an empty pool produced %q; want silence rather than a panic or a placeholder", got)
	}
	// Every line is reachable, or the pool is smaller than it looks.
	seen := make(map[string]bool, len(pool))
	for i := 0; i <= 1000; i++ {
		seen[pickPhrase(pool, float64(i)/1000)] = true
	}
	if len(seen) != len(pool) {
		t.Fatalf("an even sweep of the draw reached %d of %d lines; some are unreachable", len(seen), len(pool))
	}
}

// ---------------------------------------------------------------------------
// Verbs over the socket. The wire owes no reply, so what a player is told
// arrives as STATE — a line over their own Ваня that the whole yard can see.
// ---------------------------------------------------------------------------

// TestWhatTheServerSaysOutranksTheYardsOwnChatter. A refusal or a confirmation
// is an answer to something the player just did, and both of the other lines —
// the tiredness complaint and the idle muttering — are ambient. Losing an answer
// behind ambience would make the socket's lack of a reply channel actually cost
// something.
func TestWhatTheServerSaysOutranksTheYardsOwnChatter(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)
	acct := accountOf("a")

	base := slotStart(t)
	if err := svc.broadcast(context.Background(), base); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// An instant at which he WOULD be muttering, so this proves the server's
	// line wins rather than that the slot happened to be quiet.
	when, idle := muttering(t, svc, acct, base)
	if err := svc.broadcast(context.Background(), when); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	svc.Say(acct, "он не встаёт", sayFor)

	if err := svc.broadcast(context.Background(), when); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], acct)
	if !ok {
		t.Fatal("the player is not in the frame")
	}
	if p.Say != "он не встаёт" {
		t.Fatalf("he says %q; want the server's answer, not the slot's line %q", p.Say, idle)
	}

	// And it expires by arithmetic, with nothing scheduled to remove it.
	if err := svc.broadcast(context.Background(), when.Add(sayFor)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames = tr.frames()
	p, _ = peerOf(svc, frames[len(frames)-1], acct)
	if p.Say == "он не встаёт" {
		t.Fatal("the answer is still up past its window; it expires by comparison, so nothing should have to clear it")
	}
}

// TestALineIsCappedToWhatTheClientWillRender. The browser caps by code point, so
// a longer line would be silently shortened on the way in — and a player reading
// half a sentence is worse served than by one written to fit.
func TestALineIsCappedToWhatTheClientWillRender(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)
	acct := accountOf("a")
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	long := "это очень длинная фраза которую никто не станет читать целиком"
	svc.Say(acct, long, sayFor)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	p, _ := peerOf(svc, frames[len(frames)-1], acct)
	if n := len([]rune(p.Say)); n > sayMax {
		t.Fatalf("the line is %d code points; the client renders at most %d", n, sayMax)
	}
	if p.Say == "" {
		t.Fatal("capping removed the line entirely")
	}
}

// TestSayingSomethingToNobodyDrawsNobody. A verb can arrive from a client with
// an HTTP session and no place in the yard, and hanging a balloon on that would
// put a Ваня on the plane who never walked into it.
func TestSayingSomethingToNobodyDrawsNobody(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("watching"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)

	svc.Say(accountOf("absent"), "приветик", sayFor)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	if _, ok := peerOf(svc, frames[len(frames)-1], accountOf("absent")); ok {
		t.Fatal("an account with no placement was drawn into the yard by being spoken to")
	}
}

// TestVerbsAreRateLimitedPerAccount. The socket's own limit is ten frames a
// second, which is right for taps because a tap writes nothing. A verb writes a
// transaction, so it needs a bound of its own.
func TestVerbsAreRateLimitedPerAccount(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)
	now := at(0)

	if !svc.allowVerb(accountOf("a"), now) {
		t.Fatal("the first batch was refused")
	}
	if svc.allowVerb(accountOf("a"), now.Add(verbInterval/2)) {
		t.Fatal("a second batch inside the interval was accepted")
	}
	if !svc.allowVerb(accountOf("a"), now.Add(verbInterval)) {
		t.Fatal("a batch after the interval was refused")
	}
	// And the limit is per ACCOUNT, so one player hammering does not lock out
	// anybody else.
	if !svc.allowVerb(accountOf("b"), now) {
		t.Fatal("one account's rate limit refused another account")
	}
}

// TestADeadВаняStillCarriesWhatTheServerSaid pins the ordering bug this cost.
//
// The dead branch used to come first, so a corpse said nothing at all — and the
// one moment a player most needs to be told «он не встаёт» is exactly the moment
// his Ваня is dead and a verb has just been refused. A refusal is the server
// talking to the owner, not the Ваня talking, so being dead does not disqualify
// him from carrying it. Found by a full-stack test, pinned here where it is
// cheap.
func TestADeadВаняStillCarriesWhatTheServerSaid(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)
	acct := accountOf("a")

	died := at(0).Add(-time.Hour)
	svc.remember(acct, Pet{SkinKey: SkinVanya, DiedAt: &died}, nil)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	svc.Say(acct, "он не встаёт", sayFor)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], acct)
	if !ok {
		t.Fatal("the dead player is not in the frame")
	}
	if p.Pose != PoseDead {
		t.Fatalf("pose is %q; this test asserts nothing unless he is actually dead", p.Pose)
	}
	if p.Say != "он не встаёт" {
		t.Fatalf("a dead Ваня says %q; the server's answer to a refused verb must reach the player who pressed it", p.Say)
	}
}

// TestAVerbHeIsNotReadyForIsRefusedOverHisOwnHead is the gate as the person who
// pressed the button experiences it, driven down the one path a client has.
//
// The wire owes no reply — a verb frame is answered by the next full-state frame
// and never by an acknowledgement — so «рано ещё» arriving as a line over his own
// Ваня IS the answer, and it is the only one there is. The client greys the
// button out when the served config says he is short, which means a player should
// meet this rarely; it exists because a greyed button is a suggestion, and this
// test is what says the suggestion is backed by something.
//
// The two halves belong together: the balloon has to appear AND the press has to
// have written nothing, because a refusal that quietly charged him a re-stamp
// while apologising politely is the worst of both.
func TestAVerbHeIsNotReadyForIsRefusedOverHisOwnHead(t *testing.T) {
	relieve := mustAction(t, ActionRelieve)
	if relieve.NeedsStat == "" {
		t.Fatalf("the catalogue no longer gates %q on a stat of its own; this test is about a rule the game does not have", relieve.Key)
	}
	// A pet with nothing in it, which is the state every pet is in on the first
	// evening — and the reason this refusal is worth a sentence at all.
	repo := playedFor()
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, repo)

	// A tick first: somebody the yard has never placed has nowhere to wear a
	// line, so without it the balloon would go nowhere and the test would be
	// asserting the placement rather than the refusal.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	svc.HandleInbound(context.Background(), member("1"), testRoom,
		fmt.Appendf(nil, `{"t":%q,"verbs":[%q]}`, TypeDo, relieve.Key))

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast after the refusal: %v", err)
	}
	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], testAccount)
	if !ok {
		t.Fatalf("the player who pressed the button is not in the frame at all: %+v", frames[len(frames)-1])
	}
	if p.Say != "рано ещё" {
		t.Errorf("his Ваня says %q after a verb he is not ready for; want «рано ещё» — the balloon is the whole of the server's answer to a refused press", p.Say)
	}
	if p.Say == relieve.Done {
		t.Errorf("his Ваня announced %q, which is what the catalogue says the verb says when it LANDS; the refusal took the success path", p.Say)
	}

	// And nothing happened to the pet. Said here as well as in the pet's own
	// tests because this is the path a player actually takes, and a refusal that
	// still wrote would be invisible in the frame above.
	if n := len(repo.appended); n != 0 {
		t.Errorf("%d events were recorded for a verb that was refused: %+v", n, repo.appended)
	}
	if repo.writes != 0 {
		t.Errorf("WriteStats was called %d times by a refused verb; want none", repo.writes)
	}
	if got := repo.insertedObjects(); len(got) != 0 {
		t.Errorf("%d objects were left in the world by a verb that was refused: %+v", len(got), got)
	}
}

// TestRefusalLineSaysTheRightThingOrDeliberatelyNothing pins the whole mapping
// from a refusal to what the player reads.
//
// Two of these rows are asserting SILENCE, which is the half a well-meaning
// future edit is most likely to break: a refusal nobody can act on is worse than
// no message, because it tells a player something went wrong while giving him
// nothing to do about it. An unknown verb can only come from a client that
// invented one, and an unknown stat is our own catalogue disagreeing with itself
// — in neither case is there a button that would help.
//
// The asymmetry between those two is that only the second is OUR bug, so only
// the second is logged. That is not asserted here (a log line is not this
// function's return value) and is stated in the branch's own comment.
func TestRefusalLineSaysTheRightThingOrDeliberatelyNothing(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"a dead pet is the one refusal a player must read", ErrPetDead, "он не встаёт"},
		{"nothing to go for yet", ErrNotYet, "рано ещё"},
		{"somebody else got to the keys first", ErrClaimLost, "кто-то успел раньше"},
		// THE THREE A SEARCH CAN PRODUCE, and they are three sentences because
		// they ask the player for three different things: name a place, walk to
		// it, try somewhere else. The strings are pinned rather than derived
		// because the client branches on them — a change here is a wire change.
		{"a search naming no place at all", ErrNoSpot, "где искать-то"},
		{"a place he has not walked to", ErrTooFar, "далековато"},
		{"a place with nothing in it", ErrNothingHere, "тут пусто"},
		{"a crate somebody else emptied", ErrOutOfStock, "пиво кончилось"},
		{"too many verbs at once", ErrBatchTooLong, "не части"},
		{"a verb outside the catalogue is answered with silence", ErrUnknownAction, ""},
		{"a catalogue that disagrees with itself is answered with silence", ErrUnknownStat, ""},
		{"anything unexpected is honest and vague", errors.New("boom"), "чёт не вышло"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Wrapped, because Do wraps: the real errors arrive with the verb
			// that caused them fmt.Errorf'd around the sentinel, so a mapping
			// written with == instead of errors.Is would pass an unwrapped test
			// and fail in production.
			if got := refusalLine(context.Background(), fmt.Errorf("verb %q: %w", "выпить", c.err)); got != c.want {
				t.Fatalf("refusalLine(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// fakeProfiles is an account service that knows one thing.
type fakeProfiles struct {
	url string
	err error
	// calls counts how often the yard asked, which is the whole point of the
	// cache: a tick that reached this would show up here as hundreds.
	calls int
}

func (f *fakeProfiles) AvatarURL(context.Context, string) (string, error) {
	f.calls++
	return f.url, f.err
}

// TestAВаняWearsHisOwnersFace is the avatar reaching the wire at all.
func TestAВаняWearsHisOwnersFace(t *testing.T) {
	const face = "https://example.invalid/face.jpg"
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)
	withFace, without := accountOf("a"), accountOf("b")

	svc.remember(withFace, Pet{SkinKey: SkinVanya}, nil)
	svc.remember(without, Pet{SkinKey: SkinVanya}, nil)
	svc.setAvatar(withFace, face)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// NOT on the frame — the roster carries no URL at all, deliberately. It is
	// looked up by the handle the roster published, which is what keeps a
	// durable identifier off a frame whose identity is per-process.
	frames := tr.frames()
	last := frames[len(frames)-1]
	if _, ok := peerOf(svc, last, withFace); !ok {
		t.Fatal("the player with a face is not in the frame")
	}
	// Marshalled and searched, rather than trusting that the struct has no such
	// field: this is the assertion that the URL never reaches the wire, and it
	// should keep failing if somebody adds one back.
	raw, err := json.Marshal(last)
	if err != nil {
		t.Fatalf("marshal roster: %v", err)
	}
	if strings.Contains(string(raw), face) {
		t.Fatalf("the roster frame carries the avatar URL itself: %s", raw)
	}

	got, ok := svc.AvatarFor(svc.pseudonym(withFace))
	if !ok || got != face {
		t.Fatalf("AvatarFor his handle = (%q, %v), want (%q, true)", got, ok, face)
	}
	// And it is HIS, not the yard's. A face leaking onto everybody would be a
	// worse bug than none at all, and one a single-player test cannot see.
	if url, ok := svc.AvatarFor(svc.pseudonym(without)); ok {
		t.Fatalf("a player with no avatar was given %q", url)
	}
	// A handle nobody in the yard has is an ordinary miss, which is the answer
	// every NPC gets.
	if _, ok := svc.AvatarFor("not-a-handle"); ok {
		t.Fatal("an unknown handle was matched to somebody's face")
	}
}

// TestAVerbDoesNotBlankTheFaceOnThePlane pins the carry-over in remember.
//
// The trap it exists for: `remember` rebuilds a display entry from a PET, and it
// is called by every verb and every HTTP read — none of which fetches an
// account. Without the carry-over, pressing a button would strip your own face
// off the plane until your next hello, which is a page reload away. It would
// look like an intermittent rendering bug and would be very hard to attribute.
func TestAVerbDoesNotBlankTheFaceOnThePlane(t *testing.T) {
	const face = "https://example.invalid/face.jpg"
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil, nil, nil)
	acct := accountOf("a")

	svc.remember(acct, Pet{SkinKey: SkinVanya}, nil)
	svc.setAvatar(acct, face)
	// Exactly what a verb does to the cache after it writes.
	svc.remember(acct, Pet{SkinKey: SkinVanya}, nil)

	got, ok := svc.AvatarFor(svc.pseudonym(acct))
	if !ok || got != face {
		t.Fatalf("AvatarFor after a pet write = (%q, %v), want (%q, true) — a verb must not strip the face off the plane", got, ok, face)
	}
}

// TestTheYardAsksForAFaceOnceAndThenStopsAsking is the boundary display.go
// exists to keep, applied to the account service.
//
// The picture is fetched when a client says HELLO — a fresh socket, so
// human-paced — and never again. The counter is the assertion that matters: a
// tick that reached the account service would show up here as one call per
// frame per player, five times a second, which is the exact shape of mistake
// that made the display cache necessary in the first place (ADR-041). It is also
// the reason `load` asks BEFORE it fetches the pet: a slow account service must
// cost one player his face for one connection, never the room its frame.
func TestTheYardAsksForAFaceOnceAndThenStopsAsking(t *testing.T) {
	const face = "https://example.invalid/face.jpg"
	profiles := &fakeProfiles{url: face}
	tr := &fakeTransport{}
	tr.setMembers(member("1")) // accountOf("1") == testAccount
	svc := NewService(tr, testRoom, noPool{}, playedFor(), profiles, nil)

	svc.load(context.Background(), testAccount)
	if profiles.calls != 1 {
		t.Fatalf("hello asked the account service %d times, want exactly 1", profiles.calls)
	}

	for i := 0; i < 5; i++ {
		if err := svc.broadcast(context.Background(), at(float64(i))); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
	}
	if profiles.calls != 1 {
		t.Fatalf("after five ticks the account service had been asked %d times; the tick must never reach it", profiles.calls)
	}

	if _, ok := peerOf(svc, tr.frames()[len(tr.frames())-1], testAccount); !ok {
		t.Fatal("the player is not in the frame")
	}
	got, ok := svc.AvatarFor(svc.pseudonym(testAccount))
	if !ok || got != face {
		t.Fatalf("AvatarFor after hello = (%q, %v), want (%q, true)", got, ok, face)
	}
}

// TestAFaceThatCannotBeFetchedCostsOnlyTheFace pins the failure path.
//
// An account service that is down must not take the yard with it: the player
// draws as the catalogue art, everybody else is unaffected, and the next hello
// tries again. Silent to the player and logged for the operator, which is the
// same posture every other non-fatal read in this file takes.
func TestAFaceThatCannotBeFetchedCostsOnlyTheFace(t *testing.T) {
	profiles := &fakeProfiles{err: errors.New("account service is down")}
	tr := &fakeTransport{}
	tr.setMembers(member("1")) // accountOf("1") == testAccount
	svc := NewService(tr, testRoom, noPool{}, playedFor(), profiles, nil)

	svc.load(context.Background(), testAccount)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	got, ok := peerOf(svc, frames[len(frames)-1], testAccount)
	if !ok {
		t.Fatal("a player whose avatar failed to load fell out of the yard entirely")
	}
	if got.Art == "" {
		t.Fatal("he is drawn as nothing at all; the catalogue art is the fallback")
	}
	if url, ok := svc.AvatarFor(svc.pseudonym(testAccount)); ok {
		t.Fatalf("a failed fetch left %q behind; the lookup must miss so the client falls back", url)
	}
}

// ---------------------------------------------------------------------------
// The four locations, as the plane publishes and accepts them.
//
// One room, five places. Everything rides one frame carrying its own `loc`, and
// leaving one place for another is `vanyagotchi_goto` — a MOVEMENT message,
// answered by nothing, exactly as a tap is.
// ---------------------------------------------------------------------------

// goTo sends one `vanyagotchi_goto` frame down the inbound path, exactly as a
// client would. Through HandleInbound rather than by calling the handler, because
// the switch that routes an unknown type to nothing is part of what is under
// test.
func goTo(svc *Service, m realtime.Member, locationKey string) {
	payload := []byte(`{"t":"` + TypeGoto + `","location":"` + locationKey + `"}`)
	svc.HandleInbound(context.Background(), m, testRoom, payload)
}

// TestAGotoMovesHimToAnotherPlaceAndWritesItDown is the whole of the new verb —
// which is not a verb.
//
// FOUR THINGS HAPPEN AND THEY MUST HAPPEN TOGETHER. The row is written, so he is
// still in лес after a restart. The display cache is refreshed, so the tick draws
// him there without asking Postgres — the interesting one to get wrong, because
// the row would be right, every read would agree, and the plane would go on
// drawing him in the place he left with nothing anywhere reporting a problem.
// His placement is moved to the new location's entry point, because he did not
// walk here. And the head count follows him, because it is per location now.
//
// AND THE PET IS PUSHED BACK, which is the fifth and is the one that shipped
// missing. The roster moves his dot; it does not tell the BROWSER which place it
// is looking at, because that is read off `pets.location_key`. So a goto that
// pushed nothing left the yard drawing the place he had left, filtering his own
// Ваня out of it, and — because the sheet marks the place you are in as the row
// that means "stay" — refusing to send him back. Asserted here rather than only
// in the browser, since this is where the frame either goes out or does not.
func TestAGotoMovesHimToAnotherPlaceAndWritesItDown(t *testing.T) {
	les, ok := LocationByKey(LocationLes)
	if !ok {
		t.Fatalf("the catalogue has no location %q", LocationLes)
	}
	repo := playedFor()
	tr := &fakeTransport{}
	m := member("1")
	tr.setMembers(m)
	svc := planeService(tr, repo)
	svc.load(context.Background(), m.AccountID)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	before := tr.frames()[0]
	if p, ok := peerOf(svc, before, m.AccountID); !ok || p.Loc != "" {
		t.Fatalf("before the goto he is drawn as %+v; want the default location, which is omitted on the wire", p)
	}
	if got := inTheYard(before); got != 1 {
		t.Fatalf("the frame says %d people are in the yard before the goto; want 1", got)
	}

	goTo(svc, m, LocationLes)

	// The row.
	moved := repo.moved()
	if len(moved) != 1 || moved[0].accountID != m.AccountID || moved[0].locationKey != LocationLes {
		t.Fatalf("the goto wrote %+v; want exactly one move of %s into %q — a location that is not written down is one he loses on the next deploy",
			moved, m.AccountID, LocationLes)
	}
	// And nothing else was written: a goto moves no stat and appends no event,
	// which is why it is not a verb.
	if n := len(repo.appended); n != 0 {
		t.Errorf("%d events were appended by a goto; it is a movement message, and the log carries a verb key and no payload at all", n)
	}
	if repo.writes != 0 {
		t.Errorf("%d stat snapshots were written by a goto; going somewhere moves nothing about the pet", repo.writes)
	}

	// The cache, the placement and the head count, all read off the next frame.
	if err := svc.broadcast(context.Background(), at(1)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	after := tr.frames()[1]
	p, ok := peerOf(svc, after, m.AccountID)
	if !ok {
		t.Fatalf("he is not in the frame at all after a goto: %+v", after)
	}
	if p.Loc != LocationLes {
		t.Errorf("he is drawn with loc=%q after moving to %q; the display cache was not refreshed, so the plane is still drawing him in the place he left",
			p.Loc, LocationLes)
	}
	standingAt(t, p, les.Entry, "a Ваня who has just arrived in "+LocationLes)
	if got := inTheYard(after); got != 0 {
		t.Errorf("the frame still says %d people are in the yard after the only one left it; want 0", got)
	}
	if got := after.Here[LocationLes]; got != 1 {
		t.Errorf("the frame says %d people are in %q; want 1 — the count is per location now, and the client reads the entry for the place it is looking at",
			got, LocationLes)
	}

	// And the pet went back to him, carrying the place he is now in.
	pushed := statesFor(t, tr, m.ConnID)
	if len(pushed) != 1 {
		t.Fatalf("%d pet frames were pushed for one goto; want exactly 1 — without it the browser never learns it moved, and the travel sheet strands him in the place he left",
			len(pushed))
	}
	if got := pushed[0].Pet.LocationKey; got != LocationLes {
		t.Errorf("the pushed pet says he is in %q; want %q — the whole point of the push is to carry the new place",
			got, LocationLes)
	}
}

// TestAGotoNamingSomewhereThatDoesNotExistIsIgnored.
//
// The location key is the third inbound field a client controls completely, and
// the only defence it needs is the catalogue: an invented one does not resolve,
// so the worst an arbitrary payload buys is a frame that did nothing. It matters
// that the check is here rather than at the column, because `location_key` is
// plain text and the database would have accepted «пивная» without complaint —
// after which he would have been drawn with a loc no client matches, in a place
// with no hotspots, unable to search anywhere and invisible to everybody.
//
// Dropped in SILENCE, like every other frame this server does not believe. There
// is no reply channel, and logging one would hand any client a flood lever at its
// full ten messages a second.
func TestAGotoNamingSomewhereThatDoesNotExistIsIgnored(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{name: "a location nobody has heard of", payload: `{"t":"` + TypeGoto + `","location":"пивная"}`},
		{name: "no location at all", payload: `{"t":"` + TypeGoto + `"}`},
		{name: "an empty location", payload: `{"t":"` + TypeGoto + `","location":""}`},
		{name: "not json", payload: `{"t":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := playedFor()
			tr := &fakeTransport{}
			m := member("1")
			tr.setMembers(m)
			svc := planeService(tr, repo)
			svc.load(context.Background(), m.AccountID)
			if err := svc.broadcast(context.Background(), at(0)); err != nil {
				t.Fatalf("broadcast: %v", err)
			}

			svc.HandleInbound(context.Background(), m, testRoom, []byte(tc.payload))

			if got := repo.moved(); len(got) != 0 {
				t.Fatalf("a goto naming %s wrote %+v; the catalogue is what stands between an arbitrary string and a location_key nothing can render", tc.name, got)
			}
			if err := svc.broadcast(context.Background(), at(1)); err != nil {
				t.Fatalf("broadcast: %v", err)
			}
			f := tr.frames()[1]
			if p, ok := peerOf(svc, f, m.AccountID); !ok || p.Loc != "" {
				t.Errorf("after a goto naming %s he is drawn as %+v; want him exactly where he was", tc.name, p)
			}
			if got := inTheYard(f); got != 1 {
				t.Errorf("the frame says %d people are in the yard after a refused goto; want the 1 who never left", got)
			}
			// And no unicast at all. An ACCEPTED goto pushes the pet back, because
			// it moved a pet column; a dropped one moved nothing, so there is no
			// new state to carry and the sender — who invented the location — is
			// owed no explanation either.
			if n := len(tr.unicasts()); n != 0 {
				t.Errorf("%d unicasts were sent for a goto this server refused; a frame that changed nothing has no state to push", n)
			}
		})
	}
}

// TestAGotoThatCannotBeWrittenDownDoesNotMoveHimOnThePlaneEither.
//
// The row is written first and the caches only if it succeeded. The other order
// would leave the plane drawing him in a place the database has never heard of,
// and the next hello would silently put him back — a teleport with no cause the
// player could see and nothing in a log to explain it.
func TestAGotoThatCannotBeWrittenDownDoesNotMoveHimOnThePlaneEither(t *testing.T) {
	repo := playedFor()
	repo.setLocationErr = errors.New("the database is having a moment")
	tr := &fakeTransport{}
	m := member("1")
	tr.setMembers(m)
	svc := planeService(tr, repo)
	svc.load(context.Background(), m.AccountID)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	goTo(svc, m, LocationZabroshka)

	if err := svc.broadcast(context.Background(), at(1)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[1]
	p, ok := peerOf(svc, f, m.AccountID)
	if !ok {
		t.Fatalf("he is not in the frame at all: %+v", f)
	}
	if p.Loc != "" {
		t.Errorf("the write failed and he is drawn in %q anyway; the plane would be showing a place the database has never heard of, and the next hello would put him back with no cause anybody could see",
			p.Loc)
	}
	if got := inTheYard(f); got != 1 {
		t.Errorf("the frame says %d people are in the yard after a goto that could not be written; want the 1 who never moved", got)
	}
}

// TestGoingSomewhereIsBoundedSeparatelyFromPressingAButton.
//
// A goto writes a row, so at the socket's ten frames a second a client
// alternating between two places would be ten UPDATEs a second — which is the
// same reason a batch of verbs has a bound of its own, and it earns the same
// one-a-second limit.
//
// ON ITS OWN CLOCK, which is the half worth a test. Sharing the verb budget
// would mean that walking into лес ate the allowance for the drink you walked
// there to have — and a verb refused by the rate limit is refused in SILENCE, so
// the player would meet it as a button that did nothing.
func TestGoingSomewhereIsBoundedSeparatelyFromPressingAButton(t *testing.T) {
	repo := playedFor()
	tr := &fakeTransport{}
	m := member("1")
	tr.setMembers(m)
	svc := planeService(tr, repo)
	svc.load(context.Background(), m.AccountID)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	goTo(svc, m, LocationLes)
	// Same instant, so inside the interval however it is tuned.
	goTo(svc, m, LocationKusty)
	if got := repo.moved(); len(got) != 1 || got[0].locationKey != LocationLes {
		t.Fatalf("two gotos in the same instant wrote %+v; want only the first — a write on the socket needs a bound the socket's own rate limit cannot give it", got)
	}

	// And a verb in the same instant is NOT charged for it. The two budgets are
	// separate, so arriving somewhere and immediately acting is a thing a person
	// can do.
	svc.mu.Lock()
	held := svc.pos[m.AccountID]
	svc.mu.Unlock()
	if !held.lastVerb.IsZero() {
		t.Errorf("a goto stamped the verb clock as well as its own; walking into a location would eat the allowance for the drink you walked there to have")
	}
	if held.lastGoto.IsZero() {
		t.Error("the goto stamped no clock of its own; nothing would bound the write at all")
	}

	// A minute later he may move again, which is what says the bound is a gap
	// rather than a one-shot.
	if err := svc.broadcast(context.Background(), at(1)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	goTo(svc, m, LocationKusty)
	if got := repo.moved(); len(got) != 2 || got[1].locationKey != LocationKusty {
		t.Fatalf("a goto a minute later wrote %+v; want a second move into %q", got, LocationKusty)
	}
}

// TestTheHeadCountIsPerLocationAndCountsOnlyPeople.
//
// `Here` had to become a map, and the reason is the one that made it exist at
// all: the client deliberately cannot tell a person from a character or from a
// thing on the ground, so it cannot count its own location by filtering the
// peers. One number would be the whole world's, which is not a thing any screen
// can say.
//
// What it counts is unchanged — distinct connected ACCOUNTS, snapped before the
// sleepers, the cast and the props are appended — and this pins both halves at
// once: two devices of one person are one head, and the лес that holds him holds
// nothing else.
func TestTheHeadCountIsPerLocationAndCountsOnlyPeople(t *testing.T) {
	les, ok := LocationByKey(LocationLes)
	if !ok {
		t.Fatalf("the catalogue has no location %q", LocationLes)
	}
	kind := leavingKind(t, ActionRelieve)
	repo := playedFor()
	// Something lying about in лес, so a count that included props would be wrong
	// in the one location under test.
	repo.objects = []WorldObject{anObjectIn(9, kind.Key, LocationLes, Point{X: 0.3, Y: 0.4}, epoch.Add(time.Hour))}
	tr := &fakeTransport{}
	// Three sockets across two accounts: one person on two devices, plus one more.
	both := []realtime.Member{
		{ConnID: "a1", AccountID: accountOf("a")},
		{ConnID: "a2", AccountID: accountOf("a")},
		member("b"),
	}
	tr.setMembers(both...)
	svc := planeService(tr, repo)
	svc.load(context.Background(), accountOf("a"))

	// The one with two devices goes to лес. Through the production path, so the
	// fixture cannot disagree with what a real goto does.
	svc.arrive(accountOf("a"), les)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]
	if got := f.Here[LocationLes]; got != 1 {
		t.Errorf("the frame says %d people are in %q; want 1 — two devices of one person are one Ваня standing in one place, and the куча lying beside him is not somebody",
			got, LocationLes)
	}
	if got := inTheYard(f); got != 1 {
		t.Errorf("the frame says %d people are in the yard; want the one who stayed", got)
	}
	// Zeroes are absent rather than published: four empty places cost nothing.
	for _, loc := range Content().Locations {
		if loc.Key == LocationLes || loc.Key == LocationYard {
			continue
		}
		if _, present := f.Here[loc.Key]; present {
			t.Errorf("the frame publishes an entry for %q, where nobody is standing; an absent key already means nought", loc.Key)
		}
	}
}

// TestEveryCharacterStaysInTheLocationTheCatalogueGivesHim.
//
// An NPC is catalogue plus arithmetic and has no row, so where he is is a
// content property like his art and his pattern — and getting it wrong is
// invisible twice over: a character with a location nothing matches appears
// nowhere at all, and one with no location appears in every place at once. The
// three regulars are in двор and none of them names it, which is the "empty means
// the yard" rule doing its job.
func TestEveryCharacterStaysInTheLocationTheCatalogueGivesHim(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("1"))
	svc := planeService(tr, &fakeRepo{})
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]

	if len(Content().NPCs) == 0 {
		t.Fatal("the catalogue has no characters at all; this test is vacuous")
	}
	for _, npc := range Content().NPCs {
		p, ok := peerByID(f, npcPrefix+npc.Key)
		if !ok {
			t.Fatalf("%q is in the catalogue and not in the frame: %+v", npc.Key, f)
		}
		if want := onWire(npc.Location); p.Loc != want {
			t.Errorf("%q is drawn with loc=%q; the catalogue puts him in %q, which goes on the wire as %q",
				npc.Key, p.Loc, locationOr(npc.Location), want)
		}
	}
	// And none of them is counted, wherever he is: the head count is people.
	if got := inTheYard(f); got != 1 {
		t.Errorf("the frame says %d people are in the yard while one socket is open and the rest of the cast is furniture: %+v", got, f)
	}
}

// TestLosingHisNerveIsDecidedOnceForAnInstantAndCarriesItsOwnLine is the roll
// itself, asserted directly because nothing else can assert it.
//
// The key is 32 bytes of crypto/rand minted per process and there is no seam to
// fix it, deliberately — a player who could predict his own roll would simply
// press at the instants that work and the mechanic would evaporate. So what a
// test CAN pin is not the value but the three properties that make it usable:
// it is a pure function of its inputs, a chance of nought never fires, and the
// sentence always comes out of the catalogue's own pool.
func TestLosingHisNerveIsDecidedOnceForAnInstantAndCarriesItsOwnLine(t *testing.T) {
	svc := NewService(nil, testRoom, nil, nil, nil, nil)

	// DETERMINISTIC. Asked twice about the same (account, verb, instant) it
	// answers the same thing, which is what stops two viewers — or the live path
	// and anything that re-examined it — disagreeing about whether he went.
	for i := 0; i < 200; i++ {
		when := at(0).Add(time.Duration(i) * time.Second)
		lost, line := svc.shy(testAccount, ActionRelieve, relieveFailChance, when)
		again, sameLine := svc.shy(testAccount, ActionRelieve, relieveFailChance, when)
		if lost != again || line != sameLine {
			t.Fatalf("the same instant answered (%v,%q) and then (%v,%q); the roll is not a function of its inputs",
				lost, line, again, sameLine)
		}
		if lost && !slices.Contains(shySays, line) {
			t.Fatalf("he lost his nerve and said %q, which is not one of the catalogue's lines", line)
		}
		if !lost && line != "" {
			t.Fatalf("he went through with it and still said %q; a line belongs to a refusal", line)
		}
	}

	// A CHANCE OF NOUGHT NEVER FIRES, which is what makes the field safe to leave
	// off every other verb in the catalogue: they are not rolling and losing, they
	// are not rolling at all.
	for i := 0; i < 500; i++ {
		when := at(0).Add(time.Duration(i) * time.Millisecond)
		if lost, _ := svc.shy(testAccount, ActionDrink, 0, when); lost {
			t.Fatalf("a verb with no FailChance was refused for nerves at %s", when.UTC())
		}
	}

	// AND IT ACTUALLY FIRES. A roll that never came up would leave every
	// assertion above vacuous and the mechanic silently absent.
	fired := 0
	for i := 0; i < 500; i++ {
		if lost, _ := svc.shy(testAccount, ActionRelieve, relieveFailChance, at(0).Add(time.Duration(i)*time.Millisecond)); lost {
			fired++
		}
	}
	if fired == 0 {
		t.Fatal("500 presses and he never once lost his nerve; the roll is disconnected")
	}

	// The two rolls a Ваня can take at ONE instant are independent, which is what
	// the namespace prefix in the seed buys: without it, giving up on a walk and
	// losing his nerve would be the same draw, and a player who saw one would know
	// the other.
	same := 0
	for i := 0; i < 200; i++ {
		when := at(0).Add(time.Duration(i) * time.Millisecond)
		lost, _ := svc.shy(testAccount, ActionRelieve, 0.5, when)
		other, _ := svc.shy(testAccount, ActionDrink, 0.5, when)
		if lost == other {
			same++
		}
	}
	if same == 200 || same == 0 {
		t.Fatalf("two different verbs agreed on %d of 200 instants; the roll is not keyed on the verb", same)
	}
}
