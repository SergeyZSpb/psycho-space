// Package session manages server-side opaque sessions. The raw token lives only
// in the client's httpOnly cookie; the DB stores an HMAC of it with a TTL, so
// sessions are revocable and no bearer secret is recoverable from the database.
package session

import (
	"context"
	"errors"
	"fmt"
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

// Resolved is a session that is currently valid: its own primary key and the
// account it belongs to.
//
// The id is here so that something outliving the request — a WebSocket, which
// is authorised once at the handshake and then has no request left to re-check
// on — can retain a handle to *this* session and be re-judged against it later.
// It is the row's id and never the token: only the token's HMAC is stored, and
// the raw value is meant to live in the cookie and nowhere else.
type Resolved struct {
	ID        string
	AccountID string
}

// Resolve returns the session and its account for a valid token.
func (m *Manager) Resolve(ctx context.Context, raw string) (Resolved, error) {
	if raw == "" {
		return Resolved{}, ErrInvalid
	}
	var out Resolved
	err := m.q.QueryRow(ctx,
		`SELECT id::text, account_id::text FROM sessions
		 WHERE token_hash = $1 AND deleted_at IS NULL AND expires_at > now()`,
		m.hash(raw),
	).Scan(&out.ID, &out.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolved{}, ErrInvalid
	}
	if err != nil {
		return Resolved{}, err
	}
	return out, nil
}

// RevokedSessions reports which of sessionIDs may no longer act: expired,
// revoked, unknown, or belonging to an account that is no longer approved.
//
// It answers the whole batch in one query on purpose. Its caller is a periodic
// sweep over every live WebSocket, and asking per socket would be two hundred
// round trips every half-minute, forever, to learn nothing almost every time.
//
// The condition is deliberately the same one requireAuth applies per request —
// a live session AND an approved account — expressed for many rows at once.
// **If either half of that rule changes, this query changes with it**; the whole
// value of the sweep is that a socket is held to the same standard as an HTTP
// request, and the two drifting apart would be invisible until someone kept
// access they should have lost.
//
// Unknown ids count as revoked, which is the fail-closed reading and the right
// one: a session id that names no row cannot be evidence of authorisation. An
// error is NOT revocation — it is returned as an error so the caller closes
// nobody (see realtime.Authorizer).
func (m *Manager) RevokedSessions(ctx context.Context, sessionIDs []string) ([]string, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	// Ask which are alive and subtract, rather than asking the database to
	// report absences: an id that matches nothing then falls out by construction,
	// with no reliance on the query echoing back ids it never found.
	//
	// 'approved' is spelled out rather than imported from the account package —
	// this is a query against a column whose CHECK constraint spells the same
	// three values out in 001_init.sql, and it mirrors account.StatusApproved.
	rows, err := m.q.Query(ctx,
		`SELECT s.id::text
		   FROM sessions s
		   JOIN accounts a ON a.id = s.account_id
		  WHERE s.id = ANY($1::uuid[])
		    AND s.deleted_at IS NULL
		    AND s.expires_at > now()
		    AND a.deleted_at IS NULL
		    AND a.status = 'approved'`,
		sessionIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("session: revalidate: %w", err)
	}
	defer rows.Close()

	alive := make(map[string]struct{}, len(sessionIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("session: revalidate scan: %w", err)
		}
		alive[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: revalidate rows: %w", err)
	}

	var dead []string
	for _, id := range sessionIDs {
		if _, ok := alive[id]; !ok {
			dead = append(dead, id)
		}
	}
	return dead, nil
}

// Revoke soft-deletes the session for a token (idempotent).
func (m *Manager) Revoke(ctx context.Context, raw string) error {
	_, err := m.q.Exec(ctx,
		`UPDATE sessions SET deleted_at = now() WHERE token_hash = $1 AND deleted_at IS NULL`,
		m.hash(raw),
	)
	return err
}

// RevokeAllForAccount soft-deletes every active session for an account (used
// when access is revoked/blocked so the cut is immediate everywhere).
func (m *Manager) RevokeAllForAccount(ctx context.Context, accountID string) error {
	_, err := m.q.Exec(ctx,
		`UPDATE sessions SET deleted_at = now() WHERE account_id = $1::uuid AND deleted_at IS NULL`,
		accountID,
	)
	return err
}

// SetCookie writes the session cookie.
func (m *Manager) SetCookie(w http.ResponseWriter, raw string) {
	//nolint:gosec // G124: Secure is env-driven (config.CookieSecure — true in prod,
	// false only so local http://localhost works); HttpOnly and SameSite are set below.
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
}

// ClearCookie expires the session cookie.
func (m *Manager) ClearCookie(w http.ResponseWriter) {
	//nolint:gosec // G124: Secure is env-driven (config.CookieSecure — true in prod,
	// false only so local http://localhost works); HttpOnly and SameSite are set below.
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
