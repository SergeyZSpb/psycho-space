-- «СИМУЛЯТОР ФИНТЕХА» — the office floor becomes data.
--
-- WHAT THIS SUPERSEDES. migrations/013 states, in its header, that "the OFFICE is
-- a constant in the Go catalogue … there is no seed and no geometry here because
-- there is nothing to vary". That was true and is not any more: the floor is
-- generated, rebuilt daily, and editable by hand from the admin section, so it is
-- an instance rather than content. 013 is applied and immutable — the migrator
-- keys schema_migrations on the FILENAME and a rewritten 013 would look unapplied
-- — so the correction lives here, in the file that makes it false.
--
-- ONE OPAQUE BODY, AND WHY IT IS NOT A ROW PER SOLID. The layout is read whole,
-- written whole, and never queried by its parts: nothing selects the desks in the
-- top half, nothing joins against a flowerpot. What a row set WOULD add is an
-- ordering column that has to be maintained, because the ORDER of the solids is
-- part of the collision contract with the browser's copy of the resolver — the
-- push-out runs once per rectangle in catalogue order, on both ends. So the body
-- is jsonb, held as one value, exactly as it is served.
--
-- This is ADR-039's named trigger firing rather than an exception to it: game
-- CONTENT stays a Go catalogue, and what is stored here is one authored instance
-- of it. Nothing else in the schema learns a game-specific shape.
--
-- NO SEED COLUMN AND NO DAY COLUMN. A hand-edited floor has no seed that would
-- reproduce it, so a seed would be true of some rows and a lie on others; and the
-- day a row belongs to is derivable from created_at, which cannot drift from it.
-- `source` says where the geometry came from and is set by the endpoint that
-- wrote it, never by its body — so it cannot be claimed.
CREATE TABLE IF NOT EXISTS game_fintech_layouts (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source     text NOT NULL,
    body       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

-- The only read this table has is "the newest one", so the index is the one that
-- answers it. Partial on the soft-delete predicate, like every other index here.
CREATE INDEX IF NOT EXISTS game_fintech_layouts_current_idx
    ON game_fintech_layouts (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
