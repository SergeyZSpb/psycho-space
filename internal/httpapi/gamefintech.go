package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strconv"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/gamefintech"
	"github.com/go-chi/chi/v5"
)

// «СИМУЛЯТОР ФИНТЕХА» over HTTP, which is deliberately only the edges of a shift.
//
// Clocking in, resuming after a page reload, walking out, and looking at what
// you and everybody else have already earned. NOTHING ABOUT PLAYING IS HERE:
// input arrives as a socket frame and the office goes back as a snapshot ten
// times a second, because a request per step would be absurd and because the
// socket's own rate limit is a bound this game wants rather than one it has to
// work around.
//
// Starting a shift returns NO GEOMETRY, only the IDENTITY of the floor it was
// joined on. The layout rides the catalogue the client already fetched to draw
// its splash screen — as it always has — but the office can now be rebuilt while
// a tab sits on that splash, so the shift response carries the floor's content
// hash and a client whose cached catalogue names a different one refetches before
// it draws anything. Sixteen bytes, once per shift, against re-sending a floor
// plan on every start. A shift start still touches no Postgres row: a shift is
// written once, when it ends.

// gameFintechAvailable reports whether the game is wired, and answers 503 when it
// is not. Deps documents that a field may be nil in a caller that does not
// exercise its routes, so nil is an expected state rather than a bug.
func (s *Server) gameFintechAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.d.GameFintech != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "game_unavailable")
	return false
}

// handleGameFintechConfig serves the content catalogue: the office the client
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
func (s *Server) handleGameFintechConfig(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.d.GameFintech.Config())
}

// The admin section's one read: the floor in force, everything needed to draw it,
// and the two facts about it that are not in the geometry.
//
// UNDER THE GAME'S OWN ROUTE GROUP AND NOT UNDER /api/admin, which is the whole
// of ADR-028 applied to a control panel: deleting this game has to stay «remove
// its package, its migration, its routes and its views», and a level editor
// hanging off the account-administration surface would leave a game-shaped hole in
// admin.go. The direction that IS allowed is this one — a game depending on the
// generic requireAdmin middleware, which knows nothing about any game.
type fintechAdminLayout struct {
	Office fintechAdminOffice `json:"office"`
	Layout gamefintech.Layout `json:"layout"`
	// Source is where this floor came from and InstalledAt is when it arrived.
	Source      string    `json:"source"`
	InstalledAt time.Time `json:"installed_at"`
	// Occupants is how many people are standing on it right now, which is what
	// makes «rebuild the office» a decision rather than a button.
	Occupants int `json:"occupants"`
	// Kinds is the taxonomy with its Russian labels, so the plan's legend names
	// what it is drawing rather than colouring three anonymous squares.
	Kinds []fintechAdminKind `json:"kinds"`
	// Spots are the fixed catalogue points furniture has to keep off. They are
	// drawn on the plan because «spot_blocked» otherwise names a place with
	// nothing on it, and the only fix available to an admin is dragging at random.
	Spots []fintechAdminSpot `json:"spots"`
	// Ended is how many shifts the request that produced this payload threw out.
	// Absent on a plain read, which is what makes it a report of what just
	// happened rather than a fact about the floor.
	Ended *int `json:"ended,omitempty"`
}

// fintechAdminOffice is the room and the rules that bound what may stand in it.
type fintechAdminOffice struct {
	W            float64 `json:"w"`
	H            float64 `json:"h"`
	PlayerRadius float64 `json:"player_radius"`
	BossRadius   float64 `json:"boss_radius"`
	MinGap       float64 `json:"min_gap"`
}

type fintechAdminKind struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type fintechAdminSpot struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	What string  `json:"what"`
}

// fintechAdminKinds pairs each kind with the Russian the legend shows. The pairs
// live here rather than in the game's catalogue because they are labels for an
// ADMIN surface — nothing a player ever sees — and the catalogue is what the game
// itself is made of.
var fintechAdminKinds = []fintechAdminKind{
	{Key: string(gamefintech.KindDesk), Label: "стол"},
	{Key: string(gamefintech.KindFlower), Label: "цветок"},
	{Key: string(gamefintech.KindTree), Label: "фикус"},
}

