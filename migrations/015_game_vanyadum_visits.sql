-- 015_game_vanyadum_visits: «ВАНЯДУМ» stops being one arena per run and becomes
-- one shared, continuously running заброшка, so its durable footprint changes
-- shape with it: game_vanyadum_runs is replaced by game_vanyadum_visits.
--
-- Why the table cannot simply be renamed. A RUN had a beginning, an objective
-- and a result — you started one over HTTP, you collected every beer, it ended
-- and it was scored. A VISIT has none of those: the building never ends, there
-- is nothing to complete, and what happened is "somebody was here for a while
-- and drank this much". So `success` has no meaning to carry forward (every row
-- ever written has it true, which is another way of saying it recorded nothing),
-- and `joined_at` is a new fact nothing used to know.
--
-- Why the old rows are not migrated. They record a mode that will not exist, and
-- reinterpreting them as visits would put rows on the splash screen describing
-- something the player cannot do any more. This game has been in production for
-- days rather than months, the rows are a handful of joke забеги, and the honest
-- thing is to drop them rather than to translate them into a claim about a
-- заброшка nobody can walk into now.
--
-- What is deliberately NOT here, each because it would make content cost a
-- migration:
--
--   * No `game_key` column. The table name carries the game's identity — that is
--     the whole point of the naming convention, and a discriminator column would
--     be a second, weaker copy of it.
--   * No enums for anything. Pickups, surfaces and the level's shape are a Go
--     catalogue (content.go), so a new pickup is a deploy and never an
--     ALTER TYPE.
--   * No kills, deaths or streak columns. There is no gun and no enemy yet;
--     those arrive together and will bring their own migration. A column nothing
--     writes is complexity bought against a requirement that does not exist.
--   * No per-pickup or per-event rows. A visit that recorded every beer would be
--     an event log for a joke shooter, and the summary below answers every
--     question anybody has asked of it.
--   * No art table. This game stores no art at all: geometry, textures, sprites
--     and sound are generated from the seed on the client, so the shared
--     game_assets store is not used and no blob is uploaded for it, ever.

CREATE TABLE IF NOT EXISTS game_vanyadum_visits (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,

    -- Which building this was. The заброшка is regenerated every time it empties,
    -- so without the seed a visit cannot say which one you were in — and "we were
    -- in that one together" is the only thing anybody has ever wanted to ask of
    -- these rows. Eight bytes to keep it answerable.
    seed        bigint NOT NULL,

    -- When they walked in, and how long they actually had a connection. The
    -- second is measured to their last seen connection rather than to the moment
    -- the abandon grace expired, so the two minutes spent waiting for somebody
    -- who never came back are not counted as time in the building.
    joined_at   timestamptz NOT NULL,
    seconds     integer NOT NULL DEFAULT 0,

    -- How much beer was collected on the way. A plain column rather than a bag of
    -- counters: there is one pickup today, and a JSONB column added in advance of
    -- a second one would be complexity bought against a requirement that does not
    -- exist yet.
    beer        integer NOT NULL DEFAULT 0,

    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

-- The only read: an account's own recent visits, newest first. It exists so that
-- a human can confirm in production that a visit was written without opening a
-- database.
CREATE INDEX IF NOT EXISTS game_vanyadum_visits_account_idx
    ON game_vanyadum_visits (account_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- And the table it replaces goes in the same migration, because a codebase that
-- carries both the old and the new way of storing something is one where every
-- reader has to work out which is live. 010_game_vanyadum.sql is deliberately
-- neither edited nor deleted: the migrator keys schema_migrations on the
-- FILENAME, so it stays as the record of what happened, exactly as 013 did when
-- 014 renamed its table.
DROP TABLE IF EXISTS game_vanyadum_runs;
