# psycho-space — Project Rules (CLAUDE.md)

Working rules for this repository — the *what*. The reasoning behind the shape of the system lives in `docs/ARCHITECTURE.md` (§1–7 the structure, §8 a one-paragraph summary of every decision, each linking to its full record in `docs/adrs/`), and this file points there rather than restating it, so a rule and its rationale cannot drift apart. This project is standalone and unrelated to any employer.

**Canonical living doc:** `~/Desktop/psycho-space/psycho-space.md` — the **root index**: project state, phased rollout, the owner's TODO list, and a link to each topic plan (dated `YYYYMMDD_<slug>.md` files in the same folder). Read it first; keep it current as work lands (every file there opens with an `## LLM Continuation Context` block for fast hand-off). That folder holds everything project-local and uncommitted — the living doc set, the game-art source images in `vanya_assets/`, and the operator's private detail (server host, hardened SSH port), none of which may ever enter this repository. If the folder isn't on your machine, ask the owner.

**In-repo documentation:** [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) (structure + the decision summaries, Mermaid) · [`docs/adrs/`](docs/adrs/) (one file per decision record) · [`docs/RUNBOOK.md`](docs/RUNBOOK.md) (operations & debugging).

## Working with Claude — chat tone

**Chat is terse pidgin; artifacts are proper English.** In session replies (prose back to the user), default to **terse pidgin**: drop articles/copulas, short sentences, lead with the answer, no preamble, no restating the question — optimise for the reader's speed. This applies to **chat only**. The moment text lands in an **artifact** — a source file, commit message, PR description, code comment, the living doc, an `## LLM Continuation Context` block, a ticket — it is **well-formed English** per the conventions below. Keep identifiers, code, paths, commands, and any safety-relevant or conditional statement **verbatim and unambiguous**; pidgin trims the prose around them, never the precision. When a nuance would be lost by dropping a word, keep the word.

## What this is

A Russian-language landing page + allowlist-gated web app for a small community. The landing is deliberately cringe; login is via **VK ID** or **Яндекс ID** — two doors, one identity model behind them (`(provider, blind index)`, ADR-054), and no linking between them. The app is several sections behind one shell, and an approved user lands in **«Ванягоччи»** — the front door, named once as `HOME_ROUTE_NAME` in `web/src/constants.ts` because the `/app` index, the router's guard redirects and the post-login push all have to agree on it. The other sections are «Смолтолк в Химках», **«ВАНЯДУМ»** — the first 3D game, a first-person shooter on a generated заброшка — **«СИМУЛЯТОР ФИНТЕХА»**, a top-down office arena where your salary accrues only while you stand perfectly still, and the **Wishlist with upvotes**, which was the first one built (the UI still says more are coming). Access is allowlist-gated: the owner is promoted to admin, then approves everyone else; unapproved users are told to ask to be allowlisted. RU region, single environment (prod), under personal-data law (152-ФЗ).

## Stack & layout

- **Backend:** Go 1.26 (via mise) · chi router · pgx/v5 · slog. No ORM, no Redis — all state in PostgreSQL with `expires_at` TTLs.
- **Frontend:** Vue 3 · Vite · TypeScript · Vuetify (Material) · vue-router · pinia, plus **three.js** for «ВАНЯДУМ» alone — reachable from one module, loaded as an async chunk behind that route, so nobody who never opens the game pays its ~177 kB gzip. Built and **embedded into the Go binary** (`go:embed internal/web/dist`).
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
  yandex/    Яндекс ID client — plain OAuth 2.0, no SDK, no id_token; also builds
             the authorize URL, so the client id lives only in config (ADR-055)
  wishlist/  items, comments, votes (upvote toggle on both)
  gamekhimki/  «Смолтолк в Химках» — LLM-judged dialogue: content/persona, judge, runs, art blobs
  gamevanyadum/  «ВАНЯДУМ» — the first 3D game, and the first thing here that
                    SIMULATES: collision destroys closed-form motion, so a 20 Hz
                    fixed-step loop advances in-memory arenas (one per run) and
                    Postgres is touched exactly twice per run — never on a tick.
                    content.go  the catalogue — movement, pickups, surfaces
                    level.go    the seeded Doom-style sector graph + derived walls
                    sim.go      Step: pure (level, player, command) → player
                    arena.go    one run's world + the real-time budget
                    history.go  the rewind ring lag compensation reads
                    service.go  the arena map, the tick, the one writer
  gamevanyagotchi/  «Ванягоччи» — the shared plane (in memory) + the pet (Postgres):
                    content.go  the catalogue — stats, actions, skins, NPCs, every constant
                    decay.go    time arithmetic for stats · motion.go the same for space
                    display.go  the in-memory cache the 5 Hz broadcast draws from
                    world.go    what is lying about in the yard — same cache rule
                    message.go  the wire types · service.go the verbs and the tick
  gamefintech/  «СИМУЛЯТОР ФИНТЕХА» — the second thing that SIMULATES and the first
                    to do it in DOM: pursuit is not closed form, so a 20 Hz loop
                    advances ONE process-wide office (not one arena per run) and
                    Postgres is touched once per shift — never on a tick.
                    content.go  the catalogue — the STATIC office, every constant
                    sim.go      Step: pure (desks, player, command) → player
                    boss.go     the man who walks at the nearest employee, and grins
                    chaser.go   Claude Code — the same walk, a different consequence
                    npc.go      Серега and Тёма, who are scenery with opinions
                    office.go   the one shared world + the per-occupant budget
                    message.go  the wire types · service.go the office and the tick
  settings/  app_settings key/value (open registration)
  web/       go:embed of the built SPA (dir gitignored except the tracked .gitkeep,
             which is what keeps `go build` working before the SPA is built)
