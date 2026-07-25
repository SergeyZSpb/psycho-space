package httpapi

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/logging"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/go-chi/chi/v5/middleware"
)

// clientIP resolves the address a request is rate-limited by.
//
// The app listens on loopback and is reachable only through nginx, which sets
// X-Real-IP from $remote_addr — a value nginx overwrites on every request, so a
// client cannot forge it. X-Forwarded-For is deliberately NOT consulted: nginx
// passes it as $proxy_add_x_forwarded_for, which *appends* the peer address to
// whatever the client sent, leaving its leftmost entry attacker-controlled.
//
// chi's middleware.RealIP trusted exactly that leftmost entry and overwrote
// r.RemoteAddr with it, so anyone could bypass every per-IP rate limit — the
// login limiter and the one guarding the paid LLM endpoint included — by
// varying a header per request. It is no longer used.
//
// The header is honoured only when the request actually arrived from the local
// proxy; a direct hit on the app port (should it ever be exposed) is keyed by
// its real peer address instead.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isLocalPeer(host) {
		return host
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return ip.String()
		}
	}
	return host
}

// isLocalPeer reports whether the direct TCP peer is the reverse proxy on this
// host. Requests reaching the app any other way are not trusted to carry
// forwarding headers.
func isLocalPeer(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// accountLogContext installs a per-request account-id holder so every log line
// carries account_id (filled by currentAccount once the session resolves,
// "anonymous" until then). Must run before requestLogger.
func accountLogContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(logging.WithAccountHolder(r.Context())))
	})
}

// traceHeader echoes the request's trace id back to the client so it is visible
// in the browser/network tab and can be surfaced in error modals.
func traceHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := observability.TraceID(r.Context()); id != "" {
			w.Header().Set("X-Trace-Id", id)
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimit caps request bodies to n bytes.
func bodyLimit(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger emits one structured line per request. It deliberately does NOT
// log the client IP or any personal data (152-ФЗ minimisation); nginx keeps
// access logs with IPs for ops/fail2ban.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" { // kubelet-style probe noise
			next.ServeHTTP(w, r)
			return
		}
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		slog.InfoContext(r.Context(), "http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"trace_id", observability.TraceID(r.Context()),
		)
	})
}
