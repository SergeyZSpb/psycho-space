# ADR-013 · The prompt is laid out for prefix caching, and history is replayed as JSON

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The prompt is laid out for prefix caching, and history is replayed as JSON
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-013--the-prompt-is-laid-out-for-prefix-caching-and-history-is-replayed-as-json) — this file is the detail behind it.

---

Static system prompt → history → one volatile message last. Each past turn is replayed as the JSON object the judge returned.

_Reasoning, both measured:_ the provider bills a cached prefix at a quarter rate, and the first volatile byte invalidates everything after it — the tension value used to sit near the top of the system prompt, so nothing downstream could ever be cached, for any player. And the model imitates whatever format it sees: given prose history with a bracketed footer, it answered in prose with a bracketed footer and no JSON at all.
