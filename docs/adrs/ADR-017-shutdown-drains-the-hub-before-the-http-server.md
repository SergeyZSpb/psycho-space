# ADR-017 · Shutdown drains the hub before the HTTP server

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Shutdown drains the hub before the HTTP server
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.5](../ARCHITECTURE.md#adr-017--shutdown-drains-the-hub-before-the-http-server) — this file is the detail behind it.
- **related:** [ADR-018](./ADR-018-the-close-reason-travels-as-a-frame-not-as-a.md)

---

_Reasoning:_ `http.Server.Shutdown` does not close or wait for hijacked connections — its own documentation says so. This service restarts on every deploy, several times a day, so without an explicit drain each one would reset every player's socket with no warning at all. Draining first gives every connected client a reason before the socket goes away, which is what lets it distinguish a planned restart from a network failure and reconnect promptly instead of backing off.
