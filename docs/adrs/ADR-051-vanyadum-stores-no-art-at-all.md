# ADR-051 · «ВАНЯДУМ» stores no art at all

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** «ВАНЯДУМ» stores no art at all — geometry, textures and props are generated from a seed
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-051--ванядум-stores-no-art-at-all) — this file is the detail behind it.
- **related:** [ADR-026](./ADR-026-game-art-lives-in-postgres-not-in-git-or-the.md) · [ADR-031](./ADR-031-game-asset-storage-is-shared-infrastructure.md) · [ADR-039](./ADR-039-game-content-is-a-go-catalogue-and-the-schema.md) · [ADR-047](./ADR-047-vanyadum-renders-in-webgl-and-only-the.md) · [ADR-050](./ADR-050-the-level-is-generated-on-the-server-and-sent.md)
- **code:** `web/src/lib/vanyadumTexture.ts` — `generateTexture`, a pure `(surface, size, seed) → Uint8Array` · `internal/gamevanyadum/content.go` — `Surfaces`, the five entries that are the whole palette · `web/src/render/vanyadumScene.ts` — the shotgun and the bottle, built from primitives
- **re-examine when:** a sprite, a model, a glTF loader or an upload is proposed for this game. Enemies arrive next iteration and are the first real test of it.

---

«Смолтолк в Химках» keeps its pictures as blobs in Postgres ([ADR-026](./ADR-026-game-art-lives-in-postgres-not-in-git-or-the.md)) behind a shared, game-agnostic store ([ADR-031](./ADR-031-game-asset-storage-is-shared-infrastructure.md)). «ВАНЯДУМ» uses **neither**, and that is a decision rather than an omission: this game has no authored art to store. Every visible thing is a function of a seed and a handful of catalogue numbers.

- **Geometry** is extruded from the level's sector graph ([ADR-050](./ADR-050-the-level-is-generated-on-the-server-and-sent.md)). No modeller, no glTF, no loader in the bundle.
- **Textures** are generated from five short catalogue entries — a base colour, an accent, a noise amount, a roughness and a pattern name — into concrete, brick and boards.
- **Props** — the двустволка and the beer bottle — are assembled from boxes and cylinders in about thirty lines each.
- **Lighting** is a per-sector level and four fixed face multipliers baked into vertex colours, drawn with an unlit material. There is no lighting pass at all, which is both cheaper and much closer to the game this is a joke about.

_The consequence on the wire, which is the point._ The entire appearance of a заброшка costs the same few kilobytes the level already costs, and **zero art bytes, forever**. Against the project's rule that bytes are a design constraint because the audience is on a phone on mobile data, this is the largest single saving available to a 3D game, and it is available only because nothing here was drawn by a person.

_The load-bearing detail: generated into a typed array, never drawn into a canvas._ A texture is a pure `(surface, size, seed) → Uint8Array` that the renderer wraps in a `DataTexture`. The obvious alternative — drawing with a 2D canvas context — produces the same pixels and is **untestable**: jsdom has no canvas, and once the pixels are on the GPU nothing can assert on them at all. As a typed array it is ordinary node-testable code, and `vanyadumTexture.spec.ts` pins that the same seed gives the same bytes, that every channel stays inside a byte, that alpha is always opaque, that a brick pattern actually contains mortar, and that an unknown pattern degrades to plain rather than throwing. This is the same move [ADR-047](./ADR-047-vanyadum-renders-in-webgl-and-only-the.md) makes with geometry, and for the same reason: a canvas can be given nothing that a test needs to look at.

_Reproducibility is a requirement, not a nicety._ The seed is explicit and the PRNG is `mulberry32` rather than `Math.random`, so a reload mid-run does not repaint the world in different concrete, and two phones looking at the same wall see the same wall. The texture seed is derived from the **surface key** rather than the level seed, deliberately: two rooms made of the same material should look like the same material.

_What this does not claim._ Procedural art is not free — it is a different kind of work, and it has a ceiling. A нейрослоп needs to read as a glitchy, over-smooth, six-fingered AI image, and that is a thing a generator can do convincingly *because* the target is deliberately bad art, which is the joke. If a future iteration wants something genuinely drawn, the shared asset store is right there and unprefixed precisely so that any game may use it — this record says this game does not need it, not that it may not have it. The first real test is the enemies, next iteration.
