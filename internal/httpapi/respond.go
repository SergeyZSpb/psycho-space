package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/SergeyZSpb/psycho-space/internal/observability"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validUUID reports whether s is a canonical UUID (guards ::uuid casts from
// turning malformed input into 500s).
func validUUID(s string) bool { return uuidRe.MatchString(s) }

// writeJSON serialises v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode failed", "err", err)
	}
}

// writeError sends a stable machine-readable error code plus the request's
// trace_id, so the client can show it and the user can quote it to support.
func writeError(w http.ResponseWriter, r *http.Request, status int, code string) {
	writeJSON(w, status, map[string]string{
		"error":    code,
		"trace_id": observability.TraceID(r.Context()),
	})
}
