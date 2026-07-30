// Package gamefintech is «СИМУЛЯТОР ФИНТЕХА» — the fourth game, and the first one
// whose whole point is standing still.
//
// You are Карен. The job is not to work and to be paid for it: the salary
// accrues only while you are standing perfectly still, and the longer you have
// stood there the faster it accrues, up to three times the base rate. Walking
// stops it and, after a short grace window, resets the ramp you were protecting.
// Meanwhile a smiling bald man crosses the office towards you, and the moment he
// reaches you the shift is over — he is very glad to see you, and that is the
// worst thing that can happen to anybody.
//
// The dash is the whole skill ceiling: it is fast, it is short, it earns nothing
// — and it does NOT reset the ramp. Moving out of his way costs you the seconds
// you spend doing it and nothing else, so the game is about how late you are
// willing to leave it.
//
// # It shares nothing with any other game
//
// Per ADR-028 and ADR-030 this package imports no other game and no other game
// imports it, even where the code would be identical. It borrows patterns from
// «ВАНЯДУМ» — the pure Step, the injected tick, the one persistence goroutine,
// the narrow transport seam — by RE-IMPLEMENTING them. Deleting this game means
// deleting this package, its migration, its routes and its views, and nothing
// else.
//
// # No LLM on any path, ever
//
// ADR-016 forbids a realtime message reaching a paid model, and every line the
// bald man says is a catalogue string in content.go. Nothing in this package may
// import internal/gamekhimki or grow an HTTP client.
//
// # Why this package has a loop
//
// The rest of this project computes time-varying state on read and never ticks
// it (ADR-038), and «Ванягоччи» can do that because everything it draws is
// closed form: a position is pattern(params, now−epoch). PURSUIT destroys closed
// form. Where the bald man is at t depends on where every player was at every
// instant before t, so it has to be integrated step by step — which is why this
// package runs a fixed-step simulation at SimHz, exactly as «ВАНЯДУМ» does for
// collision.
//
// That does not reopen ADR-038, because ADR-038 is about DURABLE state. This
// loop touches memory only:
//
//   - The office lives in one struct in this package and nowhere else.
//   - Postgres is written exactly ONCE per shift — when it ends — and never on a
//     tick. Nothing is read from Postgres to start one.
//   - The office is deliberately ephemeral. A restart loses shifts in flight, in
//     the same way and for the same reason the hub loses presence.
//   - The ticker is injected (ADR-034), so every test drives it by hand and no
//     test sleeps.
//
// # One office, not one arena per run
//
// «ВАНЯДУМ» builds an arena per run because a run is a freshly generated
// заброшка. This game has ONE office that is always the same office (the layout
// is a catalogue constant, not a generator), so there is one process-wide Office
// with a slot per account, torn down when the last person leaves. Iteration 1
// ships solo gameplay on multiplayer plumbing on purpose: the alternative is a
// rewrite disguised as a later iteration.
//
// # Authority
//
// The client sends intent and never a fact: a direction, and a request to dash.
// Never a position, never a salary, never a claim to have dodged anything. The
// server owns the simulation and publishes idempotent full-state snapshots, so a
// dropped frame costs nothing and the next one is the truth again.
//
// Step is a pure function of (desks, player, command) — no clock, no RNG, no map
// iteration — which is what lets it be ported to TypeScript for client-side
// prediction and pinned by the golden vectors in testdata/. Do not give it
// anything ambient.
package gamefintech

import "time"

// Room is the realtime room this game listens in.
//
// It lives here rather than in internal/httpapi because a room name is a game's
// property, not the platform's: the upgrade handler holds a map of registered
// rooms and learns this string from the composition root, so nothing in an
// unprefixed package ever spells out the name of a game (ADR-033).
const Room = "fintech"

