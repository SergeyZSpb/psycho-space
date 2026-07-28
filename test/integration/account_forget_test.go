//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
)

// Forgetting a person while keeping what they wrote.
//
// The whole feature in one shape: after it, the same VK account logging in
// again is a genuinely NEW account, and every contribution the old one made is
// still there with an anonymous author. Both halves are asserted, because
// either one alone is a different (and wrong) feature.

// forgottenState reads the columns that decide whether an account has really
// been anonymised. Straight SQL rather than the API, because the point is what
// is in the database — an endpoint that lies about it would pass a test written
// against itself.
func forgottenState(t *testing.T, id string) (ref []byte, forgotten bool, status, role string, consent *string) {
	t.Helper()
	var forgottenAt *string
	err := pool.QueryRow(context.Background(),
		`SELECT identity_ref, forgotten_at::text, status, role, consent_version
		   FROM accounts WHERE id = $1::uuid`, id).
		Scan(&ref, &forgottenAt, &status, &role, &consent)
	if err != nil {
		t.Fatalf("read account %s: %v", id, err)
	}
	return ref, forgottenAt != nil, status, role, consent
}

func TestForgetAnonymisesThePersonAndKeepsTheContent(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	root := loginAs(t, app.URL, "770001", "superadmin")
	victim := loginAs(t, app.URL, "770002", "user")
	bystander := loginAs(t, app.URL, "770003", "user")
	victimID := accountIDByUID(t, "770002")

	// The victim writes something other people join in on — which is exactly
	// what a hard delete would have taken with them.
	status, item := doJSON(t, victim, http.MethodPost, app.URL+"/api/wishlist/items",
		map[string]any{"title": "заброшка-2", "body": "надо"})
	if status != http.StatusCreated {
		t.Fatalf("create item: %d %v", status, item)
	}
	itemID, _ := item["id"].(string)

	if s, b := doJSON(t, bystander, http.MethodPost,
		app.URL+"/api/wishlist/items/"+itemID+"/comments",
		map[string]any{"body": "плюсую"}); s != http.StatusCreated {
		t.Fatalf("bystander comment: %d %v", s, b)
	}
	if s, _ := doJSON(t, bystander, http.MethodPost,
		app.URL+"/api/wishlist/items/"+itemID+"/vote", nil); s != http.StatusNoContent {
		t.Fatalf("bystander vote: %d", s)
	}

	refBefore, forgottenBefore, _, _, consentBefore := forgottenState(t, victimID)
	if forgottenBefore {
		t.Fatal("a fresh account is already marked forgotten")
	}
	if consentBefore == nil {
		t.Fatal("a logged-in account has no consent recorded, so this test cannot prove it is cleared")
	}

	// --- forget ---
	if s, b := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/"+victimID+"/forget", nil); s != http.StatusNoContent {
		t.Fatalf("forget: %d %v", s, b)
	}

	refAfter, forgotten, statusAfter, roleAfter, consentAfter := forgottenState(t, victimID)
	if !forgotten {
		t.Fatal("forgotten_at was not stamped")
	}
	if string(refAfter) == string(refBefore) {
		t.Fatal("the blind index was not replaced, so a re-login would still find this row")
	}
	if statusAfter != "blocked" || roleAfter != "user" {
		t.Fatalf("status %q role %q after forgetting", statusAfter, roleAfter)
	}
	if consentAfter != nil {
		t.Fatalf("consent survived: %v — a retained consent record for somebody who is gone is the thing minimisation is about", *consentAfter)
	}

	// The personal data is destroyed in place: no ciphertext of a real person
	// left under a key that still exists.
	var firstName, lastName, avatar *[]byte
	if err := pool.QueryRow(context.Background(),
		`SELECT first_name_enc, last_name_enc, avatar_url_enc FROM accounts WHERE id = $1::uuid`,
		victimID).Scan(&firstName, &lastName, &avatar); err != nil {
		t.Fatal(err)
	}
	if firstName != nil || lastName != nil || avatar != nil {
		t.Fatal("encrypted profile fields survived being forgotten")
	}

	// --- and the content is still there, with an anonymous author ---
	s2, list := doJSON(t, bystander, http.MethodGet, app.URL+"/api/wishlist/items", nil)
	if s2 != http.StatusOK {
		t.Fatalf("list items: %d", s2)
	}
	items, _ := list["items"].([]any)
	var found map[string]any
	for _, raw := range items {
		it, _ := raw.(map[string]any)
		if it["id"] == itemID {
			found = it
		}
	}
	if found == nil {
		t.Fatal("the forgotten user's idea disappeared — that is the hard-delete behaviour this feature exists to avoid")
	}
	author, _ := found["author"].(map[string]any)
	name, _ := author["display_name"].(string)
	if name == "" {
		t.Fatal("the author rendered blank rather than anonymous")
	}
	if len(name) < 7 || name[:7] != "psycho-" {
		t.Fatalf("author is %q, expected the psycho-<handle> fallback", name)
	}
	if url, _ := author["profile_url"].(string); url != "" {
		t.Fatalf("the author still links to a VK profile: %q", url)
	}
	if votes, _ := found["votes"].(float64); votes < 1 {
		t.Fatalf("the bystander's vote went with them: %v", votes)
	}

	// The bystander's comment on the forgotten user's idea survived too.
	s3, comments := doJSON(t, bystander, http.MethodGet,
		app.URL+"/api/wishlist/items/"+itemID+"/comments", nil)
	if s3 != http.StatusOK {
		t.Fatalf("list comments: %d", s3)
	}
	if got, _ := comments["comments"].([]any); len(got) != 1 {
		t.Fatalf("expected the bystander's comment to survive, got %d", len(got))
	}
}

