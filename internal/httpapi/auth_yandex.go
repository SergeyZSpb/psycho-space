package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/yandex"
)

// yandexProvider adapts the Yandex ID client to oauthProvider.
//
// It is much smaller than its VK counterpart because Yandex is plain OAuth 2.0:
// no SDK, no widget, no device id, no id_token and so nothing to verify or
// cross-check. The provider hands back one id and that is the identity.
type yandexProvider struct {
	client *yandex.Client
	cfg    interface{ Configured() bool }
}

func (yandexProvider) Name() string { return account.ProviderYandex }

func (p yandexProvider) Configured() bool { return p.cfg.Configured() }

func (p yandexProvider) Exchange(ctx context.Context, req oauthCallbackReq) (*oauthTokens, error) {
	tok, err := p.client.ExchangeCode(ctx, req.Code, req.CodeVerifier)
	if err != nil {
		return nil, err
	}
	return &oauthTokens{AccessToken: tok.AccessToken}, nil
}

func (p yandexProvider) Profile(ctx context.Context, tok *oauthTokens) (*oauthProfile, error) {
	info, err := p.client.UserInfo(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	if info.UserID == "" {
		return nil, errNoUserID
	}
	return &oauthProfile{
		UserID:    info.UserID,
		FirstName: info.FirstName,
		LastName:  info.LastName,
		Avatar:    info.Avatar,
		Sex:       info.Sex,
		Birthday:  info.Birthday,
	}, nil
}

// yandex returns the Yandex provider bound to this server's dependencies.
func (s *Server) yandex() yandexProvider {
	return yandexProvider{client: s.d.Yandex, cfg: s.d.Config.Yandex}
}

// handleYandexState mints a CSRF state value AND builds the authorize URL the
// browser should be sent to.
//
// The URL is built here rather than in the SPA because it is the one way to
// keep the client id and the redirect URI in a single place. The VK flow cannot
// do this — its SDK builds the URL in the browser — and the cost of that is a
// three-way agreement between the SPA's constants, the backend's environment
// and the provider's dashboard, which has broken production once already
// (RUNBOOK, "the redirect URL, and what a 405 means"). Yandex needs no SDK, so
// there are two copies instead of three, and the SPA can never be the stale one.
//
// The PKCE challenge is generated in the browser and arrives as a query
// parameter. That is fine — a challenge is public by design, it is the verifier
// that is secret — but it is still validated before being interpolated into a
// URL we hand to a browser.
func (s *Server) handleYandexState(w http.ResponseWriter, r *http.Request) {
	if !s.d.Config.Yandex.Configured() {
		writeError(w, r, http.StatusServiceUnavailable, "oauth_not_configured")
		return
	}
	challenge := r.URL.Query().Get("code_challenge")
	if !validCodeChallenge(challenge) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	state, ok := s.mintState(w, r, account.ProviderYandex)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"state":         state,
		"authorize_url": s.d.Yandex.AuthorizeURL(state, challenge),
	})
}

// validCodeChallenge accepts an RFC 7636 code challenge: 43–128 characters from
// the unreserved set. An S256 challenge is always exactly 43, but the range is
// the one the RFC specifies and there is no reason to be stricter than it.
func validCodeChallenge(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	return strings.IndexFunc(v, func(rn rune) bool {
		return !strings.ContainsRune(unreserved, rn)
	}) == -1
}
