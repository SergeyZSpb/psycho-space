package gamevanyadum

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// epoch is the clock every test in this file starts from. The world takes its
// time from the tick, so a fixed instant is enough for everything except the
// abandon grace, which advances it deliberately.
var epoch = time.Unix(0, 0)

// newTestWorld generates a заброшка with one occupant in it and returns both,
// which is what almost every test here needs.
func newTestWorld(t *testing.T, seed int64) (*World, string) {
	t.Helper()
	w := NewWorld(uuid.New(), seed)
	noSlops(w)
	acc := uuid.New().String()
	if _, ok := w.Join(acc, "pseudo-"+acc[:8], epoch); !ok {
		t.Fatal("a fresh заброшка refused its first occupant")
	}
	settled(w, acc)
	return w, acc
}

// noSlops pushes the spawner's deadline out of reach, which is the state almost
// every test in this file wants: a building holding nobody but the people the
// test put in it.
//
// IT IS THE SAME KIND OF THING settled IS. A нейрослоп walks at the nearest man
// and takes health off him when it arrives, so a fixture that let one appear
// would be asserting the слоп's rules rather than the one it is about — and a
// test that passes or fails depending on how far a creature happened to walk is
// a test nobody can read. The слопы have their own file, where the deadline is
// put back deliberately (slop_test.go, slopsFrom).
//
// A DEADLINE RATHER THAN A FLAG, because there is no flag: nothing in production
// can turn the spawner off, and a switch that existed only for tests would be
// exactly the test-only machinery in a production path this project forbids. The
// tick counter is an int64 and a world lives in memory, so a deadline at the end
// of the range is one no заброшка will ever reach.
func noSlops(w *World) { w.slopSpawnAt = math.MaxInt64 }

// settled ends somebody's walk-in protection, which is the state almost every
// test below wants him in: a man who has been in the building a while.
//
// WITHOUT IT EVERY FIXTURE OPENS WITH TWO SECONDS OF NOBODY BEING ABLE TO SHOOT
// ANYBODY. Join grants the same window a respawn does (world.go, protect), and
// while it is running a trigger is refused and a target is not a target — so a
// duel written to start immediately would be asserting the protection rule
// rather than the one it is about. The grant has its own tests
// (TestAManWhoHasJustWalkedInIsProtectedTooAndCannotShootEither), which is where
// it belongs.
//
// BOTH HALVES OF IT, because the world's deadline is the authority and the
// countdown is only a prediction of it: clearing the seconds alone would leave
// him untouchable by the rule targetsFor actually reads.
func settled(w *World, accs ...string) {
	for _, acc := range accs {
		o := w.Occupant(acc)
		o.State.ProtectedLeft, o.protectedUntil = 0, 0
	}
}

// subStep is the dt a real command carries. The client emits
// MaxCommandsPerFrame sub-steps per frame at InputHz, so its demand is exactly
// what the time budget accrues — which is why a queue that falls behind never
// catches up on its own (web/src/lib/vanyadumInput.ts).
const subStep = 1.0 / (InputHz * MaxCommandsPerFrame)

// eastward is the yaw that walks along +X. Yaw zero looks along +Y, and a test
// that wants a straight line wants one axis so that the distance it asserts is
// the distance the simulation computed rather than a rotation of it.
const eastward = math.Pi / 2

// walkOnTheWire sends `frames` frames of `perFrame` copies of proto and then
// drains whatever is left, shaped exactly as the browser shapes a frame: the
// sub-steps just sampled, preceded by the tail of everything this client has not
// seen acknowledged, capped at the published redundancy window (buildInputFrame
// in web/src/lib/vanyadumInput.ts).
//
// ONE TICK BETWEEN FRAMES, rather than the two a 10 Hz client averages against a
// 20 Hz simulation, because a frame landing while the queue still holds
// something is the state the redundancy rule is actually judged in. It is not a
// contrived one: the client's demand equals the budget's accrual exactly, so a
// single late tick or early frame leaves the queue behind and nothing but the
// player standing still ever brings it back.
func walkOnTheWire(t *testing.T, w *World, acc string, frames, perFrame int, proto Command) {
	t.Helper()
	var unacked []Command
	seq := int64(0)
	for f := 0; f < frames; f++ {
		fresh := make([]Command, 0, perFrame)
		for i := 0; i < perFrame; i++ {
			seq++
			c := proto
			c.Seq = seq
			fresh = append(fresh, c)
		}
		tail := unacked
		if len(tail) > RedundantCommands {
			tail = tail[len(tail)-RedundantCommands:]
		}
		w.Enqueue(acc, &ParsedInput{Cmds: append(append([]Command{}, tail...), fresh...)})
		unacked = append(unacked, fresh...)

		w.Advance(SimStep.Seconds(), epoch)

		ack := w.Occupant(acc).State.LastSeq
		kept := unacked[:0]
		for _, c := range unacked {
			if c.Seq > ack {
				kept = append(kept, c)
			}
		}
		unacked = kept
	}

	// Every queued command is affordable within a bounded number of ticks
	// (MaxStepSeconds is below TimeBudgetCap, pinned below), so this bound is
	// arithmetic on a finite queue rather than a wait on anything that could be
	// slow — a stuck queue fails the assertion instead of hanging the suite.
	for i := 0; i < frames*perFrame*2 && len(w.Occupant(acc).pending) > 0; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	if n := len(w.Occupant(acc).pending); n != 0 {
		t.Fatalf("%d commands never drained", n)
	}
}

func TestEightSubStepsWalkExactlyEightSubSteps(t *testing.T) {
	// Eight sub-steps of walking asked for, eight walked. The overshoot this
	// rules out is not rounding: a command the world had queued but not yet
	// simulated used to be accepted a second time when the client repeated it,
	// so the redundancy window bought movement rather than insurance, and the
	// player was dragged forward while walking.
	w, acc := newTestWorld(t, 11)
	start := w.Occupant(acc).State.Pos

	// Straight along +X from the middle of the spawn room. Every room is at
	// least RoomMin across, so a metre of walking cannot reach a wall to be slid
	// along, and the generator never puts a pickup in the spawn room, so nothing
	// here can pick anything up underneath the assertion.
	const frames = 2
	walkOnTheWire(t, w, acc, frames, MaxCommandsPerFrame, Command{Dt: subStep, MY: 1, Yaw: eastward})

	want := frames * MaxCommandsPerFrame * subStep * WalkSpeed
	got := math.Hypot(w.Occupant(acc).State.Pos.X-start.X, w.Occupant(acc).State.Pos.Y-start.Y)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("walked %.6f m where %.6f m was asked for", got, want)
	}
}

func TestARedundantCopyOfAQueuedCommandIsDroppedToo(t *testing.T) {
	// The half of the redundancy rule that used to be missing. A command the
	// world has ACCEPTED but not yet simulated is still unacknowledged, so the
	// client is right to repeat it — and the world must still drop the copy,
	// because deduplicating against what has been APPLIED lets precisely those
	// through, and a frame carries twice what a tick can afford.
	w, acc := newTestWorld(t, 3)
	fresh := []Command{
		{Seq: 1, Dt: subStep, MY: 1}, {Seq: 2, Dt: subStep, MY: 1},
		{Seq: 3, Dt: subStep, MY: 1}, {Seq: 4, Dt: subStep, MY: 1},
	}
	w.Enqueue(acc, &ParsedInput{Cmds: fresh})

	// One tick buys 50 ms, which is two of these four.
	w.Advance(SimStep.Seconds(), epoch)
	if got := w.Occupant(acc).State.LastSeq; got != 2 {
		t.Fatalf("a tick acknowledged up to %d of four 25 ms sub-steps, expected 2", got)
	}

	// The next frame repeats the two still waiting and adds two fresh ones.
	w.Enqueue(acc, &ParsedInput{Cmds: []Command{
		fresh[2], fresh[3],
		{Seq: 5, Dt: subStep, MY: 1}, {Seq: 6, Dt: subStep, MY: 1},
	}})

	if got := len(w.Occupant(acc).pending); got != 4 {
		t.Fatalf("queue holds %d commands; the two repeats were accepted a second time", got)
	}
}

func TestACommandLargerThanTheBudgetWaitsWholeRatherThanBeingTruncated(t *testing.T) {
	// The ack is one sequence number and the client drops everything at or below
	// it, so there is no way to acknowledge a fraction of a command. Simulating
	// one in part and acknowledging it in full is therefore permanent
	// divergence, and waiting a tick is the only honest answer.
	w, acc := newTestWorld(t, 11)
	start := w.Occupant(acc).State.Pos
	w.Enqueue(acc, &ParsedInput{Cmds: []Command{
		{Seq: 1, Dt: MaxStepSeconds, MY: 1, Yaw: eastward},
	}})

	// One tick is 50 ms against a command asking for 200, so nothing about it
	// may happen yet: no movement, no acknowledgement, and it stays in the queue.
	w.Advance(SimStep.Seconds(), epoch)
	if w.Occupant(acc).State.Pos != start {
		t.Fatalf("an unaffordable command moved him to %+v", w.Occupant(acc).State.Pos)
	}
	if got := w.Occupant(acc).State.LastSeq; got != 0 {
		t.Fatalf("acknowledged sequence %d without having simulated it", got)
	}
	if got := len(w.Occupant(acc).pending); got != 1 {
		t.Fatalf("the command left the queue unsimulated, %d left", got)
	}

	// Four ticks buy exactly what it asked for, and then it is applied WHOLE.
	//
	// THE TICK COUNT IS EXACT ARITHMETIC AND NOT A TOLERANCE, here and in every
	// other test in this package that counts ticks. Four accruals of 0.05 sum to
	// precisely 0.2 in IEEE754 as SimHz and MaxStepSeconds stand today, so the
	// command is affordable on the fourth tick rather than the fifth. Retune
	// either constant to a pair whose accumulation lands one ULP short and this
	// becomes a fifth-tick command and the assertion fails — over the tuning,
	// not over the rule the test is named for. Read a failure here that way
	// before assuming the drain loop broke.
	for i := 0; i < 3; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	moved := math.Hypot(w.Occupant(acc).State.Pos.X-start.X, w.Occupant(acc).State.Pos.Y-start.Y)
	if want := MaxStepSeconds * WalkSpeed; math.Abs(moved-want) > 1e-9 {
		t.Fatalf("moved %.6f m once affordable, the command asked for %.6f m", moved, want)
	}
	if got := w.Occupant(acc).State.LastSeq; got != 1 {
		t.Fatalf("ack is %d after the command was fully simulated", got)
	}
	if got := len(w.Occupant(acc).pending); got != 0 {
		t.Fatalf("%d commands left in the queue after it was applied", got)
	}
}

func TestTheLargestCommandIsAlwaysEventuallyAffordable(t *testing.T) {
	// A command is simulated whole or it waits, which is only safe while the
	// largest command that can exist is no larger than the largest budget that
	// can be banked. Retune MaxStepSeconds past TimeBudgetCap and an occupant
	// who sends one freezes for ever — the queue simply stops draining, nothing
	// else in the package notices, and the player sees a game that has stopped
	// responding rather than an error. So the relationship is pinned here rather
	// than left to be rediscovered.
	if MaxStepSeconds > TimeBudgetCap {
		t.Fatalf("a maximal command asks for %.3fs and the budget caps at %.3fs, so it could never be afforded",
			MaxStepSeconds, TimeBudgetCap)
	}
}

func TestAnUnsanitisedDtCannotBlockTheQueue(t *testing.T) {
	// Enqueue is the boundary at which a command becomes trustworthy, and this
	// is why it has to be. The drain loop tests affordability on Dt directly and
	// NaN <= x is false for every x, so a NaN reaching the queue would wait for a
	// budget that can never arrive and hold everything behind it there for ever
	// — no log, no error, just an occupant who has stopped responding. +Inf does
	// the same, and a dt of a thousand seconds does it too once TimeBudgetCap is
	// what it is.
	//
	// The API is what is under test rather than the production path. parseInput
	// happens to sanitise on the way in, so production is safe today by
	// coincidence of ordering; Enqueue is exported and accepts a ParsedInput
	// anybody may build, so the queue has to be safe on its own terms.
	for _, tc := range []struct {
		name string
		dt   float64
		// walks is the dt the clamp leaves, and therefore the distance a command
		// that got past the boundary intact is worth.
		walks float64
	}{
		{"NaN", math.NaN(), 0},
		{"positive infinity", math.Inf(1), MaxStepSeconds},
		{"longer than the largest step", 1000, MaxStepSeconds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, acc := newTestWorld(t, 11)
			start := w.Occupant(acc).State.Pos
			w.Enqueue(acc, &ParsedInput{Cmds: []Command{
				{Seq: 1, Dt: tc.dt, MY: 1, Yaw: eastward},
			}})

			// A clamped command costs at most MaxStepSeconds, which is four
			// ticks' worth, so ten is generous. An unclamped one never drains at
			// all, so the count is a bound on a finite queue rather than a wait
			// on anything slow — the assertion below fails instead of hanging.
			for i := 0; i < 10; i++ {
				w.Advance(SimStep.Seconds(), epoch)
			}

			if n := len(w.Occupant(acc).pending); n != 0 {
				t.Fatalf("%d commands never drained — the queue is blocked for ever", n)
			}
			if got := w.Occupant(acc).State.LastSeq; got != 1 {
				t.Fatalf("ack is %d, so the command was never folded in", got)
			}
			moved := math.Hypot(w.Occupant(acc).State.Pos.X-start.X, w.Occupant(acc).State.Pos.Y-start.Y)
			if want := tc.walks * WalkSpeed; math.Abs(moved-want) > 1e-9 {
				t.Fatalf("walked %.6f m; clamped, this command is worth %.6f m", moved, want)
			}
		})
	}
}

