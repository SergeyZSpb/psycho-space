# ADR-006 · Provider tokens are discarded after the profile fetch

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** provider tokens are discarded after the profile fetch
- **status:** Accepted · 2026-07-25 (generalised to both providers 2026-07-28)
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#adr-006--provider-tokens-are-discarded-after-the-profile-fetch) — this file is the detail behind it.
- **related:** ADR-054 · ADR-055
- **code:** `internal/vk/client.go` · `internal/yandex/client.go` · `internal/httpapi/auth_provider.go` — `oauthProvider.Profile`, where the tokens go out of scope

---

The code exchange happens on the server with the confidential credential — VK's service token, Yandex's client secret — and the resulting access token (and VK's refresh and id tokens) are used once to read the profile and then dropped. Nothing is persisted and nothing is returned to the browser.

_Reasoning:_ we never act on a user's behalf at their provider, so storing a credential that would let us is pure liability. It is also why no refresh token is requested from Yandex at all: a credential we have no use for is one that can only be stolen.

_Consequence:_ every login is a fresh exchange, which is correct — the profile is re-read on each login anyway, so the account row stays current, and there is no stale-token path to reason about.
