// Package gamevanyadum is «ВАНЯДУМ» — the third game, and the first in 3D.
//
// Ваня, the last real defender of traditional Russian rap values, walks a
// generated заброшка with a double-barrelled обрез, loads it with the beer he
// finds on the way, and shoots the нейрослопы that walk out of it at him. The
// punchlines are still to come; the shotgun arrived first.
//
// # One building, always running, and nothing that ends
//
// There is ONE заброшка for the whole process and everybody is in it. No
// matchmaking, no rooms, no runs and no objective: you open a socket, you are in
// the building, the pickups come back, the нейрослопы come back, and you leave
// when your connections stop coming back. The world is generated on demand and torn down when the last
// person walks out, so the next arrival gets a fresh seed and nothing ever
// regenerates under anybody's feet.
//
// It was one arena per run, which is the half of ADR-048 this iteration reverses
// — deliberately, and onto the shape the sibling office arrived at in ADR-056. A
// заброшка nobody else can walk into is a single-player game carrying
// multiplayer netcode it never uses.
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
//   - The world lives behind one pointer in this package and nowhere else.
//   - Postgres is written exactly ONCE PER VISIT — one summary row when somebody
//     stops coming back — and never on a tick.
//   - The world is deliberately ephemeral. A restart loses the building and
//     everybody in it, in the same way and for the same reason the hub loses
//     presence.
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
// Step is a pure function of (level, player, command) and draws no randomness at
// all. That is what makes the simulation table-testable, and it is what the
// client-side prediction port reproduces exactly — see the golden vectors before
// making Step depend on anything ambient.
package gamevanyadum

import "time"

// Room is the realtime room this game listens in.
//
// It lives here rather than in internal/httpapi because a room name is a game's
// property, not the platform's: the upgrade handler holds a map of registered
// rooms and learns this string from the composition root, so nothing in the
// unprefixed packages ever spells out the name of a game.
//
// ONE ROOM FOR THE WHOLE GAME, and now one world behind it — so membership of
// the room IS being in the building, and the hello that announces a connection
// is the only join there is. Snapshots still go out through PublishTo rather
// than as a broadcast, because a frame names everybody except its own reader.
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

// StandingsInterval is how often the standings frame goes out.
//
// A SECOND RATHER THAN A TICK, because it is a readout and not a picture. The
// numbers on it move a few times a minute — how long you have been in the
// building advances once a second by construction, and what you are carrying
// changes when you walk over a bottle — and nobody reads a score twenty times a
// second. Putting the same numbers on the snapshot instead would cost twenty
// times as much to say the same thing; the arithmetic is written out on the
// Standings type.
//
// It is not the ONLY thing that publishes one. The roster changing publishes one
// on the tick it changed, and a connection that has never been sent one gets it
// ahead of its first snapshot, so that no client is ever shown a slot it has not
// been given a name for. See World.RosterVersion and Service.boarded.
const StandingsInterval = time.Second

// standingsEvery is that interval in simulation ticks, which is the unit the
// world counts in. Derived rather than typed out, so the two can never disagree.
const standingsEvery = int64(StandingsInterval / SimStep)

// visibleHold is how long a peer stays on a reader's frame after leaving the set
// that reader could plausibly see.
//
// IT EXISTS BECAUSE A SECTOR IS DERIVED FROM A POSITION, so a man standing in a
// doorway between two rooms belongs to whichever of them the last sub-centimetre
// of movement put him in — and he crosses back and forth at up to the tick rate
// without walking anywhere. Interest management sends the reader's own room and
// the rooms through its doorways (level.go, buildVisibility), so that flip moves
// the whole visible set with it: a third room adjacent to one of the two and not
// to the other joins and leaves the frame twenty times a second, and the people
// in it strobe. It happens at BOTH ends — a peer jittering on a boundary drops
// out of a set he never really left, exactly as a reader jittering does.
//
// The fix is a memory rather than a smoothed position: a peer is sent while he
// is visible OR was visible within the last visibleHold, so a set that keeps
// coming back never goes away. Two hundred milliseconds is the shortest span
// that comfortably outlasts jitter at the tick rate — four ticks, so a flip has
// to stay flipped for four consecutive ticks to remove anybody — and it is short
// enough that somebody who genuinely walked out of sight is not left standing in
// a doorway he left. A hold can only ADD to a filtered set, so it cannot exceed
// the unfiltered one the wire budget is taken on (message_test.go).
const visibleHold = 200 * time.Millisecond

// visibleHoldTicks is that hold in simulation ticks, which is the unit the world
// counts in. Derived rather than typed out, so the two can never disagree.
const visibleHoldTicks = int64(visibleHold / SimStep)

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
// whose sequence it has already ACCEPTED — the ones still waiting in its queue
// as well as the ones already stepped (Occupant.highSeq) — and that is what
// makes the redundancy free rather than a way to buy extra simulation.
// Deduplicating against what has been APPLIED instead would let through
// precisely the repeats of what is queued, which is most of the window.
const RedundantCommands = 6

