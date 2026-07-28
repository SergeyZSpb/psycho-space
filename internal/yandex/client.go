// Package yandex is a minimal client for the Yandex ID (oauth.yandex.ru)
// authorization-code flow: send the user to Yandex, swap the returned
// authorization code for an access token on the backend, then read the
// profile once.
//
// Yandex ID is plain OAuth 2.0 — there is no OIDC layer, so no id_token, no
// JWKS and nothing to verify cryptographically; the code exchange happens
// server-side over TLS with the client secret, and that is what makes the
// identity trustworthy. The access token is used exactly once, to read the
// profile, and is then discarded: no refresh token is requested, stored or
// used, because the session this login produces is our own (ADR-006) and we
// never call Yandex again on the user's behalf.
package yandex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to the Yandex ID OAuth endpoints.
type Client struct {
	http         *http.Client
	oauthURL     string
	infoURL      string
	clientID     string
	clientSecret string
	redirectURI  string
}

// New builds a Yandex ID client. oauthBaseURL defaults to
// https://oauth.yandex.ru (the authorize + token host) and infoURL defaults to
// https://login.yandex.ru/info (the profile endpoint, which lives on a
// different host — hence two settings rather than one base). Both are trimmed
// of a trailing slash so a slash left in configuration cannot produce a
// double-slash path such as "//authorize".
func New(oauthBaseURL, infoURL, clientID, clientSecret, redirectURI string) *Client {
	if oauthBaseURL == "" {
		oauthBaseURL = "https://oauth.yandex.ru"
	}
	if infoURL == "" {
		infoURL = "https://login.yandex.ru/info"
	}
	return &Client{
		http:         &http.Client{Timeout: 10 * time.Second},
		oauthURL:     strings.TrimRight(oauthBaseURL, "/"),
		infoURL:      strings.TrimRight(infoURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
	}
}

// Tokens is the result of a successful code exchange. Yandex also returns a
// token_type ("bearer") and, for some app configurations, a refresh token; we
// decode neither. The profile endpoint needs the "OAuth" authorization scheme
// rather than whatever token_type says, and a refresh token we would have to
// store is personal-data-adjacent state we have no use for.
type Tokens struct {
	AccessToken string
	ExpiresIn   int
}

// UserInfo is the subset of the Yandex profile we use. The field set and the
// meaning of each field deliberately match vk.UserInfo, so a caller can treat
// either provider's answer alike. Sex is "male", "female" or "" (unspecified);
// Birthday is ISO "YYYY-MM-DD" or "". Avatar is a ready-to-render URL, or ""
// when the account has no picture of its own.
type UserInfo struct {
	UserID    string
	FirstName string
	LastName  string
	Avatar    string
	Sex       string
	Birthday  string
}

// nullString unmarshals a JSON string that Yandex may send as null (sex and
// birthday routinely are, and the name fields can be), mapping null to the
// empty string so callers never handle a nil pointer.
type nullString string

func (n *nullString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*n = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*n = nullString(s)
	return nil
}

// AuthorizeURL builds the URL the browser is sent to in order to start the
// flow. state is the CSRF/replay guard we hand back to ourselves on the
// callback, and codeChallenge is the S256 PKCE challenge for the verifier the
// code exchange will present.
//
// No scope parameter is sent, and that is deliberate: Yandex grants exactly
// the rights registered on the application in the developer console, and a
// scope parameter is the wrong model for this provider — asking for anything
// there can only produce a rejection, never a wider grant.
func (c *Client) AuthorizeURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return c.oauthURL + "/authorize?" + q.Encode()
}

// ExchangeCode swaps the authorization code for an access token
// (grant_type=authorization_code), authenticating with the client secret.
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("redirect_uri", c.redirectURI)
	form.Set("code_verifier", codeVerifier)

	body, status, err := c.postForm(ctx, "/token", form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("yandex: code exchange failed: %s", yandexError(body, status))
	}

	var r struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("yandex: decode token response: %w", err)
	}
	if r.AccessToken == "" {
		return nil, fmt.Errorf("yandex: token response missing access_token")
	}
	return &Tokens{AccessToken: r.AccessToken, ExpiresIn: r.ExpiresIn}, nil
}

