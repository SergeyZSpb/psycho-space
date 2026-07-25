#!/usr/bin/env bash
# Render a Markdown test + coverage summary for a GitHub Actions run.
#
# Reads whatever artifacts the preceding steps produced and degrades gracefully
# when one is missing (a failed step should still leave a readable summary, not
# break the run). Writes to stdout; the workflow appends it to
# $GITHUB_STEP_SUMMARY.
#
# Expected inputs, all optional:
#   .coverage/unit.json         gotestsum --jsonfile  (Go unit)
#   .coverage/unit.out          go test -coverprofile (Go unit)
#   .coverage/integration.json  gotestsum --jsonfile  (Go integration)
#   .coverage/integration.out   go test -coverprofile (Go integration)
#   web/coverage/coverage-summary.json  vitest --coverage (json-summary)
#   web/vitest-results.json     vitest --reporter=json
#   web/e2e-results.json        playwright --reporter=json
set -uo pipefail
cd "$(dirname "$0")/.."

have() { [ -s "$1" ]; }

# Count passed/failed tests in a gotestsum JSON stream.
go_counts() { # $1 = jsonfile -> "pass fail skip"
  if ! have "$1"; then echo "- - -"; return; fi
  jq -rs '
    [ .[] | select(.Test != null and (.Action | IN("pass","fail","skip"))) ] as $t
    | [ ($t | map(select(.Action=="pass")) | length),
        ($t | map(select(.Action=="fail")) | length),
        ($t | map(select(.Action=="skip")) | length) ]
    | @tsv' "$1" 2>/dev/null || echo "- - -"
}

go_cover() { # $1 = coverprofile -> "12.3%"
  if ! have "$1"; then echo "—"; return; fi
  go tool cover -func="$1" 2>/dev/null | tail -1 | awk '{print $NF}' || echo "—"
}

row() { printf '| %s | %s | %s | %s | %s |\n' "$1" "$2" "$3" "$4" "$5"; }

echo "## Tests"
echo
echo "| Suite | Passed | Failed | Skipped | Coverage |"
echo "|---|---:|---:|---:|---:|"

read -r up uf us <<<"$(go_counts .coverage/unit.json)"
row "Go unit" "${up:--}" "${uf:--}" "${us:--}" "$(go_cover .coverage/unit.out)"

read -r ip if_ is <<<"$(go_counts .coverage/integration.json)"
row "Go integration (testcontainers)" "${ip:--}" "${if_:--}" "${is:--}" "$(go_cover .coverage/integration.out)"

if have web/vitest-results.json; then
  read -r wp wf ws <<<"$(jq -r '[.numPassedTests, .numFailedTests, .numPendingTests] | @tsv' web/vitest-results.json)"
else
  wp="-"; wf="-"; ws="-"
fi
wcov="—"
if have web/coverage/coverage-summary.json; then
  wcov="$(jq -r '.total.lines.pct | tostring + "%"' web/coverage/coverage-summary.json)"
fi
row "Web unit (vitest)" "$wp" "$wf" "$ws" "$wcov"

pw_counts() { # $1 = playwright json report -> "pass fail skip"
  if ! have "$1"; then echo "- - -"; return; fi
  jq -r '
    [ .suites | .. | objects | select(has("results")) | .results[]? | .status ] as $r
    | [ ($r | map(select(. == "passed")) | length),
        ($r | map(select(. == "failed" or . == "timedOut")) | length),
        ($r | map(select(. == "skipped")) | length) ] | @tsv' "$1" 2>/dev/null || echo "- - -"
}

read -r ep ef es <<<"$(pw_counts web/e2e-results.json)"
row "Web UI (Playwright, stubbed /api)" "${ep:--}" "${ef:--}" "${es:--}" "—"

read -r sp sf ss <<<"$(pw_counts web/e2e-stack-results.json)"
row "Full-stack e2e (real binary + Postgres)" "${sp:--}" "${sf:--}" "${ss:--}" "—"

echo
echo "_Go coverage is reported per layer: the unit profile measures each package's own tests; the integration profile is built with \`-coverpkg=./...\`, so it shows what an end-to-end run exercises. Web coverage counts \`src/{lib,stores,api}\` — the rendered UI is verified by the Playwright suite instead._"

# Name the failures directly — clicking through to the log should be optional.
fails="$(jq -rs '.[] | select(.Action=="fail" and .Test != null) | "- `" + .Package + "` " + .Test' \
  .coverage/unit.json .coverage/integration.json 2>/dev/null | sort -u)"
if [ -n "$fails" ]; then
  echo
  echo "### Failed Go tests"
  echo
  echo "$fails"
fi
