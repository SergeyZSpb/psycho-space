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
gofmt_() { if command -v mise >/dev/null 2>&1; then mise exec -- gofmt "$@"; else gofmt "$@"; fi; }

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
  if command -v golangci-lint >/dev/null 2>&1; then
    echo "== golangci-lint =="
    golangci-lint run
  else
    echo "info: golangci-lint not installed — skipping (recommended: mise/asdf install it)" >&2
  fi
}

target_test() {
  echo "== unit tests =="
  go_ test ./...
}

target_integration() {
  if [ -d test/integration ]; then
    echo "== integration tests (testcontainers) =="
    go_ test -tags=integration ./test/integration/...
  else
    echo "info: no test/integration yet — skipping" >&2
  fi
}

target_web() {
  if [ -f web/package.json ]; then
    echo "== web (type-check + unit) =="
    ( cd web && npm_ ci --no-audit --no-fund && npm_ run type-check && npm_ run test )
  else
    echo "info: no web/ frontend yet — skipping" >&2
  fi
}

target_run() {
  [ -f .env ] && { echo "== sourcing .env =="; set -a; . ./.env; set +a; }
  go_ run ./cmd/psycho-space
}

target_pre_commit() {
  target_build
  target_lint
  target_test
  target_web
  target_integration
  echo "== pre-commit OK =="
}

case "${1:-help}" in
  build)       target_build ;;
  lint)        target_lint ;;
  test)        target_test ;;
  integration) target_integration ;;
  web)         target_web ;;
  run)         target_run ;;
  pre-commit)  target_pre_commit ;;
  help|*)
    cat <<'EOF'
psycho-space dev.sh targets:
  build        go build ./...
  lint         gofmt check + go vet (+ golangci-lint if installed)
  test         unit tests
  integration  testcontainers integration tests (when test/integration exists)
  web          frontend type-check + unit tests (when web/ exists)
  run          run the server locally (sources ./.env if present)
  pre-commit   build + lint + test + web + integration (the git hook runs this)
EOF
    ;;
esac
