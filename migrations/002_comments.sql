-- 002_comments: comments on wishlist items, each upvotable like items.

CREATE TABLE wishlist_comments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id    uuid NOT NULL REFERENCES wishlist_items (id),
    account_id uuid NOT NULL REFERENCES accounts (id),
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE INDEX idx_wishlist_comments_item ON wishlist_comments (item_id, created_at) WHERE deleted_at IS NULL;

-- Ephemeral toggle votes (hard-deleted on un-vote), one per (comment, account).
CREATE TABLE wishlist_comment_votes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    comment_id uuid NOT NULL REFERENCES wishlist_comments (id),
    account_id uuid NOT NULL REFERENCES accounts (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (comment_id, account_id)
);
