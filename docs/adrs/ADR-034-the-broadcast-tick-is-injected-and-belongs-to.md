# ADR-034 · The broadcast tick is injected, and belongs to the game

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The broadcast tick is injected, and belongs to the game
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.5](../ARCHITECTURE.md#adr-034--the-broadcast-tick-is-injected-and-belongs-to-the-game) — this file is the detail behind it.

---

`gamevanyagotchi.Service.Run(ctx, tick <-chan time.Time)` takes its tick as a parameter. `main` passes a `time.Ticker`; tests pass a channel they fire themselves. The hub has no tick of its own.

_Reasoning:_ two separate things, both load-bearing.

**It is a render tick, not the background timer this project rules out.** The rule that nothing runs on a timer is about *state*: no cron, no per-entity goroutine, nothing that writes to the database because time passed. This loop writes nothing, owns nothing and decides nothing — it reads the hub's current members and sends a snapshot. Because the frame is full state rather than a step forward from the last one, a tick that is late, early, skipped or duplicated produces the same correct frame. That property is what makes the distinction safe rather than a euphemism.

**Injecting it removes every timing sleep from the tests.** The repository has no clock injection anywhere, and determinism has so far come from substituting network dependencies. A test that fires the tick and then reads the frame it caused has no race to lose; the alternative is `time.Sleep(250ms)` in every realtime test, which is slow when it works and flaky when it does not. It is an ordinary constructor-style parameter, not test-only code on a production path — the same shape as `session.NewManager`'s injected TTL.

_Consequence:_ the rate lives in `gamevanyagotchi.BroadcastInterval` and is half of a two-part decision — the other half is the CSS transition duration on the client, chosen to be slightly longer so consecutive segments overlap. Changing one without the other makes motion either stutter or lag, which is why the constant is documented as a pair rather than exposed as a knob.