const (
	// SimHz is the simulation rate: twenty fixed steps a second. Everything
	// about movement and the money ramp is defined against this number, so
	// changing it changes how the game PLAYS and not merely how often it is
	// drawn.
	SimHz = 20

	// SimStep is one simulation step. Derived rather than typed out, so the two
	// can never disagree.
	SimStep = time.Second / SimHz

	// SnapshotEvery is how many simulation steps pass between snapshots: every
	// tick, so the wire runs at the simulation's own rate.
	//
	// IT WAS TWO, AND THAT WAS THE LARGEST SINGLE TERM IN THE LATENCY LEDGER.
	// The лысый cannot be predicted — his intent is not the client's to guess —
	// so he is drawn from an interpolation buffer, in the recent past, and the
	// delay that buffer needs is a multiple of THIS period (see DELAY_PERIODS in
	// web/src/lib/fintechInterp.ts). At 10 Hz that was 150 ms of staleness, or
	// 0.6 m at his walking speed, on top of the downlink — against a catch radius
	// of 1.2 m. Which is how a shift ended while he was still drawn a couple of
	// metres away: the client was dodging a man who was no longer there.
	//
	// Halving the period halves that, and costs 140 bytes a tick per viewer —
	// 2.8 kB/s, about 22 kbit/s, on a floor that holds three people. The client
	// needs no change at all: it derives the delay from the served `snapshot_hz`
	// rather than hardcoding a period, so publishing faster tightens the buffer
	// by itself.
	//
	// The reason it was two is still true and is simply outweighed: this office
	// is shared, so the cost is per viewer per tick where «ВАНЯДУМ» pays it once.
	// Three viewers of a 336-byte worst case is 20 kB/s for the whole game.
	SnapshotEvery = 1

	// RenderDelayPeriods is how far into the past the client draws everything it
	// does NOT predict — the лысый, Claude, the colleagues — as a multiple of the
	// snapshot period.
	//
	// IT LIVES ON THE SERVER BECAUSE THE SERVER HAS TO KNOW IT. The client needs
	// the number to size its interpolation buffer, and the office needs the same
	// number to resolve a catch against the world the victim was actually looking
	// at. Two copies of one constant is two things to keep in agreement, so it is
	// published in the catalogue as `render_delay_ms` and the browser obeys it.
	//
	// One period would be exactly enough if frames were perfectly spaced, which is
	// the one thing they are not; the extra half absorbs ordinary jitter and a
	// single dropped frame.
	RenderDelayPeriods = 1.5

	// RenderDelaySeconds is that delay as a duration. Derived rather than typed
	// out, so changing the publish rate moves both ends of the compensation.
	RenderDelaySeconds = RenderDelayPeriods / (SimHz / SnapshotEvery)

	// CatchRewindMax bounds how far the office will rewind to resolve a catch, in
	// seconds.
	//
	// THE REWIND IS DERIVED FROM A NUMBER THE CLIENT CONTROLS — the last snapshot
	// tick it says it drew — so it needs a ceiling, or a client claiming an absurd
	// latency would be permanently a metre further from the лысый than it really
	// is. At his walking speed this is 1.2 m, which is one catch radius: the worst
	// a dishonest client buys is being caught a third of a second later, and he is
	// still walking at them the whole time.
	//
	// It is also comfortably above the honest case. A render delay of 75 ms plus a
	// bad mobile round trip and a full input window is around 250 ms.
	CatchRewindMax = 0.3

	// InputHz is how often the client sends. It is published in the catalogue
	// rather than chosen by the client, because it is half of the ratio below.
	InputHz = 10

	// MaxCommandsPerFrame bounds the FRESH sub-steps one input frame may carry.
	//
	// The client samples at SimHz and sends at InputHz, so an honest frame
	// carries SimHz/InputHz = 2 sub-steps. The cap is four rather than two: a
	// client whose timer drifted, or whose page was briefly throttled, produces
	// three or four honestly, and refusing them would make an ordinary phone
	// stutter. What actually bounds how much SIMULATION a frame can buy is the
	// per-occupant time budget (TimeBudgetCap), not this number — this one only
	// stops a frame being unboundedly large.
	MaxCommandsPerFrame = 4

	// RedundantCommands is how many already-sent commands a client may repeat in
	// a frame so that one lost packet costs no input at all.
	//
	// The pending list prediction already keeps exists for reconciliation, so
	// resending its tail is a loop and a few bytes. The office drops any command
	// whose sequence it has already applied, which is what makes the redundancy
	// free rather than a way to buy extra simulation.
	RedundantCommands = 6

	// MaxStepSeconds bounds one command's dt. A client that claims a huge dt is
	// asking to teleport; a client whose tab was backgrounded produces the same
	// claim honestly. Both are answered the same way — clamp, never trust.
	MaxStepSeconds = 0.2

	// TimeBudgetCap is how much CLIENT-CLAIMED simulated time one occupant may
	// spend in a single tick.
	//
	// THIS IS THE SPEED-HACK GUARD. An occupant accrues budget at exactly real
	// time and spends it on the commands they send, so a client that filled
	// every frame with the largest legal dt cannot run faster than everybody
	// else — it merely queues.
	//
	// In steady operation the budget cannot bank across ticks, because any part
	// of a tick no command claimed is simulated as standing still (see
	// Office.Advance) and that consumes the remainder. The cap therefore binds
	// in exactly the case it is there for: a tick that delivers far more time
	// than a tick should — a stalled loop, a suspended process, a test driving
	// the office with a five-second step — where a client with a full queue
	// would otherwise buy seconds of movement in one step.
	TimeBudgetCap = 0.5

	// MaxOccupants is how many people may be in the office at once. Owner-
	// directed, and deliberately small: this is a joke about one open-plan floor
	// and a handful of friends, not a lobby.
	MaxOccupants = 3

	// AbandonGrace is how long an occupant survives with no connection before
	// the shift is ended for them.
	//
	// It is not a disconnect timeout: a page reload, a tunnel, a phone locking
	// and unlocking all take a few seconds, and ending the shift for any of them
	// would make the game unplayable on a bus. What it protects against is the
	// occupant nobody comes back to, who would otherwise stand in the office
	// earning money until the process restarted.
	//
	// Unlike «ВАНЯДУМ», an abandoned shift IS written (if it lasted long
	// enough). A забег somebody walked away from is not a result; a shift
	// somebody walked away from is exactly what this game is about.
	AbandonGrace = 90 * time.Second

	// MinShiftSeconds is the shortest shift worth a row. Below it the shift is
	// dropped rather than written: an accidental tap that starts and ends a
	// shift in a second is not a result, and a leaderboard full of them is
	// noise.
	MinShiftSeconds = 3.0
)