func TestTrimmedInputIsNotRestoredByAResend(t *testing.T) {
	// maxPending bounds MEMORY, and it enforces that by dropping the OLDEST. So
	// deduplicating on highSeq makes a trimmed command unrecoverable: its
	// sequence stays below the high-water mark for ever, and the resend that the
	// old dedupe would have let restore it is now dropped as a duplicate.
	//
	// That cost is deliberate and it is the right way round. The queue only
	// reaches this depth after roughly a second in which nothing drained, and
	// simulating movement a second stale drags a player somewhere he no longer
	// is, in front of everybody watching him — losing it is cheaper, and the next
	// snapshot reconciles the client to wherever the server actually left him.
	const maxPending = 4 * (MaxCommandsPerFrame + RedundantCommands)

	w, acc := newTestWorld(t, 7)
	sent := make([]Command, 0, maxPending+MaxCommandsPerFrame)
	for i := 1; i <= maxPending+MaxCommandsPerFrame; i++ {
		sent = append(sent, Command{Seq: int64(i), Dt: subStep, MY: 1})
	}
	// No tick between the frames — the queue drains on Advance and nothing else,
	// so this is the burst that overflows it.
	for i := 0; i < len(sent); i += MaxCommandsPerFrame {
		w.Enqueue(acc, &ParsedInput{Cmds: sent[i : i+MaxCommandsPerFrame]})
	}
	if got := len(w.Occupant(acc).pending); got != maxPending {
		t.Fatalf("queue holds %d commands, the trim caps it at %d", got, maxPending)
	}
	if got := w.Occupant(acc).pending[0].Seq; got != MaxCommandsPerFrame+1 {
		t.Fatalf("queue starts at %d, so the trim took from the back", got)
	}

	// The client was never acknowledged for the trimmed ones, so it is entitled
	// to resend them and will. They must not come back.
	//
	// THE HEAD SEQUENCE IS THE LOAD-BEARING ASSERTION, not the length: a
	// re-accepted command is appended and then trimmed away again, so the queue
	// is the same size either way and only what it STARTS at reveals that four
	// live commands were pushed off the front to make room for four stale ones.
	w.Enqueue(acc, &ParsedInput{Cmds: sent[:MaxCommandsPerFrame]})
	if got := len(w.Occupant(acc).pending); got != maxPending {
		t.Fatalf("queue grew to %d: a trimmed command was re-accepted", got)
	}
	if got := w.Occupant(acc).pending[0].Seq; got != MaxCommandsPerFrame+1 {
		t.Fatalf("queue now starts at %d: a trimmed command was re-accepted", got)
	}
}

func TestTimeBudgetStopsASpeedHack(t *testing.T) {
	// The attack this defends against needs no field out of range anywhere: the
	// socket allows ten frames a second, each may carry four sub-steps of up to
	// MaxStepSeconds, so a client that fills every frame asks for eight seconds
	// of simulation per real second. Every individual value is legal; the total
	// is eight times everybody else's speed.
	//
	// So the world spends REAL time, not claimed time.
	w, acc := newTestWorld(t, 3)
	start := w.Occupant(acc).State.Pos

	seq := int64(0)
	for i := 0; i < 10; i++ {
		cmds := make([]Command, 0, MaxCommandsPerFrame)
		for j := 0; j < MaxCommandsPerFrame; j++ {
			seq++
			cmds = append(cmds, Command{Seq: seq, Dt: MaxStepSeconds, MY: 1, Yaw: 0})
		}
		w.Enqueue(acc, &ParsedInput{Cmds: cmds})
	}

	// A multiple of the four ticks one maximal command costs, so the budget lands
	// exactly on a command boundary and what follows is an equality rather than a
	// bound. That tick count is exact IEEE754 arithmetic and not a tolerance —
	// see the note in TestACommandLargerThanTheBudgetWaitsWholeRatherThanBeingTruncated
	// before reading a failure here as a broken drain loop.
	const ticks = 8
	for i := 0; i < ticks; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}

	// Straight along +Y from the middle of the spawn room, and two commands'
	// worth of it: every room is at least RoomMin across, so two metres cannot
	// reach a wall to be slid along.
	moved := math.Hypot(w.Occupant(acc).State.Pos.X-start.X, w.Occupant(acc).State.Pos.Y-start.Y)
	want := ticks * SimStep.Seconds() * WalkSpeed
	if math.Abs(moved-want) > 1e-9 {
		asked := float64(seq) * MaxStepSeconds * WalkSpeed
		t.Fatalf("%.2f s of ticks moved %.3f m; real time buys %.3f m and the queue asked for %.3f m",
			ticks*SimStep.Seconds(), moved, want, asked)
	}
}

func TestTimeBudgetLetsAStutteringClientCatchUp(t *testing.T) {
	// The other half of the same rule, and the reason the cap is not zero: a
	// phone that was backgrounded, or a wifi hiccup, delivers a burst that is
	// completely honest. Refusing it would make the game unplayable on a bus.
	//
	// IT IS WHAT THE IDLE FILL'S GUARD PROTECTS. The fill charges the budget for
	// the time it simulates (Advance), so a fill that ran on every quiet tick
	// would spend exactly the cushion this test is about — which is why it runs
	// only when something is actually counting down. Here the gun is cold, so
	// nothing is, so the half second banks and the burst is affordable.
	w, acc := newTestWorld(t, 3)
	start := w.Occupant(acc).State.Pos

	// Nothing arrives for half a second of real time, so the budget fills.
	for i := 0; i < SimHz/2; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	w.Enqueue(acc, &ParsedInput{Cmds: []Command{
		{Seq: 1, Dt: 0.1, MY: 1, Yaw: 0}, {Seq: 2, Dt: 0.1, MY: 1, Yaw: 0},
		{Seq: 3, Dt: 0.1, MY: 1, Yaw: 0}, {Seq: 4, Dt: 0.1, MY: 1, Yaw: 0},
	}})
	w.Advance(SimStep.Seconds(), epoch)

	moved := math.Hypot(w.Occupant(acc).State.Pos.X-start.X, w.Occupant(acc).State.Pos.Y-start.Y)
	if moved < 3*SimStep.Seconds()*WalkSpeed {
		t.Fatalf("a banked burst only moved %.3f m; the catch-up budget was not spent", moved)
	}
}

func TestPendingInputIsBounded(t *testing.T) {
	// A client that keeps sending while the tick is stalled must not be able to
	// grow this slice without bound. Oldest goes first, because stale input is
	// the input least worth simulating.
	w, acc := newTestWorld(t, 3)
	for i := 0; i < 100; i++ {
		w.Enqueue(acc, &ParsedInput{Cmds: []Command{{Seq: int64(i + 1), Dt: dt, MY: 1}}})
	}
	// The bound is the frame cap plus the redundancy window, four frames deep —
	// redundant copies are dropped by sequence before they reach the queue, so
	// what this guards is a client sending genuinely new input faster than the
	// tick drains it.
	if want := 4 * (MaxCommandsPerFrame + RedundantCommands); len(w.Occupant(acc).pending) > want {
		t.Fatalf("queue grew to %d commands, bound is %d", len(w.Occupant(acc).pending), want)
	}
}

// standOn puts an occupant on top of a pickup, which is the whole interaction —
// there is no use button by design.
func standOn(w *World, acc string, i int) {
	p := w.Level.Pickups[i]
	o := w.Occupant(acc)
	o.State.Pos, o.State.Sector = p.Pos, p.Sector
}

// standAtSpawn walks somebody back to the middle of the first room, which is the
// one place the generator never puts anything — so a test can let the clock run
// without collecting something under its own assertion.
func standAtSpawn(w *World, acc string) {
	o := w.Occupant(acc)
	o.State.Pos, o.State.Sector = w.Level.Spawn, w.Level.SpawnSector
}

func TestWalkingOverSomethingPicksItUp(t *testing.T) {
	w, acc := newTestWorld(t, 11)
	standOn(w, acc, 0)

	w.Advance(SimStep.Seconds(), epoch)

	if w.available(0) {
		t.Fatal("stood on the beer and it is still lying there")
	}
	if w.Occupant(acc).State.Counters["beer"] != 1 {
		t.Fatalf("counter is %d, expected 1", w.Occupant(acc).State.Counters["beer"])
	}
}

func TestAPickupIsCollectedOnceUntilItComesBack(t *testing.T) {
	w, acc := newTestWorld(t, 11)
	standOn(w, acc, 0)
	for i := 0; i < 5; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	if got := w.Occupant(acc).State.Counters["beer"]; got != 1 {
		t.Fatalf("standing on it for five ticks gave %d beers", got)
	}
}

func TestAPickupComesBackOnExactlyTheRightTickAndNotBefore(t *testing.T) {
	// THE RESPAWN IS A DEADLINE IN TICKS, not a float counted down, and this is
	// the test that would catch it drifting: a countdown subtracting SimStep from
	// a float accumulates the error of 0.05's binary expansion, so "back after
	// PickupRespawn" would be a tick early or late depending on which way the
	// last subtraction rounded, and no assertion could be exact.
	//
	// It matters beyond tidiness because the client marks a return by comparing
	// consecutive remaining-masks: an interval that wobbles by a tick is a mark
	// that fires on a frame the server did not intend.
	w, acc := newTestWorld(t, 11)
	standOn(w, acc, 0)

	// The tick that takes it. Everything below is counted from this one.
	w.Advance(SimStep.Seconds(), epoch)
	took := w.Tick
	if w.available(0) {
		t.Fatal("the beer was not taken at all, so this test proves nothing")
	}

	// Walk away, so nothing collects it again the instant it returns.
	standAtSpawn(w, acc)

	// One tick short of the interval it is still gone.
	for w.Tick < took+pickupRespawnTicks-1 {
		w.Advance(SimStep.Seconds(), epoch)
		if w.available(0) {
			t.Fatalf("it came back on tick %d, %d ticks early", w.Tick, took+pickupRespawnTicks-w.Tick)
		}
	}

	// And on the tick itself it is lying there again.
	w.Advance(SimStep.Seconds(), epoch)
	if !w.available(0) {
		t.Fatalf("tick %d is exactly %d ticks after it was taken and it is still gone", w.Tick, pickupRespawnTicks)
	}
	if got := mustSnapshot(t, w, acc).Left & 1; got != 1 {
		t.Fatal("the wire still says it is gone, so the return never reached the client")
	}
}

func TestTheMaskIsTheOnlyThingThatSaysAPickupCameBack(t *testing.T) {
	// A return travels as idempotent full state and never as an event: the mask
	// is re-sent twenty times a second anyway, so a bit coming back IS the
	// announcement, and an "it respawned" field would be bytes spent saying
	// nothing on almost every frame that carried it.
	w, acc := newTestWorld(t, 11)
	standOn(w, acc, 0)
	w.Advance(SimStep.Seconds(), epoch)

	// The one event this game does emit — the collection itself, which is
	// addressed to the person who did it and cannot be derived from anything.
	if got := mustSnapshot(t, w, acc); len(got.Events) != 1 || got.Events[0].E != EventPickup {
		t.Fatalf("collecting published %+v", got.Events)
	}

	standAtSpawn(w, acc)
	for w.Tick < pickupRespawnTicks+2 {
		w.Advance(SimStep.Seconds(), epoch)
	}
	if !w.available(0) {
		t.Fatal("it never came back, so this test proves nothing")
	}
	if got := mustSnapshot(t, w, acc).Events; len(got) != 0 {
		t.Fatalf("the respawn produced %+v; it belongs on the mask and nowhere else", got)
	}
}

func TestTheBuildingHoldsSeveralPeopleAndRefusesTheNextOne(t *testing.T) {
	// The capacity, and the fact that a refusal is a REFUSAL — the service turns
	// this false into a frame the player can read, because silence is this game's
	// answer to a frame it cannot parse, and a full заброшка is a frame it parsed
	// perfectly well and cannot honour.
	w := NewWorld(uuid.New(), 11)
	for i := 0; i < MaxOccupants; i++ {
		id := "account-" + strconv.Itoa(i)
		if _, ok := w.Join(id, "pseudo-"+id, epoch); !ok {
			t.Fatalf("occupant %d of %d was refused", i+1, MaxOccupants)
		}
	}
	if got := w.Occupants(); got != MaxOccupants {
		t.Fatalf("the building holds %d people, expected %d", got, MaxOccupants)
	}
	if _, ok := w.Join("account-too-many", "pseudo-late", epoch); ok {
		t.Fatalf("occupant %d walked into a building that holds %d", MaxOccupants+1, MaxOccupants)
	}

	// And somebody already inside is not refused by his own building being full.
	// A second hello is a reconnect — a page reload, a tunnel, a phone waking up
	// — and turning one into «заброшка полна» would lock a player out of the
	// building he is standing in.
	if _, ok := w.Join("account-0", "pseudo-account-0", epoch.Add(time.Minute)); !ok {
		t.Fatal("a reconnect from somebody already inside was refused as an overflow")
	}
	if got := w.Occupants(); got != MaxOccupants {
		t.Fatalf("a reconnect changed the population to %d", got)
	}
	if got := w.Occupant("account-0").LastSeen; !got.Equal(epoch.Add(time.Minute)) {
		t.Fatalf("a reconnect left LastSeen at %v, so the grace would expire under a socket that came back", got)
	}
}

func TestSomebodyWhoseConnectionStopsComingBackLeavesAfterTheGrace(t *testing.T) {
	// The only way out of the building, and therefore the only thing that
	// produces a visit. Everything shorter than the grace is a reload, a tunnel
	// or a phone locking, and losing somebody's place for one of those would make
	// the game unplayable on a bus.
	w, acc := newTestWorld(t, 11)

	// Still connected: the service marks everybody with a connection on every
	// tick, and a player standing perfectly still sends nothing at all.
	for i := 0; i < 3; i++ {
		at := epoch.Add(time.Duration(i) * time.Second)
		w.Seen(acc, at)
		if left := w.Advance(SimStep.Seconds(), at); len(left) != 0 {
			t.Fatalf("somebody with a connection was taken out of the building: %+v", left)
		}
	}

	// The connection goes, and an absence of exactly the grace is inside it.
	if left := w.Advance(SimStep.Seconds(), epoch.Add(2*time.Second+AbandonGrace)); len(left) != 0 {
		t.Fatal("an absence of exactly the grace ended the visit; it is a floor, not a ceiling")
	}
	if got := w.Occupants(); got != 1 {
		t.Fatalf("the building holds %d people", got)
	}

	// And then it is past it.
	left := w.Advance(SimStep.Seconds(), epoch.Add(2*time.Second+AbandonGrace+time.Second))
	if len(left) != 1 || left[0].AccountID != acc {
		t.Fatalf("the grace expired and produced %+v", left)
	}
	if got := w.Occupants(); got != 0 {
		t.Fatalf("the building still holds %d people", got)
	}
	if w.Occupant(acc) != nil {
		t.Fatal("the occupant is still in the map after leaving")
	}
}

