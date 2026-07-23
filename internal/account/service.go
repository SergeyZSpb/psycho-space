package account

import (
	"context"
	"encoding/hex"

	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
)

// Service is the account business logic: encrypt on write, decrypt on read,
// blind-index for lookups.
type Service struct {
	q    db.DBTX
	repo Repository
	enc  *crypto.Encryptor
	bi   *crypto.BlindIndexer
}

// NewService wires the account service.
func NewService(q db.DBTX, repo Repository, enc *crypto.Encryptor, bi *crypto.BlindIndexer) *Service {
	return &Service{q: q, repo: repo, enc: enc, bi: bi}
}

// LoginInput is the profile pulled from VK plus the consent version.
// AutoApprove (open-registration mode) approves a NEW account immediately with
// the standard user role; it never affects an existing account.
type LoginInput struct {
	VKUserID       string
	FirstName      string
	LastName       string
	Avatar         string
	ConsentVersion string
	AutoApprove    bool
}

// UpsertOnLogin creates or refreshes the account for a VK user and records consent.
func (s *Service) UpsertOnLogin(ctx context.Context, in LoginInput) (*Account, error) {
	vkEnc, err := s.enc.EncryptString(in.VKUserID)
	if err != nil {
		return nil, err
	}
	fnEnc, err := s.encOptional(in.FirstName)
	if err != nil {
		return nil, err
	}
	lnEnc, err := s.encOptional(in.LastName)
	if err != nil {
		return nil, err
	}
	avEnc, err := s.encOptional(in.Avatar)
	if err != nil {
		return nil, err
	}

	defaultStatus := StatusPending
	if in.AutoApprove {
		defaultStatus = StatusApproved
	}
	row, err := s.repo.Upsert(ctx, s.q, UpsertParams{
		Ref:            s.bi.Index(in.VKUserID),
		VKUserIDEnc:    vkEnc,
		FirstNameEnc:   fnEnc,
		LastNameEnc:    lnEnc,
		AvatarEnc:      avEnc,
		ConsentVersion: in.ConsentVersion,
		DefaultStatus:  defaultStatus,
	})
	if err != nil {
		return nil, err
	}
	return s.toAccount(row)
}

// GetByID returns the decrypted account.
func (s *Service) GetByID(ctx context.Context, id string) (*Account, error) {
	row, err := s.repo.GetByID(ctx, s.q, id)
	if err != nil {
		return nil, err
	}
	return s.toAccount(row)
}

// ListByStatus returns decrypted accounts in a given allowlist state.
func (s *Service) ListByStatus(ctx context.Context, status string) ([]*Account, error) {
	rows, err := s.repo.ListByStatus(ctx, s.q, status)
	if err != nil {
		return nil, err
	}
	out := make([]*Account, 0, len(rows))
	for _, r := range rows {
		a, err := s.toAccount(r)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Approve allowlists an account.
func (s *Service) Approve(ctx context.Context, id string) error {
	return s.repo.SetStatus(ctx, s.q, id, StatusApproved)
}

// Block bars an account.
func (s *Service) Block(ctx context.Context, id string) error {
	return s.repo.SetStatus(ctx, s.q, id, StatusBlocked)
}

// Promote makes an account an approved admin.
func (s *Service) Promote(ctx context.Context, id string) error {
	return s.repo.Promote(ctx, s.q, id)
}

func (s *Service) encOptional(v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	return s.enc.EncryptString(v)
}

func (s *Service) decOptional(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	return s.enc.DecryptString(blob)
}

func (s *Service) toAccount(r encRow) (*Account, error) {
	vkID, err := s.enc.DecryptString(r.VKUserIDEnc)
	if err != nil {
		return nil, err
	}
	fn, err := s.decOptional(r.FirstNameEnc)
	if err != nil {
		return nil, err
	}
	ln, err := s.decOptional(r.LastNameEnc)
	if err != nil {
		return nil, err
	}
	av, err := s.decOptional(r.AvatarEnc)
	if err != nil {
		return nil, err
	}
	handle := hex.EncodeToString(r.Ref)
	if len(handle) > 8 {
		handle = handle[:8]
	}
	return &Account{
		ID:        r.ID,
		Role:      r.Role,
		Status:    r.Status,
		VKUserID:  vkID,
		FirstName: fn,
		LastName:  ln,
		AvatarURL: av,
		Handle:    handle,
		CreatedAt: r.CreatedAt,
	}, nil
}