migrations/  NNN_*.sql, embedded, auto-applied, immutable once shipped
web/         Vue SPA source (built to internal/web/dist, embedded at compile time)
  src/realtime/  module-scoped WebSocket client + reconnect policy (refcounted)
  src/lib/       pure per-feature logic (vanyagotchiPlane/Pet, gameKhimki*,
                 vanyadum{Level,Texture,Input,Rules,Step,Predict,Interp},
                 fintech{Step,Predict,Plane,Rules}) — unit-tested, no WebGL
  src/render/    the impure half: vanyadumScene.ts, the ONLY module importing
                 three.js, loaded as an async chunk behind its own route
  e2e/       Playwright: 360px layout (+ @wide at desktop), /api stubbed
  e2e-stack/ Playwright: full-stack, real binary + real Postgres
test/integration/  //go:build integration — testcontainers-go + fake VK server
scripts/     bootstrap.sh, harden-finalize.sh, e2e-stack.sh, ci-test-summary.sh
deploy/      systemd unit, nginx conf, psycho-deploy + make-superadmin helpers
docs/        ARCHITECTURE.md (structure + decision summaries) · RUNBOOK.md
  adrs/      ADR-0NN-<slug>.md — one file per decision record, rewritten in place
```

## Architecture — see `docs/ARCHITECTURE.md`

The structural view lives in **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)** (Mermaid, renders on GitHub): the logical container diagram, runtime sequences for login / a gated request / a game turn / the deploy, the package dependency graph, the ER model, the security view, and — in §8 — a one-paragraph summary of every decision record. Read it before changing anything structural — and **update it in the same change** when you add a domain package, a route group, a table, or a runtime flow.

The reasoning behind those choices — why sessions are server-side, why the SPA is embedded, why there are two Playwright suites — is in the numbered decision records: **one paragraph each in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) §8, and the full record as its own file in [`docs/adrs/`](docs/adrs/)**.

**A record states the decision as it stands today, and is rewritten in place when it changes.** There is no append-only rule, no `Superseded by`, and no amendment chains — those were tried and made the simple question ("what do we actually do about X?") require reading three records in the right order to answer. **The history is not lost: it lives in `git log -p docs/adrs/ADR-0NN-*.md`**, which shows every version of a decision alongside the commit message explaining why it moved — a better account of the thinking than a status line ever was. Two rules survive: **a number is never reused and gaps are permanent**, so existing references never shift; and a record that no longer applies at all is deleted rather than repurposed. Operational procedure is in **[`docs/RUNBOOK.md`](docs/RUNBOOK.md)**.

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
| «ВАНЯДУМ» | `internal/gamevanyadum/` | `game_vanyadum_runs` | `/api/game-vanyadum/*` | `GameVanyadumView.vue` at `/app/game-vanyadum` |
| «СИМУЛЯТОР ФИНТЕХА» | `internal/gamefintech/` | `game_fintech_shifts` | `/api/game-fintech/*` | `GameFintechView.vue` at `/app/game-fintech` |

- **Shared infrastructure is never prefixed** — `realtime`, `gameassets`, `session`, `account`, `crypto`, `db`, `logging`, `observability`, `httpapi`. A game may depend on these; none of them may know a game exists.
- **Inside a game's own package, types keep plain names** (`gamekhimki.Service`, never `gamekhimki.GameKhimkiService` — the linter rejects the stutter).
- **`wishlist` and `settings` are non-game sections** — neither games nor infrastructure. Unprefixed; this rule does not reach them.
- **Where the line falls:** does it encode a rule of *this* game, or is it a capability any game would want? Rules are per-game (runs, scores, pets, tuning constants); capabilities are shared (the art blob store, the realtime transport).
- **`game_key` column *values* are data, not names** — they do not move with a rename.

- **A game makes two decisions, not one: how it renders, and whether it owns a tick.** They are independent axes, and each game answers both for itself — which is what ADR-028 is for. The yard is DOM and CSS with no simulation at all (ADR-046, ADR-042); the shooter is WebGL over a 20 Hz server tick, because collision destroys closed form (ADR-047, ADR-048); «СИМУЛЯТОР ФИНТЕХА» is **DOM over a 20 Hz server tick** — it simulates because pursuit is not closed form, and it stays in the DOM because none of ADR-046's flip triggers fire (ADR-057). None of that is a contradiction. What does NOT vary is where the line falls when a canvas *is* used: **the canvas holds the world and nothing else.** Every readout, control and word of text stays real DOM, because nothing inside a canvas can be asserted on without pixel comparison and a test-only introspection API may not ship. A change that moves the HUD onto the canvas because it would look nicer is deleting a test surface, not tidying a view.
- **A realtime game's room name lives in the game's own package**, and `httpapi` holds only a map from room name to handler. Adding a game is a line in `main.go` and no change to any unprefixed package.

**Reasoning for all of the above lives in the records — [ADR-028](docs/adrs/) (self-contained modules), ADR-030 (the naming convention), ADR-031 (why the asset store is shared), ADR-046/047/057 (each game's rendering decision, where the canvas line falls, and why owning a tick does not oblige a game to leave the DOM), ADR-056 (one shared office against one arena per run), summarised in `docs/ARCHITECTURE.md` §8.** Read those before arguing with this rule. They are settled: rewriting one to mean something else is how a decision changes, so do it deliberately and in a commit that says why — not as a side effect of disagreeing with it.

### Everything a player does is visible to everyone, and derived rather than sent

Two halves of one rule: **an action is acknowledged where it happened**, and **everybody sees it — not just the person who did it.** It covers one-off *actions* and ongoing *buffs* alike, and it is a gate on every new verb, effect and state.

**Actions get a brief mark, at the place they happened.** Standing, walking, dashing and being caught need none — you can already see all of them. A *verb* cannot be seen: you press something, and a second later the world behaves differently. Without an acknowledgement a player who is not sure he pressed anything reads that as the game misbehaving, and a player who is sure reads it as being ignored.

**Buffs are shown for as long as they last**, on whoever is carrying them: a state with a duration is not an event, and a mark that flashes once and vanishes tells you nothing about the eight seconds that follow. The bald man going green while drunk is the shape — a property of the figure, visible the whole time, gone when it is.

**And all of it belongs to the whole office, not to the actor.** A colleague pointing the antagonist at *you* is the moment it matters most, so an effect only its author can see is a bug rather than a smaller feature. Assume every screen shows every action and every buff of every player, and treat "only the person who pressed it sees this" as unfinished.

**Derive it locally; do not spend the wire on it.** This is the constraint that makes the rest affordable:

- **Prefer a value the frame already carries.** A timer crossing from zero to non-zero, a balloon index becoming a line the catalogue already publishes, two consecutive positions implying a speed no walk could reach — all of these are free, and all of them work for peers as well as for yourself.
- **Join against the catalogue, which was fetched once.** Matching a published string to its index is better than a new field, and it keeps the pool layout server-side, so it can still be retuned without a client deploy.
- **A dedicated "an event happened" field is the last resort**, because it is bytes on a repeating payload to say *nothing happened* almost every time it is sent. If one is genuinely unavoidable, make it an index or a duration rather than a description, omit it in the resting state, and say in the change what it cost — bytes × rate × players × viewers.
- **A level is not an event.** Marking on "the cooldown is non-zero" rather than "it just started" flashes the plane ten times a second for the whole cooldown. Compare against the previous frame, per entity, and do it **before** anything overwrites the value being compared to.

**Keep it small.** One mark, well under half a second; a buff shown as a property of the figure rather than as something orbiting it. No screen shake, no full-plane flash, no sound. These games are played while something is walking towards you, and an effect big enough to watch is an effect that takes your eye off the only thing that matters. It says WHERE, not WHAT — the words are the balloons' job — and kinds are told apart by colour or shape when two can land at once.

**It still shows under `prefers-reduced-motion`** — unanimated, or dimmer, but present. Somebody who asked for less motion still needs to know something happened. Clear it on a timer rather than on `animationend`, which never fires when the animation is switched off.

A new verb ships with its acknowledgement, its buff rendering and its visibility to other players in the same change, exactly as it ships with its line in the splash cheatsheet. **An action nobody else can see is an unfinished action.**

### A game states its rules on its own splash screen

**Every game's splash screen carries a cheatsheet of that game's current rules** — what the player needs in order to play, on one screen, before they start. Not flavour text and not a vague description of the idea: the actual numbers and the actual consequences. What drains, what fixes it, what kills you, what the controls are, what happens while you are away.

This exists because the audience is a handful of friends who will open the thing once, on a phone, with no intention of reading anything else. A rule that is only in `content.go` is a rule nobody playing the game knows.

- **Derive it from the served catalogue wherever the catalogue carries it.** `GET /api/game-<name>/config` already publishes stats, their rates and penalties, and actions with their effects. Generate those lines from the config rather than typing the numbers out, so retuning a constant updates the cheatsheet by itself. A hand-typed number is a number that goes stale the first time somebody changes it.
- **What genuinely cannot be derived is hardcoded prose, and it is marked** with a comment saying so, because that is the part a rules change has to come back and edit by hand.
- **Generate the lines in a pure, unit-tested helper**, not in the template — the same rule the rest of the SPA follows.
- **Keeping it current is part of the task, not a follow-up.** A change to a game's rules — a new stat, a retuned constant, a new action, a new way to die — updates the splash cheatsheet **in the same change**, and *Task workflow* step 7 makes it a gate. A cheatsheet that describes the previous version of the game is worse than none, because a player will believe it.

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
- All security randomness via `crypto/rand`. Never log personal data or tokens (log the `identity_ref` hex if you must correlate).
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

**Design: prefer the simple thing, and prefer keeping the choice open**

Two habits, and they pull in the same direction — both are about not paying today for a future you are guessing at.

**Do not manufacture complexity that the problem did not bring.** Some complexity is inherent: a contested claim really does need an atomic statement, coupled decay really is piecewise arithmetic. That kind is earned and belongs in the code with its reasoning beside it. The other kind is *accidental* — an interface with one implementation, a config key with one value, a factory that constructs one thing, a layer that forwards a call unchanged, an abstraction extracted from a single use. Every one of those is a thing to read, to keep consistent and to change twice, bought against a second use that may never arrive. The test is blunt: **name the second use.** If you cannot, write the direct version. Duplication is cheaper to undo than the wrong abstraction — this repository already says so about games (ADR-028), and it is true generally.

**Prefer optionality to an early commitment made for no reason.** When two designs cost about the same today and one leaves more open, take that one — but only when it is genuinely free. The event store is the shape: `apply(state, event)` costs nothing over the direct version and leaves batching, replay and retro-tuning reachable, so it was worth taking; a whole scheduling framework for one recurring thing would not have been. Optionality is a *tiebreaker between equals*, not a licence to build a seam nobody asked for — a seam kept open at real cost is just accidental complexity with a better story.

The two rules resolve any apparent conflict between them the same way: **when in doubt, do the smaller thing.** It is easier to add a seam to code that is simple than to remove one from code that is not, and the version you actually understand is the one you can change when the second use finally shows up.

**Bytes on the wire are a design constraint — the audience is on a phone, on mobile data**

This is played on phones, outdoors, on whatever signal the person has. So the size and frequency of what crosses the network is a first-class design input, not something to measure afterwards — and the realtime socket is where it bites, because a frame repeats forever.

- **Nothing that never changes may ride on a repeating frame.** The 5 Hz roster is idempotent full state, which is the right shape (a dropped frame costs nothing) and also the expensive one: every byte in it is re-sent per player, per tick, per viewer, so the cost is quadratic in the yard. A field that is constant for the life of a session — a URL, a display name, anything derived from a durable row — is the wrong thing to put there. Publish an **identifier** and let the client fetch the constant thing once.
- **A one-off cached request beats a repeating payload, and that is the tie-break.** These two rules pull against each other: fewer round trips is better, *and* smaller frames are better. When they conflict, prefer the request — the browser caches it and it happens once per peer, where a frame payload happens five times a second forever. A ~250-character avatar URL on the roster was about a megabit a second at ten players; the same picture fetched by peer id is zero on the socket and one cached GET per face. (ADR-037.)
- **Then be un-chatty about the requests too.** Don't make a client issue N calls where one payload would do, don't poll for something the socket already reports, and don't send a field nobody reads. The catalogue is fetched once and everything is derived from it; keep that shape.
- **Prefer omitting to sending empty.** `omitempty` on every optional field, absent rather than `""`, and no field that exists only to say "there is nothing here".
- **When a payload has to grow, say what it costs** in the commit or the comment: bytes × rate × players × viewers. A number in the reasoning is what stops the next person guessing.

**No legacy code — the codebase carries one way of doing each thing**

When a change supersedes something, **the superseded thing goes in the same commit**. Not next week, not behind a flag, not "once the client has moved". A codebase that carries both the old and the new way of doing something is one where every reader has to work out which is live, and every future change has to be made twice or made wrong.

What this forbids, concretely:

- **A superseded implementation kept alongside its replacement** — a `fooLegacy`, an `//nolint:unused` corpse, a commented-out block "for reference". Git has the reference.
- **A compatibility alias or shim with no live caller.** The `/api/game/*` route was kept for exactly one deploy cycle with the reason and the expiry written down, then deleted and its absence pinned by a test. That is the only acceptable shape: a stated reason, a stated expiry, and a test that fails when the expiry passes.
- **A dead flag, unused config key, or parameter nothing sets.** An option with one value is not an option.
- **A second path to the same outcome** — an HTTP endpoint and a socket frame doing the same job, two functions computing the same number. Pick one, and if a second genuinely must exist for a while, it is a thin adapter onto the first rather than a copy of it.
- **A test asserting behaviour that no longer exists**, or a fixture for a shape nothing produces.

**A migration that cannot be finished in one commit is planned as a sequence that each end green**, with the removal named as its own step and done — not left as the step nobody comes back for. If a change lands the new way and cannot yet delete the old, that is stated in the end-of-task report as an explicit debt with what unblocks it, so it is a decision rather than a drift.

The exception is genuinely immutable history: `migrations/` are forward-only and never edited, and `docs/adrs/` records are rewritten rather than accumulated. Those are records of what happened, not code anybody executes.

**Tests are a deliverable**
- Every code-touching change **extends the test base**: unit tests for the changed logic **and**, when applicable, an integration or e2e test proving the behaviour end-to-end. Running the existing suite green is necessary but not sufficient.
- A behaviour change landing with no test delta is incomplete. Docs/config/mechanical changes may skip tests — state the reason.
- **Four suites, four parallel CI jobs.** Go unit (`./dev.sh test`) · testcontainers integration, Go-level with a fake VK server (`./dev.sh integration`) · Playwright mobile-layout with `/api` stubbed in the browser (`./dev.sh e2e`) · Playwright full-stack against the real binary and a real Postgres (`./dev.sh e2e-stack`). In CI the four run concurrently (`.github/workflows/tests.yml`, called by both `ci.yml` and `deploy.yml`), so the pipeline costs the slowest suite rather than their sum. Put a test where it will fail for the right reason: layout regressions in the stubbed suite, "did it actually persist" in the full-stack one.
- **Bound a wait by a deadline, never by an attempt count, and assert an outcome rather than an announcement.** Both rules are written in blood: a `for (let i = 0; i < N; …)` measures how hard you are willing to try, but a loaded CI runner changes how long each try *takes*, so the count runs out mid-convergence and the test lies about why; and reading a short-lived thing — a 4-second speech balloon, a frame from a render loop a backgrounded tab has paused — in two round trips lets it expire in the gap, so a verb that landed is reported as refused. **Never** answer a flake with `retries`, and never with a flag that turns a game's randomness off — that is test-only machinery in a production path. Determinism comes from direct DB setup. Full procedure, including how to reproduce CI's contention locally, is in [`docs/RUNBOOK.md`](docs/RUNBOOK.md) → *A test that passes on its own and fails in CI*.
- **The Playwright specs are type-checked too** (`web/tsconfig.json` includes `e2e` and `e2e-stack`). Playwright transpiles a spec without checking it, which made a test the one place in this repo where changing a shared helper's signature failed silently.
- `./dev.sh cover` reports coverage for all of it; CI writes the same table plus pass/fail counts into the run's job summary.
- **While developing, run the narrow thing. The gate is what runs everything, and it runs it once.** Every target takes arguments and narrows to what you pass: `./dev.sh test -run TestVKRedirect ./internal/httpapi/` · `./dev.sh integration -run TestLoginFlow ./test/integration/` · `./dev.sh web vkRedirect` · `./dev.sh e2e authRedirect.spec.ts -g "cancelled"` · `./dev.sh e2e-stack app.spec.ts -g "redirect target"`. Run the handful of tests that could plausibly fail from what you just changed, as often as you like — seconds each. Then commit, and let `./dev.sh pre-commit` be the single full pass. **Do not run a full suite by hand and then commit**: the hook runs the same suite immediately afterwards, so it is the same minutes spent twice, and the second run is the one that actually gates. The exception is a change whose blast radius you genuinely cannot bound (a shared helper, a router, a config every spec loads) — there, one broad run before the gate is cheaper than discovering it inside the hook.

**Frontend (SPA)**
- **Mobile-first & responsive — mandatory.** The site must be fully usable on phones (target ≈360 px wide) as well as desktop. Use Vuetify's responsive grid + breakpoints (`v-container`/`v-row`/`v-col`, `d-*` display utilities, `useDisplay()`), fluid layouts, and a mobile nav pattern (drawer / bottom nav) — never fixed pixel widths that overflow small screens. Keep the `viewport` meta in `index.html`.
- Touch targets ≥ 44 px; no hover-only affordances (tap + keyboard must both work).
- **Verify at mobile width before shipping any UI change** — and it is enforced, not just asked for: `./dev.sh e2e` runs the whole Playwright layout suite at **360 px** in the pre-commit gate and fails on horizontal overflow or a sub-44 px tap target. A change that only looks right on desktop is incomplete. Desktop (1440 px) re-runs only the tests tagged **`@wide`** — the ones whose claim is about width (cross-width ratios, overflow, the never-scroll shell, the ≥960 px permanent drawer). **If you write a test that could fail at desktop while passing on a phone, tag it `@wide`**; the bar and the reasoning are in the header of `web/playwright.config.ts`.
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
- `PSYCHOSPACE_VK_*` and `PSYCHOSPACE_YANDEX_*` — optional locally, and **neither real login can run on a workstation**: VK ID is IP-allowlisted to the production host and both providers' redirect URIs are the production domain. Use `./dev.sh seed` instead; it mints an approved account and prints its session cookie, and takes `-provider yandex` when you need the second provider's shape. The Yandex trio is all-or-none — set all three or none, since one alone fails at startup by design.
- `PSYCHOSPACE_LLM_BASE_URL` / `_API_KEY` / `_MODEL` — needed only to play the game locally. **Every turn costs real money**, so leave them blank unless you are working on the game; unset, `/api/game-khimki/attempt` answers 503 and everything else works.
- `PSYCHOSPACE_LOG_DIR`, `PSYCHOSPACE_SESSION_TTL`, `PSYCHOSPACE_OTLP_ENDPOINT` — optional.

The full-stack e2e suite needs none of this: `scripts/e2e-stack.sh` generates throwaway keys per run and starts its own database.

**GitHub Actions `prod` environment secrets** (Settings → Environments → prod). The deploy workflow reads these names verbatim and renders `/etc/psycho-space/app.env` from them:

`DEPLOY_SSH_KEY` · `DEPLOY_SSH_HOST` · `DEPLOY_SSH_PORT` · `DEPLOY_SSH_USER` · `POSTGRES_PASSWORD` · `APP_ENC_KEY` · `APP_HMAC_KEY` · `APP_SESSION_KEY` · `VK_APP_ID` · `VK_SERVICE_TOKEN` · `VK_REDIRECT_URI` · `YANDEX_CLIENT_ID` · `YANDEX_CLIENT_SECRET` · `YANDEX_REDIRECT_URI` · `LLM_BASE_URL` · `LLM_API_KEY` · `LLM_MODEL` · optional `VK_VERIFY_IDTOKEN` / `VK_JWKS_URL` / `VK_ISSUER`.

**Handle with care.** `APP_ENC_KEY` and `APP_HMAC_KEY` are not rotatable in place: losing the encryption key makes stored profiles unrecoverable, and changing the HMAC key breaks every blind index, which orphans every account. The server host and hardened SSH port are secret too — they live in these secrets, in the operator's `~/.ssh/config`, and in the local living doc, and must never appear in the repository.

## Every doc opens with an LLM-continuation block

**Every documentation file in this repo starts with an `## LLM Continuation Context` block**, directly under the H1. Its only job is to let the *next agent* (or the owner, six weeks later) resume the topic without re-deriving it. It is written **for machines, not humans** — optimise for hand-off, not readability; that it renders on the page too is an accepted cost, not a reason to soften it into prose.

- **New docs: mandatory.** No doc is created without one.
- **Existing docs: add on touch.** Editing an older doc that lacks one? Add it in that edit. Don't retrofit docs you aren't otherwise touching.
- **Keep it current.** Update it in the same commit that changes the doc, so `status` / `next` / `done` never lie. **A stale block is worse than none** — it will be believed.
- **When it grows into a history, rewrite it as a snapshot.** A `status:` field that has been appended to for a dozen iterations stops being orientation and becomes a chronicle — *then we did I6, then I6b, then I7a* — and a block that long is a block nobody reads, which costs more than a short one that omits something. So when the historical narrative gets large, **replace it with a current-state description: what the thing IS now, not how it got here.** Git already holds the history perfectly, and far better than a paragraph does. Rewriting is not the same as deleting a fact that still matters — a constraint or a decision that still binds stays, it just stops being narrated in the order it happened. Overwriting is correct here, and it is the same instinct that governs `docs/adrs/`: both describe the present, and `git log -p` is where either one's history lives.
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

**With a goal set, run to the end of it.** A standing `/goal` is standing authorisation: work through it without checking in, and treat an interruption as something to be justified rather than as the safe default. Interrupting is *not* free — it costs the person their attention and costs the run its momentum, and a question they have effectively already answered is worse than a decision made and reported. So stop only when continuing would be unsafe, destructive, or genuinely unknowable: a fact that cannot be discovered from the code, an irreversible action outside what the goal implies, a contradiction inside the goal itself. Everything else — which of two reasonable designs, how to slice the work, what to name a thing, whether to split a deploy — is decided, done, and **written into the end-of-task report** under judgement calls, where it can be reviewed after the fact instead of blocking the work before it. When something genuinely cannot be finished, leave the tree green, say exactly where it stopped and what unblocks it, and keep going with the rest of the goal rather than stopping the whole run.

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
1. **Ground it — read `docs/ARCHITECTURE.md` before writing code, not after.** Both altitudes: §1–7 for the structure you are about to change (the package layout, the runtime flow, the ER model, the API map), and **§8 and `docs/adrs/` for the decision records that already govern it.** A recorded decision is settled: build on it, and if you believe it is wrong, say so and change the record deliberately — never quietly implement against it. This step is what stops a change from re-deriving, contradicting, or silently reversing a decision that was already paid for; several §8 records exist because something was learned the expensive way. Then read the living doc + this file, and for anything non-trivial write or refresh the plan in the living doc *before* coding, keeping its `## LLM Continuation Context` block (`status`/`next`/`done`) accurate.
2. **Branch** — `<type>-short-slug` off an up-to-date `main`; implement in small, reviewable slices.
3. **Extend the test base** — unit tests for the changed logic **and** a testcontainers integration test when there's an end-to-end path (see *Tests are a deliverable*).
4. **Gate — and let it be the only full run.** While implementing, run just the tests your change could break (targeted args, see *Tests are a deliverable*); then `./dev.sh pre-commit` must pass (build → lint → unit → web → e2e → integration → full-stack e2e). Never `--no-verify`; fix the cause. Don't pre-run the whole suite by hand first — the hook is about to run it anyway.
5. **Commit + push to `main`** — Conventional Commits. This deploys, so only push a green, verified change.
6. **Watch the deploy to completion, and read what it produced — never fire-and-forget.** Pushing to `main` triggers `deploy.yml` (full suite, then ships over SSH). All four steps are required before the task is done:
   - **Watch it to a conclusion:** `gh run watch <run-id> --exit-status` (find it with `gh run list --limit 1`). A run left unwatched is a task left unfinished — a red deploy means production is still running the old code.
   - **Read the job output, don't just accept the green tick.** Open the run's **job summary** and check the test + coverage table each workflow publishes: per-suite pass/fail/skip counts and coverage percentages. Confirm the numbers moved the way your change should have moved them — a suite that silently ran **zero** tests is green and worthless, and coverage that fell where you added code means the test you wrote isn't exercising it. `gh run view <run-id>` lists the jobs; `gh run view <run-id> --log-failed` gets straight to a failure.
   - **Check no secrets were printed:** `./scripts/ci-check-secrets.sh <run-id>` (zero-arg = latest run). **The logs of this repository are public.** See *Never print a secret in CI* below.
   - **Verify the behaviour in production** — the health check plus whatever you actually changed.
7. **Write back — the docs are part of the change, not a follow-up.** In the same commit: `docs/ARCHITECTURE.md` if you touched the structure (a package, a route group, a table, a runtime flow); a **new record** — a file in `docs/adrs/` plus a one-paragraph summary and link in `docs/ARCHITECTURE.md` §8, taking the next global number — **only** if you made an *architectural* decision, one that shapes deployment, data, a component boundary, or the cost of a whole class of change, whose reasoning is not recoverable from the diff; a tuning constant, a UI behaviour or a test-harness fix gets a code comment instead. A decision that has *changed* is **rewritten in place** in its existing record, in a commit whose message says what moved and why, because `git log -p` on that file is now the history; `docs/RUNBOOK.md` if you worked out an operational or debugging procedure, or if you changed behaviour it describes; **the game's own splash-screen rules cheatsheet if you changed how that game plays** (see *A game states its rules on its own splash screen* — the player-facing rules are a doc, and this is the step that keeps them true); this file if a convention changed; and the living doc for durable project state. Each doc's `## LLM Continuation Context` block is updated with it — a stale block is worse than none, and one that has grown into a history gets **rewritten as a current-state snapshot** rather than appended to. Docs that contradict the code are a defect owned by the change that caused them.

**CI vs deploy:** `main` = `deploy.yml` — it calls `tests.yml` (four parallel jobs: Go lint+unit+integration · web type-check+unit+build · UI layout · full-stack e2e, joined by `summary`), builds the binary alongside them, and auto-deploys over SSH once every one of them is green. The normal path. Both workflows publish a test + coverage summary to the run's job summary and upload the Playwright videos. Any non-`main` branch/PR = `ci.yml` (same tests, no deploy) — only if you deliberately want to stage something before it deploys.

## Completion protocol (Definition of Done)

Close a work item with a compact checklist — mark each **✅ done · ⏭️ skipped (+ why) · ➖ n/a**, and only ✅ what you actually verified:

| Gate | Status |
|------|--------|
| Requirements grounded — `docs/ARCHITECTURE.md` §1–7 + the governing records read *before* coding, living doc read | |
| Test base extended — unit + integration (or stated reason) | |
| `./dev.sh pre-commit` green | |
| Pushed to `main` → **auto-deploy watched to green** *(or noted as an owner action)* | |
| CI job output read — test counts + coverage checked, not just the green tick | |
| CI logs scanned for leaked secrets (`./scripts/ci-check-secrets.sh`) | |
| Behaviour verified in production | |
| Docs synced — `ARCHITECTURE.md` (structure + any new or rewritten record in `docs/adrs/`) / `RUNBOOK.md` as applicable, each with its continuation block | |
| Game rules changed? — the game's splash-screen cheatsheet updated with them | |
| Living doc current to as-built; LLM-continuation block updated | |
| No legacy left behind — every superseded path, shim, flag or fixture deleted in the same commit (or the debt named) | |
| Secrets/PII posture respected — nothing sensitive committed | |

Then close with a structured **end-of-task report** — sections in this order, each tight bullets rather than prose. This is the receipt the user reviews after letting the work run unsupervised, so it has to let them reconstruct *what changed, where it landed, and what was decided for them* at a glance. Omit a section only when it is genuinely empty, and then say "none" rather than dropping the heading.

1. **What shipped** — the user-visible or behavioural changes, one line each. Not a file-by-file diff.
2. **Tests added or extended** — named (file / test), unit **and** integration/e2e. Mandatory section; "none" is valid only for a genuinely test-exempt change, with the reason.
3. **CI + deploy** — the run, its conclusion, the test counts and coverage from its summary, the secret scan result, and what was verified in production.
4. **Working-tree status** — branch, and whether anything is left staged, unstaged, or untracked. Anything left dirty is called out with why.
5. **Push status** — what was committed and what was pushed, naming the ref. Make it unambiguous whether anything landed on `main`.
6. **Legacy removed** — what this change superseded and deleted, named. If anything superseded is still in the tree, say what, why it could not go yet, and what unblocks its removal. "None" when the change added something new rather than replacing anything.
7. **Judgement calls made without being asked** — every decision the user did not direct: design choices, trade-offs, assumptions filled in for missing requirements, scope added or dropped. This is the review surface; when in doubt, list it. "None" only if the task was fully specified.
8. **The Definition-of-Done table above**, filled in.

## Deploy

- **Provisioning is one-time and manual:** `scripts/bootstrap.sh` (postgres, nginx, certbot, systemd, deploy user + CI key, then SSH hardening — non-standard port + fail2ban). Run it once over the existing root SSH. See the living doc's TODO section.
- **App deploys are CI:** push to `main` → `.github/workflows/deploy.yml` (environment `prod`) builds the SPA + Go binary, ships it over SSH, renders `/etc/psycho-space/app.env` from secrets, migrates, restarts the service, and health-checks `https://psycho-space.ru/healthz`. Watch the run; a red deploy means prod wasn't updated.

## Debugging

See `docs/RUNBOOK.md`: SSH access, service logs (`journalctl` + `/var/log/psycho-space/app.log`), DB queries over SSH (`psql`), nginx/cert checks, and the admin-bootstrap SQL.