func TestAVisitIsMeasuredToTheLastConnectionAndNotToTheGrace(t *testing.T) {
	// Somebody who joined and left forty seconds later stayed forty seconds — not
	// forty plus the two minutes spent waiting to see whether they were coming
	// back. AbandonGrace is a tolerance for a flaky connection, and counting it
	// would put two extra minutes on every visit in the table.
	w := NewWorld(uuid.New(), 11)
	joined := epoch.Add(10 * time.Minute)
	o, ok := w.Join("account-late", "pseudo-late", joined)
	if !ok {
		t.Fatal("a fresh заброшка refused its first occupant")
	}
	w.Seen("account-late", joined.Add(40*time.Second))

	if got := o.Stayed(); got != 40 {
		t.Fatalf("stayed %d seconds, expected 40", got)
	}

	left := w.Advance(SimStep.Seconds(), joined.Add(40*time.Second+AbandonGrace+time.Second))
	if len(left) != 1 {
		t.Fatalf("the grace produced %+v", left)
	}
	if got := left[0].Stayed(); got != 40 {
		t.Fatalf("the visit records %d seconds; the grace was counted as time in the building", got)
	}
}

func TestAClockThatWentBackwardsIsNotANegativeVisit(t *testing.T) {
	// Cheap to rule out, and the only column in the table that could go negative.
	w := NewWorld(uuid.New(), 1)
	o, _ := w.Join("acct", "pseudo", time.Unix(1000, 0))
	o.LastSeen = time.Unix(900, 0)
	if got := o.Stayed(); got != 0 {
		t.Fatalf("a clock that went backwards gave %d seconds", got)
	}
}

func TestForgettingSomebodyTakesThemOutWithoutAVisit(t *testing.T) {
	// The admin «забыть» path. It runs twice around the anonymising statement, so
	// it has to be idempotent, and it is called for accounts that have never
	// played, so an unknown one is a no-op rather than an error. Nothing is
	// returned, which is exactly how it differs from the grace expiring: there is
	// no occupant handed back for anybody to write a row from.
	w, acc := newTestWorld(t, 11)
	if !w.Remove(acc) {
		t.Fatal("removing somebody who was here reported that they were not")
	}
	if w.Remove(acc) {
		t.Fatal("removing the same person twice reported a second removal")
	}
	if w.Remove("account-never-here") {
		t.Fatal("removing somebody who was never here reported a removal")
	}
	if got := w.Occupants(); got != 0 {
		t.Fatalf("the building holds %d people", got)
	}
}

func TestSnapshotQuantisesAndClearsItsEvents(t *testing.T) {
	// An event is delivered ONCE, on the next frame. A snapshot that re-sent it
	// would replay the same sound forever, which is the failure mode that makes
	// people mute a game.
	w, acc := newTestWorld(t, 11)
	standOn(w, acc, 0)
	w.Advance(SimStep.Seconds(), epoch)

	first := mustSnapshot(t, w, acc)
	if len(first.Events) != 1 || first.Events[0].E != EventPickup {
		t.Fatalf("expected one pickup event, got %+v", first.Events)
	}
	if second := mustSnapshot(t, w, acc); len(second.Events) != 0 {
		t.Fatalf("events repeated on the next frame: %+v", second.Events)
	}

	// Positions are centimetres, never floats: at twenty frames a second a
	// float64 metre is seventeen characters of noise nobody can see.
	if want := cm(w.Occupant(acc).State.Pos.X); first.X != want {
		t.Fatalf("x quantised to %d, expected %d", first.X, want)
	}
}

func TestSnapshotSaysWhatIsOnTheFloorAsABitmask(t *testing.T) {
	// One word instead of the list of remaining ids. Bit i is the pickup at INDEX
	// i, which is the whole contract the client draws from — an index is dense by
	// construction where an id need not be.
	//
	// READ FROM THE ROOM THE PICKUP IS IN, because the mask is now cut to what
	// the reader can see into (world.go, SnapshotFor). Standing on the thing is
	// the simplest way to be somewhere it is visible from, and it is where the
	// second half of this test needs him anyway.
	w, acc := newTestWorld(t, 11)
	const idx = 0
	standOn(w, acc, idx)
	full := mustSnapshot(t, w, acc).Left
	if full&(1<<idx) == 0 {
		t.Fatalf("standing on index %d, the mask %b does not say it is there", idx, full)
	}

	// Collecting one clears exactly its own bit and nothing else.
	w.Advance(SimStep.Seconds(), epoch)

	want := full &^ (1 << idx)
	if got := mustSnapshot(t, w, acc).Left; got != want {
		t.Fatalf("after collecting index %d the mask is %b, expected %b", idx, got, want)
	}
	// Idempotent full state, exactly as the list was: the next frame restates the
	// same world rather than reporting a change, so a dropped frame costs
	// nothing.
	if got := mustSnapshot(t, w, acc).Left; got != want {
		t.Fatalf("the following frame said %b, so the mask is not full state", got)
	}
}

func TestTwoPlayersReachingTheSameBeerAreResolvedDeterministically(t *testing.T) {
	// The property this whole simulation is built to have: the same seed and the
	// same input transcript produce the same world, byte for byte. Step is pure
	// so that it can be, and the world has to hold up its end.
	//
	// Go randomises map order on every range, so a world that walked its
	// occupants in map order would award a contested pickup by coin flip — and
	// collect mutates world-wide state, so the flip is visible in everybody's
	// counters and in the mask on everybody's frame.
	//
	// Repeated inside one process rather than across several: the randomisation
	// is per range, so a hundred iterations here see a hundred different orders.
	ids := []string{"account-a", "account-b", "account-c"}
	build := func() *World {
		w := NewWorld(uuid.Nil, 11)
		for _, id := range ids {
			if _, ok := w.Join(id, "pseudo-"+id, epoch); !ok {
				t.Fatalf("%s was refused", id)
			}
			standOn(w, id, 0)
			// The same transcript for everybody, so nothing except the world's
			// own ordering can separate them.
			//
			// THE TRIGGER IS PULLED ON IT, because the digest now hashes the gun
			// and a digest that hashes something no transcript exercises proves
			// nothing about it. Everybody spawns loaded, so all three fire on
			// this step and all three end with one barrel and the same cadence
			// running — which is what makes any per-occupant disagreement about
			// the gun a difference in the bytes rather than an invisible one.
			w.Enqueue(id, &ParsedInput{Cmds: []Command{{Seq: 1, Dt: subStep, MY: 1, Yaw: eastward, Fire: true}}})
		}
		w.Advance(SimStep.Seconds(), epoch)
		return w
	}

	first := build()
	// The contention has to actually happen, or every iteration below agrees
	// about nothing in particular.
	if first.available(0) {
		t.Fatal("none of them picked it up, so this test proves nothing")
	}
	awarded := 0
	for _, id := range ids {
		awarded += first.Occupant(id).State.Counters["beer"]
	}
	if awarded != 1 {
		t.Fatalf("%d beers awarded for one bottle; the contention this test needs did not happen", awarded)
	}

	want := worldDigest(t, first, ids...)
	for run := 1; run < 200; run++ {
		if got := worldDigest(t, build(), ids...); got != want {
			t.Fatalf("run %d produced a different world:\n want %s\n  got %s", run, want, got)
		}
	}
}

func TestPeersArriveInAStableOrder(t *testing.T) {
	// Same rule, cheaper consequence: a peers array built by ranging the occupant
	// map would reshuffle between two renders of an unchanged world. That makes
	// any golden test over the wire shape flap, and it asks a client to re-key its
	// bookkeeping every frame for no reason at all.
	//
	// It is SLOT order, which is stable because a slot does not move while its
	// holder is in the building — and it is the order the standings lists the same
	// people in, so a client reading the two frames together never has to sort
	// either.
	//
	// The building is filled from MaxOccupants rather than from a list of names,
	// so the day the capacity moves — and it has, to pay for the нейрослопы — this
	// test still fills it rather than being refused at the door.
	w := NewWorld(uuid.Nil, 11)
	names := []string{"account-a", "account-c", "account-b", "account-d", "account-e"}[:MaxOccupants]
	for _, id := range names {
		if _, ok := w.Join(id, "pseudo-"+id, epoch); !ok {
			t.Fatalf("%s was refused", id)
		}
	}
	w.Advance(SimStep.Seconds(), epoch)

	// The order they walked in, and not the order their accounts sort in: c took
	// the second place because he arrived second.
	want := make([]int, 0, MaxOccupants-1)
	for i := 1; i < MaxOccupants; i++ {
		want = append(want, i)
	}
	for i := 0; i < 100; i++ {
		got := make([]int, 0, len(want))
		for _, p := range mustSnapshot(t, w, "account-a").Peers {
			got = append(got, p.Slot)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("render %d listed peers in slots %v, expected %v", i, got, want)
		}
	}
}

func TestASlotIsStableWhileItsHolderIsHereAndReusedAfterHeLeaves(t *testing.T) {
	// The two halves of what a slot is. It is small enough to put on a frame that
	// repeats twenty times a second exactly BECAUSE it is a place in the building
	// rather than a handle on a person — and the price of that is that a place is
	// somebody else's the moment it is empty.
	w := NewWorld(uuid.Nil, 11)
	first, ok := w.Join("account-a", "pseudo-a", epoch)
	if !ok {
		t.Fatal("the first occupant was refused")
	}
	second, ok := w.Join("account-b", "pseudo-b", epoch)
	if !ok {
		t.Fatal("the second occupant was refused")
	}
	if first.Slot == second.Slot {
		t.Fatalf("two people are standing in place %d", first.Slot)
	}

	// Stable while he is here: neither ticking nor reconnecting moves it.
	for i := 0; i < 10; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	if _, ok := w.Join("account-a", "pseudo-a", epoch.Add(time.Minute)); !ok {
		t.Fatal("a reconnect was refused")
	}
	if got := w.Occupant("account-a").Slot; got != first.Slot {
		t.Fatalf("ten ticks and a reconnect moved him from place %d to %d", first.Slot, got)
	}

	// And reused once he stops coming back. Recorded before the removal, because
	// the occupant the world hands back afterwards is a different person.
	freed := first.Slot
	w.Advance(SimStep.Seconds(), epoch.Add(time.Minute+AbandonGrace+time.Second))
	if w.Occupant("account-a") != nil {
		t.Fatal("he outlived the grace")
	}
	third, ok := w.Join("account-c", "pseudo-c", epoch.Add(2*time.Minute))
	if !ok {
		t.Fatal("the next arrival was refused a place that had just been freed")
	}
	if third.Slot != freed {
		t.Fatalf("the freed place %d was not reused; the newcomer got %d", freed, third.Slot)
	}
	// The roster version is what tells the service to publish a standings frame
	// out of turn, and it is exactly this sequence — a place changing hands — that
	// a client cannot survive being told about a second late.
	if w.RosterVersion() <= 0 {
		t.Fatal("a join, a leave and a join did not move the roster version")
	}
}

func TestOnlyYourOwnRoomAndTheOnesThroughItsDoorwaysAreSent(t *testing.T) {
	// INTEREST MANAGEMENT. The sector graph is already the potentially-visible
	// set: a peer in your room or through one of its doorways is sent, and one
	// standing two rooms away is not, because there is a solid wall in the way and
	// the bytes buy nothing.
	//
	// Driven by placing people rather than by walking them, because what is under
	// test is the filter and not the walk — and the level generator's graph is a
	// tree, so every seed has a room two doorways from the spawn to use.
	w := NewWorld(uuid.Nil, 11)
	near, far := twoRoomsApart(t, w.Level)

	me, ok := w.Join("account-me", "pseudo-me", epoch)
	if !ok {
		t.Fatal("the first occupant was refused")
	}
	me.State.Sector = w.Level.SpawnSector

	// Somebody through a doorway, which is what a neighbouring sector means.
	neighbour, _ := w.Join("account-neighbour", "pseudo-neighbour", epoch)
	neighbour.State.Sector = near
	// And somebody two rooms away. Three people rather than the four this used to
	// place, because that is the whole building now (MaxOccupants) — the man in
	// the reader's own room is the stranger, at the end, once he has walked all
	// the way in.
	stranger, _ := w.Join("account-stranger", "pseudo-stranger", epoch)
	stranger.State.Sector = far

	sent := map[int]bool{}
	for _, p := range mustSnapshot(t, w, "account-me").Peers {
		sent[p.Slot] = true
	}
	if !sent[neighbour.Slot] {
		t.Fatalf("somebody in sector %d, through a doorway from sector %d, was filtered out",
			near, w.Level.SpawnSector)
	}
	if sent[stranger.Slot] {
		t.Fatalf("somebody in sector %d, two rooms from sector %d, was sent anyway",
			far, w.Level.SpawnSector)
	}

	// SYMMETRIC, because adjacency is: the man who cannot see you cannot be seen
	// by you either. Once something shoots, that is what stops a player being hit
	// by somebody his own client was never told about.
	for _, p := range mustSnapshot(t, w, "account-stranger").Peers {
		if p.Slot == me.Slot {
			t.Fatalf("sector %d cannot see sector %d, but the reverse was sent", w.Level.SpawnSector, far)
		}
	}

	// And walking back into view puts him back on the frame. Absence is full
	// state and not an event, so nothing has to announce either direction.
	stranger.State.Sector = near
	if !inFrame(t, w, "account-me", stranger.Slot) {
		t.Fatal("he stepped into the next room and was still not sent")
	}
	// And all the way into the reader's own room, which is the case the filter
	// must never touch.
	stranger.State.Sector = w.Level.SpawnSector
	if !inFrame(t, w, "account-me", stranger.Slot) {
		t.Fatal("somebody standing in the same room was filtered out")
	}
}

// inFrame reports whether one viewer's snapshot names a peer.
func inFrame(t *testing.T, w *World, viewer string, slot int) bool {
	t.Helper()
	for _, p := range mustSnapshot(t, w, viewer).Peers {
		if p.Slot == slot {
			return true
		}
	}
	return false
}

