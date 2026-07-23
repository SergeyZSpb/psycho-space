# psycho-space — Project Rules (CLAUDE.md)

Self-contained working rules for this repository. Any developer (with or without Claude) should be able to pick up the project from this file alone. This project is standalone and unrelated to any employer.

**Canonical living doc:** `~/Desktop/psycho-space.md` — the design, phased rollout, and the owner's TODO list. Read it first; keep it current as work lands (it opens with an `## LLM Continuation Context` block for fast hand-off). If that path isn't present on your machine, ask the owner for the current living-doc location.

## Working with Claude — chat tone

**Chat is terse pidgin; artifacts are proper English.** In session replies (prose back to the user), default to **terse pidgin**: drop articles/copulas, short sentences, lead with the answer, no preamble, no restating the question — optimise for the reader's speed. This applies to **chat only**. The moment text lands in an **artifact** — a source file, commit message, PR description, code comment, the living doc, an `## LLM Continuation Context` block, a ticket — it is **well-formed English** per the conventions below. Keep identifiers, code, paths, commands, and any safety-relevant or conditional statement **verbatim and unambiguous**; pidgin trims the prose around them, never the precision. When a nuance would be lost by dropping a word, keep the word.

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
  session/  server-side opaque sessions
  account/  accounts: upsert-by-blind-index, allowlist status + role tier
  vk/       VK ID client: ExchangeCode + UserInfo
  wishlist/ items + votes (upvote toggle)
  observability/  OpenTelemetry tracing (generated always, export opt-in)
  httpapi/  chi router, middleware, auth/wishlist/admin handlers
  web/      go:embed of the built SPA (dir gitignored except .gitkeep; Go serves a fallback until built)
migrations/ NNN_*.sql, embedded, auto-applied, immutable once shipped
web/        Vue SPA source (built to internal/web/dist, embedded at compile time)
test/integration/  //go:build integration — testcontainers-go + fake VK server
scripts/    bootstrap.sh + harden-finalize.sh (one-time provisioning)
deploy/     systemd unit, nginx conf, psycho-deploy + make-superadmin helpers
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

**Go engineering standards (always apply)**
- Idiomatic Go; `gofmt` + `go vet` clean; small, focused packages with a minimal, documented exported surface (doc comment on every exported identifier).
- `context.Context` is the first arg of anything doing I/O; honour cancellation/deadlines. No `context.Background()` deep in call stacks — thread the request context.
- Wrap errors with `%w` and context; compare sentinels via `errors.Is`, extract typed via `errors.As`. Don't log **and** return the same error (pick one owner). Never leak `err.Error()` to clients.
- Construct dependencies explicitly and inject them; no global mutable state, no `init()` side effects.
- Concurrency: guard shared state, prefer channels / `sync` primitives, and never leak goroutines — every goroutine exits on ctx cancel.
- `crypto/rand` for anything security-sensitive; never `math/rand`.
- Tests are table-driven where it helps; helpers call `t.Helper()`; synchronise on conditions, never `time.Sleep`.

**Security & personal data (152-ФЗ posture)**
- Minimise stored personal data; **encrypt at rest** what we store (AES-256-GCM, per-row nonce; keys from env, base64 32-byte, validated at startup).
- Equality lookups on personal identifiers use the **HMAC-SHA256 blind index**, never plaintext.
- Session tokens are random (`crypto/rand`), stored only as `HMAC-SHA256`; the raw token lives only in an `httpOnly; Secure; SameSite=Lax` cookie.
- All security randomness via `crypto/rand`. Never log personal data or tokens (log the `vk_user_ref` hex if you must correlate).
- **Secrets never enter the repo.** They live only in GitHub Actions `prod` environment secrets and, on the server, in `/etc/psycho-space/app.env` (chmod 600). `.env` is gitignored.
- **No test/dev-only code in production paths** — no test endpoints, mock handlers, or debug backdoors. Tests use real flows or direct DB setup.
- Consent (152-ФЗ) is captured before any PD processing: the VK widget is gated behind an explicit consent checkbox; `consent_at`/`consent_version` are recorded.

