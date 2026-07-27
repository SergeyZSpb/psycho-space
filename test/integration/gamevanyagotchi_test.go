//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/gamevanyagotchi"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/coder/websocket"
)

// expectRoster fires the broadcast tick until a roster frame arrives, then hands
// it back. Frames from other sources — the readiness probe, a bye — are skipped.
//
// The tick is re-fired rather than waited on because the tick is the test's: one
// frame per tick, and nothing in the system produces one on its own.
//
// The zero time is deliberate for every test that does not care about the grace
// period: it is the clock the broadcast measures absence against, and a run of
// ticks all carrying it means no position is ever old enough to be forgotten.
func expectRoster(t *testing.T, tick chan<- time.Time, frames <-chan []byte) gamevanyagotchi.Roster {
	t.Helper()
	return expectRosterAt(t, tick, frames, time.Time{})
}

// expectPetPushed reads the `vanyagotchi_state` frame a client is sent after its
// own pet has changed.
//
// SCANNED rather than taken off the head of the channel, because the same socket
// is carrying rosters five times a second and the push arrives among them. It
// drives no tick of its own: a push is not part of the broadcast, it is sent by
// whichever read pump handled the frame that caused it.
func expectPetPushed(t *testing.T, frames <-chan []byte) gamevanyagotchi.State {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case data, ok := <-frames:
			if !ok {
				t.Fatal("the socket closed while waiting for the pet to be pushed back")
			}
			var f gamevanyagotchi.StateFrame
			if err := json.Unmarshal(data, &f); err != nil || f.T != gamevanyagotchi.TypeStateFrame {
				continue // a roster, a probe or a bye
			}
			return f.State
		case <-deadline:
			t.Fatal("no vanyagotchi_state frame arrived; the browser reads which place it is looking at off the pet, so a change it is never told about strands it in the place it left")
		}
	}
}

// vanyagotchiInTheYard is how many people a roster says are standing in двор.
//
// `vanyagotchiInTheYard(Roster)` became a map per location when the four locations arrived, and
// nearly every test in this file is about the yard because that is where a pet
// is created — so this reads the entry those tests mean and leaves the map
// itself for the ones that are about several places at once. A location nobody
// is in has no entry, and a map answers nought for one, which is exactly what
// "nobody is there" should read as.
func vanyagotchiInTheYard(r gamevanyagotchi.Roster) int {
	return r.Here[gamevanyagotchi.LocationYard]
}

// expectRosterAt is expectRoster with the clock the tick carries spelled out.
//
// That clock is the test's, not the wall's: the broadcast measures how long
// somebody has been absent against the instant on the tick it is given, so a
// test can run the grace period out to its end by sending a timestamp on the far
// side of it rather than by waiting two real minutes.
func expectRosterAt(t *testing.T, tick chan<- time.Time, frames <-chan []byte, now time.Time) gamevanyagotchi.Roster {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case tick <- now:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("the broadcast loop stopped consuming ticks")
		}
		select {
		case data, ok := <-frames:
			if !ok {
				t.Fatal("socket closed while waiting for a roster")
			}
			var r gamevanyagotchi.Roster
			if err := json.Unmarshal(data, &r); err != nil || r.T != gamevanyagotchi.TypeRoster {
				continue // a probe or a bye
			}
			return r
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("no roster frame arrived")
	return gamevanyagotchi.Roster{}
}

// walkTo taps a destination on one socket and ticks the clock on until he is
// standing there, answering with the instant on the tick that saw him arrive.
//
// Both halves earn their keep. The tap is asynchronous — it is read on its own
// socket's pump, so a broadcast can land before it — and it now starts a WALK,
// measured from whatever instant the last tick carried, so a run of ticks all
// carrying ONE instant would show him setting off and never arriving. Advancing
// the clock a minute per tick outruns any walk this plane allows, which is about
// seven seconds corner to corner however the numbers are retuned.
//
// The instant it returns is the one to keep ticking at afterwards: it is the
// last tick he was connected for, which is what a departure is written down as.
func walkTo(t *testing.T, conn *websocket.Conn, tick chan<- time.Time, frames <-chan []byte,
	from time.Time, to gamevanyagotchi.Point,
) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText,
		fmt.Appendf(nil, `{"t":"%s","x":%v,"y":%v}`, gamevanyagotchi.TypeMove, to.X, to.Y)); err != nil {
		t.Fatalf("move: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; ; i++ {
		now := from.Add(time.Duration(i) * time.Minute)
		if peerAt(expectRosterAt(t, tick, frames, now), to.X, to.Y) {
			return now
		}
		if time.Now().After(deadline) {
			t.Fatalf("nobody ever reached (%v,%v)", to.X, to.Y)
		}
	}
}

// peerAt reports whether a roster contains a PERSON standing exactly here. The
// yard's regulars are skipped: they are moving all the time and a test that
// happened to catch one on the spot it was looking for would pass for the wrong
// reason.
func peerAt(r gamevanyagotchi.Roster, x, y float64) bool {
	for _, p := range peopleIn(r) {
		if p.X == x && p.Y == y {
			return true
		}
	}
	return false
}

// regulars is the handles the yard's NPCs are published under.
//
// The "npc-" prefix is the server's own naming, and this is where the test suite
// depends on it — asserted against the wire in
// TestVanyagotchiTheYardHasItsRegularsInIt and derived from here everywhere else.
// The client needs none of this: to it they are entities like any other, which is
// the whole reason a new character costs no client work.
func regulars() map[string]gamevanyagotchi.NPC {
	out := make(map[string]gamevanyagotchi.NPC, len(gamevanyagotchi.Content().NPCs))
	for _, npc := range gamevanyagotchi.Content().NPCs {
		out["npc-"+npc.Key] = npc
	}
	return out
}

// propPrefix is what the server publishes a THING under, as opposed to somebody:
// a deposit on the ground, the crate of beer.
//
// The same shape as the regulars' "npc-", and here for the same reason. To the
// client there is no such thing as a world object — it resolves whatever art key
// an entity carries against the catalogue and holds no notion of kinds — so the
// only thing distinguishing a deposit on the grass from a player standing in it,
// on the wire, is this prefix.
//
// The lost key is deliberately NOT among them any more: a hidden kind is skipped
// before it becomes an entity at all, which is what makes «искать ключи» a search
// rather than a race to press. See TestVanyagotchiTheHiddenKeyNeverReachesTheWire.
const propPrefix = "obj-"

