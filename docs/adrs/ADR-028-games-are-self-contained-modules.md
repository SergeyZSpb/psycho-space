# ADR-028 · Games are self-contained modules

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Games are self-contained modules
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-028--games-are-self-contained-modules) — this file is the detail behind it.

---

Each game owns its Go package, its `<game>_*` tables, its routes, its views and its leaderboard code, and **no game imports another — not even where the code would be identical.** There is no shared games layer: no common game service, repository or table, no extracted game-UI shell, no generic board building. What is shared is *platform* — `realtime`, `session`, `account`, `crypto`, `db`, `logging`, `observability`, the `httpapi` router and middleware, and on the front end `apiFetch`, the error store, the theme and the app shell — and none of those may know that a game exists. The boundary test is blunt: deleting a game must be deleting its package, its migration, its routes and its views, and nothing else.

_Reasoning:_ these games are jokes for a small group, with a short and unpredictable life. The realistic future of any one of them is deletion, not extension — and premature sharing bills you at exactly the wrong moment, when you want something gone and find it welded to something you are keeping. A few duplicated files are far cheaper than that, so the duplication between games is the design and not debt to be cleaned up later.

_Consequence:_ the WebSocket is addressed as the game-agnostic `/api/realtime?room=…`, and a game's own message types live in that game's package and are published *through* the hub rather than added to it. `CLAUDE.md` → *Games are self-contained modules* carries the same rule as a working rule; that duplication is deliberate too, because that file has to stand on its own.
