# psycho-space

это супер нейрослоп приложулька оххх оххх психоспасе

A small Russian-language landing page + allowlist-gated community app. Login via **VK ID**. Inside: a **wishlist** with upvotes and threaded upvotable comments, and **«Смолтолк в Химках»**, a game where an LLM judges your attempt to talk your way past дядя Ваня. More sections to come.

One Go binary (with the Vue SPA compiled into it) + PostgreSQL + nginx on a single Ubuntu box. Push to `main` deploys it.

> **Where things are written down:** [`CLAUDE.md`](./CLAUDE.md) — working rules, conventions, the environment and secrets you need, and the task protocol · [`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) — the structure as Mermaid diagrams, and §8 the numbered decision records saying why it is built this way · [`docs/RUNBOOK.md`](./docs/RUNBOOK.md) — operating and debugging it. The owner's living doc (roadmap, TODO, private operational detail) is local and uncommitted, at `~/Desktop/psycho-space/psycho-space.md`.

## Quick start (local)

Prereqs: [mise](https://mise.jdx.dev/) and Docker.

```bash
mise install                 # Go, Node and golangci-lint, versions per mise.toml
cp .env.example .env         # fill the three keys: openssl rand -base64 32
./dev.sh db-up               # local Postgres (docker compose)
./dev.sh run                 # http://localhost:8080 — API + the embedded SPA
```

For frontend work, run the Vite dev server alongside it — it hot-reloads and proxies `/api` to :8080:

```bash
cd web && mise exec -- npm run dev    # http://localhost:5173
```

**Getting into the gated area locally.** VK ID is IP-allowlisted to the production host and its redirect URI is the production domain, so the real login cannot run on a workstation. Seed an account instead:

```bash
./dev.sh seed                          # superadmin
./dev.sh seed -role user -name Гость   # a plain approved user
```

It prints a `psycho_session` cookie value — set it for the origin you are using and reload. Full recipe in [`docs/RUNBOOK.md`](./docs/RUNBOOK.md).

## Commands

```bash
./dev.sh build         # go build ./...
./dev.sh lint          # gofmt + go vet + golangci-lint (all mandatory)
./dev.sh test          # Go unit tests
./dev.sh integration   # testcontainers integration tests (needs Docker)
./dev.sh web           # frontend type-check + vitest
./dev.sh e2e           # Playwright at mobile viewports, /api stubbed
./dev.sh e2e-stack     # Playwright against the real binary + Postgres (needs Docker)
./dev.sh cover         # coverage for all of the above
./dev.sh pre-commit    # the full gate the git hook runs
./dev.sh db-up|db-down # local Postgres
```

The pre-commit hook is wired automatically the first time you run any `./dev.sh` command, and **is never bypassed** — `--no-verify` is forbidden. Pushing to `main` deploys to production, so the gate is the only thing between a mistake and the live site.

## Tests

Four suites, each with a different job:

| Suite | What it proves | Where |
|---|---|---|
| Go unit | the logic is right | `internal/*/[a-z]*_test.go` |
| Integration (testcontainers) | the API works against a real Postgres, with a fake VK server | `test/integration/` |
| Playwright — layout | nothing overflows and every tap target is reachable at 360/390/768 px | `web/e2e/` (`/api` stubbed in the browser) |
| Playwright — full stack | an action actually persisted — real binary, real database | `web/e2e-stack/` |

Every code change is expected to grow one of them; see *Tests are a deliverable* in [`CLAUDE.md`](./CLAUDE.md).

First Playwright run needs the browser once: `(cd web && npx playwright install chromium)`.

## Deploy

Push to `main` → `.github/workflows/deploy.yml` runs the whole suite, builds the binary with the SPA embedded, ships it over SSH, migrates, restarts, and health-checks the live site. Non-`main` branches run `ci.yml` — same tests, no deploy.

Both workflows publish a **test + coverage summary** to the run's job summary and upload the Playwright videos. Watch a run and read what it produced rather than trusting the green tick:

```bash
gh run list --limit 1
gh run watch <run-id> --exit-status
gh run view  <run-id>                     # jobs; the summary has the test/coverage table
./scripts/ci-check-secrets.sh <run-id>    # this repo is public — the logs are too
```

Server provisioning is a separate one-time manual step (`scripts/bootstrap.sh`), not part of CI.

## Security posture

It is a small app, but it handles real personal data under Russian personal-data law (152-ФЗ): profile fields are encrypted at rest, looked up through an HMAC blind index rather than in plaintext, and consent is captured before anything is processed. Secrets live only in GitHub Actions environment secrets and in a root-only file on the server — never in this repository, and never in a log. See [`CLAUDE.md`](./CLAUDE.md) and [`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md).
