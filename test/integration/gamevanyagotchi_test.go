//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/gamevanyagotchi"
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
