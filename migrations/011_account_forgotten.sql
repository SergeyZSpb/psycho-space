-- 011_account_forgotten: forgetting a person without deleting what they wrote.
--
-- The admin action this supports removes the PERSON from the system while
-- leaving their CONTRIBUTIONS in place: a wishlist idea other people commented
-- on, a comment other people replied to and upvoted, a leaderboard time. After
-- it, logging in with the same VK account produces a genuinely new account —
-- new id, `pending`, first-login flow — because the blind index that used to
-- match is gone.
--
-- WHY NOT A HARD DELETE. Nine of the ten foreign keys to `accounts` have no
-- `ON DELETE CASCADE`, so a real DELETE means an explicit child-first
-- transaction — and, far more importantly, it would take other people's
-- comments and votes with it, because they hang off the deleted user's items.
-- Cascading one person's erasure into somebody else's conversation is the wrong
-- default. (A hard delete remains the right answer if a row must genuinely
-- cease to exist; this is the everyday one.)
--
-- WHY NOT A PLAIN SOFT DELETE. `accounts.vk_user_ref` is a plain, non-partial
-- UNIQUE (001_init.sql), and the login upsert is `ON CONFLICT (vk_user_ref) DO
-- UPDATE` whose SET list touches neither `deleted_at` nor `status`. So setting
-- `deleted_at` alone leaves the row holding the blind index: the next login
-- silently reuses it, keeps the old id and the old status, and hands back a
-- session cookie for an account that every read then refuses to find. The user
-- ends up logged in and permanently 401, invisible to the admin list. That is
-- worse than doing nothing.
--
-- SO THE MECHANISM IS ANONYMISATION. The row stays, visible and joinable, and
-- everything identifying is destroyed in place:
--
--   * `vk_user_ref` is overwritten with random bytes, which frees the real
--     blind index so a re-login inserts a new account. It is not merely
--     cleared, because the column is NOT NULL and UNIQUE.
--   * every `*_enc` field is emptied, so no ciphertext of a real person remains
--     under a key that still exists.
--   * `consent_at` / `consent_version` are cleared — consent has been withdrawn,
--     and a retained consent record for a person who is no longer here is
--     exactly the thing 152-ФЗ minimisation is about.
--
-- The row then renders through the display fallbacks the code already has:
-- `DisplayName()` returns `psycho-<handle>` when the name is empty, and
-- `VKURL()` returns nothing when the VK id is. So a comment keeps its shape and
-- loses its author, with no tombstone vocabulary invented for the purpose.
--
-- `deleted_at` is deliberately NOT set. Author lookup goes through `GetByID`,
-- which filters `deleted_at IS NULL`, so soft-deleting the row would blank the
-- author of every comment it wrote rather than anonymising it.

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS forgotten_at timestamptz;

COMMENT ON COLUMN accounts.forgotten_at IS
    'When this account was anonymised: identity destroyed in place, contributions kept. '
    'NULL for every ordinary account.';

-- The admin list filters these out, and the login path must never resurrect
-- one. Partial, because the overwhelming majority of rows are NULL.
CREATE INDEX IF NOT EXISTS accounts_forgotten_idx
    ON accounts (forgotten_at)
    WHERE forgotten_at IS NOT NULL;
