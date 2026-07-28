# ADR-056 · The office is one process-wide arena, not one per run

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The office is one process-wide arena, not one per run
- **status:** Accepted · 2026-07-29
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-056--the-office-is-one-process-wide-arena-not-one-per-run) — this file is the detail behind it.
- **related:** [ADR-028](./ADR-028-games-are-self-contained-modules.md) · [ADR-033](./ADR-033-a-game-reads-the-socket-through-a-game.md) · [ADR-045](./ADR-045-a-location-is-not-a-room-the-roster-is.md) · [ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md) · [ADR-052](./ADR-052-the-netcode-is-built-multiplayer-complete.md) · [ADR-057](./ADR-057-a-dom-game-may-own-a-fixed-step-simulation.md)
- **code:** `internal/gamekaren/office.go` — the world, its occupant map and its lifecycle · `service.go` — the office pointer, the tick and the one writer · `boss.go` — `StepBoss`, which targets the nearest occupant and is where co-op actually lives
- **re-examine when:** somebody proposes an office per shift, an office per group of friends, or a second office of any kind — and when the occupant cap stops being a constant.

---

«СИМУЛЯТОР КАРЕНА» holds **one office for the whole process**. Players join it and leave it; it is created by the first shift that starts and destroyed when the last occupant goes. This is the opposite of what the game before it does, and the difference is worth a record because both are correct.

_Why «ВАНЯДУМ» is not the precedent._ That game gives **each run its own arena** ([ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md)), keyed by account, because a run is a freshly generated заброшка that exists for one player: two people are never in the same building, and putting them there would mean agreeing on a level neither of them asked for. The arena map is therefore the natural model, and "an arena is not a room" is what keeps the platform ignorant of runs.

_Why this game is the other shape._ There is one опенспейс. The premise is a workplace, the design is co-op for two or three, and "co-op" here means **several Карена in the same office being chased by the same bald man** — not several offices synchronised. So the world is process-wide, the occupant map is inside it rather than around it, and joining a shift is walking into a room that may already have somebody in it.

_What that buys, and it is the entire reason:_ **the pursuit rule is the co-op.** Лысый walks at the nearest live occupant. That single line turns positioning into a negotiation nobody has to be taught — the moment there are two of you, every step you take is also a decision about your colleague — and it requires no verb, no wire field and no UI. A per-run world cannot express it at all, because there is nobody else in the world to be nearer than you.

_Why iteration 1 ships it anyway, with one player in it._ Iteration 1 has no peers on screen and no co-op verbs; it could have been built as one arena per shift and converted later. It was not, because the conversion is not an iteration — it is a rewrite of the state model, the tick, the snapshot addressing and the capacity rule, arriving under the name of a feature. This is the same argument [ADR-052](./ADR-052-the-netcode-is-built-multiplayer-complete.md) made about netcode and settled the same way: build the shape that supports the second player, then add the second player. Multi-occupancy costs a map and a cap on day one; retrofitting it costs the day.

_One world, but per-occupant frames — a third addressing model, deliberately._ The yard broadcasts **one roster to everybody** because everybody is looking at the same thing ([ADR-045](./ADR-045-a-location-is-not-a-room-the-roster-is.md)). «ВАНЯДУМ» unicasts because **every player has his own world**. This game is neither: there is one world, but each occupant's frame carries *his* salary, *his* multiplier, *his* dash cooldown and *his* acknowledged input sequence, so the snapshot is built per occupant and addressed to a connection through `PublishTo`. The room `karen` still exists and is still what the platform knows ([ADR-033](./ADR-033-a-game-reads-the-socket-through-a-game.md)); it is the membership query, not the fan-out.

_What it costs, stated rather than discovered._

- **A restart loses the office**, and with it every shift in flight — exactly as a restart loses «ВАНЯДУМ»'s arenas and the hub's presence. A shift is a few minutes and a lost one costs a replay. Accepted, not fixed: persisting a world twenty times a second is the thing this design exists not to do.
- **Capacity is a property of the office**, not of a map of them. `MaxOccupants` is a hard cap and a fourth player is refused at `POST /shifts` rather than queued, which is the honest answer for a game designed for three.
- **An empty office is torn down entirely** and the next shift builds a fresh one, so there is no idle world ticking for nobody and no state that quietly accumulates between play sessions. The boss's "walk back toward spawn when there are no targets" is therefore a state that exists for at most the gap between the last occupant leaving and the teardown — it is written because the office must be well-defined at every instant, not because anybody will see it.
- **The office is the unit of contention**, so everything that touches it takes one lock, and the discipline «ВАНЯДУМ» already needed applies unchanged: build bytes under the lock, publish after it. Calling `PublishTo` while holding the office mutex would put the hub's write path inside the simulation's critical section.

_What it does not change._ Postgres is still touched **once per shift** — one summary row when the shift ends — and never on a tick, so this stays on the right side of [ADR-038](./ADR-038-time-varying-state-is-computed-on-read-never.md) for the same reasons [ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md) does. And the office shares nothing with any other game's world: it is re-implemented rather than factored out of «ВАНЯДУМ»'s arena, which is [ADR-028](./ADR-028-games-are-self-contained-modules.md) working as intended.
