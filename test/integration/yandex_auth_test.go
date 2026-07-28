//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/gameassets"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/settings"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/SergeyZSpb/psycho-space/internal/yandex"
)

// The URL Yandex returns the browser to. Like VK's, it is a PAGE of the SPA and
// not the POST-only callback endpoint — a browser landing on the API would get
// a bare 405, which is the production bug this project has already had once.
const yandexRedirect = "https://psycho-space.ru/auth/yandex/redirect"

// A PKCE challenge of the right shape (43 chars, unreserved). The state endpoint
// validates it before interpolating it into a URL it hands a browser.
const testCodeChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

// fakeYandex stands in for oauth.yandex.ru + login.yandex.ru. The `code` posted
// to /token IS the user id, so one server mints as many distinct users as a test
// needs — the same trick fakeVKDynamic uses.
//
// It is deliberately strict about the two things most easily got wrong against
// the real Yandex: /token is POST-only and form-encoded, and /info demands
// `Authorization: OAuth <token>` rather than `Bearer`.
func fakeYandex() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") == "" {
				http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `{"token_type":"bearer","access_token":"AT-%s","expires_in":3600}`, r.Form.Get("code"))
		case "/info":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "OAuth ")
			if tok == r.Header.Get("Authorization") || tok == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			uid := strings.TrimPrefix(tok, "AT-")
			fmt.Fprintf(w, `{"id":%q,"first_name":"Иван","last_name":"Яндексов","sex":"male","birthday":"1990-05-15","default_avatar_id":"abc123","is_avatar_empty":false}`, uid)
		default:
			http.NotFound(w, r)
		}
	}))
}

// buildAppProviders builds the app with either provider configured, both, or
// neither: pass "" for a base URL to leave that provider unconfigured, which is
// how the 503 paths are exercised.
func buildAppProviders(vkBaseURL, yandexBaseURL string) http.Handler {
	sessions := session.NewManager(pool, key(3), time.Hour, false)
	cfg := config.Config{Env: "dev"}

	var vkClient *vk.Client
	if vkBaseURL != "" {
		cfg.VK = config.VK{AppID: "app-1", ServiceToken: "svc", RedirectURI: vkRedirect, BaseURL: vkBaseURL}
		vkClient = vk.New(vkBaseURL, "app-1", "svc", vkRedirect)
	} else {
		vkClient = vk.New("", "", "", "")
	}

	var yandexClient *yandex.Client
	if yandexBaseURL != "" {
		cfg.Yandex = config.Yandex{
			ClientID:     "client-1",
			ClientSecret: "secret-1",
			RedirectURI:  yandexRedirect,
			OAuthBaseURL: yandexBaseURL,
			InfoURL:      yandexBaseURL + "/info",
		}
	}
	yandexClient = yandex.New(cfg.Yandex.OAuthBaseURL, cfg.Yandex.InfoURL,
		cfg.Yandex.ClientID, cfg.Yandex.ClientSecret, cfg.Yandex.RedirectURI)

	h := httpapi.NewServer(httpapi.Deps{
		Config:   cfg,
		Pool:     pool,
		WebFS:    fstest.MapFS{"index.html": {Data: []byte("<html>psycho</html>")}},
		VK:       vkClient,
		Yandex:   yandexClient,
		Accounts: newAccountService(),
		Sessions: sessions,
		Settings: settings.NewService(pool),
		// The asset store is here only because the account service the yard uses
		// takes one; no test in this file touches art.
		GameAssets: gameassets.NewService(pool, gameassets.NewPostgresRepository()),
	}).Handler()
	return observability.WrapHandler(h, "http.server")
}

