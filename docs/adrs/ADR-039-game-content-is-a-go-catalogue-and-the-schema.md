# ADR-039 · Game content is a Go catalogue, and the schema stores only its keys

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Game content is a Go catalogue, and the schema stores only its keys
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.8](../ARCHITECTURE.md#adr-039--game-content-is-a-go-catalogue-and-the-schema) — this file is the detail behind it.

---

A game's content — the stats and their rates and bounds, the actions, the skins, the locations, the labels — lives in one Go file inside that game's package (`content.go`) and is served whole to the SPA by that game's `GET /config`. The database stores **keys as `text`**, never Postgres enums, and holds none of the meaning. The SPA hardcodes no key, no label and no threshold: it renders whatever the config describes.

_Reasoning:_ this is a decision about the cost of a whole class of change, and migrations here are **immutable**, so getting it wrong is permanent. With enums, every new stat, skin, location or object kind is an `ALTER TYPE` — a migration, forever, for a value whose entire meaning is a label and a number. With a column per stat, every new stat is an `ALTER TABLE`. With the catalogue plus `text` keys, adding one is a Go-file edit: no migration, no client deploy, and the value's rate, bounds, label and rendering are all defined in the single place that can validate them against content anyway. A row whose key has left the catalogue is unrenderable and is skipped on read, which is the correct failure for a value only content can define.

_The homogeneous half gets rows; the heterogeneous half gets columns._ A pet's stats are all the same shape — a scalar with a rate and an `as_of` — so they are rows in a tall table, one decay expression covers every one of them, and adding a stat needs no schema at all. A **stat whose rate is zero is a lifetime counter**, which is how this game gets its records without a second runs table. World objects are the opposite: heterogeneous rows carrying contended invariants (`claimed_by`, `remaining`, `exhausted_at`) that must be indexable, `NOT NULL`-able and `CHECK`-able, because a typo silently reading as NULL is the one bug class a contested claim can least afford. Choosing differently in the two tables is the decision, not an inconsistency.

_And there is no JSONB, deliberately._ Both candidate uses were cosmetic and derive better from `hash(id)` against the catalogue — zero storage, and unable to drift out of step with the content. A JSONB column added now would ship unused, and an unused escape hatch is where load-bearing state goes to hide from constraints. _The trigger to revisit is named:_ the first kind that needs a persisted, kind-specific, non-derivable value earns either that column or a narrow side table **then**, decided with the concrete case in hand.

_Consequence:_ the property is testable rather than aspirational, and is tested — the stubbed Playwright suite serves a config containing a stat and an action the SPA has never heard of and asserts both render, labelled from the config. A client that had learned a content key fails that test. The same rule is why an invariant that must live in the database cannot name a content value in DDL: the "at most one active event of a kind" index is predicated on a `singleton` boolean the catalogue sets at insert time, not on `kind IN ('key', 'beer_crate')`, which would have put content into an immutable migration.
