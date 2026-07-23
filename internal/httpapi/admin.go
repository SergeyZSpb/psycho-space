package httpapi

import (
	"encoding/json"
	"errors"
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
	target, err := s.d.Accounts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
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
	w.WriteHeader(http.StatusNoContent)
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

func adminAccountResponse(a *account.Account) map[string]any {
	return map[string]any{
		"id":           a.ID,
		"handle":       a.Handle,
		"display_name": a.DisplayName(),
		"avatar_url":   a.AvatarURL,
		"vk_url":       a.VKURL(),
		"role":         a.Role,
		"status":       a.Status,
		"created_at":   a.CreatedAt,
	}
}