func TestAReaderJitteringInADoorwayDoesNotStrobeThePeopleAroundHim(t *testing.T) {
	// THE DEFECT THIS HOLD EXISTS FOR. A sector is derived from a position, so a
	// man standing in the doorway between two rooms belongs to whichever of them
	// the last sub-centimetre of movement put him in — and he crosses back and
	// forth without walking anywhere. The visible set is his room plus the rooms
	// through its doorways, so it moves with him: somebody adjacent to one of the
	// two rooms and not the other joins and leaves the frame at the tick rate, and
	// in a shooter that is a figure you would be aiming at.
	//
	// Driven by placing the reader rather than by walking him into a real doorway,
	// because what is under test is the filter's memory and not the geometry that
	// provokes it: alternating the sector every tick IS the boundary case, exactly
	// reproduced and without depending on where a seed happened to put a wall.
	w := NewWorld(uuid.Nil, 11)
	near, far := twoRoomsApart(t, w.Level)

	me, ok := w.Join("account-me", "pseudo-me", epoch)
	if !ok {
		t.Fatal("the first occupant was refused")
	}
	me.State.Sector = w.Level.SpawnSector
	peer, _ := w.Join("account-peer", "pseudo-peer", epoch)
	peer.State.Sector = far

	// From the spawn he cannot be seen at all, and nothing has been recorded yet.
	if peers := mustSnapshot(t, w, "account-me").Peers; len(peers) != 0 {
		t.Fatalf("a man two rooms away was sent before anybody moved: %+v", peers)
	}

	// Now the doorway between the spawn and `near`, flipped every tick. He is
	// visible from one side and not from the other, and he must not blink.
	for i := 0; i < 40; i++ {
		if i%2 == 0 {
			me.State.Sector = near
		} else {
			me.State.Sector = w.Level.SpawnSector
		}
		w.Advance(SimStep.Seconds(), epoch)
		if len(mustSnapshot(t, w, "account-me").Peers) == 0 {
			t.Fatalf("tick %d of standing in a doorway dropped him off the frame", i)
		}
		// SYMMETRIC, because the hold is written for both directions of a pair on
		// the tick it is recorded. A man who can see you and cannot be seen by you
		// is what a hit test may not be built on.
		if len(mustSnapshot(t, w, "account-peer").Peers) == 0 {
			t.Fatalf("tick %d: the reader dropped off the peer's own frame", i)
		}
	}

	// And it is a hold rather than a licence: walk properly out of sight and he
	// goes, a fifth of a second later. One tick on the visible side first, so the
	// arithmetic is exact — the hold covers the tick it was recorded on and the
	// visibleHoldTicks−1 after it, and the loop above ends on whichever side of
	// the doorway its last iteration fell.
	me.State.Sector = near
	w.Advance(SimStep.Seconds(), epoch)
	me.State.Sector = w.Level.SpawnSector
	for i := int64(1); i < visibleHoldTicks; i++ {
		w.Advance(SimStep.Seconds(), epoch)
		if len(mustSnapshot(t, w, "account-me").Peers) == 0 {
			t.Fatalf("he was dropped %d ticks after leaving the set, before the %d-tick hold was up",
				i, visibleHoldTicks)
		}
	}
	w.Advance(SimStep.Seconds(), epoch)
	if peers := mustSnapshot(t, w, "account-me").Peers; len(peers) != 0 {
		t.Fatalf("the hold never expired: %+v", peers)
	}
}

func TestAFreedPlaceDoesNotInheritTheLastHoldersVisibility(t *testing.T) {
	// A place is reused, so the memory that holds a peer on the frame has to go
	// with its holder. Otherwise the first fifth of a second of somebody else's
	// visit is spent being drawn in a room the reader cannot see into, because the
	// man who used to stand in that place could be.
	w := NewWorld(uuid.Nil, 11)
	_, far := twoRoomsApart(t, w.Level)

	me, _ := w.Join("account-me", "pseudo-me", epoch)
	me.State.Sector = far
	leaver, _ := w.Join("account-leaver", "pseudo-leaver", epoch)
	leaver.State.Sector = far
	freed := leaver.Slot

	// One tick in the same room, which is what fills the memory.
	w.Advance(SimStep.Seconds(), epoch)
	if len(mustSnapshot(t, w, "account-me").Peers) != 1 {
		t.Fatal("two people in one room could not see each other")
	}

	// The leaver stops coming back. The reader is still connected, so only one of
	// them is taken out.
	gone := epoch.Add(AbandonGrace + time.Second)
	w.Seen("account-me", gone)
	w.Advance(SimStep.Seconds(), gone)
	if w.Occupant("account-leaver") != nil {
		t.Fatal("he outlived the grace")
	}

	// And his place is handed to somebody standing where the reader cannot see.
	newcomer, ok := w.Join("account-newcomer", "pseudo-newcomer", gone)
	if !ok {
		t.Fatal("the newcomer was refused a place that had just been freed")
	}
	if newcomer.Slot != freed {
		t.Fatalf("the freed place %d was not reused; the newcomer got %d", freed, newcomer.Slot)
	}
	newcomer.State.Sector = w.Level.SpawnSector
	if peers := mustSnapshot(t, w, "account-me").Peers; len(peers) != 0 {
		t.Fatalf("the newcomer inherited the last holder's visibility: %+v", peers)
	}
}

// twoRoomsApart returns a sector adjacent to the spawn and one exactly two
// doorways from it, which is the pair every interest-management assertion needs.
//
// The portal graph is a tree (level.go), so "two away" is unambiguous: it is a
// neighbour of a neighbour that is not itself a neighbour.
func twoRoomsApart(t *testing.T, l *Level) (near, far int) {
	t.Helper()
	adjacent := func(a int) []int {
		var out []int
		for _, p := range l.Portals {
			switch a {
			case p.A:
				out = append(out, p.B)
			case p.B:
				out = append(out, p.A)
			}
		}
		return out
	}
	first := adjacent(l.SpawnSector)
	for _, n := range first {
		for _, f := range adjacent(n) {
			if f == l.SpawnSector || slices.Contains(first, f) {
				continue
			}
			return n, f
		}
	}
	t.Fatalf("seed %d generated no room two doorways from the spawn", l.Seed)
	return 0, 0
}

func TestTheStandingsAreOfTheBuildingAndNotOfWhatYouCanSee(t *testing.T) {
	// The half of this that is deliberate: a snapshot is cut to what the reader
	// could plausibly see, and the standings is not cut at all. It is a readout of
	// the building — everybody in it, including the reader himself and including
	// the man two rooms away he has no idea is there.
	w := NewWorld(uuid.Nil, 11)
	_, far := twoRoomsApart(t, w.Level)

	me, _ := w.Join("account-me", "pseudo-me", epoch)
	me.State.Sector = w.Level.SpawnSector
	hidden, _ := w.Join("account-hidden", "pseudo-hidden", epoch)
	hidden.State.Sector = far
	hidden.State.Counters = map[string]int{"beer": 3}

	if peers := mustSnapshot(t, w, "account-me").Peers; len(peers) != 0 {
		t.Fatalf("the man two rooms away is on the snapshot: %+v", peers)
	}

	board := w.Standings(epoch.Add(90 * time.Second))
	if len(board.Rows) != 2 {
		t.Fatalf("the standings list %d of the 2 people in the building: %+v", len(board.Rows), board.Rows)
	}
	bySlot := map[int]StandingsRow{}
	for _, r := range board.Rows {
		bySlot[r.Slot] = r
	}
	// The reader is on his own board. There is nothing else to compare against.
	mine, ok := bySlot[me.Slot]
	if !ok {
		t.Fatalf("the reader is not in his own standings: %+v", board.Rows)
	}
	if mine.Name != "pseudo-me" || mine.Seconds != 90 {
		t.Fatalf("the reader's own row is %+v", mine)
	}
	theirs := bySlot[hidden.Slot]
	if theirs.Name != "pseudo-hidden" || theirs.Bag["beer"] != 3 {
		t.Fatalf("the hidden man's row is %+v", theirs)
	}
	// In slot order, which is the order the peers array uses, so nothing has to
	// sort either of them.
	if board.Rows[0].Slot >= board.Rows[1].Slot {
		t.Fatalf("the standings are not in slot order: %+v", board.Rows)
	}
}

func TestTheStandingsSlotsAreTheSnapshotsSlots(t *testing.T) {
	// The correspondence the two frames exist to have. A snapshot addresses a peer
	// by a number and says nothing else about him; the standings is where that
	// number becomes a name. If they could disagree, a client would be labelling
	// figures with the wrong people's handles.
	w := NewWorld(uuid.Nil, 11)
	for _, id := range []string{"account-a", "account-b", "account-c"} {
		if _, ok := w.Join(id, "pseudo-"+id, epoch); !ok {
			t.Fatalf("%s was refused", id)
		}
	}
	w.Advance(SimStep.Seconds(), epoch)

	named := map[int]string{}
	for _, r := range w.Standings(epoch).Rows {
		named[r.Slot] = r.Name
	}
	for _, reader := range []string{"account-a", "account-b", "account-c"} {
		for _, p := range mustSnapshot(t, w, reader).Peers {
			name, ok := named[p.Slot]
			if !ok {
				t.Fatalf("%s was sent a peer in slot %d that the standings do not name", reader, p.Slot)
			}
			if name == "pseudo-"+reader {
				t.Fatalf("%s is looking at himself in slot %d", reader, p.Slot)
			}
		}
	}
	// And the mapping survives somebody leaving and his place being taken: the
	// name against the slot is the newcomer's, which is the only thing that lets a
	// client tell that the figure it was drawing there is gone.
	w.Advance(SimStep.Seconds(), epoch.Add(AbandonGrace+time.Second))
	if _, ok := w.Join("account-d", "pseudo-account-d", epoch.Add(2*AbandonGrace)); !ok {
		t.Fatal("nobody could walk into a building everybody had left")
	}
	for _, r := range w.Standings(epoch.Add(2 * AbandonGrace)).Rows {
		if r.Name != "pseudo-account-d" {
			t.Fatalf("slot %d is still named %q after everybody left", r.Slot, r.Name)
		}
	}
}

func TestNobodyWinsEveryContestedPickup(t *testing.T) {
	// DETERMINISM DOES NOT REQUIRE A FIXED ORDER, and the test above would pass
	// just as happily if it did. Sorting the occupants makes a contested bottle
	// go to whoever drew the lexicographically smaller UUID — the same account,
	// every tick, for the life of both accounts, and every hit test too once
	// something shoots. That is a permanent advantage dressed up as a tie-break.
	//
	// So Advance rotates the sorted order by the tick, and this is what that
	// buys: the same bottle contested over and over goes to each of them in turn.
	ids := []string{"account-a", "account-b"}
	w := NewWorld(uuid.Nil, 11)
	for _, id := range ids {
		if _, ok := w.Join(id, "pseudo-"+id, epoch); !ok {
			t.Fatalf("%s was refused", id)
		}
	}

	wins := map[string]int{}
	const contests = 40
	for i := 0; i < contests; i++ {
		// A fresh contest each tick: the bottle is put back and both of them are
		// standing on it with nothing else to separate them. The counters go with
		// it, because beer caps at PickupKind.Max and a capped counter would stop
		// recording who won.
		w.ready[0] = 0
		for _, id := range ids {
			standOn(w, id, 0)
			w.Occupant(id).State.Counters = nil
		}
		w.Advance(SimStep.Seconds(), epoch)

		won := ""
		for _, id := range ids {
			if w.Occupant(id).State.Counters["beer"] > 0 {
				if won != "" {
					t.Fatalf("contest %d awarded the same bottle to both of them", i)
				}
				won = id
			}
		}
		if won == "" {
			t.Fatalf("contest %d awarded the bottle to nobody, so this test proves nothing", i)
		}
		wins[won]++
	}

	for _, id := range ids {
		if wins[id] == 0 {
			t.Fatalf("%s won none of %d contested pickups (%v); the turn order is a fixed priority",
				id, contests, wins)
		}
	}
}

