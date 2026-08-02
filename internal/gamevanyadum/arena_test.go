package gamevanyadum

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestArena(t *testing.T, seed int64) *Arena {
	t.Helper()
	acc := uuid.New().String()
	return NewArena(uuid.New(), acc, "pseudo-"+acc[:8], seed, time.Unix(0, 0))
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
func walkOnTheWire(t *testing.T, a *Arena, frames, perFrame int, proto Command) {
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
		a.Enqueue(a.AccountID, &ParsedInput{Cmds: append(append([]Command{}, tail...), fresh...)})
		unacked = append(unacked, fresh...)

		a.Advance(SimStep.Seconds(), time.Unix(0, 0))

		ack := a.Owner().State.LastSeq
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
	for i := 0; i < frames*perFrame*2 && len(a.Owner().pending) > 0; i++ {
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	if n := len(a.Owner().pending); n != 0 {
		t.Fatalf("%d commands never drained", n)
	}
}

func TestEightSubStepsWalkExactlyEightSubSteps(t *testing.T) {
	// Eight sub-steps of walking asked for, eight walked. The overshoot this
	// rules out is not rounding: a command the arena had queued but not yet
	// simulated used to be accepted a second time when the client repeated it,
	// so the redundancy window bought movement rather than insurance, and the
	// player was dragged forward while walking.
	a := newTestArena(t, 11)
	start := a.Owner().State.Pos

	// Straight along +X from the middle of the spawn room. Every room is at
	// least RoomMin across, so a metre of walking cannot reach a wall to be
	// slid along, and the generator never puts a pickup in the spawn room, so
	// nothing here can end the run underneath the assertion.
	const frames = 2
	walkOnTheWire(t, a, frames, MaxCommandsPerFrame, Command{Dt: subStep, MY: 1, Yaw: eastward})

	want := frames * MaxCommandsPerFrame * subStep * WalkSpeed
	got := math.Hypot(a.Owner().State.Pos.X-start.X, a.Owner().State.Pos.Y-start.Y)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("walked %.6f m where %.6f m was asked for", got, want)
	}
}

func TestARedundantCopyOfAQueuedCommandIsDroppedToo(t *testing.T) {
	// The half of the redundancy rule that used to be missing. A command the
	// arena has ACCEPTED but not yet simulated is still unacknowledged, so the
	// client is right to repeat it — and the arena must still drop the copy,
	// because deduplicating against what has been APPLIED lets precisely those
	// through, and a frame carries twice what a tick can afford.
	a := newTestArena(t, 3)
	fresh := []Command{
		{Seq: 1, Dt: subStep, MY: 1}, {Seq: 2, Dt: subStep, MY: 1},
		{Seq: 3, Dt: subStep, MY: 1}, {Seq: 4, Dt: subStep, MY: 1},
	}
	a.Enqueue(a.AccountID, &ParsedInput{Cmds: fresh})

	// One tick buys 50 ms, which is two of these four.
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	if got := a.Owner().State.LastSeq; got != 2 {
		t.Fatalf("a tick acknowledged up to %d of four 25 ms sub-steps, expected 2", got)
	}

	// The next frame repeats the two still waiting and adds two fresh ones.
	a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{
		fresh[2], fresh[3],
		{Seq: 5, Dt: subStep, MY: 1}, {Seq: 6, Dt: subStep, MY: 1},
	}})

	if got := len(a.Owner().pending); got != 4 {
		t.Fatalf("queue holds %d commands; the two repeats were accepted a second time", got)
	}
}

