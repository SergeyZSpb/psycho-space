-- 009_game_vanyagotchi_events: what the player actually did, in order.
--
-- Until now a pet's state was ONLY its stat rows: the pair (value, as_of) per
-- stat, overwritten by every action. That is enough to know what a Ваня is and
-- useless for knowing how he got there — an action left no trace once its
-- effect had been folded into a number, so a retuned constant could not be
-- applied retroactively, a bug could not be reproduced from a real pet, and
-- "what did I do yesterday" had no answer at all.
--
-- This table is the missing half: an append-only log of the verbs, each stamped
-- with the instant the SERVER accepted it. A client never supplies a time —
-- everything in this game is computed from timestamps, so a forged one would
-- rewrite history, and the whole decay model rests on that instant being true.
--
-- WHAT THIS IS NOT: a second source of truth. The stat rows in
-- game_vanyagotchi_pet_stats remain the SNAPSHOT and stay authoritative for a
-- read, so reading a pet is still one indexed query and one subtraction however
-- old it is. Every existing pet therefore keeps its state with no backfill and
-- no migration of data — its current rows simply are its first snapshot, and
-- the log starts from empty alongside it.
--
-- The two are written in ONE transaction, so a snapshot can never disagree with
-- the events that produced it. The log is what makes a fold possible; the
-- snapshot is what makes it unnecessary on the hot path.
--
-- Deliberately absent, each for a stated reason:
--
--   * No payload / JSONB column. A verb's effects are catalogue content
--     (content.go), so storing them here would freeze a copy of the catalogue
--     at write time — and freezing them is precisely what makes retro-tuning
--     impossible, which is the main thing this table exists to enable.
--   * No result or resulting-value columns. The result is a pure function of
--     (snapshot, verb, at) — recomputing it is exact and cheap, whereas storing
--     it creates a second answer that can drift from the one the code produces.
--   * No actor column. A pet belongs to exactly one account, so pet_id already
--     names who did it. An admin acting on somebody else's pet does not exist
--     and would need a new column when it does.
--   * No Postgres enum for verb. Same rule as everywhere else here: an enum
--     makes every new action an ALTER TYPE, i.e. a permanent migration, which
--     is the cost the Go catalogue exists to remove.

CREATE TABLE IF NOT EXISTS game_vanyagotchi_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id      uuid NOT NULL REFERENCES game_vanyagotchi_pets (id) ON DELETE CASCADE,

    -- seq orders the events of one pet, and orders them TOTALLY. `at` cannot do
    -- that job: a batch of verbs sent in a single frame is applied at one
    -- instant, so several rows legitimately share an `at`, and drink-then-
    -- relieve is a different pet from relieve-then-drink. A replay that sorted
    -- by time alone would be free to swap them.
    seq         bigint NOT NULL CHECK (seq > 0),

    -- The catalogue key of the action. `text`, validated in Go — a row whose
    -- verb has left the catalogue is unreplayable and is skipped, the same rule
    -- every other content key in this schema follows.
    verb        text NOT NULL CHECK (verb <> ''),

    -- When the SERVER accepted it. This is the value the entire decay model is
    -- integrated against, which is why it is never client-supplied.
    at          timestamptz NOT NULL,

    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

-- The log is append-only, and this is what enforces it: an appender computes the
-- next seq and inserts, so two connections racing to act on the same pet cannot
-- both take the same slot. The loser retries with the next number rather than
-- silently overwriting a sibling event.
CREATE UNIQUE INDEX IF NOT EXISTS idx_game_vanyagotchi_events_seq
    ON game_vanyagotchi_events (pet_id, seq);

-- Replay reads a pet's whole history in order; an audit reads a window of it.
-- Both are (pet_id, seq) scans, which the unique index above already serves --
-- this one covers the by-time questions instead: "what happened yesterday", and
-- the eventual snapshot policy's "events since this instant".
CREATE INDEX IF NOT EXISTS idx_game_vanyagotchi_events_at
    ON game_vanyagotchi_events (pet_id, at)
    WHERE deleted_at IS NULL;
