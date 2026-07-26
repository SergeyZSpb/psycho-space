# psycho-space — Project Rules (CLAUDE.md)

Working rules for this repository — the *what*. The reasoning behind the shape of the system lives in `docs/ARCHITECTURE.md` (§1–7 the structure, §8 the numbered decision records), and this file points there rather than restating it, so a rule and its rationale cannot drift apart. This project is standalone and unrelated to any employer.

**Canonical living doc:** `~/Desktop/psycho-space/psycho-space.md` — the **root index**: project state, phased rollout, the owner's TODO list, and a link to each topic plan (dated `YYYYMMDD_<slug>.md` files in the same folder). Read it first; keep it current as work lands (every file there opens with an `## LLM Continuation Context` block for fast hand-off). That folder holds everything project-local and uncommitted — the living doc set, the game-art source images in `vanya_assets/`, and the operator's private detail (server host, hardened SSH port), none of which may ever enter this repository. If the folder isn't on your machine, ask the owner.

**In-repo documentation:** [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) (structure + numbered decision records, Mermaid) · [`docs/RUNBOOK.md`](docs/RUNBOOK.md) (operations & debugging).

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
  realtime/  WebSocket hub + per-connection pumps, game-agnostic (no game may be named here)
  gameassets/  the shared art blob store — infrastructure, serves every game
  session/   server-side opaque sessions
  account/   accounts: upsert-by-blind-index, allowlist status + role tier
  vk/        VK ID client (ExchangeCode + UserInfo) + optional id_token verifier
  wishlist/  items, comments, votes (upvote toggle on both)
  gamekhimki/  «Смолтолк в Химках» — LLM-judged dialogue: content/persona, judge, runs, art blobs
  gamevanyagotchi/  «Ванягоччи» — the shared plane (in memory) + the pet (Postgres):
                    content.go  the catalogue — stats, actions, skins, NPCs, every constant
                    decay.go    time arithmetic for stats · motion.go the same for space
                    display.go  the in-memory cache the 5 Hz broadcast draws from
                    message.go  the wire types · service.go the verbs and the tick
  settings/  app_settings key/value (open registration)
  web/       go:embed of the built SPA (dir gitignored except .gitkeep)
migrations/  NNN_*.sql, embedded, auto-applied, immutable once shipped
web/         Vue SPA source (built to internal/web/dist, embedded at compile time)
  src/realtime/  module-scoped WebSocket client + reconnect policy (refcounted)
  src/lib/       pure per-feature logic (vanyagotchiPlane/Pet, gameKhimki*) — unit-tested
  e2e/       Playwright: mobile layout, /api stubbed in the browser
  e2e-stack/ Playwright: full-stack, real binary + real Postgres
