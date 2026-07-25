#!/usr/bin/env bash
# Restart the full-stack e2e server in place and wait until it serves again.
#
# This exists for one test. Every production deploy restarts the binary, several
# times a day, and pages that were open across the restart have to notice and
# recover — which is only testable if a test can take the server away and give
# it back. From a Playwright test in web/e2e-stack/:
#
#   import { execFileSync } from 'node:child_process';
#   import { fileURLToPath } from 'node:url';
#   import { dirname, join } from 'node:path';
#
#   const script = join(dirname(fileURLToPath(import.meta.url)), '..', '..',
#                       'scripts', 'e2e-stack-restart.sh');
#   const newPid = execFileSync('bash', [script], { encoding: 'utf8', timeout: 90_000 }).trim();
#
# It prints the new server pid on stdout (progress goes to stderr) and exits 0
# once that process answers /healthz. It blocks for as long as the restart takes
# — about a second in practice, since nothing is rebuilt — and gives up at 60 s.
#
# It does not restart anything itself: it signals scripts/e2e-stack.sh, which
# owns the server process and, more to the point, owns the per-run encryption,
# blind-index and session keys in its environment. Respawning there reuses those
# keys, so the session cookies in web/e2e-stack/.stack.json stay valid across the
# restart; regenerating them against the surviving database would invalidate
# every seeded cookie and take the whole suite down with it (ADR-005).
#
# What a restart deliberately leaves alone: the Postgres container, the seeded
# rows in it, and web/e2e-stack/.stack.json.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${E2E_PORT:-8081}"
BASE_URL="http://127.0.0.1:${PORT}"
STACK_DIR=".e2e"
SUPERVISOR_PID_FILE="$STACK_DIR/supervisor.pid"
SERVER_PID_FILE="$STACK_DIR/server.pid"
TIMEOUT_S="${E2E_RESTART_TIMEOUT_S:-60}"

log() { echo "[e2e-restart] $*" >&2; }
die() { log "$*"; exit 1; }

not_running="the stack is not running — start it with ./dev.sh e2e-stack or scripts/e2e-stack.sh"
[ -f "$SUPERVISOR_PID_FILE" ] || die "no ${SUPERVISOR_PID_FILE}: ${not_running}"
[ -f "$SERVER_PID_FILE" ] || die "no ${SERVER_PID_FILE}: ${not_running}"

supervisor_pid="$(cat "$SUPERVISOR_PID_FILE")"
old_pid="$(cat "$SERVER_PID_FILE")"
kill -0 "$supervisor_pid" 2>/dev/null ||
  die "supervisor ${supervisor_pid} from ${SUPERVISOR_PID_FILE} is gone: ${not_running}"

log "asking supervisor ${supervisor_pid} to restart server ${old_pid}"
kill -USR1 "$supervisor_pid"

deadline=$(( SECONDS + TIMEOUT_S ))

# Wait for the pidfile to name a different process BEFORE checking health, and
# not the other way round. The old server keeps answering /healthz for the
# moment its graceful shutdown takes, so polling the endpoint first would report
# success without a restart having happened at all.
new_pid="$old_pid"
while [ "$new_pid" = "$old_pid" ]; do
  [ "$SECONDS" -lt "$deadline" ] || die "server pid never changed from ${old_pid} within ${TIMEOUT_S}s"
  sleep 0.1
  new_pid="$(cat "$SERVER_PID_FILE" 2>/dev/null || echo "$old_pid")"
done

until curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; do
  [ "$SECONDS" -lt "$deadline" ] || die "restarted server ${new_pid} never became healthy within ${TIMEOUT_S}s"
  kill -0 "$new_pid" 2>/dev/null || die "restarted server ${new_pid} exited — see ${STACK_DIR}/server.log"
  sleep 0.1
done

log "restarted: server ${old_pid} -> ${new_pid}, ${BASE_URL}/healthz answering again"
printf '%s\n' "$new_pid"
