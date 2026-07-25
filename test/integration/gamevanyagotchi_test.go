//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
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

// peerAt reports whether a roster contains an entity standing exactly here.
func peerAt(r gamevanyagotchi.Roster, x, y float64) bool {
	for _, p := range r.Peers {
		if p.X == x && p.Y == y {
			return true
		}
	}
	return false
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
	if got := expectRoster(t, tick, framesB); len(got.Peers) != 2 {
		t.Fatalf("roster carries %d peers; want both players: %+v", len(got.Peers), got)
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
	if got := expectRoster(t, tick, framesB); len(got.Peers) != 2 {
		t.Fatalf("roster carries %d peers before the disconnect; want 2", len(got.Peers))
	}

	_ = connA.CloseNow()

	deadline := time.Now().Add(5 * time.Second)
	for {
		r := expectRoster(t, tick, framesB)
		if len(r.Peers) == 1 {
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
	if len(first.Peers) != 2 {
		t.Fatalf("three sockets across two accounts produced %d entities; want 2: %+v",
			len(first.Peers), first)
	}
	if first.Peers[0].ID == first.Peers[1].ID {
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
				if len(r.Peers) != 2 {
					t.Fatalf("a move from the %s produced %d entities; want 2: %+v", what, len(r.Peers), r)
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
		if len(r.Peers) == 2 && peerAt(r, 0.75, 0.25) {
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
		if len(r.Peers) == 1 {
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
	if len(r.Peers) != 2 {
		t.Fatalf("roster carries %d peers; want both players: %+v", len(r.Peers), r)
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
	for _, p := range r.Peers {
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
	base := time.Now().Add(-time.Hour).UTC()
	stood := gamevanyagotchi.Point{X: 0.125, Y: 0.875}
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connA.Write(ctx, websocket.MessageText,
		fmt.Appendf(nil, `{"t":"vanyagotchi_move","x":%v,"y":%v}`, stood.X, stood.Y)); err != nil {
		t.Fatalf("A move: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !peerAt(expectRosterAt(t, tick, framesB, base), stood.X, stood.Y) {
		if time.Now().After(deadline) {
			t.Fatal("A's move never reached the plane")
		}
	}

	// A's socket goes. Nothing tells the game so: absence is inferred by the
	// next tick from the hub's membership.
	_ = connA.CloseNow()
	deadline = time.Now().Add(5 * time.Second)
	for len(expectRosterAt(t, tick, framesB, base).Peers) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the disconnected player is still on the plane")
		}
	}

	x, y, seen := petWaitStanding(t, idA)
	if x != stood.X || y != stood.Y {
		t.Fatalf("written down at (%v,%v); want where he was standing (%v,%v)", x, y, stood.X, stood.Y)
	}
	if d := seen.Sub(base); d < -time.Second || d > time.Second {
		t.Fatalf("last_seen_at = %s, want the last tick he was connected (%s, off by %s) — an hour out is a wall clock rather than the tick's",
			seen.UTC(), base, d)
	}

	// Run the grace period out, so the in-memory position is genuinely forgotten
	// and the row above is the only thing left that knows where he was standing.
	// The queue is drained first because this is the one assertion that turns on
	// WHICH tick produced a frame.
	drainFrames(framesB)
	expired := base.Add(3 * gamevanyagotchi.PositionGrace)
	for i := range 2 {
		if len(expectRosterAt(t, tick, framesB, expired).Peers) != 1 {
			t.Fatalf("tick %d past the grace still has somebody extra on the plane", i)
		}
	}

	// He comes back on a fresh socket, exactly as he would after a deploy. No
	// tick is fired between the socket registering and its hello being answered:
	// one that landed in that gap would place him at the spawn, and the load
	// would then find a position already in memory and leave him there.
	connA2, _, err := dialRealtime(t, app.URL, cookieHeader(t, cliA, app.URL), "http://localhost")
	if err != nil {
		t.Fatalf("dial A again: %v", err)
	}
	defer connA2.CloseNow()
	framesA2 := readFrames(t, connA2)
	waitRegistered(t, hub, framesA2)
	handleA := helloAndWaitForTheLoad(t, connA2, framesA2)

	r := expectRosterAt(t, tick, framesA2, expired.Add(time.Minute))
	p, ok := peerByID(r, handleA)
	if !ok {
		t.Fatalf("the reconnected player is missing from the roster: %+v", r)
	}
	if p.X != stood.X || p.Y != stood.Y {
		t.Fatalf("he came back at (%v,%v); want where he was standing when he left (%v,%v) — the spawn is (%v,%v)",
			p.X, p.Y, stood.X, stood.Y, entryPoint(t).X, entryPoint(t).Y)
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
		if len(expectRosterAt(t, tick, frames, base.Add(time.Duration(i)*time.Second)).Peers) != 1 {
			t.Fatalf("tick %d published a roster that is not just this player", i)
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