// peopleIn is the entities in a roster that are neither the yard's regulars nor
// the things lying about in it: the players, and anybody asleep.
//
// Both exclusions earn their keep, and the second one arrived the expensive way.
// Deposits are player-created and durable, and this suite shares one database —
// so a roster can carry any number of them, and counting one as a person made a
// yard holding one player look like a yard holding two. (The key used to be the
// worst of these, because every hello starts a hunt and the key was drawn; it is
// hidden now, so the exclusion it forced is kept for the things still visible.)
func peopleIn(r gamevanyagotchi.Roster) []gamevanyagotchi.Peer {
	npcs := regulars()
	out := make([]gamevanyagotchi.Peer, 0, len(r.Peers))
	for _, p := range r.Peers {
		if _, is := npcs[p.ID]; is || strings.HasPrefix(p.ID, propPrefix) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// awakeIn is the players actually connected: not the regulars, and not the
// bodies asleep in the yard.
//
// The sleepers are why a count of entities is no longer a count of people, and
// why the assertions below mostly use vanyagotchiInTheYard(Roster) instead. This exists for the
// few that need the entities themselves — and it has to be filtered rather than
// counted, because the database is shared by the whole package and a pet another
// test left a position on is asleep in this test's yard too.
func awakeIn(r gamevanyagotchi.Roster) []gamevanyagotchi.Peer {
	out := make([]gamevanyagotchi.Peer, 0, len(r.Peers))
	for _, p := range peopleIn(r) {
		if p.Pose != gamevanyagotchi.PoseAsleep {
			out = append(out, p)
		}
	}
	return out
}

// peerByID finds one entity in a roster by the handle it is published under.
// Nothing in a frame says which account it belongs to — that is the whole point
// of the pseudonym — so a test learns its own handle from the handshake and
// matches on that.
func peerByID(r gamevanyagotchi.Roster, id string) (gamevanyagotchi.Peer, bool) {
	for _, p := range r.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return gamevanyagotchi.Peer{}, false
}

// drainFrames empties whatever a socket already has queued, so that the next
// frame a test reads is one the next tick produced rather than one an earlier
// tick did. Needed only where an assertion turns on WHICH tick a frame came
// from — running the grace period out, where the clock on that tick is the whole
// point.
func drainFrames(frames <-chan []byte) {
	for {
		select {
		case <-frames:
		default:
			return
		}
	}
}

// entryPoint is where the plane puts somebody who has never stood anywhere: the
// default location's entry point, taken from the catalogue rather than written
// down, so moving the spawn is a content change and not a test edit.
func entryPoint(t *testing.T) gamevanyagotchi.Point {
	t.Helper()
	cfg := gamevanyagotchi.Content()
	for _, l := range cfg.Locations {
		if l.Key == cfg.DefaultLocation {
			return l.Entry
		}
	}
	t.Fatalf("the catalogue's default location %q is not in its own list of locations", cfg.DefaultLocation)
	return gamevanyagotchi.Point{}
}

// helloAndWaitForTheLoad says hello twice and returns the handle the client is
// told it is drawn under.
//
// The SECOND answer is the barrier, and this test file needs one. A hello is
// answered BEFORE the connection's read pump goes on to read the pet out of
// Postgres, and both happen on that one goroutine — so a reply to the second
// hello can only have been written after the first hello's read had finished.
//
// Without it, a test that fired a broadcast tick straight after the handshake
// would be racing that database read: a tick that won would place the player at
// the spawn, and the load, finding a position already in memory, would leave him
// there. The same window exists in production — a socket is registered before
// its hello arrives, and a tick can land in between — so this barrier makes the
// test deterministic rather than papering over anything.
func helloAndWaitForTheLoad(t *testing.T, conn *websocket.Conn, frames <-chan []byte) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := range 2 {
		if err := conn.Write(ctx, websocket.MessageText,
			[]byte(`{"t":"`+gamevanyagotchi.TypeHello+`"}`)); err != nil {
			t.Fatalf("hello %d: %v", i, err)
		}
	}

	var id string
	seen := 0
	for seen < 2 {
		select {
		case data, ok := <-frames:
			if !ok {
				t.Fatal("socket closed while waiting for the handshake replies")
			}
			var y gamevanyagotchi.You
			if err := json.Unmarshal(data, &y); err != nil || y.T != gamevanyagotchi.TypeYou {
				continue
			}
			if y.ID == "" {
				t.Fatalf("handshake reply carries no id: %s", data)
			}
			id, seen = y.ID, seen+1
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 2 handshake replies arrived", seen)
		}
	}
	return id
}

// expectYou requires the handshake reply to arrive on this socket and hands back
// the id it carries. Rosters and probes flow on the same channel, so it skips
// anything that is not the reply.
func expectYou(t *testing.T, conn *websocket.Conn, frames <-chan []byte) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText,
		[]byte(`{"t":"`+gamevanyagotchi.TypeHello+`"}`)); err != nil {
		t.Fatalf("hello: %v", err)
	}
	for {
		select {
		case data, ok := <-frames:
			if !ok {
				t.Fatal("socket closed while waiting for the handshake reply")
			}
			var y gamevanyagotchi.You
			if err := json.Unmarshal(data, &y); err != nil || y.T != gamevanyagotchi.TypeYou {
				continue
			}
			if y.ID == "" {
				t.Fatalf("handshake reply carries no id: %s", data)
			}
			return y.ID
		case <-time.After(3 * time.Second):
			t.Fatal("no handshake reply arrived")
		}
	}
}

// expectNoYou proves a socket was NOT sent a handshake reply, using the hub
// itself as the barrier: one goroutine owns every room, so a frame published now
// is queued behind everything the hub has already been asked to deliver — and
// its arrival here means any earlier unicast to this socket would have arrived
// first. Without that ordering the assertion would only be "nothing has turned
// up yet", which is a different and much weaker claim.
func expectNoYou(t *testing.T, hub *realtime.Hub, frames <-chan []byte) {
	t.Helper()
	// A one-off marker rather than the shared readiness probe: this channel may
	// still hold probes published while other sockets were registering, and
	// returning on one of those would end the drain before the barrier.
	marker := fmt.Sprintf(`{"t":"barrier-%d"}`, time.Now().UnixNano())
	if err := hub.Publish(context.Background(), "yard", []byte(marker)); err != nil {
		t.Fatalf("publish barrier: %v", err)
	}
	for {
		select {
		case data, ok := <-frames:
			if !ok {
				t.Fatal("socket closed while draining")
			}
			if string(data) == marker {
				return
			}
			var y gamevanyagotchi.You
			if err := json.Unmarshal(data, &y); err == nil && y.T == gamevanyagotchi.TypeYou {
				t.Fatalf("a handshake reply was delivered to somebody who never asked: %s", data)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the barrier frame never came back")
		}
	}
}

// TestVanyagotchiTwoPlayersSeeEachOther is the iteration's deliverable, driven
// over two real sockets through the real handler: what one player does becomes
// visible to the other, and the frame that carries it describes the whole plane
// rather than a delta.
func TestVanyagotchiTwoPlayersSeeEachOther(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7101", "user")
	cliB := loginAs(t, app.URL, "7102", "user")

	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()

	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)

	// Both are on the plane before anybody has moved.
	if got := expectRoster(t, tick, framesB); vanyagotchiInTheYard(got) != 2 {
		t.Fatalf("the roster says %d people are in the yard; want both players: %+v", vanyagotchiInTheYard(got), got)
	}

	// A moves, and B is told about it. Identity never travels in the payload —
	// the frame below claims nothing about who is sending it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connA.Write(ctx, websocket.MessageText,
		[]byte(`{"t":"vanyagotchi_move","x":0.125,"y":0.875}`)); err != nil {
		t.Fatalf("A move: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		r := expectRoster(t, tick, framesB)
		if peerAt(r, 0.125, 0.875) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B never saw A's move; last roster %+v", r)
		}
	}
}

