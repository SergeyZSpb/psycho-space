# ADR-041 · The broadcast tick renders from a cache, and position outlives the process

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The broadcast tick renders from a cache, and position outlives the process
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.8](../ARCHITECTURE.md#adr-041--the-broadcast-tick-renders-from-a-cache-and) — this file is the detail behind it.

---

The 5 Hz roster carries each entity's **appearance** — art key, name, and a pose derived from that pet's own stats — so every player sees every Ваня properly rather than only their own. That data is durable and the tick is not allowed to read it: appearance comes from an **in-memory display cache**, filled when a client says hello and refreshed by the HTTP read path, and the tick reads nothing but memory. Separately, a pet's **position becomes durable** — written once when its owner's last connection goes away, and on shutdown for everybody still standing — so a deploy no longer teleports the yard back to the middle.

_Reasoning:_ the tick is a render step, and ADR-034 and ADR-038 both rest on it owning nothing: that is what makes a late, early, skipped or duplicated tick harmless. A query per tick would be five a second per room forever, to re-fetch a name and a skin key that change roughly never — and it would put a database round trip inside the one loop that must never block, where a slow query becomes a frozen yard for everyone.

**What is cached is the pairs, not the pose.** This is the whole subtlety. A pose is a function of the clock, so caching one would be caching an answer that expires — an hour later the plane would still be drawing a comfortable Ваня who has been at death's door since lunchtime, and nothing would be obviously wrong. Caching `(value, as_of)` and deriving the pose on each tick costs one subtraction per stat, needs no invalidation, and stays correct indefinitely, for exactly the reason ADR-038 gives: the value is a function of the pair and the clock rather than an accumulation. The cache is therefore refreshed for *correctness of identity* (a rename, a new skin) and never for *freshness of derived state*.

**The two moments the durable half is read are both human-paced.** A hello is a fresh socket, and it arrives on that connection's own read pump — so a slow query delays one client's next frame and never the room's. An action is an HTTP request that already touches the database. There is deliberately no join/leave callback for this: the hub stays game-agnostic and presence is still pulled rather than pushed (ADR-033).

**Position is written on departure, never on movement.** Thirty players moving at the socket's ten messages a second would be three hundred writes a second to persist something read only when they come back. The tick *notices* a departure and hands it to a writer goroutine down a buffered channel — a full queue drops rather than blocks, because the plane must keep running and the cost is one Ваня reappearing where he was last written. `saved` on the placement is what makes an absence cost one write rather than one per tick for the whole grace period.

_Consequence, and it is the part that only fails in production:_ a graceful shutdown cancels the context that the tick loop, the writer and every socket share, so without care a deploy would write nothing at all — the exact case durable position exists for. `Run` therefore flushes every held position on its way out, under a fresh bounded context, because the context that just ended is the reason it is flushing. A crash still loses the last position, and that is accepted: it is the same thing a dropped queue costs, and it is an acceptable failure for a nap.
