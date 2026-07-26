# ADR-010 · Open registration is a toggle, not a rebuild

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Open registration is a toggle, not a rebuild
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.3](../ARCHITECTURE.md#adr-010--open-registration-is-a-toggle-not-a-rebuild) — this file is the detail behind it.

---

`app_settings.open_registration` auto-approves new accounts as plain users when on; existing accounts are untouched either way.

_Reasoning:_ the setting is a row read at login time and it only ever supplies the status of a **brand-new** account — the login upsert's `ON CONFLICT` clause never touches `status` or `role` — so the toggle is reversible in either direction with no migration and no redeploy, and no existing account moves because it flipped.