// UserInfo fetches the profile for the given access token.
func (c *Client) UserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	body, status, err := c.getInfo(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("yandex: info failed: %s", yandexError(body, status))
	}

	// Only these fields are decoded. Yandex also returns default_email, emails,
	// psuid, default_phone, real_name and client_id; we deliberately do not
	// decode any of them. Personal-data minimisation (152-ФЗ) — this project
	// has no use for an email address or a phone number, so it must not read
	// one, let alone store one.
	//
	// `display_name` is also not decoded, and that is a decision rather than an
	// omission: for most Yandex accounts it is the login, which is usually the
	// local part of an email address. A nameless account already renders as
	// `psycho-<handle>` — the same fallback a nameless VK account gets — and
	// publishing somebody's login to every other player instead would be both a
	// surprise and more personal data on the wire than was asked for.
	var r struct {
		ID              string     `json:"id"`
		FirstName       nullString `json:"first_name"`
		LastName        nullString `json:"last_name"`
		Sex             nullString `json:"sex"`      // "male", "female" or null
		Birthday        nullString `json:"birthday"` // "YYYY-MM-DD" or null
		DefaultAvatarID string     `json:"default_avatar_id"`
		IsAvatarEmpty   bool       `json:"is_avatar_empty"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("yandex: decode info: %w", err)
	}

	return &UserInfo{
		UserID:    r.ID,
		FirstName: string(r.FirstName),
		LastName:  string(r.LastName),
		Avatar:    avatarURL(r.DefaultAvatarID, r.IsAvatarEmpty),
		// Yandex already speaks "male"/"female", and already formats birthdays
		// as ISO "YYYY-MM-DD"; null became "" above. A partially zeroed value
		// such as "0000-12-23" (a day and month, no year) is passed through
		// unchanged — it is a real answer, and nothing downstream parses it.
		Sex:      string(r.Sex),
		Birthday: string(r.Birthday),
	}, nil
}

// avatarURL renders the profile picture URL Yandex documents for an avatar id.
// It returns "" when is_avatar_empty is set — that flag marks the placeholder
// Yandex assigns to an account with no picture of its own, and rendering it
// would show every such user the same stock face — and likewise when no avatar
// id was returned at all.
func avatarURL(defaultAvatarID string, isAvatarEmpty bool) string {
	if isAvatarEmpty || defaultAvatarID == "" {
		return ""
	}
	return "https://avatars.yandex.net/get-yapic/" + defaultAvatarID + "/islands-200"
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return c.do(req, path)
}

func (c *Client) getInfo(ctx context.Context, accessToken string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.infoURL+"?format=json", nil)
	if err != nil {
		return nil, 0, err
	}
	// The scheme is "OAuth", not "Bearer". Yandex's passport API rejects a
	// Bearer-prefixed token with 401, and that mistake is the single most
	// common cause of an unexplained 401 here — do not "fix" this to Bearer.
	req.Header.Set("Authorization", "OAuth "+accessToken)
	req.Header.Set("Accept", "application/json")
	return c.do(req, "/info")
}

func (c *Client) do(req *http.Request, path string) ([]byte, int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("yandex: request %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("yandex: read %s: %w", path, err)
	}
	return body, resp.StatusCode, nil
}

// yandexError renders a compact, non-secret description of a Yandex error
// body. Only the documented error fields are surfaced — never the raw body,
// which is echoed back into an error string and must not be able to carry a
// token or the client secret out with it.
func yandexError(body []byte, status int) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Sprintf("http %d: %s: %s", status, e.Error, e.Description)
	}
	return fmt.Sprintf("http %d", status)
}
