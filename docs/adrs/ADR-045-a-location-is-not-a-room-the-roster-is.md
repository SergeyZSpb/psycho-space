# ADR-045 · A location is not a room — the roster is filtered, not split

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** A location is not a room — the roster is filtered, not split
- **status:** Accepted · 2026-07-27
- **summary:** one paragraph in [ARCHITECTURE.md §8.4](../ARCHITECTURE.md#adr-045--a-location-is-not-a-room--the-roster-is-filtered-not-split) — this file is the detail behind it.
- **related:** [ADR-028](./ADR-028-games-are-self-contained-modules.md) · [ADR-033](./ADR-033-a-game-reads-the-socket-through-a-game.md) · [ADR-037](./ADR-037-one-account-is-one-entity-and-the-wire.md) · [ADR-039](./ADR-039-game-content-is-a-go-catalogue-and-the-schema.md)
- **code:** `internal/gamevanyagotchi/content.go` — `Location`, `LocationYard`, `NPC.Location`, `ObjectKind.Location` · `internal/gamevanyagotchi/message.go` — `Peer.Loc`, `Roster.Here`, `onWire` · `internal/gamevanyagotchi/service.go` — `place`, `cast`, `handleGoto` · `internal/gamevanyagotchi/world.go` — `loadWorld`, `worldLimitPerLocation`, `settleHunt`

---

«Ванягоччи» has five places — двор, лес, лифт, кусты, заброшка — and they are **not** realtime rooms. There is one room for the whole game (the existing `yard`), every entity in every location rides the same 5 Hz frame, and each carries a `loc` key that the **client** filters on to decide what to draw. A location is a field inside the game's own messages and a key in its own catalogue; `internal/realtime` never learns that the concept exists.

_Reasoning:_ the tempting design is one room per location, and it is wrong in two independent ways.

**It would make adding a location edit a platform file.** `internal/httpapi/realtime.go` keeps a **closed set** of room names, for the good reason that an open-ended room name lets a client create unbounded rooms. So a game teaching it a new name is exactly the boundary violation the naming convention exists to catch ([ADR-030](./ADR-030-game-modules-are-named-gamename.md)): `git grep -il gamevanyagotchi` would start returning an unprefixed platform file, and the grep would be telling us the boundary was already broken. Under the filtered design, adding a location costs **nothing at the transport layer, ever** — it is a catalogue entry, which is the property [ADR-039](./ADR-039-game-content-is-a-go-catalogue-and-the-schema.md) exists to protect.

**And it would empty the world.** This game is played by five to thirty friends. Splitting presence across five rooms makes each one contain nobody, which is precisely the failure the populated-world design was built to prevent — the same reason absent players are drawn asleep where they stood rather than removed ([ADR-042](./ADR-042-everything-that-moves-is-a-function-of.md)). One room keeps the head count honest across the whole world, and lets a player see where everybody else is before deciding where to walk.

_What it costs, stated rather than waved away._ A client receives frames about places it is not standing in. That is a real price and it is paid in three places, each bounded deliberately:

- **Per-entity.** `Peer.Loc` is `omitempty` and **omitted for the default location**, so the common case — everybody in the yard — adds nothing at all. A peer elsewhere costs about twelve bytes.
- **Per-frame.** `worldLimit` became `worldLimitPerLocation` and fell from 24 to 6. A single world-wide budget would let a busy yard starve every other place, up to and including whichever one is hiding the key; five locations at six objects is about 1.8 KB a frame, against 1.4 KB before. Keeping 24 per location would have been 7.2 KB.
- **The head count is a map**, `{"yard":3,"les":1}`, with zeroes omitted. It has to be sent rather than derived for the reason [ADR-039](./ADR-039-game-content-is-a-go-catalogue-and-the-schema.md) gives: deriving it client-side would mean teaching the browser to tell a person from an NPC from a deposit, which is the one thing the entity frame is shaped to prevent.

_Consequence — everything positional gains a location, and forgetting one is a real bug rather than a cosmetic one._ Coordinates are normalised **per location**, so the same `(0.82, 0.22)` is the beer crate in двор and an empty patch of лес. Every question about nearness therefore has to ask *where* first: `beside` refuses across locations, or a Ваня in лес could drink from a shop four places away; `searched` compares the key's own `location_key`, or a key hidden in лес would be nearest двор's куст and claimable from a bench; `settleHunt` filters the sad faces to the location the key was found in, or somebody in лифт is drained to grayscale for something in заброшка with no local explanation. Each of those is one comparison, and each was a live bug the moment the fifth place existed.

_Consequence — the singleton stayed world-wide, and that needed no schema._ There is one key in the **world**, not one per location: the partial unique index is on `kind` alone, so `ActiveSingleton` lost its location parameter rather than gaining a loop. A per-location ask is a question that index does not answer. The crate is pinned to двор by a catalogue field; the key names no location and is re-hidden somewhere new by `crypto/rand` each time it is won. The player is told nothing about which place holds it — searching everywhere is the game.

_Considered and rejected: filtering server-side, per recipient._ It would halve the bytes and it is refused for the same reason `Roster.Store` is one shared block rather than a field on every peer, and the reason `Peer` carries no "you" flag: **the frame is fanned out to the whole room**, so anything that differed per player would have to be rendered per player, once per recipient per tick, and the broadcast would stop being one marshal. The escape hatch if it is ever needed is the one already written down — interest management at around a hundred concurrent movers — and this design reaches that ceiling no sooner than the single-location one did.
