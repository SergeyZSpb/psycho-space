package gamevanyadum

import (
	"encoding/json"
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
	acc := uuid.New().String()
	if _, ok := w.Join(acc, "pseudo-"+acc[:8], epoch); !ok {
		t.Fatal("a fresh заброшка refused its first occupant")
	}
	return w, acc
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
	w, acc := newTestWorld(t, 11)
	full := uint32(1)<<len(w.Level.Pickups) - 1
	if got := mustSnapshot(t, w, acc).Left; got != full {
		t.Fatalf("a fresh level published %b, expected %b", got, full)
	}

	// Collecting one clears exactly its own bit and nothing else.
	const idx = 0
	standOn(w, acc, idx)
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
	w := NewWorld(uuid.Nil, 11)
	for _, id := range []string{"account-a", "account-c", "account-b", "account-d"} {
		if _, ok := w.Join(id, "pseudo-"+id, epoch); !ok {
			t.Fatalf("%s was refused", id)
		}
	}
	w.Advance(SimStep.Seconds(), epoch)

	// The order they walked in, and not the order their accounts sort in: c took
	// the second place because he arrived second.
	want := []int{1, 2, 3}
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

	// Somebody in the reader's own room.
	roommate, _ := w.Join("account-roommate", "pseudo-roommate", epoch)
	roommate.State.Sector = w.Level.SpawnSector
	// Somebody through a doorway, which is what a neighbouring sector means.
	neighbour, _ := w.Join("account-neighbour", "pseudo-neighbour", epoch)
	neighbour.State.Sector = near
	// And somebody two rooms away.
	stranger, _ := w.Join("account-stranger", "pseudo-stranger", epoch)
	stranger.State.Sector = far

	sent := map[int]bool{}
	for _, p := range mustSnapshot(t, w, "account-me").Peers {
		sent[p.Slot] = true
	}
	if !sent[roommate.Slot] {
		t.Fatal("somebody standing in the same room was filtered out")
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
	for _, p := range mustSnapshot(t, w, "account-me").Peers {
		if p.Slot == stranger.Slot {
			return
		}
	}
	t.Fatal("he stepped into the next room and was still not sent")
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
	}
	out := struct {
		Tick  int64
		Ready []int64
		Occs  []occupantState
	}{Tick: w.Tick, Ready: w.ready}
	for _, id := range ids {
		o := w.Occupant(id)
		if o == nil {
			t.Fatalf("no occupant %q to digest", id)
		}
		out.Occs = append(out.Occs, occupantState{
			ID: id, Pos: o.State.Pos, Sector: o.State.Sector,
			Health: o.State.Health, LastSeq: o.State.LastSeq, Counters: o.State.Counters,
			Loaded: o.State.Loaded, Cooldown: o.State.CooldownLeft, Reload: o.State.ReloadLeft,
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
		standAtSpawn(w, acc)
		watchers = append(watchers, acc)
	}
	return w, shooter, watchers
}

// firedPeer reads one peer's muzzle flash out of the frame a viewer is actually
// sent, rather than off the occupant — what is being checked is what crosses the
// wire, and a field with the wrong json tag would satisfy any assertion made
// against the struct.
func firedPeer(t *testing.T, w *World, viewer string, slot int) bool {
	t.Helper()
	for _, p := range mustSnapshot(t, w, viewer).Peers {
		if p.Slot == slot {
			return p.Fired
		}
	}
	t.Fatalf("slot %d is not on the frame sent to %s at all", slot, viewer)
	return false
}

func TestAPeersShotIsOnTheFrameForOneTickOnly(t *testing.T) {
	// An action nobody else can see is an unfinished action (CLAUDE.md), and a
	// обрез going off is this game's loudest one — so a shot has to reach the
	// people it was aimed at and not only the man who pulled the trigger. He
	// needs no telling: his own barrel count is on his own snapshot, and the
	// count falling by one IS the shot. A peer carries no barrel count, so this
	// is the field that says so.
	//
	// AND THE WHOLE BUDGET FOR IT RESTS ON IT BEING PER-ACTION. It is priced at
	// the gun's cadence — three a second — rather than at the twenty a second
	// every other peer field costs (message.go, Peer.Fired; message_test.go,
	// firedPerSecond). A flag that stayed up for the whole cooldown, which is the
	// natural way to write it and the shape a duration would have had, rides
	// every tick instead of three: 9 bytes × 20 Hz × 4 peers = 720 B/s, where the
	// frame had 387 B/s of headroom before this field existed at all. So this
	// test is not about a flash looking right, it is what holds the ceiling
	// arithmetic true.
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
	w, shooter, watchers := watching(t, 3)
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
