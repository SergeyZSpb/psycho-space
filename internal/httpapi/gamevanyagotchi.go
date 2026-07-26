package httpapi

import (
	"errors"
	"net/http"

	"github.com/SergeyZSpb/psycho-space/internal/gamevanyagotchi"
	"github.com/go-chi/chi/v5"
)

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

// handleGameVanyagotchiAction applies one catalogue action and answers with the
// server's recomputed state.
//
// The verb is a path segment validated against the catalogue, and the body
// carries nothing at all. The client says "heal", never "set hp to 80", so there
// is no number in the request to forge — and because the allowlist is the
// catalogue rather than a switch in here, an action added to content.go is
// reachable without touching this file or the SPA.
// DEBT, and named rather than left to drift (CLAUDE.md → *No legacy code*).
// The SPA no longer calls this: verbs travel over the socket, through the same
// Service.Do this handler calls. It survives only because six integration tests
// drive a verb through it, and moving them to Service.Do is the one thing
// between this route and deletion. Nothing new may be built on it.
func (s *Server) handleGameVanyagotchiAction(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyagotchiAvailable(w, r) {
		return
	}
	viewer, _ := accountFromContext(r.Context())
	st, err := s.d.GameVanyagotchi.Act(r.Context(), viewer.ID, chi.URLParam(r, "action"))
	if err != nil {
		switch {
		case errors.Is(err, gamevanyagotchi.ErrUnknownAction):
			writeError(w, r, http.StatusNotFound, "unknown_action")
		case errors.Is(err, gamevanyagotchi.ErrPetDead):
			// 409 rather than 422: the request is perfectly well formed and
			// would succeed against a living pet. What is wrong is the state of
			// the world, and the client's remedy is a different action.
			writeError(w, r, http.StatusConflict, "pet_dead")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal")
		}
		return
	}
	writeJSON(w, http.StatusOK, st)
}
