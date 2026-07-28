# ADR-005 · Personal data is encrypted at rest, and looked up through a blind index

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Personal data is encrypted at rest, and looked up through a blind index
- **status:** Accepted · 2026-07-25 (scoped by provider 2026-07-28)
- **related:** ADR-054 — the pair `(provider, identity_ref)` is the identity
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#adr-005--personal-data-is-encrypted-at-rest-and-looked-up-through-a-blind-index) — this file is the detail behind it.

---

Profile fields are AES-256-GCM with a per-row nonce. Lookups (login, dedupe, allowlist) go through a deterministic `HMAC-SHA256` blind index over the provider's **raw** user id, never plaintext and never a reversible identifier. Since there is more than one provider, the index alone is not an identity — it is scoped by the provider column beside it, because two providers hand out the same small integers ([ADR-054](../ARCHITECTURE.md#adr-054--an-identity-is-a-provider-and-a-blind-index-and-a-second-provider-is-a-second-account)).

_Reasoning:_ 152-ФЗ minimisation, and the practical version of it — a database dump on its own should not be a list of who uses the site. The cost is that equality is the only query available on those columns, which is all the application needs.

_Consequence, learned the hard way:_ the keys are load-bearing, and so is the **input**. Rotating `APP_HMAC_KEY` breaks every blind index; losing `APP_ENC_KEY` makes stored profiles unrecoverable; and changing what is fed to the index — namespacing it, trimming it, normalising it — does exactly what rotating the key does, silently, which is why adding a second provider left the input alone and added a column instead. A single row that cannot be decrypted makes the whole admin list fail — which is how the full-stack e2e suite caught its own environment reusing a database across runs with fresh keys.