// worldDigest is the whole of a world's mutable state as bytes, so that two
// buildings can be compared for being identical rather than merely similar.
//
// The occupant ids come in from the caller rather than from the world, because a
// digest that asked the world for its own ordering would be agreeing with the
// thing it exists to check.
//
// EVERY FIELD OF Player BELONGS IN HERE, and the gun is the reason to say so.
// The digest omitted the barrels and both timers for exactly as long as they
// existed, which made this a determinism check that could not see a gun: two
// runs whose occupants had fired in a different order, or whose idle fill had
// granted a different amount of time to a cooldown, hashed the same. A field
// left out is not a smaller test, it is a test that passes for a state it never
// looked at.
func worldDigest(t *testing.T, w *World, ids ...string) string {
	t.Helper()
	type occupantState struct {
		ID       string
		Pos      Vec2
		Sector   int
		Health   int
		LastSeq  int64
		Counters map[string]int
		Loaded   int
		Cooldown float64
		Reload   float64
		// The three counters are here for the same reason, and they are the ones
		// the нейрослопы made worth hashing: a transcript in which the same shots
		// landed on different targets, or in which one run's слоп reached a
		// different man, produces identical positions on the tick after the death
		// and different careers for ever.
		Kills     int
		Deaths    int
		Betrayals int
	}
	// The нейрослопы are in the digest because they are in the world, and the
	// replay property is about the WORLD rather than about the people in it. A
	// digest that hashed only the occupants would go on agreeing with itself while
	// the spawner drew a different room, the pathing sent a creature through a
	// different doorway, or the separation rule broke a tie the other way — which
	// is precisely the class of change this test exists to catch.
	type slopState struct {
		ID      int
		Pos     Vec2
		Sector  int
		Health  int
		TouchAt int64
	}
	out := struct {
		Tick    int64
		Ready   []int64
		Occs    []occupantState
		Slops   []slopState
		SpawnAt int64
	}{Tick: w.Tick, Ready: w.ready, SpawnAt: w.slopSpawnAt}
	for _, sl := range w.slops {
		if sl == nil {
			continue
		}
		out.Slops = append(out.Slops, slopState{
			ID: sl.ID, Pos: sl.Pos, Sector: sl.Sector, Health: sl.Health, TouchAt: sl.touchAt,
		})
	}
	for _, id := range ids {
		o := w.Occupant(id)
		if o == nil {
			t.Fatalf("no occupant %q to digest", id)
		}
		out.Occs = append(out.Occs, occupantState{
			ID: id, Pos: o.State.Pos, Sector: o.State.Sector,
			Health: o.State.Health, LastSeq: o.State.LastSeq, Counters: o.State.Counters,
			Loaded: o.State.Loaded, Cooldown: o.State.CooldownLeft, Reload: o.State.ReloadLeft,
			Kills: o.kills, Deaths: o.deaths, Betrayals: o.betrayals,
		})
	}
	// encoding/json sorts map keys, so the maps in here contribute the same bytes
	// for the same contents whatever order they were filled in.
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// mustSnapshot renders the world for one occupant, failing the test rather than
// returning a zero value — every caller here has just put that occupant in the
// building, so a miss is a broken test rather than a case worth handling.
func mustSnapshot(t *testing.T, w *World, accountID string) Snapshot {
	t.Helper()
	s, ok := w.SnapshotFor(accountID)
	if !ok {
		t.Fatalf("no snapshot for occupant %q", accountID)
	}
	return s
}

// --- the gun, in the world --------------------------------------------------

// arm puts an occupant somewhere harmless with a gun in whatever state a test
// needs. The spawn room is the one place the generator never puts anything, so
// the clock can run without something being collected under an assertion.
func arm(w *World, acc string, loaded, ammo int) *Occupant {
	standAtSpawn(w, acc)
	o := w.Occupant(acc)
	o.State.Loaded = loaded
	if ammo > 0 {
		o.State.Counters[AmmoCounter] = ammo
	}
	return o
}

// fireOnTheWire delivers one trigger pull as a client delivers one: a single
// sub-step carrying the player's own angles, through the queue.
func fireOnTheWire(w *World, acc string, seq int64) {
	o := w.Occupant(acc)
	w.Enqueue(acc, &ParsedInput{Cmds: []Command{
		{Seq: seq, Dt: subStep, Yaw: o.State.Yaw, Pitch: o.State.Pitch, Fire: true},
	}})
}

func TestTheGunKeepsRunningWhileTheClientSaysNothing(t *testing.T) {
	// THE IDLE FILL, and the reason the world grew one. A client emits a command
	// only when something happened — moving, or looking somewhere new — so a
	// player who fires and then stands still with his thumb off the glass sends
	// NOTHING (web/src/lib/vanyadumInput.ts). Every timer this game had before was
	// a position, and a position does not change while nobody is asking it to; a
	// cadence does, because it runs on time passing rather than on input.
	//
	// Without the fill the gun would work only while you were walking, which is
	// the state you are least often in when you are shooting at something.
	w, acc := newTestWorld(t, 11)
	o := arm(w, acc, Barrels, 0)

	fireOnTheWire(w, acc, 1)
	w.Advance(SimStep.Seconds(), epoch)
	if o.State.Loaded != Barrels-1 {
		t.Fatalf("a shot over the wire left %d barrels, expected %d", o.State.Loaded, Barrels-1)
	}
	if o.State.CooldownLeft <= 0 {
		t.Fatal("the shot started no cadence")
	}

	// And now nothing at all arrives, for as long as it takes.
	ticks := 0
	for ; o.State.CooldownLeft > 0 && ticks < 4*SimHz; ticks++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	if o.State.CooldownLeft > 0 {
		t.Fatalf("%d silent ticks and the cadence still has %.3f s on it", ticks, o.State.CooldownLeft)
	}
	if elapsed := float64(ticks) * SimStep.Seconds(); math.Abs(elapsed-FireCooldownSeconds) > SimStep.Seconds()+1e-9 {
		t.Fatalf("the cadence ran out after %.3f s of silence; the catalogue says %.3f", elapsed, FireCooldownSeconds)
	}
	if o.State.Pos != w.Level.Spawn {
		t.Fatalf("the idle fill walked him from the spawn to %+v", o.State.Pos)
	}
}

func TestAnIdleTickDoesNotSnapTheView(t *testing.T) {
	// The trap inside the fill, and the reason it does not build its command out
	// of the zero value: Step assigns c.Yaw and c.Pitch unconditionally, so a
	// bare Command{Dt: idle} would point every player who stopped moving due
	// north and level — twenty times a second, for as long as they stood there.
	w, acc := newTestWorld(t, 11)
	o := arm(w, acc, Barrels, 0)
	o.State.Yaw, o.State.Pitch = 2.2, -0.7

	fireOnTheWire(w, acc, 1)
	w.Advance(SimStep.Seconds(), epoch)
	for i := 0; i < SimHz; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	if math.Abs(o.State.Yaw-2.2) > 1e-9 || math.Abs(o.State.Pitch-(-0.7)) > 1e-9 {
		t.Fatalf("a second of silence moved the view to %.3f/%.3f", o.State.Yaw, o.State.Pitch)
	}
}

func TestQuietTimeCannotBeBankedAndSpentOnTheGun(t *testing.T) {
	// THE EXPLOIT THE FILL'S CHARGE CLOSES, and it needs no field out of range
	// anywhere. If the fill granted the gun its idle seconds for free, a client
	// could stand still while a reload ran — advancing it at real time, because
	// the world was doing that for him — bank the same seconds in his time
	// budget, and then deliver them as a burst of standing-still commands to
	// advance it AGAIN. Close to double the fire rate and a third off every
	// reload, repeatable for as long as he alternated.
	//
	// So the assertion is the invariant rather than the symptom: over any window,
	// the gun cannot be granted more time than actually passed.
	w, acc := newTestWorld(t, 11)
	o := arm(w, acc, 0, ReloadCost)

	fireOnTheWire(w, acc, 1)
	w.Advance(SimStep.Seconds(), epoch)
	if o.State.ReloadLeft != ReloadSeconds {
		t.Fatalf("the reload did not start: %+v", o.State)
	}
	ticks := 1

	// Half a second of silence — exactly TimeBudgetCap, which is the most that
	// could ever be banked.
	for i := 0; i < SimHz/2; i++ {
		w.Advance(SimStep.Seconds(), epoch)
		ticks++
	}

	// And now the burst: a full frame of the largest sub-steps the parser will
	// pass through, every one of them standing perfectly still.
	cmds := make([]Command, 0, MaxCommandsPerFrame)
	for i := 0; i < MaxCommandsPerFrame; i++ {
		cmds = append(cmds, Command{Seq: int64(i + 2), Dt: MaxStepSeconds, Yaw: o.State.Yaw})
	}
	w.Enqueue(acc, &ParsedInput{Cmds: cmds})
	w.Advance(SimStep.Seconds(), epoch)
	ticks++

	granted := ReloadSeconds - o.State.ReloadLeft
	real := float64(ticks) * SimStep.Seconds()
	if granted > real+1e-9 {
		t.Fatalf("%.3f s of reload was granted in %.3f s of real time", granted, real)
	}
}

func TestAColdGunIsNotChargedForStandingStill(t *testing.T) {
	// The other half of the fill's guard, and it is what keeps the charge above
	// from becoming a tax on doing nothing. A still step against a gun with
	// nothing counting down provably changes no field of the player — the axes
	// are zero, so no position, sector or angle can move, and both timers are
	// already at rest — so charging the budget for it would spend the honest
	// client's catch-up cushion on a simulation that did nothing.
	w, acc := newTestWorld(t, 3)
	o := arm(w, acc, Barrels, 0)

	for i := 0; i < SimHz/2; i++ {
		w.Advance(SimStep.Seconds(), epoch)
	}
	if o.budget < TimeBudgetCap-1e-9 {
		t.Fatalf("half a second of standing still with a cold gun banked %.3f s, the cap is %.3f",
			o.budget, TimeBudgetCap)
	}
}

func TestTheSnapshotCarriesTheGunAndOmitsItWhenItIsResting(t *testing.T) {
	// What the HUD draws and what the predictor reconciles against. The shell
	// count is the SERVER's, so a client that mispredicted a refused shot is put
	// right within one frame rather than showing a number it made up.
	w, acc := newTestWorld(t, 11)
	o := arm(w, acc, Barrels, ReloadCost)

	resting, ok := w.SnapshotFor(acc)
	if !ok {
		t.Fatal("no snapshot")
	}
	if resting.Loaded != Barrels {
		t.Fatalf("a full gun was published as %d barrels", resting.Loaded)
	}
	raw, err := json.Marshal(resting)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"d"`, `"r"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("an idle gun published %s: %s", key, raw)
		}
	}

	// A shot, and the two things a client compares against the previous frame to
	// know it happened: one fewer barrel, and a cadence that has appeared.
	fireOnTheWire(w, acc, 1)
	w.Advance(SimStep.Seconds(), epoch)
	fired, _ := w.SnapshotFor(acc)
	if fired.Loaded != Barrels-1 {
		t.Fatalf("after a shot the frame says %d barrels", fired.Loaded)
	}
	if want := ms(FireCooldownSeconds); fired.Cooldown != want {
		t.Fatalf("the cadence was published as %d ms, expected %d", fired.Cooldown, want)
	}
	if fired.Reload != 0 {
		t.Fatalf("a shot published a reload of %d ms", fired.Reload)
	}

	// And the reload, in the units the wire carries rather than the simulation's.
	o.State.Loaded, o.State.CooldownLeft = 0, 0
	fireOnTheWire(w, acc, 2)
	w.Advance(SimStep.Seconds(), epoch)
	reloading, _ := w.SnapshotFor(acc)
	if want := ms(ReloadSeconds); reloading.Reload != want {
		t.Fatalf("the reload was published as %d ms, expected %d", reloading.Reload, want)
	}
	if reloading.Loaded != 0 {
		t.Fatalf("a reloading gun published %d barrels", reloading.Loaded)
	}
}

// watching puts a second and third occupant in the spawn room alongside the one
// newTestWorld made, so that everybody can see everybody. It returns the
// shooter and the people watching him.
func watching(t *testing.T, n int) (*World, string, []string) {
	t.Helper()
	w, shooter := newTestWorld(t, 11)
	arm(w, shooter, Barrels, 0)
	var watchers []string
	for i := 0; i < n; i++ {
		acc := uuid.New().String()
		if _, ok := w.Join(acc, "pseudo-"+acc[:8], epoch); !ok {
			t.Fatalf("watcher %d was refused", i)
		}
		settled(w, acc)
		standAtSpawn(w, acc)
		watchers = append(watchers, acc)
	}
	return w, shooter, watchers
}

// peerStateSeen reads one peer's state out of the frame a viewer is actually
// sent, rather than off the occupant — what is being checked is what crosses the
// wire, and a field with the wrong json tag would satisfy any assertion made
// against the struct.
func peerStateSeen(t *testing.T, w *World, viewer string, slot int) int {
	t.Helper()
	for _, p := range mustSnapshot(t, w, viewer).Peers {
		if p.Slot == slot {
			return p.St
		}
	}
	t.Fatalf("slot %d is not on the frame sent to %s at all", slot, viewer)
	return 0
}

// firedPeer is that state read as the one question most of these tests ask.
func firedPeer(t *testing.T, w *World, viewer string, slot int) bool {
	t.Helper()
	return peerStateSeen(t, w, viewer, slot) == PeerFired
}

func TestAPeersShotIsOnTheFrameForOneTickOnly(t *testing.T) {
	// An action nobody else can see is an unfinished action (CLAUDE.md), and a
	// обрез going off is this game's loudest one — so a shot has to reach the
	// people it was aimed at and not only the man who pulled the trigger. He
	// needs no telling: his own barrel count is on his own snapshot, and the
	// count falling by one IS the shot. A peer carries no barrel count, so this
	// is the field that says so.
	//
	// AND IT IS AN INSTANT RATHER THAN A STATE, which is what this test holds it
	// to. `st` carries four values (message.go, Peer.St) and two of them are
	// DURATIONS a man is in for seconds at a time — which is why the field is now
	// priced at the full snapshot rate and why it cost the building a place. The
	// muzzle flash must not quietly join them: a flash that stayed up for the
	// whole cooldown would be seven times the traffic it is budgeted for, on a
	// field that is already the most expensive thing on a peer.
	w, shooter, watchers := watching(t, 1)
	watcher := watchers[0]
	slot := w.Occupant(shooter).Slot

	// A world nothing has advanced is on tick zero, and so is an occupant who has
	// never fired — the two must not be read as agreeing with each other.
	if firedPeer(t, w, watcher, slot) {
		t.Fatal("a peer on a freshly generated building is flagged as having just fired")
	}
	w.Advance(SimStep.Seconds(), epoch)
	if firedPeer(t, w, watcher, slot) {
		t.Fatal("a peer who has never pulled a trigger is flagged as having fired")
	}

	fireOnTheWire(w, shooter, 1)
	w.Advance(SimStep.Seconds(), epoch)
	if w.Occupant(shooter).State.Loaded != Barrels-1 {
		t.Fatalf("the shot this test is about did not happen: %d barrels", w.Occupant(shooter).State.Loaded)
	}
	if !firedPeer(t, w, watcher, slot) {
		t.Fatal("somebody fired two metres away and the frame said nothing about it")
	}

	// And it is gone on the very next one, and stays gone for the whole cadence —
	// which is exactly where a level would have gone on saying "still cooling"
	// twenty times a second.
	for tick := 1; tick <= int(FireCooldownSeconds/SimStep.Seconds())+2; tick++ {
		w.Advance(SimStep.Seconds(), epoch)
		if firedPeer(t, w, watcher, slot) {
			t.Fatalf("the flash is still on the frame %d ticks after the shot", tick)
		}
	}
}

func TestEverybodyWatchingIsToldAboutTheSameShot(t *testing.T) {
	// The reason the mark is a TICK NUMBER compared against the world's, rather
	// than a flag cleared when it is read. SnapshotFor runs once per VIEWER and
	// consumes this occupant's events on the way out, so a flash written the same
	// way would be handed to whichever viewer happened to be rendered first and
	// withheld from everybody else — and which one that is depends on slot order,
	// so the bug would look like "sometimes you see it".
	//
	// An event is addressed to one person and is right to be consumed; a gunshot
	// belongs to the building.
	// Everybody else the building can hold, which is what makes "the same shot"
	// mean "every viewer there is" — and is derived rather than typed, because the
	// нейрослопы took a place out of the заброшка (MaxOccupants).
	w, shooter, watchers := watching(t, MaxOccupants-1)
	slot := w.Occupant(shooter).Slot

	fireOnTheWire(w, shooter, 1)
	w.Advance(SimStep.Seconds(), epoch)

	for i, acc := range watchers {
		if !firedPeer(t, w, acc, slot) {
			t.Fatalf("watcher %d was not told about the shot the others were", i)
		}
	}
	// And rendering all of them again does not un-tell anybody: the frame is a
	// read of the world rather than a queue drained by looking at it.
	for i, acc := range watchers {
		if !firedPeer(t, w, acc, slot) {
			t.Fatalf("watcher %d lost the shot on a second render of the same tick", i)
		}
	}
}

