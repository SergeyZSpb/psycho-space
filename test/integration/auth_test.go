//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SergeyZSpb/psycho-space/internal/account"
)

type callbackBody struct {
	Code           string `json:"code"`
	DeviceID       string `json:"device_id"`
	State          string `json:"state"`
	CodeVerifier   string `json:"code_verifier"`
	ConsentVersion string `json:"consent_version"`
}

func getState(t *testing.T, cli *http.Client, base string) string {
	t.Helper()
	resp, err := cli.Get(base + "/api/auth/vk/state")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	defer resp.Body.Close()
	var m map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if m["state"] == "" {
		t.Fatal("empty state")
	}
	return m["state"]
}

func postCallback(t *testing.T, cli *http.Client, base string, b callbackBody) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(b)
	resp, err := cli.Post(base+"/api/auth/vk/callback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestVKLoginFlow(t *testing.T) {
	vkSrv := fakeVK("777", "Иван", "Петров")
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}

	// First login → pending (allowlist gate).
	state := getState(t, cli, app.URL)
	status, resp := postCallback(t, cli, app.URL, callbackBody{
		Code: "c", DeviceID: "d", State: state, CodeVerifier: "v", ConsentVersion: account.ConsentVersion,
	})
	if status != http.StatusOK || resp["status"] != "pending" {
		t.Fatalf("first login: status=%d body=%v; want pending", status, resp)
	}
	acc0, _ := resp["account"].(map[string]any)
	handle, _ := acc0["handle"].(string)
	if len(handle) != 8 {
		t.Fatalf("handle = %q, want 8 hex chars (account=%v)", handle, acc0)
	}
	// Pending users get a session so the SPA can poll for approval.
	if ms, me := doJSON(t, cli, http.MethodGet, app.URL+"/api/auth/me", nil); ms != http.StatusOK || me["account"].(map[string]any)["status"] != "pending" {
		t.Fatalf("pending /me: status=%d body=%v; want 200 pending", ms, me)
	}

	// Approve THIS test's account, found by its own VK id.
	//
	// It used to approve `pending[0]` — the oldest pending account in the whole
	// database — which is only this test's account when no other test has left
	// one behind. That held until the first test that does, and then this one
	// failed somewhere else entirely with a confusing message about a login not
	// being approved.
	svc := newAccountService()
	pending, err := svc.ListByStatus(context.Background(), "pending")
	if err != nil || len(pending) == 0 {
		t.Fatalf("list pending: err=%v n=%d", err, len(pending))
	}
	if err := svc.Approve(context.Background(), accountIDByUID(t, "777")); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Second login → approved + session cookie.
	state2 := getState(t, cli, app.URL)
	status, resp = postCallback(t, cli, app.URL, callbackBody{
		Code: "c", DeviceID: "d", State: state2, CodeVerifier: "v", ConsentVersion: account.ConsentVersion,
	})
	if status != http.StatusOK || resp["status"] != "approved" {
		t.Fatalf("second login: status=%d body=%v; want approved", status, resp)
	}

	// /me returns the account with decrypted display + clickable VK link.
	meResp, err := cli.Get(app.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d", meResp.StatusCode)
	}
	var me map[string]any
	_ = json.NewDecoder(meResp.Body).Decode(&me)
	acc := me["account"].(map[string]any)
	if acc["display_name"] != "Иван Петров" {
		t.Fatalf("display_name = %v", acc["display_name"])
	}
	if acc["profile_url"] != "https://vk.com/id777" {
		t.Fatalf("profile_url = %v", acc["profile_url"])
	}

	// The full vkid.personal_info set is stored: sex + birthday decrypt back.
	stored, err := svc.GetByID(context.Background(), accountIDByUID(t, "777"))
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	// Stored in the shared vocabulary, not VK's: the fake VK server sends sex=2
	// and an unpadded "15.5.1990", and both are normalised at the provider
	// boundary so a VK row and a Yandex row are indistinguishable here.
	if stored.Sex != "male" || stored.Birthday != "1990-05-15" {
		t.Fatalf("sex/birthday = %q / %q; want \"male\" / \"1990-05-15\"", stored.Sex, stored.Birthday)
	}

	// Personal data is encrypted at rest — the plaintext name / birth year must not appear.
	var enc, bdEnc []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT first_name_enc, birthday_enc FROM accounts WHERE encode(identity_ref,'hex') LIKE $1`, handle+"%",
	).Scan(&enc, &bdEnc); err != nil {
		t.Fatalf("read enc: %v", err)
	}
	if bytes.Contains(enc, []byte("Иван")) {
		t.Fatal("plaintext name found in first_name_enc — not encrypted!")
	}
	if bytes.Contains(bdEnc, []byte("1990")) {
		t.Fatal("plaintext birth year found in birthday_enc — not encrypted!")
	}

	// Logout revokes the session.
	logoutResp, err := cli.Post(app.URL+"/api/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutResp.StatusCode)
	}
	after, err := cli.Get(app.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("me after logout: %v", err)
	}
	after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401", after.StatusCode)
	}
}

func TestConsentRequired(t *testing.T) {
	vkSrv := fakeVK("888", "A", "B")
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}

	state := getState(t, cli, app.URL)
	status, resp := postCallback(t, cli, app.URL, callbackBody{
		Code: "c", DeviceID: "d", State: state, CodeVerifier: "v", ConsentVersion: "",
	})
	if status != http.StatusBadRequest || resp["error"] != "consent_required" {
		t.Fatalf("no-consent: status=%d body=%v; want 400 consent_required", status, resp)
	}
}

// TestConsentEditionIsTheServers pins the second half of ADR-008: the edition a
// login is recorded under is the server's, never whatever the browser sent. An
// edition the server does not publish — the previous one from a tab left open
// across a deploy, or one that never existed — is refused before the provider
// is contacted, so no record can claim agreement to a text the server is not
// serving. Each stale case runs against a fresh state, so the refusal cannot be
// the CSRF check answering instead.
func TestConsentEditionIsTheServers(t *testing.T) {
	vk := fakeVK("889", "A", "B")
	defer vk.Close()
	var providerCalls atomic.Int32
	vkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		vk.Config.Handler.ServeHTTP(w, r)
	}))
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}

	for _, stale := range []string{"v3", "v999"} {
		state := getState(t, cli, app.URL)
		status, resp := postCallback(t, cli, app.URL, callbackBody{
			Code: "c", DeviceID: "d", State: state, CodeVerifier: "v", ConsentVersion: stale,
		})
		if status != http.StatusConflict || resp["error"] != "consent_outdated" {
			t.Fatalf("edition %q: status=%d body=%v; want 409 consent_outdated", stale, status, resp)
		}
	}
	if n := providerCalls.Load(); n != 0 {
		t.Fatalf("a refused login reached the provider %d times; want 0", n)
	}

	state := getState(t, cli, app.URL)
	status, resp := postCallback(t, cli, app.URL, callbackBody{
		Code: "c", DeviceID: "d", State: state, CodeVerifier: "v", ConsentVersion: account.ConsentVersion,
	})
	if status != http.StatusOK {
		t.Fatalf("current edition: status=%d body=%v; want 200", status, resp)
	}
	id, _ := resp["account"].(map[string]any)["id"].(string)
	var stored string
	if err := pool.QueryRow(context.Background(),
		`SELECT consent_version FROM accounts WHERE id = $1::uuid`, id).Scan(&stored); err != nil {
		t.Fatalf("read consent_version: %v", err)
	}
	if stored != account.ConsentVersion {
		t.Fatalf("stored edition = %q, want the server's %q", stored, account.ConsentVersion)
	}
}

func TestBadState(t *testing.T) {
	vkSrv := fakeVK("999", "A", "B")
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}

	_ = getState(t, cli, app.URL) // sets the cookie
	status, resp := postCallback(t, cli, app.URL, callbackBody{
		Code: "c", DeviceID: "d", State: "wrong-state", CodeVerifier: "v", ConsentVersion: account.ConsentVersion,
	})
	if status != http.StatusBadRequest || resp["error"] != "bad_state" {
		t.Fatalf("bad-state: status=%d body=%v; want 400 bad_state", status, resp)
	}
}

func TestConfigNotConfigured(t *testing.T) {
	// A server with VK not configured returns 503 on callback.
	cfgless := httptest.NewServer(buildAppNoVK())
	defer cfgless.Close()
	resp, err := http.Post(cfgless.URL+"/api/auth/vk/callback", "application/json",
		strings.NewReader(`{"consent_version":"v1","code":"c","device_id":"d","code_verifier":"v","state":"s"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	// The code is provider-neutral: which provider failed goes to the log and
	// the trace, never to the client (ADR-024). Pinned because the SPA's
	// KnownErrorCode list has to agree with it.
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "oauth_not_configured" {
		t.Fatalf("error = %v, want oauth_not_configured", body["error"])
	}
}
