// Package settings is a tiny key/value store for global app settings held in
// Postgres. The only setting today is open_registration.
package settings

import (
	"context"
	"errors"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/jackc/pgx/v5"
)

// KeyOpenRegistration, when "true", auto-approves newly created accounts on
// first login (they still get the standard `user` role).
const KeyOpenRegistration = "open_registration"

// Service reads and writes app settings.
type Service struct {
	q db.DBTX
}

// NewService wires the settings service.
func NewService(q db.DBTX) *Service { return &Service{q: q} }

func (s *Service) get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.q.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Service) set(ctx context.Context, key, value string) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

// OpenRegistration reports whether new users are auto-approved on first login.
func (s *Service) OpenRegistration(ctx context.Context) (bool, error) {
	v, err := s.get(ctx, KeyOpenRegistration)
	return v == "true", err
}

// SetOpenRegistration toggles open registration.
func (s *Service) SetOpenRegistration(ctx context.Context, enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.set(ctx, KeyOpenRegistration, v)
}