func TestTheVisitRecordsWhatWasFoundAndNotWhatWasLeftOver(t *testing.T) {
	// The two numbers were the same until the gun started spending beer, and the
	// column means the first of them — "how much beer was collected on the way",
	// in a migration that can no longer be edited. So a visit that read the bag
	// would record a nought for every player who actually used what he found,
	// which is the one thing the splash screen's «твои визиты» list is for.
	w, acc := newTestWorld(t, 11)
	standOn(w, acc, 0)
	w.Advance(SimStep.Seconds(), epoch)

	o := w.Occupant(acc)
	found := o.collected["beer"]
	if found == 0 {
		t.Fatal("standing on a bottle collected nothing")
	}
	if got := o.State.Counters["beer"]; got != found {
		t.Fatalf("carrying %d of the %d collected before anything was spent", got, found)
	}

	// Now empty the gun into the floor and reload out of the bag, which is the
	// ordinary thing a player does with the beer he just picked up.
	o.State.Loaded = 0
	standAtSpawn(w, acc)
	fireOnTheWire(w, acc, 1)
	w.Advance(SimStep.Seconds(), epoch)
	if o.State.ReloadLeft <= 0 {
		t.Fatalf("the reload did not start: %+v", o.State)
	}
	if got := o.State.Counters["beer"]; got != found-ReloadCost {
		t.Fatalf("the bag holds %d after a reload of %d out of %d", got, ReloadCost, found)
	}
	if got := o.collected["beer"]; got != found {
		t.Fatalf("spending beer changed what was collected from %d to %d", found, got)
	}
}

// --- being shot -------------------------------------------------------------
//
// The hit test's own geometry is in hit_test.go, against no world at all. What
// is asserted here is everything the world adds to it: which instant a shot is
// resolved in, who is allowed to be a target, what a hit costs, and what the
// frame says about all of it.

// worldIn builds a заброшка around a HAND-BUILT level, the way the simulation
// tests build one, so a duel can be fought in a room whose dimensions a reader
// can hold in his head. A generated building is right for the rules that are
// about every building; a shot resolved to the centimetre is not one of those.
func worldIn(t *testing.T, l *Level) *World {
	t.Helper()
	w := NewWorld(uuid.New(), 1)
	w.Level = l
	w.ready = make([]int64, len(l.Pickups))
	w.visible = buildVisibility(l)
	w.routes = buildRoutes(l)
	noSlops(w)
	return w
}

// threeRooms is a corridor of rooms whose doorways LINE UP: a shot along Y = 5
// from the first passes through both openings and reaches the third.
//
// That is exactly the case the visibility approximation gets wrong on purpose
// (level.go, buildVisibility): rooms 0 and 2 share no portal, so neither man is
// ever on the other's frame, while the geometry would happily carry a shot
// between them. It is the fixture for the rule that you can only hit what you
// were sent.
func threeRooms() *Level {
	l := &Level{
		Sectors: []Sector{
			{ID: 0, MinX: 0, MinY: 0, MaxX: 10, MaxY: 10, FloorZ: 0, CeilZ: CeilingHeight},
			{ID: 1, MinX: 10, MinY: 0, MaxX: 20, MaxY: 10, FloorZ: 0, CeilZ: CeilingHeight},
			{ID: 2, MinX: 20, MinY: 0, MaxX: 30, MaxY: 10, FloorZ: 0, CeilZ: CeilingHeight},
		},
		Portals: []Portal{
			{A: 0, B: 1, Vertical: true, At: 10, Lo: 4, Hi: 6},
			{A: 1, B: 2, Vertical: true, At: 20, Lo: 4, Hi: 6},
		},
	}
	l.Walls = buildWalls(l)
	l.Spawn, l.SpawnSector = Vec2{X: 5, Y: 5}, 0
	return l
}

// stopwatch is a clock that MOVES, and every test below drives the world with
// one.
//
// IT IS NOT A STYLE CHOICE. The rewind buffer is indexed by wall-clock instants
// (history.go), so a world advanced a hundred times at one fixed instant has a
// hundred frames stamped identically — and every rewind then clamps to the
// oldest of them, which is a world nobody was ever in. The tests above can use a
// fixed epoch because nothing they assert reads the buffer. Nothing below can.
type stopwatch struct{ now time.Time }

func newStopwatch() *stopwatch { return &stopwatch{now: epoch} }

func (s *stopwatch) tick(w *World) {
	s.now = s.now.Add(SimStep)
	w.Advance(SimStep.Seconds(), s.now)
}

func (s *stopwatch) run(w *World, ticks int) {
	for i := 0; i < ticks; i++ {
		s.tick(w)
	}
}

// walkIn puts somebody in the building exactly as a connection does — with the
// walk-in protection Join grants him — and stands him where the test wants him.
// Only the tests about that grant want this; everything else wants joinTo.
func walkIn(t *testing.T, w *World, at Vec2, yaw float64) string {
	t.Helper()
	acc := uuid.New().String()
	if _, ok := w.Join(acc, "pseudo-"+acc[:8], epoch); !ok {
		t.Fatal("the building refused an occupant this test needs")
	}
	stand(t, w, acc, at, yaw)
	return acc
}

// joinTo walks somebody into the building, stands him where the test wants him,
// and settles him in — see settled for why almost every test wants that.
func joinTo(t *testing.T, w *World, at Vec2, yaw float64) string {
	t.Helper()
	acc := walkIn(t, w, at, yaw)
	settled(w, acc)
	return acc
}

// stand puts an occupant somewhere, deriving the room from the position exactly
// as the resolver would.
func stand(t *testing.T, w *World, acc string, at Vec2, yaw float64) {
	t.Helper()
	sector := w.Level.SectorAt(at)
	if sector < 0 {
		t.Fatalf("this test wants somebody standing at %v, which is outside the building", at)
	}
	o := w.Occupant(acc)
	o.State.Pos, o.State.Sector, o.State.Yaw = at, sector, yaw
}

// duel is the fixture almost every test below wants: one open room, two men four
// metres apart on the same line, the first looking straight at the second.
func duel(t *testing.T) (*World, *stopwatch, string, string) {
	t.Helper()
	w := worldIn(t, room(0, 0, 20, 20, 0))
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	victim := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)
	return w, newStopwatch(), shooter, victim
}

// shot pulls the trigger once, through the queue, and advances the world one
// step — which is where the trigger is read, the barrel is spent and the ray is
// cast.
func shot(w *World, sw *stopwatch, acc string, seq int64) {
	fireOnTheWire(w, acc, seq)
	sw.tick(w)
}

func TestOneBarrelTakesHalfOfHim(t *testing.T) {
	w, sw, shooter, victim := duel(t)
	sw.run(w, 3) // a little history, so the shot is resolved against a real one

	shot(w, sw, shooter, 1)

	if got := w.Occupant(victim).State.Health; got != StartHealth-BarrelDamage {
		t.Fatalf("one barrel left him on %d of %d", got, StartHealth)
	}
	if got := w.Occupant(shooter).State.Health; got != StartHealth {
		t.Fatalf("the man who fired is on %d health himself", got)
	}
	if got := w.Occupant(shooter).State.Loaded; got != Barrels-1 {
		t.Fatalf("the shot that landed spent %d barrels", Barrels-got)
	}
}

func TestAFullGunIsExactlyOneKillAndNothingElseEnds(t *testing.T) {
	// Two barrels, one man down — the whole of BarrelDamage being half of
	// MaxHealth. And the заброшка does not notice: nothing here ends, so the
	// bystander is untouched, still standing, and the building still holds
	// everybody.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	victim := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)
	bystander := joinTo(t, w, Vec2{X: 5, Y: 16}, 0)
	sw.run(w, 3)

	shot(w, sw, shooter, 1)
	// The cadence has to expire before the second barrel, exactly as it does for
	// a player mashing the glass.
	sw.run(w, int(FireCooldownSeconds/SimStep.Seconds())+1)
	shot(w, sw, shooter, 2)

	v := w.Occupant(victim)
	if v.State.Health != 0 {
		t.Fatalf("two barrels left him on %d health", v.State.Health)
	}
	if v.deaths != 1 {
		t.Fatalf("he died once and his counter says %d", v.deaths)
	}
	if got := w.Occupant(shooter).betrayals; got != 1 {
		t.Fatalf("the man who did it has %d betrayals", got)
	}
	if got := w.Occupant(shooter).deaths; got != 0 {
		t.Fatalf("killing somebody cost the shooter %d deaths of his own", got)
	}
	// NOTHING ELSE IS TOUCHED. There is no round, no result and no interruption:
	// one occupant is on the floor and everybody else is still walking about.
	b := w.Occupant(bystander)
	if b.State.Health != StartHealth || b.deaths != 0 {
		t.Fatalf("a man in the corner is on %d health with %d deaths", b.State.Health, b.deaths)
	}
	if w.Occupants() != 3 {
		t.Fatalf("a death emptied the building down to %d", w.Occupants())
	}
}

func TestHeGetsUpAtTheSpawnWhenTheTimeIsUpAndNotBefore(t *testing.T) {
	w, sw, shooter, victim := duel(t)
	sw.run(w, 3)
	killedOn := kill(t, w, sw, shooter, victim)

	v := w.Occupant(victim)
	// The deadline is an integer tick rather than a float being decremented
	// (world.go, downUntil), so "not before" is exact and worth asserting to the
	// tick: he is still on the floor on the last one before it.
	deadline := v.downUntil
	if deadline != killedOn+downTicks {
		t.Fatalf("shot on tick %d, he is down until %d, expected %d", killedOn, deadline, killedOn+downTicks)
	}
	for w.Tick < deadline-1 {
		sw.tick(w)
	}
	if v.State.Health != 0 {
		t.Fatalf("he stood up on tick %d, one before the deadline at %d", w.Tick, deadline)
	}
	sw.tick(w)

	if v.State.Health != StartHealth {
		t.Fatalf("he came back on %d health", v.State.Health)
	}
	if v.State.Pos != w.Level.Spawn || v.State.Sector != w.Level.SpawnSector {
		t.Fatalf("he came back at %v in room %d, the spawn is %v in room %d",
			v.State.Pos, v.State.Sector, w.Level.Spawn, w.Level.SpawnSector)
	}
	if v.State.Loaded != Barrels {
		t.Fatalf("he came back holding %d barrels", v.State.Loaded)
	}
	if v.State.ProtectedLeft <= 0 {
		t.Fatal("he came back with no protection at all, which is the spawn camp this rule exists to remove")
	}
	if v.downUntil != 0 {
		t.Fatalf("the deadline he got up on is still set to %d", v.downUntil)
	}
}

func TestADeathCostsTheDeadManNothingHeWasCarrying(t *testing.T) {
	// Stated as a test because it is a decision rather than an oversight
	// (content.go, DownTime): dying costs the three seconds and the walk back,
	// and never the bottles somebody toured the building for.
	w, sw, shooter, victim := duel(t)
	w.Occupant(victim).State.Counters[AmmoCounter] = 4
	sw.run(w, 3)
	kill(t, w, sw, shooter, victim)
	sw.run(w, int(downTicks)+1)

	if got := w.Occupant(victim).State.Counters[AmmoCounter]; got != 4 {
		t.Fatalf("he walked in with 4 bottles, died, and got up with %d", got)
	}
}

func TestAManOnTheFloorPicksNothingUp(t *testing.T) {
	// collect runs from Advance rather than from Step, so the rule Step already
	// applies to everything else a dead man might do — he does not walk and does
	// not shoot — has to be stated separately for the one thing he can still be
	// standing on. Killed on a bottle, the respawn elapsing during his three
	// seconds down, and he drinks it lying there.
	l := room(0, 0, 20, 20, 0)
	l.Pickups = []Pickup{{ID: 0, Kind: "beer", Sector: 0, Pos: Vec2{X: 9, Y: 10}}}
	w := worldIn(t, l)
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	victim := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)

	// He takes it while he is alive, which is what leaves him standing on an empty
	// spot with the respawn running.
	sw.run(w, 3)
	carrying := w.Occupant(victim).State.Counters[AmmoCounter]
	if carrying == 0 {
		t.Fatal("he did not pick up the bottle he is standing on, so this fixture proves nothing")
	}
	kill(t, w, sw, shooter, victim)

	// The bottle comes back while he is still on the floor on top of it. Set
	// rather than waited out: PickupRespawn is ten times DownTime, and what is
	// being tested is the guard rather than the arithmetic of the two constants.
	w.ready[0] = w.Tick
	sw.run(w, 3)

	v := w.Occupant(victim)
	if v.State.Health != 0 {
		t.Fatal("he got up before the part of the test that needs him down")
	}
	if got := v.State.Counters[AmmoCounter]; got != carrying {
		t.Fatalf("a corpse drank one: he went down with %d bottles and is lying there with %d", carrying, got)
	}
	if !w.available(0) {
		t.Fatal("the bottle standing next to a corpse was taken by him")
	}
	if got := v.collected[AmmoCounter]; got != carrying {
		t.Fatalf("his visit records %d bottles found, and he found %d before he died", got, carrying)
	}

	// And the control: on his feet, on the same bottle, he takes it — so the
	// refusal above is being dead rather than a spot nothing can be collected
	// from.
	sw.run(w, int(downTicks)+1)
	stand(t, w, victim, Vec2{X: 9, Y: 10}, 0)
	sw.tick(w)
	if got := v.State.Counters[AmmoCounter]; got != carrying+1 {
		t.Fatalf("back on his feet on the same bottle he is carrying %d, expected %d", got, carrying+1)
	}
}

