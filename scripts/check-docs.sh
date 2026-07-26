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
# 2. Decision-record integrity. A record is a one-paragraph summary in
#    ARCHITECTURE.md section 8 PLUS a file under docs/adrs/, and the two halves
#    drift apart silently: a summary whose file was never written is a dead
#    link, and a file nobody summarised is a decision that cannot be found from
#    the document people actually read. Records also cross-reference each other,
#    now across files, so a retitle breaks a link in another file entirely.
#    None of it is visible until somebody clicks.
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
# Checked: no id repeats, the summary layer and docs/adrs/ describe the same set
# of records, every summary links to its own file, no record carries a
# supersession breadcrumb (they are rewritten in place now), and every anchor
# and relative link resolves.
#
# Deliberately NOT checked, for the same reason as before — the numbers are
# identifiers, not a sequence. The ORDER ids appear in is free, because records
# are grouped by subject and a new one takes the next global number wherever it
# lands. And GAPS are free, because a record withdrawn for failing the log's bar
# keeps its number retired for ever. A check demanding either would force a
# renumber, and renumbering breaks every reference that already exists — which
# is precisely what these ids are for.
#
# Slugs follow GitHub: markdown stripped, lowercased, punctuation dropped, spaces
# to hyphens — note the "·" separator leaves a double hyphen, easy to get wrong
# by hand and invisible until someone clicks the link.
echo "== docs: decision-record integrity =="
if [ -f docs/ARCHITECTURE.md ]; then
  python3 - <<'PY' || fail=1
import re
import sys
from pathlib import Path

arch = Path("docs/ARCHITECTURE.md")
adrs = Path("docs/adrs")
src = arch.read_text(encoding="utf-8")

problems = []

# Section 8 is the SUMMARY layer: one heading, one paragraph and one link per
# record. The record itself is a file under docs/adrs/. Both halves must exist
# and neither may drift from the other -- a summary with no file is a dead link,
# and a file with no summary is a decision nobody can find.
ids = re.findall(r"^#### (ADR-\d{3}) ", src, re.M)
if not ids:
    print(f"{arch}: no ADR summaries found -- expected '#### ADR-0NN . title' headings",
          file=sys.stderr)
    sys.exit(1)

dupes = sorted({i for i in ids if ids.count(i) > 1})
if dupes:
    problems.append(f"duplicate record ids in the summary layer: {', '.join(dupes)}")

# Gaps are legitimate and are NOT checked. A record withdrawn for failing the
# log's bar keeps its number retired for ever, so every reference that already
# exists keeps pointing at what it always meant. Demanding contiguity would
# force a renumber, the one operation guaranteed to break references.
files = sorted(adrs.glob("ADR-*.md")) if adrs.is_dir() else []
file_ids = {}
for f in files:
    m = re.match(r"(ADR-\d{3})-", f.name)
    if not m:
        problems.append(f"{f}: filename does not start with an ADR id")
        continue
    if m.group(1) in file_ids:
        problems.append(f"two files claim {m.group(1)}: {file_ids[m.group(1)].name}, {f.name}")
    file_ids[m.group(1)] = f

for missing in sorted(set(ids) - set(file_ids)):
    problems.append(f"{missing} is summarised in ARCHITECTURE.md but has no file in docs/adrs/")
for orphan in sorted(set(file_ids) - set(ids)):
    problems.append(f"{orphan} has a file in docs/adrs/ but no summary in ARCHITECTURE.md section 8")

# Every summary must link to its own record, or the split is a dead end.
for rid in ids:
    if rid not in file_ids:
        continue
    block = re.search(rf"^#### {rid} .*?(?=^#### |^### |^## |\Z)", src, re.M | re.S)
    if block and f"](adrs/{file_ids[rid].name})" not in block.group(0):
        problems.append(f"the {rid} summary does not link to adrs/{file_ids[rid].name}")

# The status vocabulary is `Accepted` and nothing else: a record is rewritten in
# place when the decision changes, so a supersession or amendment breadcrumb
# means somebody followed the retired append-only discipline. The history of a
# decision lives in `git log -p` on its file.
for f in files:
    text = f.read_text(encoding="utf-8")
    for bad in ("Superseded by", "amended by", "amends ADR-"):
        if bad in text:
            problems.append(f"{f}: contains '{bad}' -- records are rewritten in place now, "
                            f"and a decision's history lives in `git log -p {f}`")


def slug(text):
    r"""GitHub's heading slug, near enough for a link check.

    NOTE the Unicode class. An earlier version stripped every non-ASCII
    character, so a link to a heading containing Cyrillic -- of which this
    repository has several -- was judged dead while being perfectly correct on
    GitHub, and a link that satisfied the checker would have been broken there.
    Python 3's \w is Unicode-aware, so Cyrillic survives here exactly as it
    does in GitHub's own slugger.
    """
    t = re.sub(r"`([^`]*)`", r"\1", text)
    t = re.sub(r"\*+([^*]*)\*+", r"\1", t)
    t = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", t)
    t = t.strip().lower()
    t = re.sub(r"[^\w \-]", "", t, flags=re.UNICODE)
    return t.replace(" ", "-")


# Links in both directions: anchors against the file they live in, relative
# file links against the filesystem.
for path in [arch] + files:
    text = path.read_text(encoding="utf-8")
    headings = {slug(m.group(2)) for m in re.finditer(r"^(#{1,6})\s+(.+)$", text, re.M)}
    outside_code = re.sub(r"`[^`]*`", "", text)
    for anchor in re.findall(r"\]\(#([^)]+)\)", outside_code):
        if anchor not in headings:
            problems.append(f"{path}: dead anchor #{anchor} -- no heading in this file slugifies to it")
    for link in re.findall(r"\]\((?!https?:|#)([^)#]+?)(?:#[^)]*)?\)", outside_code):
        if not (path.parent / link).exists():
            problems.append(f"{path}: dead link {link} -- no such file")

if problems:
    for problem in problems:
        print(f"docs: {problem}", file=sys.stderr)
    sys.exit(1)

highest = max(int(i.split("-")[1]) for i in ids)
print(f"  {len(ids)} records, highest ADR-{highest:03d}, summary layer and docs/adrs/ agree, every link resolves")
PY
else
  note "info: no docs/ARCHITECTURE.md -- skipping record checks"
fi

if [ "$fail" -ne 0 ]; then
  note ""
  note "docs check failed. These are the two doc defects the rest of the gate cannot see:"
  note "a broken mermaid block is invisible until the page is opened on GitHub, and a dead"
  note "anchor is invisible until someone clicks it."
  exit 1
fi

echo "== docs OK =="
