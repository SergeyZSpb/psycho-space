# ADR-059 · The catch is resolved in the victim's timeframe, because being caught is a hit test

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** «СИМУЛЯТОР ФИНТЕХА» rewinds the лысый and Claude Code by however far behind the victim's screen is, and resolves the catch against that — the fourth Gambetta rung, in a game with nothing in it that shoots.
- **status:** Accepted · 2026-07-30
- **summary:** one paragraph in [ARCHITECTURE.md §8.5](../ARCHITECTURE.md#adr-059--the-catch-is-resolved-in-the-victims-timeframe-because-being-caught-is-a-hit-test) — this file is the detail behind it.
- **related:** [ADR-052](./ADR-052-the-netcode-is-built-multiplayer-complete.md) · [ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md) · [ADR-049](./ADR-049-input-is-batched-to-fit-the-sockets-bound.md) · [ADR-057](./ADR-057-a-dom-game-may-own-a-fixed-step-simulation.md)
- **code:** `internal/gamefintech/office.go` — `trail`, `seenBy`, `Occupant.RTT`, and the two hit tests in `Advance` · `internal/gamefintech/message.go` — `Input.Seen`, the `k` field it finally decodes · `internal/gamefintech/gamefintech.go` — `RenderDelayPeriods`, `RenderDelaySeconds`, `CatchRewindMax` · `web/src/lib/fintechInterp.ts` — the buffer that creates the delay being compensated for
- **re-examine when:** something proposes to resolve a new consequence against the office's present, to let the client choose its own render delay, or to raise `CatchRewindMax` without saying what it costs in metres.

---

A shift ends when the лысый reaches you. That is a **hit test**, and this game resolved it in the office's present against a man the victim only ever sees in the past — so shifts ended while he was still drawn a metre or two short. This record is the fix and, more usefully, the correction of a claim: `fintechInterp.ts` said there was no fourth rung to build here because *nothing in this game shoots*. Being caught is the shot.

_The ledger, because the size is the argument._ Three errors, and they **add** in exactly the situation the game is about — running away.

| | drawn at | error, at his 4 m/s and your 6.4 m/s |
|---|---|---|
| your own Карен | predicted now, plus up to 25 ms of carry | — |
| the office's copy of you | now − (input batching + uplink) | you are **0.6–1.0 m** nearer him than drawn |
| the лысый on your screen | server truth − (render delay + downlink) | he is **0.6–0.9 m** nearer you than drawn |

`CatchRadius + PlayerRadius` is 1.2 m. The accumulated error is **1.4–1.8 m**, which is larger than the thing it is an error in — so he catches you while drawn up to three metres off, and there is no tuning of the radius that fixes it, because the error scales with the connection rather than with the geometry.

_What it does._ The office keeps a short ring of both men's positions, one entry per tick, and resolves each occupant's catch against the entry their screen is actually showing. Two terms decide which entry, and they are different things:

- **The round trip**, derived rather than reported. Every input frame carries `k`, the last snapshot tick that client had received; the tick rate is fixed, so the gap between that tick and the office's current one *is* the loop — downlink, the client's own send window, uplink. It is smoothed with a slow exponential average, because one late frame is not a slower connection and rewinding by a spike would resolve a catch against a world nobody was ever looking at. This is the field ADR-052 put on the wire for «ВАНЯДУМ» and this game accepted and discarded.
- **The render delay**, which is not latency at all. The interpolation buffer deliberately draws everything unpredicted 1.5 snapshot periods in the past, and that is a property of the *renderer* rather than of the network — so a client on a perfect connection is still 75 ms behind, and a compensation that ignored it would leave a third of the original defect in place.

_Which is why the render delay is served rather than chosen._ Both ends need the same number and only one of them can be authoritative: the browser sizes its buffer with `sim.render_delay_ms` and the office rewinds by exactly the same value. A client picking its own would be choosing how far behind the office believed it to be — which is the same authority mistake as sending a position instead of an intent.

_It is bounded, and the bound is stated in metres._ The rewind is derived from a number the client controls, so `CatchRewindMax` caps it at 0.3 s. At his walking speed that is 1.2 m — one catch radius — so the worst a dishonest client buys is being caught a third of a second later, while he keeps walking at them the whole time. The honest case is comfortably inside it: 75 ms of render delay plus a bad mobile round trip and a full input window is around 250 ms. A tick claimed from the *future* is discarded rather than clamped, because it is a client guessing or an office rebuilt underneath one, and neither is a measurement.

_Claude Code is rewound too_, and not for symmetry. He is drawn from the same buffer, so landing a slow on somebody who watched him miss is the identical complaint with a smaller consequence.

_What is deliberately NOT rewound._ Pursuit — who he walks at — uses present positions, because it is a decision rather than a hit test and rewinding it would make him chase a ghost. The bottle and the кальян are resolved in the present too: they do not move, so there is no second timeframe to disagree with. And the grin is not rewound, because it travels on the frame with the position it belongs to and the client interpolates the pair together.

_The visible consequence, accepted openly._ He can now be drawn standing on you for a fraction of a second without the shift ending. That is the standard lag-compensation trade — the shooter's «I was behind cover» — and it is the right way round: the player is given the version of events they watched, and the man is still walking at them.