func TestACommandLargerThanTheBudgetWaitsWholeRatherThanBeingTruncated(t *testing.T) {
	// The ack is one sequence number and the client drops everything at or below
	// it, so there is no way to acknowledge a fraction of a command. Simulating
	// one in part and acknowledging it in full is therefore permanent
	// divergence, and waiting a tick is the only honest answer.
	a := newTestArena(t, 11)
	start := a.Owner().State.Pos
	a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{
		{Seq: 1, Dt: MaxStepSeconds, MY: 1, Yaw: eastward},
	}})

	// One tick is 50 ms against a command asking for 200, so nothing about it
	// may happen yet: no movement, no acknowledgement, and it stays in the queue.
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	if a.Owner().State.Pos != start {
		t.Fatalf("an unaffordable command moved him to %+v", a.Owner().State.Pos)
	}
	if got := a.Owner().State.LastSeq; got != 0 {
		t.Fatalf("acknowledged sequence %d without having simulated it", got)
	}
	if got := len(a.Owner().pending); got != 1 {
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
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	moved := math.Hypot(a.Owner().State.Pos.X-start.X, a.Owner().State.Pos.Y-start.Y)
	if want := MaxStepSeconds * WalkSpeed; math.Abs(moved-want) > 1e-9 {
		t.Fatalf("moved %.6f m once affordable, the command asked for %.6f m", moved, want)
	}
	if got := a.Owner().State.LastSeq; got != 1 {
		t.Fatalf("ack is %d after the command was fully simulated", got)
	}
	if got := len(a.Owner().pending); got != 0 {
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
	//
	// THIS IS A GUARD AGAINST A FUTURE RETUNE, NOT A REGRESSION TEST. It compares
	// two untyped constants and nothing else, so it passes identically on the
	// code this iteration replaced — which had no starvation to guard against,
	// because it truncated the boundary command instead of waiting for it. What
	// the new drain loop added is a dependency on this inequality, and this is
	// where that dependency is written down.
	if MaxStepSeconds > TimeBudgetCap {
		t.Fatalf("a maximal command asks for %.3fs and the budget caps at %.3fs, so it could never be afforded",
			MaxStepSeconds, TimeBudgetCap)
	}
}

func TestAnUnsanitisedDtCannotBlockTheQueue(t *testing.T) {
	// Enqueue is the boundary at which a command becomes trustworthy, and this
	// is why it has to be. The drain loop tests affordability on Dt directly and
	// NaN <= x is false for every x, so a NaN reaching the queue would wait for a
	// budget that can never arrive and hold everything behind it there for the
	// rest of the run — no log, no error, just an occupant who has stopped
	// responding. +Inf does the same, and a dt of a thousand seconds does it too
	// once TimeBudgetCap is what it is.
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
			a := newTestArena(t, 11)
			start := a.Owner().State.Pos
			a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{
				{Seq: 1, Dt: tc.dt, MY: 1, Yaw: eastward},
			}})

			// A clamped command costs at most MaxStepSeconds, which is four
			// ticks' worth, so ten is generous. An unclamped one never drains at
			// all, so the count is a bound on a finite queue rather than a wait
			// on anything slow — the assertion below fails instead of hanging.
			for i := 0; i < 10; i++ {
				a.Advance(SimStep.Seconds(), time.Unix(0, 0))
			}

			if n := len(a.Owner().pending); n != 0 {
				t.Fatalf("%d commands never drained — the queue is blocked for ever", n)
			}
			if got := a.Owner().State.LastSeq; got != 1 {
				t.Fatalf("ack is %d, so the command was never folded in", got)
			}
			moved := math.Hypot(a.Owner().State.Pos.X-start.X, a.Owner().State.Pos.Y-start.Y)
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

	a := newTestArena(t, 7)
	sent := make([]Command, 0, maxPending+MaxCommandsPerFrame)
	for i := 1; i <= maxPending+MaxCommandsPerFrame; i++ {
		sent = append(sent, Command{Seq: int64(i), Dt: subStep, MY: 1})
	}
	// No tick between the frames — the queue drains on Advance and nothing else,
	// so this is the burst that overflows it.
	for i := 0; i < len(sent); i += MaxCommandsPerFrame {
		a.Enqueue(a.AccountID, &ParsedInput{Cmds: sent[i : i+MaxCommandsPerFrame]})
	}
	if got := len(a.Owner().pending); got != maxPending {
		t.Fatalf("queue holds %d commands, the trim caps it at %d", got, maxPending)
	}
	if got := a.Owner().pending[0].Seq; got != MaxCommandsPerFrame+1 {
		t.Fatalf("queue starts at %d, so the trim took from the back", got)
	}

	// The client was never acknowledged for the trimmed ones, so it is entitled
	// to resend them and will. They must not come back.
	//
	// THE HEAD SEQUENCE IS THE LOAD-BEARING ASSERTION, not the length: a
	// re-accepted command is appended and then trimmed away again, so the queue
	// is the same size either way and only what it STARTS at reveals that four
	// live commands were pushed off the front to make room for four stale ones.
	a.Enqueue(a.AccountID, &ParsedInput{Cmds: sent[:MaxCommandsPerFrame]})
	if got := len(a.Owner().pending); got != maxPending {
		t.Fatalf("queue grew to %d: a trimmed command was re-accepted", got)
	}
	if got := a.Owner().pending[0].Seq; got != MaxCommandsPerFrame+1 {
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
	// So the arena spends REAL time, not claimed time.
	//
	// WHOLE-OR-WAIT CHANGED HOW THAT DEFENCE SHOWS UP, and this test follows it.
	// The truncating drain simulated a fragment of the head command on the very
	// first tick, so one Advance was enough to see the budget bite and an upper
	// bound on a single tick's distance was the whole assertion. Now a command is
	// afforded whole or left where it is, and one tick affords none of these — so
	// that same upper bound would pass over a queue nothing had touched. The rate
	// limit is therefore probed across enough ticks for commands to genuinely
	// drain, and asserted as an EQUALITY against the elapsed budget.
	a := newTestArena(t, 3)
	start := a.Owner().State.Pos

	seq := int64(0)
	for i := 0; i < 10; i++ {
		cmds := make([]Command, 0, MaxCommandsPerFrame)
		for j := 0; j < MaxCommandsPerFrame; j++ {
			seq++
			cmds = append(cmds, Command{Seq: seq, Dt: MaxStepSeconds, MY: 1, Yaw: 0})
		}
		a.Enqueue(a.AccountID, &ParsedInput{Cmds: cmds})
	}

	// A multiple of the four ticks one maximal command costs, so the budget lands
	// exactly on a command boundary and what follows is an equality rather than a
	// bound. That tick count is exact IEEE754 arithmetic and not a tolerance —
	// see the note in TestACommandLargerThanTheBudgetWaitsWholeRatherThanBeingTruncated
	// before reading a failure here as a broken drain loop.
	const ticks = 8
	for i := 0; i < ticks; i++ {
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}

	// Straight along +Y from the middle of the spawn room, and two commands'
	// worth of it: every room is at least RoomMin across, so two metres cannot
	// reach a wall to be slid along, and the generator puts no pickup in the
	// spawn room, so nothing here can end the run underneath the assertion.
	moved := math.Hypot(a.Owner().State.Pos.X-start.X, a.Owner().State.Pos.Y-start.Y)
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
	a := newTestArena(t, 3)
	start := a.Owner().State.Pos

	// Nothing arrives for half a second of real time, so the budget fills.
	for i := 0; i < SimHz/2; i++ {
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{
		{Seq: 1, Dt: 0.1, MY: 1, Yaw: 0}, {Seq: 2, Dt: 0.1, MY: 1, Yaw: 0},
		{Seq: 3, Dt: 0.1, MY: 1, Yaw: 0}, {Seq: 4, Dt: 0.1, MY: 1, Yaw: 0},
	}})
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))

	moved := math.Hypot(a.Owner().State.Pos.X-start.X, a.Owner().State.Pos.Y-start.Y)
	if moved < 3*SimStep.Seconds()*WalkSpeed {
		t.Fatalf("a banked burst only moved %.3f m; the catch-up budget was not spent", moved)
	}
}