// InterpolationDelayPeriods is how far in the past a peer is drawn, as a
// MULTIPLE OF THE SNAPSHOT PERIOD rather than as a duration.
//
// A multiple, because the number has to survive a change to the publish rate,
// and SnapshotInterval's own comment above invites exactly that change. A delay
// typed out in milliseconds does not follow it: halve the publish rate to 10 Hz
// and a fixed 120 ms is suddenly 1.2 periods, under one period plus jitter, so
// the client's interpolation buffer runs dry and every peer visibly stutters
// between frames. Expressed as a ratio it moves with the rate, on both ends at
// once — the server rewinds by exactly this and the client draws by exactly
// this.
//
// Two snapshots' worth plus a little: enough that an ordinary late frame still
// has a bracketing pair to interpolate between, small enough that a peer is not
// visibly behind where they are.
const InterpolationDelayPeriods = 2.4

// InterpolationDelay is that delay as a duration.
//
// It is a CONSTANT BOTH ENDS AGREE ON, published in the catalogue rather than
// reported by the client — a client-supplied render delay is a client-supplied
// advantage, because it is exactly the number lag compensation rewinds by.
//
// The scaling goes through thousandths because Go will not convert a fractional
// constant to a time.Duration: `SnapshotInterval * 2.4` does not compile at all,
// and routing the ratio through a float64 variable would make the result depend
// on which way a rounding error fell.
//
// WHAT MAKES THIS FORM EXACT IS NOT INTEGER ARITHMETIC. 2.4 is an untyped FLOAT
// constant and 2.4 has no finite binary expansion — the exactness comes from the
// compiler evaluating untyped constants as rationals, so it holds 2.4 as 12/5
// and 12/5 × 1000 is precisely 2400 before anything becomes a Duration. The
// conversion then demands a whole number, which is the property actually worth
// having here: a ratio that does not scale cleanly by a thousand FAILS TO
// COMPILE rather than quietly rounding. Three decimals of a period are available
// to whoever retunes the ratio, and a fourth is a build error.
const InterpolationDelay = SnapshotInterval * (InterpolationDelayPeriods * 1000) / 1000

// RewindMax bounds how far back lag compensation will rewind, AND IT IS STATED
// IN METRES, because metres are the only unit in which the question it answers
// means anything: what does a client that lies about its latency actually buy?
//
// 0.5 s × WalkSpeed 5.0 m/s = 2.5 m, or about three and a half player diameters
// (PlayerRadius 0.35). So the worst a liar gets is to be resolved against where
// you stood three and a half body-widths ago — irritating, and not enough to
// kill somebody who has already broken line of sight. It replaced a bound of
// 1.2 s, which was 6 m and quite enough to shoot a man around a corner.
//
// IT CANNOT BE MUCH SMALLER, because it also has to clear the HONEST case or it
// clips the players it was never aimed at — AND THE HONEST CASE IS NOT A NETWORK
// ROUND TRIP. What RewindTo composes and clamps is World.Enqueue's sample plus
// InterpolationDelay, and that sample is derived from the last snapshot tick the
// client had received, so it carries three things and only one of them is the
// network:
//
//   - InterpolationDelay, 120 ms, paid by every client on every connection by
//     construction.
//   - The client's send window. Input is batched onto a timer at InputHz, so a
//     keypress waits up to 100 ms for the next frame to leave.
//   - The network round trip itself.
//
// 500 − 120 − 100 therefore leaves 280 ms of genuine round trip inside the
// ceiling, which clears RU mobile data with room to spare. At 400 ms the same
// arithmetic left 180 ms, which does not: an ordinary phone would have been
// silently under-compensated, and being under-compensated is the exact complaint
// lag compensation exists to remove. ADR-059 does this arithmetic for
// «СИМУЛЯТОР ФИНТЕХА» and counts the input window there too; the comment this
// one replaced dropped it, and lost 100 ms of the budget by dropping it.
//
// Quantisation is the one term that can only help: the sample counts WHOLE
// ticks, so it rounds the loop down by up to one SimStep and never up. A lost
// snapshot does push it up — but honestly, because a client whose newest frame
// went missing really is drawing from an older one.
//
// RE-DERIVE IT WHEN THE НЕЙРОСЛОП ARRIVES. The 2.5 m above is WalkSpeed's, and
// walking is the fastest thing in the level today. The first enemy that moves
// faster than a player makes that figure wrong, and this is the constant where
// it has to be noticed.
const RewindMax = 500 * time.Millisecond

// HistoryWindow is the ring buffer's CAPACITY, and nothing else.
//
// It used to bound the rewind as well, which is two jobs for one number and the
// wrong answer for both — a ring sized generously for a bad mobile connection
// became a licence to rewind by all of it. RewindMax bounds the rewind now, so
// this only has to be a little longer than RewindMax: no rewind can ever ask for
// more.
//
// A LITTLE LONGER IS EXACTLY ONE STEP, and the arithmetic is spelled out because
// it is a fencepost in both directions. The ring holds HistoryWindow/SimStep
// frames, and the span between its oldest and its newest is one step SHORTER
// than that — so the past actually reachable is HistoryWindow − SimStep, and
// adding two steps here buys ONE step of margin past RewindMax rather than two.
// That one step is what keeps the deepest legal rewind BRACKETED by two recorded
// frames rather than clamped to the oldest one. Today: 600 ms of capacity, 12
// frames, 550 ms of reachable past, a 500 ms ceiling.
// TestTheRingIsLongEnoughToServeTheDeepestLegalRewind fails if a retune ever
// closes that gap.
const HistoryWindow = RewindMax + 2*SimStep

// MaxStepSeconds bounds one command's dt. A client that claims a huge dt is
// asking to teleport; a client whose tab was backgrounded produces the same
// claim honestly. Both are answered the same way — clamp, never trust.
const MaxStepSeconds = 0.2
