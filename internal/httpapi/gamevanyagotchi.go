package httpapi

import "net/http"

// available reports whether the game is wired, and answers 503 when it is not.
//
// Deps documents that a field may be nil in a caller that does not exercise its
// routes, so nil is an expected state rather than a bug — and without this the
// pet handlers would dereference it, panic, and be recovered into a bare 500
// that says nothing. The same shape the realtime and asset routes already use.
func (s *Server) gameVanyagotchiAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.d.GameVanyagotchi != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "game_unavailable")
	return false
}

// handleGameVanyagotchiConfig serves the content catalogue: stats and their
// rates, the actions, the skins, the locations.
//
// Everything the client needs in order to render and label the game comes from
// here, so the SPA hardcodes no key and no number — which is what makes adding a
// stat or retitling an action a backend-only change.
func (s *Server) handleGameVanyagotchiConfig(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyagotchiAvailable(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.d.GameVanyagotchi.Config())
}

// handleGameVanyagotchiState returns the caller's pet with every stat decayed to
// this instant.
//
// It is a GET that writes, which is worth naming rather than hiding: it creates
// the pet on first sight and records a death the first time one is observed.
// Both are idempotent and both are lazy on purpose — the alternative to writing
// on read is a background job, and this game has none by design.
func (s *Server) handleGameVanyagotchiState(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyagotchiAvailable(w, r) {
		return
	}
	viewer, _ := accountFromContext(r.Context())
	st, err := s.d.GameVanyagotchi.State(r.Context(), viewer.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// There is no action handler here, and that absence is the design.
//
// A verb arrives over the socket instead, as one `vanyagotchi_do` frame, and is
// interpreted by the one `Service.Do` that a replay also goes through. What the
// server decided comes back as STATE — a line over the player's own Ваня and a
// push of the pet — never as a response body, because the 5 Hz full-state frame
// already reconciles the yard and a verb that owes a reply is a verb with two
// ways of being answered. See ADR-043.
