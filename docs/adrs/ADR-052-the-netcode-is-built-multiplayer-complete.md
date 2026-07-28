# ADR-052 · The netcode is built multiplayer-complete, before there is a second player

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** All four Gambetta rungs — client-side prediction, server reconciliation, entity interpolation, lag compensation — built together, so the game is genuinely ready to become multiplayer rather than merely unblocked from it
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.5](../ARCHITECTURE.md#adr-052--the-netcode-is-built-multiplayer-complete-before-there-is-a-second-player) — this file is the detail behind it.
- **related:** [ADR-042](./ADR-042-everything-that-moves-is-a-function-of.md) · [ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md) · [ADR-049](./ADR-049-input-is-batched-to-fit-the-sockets-bound.md) · [ADR-050](./ADR-050-the-level-is-generated-on-the-server-and-sent.md)
- **code:** `internal/gamevanyadum/sim.go` — `Step`, the authority · `web/src/lib/vanyadumStep.ts` — the port · `web/src/lib/vanyadumPredict.ts` — prediction and reconciliation · `web/src/lib/vanyadumInterp.ts` — the interpolation buffer · `internal/gamevanyadum/history.go` — the rewind buffer · `internal/gamevanyadum/testdata/golden_*.json` — the conformance vectors
- **re-examine when:** the two `Step` implementations are proposed to diverge for any reason, or somebody suggests moving a decision to the client because prediction "already knows the answer"
- **the reversal:** this record supersedes the *staging* plan in the living doc's §5.1 (climb one rung on demand). It does **not** touch ADR-049, whose subject is the socket rate bound and which stands unchanged.

---

«ВАНЯДУМ» iteration 1 shipped rung one — an authoritative server and a client that draws what it is told — deliberately, in order to measure the feel before building anything on top of it. **It was measured and it failed**: movement reads as roughly twenty frames a second, because the camera only changes when a snapshot lands, while the screen redraws sixty times a second. So the netcode was going to be built anyway. The decision recorded here is that **all four rungs are built together, now**, and that the target is not "movement feels smooth" but "a second player can be added without changing a load-bearing shape".

_Why not keep climbing one rung at a time._ The staged plan was right about one thing — do not build what you cannot yet evaluate — and wrong about the cost curve. Each rung *individually* is cheap to add later; what is expensive to retrofit is the **shape** each one requires, and those shapes are shared:

- Prediction needs a per-command sequence number and an acknowledgement. Adding one to a live protocol is a coordinated deploy.
- Interpolation needs a **timeline** — a server timestamp on every snapshot and an array of entities that are not you. Adding an array later is another coordinated deploy, and the client has to grow a second rendering path beside the one it already has.
- Lag compensation needs the server to have been **keeping a history all along**. A ring buffer cannot be retrofitted onto the past.
- All three need the simulation to be identical on both ends, which is a claim that has to be *tested*, not asserted.

Doing them together means one protocol design, one conformance test, and one rendering path. Doing them apart means three protocol revisions and a client that grows a special case each time. That is the specific reason this is not the project's usual "build the smaller thing" answer: here the smaller things sum to more than the whole.

_What each rung is, in this game._ Prediction applies the player's own input locally through the same `Step` the server runs, keeping the commands pending; reconciliation resets to the server's authoritative position on each snapshot and replays whatever is still unacknowledged on top of it, with corrections eased in over about a tenth of a second rather than snapped. Interpolation renders everything that is *not* you about a hundred milliseconds in the past, between the two authoritative snapshots that bracket that instant, because another player's intent cannot be predicted. Lag compensation keeps a short ring of past world states on the server and resolves a shot against the world **as the shooter actually saw it**, rather than against the present.

_The cost, and it is the one this design spent a record avoiding._ `Step` now exists twice — in Go and in TypeScript — which is a second implementation of one rule, exactly what the project's rules tell you not to build. It is accepted here because prediction is not possible without it, and it is made safe rather than merely permitted:

- `Step` is a pure function of `(level, player, command)` with a fixed timestep and no clock, no randomness and no query. It was written that way in iteration 1 against precisely this day.
- The two are pinned by **golden vectors**: a Go test emits `(seed, input transcript) → position trace` as JSON into `testdata/`, and a vitest asserts the TypeScript reproduces it to the centimetre. Divergence is a red test in the ordinary gate, not a player walking through a wall somebody else can see.
- The **server remains the only authority**. Prediction is a rendering optimisation that is usually right; when it is wrong the server's answer wins, unconditionally and without negotiation. Nothing in this record moves a decision to the client, and the reconciliation path is also a free divergence audit — the server has already computed the truth, so a client that disagrees persistently is visible.

_What "ready for multiplayer" is claimed to mean_, stated precisely so it can be checked rather than believed: the arena holds several occupants and is not a room ([ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md)); the snapshot carries a peers array and a timeline, empty today; the simulation is deterministic and identical on both ends, with a test that says so; the server keeps a state history; and authority never moved. Adding a second player is then filling an array that already exists.

_What is deliberately still missing_, so this record does not overclaim. **Unreliable transport is the biggest structural gap**: WebSocket is TCP, so a lost packet head-of-line blocks everything behind it and a bad mobile moment produces freeze-then-jump — exactly wrong for a stream where the next snapshot supersedes the last. WebRTC DataChannel or WebTransport is the real answer and is a second transport with its own handshake, its own authentication and its own rate limiting, which is why it is not in this change. Also absent: delta compression against a baseline, interest management (the sector graph's neighbour set gives it almost free, and it is the first thing to add when peers appear), binary bit-packing, and an adaptive jitter buffer. Each is listed with its trigger in the living doc; none of them is a shape that is expensive to retrofit, which is exactly why they are not here.
