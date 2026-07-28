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
	SexEnc       []byte
	BirthdayEnc  []byte
	Role         string
	Status       string
	CreatedAt    time.Time
}

// UpsertParams carries the encrypted fields to insert/update on login.
// DefaultStatus is applied only on INSERT (new account); an existing account's
// status is never changed by login.
type UpsertParams struct {
	Ref            []byte
	VKUserIDEnc    []byte
	FirstNameEnc   []byte
	LastNameEnc    []byte
	AvatarEnc      []byte
	SexEnc         []byte
	BirthdayEnc    []byte
	ConsentVersion string
	DefaultStatus  string
}

// Repository is the storage boundary for accounts. All methods take a db.DBTX so
// they compose with transactions.
type Repository interface {
	Upsert(ctx context.Context, q db.DBTX, p UpsertParams) (encRow, error)
	GetByID(ctx context.Context, q db.DBTX, id string) (encRow, error)
	ListByStatus(ctx context.Context, q db.DBTX, status string) ([]encRow, error)
	SetStatus(ctx context.Context, q db.DBTX, id, status string) error
	Promote(ctx context.Context, q db.DBTX, id string) error
	Demote(ctx context.Context, q db.DBTX, id string) error
	// Forget anonymises an account in place: the blind index is replaced with
	// the caller-supplied random reference, every encrypted field is emptied,
	// consent is withdrawn and `forgotten_at` is stamped. What the account
	// WROTE is untouched — see migrations/011 for why this is not a delete.
	//
	// The new reference and the empty ciphertext are passed in rather than
	// generated here, because only the service holds the encryptor and only
	// crypto/rand should be minting the reference.
	Forget(ctx context.Context, q db.DBTX, id string, newRef, emptyEnc []byte) error
}
