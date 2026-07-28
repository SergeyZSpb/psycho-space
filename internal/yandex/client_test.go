package yandex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// infoServer serves the profile endpoint the way Yandex does: it answers 401
// unless the request carries "Authorization: OAuth <token>", which is what
// pins the scheme (a "Bearer" prefix must not pass).
func infoServer(t *testing.T, token, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format = %q; want json", r.URL.Query().Get("format"))
		}
		if r.Header.Get("Authorization") != "OAuth "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestExchangeCode(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Errorf("path = %q; want /token", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q; want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %q", ct)
		}
		_ = r.ParseForm()
		form = r.Form
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"bearer","access_token":"AT","expires_in":3600}`))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.URL+"/info", "client-1", "s3cret", "https://psycho-space.ru/api/auth/yandex/callback")
	tok, err := c.ExchangeCode(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "AT" || tok.ExpiresIn != 3600 {
		t.Fatalf("tokens = %+v", tok)
	}

	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "the-code",
		"client_id":     "client-1",
		"client_secret": "s3cret",
		"redirect_uri":  "https://psycho-space.ru/api/auth/yandex/callback",
		"code_verifier": "the-verifier",
	}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("form[%s] = %q; want %q", k, got, v)
		}
	}
	if len(form) != len(want) {
		t.Errorf("form has %d fields %v; want exactly %d", len(form), keys(form), len(want))
	}
}

func TestExchangeCodeErrorHidesSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// A provider error body: the helper may surface the documented error
		// fields, and nothing else — least of all what we sent it.
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code has expired","secret_echo":"s3cret"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.URL+"/info", "client-1", "s3cret", "uri")
	_, err := c.ExchangeCode(context.Background(), "x", "y")
	if err == nil {
		t.Fatal("expected an error on 400")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error leaks the client secret: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error = %v; want the provider error code", err)
	}
}

func TestExchangeCodeMissingAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.URL+"/info", "id", "sec", "uri")
	if _, err := c.ExchangeCode(context.Background(), "x", "y"); err == nil {
		t.Fatal("expected an error when the response carries no access_token")
	}
}

func TestUserInfoRejectsBearerScheme(t *testing.T) {
	// The stub only accepts "OAuth AT"; a client sending "Bearer AT" — the
	// classic Yandex mistake — gets the same 401 the real endpoint returns.
	srv := infoServer(t, "AT", `{"id":"1"}`)
	defer srv.Close()

	c := New("", srv.URL+"/info", "id", "sec", "uri")
	if _, err := c.UserInfo(context.Background(), "WRONG"); err == nil {
		t.Fatal("expected an error when the token does not match the OAuth-scheme header")
	}
	if _, err := c.UserInfo(context.Background(), "AT"); err != nil {
		t.Fatalf("UserInfo with the OAuth scheme: %v", err)
	}
}

func TestUserInfoMapsFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want UserInfo
	}{
		{
			name: "full profile",
			body: `{"id":"1234567890","first_name":"Иван","last_name":"Петров",
			        "display_name":"vanya","sex":"male","birthday":"1990-05-15",
			        "default_avatar_id":"abc123","is_avatar_empty":false}`,
			want: UserInfo{
				UserID:    "1234567890",
				FirstName: "Иван",
				LastName:  "Петров",
				Avatar:    "https://avatars.yandex.net/get-yapic/abc123/islands-200",
				Sex:       "male",
				Birthday:  "1990-05-15",
			},
		},
		{
			name: "nulls become empty strings",
			body: `{"id":"42","first_name":"A","last_name":"B","sex":null,"birthday":null,
			        "default_avatar_id":"av","is_avatar_empty":false}`,
			want: UserInfo{
				UserID:    "42",
				FirstName: "A",
				LastName:  "B",
				Avatar:    "https://avatars.yandex.net/get-yapic/av/islands-200",
			},
		},
		{
			name: "female",
			body: `{"id":"7","first_name":"C","last_name":"D","sex":"female","is_avatar_empty":true}`,
			want: UserInfo{UserID: "7", FirstName: "C", LastName: "D", Sex: "female"},
		},
		{
			name: "zeroed birthday components are passed through",
			body: `{"id":"8","first_name":"E","last_name":"F","birthday":"0000-12-23","is_avatar_empty":true}`,
			want: UserInfo{UserID: "8", FirstName: "E", LastName: "F", Birthday: "0000-12-23"},
		},
		{
			name: "placeholder avatar is dropped",
			body: `{"id":"9","first_name":"G","last_name":"H","default_avatar_id":"placeholder","is_avatar_empty":true}`,
			want: UserInfo{UserID: "9", FirstName: "G", LastName: "H"},
		},
		{
			name: "no avatar id at all",
			body: `{"id":"10","first_name":"I","last_name":"J","default_avatar_id":"","is_avatar_empty":false}`,
			want: UserInfo{UserID: "10", FirstName: "I", LastName: "J"},
		},
		{
			name: "avatar id containing a slash is interpolated raw",
			body: `{"id":"11","first_name":"K","last_name":"L","default_avatar_id":"41236/abc-1","is_avatar_empty":false}`,
			want: UserInfo{
				UserID:    "11",
				FirstName: "K",
				LastName:  "L",
				Avatar:    "https://avatars.yandex.net/get-yapic/41236/abc-1/islands-200",
			},
		},
		{
			// display_name is deliberately NOT read: for most Yandex accounts it
			// is the login, usually the local part of an email address, and
			// publishing that to every other player would be both a surprise and
			// more personal data than was asked for. A nameless account renders
			// as psycho-<handle>, exactly as a nameless VK account does.
			name: "a nameless account stays nameless — display_name is not borrowed",
			body: `{"id":"12","first_name":null,"last_name":null,"display_name":"vanya","is_avatar_empty":true}`,
			want: UserInfo{UserID: "12"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := infoServer(t, "AT", tc.body)
			defer srv.Close()

			c := New("", srv.URL+"/info", "id", "sec", "uri")
			got, err := c.UserInfo(context.Background(), "AT")
			if err != nil {
				t.Fatalf("UserInfo: %v", err)
			}
			if *got != tc.want {
				t.Fatalf("UserInfo = %+v; want %+v", *got, tc.want)
			}
		})
	}
}

func TestUserInfoErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`oops`))
	}))
	defer srv.Close()

	c := New("", srv.URL, "id", "sec", "uri")
	_, err := c.UserInfo(context.Background(), "AT")
	if err == nil {
		t.Fatal("expected an error on 500")
	}
	if strings.Contains(err.Error(), "oops") {
		t.Fatalf("error leaks the raw provider body: %v", err)
	}
}

func TestAuthorizeURL(t *testing.T) {
	c := New("https://oauth.example.test/", "", "client-1", "sec", "https://psycho-space.ru/cb")
	raw := c.AuthorizeURL("the-state", "the-challenge")

	// A trailing slash in configuration must not produce "//authorize".
	if !strings.HasPrefix(raw, "https://oauth.example.test/authorize?") {
		t.Fatalf("authorize URL = %q", raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{
		"response_type":         "code",
		"client_id":             "client-1",
		"redirect_uri":          "https://psycho-space.ru/cb",
		"state":                 "the-state",
		"code_challenge":        "the-challenge",
		"code_challenge_method": "S256",
	}
	q := u.Query()
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("query[%s] = %q; want %q", k, got, v)
		}
	}
	if _, ok := q["scope"]; ok {
		t.Error("authorize URL carries a scope parameter; Yandex grants the rights registered on the app")
	}
	if len(q) != len(want) {
		t.Errorf("query has %d params %v; want exactly %d", len(q), keys(q), len(want))
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	c := New("", "", "id", "sec", "uri")
	if c.oauthURL != "https://oauth.yandex.ru" {
		t.Errorf("oauthURL = %q", c.oauthURL)
	}
	if c.infoURL != "https://login.yandex.ru/info" {
		t.Errorf("infoURL = %q", c.infoURL)
	}
	if !strings.HasPrefix(c.AuthorizeURL("s", "c"), "https://oauth.yandex.ru/authorize?") {
		t.Errorf("default authorize URL = %q", c.AuthorizeURL("s", "c"))
	}
}

func keys(v url.Values) []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	return out
}
