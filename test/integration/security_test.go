//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRateLimitAuth confirms the per-IP limit (30/min) on the auth endpoints.
func TestRateLimitAuth(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	cli := &http.Client{}
	got429 := false
	for i := 1; i <= 40; i++ {
		resp, err := cli.Get(app.URL + "/api/auth/vk/state")
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		resp.Body.Close()
		switch {
		case i <= 30 && resp.StatusCode != http.StatusOK:
			t.Fatalf("req %d: status %d, want 200 (under limit)", i, resp.StatusCode)
		case i == 31:
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("req 31: status %d, want 429", resp.StatusCode)
			}
			got429 = true
		}
	}
	if !got429 {
		t.Fatal("never hit the rate limit")
	}
}

// TestBlockRevokesSessions confirms a blocked user's existing session is killed.
func TestBlockRevokesSessions(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	super := loginAs(t, app.URL, "4001", "superadmin")
	user := loginAs(t, app.URL, "4002", "user")

	// User has a working session.
	if s, _ := doJSON(t, user, http.MethodGet, app.URL+"/api/auth/me", nil); s != http.StatusOK {
		t.Fatalf("pre-block me: %d", s)
	}

	// Superadmin blocks them.
	idUser := accountIDByUID(t, "4002")
	if s, _ := doJSON(t, super, http.MethodPost, app.URL+"/api/admin/accounts/"+idUser+"/block", nil); s != http.StatusNoContent {
		t.Fatalf("block: %d", s)
	}

	// The user's session is now revoked → 401 everywhere.
	if s, _ := doJSON(t, user, http.MethodGet, app.URL+"/api/auth/me", nil); s != http.StatusUnauthorized {
		t.Fatalf("post-block me: %d, want 401", s)
	}
	if s, _ := doJSON(t, user, http.MethodGet, app.URL+"/api/wishlist/items", nil); s != http.StatusUnauthorized {
		t.Fatalf("post-block wishlist: %d, want 401", s)
	}
}

// TestSelfBlockForbidden confirms an actor cannot block their own account.
func TestSelfBlockForbidden(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	super := loginAs(t, app.URL, "4101", "superadmin")
	idSuper := accountIDByUID(t, "4101")
	s, body := doJSON(t, super, http.MethodPost, app.URL+"/api/admin/accounts/"+idSuper+"/block", nil)
	if s != http.StatusForbidden || body["error"] != "cannot_modify_self" {
		t.Fatalf("self-block: status=%d body=%v; want 403 cannot_modify_self", s, body)
	}
}
