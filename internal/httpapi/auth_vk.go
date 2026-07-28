package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
)

// vkProvider adapts the VK ID client to oauthProvider. Everything VK-specific
// that the shared handler must not know about lives here: the device id, the
// service-token exchange, the cross-check between the two answers VK gives, and
// the optional id_token verification.
type vkProvider struct {
	client   *vk.Client
	verifier *vk.IDTokenVerifier // nil = id_token verification disabled
	cfg      interface{ Configured() bool }
}

func (vkProvider) Name() string { return account.ProviderVK }

func (p vkProvider) Configured() bool { return p.cfg.Configured() }

// Exchange runs the confidential backend code-exchange. VK additionally needs
// the device id its widget hands the browser, so a request without one cannot
// be completed and is rejected as incomplete rather than attempted.
func (p vkProvider) Exchange(ctx context.Context, req oauthCallbackReq) (*oauthTokens, error) {
	if req.DeviceID == "" {
		return nil, fmt.Errorf("%w: device_id", errIncompleteRequest)
	}
	tok, err := p.client.ExchangeCode(ctx, req.Code, req.CodeVerifier, req.DeviceID)
	if err != nil {
		return nil, err
	}
	return &oauthTokens{AccessToken: tok.AccessToken, UserID: tok.UserID, IDToken: tok.IDToken}, nil
}

// Profile reads the VK profile and then asks whether VK agrees with itself.
//
// VK identifies the user twice — once in the token response and once in
// user_info — so the two are compared. They should never differ; if they do,
// something has gone wrong upstream and the safe answer is to refuse rather
// than to pick one.
func (p vkProvider) Profile(ctx context.Context, tok *oauthTokens) (*oauthProfile, error) {
	info, err := p.client.UserInfo(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}

	uid := info.UserID
	if uid == "" {
		uid = tok.UserID
	}
	if uid == "" {
		return nil, errNoUserID
	}
	if tok.UserID != "" && info.UserID != "" && tok.UserID != info.UserID {
		return nil, errIdentityMismatch
	}
	// Defense-in-depth: verify the id_token signature (JWKS) when enabled.
	if p.verifier != nil {
		if err := p.verifier.Verify(tok.IDToken, uid); err != nil {
			return nil, fmt.Errorf("%w: %w", errIDTokenInvalid, err)
		}
	}

	return &oauthProfile{
		UserID:    uid,
		FirstName: info.FirstName,
		LastName:  info.LastName,
		Avatar:    info.Avatar,
		Sex:       info.Sex,
		Birthday:  info.Birthday,
	}, nil
}

// vk returns the VK provider bound to this server's dependencies.
func (s *Server) vk() vkProvider {
	return vkProvider{client: s.d.VK, verifier: s.d.VKVerifier, cfg: s.d.Config.VK}
}

// handleVKState mints a CSRF state value and returns it so the SPA can pass it
// to the VK widget.
//
// Unlike Yandex, VK does not get its authorize URL from here: the VK ID SDK
// builds it in the browser, which is why the app id and redirect path also have
// to exist in the SPA. See handleYandexState for the shape that avoids that.
func (s *Server) handleVKState(w http.ResponseWriter, r *http.Request) {
	state, ok := s.mintState(w, r, account.ProviderVK)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": state})
}
