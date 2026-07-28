# ADR-048 · The simulation is a server-owned fixed-step tick over in-memory arenas

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The simulation is a server-owned fixed-step tick over in-memory arenas
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-048--the-simulation-is-a-server-owned-fixed-step-tick-over-in-memory-arenas) — this file is the detail behind it.
- **related:** [ADR-034](./ADR-034-the-broadcast-tick-is-injected-and-belongs-to.md) · [ADR-038](./ADR-038-time-varying-state-is-computed-on-read-never.md) · [ADR-041](./ADR-041-the-broadcast-tick-renders-from-a-cache-and.md) · [ADR-042](./ADR-042-everything-that-moves-is-a-function-of.md) · [ADR-043](./ADR-043-a-verb-travels-over-the-socket-and-is.md) · [ADR-049](./ADR-049-input-is-batched-to-fit-the-sockets-bound.md)
- **code:** `internal/gamevanyadum/sim.go` — `Step`, the pure movement function · `arena.go` — one run's world, the time budget · `service.go` — the tick, the arena map, the one writer · `cmd/psycho-space/main.go` — where the ticker is injected
- **re-examine when:** something proposes to persist arena state, to tick anything durable, or to give `Step` a clock, a random source or a query.

---

«ВАНЯДУМ» runs a **fixed-step simulation at 20 Hz**, in the server, over arenas that live entirely in memory. This is a new kind of thing in this system, and this record exists because the obvious objection — *"nothing in this project runs on a timer"* — is correct about the rule and wrong about what it forbids.

_Why the yard's mechanism does not transfer._ «Ванягоччи» gets away with no simulation at all, because everything it draws is closed-form: a position is `pattern(params, now − epoch)`, so a tick that is late, early, skipped or duplicated still produces the correct world ([ADR-042](./ADR-042-everything-that-moves-is-a-function-of.md)), and every stat decays from a `(value, as_of)` pair computed on read ([ADR-038](./ADR-038-time-varying-state-is-computed-on-read-never.md)). **Collision destroys closed form.** Where a player is at *t* depends on every wall he slid along on the way there, which is a path integral rather than an expression; there is no `position(t)` to write down. So it has to be integrated, step by step, at a fixed rate — a variable step would make the same input produce different outcomes on a fast phone and a slow one, which is both unfair and untestable.

_Why this does not reopen ADR-038._ That record is about **durable** state: no background job may sweep a table, expire a row, or advance a stored value, because the moment one exists every read has to wonder whether it has run yet. Every clause of it is honoured here.

- The tick touches **memory only** — the same discipline the yard's 5 Hz broadcast already follows in reading from a display cache and never from the pool ([ADR-041](./ADR-041-the-broadcast-tick-renders-from-a-cache-and.md)).
- **Postgres is touched exactly twice per run:** once at the start, to nothing but create an id, and once at the end, to write a summary. Never on a tick. The one `INSERT` this game makes is queued to a separate writer goroutine, so a slow database delays a row and never a simulation.
- An arena is **ephemeral by design and by admission**. A restart loses runs in flight, in the same way and for the same reason the hub loses presence. A run is a few minutes long and a lost one costs a replay; the alternative is persisting a world twenty times a second, which is the thing this whole design exists not to do.
- The ticker is **injected** ([ADR-034](./ADR-034-the-broadcast-tick-is-injected-and-belongs-to.md)): `main` passes a `time.Ticker` and every test passes a channel it fires by hand, so "advance exactly thirty-two steps and then look" is a thing a test can say and there is not one sleep in the suite.

_The level is not state either._ It is a pure function of a seed ([ADR-050](./ADR-050-the-level-is-generated-on-the-server-and-sent.md)), so what survives a run is eight bytes and a summary, and the geometry is reproducible rather than stored.

_An arena is not a room, and the platform never learns what one is._ Every player has his own world, but the realtime layer knows only that «ВАНЯДУМ» listens in the room `vanyadum`; snapshots are addressed to a **connection** through the existing `PublishTo` seam. A room per run was refused for the reason [ADR-045](./ADR-045-a-location-is-not-a-room-the-roster-is.md) refused a room per location: it would push a game's vocabulary into the unprefixed platform file that the naming convention exists to keep clean. What did change there is one line's worth: `httpapi` now holds a **map of room name to handler** rather than a single handler, so the set of valid rooms is exactly what the composition root registered — and each game exports its own room name, so no unprefixed package spells out the name of a game. Multiplayer is then *arenas with more than one occupant*, which is why the arena is written to hold several players from the first day even though iteration 1 never puts two people in one.

_Authority, and the two things it required._ The client sends **intent and never a fact** — the axes it is pushing and where it is looking, never a position, a health value or a claim to have hit something. Two consequences fall out and both are load-bearing.

**`Step` is pure.** It takes a level, a player and a command, and returns the next player; it reads no clock, holds no state and never queries anything. That is what makes movement table-testable without a socket, and it is written that way against a future that has not arrived: if the feel gate fails and the netcode climbs to client-side prediction, this exact function has to run in the browser as well, pinned to the Go original by golden vectors — which is only possible while it depends on nothing ambient. Do not give it a clock, a random source or a map iteration.

**Simulated time is spent, not claimed.** The socket permits ten frames a second and each may carry four sub-steps of up to 0.2 s, so a client that fills every frame would ask for eight seconds of simulation per real second — running eight times faster than everybody else with **no single field out of range anywhere**. So each arena accrues a time budget at exactly real time and spends it on the commands it receives, with a half-second cap so an honest burst from a backgrounded phone can still catch up. This is the guard that a per-field clamp cannot be: every individual value in that attack is legal, and only the total is not.
