package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/gamevanyadum"
	"github.com/google/uuid"
)

// «ВАНЯДУМ» over HTTP, which is deliberately only the edges of a run.
//
// Starting a run, resuming one after a page reload, giving up, and looking at
// what you have already done. NOTHING ABOUT PLAYING IS HERE: input arrives as a
// socket frame and the world goes back as a snapshot, twenty times a second,
// because a request per step would be absurd and because the socket's own rate
// limit is a bound this game wants rather than one it has to work around.
//
// The level is served exactly once, by whichever of the two run endpoints
// produced the arena. It is a few kilobytes, it never changes for the life of a
// run, and re-sending it on a snapshot would be the single most expensive
// mistake available in this game.

// gameVanyadumAvailable reports whether the game is wired, and answers 503 when
// it is not. Deps documents that a field may be nil in a caller that does not
// exercise its routes, so nil is an expected state rather than a bug.
func (s *Server) gameVanyadumAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.d.GameVanyadum != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "game_unavailable")
	return false
}

// handleGameVanyadumConfig serves the content catalogue: the player's
// dimensions, the pickups, the surfaces the client generates textures from, and
// the rates it has to match.
//
// The SPA hardcodes no number from this game. The splash screen's rules
// cheatsheet is built from what this returns, so retuning a constant in the
// game's content.go changes what the player is told with no client change.
func (s *Server) handleGameVanyadumConfig(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyadumAvailable(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.d.GameVanyadum.Config())
}

// vanyadumRun is a started or resumed run as the browser receives it: which run
// it is, and the whole level to build meshes from.
type vanyadumRun struct {
	RunID string              `json:"run_id"`
	Level *gamevanyadum.Level `json:"level"`
	Room  string              `json:"room"`
}

// handleGameVanyadumStart begins a run and returns its level.
//
// A second start while one is already going is a 409 rather than a silent
// replacement. Dropping the existing arena would throw away a run somebody is in
// the middle of on their other tab, and nothing here can tell which one they
// meant — so the client is told, and offers «сдаться» (the DELETE below).
func (s *Server) handleGameVanyadumStart(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyadumAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := uuid.Parse(acc.ID)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	a, err := s.d.GameVanyadum.StartRun(id, time.Now())
	switch {
	case errors.Is(err, gamevanyadum.ErrRunInProgress):
		writeError(w, r, http.StatusConflict, "run_in_progress")
		return
	case errors.Is(err, gamevanyadum.ErrTooManyArenas):
		writeError(w, r, http.StatusServiceUnavailable, "too_busy")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "gamevanyadum: start run", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusCreated, vanyadumRun{
		RunID: a.RunID.String(),
		Level: a.Level,
		Room:  gamevanyadum.Room,
	})
}

// handleGameVanyadumCurrent returns the run already in progress, or 404.
//
// This is what a page reload needs, and it is why the level is not sent over the
// socket: after a refresh the browser has a session, a socket and no geometry at
// all, and the arena it is about to reconnect to is still being simulated. An
// arena survives a reload by design — see AbandonGrace.
func (s *Server) handleGameVanyadumCurrent(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyadumAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := uuid.Parse(acc.ID)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	a, ok := s.d.GameVanyadum.CurrentRun(id)
	if !ok {
		writeError(w, r, http.StatusNotFound, "no_run")
		return
	}
	writeJSON(w, http.StatusOK, vanyadumRun{
		RunID: a.RunID.String(),
		Level: a.Level,
		Room:  gamevanyadum.Room,
	})
}

// handleGameVanyadumAbandon throws a run away without recording it.
//
// A run somebody walked out of is not a result, so nothing is written — the only
// rows in this game's one table are runs that actually ended.
func (s *Server) handleGameVanyadumAbandon(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyadumAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := uuid.Parse(acc.ID)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.d.GameVanyadum.AbandonRun(id)
	w.WriteHeader(http.StatusNoContent)
}

// vanyadumRunRow is one finished run as the splash screen shows it.
type vanyadumRunRow struct {
	Success   bool      `json:"success"`
	Seconds   int       `json:"seconds"`
	Beer      int       `json:"beer"`
	CreatedAt time.Time `json:"created_at"`
}

// handleGameVanyadumMyRuns lists the caller's own recent runs.
//
// It exists so that "the run was written" is something a person can check in
// production without opening a database, which is what makes the persistence
// half of this iteration verifiable from outside the code.
func (s *Server) handleGameVanyadumMyRuns(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyadumAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := uuid.Parse(acc.ID)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	runs, err := s.d.GameVanyadum.RecentRuns(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "gamevanyadum: recent runs", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]vanyadumRunRow, 0, len(runs))
	for _, run := range runs {
		out = append(out, vanyadumRunRow{
			Success:   run.Success,
			Seconds:   run.Seconds,
			Beer:      run.Beer,
			CreatedAt: run.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}