// handleGameFintechAdminLayout serves the floor in force to an admin.
func (s *Server) handleGameFintechAdminLayout(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, fintechAdminPayload(s.d.GameFintech.Floor(), nil))
}

// handleGameFintechAdminReroll rebuilds the office and throws everybody out.
//
// It answers with the SAME payload the read does, plus how many shifts it ended —
// so the page that pressed it redraws from the response rather than fetching
// again, and cannot briefly show the floor it has just replaced.
//
// The actor is logged, which is the one audit precedent this codebase has (see
// the forget path in admin.go). This is a destructive action taken on behalf of
// everybody who was working at the time, and «who rebuilt the office at 3am» is a
// question somebody will eventually ask.
func (s *Server) handleGameFintechAdminReroll(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	actor, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	ended, err := s.d.GameFintech.Renovate(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "gamefintech: renovate", "err", err, "actor_id", actor.ID)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	slog.InfoContext(r.Context(), "gamefintech: office rebuilt by hand", "actor_id", actor.ID, "ended", ended)
	writeJSON(w, http.StatusOK, fintechAdminPayload(s.d.GameFintech.Floor(), &ended))
}

// fintechAdminPayload is the admin section's view of a floor. One assembler for
// both the read and the rebuild, so the page is written against one shape.
func fintechAdminPayload(floor gamefintech.Floor, ended *int) fintechAdminLayout {
	return fintechAdminLayout{
		Office: fintechAdminOffice{
			W: gamefintech.OfficeW, H: gamefintech.OfficeH,
			PlayerRadius: gamefintech.PlayerRadius,
			BossRadius:   gamefintech.BossRadius,
			MinGap:       gamefintech.MinGap,
		},
		Layout:      floor.Layout,
		Source:      floor.Source,
		InstalledAt: floor.InstalledAt,
		Occupants:   floor.Occupants,
		Kinds:       fintechAdminKinds,
		Spots:       fintechAdminSpots(),
		Ended:       ended,
	}
}

// fintechAdminSpots is every fixed catalogue point, labelled with what it is.
//
// Assembled from the game's own exported catalogue rather than from a second list:
// these are the same points ValidateLayout keeps furniture off, and a copy would
// be a plan that marks somewhere the validator does not care about.
func fintechAdminSpots() []fintechAdminSpot {
	out := []fintechAdminSpot{
		{X: gamefintech.PlayerSpawnX, Y: gamefintech.PlayerSpawnY, What: "player"},
		{X: gamefintech.BossSpawnX, Y: gamefintech.BossSpawnY, What: "boss"},
		{X: gamefintech.ChaserSpawnX, Y: gamefintech.ChaserSpawnY, What: "chaser"},
	}
	for _, n := range gamefintech.NPCCast {
		out = append(out, fintechAdminSpot{X: n.Spawn.X, Y: n.Spawn.Y, What: "npc"})
	}
	for _, at := range gamefintech.BottleSpots {
		out = append(out, fintechAdminSpot{X: at.X, Y: at.Y, What: "bottle"})
	}
	for _, at := range gamefintech.HookahSpots {
		out = append(out, fintechAdminSpot{X: at.X, Y: at.Y, What: "hookah"})
	}
	return out
}

// fintechShift is a started or resumed shift as the browser receives it.
//
// The room travels with it rather than being hardcoded in the view, so the name
// of the socket room lives in exactly one place — the game's own package.
type fintechShift struct {
	ShiftID string `json:"shift_id"`
	Room    string `json:"room"`
	// Persona is which employee this shift is — an index into the catalogue's
	// `personas`. It rides the two shift responses rather than the frame, because
	// it is constant for the life of a shift and a repeating payload is the wrong
	// place for anything that never changes. It is not `omitempty`: zero is Карен,
	// a real answer, and an absent field would be indistinguishable from him.
	Persona int `json:"persona"`
	// OfficeID is the content hash of the floor this shift is being played on.
	// The client compares it against the catalogue it cached at mount; a
	// difference means the office was rebuilt and the catalogue is stale.
	OfficeID string `json:"office_id"`
}

