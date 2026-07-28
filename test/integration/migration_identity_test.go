//go:build integration

package integration

import (
	"context"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/migrations"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestExistingAccountsSurvivetheIdentityMigration is the test this whole change
// owes, and the only one that can be written.
//
// Every other test in this package runs against a database built by applying
// EVERY migration to an empty schema, so all of them prove that a NEW account
// works. None of them can prove the thing that actually matters here: that an
// account which already existed before 012 — created by the old code, indexed by
// the old blind index, sitting in the old column — is still the same account
// afterwards, and can still log in.
//
// So this test builds a database at the state the world was in before this
// change (migrations 001..011 only), creates an account through the real service
// exactly as the old login path did, then applies 012 and logs the same identity
// in again. The account id, the handle, the role and the status must all be
// unchanged, and no second row may appear.
//
// If this ever fails, DO NOT "fix" it by changing what goes into the blind
// index. The HMAC key cannot be rotated, so an index that stops matching cannot
// be repaired: every existing user would silently become a new pending account
// and lose everything attached to the old row.
func TestExistingAccountsSurvivetheIdentityMigration(t *testing.T) {
	ctx := context.Background()

	// Its own database, at its own migration level. The shared pool from
	// TestMain is already fully migrated and cannot be rewound.
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("psychospace"),
		postgres.WithUsername("psychospace"),
		postgres.WithPassword("psychospace"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	oldPool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(oldPool.Close)

	// --- the world as it was before this change -----------------------------
	if err := db.Migrate(ctx, oldPool, migrationsBefore(t, "012_")); err != nil {
		t.Fatalf("migrate to the pre-012 schema: %v", err)
	}

	enc, err := crypto.NewEncryptor(key(1))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	bi, err := crypto.NewBlindIndexer(key(2))
	if err != nil {
		t.Fatalf("blind indexer: %v", err)
	}

	// The old code had no provider column, so it inserted without one. Writing
	// the row by hand is the honest way to reproduce that: today's service
	// cannot, because it now names columns that do not exist yet.
	const vkUserID = "777777"
	idEnc, err := enc.EncryptString(vkUserID)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	nameEnc, err := enc.EncryptString("Пред Существовавший")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	var beforeID string
	err = oldPool.QueryRow(ctx, `
		INSERT INTO accounts
			(vk_user_ref, vk_user_id_enc, first_name_enc, role, status, last_login_at, consent_at, consent_version)
		VALUES ($1, $2, $3, 'admin', 'approved', now(), now(), 'v2')
		RETURNING id::text`,
		bi.Index(vkUserID), idEnc, nameEnc).Scan(&beforeID)
	if err != nil {
		t.Fatalf("seed a pre-012 account: %v", err)
	}

	// --- apply the change ----------------------------------------------------
	if err := db.Migrate(ctx, oldPool, migrations.FS); err != nil {
		t.Fatalf("apply 012: %v", err)
	}

	// The rename must have moved the VALUES, not just the column names: this is
	// the same account, indexed by the same bytes, because the blind index input
	// did not change.
	var provider string
	var refMatches bool
	err = oldPool.QueryRow(ctx,
		`SELECT provider, identity_ref = $2 FROM accounts WHERE id = $1::uuid`,
		beforeID, bi.Index(vkUserID)).Scan(&provider, &refMatches)
	if err != nil {
		t.Fatalf("read the migrated row: %v", err)
	}
	if provider != account.ProviderVK {
		t.Fatalf("a pre-existing account was backfilled as %q, want %q", provider, account.ProviderVK)
	}
	if !refMatches {
		t.Fatal("the blind index changed during the migration — every existing account is now orphaned")
	}

	// --- and the same person logging in again is the same account ------------
	accounts := account.NewService(oldPool, account.NewPostgresRepository(), enc, bi)
	acc, err := accounts.UpsertOnLogin(ctx, account.LoginInput{
		Provider:       account.ProviderVK,
		ProviderUserID: vkUserID,
		FirstName:      "Пред",
		LastName:       "Существовавший",
		ConsentVersion: "v3",
	})
	if err != nil {
		t.Fatalf("log the pre-existing account in again: %v", err)
	}

	if acc.ID != beforeID {
		t.Fatalf("the returning user became a NEW account: was %s, now %s", beforeID, acc.ID)
	}
	// The login upsert must never touch role or status — an admin who logs in
	// stays an admin, and this is the migration that could have reset it.
	if acc.Role != account.RoleAdmin {
		t.Fatalf("role = %q, want admin — login must not change it", acc.Role)
	}
	if acc.Status != account.StatusApproved {
		t.Fatalf("status = %q, want approved — login must not change it", acc.Status)
	}
	if acc.Provider != account.ProviderVK {
		t.Fatalf("provider = %q, want vk", acc.Provider)
	}

	var rows int
	if err := oldPool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&rows); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if rows != 1 {
		t.Fatalf("account count = %d, want 1 — the returning login inserted a duplicate", rows)
	}

	// The handle shown on the pending screen is derived from the blind index, so
	// it is stable across the migration too. An owner who wrote theirs down
	// before the deploy can still hand it to make-superadmin afterwards.
	var handle string
	if err := oldPool.QueryRow(ctx,
		`SELECT left(encode(identity_ref,'hex'),8) FROM accounts WHERE id = $1::uuid`, beforeID).Scan(&handle); err != nil {
		t.Fatalf("read handle: %v", err)
	}
	if handle != acc.Handle {
		t.Fatalf("handle drifted: database says %s, service says %s", handle, acc.Handle)
	}
}

// TestTheIdentityConstraintIsComposite guards the constraint itself rather than
// its effect, because the effect (a collision) is only visible with two
// providers and this is the cheaper thing to break loudly.
func TestTheIdentityConstraintIsComposite(t *testing.T) {
	var cols []string
	err := pool.QueryRow(context.Background(), `
		SELECT array_agg(a.attname ORDER BY a.attname)
		  FROM pg_constraint c
		  JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY(c.conkey)
		 WHERE c.conrelid = 'accounts'::regclass
		   AND c.contype  = 'u'
		   AND c.conname  = 'accounts_identity_key'`).Scan(&cols)
	if err != nil {
		t.Fatalf("read the identity constraint: %v", err)
	}
	if len(cols) != 2 || cols[0] != "identity_ref" || cols[1] != "provider" {
		t.Fatalf("identity uniqueness is over %v, want (provider, identity_ref)", cols)
	}
}

// migrationsBefore returns the embedded migrations with everything from the
// named prefix onwards removed, so a test can build the schema as it stood
// before a particular change.
func migrationsBefore(t *testing.T, prefix string) fs.FS {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	out := fstest.MapFS{}
	var kept int
	for _, name := range names {
		if name >= prefix {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = &fstest.MapFile{Data: body}
		kept++
	}
	if kept == 0 {
		t.Fatal(fmt.Sprintf("no migrations before %q — the prefix is wrong", prefix))
	}
	return out
}
