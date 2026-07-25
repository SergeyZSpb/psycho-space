-- 008_game_vanyagotchi: the durable half of «Ванягоччи» (game 2) — the pet, the
-- stats that change on their own, and the things lying about in the world.
--
-- All three tables land together even though only the first two are written
-- this iteration. Migrations here are immutable once shipped, so the shape of
-- the world-object table is settled now, while the three mechanics that will
-- use it are already specified (a relief deposit with a TTL, a single-winner key
-- claim, a finite beer stock), rather than guessed at again later under an
-- ALTER. The invariant that table exists to hold — at most one active event of a
-- kind — is pinned by a test in this same change, so the DDL is exercised rather
-- than merely present.
--
-- What is deliberately NOT here, each because it would make content cost a
-- migration:
--
--   * No `game_key` column. The table name carries the game's identity, which is
--     the entire point of the naming convention; a discriminator column would be
--     a second, weaker copy of it. (The shared game_assets store keeps its
--     game_key because it really is multi-game — art storage is infrastructure.)
--   * No art or asset table. Blobs live in the shared game_assets store, so a
--     new sprite is an upload plus a catalogue entry.
--   * No Postgres enums for kind / location_key / stat_key / skin_key. An enum
--     makes every new content value an ALTER TYPE, i.e. a migration — precisely
--     the cost the Go catalogue in content.go exists to remove. `text` plus the
--     allowlist in the catalogue validates in the only place that can see
--     content anyway, and a row whose key has left the catalogue is simply
--     unrenderable and skipped on read.
--   * No JSONB. Both candidate uses were cosmetic and derive better from
--     hash(id) against the catalogue; an unused escape hatch is where
--     load-bearing state goes to hide from constraints.
--
-- Floating point is `double precision` rather than `real`. Every position and
-- every stat is a Go float64 end to end — the plane's coordinates are float64
-- on the wire and clamped as float64 before they are stored — and a float4
-- round trip would hand back a number that differs from the one just written.
-- Four bytes a row is not a reason to introduce that.

