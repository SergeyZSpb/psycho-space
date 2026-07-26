# ADR-037 · One account is one entity, and the wire carries a pseudonym

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** One account is one entity, and the wire carries a pseudonym
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.5](../ARCHITECTURE.md#adr-037--one-account-is-one-entity-and-the-wire) — this file is the detail behind it.

---

A game's roster contains **one entity per account**, not one per connection, and the id it publishes is a **pseudonym derived per process** — `HMAC-SHA256(processKey, accountID)`, base64url, truncated — where `processKey` is 32 bytes of `crypto/rand` minted when the service is constructed and never persisted or configured. `Hub.PublishTo(ctx, connID, msg)` is added as a third game-agnostic seam so a service can answer one connection; a game learns which entity a client is by sending a hello and being told, over that unicast.

_Reasoning:_ three separate things forced it, and one mechanism settles all three.

**Signing in on a second device produced a second Ваня.** The hub allows three connections per account, and the roster was keyed by connection, so a phone and a laptop were two dots that could stand in different places and be moved independently. That is a bug about *identity*, not about presence: the hub is right that presence is per connection, and the game is what decides an account is one thing in its world. Keying the game's own state by account fixes it where the decision belongs and leaves `realtime` unchanged.

**The obvious id to use instead was the account's, and it must not be.** `accounts.id` is a durable cross-session identifier, and a roster is broadcast to everybody else in the room — so publishing it would hand every player a stable handle on every other player, permanently, for the sake of drawing a circle. The pseudonym is stable exactly as long as it needs to be (one process) and no longer, which is the same lifetime presence already has: the key dies with the process that minted it, so nothing correlates across a restart. It needs no configuration, and there is no key to rotate wrongly.

**A client cannot recognise itself from a pseudonym**, by construction — that is what makes it one. So the server has to say, and saying it to one connection rather than to the room is what `PublishTo` is for. The request arrives through the existing inbound `Handler` (the client sends a hello, the reply goes back to the connection that asked), so no join/leave lifecycle hook was needed and ADR-033's seam count grew by exactly one.

_Consequence:_ identity is the game's business, not the transport's. The hub's `Member` still carries both a connection id and an account id and still decides nothing; what a game does with them is this record. A pseudonym also changes on every reconnect, so a client asks again each time it opens a socket rather than caching the answer.

_Found while fixing it:_ the connection-cap rejection never actually delivered its `bye`. `Register` runs after the 101, and the refusal path called `Conn.Close`, which only queues onto a channel that the write pump drains — and `Serve`, which starts that pump, is never reached for a refused connection. So the frame explaining "too many connections" was written to nobody and the socket was dropped bare. `Conn.Refuse` now writes it on the calling goroutine, which is safe precisely because no pump exists yet to race it.
