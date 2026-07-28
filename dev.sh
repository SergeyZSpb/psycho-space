#!/usr/bin/env bash
# Developer entrypoint: build / lint / test / run, plus the pre-commit gate.
# Usage: ./dev.sh <target>   (see `./dev.sh help`)
set -euo pipefail
cd "$(dirname "$0")"

# -----------------------------------------------------------------------------
# Mandatory local pre-commit hook auto-activation (idempotent).
# core.hooksPath is per-clone git config (not committed); a fresh clone has it
# unset, which silently disables the hook. Wire it on every dev.sh invocation so
# the gate (build -> lint -> unit -> web -> integration) always runs before push.
# No-op once configured.
if command -v git >/dev/null 2>&1 && { [ -d .git ] || git rev-parse --git-dir >/dev/null 2>&1; }; then
  if [ "$(git config --get core.hooksPath 2>/dev/null || true)" != ".githooks" ]; then
    git config core.hooksPath .githooks
    echo "info: enabled .githooks (pre-commit hook now active)" >&2
  fi
  if [ -f .githooks/pre-commit ] && [ ! -x .githooks/pre-commit ]; then
    chmod +x .githooks/pre-commit
    echo "info: made .githooks/pre-commit executable" >&2
  fi
fi
# -----------------------------------------------------------------------------

# Route go/npm through mise so versions match mise.toml when mise is present.
go_()    { if command -v mise >/dev/null 2>&1; then mise exec -- go    "$@"; else go    "$@"; fi; }
npm_()   { if command -v mise >/dev/null 2>&1; then mise exec -- npm   "$@"; else npm   "$@"; fi; }
npx_()   { if command -v mise >/dev/null 2>&1; then mise exec -- npx   "$@"; else npx   "$@"; fi; }
gofmt_() { if command -v mise >/dev/null 2>&1; then mise exec -- gofmt "$@"; else gofmt "$@"; fi; }
golangci_() {
  if command -v mise >/dev/null 2>&1 && mise which golangci-lint >/dev/null 2>&1; then
    mise exec -- golangci-lint "$@"
  elif command -v golangci-lint >/dev/null 2>&1; then
    golangci-lint "$@"
  else
    echo "golangci-lint not found — it is pinned in mise.toml; run 'mise install'." >&2
    return 1
  fi
}

target_build() {
  echo "== build =="
  go_ build ./...
}

target_lint() {
  echo "== lint (gofmt + go vet) =="
  local unformatted
  unformatted="$(gofmt_ -l . 2>/dev/null || true)"
  if [ -n "$unformatted" ]; then
    echo "gofmt: these files need formatting:" >&2
    echo "$unformatted" >&2
    return 1
  fi
  go_ vet ./...
  echo "== golangci-lint =="
  golangci_ run ./...
  # Two doc defects the rest of the gate is blind to: a mermaid block broken by
  # a semicolon (invisible until the page is opened on GitHub) and a decision
  # record with a dead anchor or a duplicate id. Both shipped to main before
  # this check existed. Pure text inspection, so it costs milliseconds.
  ./scripts/check-docs.sh
}

# Every test target takes arguments, and passing some narrows the run: while
# developing you are meant to run the handful of tests that could plausibly fail
# (`./dev.sh test -run TestVKRedirect ./internal/httpapi/`), not the whole suite
# — the pre-commit gate runs everything exactly once, and a full manual run
# before it is the same minutes spent twice. Bare, each target still runs its
# whole suite, which is what the gate does.
target_test() {
  echo "== unit tests =="
  if [ "$#" -gt 0 ]; then go_ test "$@"; else go_ test ./...; fi
}

target_integration() {
  if [ -d test/integration ]; then
    echo "== integration tests (testcontainers) =="
    if [ "$#" -gt 0 ]; then
      go_ test -tags=integration "$@"
    else
      go_ test -tags=integration ./test/integration/...
    fi
  else
    echo "info: no test/integration yet — skipping" >&2
  fi
}

target_web() {
  if [ -f web/package.json ]; then
    # Arguments mean a targeted vitest run — the type-check is the whole
    # frontend either way, so it belongs to the full pass, not to this one.
    if [ "$#" -gt 0 ]; then
      echo "== web (vitest: $*) =="
      ( cd web && [ -d node_modules ] || npm_ ci --no-audit --no-fund )
      ( cd web && npx_ vitest run "$@" )
      return
    fi
    echo "== web (type-check + unit) =="
    ( cd web && npm_ ci --no-audit --no-fund && npm_ run type-check && npm_ run test )
  else
    echo "info: no web/ frontend yet — skipping" >&2
  fi
}

# Playwright mobile-layout suite. Part of the pre-commit gate: the mobile-first
# rule in CLAUDE.md is only real if something enforces it, and the whole suite
# runs in seconds once the browser is cached. Everything runs at 360px; the
# desktop project runs only the `@wide` tests, which are the ones whose claim is
# about width (see the header of web/playwright.config.ts). The browser download
# is a one-off (`npx playwright install chromium`); every project in
# playwright.config.ts is Chromium, so nothing else is fetched.
target_e2e() {
  if [ ! -f web/playwright.config.ts ]; then
    echo "info: no web/playwright.config.ts — skipping e2e" >&2
    return 0
  fi
  echo "== e2e (Playwright: 360px in full, desktop for @wide) =="
  ( cd web && [ -d node_modules ] || npm_ ci --no-audit --no-fund )
  if ! ( cd web && npx_ playwright test "$@" ); then
    echo "e2e failed. If the browser is missing, run: (cd web && npx playwright install chromium)" >&2
    return 1
  fi
}

