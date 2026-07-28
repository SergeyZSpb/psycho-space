-- 013_game_karen: the entire durable footprint of «СИМУЛЯТОР КАРЕНА» (game 4).
--
-- One table, and it is a summary rather than a world. Everything else about a
-- shift lives in memory for the few minutes it takes to play:
--
--   * The OFFICE is a constant in the Go catalogue (internal/gamekaren/
--     content.go): the same room, the same eight desks, the same two spawns,
--     every time. There is no seed and no geometry here because there is nothing
--     to vary — this game's whole simplification against «ВАНЯДУМ» is that the
--     level never changes.
--   * The SHIFT IN PROGRESS — where you are standing, what you have earned, how
--     long you have been still, where the bald man is — is deliberately
--     ephemeral, and is lost on restart in exactly the way the realtime hub's
--     presence is. A shift is a few minutes long and a lost one costs a replay.
--     This is what keeps the game's 20 Hz simulation clear of the rule that
--     nothing in this project ticks durable state.
--
-- What is deliberately NOT here, each because it would make content cost a
-- migration:
--
--   * No `game_key` column. The table name carries the game's identity — that is
--     the whole point of the naming convention, and a discriminator column would
--     be a second, weaker copy of it.
--   * No enum for `cause`. It is `text`, so iteration 4's «тебя раскусили» is a
--     line in the Go catalogue and never an ALTER TYPE. The two values today are
--     'promoted' (he reached you) and 'left' (you walked out, or your connection
--     did).
--   * No JSONB, and no per-tick or per-event rows. A shift that recorded every
--     step would be a movement log for a joke about standing still, and the four
--     columns below answer every question anybody has asked of it.
--   * No multiplier, streak or dash counters. All three are derivable from the
--     simulation and none of them survive the shift; storing the peak of a
--     number nobody compares would be a column added against a requirement that
--     does not exist.
--   * No art table. Iteration 1 ships with zero uploaded assets and CSS
--     placeholder shapes, and when art does arrive it goes in the SHARED
--     game_assets store keyed on (game_key, art_key) — infrastructure, not this
--     game's schema.
--
-- `salary` and `seconds` are `double precision` rather than `real` or `integer`.
-- The simulation produces fractions of both — money accrues continuously at a
-- multiplier that is itself a fraction, and a shift ends mid-tick — and
-- `real`'s seven digits would start losing rubles on a shift worth six figures,
-- which is exactly the shift somebody would want to argue about. Rounding at the
-- boundary instead would make the leaderboard disagree with the number the
-- player watched climb.

CREATE TABLE IF NOT EXISTS game_karen_shifts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,

    -- Why the shift ended: 'promoted' | 'left'. Text, so a third ending is a
    -- deploy and not a migration.
    cause       text NOT NULL,

    -- What it was worth, in rubles, and how long it lasted, in seconds. A shift
    -- shorter than the service's MinShiftSeconds is never written at all, so
    -- every row here is one somebody actually played.
    salary      double precision NOT NULL DEFAULT 0,
    seconds     double precision NOT NULL DEFAULT 0,

    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

-- The splash screen's «твои смены»: an account's own shifts, newest first. It
-- exists so that a human can confirm in production that a shift was written
-- without opening a database.
CREATE INDEX IF NOT EXISTS game_karen_shifts_account_idx
    ON game_karen_shifts (account_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- The leaderboard, which reads the best shift per account and therefore scans in
-- salary order.
CREATE INDEX IF NOT EXISTS game_karen_shifts_salary_idx
    ON game_karen_shifts (salary DESC)
    WHERE deleted_at IS NULL;
