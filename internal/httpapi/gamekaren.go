package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strconv"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/gamekaren"
	"github.com/go-chi/chi/v5"
)

// «СИМУЛЯТОР КАРЕНА» over HTTP, which is deliberately only the edges of a shift.
//
// Clocking in, resuming after a page reload, walking out, and looking at what
// you and everybody else have already earned. NOTHING ABOUT PLAYING IS HERE:
// input arrives as a socket frame and the office goes back as a snapshot ten
// times a second, because a request per step would be absurd and because the
// socket's own rate limit is a bound this game wants rather than one it has to
// work around.
//
// Starting a shift returns NO LEVEL, which is the one thing that reads oddly
// next to «ВАНЯДУМ» and is the load-bearing simplification of this game: the
// office is static and already in the catalogue, so there is nothing per-shift
// to send. A shift start touches no Postgres row either — a shift is written
// once, when it ends.

// gameKarenAvailable reports whether the game is wired, and answers 503 when it
// is not. Deps documents that a field may be nil in a caller that does not
// exercise its routes, so nil is an expected state rather than a bug.
func (s *Server) gameKarenAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.d.GameKaren != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "game_unavailable")
	return false
}

// handleGameKarenConfig serves the content catalogue: the office the client
// draws, the money ramp, the movement rates it has to match, the boss, and both
// endings.
//
// The SPA hardcodes no number from this game. The splash screen's rules
// cheatsheet is GENERATED from what this returns, so retuning a constant in the
// game's content.go changes what the player is told with no client change —
// which is the whole reason the cheatsheet is derived rather than typed.
//
// Nothing here is decorated with uploaded art, and that is not an omission: this
// game's catalogue carries no art keys at all yet (iteration 1 draws CSS shapes
// on purpose), so there is nothing for the blob store to point at. The day the
// office grows a sprite key, this handler grows the PresentArtKeys pass the
// other two games already have — and the game's service grows the asset-store
// dependency that pass needs, which it deliberately does not have today.
func (s *Server) handleGameKarenConfig(w http.ResponseWriter, r *http.Request) {
	if !s.gameKarenAvailable(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.d.GameKaren.Config())
}

// karenShift is a started or resumed shift as the browser receives it.
//
// The room travels with it rather than being hardcoded in the view, so the name
// of the socket room lives in exactly one place — the game's own package.
type karenShift struct {
	ShiftID string `json:"shift_id"`
	Room    string `json:"room"`
}

// handleGameKarenStart clocks in.
//
// A second start while one is already going is a 409 rather than a silent
// replacement: dropping the occupant would throw away a shift somebody is in the
// middle of on their other tab, and nothing here can tell which one they meant —
// so the client is told, and offers «УЙТИ» (the DELETE below).
//
// A full office is a 503 rather than a 409, because it is a fact about the
// process and not about the caller: nothing they did is wrong and trying again
// later is the correct response.
func (s *Server) handleGameKarenStart(w http.ResponseWriter, r *http.Request) {
	if !s.gameKarenAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	shiftID, err := s.d.GameKaren.StartShift(r.Context(), acc.ID)
	switch {
	case errors.Is(err, gamekaren.ErrShiftInProgress):
		writeError(w, r, http.StatusConflict, "shift_in_progress")
		return
	case errors.Is(err, gamekaren.ErrOfficeFull):
		writeError(w, r, http.StatusServiceUnavailable, "office_full")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "gamekaren: start shift", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusCreated, karenShift{ShiftID: shiftID, Room: gamekaren.Room})
}

// handleGameKarenCurrent returns the shift already in progress, or 404.
//
// This is what a page reload needs: after a refresh the browser has a session, a
// socket and no idea which shift it is about to reconnect to, while the office
// is still being simulated without it. An occupant survives a reload by design —
// see AbandonGrace.
func (s *Server) handleGameKarenCurrent(w http.ResponseWriter, r *http.Request) {
	if !s.gameKarenAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	shiftID, ok := s.d.GameKaren.CurrentShift(acc.ID)
	if !ok {
		writeError(w, r, http.StatusNotFound, "no_shift")
		return
	}
	writeJSON(w, http.StatusOK, karenShift{ShiftID: shiftID, Room: gamekaren.Room})
}

