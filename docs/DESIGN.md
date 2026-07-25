# psycho-space — Design decisions

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** the *why* behind psycho-space — the decisions that shaped it, each with the reasoning and, where one exists, the measurement that settled it. `ARCHITECTURE.md` describes the shape; this file says why that shape.
- **status:** current as of 2026-07-25. The project is deployed and live; the game is the largest subsystem.
- **related:** `../CLAUDE.md` (rules), `ARCHITECTURE.md` (structure), `RUNBOOK.md` (operations), and the owner's local living doc for the private operational detail and the outstanding TODO list.
- **done:** every decision below is settled and implemented.
- **next:** append a section when a decision is made that a future reader would otherwise have to reverse-engineer. Amend in place when one is revisited — and say what changed it.
- **decisions / constraints:** the entries below are **settled**. Do not relitigate one without a new measurement or a new requirement; several were paid for with real failures, and a few with real money.

---

## Why this file exists

The code says *what*, and comments say *why this line*. Neither says why the system is shaped the way it is, and that is exactly what gets re-derived — usually wrongly — by whoever touches the project next. Each entry below is a decision, its reasoning, and its consequence.

The rule for this file: **an entry is added when the reasoning is not recoverable from the diff.** "Renamed a variable" is not a decision. "Chose server-side sessions over JWT" is.

## Platform and delivery

### The SPA is embedded in the Go binary

`go:embed internal/web/dist` compiles the built frontend into the executable, so a release is one file. nginx does TLS, headers, and a proxy — it never serves an asset or knows a path.

_Consequence:_ a CSS-only change still rebuilds and redeploys the binary. For one box and one maintainer that is cheaper than operating a second artifact with its own cache-busting and deploy order.

### Provisioning is a one-time manual script; only the app deploys from CI

`scripts/bootstrap.sh` installs Postgres, nginx, certbot, systemd units, the `deploy` user, and the CI key, then hardens SSH. It is run once, by hand, over the existing root access — and it deliberately leaves SSH listening on **both** the old and the new port so a mistake cannot lock the operator out. `scripts/harden-finalize.sh` closes the old port afterwards, once the new one is proven from a second terminal.

_Reasoning:_ the lockout-sensitive part of provisioning is exactly the part that should not run unattended from a pipeline.

### Push to `main` deploys; the gates are the safety net

There is one environment (production), one maintainer, and no staging. Feature branches are optional. What keeps that safe is that the mandatory pre-commit hook and the deploy workflow run the same suite — build, lint, unit, web, both e2e suites, integration — and the deploy is followed by an external health check.

_Consequence:_ a red deploy means production is stale. That is treated as unfinished work, not as a notification.

## Identity and personal data

### Server-side opaque sessions, not JWT

A 32-byte `crypto/rand` token is delivered in an `httpOnly; Secure; SameSite=Strict` cookie; only its HMAC is stored, alongside `expires_at`.

_Reasoning:_ the allowlist needs **instant revocation** — blocking someone has to end their access now, not at the next token expiry. A stateless token cannot do that without a revocation list, which is a session table wearing a disguise.

### Personal data is encrypted at rest, and looked up through a blind index

Profile fields are AES-256-GCM with a per-row nonce. Lookups (login, dedupe, allowlist) go through a deterministic `HMAC-SHA256(vk_user_id)` blind index, never plaintext and never a reversible identifier.

_Reasoning:_ 152-ФЗ minimisation, and the practical version of it — a database dump on its own should not be a list of who uses the site. The cost is that equality is the only query available on those columns, which is all the application needs.

_Consequence, learned the hard way:_ the keys are load-bearing. Rotating `APP_HMAC_KEY` breaks every blind index; losing `APP_ENC_KEY` makes stored profiles unrecoverable. A single row that cannot be decrypted makes the whole admin list fail — which is how the full-stack e2e suite caught its own environment reusing a database across runs with fresh keys.

### VK tokens are discarded after the profile fetch

The code exchange happens on the server with the service token; the resulting access/refresh tokens are used once to read `user_info` and then dropped.

_Reasoning:_ we never act on the user's behalf at VK, so storing a credential that would let us is pure liability.

### A session cookie is issued even for pending and blocked accounts

_Reasoning:_ the SPA needs an identity to poll `/api/auth/me` with, so a waiting user's screen comes alive the instant an admin approves them, and a blocked user gets told what happened instead of a bare login screen. Authorization is unaffected — `requireAuth` still demands `status == approved`.

### Consent is a gate, not a checkbox on a form

The VK widget is not mounted until the consent box is ticked; `consent_at` and `consent_version` are recorded server-side, and the version is bumped whenever the disclosed data set changes.

_Reasoning:_ consent has to precede processing to mean anything. Mounting the widget first and recording consent afterwards would reverse that order.

