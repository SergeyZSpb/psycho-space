package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/session"
)

const vkStateCookie = "psycho_vk_state"

// handleVKState mints a CSRF state value, sets it as an httpOnly cookie, and
// returns it so the SPA can pass it to the VK widget.
func (s *Server) handleVKState(w http.ResponseWriter, r *http.Request) {
	state, err := crypto.RandomToken(16)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     vkStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.d.Config.CookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	writeJSON(w, http.StatusOK, map[string]string{"state": state})
}

type vkCallbackReq struct {
	Code           string `json:"code"`
	DeviceID       string `json:"device_id"`
	State          string `json:"state"`
	CodeVerifier   string `json:"code_verifier"`
	ConsentVersion string `json:"consent_version"`
}

// handleVKCallback runs the confidential backend code-exchange, upserts the
// account (encrypting personal data + recording consent), and either issues a
// session (approved) or reports the allowlist status (pending/blocked).
func (s *Server) handleVKCallback(w http.ResponseWriter, r *http.Request) {
	if !s.d.Config.VK.Configured() {
		writeError(w, r, http.StatusServiceUnavailable, "vk_not_configured")
		return
	}
	ctx := r.Context()

	var req vkCallbackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	// Consent must precede any personal-data processing.
	if req.ConsentVersion == "" {
		writeError(w, r, http.StatusBadRequest, "consent_required")
		return
	}
	if req.Code == "" || req.CodeVerifier == "" || req.DeviceID == "" {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	// CSRF: the returned state must match the cookie we set in /vk/state.
	c, err := r.Cookie(vkStateCookie)
	if err != nil || c.Value == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(req.State)) != 1 {
		writeError(w, r, http.StatusBadRequest, "bad_state")
		return
	}

	tok, err := s.d.VK.ExchangeCode(ctx, req.Code, req.CodeVerifier, req.DeviceID)
	if err != nil {
		slog.ErrorContext(ctx, "vk code exchange failed", "err", err)
		writeError(w, r, http.StatusBadGateway, "vk_exchange_failed")
		return
	}
	info, err := s.d.VK.UserInfo(ctx, tok.AccessToken)
	if err != nil {
		slog.ErrorContext(ctx, "vk user_info failed", "err", err)
		writeError(w, r, http.StatusBadGateway, "vk_userinfo_failed")
		return
	}

	uid := info.UserID
	if uid == "" {
		uid = tok.UserID
	}
	if uid == "" {
		slog.ErrorContext(ctx, "vk returned no user id")
		writeError(w, r, http.StatusBadGateway, "vk_no_user_id")
		return
	}
	if tok.UserID != "" && info.UserID != "" && tok.UserID != info.UserID {
		slog.ErrorContext(ctx, "vk user_id mismatch between token and user_info")
		writeError(w, r, http.StatusBadGateway, "vk_user_mismatch")
		return
	}
	// Defense-in-depth: verify the id_token signature (JWKS) when enabled.
	if s.d.VKVerifier != nil {
		if err := s.d.VKVerifier.Verify(tok.IDToken, uid); err != nil {
			slog.ErrorContext(ctx, "vk id_token verification failed", "err", err)
			writeError(w, r, http.StatusBadGateway, "vk_idtoken_invalid")
			return
		}
	}

	// Open-registration mode auto-approves brand-new accounts (standard role).
	autoApprove := false
	if s.d.Settings != nil {
		if open, err := s.d.Settings.OpenRegistration(ctx); err != nil {
			slog.ErrorContext(ctx, "read open_registration failed", "err", err)
		} else {
			autoApprove = open
		}
	}
	acc, err := s.d.Accounts.UpsertOnLogin(ctx, account.LoginInput{
		VKUserID:       uid,
		FirstName:      info.FirstName,
		LastName:       info.LastName,
		Avatar:         info.Avatar,
		ConsentVersion: req.ConsentVersion,
		AutoApprove:    autoApprove,
	})
	if err != nil {
		slog.ErrorContext(ctx, "account upsert failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	clearCookie(w, vkStateCookie, s.d.Config.CookieSecure())

	// Always issue a session — even for pending/blocked — so the client can poll
	// /api/auth/me and proceed the moment an admin approves, without re-running
	// the VK flow. requireAuth still gates resource access on approval.
	raw, err := s.d.Sessions.Create(ctx, acc.ID)
	if err != nil {
		slog.ErrorContext(ctx, "session create failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	s.d.Sessions.SetCookie(w, raw)
	writeJSON(w, http.StatusOK, map[string]any{"status": acc.Status, "account": publicAccount(acc)})
}

// handleMe returns the current account, or 401.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.currentAccount(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": publicAccount(acc)})
}

// handleLogout revokes the session and clears the cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(session.CookieName); err == nil {
		_ = s.d.Sessions.Revoke(r.Context(), c.Value)
	}
	s.d.Sessions.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// currentAccount resolves the session cookie to an account.
func (s *Server) currentAccount(r *http.Request) (*account.Account, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	id, err := s.d.Sessions.Resolve(r.Context(), c.Value)
	if err != nil {
		return nil, false
	}
	acc, err := s.d.Accounts.GetByID(r.Context(), id)
	if err != nil {
		return nil, false
	}
	return acc, true
}

func publicAccount(a *account.Account) map[string]any {
	return map[string]any{
		"id":           a.ID,
		"display_name": a.DisplayName(),
		"avatar_url":   a.AvatarURL,
		"vk_url":       a.VKURL(),
		"role":         a.Role,
		"status":       a.Status,
		"handle":       a.Handle, // shown on the pending screen; harmless elsewhere
	}
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// --- auth middleware (used by protected routes) -----------------------------

type ctxKey int

const accountCtxKey ctxKey = iota

// requireAuth ensures an approved account; it stores it in the request context.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, ok := s.currentAccount(r)
		if !ok {
			writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !acc.IsApproved() {
			writeError(w, r, http.StatusForbidden, "not_approved")
			return
		}
		ctx := context.WithValue(r.Context(), accountCtxKey, acc)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin must be chained after requireAuth.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, ok := accountFromContext(r.Context())
		if !ok || !acc.IsAdmin() {
			writeError(w, r, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSuperadmin must be chained after requireAuth.
func (s *Server) requireSuperadmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, ok := accountFromContext(r.Context())
		if !ok || !acc.IsSuperadmin() {
			writeError(w, r, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func accountFromContext(ctx context.Context) (*account.Account, bool) {
	acc, ok := ctx.Value(accountCtxKey).(*account.Account)
	return acc, ok
}
