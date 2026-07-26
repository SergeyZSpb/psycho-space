# ADR-038 · Time-varying state is computed on read, never ticked

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Time-varying state is computed on read, never ticked
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.8](../ARCHITECTURE.md#adr-038--time-varying-state-is-computed-on-read-never-ticked) — this file is the detail behind it.

---

Anything that changes with the clock is stored as the pair `(value, as_of)` and evaluated when somebody reads it — `clamp(value − rate × hoursSince(as_of), min, max)`. There is no cron, no background goroutine, no per-entity timer and no scheduler anywhere in the system. Facts that a passage of time *creates* rather than merely alters — a pet's death — are **materialised lazily and idempotently by the first read that observes them**, at the instant derived from the pair rather than at the moment somebody happened to look. The 5 Hz realtime broadcast is not an exception to this: it renders, and writes nothing.

_Reasoning:_ the obvious alternative is a job that walks every pet every minute and decrements. It costs a scheduler, a leader problem the day there are two processes, a per-entity write rate proportional to the population, and a class of bug where the job stops and the world silently freezes. The closed form costs one subtraction, and reading a value after a month away is exactly as cheap as reading it a second later, because it *is* the same subtraction. Offline progression is then not a feature anybody built — it is what the expression already means.

_Two properties are load-bearing, and both are easy to break by accident._ First, the result is **exact, not an approximation of ticking**: linear decay evaluated at an instant is precisely what a continuous simulation would have produced, so there is no divergence between "was away" and "was watching" and nothing to gain by choosing when to look. **That safety is a property of linearity, not of the pattern.** The moment a rate depends on another decaying value — compounding, one stat draining another — the closed form becomes an approximation whose *error sign* decides whether being absent beats playing; a shipped idle game had exactly that bug and made not playing strictly better. If non-linear decay is ever wanted, derive the closed form from the continuous model and check the direction of its error deliberately. Second, **server time is the only clock**: `now` is the server's and `as_of` is a column, so a device with a wound-forward clock changes nothing, and the client is sent `server_now` so its own drawing can correct for its skew.

_Consequence:_ a `GET` is allowed to write, which reads oddly in a route table and is the honest shape here — the write is idempotent and conditional (`UPDATE … WHERE died_at IS NULL`), so concurrent observers converge and the loser of the race can report the winner's timestamp without reading it back, because both derive the identical instant from the identical pair. It also means **nothing happens to a world nobody is looking at**, which is correct rather than a compromise: an event no player could have witnessed is not an event.
