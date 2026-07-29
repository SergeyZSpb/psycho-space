#!/usr/bin/env bash
# Scan a GitHub Actions run's logs for anything that looks like a leaked secret.
#
# THIS REPOSITORY IS PUBLIC, and so are its Actions logs. GitHub masks the
# literal value of a registered secret as `***`, but it cannot mask a value that
# has been transformed — base64-encoded, embedded in a URL, echoed by a tool
# that reformats it, or printed by `set -x` before substitution. Those are the
# leaks this looks for.
#
# Zero-arg: checks the most recent run on the default branch.
#   ./scripts/ci-check-secrets.sh              # latest run
#   ./scripts/ci-check-secrets.sh 30152599321  # a specific run id
#
# Requires `gh` authenticated through GH_TOKEN (see CLAUDE.md → Environment).
# Exits non-zero if anything matched, so it can gate a task's completion.
#
# It never prints a matched value — only the pattern that hit and where. Printing
# the finding would put the secret in a second place, which is the problem, not
# the fix.
set -uo pipefail
cd "$(dirname "$0")/.."

REPO="${PSYCHO_REPO:-SergeyZSpb/psycho-space}"
RUN_ID="${1:-}"

if ! command -v gh >/dev/null 2>&1; then
  echo "ci-check-secrets: gh is not installed — see CLAUDE.md → Environment & secrets" >&2
  exit 2
fi

if [ -z "$RUN_ID" ]; then
  RUN_ID="$(gh run list --repo "$REPO" --limit 1 --json databaseId --jq '.[0].databaseId')"
  [ -n "$RUN_ID" ] || { echo "ci-check-secrets: could not resolve the latest run" >&2; exit 2; }
fi

echo "== scanning run $RUN_ID for leaked secrets =="

raw="$(mktemp)"
log="$(mktemp)"
trap 'rm -f "$raw" "$log"' EXIT
if ! gh run view "$RUN_ID" --repo "$REPO" --log > "$raw" 2>/dev/null; then
  echo "ci-check-secrets: could not download logs for run $RUN_ID (still running?)" >&2
  exit 2
fi

# Neutralise the two things that look like secrets but are not, before matching.
# Actions echoes each `run:` script verbatim in its ##[group] header, so the log
# legitimately contains lines like `postgres://user:${DB_PW}@…` — an unexpanded
# variable, not a value — and GitHub replaces real secret values with `***`.
# Both are replaced by a space, which breaks the "looks like a credential"
# patterns below while leaving any genuine value intact. sed is line-preserving,
# so reported line numbers still match the real log.
sed -E 's/\$\{[A-Za-z_][A-Za-z0-9_]*\}/ /g; s/\$[A-Za-z_][A-Za-z0-9_]*/ /g; s/\*\*\*+/ /g' "$raw" > "$log"

# name|regex. Keep each regex specific: a false positive that cries wolf every
# run trains people to ignore the check, which is worse than not having it.
patterns=(
  'GitHub token|gh[pousr]_[A-Za-z0-9]{16,}'
  'GitHub fine-grained PAT|github_pat_[A-Za-z0-9_]{20,}'
  'Private key block|-----BEGIN [A-Z ]*PRIVATE KEY-----'
  'Postgres URL with a password|postgres://[^:]+:[^@[:space:]]{3,}@'
  'Populated app key|PSYCHOSPACE_(ENC|HMAC|SESSION)_KEY=[A-Za-z0-9+/]{16,}'
  'Populated VK service token|PSYCHOSPACE_VK_SERVICE_TOKEN=[A-Za-z0-9._-]{16,}'
  'Populated LLM API key|PSYCHOSPACE_LLM_API_KEY=[A-Za-z0-9._-]{16,}'
  'AWS-style access key|AKIA[0-9A-Z]{16}'
  'Authorization header value|[Aa]uthorization: *(Bearer|Basic) +[A-Za-z0-9._~+/-]{16,}'
)

found=0
for entry in "${patterns[@]}"; do
  name="${entry%%|*}"
  regex="${entry#*|}"
  # -e, and it is load-bearing rather than tidiness. Without it grep reads a
  # pattern that STARTS WITH A DASH as options — which the private-key pattern
  # does, `-----BEGIN … PRIVATE KEY-----` — so grep errored, `2>/dev/null` hid
  # the message, `|| true` swallowed the status, and `${hits:-0}` turned the
  # empty output into a clean zero. That check reported "no match" on every run
  # it had ever been asked about, including runs containing an actual key block,
  # and it did it silently. The most valuable pattern here was the one disabled:
  # DEPLOY_SSH_KEY is a PEM and bootstrap.sh prints one.
  #
  # A grep failure is now fatal rather than absorbed, for the same reason: a
  # security check that cannot run must say so, not pass.
  # -c only: the count and the pattern name are safe to print; the match is not.
  hits="$(grep -Ec -e "$regex" "$log")"
  rc=$?
  if [ "$rc" -gt 1 ]; then
    echo "ci-check-secrets: grep failed on pattern '$name' (exit $rc) — the check did NOT run" >&2
    exit 2
  fi
  if [ "${hits:-0}" -gt 0 ]; then
    echo "LEAK? $name — $hits matching line(s)" >&2
    # Point at the step, not the text. Step names come from the workflow and are
    # not secret; the log line itself is withheld on purpose.
    grep -En -e "$regex" "$log" 2>/dev/null | head -5 | cut -d: -f1 | while read -r n; do
      step="$(sed -n "${n}p" "$log" | cut -f1 | head -1)"
      echo "    line $n${step:+, step: $step}" >&2
    done
    found=1
  fi
done

if [ "$found" -ne 0 ]; then
  cat >&2 <<'EOF'

A pattern above matched in a PUBLIC log. Do not paste the offending line
anywhere to "check" it — open the run in the browser, confirm, and if it is a
real value then treat the secret as compromised: rotate it, delete the log
(Actions → the run → Delete), and fix whatever printed it.

Rotation notes: APP_ENC_KEY cannot be rotated in place (stored profiles become
unreadable) and APP_HMAC_KEY cannot either (every blind index breaks) — see
CLAUDE.md. Anything else (deploy key, PAT, VK service token, LLM key, DB
password) is safe to rotate.
EOF
  exit 1
fi

echo "== no secret-shaped strings found in run $RUN_ID =="
