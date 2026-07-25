# psycho-space — Project Rules (CLAUDE.md)

Self-contained working rules for this repository. Any developer (with or without Claude) should be able to pick up the project from this file alone. This project is standalone and unrelated to any employer.

**Canonical living doc:** `~/Desktop/psycho-space/psycho-space.md` — the **root index**: project state, phased rollout, the owner's TODO list, and a link to each topic plan (dated `YYYYMMDD_<slug>.md` files in the same folder). Read it first; keep it current as work lands (every file there opens with an `## LLM Continuation Context` block for fast hand-off). That folder holds everything project-local and uncommitted — the living doc set, the game-art source images in `vanya_assets/`, and the operator's private detail (server host, hardened SSH port), none of which may ever enter this repository. If the folder isn't on your machine, ask the owner.

**In-repo documentation:** [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) (structure, Mermaid) · [`docs/DESIGN.md`](docs/DESIGN.md) (why) · [`docs/RUNBOOK.md`](docs/RUNBOOK.md) (operations & debugging).

## Working with Claude — chat tone

**Chat is terse pidgin; artifacts are proper English.** In session replies (prose back to the user), default to **terse pidgin**: drop articles/copulas, short sentences, lead with the answer, no preamble, no restating the question — optimise for the reader's speed. This applies to **chat only**. The moment text lands in an **artifact** — a source file, commit message, PR description, code comment, the living doc, an `## LLM Continuation Context` block, a ticket — it is **well-formed English** per the conventions below. Keep identifiers, code, paths, commands, and any safety-relevant or conditional statement **verbatim and unambiguous**; pidgin trims the prose around them, never the precision. When a nuance would be lost by dropping a word, keep the word.

## What this is

A Russian-language landing page + allowlist-gated web app for a small community. The landing is deliberately cringe; login is via **VK ID** only. The app's first section is a **Wishlist with upvotes** — the first of several planned sections (the UI says so). Access is allowlist-gated: the owner is promoted to admin, then approves everyone else; unapproved users are told to ask to be allowlisted. RU region, single environment (prod), under personal-data law (152-ФЗ).

## Stack & layout

- **Backend:** Go 1.26 (via mise) · chi router · pgx/v5 · slog. No ORM, no Redis — all state in PostgreSQL with `expires_at` TTLs.
- **Frontend:** Vue 3 · Vite · TypeScript · Vuetify (Material) · vue-router · pinia. Built and **embedded into the Go binary** (`go:embed internal/web/dist`).
- **Infra:** one Ubuntu 24.04 box · PostgreSQL 16 · nginx (TLS via certbot) · systemd. Deployed over SSH by GitHub Actions.

```
cmd/psycho-space/  entrypoint (config load, DI, migrate, graceful shutdown)
cmd/dev-seed/      local approved-account + session seeder (dev only, never deployed)
internal/
  config/    env config; base64 32-byte keys, no secret defaults, fail-fast
  crypto/    AES-256-GCM AEAD + HMAC-SHA256 blind index + token helpers
  db/        pgxpool, DBTX interface, embedded-SQL migrator
  logging/   slog JSON → stdout (+ rotated file when LOG_DIR set)
  observability/  OpenTelemetry tracing (generated always, export opt-in)
  httpapi/   chi router, middleware, auth/wishlist/game/admin handlers
  session/   server-side opaque sessions
  account/   accounts: upsert-by-blind-index, allowlist status + role tier
  vk/        VK ID client (ExchangeCode + UserInfo) + optional id_token verifier
  wishlist/  items, comments, votes (upvote toggle on both)
  game/      LLM-judged dialogue: content/persona, judge, runs, art blobs
  settings/  app_settings key/value (open registration)
  web/       go:embed of the built SPA (dir gitignored except .gitkeep)
migrations/  NNN_*.sql, embedded, auto-applied, immutable once shipped
web/         Vue SPA source (built to internal/web/dist, embedded at compile time)
  e2e/       Playwright: mobile layout, /api stubbed in the browser
  e2e-stack/ Playwright: full-stack, real binary + real Postgres
test/integration/  //go:build integration — testcontainers-go + fake VK server
scripts/     bootstrap.sh, harden-finalize.sh, e2e-stack.sh, ci-test-summary.sh
deploy/      systemd unit, nginx conf, psycho-deploy + make-superadmin helpers
docs/        ARCHITECTURE.md · DESIGN.md · RUNBOOK.md
```

