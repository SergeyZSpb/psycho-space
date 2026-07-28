package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/logging"
	"github.com/SergeyZSpb/psycho-space/internal/session"
)

// stateCookieName is the per-provider CSRF cookie. It is per-provider on
// purpose: one shared name would mean a half-finished login in one tab silently
// destroying the state of a half-finished login in another, and the second
// person to click would get `bad_state` for no reason they could see.
func stateCookieName(provider string) string { return "psycho_oauth_state_" + provider }

// mintState issues a CSRF state value and sets it as an httpOnly cookie scoped
// to one provider. It reports false — having already answered the request —
// when the token could not be minted.
func (s *Server) mintState(w http.ResponseWriter, r *http.Request, provider string) (string, bool) {
	state, err := crypto.RandomToken(16)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return "", false
	}
	//nolint:gosec // G124: Secure is env-driven (config.CookieSecure — true in prod,
	// false only so local http://localhost works); HttpOnly and SameSite are set below.
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName(provider),
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.d.Config.CookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	return state, true
}

// oauthCallbackReq is the body the SPA posts back after the provider has handed
// it an authorization code. DeviceID is VK's alone — Yandex has no such
// concept — and is simply absent from a Yandex request.
type oauthCallbackReq struct {
	Code           string `json:"code"`
	DeviceID       string `json:"device_id"`
	State          string `json:"state"`
	CodeVerifier   string `json:"code_verifier"`
	ConsentVersion string `json:"consent_version"`
}

// handleOAuthCallback runs one provider's confidential backend code-exchange,
// upserts the account (encrypting personal data + recording consent), and either
// issues a session (approved) or reports the allowlist status (pending/blocked).
//
// Everything here except the exchange itself is shared between providers: the
// consent gate, the CSRF check, the upsert, the session and the response are
// properties of this application rather than of VK or Yandex. The provider seam
// is deliberately narrow — see oauthProvider.
func (s *Server) handleOAuthCallback(p oauthProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !p.Configured() {
			writeError(w, r, http.StatusServiceUnavailable, "oauth_not_configured")
			return
		}
		ctx := r.Context()

		var req oauthCallbackReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "bad_request")
			return
		}
		// Consent must precede any personal-data processing.
		if req.ConsentVersion == "" {
			writeError(w, r, http.StatusBadRequest, "consent_required")
			return
		}
		if req.Code == "" || req.CodeVerifier == "" {
			writeError(w, r, http.StatusBadRequest, "bad_request")
			return
		}
		// CSRF: the returned state must match the cookie we set in /<provider>/state.
		c, err := r.Cookie(stateCookieName(p.Name()))
		if err != nil || c.Value == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(req.State)) != 1 {
			writeError(w, r, http.StatusBadRequest, "bad_state")
			return
		}

		tok, err := p.Exchange(ctx, req)
		if err != nil {
			if errors.Is(err, errIncompleteRequest) {
				writeError(w, r, http.StatusBadRequest, "bad_request")
				return
			}
			slog.ErrorContext(ctx, "oauth code exchange failed", "provider", p.Name(), "err", err)
			writeError(w, r, http.StatusBadGateway, "oauth_exchange_failed")
			return
		}
		info, err := p.Profile(ctx, tok)
		if err != nil {
			slog.ErrorContext(ctx, "oauth profile fetch failed", "provider", p.Name(), "err", err)
			switch {
			case errors.Is(err, errNoUserID):
				writeError(w, r, http.StatusBadGateway, "oauth_no_user_id")
			case errors.Is(err, errIdentityMismatch):
				writeError(w, r, http.StatusBadGateway, "oauth_identity_mismatch")
			case errors.Is(err, errIDTokenInvalid):
				writeError(w, r, http.StatusBadGateway, "oauth_idtoken_invalid")
			default:
				writeError(w, r, http.StatusBadGateway, "oauth_userinfo_failed")
			}
			return
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
			Provider:       p.Name(),
			ProviderUserID: info.UserID,
			FirstName:      info.FirstName,
			LastName:       info.LastName,
			Avatar:         info.Avatar,
			Sex:            info.Sex,
			Birthday:       info.Birthday,
			ConsentVersion: req.ConsentVersion,
			AutoApprove:    autoApprove,
		})
		if err != nil {
			slog.ErrorContext(ctx, "account upsert failed", "err", err)
			writeError(w, r, http.StatusInternalServerError, "internal")
			return
		}
		logging.SetAccountID(ctx, acc.ID) // correlate the rest of this login's logs
		clearCookie(w, stateCookieName(p.Name()), s.d.Config.CookieSecure())

		// Always issue a session — even for pending/blocked — so the client can poll
		// /api/auth/me and proceed the moment an admin approves, without re-running
		// the login flow. requireAuth still gates resource access on approval.
		raw, err := s.d.Sessions.Create(ctx, acc.ID)
		if err != nil {
			slog.ErrorContext(ctx, "session create failed", "err", err)
			writeError(w, r, http.StatusInternalServerError, "internal")
			return
		}
		s.d.Sessions.SetCookie(w, raw)
		writeJSON(w, http.StatusOK, map[string]any{"status": acc.Status, "account": publicAccount(acc)})
	}
}

