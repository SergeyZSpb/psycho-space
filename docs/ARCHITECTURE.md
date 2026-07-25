# psycho-space — Architecture

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** psycho-space at two altitudes in one file — the structural view (§1–7: logical containers, runtime flows, package layout, data model, deployment) and the numbered decision records that say *why* it has that shape (§8, append-only). `CLAUDE.md` carries the *rules*; this file carries the *shape* and the *why*.
- **status:** current as of «Ванягоччи» I4 (2026-07-25). One Go binary (embedded Vue SPA + `/api`) behind nginx on a single Ubuntu box, PostgreSQL 16 local. The realtime transport is shipped, carries a `bye` frame, and now has three game seams — inbound `Handler`, `Hub.Members`, and `Hub.PublishTo` for a unicast reply (ADR-033, ADR-037). Two games are in play: **«Смолтолк в Химках»** (shipped — LLM-judged dialogue, `internal/gamekhimki/`, `/api/game-khimki/*`, the only paid path) and **«Ванягоччи»** (`internal/gamevanyagotchi/` — realtime, **no LLM on any path**; `/app/game-vanyagotchi` renders the shared plane, sends taps back, and **survives a deploy** — it reconnects with jittered backoff, treats a revoked session as terminal, and shows one entity per **account** under a per-process pseudonym (ADR-037). It still has **no table, no pet and no durable state**). §8 was created on 2026-07-25 by merging `docs/DESIGN.md` into this file — 26 records, bodies moved verbatim — ADR-027…034 and ADR-037 were appended the same day, and four records were withdrawn the same day for failing the log's architecture bar, so the numbering has permanent gaps.
- **rename complete (2026-07-25):** game 1 moved off generic `game` naming onto the `Game<Name>` convention (ADR-030) — package `internal/game/` → `internal/gamekhimki/` (types inside keep plain names, so `gamekhimki.Service`), table `game_runs` → `game_khimki_runs` via `migrations/007_game_khimki_rename.sql` (**`game_assets` deliberately NOT renamed** — the blob store is shared infrastructure, ADR-031), routes `/api/game/*` → `/api/game-khimki/*`, SPA `GameView.vue` → `GameKhimkiView.vue` and `/app/game` → `/app/game-khimki` with a permanent redirect. `game_key` **values** are untouched (`smalltalk_khimki`) — data, not names, and the art blobs are keyed on them. **The one-deploy-cycle `/api/game/*` alias has served its cycle and is deleted**; `TestGameKhimkiLegacyPathAliasIsGone` pins its absence, and nothing may be written against that prefix again. The `/app/game` → `/app/game-khimki` SPA redirect is permanent and stays. Sections 1–7 below describe the post-rename state.
- **code:** `cmd/psycho-space/main.go` (DI root — read this first), `internal/httpapi/router.go` (every route and middleware), `migrations/` (schema, forward-only).
- **relocate:** `grep -rn "func (s \*Server) handle" internal/httpapi` lists every handler; `internal/*/service.go` is each domain's entry point; `grep -n '^#### ADR-' docs/ARCHITECTURE.md` lists every decision record.
- **adr:** §8 is an **append-only** decision log. Never edit an accepted record's decision or reasoning. A retired decision gets a **new** record and the old one is marked `_Superseded by ADR-0NN · date_` with its body untouched; a decision that still stands but whose *mechanism* changed keeps its record with `· amended by [ADR-0NN](#anchor) — what changed` appended to the status line, and the amending record carries `· amends ADR-0NN` (ADR-017 / ADR-018 are the worked example). Status vocabulary is `Accepted` and `Superseded` only — no `Proposed`. **The bar is architecture:** a decision that shapes deployment, data, a component boundary, or the cost of a whole class of change. A tuning constant, a UI behaviour or a test-harness fix does **not** get a record however subtle its reasoning — that goes in a comment beside the code. Four records were withdrawn on 2026-07-25 for failing this bar, so **the numbering has gaps and a number is never reused**; existing references therefore never shift. Numbers are identifiers, not an ordering and not a sequence: take the next global one, wherever the group. Highest record when this was written: **ADR-037** — confirm with `grep -o 'ADR-[0-9]\{3\}' docs/ARCHITECTURE.md | sort -u | tail -1`. `./scripts/check-docs.sh` (in the lint gate) rejects a duplicate id or a dead anchor, and deliberately permits gaps.
- **done:** auth/accounts/allowlist, wishlist + comments (both upvotable), the LLM-judged game, admin + settings, tracing, rate limiting keyed on a trusted client IP — §1–7 describe all of it, §8 records the decisions behind it.
- **next:** keep this file in step with the code — a new domain package, route group, table, or runtime flow updates the matching section here in the same change, and a decision whose reasoning is not recoverable from the diff is appended to §8 as a **new** record (`CLAUDE.md` → *Task workflow* step 7 makes both a gate).
- **related:** `../CLAUDE.md` (rules), `RUNBOOK.md` (operations — and the owner of the measurements and operational economics, notably the game's per-turn cost, which is re-measured rather than superseded), the owner's local living doc (roadmap, TODO, private operational detail). `docs/DESIGN.md` was merged into §8 here on 2026-07-25 and deleted; `git log -- docs/DESIGN.md` still resolves its history.
- **decisions / constraints:** SPA is embedded in the binary, not separately hosted; sessions are server-side opaque tokens, never JWT; personal data is encrypted at rest and looked up through a blind index, never plaintext; migrations are immutable once shipped; no test-only code in production paths; **each game is a self-contained module that shares no DB or service code with any other game** — duplication between games is deliberate, and shared code is platform only (ADR-028; `CLAUDE.md` → *Games are self-contained modules*); **every game module is named `Game<Name>` at every layer** and platform packages are deliberately unprefixed (ADR-030) — with the boundary drawn at rule-versus-mechanism, so a game owns its state but not generic capabilities like the art blob store (ADR-031). Each of these has a record in §8 carrying its reasoning; do not relitigate one there by editing it.
- **diagram authoring constraint:** the Mermaid blocks here must parse on GitHub, and a `;` anywhere in sequence-diagram message or note text is a **statement separator**, not punctuation — it silently breaks the whole diagram (`Parse error … got 'NEWLINE'`). Use `<br/>` or an em dash instead. Quotes, braces, `=`, and parentheses (including in a `participant X as Name (alias)`) are all safe. Validate a diagram change by rendering it, not by eye: extract each mermaid fence to its own `.mmd` file and run `npx -y @mermaid-js/mermaid-cli@latest -p pconf.json -i b.mmd -o b.svg`, where `pconf.json` is `{"args":["--no-sandbox"]}` (Chrome cannot sandbox in this environment).

---

## 1. Logical view

One process serves everything. nginx terminates TLS and proxies to it; PostgreSQL holds all state (there is no Redis — TTLs are `expires_at` columns).

```mermaid
flowchart TB
    subgraph client["Browser (mobile-first)"]
        SPA["Vue 3 SPA<br/>Vuetify · pinia · vue-router"]
    end

    subgraph box["Single Ubuntu 24.04 box"]
        NGINX["nginx<br/>TLS (certbot) · security headers · X-Real-IP"]
        subgraph bin["psycho-space — one Go binary (systemd)"]
            EMBED["embedded SPA<br/>go:embed internal/web/dist"]
            API["chi router /api<br/>middleware · handlers"]
            DOM["domain services<br/>account · session · wishlist · settings<br/>gamekhimki (a game) · gameassets (shared)"]
            REPO["repositories (pgx)"]
        end
        PG[("PostgreSQL 16<br/>localhost")]
    end

    VK["VK ID<br/>id.vk.ru"]
    LLM["LLM judge — «Смолтолк в Химках» only<br/>OpenAI-compatible endpoint"]

    SPA -- HTTPS --> NGINX
    NGINX -- "127.0.0.1:8080" --> EMBED
    NGINX -- "127.0.0.1:8080" --> API
    API --> DOM --> REPO --> PG
    DOM -- "code exchange + user_info" --> VK
    DOM -- "one chat completion per turn (paid)" --> LLM
```

**Why one binary.** The SPA is compiled into the executable, so a deploy is a single file plus a restart, and nginx never needs to know about static asset paths. See [§8 → ADR-001](#adr-001--the-spa-is-embedded-in-the-go-binary) for why, and for what it costs.

## 2. Runtime views

### 2.1 Login — VK ID confidential backend exchange

The authorization code is exchanged **on the server**, so the VK service token never reaches the browser. A session cookie is issued even for `pending` and `blocked` accounts: it identifies without authorizing, because `requireAuth` still demands `status == approved`. See [§8 → ADR-007](#adr-007--a-session-cookie-is-issued-even-for-pending-and-blocked-accounts) for why.

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser (SPA)
    participant A as psycho-space
    participant V as id.vk.ru
    participant P as PostgreSQL

    B->>A: GET /api/auth/vk/state
    A-->>B: state (+ httpOnly state cookie)
    Note over B: consent checkbox must be ticked<br/>before the VK widget is mounted (152-ФЗ)
    B->>V: OneTap + PKCE
    V-->>B: code, device_id
    B->>A: POST /api/auth/vk/callback {code, device_id, state, code_verifier, consent_version}
    A->>V: POST /oauth2/auth (code + service_token + code_verifier)
    V-->>A: access_token (+ id_token)
    A->>V: GET /oauth2/user_info
    V-->>A: profile (name, sex, birthday, avatar, user_id)
    A->>P: upsert by blind index HMAC-SHA256(vk_user_id)<br/>profile fields AES-256-GCM encrypted
    A->>P: INSERT session (token_hash = HMAC(token))
    A-->>B: Set-Cookie httpOnly<br/>Secure<br/>SameSite=Strict<br/>+ {status, account}
    Note over A: VK tokens are discarded here — never stored
```

### 2.2 A wishlist upvote (the shape every gated request has)

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser
    participant M as middleware chain
    participant H as handler
    participant P as PostgreSQL

    B->>M: POST /api/wishlist/items/{id}/vote (session cookie)
    Note over M: Recoverer → accountLogContext → traceHeader<br/>→ requestLogger → bodyLimit(1 MiB) → rateLimit(240/min)
    M->>H: request + trace id
    H->>P: SELECT session by HMAC(token), not expired
    P-->>H: account (must be status=approved)
    H->>P: INSERT wishlist_votes ON CONFLICT DO NOTHING
    H-->>B: 204 (+ X-Trace-Id)
```

Any non-2xx answers `{error: "<stable_code>", trace_id}` — never `err.Error()` — and the SPA shows the trace id in a copyable modal. See [§8 → ADR-024](#adr-024--errors-carry-a-trace-id-and-never-carry-the-error-text) for why.

### 2.3 A turn in «Смолтолк в Химках» (the only paid path)

**This flow is specific to «Смолтолк в Химках»** — the LLM-judged dialogue game in `internal/gamekhimki/`, served under `/api/game-khimki/*` — and nothing in it generalises to another game. The second game, «Ванягоччи» (realtime, in design), makes no LLM call on any path: [§8 → ADR-016](#adr-016--no-realtime-message-may-reach-the-llm) forbids a realtime message from reaching the judge at all, so «Смолтолк в Химках» is the only paid path in the system, not merely the first one.

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser
    participant A as psycho-space
    participant L as LLM judge
    participant P as PostgreSQL

    B->>A: POST /api/game-khimki/attempt {transcript, choice, anger, themes_done}
    Note over A: rate limit 5/min per client IP — every call costs money
    A->>A: build prompt: static system → history (replayed as JSON) → one volatile message last
    A->>L: chat completion (reasoning_effort: none, max_tokens 1500)
    L-->>A: {reply, art, achieved, game_over, anger, options[], themes_done[]}
    A->>A: salvageJudgeReply → flexInt/flexBool → clamp anger<br/>autoMarkThemes / confirmThemes / steerTheme
    alt anger >= AngerLoseAt (90)
        A-->>B: run ends server-side, GameOverArt forced
    else
        A-->>B: judged turn
    end
    B->>A: POST /api/game-khimki/runs {success, steps}
    A->>P: INSERT game_khimki_runs (feeds the four record leaderboards)
```

The prompt order is load-bearing, and so is the shape of the history: static system prompt → each past turn replayed as the JSON the judge returned → one volatile message last. See [§8 → ADR-013](#adr-013--the-prompt-is-laid-out-for-prefix-caching-and-history-is-replayed-as-json) for why. Measurements and per-turn costs: `RUNBOOK.md` → *Working on the game*.

### 2.4 Realtime connection lifetime

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser
    participant N as nginx
    participant A as psycho-space
    participant H as hub
    participant G as gamevanyagotchi

    B->>N: GET /api/realtime (session cookie, Origin)
    Note over N: location /api/realtime<br/>proxy_http_version 1.1 + Upgrade/Connection<br/>re-declares X-Real-IP (headers do NOT merge)
    N->>A: upgraded request
    Note over A: requireAuth (approved only) → origin check → 101
    A->>H: Register(conn, room) — caps checked here
    loop while connected
        G->>H: Members(room) on the game's own 5 Hz tick
        G->>H: Publish(room, roster) — idempotent full state
        H-->>B: broadcast (non-blocking — a slow client is dropped, never waited on)
        B->>A: frames (≤4 KiB, ≤10/s)
        A->>G: HandleInbound(Member, room, payload) — read pump, after the rate limit
    end
    Note over A,H: SIGTERM → cancel hub ctx → hub asks each conn to close
    H->>A: Close(1001, "restart")
    A-->>B: {"t":"bye","code":1001,...} then the socket drops
    Note over A,H: THEN http.Shutdown (Shutdown alone does not close hijacked connections)
```

**The hub carries bytes and decides nothing.** A game publishes through it and reads from it across two game-agnostic seams — a `Handler` for inbound frames and a `Members` query for presence — so `internal/realtime` contains no game's vocabulary and «Ванягоччи» owns its own wire types. See [ADR-033](#adr-033--a-game-reads-the-socket-through-a-game-agnostic-handler-and-pulls-presence) and [ADR-034](#adr-034--the-broadcast-tick-is-injected-and-belongs-to-the-game).

**On the browser side** the socket is owned at module scope (`web/src/realtime/socket.ts`), not by a component: the yard is a lazy child route, so a component-owned socket would re-handshake on every navigation and spend another of the three connections the server allows per account. Its lifetime follows a subscription refcount with a grace period rather than the route. The rule that governs everything rendered from a frame is written in that file and in `web/src/lib/vanyagotchiPlane.ts`, and is worth knowing before touching either: **membership is reactive, positions are not.** Who is present goes through pinia and a keyed list; where they are is written straight to two CSS custom properties, because a 5 Hz frame bound to reactivity costs a scheduler pass and a vdom patch per entity to produce a transform the compositor could have been handed directly.

The reason arrives as a **frame**, not as a WebSocket close code — a browser sees `1006` for every disconnect and reads the reason from the last `bye` frame instead. Codes: `1001` planned restart (reconnect promptly), `1013` evicted or over a cap (back off), `4001` session revoked (terminal — stop). See [ADR-018 · *The close reason travels as a frame*](#adr-018--the-close-reason-travels-as-a-frame-not-as-a-close-code) for why, and [ADR-019 · *The read pump must not observe shutdown*](#adr-019--the-read-pump-must-not-observe-shutdown) for the library trap that makes the ordering load-bearing.

### 2.5 Deploy

```mermaid
sequenceDiagram
    autonumber
    participant D as Developer
    participant GH as GitHub Actions (deploy.yml)
    participant S as Server
    participant P as PostgreSQL

    D->>GH: git push main (pre-commit gate already green locally)
    GH->>GH: lint · unit · web · e2e · integration · full-stack e2e
    GH->>GH: build SPA → embed → build linux binary
    GH->>S: scp binary + app.env (rendered from prod secrets) over SSH
    S->>S: install unit + nginx conf, restart service
    S->>P: migrations applied at boot (embedded, forward-only)
    GH->>S: GET https://psycho-space.ru/healthz (gate)
```

A push to `main` is a deploy, and a red run means production was not updated. See [§8 → ADR-003](#adr-003--push-to-main-deploys-the-gates-are-the-safety-net) for why that is safe without a staging environment.

## 3. Package structure

```mermaid
flowchart LR
    MAIN["cmd/psycho-space<br/>config · DI · migrate · shutdown"]
    SEED["cmd/dev-seed<br/>local session seeder (dev only)"]

    subgraph platform["platform packages"]
        CFG["config<br/>env, fail-fast, no secret defaults"]
        CRY["crypto<br/>AES-256-GCM · HMAC blind index · tokens"]
        DB["db<br/>pgxpool · DBTX · embedded migrator"]
        LOG["logging<br/>slog JSON (+ rotated file)"]
        OBS["observability<br/>OpenTelemetry spans + trace ids"]
    end

    HTTP["httpapi<br/>chi router · middleware · handlers"]
    RT["realtime<br/>WebSocket hub · per-conn pumps"]

    subgraph domain["domain packages — repository.go + postgres_repository.go + service.go + errors.go"]
        ACC["account"]
        SESS["session"]
        WISH["wishlist"]
        SET["settings"]
        VKP["vk (client + id_token verifier)"]
        subgraph games["games — self-contained, share nothing with each other"]
            GAME["gamekhimki<br/>«Смолтолк в Химках»"]
            VANYA["gamevanyagotchi<br/>«Ванягоччи» — shared plane"]
        end
    end

    WEB["web<br/>go:embed of the built SPA"]
    MIG["migrations<br/>NNN_*.sql, embedded"]

    MAIN --> CFG & DB & LOG & OBS & HTTP & WEB & MIG & RT & VANYA
    HTTP --> ACC & SESS & WISH & GAME & SET & VKP & RT
    ACC & SESS & WISH & GAME & SET --> DB
    ACC & SESS --> CRY
    VANYA -- "publishes through / reads from" --> RT
    SEED -.reuses.-> ACC & SESS & CRY & DB
```

**The rule:** dependencies point inward and downward — handlers know services, services know repositories, repositories know `db.DBTX`. Nothing in `internal/*` imports `httpapi`. Adding a feature means a new `internal/<domain>/` package with those four files, a `NNN_*.sql` migration, wiring in `main.go` + `httpapi.Deps` + routes, and a case in `test/integration/`.

**Games are the exception to the usual instinct to factor things out.** Each game is a self-contained module: its own package, its own `game_<name>_*` tables, its own routes and views, its own leaderboard code — and **no game imports another, even where the code would be identical.** A game may depend on platform packages (`realtime`, `session`, `account`, `crypto`, `db`, and the `httpapi` plumbing); none of those may know a game exists, which is why the socket is addressed as the game-agnostic `/api/realtime?room=…` and game-specific message types live in the game's own package. The test for the boundary: deleting a game must mean deleting its package, its migration, its routes and its views — and nothing else. See [§8 → ADR-028](#adr-028--games-are-self-contained-modules) for why, and `CLAUDE.md` → *Games are self-contained modules* for the same rule stated as a working rule.

**And each game's name is spelled out at every layer**, which is what makes that boundary test executable rather than a judgement call: package `internal/game<name>/`, tables `game_<name>_*`, routes `/api/game-<name>/*`, view `Game<Name>View.vue` at `/app/game-<name>` — so `git grep -il game<name>` enumerates the whole module. «Смолтолк в Химках» is `gamekhimki`; «Ванягоччи» will be `gamevanyagotchi`. Platform packages stay unprefixed on purpose, because the missing prefix is the signal that they are game-agnostic. See [§8 → ADR-030](#adr-030--game-modules-are-named-gamename).

## 4. Data model

Every table carries `created_at` / `updated_at`, and everything soft-deletable carries `deleted_at` (queries filter `WHERE deleted_at IS NULL`).

```mermaid
erDiagram
    accounts ||--o{ sessions : "has"
    accounts ||--o{ wishlist_items : "authors"
    accounts ||--o{ wishlist_votes : "casts"
    accounts ||--o{ wishlist_comments : "authors"
    accounts ||--o{ wishlist_comment_votes : "casts"
    accounts ||--o{ game_khimki_runs : "plays"
    wishlist_items ||--o{ wishlist_votes : "receives"
    wishlist_items ||--o{ wishlist_comments : "has"
    wishlist_comments ||--o{ wishlist_comment_votes : "receives"

    accounts {
        uuid id PK
        bytea vk_user_ref UK "blind index HMAC-SHA256(vk_user_id)"
        bytea vk_user_id_enc "AES-256-GCM"
        bytea first_name_enc
        bytea last_name_enc
        bytea avatar_url_enc
        bytea sex_enc
        bytea birthday_enc
        text role "user | admin | superadmin"
        text status "pending | approved | blocked"
        timestamptz consent_at
        text consent_version
    }
    sessions {
        uuid id PK
        uuid account_id FK
        bytea token_hash UK "HMAC of the raw cookie value"
        timestamptz expires_at
    }
    wishlist_items {
        uuid id PK
        uuid account_id FK
        text title
        text body
    }
    wishlist_votes {
        uuid id PK
        uuid item_id FK
        uuid account_id FK
    }
    wishlist_comments {
        uuid id PK
        uuid item_id FK
        uuid account_id FK
        text body
    }
    wishlist_comment_votes {
        uuid id PK
        uuid comment_id FK
        uuid account_id FK
    }
    game_khimki_runs {
        uuid id PK
        uuid account_id FK
        text game_key
        text character_key
        boolean success
        integer steps
    }
    game_assets {
        text game_key PK
        text art_key PK
        text content_type
        bytea bytes "the art image itself"
    }
    app_settings {
        text key PK
        text value
    }
```

`game_assets` and `app_settings` stand apart — neither references an account. The art bytes live in Postgres, not in git and not in the binary. See [§8 → ADR-026](#adr-026--game-art-lives-in-postgres-not-in-git-or-the-binary) for why (that record predates the rename and names the table `game_assets`; [ADR-030](#adr-030--game-modules-are-named-gamename) is the amendment).

`game_khimki_runs` and `game_assets` belong to **«Смолтолк в Химках»**, and now say so — they were `game_runs` and `game_assets` until `migrations/007_game_khimki_rename.sql`. A second game gets its own `game_<name>_*` tables rather than rows in these — see [§8 → ADR-028](#adr-028--games-are-self-contained-modules) and [ADR-030](#adr-030--game-modules-are-named-gamename). Their `game_key` **values** did not move with the tables: the column still reads `smalltalk_khimki`, because it is data rather than a name and the art blobs are keyed on it.

## 5. API map

Everything is under `/api`, authenticated by the session cookie. `GET /healthz` sits outside it (the deploy gate polls it).

| Group | Endpoints | Access |
|---|---|---|
| `auth` | `GET vk/state` · `POST vk/callback` · `GET me` · `POST logout` | public (30/min per IP on the VK pair) |
| `wishlist` | `GET/POST items` · `DELETE items/{id}` · `POST/DELETE items/{id}/vote` · `GET/POST items/{id}/comments` · `DELETE comments/{id}` · `POST/DELETE comments/{id}/vote` | approved |
| `game-khimki` | `GET assets/{game}/{key}` | **public** (art, cacheable) |
| `game-khimki` | `GET config` · `POST attempt` (5/min per IP — paid) · `POST runs` · `GET runs/leaderboard` · `GET runs/me` | approved |
| `admin` | `GET accounts?status=` · `POST accounts/{id}/approve` · `POST accounts/{id}/block` · `GET settings` | admin+ |
| `admin` | `POST accounts/{id}/promote` · `POST accounts/{id}/demote` · `PUT settings/open-registration` | superadmin only |
| `realtime` | `GET realtime?room=` — WebSocket upgrade | approved |

The two `game-khimki` rows are **«Смолтолк в Химках»**; a second game gets its own `/api/game-<name>/*` group rather than new keys in this one, while `realtime` is game-agnostic by design ([§8 → ADR-028](#adr-028--games-are-self-contained-modules), [ADR-030](#adr-030--game-modules-are-named-gamename)).

**`/api/game/*` no longer answers.** The pre-rename prefix was registered as a second route group on the same handlers for exactly one deploy cycle, so that a browser holding the previous SPA build in cache would not break mid-run; that cycle is over and the registration is deleted. `TestGameKhimkiLegacyPathAliasIsGone` in `test/integration/gamekhimki_test.go` now pins its **absence** — it asserts 404 rather than 401 on a gated path, because 401 would mean the route group had been re-registered and was merely refusing the request. On the client side `/app/game` redirects permanently to `/app/game-khimki`; that redirect is not an alias and stays.

Anything not matching `/api` or `/healthz` is served the embedded SPA, so client-side routes resolve on a hard refresh.

## 6. Security view

| Concern | Mechanism | Where |
|---|---|---|
| Personal data at rest | AES-256-GCM per field, per-row nonce; key from env, validated at startup | `internal/crypto`, `*_enc` columns |
| Lookup without plaintext | Deterministic `HMAC-SHA256(vk_user_id)` blind index | `accounts.vk_user_ref` |
| Sessions | 32-byte `crypto/rand` token; only its HMAC is stored; `httpOnly; Secure; SameSite=Strict` | `internal/session` |
| Authorization | `requireAuth` (status must be `approved`) → `requireAdmin` → `requireSuperadmin` | `internal/httpapi/router.go` |
| Revocation | Blocking an account deletes its sessions immediately | `internal/account`, `internal/session` |
| Rate limiting | Per client IP: 30/min login, **5/min `game-khimki/attempt`** (paid), 240/min blanket | `internal/httpapi/router.go` |
| Trusted client IP | `X-Real-IP`, honoured **only** from the loopback proxy; `X-Forwarded-For` is never trusted — see [§8 → ADR-027](#adr-027--the-client-ip-comes-from-x-real-ip-trusted-only-from-a-loopback-peer) | `internal/httpapi/middleware.go` — `clientIP` |
| Request size | 1 MiB body cap on every route | `bodyLimit` |
| Error disclosure | Stable codes + trace id; `err.Error()` never reaches a client | `internal/httpapi/respond.go` |
| Asset content type | Allowlisted image types + `nosniff` | `internal/httpapi/gamekhimki.go` — `imageContentType` |
| Consent (152-ФЗ) | Checkbox gates the VK widget; `consent_at` + `consent_version` persisted | SPA + `accounts` |
| WebSocket origin | Validated at upgrade (library default; never `InsecureSkipVerify`) — the same-origin policy does **not** apply to WebSocket | `internal/httpapi/realtime.go` |
| WebSocket frame size | `SetReadLimit(4096)` — the 1 MiB `bodyLimit` wraps `r.Body` and the hijack bypasses it | `internal/realtime/conn.go` |
| WebSocket message rate | 10/s per connection, burst 20 — the HTTP limiter fires once, at the handshake. Checked **before** the frame reaches a game, so a game inherits the bound rather than having to reimplement it | `internal/realtime/conn.go` |
| Socket identity | The account is bound at upgrade and travels to a game as `realtime.Member`; **no inbound frame has an account field**, so a payload cannot claim to be someone else | `internal/realtime/conn.go` — `readPump` |
| Identity on the wire | A broadcast roster carries a **per-process pseudonym**, never `accounts.id` — a durable cross-session handle must not be published to every other player ([ADR-037](#adr-037--one-account-is-one-entity-and-the-wire-carries-a-pseudonym)) | `internal/gamevanyagotchi` — `pseudonym` |
| Inbound payloads | Text frames only, ≤4 KiB, parsed by the owning game; anything malformed, unknown or non-finite is dropped without a reply and without a log line (a log per bad frame would be a flood lever at 10/s) | `internal/gamevanyagotchi/message.go` |
| Connection caps | 3 per account, 200 per process | `internal/realtime/hub.go` |
| Revocation on a live socket | Blocking an account revokes its sessions and closes its sockets, sending `bye` code 4001. **This in-process kick is the only path that cuts a live socket** — there is no revalidation sweep, so a session that expires on its own, or a block applied straight in the database, leaves the socket open until the peer or the process goes away. The reconnect is still refused by `requireAuth`. | `internal/httpapi/admin.go` → `Hub.KickAccount` |

## 7. Where things are written down

| Question | File |
|---|---|
| How do I work on this? Conventions, gates, workflow | `../CLAUDE.md` |
| What is the shape of the system? | this file |
| Why is it like that? Decisions and their rationale | this file, [§8](#8-decision-records-adrs) — the append-only record log |
| How do I debug, deploy, or operate it? | `RUNBOOK.md` |
| What is still to do, and the owner's private operational detail | the local living doc (`~/Desktop/psycho-space/psycho-space.md`) |

## 8. Decision records (ADRs)

The code says *what*, and comments say *why this line*. Neither says why the system is shaped the way it is, and that is exactly what gets re-derived — usually wrongly — by whoever touches the project next. Each entry below is a decision, its reasoning, and its consequence.

**The bar is high, and it is about architecture.** A record is for a decision that shapes the *system* — how it is deployed, how it stores and protects data, where a boundary between components falls, what a whole class of future change will cost. Two questions have to be answered yes: is the reasoning unrecoverable from the diff, **and** would somebody redesigning this part need to know it? "Chose server-side sessions over JWT" is a record. "Renamed a variable" is not, and neither is a tuning constant, a UI behaviour, or a bug fix in the test harness — however subtle the reasoning behind it, that reasoning belongs in a comment next to the code it governs, where it will be read by the person actually changing it.

Four records were withdrawn on 2026-07-25 for failing that bar — an animation speed, a nav-drawer flourish, a test-harness race, and a note about defensive code that was correctly absent. Each one's reasoning still exists as a comment beside the code, which is where it was always more useful. **Withdrawal leaves a permanent gap in the numbering:** a number is never reused, so every existing reference keeps meaning what it meant, and `git log` still has the withdrawn text.

Sections 1–7 above are the structural view — what the system is made of and how it behaves. The records below are the other altitude: the durable decisions that produced that shape, each with the reasoning, and where one exists the measurement or the failure that settled it. They are grouped by subject, and a record describes a decision rather than the current code, which the sections above already cover.

**Records are append-only.** Never edit an accepted record's decision or its reasoning. The whole value of the log is that it says what was decided and why *at the time*; a record that has been quietly rewritten cannot be relied on for that. A decision is revisited by adding a record, never by editing one:

- **Retired** — the decision no longer holds. Write a new record, and leave the old one's body untouched, appending `_Superseded by ADR-0NN · <date>_` to its status line.
- **Amended** — the decision still stands, but the mechanism that implements it changed. Keep the record, and append `· amended by [ADR-0NN](#anchor) — <what changed>` to its status line; the new record carries `· amends ADR-0NN` in its own. ADR-017 and ADR-018 are the worked example.

Fixing a typo or a rotted link inside a record is fine. Changing what it decided, or why, is not.

**The status vocabulary is `Accepted` and `Superseded`, and nothing else.** There is no `Proposed`: a record here is written in the same commit as the change it describes, so by the time one exists the decision has already shipped. Proposals belong in the owner's living doc.

**The date on a record is when the record was written, not when the decision was taken.** They are usually the same day, and when they are not, the record's own commit in `git log` is the authority on the former.

**The numbers are identifiers, not an ordering, and not a sequence.** A new record takes the next globally unused number whichever group it lands in, so numbering within a group is often non-sequential, and a withdrawn record leaves a hole. Both are expected; renumbering to tidy either up would break every existing reference, and a reused number would silently redirect one. Find the next number with:

```bash
grep -o 'ADR-[0-9]\{3\}' docs/ARCHITECTURE.md | sort -u | tail -1
```

Records 001–026 were written on 2026-07-25 when this log was created, from `docs/DESIGN.md`; edits made to that file before the merge are in `git log -- docs/DESIGN.md` and are not reconstructed here. Immutability applies from the merge forward.

**Division of labour with the runbook.** A record owns the durable decision and its reasoning. `RUNBOOK.md` owns the measurements and the operational economics — the game's per-turn cost above all, which is a figure to be re-measured as models and prices move, not a decision to be superseded. A fresh cost measurement is a runbook edit, not a new record here.

### 8.1 Platform and delivery

#### ADR-001 · The SPA is embedded in the Go binary

_Accepted · 2026-07-25_

`go:embed internal/web/dist` compiles the built frontend into the executable, so a release is one file. nginx does TLS, headers, and a proxy — it never serves an asset or knows a path.

_Consequence:_ a CSS-only change still rebuilds and redeploys the binary. For one box and one maintainer that is cheaper than operating a second artifact with its own cache-busting and deploy order.

#### ADR-002 · Provisioning is a one-time manual script; only the app deploys from CI

_Accepted · 2026-07-25_

`scripts/bootstrap.sh` installs Postgres, nginx, certbot, systemd units, the `deploy` user, and the CI key, then hardens SSH. It is run once, by hand, over the existing root access — and it deliberately leaves SSH listening on **both** the old and the new port so a mistake cannot lock the operator out. `scripts/harden-finalize.sh` closes the old port afterwards, once the new one is proven from a second terminal.

_Reasoning:_ the lockout-sensitive part of provisioning is exactly the part that should not run unattended from a pipeline.

#### ADR-003 · Push to `main` deploys; the gates are the safety net

_Accepted · 2026-07-25_

There is one environment (production), one maintainer, and no staging. Feature branches are optional. What keeps that safe is that the mandatory pre-commit hook and the deploy workflow run the same suite — build, lint, unit, web, both e2e suites, integration — and the deploy is followed by an external health check.

_Consequence:_ a red deploy means production is stale. That is treated as unfinished work, not as a notification.

### 8.2 Identity and personal data

#### ADR-004 · Server-side opaque sessions, not JWT

_Accepted · 2026-07-25_

A 32-byte `crypto/rand` token is delivered in an `httpOnly; Secure; SameSite=Strict` cookie; only its HMAC is stored, alongside `expires_at`.

_Reasoning:_ the allowlist needs **instant revocation** — blocking someone has to end their access now, not at the next token expiry. A stateless token cannot do that without a revocation list, which is a session table wearing a disguise.

#### ADR-005 · Personal data is encrypted at rest, and looked up through a blind index

_Accepted · 2026-07-25_

Profile fields are AES-256-GCM with a per-row nonce. Lookups (login, dedupe, allowlist) go through a deterministic `HMAC-SHA256(vk_user_id)` blind index, never plaintext and never a reversible identifier.

_Reasoning:_ 152-ФЗ minimisation, and the practical version of it — a database dump on its own should not be a list of who uses the site. The cost is that equality is the only query available on those columns, which is all the application needs.

_Consequence, learned the hard way:_ the keys are load-bearing. Rotating `APP_HMAC_KEY` breaks every blind index; losing `APP_ENC_KEY` makes stored profiles unrecoverable. A single row that cannot be decrypted makes the whole admin list fail — which is how the full-stack e2e suite caught its own environment reusing a database across runs with fresh keys.

#### ADR-006 · VK tokens are discarded after the profile fetch

_Accepted · 2026-07-25_

The code exchange happens on the server with the service token; the resulting access/refresh tokens are used once to read `user_info` and then dropped.

_Reasoning:_ we never act on the user's behalf at VK, so storing a credential that would let us is pure liability.

#### ADR-007 · A session cookie is issued even for pending and blocked accounts

_Accepted · 2026-07-25_

_Reasoning:_ the SPA needs an identity to poll `/api/auth/me` with, so a waiting user's screen comes alive the instant an admin approves them, and a blocked user gets told what happened instead of a bare login screen. Authorization is unaffected — `requireAuth` still demands `status == approved`.

#### ADR-008 · Consent is a gate, not a checkbox on a form

_Accepted · 2026-07-25_

The VK widget is not mounted until the consent box is ticked; `consent_at` and `consent_version` are recorded server-side, and the version is bumped whenever the disclosed data set changes.

_Reasoning:_ consent has to precede processing to mean anything. Mounting the widget first and recording consent afterwards would reverse that order.

### 8.3 Roles and access

#### ADR-009 · Three tiers, with promotion reserved to one of them

_Accepted · 2026-07-25_

`user < admin < superadmin`. Admins approve and block; only the superadmin promotes or demotes, and the superadmin cannot be blocked.

_Reasoning:_ the failure this prevents is an admin locking out the owner, or a mutual-demotion standoff. One unrevokable root is the simplest structure that has no such state.

#### ADR-010 · Open registration is a toggle, not a rebuild

_Accepted · 2026-07-25_

`app_settings.open_registration` auto-approves new accounts as plain users when on; existing accounts are untouched either way.

_Reasoning:_ the setting is a row read at login time and it only ever supplies the status of a **brand-new** account — the login upsert's `ON CONFLICT` clause never touches `status` or `role` — so the toggle is reversible in either direction with no migration and no redeploy, and no existing account moves because it flipped.

### 8.4 The games

Records 011–014 and 029 are all about **«Смолтолк в Химках»**, the LLM-judged game; ADR-028 and ADR-030 are the rules that govern every game — the first says a game shares nothing with another game, the second says how a game module is named so that the first is checkable with a grep. «Смолтолк в Химках» is documented at length in `RUNBOOK.md` → *Working on the game*, because most of what matters there is operational (what a failure looks like in the log, what a turn costs). The decisions worth stating as decisions:

#### ADR-011 · The judge is an LLM, and there is no mock

_Accepted · 2026-07-25_

An unconfigured LLM answers `503` rather than falling back to canned replies.

_Reasoning:_ a mock judge would be test-only code on a production path — forbidden here — and a fallback that quietly produces worse dialogue is harder to notice than an outage.

#### ADR-012 · Theme progress steers the options but never awards the win

_Accepted · 2026-07-25_

The server tracks which of the character's deep themes the conversation has genuinely opened, uses that to aim one answer slot at a still-closed theme, and marks a theme open by itself when the conversation has engaged it enough times.

_Reasoning:_ two separate failures. Steering the slot at the *last* remaining theme every turn made the conversation collapse onto one subject and the run unwinnable — measured at 15 of 20 option sets having all four options on the same topic. And making theme state the win condition would let a tampering client award itself the ending, so `achieved` stays the judge's reading of the dialogue.

#### ADR-013 · The prompt is laid out for prefix caching, and history is replayed as JSON

_Accepted · 2026-07-25_

Static system prompt → history → one volatile message last. Each past turn is replayed as the JSON object the judge returned.

_Reasoning, both measured:_ the provider bills a cached prefix at a quarter rate, and the first volatile byte invalidates everything after it — the tension value used to sit near the top of the system prompt, so nothing downstream could ever be cached, for any player. And the model imitates whatever format it sees: given prose history with a bracketed footer, it answered in prose with a bracketed footer and no JSON at all.

#### ADR-014 · The third theme is alcohol, deliberately, and must not become drugs

_Accepted · 2026-07-25_

The provider's content filter answered substance-use turns with prose instead of JSON, which players saw as an error. `TestContentAvoidsDrugFlavouredPrompts` guards the whole prompt surface against the regression.

#### ADR-028 · Games are self-contained modules

_Accepted · 2026-07-25_

Each game owns its Go package, its `<game>_*` tables, its routes, its views and its leaderboard code, and **no game imports another — not even where the code would be identical.** There is no shared games layer: no common game service, repository or table, no extracted game-UI shell, no generic board building. What is shared is *platform* — `realtime`, `session`, `account`, `crypto`, `db`, `logging`, `observability`, the `httpapi` router and middleware, and on the front end `apiFetch`, the error store, the theme and the app shell — and none of those may know that a game exists. The boundary test is blunt: deleting a game must be deleting its package, its migration, its routes and its views, and nothing else.

_Reasoning:_ these games are jokes for a small group, with a short and unpredictable life. The realistic future of any one of them is deletion, not extension — and premature sharing bills you at exactly the wrong moment, when you want something gone and find it welded to something you are keeping. A few duplicated files are far cheaper than that, so the duplication between games is the design and not debt to be cleaned up later.

_Consequence:_ the WebSocket is addressed as the game-agnostic `/api/realtime?room=…`, and a game's own message types live in that game's package and are published *through* the hub rather than added to it. `CLAUDE.md` → *Games are self-contained modules* carries the same rule as a working rule; that duplication is deliberate too, because that file has to stand on its own.

#### ADR-029 · The judge runs on DeepSeek V4 Flash

_Accepted · 2026-07-25 · amended by [ADR-030](#adr-030--game-modules-are-named-gamename) — the endpoint is now `/api/game-khimki/attempt`_

«Смолтолк в Химках» judges its turns with `deepseek-v4-flash` over the OpenAI-compatible endpoint (`PSYCHOSPACE_LLM_MODEL` carries the full folder-specific model URI), replacing `yandexgpt-5-lite` — and it runs with **`reasoning_effort: "none"`**.

_Reasoning:_ DeepSeek costs more per turn than the model it replaced, and the difference buys visibly better play — it produced the first winning run seen in any audit. Its content filter is also not the one that pushed the third theme off substance use (ADR-014), so the character can swear in character. Reasoning is off because this model bills `reasoning_content` as output, the dearest rate, and twice it spent the entire completion budget thinking and returned an empty reply — `finish_reason: length`, 1500 completion tokens, zero characters of dialogue, a turn lost and billed in full. Judging is a rule-following task, not a puzzle, so the chain of thought was buying nothing that the failure class cost. (`thinking` and `enable_thinking` are rejected by this endpoint; `reasoning_effort` is the knob it accepts.)

_Consequence:_ the `/api/game/attempt` limit was halved from 10/min to **5/min per client IP**, because a turn costs about twice what it did — and there is still no per-account cap, so one determined player remains the real cost exposure. The salvage path stays even though this model rarely returns malformed JSON, because a bad turn costs a player their move.

Per-turn economics — the price table, the current cost per turn and how it got there — stay in `RUNBOOK.md` → *Working on the game*, to be re-measured as models and prices move rather than superseded here.

#### ADR-030 · Game modules are named `Game<Name>`

_Accepted · 2026-07-25 · amends ADR-029_

Every game module carries its own name at every layer: package `internal/game<name>/`, tables `game_<name>_*`, routes `/api/game-<name>/*`, view `Game<Name>View.vue` served at `/app/game-<name>`, and any exported identifier that names the game from outside its package. Game 1 moved onto the convention from generic `game` naming in this change — `internal/gamekhimki/`, `game_khimki_runs` (`migrations/007_game_khimki_rename.sql`), `/api/game-khimki/*`, `GameKhimkiView.vue` at `/app/game-khimki` — and game 2 is `gamevanyagotchi` throughout. **Shared infrastructure is deliberately not prefixed:** `realtime`, `session`, `account`, `crypto`, `db`, `logging`, `observability`, `httpapi`.

_Reasoning:_ ADR-028 makes deleting a game the design centre, and its boundary test — "removing a game is removing its package, its migration, its routes and its views, and nothing else" — was a judgement call that required knowing the codebase. Spelling the name out at every layer turns that test into a command: `git grep -il game<name>` enumerates the module, across Go, SQL, routes and the SPA, for someone who has never read it. The check also runs in the other direction, which is the more valuable half: if that list ever contains a file another game needs, the boundary is *already* broken and the grep has just said so. Generic `game` naming could not do either — it matched the platform, the other game, and the word "game" in prose.

The unprefixed platform names are load-bearing, not an omission. The *absence* of a game's name is the signal that a module is game-agnostic, which is why the socket is addressed `/api/realtime?room=…` rather than per-game and why game-specific message types live in the game's package rather than in `realtime` (ADR-028, ADR-016). Prefixing one of those would be a lie, and dropping the prefix from a game module would erase the signal. `wishlist` and `settings` are a third class — non-game **sections**, neither games nor platform — and stay unprefixed too; this convention is about game modules, not about every domain package.

_The one exception is inside a game's own package,_ where Go convention wins: `GameKhimkiService` in package `gamekhimki` stutters, `revive` flags it, and the linter is mandatory. Types inside a game package therefore keep plain names and read as `gamekhimki.Service` at the call site, where the package qualifier already carries the prefix. The prefix belongs to the package and to every layer outside it.

_Consequence:_ `game_key` **values** did not move with the table — `smalltalk_khimki` is data, already unambiguous, and the art blobs are keyed on it. `/api/game/*` is kept as an alias for exactly one deploy cycle so a stale SPA does not break mid-run, and `/app/game` redirects permanently. One earlier record names an identifier this rename retired and is amended rather than edited: ADR-029's `/api/game/attempt` is now `/api/game-khimki/attempt`. That decision stands exactly as written — only the name changed.

_What this rule does **not** reach_ is the asset blob store, which is shared infrastructure rather than any game's property — see ADR-031. Only the game's own *state* moved namespace.

#### ADR-031 · Game asset storage is shared infrastructure, not a game's property

_Accepted · 2026-07-25_

Art blobs live in one unprefixed `game_assets` table, read through one unprefixed package (`internal/gameassets`) and served from one game-agnostic route (`GET /api/game-assets/{game}/{key}`). A game supplies its own `game_key` and nothing else. Migration 007 therefore renamed `game_runs` to `game_khimki_runs` but deliberately left `game_assets` alone.

_Reasoning:_ ADR-028 refuses shared code between games, and the first pass at ADR-030 applied that to the asset table too — which was wrong, and the schema had already said so: `game_assets` has carried a `game_key` discriminator since migration 006, so it was always a multi-game store. Making it per-game would have thrown that away and duplicated the blob query, the content-type allowlist and the caching handler once per game.

The line ADR-028 was missing, and which this record supplies, is **rule versus mechanism**. A game's *state* is a rule of that game — its runs, its scores, its pet, its world objects — and sharing it couples two games' lifecycles, which is exactly what ADR-028 forbids. Storing bytes under a key and serving them with a validated content type is a *mechanism* any game needs and none of them defines. The test to apply at the next boundary decision: **does it encode a rule of this game, or is it a capability any game would want?** Assets are a capability. A decay rate is a rule.

_Consequence:_ adding art for a new character, NPC or location is an upload against an existing table — no migration, no new route, no serving code, and no schema change per game. The dependency runs one way only: `gamekhimki` declares a narrow `AssetPresence` interface that the shared service satisfies, so a game depends on infrastructure and infrastructure never learns a game exists (ADR-028). The store being shared is also why its content-type allowlist matters more than it looks: it is one control protecting every game's origin at once.

_Note the asymmetry is deliberate and will read as inconsistent at a glance:_ two tables created in adjacent migrations, one renamed per-game and one not. The reason is above, and the distinction is worth more than the symmetry.

### 8.5 Realtime

#### ADR-015 · WebSocket, in the same binary, with an in-memory hub

_Accepted · 2026-07-25_

There is one process, so the hub is a map guarded by a single goroutine and messages reach every client in the room in microseconds. Presence lives only in memory, because it is meaningless after a restart — persisting it would let it lie.

_Reasoning for WebSocket over SSE:_ at this scale neither latency nor efficiency decides it. What decides it is that every client→server action over SSE is a fresh HTTP request through the blanket per-IP limiter — and that limiter is the same mechanism protecting the paid LLM endpoint. Loosening it for chat-frequency traffic would be loosening exactly the wrong thing. A WebSocket spends one token at the handshake and then bounds itself.

#### ADR-016 · No realtime message may reach the LLM

_Accepted · 2026-07-25_

_Reasoning:_ the LLM is the only paid dependency, and its cost is currently bounded by human turn-taking behind a 5/min per-IP limit. A broadcast or a timer can multiply one player's action into many calls, which is unbounded in a way the first game never was. If a feature needs the judge, it goes through the existing HTTP endpoint. This is written in the package doc comment because it is the sort of rule that erodes silently.

#### ADR-017 · Shutdown drains the hub before the HTTP server

_Accepted · 2026-07-25 · amended by [ADR-018](#adr-018--the-close-reason-travels-as-a-frame-not-as-a-close-code) — the drain delivers its reason as a `bye` frame, not as close code 1001 (`aec6b63`)_

_Reasoning:_ `http.Server.Shutdown` does not close or wait for hijacked connections — its own documentation says so. This service restarts on every deploy, several times a day, so without an explicit drain each one would reset every player's socket with no warning at all. Draining first gives every connected client a reason before the socket goes away, which is what lets it distinguish a planned restart from a network failure and reconnect promptly instead of backing off.

#### ADR-018 · The close *reason* travels as a frame, not as a close code

_Accepted · 2026-07-25 · amends ADR-017_

A server-initiated close sends one last text frame — `{"t":"bye","code":1001,"reason":"restart"}` — immediately before dropping the socket. The transport close itself stays abrupt, so a browser reports `1006 / wasClean:false` for every disconnect. The client branches on the `code` in that frame, not on `CloseEvent.code`.

_Reasoning:_ emitting a real close code means calling the library's `Conn.Close`, and that runs a full close handshake: a 5 s write, then a 5 s wait for the peer's reply which needs the read lock our own read pump is already holding while blocked in `Read`, then a join bounded by a 15 s timer. That is seconds of stall on the two paths that must never stall — the single hub goroutine, which would freeze the whole room, and a shutdown drain budgeted at 5 s for every connection at once. The unexported `writeClose`, which would emit the code without waiting, is not reachable. So the choice is between a code that arrives late enough to hurt and a frame that arrives on time; the frame wins, and it can carry more than a number.

_Nothing safety-critical rests on that frame arriving._ Blocking an account also revokes its sessions, so its reconnect is refused by `requireAuth` with a 401 before any upgrade — and **that HTTP status, not the frame, is what the client treats as terminal.** The `bye` only makes the stop immediate.

#### ADR-019 · The read pump must not observe shutdown

_Accepted · 2026-07-25_

Reads run on `context.WithoutCancel`, so cancelling the hub context does not cancel them.

_Reasoning:_ `coder/websocket`'s `setupReadTimeout` installs a `context.AfterFunc` on the read context that calls `c.close()` when it fires. So a read whose context is cancelled does not merely return an error — **it tears down the whole connection.** Handing it the hub context meant that on every deploy the read pump destroyed the socket before the write pump could say why, silently degrading the most common disconnect in production into an unexplained network error. The loop still always terminates, because every path out of `Serve` calls `hardClose` and that makes the read fail.

_Recorded because it is invisible in the API:_ nothing in `Read`'s signature suggests the context outlives the call, and the first version of this code passed its own test by winning a goroutine race. The regression test now inserts a deliberate gap between the cancellation and the close request so it cannot pass by luck.

#### ADR-033 · A game reads the socket through a game-agnostic `Handler`, and pulls presence

_Accepted · 2026-07-25 · amended by [ADR-037](#adr-037--one-account-is-one-entity-and-the-wire-carries-a-pseudonym) — the roster identifies an entity per **account**, under a derived pseudonym, and a third seam (`PublishTo`) was added_

`internal/realtime` gained exactly two seams: a `Handler` interface (`HandleInbound(ctx, Member, room, payload)`) called on the connection's own read pump, and `Hub.Members(ctx, room) []Member`. The handler is supplied by the composition root through `httpapi.Deps.RealtimeHandler`, which is typed as the interface, so no platform file names a game. «Ванягоччи» owns its own wire types in its own package and publishes through `Hub.Publish`.

_Reasoning:_ before this, the read pump discarded every payload — it read frames only to enforce the read limit and the rate limit — so no inbound message could reach any domain service at all, and the hub had no way to say who was in a room. Both were needed, and the shape of each was the decision.

**Inbound dispatch runs on the read pump, never on the hub goroutine.** That goroutine owns every room and fans out to every client; a game handler that blocked there would freeze the whole yard behind one player, which is precisely what the hub's non-blocking fanout exists to prevent. One read pump per connection means a slow handler delays only the client that sent the frame. The rate check runs *before* the handler for a second reason worth stating: it means a game inherits the socket's bound for free instead of every game having to remember to limit itself.

**Presence is pulled, not pushed.** The tempting alternative — the hub notifying a service on join and leave — makes presence a thing two components each believe they know, and the bug is then a service whose roster has quietly drifted from the hub's. Rebuilding the roster from `Members` on every broadcast cannot drift, needs no join/leave bookkeeping, and prunes departed connections by construction. It also composes with the backpressure design: a roster built from the current member set *is* idempotent full state, so a dropped frame costs nothing.

`Member` is a value type carrying a connection id and an account id, deliberately not the `Sink` — a service should be able to ask who is present without acquiring the ability to write to, or close, somebody's socket.

_Consequence:_ the roster broadcast to peers identifies entities by **connection** id, which is a per-socket UUID that means nothing once the socket is gone. Two tabs are two entities, and no durable per-person identifier is fanned out to the room — so the frame carries no personal data and needs no redaction step.

#### ADR-034 · The broadcast tick is injected, and belongs to the game

_Accepted · 2026-07-25_

`gamevanyagotchi.Service.Run(ctx, tick <-chan time.Time)` takes its tick as a parameter. `main` passes a `time.Ticker`; tests pass a channel they fire themselves. The hub has no tick of its own.

_Reasoning:_ two separate things, both load-bearing.

**It is a render tick, not the background timer this project rules out.** The rule that nothing runs on a timer is about *state*: no cron, no per-entity goroutine, nothing that writes to the database because time passed. This loop writes nothing, owns nothing and decides nothing — it reads the hub's current members and sends a snapshot. Because the frame is full state rather than a step forward from the last one, a tick that is late, early, skipped or duplicated produces the same correct frame. That property is what makes the distinction safe rather than a euphemism.

**Injecting it removes every timing sleep from the tests.** The repository has no clock injection anywhere, and determinism has so far come from substituting network dependencies. A test that fires the tick and then reads the frame it caused has no race to lose; the alternative is `time.Sleep(250ms)` in every realtime test, which is slow when it works and flaky when it does not. It is an ordinary constructor-style parameter, not test-only code on a production path — the same shape as `session.NewManager`'s injected TTL.

_Consequence:_ the rate lives in `gamevanyagotchi.BroadcastInterval` and is half of a two-part decision — the other half is the CSS transition duration on the client, chosen to be slightly longer so consecutive segments overlap. Changing one without the other makes motion either stutter or lag, which is why the constant is documented as a pair rather than exposed as a knob.

#### ADR-037 · One account is one entity, and the wire carries a pseudonym

_Accepted · 2026-07-25 · amends ADR-033_

A game's roster contains **one entity per account**, not one per connection, and the id it publishes is a **pseudonym derived per process** — `HMAC-SHA256(processKey, accountID)`, base64url, truncated — where `processKey` is 32 bytes of `crypto/rand` minted when the service is constructed and never persisted or configured. `Hub.PublishTo(ctx, connID, msg)` is added as a third game-agnostic seam so a service can answer one connection; a game learns which entity a client is by sending a hello and being told, over that unicast.

_Reasoning:_ three separate things forced it, and one mechanism settles all three.

**Signing in on a second device produced a second Ваня.** The hub allows three connections per account, and the roster was keyed by connection, so a phone and a laptop were two dots that could stand in different places and be moved independently. That is a bug about *identity*, not about presence: the hub is right that presence is per connection, and the game is what decides an account is one thing in its world. Keying the game's own state by account fixes it where the decision belongs and leaves `realtime` unchanged.

**The obvious id to use instead was the account's, and it must not be.** `accounts.id` is a durable cross-session identifier, and a roster is broadcast to everybody else in the room — so publishing it would hand every player a stable handle on every other player, permanently, for the sake of drawing a circle. The pseudonym is stable exactly as long as it needs to be (one process) and no longer, which is the same lifetime presence already has: the key dies with the process that minted it, so nothing correlates across a restart. It needs no configuration, and there is no key to rotate wrongly.

**A client cannot recognise itself from a pseudonym**, by construction — that is what makes it one. So the server has to say, and saying it to one connection rather than to the room is what `PublishTo` is for. The request arrives through the existing inbound `Handler` (the client sends a hello, the reply goes back to the connection that asked), so no join/leave lifecycle hook was needed and ADR-033's seam count grew by exactly one.

_Consequence:_ ADR-033's closing paragraph — that entities are identified by connection id, and that two tabs are two entities — no longer describes the system, and is amended rather than edited. The hub's own `Member` still carries both ids and still decides nothing; what changed is what a game does with them. A pseudonym also changes on every reconnect, so a client asks again each time it opens a socket rather than caching the answer.

_Found while fixing it:_ the connection-cap rejection never actually delivered its `bye`. `Register` runs after the 101, and the refusal path called `Conn.Close`, which only queues onto a channel that the write pump drains — and `Serve`, which starts that pump, is never reached for a refused connection. So the frame explaining "too many connections" was written to nobody and the socket was dropped bare. `Conn.Refuse` now writes it on the calling goroutine, which is safe precisely because no pump exists yet to race it.

### 8.6 Testing

#### ADR-021 · Two Playwright suites, on purpose

_Accepted · 2026-07-25_

`web/e2e/` stubs `/api` in the browser and asserts **layout** at phone widths; `web/e2e-stack/` drives the **real binary against a real PostgreSQL** and asserts that actions persisted.

_Reasoning:_ they fail for different reasons, and each is bad at the other's job. Stubbing makes awkward states (pending, blocked, a 90-character unbroken word) trivial to render and keeps the responsive matrix fast; only the real stack can prove that an upvote became a row. Both are in the pre-commit gate.

_Consequence:_ the full-stack suite runs one viewport and one worker — every project would replay the whole suite against the same database, and the first to approve the seeded pending account would leave the next with nothing to approve.

#### ADR-022 · The pre-commit hook is the gate, and it is never skipped

_Accepted · 2026-07-25_

`./dev.sh pre-commit` runs build → lint (including `golangci-lint`, pinned in `mise.toml`) → unit → web → e2e → integration → full-stack e2e. `dev.sh` re-points `core.hooksPath` on every invocation, because that setting is per-clone and a fresh clone silently has no hook.

_Reasoning:_ pushing to `main` deploys. A skipped hook is a broken production site, and `--no-verify` is forbidden for that reason. Making the linter mandatory rather than "recommended if installed" closed the gap where a finding was invisible on one machine and blocking on another.

#### ADR-023 · Tests are a deliverable, separately from the suite passing

_Accepted · 2026-07-25_

Running the existing tests green proves nothing was broken; it does not prove the change was tested. Every code-touching change extends the suite — unit tests for the logic, and an integration or e2e test when there is an end-to-end path.

### 8.7 Operations

#### ADR-024 · Errors carry a trace id, and never carry the error text

_Accepted · 2026-07-25_

Every non-2xx returns `{error: "<stable_code>", trace_id}` and every response sets `X-Trace-Id`. The SPA shows the id in a copyable modal.

_Reasoning:_ the user can report something actionable, and a support conversation never requires them to describe symptoms. Internal error text stays internal.

#### ADR-025 · Tracing is always generated; exporting is opt-in

_Accepted · 2026-07-25_

OpenTelemetry spans and trace ids exist unconditionally; export only happens if `PSYCHOSPACE_OTLP_ENDPOINT` is set.

_Reasoning:_ trace ids are the identifier above, so they cannot be conditional. A collector on a one-box deployment usually is not worth running, so exporting is the part that is optional.

#### ADR-026 · Game art lives in Postgres, not in git or the binary

_Accepted · 2026-07-25_

`game_assets` holds the image bytes; the config endpoint advertises an image URL only for keys that actually have a blob, and everything else falls back to an emoji placeholder.

_Reasoning:_ art would otherwise inflate the repository and the binary forever, and partial uploads degrade gracefully instead of producing broken images.

#### ADR-027 · The client IP comes from `X-Real-IP`, trusted only from a loopback peer

_Accepted · 2026-07-25_

`clientIP` supplies the key for every per-IP rate limit. It reads `X-Real-IP`, and **only** when the request's own TCP peer is a loopback address; a request that arrived any other way, or one whose `X-Real-IP` is missing or unparsable, is keyed by that peer address instead. `X-Forwarded-For` is never consulted, and chi's `middleware.RealIP` is deliberately not installed.

_Reasoning:_ nginx passes `X-Forwarded-For: $proxy_add_x_forwarded_for`, which *appends* the peer to whatever the client already sent, so the header's leftmost entry is attacker-controlled. `middleware.RealIP` trusted exactly that entry and overwrote `r.RemoteAddr` with it — which made every per-IP limit forgeable by varying one header per request, the login limiter and the limiter guarding the paid LLM endpoint included. `X-Real-IP` is safe in the same position for two reasons that have to hold together: nginx sets it from `$remote_addr`, overwriting whatever the client sent, and the loopback check means a value that reached the app by any other route is not believed.

_Consequence:_ the limits are only meaningful while the app sits behind that proxy, which is already the deployment (it listens on loopback). Both halves are pinned by tests, because the failure is silent: `TestClientIPTrustsProxyHeaderOnlyFromLoopback` covers the trust rule, and `TestRateLimitNotBypassableByForwardedHeader` drives a client rotating `X-Forwarded-For` and requires it to still be counted as one client.
