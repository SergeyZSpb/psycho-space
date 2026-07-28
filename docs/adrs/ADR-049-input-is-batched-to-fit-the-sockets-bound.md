# ADR-049 · Input is batched to fit the socket's bound, never to loosen it

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Input is batched to fit the socket's bound, never to loosen it
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.5](../ARCHITECTURE.md#adr-049--input-is-batched-to-fit-the-sockets-bound-never-to-loosen-it) — this file is the detail behind it.
- **related:** [ADR-033](./ADR-033-a-game-reads-the-socket-through-a-game.md) · [ADR-043](./ADR-043-a-verb-travels-over-the-socket-and-is.md) · [ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md)
- **code:** `internal/realtime/conn.go` — `msgPerSecond`, `msgBurst`, `MaxFrameBytes` (the bound) · `internal/gamevanyadum/gamevanyadum.go` — `MaxCommandsPerFrame`, `InputHz` · `web/src/lib/vanyadumInput.ts` — `createSampler`, `coalesce` (the client half)
- **re-examine when:** somebody proposes raising the socket's message rate for one room, or a per-room limiter. Both are the thing this record refuses.

---

The realtime layer caps a connection at **ten messages a second, burst twenty**, with a 4 KiB frame limit, and the check runs *before* a game's handler ever sees a frame — which is how a game inherits the bound for free ([ADR-033](./ADR-033-a-game-reads-the-socket-through-a-game.md)). A first-person shooter wants input far more often than ten times a second. **The bound is a security property, so the game fits inside it rather than asking for an exemption.**

_The shape._ The client samples input on every animation frame — roughly forty times a second — and sends **one frame per hundred milliseconds carrying up to four sub-steps**, each with its own `dt` and its own axes. The server applies them in order. Ten frames a second is a third of the socket's allowance rather than all of it, so a burst of retries or a clock running fast cannot get somebody disconnected for rate abuse in the middle of a fight.

_What batching buys that a lower sample rate would not._ A flick that starts and ends inside one send window still reaches the simulation, because the sub-steps that happened inside that window travel with it. Sampling at ten hertz instead would round the flick away, and on a touchscreen a flick is most of what aiming is.

_Three rules fall out, and each was a real defect before it was a rule._

- **A frame that says nothing is not sent.** Standing still with the screen untouched produces no sub-step at all, so no frame goes out. The obvious version records "dt of nothing" on every animation frame and ships ten frames a second of it forever, to a phone on mobile data, for a simulation that would do precisely nothing with them. Turning counts as something — aim is the client's own state and the server has to be told when it moves, even if the feet did not. This was found by the layout suite rather than by reasoning.
- **A stall is coalesced, not truncated.** A garbage collection, a phone waking, a slow frame — all produce more samples than a frame may carry. They are merged into at most four sub-steps whose `dt` values still sum to the real elapsed time, keeping the most recent axes in each bucket. Dropping the surplus instead would silently shorten the player's movement.
- **The surplus is dropped server-side, and the frame is not refused.** A frame carrying more sub-steps than the ratio allows is a client asking for extra simulation time. Truncating keeps the honest client that drifted by one step playing, and gains the dishonest one nothing — because the *time budget* on the arena ([ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md)) is what actually decides how much simulation a player gets, and no arrangement of frames can move it.

_Why not simply raise the limit for this room._ Because the limiter is not a performance knob. It bounds what one authenticated connection can make the process do — parse, dispatch, allocate — and it is the same control that protects every other realtime feature in the binary. A per-room exemption would mean the strength of that control depended on which room a client asked for, which is an argument nobody wants to have during an incident. Fitting inside it cost about forty lines of client code and produced a protocol that is better anyway: fewer, larger frames, and a client that says nothing when nothing happened.

_A consequence to accept honestly._ Input reaches the server up to a hundred milliseconds after the thumb moved, and there is no client-side prediction in this iteration — so the felt latency is that window plus the round trip. That is rung one of the netcode ladder, shipped deliberately in order to be measured. If it feels wrong, the answer is client-side prediction and reconciliation (the `seq`/`ack` pair is already on the wire, unused, for exactly that), **not** a faster send rate — a hundred milliseconds of batching is a small part of the problem prediction solves, and raising the rate would trade a security property for a fraction of the fix.
