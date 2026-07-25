//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/coder/websocket"
)

// wsURL turns a test server's http URL into a ws one.
func wsURL(base, path string) string {
	return "ws" + strings.TrimPrefix(base, "http") + path
}

// cookieHeader renders a logged-in client's jar as a Cookie header, since the
// WebSocket dialer takes raw headers rather than a jar.
func cookieHeader(t *testing.T, cli *http.Client, base string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", base, err)
	}
	var parts []string
	for _, c := range cli.Jar.Cookies(u) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// dialRealtime opens a socket carrying the given session cookie (empty = none).
func dialRealtime(t *testing.T, appURL, cookie, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", cookie)
	}
	if origin != "" {
		h.Set("Origin", origin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL(appURL, "/api/realtime"), &websocket.DialOptions{HTTPHeader: h})
}

// readFrames starts the single reader for a socket and streams every frame it
// delivers. Only one goroutine may read a WebSocket, so tests take frames from
// here rather than calling Read themselves — otherwise a readiness probe and an
// assertion end up competing for the same frame.
func readFrames(t *testing.T, conn *websocket.Conn) <-chan []byte {
	t.Helper()
	out := make(chan []byte, 32)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		defer close(out)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			select {
			case out <- data:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// waitRegistered publishes a probe until one comes back, which proves the
// handler has registered the socket with the hub. That is a condition, not a
// duration — the handler registers after the upgrade response has already been
// returned to the dialer.
func waitRegistered(t *testing.T, hub *realtime.Hub, frames <-chan []byte) {
	t.Helper()
	const probe = `{"t":"probe"}`
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := hub.Publish(context.Background(), "yard", []byte(probe)); err != nil {
			t.Fatalf("publish: %v", err)
		}
		select {
		case got, ok := <-frames:
			if !ok {
				t.Fatal("socket closed while waiting for registration")
			}
			if string(got) == probe {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("socket never registered with the hub")
}

// expectBye requires a bye frame carrying wantCode to arrive, skipping any
// leftover readiness probes.
func expectBye(t *testing.T, frames <-chan []byte, wantCode int) {
	t.Helper()
	for {
		select {
		case data, ok := <-frames:
			if !ok {
				t.Fatalf("socket closed without a bye frame (wanted code %d)", wantCode)
			}
			var b realtime.Bye
			if err := json.Unmarshal(data, &b); err != nil || b.T != realtime.TypeBye {
				continue
			}
			if b.Code != wantCode {
				t.Fatalf("bye code = %d, want %d (reason %q)", b.Code, wantCode, b.Reason)
			}
			return
		case <-time.After(3 * time.Second):
			t.Fatalf("no bye frame arrived (wanted code %d)", wantCode)
		}
	}
}

// expectClosed requires the frame stream to end — i.e. the socket really did go
// away rather than merely being told it would.
func expectClosed(t *testing.T, frames <-chan []byte) {
	t.Helper()
	for {
		select {
		case _, ok := <-frames:
			if !ok {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("socket is still open")
		}
	}
}

// TestRealtimeRequiresApprovedSession confirms the socket is gated by exactly
// the same rule as every other resource: a valid session AND status=approved.
// The check happens before the upgrade, so an unauthorised caller gets the
// normal JSON error envelope rather than a half-open socket.
func TestRealtimeRequiresApprovedSession(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, _, _ := buildAppRealtime(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	t.Run("anonymous is refused", func(t *testing.T) {
		_, resp, err := dialRealtime(t, app.URL, "", "http://localhost")
		if err == nil {
			t.Fatal("anonymous dial succeeded; want refusal")
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %v, want 401", resp)
		}
	})

	t.Run("pending account is refused", func(t *testing.T) {
		cli := loginAs(t, app.URL, "7001", "user")
		cookie := cookieHeader(t, cli, app.URL)
		setRoleStatus(t, accountIDByUID(t, "7001"), "user", "pending")
		_, resp, err := dialRealtime(t, app.URL, cookie, "http://localhost")
		if err == nil {
			t.Fatal("pending dial succeeded; want refusal")
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %v, want 403", resp)
		}
	})

	t.Run("approved account connects", func(t *testing.T) {
		user := loginAs(t, app.URL, "7002", "user")
		userCookie := cookieHeader(t, user, app.URL)
		conn, _, err := dialRealtime(t, app.URL, userCookie, "http://localhost")
		if err != nil {
			t.Fatalf("approved dial failed: %v", err)
		}
		conn.CloseNow()
	})
}

// TestRealtimeRejectsForeignOrigin is the CSWSH guard. The same-origin policy
// does not apply to WebSocket, so without this check a page on another origin
// could open an authenticated socket using the victim's cookie.
func TestRealtimeRejectsForeignOrigin(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, _, _ := buildAppRealtime(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	user := loginAs(t, app.URL, "7003", "user")
	userCookie := cookieHeader(t, user, app.URL)
	_, resp, err := dialRealtime(t, app.URL, userCookie, "https://evil.example")
	if err == nil {
		t.Fatal("dial from a foreign origin succeeded; want refusal")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

// TestRealtimeDeliversBroadcast proves the whole path end to end: a real
// upgrade through the router and middleware, registration in the hub, and a
// published message arriving on the wire.
func TestRealtimeDeliversBroadcast(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _ := buildAppRealtime(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	user := loginAs(t, app.URL, "7004", "user")
	userCookie := cookieHeader(t, user, app.URL)
	conn, _, err := dialRealtime(t, app.URL, userCookie, "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// The handler registers after the upgrade returns, so retry the publish
	// until it lands rather than sleeping a fixed amount.
	readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got := make(chan string, 1)
	go func() {
		_, data, err := conn.Read(readCtx)
		if err == nil {
			got <- string(data)
		}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := hub.Publish(context.Background(), "yard", []byte(`{"t":"hello"}`)); err != nil {
			t.Fatalf("publish: %v", err)
		}
		select {
		case msg := <-got:
			if msg != `{"t":"hello"}` {
				t.Fatalf("payload = %q, want the published message", msg)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("published message never arrived on the socket")
}

// TestRealtimeClosesOversizedFrame covers the third latent problem: the global
// 1 MiB body limit wraps r.Body, which the hijack bypasses, so it does not
// bound WebSocket frames at all. SetReadLimit is what does.
func TestRealtimeClosesOversizedFrame(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, _, _ := buildAppRealtime(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	user := loginAs(t, app.URL, "7005", "user")
	userCookie := cookieHeader(t, user, app.URL)
	conn, _, err := dialRealtime(t, app.URL, userCookie, "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Comfortably over MaxFrameBytes.
	oversized := strings.Repeat("x", realtime.MaxFrameBytes*4)
	// The write itself may succeed (it is buffered) — the close arrives after.
	_ = conn.Write(ctx, websocket.MessageText, []byte(oversized))

	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("oversized frame was accepted; want the connection closed")
	}
}

// TestRealtimeDrainsOnHubShutdown is the second latent problem at the HTTP
// level: http.Server.Shutdown does not close hijacked connections, so cancelling
// the hub is what gives a connected client a close frame instead of a reset.
// The client can then tell a deploy from a network failure.
func TestRealtimeDrainsOnHubShutdown(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()

	handler, hub, stopHub := buildAppRealtime(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	user := loginAs(t, app.URL, "7006", "user")
	userCookie := cookieHeader(t, user, app.URL)
	conn, _, err := dialRealtime(t, app.URL, userCookie, "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)

	stopHub()
	select {
	case <-hub.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("hub did not drain")
	}

	// The last frame before the socket goes away says why, so the client can tell
	// a deploy from a network failure and reconnect promptly instead of backing
	// off. The transport close cannot carry that — see realtime.Conn.Close for
	// why the reason travels as a frame instead of a close code.
	//
	// This is also the ordering that made the mechanism subtle: shutdown cancels
	// the hub context and only then asks each connection to close, so the write
	// pump sees cancellation before the reason exists.
	expectBye(t, frames, realtime.CloseGoingAway)
	expectClosed(t, frames)
}

// TestRealtimeBlockDeliversTerminalBye covers the revocation path end to end,
// through the real admin endpoint rather than by poking the hub: blocking an
// account must both revoke its sessions and close its live sockets, and the
// socket must be told the reason is terminal so the client stops reconnecting
// instead of hammering the handshake forever.
func TestRealtimeBlockDeliversTerminalBye(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	handler, hub, _ := buildAppRealtime(t, vkSrv.URL)
	app := httptest.NewServer(handler)
	defer app.Close()

	victim := loginAs(t, app.URL, "7007", "user")
	victimCookie := cookieHeader(t, victim, app.URL)
	super := loginAs(t, app.URL, "7008", "superadmin")
	victimID := accountIDByUID(t, "7007")

	conn, _, err := dialRealtime(t, app.URL, victimCookie, "http://localhost")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	frames := readFrames(t, conn)
	waitRegistered(t, hub, frames)

	if s, _ := doJSON(t, super, http.MethodPost,
		app.URL+"/api/admin/accounts/"+victimID+"/block", nil); s != http.StatusNoContent {
		t.Fatalf("block returned %d, want 204", s)
	}

	expectBye(t, frames, realtime.CloseUnauthorized)
	expectClosed(t, frames)

	// And the terminal signal does not depend on that frame arriving: the block
	// revoked the session, so a reconnect is refused before any upgrade. This is
	// what the client actually keys "stop trying" off.
	if _, resp, err := dialRealtime(t, app.URL, victimCookie, "http://localhost"); err == nil {
		t.Fatal("a blocked account could reconnect")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reconnect status = %v, want 401 (session revoked)", resp)
	}
}