**Git & workflow**
- Set a git identity appropriate to you before committing (`git config user.name/user.email`).
- Push over HTTPS with a personal access token; don't persist the token in `.git/config` — push via an inline `https://x-access-token:$TOKEN@github.com/...` URL.
- **Conventional Commits:** `<type>(<scope>): <subject>` — types `feat|fix|refactor|perf|test|docs|chore|build|ci|style|revert`. Imperative, ≤72 chars, no trailing period. Explain the *why* in the body for non-trivial changes.
- **Feature branch → PR** for changes; CI must be green before merge. (Solo maintainer may self-merge once CI passes.) The scaffold/bootstrap commit lands on `main` directly.
- **Pre-commit hook is mandatory and never bypassed** (`--no-verify` is forbidden). It runs `./dev.sh pre-commit` = build → lint → unit → web → integration. `dev.sh` self-heals `core.hooksPath` on every run. If a check fails, fix the cause — never skip.

**Tests are a deliverable**
- Every code-touching change **extends the test base**: unit tests for the changed logic **and**, when applicable, a testcontainers integration test proving the behaviour end-to-end. Running the existing suite green is necessary but not sufficient.
- A behaviour change landing with no test delta is incomplete. Docs/config/mechanical changes may skip tests — state the reason.

**Toolchain**
- Use **mise** for local work: `mise install` once, then `./dev.sh <target>` (dev.sh routes go/npm through mise). Versions are pinned in `mise.toml`.
- `golangci-lint` is recommended but optional locally; the mandatory lint gate is `gofmt` + `go vet`.

## Task workflow

For each work item:
1. **Ground it** — read the living doc + this file. For anything non-trivial, write or refresh the plan in the living doc *before* coding, and keep its `## LLM Continuation Context` block (`status`/`next`/`done`) accurate.
2. **Branch** — `<type>-short-slug` off an up-to-date `main`; implement in small, reviewable slices.
3. **Extend the test base** — unit tests for the changed logic **and** a testcontainers integration test when there's an end-to-end path (see *Tests are a deliverable*).
4. **Gate** — `./dev.sh pre-commit` must pass (build → lint → unit → web → integration). Never `--no-verify`; fix the cause.
5. **Commit + push** — Conventional Commits. Open a PR for review-gated changes; scaffold/docs may land on `main` directly.
6. **Watch CI to completion — don't fire-and-forget.** A **feature/PR branch** runs `ci.yml` (tests only, no deploy) — watch it to green and fix any red job at the root before merging. When the change lands on **`main`**, it **auto-deploys** (`deploy.yml`) — watch that run to green too, then verify the deploy (health check / the behaviour you changed). A red deploy means prod is stale; treat it as unfinished work.
7. **Write back** — fold durable decisions into the living doc; update this file if a convention changed.

**Branch → CI → deploy model:** feature/PR branches = `ci.yml` (build · lint · unit · web · integration), **no deploy**. `main` = `deploy.yml` (same tests, then auto-deploy over SSH). So merging to `main` *is* the deploy trigger — only merge a green, verified change.

## Completion protocol (Definition of Done)

Close a work item with a compact checklist — mark each **✅ done · ⏭️ skipped (+ why) · ➖ n/a**, and only ✅ what you actually verified:

| Gate | Status |
|------|--------|
| Requirements grounded (living doc read) | |
| Test base extended — unit + integration (or stated reason) | |
| `./dev.sh pre-commit` green · **PR CI watched to green** | |
| Merged to `main` → **auto-deploy watched to green + verified** *(or noted as an owner action)* | |
| Living doc current to as-built; LLM-continuation block updated | |
| Secrets/PII posture respected — nothing sensitive committed | |

Then give a short **end-of-task report**: what shipped (behavioural bullets) · tests added/extended (named) · areas/repos touched · push status (which ref — branch vs `main`) · judgement calls made without explicit direction.

## Deploy

- **Provisioning is one-time and manual:** `scripts/bootstrap.sh` (postgres, nginx, certbot, systemd, deploy user + CI key, then SSH hardening — non-standard port + fail2ban). Run it once over the existing root SSH. See the living doc's TODO section.
- **App deploys are CI:** push to `main` → `.github/workflows/deploy.yml` (environment `prod`) builds the SPA + Go binary, ships it over SSH, renders `/etc/psycho-space/app.env` from secrets, migrates, restarts the service, and health-checks `https://psycho-space.ru/healthz`. Watch the run; a red deploy means prod wasn't updated.

## Debugging

See `docs/RUNBOOK.md`: SSH access, service logs (`journalctl` + `/var/log/psycho-space/app.log`), DB queries over SSH (`psql`), nginx/cert checks, and the admin-bootstrap SQL.
