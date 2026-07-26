# ADR-012 · Theme progress steers the options but never awards the win

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Theme progress steers the options but never awards the win
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-012--theme-progress-steers-the-options-but-never-awards-the-win) — this file is the detail behind it.

---

The server tracks which of the character's deep themes the conversation has genuinely opened, uses that to aim one answer slot at a still-closed theme, and marks a theme open by itself when the conversation has engaged it enough times.

_Reasoning:_ two separate failures. Steering the slot at the *last* remaining theme every turn made the conversation collapse onto one subject and the run unwinnable — measured at 15 of 20 option sets having all four options on the same topic. And making theme state the win condition would let a tampering client award itself the ending, so `achieved` stays the judge's reading of the dialogue.
