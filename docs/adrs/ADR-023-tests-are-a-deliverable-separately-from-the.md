# ADR-023 · Tests are a deliverable, separately from the suite passing

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Tests are a deliverable, separately from the suite passing
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.6](../ARCHITECTURE.md#adr-023--tests-are-a-deliverable-separately-from-the-suite-passing) — this file is the detail behind it.

---

Running the existing tests green proves nothing was broken; it does not prove the change was tested. Every code-touching change extends the suite — unit tests for the logic, and an integration or e2e test when there is an end-to-end path.