// kill empties both barrels into somebody, waiting out the cadence between them,
// and fails the test if he is still standing. It returns the tick the fatal
// barrel landed on — the cadence is waited out BEFORE each shot rather than
// after, so the world is left standing exactly on that tick and a test can say
// what should happen a known number of them later.
func kill(t *testing.T, w *World, sw *stopwatch, shooter, victim string) int64 {
	t.Helper()
	seq := w.Occupant(shooter).State.LastSeq
	for i := 0; i < Barrels; i++ {
		if i > 0 {
			sw.run(w, int(FireCooldownSeconds/SimStep.Seconds())+1)
		}
		seq++
		shot(w, sw, shooter, seq)
	}
	if got := w.Occupant(victim).State.Health; got != 0 {
		t.Fatalf("%d barrels left him on %d health", Barrels, got)
	}
	return w.Tick
}

func TestAManOnTheFloorIsNotATargetAndCannotShootBack(t *testing.T) {
	w, sw, shooter, victim := duel(t)
	// Face him back, so the only thing keeping his own shot from landing is
	// being dead.
	stand(t, w, victim, Vec2{X: 9, Y: 10}, eastward+math.Pi)
	w.Occupant(victim).State.Loaded = Barrels
	sw.run(w, 3)
	kill(t, w, sw, shooter, victim)

	deaths := w.Occupant(victim).deaths
	before := w.Occupant(shooter).State.Health

	// A third barrel into the corpse changes nothing at all.
	sw.run(w, int(FireCooldownSeconds/SimStep.Seconds())+1)
	w.Occupant(shooter).State.Loaded = Barrels
	shot(w, sw, shooter, 99)
	if got := w.Occupant(victim).deaths; got != deaths {
		t.Fatalf("shooting a man who was already down killed him again: %d deaths", got)
	}

	// And his own trigger does nothing while he is on it: no barrel spent, and
	// certainly nobody hurt.
	loaded := w.Occupant(victim).State.Loaded
	fireOnTheWire(w, victim, 1)
	sw.tick(w)
	if got := w.Occupant(victim).State.Loaded; got != loaded {
		t.Fatalf("a dead man fired: %d barrels became %d", loaded, got)
	}
	if got := w.Occupant(shooter).State.Health; got != before {
		t.Fatalf("a dead man shot the shooter down to %d health", got)
	}
}

func TestADeadManIsStillAcknowledgedSoHisClientDoesNotChoke(t *testing.T) {
	// He is refused, not ignored. The commands he sends while on the floor are
	// still drained and still acknowledged, because the client drops everything
	// at or below the ack from its pending list — a server that stopped
	// acknowledging would leave a browser replaying a growing list of input the
	// simulation has already decided does nothing.
	w, sw, shooter, victim := duel(t)
	sw.run(w, 3)
	kill(t, w, sw, shooter, victim)

	w.Enqueue(victim, &ParsedInput{Cmds: []Command{
		{Seq: 41, Dt: subStep, MY: 1}, {Seq: 42, Dt: subStep, MY: 1},
	}})
	sw.run(w, 4)

	v := w.Occupant(victim)
	if v.State.LastSeq != 42 {
		t.Fatalf("a dead man's input was acknowledged up to %d of 42", v.State.LastSeq)
	}
	if len(v.pending) != 0 {
		t.Fatalf("%d of his commands are still queued", len(v.pending))
	}
	if v.State.Pos.X != 9 || v.State.Pos.Y != 10 {
		t.Fatalf("his own input walked his corpse to %v", v.State.Pos)
	}
}

func TestProtectionStopsBothTheShotAndTheTrigger(t *testing.T) {
	// BOTH HALVES, OR IT IS A WEAPON (content.go, SpawnProtectSeconds). The man
	// who has just got up is standing on the one spawn everybody knows about, so
	// he cannot be shot there — and he cannot shoot from there either, which is
	// the half that stops the rule from handing the spawn to whoever died last.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	// Both of them at the spawn, which is where the camp would happen.
	shooter := joinTo(t, w, w.Level.Spawn, eastward)
	victim := joinTo(t, w, Vec2{X: w.Level.Spawn.X + 4, Y: w.Level.Spawn.Y}, eastward+math.Pi)
	sw.run(w, 3)
	kill(t, w, sw, shooter, victim)
	sw.run(w, int(downTicks)+1)

	// He is up, at the spawn, and the man who killed him is standing there with
	// a full gun.
	//
	// A FEW TICKS OF BEING ALIVE FIRST, and they are load-bearing rather than
	// tidy: the rewind reaches about two and a half ticks into the past, so a
	// shot fired the instant he stood up would be resolved against the corpse he
	// was — and would miss for a reason that has nothing to do with protection.
	// This waits until the world the shooter is rewound into holds a living,
	// protected man, which is the only thing being tested here.
	v := w.Occupant(victim)
	sw.run(w, 5)
	stand(t, w, shooter, Vec2{X: w.Level.Spawn.X - 3, Y: w.Level.Spawn.Y}, eastward)
	sw.run(w, 3)
	w.Occupant(shooter).State.Loaded = Barrels
	shot(w, sw, shooter, 50)
	if v.State.Health != StartHealth {
		t.Fatalf("a protected man was shot down to %d", v.State.Health)
	}

	// And his own trigger is refused, so protection is not a licence.
	fireOnTheWire(w, victim, 1)
	sw.tick(w)
	if v.State.Loaded != Barrels {
		t.Fatalf("a protected man fired: %d barrels left", v.State.Loaded)
	}

	// It expires on its own, WITHOUT HIM SENDING ANYTHING — a man who respawns
	// and stands perfectly still emits no commands at all, and a protection that
	// only ran down while he walked would be permanent for anybody hiding
	// (world.go, the idle fill, and Player.ticking).
	sw.run(w, int(SpawnProtectSeconds/SimStep.Seconds())+2)
	if v.State.ProtectedLeft != 0 {
		t.Fatalf("protection has %.2fs left after standing still through the whole window", v.State.ProtectedLeft)
	}
	fireOnTheWire(w, victim, 2)
	sw.tick(w)
	if v.State.Loaded != Barrels-1 {
		t.Fatalf("the trigger is still refused after the window: %d barrels", v.State.Loaded)
	}
	w.Occupant(shooter).State.Loaded = Barrels
	shot(w, sw, shooter, 51)
	if v.State.Health == StartHealth {
		t.Fatal("he is still untouchable after the window expired")
	}
}

func TestProtectionExpiresOnRealTimeAndNotOnWhatTheClientSends(t *testing.T) {
	// THE WINDOW IS AN EXPIRY, so something the client does not control has to end
	// it (Occupant.protectedUntil). The countdown on his Player cannot: it is
	// drained by the dt of the commands he sends, and the idle fill that would
	// advance it for a silent player is suppressed by a client claiming any part
	// of the tick at all (world.go, Advance). One millisecond of command per tick
	// therefore runs a two-second window at two percent of real time — before the
	// deadline existed, 1.8 s of it was still left after ten seconds of wall
	// clock, and the man holding it could not be shot.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	camper := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)
	w.protect(w.Occupant(camper))

	// A sliver of a command every tick, for three times the window — the smallest
	// claim that still suppresses the fill, which is the whole of the exploit.
	const sliver = 0.001
	for tick := 0; tick < 3*int(protectTicks); tick++ {
		o := w.Occupant(camper)
		w.Enqueue(camper, &ParsedInput{Cmds: []Command{
			{Seq: int64(tick) + 1, Dt: sliver, Yaw: o.State.Yaw, Pitch: o.State.Pitch},
		}})
		sw.tick(w)
		// Halfway in he is genuinely still protected, or everything below would
		// pass against a world that never granted him anything.
		if tick == int(protectTicks)/2 && !w.protected(o) {
			t.Fatal("the window this test is about had already gone halfway into it")
		}
	}

	v := w.Occupant(camper)
	if v.State.ProtectedLeft != 0 {
		t.Fatalf("after %.1f s of wall clock his own countdown still holds %.2f s",
			3*float64(protectTicks)*SimStep.Seconds(), v.State.ProtectedLeft)
	}
	if w.protected(v) {
		t.Fatal("the world still holds him protected long after the deadline it set")
	}
	// And the consequence, which is the only part a player would notice: he is an
	// ordinary target again.
	w.Occupant(shooter).State.Loaded = Barrels
	shot(w, sw, shooter, 1)
	if v.State.Health != StartHealth-BarrelDamage {
		t.Fatalf("a man who has drip-fed commands for %.1f s is on %d health",
			3*float64(protectTicks)*SimStep.Seconds(), v.State.Health)
	}
}

func TestAManWhoHasJustWalkedInIsProtectedTooAndCannotShootEither(t *testing.T) {
	// RISE'S ARGUMENT, APPLIED TO THE OTHER WAY OF APPEARING AT THE SPAWN. One
	// spawn point, friendly fire on, and a man materialising where everybody knows
	// he will — the newcomer is if anything the easier of the two to camp, because
	// his browser is still loading the building (content.go, SpawnProtectSeconds).
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	newcomer := walkIn(t, w, Vec2{X: 9, Y: 10}, eastward+math.Pi)
	sw.run(w, 3) // a little history, so the shot is resolved against a real one

	shot(w, sw, shooter, 1)
	n := w.Occupant(newcomer)
	if n.State.Health != StartHealth {
		t.Fatalf("a man who had just walked in was shot down to %d", n.State.Health)
	}

	// And his own trigger is refused, which is the half of the rule that stops
	// protection from being a weapon: he is looking straight back at the shooter.
	fireOnTheWire(w, newcomer, 1)
	sw.tick(w)
	if n.State.Loaded != Barrels {
		t.Fatalf("a protected newcomer fired: %d barrels left", n.State.Loaded)
	}
	if got := w.Occupant(shooter).State.Health; got != StartHealth {
		t.Fatalf("his refused shot took the shooter down to %d", got)
	}

	// It runs out on its own with him sending nothing further, and then he is an
	// ordinary man in a building.
	sw.run(w, int(protectTicks)+2)
	if n.State.ProtectedLeft != 0 {
		t.Fatalf("his protection has %.2fs left after the whole window passed", n.State.ProtectedLeft)
	}

	// AND RELOADING THE PAGE DOES NOT BUY ANOTHER ONE. A second hello is a
	// reconnect rather than a second visit (world.go, Join), so it hands back the
	// occupant he already is — otherwise being permanently untouchable would be a
	// matter of refreshing every two seconds.
	if _, ok := w.Join(newcomer, "pseudo", epoch); !ok {
		t.Fatal("the building refused a reconnect")
	}
	if n.State.ProtectedLeft != 0 || w.protected(n) {
		t.Fatalf("a reload refreshed his protection: %.2fs left, deadline %d against tick %d",
			n.State.ProtectedLeft, n.protectedUntil, w.Tick)
	}
	w.Occupant(shooter).State.Loaded = Barrels
	shot(w, sw, shooter, 2)
	if n.State.Health != StartHealth-BarrelDamage {
		t.Fatalf("he is on %d health after the window expired, so it never ended", n.State.Health)
	}
}

func TestAShotIsResolvedWhereTheShooterSawHimAndNotWhereHeIsNow(t *testing.T) {
	// THE FOURTH RUNG, and the case it exists for. What is on the shooter's
	// screen is a peer interpolated into the past by his own latency plus the
	// served interpolation delay (history.go, RewindTo), so a man who aimed at
	// what he could see and was right must not be told he missed.
	//
	// Both directions in one test, because either alone would pass on a server
	// that resolved in the wrong timeframe:
	//
	//   - a target who has since MOVED OUT of the line is still hit, and
	//   - a target who has since moved INTO it is not.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	onTheLine := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)
	offTheLine := joinTo(t, w, Vec2{X: 9, Y: 16}, 0)
	sw.run(w, 6) // enough recorded past to rewind into

	// They swap places, and the shot goes off before a single frame of the new
	// world has been drawn on anybody's screen.
	stand(t, w, onTheLine, Vec2{X: 9, Y: 16}, 0)
	stand(t, w, offTheLine, Vec2{X: 9, Y: 10}, 0)
	shot(w, sw, shooter, 1)

	if got := w.Occupant(onTheLine).State.Health; got != StartHealth-BarrelDamage {
		t.Fatalf("the man the shooter was actually looking at is on %d health", got)
	}
	if got := w.Occupant(offTheLine).State.Health; got != StartHealth {
		t.Fatalf("the man who had just stepped into the line — and was drawn nowhere near it — is on %d", got)
	}
}

func TestAClientCannotBeResolvedAgainstWhereYouStoodASecondAgo(t *testing.T) {
	// The ceiling, from the shot's end. RewindMax is stated in metres because
	// what a liar buys is a distance (gamevanyadum.go), and this is that bound
	// being enforced on the thing it exists for: the rewind is composed and
	// clamped in one place, so a client echoing a tick from the far past is
	// resolved against half a second ago and no further.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	victim := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)
	sw.run(w, 4)

	// He steps out of the line and stays there for comfortably longer than the
	// ceiling allows anybody to reach back.
	stand(t, w, victim, Vec2{X: 9, Y: 16}, 0)
	sw.run(w, int(RewindMax/SimStep)+1)

	// And the shooter claims to have been watching a frame from the very
	// beginning of the building, which is the widest lie the protocol can carry.
	w.Enqueue(shooter, &ParsedInput{Seen: 1})
	if got := w.RTT(shooter); got != RewindMax {
		t.Fatalf("an absurd echo produced a round trip of %v, not the %v ceiling", got, RewindMax)
	}
	shot(w, sw, shooter, 1)

	if got := w.Occupant(victim).State.Health; got != StartHealth {
		t.Fatalf("a client claiming seconds of latency shot a man who left the line long ago: %d health", got)
	}
}

