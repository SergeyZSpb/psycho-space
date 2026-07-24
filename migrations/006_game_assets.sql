-- 006_game_assets: art images for the game, stored as BLOBs in Postgres (kept
-- out of the repo/binary). Uploaded out-of-band by the owner over SSH (see the
-- runbook "Game assets"), served by GET /api/game/assets/{game}/{key}, and
-- downloaded on demand by the client. An admin upload UI may come later.
CREATE TABLE game_assets (
    game_key     text NOT NULL,
    art_key      text NOT NULL,
    content_type text NOT NULL DEFAULT 'image/webp',
    bytes        bytea NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (game_key, art_key)
);
