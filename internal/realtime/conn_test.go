package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialConn brings up a live socket with no inbound handler.
func dialConn(t *testing.T) (*Conn, *websocket.Conn) {
	t.Helper()
	return dialConnWith(t, nil)
}

// dialConnWith brings up a live socket and returns both ends: the server-side
// Conn under test and the client that observes what it writes. Serve is
// deliberately left unstarted so a test can decide whether pumps are running.
func dialConnWith(t *testing.T, handler Handler) (*Conn, *websocket.Conn) {
	t.Helper()

	accepted := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:bodyclose // Accept hijacks; there is no response body to close.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		accepted <- ws
		// Hold the handler so the hijacked connection outlives the handshake.
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, resp, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = client.CloseNow() })

	select {
	case ws := <-accepted:
		return NewConn("test-conn", "acct-1", "sess-1", "yard", ws, handler), client
	case <-time.After(5 * time.Second):
		t.Fatal("handshake never reached the server")
		return nil, nil
	}
}

// readBye reads one frame and decodes it as a Bye.
func readBye(t *testing.T, client *websocket.Conn) Bye {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	typ, data, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v — expected a bye frame before the close", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("message type = %v, want text", typ)
	}
	var b Bye
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	if b.T != TypeBye {
		t.Fatalf("t = %q, want %q (payload %q)", b.T, TypeBye, data)
	}
	return b
}

// TestCloseNeverBlocks is the invariant the hub depends on. Close is called from
// the single hub goroutine and from the shutdown path, so if it could ever block
// it would stall every other client in the room — and here nothing is draining
// the connection at all, which is the worst case.
func TestCloseNeverBlocks(t *testing.T) {
	conn, _ := dialConn(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.Close(CloseGoingAway, "restart")
		// A second call must also be a no-op rather than a second send on a
		// channel nobody is reading.
		conn.Close(CloseUnauthorized, "unauthorized")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked with no write pump draining the request")
	}
}

// TestServeDeliversByeBeforeClosing is the property the client's reconnect
// strategy rests on: it must be able to tell a revoked session (stop, terminal)
// from a planned restart (reconnect promptly) from a network drop (back off).
// The transport close cannot carry that — see Conn.Close — so it arrives as the
// last frame instead, and this proves it actually does.
func TestServeDeliversByeBeforeClosing(t *testing.T) {
	hub, _ := startHub(t)
	conn, client := dialConn(t)

	go conn.Serve(context.Background(), hub)

	conn.Close(CloseUnauthorized, "unauthorized")

	got := readBye(t, client)
	if got.Code != CloseUnauthorized {
		t.Errorf("code = %d, want %d (CloseUnauthorized)", got.Code, CloseUnauthorized)
	}
	if got.Reason != "unauthorized" {
		t.Errorf("reason = %q, want %q", got.Reason, "unauthorized")
	}

	// And the socket really does go away afterwards.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := client.Read(ctx); err == nil {
		t.Fatal("socket still readable after the bye frame")
	}
}

// TestServeDeliversByeWhenContextCancelledFirst pins the shutdown ordering that
// made this whole mechanism subtle. main cancels the hub context and only then
// walks the hub's clients calling Close, so the write pump sees cancellation
// BEFORE the close request exists — and a write on an already-cancelled context
// fails instantly. Without the grace window and the detached write context, the
// client would learn nothing on every single deploy, which is the most common
// disconnect in production.
func TestServeDeliversByeWhenContextCancelledFirst(t *testing.T) {
	hub, _ := startHub(t)
	conn, client := dialConn(t)

	ctx, cancel := context.WithCancel(context.Background())
	go conn.Serve(ctx, hub)

	// Deliberately in the order shutdown uses: cancel, then ask to close.
	cancel()
	// The pause is the point of the test, not an attempt to wait for a condition
	// we could observe. The library installs a context.AfterFunc on the read
	// context that closes the connection outright, so the failure mode is the read
	// pump reacting to the cancellation before the close request exists. Without
	// this gap the test passes or fails on goroutine scheduling — it originally
	// passed while the bug was live.
	time.Sleep(50 * time.Millisecond)
	conn.Close(CloseGoingAway, "restart")

	got := readBye(t, client)
	if got.Code != CloseGoingAway {
		t.Errorf("code = %d, want %d (CloseGoingAway)", got.Code, CloseGoingAway)
	}
	if got.Reason != "restart" {
		t.Errorf("reason = %q, want %q", got.Reason, "restart")
	}
}

// TestServeExitsWhenCancelledWithNoCloseRequest guards the other side of that
// grace window: a cancellation that is never followed by a Close must not hold
// the connection open for longer than the window, or shutdown would pay the
// full wait for every idle socket.
func TestServeExitsWhenCancelledWithNoCloseRequest(t *testing.T) {
	hub, _ := startHub(t)
	conn, _ := dialConn(t)

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		conn.Serve(ctx, hub)
	}()

	cancel()
	select {
	case <-served:
	case <-time.After(byeGrace + 2*time.Second):
		t.Fatalf("Serve did not return within %v of cancellation", byeGrace)
	}
}
