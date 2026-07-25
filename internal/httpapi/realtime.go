package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// handleRealtime upgrades an approved session to a WebSocket and hands it to
// the hub. The path is deliberately game-agnostic (`/api/realtime?room=…`) so a
// second realtime feature never needs an nginx change.
func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	acc, ok := accountFromContext(r.Context())
	if !ok { // requireAuth ran before us; this is belt and braces.
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.d.Realtime == nil || s.d.RealtimeCtx == nil {
		writeError(w, r, http.StatusServiceUnavailable, "realtime_unavailable")
		return
	}

	room := r.URL.Query().Get("room")
	if room == "" {
		room = DefaultRoom
	}
	if !isKnownRoom(room) {
		writeError(w, r, http.StatusBadRequest, "unknown_room")
		return
	}

	// No deadline juggling here on purpose. The server sets ReadTimeout and
	// WriteTimeout, and the folklore (golang/go#8296) is that a hijacked
	// connection inherits them and a WebSocket therefore dies when they expire.
	// That has not been true for a long time: net/http's hijackLocked calls
	// rwc.SetDeadline(time.Time{}) before handing the connection over, so the
	// deadlines are already gone by the time Accept sees it.
	// TestHijackedConnOutlivesServerWriteTimeout pins that behaviour, so if a
	// future Go release changes it we find out from a failing test rather than
	// from sockets dying in production.

	// Origin is validated by the library and must stay validated: the
	// same-origin policy does not apply to WebSocket, so a cross-origin page
	// could otherwise open an authenticated socket. SameSite=Strict on the
	// session cookie already blunts that, but relying on one browser-side
	// control is not enough. Never set InsecureSkipVerify.
	opts := &websocket.AcceptOptions{}
	if host := originHost(s.d.Config.BaseURL); host != "" {
		opts.OriginPatterns = []string{host}
	}

	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		// Accept has already written a response.
		slog.WarnContext(r.Context(), "realtime: upgrade refused", "err", err)
		return
	}

	conn := realtime.NewConn(uuid.NewString(), acc.ID, room, ws, s.d.RealtimeHandler)
	if err := s.d.Realtime.Register(r.Context(), conn, room); err != nil {
		// Registration runs after the 101, so a refusal cannot be an HTTP status
		// — it has to go out on the socket, and Refuse is what delivers it since
		// no write pump exists yet.
		//
		// Hitting the connection cap gets its own reason rather than the generic
		// one. It is the only refusal here a person can act on ("close another
		// tab"), and a client that is told nothing specific cannot tell it from
		// an eviction, a restart, or the network going away.
		reason := "cannot register"
		if errors.Is(err, realtime.ErrTooManyConnections) {
			reason = realtime.ReasonTooManyConnections
		}
		conn.Refuse(r.Context(), realtime.CloseTryAgainLater, reason)
		slog.WarnContext(r.Context(), "realtime: register refused",
			"err", err, "account_id", acc.ID, "room", room, "reason", reason)
		return
	}

	slog.InfoContext(r.Context(), "realtime connected",
		"conn_id", conn.ID(), "account_id", acc.ID, "room", room,
		"trace_id", observability.TraceID(r.Context()))

	// Serve blocks until both pumps exit. The context is the hub's, not the
	// request's: the request context is cancelled the moment this handler
	// returns, which would kill the socket immediately.
	conn.Serve(s.d.RealtimeCtx, s.d.Realtime)

	slog.InfoContext(r.Context(), "realtime disconnected",
		"conn_id", conn.ID(), "account_id", acc.ID, "room", room)
}

// originHost extracts the host a WebSocket handshake must originate from.
func originHost(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// Rooms are a closed set: an open-ended room name would let any client create
// unbounded rooms, and there is no reason to allow it.
//
// DefaultRoom is exported so the composition root can hand the same name to
// whichever service publishes into it, instead of that service spelling the
// string out a second time. One room serves a whole game — locations inside a
// game are a field in its own messages, deliberately not rooms, because a room
// per location would both make this platform file learn a game's vocabulary and
// scatter a handful of players across empty rooms.
const DefaultRoom = "yard"

func isKnownRoom(room string) bool { return room == DefaultRoom }
