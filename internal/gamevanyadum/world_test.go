package gamevanyadum

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
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
			w.Enqueue(id, &ParsedInput{Cmds: []Command{{Seq: 1, Dt: subStep, MY: 1, Yaw: eastward}}})
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
	// Same rule, cheaper consequence: the peers array is built by walking the
	// occupants, so map order would reshuffle it between two renders of an
	// unchanged world. That makes any golden test over the wire shape flap, and
	// it asks a client to re-key its bookkeeping every frame for no reason at
	// all.
	w := NewWorld(uuid.Nil, 11)
	for _, id := range []string{"account-a", "account-c", "account-b", "account-d"} {
		if _, ok := w.Join(id, "pseudo-"+id, epoch); !ok {
			t.Fatalf("%s was refused", id)
		}
	}
	w.Advance(SimStep.Seconds(), epoch)

	// Account order, which is the world's own order and not the order they
	// joined in.
	want := []string{"pseudo-account-b", "pseudo-account-c", "pseudo-account-d"}
	for i := 0; i < 100; i++ {
		got := make([]string, 0, len(want))
		for _, p := range mustSnapshot(t, w, "account-a").Peers {
			got = append(got, p.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("render %d listed peers as %v, expected %v", i, got, want)
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
func worldDigest(t *testing.T, w *World, ids ...string) string {
	t.Helper()
	type occupantState struct {
		ID       string
		Pos      Vec2
		Sector   int
		Health   int
		LastSeq  int64
		Counters map[string]int
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