// yandexState calls the state endpoint the way the SPA does and returns both
// halves of its answer.
func yandexState(t *testing.T, cli *http.Client, base string) (state, authorizeURL string) {
	t.Helper()
	resp, err := cli.Get(base + "/api/auth/yandex/state?code_challenge=" + testCodeChallenge)
	if err != nil {
		t.Fatalf("yandex state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("yandex state: status %d", resp.StatusCode)
	}
	var m map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if m["state"] == "" {
		t.Fatal("empty state")
	}
	return m["state"], m["authorize_url"]
}

func postYandexCallback(t *testing.T, cli *http.Client, base string, b callbackBody) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(b)
	resp, err := cli.Post(base+"/api/auth/yandex/callback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("yandex callback: %v", err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestYandexLoginFlow(t *testing.T) {
	ySrv := fakeYandex()
	defer ySrv.Close()
	app := httptest.NewServer(buildAppProviders("", ySrv.URL))
	defer app.Close()

	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}

	state, authorizeURL := yandexState(t, cli, app.URL)

	// The authorize URL is built by the server, which is the whole point: the
	// client id and the redirect URI exist in one place instead of also being
	// copied into the SPA.
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("authorize_url is not a URL: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != "client-1" || q.Get("redirect_uri") != yandexRedirect {
		t.Fatalf("authorize_url lost its identity: %s", authorizeURL)
	}
	if q.Get("state") != state || q.Get("code_challenge") != testCodeChallenge || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize_url lost its PKCE/state binding: %s", authorizeURL)
	}
	if _, ok := q["scope"]; ok {
		t.Error("authorize_url must not send scope — Yandex grants the rights registered on the app")
	}

	status, body := postYandexCallback(t, cli, app.URL, callbackBody{
		Code: "555", State: state, CodeVerifier: "verifier", ConsentVersion: "v3",
	})
	if status != http.StatusOK {
		t.Fatalf("callback: status %d body %v", status, body)
	}
	if body["status"] != "pending" {
		t.Fatalf("first login should be pending, got %v", body["status"])
	}
	acc, _ := body["account"].(map[string]any)
	if acc["provider"] != "yandex" {
		t.Fatalf("provider = %v, want yandex", acc["provider"])
	}
	// Yandex has no public profile page, so there is genuinely no URL to link to.
	if acc["profile_url"] != "" {
		t.Fatalf("profile_url = %v, want empty for Yandex", acc["profile_url"])
	}
	if acc["display_name"] != "Иван Яндексов" {
		t.Fatalf("display_name = %v", acc["display_name"])
	}
	firstID := acc["id"]

	// Logging in again is the SAME account: the upsert conflicts on
	// (provider, identity_ref) and finds the row it wrote a moment ago.
	state2, _ := yandexState(t, cli, app.URL)
	status, body = postYandexCallback(t, cli, app.URL, callbackBody{
		Code: "555", State: state2, CodeVerifier: "verifier", ConsentVersion: "v3",
	})
	if status != http.StatusOK {
		t.Fatalf("second callback: status %d body %v", status, body)
	}
	acc2, _ := body["account"].(map[string]any)
	if acc2["id"] != firstID {
		t.Fatalf("second login made a new account: %v then %v", firstID, acc2["id"])
	}
}

// TestSameUserIdAtTwoProvidersIsTwoAccounts is the reason migrations/012 exists.
//
// Both providers hand out small numeric user ids, so the same number arrives
// from each. Under the old single-column UNIQUE on the blind index they would
// have collided into one account and whoever logged in second would have taken
// over the first person's everything.
func TestSameUserIdAtTwoProvidersIsTwoAccounts(t *testing.T) {
	const sharedID = "12345"

	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	ySrv := fakeYandex()
	defer ySrv.Close()
	app := httptest.NewServer(buildAppProviders(vkSrv.URL, ySrv.URL))
	defer app.Close()

	vkJar, _ := cookiejar.New(nil)
	vkCli := &http.Client{Jar: vkJar}
	status, body := postCallback(t, vkCli, app.URL, callbackBody{
		Code: sharedID, DeviceID: "dev", State: getState(t, vkCli, app.URL),
		CodeVerifier: "verifier", ConsentVersion: "v3",
	})
	if status != http.StatusOK {
		t.Fatalf("vk callback: status %d body %v", status, body)
	}
	vkAcc, _ := body["account"].(map[string]any)

	yJar, _ := cookiejar.New(nil)
	yCli := &http.Client{Jar: yJar}
	state, _ := yandexState(t, yCli, app.URL)
	status, body = postYandexCallback(t, yCli, app.URL, callbackBody{
		Code: sharedID, State: state, CodeVerifier: "verifier", ConsentVersion: "v3",
	})
	if status != http.StatusOK {
		t.Fatalf("yandex callback: status %d body %v", status, body)
	}
	yAcc, _ := body["account"].(map[string]any)

	if vkAcc["id"] == yAcc["id"] {
		t.Fatal("VK user 12345 and Yandex user 12345 became the SAME account — the blind index collided")
	}
	if vkAcc["provider"] != "vk" || yAcc["provider"] != "yandex" {
		t.Fatalf("providers not recorded: %v / %v", vkAcc["provider"], yAcc["provider"])
	}
	// The handle is the blind index, which is over the raw id and therefore
	// identical for both — that is expected and harmless, because the handle is
	// a display string and never the identity. The identity is the pair.
	if vkAcc["handle"] != yAcc["handle"] {
		t.Logf("handles differ (%v / %v) — fine, but the test's premise was that they would not",
			vkAcc["handle"], yAcc["handle"])
	}
}

