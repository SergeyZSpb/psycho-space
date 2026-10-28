# ADR-008 · Consent is a gate, not a checkbox on a form

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Consent is a gate, not a checkbox on a form — and the edition it is recorded under is the server's.
- **status:** Accepted · 2026-07-25. Current edition `v4`: the box is the consent alone, and the server owns the edition.
- **code:** the gate `web/src/views/LandingView.vue` (`consent-box`) · the edition `account.ConsentVersion` (`internal/account/service.go`) and its SPA copy `CONSENT_VERSION` (`web/src/constants.ts`), pinned equal by `internal/account/consent_test.go` · the check `internal/httpapi/auth.go` (`handleOAuthCallback`) · the stash across a redirect `ssConsentKey` (`web/src/constants.ts`).
- **related:** ADR-054
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#adr-008--consent-is-a-gate-not-a-checkbox-on-a-form) — this file is the detail behind it.

---

Neither login affordance is reachable until the consent box is ticked — the VK widget is not mounted, and the Yandex button is not on screen — and `consent_at` / `consent_version` are recorded server-side.

_Reasoning:_ consent has to precede processing to mean anything. Mounting the widget first and recording consent afterwards would reverse that order.

_The box is the consent and nothing else._ Since 01.09.2025 a consent must be given apart from any other document the person confirms (152-ФЗ ст. 9 ч. 1). So the box's label links the consent document alone, and the processing policy sits underneath it as a link to read; ticking the box agrees to nothing in the policy, and the policy says so. A second required box for the policy was considered and not taken: reading a policy is not an act the law asks the person to confirm, and a box for it would only restore the bundling under a different layout.

_The edition is the server's, not the browser's._ The server holds the current edition as a constant and refuses a login carrying any other: an absent edition is `400 consent_required`, a different one is `409 consent_outdated`, and both are answered before the provider is contacted. What is recorded is the server's constant, never the value the request carried. Recording the browser's value made the record one the subject wrote themselves — anything at all was accepted as long as it was not empty.

The texts live in the SPA, so the SPA carries the same edition beside them, and a Go test fails when the two copies disagree; both ship in one binary, so a disagreement would not be a skew that heals after a deploy but a build in which every login is refused. The value the SPA sends is the edition of the text that was **on screen when the box was ticked**, stashed in `sessionStorage` beside the PKCE verifier. A redirect-mode login reloads the SPA on its way back, and the bundle that loads may be newer than the one the person read; sending that bundle's edition would record agreement to a text nobody was shown. With the stash, a deploy between the tick and the return trip ends in `409`, and the person ticks the box again under the new text.

_When the version bumps._ Whenever the consent's text changes in substance — the **disclosed data set**, its **source**, a **processor**, the purpose. Consent is to processing, by us, of data obtained from a named party, so naming a second party is a different statement even when the fields are identical: adding Yandex ID changed nothing about what is stored and still took the version from `v2` to `v3`. Giving the consent a box of its own and naming the hosting provider took it to `v4`.

_Consequence:_ existing accounts keep the version they actually agreed to until their next login, which is the correct audit trail rather than a retroactive claim that they consented to something they were never shown. A tab left open across a bump cannot log in until it is reloaded, which is the intended cost. The email address Yandex would happily supply is deliberately not requested and not decoded, so it never enters the disclosed set at all.
