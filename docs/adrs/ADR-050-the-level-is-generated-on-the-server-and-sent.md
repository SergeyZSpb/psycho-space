# ADR-050 · The level is generated on the server and sent once

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The level is generated on the server and sent once
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-050--the-level-is-generated-on-the-server-and-sent-once) — this file is the detail behind it.
- **related:** [ADR-039](./ADR-039-game-content-is-a-go-catalogue-and-the-schema.md) · [ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md) · [ADR-051](./ADR-051-vanyadum-stores-no-art-at-all.md)
- **code:** `internal/gamevanyadum/level.go` — `Generate`, `buildWalls`, the sector graph · `internal/gamevanyadum/level_test.go` — the invariants, swept over 300 seeds · `web/src/lib/vanyadumLevel.ts` — the client, which only ever *builds meshes from* a level
- **re-examine when:** somebody proposes shipping the seed instead of the graph, or generating anything about the world in the browser.

---

A «ВАНЯДУМ» level is **generated in Go, once, when a run starts, and sent to the browser whole** — a few kilobytes of JSON over HTTP, referenced by index thereafter and never on a snapshot. The client's entire role is to build meshes from a description it was given.

_The trap this avoids._ The tempting alternative is to send the **seed** and generate the level a second time in TypeScript: it is smaller on the wire and it feels elegant. It is a generator implemented twice, and two implementations of a floating-point algorithm diverge — not in an obvious way, but on one seed in a hundred, at one wall, by a centimetre. The first symptom is a player walking through geometry another player can see, and the second is that nobody can reproduce it. This is the same instinct the project's *no legacy* rule states generally (**a second path to the same outcome**) and it is worth a record here because the wire-size argument is genuinely attractive and would be made again.

_What is actually sent._ The model is **Doom's**, not Quake's: a level is a set of rectangular sectors, each with its own floor and ceiling height, joined by portals cut through the walls they share. That buys everything the game needs — rooms at different heights, steps you walk up, doorways, and later a door a key opens — while collision stays a circle against a list of axis-aligned segments: exact, table-testable, and with no physics engine anywhere. Full BSP, arbitrary convex sectors and true 3D brushes would each be more faithful and each cost far more than this game can spend; the sector graph is the seam that would let something richer replace them.

The **walls are derived rather than authored**: every sector edge becomes a solid segment except where a portal is subtracted from it, leaving the jambs either side of a doorway as walls in their own right. Doing that once, on the server, is what lets the simulation never reason about doorways at all — it only ever pushes a circle out of a list of segments — and the same list is what the client extrudes into geometry.

_Why the graph rather than triangles._ Sending finished meshes would make the server decide how the game looks and would put a renderer's concerns into Go. Sending the graph keeps the split where it belongs: the server owns **where things are**, the client owns **how they are drawn**. It also keeps the payload small enough that this decision costs nothing worth measuring against the alternative.

_And why the level is not stored._ It is a pure function of its seed, so eight bytes in the run's row reproduce it exactly. That is what lets the arena be ephemeral ([ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md)) without losing the ability to say which заброшка a time was set on, and it is the same reasoning as the catalogue's ([ADR-039](./ADR-039-game-content-is-a-go-catalogue-and-the-schema.md)): store keys and seeds, never the thing they generate.

_The invariants are tested as properties, over hundreds of seeds, not as examples._ A generator is exactly the code where a hand-picked case proves nothing, because the failure that matters shows up on the seed nobody thought to write down. `level_test.go` sweeps 300 seeds asserting that no two rooms overlap, that every room is reachable from the spawn, that every doorway is wide enough for the player and rises no more than he can step, that nothing is generated inside a wall, that the portals really are cut out of the walls, and that there is always something to collect. A separate sweep walks a deterministic wanderer through eighty generated levels asserting only that he is always *somewhere* — escaping the level being the failure that turns into a black screen and a player falling forever.