func TestANewcomerIsNotShotWhereTheLastHolderOfHisPlaceStood(t *testing.T) {
	// THE ONE FAILURE LAG COMPENSATION MUST NEVER PRODUCE. The rewind buffer is
	// keyed by SLOT (history.go, spots), because the rewind and the wire have to
	// name the same things — and a slot is handed to somebody else the moment its
	// holder leaves. Everything recorded while the last man stood in it therefore
	// describes the next one, for the whole of RewindMax, unless the ring is
	// purged when the place is freed (history.forget).
	//
	// It is reachable on the ordinary path rather than by contrivance: Advance
	// records the frame and THEN releases whoever has been abandoned, so the last
	// thing the ring learns about a place is where the departed man was standing
	// on the tick he left it.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	leaver := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)
	freed := w.Occupant(leaver).Slot
	sw.run(w, 4) // the ring fills with him standing squarely on the line

	// He drops his connection, and the next tick takes him out of the building.
	w.Occupant(leaver).LastSeen = sw.now.Add(-2 * AbandonGrace)
	sw.tick(w)
	if w.Occupant(leaver) != nil {
		t.Fatal("the man this test needs out of the building is still in it")
	}

	// Somebody else walks in and takes the place he was holding — the lowest free
	// one, which is his — and stands nowhere near the line of fire. He is settled
	// in deliberately: a protected newcomer would be untouchable for a reason that
	// has nothing to do with the ring, and would hide exactly the bug being
	// probed.
	newcomer := joinTo(t, w, Vec2{X: 9, Y: 17}, 0)
	if got := w.Occupant(newcomer).Slot; got != freed {
		t.Fatalf("the newcomer took place %d, not the freed %d, so this test proves nothing", got, freed)
	}

	// And the shot goes down the line the departed man was on. The rewind reaches
	// about two and a half ticks back, so this is fired well inside the window in
	// which the ring still remembers him.
	sw.tick(w)
	shot(w, sw, shooter, 1)

	n := w.Occupant(newcomer)
	if n.State.Health != StartHealth {
		t.Fatalf("a man seven metres off the line was shot down to %d, standing in somebody else's past", n.State.Health)
	}
	if got := w.Occupant(shooter).betrayals; got != 0 {
		t.Fatalf("the shot that hit nobody was recorded as %d betrayals", got)
	}

	// The control: the same shooter, the same gun, and the newcomer standing
	// where the line actually is. If this missed too, the miss above would be a
	// broken fixture rather than the purge.
	stand(t, w, newcomer, Vec2{X: 9, Y: 10}, 0)
	sw.run(w, int(FireCooldownSeconds/SimStep.Seconds())+1)
	shot(w, sw, shooter, 2)
	if n.State.Health != StartHealth-BarrelDamage {
		t.Fatalf("standing on the line himself he is on %d health, so the fixture cannot hit anybody", n.State.Health)
	}
}

func TestYouCannotShootSomebodyYouWereNeverSent(t *testing.T) {
	// The visibility filter is load-bearing now rather than merely economical.
	// Rooms 0 and 2 share no doorway, so neither man is ever on the other's
	// frame — and the two openings line up, so the geometry alone would carry the
	// shot straight through. A kill nobody could see coming, landed by a shooter
	// whose screen was empty, is the one thing interest management may not permit.
	w := worldIn(t, threeRooms())
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 2, Y: 5}, eastward)
	nextDoor := joinTo(t, w, Vec2{X: 15, Y: 5}, 0)
	twoRoomsOver := joinTo(t, w, Vec2{X: 25, Y: 5}, 0)
	sw.run(w, 3)

	// He really is invisible, which is what makes the miss below meaningful.
	for _, p := range mustSnapshot(t, w, shooter).Peers {
		if p.Slot == w.Occupant(twoRoomsOver).Slot {
			t.Fatal("the man two rooms away is on the frame; this fixture proves nothing")
		}
	}

	// The near man is taken out of the line so that the only thing the ray can
	// reach is the one the frame never mentioned.
	stand(t, w, nextDoor, Vec2{X: 15, Y: 9}, 0)
	sw.run(w, 3)
	shot(w, sw, shooter, 1)

	if got := w.Occupant(twoRoomsOver).State.Health; got != StartHealth {
		t.Fatalf("a man who was never on the shooter's frame is on %d health", got)
	}
	// And the control: the same shot with somebody VISIBLE in the line lands, so
	// the miss above is the filter rather than the geometry.
	stand(t, w, nextDoor, Vec2{X: 15, Y: 5}, 0)
	sw.run(w, int(FireCooldownSeconds/SimStep.Seconds())+1)
	shot(w, sw, shooter, 2)
	if got := w.Occupant(nextDoor).State.Health; got != StartHealth-BarrelDamage {
		t.Fatalf("a shot through the doorway at a peer he could see left him on %d", got)
	}
}

func TestTheFrameSaysWhoIsDownAndWhoIsProtected(t *testing.T) {
	// What crosses the wire, read off the frame a viewer is actually sent. A
	// state a peer carries and nobody can see is the same as no state at all.
	w, sw, shooter, victim := duel(t)
	sw.run(w, 3)
	slot := w.Occupant(victim).Slot

	if got := peerStateSeen(t, w, shooter, slot); got != 0 {
		t.Fatalf("a man standing about unharmed is published as state %d", got)
	}
	kill(t, w, sw, shooter, victim)

	if got := peerStateSeen(t, w, shooter, slot); got != PeerDown {
		t.Fatalf("a man on the floor is published as state %d, expected %d", got, PeerDown)
	}
	// His own frame says how long, in the milliseconds the wire carries.
	mine := mustSnapshot(t, w, victim)
	if mine.Health != 0 {
		t.Fatalf("his own frame says %d health", mine.Health)
	}
	if mine.Down <= 0 || mine.Down > int(DownTime/time.Millisecond) {
		t.Fatalf("his own frame says %d ms to get up, of a window of %v", mine.Down, DownTime)
	}
	if mine.Protect != 0 {
		t.Fatalf("a man on the floor is published as protected for %d ms", mine.Protect)
	}

	sw.run(w, int(downTicks)+1)
	if got := peerStateSeen(t, w, shooter, slot); got != PeerProtected {
		t.Fatalf("a man who has just got up is published as state %d, expected %d", got, PeerProtected)
	}
	up := mustSnapshot(t, w, victim)
	if up.Down != 0 {
		t.Fatalf("a man on his feet is published as %d ms from getting up", up.Down)
	}
	if up.Protect <= 0 || up.Protect > ms(SpawnProtectSeconds) {
		t.Fatalf("his protection is published as %d ms of a window of %v s", up.Protect, SpawnProtectSeconds)
	}

	// And it goes away by itself, leaving a peer that costs nothing again.
	sw.run(w, int(SpawnProtectSeconds/SimStep.Seconds())+2)
	if got := peerStateSeen(t, w, shooter, slot); got != 0 {
		t.Fatalf("a man who is simply standing there is published as state %d", got)
	}
	if got := mustSnapshot(t, w, victim).Protect; got != 0 {
		t.Fatalf("expired protection is still published as %d ms", got)
	}
}

func TestEverybodyIsToldSomebodyWasHitAndOnlyOnTheTickItHappened(t *testing.T) {
	// The project's rule, applied to the loudest thing this game does: an action
	// nobody else can see is an unfinished action. A hit moves nobody, so there
	// is nothing on the frame to derive it from — which is why the mark exists at
	// all — and it belongs to the whole room rather than to the man who fired.
	//
	// IT IS ALSO THE SHOOTER'S OWN ACKNOWLEDGEMENT. He knows he fired, because
	// his own barrel count fell; the man he was pointing at is marked on the very
	// same frame; and that is the whole of "I connected", with no field addressed
	// to him.
	w := worldIn(t, room(0, 0, 20, 20, 0))
	sw := newStopwatch()
	shooter := joinTo(t, w, Vec2{X: 5, Y: 10}, eastward)
	victim := joinTo(t, w, Vec2{X: 9, Y: 10}, 0)
	watcher := joinTo(t, w, Vec2{X: 5, Y: 16}, 0)
	sw.run(w, 3)
	slot := w.Occupant(victim).Slot

	shot(w, sw, shooter, 1)

	for who, viewer := range map[string]string{"the shooter": shooter, "a bystander": watcher} {
		if got := peerStateSeen(t, w, viewer, slot); got != PeerHit {
			t.Fatalf("%s sees the man who was just shot as state %d, expected %d", who, got, PeerHit)
		}
	}
	// Rendering the same tick again does not un-tell anybody: the mark is read
	// against the world's tick rather than consumed by whoever looks first.
	if got := peerStateSeen(t, w, watcher, slot); got != PeerHit {
		t.Fatal("the second viewer of the same tick lost the hit")
	}
	// And the victim's own frame needs no mark: his health fell, which is the
	// same per-frame comparison the client already makes for the barrel count.
	if got := mustSnapshot(t, w, victim).Health; got != StartHealth-BarrelDamage {
		t.Fatalf("the man who was shot is published as %d health", got)
	}

	// One tick, exactly. A mark that stayed up would be a duration rather than an
	// instant, and the wire budget is measured on the difference.
	sw.tick(w)
	if got := peerStateSeen(t, w, watcher, slot); got != 0 {
		t.Fatalf("the hit mark is still on the frame a tick later, as state %d", got)
	}
}

func TestTheStandingsCountTheDeathsAndTheBetrayalsAndNoKills(t *testing.T) {
	// The joke, as a frame. Every kill in this building is a friend's, so it goes
	// on its own line and adds to no total anywhere — there is no kill column,
	// because there is nothing here to kill but each other.
	w, sw, shooter, victim := duel(t)
	sw.run(w, 3)
	kill(t, w, sw, shooter, victim)

	board := w.Standings(epoch)
	rows := map[int]StandingsRow{}
	for _, r := range board.Rows {
		rows[r.Slot] = r
	}
	if got := rows[w.Occupant(shooter).Slot]; got.Betrayals != 1 || got.Deaths != 0 {
		t.Fatalf("the shooter's row says %d betrayals and %d deaths", got.Betrayals, got.Deaths)
	}
	if got := rows[w.Occupant(victim).Slot]; got.Deaths != 1 || got.Betrayals != 0 {
		t.Fatalf("the victim's row says %d deaths and %d betrayals", got.Deaths, got.Betrayals)
	}

	// On the standings and NOT on the snapshot: both numbers move a few times a
	// minute, and the snapshot repeats twenty times a second.
	raw, err := json.Marshal(mustSnapshot(t, w, shooter))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"br"`, `"d":1`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("a score is riding the repeating frame (%s): %s", key, raw)
		}
	}
}

func TestTheSameShotsKillTheSamePeopleEveryTime(t *testing.T) {
	// The property the whole simulation is built to have, extended to the thing
	// that now depends on the order occupants are stepped in. Two men firing at
	// each other on the same tick is a contest, and the step order decides it —
	// so it must be decided by the WORLD's own state (the tick, via turnOrder)
	// rather than by Go's map iteration, which is randomised on every range.
	digest := func() string {
		w := worldIn(t, room(0, 0, 20, 20, 0))
		sw := newStopwatch()
		// Fixed account ids, because the order they sort in is part of what is
		// being pinned — generated ones would make each run a different world.
		accs := []string{
			"00000000-0000-0000-0000-0000000000a1",
			"00000000-0000-0000-0000-0000000000b2",
			"00000000-0000-0000-0000-0000000000c3",
		}
		for i, acc := range accs {
			if _, ok := w.Join(acc, "pseudo", epoch); !ok {
				t.Fatal("the building refused an occupant")
			}
			settled(w, acc)
			// A triangle, each man looking at the next one round it.
			stand(t, w, acc, Vec2{X: 8 + float64(i)*2, Y: 10}, eastward)
			w.Occupant(acc).State.Counters[AmmoCounter] = 3
		}
		// The one facing the others is turned round, so the fire goes both ways.
		stand(t, w, accs[2], Vec2{X: 12, Y: 10}, eastward+math.Pi)

		// The protection is part of the digest, because it decides who is a target
		// — a window granted or expired one tick out of step is a different
		// massacre. SAMPLED AS THE RUN GOES rather than read off the end of it:
		// every window either expires or is cleared by a death (world.go, wound),
		// so by the last tick a final reading of it is zero for everybody and a
		// drift in the middle would leave no trace in it at all.
		protected := map[string]float64{}

		var seq int64
		for round := 0; round < 40; round++ {
			seq++
			for _, acc := range accs {
				fireOnTheWire(w, acc, seq)
			}
			sw.run(w, 4)
			for _, acc := range accs {
				protected[acc] += w.Occupant(acc).State.ProtectedLeft
			}
		}

		var b strings.Builder
		for _, acc := range accs {
			o := w.Occupant(acc)
			fmt.Fprintf(&b, "%s hp=%d down=%d prot=%d/%.4f deaths=%d betrayals=%d pos=%.4f,%.4f loaded=%d|",
				acc[len(acc)-2:], o.State.Health, o.downUntil,
				o.protectedUntil, protected[acc], o.deaths, o.betrayals,
				o.State.Pos.X, o.State.Pos.Y, o.State.Loaded)
		}
		return b.String()
	}

	want := digest()
	if !strings.Contains(want, "deaths=1") && !strings.Contains(want, "deaths=2") {
		t.Fatalf("nobody died in a transcript that is supposed to be a massacre: %s", want)
	}
	for i := 0; i < 8; i++ {
		if got := digest(); got != want {
			t.Fatalf("run %d produced a different massacre:\n got %s\nwant %s", i, got, want)
		}
	}
}

func TestTheFloorMaskIsCutToTheRoomsYouCanSee(t *testing.T) {
	// THE PRIVACY RESIDUAL, closed. The mask names the position of everything on
	// the floor, and a bit clearing plus the next standings frame reconstructs
	// where a player the reader was never sent was standing when he took it. That
	// was tolerable while nothing in this game could shoot; the обрез is what
	// ended the exemption.
	l := threeRooms()
	l.Pickups = []Pickup{
		{ID: 0, Kind: "beer", Sector: 1, Pos: Vec2{X: 15, Y: 8}},
		{ID: 1, Kind: "beer", Sector: 2, Pos: Vec2{X: 25, Y: 8}},
	}
	w := worldIn(t, l)
	reader := joinTo(t, w, Vec2{X: 2, Y: 5}, eastward)

	got := mustSnapshot(t, w, reader).Left
	if got&1 == 0 {
		t.Fatalf("the bottle in the room through his own doorway is missing from %b", got)
	}
	if got&2 != 0 {
		t.Fatalf("the mask %b names a bottle two rooms away, which is a position he was never sent", got)
	}

	// And walking into that room puts it back, so the field is still idempotent
	// full state of what he can see rather than a memory of what he has seen.
	stand(t, w, reader, Vec2{X: 15, Y: 5}, eastward)
	if got := mustSnapshot(t, w, reader).Left; got&2 == 0 {
		t.Fatalf("standing next door to it, the mask %b still says it is not there", got)
	}
}