// handleGameFintechStart clocks in.
//
// A second start while one is already going is a 409 rather than a silent
// replacement: dropping the occupant would throw away a shift somebody is in the
// middle of on their other tab, and nothing here can tell which one they meant —
// so the client is told, and offers «УЙТИ» (the DELETE below).
//
// A full office is a 503 rather than a 409, because it is a fact about the
// process and not about the caller: nothing they did is wrong and trying again
// later is the correct response.
func (s *Server) handleGameFintechStart(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	working, err := s.d.GameFintech.StartShift(r.Context(), acc.ID)
	switch {
	case errors.Is(err, gamefintech.ErrShiftInProgress):
		writeError(w, r, http.StatusConflict, "shift_in_progress")
		return
	case errors.Is(err, gamefintech.ErrOfficeFull):
		writeError(w, r, http.StatusServiceUnavailable, "office_full")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "gamefintech: start shift", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusCreated, fintechShift{
		ShiftID: working.ShiftID, Room: gamefintech.Room,
		Persona: working.Persona, OfficeID: working.OfficeID,
	})
}

// handleGameFintechCurrent returns the shift already in progress, or 404.
//
// This is what a page reload needs: after a refresh the browser has a session, a
// socket and no idea which shift it is about to reconnect to, while the office
// is still being simulated without it. An occupant survives a reload by design —
// see AbandonGrace.
func (s *Server) handleGameFintechCurrent(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	// The start tick is deliberately dropped here: it exists so a socket can draw
	// how long the shift has lasted, and it arrives on the ready frame that the
	// socket's own hello triggers. Putting it on this response as well would be two
	// places to keep one number right.
	working, ok := s.d.GameFintech.CurrentShift(acc.ID)
	if !ok {
		writeError(w, r, http.StatusNotFound, "no_shift")
		return
	}
	writeJSON(w, http.StatusOK, fintechShift{
		ShiftID: working.ShiftID, Room: gamefintech.Room,
		Persona: working.Persona, OfficeID: working.OfficeID,
	})
}

