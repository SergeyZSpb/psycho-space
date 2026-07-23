package vk

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// IDTokenVerifier verifies the RS256 signature of a VK ID id_token against a
// JWKS, checks expiry, and asserts the subject matches the expected user id.
// This is defense-in-depth: the code exchange already happens over a direct TLS
// channel to VK, but verifying the id_token guards against a tampered/misrouted
// token response.
type IDTokenVerifier struct {
	keyfunc jwt.Keyfunc
	issuer  string
}

// NewIDTokenVerifier fetches the JWKS (with caching + rotation) from jwksURL.
// issuer, when non-empty, is asserted against the token's iss claim.
func NewIDTokenVerifier(ctx context.Context, jwksURL, issuer string) (*IDTokenVerifier, error) {
	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("vk: init JWKS from %s: %w", jwksURL, err)
	}
	return &IDTokenVerifier{keyfunc: k.Keyfunc, issuer: issuer}, nil
}

// Verify checks the id_token and that its subject equals expectedUserID.
func (v *IDTokenVerifier) Verify(idToken, expectedUserID string) error {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithExpirationRequired(),
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	token, err := jwt.Parse(idToken, v.keyfunc, opts...)
	if err != nil {
		return fmt.Errorf("vk: id_token invalid: %w", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return errors.New("vk: id_token has unexpected claims type")
	}
	sub := claimString(claims, "sub")
	if sub == "" {
		sub = claimString(claims, "user_id")
	}
	if sub == "" || sub != expectedUserID {
		return fmt.Errorf("vk: id_token subject %q != user id %q", sub, expectedUserID)
	}
	return nil
}

// claimString reads a claim that may be a string or a number.
func claimString(claims jwt.MapClaims, key string) string {
	switch v := claims[key].(type) {
	case string:
		return v
	case float64:
		// avoid scientific notation for integer ids
		return strings.TrimSuffix(fmt.Sprintf("%.0f", v), ".0")
	case int64:
		return fmt.Sprintf("%d", v)
	default:
		return ""
	}
}