## Architecture — see `docs/ARCHITECTURE.md`

The structural view lives in **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)** (Mermaid, renders on GitHub): the logical container diagram, runtime sequences for login / a gated request / a game turn / the deploy, the package dependency graph, the ER model, and the security view. Read it before changing anything structural — and **update it in the same change** when you add a domain package, a route group, a table, or a runtime flow.

The reasoning behind those choices — why sessions are server-side, why the SPA is embedded, why there are two Playwright suites — is in **[`docs/DESIGN.md`](docs/DESIGN.md)**. Operational procedure is in **[`docs/RUNBOOK.md`](docs/RUNBOOK.md)**.

One-paragraph orientation, so this file stands alone: a browser hits nginx (TLS, security headers), which proxies to a single Go binary on `127.0.0.1:8080` serving both the embedded Vue SPA and `/api`; the binary talks to a local PostgreSQL through pgx. Login is VK ID with the code exchanged on the backend; access is allowlist-gated (`pending` → `approved` by an admin); every non-2xx returns `{error, trace_id}` and sets `X-Trace-Id`.

**Adding a feature:** new package under `internal/<domain>/` (`repository.go` interface + `postgres_repository.go` + `service.go` + `errors.go`), a `NNN_*.sql` migration, wire it into `main.go` DI + `httpapi.Deps` + routes, extend `test/integration/`, and update `docs/ARCHITECTURE.md`.

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
- **Push directly to `main`** (single maintainer). `main` auto-deploys, so every push goes to prod — the mandatory pre-commit gate + the deploy job's full test suite are the safety net. Feature branches + PRs are optional (use one only when you want to stage/review something before it deploys).
- **Pre-commit hook is mandatory and never bypassed** (`--no-verify` is forbidden). It runs `./dev.sh pre-commit` = build → lint → unit → web → **e2e** → integration → **full-stack e2e**. `dev.sh` self-heals `core.hooksPath` on every run. If a check fails, fix the cause — never skip. Docker must be running (the integration and full-stack suites need it).

**Tests are a deliverable**
- Every code-touching change **extends the test base**: unit tests for the changed logic **and**, when applicable, an integration or e2e test proving the behaviour end-to-end. Running the existing suite green is necessary but not sufficient.
- A behaviour change landing with no test delta is incomplete. Docs/config/mechanical changes may skip tests — state the reason.
- **Four suites, four jobs.** Go unit (`./dev.sh test`) · testcontainers integration, Go-level with a fake VK server (`./dev.sh integration`) · Playwright mobile-layout with `/api` stubbed in the browser (`./dev.sh e2e`) · Playwright full-stack against the real binary and a real Postgres (`./dev.sh e2e-stack`). Put a test where it will fail for the right reason: layout regressions in the stubbed suite, "did it actually persist" in the full-stack one.
- `./dev.sh cover` reports coverage for all of it; CI writes the same table plus pass/fail counts into the run's job summary.

**Frontend (SPA)**
- **Mobile-first & responsive — mandatory.** The site must be fully usable on phones (target ≈360 px wide) as well as desktop. Use Vuetify's responsive grid + breakpoints (`v-container`/`v-row`/`v-col`, `d-*` display utilities, `useDisplay()`), fluid layouts, and a mobile nav pattern (drawer / bottom nav) — never fixed pixel widths that overflow small screens. Keep the `viewport` meta in `index.html`.
- Touch targets ≥ 44 px; no hover-only affordances (tap + keyboard must both work).
- **Verify at mobile width before shipping any UI change** — and it is enforced, not just asked for: `./dev.sh e2e` runs the Playwright suite at 360/390/768 px in the pre-commit gate and fails on horizontal overflow or a sub-44 px tap target. A change that only looks right on desktop is incomplete.
- Dark/light theme both supported; RU-only copy.

