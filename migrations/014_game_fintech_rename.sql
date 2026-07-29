-- 014_game_fintech_rename: the fourth game is reframed from «СИМУЛЯТОР КАРЕНА»
-- to «СИМУЛЯТОР ФИНТЕХА», so its table moves with it:
-- game_karen_shifts -> game_fintech_shifts.
--
-- Why: the joke used to be about one man. The reframe makes it about the
-- industry — the office is a fintech, and the person standing still is Карен, or
-- Андрюха, or Саня, or Даша, drawn at random when a shift starts. Every game is
-- a self-contained module named consistently across code, routes and tables
-- (package gamefintech, /api/game-fintech/*, game_fintech_*), so removing this
-- game stays `git grep -il gamefintech` and nothing else, and the name on the
-- screen and the name in the schema cannot drift apart.
--
-- 013_game_karen.sql is deliberately NOT edited and deliberately NOT renamed.
-- The migrator keys schema_migrations on the FILENAME, so a renamed 013 would
-- look unapplied, re-run after this file, and its
-- `CREATE TABLE IF NOT EXISTS game_karen_shifts` would recreate an empty old
-- table beside the live one. It is a record of what happened — the same reason
-- docs/adrs/ records describe the present while `git log -p` holds the history.
--
-- The game_key COLUMN VALUES are untouched, as in 007: `karen` stays `karen`.
-- A game_key value is data, not a name, and the shared game_assets store is
-- keyed on (game_key, art_key). Nothing is uploaded under it today, which is
-- exactly why it must not be changed opportunistically — the rule is
-- unconditional, and the first blob uploaded under a renamed key would silently
-- orphan. The same goes for the `cause` values 'promoted' and 'left', which are
-- in every stored row and are the join key to the catalogue's ending copy.
--
-- RENAME preserves the rows, so this is not a create-and-copy: no data moves and
-- the migration is instant (a catalog update under an ACCESS EXCLUSIVE lock).

ALTER TABLE game_karen_shifts RENAME TO game_fintech_shifts;

-- Postgres does NOT rename a table's indexes or constraints along with the
-- table, so every dependent object still carries the old name and has to be
-- renamed explicitly — the lesson 007 wrote down. Left alone they would lie
-- about which table they belong to and would collide with a future game's
-- objects. The four below are the ones Postgres generated for 013: the implicit
-- primary key from `id uuid PRIMARY KEY`, the foreign key from
-- `account_id ... REFERENCES accounts (id)`, and the two explicit indexes.
-- There is no CHECK constraint (unlike 005's game_runs_steps_check) and no
-- sequence, because 013 declares neither.
--
-- Renaming a constraint that is backed by an index renames the index too, so the
-- primary key needs only the constraint form. An index is addressed by its own
-- name; a constraint is addressed through its table, which by then is the NEW
-- name.
ALTER INDEX game_karen_shifts_account_idx RENAME TO game_fintech_shifts_account_idx;
ALTER INDEX game_karen_shifts_salary_idx  RENAME TO game_fintech_shifts_salary_idx;
ALTER TABLE game_fintech_shifts RENAME CONSTRAINT game_karen_shifts_pkey TO game_fintech_shifts_pkey;
ALTER TABLE game_fintech_shifts RENAME CONSTRAINT game_karen_shifts_account_id_fkey TO game_fintech_shifts_account_id_fkey;
