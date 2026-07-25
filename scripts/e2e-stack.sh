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
#   5. supervises the server until killed — restarting it on SIGUSR1 — and tears
#      the database down on exit
#
# Restarting the server mid-suite: a test asks for one by running
# scripts/e2e-stack-restart.sh, which signals this process. That is worth
# testing because a deploy is a restart of exactly this shape and happens
# several times a day in production, and pages that were open across one have to
# recover.
#
# The restart is performed HERE, by the supervisor loop at the bottom, rather
# than by the helper script — because the encryption, blind-index and session
# keys are generated per run and exist only in this shell's environment.
# Respawning from here reuses them by construction, so every session cookie
# already handed to the tests keeps working. A helper that started the binary
# itself would have to reload those keys from somewhere, and the only somewhere
# is a file on disk holding key material. See ADR-005 for why fresh keys against
# an existing database is the exact failure this stack has already hit once.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${E2E_PORT:-8081}"
DB_PORT="${E2E_DB_PORT:-55433}"
BASE_URL="http://127.0.0.1:${PORT}"
STACK_DIR=".e2e"
SEED_FILE="web/e2e-stack/.stack.json"
SUPERVISOR_PID_FILE="$STACK_DIR/supervisor.pid"
SERVER_PID_FILE="$STACK_DIR/server.pid"

log() { echo "[e2e-stack] $*" >&2; }

server_pid=""
tmp_seed=""
cleanup() {
  local code=$?
  [ -n "$tmp_seed" ] && rm -f "$tmp_seed"
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
  # The pidfiles die with the process they name. A stale one is not harmless:
  # scripts/e2e-stack-restart.sh reads them and signals what it finds, and a pid
  # from a finished run belongs to whatever the kernel handed the number to next.
  rm -f "$SUPERVISOR_PID_FILE" "$SERVER_PID_FILE"
  log "stopping the e2e database"
  docker compose --profile e2e down --remove-orphans >/dev/null 2>&1 || true
  exit "$code"
}
trap cleanup EXIT INT TERM

# SIGUSR1 is the restart request. The handler only records it; the supervisor
# loop does the work, where it can wait for the old process to release the port
# before starting the new one.
restart_requested=0
trap 'restart_requested=1' USR1

# Written through a rename so a reader never catches a half-written number: the
# restart helper polls this file to learn that the swap has happened.
write_pid_file() { # $1 path, $2 pid
  printf '%s\n' "$2" > "${1}.tmp"
  mv -f "${1}.tmp" "$1"
}

mkdir -p "$STACK_DIR" "$(dirname "$SEED_FILE")"

# Drop any seed file an earlier run left behind, before anything can read it.
# The encryption and blind-index keys below are regenerated per run, so a stale
# file is worse than a missing one: it parses perfectly and hands the tests
# cookies this server will never accept. The same goes for a leftover pidfile.
rm -f "$SEED_FILE" "$SERVER_PID_FILE"
write_pid_file "$SUPERVISOR_PID_FILE" "$$"

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
# checked into the repo would look exactly like a leaked secret. They are
# exported once, here, and every generation of the server inherits these same
# values — a restart must never mint new ones (see the header).
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

# Appends, so a restart keeps the previous generation's output — after a restart
# test fails, the half you want to read is usually the half before the restart.
# The file is truncated once, below, at first start.
start_server() {
  "$STACK_DIR/psycho-space" >>"$STACK_DIR/server.log" 2>&1 &
  server_pid=$!
  write_pid_file "$SERVER_PID_FILE" "$server_pid"
}

wait_healthy() {
  for _ in $(seq 1 60); do
    if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then return 0; fi
    if ! kill -0 "$server_pid" 2>/dev/null; then
      log "server exited during startup:"
      tail -n 50 "$STACK_DIR/server.log" >&2
      return 1
    fi
    sleep 0.5
  done
  log "server never became healthy"
  return 1
}

# Stop the current server and block until it is really gone. The listening
# socket is released only when the process exits, so a replacement started a
# moment too early dies on "address already in use" — signalling is not enough,
# this has to wait. The budget is generous because the binary drains the
# WebSocket hub (up to 5 s) before its 15 s graceful HTTP shutdown.
stop_server() {
  kill -TERM "$server_pid" 2>/dev/null || true
  for _ in $(seq 1 300); do # 30 s
    # `wait` can itself be cut short by another trapped signal, so its returning
    # is not proof of death; `kill -0` is.
    wait "$server_pid" 2>/dev/null || true
    kill -0 "$server_pid" 2>/dev/null || return 0
    sleep 0.1
  done
  log "server ${server_pid} ignored SIGTERM — killing it"
  kill -KILL "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}

log "starting the server on ${BASE_URL} (migrations run at boot)"
: > "$STACK_DIR/server.log"
start_server
wait_healthy || exit 1

log "seeding accounts"
seed() { # $1 role, $2 status, $3 vk-id, $4 name
  go run ./cmd/dev-seed -json -role "$1" -status "$2" -vk-id "$3" -name "$4"
}
# Written to a temporary file and renamed into place, never redirected straight
# at $SEED_FILE. Playwright's readiness signal is the health endpoint above, so
# by the time this runs the suite is ALREADY executing tests — and `> $SEED_FILE`
# truncates on open and then appends one line per `go run`, roughly a second of
# progressively-invalid JSON. A test that called loginAs in that window read a
# half-written file and failed with a JSON parse error, which is what took a
# deploy red. rename(2) is atomic, so a reader now sees either no file at all or
# the finished one, and the fixture waits for it.
tmp_seed="$(mktemp "${SEED_FILE}.XXXXXX")" # same directory, so the rename is atomic
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
} > "$tmp_seed"
mv -f "$tmp_seed" "$SEED_FILE"

log "stack ready — ${BASE_URL} (accounts in ${SEED_FILE})"

# Supervise the server for the rest of the run. Nothing above this line is
# repeated on a restart, and that is the whole point: the database is not
# recreated, the accounts are not re-seeded, $SEED_FILE is not rewritten, and
# the keys are not regenerated — so every session cookie the tests are holding
# survives the restart, which is what makes the test meaningful rather than a
# test of the seeding.
while :; do
  if [ "$restart_requested" -eq 1 ]; then
    restart_requested=0
    log "restart requested — stopping server ${server_pid}"
    stop_server
    start_server
    wait_healthy || exit 1
    log "server restarted — now pid ${server_pid}"
    continue
  fi
  rc=0
  wait "$server_pid" || rc=$?
  # `wait` also returns when a trapped signal arrives, with the child still
  # alive, so the loop re-checks the flag instead of assuming the server died.
  if [ "$restart_requested" -eq 0 ]; then
    log "server exited (status ${rc})"
    exit "$rc"
  fi
done
