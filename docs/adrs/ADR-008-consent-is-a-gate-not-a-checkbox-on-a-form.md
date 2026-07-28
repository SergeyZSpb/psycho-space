# ADR-008 · Consent is a gate, not a checkbox on a form

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Consent is a gate, not a checkbox on a form
- **status:** Accepted · 2026-07-25 (extended to both providers, `v3`, 2026-07-28)
- **related:** ADR-054
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#adr-008--consent-is-a-gate-not-a-checkbox-on-a-form) — this file is the detail behind it.

---

Neither login affordance is reachable until the consent box is ticked — the VK widget is not mounted, and the Yandex button does nothing — and `consent_at` / `consent_version` are recorded server-side.

_Reasoning:_ consent has to precede processing to mean anything. Mounting the widget first and recording consent afterwards would reverse that order.

_When the version bumps._ Whenever the **disclosed data set** changes, and equally whenever its **source** does: consent is to processing, by us, of data obtained from a named party, so naming a second party is a different statement even when the fields are identical. Adding Yandex ID changed nothing about what is stored — id, имя, фамилия, пол, дата рождения, аватар — and still took the version from `v2` to `v3`.

_Consequence:_ existing accounts keep the version they actually agreed to until their next login, which is the correct audit trail rather than a retroactive claim that they consented to something they were never shown. The email address Yandex would happily supply is deliberately not requested and not decoded, so it never enters the disclosed set at all.
