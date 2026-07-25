#!/usr/bin/env bash
# Bring up the real application stack for the full-stack e2e suite and keep it
# in the foreground until killed.
#
# This is what makes web/e2e-stack/ a genuine end-to-end test: the browser talks
# to the actual Go binary (serving the embedded SPA and /api) over HTTP, and the
# binary talks to a real PostgreSQL. Nothing is stubbed. The other suite
# (web/e2e/) intercepts /api in the browser and is a responsive-layout check.
#
# Started by Playwright's `webServer` (see web/playwright.stack.config.ts), but
# it also runs standalone:  ./scripts/e2e-stack.sh
#
# What it does, in order:
#   1. starts the throwaway Postgres (compose profile `e2e`, tmpfs, port 55433)
#   2. builds the SPA into internal/web/dist and compiles the server
#   3. starts the server on E2E_PORT — it applies migrations at boot
#   4. seeds accounts (approved user, superadmin, pending, blocked) and writes
#      their session cookies to web/e2e-stack/.stack.json for the tests
#   5. waits, and tears the database down on exit
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${E2E_PORT:-8081}"
DB_PORT="${E2E_DB_PORT:-55433}"
BASE_URL="http://127.0.0.1:${PORT}"
STACK_DIR=".e2e"
SEED_FILE="web/e2e-stack/.stack.json"

log() { echo "[e2e-stack] $*" >&2; }

server_pid=""
cleanup() {
  local code=$?
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
  log "stopping the e2e database"
  docker compose --profile e2e down --remove-orphans >/dev/null 2>&1 || true
  exit "$code"
}
trap cleanup EXIT INT TERM

mkdir -p "$STACK_DIR" "$(dirname "$SEED_FILE")"

log "starting Postgres (compose profile e2e, port ${DB_PORT})"
# Force-recreate: compose would happily reuse a container left running by an
# earlier run, and its data would still be there. That matters more than usual
# here — the encryption and blind-index keys below are regenerated per run, so
# rows written by a previous run cannot be decrypted by this one, and a single
# undecryptable account is enough to make the admin list answer 500.
docker compose --profile e2e down --remove-orphans >/dev/null 2>&1 || true
docker compose --profile e2e up -d --wait --force-recreate db-e2e

log "building the SPA and the server binary"
( cd web && npm run build >/dev/null )
go build -o "$STACK_DIR/psycho-space" ./cmd/psycho-space

# Throwaway keys, generated per run: the stack is disposable, and a fixed key
# checked into the repo would look exactly like a leaked secret.
export PSYCHOSPACE_ENV=dev
export PSYCHOSPACE_HTTP_ADDR="127.0.0.1:${PORT}"
export PSYCHOSPACE_BASE_URL="$BASE_URL"
export PSYCHOSPACE_DATABASE_URL="postgres://psychospace:psychospace@127.0.0.1:${DB_PORT}/psychospace?sslmode=disable"
export PSYCHOSPACE_ENC_KEY="$(openssl rand -base64 32)"
export PSYCHOSPACE_HMAC_KEY="$(openssl rand -base64 32)"
export PSYCHOSPACE_SESSION_KEY="$(openssl rand -base64 32)"
# VK and the LLM stay unconfigured: the login flow needs the registered prod
# domain, and the game judge costs money per call. Both answer 503, which the
# e2e suite asserts rather than works around.
export PSYCHOSPACE_VK_APP_ID=""
export PSYCHOSPACE_VK_SERVICE_TOKEN=""
export PSYCHOSPACE_LLM_BASE_URL=""
export PSYCHOSPACE_LLM_API_KEY=""
export PSYCHOSPACE_LLM_MODEL=""

log "starting the server on ${BASE_URL} (migrations run at boot)"
"$STACK_DIR/psycho-space" >"$STACK_DIR/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 60); do
  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    log "server exited during startup:"
    cat "$STACK_DIR/server.log" >&2
    exit 1
  fi
  sleep 0.5
done
curl -fsS "${BASE_URL}/healthz" >/dev/null || { log "server never became healthy"; exit 1; }

log "seeding accounts"
seed() { # $1 role, $2 status, $3 vk-id, $4 name
  go run ./cmd/dev-seed -json -role "$1" -status "$2" -vk-id "$3" -name "$4"
}
{
  echo '{'
  printf '  "baseURL": "%s",\n' "$BASE_URL"
  printf '  "user": %s,\n'      "$(seed user       approved 900001 'Тест Пользователь' | tr -d '\n')"
  printf '  "superadmin": %s,\n' "$(seed superadmin approved 900002 'Сергей Зобнин'    | tr -d '\n')"
  printf '  "pending": %s,\n'   "$(seed user       pending  900003 'Ждун Ожидающий'   | tr -d '\n')"
  printf '  "blocked": %s,\n'   "$(seed user       blocked  900004 'Заблокированный'  | tr -d '\n')"
  # A second pending account so the "approve it" test and the "pending screen"
  # test never fight over the same row, whatever order they run in.
  printf '  "pending2": %s\n'   "$(seed user       pending  900005 'Ждун Второй'      | tr -d '\n')"
  echo '}'
} > "$SEED_FILE"

log "stack ready — ${BASE_URL} (accounts in ${SEED_FILE})"
wait "$server_pid"
