package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/go-chi/chi/v5"
)

// handleSettingsGet returns global settings (admins can read).
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	open, err := s.d.Settings.OpenRegistration(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"open_registration": open})
}

// handleSetOpenRegistration toggles open registration (superadmin only). When
// on, new users are auto-approved (standard user role) on first login.
func (s *Server) handleSetOpenRegistration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if err := s.d.Settings.SetOpenRegistration(r.Context(), req.Enabled); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"open_registration": req.Enabled})
}

func (s *Server) handleAdminList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = account.StatusPending
	}
	switch status {
	case account.StatusPending, account.StatusApproved, account.StatusBlocked:
	default:
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	accs, err := s.d.Accounts.ListByStatus(r.Context(), status)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]map[string]any, 0, len(accs))
	for _, a := range accs {
		out = append(out, adminAccountResponse(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// handleAdminApprove accepts a user (admin or superadmin).
func (s *Server) handleAdminApprove(w http.ResponseWriter, r *http.Request) {
	s.adminSetStatus(w, r, account.StatusApproved)
}

// handleAdminBlock revokes access (admin or superadmin). The user then sees the
// "contact admins for access" screen.
func (s *Server) handleAdminBlock(w http.ResponseWriter, r *http.Request) {
	s.adminSetStatus(w, r, account.StatusBlocked)
}

func (s *Server) adminSetStatus(w http.ResponseWriter, r *http.Request, status string) {
	actor, _ := accountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	// You cannot block yourself (avoids self-lockout, incl. the superadmin).
	if status == account.StatusBlocked && id == actor.ID {
		writeError(w, r, http.StatusForbidden, "cannot_modify_self")
		return
	}
	target, err := s.d.Accounts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	// The superadmin is unrevokable: nobody can block them.
	if status == account.StatusBlocked && target.IsSuperadmin() {
		writeError(w, r, http.StatusForbidden, "cannot_block_superadmin")
		return
	}
	// Guard against privilege issues: only a superadmin may modify admins/superadmins.
	if target.IsAdmin() && !actor.IsSuperadmin() {
		writeError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if status == account.StatusApproved {
		err = s.d.Accounts.Approve(r.Context(), id)
	} else {
		err = s.d.Accounts.Block(r.Context(), id)
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	// Revoke all sessions so a blocked user is cut off immediately everywhere.
	if status == account.StatusBlocked {
		if err := s.d.Sessions.RevokeAllForAccount(r.Context(), id); err != nil {
			slog.ErrorContext(r.Context(), "revoke sessions on block failed", "err", err)
		}
		// A live WebSocket has no request left to re-check the session on, so
		// revoking in the database is not enough — close the sockets too.
		//
		// This in-process kick is not the only thing that cuts a live socket, but
		// it is the only one that does so instantly: realtime.Revalidator sweeps
		// every RevalidateInterval and would close these connections anyway, along
		// with the two cases this path cannot see (a session expiring on its own,
		// and a block applied straight in the database). Keeping the kick means an
		// admin's action takes effect at once rather than up to a sweep later.
		// See realtime.Hub.KickAccount.
		if s.d.Realtime != nil {
			s.d.Realtime.KickAccount(id)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminForget anonymises an account: the person goes, what they wrote
// stays. Route is gated to superadmin only.
//
// After it the same VK account logging in again is a genuinely NEW account —
// new id, `pending`, the whole first-login flow — because the blind index that
// used to match has been overwritten. Their wishlist ideas, the comments other
// people replied to, the votes and the leaderboard times all remain, now
// authored by an anonymous `psycho-…` that links nowhere. See
// migrations/011_account_forgotten.sql for why this is neither a hard delete
// nor a plain soft one.
//
// THE ORDER BELOW IS LOAD-BEARING and is stricter than the block path's, because
// this one races with writes that would recreate what it is removing:
//
//  1. Kick the sockets FIRST. While one is open, a «Ванягоччи» frame can reach
//     `EnsurePet` and insert a row, and a «ВАНЯДУМ» hello can put them back in
//     the заброшка. The kick is asynchronous, so it narrows the window rather than
//     closing it — which is survivable here only because this operation does
//     not delete rows, so the worst case is a stray row belonging to an
//     anonymous account.
//  2. Drop the in-memory state, or the yard goes on drawing them by name.
//  3. Then anonymise, which is the one statement that matters.
//  4. Then purge again, because a tick between 2 and 3 can re-create a
//     provisional placement from a connection still closing.
func (s *Server) handleAdminForget(w http.ResponseWriter, r *http.Request) {
	actor, _ := accountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	// Nobody erases themselves. It is irreversible and it would take the
	// superadmin — the one unrevokable account — with it.
	if id == actor.ID {
		writeError(w, r, http.StatusForbidden, "cannot_modify_self")
		return
	}
	target, err := s.d.Accounts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	if target.IsSuperadmin() {
		writeError(w, r, http.StatusForbidden, "cannot_modify_superadmin")
		return
	}

	// 1 — cut every live connection before anything else.
	if s.d.Realtime != nil {
		s.d.Realtime.KickAccount(id)
	}
	if err := s.d.Sessions.RevokeAllForAccount(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), "revoke sessions on forget failed", "err", err)
	}

	// 2 — drop what the games remember about them.
	s.forgetInGames(id)

	// 3 — the statement this whole handler exists for.
	if err := s.d.Accounts.Forget(r.Context(), id); err != nil {
		if errors.Is(err, account.ErrAlreadyForgotten) {
			writeError(w, r, http.StatusConflict, "already_forgotten")
			return
		}
		slog.ErrorContext(r.Context(), "forget account failed", "err", err, "account_id", id)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}

	// 4 — and again, in case a tick re-created a placement in between.
	s.forgetInGames(id)

	slog.InfoContext(r.Context(), "account forgotten", "account_id", id, "actor_id", actor.ID)
	w.WriteHeader(http.StatusNoContent)
}

// forgetInGames drops an account from every game's in-memory state.
//
// Each game is asked separately and neither knows about the other — this
// function is the whole of what "an account went away" means to them, and a
// fourth game adds a line here and nothing else.
func (s *Server) forgetInGames(id string) {
	if s.d.GameVanyagotchi != nil {
		s.d.GameVanyagotchi.Forget(id)
	}
	if s.d.GameVanyadum != nil {
		// Takes them out of the заброшка without recording the visit — a visit
		// belonging to somebody who is being erased is not a result. Safe to call
		// twice and safe for an account that has never played, because this
		// function IS called twice: once before the anonymising statement and
		// once after it.
		s.d.GameVanyadum.PurgeAccount(id)
	}
	if s.d.GameFintech != nil {
		// Drops the occupant out of the office without writing the shift, for
		// the same reason. It is deliberately safe to call twice and safe for an
		// account the office has never seen, because this function IS called
		// twice — once before the anonymising statement and once after it.
		s.d.GameFintech.PurgeAccount(id)
	}
}

// handleAdminPromote makes a user an admin. Route is gated to superadmin only.
func (s *Server) handleAdminPromote(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if _, err := s.d.Accounts.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, account.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	if err := s.d.Accounts.Promote(r.Context(), id); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminDemote returns an admin to the standard user role. Route is gated to
// superadmin only; the superadmin themselves cannot be demoted (unrevokable).
func (s *Server) handleAdminDemote(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	target, err := s.d.Accounts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	if target.IsSuperadmin() {
		writeError(w, r, http.StatusForbidden, "cannot_modify_superadmin")
		return
	}
	if err := s.d.Accounts.Demote(r.Context(), id); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func adminAccountResponse(a *account.Account) map[string]any {
	return map[string]any{
		"id":           a.ID,
		"handle":       a.Handle,
		"display_name": a.DisplayName(),
		"avatar_url":   a.AvatarURL,
		"provider":     a.Provider,
		"profile_url":  a.ProfileURL(),
		"role":         a.Role,
		"status":       a.Status,
		"created_at":   a.CreatedAt,
	}
}
