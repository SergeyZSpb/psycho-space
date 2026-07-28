// Package gamevanyadum is «ВАНЯДУМ» — the third game, and the first in 3D.
//
// Ваня, the last real defender of traditional Russian rap values, walks a
// generated заброшка looking for the keys, drinks the beer he finds on the way,
// and eventually kills нейрослопы with punchlines. This iteration is the
// walking skeleton: a generated non-flat level, a server-owned simulation, and
// one pickup.
//
// # It shares nothing with any other game
//
// Per ADR-028 and ADR-030 this package imports no other game and no other game
// imports it, even where the code would be identical. It borrows patterns from
// «Ванягоччи» — the Go catalogue, the injected tick, the narrow transport seam —
// by re-implementing them. Deleting this game means deleting this package, its
// migration, its routes and its views, and nothing else.
//
// # No LLM on any path, ever
//
// ADR-016 forbids a realtime message reaching the judge, and its reasoning — one
// player's action multiplying into unbounded paid calls — applies with far more
// force here than it did to the yard: this game would fire a line every time a
// trigger is pulled. The punchline generator is a Go catalogue and stays one.
// Nothing in this package may import internal/gamekhimki or grow an HTTP client.
//
// # The simulation, and why this package has a loop when nothing else does
//
// The rest of this project computes time-varying state on read and never ticks
// it (ADR-038), and «Ванягоччи» can do that because everything it draws is
// closed-form: a position is pattern(params, now−epoch), so a tick that is late,
// early, skipped or duplicated still produces the right world (ADR-042).
//
// Collision destroys closed form. Where a player is at t depends on every wall
// he slid along getting there, so it has to be integrated step by step, and this
// package therefore runs a fixed-step simulation at SimHz.
//
// That does not reopen ADR-038, because ADR-038 is about DURABLE state. This
// loop touches memory only:
//
//   - An arena lives in a map in this package and nowhere else.
//   - Postgres is read and written exactly twice per run — once when it starts,
//     once when it ends — and never on a tick.
//   - An arena is deliberately ephemeral. A restart loses runs in flight, in the
//     same way and for the same reason the hub loses presence.
//   - The ticker is injected (ADR-034), so every test drives it by hand and no
//     test sleeps.
//
// # Authority
//
// The client sends intent and never a fact: a direction to walk and where it is
// looking, never a position, a health value or a claim to have hit something.
// The server owns the whole simulation and publishes idempotent full-state
// snapshots, so a dropped frame costs nothing and the next one is the truth
// again.
//
// Step is a pure function of (arena state, command) with all randomness drawn
// from a seeded PRNG held on the arena. That is what makes the simulation
// table-testable, and it is what a client-side prediction port would have to
// reproduce exactly if the netcode ever climbs to that rung — see the design
// doc's netcode ladder before making Step depend on anything ambient.
package gamevanyadum

import "time"

// Room is the realtime room this game listens in.
//
// It lives here rather than in internal/httpapi because a room name is a game's
// property, not the platform's: the upgrade handler holds a map of registered
// rooms and learns this string from the composition root, so nothing in the
// unprefixed packages ever spells out the name of a game.
//
// ONE ROOM FOR THE WHOLE GAME, even though every player has his own arena. A
// room per run would make the platform learn what a run is, and gains nothing:
// snapshots go out through PublishTo, addressed to a connection.
const Room = "vanyadum"

// SimHz is the simulation rate: twenty fixed steps a second. Everything about
// movement is defined against this number, so changing it changes how the game
// plays and not merely how often it is drawn.
const SimHz = 20

// SimStep is one simulation step. Derived rather than typed out, so the two can
// never disagree.
const SimStep = time.Second / SimHz

// SnapshotInterval is how often a player is sent the world. It is deliberately
// equal to SimStep in this iteration: a snapshot is cheap, and sending one per
// step removes an entire class of question about which tick a snapshot
// describes. If the measured bandwidth (design doc §5) says otherwise, this is
// the constant to change — not the simulation rate.
const SnapshotInterval = SimStep

// MaxCommandsPerFrame bounds an input frame. The socket already allows only ten
// frames a second (internal/realtime/conn.go), and the client samples input at
// four times that, so four sub-steps per frame is exactly the ratio — anything
// beyond it is a client trying to buy extra simulation time.
//
// This is the design's answer to the socket's rate limit: fit inside a security
// property rather than loosen it.
const MaxCommandsPerFrame = 4

// RedundantCommands is how many already-sent commands a client may repeat in a
// frame so that one lost packet costs no input at all.
//
// The pending list prediction already keeps exists for reconciliation, so
// resending its tail is a loop and a few bytes. The server drops any command
// whose sequence it has already applied, which is what makes the redundancy
// free rather than a way to buy extra simulation.
const RedundantCommands = 6

// InterpolationDelay is how far in the past a peer is drawn.
//
// It is a CONSTANT BOTH ENDS AGREE ON, published in the catalogue rather than
// reported by the client — a client-supplied render delay is a client-supplied
// advantage, because it is exactly the number lag compensation rewinds by.
//
// Two snapshots' worth plus a little: enough that an ordinary late frame still
// has a bracketing pair to interpolate between, small enough that a peer is not
// visibly behind where they are.
const InterpolationDelay = 120 * time.Millisecond

// HistoryWindow is how far back the server can rewind the world.
//
// It bounds lag compensation: a shot from a player whose round trip plus
// interpolation delay exceeds this is resolved against the oldest frame there
// is, rather than against a fabricated one. Generous enough for a bad mobile
// connection, short enough that the ring costs nothing.
const HistoryWindow = 1200 * time.Millisecond

// MaxStepSeconds bounds one command's dt. A client that claims a huge dt is
// asking to teleport; a client whose tab was backgrounded produces the same
// claim honestly. Both are answered the same way — clamp, never trust.
const MaxStepSeconds = 0.2