// handleMe returns the current account, or 401.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acc, _, ok := s.currentAccount(r)
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

// currentAccount resolves the session cookie to an account, and to the id of the
// session that carried it.
//
// The session id is returned alongside because a WebSocket needs it: the socket
// outlives this request and has to be re-judged against *this* session later, so
// the one place that already resolves the cookie is the place to hand it on.
// Callers with no such need discard it.
func (s *Server) currentAccount(r *http.Request) (*account.Account, string, bool) {
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return nil, "", false
	}
	sess, err := s.d.Sessions.Resolve(r.Context(), c.Value)
	if err != nil {
		return nil, "", false
	}
	acc, err := s.d.Accounts.GetByID(r.Context(), sess.AccountID)
	if err != nil {
		return nil, "", false
	}
	logging.SetAccountID(r.Context(), acc.ID) // stamp all subsequent logs for this request
	return acc, sess.ID, true
}

func publicAccount(a *account.Account) map[string]any {
	return map[string]any{
		"id":           a.ID,
		"display_name": a.DisplayName(),
		"avatar_url":   a.AvatarURL,
		"provider":     a.Provider,
		"profile_url":  a.ProfileURL(), // "" for Yandex and for a forgotten account
		"role":         a.Role,
		"status":       a.Status,
		"handle":       a.Handle, // shown on the pending screen; harmless elsewhere
	}
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	//nolint:gosec // G124: Secure is env-driven (config.CookieSecure — true in prod,
	// false only so local http://localhost works); HttpOnly and SameSite are set below.
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

const (
	accountCtxKey ctxKey = iota
	sessionIDCtxKey
)

// requireAuth ensures an approved account; it stores it, and the id of the
// session that authenticated it, in the request context.
//
// This pair — a live session AND status=approved — is the definition of
// "authorized" for the whole application. A WebSocket is admitted by it once and
// then runs for hours without another request, so the same rule is re-applied to
// live sockets in batch by session.Manager.RevokedSessions; change one and the
// other has to change with it.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, sessionID, ok := s.currentAccount(r)
		if !ok {
			writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !acc.IsApproved() {
			writeError(w, r, http.StatusForbidden, "not_approved")
			return
		}
		ctx := context.WithValue(r.Context(), accountCtxKey, acc)
		ctx = context.WithValue(ctx, sessionIDCtxKey, sessionID)
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

// sessionIDFromContext returns the id of the session requireAuth authenticated
// this request with. Only the WebSocket upgrade needs it — see handleRealtime.
func sessionIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionIDCtxKey).(string)
	return id, ok
}
