# ADR-033 · A game reads the socket through a game-agnostic `Handler`, and pulls presence

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** A game reads the socket through a game-agnostic Handler, and pulls presence
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.5](../ARCHITECTURE.md#adr-033--a-game-reads-the-socket-through-a-game) — this file is the detail behind it.
- **related:** [ADR-037](./ADR-037-one-account-is-one-entity-and-the-wire.md)
- **code:** `internal/realtime`

---

`internal/realtime` gained exactly two seams: a `Handler` interface (`HandleInbound(ctx, Member, room, payload)`) called on the connection's own read pump, and `Hub.Members(ctx, room) []Member`. The handler is supplied by the composition root through `httpapi.Deps.RealtimeHandler`, which is typed as the interface, so no platform file names a game. «Ванягоччи» owns its own wire types in its own package and publishes through `Hub.Publish`.

_Reasoning:_ before this, the read pump discarded every payload — it read frames only to enforce the read limit and the rate limit — so no inbound message could reach any domain service at all, and the hub had no way to say who was in a room. Both were needed, and the shape of each was the decision.

**Inbound dispatch runs on the read pump, never on the hub goroutine.** That goroutine owns every room and fans out to every client; a game handler that blocked there would freeze the whole yard behind one player, which is precisely what the hub's non-blocking fanout exists to prevent. One read pump per connection means a slow handler delays only the client that sent the frame. The rate check runs *before* the handler for a second reason worth stating: it means a game inherits the socket's bound for free instead of every game having to remember to limit itself.

**Presence is pulled, not pushed.** The tempting alternative — the hub notifying a service on join and leave — makes presence a thing two components each believe they know, and the bug is then a service whose roster has quietly drifted from the hub's. Rebuilding the roster from `Members` on every broadcast cannot drift, needs no join/leave bookkeeping, and prunes departed connections by construction. It also composes with the backpressure design: a roster built from the current member set *is* idempotent full state, so a dropped frame costs nothing.

`Member` is a value type carrying a connection id and an account id, deliberately not the `Sink` — a service should be able to ask who is present without acquiring the ability to write to, or close, somebody's socket.

_Consequence:_ these seams say who is connected, not who a player *is*. A game therefore decides for itself what an entity is and what identifies it on the wire — one entity per account under a per-process pseudonym, which is [ADR-037](./ADR-037-one-account-is-one-entity-and-the-wire.md). The hub stays correct that presence is per connection; `Member` carries both ids and decides nothing.
