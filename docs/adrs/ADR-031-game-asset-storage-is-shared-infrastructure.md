# ADR-031 · Game asset storage is shared infrastructure, not a game's property

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Game asset storage is shared infrastructure, not a game's property
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-031--game-asset-storage-is-shared-infrastructure-not-a-games-property) — this file is the detail behind it.
- **code:** `internal/gameassets`

---

Art blobs live in one unprefixed `game_assets` table, read through one unprefixed package (`internal/gameassets`) and served from one game-agnostic route (`GET /api/game-assets/{game}/{key}`). A game supplies its own `game_key` and nothing else. Migration 007 therefore renamed `game_runs` to `game_khimki_runs` but deliberately left `game_assets` alone.

_Reasoning:_ ADR-028 refuses shared code between games, and the first pass at ADR-030 applied that to the asset table too — which was wrong, and the schema had already said so: `game_assets` has carried a `game_key` discriminator since migration 006, so it was always a multi-game store. Making it per-game would have thrown that away and duplicated the blob query, the content-type allowlist and the caching handler once per game.

The line ADR-028 was missing, and which this record supplies, is **rule versus mechanism**. A game's *state* is a rule of that game — its runs, its scores, its pet, its world objects — and sharing it couples two games' lifecycles, which is exactly what ADR-028 forbids. Storing bytes under a key and serving them with a validated content type is a *mechanism* any game needs and none of them defines. The test to apply at the next boundary decision: **does it encode a rule of this game, or is it a capability any game would want?** Assets are a capability. A decay rate is a rule.

_Consequence:_ adding art for a new character, NPC or location is an upload against an existing table — no migration, no new route, no serving code, and no schema change per game. The dependency runs one way only: `gamekhimki` declares a narrow `AssetPresence` interface that the shared service satisfies, so a game depends on infrastructure and infrastructure never learns a game exists (ADR-028). The store being shared is also why its content-type allowlist matters more than it looks: it is one control protecting every game's origin at once.

_Note the asymmetry is deliberate and will read as inconsistent at a glance:_ two tables created in adjacent migrations, one renamed per-game and one not. The reason is above, and the distinction is worth more than the symmetry.
