# ADR-054 · An identity is a provider and a blind index, and a second provider is a second account

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** how a login identifies somebody now that there are two providers — the composite key `(provider, identity_ref)`, why the blind-index input did not change, and why a Yandex login is a new account rather than a link to a VK one.
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#82-identity-and-personal-data) — this file is the detail behind it.
- **related:** ADR-004 · ADR-005 · ADR-006 · ADR-008 · ADR-053 · ADR-055
- **code:** `migrations/012_identity_provider.sql` — the reasoning, at the schema · `internal/account/service.go` — `UpsertOnLogin`, where the index is taken · `internal/account/postgres_repository.go` — the `ON CONFLICT (provider, identity_ref)` · `internal/httpapi/auth_provider.go` — the provider seam · `test/integration/migration_identity_test.go` — the proof that existing accounts survived
- **re-examine when:** somebody proposes account linking, a third provider, or namespacing the blind-index input. The first is a real feature with a real cost, written out below; the third is the one that must not happen quietly.

---

The identity of an account is the pair **`(provider, identity_ref)`** — which door somebody came through, and a deterministic `HMAC-SHA256` of their raw user id at that provider. Uniqueness is over the pair. The blind index alone is not an identity, and has not been since there was more than one way in.

_Reasoning._ VK and Yandex both hand out small numeric user ids, and the index is taken over the id as the provider states it. So VK user `12345` and Yandex user `12345` produce **identical** blind indexes. Under the single-column `UNIQUE` this schema shipped with, they would have been the same row: the second person to log in would have landed inside the first person's account, with their wishlist, their pet, their role and their approval. That is not a remote possibility to be watched for, it is arithmetic, and `TestSameUserIdAtTwoProvidersIsTwoAccounts` fails loudly if the constraint is ever weakened back.

_Why the index input did not change._ The obvious alternative needs no migration at all: feed the index `"vk:12345"` instead of `"12345"`. It was rejected because **`APP_HMAC_KEY` cannot be rotated, and the indexed value is exactly as load-bearing as the key**. Change what goes in and every existing account's reference stops matching at login — so every current user silently becomes a brand-new `pending` account, keeps nothing, and cannot be recovered by re-running anything, because the old rows are still there holding indexes nobody can reproduce. A namespaced index for new providers only would have worked, but it makes VK permanently the exception inside the most sensitive function in the system, with nothing in the schema explaining why. Carrying the provider in a column says it once, in the place a reader looks.

_Consequence, and it is the point._ Everything that was true of one identity is now true of a pair, and only of a pair. The `Handle` — the first eight hex of the index, shown on the pending screen — is deliberately **not** part of the identity and can therefore be shared by two accounts from different providers with the same id. That is harmless because it is a display string and a search prefix, never a key; `make-superadmin` prints the provider beside it so a two-row answer is legible rather than ambiguous.

_Why a Yandex login is a new account._ Somebody who has used VK and then logs in with Yandex gets a second, `pending`, empty account. Linking would need a UI to start it, an authenticated second-provider flow, a merge policy for two sets of wishlist items and two pets, a de-link path, and a new attack surface at the merge. Against a handful of friends who will each pick one door and keep using it, that is a subsystem bought for a sentence's worth of confusion. The pair is also precisely the shape that makes linking *addable* later — an `account_identities` child table, with nothing here disturbed — so this defers the decision rather than foreclosing it.

_What this is not._ It is not a claim that a person is an account. It is the opposite: an account is one **identity at one provider**, and the system has never had a concept of a person spanning two. Any future feature that needs one has to introduce it deliberately, which is better than discovering that a collision had been quietly asserting it all along.
