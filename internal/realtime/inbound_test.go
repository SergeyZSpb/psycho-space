package realtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// recordingHandler captures what the read pump delivers.
type recordingHandler struct {
	mu   sync.Mutex
	got  []recorded
	hold chan struct{} // when non-nil, HandleInbound blocks on it
}

type recorded struct {
	member  Member
	room    string
	payload string
}

func (h *recordingHandler) HandleInbound(_ context.Context, m Member, room string, payload []byte) {
	if h.hold != nil {
		<-h.hold
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Copied: the read buffer belongs to the library and may be reused.
	h.got = append(h.got, recorded{member: m, room: room, payload: string(payload)})
}

func (h *recordingHandler) calls() []recorded {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]recorded(nil), h.got...)
}

// TestReadPumpDeliversTextFramesToTheHandler is the seam a game reads through.
// Before this existed the read pump discarded every payload, so no inbound
// message could reach any domain service at all.
func TestReadPumpDeliversTextFramesToTheHandler(t *testing.T) {
	hub, _ := startHub(t)
	h := &recordingHandler{}
	conn, client := dialConnWith(t, h)

	go conn.Serve(context.Background(), hub)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Write(ctx, websocket.MessageText, []byte(`{"t":"hello"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	waitFor(t, "the handler to receive the frame", func() bool { return len(h.calls()) == 1 })
	got := h.calls()[0]
	if got.payload != `{"t":"hello"}` {
		t.Errorf("payload = %q; want the frame as sent", got.payload)
	}
	if got.room != "yard" {
		t.Errorf("room = %q; want yard", got.room)
	}
	// Identity comes from the connection, never from the payload — the frame
	// above claims nothing, and the handler still knows who sent it.
	if got.member.ConnID != "test-conn" || got.member.AccountID != "acct-1" {
		t.Errorf("member = %+v; want the connection's own identity", got.member)
	}
}

// TestReadPumpIgnoresBinaryFrames pins the one-encoding rule. Every protocol
// here is JSON text; accepting binary too would create a second decode path that
// nothing exercises, which is where a parser bug would live undisturbed.
func TestReadPumpIgnoresBinaryFrames(t *testing.T) {
	hub, _ := startHub(t)
	h := &recordingHandler{}
	conn, client := dialConnWith(t, h)

	go conn.Serve(context.Background(), hub)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Write(ctx, websocket.MessageBinary, []byte{0x00, 0x01}); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	// Then a text frame, which must arrive — proving the binary one was skipped
	// rather than the whole pump having stopped.
	if err := client.Write(ctx, websocket.MessageText, []byte(`{"t":"after"}`)); err != nil {
		t.Fatalf("write text: %v", err)
	}

	waitFor(t, "the text frame to arrive", func() bool { return len(h.calls()) == 1 })
	if got := h.calls()[0].payload; got != `{"t":"after"}` {
		t.Fatalf("handler saw %q; the binary frame should have been dropped", got)
	}
}

// TestReadPumpRejectsAnOversizedFrameBeforeTheHandler proves SetReadLimit still
// stands in front of the handler now that one exists. The 1 MiB HTTP body cap
// does not apply to a hijacked connection, so this is the only bound on frame
// size, and a handler must never be handed something bigger than it.
func TestReadPumpRejectsAnOversizedFrameBeforeTheHandler(t *testing.T) {
	hub, _ := startHub(t)
	h := &recordingHandler{}
	conn, client := dialConnWith(t, h)

	go conn.Serve(context.Background(), hub)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	oversized := []byte(`{"t":"` + strings.Repeat("x", MaxFrameBytes+1) + `"}`)
	// The write itself may succeed; the read side is what refuses it.
	_ = client.Write(ctx, websocket.MessageText, oversized)

	// The socket dies rather than delivering it. Reading until failure is the
	// observable end of the connection.
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	for {
		if _, _, err := client.Read(readCtx); err != nil {
			break
		}
	}
	if n := len(h.calls()); n != 0 {
		t.Fatalf("handler was called %d times with an oversized frame; want 0", n)
	}
}

// TestReadPumpSurvivesANilHandler covers the wiring case where nothing is
// listening — a deployment with no realtime game, and every existing test in
// this package. The bounds must still be enforced and the pump must not panic.
func TestReadPumpSurvivesANilHandler(t *testing.T) {
	hub, _ := startHub(t)
	conn, client := dialConn(t) // nil handler

	go conn.Serve(context.Background(), hub)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Write(ctx, websocket.MessageText, []byte(`{"t":"ignored"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Still alive afterwards: a nil handler is "nobody is listening", not an
	// error. Asking for a bye is the cheapest way to prove the pumps still run.
	conn.Close(CloseGoingAway, "restart")
	if got := readBye(t, client); got.Code != CloseGoingAway {
		t.Fatalf("code = %d; want %d", got.Code, CloseGoingAway)
	}
}

// TestSlowHandlerDoesNotStallTheHub is the reason HandleInbound is called on the
// read pump rather than on the hub goroutine. One client's handler taking its
// time must not stop the room: if dispatch ever moves onto the hub loop, this
// fails, and the whole yard freezing behind one phone is exactly the failure the
// hub's non-blocking fanout exists to prevent.
func TestSlowHandlerDoesNotStallTheHub(t *testing.T) {
	hub, _ := startHub(t)
	hold := make(chan struct{})
	h := &recordingHandler{hold: hold}
	defer close(hold)

	conn, client := dialConnWith(t, h)
	if err := hub.Register(context.Background(), conn, "yard"); err != nil {
		t.Fatalf("register: %v", err)
	}
	go conn.Serve(context.Background(), hub)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Write(ctx, websocket.MessageText, []byte(`{"t":"slow"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// With the handler wedged, the hub must still answer. Members goes through
	// the hub goroutine, so a reply proves that goroutine is not blocked.
	waitFor(t, "the hub to stay responsive while a handler is blocked", func() bool {
		mctx, c := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer c()
		got, err := hub.Members(mctx, "yard")
		return err == nil && len(got) == 1
	})
}

// TestReadPumpRateLimitRunsBeforeTheHandler pins the ordering that lets a game
// inherit the socket's bound for free. If the handler ran first, a flood would
// reach it at full rate and every game would have to remember to limit itself.
func TestReadPumpRateLimitRunsBeforeTheHandler(t *testing.T) {
	hub, _ := startHub(t)
	h := &recordingHandler{}
	conn, client := dialConnWith(t, h)

	go conn.Serve(context.Background(), hub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Comfortably past the burst; the limiter must cut in partway through.
	const sent = msgBurst * 4
	for i := 0; i < sent; i++ {
		if err := client.Write(ctx, websocket.MessageText, []byte(`{"t":"flood"}`)); err != nil {
			break // the connection was closed on us, which is the expected end
		}
	}

	// Nothing publishes to this connection, so the first frame it ever receives
	// is the bye — and its code says the limiter, not the peer, ended this.
	if got := readBye(t, client); got.Code != CloseTryAgainLater {
		t.Fatalf("code = %d; want %d (CloseTryAgainLater)", got.Code, CloseTryAgainLater)
	}
	if n := len(h.calls()); n >= sent {
		t.Fatalf("handler saw %d of %d frames; the limiter should have stopped it first", n, sent)
	}
}
