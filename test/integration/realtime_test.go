//go:build integration

package integration

import (
	"context"
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

	// Let the handler register before draining, using a delivered broadcast as
	// the barrier rather than a sleep.
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	ready := make(chan struct{})
	go func() {
		for {
			_, data, err := conn.Read(readCtx)
			if err != nil {
				return
			}
			if string(data) == `{"t":"ready"}` {
				close(ready)
				return
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = hub.Publish(context.Background(), "yard", []byte(`{"t":"ready"}`))
		select {
		case <-ready:
			deadline = time.Time{} // registered
		case <-time.After(50 * time.Millisecond):
			continue
		}
		break
	}

	stopHub()
	select {
	case <-hub.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("hub did not drain")
	}

	// The client now sees a close, and it carries the going-away code so the
	// client knows to reconnect promptly rather than back off.
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelClose()
	if _, _, err := conn.Read(closeCtx); err == nil {
		t.Fatal("socket still readable after the hub drained")
	}
}
