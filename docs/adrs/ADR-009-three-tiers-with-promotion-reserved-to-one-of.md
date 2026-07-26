# ADR-009 · Three tiers, with promotion reserved to one of them

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Three tiers, with promotion reserved to one of them
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.3](../ARCHITECTURE.md#adr-009--three-tiers-with-promotion-reserved-to-one-of-them) — this file is the detail behind it.

---

`user < admin < superadmin`. Admins approve and block; only the superadmin promotes or demotes, and the superadmin cannot be blocked.

_Reasoning:_ the failure this prevents is an admin locking out the owner, or a mutual-demotion standoff. One unrevokable root is the simplest structure that has no such state.