func TestYandexConsentRequired(t *testing.T) {
	ySrv := fakeYandex()
	defer ySrv.Close()
	app := httptest.NewServer(buildAppProviders("", ySrv.URL))
	defer app.Close()

	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}
	state, _ := yandexState(t, cli, app.URL)

	status, body := postYandexCallback(t, cli, app.URL, callbackBody{
		Code: "1", State: state, CodeVerifier: "verifier", // no consent version
	})
	if status != http.StatusBadRequest || body["error"] != "consent_required" {
		t.Fatalf("want 400 consent_required, got %d %v", status, body)
	}
}

func TestYandexBadState(t *testing.T) {
	ySrv := fakeYandex()
	defer ySrv.Close()
	app := httptest.NewServer(buildAppProviders("", ySrv.URL))
	defer app.Close()

	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}
	_, _ = yandexState(t, cli, app.URL)

	status, body := postYandexCallback(t, cli, app.URL, callbackBody{
		Code: "1", State: "not-the-state", CodeVerifier: "verifier", ConsentVersion: "v3",
	})
	if status != http.StatusBadRequest || body["error"] != "bad_state" {
		t.Fatalf("want 400 bad_state, got %d %v", status, body)
	}
}

// TestYandexStateCookieIsSeparateFromVKs pins the reason the cookies are named
// per provider: a login started at one provider must not invalidate a login
// started at the other in another tab.
func TestYandexStateCookieIsSeparateFromVKs(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	ySrv := fakeYandex()
	defer ySrv.Close()
	app := httptest.NewServer(buildAppProviders(vkSrv.URL, ySrv.URL))
	defer app.Close()

	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}

	vkStateValue := getState(t, cli, app.URL)
	// Starting the Yandex flow second must not disturb the VK state already held.
	yState, _ := yandexState(t, cli, app.URL)
	if yState == vkStateValue {
		t.Fatal("both providers minted the same state value")
	}

	status, body := postCallback(t, cli, app.URL, callbackBody{
		Code: "9001", DeviceID: "dev", State: vkStateValue,
		CodeVerifier: "verifier", ConsentVersion: "v3",
	})
	if status != http.StatusOK {
		t.Fatalf("the VK login was broken by starting a Yandex one: %d %v", status, body)
	}
}

func TestYandexNotConfigured(t *testing.T) {
	app := httptest.NewServer(buildAppProviders("", "")) // neither provider configured
	defer app.Close()

	resp, err := http.Get(app.URL + "/api/auth/yandex/state?code_challenge=" + testCodeChallenge)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("state: want 503, got %d", resp.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	status, body := postYandexCallback(t, &http.Client{Jar: jar}, app.URL, callbackBody{
		Code: "1", State: "x", CodeVerifier: "v", ConsentVersion: "v3",
	})
	if status != http.StatusServiceUnavailable || body["error"] != "oauth_not_configured" {
		t.Fatalf("callback: want 503 oauth_not_configured, got %d %v", status, body)
	}
}

// TestYandexStateRejectsBadChallenge: the challenge is interpolated into a URL
// handed to a browser, so its shape is checked even though it is public.
func TestYandexStateRejectsBadChallenge(t *testing.T) {
	ySrv := fakeYandex()
	defer ySrv.Close()
	app := httptest.NewServer(buildAppProviders("", ySrv.URL))
	defer app.Close()

	for _, tc := range []struct{ name, challenge string }{
		{"missing", ""},
		{"too short", "abc"},
		{"illegal characters", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSst&<>x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(app.URL + "/api/auth/yandex/state?code_challenge=" + url.QueryEscape(tc.challenge))
			if err != nil {
				t.Fatalf("state: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("want 400, got %d", resp.StatusCode)
			}
		})
	}
}

// TestYandexCallbackRejectsGET pins the redirect-URI trap at the API level: the
// callback is POST-only, so a browser sent there by a misregistered Redirect URI
// gets 405. The Yandex app must point at the SPA page instead.
func TestYandexCallbackRejectsGET(t *testing.T) {
	ySrv := fakeYandex()
	defer ySrv.Close()
	app := httptest.NewServer(buildAppProviders("", ySrv.URL))
	defer app.Close()

	resp, err := http.Get(app.URL + "/api/auth/yandex/callback?code=x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", resp.StatusCode)
	}
}
