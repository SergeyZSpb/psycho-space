# ADR-024 · Errors carry a trace id, and never carry the error text

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Errors carry a trace id, and never carry the error text
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.7](../ARCHITECTURE.md#adr-024--errors-carry-a-trace-id-and-never-carry-the-error-text) — this file is the detail behind it.

---

Every non-2xx returns `{error: "<stable_code>", trace_id}` and every response sets `X-Trace-Id`. The SPA shows the id in a copyable modal.

_Reasoning:_ the user can report something actionable, and a support conversation never requires them to describe symptoms. Internal error text stays internal.