func TestPendingInputIsBounded(t *testing.T) {
	// A client that keeps sending while the tick is stalled must not be able to
	// grow this slice without bound. Oldest goes first, because stale input is
	// the input least worth simulating.
	a := newTestArena(t, 3)
	for i := 0; i < 100; i++ {
		a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{{Seq: int64(i + 1), Dt: dt, MY: 1}}})
	}
	// The bound is the frame cap plus the redundancy window, four frames deep —
	// redundant copies are dropped by sequence before they reach the queue, so
	// what this guards is a client sending genuinely new input faster than the
	// tick drains it.
	if want := 4 * (MaxCommandsPerFrame + RedundantCommands); len(a.Owner().pending) > want {
		t.Fatalf("queue grew to %d commands, bound is %d", len(a.Owner().pending), want)
	}
}

func TestWalkingOverSomethingPicksItUp(t *testing.T) {
	// There is no use button by design, so this IS the interaction.
	a := newTestArena(t, 11)
	p := a.Level.Pickups[0]
	a.Owner().State.Pos = p.Pos
	a.Owner().State.Sector = p.Sector

	a.Advance(SimStep.Seconds(), time.Unix(0, 0))

	if !a.Taken[p.ID] {
		t.Fatal("stood on the beer and did not pick it up")
	}
	if a.Owner().State.Counters["beer"] != 1 {
		t.Fatalf("counter is %d, expected 1", a.Owner().State.Counters["beer"])
	}
}