// handleGameFintechLeave walks out, which is one of this game's two endings.
//
// Unlike «ВАНЯДУМ»'s «сдаться», this one IS a result and IS written down: the
// point of the game is the money, and money earned by standing still until you
// got bored counts exactly as much as money earned until the bald man reached
// you. A shift shorter than MinShiftSeconds is still dropped by the service —
// that is a rule about noise, not about how it ended.
func (s *Server) handleGameFintechLeave(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	err := s.d.GameFintech.LeaveShift(r.Context(), acc.ID)
	switch {
	case errors.Is(err, gamefintech.ErrNoShift):
		writeError(w, r, http.StatusNotFound, "no_shift")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "gamefintech: leave shift", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fintechShiftRow is one finished shift as the splash screen shows it.
type fintechShiftRow struct {
	Cause     string    `json:"cause"`
	Salary    float64   `json:"salary"`
	Seconds   float64   `json:"seconds"`
	CreatedAt time.Time `json:"created_at"`
}

// fintechTopRow is one line of the leaderboard: somebody's best shift.
//
// A name and three numbers, and deliberately no identifier — the board is read
// by everybody in the office, so it publishes no more about a person than the
// display name they already show on the wishlist.
type fintechTopRow struct {
	Name    string  `json:"name"`
	Cause   string  `json:"cause"`
	Salary  float64 `json:"salary"`
	Seconds float64 `json:"seconds"`
}

// fintechLimit reads the ?limit= query parameter, leaving the bound itself to the
// service.
//
// A missing or unparseable value becomes 0, which the service reads as "your
// default" — the clamp lives next to the SQL rather than here, so there is one
// place a page size is decided rather than one per caller.
func fintechLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// handleGameFintechMyShifts lists the caller's own recent shifts.
//
// It exists so that "the shift was written" is something a person can check in
// production without opening a database, which is what makes the persistence
// half of this iteration verifiable from outside the code.
func (s *Server) handleGameFintechMyShifts(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	acc, ok := accountFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	shifts, err := s.d.GameFintech.RecentShifts(r.Context(), acc.ID, fintechLimit(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "gamefintech: recent shifts", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]fintechShiftRow, 0, len(shifts))
	for _, sh := range shifts {
		out = append(out, fintechShiftRow{
			Cause:     sh.Cause,
			Salary:    sh.Salary,
			Seconds:   sh.Seconds,
			CreatedAt: sh.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"shifts": out})
}

// handleGameFintechTopShifts returns BOTH boards: the best shift per account by
// money, and the longest shift per account.
//
// Per ACCOUNT rather than per row, decided in the SQL: one very good afternoon
// would otherwise fill the whole board and the leaderboard would stop being a
// list of people.
//
// TWO BOARDS IN ONE RESPONSE rather than a `?by=` parameter fetched twice. The
// splash draws them side by side, so a client that had to issue two requests to
// render one screen would be exactly the chattiness this project's client–server
// rule exists to prevent. The keys are the metrics themselves — `salary` and
// `seconds` — so a third dimension is a third key rather than a reshaped payload.
func (s *Server) handleGameFintechTopShifts(w http.ResponseWriter, r *http.Request) {
	if !s.gameFintechAvailable(w, r) {
		return
	}
	limit := fintechLimit(r)
	bySalary, err := s.d.GameFintech.TopShifts(r.Context(), limit)
	if err != nil {
		slog.ErrorContext(r.Context(), "gamefintech: top shifts", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	bySeconds, err := s.d.GameFintech.TopShiftsBySeconds(r.Context(), limit)
	if err != nil {
		slog.ErrorContext(r.Context(), "gamefintech: longest shifts", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"salary":  s.fintechBoard(r, bySalary),
		"seconds": s.fintechBoard(r, bySeconds),
	})
}

// fintechBoard turns shifts into board rows, resolving each player's name.
//
// The name is resolved HERE rather than joined in the query because a display
// name is encrypted at rest and only the account service can read it. A lookup
// that fails leaves the row nameless rather than failing the board — a board is
// not worth a 500, and the numbers are the point.
//
// One account can appear on both boards, which costs a second lookup for it. That
// is two boards of five on a screen somebody opens between shifts, against a
// cache keyed on nothing in particular that would have to be invalidated by
// anybody changing their name.
func (s *Server) fintechBoard(r *http.Request, shifts []gamefintech.Shift) []fintechTopRow {
	out := make([]fintechTopRow, 0, len(shifts))
	for _, sh := range shifts {
		name := ""
		if s.d.Accounts != nil {
			if a, err := s.d.Accounts.GetByID(r.Context(), sh.AccountID.String()); err == nil {
				name = a.DisplayName()
			}
		}
		out = append(out, fintechTopRow{
			Name:    name,
			Cause:   sh.Cause,
			Salary:  sh.Salary,
			Seconds: sh.Seconds,
		})
	}
	return out
}

// handleGameFintechAvatar redirects to the picture to draw on a colleague.
//
// Asked for by the PSEUDONYM the snapshot already carried, which is what keeps a
// couple of hundred characters of URL off a frame that repeats ten times a second
// per viewer, and keeps a durable identifier off the wire entirely (ADR-037). One
// cached GET per face, per shift.
func (s *Server) handleGameFintechAvatar(w http.ResponseWriter, r *http.Request) {
	if s.d.GameFintech == nil {
		writeError(w, r, http.StatusServiceUnavailable, "game_unavailable")
		return
	}
	target, ok := s.d.GameFintech.AvatarFor(chi.URLParam(r, "peer"))
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
			slog.WarnContext(r.Context(), "gamefintech: refusing to redirect to a stored avatar that is not absolute https")
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
