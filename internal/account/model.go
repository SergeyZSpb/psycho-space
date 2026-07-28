// Package account owns the allowlist-gated user accounts. Personal data (the
// provider's user id, name, avatar) is encrypted at rest; lookups use a blind
// index of that id, scoped by the provider it came from.
package account

import (
	"strings"
	"time"
)

// Provider is the login provider an identity belongs to. It is half of the
// identity — the other half is the blind index — and only the pair is unique,
// because two providers both hand out small numeric user ids and the same
// number from each must not be the same person.
type Provider = string

const (
	ProviderVK     Provider = "vk"
	ProviderYandex Provider = "yandex"
)

// Status is the allowlist state of an account.
type Status = string

// Role is the authorization role.
type Role = string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusBlocked  Status = "blocked"

	RoleUser       Role = "user"
	RoleAdmin      Role = "admin"
	RoleSuperadmin Role = "superadmin"
)

// Account is the decrypted domain view of an account.
type Account struct {
	ID             string
	Role           string
	Status         string
	Provider       string // which door this identity came through
	ProviderUserID string // decrypted user id at that provider
	FirstName      string
	LastName       string
	AvatarURL      string
	Sex            string // normalised at the provider boundary: "male", "female", or ""
	Birthday       string // normalised to ISO "YYYY-MM-DD", or "" if the provider omitted it
	Handle         string // first 8 hex of the blind index — the code shown on the pending screen
	CreatedAt      time.Time
}

// DisplayName is the human label shown to other users.
func (a Account) DisplayName() string {
	n := strings.TrimSpace(a.FirstName + " " + a.LastName)
	if n == "" {
		return "psycho-" + a.Handle
	}
	return n
}

// ProfileURL is the clickable link to the user's profile at their login
// provider, or "" when there is nothing to link to.
//
// There are three ways to get "" and all of them are ordinary: an account that
// has been forgotten (its id was overwritten with the ciphertext of an empty
// string), and every Yandex account — Yandex ID has no public profile page, so
// there is genuinely no URL to build. Callers already handle the empty case
// because forgetting produced it long before a second provider did.
func (a Account) ProfileURL() string {
	if a.ProviderUserID == "" {
		return ""
	}
	if a.Provider == ProviderVK {
		return "https://vk.com/id" + a.ProviderUserID
	}
	return ""
}

// IsAdmin reports admin capabilities (admin OR superadmin).
func (a Account) IsAdmin() bool { return a.Role == RoleAdmin || a.Role == RoleSuperadmin }

// IsSuperadmin reports the top role — only they may promote users to admin.
func (a Account) IsSuperadmin() bool { return a.Role == RoleSuperadmin }

// IsApproved reports allowlisted access.
func (a Account) IsApproved() bool { return a.Status == StatusApproved }
