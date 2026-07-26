# ADR-042 · Everything that moves is a function of absolute time

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Everything that moves is a function of absolute time
- **status:** Accepted · 2026-07-26
- **summary:** one paragraph in [ARCHITECTURE.md §8.8](../ARCHITECTURE.md#adr-042--everything-that-moves-is-a-function-of) — this file is the detail behind it.
- **related:** [ADR-038](./ADR-038-time-varying-state-is-computed-on-read-never.md)

---

Nothing on the plane accumulates. An NPC's position is `pattern(params, now − epoch)`; a player's is a point along a walk with a known start; a pose is derived from stats and the clock. All of it is evaluated on the existing 5 Hz render tick, none of it is stored, and **the tick still writes nothing**. NPCs consequently have no rows, no accounts and no placements — adding one is a catalogue entry, and because the client renders whatever entities it is sent, no client deploy either.

_Reasoning:_ the alternative is a simulation — advance each thing by its velocity on every tick — and it fails in a way that is invisible until it is not. A GC pause, a slow publish or a missed tick would permanently displace the world, so two players would slowly stop seeing the same yard with nothing anywhere reporting a fault. Because position depends only on `now`, a tick that is late, early, skipped, duplicated or served to a client that has just reconnected produces the identical correct answer. It is the same self-correcting shape as computing decay from timestamps instead of counting ticks ([ADR-038](./ADR-038-time-varying-state-is-computed-on-read-never.md)), applied to space.

**Motion is a keyed function table, and it lands with its second implementation.** `wander` and `patrol` arrive together, which is what earns the map; with one pattern it would have been a function and a map with a single key. The three axes of a character — appearance, motion, and (later) what tapping it does — are separate keys, so N characters × M ways of moving costs N + M rather than N × M, and a character reusing an existing pattern with new numbers costs no code at all.

**The epoch is fixed, not process start.** Two processes — or the same one after a deploy — have to agree about where a character is, and a per-process epoch would teleport the entire cast on every restart, several times a day.

**The walk is what makes distance mean anything.** Until now the position *was* the tap: the far side of the plane was 220 ms away, so distance was decorative. It is not decorative — the beer delivery is a race to *arrive*. A tap now starts `(from, to, startedAt)` and retargets from the **current interpolated position**, so changing your mind feels like changing your mind rather than queueing a second errand. Speed is in plane-widths per second, which is why the plane has a fixed 3:4 shape: a speed in plane-widths only means the same thing to two players if a plane-width does.

**Tiredness is decided once, server-side, at accept time.** For an ambitious tap the server may decide he gives up part way, and stores that in the walk — so everybody watches him sit down in the same spot at the same moment. A per-viewer roll would desynchronise the world and a client-side one would be forgeable. It is derived by hashing (account, destination, instant) rather than drawn from a generator: no seed, no injection, no stored state, and a test can assert an exact outcome. It also converts a limitation into content, which is the point — a speed cap alone reads as a tax, whereas a Ваня who sits down halfway across the yard and announces that he is tired *is* the game.

_Consequence — the yard is never empty, and that is why position had to become durable first._ A player past the reconnect grace is no longer removed: they are rendered **asleep where they stood**. With five to thirty friends the yard is almost never occupied by two people at once, so without this a solo visit is a bare field — and the real absent friends are a far better answer to that than filler characters would have been. The sleepers cost one lazy query per process (triggered by a human saying hello, never a timer), are capped and newest-first so the roster grows with the size of the group rather than the age of the game, and are counted separately from the people: the frame carries an explicit `here` count so the client can say how many are in the yard **without ever learning to tell a person from an NPC**.
