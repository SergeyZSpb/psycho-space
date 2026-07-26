# ADR-003 · Push to `main` deploys; the gates are the safety net

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Push to main deploys; the gates are the safety net
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.1](../ARCHITECTURE.md#adr-003--push-to-main-deploys-the-gates-are-the-safety-net) — this file is the detail behind it.

---

There is one environment (production), one maintainer, and no staging. Feature branches are optional. What keeps that safe is that the mandatory pre-commit hook and the deploy workflow run the same suite — build, lint, unit, web, both e2e suites, integration — and the deploy is followed by an external health check.

_Consequence:_ a red deploy means production is stale. That is treated as unfinished work, not as a notification.
