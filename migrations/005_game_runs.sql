-- 005_game_runs: recorded play-throughs of a character dialogue, for the
-- leaderboard (how many dialogue steps taken, how many times the goal was
-- reached). One row per finished run — goal convinced (success) or step budget
-- spent (fail). Dialog content + AI judging live in code (internal/game).
CREATE TABLE game_runs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id    uuid NOT NULL REFERENCES accounts (id),
    game_key      text NOT NULL,
    character_key text NOT NULL,
    success       boolean NOT NULL,
    steps         integer NOT NULL DEFAULT 0 CHECK (steps >= 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz
);

-- Leaderboard read path: aggregate per account within a game.
CREATE INDEX idx_game_runs_board
    ON game_runs (game_key, account_id)
    WHERE deleted_at IS NULL;