func TestAPickupIsCollectedExactlyOnce(t *testing.T) {
	a := newTestArena(t, 11)
	p := a.Level.Pickups[0]
	a.Owner().State.Pos, a.Owner().State.Sector = p.Pos, p.Sector
	for i := 0; i < 5; i++ {
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	if got := a.Owner().State.Counters["beer"]; got != 1 {
		t.Fatalf("standing on it for five ticks gave %d beers", got)
	}
}

func TestCollectingEverythingEndsTheRun(t *testing.T) {
	// The objective of this iteration, and the only thing that causes the game's
	// one database write.
	a := newTestArena(t, 11)
	for _, p := range a.Level.Pickups {
		a.Owner().State.Pos, a.Owner().State.Sector = p.Pos, p.Sector
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	if !a.Ended || !a.Success {
		t.Fatalf("collected everything, run is ended=%v success=%v", a.Ended, a.Success)
	}
}

func TestAnEndedRunIgnoresFurtherInput(t *testing.T) {
	a := newTestArena(t, 11)
	for _, p := range a.Level.Pickups {
		a.Owner().State.Pos, a.Owner().State.Sector = p.Pos, p.Sector
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	before := a.Owner().State.Pos
	a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{{Seq: 99, Dt: dt, MY: 1}}})
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	if a.Owner().State.Pos != before {
		t.Fatal("a finished run kept simulating")
	}
}

func TestSnapshotQuantisesAndClearsItsEvents(t *testing.T) {
	// An event is delivered ONCE, on the next frame. A snapshot that re-sent it
	// would replay the same sound forever, which is the failure mode that makes
	// people mute a game.
	a := newTestArena(t, 11)
	p := a.Level.Pickups[0]
	a.Owner().State.Pos, a.Owner().State.Sector = p.Pos, p.Sector
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))

	first := mustSnapshot(t, a)
	if len(first.Events) != 1 || first.Events[0].E != EventPickup {
		t.Fatalf("expected one pickup event, got %+v", first.Events)
	}
	if second := mustSnapshot(t, a); len(second.Events) != 0 {
		t.Fatalf("events repeated on the next frame: %+v", second.Events)
	}

	// Positions are centimetres, never floats: at twenty frames a second a
	// float64 metre is seventeen characters of noise nobody can see.
	if want := cm(a.Owner().State.Pos.X); first.X != want {
		t.Fatalf("x quantised to %d, expected %d", first.X, want)
	}
}

func TestSnapshotListsWhatIsLeft(t *testing.T) {
	a := newTestArena(t, 11)
	if got := len(mustSnapshot(t, a).Left); got != len(a.Level.Pickups) {
		t.Fatalf("fresh level lists %d remaining, has %d", got, len(a.Level.Pickups))
	}
	p := a.Level.Pickups[0]
	a.Owner().State.Pos, a.Owner().State.Sector = p.Pos, p.Sector
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	if got := len(mustSnapshot(t, a).Left); got != len(a.Level.Pickups)-1 {
		t.Fatalf("after one pickup, %d remain of %d", got, len(a.Level.Pickups))
	}
}

func TestElapsedNeverGoesBackwards(t *testing.T) {
	// A clock that moved backwards would produce a negative run time in the only
	// table this game has. Cheap to rule out.
	a := NewArena(uuid.New(), "acct", "pseudo", 1, time.Unix(1000, 0))
	if got := a.Elapsed(time.Unix(900, 0)); got != 0 {
		t.Fatalf("a clock that went backwards gave %d seconds", got)
	}
	if got := a.Elapsed(time.Unix(1042, 0)); got != 42 {
		t.Fatalf("expected 42 seconds, got %d", got)
	}
}

// mustSnapshot renders the arena for its owner, failing the test rather than
// returning a zero value — every caller here has just created the arena, so a
// miss is a broken test rather than a case worth handling.
func mustSnapshot(t *testing.T, a *Arena) Snapshot {
	t.Helper()
	s, ok := a.SnapshotFor(a.AccountID)
	if !ok {
		t.Fatal("no snapshot for the arena's own owner")
	}
	return s
}
