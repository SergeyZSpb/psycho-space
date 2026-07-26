# ADR-014 · The third theme is alcohol, deliberately, and must not become drugs

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The third theme is alcohol, deliberately, and must not become drugs
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-014--the-third-theme-is-alcohol-deliberately-and-must-not-become-drugs) — this file is the detail behind it.

---

The provider's content filter answered substance-use turns with prose instead of JSON, which players saw as an error. `TestContentAvoidsDrugFlavouredPrompts` guards the whole prompt surface against the regression.