func TestAForgottenUserComesBackAsSomebodyElse(t *testing.T) {
	// The half the whole mechanism is for: the blind index is free, so the next
	// login INSERTS rather than conflicting, and the person arrives as a
	// brand-new pending account with a new id.
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	root := loginAs(t, app.URL, "770010", "superadmin")
	loginAs(t, app.URL, "770011", "user")
	oldID := accountIDByUID(t, "770011")

	if s, b := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/"+oldID+"/forget", nil); s != http.StatusNoContent {
		t.Fatalf("forget: %d %v", s, b)
	}

	// The same VK user logs in again, from a clean browser.
	jar, _ := cookiejar.New(nil)
	fresh := &http.Client{Jar: jar}
	resp := loginPending(t, fresh, app.URL, "770011")
	if resp["status"] != "pending" {
		t.Fatalf("a forgotten user came back as %v, not pending", resp["status"])
	}
	acc, _ := resp["account"].(map[string]any)
	newID, _ := acc["id"].(string)
	if newID == "" {
		t.Fatalf("no account id in %v", resp)
	}
	if newID == oldID {
		t.Fatal("the same row was reused — the blind index was not actually freed")
	}

	// And the old row is still there carrying the content, untouched by the
	// new one.
	if _, forgotten, _, _, _ := forgottenState(t, oldID); !forgotten {
		t.Fatal("the old row stopped being marked forgotten")
	}
}

func TestForgetIsGuarded(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	root := loginAs(t, app.URL, "770020", "superadmin")
	admin := loginAs(t, app.URL, "770021", "admin")
	user := loginAs(t, app.URL, "770022", "user")
	rootID := accountIDByUID(t, "770020")
	userID := accountIDByUID(t, "770022")

	// An ordinary admin may approve and block, but not erase — this is a
	// superadmin-only route.
	if s, _ := doJSON(t, admin, http.MethodPost,
		app.URL+"/api/admin/accounts/"+userID+"/forget", nil); s != http.StatusForbidden {
		t.Fatalf("admin forgetting a user: %d, want 403", s)
	}
	// And a plain user certainly may not.
	if s, _ := doJSON(t, user, http.MethodPost,
		app.URL+"/api/admin/accounts/"+userID+"/forget", nil); s != http.StatusForbidden {
		t.Fatalf("user forgetting a user: %d, want 403", s)
	}
	// Nobody erases themselves: it is irreversible and would take the one
	// unrevokable account with it.
	if s, b := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/"+rootID+"/forget", nil); s != http.StatusForbidden {
		t.Fatalf("superadmin forgetting themselves: %d %v, want 403", s, b)
	}
	// Nor a nonexistent one.
	if s, _ := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/11111111-1111-1111-1111-111111111111/forget", nil); s != http.StatusNotFound {
		t.Fatalf("forgetting a stranger: %d, want 404", s)
	}
	// Nor a malformed id, which must not reach a ::uuid cast.
	if s, _ := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/not-a-uuid/forget", nil); s != http.StatusBadRequest {
		t.Fatalf("malformed id: %d, want 400", s)
	}

	// Twice is a refusal rather than a silent success: an admin pressing it
	// again deserves to know the first one worked.
	if s, _ := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/"+userID+"/forget", nil); s != http.StatusNoContent {
		t.Fatalf("first forget: %d", s)
	}
	if s, b := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/"+userID+"/forget", nil); s != http.StatusConflict {
		t.Fatalf("second forget: %d %v, want 409", s, b)
	}
}

func TestForgettingCutsAccessImmediately(t *testing.T) {
	vkSrv := fakeVKDynamic()
	defer vkSrv.Close()
	app := httptest.NewServer(buildApp(vkSrv.URL))
	defer app.Close()

	root := loginAs(t, app.URL, "770030", "superadmin")
	victim := loginAs(t, app.URL, "770031", "user")
	victimID := accountIDByUID(t, "770031")

	// They are logged in and working before.
	if s, _ := doJSON(t, victim, http.MethodGet, app.URL+"/api/wishlist/items", nil); s != http.StatusOK {
		t.Fatalf("victim cannot read the wishlist before being forgotten: %d", s)
	}

	if s, _ := doJSON(t, root, http.MethodPost,
		app.URL+"/api/admin/accounts/"+victimID+"/forget", nil); s != http.StatusNoContent {
		t.Fatalf("forget: %d", s)
	}

	// Their existing session is dead at once — not at the next expiry.
	if s, _ := doJSON(t, victim, http.MethodGet, app.URL+"/api/wishlist/items", nil); s == http.StatusOK {
		t.Fatal("a forgotten user kept their access")
	}

	// And they are gone from every admin list, because an anonymous row nobody
	// can act on is noise on that screen.
	for _, st := range []string{"pending", "approved", "blocked"} {
		s, body := doJSON(t, root, http.MethodGet, app.URL+"/api/admin/accounts?status="+st, nil)
		if s != http.StatusOK {
			t.Fatalf("list %s: %d", st, s)
		}
		raw, _ := json.Marshal(body)
		if contains(string(raw), victimID) {
			t.Fatalf("a forgotten account still appears in the %s list", st)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}
