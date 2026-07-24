// Package vk is a minimal client for the VK ID (id.vk.ru) confidential
// backend code-exchange flow: swap an authorization code (produced by the
// frontend OneTap widget + PKCE) for tokens, then fetch the user's profile.
//
// Tokens are used once (to read the profile) and never persisted.
package vk

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

// Client talks to the VK ID OAuth endpoints.
type Client struct {
	http         *http.Client
	baseURL      string
	appID        string
	serviceToken string
	redirectURI  string
}

// New builds a VK ID client. baseURL defaults to https://id.vk.ru.
func New(baseURL, appID, serviceToken, redirectURI string) *Client {
	if baseURL == "" {
		baseURL = "https://id.vk.ru"
	}
	return &Client{
		http:         &http.Client{Timeout: 10 * time.Second},
		baseURL:      strings.TrimRight(baseURL, "/"),
		appID:        appID,
		serviceToken: serviceToken,
		redirectURI:  redirectURI,
	}
}

// Tokens is the result of a successful code exchange.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    int
	UserID       string // VK numeric user id (as string)
}

// UserInfo is the subset of the VK profile we use. Sex and Birthday are part of
// VK's base right (vkid.personal_info) and arrive on every login; we store them
// encrypted alongside the rest. Sex is VK's raw code ("1" female, "2" male,
// "" unspecified); Birthday is VK's "DD.MM.YYYY" string. Either may be empty.
type UserInfo struct {
	UserID    string
	FirstName string
	LastName  string
	Avatar    string
	Sex       string
	Birthday  string
}

// flexID unmarshals a JSON value that may be either a number or a string, and
// maps JSON null to the empty string.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" {
		s = ""
	}
	*f = flexID(s)
	return nil
}

// ExchangeCode swaps the authorization code for tokens (grant_type=authorization_code).
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier, deviceID string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	form.Set("device_id", deviceID)
	form.Set("client_id", c.appID)
	form.Set("redirect_uri", c.redirectURI)
	if c.serviceToken != "" {
		form.Set("service_token", c.serviceToken)
	}

	body, status, err := c.postForm(ctx, "/oauth2/auth", form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("vk: code exchange failed: %s", vkError(body, status))
	}

	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
		UserID       flexID `json:"user_id"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("vk: decode token response: %w", err)
	}
	if r.AccessToken == "" {
		return nil, fmt.Errorf("vk: token response missing access_token")
	}
	return &Tokens{
		AccessToken:  r.AccessToken,
		RefreshToken: r.RefreshToken,
		IDToken:      r.IDToken,
		ExpiresIn:    r.ExpiresIn,
		UserID:       string(r.UserID),
	}, nil
}

// UserInfo fetches the profile for the given access token.
func (c *Client) UserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	form := url.Values{}
	form.Set("client_id", c.appID)
	form.Set("access_token", accessToken)

	body, status, err := c.postForm(ctx, "/oauth2/user_info", form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("vk: user_info failed: %s", vkError(body, status))
	}

	// VK returns the profile under a "user" object; fall back to a flat shape.
	type fields struct {
		UserID    flexID `json:"user_id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Avatar    string `json:"avatar"`
		Sex       flexID `json:"sex"` // VK sends a number; flexID normalizes to a string
		Birthday  string `json:"birthday"`
	}
	var r struct {
		User *fields `json:"user"`
		fields
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("vk: decode user_info: %w", err)
	}
	f := r.fields
	if r.User != nil && r.User.UserID != "" {
		f = *r.User
	}
	return &UserInfo{
		UserID:    string(f.UserID),
		FirstName: f.FirstName,
		LastName:  f.LastName,
		Avatar:    f.Avatar,
		Sex:       string(f.Sex),
		Birthday:  f.Birthday,
	}, nil
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("vk: request %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("vk: read %s: %w", path, err)
	}
	return body, resp.StatusCode, nil
}

// vkError renders a compact, non-secret description of a VK error body.
func vkError(body []byte, status int) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Sprintf("http %d: %s: %s", status, e.Error, e.Description)
	}
	return fmt.Sprintf("http %d", status)
}
