# ADR-040 · A stat may drive another stat's rate, and it is still exact

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** A stat may drive another stat's rate, and it is still exact
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.8](../ARCHITECTURE.md#adr-040--a-stat-may-drive-another-stats-rate-and-it-is-still-exact) — this file is the detail behind it.
- **related:** [ADR-038](./ADR-038-time-varying-state-is-computed-on-read-never.md)

---

A stat's drain may be raised while **another** stat sits in a named range — health falls faster while beer is empty and faster while the bladder is full. ADR-038 said the closed form is exact "only because the decay is linear" and warned that a rate depending on another decaying value turns it into an approximation. That warning stands as written; what this record adds is the **narrow shape in which the coupling is still exact**, and the three conditions that make it so. Outside them, ADR-038's warning applies unchanged.

The conditions, all three required:

**The coupling is one-directional, and the graph is one layer deep.** Beer and bladder drive health; nothing drives them but time and the player, and health drives nothing. There is no feedback term, so no differential equation — just one integrand that depends on functions already known in closed form. A stat with penalties may never itself be a driver, and that is asserted by a test rather than left to care.

**Every driver is linear and monotone between writes.** So the instant it crosses a threshold is solvable directly, and once crossed it stays crossed — the clamp at the bound only holds it there. A penalty is therefore a **suffix** of the integration window, described by a single instant: its **onset**. The penalised stat becomes piecewise-linear with one breakpoint per penalty, and both its value and the instant it reaches zero are computed by walking those segments. Exact, `O(penalties)`, nothing stored.

**Every write re-stamps every stat.** This is the one that is easy to skip and expensive to get wrong. Health is integrated from its own `as_of`, and the drivers' trajectories have to be known across that whole window — so all the pairs must share one instant. Write a single stat alone and the maths silently **erases damage**: relieve yourself at noon, and the morning's full bladder is re-derived from the post-reset pair, which says it was never full. Nothing errors; the number is just quietly wrong in the player's favour. The repository therefore exposes `WriteStats` (plural) and no single-stat setter, so the invariant is hard to violate by accident, and a unit test asserts that an action writes every row with one `as_of` — a property invisible from the response.

_Consequence:_ the client cannot interpolate from the catalogue rate any more, because the effective rate is a function of state it would have to re-derive. Rather than ship a second implementation of this arithmetic in TypeScript — kept honest by nothing, and the exact mistake refused for NPC motion — each stat is sent with the **effective rate it is suffering right now**, and the browser draws a straight line from it. That is correct until the next onset, which is hours away, and every action answers with freshly computed server state regardless.

_And it earns its keep in the design, not just the maths:_ health stops being a chore of its own and becomes the readable consequence of two needs the player can actually act on. The bar you cannot press is driven by the two you can, each threshold is the same number as the driving bar's warning mark, and the drink that keeps him alive is what fills his bladder — so the two loops are one system rather than two timers.
