# psycho-space — Project Rules (CLAUDE.md)

Self-contained working rules for this repository. Any developer (with or without Claude) should be able to pick up the project from this file alone. This project is standalone and unrelated to any employer.

**Canonical living doc:** `~/Desktop/psycho-space.md` — the design, phased rollout, and the owner's TODO list. Read it first; keep it current as work lands (it opens with an `## LLM Continuation Context` block for fast hand-off). If that path isn't present on your machine, ask the owner for the current living-doc location.

## What this is

A Russian-language landing page + allowlist-gated web app for a small community. The landing is deliberately cringe; login is via **VK ID** only. The app's first section is a **Wishlist with upvotes** — the first of several planned sections (the UI says so). Access is allowlist-gated: the owner is promoted to admin, then approves everyone else; unapproved users are told to ask to be allowlisted. RU region, single environment (prod), under personal-data law (152-ФЗ).

## Stack & layout

- **Backend:** Go 1.26 (via mise) · chi router · pgx/v5 · slog. No ORM, no Redis — all state in PostgreSQL with `expires_at` TTLs.
- **Frontend:** Vue 3 · Vite · TypeScript · Vuetify (Material) · vue-router · pinia. Built and **embedded into the Go binary** (`go:embed internal/web/dist`).
- **Infra:** one Ubuntu 24.04 box · PostgreSQL 16 · nginx (TLS via certbot) · systemd. Deployed over SSH by GitHub Actions.

```
cmd/psycho-space/     entrypoint (config load, DI, migrate, graceful shutdown)
internal/
  config/   env config; base64 32-byte keys, no secret defaults, fail-fast
  crypto/   AES-256-GCM AEAD + HMAC-SHA256 blind index + token helpers
  db/       pgxpool, DBTX interface, embedded-SQL migrator
  logging/  slog JSON → stdout (+ rotated file when LOG_DIR set)
  httpapi/  chi router, middleware, handlers
  session/  server-side opaque sessions (added P2)
  account/  accounts: upsert-by-blind-index, allowlist status/role (P2)
  vk/       VK ID client: ExchangeCode + UserInfo (P2)
  wishlist/ items + votes (P3)
  admin/    approve/block/promote (P3)
  web/      go:embed of the built SPA (+ tracked placeholder index.html)
migrations/ NNN_*.sql, embedded, auto-applied, immutable once shipped
web/        Vue SPA source (P2+)
test/integration/  //go:build integration — testcontainers-go + fake VK server
scripts/    bootstrap.sh (one-time provisioning), make-admin.sh
deploy/     systemd unit, nginx conf
docs/RUNBOOK.md   debugging/ops (ssh, logs, db, nginx, cert, admin bootstrap)
```

## Conventions

**Go / service design**
- Layered: Handler → Service → Repository (pgx) → Postgres. Manual constructor DI in `main.go`; no DI frameworks.
- Each domain package owns its `repository.go` (interface) + `postgres_repository.go` (impl) + `service.go` + `errors.go`.
- Repositories take `db.DBTX` (works with pool or tx). Nullable columns use `*T` (pgx scans natively).
- Per-package error sentinels; compare with `errors.Is`/`errors.As`. **Never** return `err.Error()` to clients — map to a stable code via `writeError(w, status, "code")`.
- Every table has `created_at`/`updated_at`/`deleted_at`; prefer soft delete + `WHERE deleted_at IS NULL`.
- Migrations are **immutable once shipped** — add a new `NNN_*.sql`, never edit an applied one.
- HTTP server always sets Read/Write/Idle timeouts; all endpoints are behind the 1 MiB body limit.

**Security & personal data (152-ФЗ posture)**
- Minimise stored personal data; **encrypt at rest** what we store (AES-256-GCM, per-row nonce; keys from env, base64 32-byte, validated at startup).
- Equality lookups on personal identifiers use the **HMAC-SHA256 blind index**, never plaintext.
- Session tokens are random (`crypto/rand`), stored only as `HMAC-SHA256`; the raw token lives only in an `httpOnly; Secure; SameSite=Lax` cookie.
- All security randomness via `crypto/rand`. Never log personal data or tokens (log the `vk_user_ref` hex if you must correlate).
- **Secrets never enter the repo.** They live only in GitHub Actions `prod` environment secrets and, on the server, in `/etc/psycho-space/app.env` (chmod 600). `.env` is gitignored.
- **No test/dev-only code in production paths** — no test endpoints, mock handlers, or debug backdoors. Tests use real flows or direct DB setup.
- Consent (152-ФЗ) is captured before any PD processing: the VK widget is gated behind an explicit consent checkbox; `consent_at`/`consent_version` are recorded.

**Git & workflow**
- Git identity for this repo: **`SergeyZSpb` / `sergei.s.zobnin@gmail.com`** (set with `git config user.name/user.email`; do not use any other identity).
- Push auth uses the `GITHUB_PSYCHOSPACE_PAT` env var (read-write). Do not persist the token in `.git/config` — push via an inline `https://x-access-token:$TOKEN@github.com/...` URL.
- **Conventional Commits:** `<type>(<scope>): <subject>` — types `feat|fix|refactor|perf|test|docs|chore|build|ci|style|revert`. Imperative, ≤72 chars, no trailing period. Explain the *why* in the body for non-trivial changes.
- **Feature branch → PR** for changes; CI must be green before merge. (Solo maintainer may self-merge once CI passes.) The scaffold/bootstrap commit lands on `main` directly.
- **Pre-commit hook is mandatory and never bypassed** (`--no-verify` is forbidden). It runs `./dev.sh pre-commit` = build → lint → unit → web → integration. `dev.sh` self-heals `core.hooksPath` on every run. If a check fails, fix the cause — never skip.

**Tests are a deliverable**
- Every code-touching change **extends the test base**: unit tests for the changed logic **and**, when applicable, a testcontainers integration test proving the behaviour end-to-end. Running the existing suite green is necessary but not sufficient.
- A behaviour change landing with no test delta is incomplete. Docs/config/mechanical changes may skip tests — state the reason.

**Toolchain**
- Use **mise** for local work: `mise install` once, then `./dev.sh <target>` (dev.sh routes go/npm through mise). Versions are pinned in `mise.toml`.
- `golangci-lint` is recommended but optional locally; the mandatory lint gate is `gofmt` + `go vet`.

## Deploy

- **Provisioning is one-time and manual:** `scripts/bootstrap.sh` (postgres, nginx, certbot, systemd, deploy user + CI key, then SSH hardening — non-standard port + fail2ban). Run it once over the existing root SSH. See the living doc's TODO section.
- **App deploys are CI:** push to `main` → `.github/workflows/deploy.yml` (environment `prod`) builds the SPA + Go binary, ships it over SSH, renders `/etc/psycho-space/app.env` from secrets, migrates, restarts the service, and health-checks `https://psycho-space.ru/healthz`. Watch the run; a red deploy means prod wasn't updated.

## Debugging

See `docs/RUNBOOK.md`: SSH access, service logs (`journalctl` + `/var/log/psycho-space/app.log`), DB queries over SSH (`psql`), nginx/cert checks, and the admin-bootstrap SQL.
