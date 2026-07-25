#!/usr/bin/env bash
# Static checks on the markdown docs, for two failure modes that are invisible
# locally and only surface on GitHub — or not at all.
#
# 1. Mermaid semicolons. A ";" in sequence-diagram message or note text is a
#    statement separator, not punctuation: the parser reads the rest of the line
#    as a new statement and aborts the whole diagram. Two diagrams in
#    ARCHITECTURE.md shipped broken this way, one of them for several commits,
#    because nothing renders Mermaid in the local gate and a broken block just
#    silently does not appear on the page.
#
# 2. Decision-record integrity. The records in ARCHITECTURE.md are append-only
#    and cross-reference each other by anchor, so the two things that rot are
#    numbering (a duplicated or skipped id) and links (an anchor that no longer
#    matches its heading after a retitle). Both are cheap to check and neither
#    is visible until someone clicks.
#
# Deliberately no Mermaid *rendering*: that needs a headless browser, which is
# far too slow and too fragile a dependency for a hook that runs on every
# commit. Rendering stays a manual step when a diagram changes — the recipe is
# in ARCHITECTURE.md's continuation block.
set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
note() { printf '%s\n' "$*" >&2; }

# ---------------------------------------------------------------- mermaid ----
# Extract every ```mermaid fence and reject a semicolon inside it.
mermaid_semicolons() {
  local file="$1"
  awk -v file="$file" '
    /^```mermaid[[:space:]]*$/ { inblock = 1; next }
    inblock && /^```[[:space:]]*$/ { inblock = 0; next }
    inblock && /;/ {
      printf "%s:%d: semicolon in a mermaid block breaks the diagram — use <br/> or an em dash\n", file, FNR
      printf "    %s\n", $0
      found = 1
    }
    END { exit found ? 1 : 0 }
  ' "$file"
}

echo "== docs: mermaid semicolons =="
while IFS= read -r f; do
  if ! mermaid_semicolons "$f"; then
    fail=1
  fi
done < <(git ls-files '*.md')

# ------------------------------------------------------------------- ADRs ----
# The id SET must be exactly 001..N — no duplicates, no gaps — and every in-file
# #adr- anchor must resolve to a real heading. Note what is deliberately NOT
# checked: the order ids appear in the file. Records are grouped by subject, so a
# new record takes the next global number and lands out of sequence inside its
# group. The preamble says so ("numbers are identifiers, not an ordering"), and a
# check that demanded document order would fight the structure it is guarding.
#
# Slugs follow GitHub: markdown stripped, lowercased, punctuation dropped, spaces
# to hyphens — note the "·" separator leaves a double hyphen, easy to get wrong
# by hand and invisible until someone clicks the link.
echo "== docs: decision-record integrity =="
if [ -f docs/ARCHITECTURE.md ]; then
  python3 - <<'PY' || fail=1
import re
import sys

path = "docs/ARCHITECTURE.md"
src = open(path, encoding="utf-8").read()

problems = []

ids = re.findall(r"^#### (ADR-\d{3}) ", src, re.M)
if not ids:
    print(f"{path}: no ADR records found — expected '#### ADR-0NN · title' headings",
          file=sys.stderr)
    sys.exit(1)

dupes = sorted({i for i in ids if ids.count(i) > 1})
if dupes:
    problems.append(f"duplicate record ids: {', '.join(dupes)}")

nums = sorted(int(i.split("-")[1]) for i in set(ids))
gaps = [n for n in range(1, max(nums) + 1) if n not in nums]
if gaps:
    problems.append("missing record ids: "
                    + ", ".join(f"ADR-{n:03d}" for n in gaps)
                    + " — the set must be complete from ADR-001, since a gap "
                      "means a record was deleted rather than superseded")


def slug(text):
    t = re.sub(r"`([^`]*)`", r"\1", text)
    t = re.sub(r"\*+([^*]*)\*+", r"\1", t)
    t = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", t)
    t = t.strip().lower()
    t = re.sub(r"[^a-z0-9 _\-]", "", t)
    return t.replace(" ", "-")


headings = {slug(m.group(2)) for m in re.finditer(r"^(#{1,6})\s+(.+)$", src, re.M)}
# Links inside code spans are literal template text, not real links.
outside_code = re.sub(r"`[^`]*`", "", src)
for anchor in re.findall(r"\]\(#([^)]+)\)", outside_code):
    if anchor not in headings:
        problems.append(f"dead anchor #{anchor} — no heading slugifies to it")

# A superseded or amended record must point somewhere real.
for m in re.finditer(r"^_(?:Accepted|Superseded)[^\n]*$", src, re.M):
    line = m.group(0)
    for ref in re.findall(r"\b(ADR-\d{3})\b", line):
        if ref not in ids:
            problems.append(f"status line references {ref}, which does not exist: {line[:80]}")

if problems:
    for p in problems:
        print(f"{path}: {p}", file=sys.stderr)
    sys.exit(1)

print(f"  {len(ids)} records, ids contiguous, every anchor resolves")
PY
else
  note "info: no docs/ARCHITECTURE.md — skipping record checks"
fi

if [ "$fail" -ne 0 ]; then
  note ""
  note "docs check failed. These are the two doc defects the rest of the gate cannot see:"
  note "a broken mermaid block is invisible until the page is opened on GitHub, and a dead"
  note "anchor is invisible until someone clicks it."
  exit 1
fi

echo "== docs OK =="
