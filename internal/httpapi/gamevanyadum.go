package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/gamevanyadum"
	"github.com/google/uuid"
)

// «ВАНЯДУМ» over HTTP, which is deliberately only what a socket cannot carry.
//
// Three reads and nothing else: the content catalogue, the building, and what
// you have already done. NOTHING ABOUT PLAYING IS HERE, and nothing about
// JOINING either — the room already carries an authenticated account, so opening
// the socket and saying hello is walking in, and there is no start endpoint to
// disagree with it.
//
// The level is served exactly once, here. It is a few kilobytes, it never
// changes for the life of a building, and re-sending it on a snapshot would be
// the single most expensive mistake available in this game. A client that comes
// back holding a stale one finds out from the world id on its ready frame.

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
// dimensions, the pickups, the surfaces the client generates textures from, the
// rates it has to match, and the building's own rules — how many people fit and
// how long a thing takes to come back.
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

// vanyadumWorld is the заброшка as the browser receives it: which building it
// is, what it was grown from, the whole level to build meshes out of, and the
// room to open a socket in.
//
// The seed is on the level too, and stating it twice in one response is
// deliberate: this is a once-per-session read, and a client caching the pair
// should not have to reach inside the geometry to find half of its own cache
// key.
type vanyadumWorld struct {
	WorldID string              `json:"world_id"`
	Seed    int64               `json:"seed"`
	Level   *gamevanyadum.Level `json:"level"`
	Room    string              `json:"room"`
}

// handleGameVanyadumWorld serves the building everybody is in, generating one if
// nobody has been here yet.
//
// It never answers "there is no заброшка", because there is always one to walk
// into — that is what an infinite match means. It also does not put the caller
// in it: this is a read, the socket is the door, and a single door is what stops
// the two disagreeing about who is inside.
func (s *Server) handleGameVanyadumWorld(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyadumAvailable(w, r) {
		return
	}
	id, level := s.d.GameVanyadum.World()
	writeJSON(w, http.StatusOK, vanyadumWorld{
		WorldID: id.String(),
		Seed:    level.Seed,
		Level:   level,
		Room:    gamevanyadum.Room,
	})
}

// vanyadumVisitRow is one recorded visit as the splash screen shows it.
type vanyadumVisitRow struct {
	Seed     int64     `json:"seed"`
	Seconds  int       `json:"seconds"`
	Beer     int       `json:"beer"`
	JoinedAt time.Time `json:"joined_at"`
}

// handleGameVanyadumMyVisits lists the caller's own recent visits.
//
// It exists so that "the visit was written" is something a person can check in
// production without opening a database, which is what makes the persistence
// half of this iteration verifiable from outside the code.
func (s *Server) handleGameVanyadumMyVisits(w http.ResponseWriter, r *http.Request) {
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
	visits, err := s.d.GameVanyadum.RecentVisits(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "gamevanyadum: recent visits", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]vanyadumVisitRow, 0, len(visits))
	for _, v := range visits {
		out = append(out, vanyadumVisitRow{
			Seed:     v.Seed,
			Seconds:  v.Seconds,
			Beer:     v.Beer,
			JoinedAt: v.JoinedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"visits": out})
}