# Full-stack e2e: the browser drives the real binary against a real Postgres
# (scripts/e2e-stack.sh brings the stack up and seeds accounts). Needs Docker —
# the same dependency the testcontainers integration tests already have.
target_e2e_stack() {
  if [ ! -f web/playwright.stack.config.ts ]; then
    echo "info: no web/playwright.stack.config.ts — skipping full-stack e2e" >&2
    return 0
  fi
  # THIS SUITE'S ONLY ISOLATION IS THAT IT RUNS ONE TEST AT A TIME. There is one
  # database, one server, and five fixed seeded accounts that every spec mutates
  # — `DELETE FROM game_vanyagotchi_world_objects`, delete the pet, rewrite its
  # stats — none of it scoped to a test. `workers: 1` in the config is what makes
  # that safe, and since arguments here are forwarded straight to Playwright,
  # `--workers=4` would quietly override it and produce failures that look like
  # bugs in the code under test. Refused loudly instead.
  for arg in "$@"; do
    case "$arg" in
      --workers|--workers=*|-j|--fully-parallel)
        echo "error: $arg would defeat this suite's only isolation — one DB, one server," >&2
        echo "       five shared accounts, no per-test scoping. See web/playwright.stack.config.ts." >&2
        return 1
        ;;
    esac
  done
  echo "== e2e full-stack (real binary + Postgres) =="
  ( cd web && [ -d node_modules ] || npm_ ci --no-audit --no-fund )
  ( cd web && npx_ playwright test --config=playwright.stack.config.ts "$@" )
}

# Coverage for both Go layers. Kept separate rather than merged: the unit
# profile measures the packages' own tests, the integration profile is built
# with -coverpkg=./... so it reports what an end-to-end run actually exercises.
target_cover() {
  echo "== coverage (Go unit) =="
  mkdir -p .coverage
  go_ test -covermode=atomic -coverprofile=.coverage/unit.out ./...
  go_ tool cover -func=.coverage/unit.out | tail -1
  if [ -d test/integration ]; then
    echo "== coverage (Go integration) =="
    go_ test -tags=integration -covermode=atomic -coverpkg=./... \
      -coverprofile=.coverage/integration.out ./test/integration/...
    go_ tool cover -func=.coverage/integration.out | tail -1
  fi
  if [ -f web/package.json ]; then
    echo "== coverage (web) =="
    ( cd web && npm_ ci --no-audit --no-fund && npm_ run test:coverage )
  fi
}

target_run() {
  [ -f .env ] && { echo "== sourcing .env =="; set -a; . ./.env; set +a; }
  go_ run ./cmd/psycho-space
}

# Seed a local approved account + session and print the cookie, so the gated
# /app/* area opens locally without the real VK login. Dev only. Extra args pass
# through (e.g. -role user -name Гость).
target_seed() {
  [ -f .env ] && { echo "== sourcing .env =="; set -a; . ./.env; set +a; }
  go_ run ./cmd/dev-seed "$@"
}

target_db_up() {
  echo "== docker compose up -d db =="
  docker compose up -d db
}

target_db_down() {
  echo "== docker compose down (data volume kept) =="
  docker compose down
}

target_pre_commit() {
  target_build
  target_lint
  target_test
  target_web
  target_e2e
  target_integration
  target_e2e_stack
  echo "== pre-commit OK =="
}

case "${1:-help}" in
  build)       target_build ;;
  lint)        target_lint ;;
  test)        shift || true; target_test "$@" ;;
  integration) shift || true; target_integration "$@" ;;
  web)         shift || true; target_web "$@" ;;
  e2e)         shift || true; target_e2e "$@" ;;
  e2e-stack)   shift || true; target_e2e_stack "$@" ;;
  cover)       target_cover ;;
  run)         target_run ;;
  seed)        shift; target_seed "$@" ;;
  db-up)       target_db_up ;;
  db-down)     target_db_down ;;
  pre-commit)  target_pre_commit ;;
  help|*)
    cat <<'EOF'
psycho-space dev.sh targets:
  build        go build ./...
  lint         gofmt check + go vet (+ golangci-lint if installed)
  test         unit tests (args narrow it: -run TestX ./internal/pkg/)
  integration  testcontainers integration tests (args pass through)
  web          frontend type-check + unit tests (args = targeted vitest run)
  e2e          Playwright mobile-layout suite, stubbed /api (args pass through)
  e2e-stack    Playwright full-stack e2e: real binary + Postgres (needs Docker)
  cover        coverage: Go unit + Go integration + web
  run          run the server locally (sources ./.env if present)
  seed         seed a local approved account + session, print the cookie (dev; args pass through)
  db-up        start the local Postgres (docker compose up -d db)
  db-down      stop the local Postgres (keeps the data volume)
  pre-commit   build + lint + test + web + e2e + integration + e2e-stack (the git hook runs this)
EOF
    ;;
esac
