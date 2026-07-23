// Package session manages server-side opaque sessions. The raw token lives only
// in the client's httpOnly cookie; the DB stores an HMAC of it with a TTL, so
// sessions are revocable and no bearer secret is recoverable from the database.
package session

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/jackc/pgx/v5"
)

// CookieName is the session cookie name.
const CookieName = "psycho_session"

// ErrInvalid means the token is unknown, expired, or revoked.
var ErrInvalid = errors.New("session: invalid or expired")

// Manager creates, resolves, and revokes sessions.
type Manager struct {
	q      db.DBTX
	key    []byte
	ttl    time.Duration
	secure bool
}

// NewManager builds a session manager. secure controls the cookie Secure flag.
func NewManager(q db.DBTX, key []byte, ttl time.Duration, secure bool) *Manager {
	return &Manager{q: q, key: key, ttl: ttl, secure: secure}
}

func (m *Manager) hash(raw string) []byte { return crypto.HMACSHA256(m.key, []byte(raw)) }

// Create issues a new session for accountID and returns the raw token.
func (m *Manager) Create(ctx context.Context, accountID string) (string, error) {
	raw, err := crypto.RandomToken(32)
	if err != nil {
		return "", err
	}
	if _, err := m.q.Exec(ctx,
		`INSERT INTO sessions (account_id, token_hash, expires_at) VALUES ($1::uuid, $2, $3)`,
		accountID, m.hash(raw), time.Now().Add(m.ttl),
	); err != nil {
		return "", err
	}
	return raw, nil
}

// Resolve returns the account id for a valid session token.
func (m *Manager) Resolve(ctx context.Context, raw string) (string, error) {
	if raw == "" {
		return "", ErrInvalid
	}
	var accountID string
	err := m.q.QueryRow(ctx,
		`SELECT account_id::text FROM sessions
		 WHERE token_hash = $1 AND deleted_at IS NULL AND expires_at > now()`,
		m.hash(raw),
	).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalid
	}
	if err != nil {
		return "", err
	}
	return accountID, nil
}

// Revoke soft-deletes the session for a token (idempotent).
func (m *Manager) Revoke(ctx context.Context, raw string) error {
	_, err := m.q.Exec(ctx,
		`UPDATE sessions SET deleted_at = now() WHERE token_hash = $1 AND deleted_at IS NULL`,
		m.hash(raw),
	)
	return err
}

// SetCookie writes the session cookie.
func (m *Manager) SetCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
}

// ClearCookie expires the session cookie.
func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
