package httpapi

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/coder/websocket"
)

// TestHijackedConnOutlivesServerWriteTimeout pins a property this application
// depends on and does not implement: that a WebSocket is not killed by the HTTP
// server's ReadTimeout/WriteTimeout.
//
// The widely-repeated claim (golang/go#8296) is that a hijacked connection
// inherits those deadlines, so a WebSocket library must clear them.
// coder/websocket does not clear them — but it does not need to, because
// net/http's hijackLocked already calls rwc.SetDeadline(time.Time{}) before
// returning the connection. So there is nothing to fix, and handleRealtime
// deliberately does no deadline juggling.
//
// This test exists because that is a property of the standard library rather
// than of our code: if a future Go release stopped clearing the deadline, every
// socket in production would start dying at the write timeout with no obvious
// cause. The production timeout is 30s, which no test can wait out, and the
// integration harness uses httptest.NewServer, which sets no timeouts at all —
// hence NewHTTPServer taking its timeouts as a parameter, set to 100ms here.
func TestHijackedConnOutlivesServerWriteTimeout(t *testing.T) {
	const shortTimeout = 100 * time.Millisecond

	hub := realtime.NewHub()
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	go hub.Run(hubCtx)

	srv := NewServer(Deps{
		WebFS:       testWebFS(),
		Realtime:    hub,
		RealtimeCtx: hubCtx,
	})

	// A handler that upgrades exactly like handleRealtime does, minus the auth
	// (which needs a database). The deadline handling under test is identical.
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// Exactly what handleRealtime does: no deadline handling at all.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		conn := realtime.NewConn("test-conn", "test-account", "yard", ws, nil)
		if err := hub.Register(r.Context(), conn, "yard"); err != nil {
			t.Errorf("register: %v", err)
			return
		}
		conn.Serve(hubCtx, hub)
	})
	_ = srv // Deps wiring is exercised above; the handler under test is local.

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	httpSrv := NewHTTPServer(ln.Addr().String(), mux, Timeouts{
		ReadHeader: shortTimeout,
		Read:       shortTimeout,
		Write:      shortTimeout,
		Idle:       shortTimeout,
	})
	go func() { _ = httpSrv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDial()
	client, dialResp, err := websocket.Dial(dialCtx, "ws://"+ln.Addr().String()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// The handshake response body is empty on a successful upgrade, but it is
	// still a body and the linter is right to want it closed.
	if dialResp != nil && dialResp.Body != nil {
		_ = dialResp.Body.Close()
	}
	defer client.CloseNow()

	// Wait past the server's write timeout. If the hijacked connection had
	// inherited it, the socket would already be dead by now.
	time.Sleep(3 * shortTimeout)

	if err := hub.Publish(context.Background(), "yard", []byte(`{"t":"still-alive"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	typ, data, err := client.Read(readCtx)
	if err != nil {
		t.Fatalf("read after %v (3x the server write timeout): %v — a hijacked "+
			"connection is now inheriting the server deadline; handleRealtime must "+
			"clear it explicitly", 3*shortTimeout, err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("message type = %v, want text", typ)
	}
	if string(data) != `{"t":"still-alive"}` {
		t.Fatalf("payload = %q, want the published message", data)
	}
}

// TestOriginHost pins the host extraction that feeds the WebSocket origin
// allowlist. Getting this wrong either blocks every legitimate handshake or
// (worse) widens what may open an authenticated socket.
func TestOriginHost(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://psycho-space.ru", "psycho-space.ru"},
		{"https://psycho-space.ru/", "psycho-space.ru"},
		{"http://localhost:8080", "localhost:8080"},
		{"", ""},
		{"://nonsense", ""},
	}
	for _, tt := range tests {
		if got := originHost(tt.in); got != tt.want {
			t.Errorf("originHost(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
