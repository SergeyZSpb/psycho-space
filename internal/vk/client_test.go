package vk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExchangeCodeAndUserInfo(t *testing.T) {
	var gotExchange, gotUserInfo bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/oauth2/auth":
			gotExchange = true
			if r.Form.Get("grant_type") != "authorization_code" {
				t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
			}
			if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") != "the-verifier" || r.Form.Get("device_id") != "dev-1" {
				t.Errorf("unexpected form: %v", r.Form)
			}
			if r.Form.Get("service_token") != "svc-token" {
				t.Errorf("service_token = %q", r.Form.Get("service_token"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","id_token":"IDT","expires_in":3600,"user_id":777}`))
		case "/oauth2/user_info":
			gotUserInfo = true
			if r.Form.Get("access_token") != "AT" {
				t.Errorf("access_token = %q", r.Form.Get("access_token"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user":{"user_id":"777","first_name":"Иван","last_name":"Петров","avatar":"https://vk/av.jpg"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "app-1", "svc-token", "https://psycho-space.ru/api/auth/vk/callback")

	tok, err := c.ExchangeCode(context.Background(), "the-code", "the-verifier", "dev-1")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "AT" || tok.UserID != "777" || tok.IDToken != "IDT" {
		t.Fatalf("tokens = %+v", tok)
	}

	info, err := c.UserInfo(context.Background(), tok.AccessToken)
	if err != nil {
		t.Fatalf("UserInfo: %v", err)
	}
	if info.UserID != "777" || info.FirstName != "Иван" || info.LastName != "Петров" || info.Avatar == "" {
		t.Fatalf("info = %+v", info)
	}
	if !gotExchange || !gotUserInfo {
		t.Fatalf("endpoints not hit: exchange=%v userinfo=%v", gotExchange, gotUserInfo)
	}
}

func TestExchangeCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"bad code"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "app-1", "svc", "uri")
	if _, err := c.ExchangeCode(context.Background(), "x", "y", "z"); err == nil {
		t.Fatal("expected error on 400")
	}
}

func TestUserInfoFlatShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":42,"first_name":"A","last_name":"B","avatar":"x"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "app", "svc", "uri")
	info, err := c.UserInfo(context.Background(), "AT")
	if err != nil {
		t.Fatalf("UserInfo: %v", err)
	}
	if info.UserID != "42" {
		t.Fatalf("flat user_id = %q", info.UserID)
	}
}
