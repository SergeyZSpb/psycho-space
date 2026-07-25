package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClientIPTrustsProxyHeaderOnlyFromLoopback pins the rate-limit key down.
// The header may only be believed when the request came from the local nginx;
// X-Forwarded-For must never influence the key, because nginx appends to the
// client's own value and its leftmost entry is therefore attacker-controlled.
func TestClientIPTrustsProxyHeaderOnlyFromLoopback(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		realIP     string
		forwarded  string
		want       string
	}{
		{
			name:       "proxied request keys off X-Real-IP",
			remoteAddr: "127.0.0.1:41234",
			realIP:     "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "spoofed X-Forwarded-For is ignored",
			remoteAddr: "127.0.0.1:41234",
			realIP:     "203.0.113.7",
			forwarded:  "198.51.100.99, 203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "X-Forwarded-For alone cannot set the key",
			remoteAddr: "127.0.0.1:41234",
			forwarded:  "198.51.100.99",
			want:       "127.0.0.1",
		},
		{
			name:       "non-loopback peer may not claim an address",
			remoteAddr: "203.0.113.10:5555",
			realIP:     "198.51.100.99",
			want:       "203.0.113.10",
		},
		{
			name:       "unparsable X-Real-IP falls back to the peer",
			remoteAddr: "127.0.0.1:41234",
			realIP:     "not-an-ip",
			want:       "127.0.0.1",
		},
		{
			name:       "empty X-Real-IP falls back to the peer",
			remoteAddr: "127.0.0.1:41234",
			realIP:     "   ",
			want:       "127.0.0.1",
		},
		{
			name:       "IPv6 loopback proxy is trusted too",
			remoteAddr: "[::1]:41234",
			realIP:     "2001:db8::1",
			want:       "2001:db8::1",
		},
		{
			name:       "RemoteAddr without a port still yields a key",
			remoteAddr: "192.0.2.5",
			want:       "192.0.2.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.realIP != "" {
				r.Header.Set("X-Real-IP", tt.realIP)
			}
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := clientIP(r); got != tt.want {
				t.Fatalf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRateLimitNotBypassableByForwardedHeader drives the real middleware: a
// client rotating X-Forwarded-For must still be counted as one client. Before
// the fix, chi's RealIP rewrote RemoteAddr from that header and every request
// below landed in a fresh bucket, so the limiter never fired — including on the
// endpoint that spends money per call.
func TestRateLimitNotBypassableByForwardedHeader(t *testing.T) {
	s := NewServer(Deps{WebFS: testWebFS()})
	limited := s.rateLimit(3, time.Minute)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	))

	statuses := make([]int, 0, 5)
	for i := range 5 {
		r := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
		r.RemoteAddr = "127.0.0.1:41234"
		r.Header.Set("X-Real-IP", "203.0.113.7")
		// A different forged hop every time — the only thing the attacker controls.
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		rr := httptest.NewRecorder()
		limited.ServeHTTP(rr, r)
		statuses = append(statuses, rr.Code)
	}

	for i, code := range statuses[:3] {
		if code != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200 (within the limit)", i+1, code)
		}
	}
	for i, code := range statuses[3:] {
		if code != http.StatusTooManyRequests {
			t.Fatalf("request %d: status %d, want 429 despite the rotated X-Forwarded-For", i+4, code)
		}
	}
}

// TestRateLimitSeparatesRealClients guards the other direction: the fix must not
// collapse genuinely different clients into one bucket behind the proxy.
func TestRateLimitSeparatesRealClients(t *testing.T) {
	s := NewServer(Deps{WebFS: testWebFS()})
	limited := s.rateLimit(1, time.Minute)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	))

	for _, ip := range []string{"203.0.113.7", "203.0.113.8", "203.0.113.9"} {
		r := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
		r.RemoteAddr = "127.0.0.1:41234"
		r.Header.Set("X-Real-IP", ip)
		rr := httptest.NewRecorder()
		limited.ServeHTTP(rr, r)
		if rr.Code != http.StatusOK {
			t.Fatalf("first request from %s: status %d, want 200", ip, rr.Code)
		}
	}
}