-- One player's дядя Ваня. Persistent: there is no "run" in this game, so there
-- is nothing here resembling game_khimki_runs.
CREATE TABLE game_vanyagotchi_pets (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   uuid NOT NULL REFERENCES accounts (id),
    -- Named in a dialog, never on the play surface: the on-screen keyboard is
    -- the one case the never-scroll layout handles badly. NULL until then.
    name         text,
    -- Catalogue keys, validated in Go. See the note on enums above.
    skin_key     text NOT NULL,
    location_key text NOT NULL,
    -- Where he was standing when his owner last left, and when that was.
    --
    -- Nothing writes these yet — the service holds a live position in memory and
    -- keeps it briefly after the last socket goes, which is what makes a page
    -- refresh keep your place. They are here because the columns are the cheap
    -- half of a decision the owner has already made ("idle Vanyas fall asleep
    -- directly where they stood, and wake up when their owner comes back"), and
    -- because this migration is immutable: adding them later is a second file,
    -- adding them now is three lines.
    --
    -- NULL means he has never stood anywhere, and he starts at his location's
    -- entry point from the catalogue. The eventual write is once per session, on
    -- departure — never per move, because thirty players moving at the socket's
    -- ten messages a second would be three hundred writes a second to persist
    -- something only read when they leave. A crash therefore loses the last
    -- position and a sleeping Ваня reappears where he was last written, which is
    -- an acceptable failure for a nap.
    --
    -- A resolved point rather than an in-flight walk: a walk is (from, to,
    -- started_at) and is transient by construction, so what survives a departure
    -- is where he ended up.
    x            double precision CHECK (x >= 0 AND x <= 1),
    y            double precision CHECK (y >= 0 AND y <= 1),
    last_seen_at timestamptz,
    -- The moment hp reached zero, written exactly once by the first read that
    -- observes it and cleared if he is brought round. Nothing ticks: "he died at
    -- 03:00" only becomes a fact when something writes it down, and because the
    -- decay is linear the instant is derivable exactly rather than rounded up to
    -- whenever somebody happened to look.
    died_at      timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

-- One living pet per account.
--
-- A partial unique index rather than a UNIQUE column constraint, so the
-- convention this project uses everywhere else — soft delete, and every read
-- filtered on `deleted_at IS NULL` — stays available here. A plain UNIQUE would
-- count a soft-deleted pet, which would make "delete him and start over" a
-- schema change rather than an UPDATE. It is also what makes the lazy create
-- safe under concurrency: two tabs opening the game at the same instant both run
-- INSERT ... ON CONFLICT DO NOTHING and exactly one row results.
--
-- The insert must use the bare `ON CONFLICT DO NOTHING` with no conflict target:
-- that form accepts any arbiter index including a partial one, whereas naming
-- the target would also require repeating this WHERE clause for inference to
-- succeed.
CREATE UNIQUE INDEX idx_game_vanyagotchi_pets_account
    ON game_vanyagotchi_pets (account_id)
    WHERE deleted_at IS NULL;

-- Everything about a pet that changes with time on its own, one row per stat.
--
-- Tall rather than a column per stat, and the two tables in this migration
-- choose differently on purpose. Stats are a HOMOGENEOUS collection — every one
-- of them is the same shape, a scalar with a rate and an `as_of` — so rows are
-- the natural container, one decay expression covers all of them, and adding a
-- stat is a catalogue entry rather than an ALTER. World objects below are the
-- opposite: heterogeneous rows carrying contended invariants, which need real
-- columns that can be indexed, NOT NULL-ed and CHECK-ed.
--
-- (value, as_of) is the whole decay engine. The current value is
-- clamp(value − rate × (now − as_of)) evaluated on read, so there is no cron, no
-- per-pet goroutine and no timer — and offline progression is not a feature that
-- had to be built, it is what this expression already means. It is EXACT rather
-- than an approximation of ticking, but only because the decay is linear; if a
-- rate ever depends on another decaying value, the closed form becomes an
-- approximation and the sign of its error decides whether being away beats
-- playing.
--
-- A stat whose rate is zero is a lifetime counter, which is how this game will
-- get `keys_found` / `beers_drunk` / `shits_taken` later with no schema at all.
CREATE TABLE game_vanyagotchi_pet_stats (
    pet_id     uuid NOT NULL REFERENCES game_vanyagotchi_pets (id),
    stat_key   text NOT NULL,
    value      double precision NOT NULL,
    as_of      timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    -- Reads are always "every stat of this pet", so the primary key is also the
    -- access path and no further index is warranted.
    PRIMARY KEY (pet_id, stat_key)
);

-- Every durable thing standing on the plane, discriminated by `kind`: a relief
-- deposit that expires, a lost key exactly one player can claim, a crate of beer
-- with a finite count. Nothing writes this table yet — the mechanics that do
-- arrive in later iterations — but its shape is fixed here because it cannot be
-- fixed later.
CREATE TABLE game_vanyagotchi_world_objects (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             text NOT NULL,
    location_key     text NOT NULL,
    -- Normalised 0..1 per axis, the same contract the wire and the CSS custom
    -- properties use, so a phone rotating changes nothing stored. CHECKed
    -- because a coordinate off the plane is unrenderable and there is no reading
    -- of it that is merely unusual.
    x                double precision NOT NULL CHECK (x >= 0 AND x <= 1),
    y                double precision NOT NULL CHECK (y >= 0 AND y <= 1),
    -- Whether this row participates in the "at most one active per kind"
    -- invariant below.
    --
    -- The value is derived from the catalogue at insert time, and this column
    -- exists solely so the partial unique index can express that invariant
    -- WITHOUT naming any kind in DDL. The alternative — a predicate reading
    -- `kind IN ('key', 'beer_crate')` — would put content keys in an immutable
    -- migration and make "a new singleton kind" a schema change, which is the
    -- one outcome the catalogue design exists to prevent. It cannot simply
    -- follow from `exhausted_at IS NULL` either: relief deposits are never
    -- exhausted and many of them are live at once, so an invariant keyed on that
    -- alone would forbid a second player from relieving himself.
    singleton        boolean NOT NULL DEFAULT false,
    -- Who created it. NULL when the world spawned it rather than a player.
    owner_account_id uuid REFERENCES accounts (id),
    -- The SingleWinner discipline: the winning UPDATE is conditional on
    -- claimed_by IS NULL, so exactly one player wins by database constraint
    -- rather than by hub ordering, and a forged client claim cannot beat it.
    claimed_by       uuid REFERENCES accounts (id),
    claimed_at       timestamptz,
    -- The Stock discipline: `UPDATE ... WHERE remaining > 0 RETURNING` cannot
    -- oversell under concurrency. NULL for kinds that are not stock.
    remaining        integer CHECK (remaining >= 0),
    -- Set by whichever discipline used it up — the claim that won, or the
    -- drawdown that reached zero. One explicit column so both kinds share one
    -- invariant, and so a replacement can be inserted in the same statement.
    exhausted_at     timestamptz,
    -- A closed-form schedule slot, for idempotent lazy materialisation. Unused
    -- while events restart the instant they are exhausted; the column is kept
    -- because pacing is a named future change and the index below is what makes
    -- two players arriving at the same instant unable to double-spawn.
    slot_index       bigint,
    spawns_at        timestamptz NOT NULL DEFAULT now(),
    -- TTL, filtered lazily on read exactly as sessions.expires_at already is.
    -- Nothing sweeps: rows accumulate and are ignored, because a sweeper would
    -- be the background timer this design does not have.
    expires_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz
);

-- At most one active key hunt, and at most one active beer delivery, in the
-- whole world at a time — as a database invariant rather than a rule written in
-- Go, so two concurrent restarts cannot produce two keys.
CREATE UNIQUE INDEX idx_game_vanyagotchi_world_objects_singleton
    ON game_vanyagotchi_world_objects (kind)
    WHERE singleton AND exhausted_at IS NULL AND deleted_at IS NULL;

-- Makes concurrent materialisation of the same scheduled slot safe.
CREATE UNIQUE INDEX idx_game_vanyagotchi_world_objects_slot
    ON game_vanyagotchi_world_objects (location_key, kind, slot_index)
    WHERE slot_index IS NOT NULL AND deleted_at IS NULL;

-- The plane read: everything standing in one location.
CREATE INDEX idx_game_vanyagotchi_world_objects_plane
    ON game_vanyagotchi_world_objects (location_key)
    WHERE deleted_at IS NULL;
