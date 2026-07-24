package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/logging"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/go-chi/chi/v5/middleware"
)

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
