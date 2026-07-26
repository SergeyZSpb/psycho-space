# ADR-029 · The judge runs on DeepSeek V4 Flash

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The judge runs on DeepSeek V4 Flash
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-029--the-judge-runs-on-deepseek-v4-flash) — this file is the detail behind it.
- **related:** [ADR-030](./ADR-030-game-modules-are-named-gamename.md)

---

«Смолтолк в Химках» judges its turns with `deepseek-v4-flash` over the OpenAI-compatible endpoint (`PSYCHOSPACE_LLM_MODEL` carries the full folder-specific model URI), replacing `yandexgpt-5-lite` — and it runs with **`reasoning_effort: "none"`**.

_Reasoning:_ DeepSeek costs more per turn than the model it replaced, and the difference buys visibly better play — it produced the first winning run seen in any audit. Its content filter is also not the one that pushed the third theme off substance use (ADR-014), so the character can swear in character. Reasoning is off because this model bills `reasoning_content` as output, the dearest rate, and twice it spent the entire completion budget thinking and returned an empty reply — `finish_reason: length`, 1500 completion tokens, zero characters of dialogue, a turn lost and billed in full. Judging is a rule-following task, not a puzzle, so the chain of thought was buying nothing that the failure class cost. (`thinking` and `enable_thinking` are rejected by this endpoint; `reasoning_effort` is the knob it accepts.)

_Consequence:_ the `/api/game-khimki/attempt` limit was halved from 10/min to **5/min per client IP**, because a turn costs about twice what it did — and there is still no per-account cap, so one determined player remains the real cost exposure. The salvage path stays even though this model rarely returns malformed JSON, because a bad turn costs a player their move.

Per-turn economics — the price table, the current cost per turn and how it got there — stay in `RUNBOOK.md` → *Working on the game*, to be re-measured as models and prices move rather than superseded here.
