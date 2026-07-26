# ADR-006 · VK tokens are discarded after the profile fetch

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** VK tokens are discarded after the profile fetch
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#adr-006--vk-tokens-are-discarded-after-the-profile-fetch) — this file is the detail behind it.

---

The code exchange happens on the server with the service token; the resulting access/refresh tokens are used once to read `user_info` and then dropped.

_Reasoning:_ we never act on the user's behalf at VK, so storing a credential that would let us is pure liability.
