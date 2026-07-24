// Command dev-seed creates a local approved account plus an active session and
// prints the session cookie — so you can open the gated /app/* area locally
// WITHOUT the real VK login (which only works against the registered prod
// domain + allowlisted IP).
//
// DEV ONLY. It is a standalone command, never imported by the server binary and
// never deployed. It reuses the real crypto/account/session packages, so the
// account encryption and session hashing always match production exactly (no
// hand-rolled hashing to drift). It refuses to run unless PSYCHOSPACE_ENV=dev.
//
// Keys and DATABASE_URL come from the environment, same as `./dev.sh run`:
//
//	./dev.sh seed                       # superadmin "Локальный Разработчик"
//	./dev.sh seed -role user -name Гость
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/migrations"
)

func main() {
	name := flag.String("name", "Локальный Разработчик", "display name for the seeded account")
	vkID := flag.String("vk-id", "1", "fake VK user id (used for the blind index)")
	role := flag.String("role", account.RoleSuperadmin, "role: user | admin | superadmin")
	flag.Parse()

	switch *role {
	case account.RoleUser, account.RoleAdmin, account.RoleSuperadmin:
	default:
		log.Fatalf("dev-seed: invalid -role %q (want user|admin|superadmin)", *role)
	}

	cfg := config.MustLoad()
	if cfg.Env != "dev" {
		log.Fatalf("dev-seed: refusing to run outside dev (PSYCHOSPACE_ENV=%q)", cfg.Env)
	}
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("dev-seed: db connect: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		log.Fatalf("dev-seed: migrate: %v", err)
	}

	enc, err := crypto.NewEncryptor(cfg.EncKey)
	if err != nil {
		log.Fatalf("dev-seed: encryptor: %v", err)
	}
	bi, err := crypto.NewBlindIndexer(cfg.HMACKey)
	if err != nil {
		log.Fatalf("dev-seed: blind indexer: %v", err)
	}
	accounts := account.NewService(pool, account.NewPostgresRepository(), enc, bi)
	sessions := session.NewManager(pool, cfg.SessionKey, cfg.SessionTTL, cfg.CookieSecure())

	// Upsert an already-approved account (the open-registration path).
	acc, err := accounts.UpsertOnLogin(ctx, account.LoginInput{
		VKUserID:       *vkID,
		FirstName:      *name,
		ConsentVersion: "dev-seed",
		AutoApprove:    true,
	})
	if err != nil {
		log.Fatalf("dev-seed: upsert account: %v", err)
	}
	// The service only ever creates plain users; elevate if asked.
	if *role != account.RoleUser {
		if _, err := pool.Exec(ctx,
			`UPDATE accounts SET role = $1, status = 'approved', updated_at = now() WHERE id = $2::uuid`,
			*role, acc.ID); err != nil {
			log.Fatalf("dev-seed: set role: %v", err)
		}
	}

	raw, err := sessions.Create(ctx, acc.ID)
	if err != nil {
		log.Fatalf("dev-seed: create session: %v", err)
	}

	fmt.Fprintf(os.Stdout, `
✅ Seeded local account
   id:    %s
   name:  %s
   role:  %s (approved)
   vk id: %s

Open http://localhost:5173, then in DevTools → Application → Cookies add this
cookie for the origin you use (localhost:5173 dev server, or localhost:8080):

   name:  %s
   value: %s

…and reload — you land in /app. Or hit the API directly:

   curl -b '%s=%s' http://localhost:8080/api/auth/me

`, acc.ID, acc.DisplayName(), *role, *vkID, session.CookieName, raw, session.CookieName, raw)
}
