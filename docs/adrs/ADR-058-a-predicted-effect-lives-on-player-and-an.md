# ADR-058 · A predicted effect lives on `Player`; an unpredicted one lives on the occupant

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** where a new effect's state belongs in a game whose `Step` is ported to the client and pinned by golden vectors
- **status:** Accepted · 2026-07-30
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-058--a-predicted-effect-lives-on-player-an-unpredicted-one-lives-on-the-occupant) — this file is the detail behind it.
- **related:** [ADR-037](./ADR-037-one-account-is-one-entity-and-the-wire.md) · [ADR-048](./ADR-048-the-simulation-is-a-server-owned-fixed-step.md) · [ADR-052](./ADR-052-the-netcode-is-built-multiplayer-complete.md) · [ADR-056](./ADR-056-the-office-is-one-process-wide-arena-not-one.md) · [ADR-057](./ADR-057-a-dom-game-may-own-a-fixed-step-simulation.md)
- **code:** `internal/gamefintech/sim.go` — `Player.SlowLeft`, the only field this decision put there · `office.go` — `Occupant.Invincible`, `Occupant.Persona`, the ones it kept out · `web/src/lib/fintechStep.ts` + `fintechPredict.ts` — the port and the reconcile spread · `internal/gamefintech/testdata/step_vectors.json` — the artefact the first branch costs
- **re-examine when:** a game arrives whose client predicts something other than its own player, or the golden-vector regeneration stops being the expensive half of the decision.

---

An effect that **changes how the player moves** lives on `Player`, is ported to TypeScript, is covered by the golden vectors, and is folded into the predictor's reconciliation. An effect that does **not** lives on the in-memory `Occupant`, is never seen by `Step`, and costs none of that.

The two are not interchangeable and the choice is not a matter of taste: put a movement-affecting value on the occupant and the client predicts a speed the office is not using; put a movement-irrelevant value on `Player` and a 193 kB golden artefact is regenerated, a cross-language port is edited, and a reconcile spread grows, for a number the simulation never reads.

_Reasoning._ This game's client runs the server's own `Step`, pinned to it by golden vectors ([ADR-052](./ADR-052-the-netcode-is-built-multiplayer-complete.md), adopted here by [ADR-057](./ADR-057-a-dom-game-may-own-a-fixed-step-simulation.md)). That makes `Player` a **contract with three signatories** — the Go, the TypeScript and the vectors — where `Occupant` is a private in-memory struct with none. So the question "where does this field go" is really "does the client have to simulate it", and there is a measurable answer.

**The cost of getting it wrong towards the occupant** is divergence. Claude Code's slow multiplies the walk: 6.4 m/s becomes 5.12. A client that did not know about it predicts the difference — 1.28 m/s — which clears `SILENT_CORRECTION` immediately and the snap threshold in about 1.6 s of continuous walking. The symptom is a figure yanked backwards ten times a second while a thumb is held down, and this game has already shipped that bug once, with the dash cooldown, at 5.5 m of divergence per dash. So `SlowLeft` is on `Player`, in the port, in the vectors, and in the reconcile spread — five coordinated edits in one commit, which is the price.

**The cost of getting it wrong towards `Player`** is a regenerated artefact and a lost proof. The same iteration added ten seconds of invincibility and a random persona, and neither touches movement at all: being uncatchable changes who the *boss* walks at, and a persona changes what a figure *says*. Had either gone on `Player`, `testdata/step_vectors.json` would have been regenerated for it — and a vector diff is this project's evidence that a change did not alter behaviour, which is exactly how the rename in this same batch was proved safe. Spending that proof on a value the simulation cannot read is spending it for nothing.

_The test that decides it_ is one question, and it is about `Step` rather than about the effect's importance: **does `Step` have to read this to produce the same position?** The slow does. The cloud, the persona, the redirect's cooldown and the announcement timer do not — they are all on the occupant, and the frame carries their *consequences* rather than their state.

_Consequence — the reconcile spread is the sharp edge._ A predicted timer only advances inside `apply`, and `apply` only runs when a command is emitted. A player standing perfectly still emits nothing, which in *this* game is the default state — so a locally-held timer never runs down at all and the client keeps predicting an effect the office has long since expired. Every predicted duration therefore has to be **taken from the snapshot on every reconcile**, not merely initialised: the dash cooldown was the first, `SlowLeft` is the second, and the field is deliberately **required** on the `Authoritative` type rather than optional, so TypeScript names every call site that forgot it. It did, on the day this landed.

_What this does not say._ It is not an argument for predicting more. The default remains that the client predicts its own player and nothing else ([ADR-052](./ADR-052-the-netcode-is-built-multiplayer-complete.md)): the лысый, Claude Code and every colleague arrive on the wire and are interpolated, because their intent is not ours to guess. This record is about where a *player's own* state lives once it exists.
