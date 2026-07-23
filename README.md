# psycho-space

это супер нейрослоп приложулька оххх оххх психоспасе

A small Russian-language landing page + allowlist-gated community app. Login via **VK ID**. First app section: a wishlist with upvotes (more to come). Go backend + Vue SPA (embedded in one binary) + a single Ubuntu box behind nginx.

> **Details live in [`CLAUDE.md`](./CLAUDE.md)** (project rules, conventions, security posture) and the canonical living doc at `~/Desktop/psycho-space.md` (design, rollout, owner TODO). This README only covers getting started.

## Quick start (local)

Prereqs: [mise](https://mise.jdx.dev/) and Docker (for a local Postgres).

```bash
mise install                     # installs Go + Node per mise.toml
cp .env.example .env             # then fill the three keys (openssl rand -base64 32)
docker run -d --name psycho-pg -e POSTGRES_USER=psychospace \
  -e POSTGRES_PASSWORD=psychospace -e POSTGRES_DB=psychospace \
  -p 5432:5432 postgres:16
./dev.sh run                     # serves http://localhost:8080
```

## Common commands

```bash
./dev.sh build         # go build ./...
./dev.sh test          # unit tests
./dev.sh integration   # testcontainers integration tests (when present)
./dev.sh web           # frontend type-check + unit tests (when web/ present)
./dev.sh lint          # gofmt + go vet (+ golangci-lint if installed)
./dev.sh pre-commit    # the full gate the git hook runs
```

The pre-commit hook is wired automatically the first time you run any `./dev.sh` command. Never bypass it.

## Deploy

Push to `main` → GitHub Actions deploys to the production box over SSH. First-time server provisioning is a one-time manual step — see the living doc's TODO section and `scripts/bootstrap.sh`.

## Ops / debugging

See [`docs/RUNBOOK.md`](./docs/RUNBOOK.md).
