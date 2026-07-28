package account

import (
	"context"
	"crypto/rand"
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

// LoginInput is the profile pulled from the login provider plus the consent
// version. AutoApprove (open-registration mode) approves a NEW account
// immediately with the standard user role; it never affects an existing account.
//
// Sex and Birthday arrive already normalised — "male"/"female"/"" and ISO
// "YYYY-MM-DD"/"" — because agreeing on that vocabulary is the provider
// clients' job, not this package's.
type LoginInput struct {
	Provider       string
	ProviderUserID string
	FirstName      string
	LastName       string
	Avatar         string
	Sex            string
	Birthday       string
	ConsentVersion string
	AutoApprove    bool
}

// UpsertOnLogin creates or refreshes the account for one provider identity and
// records consent.
//
// The blind index is taken over the provider's RAW user id, never a namespaced
// string: the provider is carried in its own column instead. Changing what goes
// into the index would orphan every account that already exists, and the HMAC
// key cannot be rotated to repair it — see migrations/012.
func (s *Service) UpsertOnLogin(ctx context.Context, in LoginInput) (*Account, error) {
	idEnc, err := s.enc.EncryptString(in.ProviderUserID)
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
	sexEnc, err := s.encOptional(in.Sex)
	if err != nil {
		return nil, err
	}
	bdEnc, err := s.encOptional(in.Birthday)
	if err != nil {
		return nil, err
	}

	defaultStatus := StatusPending
	if in.AutoApprove {
		defaultStatus = StatusApproved
	}
	row, err := s.repo.Upsert(ctx, s.q, UpsertParams{
		Provider:       in.Provider,
		Ref:            s.bi.Index(in.ProviderUserID),
		IdentityIDEnc:  idEnc,
		FirstNameEnc:   fnEnc,
		LastNameEnc:    lnEnc,
		AvatarEnc:      avEnc,
		SexEnc:         sexEnc,
		BirthdayEnc:    bdEnc,
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

// AvatarURL returns one account's avatar, decrypted, or "" when it has none.
//
// A narrow read for callers that want the picture and nothing else — the
// realtime yard draws it on a Ваня. Returning the one field rather than the
// whole Account is the point: everything else on that struct is personal data
// the caller has no business holding, and the smallest thing that satisfies the
// need is the one least likely to end up somewhere it should not.
func (s *Service) AvatarURL(ctx context.Context, id string) (string, error) {
	acc, err := s.GetByID(ctx, id)
	if err != nil {
		return "", err
	}
	return acc.AvatarURL, nil
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

// Forget anonymises an account: the person is removed from the system and
// everything they contributed stays where it is.
//
// After it, the same provider account logging in again is a genuinely NEW
// account — new id, `pending`, the whole first-login flow — because the blind
// index that used to match it has been overwritten with random bytes. That is
// the entire mechanism, and it is why this is not a soft delete: the login
// upsert conflicts on `(provider, identity_ref)`, so a row that merely carried
// a `deleted_at` would still capture the next login and hand back a session for
// an account every read refuses to find.
//
// What survives is what other people are also part of — a wishlist idea with
// replies on it, a comment somebody upvoted, a leaderboard time. Those keep
// rendering, through display fallbacks the code already had: an account with no
// name shows as `psycho-<handle>` and one with no VK id links nowhere.
//
// The reference comes from crypto/rand, not from a hash of anything: a
// derivable replacement would let somebody who knew the input recognise the
// person it replaced, which is most of what anonymising is meant to prevent.
func (s *Service) Forget(ctx context.Context, id string) error {
	ref := make([]byte, refBytes)
	// Since Go 1.24 crypto/rand.Read cannot fail — it panics internally rather
	// than returning an error — so there is no error path to thread out.
	_, _ = rand.Read(ref)

	// The column is NOT NULL, so the provider's user id is overwritten with the
	// ciphertext of an empty string rather than cleared. Decrypting it yields
	// "", which ProfileURL() already turns into no link at all.
	empty, err := s.enc.EncryptString("")
	if err != nil {
		return err
	}
	return s.repo.Forget(ctx, s.q, id, ref, empty)
}

// refBytes is the width of a blind index — HMAC-SHA256 — so a replacement is
// indistinguishable in shape from the value it replaced.
const refBytes = 32

// Promote makes an account an approved admin.
func (s *Service) Promote(ctx context.Context, id string) error {
	return s.repo.Promote(ctx, s.q, id)
}

// Demote returns an account to the standard user role.
func (s *Service) Demote(ctx context.Context, id string) error {
	return s.repo.Demote(ctx, s.q, id)
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
	providerUserID, err := s.enc.DecryptString(r.IdentityIDEnc)
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
	sex, err := s.decOptional(r.SexEnc)
	if err != nil {
		return nil, err
	}
	bd, err := s.decOptional(r.BirthdayEnc)
	if err != nil {
		return nil, err
	}
	handle := hex.EncodeToString(r.Ref)
	if len(handle) > 8 {
		handle = handle[:8]
	}
	return &Account{
		ID:             r.ID,
		Role:           r.Role,
		Status:         r.Status,
		Provider:       r.Provider,
		ProviderUserID: providerUserID,
		FirstName:      fn,
		LastName:       ln,
		AvatarURL:      av,
		Sex:            sex,
		Birthday:       bd,
		Handle:         handle,
		CreatedAt:      r.CreatedAt,
	}, nil
}