**Toolchain**
- Use **mise** for local work: `mise install` once, then `./dev.sh <target>` (dev.sh routes go/npm/npx/golangci-lint through mise). Versions are pinned in `mise.toml` — Go, Node, **and `golangci-lint`**.
- The lint gate is `gofmt` + `go vet` + **`golangci-lint` (mandatory, not optional)**. `./dev.sh lint` fails if the linter is missing rather than skipping it — a finding must not be invisible on one machine and blocking on another. Suppress a false positive with a `//nolint:<linter> // <why>` comment that states the reason; never by weakening the config.
- **`gh` is the GitHub tool** — run status checks, watch runs, read logs with it (`gh run list`, `gh run watch`, `gh run view --log-failed`). Authenticate by environment, not by `gh auth login`: `gh` reads **`GH_TOKEN`**, so export it from the PAT you already have (`export GH_TOKEN="$GITHUB_PSYCHOSPACE_PAT"` next to that variable's definition). `gh auth login --with-token` would write a second copy of the token to `~/.config/gh/hosts.yml` — don't. `git push` stays on the inline-URL form below.

## Environment & secrets — what you need to work on this

Nothing secret is in the repository, so a fresh clone cannot run the whole thing on its own. This is the complete list of what has to exist in your environment, and where each thing comes from. Ask the owner for anything you are missing rather than inventing a value.

**Local shell (per developer)**

| Variable | What it is | Needed for |
|---|---|---|
| `GITHUB_PSYCHOSPACE_PAT` | GitHub personal access token with write access to the repo | pushing (inline-URL form), and `gh` |
| `GH_TOKEN` | set to the same value — `export GH_TOKEN="$GITHUB_PSYCHOSPACE_PAT"` | every `gh` command; avoids storing a second copy on disk |

**Local `.env`** (copy `.env.example`, never committed — `.env` is gitignored). `./dev.sh run` and `./dev.sh seed` source it.

- `PSYCHOSPACE_ENV=dev`, `PSYCHOSPACE_HTTP_ADDR`, `PSYCHOSPACE_BASE_URL`, `PSYCHOSPACE_DATABASE_URL` — point at the compose Postgres (`./dev.sh db-up`).
- `PSYCHOSPACE_ENC_KEY`, `PSYCHOSPACE_HMAC_KEY`, `PSYCHOSPACE_SESSION_KEY` — three **different** base64 32-byte values (`openssl rand -base64 32`). Startup fails fast if any is missing or the wrong length; there are no defaults on purpose. Local values are throwaway — they are not the production keys and must never be.
- `PSYCHOSPACE_VK_*` — optional locally. VK ID is IP-allowlisted to the production host and its redirect URI is the production domain, so **the real login cannot run on a workstation**. Use `./dev.sh seed` instead; it mints an approved account and prints its session cookie.
- `PSYCHOSPACE_LLM_BASE_URL` / `_API_KEY` / `_MODEL` — needed only to play the game locally. **Every turn costs real money**, so leave them blank unless you are working on the game; unset, `/api/game/attempt` answers 503 and everything else works.
- `PSYCHOSPACE_LOG_DIR`, `PSYCHOSPACE_SESSION_TTL`, `PSYCHOSPACE_OTLP_ENDPOINT` — optional.

The full-stack e2e suite needs none of this: `scripts/e2e-stack.sh` generates throwaway keys per run and starts its own database.

**GitHub Actions `prod` environment secrets** (Settings → Environments → prod). The deploy workflow reads these names verbatim and renders `/etc/psycho-space/app.env` from them:

`DEPLOY_SSH_KEY` · `DEPLOY_SSH_HOST` · `DEPLOY_SSH_PORT` · `DEPLOY_SSH_USER` · `POSTGRES_PASSWORD` · `APP_ENC_KEY` · `APP_HMAC_KEY` · `APP_SESSION_KEY` · `VK_APP_ID` · `VK_SERVICE_TOKEN` · `VK_REDIRECT_URI` · `LLM_BASE_URL` · `LLM_API_KEY` · `LLM_MODEL` · optional `VK_VERIFY_IDTOKEN` / `VK_JWKS_URL` / `VK_ISSUER`.

**Handle with care.** `APP_ENC_KEY` and `APP_HMAC_KEY` are not rotatable in place: losing the encryption key makes stored profiles unrecoverable, and changing the HMAC key breaks every blind index, which orphans every account. The server host and hardened SSH port are secret too — they live in these secrets, in the operator's `~/.ssh/config`, and in the local living doc, and must never appear in the repository.

## Task workflow

For each work item:
1. **Ground it** — read the living doc + this file. For anything non-trivial, write or refresh the plan in the living doc *before* coding, and keep its `## LLM Continuation Context` block (`status`/`next`/`done`) accurate.
2. **Branch** — `<type>-short-slug` off an up-to-date `main`; implement in small, reviewable slices.
3. **Extend the test base** — unit tests for the changed logic **and** a testcontainers integration test when there's an end-to-end path (see *Tests are a deliverable*).
4. **Gate** — `./dev.sh pre-commit` must pass (build → lint → unit → web → e2e → integration → full-stack e2e). Never `--no-verify`; fix the cause.
5. **Commit + push to `main`** — Conventional Commits. This deploys, so only push a green, verified change.
6. **Watch the deploy to completion — don't fire-and-forget.** Pushing to `main` triggers `deploy.yml` (runs the full suite, then ships over SSH). Watch that run to green, then verify (health check / the behaviour you changed). A red deploy means prod is stale — treat it as unfinished work.
7. **Write back — the docs are part of the change, not a follow-up.** In the same commit: `docs/ARCHITECTURE.md` if you touched the structure (a package, a route group, a table, a runtime flow); `docs/DESIGN.md` if you made a decision whose reasoning is not recoverable from the diff; `docs/RUNBOOK.md` if you worked out an operational or debugging procedure, or if you changed behaviour it describes; this file if a convention changed; and the living doc for durable project state. Each doc's `## LLM Continuation Context` block is updated with it — a stale block is worse than none. Docs that contradict the code are a defect owned by the change that caused them.

**CI vs deploy:** `main` = `deploy.yml` (lint · unit · web · e2e · integration · full-stack e2e, then auto-deploy over SSH) — the normal path. Both workflows publish a test + coverage summary to the run's job summary and upload the Playwright videos. Any non-`main` branch/PR = `ci.yml` (same tests, no deploy) — only if you deliberately want to stage something before it deploys.

## Completion protocol (Definition of Done)

Close a work item with a compact checklist — mark each **✅ done · ⏭️ skipped (+ why) · ➖ n/a**, and only ✅ what you actually verified:

| Gate | Status |
|------|--------|
| Requirements grounded (living doc read) | |
| Test base extended — unit + integration (or stated reason) | |
| `./dev.sh pre-commit` green | |
| Pushed to `main` → **auto-deploy watched to green + verified** *(or noted as an owner action)* | |
| Docs synced — `ARCHITECTURE.md` / `DESIGN.md` / `RUNBOOK.md` as applicable, each with its continuation block | |
| Living doc current to as-built; LLM-continuation block updated | |
| Secrets/PII posture respected — nothing sensitive committed | |

Then give a short **end-of-task report**: what shipped (behavioural bullets) · tests added/extended (named) · areas/repos touched · push status (which ref — branch vs `main`) · judgement calls made without explicit direction.

## Deploy

- **Provisioning is one-time and manual:** `scripts/bootstrap.sh` (postgres, nginx, certbot, systemd, deploy user + CI key, then SSH hardening — non-standard port + fail2ban). Run it once over the existing root SSH. See the living doc's TODO section.
- **App deploys are CI:** push to `main` → `.github/workflows/deploy.yml` (environment `prod`) builds the SPA + Go binary, ships it over SSH, renders `/etc/psycho-space/app.env` from secrets, migrates, restarts the service, and health-checks `https://psycho-space.ru/healthz`. Watch the run; a red deploy means prod wasn't updated.

## Debugging

See `docs/RUNBOOK.md`: SSH access, service logs (`journalctl` + `/var/log/psycho-space/app.log`), DB queries over SSH (`psql`), nginx/cert checks, and the admin-bootstrap SQL.
