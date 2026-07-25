//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
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
func expectRoster(t *testing.T, tick chan<- time.Time, frames <-chan []byte) gamevanyagotchi.Roster {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case tick <- time.Time{}:
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
