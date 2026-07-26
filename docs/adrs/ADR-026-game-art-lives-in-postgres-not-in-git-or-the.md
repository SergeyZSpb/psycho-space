# ADR-026 · Game art lives in Postgres, not in git or the binary

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Game art lives in Postgres, not in git or the binary
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.7](../ARCHITECTURE.md#adr-026--game-art-lives-in-postgres-not-in-git-or-the) — this file is the detail behind it.

---

`game_assets` holds the image bytes; the config endpoint advertises an image URL only for keys that actually have a blob, and everything else falls back to an emoji placeholder.

_Reasoning:_ art would otherwise inflate the repository and the binary forever, and partial uploads degrade gracefully instead of producing broken images.
