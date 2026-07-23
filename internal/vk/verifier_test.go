package vk

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func jwksJSON(t *testing.T, pub *rsa.PublicKey, kid string) string {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	m := map[string]any{"keys": []map[string]any{
		{"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig", "n": n, "e": e},
	}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIDTokenVerifier(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid, iss = "test-key", "https://id.vk.test"
	jwks := jwksJSON(t, &key.PublicKey, kid)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwks))
	}))
	defer srv.Close()

	v, err := NewIDTokenVerifier(context.Background(), srv.URL, iss)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	sign := func(claims jwt.MapClaims, signKey *rsa.PrivateKey) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = kid
		s, err := tok.SignedString(signKey)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	future := time.Now().Add(time.Hour).Unix()

	// Valid token.
	valid := sign(jwt.MapClaims{"sub": "777", "iss": iss, "exp": future}, key)
	if err := v.Verify(valid, "777"); err != nil {
		t.Fatalf("valid: %v", err)
	}
	// Subject mismatch.
	if err := v.Verify(valid, "888"); err == nil {
		t.Fatal("expected sub mismatch")
	}
	// Expired.
	if err := v.Verify(sign(jwt.MapClaims{"sub": "777", "iss": iss, "exp": time.Now().Add(-time.Hour).Unix()}, key), "777"); err == nil {
		t.Fatal("expected expired")
	}
	// Wrong issuer.
	if err := v.Verify(sign(jwt.MapClaims{"sub": "777", "iss": "https://evil", "exp": future}, key), "777"); err == nil {
		t.Fatal("expected issuer mismatch")
	}
	// Forged signature (different key, same kid).
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if err := v.Verify(sign(jwt.MapClaims{"sub": "777", "iss": iss, "exp": future}, other), "777"); err == nil {
		t.Fatal("expected signature failure")
	}
	// Numeric user_id claim fallback.
	if err := v.Verify(sign(jwt.MapClaims{"user_id": float64(777), "iss": iss, "exp": future}, key), "777"); err != nil {
		t.Fatalf("numeric user_id: %v", err)
	}
}
