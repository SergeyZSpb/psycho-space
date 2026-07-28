package httpapi

import "testing"

// The challenge is interpolated into a URL handed to a browser. It is public by
// design — the verifier is the secret half — but its shape is still checked
// before it is echoed anywhere.
func TestValidCodeChallenge(t *testing.T) {
	// What createPkce() in the SPA actually produces: base64url of a SHA-256, 43 chars.
	const realistic = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"an S256 challenge", realistic, true},
		{"the full unreserved set", "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~", true},
		{"empty", "", false},
		{"one short of the minimum", realistic[:42], false},
		{"one over the maximum", realistic + realistic + realistic + realistic, false},
		{"contains a query separator", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-c&", false},
		{"contains angle brackets", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw<c>", false},
		{"contains a space", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw c", false},
		{"standard base64 padding is not base64url", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-c=", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validCodeChallenge(tc.in); got != tc.want {
				t.Errorf("validCodeChallenge(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Per-provider state cookies are what stop a half-finished login in one tab
// invalidating a half-finished login at the other provider in another.
func TestStateCookieNamesAreDistinctPerProvider(t *testing.T) {
	vkName := stateCookieName("vk")
	yandexName := stateCookieName("yandex")
	if vkName == yandexName {
		t.Fatalf("both providers share the cookie name %q", vkName)
	}
	for _, name := range []string{vkName, yandexName} {
		if len(name) == 0 || name[0] != 'p' {
			t.Errorf("cookie name %q is not in the psycho_ namespace", name)
		}
	}
}
