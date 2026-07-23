//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
)

// loginPending drives state+callback for uid (code = uid) and returns the client
// (with cookies) and the JSON response.
func loginPending(t *testing.T, cli *http.Client, base, uid string) map[string]any {
	t.Helper()
	state := getState(t, cli, base)
	status, resp := postCallback(t, cli, base, callbackBody{
		Code: uid, DeviceID: "d", State: state, CodeVerifier: "v", ConsentVersion: "v1",
	})
	if status != http.StatusOK {
		t.Fatalf("login %s: status %d body %v", uid, status, resp)
	}
	return resp
}

// loginAs creates uid, forces role+approved, and returns an authed client.
func loginAs(t *testing.T, base, uid, role string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	cli := &http.Client{Jar: jar}
	loginPending(t, cli, base, uid) // creates the pending account
	setRoleStatus(t, accountIDByUID(t, uid), role, "approved")
	jar2, _ := cookiejar.New(nil)
	cli2 := &http.Client{Jar: jar2}
	resp := loginPending(t, cli2, base, uid) // re-login, now approved
	if resp["status"] != "approved" {
		t.Fatalf("loginAs %s: want approved, got %v", uid, resp)
	}
	return cli2
}

func doJSON(t *testing.T, cli *http.Client, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestWishlistFlow(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	cli := loginAs(t, app.URL, "1001", "user")

	// Create an idea.
	status, created := doJSON(t, cli, http.MethodPost, app.URL+"/api/wishlist/items",
		map[string]string{"title": "Большая идея", "body": "описание"})
	if status != http.StatusCreated {
		t.Fatalf("create: status %d body %v", status, created)
	}
	itemID, _ := created["id"].(string)
	if itemID == "" {
		t.Fatalf("no item id: %v", created)
	}
	author := created["author"].(map[string]any)
	if author["display_name"] != "User 1001" || author["vk_url"] != "https://vk.com/id1001" {
		t.Fatalf("author = %v", author)
	}

	// Vote → count 1, voted_by_me true.
	if s, _ := doJSON(t, cli, http.MethodPost, app.URL+"/api/wishlist/items/"+itemID+"/vote", nil); s != http.StatusNoContent {
		t.Fatalf("vote status %d", s)
	}
	items := listItems(t, cli, app.URL)
	if items[0]["votes"].(float64) != 1 || items[0]["voted_by_me"] != true {
		t.Fatalf("after vote: %v", items[0])
	}

	// Voting again is idempotent (still 1).
	_, _ = doJSON(t, cli, http.MethodPost, app.URL+"/api/wishlist/items/"+itemID+"/vote", nil)
	items = listItems(t, cli, app.URL)
	if items[0]["votes"].(float64) != 1 {
		t.Fatalf("double vote changed count: %v", items[0])
	}

	// Unvote → count 0.
	if s, _ := doJSON(t, cli, http.MethodDelete, app.URL+"/api/wishlist/items/"+itemID+"/vote", nil); s != http.StatusNoContent {
		t.Fatalf("unvote status %d", s)
	}
	items = listItems(t, cli, app.URL)
	if items[0]["votes"].(float64) != 0 || items[0]["voted_by_me"] != false {
		t.Fatalf("after unvote: %v", items[0])
	}

	// Empty title rejected.
	if s, body := doJSON(t, cli, http.MethodPost, app.URL+"/api/wishlist/items",
		map[string]string{"title": "  "}); s != http.StatusUnprocessableEntity || body["error"] != "title_required" {
		t.Fatalf("empty title: status %d body %v", s, body)
	}

	// Errors carry a trace_id for the client modal.
	if s, body := doJSON(t, cli, http.MethodPost, app.URL+"/api/wishlist/items/not-a-uuid/vote", nil); s != http.StatusBadRequest || body["trace_id"] == "" {
		t.Fatalf("expected 400 with trace_id, got %d %v", s, body)
	}
}

func listItems(t *testing.T, cli *http.Client, base string) []map[string]any {
	t.Helper()
	resp, err := cli.Get(base + "/api/wishlist/items?sort=top")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	var m struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return m.Items
}

func TestAdminRoles(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	super := loginAs(t, app.URL, "2001", "superadmin")

	// A pending user; superadmin approves them.
	pjar, _ := cookiejar.New(nil)
	loginPending(t, &http.Client{Jar: pjar}, app.URL, "2002")
	idP := accountIDByUID(t, "2002")
	if s, _ := doJSON(t, super, http.MethodPost, app.URL+"/api/admin/accounts/"+idP+"/approve", nil); s != http.StatusNoContent {
		t.Fatalf("super approve: %d", s)
	}

	// Superadmin promotes user V to admin.
	vjar, _ := cookiejar.New(nil)
	loginPending(t, &http.Client{Jar: vjar}, app.URL, "2003")
	idV := accountIDByUID(t, "2003")
	if s, _ := doJSON(t, super, http.MethodPost, app.URL+"/api/admin/accounts/"+idV+"/promote", nil); s != http.StatusNoContent {
		t.Fatalf("super promote: %d", s)
	}

	// V logs in as admin (promote made them approved+admin).
	vClientJar, _ := cookiejar.New(nil)
	vClient := &http.Client{Jar: vClientJar}
	if resp := loginPending(t, vClient, app.URL, "2003"); resp["status"] != "approved" {
		t.Fatalf("V login after promote: %v", resp)
	}

	// Admin V can approve a pending user W.
	wjar, _ := cookiejar.New(nil)
	loginPending(t, &http.Client{Jar: wjar}, app.URL, "2004")
	idW := accountIDByUID(t, "2004")
	if s, _ := doJSON(t, vClient, http.MethodPost, app.URL+"/api/admin/accounts/"+idW+"/approve", nil); s != http.StatusNoContent {
		t.Fatalf("admin V approve: %d", s)
	}

	// Admin V CANNOT promote (superadmin only) → 403.
	if s, _ := doJSON(t, vClient, http.MethodPost, app.URL+"/api/admin/accounts/"+idW+"/promote", nil); s != http.StatusForbidden {
		t.Fatalf("admin V promote: want 403, got %d", s)
	}

	// Admin V CANNOT block the superadmin → 403.
	idSuper := accountIDByUID(t, "2001")
	if s, _ := doJSON(t, vClient, http.MethodPost, app.URL+"/api/admin/accounts/"+idSuper+"/block", nil); s != http.StatusForbidden {
		t.Fatalf("admin V block superadmin: want 403, got %d", s)
	}

	// A regular approved user cannot reach admin at all → 403.
	u := loginAs(t, app.URL, "2005", "user")
	if s, _ := doJSON(t, u, http.MethodGet, app.URL+"/api/admin/accounts?status=pending", nil); s != http.StatusForbidden {
		t.Fatalf("user admin access: want 403, got %d", s)
	}

	// Revoke: superadmin blocks an approved user; they lose wishlist access (403).
	if s, _ := doJSON(t, super, http.MethodPost, app.URL+"/api/admin/accounts/"+idW+"/block", nil); s != http.StatusNoContent {
		t.Fatalf("super block W: %d", s)
	}
	wClientJar, _ := cookiejar.New(nil)
	wClient := &http.Client{Jar: wClientJar}
	loginPending(t, wClient, app.URL, "2004") // W is blocked → no session, status blocked
	if s, _ := doJSON(t, wClient, http.MethodGet, app.URL+"/api/wishlist/items", nil); s != http.StatusUnauthorized && s != http.StatusForbidden {
		t.Fatalf("blocked W wishlist access: want 401/403, got %d", s)
	}
}