// TestVanyagotchiRejectsUnusableCoordinates drives the validation over a real
// socket rather than only through the parser. An out-of-range tap is clamped
// onto the plane; a non-finite one is refused outright and must leave the sender
// where they were, because a position no renderer can draw is worse than a stale
// one.
func TestVanyagotchiRejectsUnusableCoordinates(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cli := loginAs(t, app.URL, "7103", "user")
	conn, _, err := dialRealtime(t, app.URL, cookieHeader(t, cli, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Off the plane: clamped to the corner rather than refused.
	if err := conn.Write(ctx, websocket.MessageText,
		[]byte(`{"t":"vanyagotchi_move","x":9,"y":-4}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if peerAt(expectRoster(t, tick, frames), 1, 0) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an out-of-range move was not clamped onto the plane")
		}
	}

	// Not a number, and not a position: ignored, and the last good one stands.
	for _, bad := range []string{
		`{"t":"vanyagotchi_move","x":null,"y":0.5}`,
		`{"t":"vanyagotchi_move","y":0.5}`,
		`{"t":"vanyagotchi_move"}`,
		`{"t":"nonsense","x":0.5,"y":0.5}`,
		`garbage`,
	} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(bad)); err != nil {
			t.Fatalf("write %q: %v", bad, err)
		}
	}
	for i := 0; i < 3; i++ {
		if r := expectRoster(t, tick, frames); !peerAt(r, 1, 0) {
			t.Fatalf("a rejected frame moved the player: %+v", r)
		}
	}

	// And the socket survived all of it — a bad frame is dropped, not fatal.
	if err := conn.Write(ctx, websocket.MessageText,
		[]byte(`{"t":"vanyagotchi_move","x":0.5,"y":0.5}`)); err != nil {
		t.Fatalf("socket died after bad frames: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if peerAt(expectRoster(t, tick, frames), 0.5, 0.5) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the socket stopped accepting moves after a bad frame")
		}
	}
}

// TestVanyagotchiDropsAPlayerWhoLeaves closes the loop on presence: the roster
// is rebuilt from the hub every tick, so a disconnect has to remove somebody
// with nothing having told the game they left.
func TestVanyagotchiDropsAPlayerWhoLeaves(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7104", "user")
	cliB := loginAs(t, app.URL, "7105", "user")
	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()

	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)
	if got := expectRoster(t, tick, framesB); vanyagotchiInTheYard(got) != 2 {
		t.Fatalf("the roster says %d people are in the yard before the disconnect; want 2", vanyagotchiInTheYard(got))
	}

	_ = connA.CloseNow()

	deadline := time.Now().Add(5 * time.Second)
	for {
		r := expectRoster(t, tick, framesB)
		if vanyagotchiInTheYard(r) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the disconnected player is still on the plane: %+v", r)
		}
	}
}

// TestVanyagotchiOneAccountOnTwoSocketsIsOnePeer is the bug the owner hit,
// driven over real sockets: signing in on a second device produced a second dot
// standing somewhere else, because the roster was built from the hub's member
// list and the hub reports one member per CONNECTION. One account is one Ваня,
// wherever it is connected from.
func TestVanyagotchiOneAccountOnTwoSocketsIsOnePeer(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cookieA := cookieHeader(t, loginAs(t, app.URL, "7201", "user"), app.URL)
	cookieB := cookieHeader(t, loginAs(t, app.URL, "7202", "user"), app.URL)

	// Two sockets on ONE account, and one socket belonging to somebody else so
	// the deduplication cannot pass by collapsing everybody.
	phone, _, err := dialRealtime(t, app.URL, cookieA, "http://localhost")
	if err != nil {
		t.Fatalf("dial phone: %v", err)
	}
	defer phone.CloseNow()
	laptop, _, err := dialRealtime(t, app.URL, cookieA, "http://localhost")
	if err != nil {
		t.Fatalf("dial laptop: %v", err)
	}
	defer laptop.CloseNow()
	other, _, err := dialRealtime(t, app.URL, cookieB, "http://localhost")
	if err != nil {
		t.Fatalf("dial other: %v", err)
	}
	defer other.CloseNow()

	framesPhone, framesLaptop, framesOther :=
		readFrames(t, phone), readFrames(t, laptop), readFrames(t, other)
	waitRegistered(t, hub, framesPhone)
	waitRegistered(t, hub, framesLaptop)
	waitRegistered(t, hub, framesOther)

	// Three sockets, two people, two entities — and two distinct ids, so they
	// were not merged into one either.
	first := expectRoster(t, tick, framesOther)
	if vanyagotchiInTheYard(first) != 2 {
		t.Fatalf("three sockets across two accounts produced a head count of %d; want 2: %+v", vanyagotchiInTheYard(first), first)
	}
	awake := awakeIn(first)
	if len(awake) != 2 {
		t.Fatalf("three sockets across two accounts produced %d entities; want 2: %+v", len(awake), first)
	}
	if awake[0].ID == awake[1].ID {
		t.Fatalf("two accounts were published under one id: %+v", first)
	}

	// A move from the phone moves that one entity, and the roster still has two.
	moveAndExpect := func(from *websocket.Conn, what string, x, y float64) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := from.Write(ctx, websocket.MessageText,
			fmt.Appendf(nil, `{"t":"vanyagotchi_move","x":%v,"y":%v}`, x, y)); err != nil {
			t.Fatalf("move from the %s: %v", what, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			r := expectRoster(t, tick, framesOther)
			if peerAt(r, x, y) {
				if vanyagotchiInTheYard(r) != 2 {
					t.Fatalf("a move from the %s left %d people in the yard; want 2: %+v", what, vanyagotchiInTheYard(r), r)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("a move from the %s never landed; last roster %+v", what, r)
			}
		}
	}
	moveAndExpect(phone, "phone", 0.125, 0.875)
	// The SAME entity then moves when the other device says so — proof the two
	// sockets share one position rather than owning one each.
	moveAndExpect(laptop, "laptop", 0.75, 0.25)

	// Closing one device does not take the entity away: the account still has a
	// connection, so it is still standing in the yard, where the laptop left it.
	_ = phone.CloseNow()
	waitFor := time.Now().Add(5 * time.Second)
	for {
		r := expectRoster(t, tick, framesOther)
		if vanyagotchiInTheYard(r) == 2 && peerAt(r, 0.75, 0.25) {
			break
		}
		if time.Now().After(waitFor) {
			t.Fatalf("closing one of an account's two sockets changed the plane: %+v", r)
		}
	}
	// Only when the last one goes does the entity leave.
	_ = laptop.CloseNow()
	waitFor = time.Now().Add(5 * time.Second)
	for {
		r := expectRoster(t, tick, framesOther)
		if vanyagotchiInTheYard(r) == 1 {
			break
		}
		if time.Now().After(waitFor) {
			t.Fatalf("an account with no sockets left is still on the plane: %+v", r)
		}
	}
}

// TestVanyagotchiHelloTellsAClientWhichEntityIsItself drives the handshake over
// real sockets. Every entity in the roster is an opaque handle, so a client
// cannot pick itself out by inspection — it asks, and the answer must reach that
// socket and no other.
func TestVanyagotchiHelloTellsAClientWhichEntityIsItself(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cookieA := cookieHeader(t, loginAs(t, app.URL, "7203", "user"), app.URL)
	cookieB := cookieHeader(t, loginAs(t, app.URL, "7204", "user"), app.URL)

	phone, _, err := dialRealtime(t, app.URL, cookieA, "http://localhost")
	if err != nil {
		t.Fatalf("dial phone: %v", err)
	}
	defer phone.CloseNow()
	laptop, _, err := dialRealtime(t, app.URL, cookieA, "http://localhost")
	if err != nil {
		t.Fatalf("dial laptop: %v", err)
	}
	defer laptop.CloseNow()
	other, _, err := dialRealtime(t, app.URL, cookieB, "http://localhost")
	if err != nil {
		t.Fatalf("dial other: %v", err)
	}
	defer other.CloseNow()

	framesPhone, framesLaptop, framesOther :=
		readFrames(t, phone), readFrames(t, laptop), readFrames(t, other)
	waitRegistered(t, hub, framesPhone)
	waitRegistered(t, hub, framesLaptop)
	waitRegistered(t, hub, framesOther)

	onPhone := expectYou(t, phone, framesPhone)
	// Unicast means unicast: the other account's socket was told nothing. The
	// barrier inside expectNoYou is what makes this an assertion rather than a
	// hope — see its comment.
	expectNoYou(t, hub, framesOther)

	// The same account's second device is told the same id, which is what lets
	// both screens highlight the same Ваня.
	if onLaptop := expectYou(t, laptop, framesLaptop); onLaptop != onPhone {
		t.Fatalf("the two devices of one account were told different ids (%q, %q)", onPhone, onLaptop)
	}

	// The id is one the roster actually uses, exactly once.
	r := expectRoster(t, tick, framesPhone)
	n := 0
	for _, p := range r.Peers {
		if p.ID == onPhone {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the id the client was given names %d entities in the roster; want 1: %+v", n, r)
	}

	// And it is not the account id. That is the whole reason the handshake
	// exists rather than the roster simply carrying accounts.id.
	accountID := accountIDByUID(t, "7203")
	if onPhone == accountID || strings.Contains(onPhone, accountID) {
		t.Fatal("the entity id on the wire is the account id")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal roster: %v", err)
	}
	if strings.Contains(string(raw), accountID) {
		t.Fatalf("the roster carries an account id: %s", raw)
	}
}

// TestVanyagotchiConnectionCapSaysWhy covers the refusal a client could not
// previously interpret. The caps are checked after the 101, so "too many
// connections" cannot be an HTTP status — it arrives as a bye, and it needs a
// reason of its own: it is the only refusal here that a person can act on by
// closing another tab, and one that reads like an eviction invites a reconnect
// loop that cannot succeed.
func TestVanyagotchiConnectionCapSaysWhy(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, _ := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cookie := cookieHeader(t, loginAs(t, app.URL, "7205", "user"), app.URL)

	// Fill the per-account cap — three, per realtime.defaultMaxPerAccount: a
	// phone, a laptop and one stale tab.
	for i := range 3 {
		c, _, err := dialRealtime(t, app.URL, cookie, "http://localhost")
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer c.CloseNow()
		waitRegistered(t, hub, readFrames(t, c))
	}

	// One too many. The upgrade itself still succeeds, which is exactly the
	// problem this reason solves.
	extra, _, err := dialRealtime(t, app.URL, cookie, "http://localhost")
	if err != nil {
		t.Fatalf("the fourth dial failed at the HTTP level (%v); the cap is checked after the upgrade, so it should have connected and then been told why", err)
	}
	defer extra.CloseNow()
	frames := readFrames(t, extra)

	bye := expectBye(t, frames, realtime.CloseTryAgainLater)
	if bye.Reason != realtime.ReasonTooManyConnections {
		t.Fatalf("cap refusal reason = %q; want %q", bye.Reason, realtime.ReasonTooManyConnections)
	}
	// The code alone cannot separate this from an eviction, so the reason must.
	if bye.Reason == realtime.ReasonSlowConsumer {
		t.Fatal("the cap refusal is indistinguishable from a slow-consumer eviction")
	}
	expectClosed(t, frames)
}

// ---------------------------------------------------------------------------
// Appearance and durable position, over real sockets and a real database.
//
// UID namespace: 73xx. 71xx is the rest of this file and 72xx belongs to the
// pet tests, and the database is shared across the whole package.
// ---------------------------------------------------------------------------

// TestVanyagotchiTheRosterCarriesEveryPeersAppearance is what stops the yard
// being a field of anonymous dots, driven end to end.
//
// The important word is EVERY. The frame goes to everybody in the room, and it
// describes everybody in it — so what is asserted here is that the SECOND player
// can see how the FIRST one is doing. A build where each player could only see
// their own Ваня properly would be two worlds rather than one shared one, and it
// would pass any test that only ever looked at its own entity.
func TestVanyagotchiTheRosterCarriesEveryPeersAppearance(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7301", "user")
	cliB := loginAs(t, app.URL, "7302", "user")

	// Opening the game over HTTP is what creates a pet — there is no
	// registration step in this game and the socket deliberately conjures
	// nothing — so both players do it before either of them connects.
	for who, cli := range map[string]*http.Client{"A": cliA, "B": cliB} {
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("%s opening the game: status=%d body=%v", who, s, body)
		}
	}
	idA := petID(t, accountIDByUID(t, "7301"))
	const nameA = "Ваня А"
	petSetName(t, idA, nameA)

	// Far enough in that A's health has fallen inside the catalogue's own
	// warning range, and short of the moment it runs out entirely — the window
	// is derived from the catalogue and then checked, so a retune moves it
	// rather than breaking against it.
	hpDef := petStat(t, gamevanyagotchi.StatHP)
	hours := 0.9 * petFatalHours(t, hpDef)
	rough := petValueAfter(t, hpDef, hours)
	if rough <= hpDef.Min || rough >= hpDef.WarnAt {
		t.Fatalf("after %.2fh A's health reads %.2f, which is not inside the warning range (%v, %v); this test needs a Ваня who is looking rough but is not dead",
			hours, rough, hpDef.Min, hpDef.WarnAt)
	}
	petBackdateAll(t, idA, petHours(hours))

	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()

	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)

	// A's hello is the moment the plane reads his pet: the name, the skin and
	// the backdated pairs his pose is worked out from.
	handleA := helloAndWaitForTheLoad(t, connA, framesA)

	// Read on B's socket, deliberately — and driven with a real clock, because
	// the pose is DERIVED from the cached pairs against the instant on the tick.
	// A tick carrying the zero time (which is what every test in this file that
	// does not care about the clock sends) would decay nothing and draw a Ваня
	// who has been dying since yesterday as perfectly well.
	r := expectRosterAt(t, tick, framesB, time.Now())
	if vanyagotchiInTheYard(r) != 2 {
		t.Fatalf("the roster says %d people are in the yard; want both players: %+v", vanyagotchiInTheYard(r), r)
	}
	got, ok := peerByID(r, handleA)
	if !ok {
		t.Fatalf("B's roster does not mention A at all: %+v", r)
	}
	if got.Art != gamevanyagotchi.Content().DefaultSkin {
		t.Errorf("B sees A drawn as %q; want the skin his pet actually has, %q",
			got.Art, gamevanyagotchi.Content().DefaultSkin)
	}
	if got.Label != nameA {
		t.Errorf("B sees A labelled %q; want %q — a name is only worth storing if the other players can read it", got.Label, nameA)
	}
	if got.Pose != gamevanyagotchi.PosePoorly {
		t.Errorf("B sees A drawn as %q; want %q — A's health has been inside the warning range for %.2f hours",
			got.Pose, gamevanyagotchi.PosePoorly, hours)
	}

	// And B, whose pet is freshly created and unnamed, is drawn as himself
	// rather than as A: an appearance that leaked between accounts would look
	// entirely plausible in a frame with two entities in it.
	var other gamevanyagotchi.Peer
	for _, p := range awakeIn(r) {
		if p.ID != handleA {
			other = p
		}
	}
	if other.ID == "" {
		t.Fatalf("B is missing from his own roster: %+v", r)
	}
	if other.Label != "" {
		t.Errorf("B's unnamed pet is labelled %q; want no label", other.Label)
	}
	if other.Pose != gamevanyagotchi.PoseFine {
		t.Errorf("B's freshly created pet is drawn as %q; want %q", other.Pose, gamevanyagotchi.PoseFine)
	}
}

// TestVanyagotchiAPositionSurvivesADisconnect is the whole point of the pets
// table growing x, y and last_seen_at.
//
// A position is held in memory for a grace period, which is what makes a page
// refresh keep your place — but memory dies with the process, and this
// application deploys by restarting it. Without a durable copy the entire yard
// comes back standing in the middle every time something ships, which is a
// failure that shows up nowhere but production.
//
// Every leg is driven rather than waited for: the tick carries the test's own
// clock, so the grace period is run out by sending a timestamp on the far side
// of it, and the only thing polled is the row itself — the write is deliberately
// off the broadcast's path, on a goroutine of the game's, so there is nothing
// else to synchronise with.
func TestVanyagotchiAPositionSurvivesADisconnect(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7303", "user")
	cliB := loginAs(t, app.URL, "7304", "user")
	// A's pet row is what a departure is written to, and only opening the game
	// over HTTP creates one.
	if s, body := doJSON(t, cliA, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("A opening the game: status=%d body=%v", s, body)
	}
	idA := petID(t, accountIDByUID(t, "7303"))

	// The clock the ticks carry, deliberately an hour in the past: the instant
	// written down has to be the last tick he was CONNECTED, and against a
	// present-day clock that would be indistinguishable from the instant the row
	// happened to be written.
	//
	// `walked` is a minute further on, and that minute is the journey. A tap is a
	// walk now, evaluated against the instant on the tick, so a run of ticks all
	// carrying one instant would show him setting off and never arriving. A
	// minute of the test's own clock is far longer than the seven seconds the
	// longest walk on this plane takes.
	base := time.Now().Add(-time.Hour).UTC()
	walked := base.Add(time.Minute)
	// Within tiredFrom of the entry point, so he cannot decide he is too tired
	// and sit down half way — which is a real outcome of an ambitious tap and
	// would make this test flake rather than fail.
	stood := gamevanyagotchi.Point{X: 0.25, Y: 0.75}
	if stood == entryPoint(t) {
		t.Fatal("the position this test walks to is the spawn, so a restore and a default would be indistinguishable")
	}

	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)

	// He walks there, and the clock runs on until he has. The instant that saw
	// him arrive is the one every tick below keeps carrying, so the moment he was
	// last connected is a single unambiguous timestamp rather than whichever tick
	// happened to be the last one before the socket closed.
	arrived := walkTo(t, connA, tick, framesB, walked, stood)

	// A's socket goes. Nothing tells the game so: absence is inferred by the
	// next tick from the hub's membership.
	_ = connA.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for vanyagotchiInTheYard(expectRosterAt(t, tick, framesB, arrived)) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the disconnected player is still on the plane")
		}
	}

	x, y, seen := petWaitStanding(t, idA)
	if x != stood.X || y != stood.Y {
		t.Fatalf("written down at (%v,%v); want where he was standing (%v,%v)", x, y, stood.X, stood.Y)
	}
	if d := seen.Sub(arrived); d < -time.Second || d > time.Second {
		t.Fatalf("last_seen_at = %s, want the last tick he was connected (%s, off by %s) — an hour out is a wall clock rather than the tick's",
			seen.UTC(), arrived, d)
	}

	// Run the grace period out, so the in-memory position is genuinely forgotten
	// and the row above is the only thing left that knows where he was standing.
	// The queue is drained first because this is the one assertion that turns on
	// WHICH tick produced a frame.
	drainFrames(framesB)
	expired := arrived.Add(3 * gamevanyagotchi.PositionGrace)
	for i := range 2 {
		r := expectRosterAt(t, tick, framesB, expired)
		if vanyagotchiInTheYard(r) != 1 {
			t.Fatalf("tick %d past the grace says %d people are in the yard; only B has a socket open", i, vanyagotchiInTheYard(r))
		}
		// He is not gone, though: past the grace he is asleep in the yard, which
		// is TestVanyagotchiASleeperIsStillInTheYard's subject.
		if len(peopleIn(r)) < 2 {
			t.Fatalf("tick %d past the grace left nobody lying in the yard: %+v", i, r)
		}
	}

	// He comes back after a DEPLOY, which is the only thing this row is for.
	//
	// A second app is built over the same database: a new hub, a new game, an
	// empty position map — exactly what a restart leaves behind. Reconnecting to
	// the old one would prove nothing any more, because the sleepers keep his
	// placement in memory indefinitely and he would come back from there.
	//
	// No tick is fired between the socket registering and its hello being
	// answered: one that landed in that gap would place him at the spawn, and the
	// load would then find a position already in memory and leave him there.
	deployed, hub2, _, tick2 := buildAppRealtimeGame(t, vkSrv.URL)
	app2 := httptest.NewServer(deployed)
	defer app2.Close()

	connA2, _, err := dialRealtime(t, app2.URL, cookieHeader(t, cliA, app2.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A again: %v", err)
	}
	defer connA2.CloseNow()
	framesA2 := readFrames(t, connA2)
	waitRegistered(t, hub2, framesA2)
	handleA := helloAndWaitForTheLoad(t, connA2, framesA2)

	r := expectRosterAt(t, tick2, framesA2, expired.Add(time.Minute))
	p, ok := peerByID(r, handleA)
	if !ok {
		t.Fatalf("the reconnected player is missing from the roster: %+v", r)
	}
	if p.X != stood.X || p.Y != stood.Y {
		t.Fatalf("he came back at (%v,%v) on a fresh process; want where he was standing when he left (%v,%v) — the spawn is (%v,%v)",
			p.X, p.Y, stood.X, stood.Y, entryPoint(t).X, entryPoint(t).Y)
	}
	if p.Pose == gamevanyagotchi.PoseAsleep {
		t.Fatal("he came back asleep; his socket is open, which is what being here means")
	}
}

// TestVanyagotchiAPetThatHasNeverStoodAnywhereStartsAtTheSpawn is the other
// half of the restore: NULL is not a position.
//
// A pet has no x or y until its owner has left the yard at least once, and the
// honest reading of that is "he has never stood anywhere" rather than "he is at
// the origin". Mapping it onto a corner of the plane would put every new player
// in the top-left instead of at the entry point, and would do it silently.
func TestVanyagotchiAPetThatHasNeverStoodAnywhereStartsAtTheSpawn(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cli := loginAs(t, app.URL, "7305", "user")
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("opening the game: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7305"))
	if x, y, seen := petStanding(t, id); x != nil || y != nil || seen != nil {
		t.Fatalf("a freshly created pet already has a position written down (%v,%v at %v); this test needs one that has never stood anywhere", x, y, seen)
	}

	conn, _, err := dialRealtime(t, app.URL, cookieHeader(t, cli, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)
	handle := helloAndWaitForTheLoad(t, conn, frames)

	r := expectRoster(t, tick, frames)
	p, ok := peerByID(r, handle)
	if !ok {
		t.Fatalf("a pet with no stored position was left off the plane entirely: %+v", r)
	}
	entry := entryPoint(t)
	if p.X != entry.X || p.Y != entry.Y {
		t.Fatalf("peer at (%v,%v); want the entry point (%v,%v)", p.X, p.Y, entry.X, entry.Y)
	}
	if p.Art == "" || p.Pose == "" {
		t.Fatalf("peer drawn as %+v; want a skin and a pose — a client cannot render an entity with neither", p)
	}
}

// TestVanyagotchiTheBroadcastTickWritesNothing is the rule the display cache
// exists to keep, asserted where it can actually be broken.
//
// The tick is a RENDER step: it reads no database, writes nothing, and owns
// nothing, which is what makes a late, early, skipped or duplicated tick
// harmless. A query per tick would be every player, five times a second, against
// a box that also serves the site — to re-fetch a name and a skin key that change
// roughly never.
//
// "Nothing was written" is asserted as "no updated_at moved", and that stands in
// for it: every write in this game's repository sets updated_at to now(), so a
// row whose stamp has not moved is a row nothing has touched. Counting
// statements through pg_stat would be more literal, considerably more fragile,
// and would be answering the same question.
func TestVanyagotchiTheBroadcastTickWritesNothing(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cli := loginAs(t, app.URL, "7306", "user")
	if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("opening the game: status=%d body=%v", s, body)
	}
	id := petID(t, accountIDByUID(t, "7306"))

	conn, _, err := dialRealtime(t, app.URL, cookieHeader(t, cli, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)
	// The hello is allowed to read — it is the human-paced moment this game gets
	// to — so the snapshot is taken after it has finished.
	helloAndWaitForTheLoad(t, conn, frames)

	before := petTouchedAt(t, id)
	const ticks = 20
	base := time.Now()
	for i := range ticks {
		// Each tick carries a later instant than the last, so the run also
		// covers a broadcast whose clock has moved on: time passing must not
		// become a reason to write anything either.
		if r := expectRosterAt(t, tick, frames, base.Add(time.Duration(i)*time.Second)); vanyagotchiInTheYard(r) != 1 {
			t.Fatalf("tick %d says %d people are in the yard; want just this one", i, vanyagotchiInTheYard(r))
		}
	}

	if after := petTouchedAt(t, id); !after.Equal(before) {
		t.Fatalf("something about the pet was written between %s and %s over %d broadcasts; the tick must touch no row at all",
			before.UTC(), after.UTC(), ticks)
	}
	// And no position was written for somebody who never left: a departure is
	// what earns a write, not a frame.
	if x, y, seen := petStanding(t, id); x != nil || y != nil || seen != nil {
		t.Fatalf("%d broadcasts wrote a position (%v,%v at %v) for a player who has not gone anywhere", ticks, x, y, seen)
	}
}

// TestVanyagotchiTheYardHasItsRegularsInIt drives the whole NPC argument end to
// end, over a real socket.
//
// The claim being tested is that a character costs nothing: no table, no
// migration, no client work. What arrives on the wire is therefore an ordinary
// entity with an art key the browser resolves exactly as it resolves a pet's
// skin — which is only true if the server publishes him in the roster like
// anybody else, and if he is kept OUT of the head count, because that number is
// rendered as «во дворе: N» and a yard of two friends must not claim to have four
// people in it.
func TestVanyagotchiTheYardHasItsRegularsInIt(t *testing.T) {
	npcs := regulars()
	if len(npcs) == 0 {
		t.Fatal("the catalogue has no regulars; there is nothing for this test to look for")
	}
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cli := loginAs(t, app.URL, "7307", "user")
	conn, _, err := dialRealtime(t, app.URL, cookieHeader(t, cli, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)

	// A real clock, because where a regular stands is a function of it. No hello
	// is sent, so nothing here has read the database and the only person in the
	// yard is the one socket.
	base := time.Now().UTC()
	first := expectRosterAt(t, tick, frames, base)

	if vanyagotchiInTheYard(first) != 1 {
		t.Fatalf("the roster says %d people are in the yard; one socket is open and the rest of the cast is furniture: %+v",
			vanyagotchiInTheYard(first), first)
	}
	if n := len(peopleIn(first)); n != 1 {
		t.Fatalf("%d entities that are not regulars; want the one player: %+v", n, first)
	}
	if len(first.Peers) != 1+len(npcs) {
		t.Fatalf("the frame carries %d entities; want the player plus the %d regulars: %+v",
			len(first.Peers), len(npcs), first)
	}

	for id, npc := range npcs {
		p, ok := peerByID(first, id)
		if !ok {
			t.Fatalf("%q is not in the roster; a regular the client is told about in the config but never sees is a character who does not exist", id)
		}
		if p.Art != npc.Art {
			t.Errorf("%q is drawn as %q; want the catalogue's own art %q — the client resolves this exactly as it resolves a pet's skin", id, p.Art, npc.Art)
		}
		if p.Label != npc.Label {
			t.Errorf("%q is labelled %q; want %q", id, p.Label, npc.Label)
		}
		if p.Pose != gamevanyagotchi.PoseFine {
			t.Errorf("%q is drawn as %q; a character who is not a pet has no health to be poorly about", id, p.Pose)
		}
		if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
			t.Errorf("%q is at (%v,%v), off the plane", id, p.X, p.Y)
		}
	}

	// And they are alive: minutes later, on the same socket, at least one of them
	// has moved. A cast evaluated against a frozen clock would satisfy everything
	// above.
	//
	// Polled rather than read once, because this assertion turns on WHICH tick a
	// frame came from and the socket may still be holding one the earlier tick
	// produced. A frame in which somebody has moved can only have come from the
	// later tick, so the first one that shows movement is the answer.
	drainFrames(frames)
	deadline := time.Now().Add(5 * time.Second)
	stirred, later := false, gamevanyagotchi.Roster{}
	for !stirred {
		later = expectRosterAt(t, tick, frames, base.Add(7*time.Minute))
		for id := range npcs {
			a, _ := peerByID(first, id)
			b, _ := peerByID(later, id)
			if a.X != b.X || a.Y != b.Y {
				stirred = true
			}
		}
		if time.Now().After(deadline) {
			break
		}
	}
	if !stirred {
		t.Fatalf("not one regular moved in seven minutes; their positions are a function of the clock, so either the clock is not reaching them or they are all idlers: %+v", later)
	}

	// The config the client renders them from carries no hint of how they move:
	// that stays the server's business, or there would be a second implementation
	// of it in TypeScript for the two to disagree about.
	s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/config", nil)
	if s != http.StatusOK {
		t.Fatalf("config: status=%d body=%v", s, body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	for _, forbidden := range []string{"pattern", "params", "spread", "period", "route"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+forbidden+`"`) {
			t.Errorf("the config publishes %q; how a character moves is the server's business alone", forbidden)
		}
	}
}

// TestVanyagotchiASleeperIsStillInTheYard is what turns a solo visit from a menu
// into a place, driven over two real sockets.
//
// With five to thirty friends the yard is almost never occupied by two people at
// once. Without the sleepers it would therefore be an empty field almost all the
// time — and filler characters would have been a worse answer than the real
// ones, who are all still lying about in it. The three stages have to be
// distinguishable: connected, briefly absent (a reload looks exactly like that),
// and properly asleep.
func TestVanyagotchiASleeperIsStillInTheYard(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7308", "user")
	cliB := loginAs(t, app.URL, "7309", "user")
	// A's pet, so the body lying in the yard is recognisably his rather than an
	// anonymous dot.
	if s, body := doJSON(t, cliA, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
		t.Fatalf("A opening the game: status=%d body=%v", s, body)
	}
	const nameA = "Спящий Ваня"
	petSetName(t, petID(t, accountIDByUID(t, "7308")), nameA)

	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)

	// A's hello is both how he learns his own handle and how the plane reads his
	// pet, so what B sees below is drawn from the same cache a live player is.
	handleA := helloAndWaitForTheLoad(t, connA, framesA)

	// Within tiredFrom of the entry point, so the walk cannot be refused half way
	// — giving up is a real outcome and would make this test flake, not fail.
	stood := gamevanyagotchi.Point{X: 0.7, Y: 0.25}
	if stood == entryPoint(t) {
		t.Fatal("this test lies down on the spawn, so where he is asleep and where anybody starts would be the same place")
	}
	arrived := walkTo(t, connA, tick, framesB, time.Now().UTC(), stood)

	_ = connA.CloseNow()

	// Inside the grace he is simply gone from B's yard. A reload closes a socket
	// exactly like this, and putting a body on the ground every time somebody
	// refreshed the page would be worse than showing nothing.
	drainFrames(framesB)
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := expectRosterAt(t, tick, framesB, arrived.Add(gamevanyagotchi.PositionGrace/2))
		if _, still := peerByID(r, handleA); !still {
			if vanyagotchiInTheYard(r) != 1 {
				t.Fatalf("B's roster says %d people are in the yard after A's socket closed; want 1: %+v", vanyagotchiInTheYard(r), r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("A is still on the plane long after his socket closed")
		}
	}

	// Past it he is furniture: back in the frame B receives, lying exactly where
	// he stood, drawn asleep, labelled — and NOT counted as somebody who is here.
	//
	// Polled, like the leg above: the socket may still be holding a frame from a
	// tick inside the grace, and a frame he is lying down in can only have come
	// from one past it.
	drainFrames(framesB)
	expired := arrived.Add(gamevanyagotchi.PositionGrace + time.Minute)
	deadline = time.Now().Add(5 * time.Second)
	var r gamevanyagotchi.Roster
	var asleep gamevanyagotchi.Peer
	for {
		r = expectRosterAt(t, tick, framesB, expired)
		if p, ok := peerByID(r, handleA); ok {
			asleep = p
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nobody is asleep in B's yard past the grace; a solo visit is a bare field again: %+v", r)
		}
	}
	if asleep.X != stood.X || asleep.Y != stood.Y {
		t.Fatalf("he is asleep at (%v,%v); want where he was standing when he left (%v,%v)", asleep.X, asleep.Y, stood.X, stood.Y)
	}
	if asleep.Pose != gamevanyagotchi.PoseAsleep {
		t.Fatalf("he is drawn as %q; want %q", asleep.Pose, gamevanyagotchi.PoseAsleep)
	}
	if asleep.Label != nameA {
		t.Errorf("the sleeper is labelled %q; want %q — the yard is furnished with people you know, which is the whole point of it", asleep.Label, nameA)
	}
	if vanyagotchiInTheYard(r) != 1 {
		t.Fatalf("B's roster says %d people are in the yard; B is the only one with a socket open: %+v", vanyagotchiInTheYard(r), r)
	}
	if n := len(awakeIn(r)); n != 1 {
		t.Fatalf("%d players are awake in B's yard; want just B: %+v", n, r)
	}
}

// TestVanyagotchiATapIsAJourneyAcrossTheYard is distance meaning something,
// asserted over a socket rather than in the arithmetic.
//
// Before it the position WAS the tap, so the far side of the yard was 220 ms
// away and the beer delivery could not be a race to arrive. What the other player
// has to see is the middle of the journey — somewhere that is neither where he
// was nor where he is going — because a walk that snapped to its destination on
// the first frame would satisfy "he got there" just as well.
func TestVanyagotchiATapIsAJourneyAcrossTheYard(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7310", "user")
	cliB := loginAs(t, app.URL, "7311", "user")
	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()
	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)
	handleA := expectYou(t, connA, framesA)

	// A tick first, so the tap has a clock to be measured from — and then the tap
	// itself, followed by a handshake on the same socket. Both are read by that
	// connection's own pump, in order, so the reply arriving is proof the move
	// has already been applied: without that barrier the tick below would race it.
	base := time.Now().UTC()
	entry := entryPoint(t)
	if r := expectRosterAt(t, tick, framesB, base); !peerAt(r, entry.X, entry.Y) {
		t.Fatalf("nobody is standing at the entry point before anybody has moved: %+v", r)
	}
	// Just under tiredFrom across, so it cannot be refused, and therefore about
	// two seconds long at walkSpeed — which is what the instant below is half of.
	dest := gamevanyagotchi.Point{X: 0.82, Y: 0.26}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connA.Write(ctx, websocket.MessageText,
		fmt.Appendf(nil, `{"t":"%s","x":%v,"y":%v}`, gamevanyagotchi.TypeMove, dest.X, dest.Y)); err != nil {
		t.Fatalf("A move: %v", err)
	}
	expectYou(t, connA, framesA)

	// Halfway through the journey in time, he is halfway through it in space:
	// neither where he was nor where he is going — literally.
	//
	// Read on until a frame from that later tick arrives: the socket may still be
	// holding the one the first tick produced, and a frame in which he has left
	// the entry point can only have come from a tick after he set off.
	drainFrames(framesB)
	deadline := time.Now().Add(5 * time.Second)
	var p gamevanyagotchi.Peer
	for {
		mid := expectRosterAt(t, tick, framesB, base.Add(time.Second))
		got, ok := peerByID(mid, handleA)
		if !ok {
			t.Fatalf("the walking player is missing from B's roster: %+v", mid)
		}
		if got.X != entry.X || got.Y != entry.Y {
			p = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a second in he has not left (%v,%v); he should be on his way", got.X, got.Y)
		}
	}
	if p.X == dest.X && p.Y == dest.Y {
		t.Fatalf("a second in he is already at (%v,%v); a tap is a journey, not a teleport", p.X, p.Y)
	}
	if p.X < entry.X || p.X > dest.X {
		t.Fatalf("a second in he is at (%v,%v), which is not between the entry point (%v,%v) and where he is going (%v,%v)",
			p.X, p.Y, entry.X, entry.Y, dest.X, dest.Y)
	}

	// And a minute of the test's clock later he has arrived — and then stays
	// there, which is the second half and needs no polling: once a frame from the
	// later tick has been seen, every frame after it is from a later one still.
	drainFrames(framesB)
	deadline = time.Now().Add(5 * time.Second)
	for {
		r := expectRosterAt(t, tick, framesB, base.Add(time.Minute))
		got, ok := peerByID(r, handleA)
		if !ok {
			t.Fatalf("the player who walked is missing from B's roster: %+v", r)
		}
		if got.X == dest.X && got.Y == dest.Y {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("he is at (%v,%v) a minute after setting off; want (%v,%v)", got.X, got.Y, dest.X, dest.Y)
		}
	}
	for i := range 2 {
		r := expectRosterAt(t, tick, framesB, base.Add(time.Duration(i+2)*time.Minute))
		got, _ := peerByID(r, handleA)
		if got.X != dest.X || got.Y != dest.Y {
			t.Fatalf("he is at (%v,%v) %d minutes after setting off; a finished walk is simply standing somewhere", got.X, got.Y, i+2)
		}
	}
}

// TestVanyagotchiAВаняStandingAboutMuttersOverTheWire proves the yard has a
// voice, end to end, over a real socket.
//
// The muttering is the ambient noise that stops a yard being a still life, and
// it is worth an integration test for one reason: it is derived from the clock
// rather than sent, so every layer between the arithmetic and the browser has to
// carry it without anybody publishing an event. Nothing here asks for a remark
// and nothing schedules one — the test simply drives the world's clock forward
// and reads what the server draws.
//
// This connection NEVER taps. That is what makes the assertion unambiguous
// without mirroring a phrase list into this package: the only other thing that
// puts words over a Ваня's head is the complaint he makes when he gives up on a
// walk, and he cannot give up on a walk he was never sent on.
func TestVanyagotchiAВаняStandingAboutMuttersOverTheWire(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cli := loginAs(t, app.URL, "7312", "user")
	conn, _, err := dialRealtime(t, app.URL, cookieHeader(t, cli, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)

	// A real clock, because whether he is talking is a function of it — and then
	// walked forward in steps small enough to land inside the window a remark is
	// shown for, whatever that window's exact length is. Deliberately NOT the
	// server's slot length: mirroring it here would be a second copy of a
	// constant, and stepping finely finds the window without knowing it.
	base := time.Now().UTC()
	const (
		step  = 2 * time.Second
		ticks = 400
	)

	var heard string
	for i := 0; i < ticks && heard == ""; i++ {
		r := expectRosterAt(t, tick, frames, base.Add(time.Duration(i)*step))
		for _, p := range peopleIn(r) {
			if p.Say != "" {
				heard = p.Say
			}
		}
	}
	if heard == "" {
		t.Fatalf("across %v of the world's clock nobody standing in the yard said anything; the muttering never reaches the wire",
			time.Duration(ticks)*step)
	}

	// And the regulars stayed out of it. They are furniture, and furniture that
	// talked would turn the yard into a chatroom.
	npcs := regulars()
	r := expectRosterAt(t, tick, frames, base)
	for id := range npcs {
		p, ok := peerByID(r, id)
		if !ok {
			t.Fatalf("%q is not in the roster", id)
		}
		if p.Say != "" {
			t.Fatalf("the regular %q says %q", id, p.Say)
		}
	}
}

// TestVanyagotchiTheRosterCarriesTheOwnersFace drives the avatar end to end:
// out of an encrypted column, through the account service, into the display
// cache at hello, and onto the OTHER player's screen.
//
// It has to be an integration test and it has to be read on B's socket. The
// value starts life as `avatar_url_enc` — AES-256-GCM, per-row nonce — so a unit
// test with a fake can only prove the plumbing downstream of the decrypt; and
// the whole point of a shared yard is that everyone sees everyone, so the
// assertion that matters is the one made from the other end of the room.
//
// The URL is whatever the fake VK server handed back at login, taken from the
// same place the production one would be rather than written in here twice.
func TestVanyagotchiTheRosterCarriesTheOwnersFace(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7311", "user")
	cliB := loginAs(t, app.URL, "7312", "user")
	for who, cli := range map[string]*http.Client{"A": cliA, "B": cliB} {
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("%s opening the game: status=%d body=%v", who, s, body)
		}
	}

	// What the account service says A's avatar is, decrypted — the same call the
	// game makes. Read rather than hardcoded so that changing the fake VK
	// server's fixture cannot leave this asserting a string nothing produces.
	want, err := newAccountService().AvatarURL(context.Background(), accountIDByUID(t, "7311"))
	if err != nil {
		t.Fatalf("reading A's avatar: %v", err)
	}
	if want == "" {
		t.Fatal("the fake VK server gave A no avatar, so this test would assert nothing")
	}

	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()

	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)
	// A's hello is the one moment the account service is asked. Nothing on the
	// tick reads it, which is the boundary display.go exists to keep.
	handleA := helloAndWaitForTheLoad(t, connA, framesA)

	r := expectRosterAt(t, tick, framesB, time.Now())
	if _, ok := peerByID(r, handleA); !ok {
		t.Fatalf("B's roster does not mention A at all: %+v", r)
	}

	// B asks for A's face by the handle B was given, over ordinary HTTP. The
	// roster carries no URL — a couple of hundred characters five times a second
	// for something that never changes, and the one durable thing on a frame
	// whose identity is deliberately per-process.
	res := getNoRedirect(t, cliB, app.URL+"/api/game-vanyagotchi/avatar/"+handleA)
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("asking for A's face: status=%d; want 302 to the picture on his account", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != want {
		t.Errorf("B is sent to %q for A's face; want the avatar stored on his account, %q", loc, want)
	}

	// And an entity nobody owns is an ordinary 404 rather than an error — the
	// answer every NPC gets, and what the client falls back from.
	miss := getNoRedirect(t, cliB, app.URL+"/api/game-vanyagotchi/avatar/npc-tungtung")
	defer miss.Body.Close()
	if miss.StatusCode != http.StatusNotFound {
		t.Errorf("asking for an NPC's face: status=%d; want 404", miss.StatusCode)
	}
}

// getNoRedirect performs one GET without following the redirect, because the
// redirect IS the assertion — a client that followed it would leave this test
// asserting whatever VK's CDN happened to answer.
func getNoRedirect(t *testing.T, cli *http.Client, url string) *http.Response {
	t.Helper()
	noFollow := &http.Client{
		Jar:           cli.Jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := noFollow.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return res
}

// TestVanyagotchiTheHiddenKeyNeverReachesTheWire is the deletion this iteration
// is built on, asserted at the only place that settles it: the bytes a browser
// actually receives, over a real socket, from a real database.
//
// The key used to be an ordinary entity in the roster. Every client was told
// exactly where it was five times a second, so «искать ключи» was a race to press
// a button pointing at a visible dot rather than a search. It is now absent from
// the frame entirely — not obscured, not flagged, absent — and the strong form of
// that is asserted here: no entity carries its art, no entity carries its id, and
// nothing at all is standing on its coordinates. A client reading every byte it
// will ever receive still does not know where the keys are.
//
// What must NOT go with it is the hunt's id. A hunt is STATE a late joiner has to
// be able to see and take part in, so a yard that stopped saying one was running
// would leave whoever opened the app thirty seconds ago with nothing to join.
func TestVanyagotchiTheHiddenKeyNeverReachesTheWire(t *testing.T) {
	kind := petObjectKind(t, gamevanyagotchi.KindKey)
	if !kind.Hidden {
		t.Fatalf("the catalogue no longer marks %q hidden; this test is about a rule the game does not have", kind.Key)
	}
	// Both ends, because the yard is a singleton world shared with every other
	// test in this suite: one left standing by a neighbour would be the key this
	// test then reads back, and its hello's own insert would be swallowed in
	// silence by ON CONFLICT DO NOTHING.
	petClearTheYardOf(t, kind.Key)
	t.Cleanup(func() { petClearTheYardOf(t, kind.Key) })

	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cli := loginAs(t, app.URL, "7311", "user")
	conn, _, err := dialRealtime(t, app.URL, cookieHeader(t, cli, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)

	// The hello is the human-paced moment the yard reads the world, and the one
	// that starts a hunt when it finds none — which is how the key under test gets
	// hidden in the first place, by the server, at a place only it knows.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText,
		fmt.Appendf(nil, `{"t":%q}`, gamevanyagotchi.TypeHello)); err != nil {
		t.Fatalf("hello: %v", err)
	}

	// Ticked until the hunt appears rather than slept on: the hello is handled on
	// its own read pump, so the roster that started the hunt is whichever one
	// happens to be published after it lands.
	base := time.Now().UTC()
	var frame gamevanyagotchi.Roster
	deadline := time.Now().Add(5 * time.Second)
	for {
		frame = expectRosterAt(t, tick, frames, base)
		if frame.Hunt != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no hunt was ever announced; a hello stands a key up when it finds none, and without one there is nothing here to be hidden")
		}
	}

	// Where it actually is, straight out of the table — the one place in the
	// system that knows.
	rows := petContestedRowsOf(t, kind.Key)
	var live []petContestedRow
	for _, r := range rows {
		if r.exhaustedAt == nil {
			live = append(live, r)
		}
	}
	if len(live) != 1 {
		t.Fatalf("%d keys are lost in the yard; the partial unique index permits exactly one: %+v", len(live), rows)
	}
	hidden, err := petWorldObjectPoint(t, live[0].id)
	if err != nil {
		t.Fatalf("read where the key is hidden: %v", err)
	}

	for _, p := range frame.Peers {
		if p.Art == kind.Art {
			t.Errorf("the frame draws an entity with the key's own art: %+v — everybody can see where it is, and «искать ключи» is a race to press rather than a search", p)
		}
		if p.X == hidden.X && p.Y == hidden.Y {
			t.Errorf("entity %q is standing exactly where the key is hidden, (%v,%v); a client that noticed would have the answer",
				p.ID, p.X, p.Y)
		}
		if p.ID == propPrefix+live[0].id[:12] {
			t.Errorf("the key is in the roster under its own id %q; hiding it means it is not an entity at all", p.ID)
		}
	}

	// And the hunt itself is still there, because it is state rather than an
	// announcement — the whole reason somebody arriving late can still take part.
	if frame.Hunt != live[0].id[:12] {
		t.Errorf("the frame names the hunt %q; want %q, the truncation every other id on this frame uses — hiding the key must not hide that a hunt is running",
			frame.Hunt, live[0].id[:12])
	}
}

// A 404 from the avatar route is cached only when it can never stop being one.
//
// THE BUG THIS PINS was reported from production: two browsers disagreed about
// the same handle, one showing the picture and the other showing
// `{"error":"no_avatar"}`. Neither was wrong about the server — the one that
// asked EARLY had cached its own 404 for half an hour, because the route sent
// `max-age=1800` on a miss as well as on a hit.
//
// For an NPC that is right: there is no picture and there never will be, and a
// client cannot tell an NPC from a person, so without a cached miss it would
// re-ask for every character on every reconnect. For a PERSON it is wrong: the
// avatar is read out of Postgres when that account says hello, so a peer drawn
// before its owner's hello has landed — a sleeper after a restart, most
// obviously — answers 404 and begins answering with a picture moments later.
func TestVanyagotchiAMissIsCachedOnlyWhenItIsPermanent(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, _, _, _ := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cli := loginAs(t, app.URL, "7431", "user")

	ask := func(t *testing.T, peer string) (int, string) {
		t.Helper()
		res, err := cli.Get(app.URL + "/api/game-vanyagotchi/avatar/" + peer)
		if err != nil {
			t.Fatalf("asking for %q: %v", peer, err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode, res.Header.Get("Cache-Control")
	}

	// A character the world owns: the miss is permanent, so it is cached exactly
	// like an answer would be.
	status, cache := ask(t, "npc-sahur")
	if status != http.StatusNotFound {
		t.Errorf("an NPC answered %d, want 404", status)
	}
	if !strings.Contains(cache, "max-age=") {
		t.Errorf("an NPC's permanent miss is not cached: Cache-Control=%q", cache)
	}

	// A thing on the ground: the same.
	if _, cache := ask(t, "obj-a1b2c3d4e5f6"); !strings.Contains(cache, "max-age=") {
		t.Errorf("a deposit's permanent miss is not cached: Cache-Control=%q", cache)
	}

	// A person this process has not loaded a picture for. The miss is TRANSIENT
	// and must not be stored, or the face stays missing long after it exists.
	status, cache = ask(t, "AV0XmddbiDyp")
	if status != http.StatusNotFound {
		t.Errorf("an unknown person answered %d, want 404", status)
	}
	if strings.Contains(cache, "max-age=") {
		t.Errorf("a person's TRANSIENT miss is cached (%q) — this is the reported bug: "+
			"a browser that asks before the hello lands keeps showing no_avatar", cache)
	}
	if cache != "no-store" {
		t.Errorf("Cache-Control=%q, want no-store", cache)
	}
}

// TestVanyagotchiGoingToAnotherLocationSurvivesTheRoundTrip is the location
// mechanic end to end over a real socket, against a real database.
//
// FOUR THINGS ARE PROVED HERE THAT NO UNIT TEST CAN. The frame really is one
// payload for the whole world — B is standing in двор and still receives A's
// entity, carrying the лес A walked into — which is what "locations are not
// realtime rooms" means on the wire. `pets.location_key` really is written, so
// A is still in лес after a restart. The per-location head count really reaches
// both clients. And the SPA's own filtering has something to filter on, because
// the yard's entity carries no `loc` at all while A's carries one.
func TestVanyagotchiGoingToAnotherLocationSurvivesTheRoundTrip(t *testing.T) {
	les, ok := gamevanyagotchi.LocationByKey(gamevanyagotchi.LocationLes)
	if !ok {
		t.Fatalf("the catalogue has no location %q", gamevanyagotchi.LocationLes)
	}
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _, tick := buildAppRealtimeGame(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	cliA := loginAs(t, app.URL, "7301", "user")
	cliB := loginAs(t, app.URL, "7302", "user")
	// The pets have to exist before their location can be written down, and the
	// first state read is what creates one.
	for _, cli := range []*http.Client{cliA, cliB} {
		if s, body := doJSON(t, cli, http.MethodGet, app.URL+"/api/game-vanyagotchi/state", nil); s != http.StatusOK {
			t.Fatalf("create a pet: status=%d body=%v", s, body)
		}
	}
	accountA := accountIDByUID(t, "7301")

	connA, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.CloseNow()
	connB, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliB, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.CloseNow()

	framesA, framesB := readFrames(t, connA), readFrames(t, connB)
	waitRegistered(t, hub, framesA)
	waitRegistered(t, hub, framesB)

	// Both are in the yard, and nobody carries a location: the default is omitted,
	// which is the whole reason the field is affordable on a 5 Hz frame.
	before := expectRoster(t, tick, framesB)
	if got := vanyagotchiInTheYard(before); got != 2 {
		t.Fatalf("the roster says %d people are in the yard before anybody moves; want 2: %+v", got, before)
	}
	for _, p := range peopleIn(before) {
		if p.Loc != "" {
			t.Errorf("somebody in the yard is published with loc=%q; the default is omitted on the wire: %+v", p.Loc, p)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connA.Write(ctx, websocket.MessageText,
		[]byte(`{"t":"vanyagotchi_goto","location":"`+gamevanyagotchi.LocationLes+`"}`)); err != nil {
		t.Fatalf("A goto: %v", err)
	}

	// B — who never left двор — watches A appear in лес. One frame carries the
	// whole world, so a location is something the client filters rather than
	// something the transport separates.
	deadline := time.Now().Add(5 * time.Second)
	var after gamevanyagotchi.Roster
	for {
		after = expectRoster(t, tick, framesB)
		if after.Here[gamevanyagotchi.LocationLes] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B never saw anybody arrive in %q; last roster %+v", gamevanyagotchi.LocationLes, after)
		}
	}
	if got := vanyagotchiInTheYard(after); got != 1 {
		t.Errorf("the roster says %d people are in the yard after one of the two left it; want 1: %+v", got, after)
	}
	var moved, stayed int
	for _, p := range peopleIn(after) {
		switch p.Loc {
		case gamevanyagotchi.LocationLes:
			moved++
			if p.X != les.Entry.X || p.Y != les.Entry.Y {
				t.Errorf("the one who moved is at (%v,%v); want %q's entry point (%v,%v) — he did not walk here, he left somewhere else",
					p.X, p.Y, les.Key, les.Entry.X, les.Entry.Y)
			}
		case "":
			stayed++
		default:
			t.Errorf("somebody is published in %q, which nobody was sent to: %+v", p.Loc, p)
		}
	}
	if moved != 1 || stayed != 1 {
		t.Errorf("%d entities carry loc=%q and %d carry none; want one of each: %+v", moved, les.Key, stayed, after)
	}

	// AND IT IS WRITTEN DOWN, which is the half a frame cannot show: the row is
	// what makes him still be in лес after a deploy.
	var stored string
	if err := pool.QueryRow(context.Background(),
		`SELECT location_key FROM game_vanyagotchi_pets WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountA).Scan(&stored); err != nil {
		t.Fatalf("read the mover's stored location: %v", err)
	}
	if stored != gamevanyagotchi.LocationLes {
		t.Errorf("his row says he is in %q; want %q — a location nobody wrote down is one he loses on the next restart", stored, gamevanyagotchi.LocationLes)
	}

	// AND THE MOVER IS TOLD, over his own socket, which is the half the roster
	// cannot do. The roster moves his DOT; the browser reads which place it is
	// LOOKING at off the pet, so without this push it goes on drawing двор,
	// filters his own Ваня out of it as somebody standing elsewhere, and marks
	// двор as the place he is in — which is the row the travel sheet refuses to
	// send him to, because it means "stay". He could not get back.
	pushed := expectPetPushed(t, framesA)
	if pushed.Pet.LocationKey != gamevanyagotchi.LocationLes {
		t.Errorf("the pet pushed back to the mover says he is in %q; want %q", pushed.Pet.LocationKey, gamevanyagotchi.LocationLes)
	}

	// A goto naming a place the catalogue does not have is dropped in silence, and
	// `location_key` is plain text — so the database would have taken it without a
	// word. The catalogue is the only thing standing in the way.
	if err := connA.Write(ctx, websocket.MessageText,
		[]byte(`{"t":"vanyagotchi_goto","location":"пивная"}`)); err != nil {
		t.Fatalf("A goto to nowhere: %v", err)
	}
	// Two ticks, so the write would have had every chance to land.
	expectRoster(t, tick, framesB)
	expectRoster(t, tick, framesB)
	if err := pool.QueryRow(context.Background(),
		`SELECT location_key FROM game_vanyagotchi_pets WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountA).Scan(&stored); err != nil {
		t.Fatalf("re-read the mover's stored location: %v", err)
	}
	if stored != gamevanyagotchi.LocationLes {
		t.Errorf("a goto naming a location nobody has heard of moved him to %q; the column is plain text and would have accepted anything", stored)
	}
}