## Roles and access

### Three tiers, with promotion reserved to one of them

`user < admin < superadmin`. Admins approve and block; only the superadmin promotes or demotes, and the superadmin cannot be blocked.

_Reasoning:_ the failure this prevents is an admin locking out the owner, or a mutual-demotion standoff. One unrevokable root is the simplest structure that has no such state.

### Open registration is a toggle, not a rebuild

`app_settings.open_registration` auto-approves new accounts as plain users when on; existing accounts are untouched either way.

## The game

The game is documented at length in `RUNBOOK.md` → *Working on the game*, because most of what matters there is operational (what a failure looks like in the log, what a turn costs). The decisions worth stating as decisions:

### The judge is an LLM, and there is no mock

An unconfigured LLM answers `503` rather than falling back to canned replies.

_Reasoning:_ a mock judge would be test-only code on a production path — forbidden here — and a fallback that quietly produces worse dialogue is harder to notice than an outage.

### Theme progress steers the options but never awards the win

The server tracks which of the character's deep themes the conversation has genuinely opened, uses that to aim one answer slot at a still-closed theme, and marks a theme open by itself when the conversation has engaged it enough times.

_Reasoning:_ two separate failures. Steering the slot at the *last* remaining theme every turn made the conversation collapse onto one subject and the run unwinnable — measured at 15 of 20 option sets having all four options on the same topic. And making theme state the win condition would let a tampering client award itself the ending, so `achieved` stays the judge's reading of the dialogue.

### The prompt is laid out for prefix caching, and history is replayed as JSON

Static system prompt → history → one volatile message last. Each past turn is replayed as the JSON object the judge returned.

_Reasoning, both measured:_ the provider bills a cached prefix at a quarter rate, and the first volatile byte invalidates everything after it — the tension value used to sit near the top of the system prompt, so nothing downstream could ever be cached, for any player. And the model imitates whatever format it sees: given prose history with a bracketed footer, it answered in prose with a bracketed footer and no JSON at all.

### The third theme is alcohol, deliberately, and must not become drugs

The provider's content filter answered substance-use turns with prose instead of JSON, which players saw as an error. `TestContentAvoidsDrugFlavouredPrompts` guards the whole prompt surface against the regression.

## Testing

### Two Playwright suites, on purpose

`web/e2e/` stubs `/api` in the browser and asserts **layout** at phone widths; `web/e2e-stack/` drives the **real binary against a real PostgreSQL** and asserts that actions persisted.

_Reasoning:_ they fail for different reasons, and each is bad at the other's job. Stubbing makes awkward states (pending, blocked, a 90-character unbroken word) trivial to render and keeps the responsive matrix fast; only the real stack can prove that an upvote became a row. Both are in the pre-commit gate.

_Consequence:_ the full-stack suite runs one viewport and one worker — every project would replay the whole suite against the same database, and the first to approve the seeded pending account would leave the next with nothing to approve.

### The pre-commit hook is the gate, and it is never skipped

`./dev.sh pre-commit` runs build → lint (including `golangci-lint`, pinned in `mise.toml`) → unit → web → e2e → integration → full-stack e2e. `dev.sh` re-points `core.hooksPath` on every invocation, because that setting is per-clone and a fresh clone silently has no hook.

_Reasoning:_ pushing to `main` deploys. A skipped hook is a broken production site, and `--no-verify` is forbidden for that reason. Making the linter mandatory rather than "recommended if installed" closed the gap where a finding was invisible on one machine and blocking on another.

### Tests are a deliverable, separately from the suite passing

Running the existing tests green proves nothing was broken; it does not prove the change was tested. Every code-touching change extends the suite — unit tests for the logic, and an integration or e2e test when there is an end-to-end path.

## Operations

### Errors carry a trace id, and never carry the error text

Every non-2xx returns `{error: "<stable_code>", trace_id}` and every response sets `X-Trace-Id`. The SPA shows the id in a copyable modal.

_Reasoning:_ the user can report something actionable, and a support conversation never requires them to describe symptoms. Internal error text stays internal.

### Tracing is always generated; exporting is opt-in

OpenTelemetry spans and trace ids exist unconditionally; export only happens if `PSYCHOSPACE_OTLP_ENDPOINT` is set.

_Reasoning:_ trace ids are the identifier above, so they cannot be conditional. A collector on a one-box deployment usually is not worth running, so exporting is the part that is optional.

### Game art lives in Postgres, not in git or the binary

`game_assets` holds the image bytes; the config endpoint advertises an image URL only for keys that actually have a blob, and everything else falls back to an emoji placeholder.

_Reasoning:_ art would otherwise inflate the repository and the binary forever, and partial uploads degrade gracefully instead of producing broken images.
