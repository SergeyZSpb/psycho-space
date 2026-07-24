// Package account owns the allowlist-gated user accounts. Personal data (VK id,
// name, avatar) is encrypted at rest; lookups use a blind index of the VK id.
package account

import (
	"strings"
	"time"
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
	ID        string
	Role      string
	Status    string
	VKUserID  string // decrypted VK numeric id (for the clickable link)
	FirstName string
	LastName  string
	AvatarURL string
	Sex       string // VK's raw code: "1" female, "2" male, "" unspecified
	Birthday  string // VK's "DD.MM.YYYY" string, or "" if VK omitted it
	Handle    string // first 8 hex of the blind index — the code shown on the pending screen
	CreatedAt time.Time
}

// DisplayName is the human label shown to other users.
func (a Account) DisplayName() string {
	n := strings.TrimSpace(a.FirstName + " " + a.LastName)
	if n == "" {
		return "psycho-" + a.Handle
	}
	return n
}

// VKURL is the clickable link to the user's VK profile.
func (a Account) VKURL() string {
	if a.VKUserID == "" {
		return ""
	}
	return "https://vk.com/id" + a.VKUserID
}

// IsAdmin reports admin capabilities (admin OR superadmin).
func (a Account) IsAdmin() bool { return a.Role == RoleAdmin || a.Role == RoleSuperadmin }

// IsSuperadmin reports the top role — only they may promote users to admin.
func (a Account) IsSuperadmin() bool { return a.Role == RoleSuperadmin }

// IsApproved reports allowlisted access.
func (a Account) IsApproved() bool { return a.Status == StatusApproved }
