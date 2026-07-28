package httpapi

import (
	"context"
	"errors"
)

// oauthProvider is the seam between the login providers. It is defined here, at
// the point of use, rather than in either provider package — the providers must
// not know that a second one exists, and this is the only place that needs both
// to look alike.
//
// The seam is deliberately narrow. Only the exchange and the profile fetch
// differ between VK and Yandex; the consent gate, the CSRF check, the account
// upsert, the session and the response are properties of this application and
// live once, in handleOAuthCallback.
type oauthProvider interface {
	// Name is the value stored in accounts.provider, and the suffix of this
	// provider's state cookie. It is data, not a label: renaming it would
	// orphan every account that arrived through it.
	Name() string

	// Configured reports whether this provider's credentials are present. An
	// unconfigured provider answers 503 rather than failing later and less
	// clearly.
	Configured() bool

	// Exchange trades the authorization code for tokens, on the server, with
	// the confidential credential. It returns errIncompleteRequest when the
	// request is missing something this particular provider needs.
	Exchange(ctx context.Context, req oauthCallbackReq) (*oauthTokens, error)

	// Profile reads the user's profile with the token, and performs whatever
	// identity checks the provider supports. The tokens are used here and then
	// discarded — we never act on a user's behalf at their provider, so
	// keeping a credential that would let us is pure liability (ADR-006).
	Profile(ctx context.Context, tok *oauthTokens) (*oauthProfile, error)
}

// oauthTokens is what a code exchange yields. AccessToken is the only field
// every provider populates; the other two exist because VK returns them and
// cross-checks against them, and are empty for a provider that does not.
type oauthTokens struct {
	AccessToken string
	UserID      string
	IDToken     string
}

// oauthProfile is the normalised profile both providers produce.
//
// Sex is "male", "female" or "", and Birthday is ISO "YYYY-MM-DD" or "",
// whichever provider produced them: agreeing on one vocabulary is the provider
// clients' job, so that nothing downstream has to ask where a value came from
// before it can read it.
type oauthProfile struct {
	UserID    string
	FirstName string
	LastName  string
	Avatar    string
	Sex       string
	Birthday  string
}

// The sentinels a provider may return, each mapping to one stable client-facing
// error code in handleOAuthCallback. Which provider produced it goes to the log
// and the trace, never to the client — a code plus a trace id is the contract
// (ADR-024).
var (
	// errIncompleteRequest means the callback body lacked a field this provider
	// requires — VK's device_id, for instance. It is the caller's fault, so it
	// becomes a 400 rather than a 502.
	errIncompleteRequest = errors.New("oauth: incomplete callback request")

	// errNoUserID means the provider answered without identifying anybody.
	errNoUserID = errors.New("oauth: provider returned no user id")

	// errIdentityMismatch means two of the provider's own answers disagreed
	// about who just logged in.
	errIdentityMismatch = errors.New("oauth: provider identity mismatch")

	// errIDTokenInvalid means a signed identity assertion failed verification.
	errIDTokenInvalid = errors.New("oauth: id_token verification failed")
)
