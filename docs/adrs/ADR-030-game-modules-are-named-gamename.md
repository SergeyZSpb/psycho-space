# ADR-030 · Game modules are named `Game<Name>`

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Game modules are named Game<Name>
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-030--game-modules-are-named-gamename) — this file is the detail behind it.
- **code:** `internal/gamekhimki/` · `migrations/007_game_khimki_rename.sql`

---

Every game module carries its own name at every layer: package `internal/game<name>/`, tables `game_<name>_*`, routes `/api/game-<name>/*`, view `Game<Name>View.vue` served at `/app/game-<name>`, and any exported identifier that names the game from outside its package. Game 1 moved onto the convention from generic `game` naming in this change — `internal/gamekhimki/`, `game_khimki_runs` (`migrations/007_game_khimki_rename.sql`), `/api/game-khimki/*`, `GameKhimkiView.vue` at `/app/game-khimki` — and game 2 is `gamevanyagotchi` throughout. **Shared infrastructure is deliberately not prefixed:** `realtime`, `session`, `account`, `crypto`, `db`, `logging`, `observability`, `httpapi`.

_Reasoning:_ ADR-028 makes deleting a game the design centre, and its boundary test — "removing a game is removing its package, its migration, its routes and its views, and nothing else" — was a judgement call that required knowing the codebase. Spelling the name out at every layer turns that test into a command: `git grep -il game<name>` enumerates the module, across Go, SQL, routes and the SPA, for someone who has never read it. The check also runs in the other direction, which is the more valuable half: if that list ever contains a file another game needs, the boundary is *already* broken and the grep has just said so. Generic `game` naming could not do either — it matched the platform, the other game, and the word "game" in prose.

The unprefixed platform names are load-bearing, not an omission. The *absence* of a game's name is the signal that a module is game-agnostic, which is why the socket is addressed `/api/realtime?room=…` rather than per-game and why game-specific message types live in the game's package rather than in `realtime` (ADR-028, ADR-016). Prefixing one of those would be a lie, and dropping the prefix from a game module would erase the signal. `wishlist` and `settings` are a third class — non-game **sections**, neither games nor platform — and stay unprefixed too; this convention is about game modules, not about every domain package.

_The one exception is inside a game's own package,_ where Go convention wins: `GameKhimkiService` in package `gamekhimki` stutters, `revive` flags it, and the linter is mandatory. Types inside a game package therefore keep plain names and read as `gamekhimki.Service` at the call site, where the package qualifier already carries the prefix. The prefix belongs to the package and to every layer outside it.

_Consequence:_ `game_key` **values** did not move with the table — `smalltalk_khimki` is data, already unambiguous, and the art blobs are keyed on it. `/api/game/*` was kept as an alias for exactly one deploy cycle so a stale SPA would not break mid-run; that cycle is over, the registration is deleted, and `TestGameKhimkiLegacyPathAliasIsGone` pins its absence. `/app/game` still redirects permanently, which is a redirect rather than an alias.

_What this rule does **not** reach_ is the asset blob store, which is shared infrastructure rather than any game's property — see ADR-031. Only the game's own *state* moved namespace.