test/integration/  //go:build integration — testcontainers-go + fake VK server
scripts/     bootstrap.sh, harden-finalize.sh, e2e-stack.sh, ci-test-summary.sh
deploy/      systemd unit, nginx conf, psycho-deploy + make-superadmin helpers
docs/        ARCHITECTURE.md · RUNBOOK.md
```

## Architecture — see `docs/ARCHITECTURE.md`

The structural view lives in **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)** (Mermaid, renders on GitHub): the logical container diagram, runtime sequences for login / a gated request / a game turn / the deploy, the package dependency graph, the ER model, the security view, and — in §8 — the numbered decision records. Read it before changing anything structural — and **update it in the same change** when you add a domain package, a route group, a table, or a runtime flow.

The reasoning behind those choices — why sessions are server-side, why the SPA is embedded, why there are two Playwright suites — is in **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) §8**, as numbered decision records. **Records are immutable:** a decision that is revisited gets a **new** record that supersedes or amends the old one, never an edit to what the old one decided or why. Operational procedure is in **[`docs/RUNBOOK.md`](docs/RUNBOOK.md)**.

**The bar for a record is architecture, and it is high.** A record is for a decision that shapes the *system* — how it deploys, how it stores and protects data, where a boundary between components falls, what a whole class of future change will cost. A tuning constant, a UI behaviour, an animation, a test-harness fix: **not a record**, however subtle the reasoning. That reasoning goes in a comment next to the code it governs, where the person changing it will actually read it. When in doubt, write the comment and leave the log alone — a log padded with small things stops being read, which costs more than a missing entry. Records withdrawn for failing this bar leave a **permanent gap** in the numbering; numbers are never reused, so references never shift.

One-paragraph orientation, so this file stands alone: a browser hits nginx (TLS, security headers), which proxies to a single Go binary on `127.0.0.1:8080` serving both the embedded Vue SPA and `/api`; the binary talks to a local PostgreSQL through pgx. Login is VK ID with the code exchanged on the backend; access is allowlist-gated (`pending` → `approved` by an admin); every non-2xx returns `{error, trace_id}` and sets `X-Trace-Id`.

**Adding a feature:** new package under `internal/<domain>/` (`repository.go` interface + `postgres_repository.go` + `service.go` + `errors.go`), a `NNN_*.sql` migration, wire it into `main.go` DI + `httpapi.Deps` + routes, extend `test/integration/`, and update `docs/ARCHITECTURE.md`.

### Games are self-contained modules

Each game is its own module: its own package, tables, routes and views. **No game shares DB or service code with another, and duplication between games is deliberate rather than debt.** The boundary test: *deleting a game must be removing its package, its migration, its routes and its views — and nothing else.*

**Naming — every game module is `Game<Name>`, at every layer:**

| Game | Package | Tables | Routes | View at |
|---|---|---|---|---|
| «Смолтолк в Химках» | `internal/gamekhimki/` | `game_khimki_runs` | `/api/game-khimki/*` | `GameKhimkiView.vue` at `/app/game-khimki` |
| «Ванягоччи» | `internal/gamevanyagotchi/` | `game_vanyagotchi_*` | `/api/game-vanyagotchi/*` | `GameVanyagotchiView.vue` at `/app/game-vanyagotchi` |

- **Shared infrastructure is never prefixed** — `realtime`, `gameassets`, `session`, `account`, `crypto`, `db`, `logging`, `observability`, `httpapi`. A game may depend on these; none of them may know a game exists.
- **Inside a game's own package, types keep plain names** (`gamekhimki.Service`, never `gamekhimki.GameKhimkiService` — the linter rejects the stutter).
- **`wishlist` and `settings` are non-game sections** — neither games nor infrastructure. Unprefixed; this rule does not reach them.
- **Where the line falls:** does it encode a rule of *this* game, or is it a capability any game would want? Rules are per-game (runs, scores, pets, tuning constants); capabilities are shared (the art blob store, the realtime transport).
- **`game_key` column *values* are data, not names** — they do not move with a rename.

**Reasoning for all of the above lives in `docs/ARCHITECTURE.md` §8 — [ADR-028](docs/ARCHITECTURE.md) (self-contained modules), ADR-030 (the naming convention), ADR-031 (why the asset store is shared).** Read those before arguing with this rule; they are settled and are not to be relitigated by editing them.

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

**Never print a secret in CI — the logs are public**

This repository is public, and so is every Actions log and job summary. GitHub masks the literal value of a registered secret as `***`, but that is the only thing it can do, and it is easy to defeat by accident:

- **A transformed secret is not masked.** Base64-encode it, embed it in a URL, hash it, print it a character at a time, or pass it through a tool that reformats it, and the mask no longer matches. `echo "$KEY" | base64` prints the key.
- **`set -x` prints commands after expansion.** Never enable shell tracing in a step that touches a secret.
- **Whole-environment dumps leak everything** — no `env`, `printenv`, `set`, or "debug: print all inputs" steps.
- **A file written from secrets must not be echoed.** `deploy.yml` renders `app.env` with `umask 077` and never `cat`s it; keep it that way.
- **Third-party actions see what you pass them.** Pass a secret only to a step that genuinely needs it.
- **Error paths leak too** — a tool that echoes its own arguments on failure will print the credential it was given. Prefer passing secrets by environment variable, never as a command-line argument (they also show up in `ps`).

Every task that touches CI runs `./scripts/ci-check-secrets.sh` against the run (see *Task workflow* step 6). It flags credential-shaped strings and deliberately **never prints the match** — printing it to "check" would copy the secret somewhere new. If it fires for real: rotate the value, delete the run's logs, and fix what printed it. Note that `APP_ENC_KEY` and `APP_HMAC_KEY` **cannot** be rotated without data loss, so they are the ones to be most careful with.

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
- `PSYCHOSPACE_LLM_BASE_URL` / `_API_KEY` / `_MODEL` — needed only to play the game locally. **Every turn costs real money**, so leave them blank unless you are working on the game; unset, `/api/game-khimki/attempt` answers 503 and everything else works.
- `PSYCHOSPACE_LOG_DIR`, `PSYCHOSPACE_SESSION_TTL`, `PSYCHOSPACE_OTLP_ENDPOINT` — optional.

The full-stack e2e suite needs none of this: `scripts/e2e-stack.sh` generates throwaway keys per run and starts its own database.

**GitHub Actions `prod` environment secrets** (Settings → Environments → prod). The deploy workflow reads these names verbatim and renders `/etc/psycho-space/app.env` from them:

`DEPLOY_SSH_KEY` · `DEPLOY_SSH_HOST` · `DEPLOY_SSH_PORT` · `DEPLOY_SSH_USER` · `POSTGRES_PASSWORD` · `APP_ENC_KEY` · `APP_HMAC_KEY` · `APP_SESSION_KEY` · `VK_APP_ID` · `VK_SERVICE_TOKEN` · `VK_REDIRECT_URI` · `LLM_BASE_URL` · `LLM_API_KEY` · `LLM_MODEL` · optional `VK_VERIFY_IDTOKEN` / `VK_JWKS_URL` / `VK_ISSUER`.

**Handle with care.** `APP_ENC_KEY` and `APP_HMAC_KEY` are not rotatable in place: losing the encryption key makes stored profiles unrecoverable, and changing the HMAC key breaks every blind index, which orphans every account. The server host and hardened SSH port are secret too — they live in these secrets, in the operator's `~/.ssh/config`, and in the local living doc, and must never appear in the repository.

## Every doc opens with an LLM-continuation block

**Every documentation file in this repo starts with an `## LLM Continuation Context` block**, directly under the H1. Its only job is to let the *next agent* (or the owner, six weeks later) resume the topic without re-deriving it. It is written **for machines, not humans** — optimise for hand-off, not readability; that it renders on the page too is an accepted cost, not a reason to soften it into prose.

- **New docs: mandatory.** No doc is created without one.
- **Existing docs: add on touch.** Editing an older doc that lacks one? Add it in that edit. Don't retrofit docs you aren't otherwise touching.
- **Keep it current.** Update it in the same commit that changes the doc, so `status` / `next` / `done` never lie. **A stale block is worse than none** — it will be believed.
- **Scope:** every markdown doc in this repo (`docs/*`, `README.md` where useful) and the owner's local living-doc set.

Canonical shape:

```markdown
## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** <one line — what this doc or work item is>
- **status:** <where it stands: shipped, phase 2 of 3, blocked on …>
- **code:** <path:line entry points — the ground truth>
- **relocate:** <greps or search terms to re-find that ground truth if the links rot>
- **done:** <what is complete>
- **next:** <the self-prompt: concrete next actions>
- **decisions / constraints:** <fixed decisions a continuing agent must NOT relitigate>
```

The fields are a **checklist, not a straitjacket** — drop one that is genuinely empty, add one the topic needs (`related:`, `env:`, `risks:`). The test is: *could a fresh agent, given only this block, pick up the thread and know where to look?* If not, it is too thin. **Never put secrets or personal data in one** — these render publicly.

**Durable knowledge belongs in a doc, not in an assistant's private memory.** A memory file lives on one machine, unversioned and invisible to everyone else. A doc is shared, reviewed, and version-controlled — and already opens with the block that serves the "let the next agent resume" purpose. If nothing owns a piece of knowledge yet, the missing doc is the fix.

## Working style — autonomy, subagents, and session goals

**Default to executing, not consulting.** When given a task, start: read the code, make the change, verify it, push it. Do not ask questions the codebase already answers, and do not pause for sign-off between obvious steps. Ask once, and keep going, when an answer genuinely changes what gets built. Switch to plan-or-review mode only when explicitly asked ("make a plan", "review this", "what's your approach first").

**Fan out subagents wherever the work splits.** Before starting anything non-trivial, decompose it into independent streams and dispatch them **in a single message so they run concurrently** — sequential subagent calls throw away the entire benefit. Each gets a self-contained prompt: they share no context with the session or with each other. Useful splits:

- **Read-many:** one agent per unrelated area of the codebase when a task needs several areas understood at once.
- **Interface-first:** define the contract (small, fast), then fan out tests, implementation, and callers against it.
- **Research-many:** one agent per open question (library choice, prior art, an external API's real behaviour).
- **Per-surface:** the same shape of change across several packages, views, or endpoints.

Tell subagents **not to edit files the session is editing** — parallel writes to one file lose work. Prefer having them report findings and letting the session apply the edits, unless the streams touch genuinely disjoint files.

**Propose a session goal before a large task.** For anything multi-phase or long-running, suggest the user set one with `/goal <text>`, and offer concrete text rather than just the suggestion — a standing goal re-grounds the work each turn and survives context compaction, which is what keeps a long autonomous run on track. Bundle three things into the proposed text: the objective and what "done" means here; a reminder to close every item against the Definition of Done below; and an instruction to fan out subagents for independent streams. Propose it, let the user edit or decline, then proceed either way — the goal helps continuity, it is not a gate.

**Report progress, not deliberation.** Brief status as work lands ("rate-limit fix in, dispatching docs + CI in parallel") — not a running commentary on your reasoning.

## Iterations — break large work into deployable, verifiable slices

Any work item bigger than a single commit is planned as a **sequence of iterations, each one independently deployable and independently verifiable**. This is not a scheduling preference, it is how a change gets proven on the only environment that matters: `main` auto-deploys, so an iteration that cannot ship on its own is an iteration whose behaviour nobody can observe in production.

**What qualifies as an iteration:**

- **It deploys on its own.** It leaves `main` green and production working. A slice that only makes sense once the next two land is not an iteration — it is the first third of one, and it belongs merged into the slice that completes it.
- **It is verifiable from outside the code.** There is something concrete a human or CI can run to see the new behaviour: a green test in the suite where that behaviour would actually break (see *Tests are a deliverable*), a `curl` recipe, a screen to open at 360 px, an observable log line or metric. "It compiles" and "the types line up" are not verification.
- **It carries its own tests and its own doc write-back.** Every iteration closes against the Definition of Done, not just the final one. Deferring tests or docs to a later slice defeats the purpose, because the slice has already deployed.
- **It is small enough to review in one sitting** — typically one to three commits. Consistently larger usually means the slice is doing two things.

**Structure the sequence as a walking skeleton, not as layers.** The first iteration is a thin end-to-end slice that touches every layer the finished feature will touch — migration, service, API, client — while doing the simplest possible thing. Integration risk (schema shape, auth, proxy config, the never-scroll layout) then surfaces on day one instead of at the end. Each later iteration adds one increment of real behaviour to a skeleton that never stops walking: the second entity, a real validation rule replacing a hardcoded value, the next branch of the happy path. If the first iteration contains nothing frightening, it is probably too thin to be worth deploying.

**Write the sequence down before starting.** The living doc names the iterations, the demoable deliverable for each, and the commit slicing — so progress is measured in shipped, verified increments rather than in accumulated work-in-progress. Keep that list current as slices land; a plan that still describes an iteration which already shipped is a stale plan.

## Task workflow

Applies to each work item — and separately to **each iteration** of a larger one (see above):
1. **Ground it — read `docs/ARCHITECTURE.md` before writing code, not after.** Both altitudes: §1–7 for the structure you are about to change (the package layout, the runtime flow, the ER model, the API map), and **§8 for the decision records that already govern it.** A decision recorded in §8 is settled: build on it, and if you believe it is wrong, say so and get a new record — never quietly implement against it, and never edit the existing one. This step is what stops a change from re-deriving, contradicting, or silently reversing a decision that was already paid for; several §8 records exist because something was learned the expensive way. Then read the living doc + this file, and for anything non-trivial write or refresh the plan in the living doc *before* coding, keeping its `## LLM Continuation Context` block (`status`/`next`/`done`) accurate.
2. **Branch** — `<type>-short-slug` off an up-to-date `main`; implement in small, reviewable slices.
3. **Extend the test base** — unit tests for the changed logic **and** a testcontainers integration test when there's an end-to-end path (see *Tests are a deliverable*).
4. **Gate** — `./dev.sh pre-commit` must pass (build → lint → unit → web → e2e → integration → full-stack e2e). Never `--no-verify`; fix the cause.
5. **Commit + push to `main`** — Conventional Commits. This deploys, so only push a green, verified change.
6. **Watch the deploy to completion, and read what it produced — never fire-and-forget.** Pushing to `main` triggers `deploy.yml` (full suite, then ships over SSH). All four steps are required before the task is done:
   - **Watch it to a conclusion:** `gh run watch <run-id> --exit-status` (find it with `gh run list --limit 1`). A run left unwatched is a task left unfinished — a red deploy means production is still running the old code.
   - **Read the job output, don't just accept the green tick.** Open the run's **job summary** and check the test + coverage table each workflow publishes: per-suite pass/fail/skip counts and coverage percentages. Confirm the numbers moved the way your change should have moved them — a suite that silently ran **zero** tests is green and worthless, and coverage that fell where you added code means the test you wrote isn't exercising it. `gh run view <run-id>` lists the jobs; `gh run view <run-id> --log-failed` gets straight to a failure.
   - **Check no secrets were printed:** `./scripts/ci-check-secrets.sh <run-id>` (zero-arg = latest run). **The logs of this repository are public.** See *Never print a secret in CI* below.
   - **Verify the behaviour in production** — the health check plus whatever you actually changed.
7. **Write back — the docs are part of the change, not a follow-up.** In the same commit: `docs/ARCHITECTURE.md` if you touched the structure (a package, a route group, a table, a runtime flow); a **new numbered record** appended to `docs/ARCHITECTURE.md` §8 **only** if you made an *architectural* decision — one that shapes deployment, data, a component boundary, or the cost of a whole class of change — whose reasoning is not recoverable from the diff; a tuning constant, a UI behaviour or a test-harness fix gets a code comment instead, and a revisited decision gets a record that supersedes or amends the old one, never an edit to an existing record; `docs/RUNBOOK.md` if you worked out an operational or debugging procedure, or if you changed behaviour it describes; this file if a convention changed; and the living doc for durable project state. Each doc's `## LLM Continuation Context` block is updated with it — a stale block is worse than none. Docs that contradict the code are a defect owned by the change that caused them.

**CI vs deploy:** `main` = `deploy.yml` (lint · unit · web · e2e · integration · full-stack e2e, then auto-deploy over SSH) — the normal path. Both workflows publish a test + coverage summary to the run's job summary and upload the Playwright videos. Any non-`main` branch/PR = `ci.yml` (same tests, no deploy) — only if you deliberately want to stage something before it deploys.

## Completion protocol (Definition of Done)

Close a work item with a compact checklist — mark each **✅ done · ⏭️ skipped (+ why) · ➖ n/a**, and only ✅ what you actually verified:

| Gate | Status |
|------|--------|
| Requirements grounded — `docs/ARCHITECTURE.md` §1–7 + §8 records read *before* coding, living doc read | |
| Test base extended — unit + integration (or stated reason) | |
| `./dev.sh pre-commit` green | |
| Pushed to `main` → **auto-deploy watched to green** *(or noted as an owner action)* | |
| CI job output read — test counts + coverage checked, not just the green tick | |
| CI logs scanned for leaked secrets (`./scripts/ci-check-secrets.sh`) | |
| Behaviour verified in production | |
| Docs synced — `ARCHITECTURE.md` (structure + any new §8 record) / `RUNBOOK.md` as applicable, each with its continuation block | |
| Living doc current to as-built; LLM-continuation block updated | |
| Secrets/PII posture respected — nothing sensitive committed | |

Then close with a structured **end-of-task report** — sections in this order, each tight bullets rather than prose. This is the receipt the user reviews after letting the work run unsupervised, so it has to let them reconstruct *what changed, where it landed, and what was decided for them* at a glance. Omit a section only when it is genuinely empty, and then say "none" rather than dropping the heading.

1. **What shipped** — the user-visible or behavioural changes, one line each. Not a file-by-file diff.
2. **Tests added or extended** — named (file / test), unit **and** integration/e2e. Mandatory section; "none" is valid only for a genuinely test-exempt change, with the reason.
3. **CI + deploy** — the run, its conclusion, the test counts and coverage from its summary, the secret scan result, and what was verified in production.
4. **Working-tree status** — branch, and whether anything is left staged, unstaged, or untracked. Anything left dirty is called out with why.
5. **Push status** — what was committed and what was pushed, naming the ref. Make it unambiguous whether anything landed on `main`.
6. **Judgement calls made without being asked** — every decision the user did not direct: design choices, trade-offs, assumptions filled in for missing requirements, scope added or dropped. This is the review surface; when in doubt, list it. "None" only if the task was fully specified.
7. **The Definition-of-Done table above**, filled in.

## Deploy

- **Provisioning is one-time and manual:** `scripts/bootstrap.sh` (postgres, nginx, certbot, systemd, deploy user + CI key, then SSH hardening — non-standard port + fail2ban). Run it once over the existing root SSH. See the living doc's TODO section.
- **App deploys are CI:** push to `main` → `.github/workflows/deploy.yml` (environment `prod`) builds the SPA + Go binary, ships it over SSH, renders `/etc/psycho-space/app.env` from secrets, migrates, restarts the service, and health-checks `https://psycho-space.ru/healthz`. Watch the run; a red deploy means prod wasn't updated.

## Debugging

See `docs/RUNBOOK.md`: SSH access, service logs (`journalctl` + `/var/log/psycho-space/app.log`), DB queries over SSH (`psql`), nginx/cert checks, and the admin-bootstrap SQL.
