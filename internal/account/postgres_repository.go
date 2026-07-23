package account

import (
	"context"
	"errors"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/jackc/pgx/v5"
)

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct{}

// NewPostgresRepository builds the repository.
func NewPostgresRepository() *PostgresRepository { return &PostgresRepository{} }

const selectCols = `id::text, vk_user_ref, vk_user_id_enc, first_name_enc, last_name_enc, avatar_url_enc, role, status, created_at`

func scanRow(row pgx.Row) (encRow, error) {
	var r encRow
	err := row.Scan(&r.ID, &r.Ref, &r.VKUserIDEnc, &r.FirstNameEnc, &r.LastNameEnc, &r.AvatarEnc, &r.Role, &r.Status, &r.CreatedAt)
	return r, err
}

func (PostgresRepository) Upsert(ctx context.Context, q db.DBTX, p UpsertParams) (encRow, error) {
	return scanRow(q.QueryRow(ctx, `
		INSERT INTO accounts
			(vk_user_ref, vk_user_id_enc, first_name_enc, last_name_enc, avatar_url_enc,
			 last_login_at, consent_at, consent_version)
		VALUES ($1, $2, $3, $4, $5, now(), now(), $6)
		ON CONFLICT (vk_user_ref) DO UPDATE SET
			vk_user_id_enc  = EXCLUDED.vk_user_id_enc,
			first_name_enc  = EXCLUDED.first_name_enc,
			last_name_enc   = EXCLUDED.last_name_enc,
			avatar_url_enc  = EXCLUDED.avatar_url_enc,
			last_login_at   = now(),
			updated_at      = now(),
			consent_at      = now(),
			consent_version = EXCLUDED.consent_version
		RETURNING `+selectCols,
		p.Ref, p.VKUserIDEnc, p.FirstNameEnc, p.LastNameEnc, p.AvatarEnc, p.ConsentVersion))
}

func (PostgresRepository) GetByID(ctx context.Context, q db.DBTX, id string) (encRow, error) {
	r, err := scanRow(q.QueryRow(ctx,
		`SELECT `+selectCols+` FROM accounts WHERE id = $1::uuid AND deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return encRow{}, ErrNotFound
	}
	return r, err
}

func (PostgresRepository) ListByStatus(ctx context.Context, q db.DBTX, status string) ([]encRow, error) {
	rows, err := q.Query(ctx,
		`SELECT `+selectCols+` FROM accounts WHERE status = $1 AND deleted_at IS NULL ORDER BY created_at`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []encRow
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (PostgresRepository) SetStatus(ctx context.Context, q db.DBTX, id, status string) error {
	tag, err := q.Exec(ctx,
		`UPDATE accounts SET status = $2, updated_at = now() WHERE id = $1::uuid AND deleted_at IS NULL`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (PostgresRepository) Promote(ctx context.Context, q db.DBTX, id string) error {
	tag, err := q.Exec(ctx,
		`UPDATE accounts SET role = 'admin', status = 'approved', updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
