-- 007_game_khimki_rename: move the first game's tables out of the generic
-- game_* namespace into its own: game_runs -> game_khimki_runs.
--
-- game_assets is deliberately NOT renamed. A game's STATE is its own and must
-- never be shared, which is why runs move; but storing and serving art bytes is
-- a generic capability every game needs, so the blob store is shared
-- infrastructure. It has carried a game_key discriminator since it was created,
-- i.e. it was always a multi-game store, and its unprefixed name is the signal
-- that it is game-agnostic.
--
-- Why: a second game («Ванягоччи») is arriving, so every game is now a
-- self-contained module named consistently across code, routes and tables
-- (package gamekhimki, /api/game-khimki/*, game_khimki_*). Removing a game is
-- then `git grep -il gamekhimki` and nothing else, and two games cannot quietly
-- share storage. Shared infrastructure (accounts, sessions, wishlist) keeps its
-- unprefixed names — the absence of a game prefix is what marks a table
-- game-agnostic.
--
-- The game_key COLUMN VALUES are deliberately NOT touched: they stay
-- 'smalltalk_khimki'. That value is data, it is already unambiguous, and the
-- uploaded art blobs in game_assets are keyed on it — rewriting it would
-- risk breaking the art lookup for no gain.
--
-- RENAME preserves the rows, so this is not a create-and-copy: no data moves and
-- the migration is instant (a catalog update under an ACCESS EXCLUSIVE lock).

ALTER TABLE game_runs RENAME TO game_khimki_runs;

-- Postgres does NOT rename a table's indexes or constraints along with the
-- table, so every dependent object still carries the old name and has to be
-- renamed explicitly. Left alone they would collide with the second game's
-- objects and lie about which table they belong to. Names below are the ones
-- Postgres generated for 005/006 (verified against a real 16 instance):
--
--   005: game_runs_pkey, game_runs_account_id_fkey, game_runs_steps_check,
--        idx_game_runs_board
--
-- Renaming a constraint that is backed by an index renames the index too, so the
-- pkeys need only the constraint form.
ALTER INDEX idx_game_runs_board RENAME TO idx_game_khimki_runs_board;
ALTER TABLE game_khimki_runs RENAME CONSTRAINT game_runs_pkey TO game_khimki_runs_pkey;
ALTER TABLE game_khimki_runs RENAME CONSTRAINT game_runs_account_id_fkey TO game_khimki_runs_account_id_fkey;
ALTER TABLE game_khimki_runs RENAME CONSTRAINT game_runs_steps_check TO game_khimki_runs_steps_check;
