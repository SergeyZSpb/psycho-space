# ADR-053 · Forgetting a person is anonymisation, not deletion

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Removing somebody from the system anonymises their account in place and keeps what they contributed
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#adr-053--forgetting-a-person-is-anonymisation-not-deletion) — this file is the detail behind it.
- **related:** [ADR-004](./ADR-004-server-side-opaque-sessions-not-jwt.md) · [ADR-005](./ADR-005-personal-data-is-encrypted-at-rest-and-looked.md) · [ADR-007](./ADR-007-a-session-cookie-is-issued-even-for-pending.md) · [ADR-008](./ADR-008-consent-is-a-gate-not-a-checkbox-on-a-form.md) · [ADR-009](./ADR-009-three-tiers-with-promotion-reserved-to-one-of.md)
- **code:** `migrations/011_account_forgotten.sql` — the reasoning, at the schema · `internal/account/postgres_repository.go` — `Forget`, the one statement · `internal/httpapi/admin.go` — `handleAdminForget` and the load-bearing order · `internal/gamevanyagotchi/display.go` — `Forget`, the in-memory purge
- **re-examine when:** somebody proposes a hard `DELETE FROM accounts`, or a *plain* soft delete. Both are wrong here for different reasons, and both are written out below.

---

The admin action that removes somebody from psycho-space **destroys their identity in place and leaves their contributions standing**. Afterwards the same VK account logging in again is a genuinely new account — new id, `pending`, the whole first-login flow — because the blind index that used to match it has been overwritten with random bytes.

_Why not a plain soft delete, which is what the codebase's conventions would suggest._ Every other table here soft-deletes, so setting `deleted_at` is the obvious move. **It is not merely insufficient — it is broken**, and it took reading two files to see it. `accounts.vk_user_ref` is a **plain, non-partial `UNIQUE`**, and the login path is an upsert with `ON CONFLICT (vk_user_ref) DO UPDATE` whose `SET` list touches neither `deleted_at` nor `status`. So a soft-deleted row still occupies the blind index: the next login silently reuses it, keeps the old id and the old status, and the handler hands back a session cookie — after which every request goes through `GetByID`, which filters `deleted_at IS NULL`, finds nothing, and answers **401 forever**. The person ends up logged in, permanently refused, and invisible to the admin list that would let anybody notice. That is a worse outcome than doing nothing at all.

_Why not a hard delete, which does work._ Nine of the ten foreign keys to `accounts` have no `ON DELETE CASCADE`, so it means an explicit child-first transaction across seven tables — that part is merely tedious. The real objection is what it takes with it. A wishlist idea belongs to its author, but the **comments and votes on it belong to other people**, and they hang off that idea by foreign key. Deleting one person therefore deletes somebody else's conversation. Cascading one member's erasure into another's contribution is the wrong default for a shared space, and the fact that the schema makes it awkward is the schema being right.

_So the mechanism is anonymisation._ One `UPDATE`, all of it or none:

- **`vk_user_ref` is overwritten with 32 bytes from `crypto/rand`** — the same width as the HMAC it replaces, so it is indistinguishable in shape, and *not derived from anything*, so somebody who knows the original input cannot recognise which row used to be whom. This is the part that does the actual work: it frees the blind index, which is what makes the next login an `INSERT`.
- **Every `*_enc` field is emptied.** No ciphertext of a real person is left sitting under a key that still exists ([ADR-005](./ADR-005-personal-data-is-encrypted-at-rest-and-looked.md)).
- **Consent is cleared.** A retained consent record for somebody who is no longer here is precisely what 152-ФЗ minimisation is about, and it is the one field whose absence is itself the audit trail ([ADR-008](./ADR-008-consent-is-a-gate-not-a-checkbox-on-a-form.md)).
- **`status` becomes `blocked` and `role` becomes `user`**, so the row cannot act even if something one day found a way to reach it.
- **`forgotten_at` is stamped**, and the admin list filters on it — an anonymous row nobody can act on is noise on that screen.

_`deleted_at` is deliberately left NULL_, and that is the subtle part rather than an oversight. Author lookup goes through `GetByID`, which filters on it. Setting it would blank the author of every comment the account wrote instead of anonymising it, which is the opposite of the point.

_The tombstone needed no new vocabulary_, which is the pleasing part. `DisplayName()` already falls back to `psycho-<handle>` when the name is empty, and `VKURL()` already returns nothing when the VK id is. So a scrubbed row renders as an anonymous `psycho-…` that links nowhere, through code that was written for the pending screen and had no idea this was coming.

_Two things beyond the database, and the order they happen in is load-bearing._ Live sockets are kicked **first**, because while one is open a «Ванягоччи» frame can reach `EnsurePet` and insert a row and a «ВАНЯДУМ» run can end and queue an insert. The kick is asynchronous, so it narrows the window rather than closing it — survivable here only because this operation deletes nothing, so the worst case is a stray row belonging to an anonymous account. And **in-memory state is purged**, twice, before and after the write: the yard deliberately never expires a non-provisional placement — it becomes a *sleeper*, drawn where they last stood — and the display cache holds their name and the URL of their photograph, so without it a person erased from the database would go on standing in the yard, labelled, for the life of the process.

_What this does not provide._ It is anonymisation, not destruction: the row, its timestamps and its foreign keys survive, and somebody with database access and an independent copy of the wishlist could correlate authorship by timing. That is the accepted trade for not deleting other people's words. **If a row must genuinely cease to exist** — a legal demand rather than a member leaving — the hard delete is still the right answer, and it is a different operation that should be written as one rather than by weakening this.
