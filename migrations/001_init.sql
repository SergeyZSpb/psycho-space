-- 001_init: accounts, sessions, wishlist items + votes.
-- gen_random_uuid() is built into PostgreSQL 13+ (no extension needed).

CREATE TABLE accounts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- HMAC-SHA256(vk_user_id) blind index: deterministic, non-reversible, unique.
    vk_user_ref     bytea NOT NULL UNIQUE,
    -- Encrypted (AES-256-GCM, nonce||ciphertext) personal data.
    vk_user_id_enc  bytea NOT NULL,
    first_name_enc  bytea,
    last_name_enc   bytea,
    avatar_url_enc  bytea,
    role            text NOT NULL DEFAULT 'user'    CHECK (role   IN ('user', 'admin')),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'blocked')),
    last_login_at   timestamptz,
    -- 152-ФЗ consent audit trail.
    consent_at      timestamptz,
    consent_version text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE TABLE sessions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts (id),
    -- HMAC-SHA256(session_key, raw_token); the raw token lives only in the cookie.
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE INDEX idx_sessions_account ON sessions (account_id) WHERE deleted_at IS NULL;

CREATE TABLE wishlist_items (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts (id),
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE INDEX idx_wishlist_items_active ON wishlist_items (created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE wishlist_votes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id    uuid NOT NULL REFERENCES wishlist_items (id),
    account_id uuid NOT NULL REFERENCES accounts (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
-- One active vote per (item, account); un-voting soft-deletes, re-voting inserts anew.
CREATE UNIQUE INDEX uq_wishlist_vote ON wishlist_votes (item_id, account_id) WHERE deleted_at IS NULL;