// handleGameKarenLeave walks out, which is one of this game's two endings.
//
// Unlike «ВАНЯДУМ»'s «сдаться», this one IS a result and IS written down: the
// point of the game is the money, and money earned by standing still until you
// got bored counts exactly as much as money earned until the bald man reached
// you. A shift shorter than MinShiftSeconds is still dropped by the service —
// that is a rule about noise, not about how it ended.
func (s *Server) handleGameKarenLeave(w http.ResponseWriter, r *http.Request) {
	if !s.gameKarenAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	err := s.d.GameKaren.LeaveShift(r.Context(), acc.ID)
	switch {
	case errors.Is(err, gamekaren.ErrNoShift):
		writeError(w, r, http.StatusNotFound, "no_shift")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "gamekaren: leave shift", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// karenShiftRow is one finished shift as the splash screen shows it.
type karenShiftRow struct {
	Cause     string    `json:"cause"`
	Salary    float64   `json:"salary"`
	Seconds   float64   `json:"seconds"`
	CreatedAt time.Time `json:"created_at"`
}

// karenTopRow is one line of the leaderboard: somebody's best shift.
//
// A name and three numbers, and deliberately no identifier — the board is read
// by everybody in the office, so it publishes no more about a person than the
// display name they already show on the wishlist.
type karenTopRow struct {
	Name    string  `json:"name"`
	Cause   string  `json:"cause"`
	Salary  float64 `json:"salary"`
	Seconds float64 `json:"seconds"`
}

// karenLimit reads the ?limit= query parameter, leaving the bound itself to the
// service.
//
// A missing or unparseable value becomes 0, which the service reads as "your
// default" — the clamp lives next to the SQL rather than here, so there is one
// place a page size is decided rather than one per caller.
func karenLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// handleGameKarenMyShifts lists the caller's own recent shifts.
//
// It exists so that "the shift was written" is something a person can check in
// production without opening a database, which is what makes the persistence
// half of this iteration verifiable from outside the code.
func (s *Server) handleGameKarenMyShifts(w http.ResponseWriter, r *http.Request) {
	if !s.gameKarenAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	shifts, err := s.d.GameKaren.RecentShifts(r.Context(), acc.ID, karenLimit(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "gamekaren: recent shifts", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]karenShiftRow, 0, len(shifts))
	for _, sh := range shifts {
		out = append(out, karenShiftRow{
			Cause:     sh.Cause,
			Salary:    sh.Salary,
			Seconds:   sh.Seconds,
			CreatedAt: sh.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"shifts": out})
}

// handleGameKarenTopShifts returns the best shift per account.
//
// Per ACCOUNT rather than per row, decided in the SQL: one very good afternoon
// would otherwise fill the whole board and the leaderboard would stop being a
// list of people.
//
// The name is resolved here rather than joined in the query because a display
// name is encrypted at rest and only the account service can read it. A lookup
// that fails leaves the row nameless rather than failing the board — a board is
// not worth a 500, and the numbers are the point.
func (s *Server) handleGameKarenTopShifts(w http.ResponseWriter, r *http.Request) {
	if !s.gameKarenAvailable(w, r) {
		return
	}
	shifts, err := s.d.GameKaren.TopShifts(r.Context(), karenLimit(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "gamekaren: top shifts", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]karenTopRow, 0, len(shifts))
	for _, sh := range shifts {
		name := ""
		if s.d.Accounts != nil {
			if a, err := s.d.Accounts.GetByID(r.Context(), sh.AccountID.String()); err == nil {
				name = a.DisplayName()
			}
		}
		out = append(out, karenTopRow{
			Name:    name,
			Cause:   sh.Cause,
			Salary:  sh.Salary,
			Seconds: sh.Seconds,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"shifts": out})
}

// handleGameKarenAvatar redirects to the picture to draw on a colleague.
//
// Asked for by the PSEUDONYM the snapshot already carried, which is what keeps a
// couple of hundred characters of URL off a frame that repeats ten times a second
// per viewer, and keeps a durable identifier off the wire entirely (ADR-037). One
// cached GET per face, per shift.
func (s *Server) handleGameKarenAvatar(w http.ResponseWriter, r *http.Request) {
	if s.d.GameKaren == nil {
		writeError(w, r, http.StatusServiceUnavailable, "game_unavailable")
		return
	}
	target, ok := s.d.GameKaren.AvatarFor(chi.URLParam(r, "peer"))
	// CHECKED BEFORE IT IS TRUSTED, even though it came out of our own database.
	// It was stored exactly as the provider returned it and validated nowhere on
	// the way in, so this is the first place anything has looked at it — and an
	// unvalidated redirect target is an open redirect, which turns this origin
	// into a credible way to send somebody anywhere. Absolute https with a host,
	// or it did not happen, and a value that fails is treated as no picture at
	// all so the plane simply draws a plain figure.
	if ok {
		u, err := neturl.Parse(target)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			slog.WarnContext(r.Context(), "gamekaren: refusing to redirect to a stored avatar that is not absolute https")
			ok = false
		} else {
			// Re-serialised from the parse rather than passed through, so what
			// leaves here is a URL built out of components this process checked.
			target = u.String()
		}
	}
	if !ok {
		// NEVER CACHED, and unlike the yard there is no permanent-miss case to
		// distinguish: every peer in this office is a person, so a miss means
		// either that they have no picture or that they have just walked out —
		// and neither is a fact worth a browser remembering for half an hour.
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, r, http.StatusNotFound, "no_avatar")
		return
	}
	// Private, because the answer is about one person and a shared cache must not
	// keep it. Thirty minutes: a face is the most static thing this game serves,
	// and re-asking costs a request per colleague per shift.
	w.Header().Set("Cache-Control", "private, max-age=1800")
	// 302 rather than 301: the mapping is per-process and the picture changes
	// when its owner changes it, so nothing here is permanent.
	//nolint:gosec // G710: the target is parsed, required to be absolute https with a host, and
	// re-serialised from that parse immediately above, so it is not the unvalidated taint the
	// analysis takes it for. An allowlist of provider CDN hostnames is not knowable here and
	// would fail closed on every face the day one changed.
	http.Redirect(w, r, target, http.StatusFound)
}
