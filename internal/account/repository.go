package account

import (
	"context"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// encRow is the raw, still-encrypted representation of an account row.
type encRow struct {
	ID           string
	Ref          []byte
	VKUserIDEnc  []byte
	FirstNameEnc []byte
	LastNameEnc  []byte
	AvatarEnc    []byte
	Role         string
	Status       string
	CreatedAt    time.Time
}

// UpsertParams carries the encrypted fields to insert/update on login.
type UpsertParams struct {
	Ref            []byte
	VKUserIDEnc    []byte
	FirstNameEnc   []byte
	LastNameEnc    []byte
	AvatarEnc      []byte
	ConsentVersion string
}

// Repository is the storage boundary for accounts. All methods take a db.DBTX so
// they compose with transactions.
type Repository interface {
	Upsert(ctx context.Context, q db.DBTX, p UpsertParams) (encRow, error)
	GetByID(ctx context.Context, q db.DBTX, id string) (encRow, error)
	ListByStatus(ctx context.Context, q db.DBTX, status string) ([]encRow, error)
	SetStatus(ctx context.Context, q db.DBTX, id, status string) error
	Promote(ctx context.Context, q db.DBTX, id string) error
}
