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
	"testing"
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
		Code: "c", DeviceID: "d", State: state, CodeVerifier: "v", ConsentVersion: "v1",
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

	// Approve the pending account.
	svc := newAccountService()
	pending, err := svc.ListByStatus(context.Background(), "pending")
	if err != nil || len(pending) == 0 {
		t.Fatalf("list pending: err=%v n=%d", err, len(pending))
	}
	if err := svc.Approve(context.Background(), pending[0].ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Second login → approved + session cookie.
	state2 := getState(t, cli, app.URL)
	status, resp = postCallback(t, cli, app.URL, callbackBody{
		Code: "c", DeviceID: "d", State: state2, CodeVerifier: "v", ConsentVersion: "v1",
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
	if acc["vk_url"] != "https://vk.com/id777" {
		t.Fatalf("vk_url = %v", acc["vk_url"])
	}

	// Personal data is encrypted at rest — the plaintext name must not appear.
	var enc []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT first_name_enc FROM accounts WHERE encode(vk_user_ref,'hex') LIKE $1`, handle+"%",
	).Scan(&enc); err != nil {
		t.Fatalf("read enc: %v", err)
	}
	if bytes.Contains(enc, []byte("Иван")) {
		t.Fatal("plaintext name found in first_name_enc — not encrypted!")
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

func TestBadState(t *testing.T) {
	vkSrv := fakeVK("999", "A", "B")
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}

	_ = getState(t, cli, app.URL) // sets the cookie
	status, resp := postCallback(t, cli, app.URL, callbackBody{
		Code: "c", DeviceID: "d", State: "wrong-state", CodeVerifier: "v", ConsentVersion: "v1",
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
}
