-- 012_identity_provider: an identity is a PROVIDER plus a blind index, not a
-- blind index alone.
--
-- Until now there was exactly one way in, so `vk_user_ref` — HMAC-SHA256 of the
-- VK numeric id — could be the whole identity and carry a plain UNIQUE. Adding
-- Yandex ID breaks that: both providers hand out small numeric user ids, so VK
-- user 12345 and Yandex user 12345 hash to the SAME blind index. Under the old
-- constraint they would silently become the same account, and whoever logged in
-- second would take over the first person's wishlist, pet and role. That is not
-- a remote possibility, it is arithmetic.
--
-- WHY THE INDEXED VALUE DOES NOT CHANGE. The obvious alternative is to feed the
-- index a namespaced string ("vk:12345"), which needs no schema change at all.
-- It was rejected because `APP_HMAC_KEY` cannot be rotated and the indexed VALUE
-- is just as load-bearing: change what goes in, and every existing account's
-- reference stops matching at login, so every current user silently becomes a
-- brand-new pending account and loses everything attached to the old row. The
-- blind index therefore keeps taking the provider's raw id, unchanged, and the
-- provider is carried beside it instead. Every row that exists today keeps the
-- exact bytes it has today.
--
-- WHY THE COLUMNS ARE RENAMED. A rename touches no data — the values, and so
-- every blind index, are untouched — but it stops the schema from claiming that
-- an identity is a VK identity. `vk_user_id_enc` holds a Yandex id for a Yandex
-- account, and a column whose name contradicts its contents is how the next
-- reader gets it wrong.
--
-- WHAT THIS DOES NOT DO. It does not link accounts across providers. Logging in
-- with Yandex having previously used VK produces a NEW account, deliberately —
-- linking needs a merge policy for two sets of contributions and is not worth
-- building for this audience. The composite key is precisely the shape that
-- would let an `account_identities` child table be added later without
-- disturbing anything here.

ALTER TABLE accounts RENAME COLUMN vk_user_ref    TO identity_ref;
ALTER TABLE accounts RENAME COLUMN vk_user_id_enc TO identity_id_enc;

-- 'vk' as the default backfills the rows that predate this column — every
-- account that exists when this runs arrived through VK ID, which was the only
-- door. The default is dropped again below.
ALTER TABLE accounts
    ADD COLUMN provider text NOT NULL DEFAULT 'vk'
        CHECK (provider IN ('vk', 'yandex'));

-- Renaming a column does not rename its constraint, so the inline UNIQUE from
-- 001_init.sql is still called whatever PostgreSQL generated for it. The
-- generated name is predictable (`accounts_vk_user_ref_key`), but a migration is
-- immutable once shipped and a wrong guess here is a failed deploy against a
-- database nobody can re-run the fix on, so it is looked up rather than assumed:
-- drop whichever UNIQUE constraint covers exactly the identity_ref column.
DO $$
DECLARE
    old_name text;
BEGIN
    SELECT c.conname INTO old_name
      FROM pg_constraint c
     WHERE c.conrelid = 'accounts'::regclass
       AND c.contype  = 'u'
       AND c.conkey   = ARRAY[(
            SELECT a.attnum FROM pg_attribute a
             WHERE a.attrelid = 'accounts'::regclass
               AND a.attname  = 'identity_ref')];

    IF old_name IS NULL THEN
        RAISE EXCEPTION 'no single-column UNIQUE found on accounts.identity_ref';
    END IF;

    EXECUTE format('ALTER TABLE accounts DROP CONSTRAINT %I', old_name);
END $$;

-- Uniqueness is now per provider. Cross-provider collision becomes impossible by
-- construction rather than by a string convention somebody has to remember.
ALTER TABLE accounts
    ADD CONSTRAINT accounts_identity_key UNIQUE (provider, identity_ref);

-- The default existed only for the backfill. Leaving it would let a future
-- insert forget the provider and quietly become a VK identity — an option with
-- one value, and a trap.
ALTER TABLE accounts ALTER COLUMN provider DROP DEFAULT;

COMMENT ON COLUMN accounts.provider IS
    'Which login provider this identity belongs to: vk | yandex. '
    'Half of the identity — the other half is identity_ref, and only the pair is unique.';
COMMENT ON COLUMN accounts.identity_ref IS
    'Blind index HMAC-SHA256(provider''s raw user id). Never namespaced: the input '
    'must stay byte-identical to what it was, or every existing account stops matching.';
COMMENT ON COLUMN accounts.identity_id_enc IS
    'AES-256-GCM ciphertext of the provider''s raw user id.';
