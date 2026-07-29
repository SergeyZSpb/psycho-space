package gamekaren

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

// The office is the only stateful thing in the game, and every one of these
// tests drives it by hand: no service, no socket, no goroutine, no sleep.

var epoch = time.Unix(1700000000, 0)

// snapOf reads an occupant's own snapshot back off the wire, which is the only
// way anything outside this package sees where somebody is — and therefore the
// right way for a test to look.
func snapOf(t *testing.T, o *Office, accountID string) Snapshot {
	t.Helper()
	raw, ok := o.SnapshotFor(accountID)
	if !ok {
		t.Fatalf("no snapshot for %s", accountID)
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// advance runs n ticks of real simulation time from the epoch.
func advance(o *Office, n int) []*Occupant {
	var ended []*Occupant
	for i := 0; i < n; i++ {
		now := epoch.Add(time.Duration(i+1) * SimStep)
		ended = append(ended, o.Advance(SimStep.Seconds(), now)...)
	}
	return ended
}

func TestTheSameAccountCannotStartTwice(t *testing.T) {
	// A refusal rather than a silent replacement: dropping the running shift
	// would throw away one somebody is in the middle of on their other tab.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	if err := o.Join("a", "s2", epoch); !errors.Is(err, ErrShiftInProgress) {
		t.Fatalf("the second shift was allowed: %v", err)
	}
	// And leaving makes room for the next one.
	if _, ok := o.Leave("a"); !ok {
		t.Fatal("leaving found nobody")
	}
	if err := o.Join("a", "s3", epoch); err != nil {
		t.Fatalf("starting after leaving was refused: %v", err)
	}
}

func TestTheFloorIsCapped(t *testing.T) {
	o := NewOffice()
	for i := 0; i < MaxOccupants; i++ {
		if err := o.Join(string(rune('a'+i)), "s", epoch); err != nil {
			t.Fatalf("occupant %d refused: %v", i, err)
		}
	}
	if err := o.Join("z", "s", epoch); !errors.Is(err, ErrOfficeFull) {
		t.Fatalf("the cap did not bite: %v", err)
	}
	if got := o.Occupants(); got != MaxOccupants {
		t.Fatalf("%d people got in", got)
	}
}

func TestEmptyIsTrueOnceEverybodyHasGoneHome(t *testing.T) {
	// It is what the service watches to drop the office entirely, which is what
	// puts the bald man back at the far wall for the next shift.
	o := NewOffice()
	if !o.Empty() {
		t.Fatal("a fresh office is not empty")
	}
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	if err := o.Join("b", "s2", epoch); err != nil {
		t.Fatal(err)
	}
	if o.Empty() {
		t.Fatal("two people in and it is empty")
	}
	o.Leave("a")
	if o.Empty() {
		t.Fatal("one person in and it is empty")
	}
	o.Leave("b")
	if !o.Empty() {
		t.Fatal("nobody in and it is not empty")
	}
}

func TestStandingStillEarnsWithoutSendingAnything(t *testing.T) {
	// THE IDLE FILL, AND THE GAME DOES NOT WORK WITHOUT IT. The client sends
	// nothing while the player stands still — a frame per tick of somebody doing
	// nothing is the mistake «ВАНЯДУМ» shipped once — so the office has to
	// simulate the time no command claimed. Without this, standing perfectly
	// still would pay nothing, which is the one outcome the design cannot have.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	advance(o, SimHz) // one second, no input at all

	s := snapOf(t, o, "a")
	if s.Pay <= 0 {
		t.Fatal("a second of standing perfectly still paid nothing")
	}
	if s.St < 900 || s.St > 1100 {
		t.Fatalf("a second of stillness banked a %d ms streak", s.St)
	}
	// And the ramp is climbing: after a full RampSeconds the multiplier is at
	// the cap, which is the number the whole splash screen is about.
	//
	// Exactly RampSeconds and not a tick more, because the bald man arrives at
	// the spawn about half a second after the ramp completes — standing still
	// from the start earns the ×3 exactly once, and then it is his turn. That
	// margin is the demo the design describes, and it is thin on purpose.
	advance(o, int(RampSeconds*SimHz)-SimHz)
	if got := snapOf(t, o, "a").M; got != hundredths(MaxMultiplier) {
		t.Fatalf("after %v seconds of stillness the multiplier is ×%v", RampSeconds, float64(got)/100)
	}
}

func TestQueuedInputDrainsAtRealTimeRatherThanAllAtOnce(t *testing.T) {
	// Two sub-steps arrive per input frame at 10 Hz and are spent one per tick
	// at 20 Hz, so a client cannot get ahead by batching — it merely queues.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	cmds := make([]Command, 0, 10)
	for i := 1; i <= 10; i++ {
		cmds = append(cmds, Command{Seq: uint32(i), Dt: SimStep.Seconds(), MX: 1})
	}
	o.Enqueue("a", cmds, epoch)

	advance(o, 1)
	if got := snapOf(t, o, "a").Ack; got != 1 {
		t.Fatalf("one tick acknowledged %d commands' worth", got)
	}
	advance(o, 4)
	if got := snapOf(t, o, "a").Ack; got != 5 {
		t.Fatalf("five ticks acknowledged up to %d", got)
	}
}

func TestACommandAlreadyAppliedIsDroppedRatherThanReplayed(t *testing.T) {
	// This is what makes input redundancy free: a client resends the tail of its
	// unacknowledged commands in every frame, and a replayed one would be
	// movement that happens twice on the server and once on the client — which
	// the player feels as being dragged.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	c := Command{Seq: 1, Dt: SimStep.Seconds(), MX: 1}
	o.Enqueue("a", []Command{c}, epoch)
	advance(o, 1)
	moved := snapOf(t, o, "a").X

	// The same command again, five times over, exactly as a lossy client would
	// resend it.
	o.Enqueue("a", []Command{c, c, c, c, c}, epoch)
	advance(o, 5)
	if got := snapOf(t, o, "a").X; got != moved {
		t.Fatalf("a resent command moved him again: %d → %d", moved, got)
	}
	// A zero sequence is "unset" and is dropped for the same reason.
	o.Enqueue("a", []Command{{Dt: SimStep.Seconds(), MX: 1}}, epoch)
	advance(o, 2)
	if got := snapOf(t, o, "a").X; got != moved {
		t.Fatalf("an unsequenced command was applied: %d → %d", moved, got)
	}
}

func TestTheTimeBudgetCapsBankedSimulatedTime(t *testing.T) {
	// THE SPEED HACK. A client that fills every frame with the largest legal dt
	// is asking to run faster than everybody else, with no single field out of
	// range anywhere. The answer is that simulated time is bought at real time:
	// a tick may spend at most TimeBudgetCap seconds of client-claimed movement,
	// however much is queued and however long the tick itself claims to be.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	var cmds []Command
	for i := 1; i <= maxPending; i++ {
		cmds = append(cmds, Command{Seq: uint32(i), Dt: MaxStepSeconds, MX: 1})
	}
	o.Enqueue("a", cmds, epoch)
	start := snapOf(t, o, "a").X

	// One tick, claiming five whole seconds — a stalled loop, a suspended
	// process, or a test being unkind. The queue holds eight seconds of
	// movement; the cap says half a second of it may be spent.
	o.Advance(5.0, epoch.Add(time.Second))

	moved := float64(snapOf(t, o, "a").X-start) / 100
	if limit := TimeBudgetCap*WalkSpeed + 1e-6; moved > limit {
		t.Fatalf("one five-second tick moved him %v m, the cap allows %v m", moved, limit)
	}
	if moved <= 0 {
		t.Fatalf("the queue was not simulated at all: %v m", moved)
	}
}

func TestQuietTimeCannotBeBankedAndSpentOnMovement(t *testing.T) {
	// The other half of the same rule. Standing still is simulated by the office
	// rather than by the client, and that simulation CONSUMES the budget — so a
	// player cannot earn the ramp for ten seconds and then cash those ten
	// seconds in as free movement.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	advance(o, 5*SimHz) // five seconds of standing perfectly still
	start := snapOf(t, o, "a").X

	var cmds []Command
	for i := 1; i <= maxPending; i++ {
		cmds = append(cmds, Command{Seq: uint32(i), Dt: MaxStepSeconds, MX: 1})
	}
	o.Enqueue("a", cmds, epoch)
	advance(o, 1)

	moved := float64(snapOf(t, o, "a").X-start) / 100
	if limit := WalkSpeed*SimStep.Seconds() + 1e-6; moved > limit {
		t.Fatalf("five quiet seconds bought %v m of movement in one tick, a tick is worth %v m", moved, limit)
	}
}

func TestBeingCaughtEndsTheShiftAsAPromotion(t *testing.T) {
	// End to end, with nothing reached into: somebody stands still at the spawn
	// earning money, and the bald man crosses the office to congratulate them.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}

	// Long enough for him to walk the length of the floor and no longer, so a
	// boss who stopped moving fails here rather than hanging.
	ended := advance(o, 400)
	if len(ended) != 1 {
		t.Fatalf("%d shifts ended", len(ended))
	}
	occ := ended[0]
	if occ.Cause != CausePromoted {
		t.Fatalf("the shift ended as %q", occ.Cause)
	}
	if !occ.Ended || occ.State.Alive {
		t.Fatalf("a caught occupant is still working: %+v", occ.State)
	}
	if occ.State.Salary <= 0 {
		t.Fatal("he earned nothing on the way to being promoted")
	}
	if !o.Empty() {
		t.Fatal("the caught occupant is still on the floor")
	}
	if _, ok := o.SnapshotFor("a"); ok {
		t.Fatal("a caught occupant is still being sent snapshots")
	}
}

func TestAnOccupantNobodyHasSeenIsEndedAsHavingWalkedOut(t *testing.T) {
	// Not a disconnect timeout — a reload, a tunnel or a phone locking all take
	// seconds. What this catches is the occupant nobody comes back to, who would
	// otherwise stand in the office earning money until the process restarted.
	//
	// Unlike «ВАНЯДУМ», the shift IS recorded: a забег somebody walked away from
	// is not a result, but a SHIFT somebody walked away from is exactly what this
	// game is about.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}

	// One tick just inside the grace, and one just outside it. The clock is the
	// tick's own, so nothing here waits for ninety seconds.
	if got := o.Advance(SimStep.Seconds(), epoch.Add(AbandonGrace)); len(got) != 0 {
		t.Fatalf("ended at exactly the grace: %+v", got[0])
	}
	ended := o.Advance(SimStep.Seconds(), epoch.Add(AbandonGrace+time.Second))
	if len(ended) != 1 {
		t.Fatalf("%d shifts ended past the grace", len(ended))
	}
	if ended[0].Cause != CauseLeft {
		t.Fatalf("an abandoned shift ended as %q", ended[0].Cause)
	}
	if !o.Empty() {
		t.Fatal("the abandoned occupant is still on the floor")
	}
}

func TestBeingSeenKeepsAShiftAlive(t *testing.T) {
	// The other side of the same rule: a connection is presence, whether or not
	// anything is sent down it. A player standing perfectly still sends nothing
	// at all and is the most present person in the game.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		at := epoch.Add(time.Duration(i) * AbandonGrace)
		o.Seen("a", at)
		if got := o.Advance(SimStep.Seconds(), at); len(got) != 0 {
			t.Fatalf("tick %d ended a shift that was still connected: %q", i, got[0].Cause)
		}
	}
	if o.Empty() {
		t.Fatal("a connected occupant was dropped")
	}
}

func TestASnapshotDescribesTheOccupantItIsAddressedTo(t *testing.T) {
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	if err := o.Join("b", "s2", epoch); err != nil {
		t.Fatal(err)
	}
	// Move one of them and not the other.
	o.Enqueue("a", []Command{{Seq: 1, Dt: SimStep.Seconds(), MX: 1}}, epoch)
	advance(o, 1)

	sa, sb := snapOf(t, o, "a"), snapOf(t, o, "b")
	if sa.X == sb.X {
		t.Fatalf("both snapshots put them in the same place: %d", sa.X)
	}
	if sa.B != sb.B {
		t.Fatalf("the two frames disagree about the bald man: %+v %+v", sa.B, sb.B)
	}
	if sa.Tick != sb.Tick {
		t.Fatalf("the two frames are on different ticks: %d %d", sa.Tick, sb.Tick)
	}
	if _, ok := o.SnapshotFor("nobody"); ok {
		t.Fatal("an account with no shift got a snapshot")
	}
}

func TestTheTickIsTheClientsTimeline(t *testing.T) {
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	advance(o, 7)
	if got := o.Tick(); got != 7 {
		t.Fatalf("seven ticks left the counter at %d", got)
	}
	if got := snapOf(t, o, "a").Tick; got != 7 {
		t.Fatalf("the frame says tick %d", got)
	}
}

func TestTwoOccupantsAreSteppedInADeterministicOrder(t *testing.T) {
	// Nothing in this game iterates a map to produce a result, because map order
	// is randomised in Go and the bald man's choice of victim would otherwise
	// differ between processes. Two people at exactly the same distance is not a
	// hypothetical in a room this size.
	run := func() (Vec2, Vec2) {
		o := NewOffice()
		if err := o.Join("a", "s1", epoch); err != nil {
			t.Fatal(err)
		}
		if err := o.Join("b", "s2", epoch); err != nil {
			t.Fatal(err)
		}
		// Symmetrically apart, so his choice is a pure tie.
		o.Enqueue("a", []Command{{Seq: 1, Dt: MaxStepSeconds, MX: -1}}, epoch)
		o.Enqueue("b", []Command{{Seq: 1, Dt: MaxStepSeconds, MX: 1}}, epoch)
		advance(o, 100)
		sa, sb := snapOf(t, o, "a"), snapOf(t, o, "b")
		return Vec2{X: float64(sa.B.X), Y: float64(sa.B.Y)}, Vec2{X: float64(sb.X), Y: float64(sb.Y)}
	}
	first, firstB := run()
	for i := 0; i < 20; i++ {
		got, gotB := run()
		if got != first || gotB != firstB {
			t.Fatalf("run %d diverged: boss %+v vs %+v, occupant %+v vs %+v", i, got, first, gotB, firstB)
		}
	}
}

func TestPendingInputIsBounded(t *testing.T) {
	// A client that floods must not make the office hold an unbounded slice.
	// What bounds how much of it is SIMULATED is the time budget; this bounds
	// the memory.
	o := NewOffice()
	if err := o.Join("a", "s1", epoch); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 100; frame++ {
		cmds := make([]Command, 0, MaxInboundCommands)
		for i := 0; i < MaxInboundCommands; i++ {
			cmds = append(cmds, Command{Seq: uint32(frame*MaxInboundCommands + i + 1), Dt: MaxStepSeconds, MX: 1})
		}
		o.Enqueue("a", cmds, epoch)
	}
	// The newest maxPending commands survive; everything older was dropped
	// before it could be simulated, so the position is bounded by the budget and
	// not by how much was sent.
	advance(o, 1)
	x := float64(snapOf(t, o, "a").X) / 100
	if limit := PlayerSpawnX + TimeBudgetCap*WalkSpeed + 1e-6; x > limit {
		t.Fatalf("a thousand queued commands moved him to %v, the cap allows %v", x, limit)
	}
	if math.Abs(x-PlayerSpawnX) < 1e-9 {
		t.Fatal("none of the queue was simulated at all")
	}
}

func TestEnqueueIgnoresAnAccountThatIsNotWorking(t *testing.T) {
	o := NewOffice()
	o.Enqueue("nobody", []Command{{Seq: 1, Dt: 0.05, MX: 1}}, epoch)
	if !o.Empty() {
		t.Fatal("sending input started a shift")
	}
}

// TestDriftDoesNotEatTheDash pins the guard on the idle fill.
//
// The fill exists because a still client sends nothing at all, so the server has
// to advance the shift itself or standing still would earn nothing. But a client
// that IS sending under-fills the tick by a millisecond or two of ordinary
// browser clock drift, and filling THAT gap with a still step is not free: Step
// reads DashLeft, so a still step during a dash decrements the dash timer at
// dash speed while moving the player nowhere. The burst quietly lost distance to
// drift on most ticks, and the client — which predicted all of it — was
// corrected at the end of every one.
//
// The timer is what the test reads, because it is what actually differs. With
// the guard the dash is spent only by time the player claimed; without it every
// tick also charges the unclaimed remainder.
func TestDriftDoesNotEatTheDash(t *testing.T) {
	now := time.Unix(1700000000, 0)
	o := NewOffice()
	if err := o.Join("a", "shift-a", now); err != nil {
		t.Fatalf("join: %v", err)
	}

	// 40 ms claimed out of every 50 ms tick — a browser timer does not tile a
	// tick evenly, and this is an ordinary amount of drift rather than a bad one.
	const claimed = 0.04
	o.Enqueue("a", []Command{{Seq: 1, Dt: claimed, MX: 0, MY: -1, Dash: true}}, now)
	o.Advance(SimStep.Seconds(), now)

	seq := uint32(2)
	for i := 0; i < 3; i++ {
		o.Enqueue("a", []Command{{Seq: seq, Dt: claimed, MX: 0, MY: -1}}, now)
		seq++
		now = now.Add(SimStep)
		o.Advance(SimStep.Seconds(), now)
	}

	// Four claimed sub-steps have been simulated, so exactly that much of the
	// dash is gone. Charging the unclaimed 10 ms of each tick as well would leave
	// 0.02 here instead.
	want := DashSeconds - 4*claimed
	if got := o.dashLeftOf(t, "a"); math.Abs(got-want) > 1e-9 {
		t.Fatalf("dash has %.4f s left, want %.4f s — drift is being charged to the dash", got, want)
	}
}

// TestStandingPerfectlyStillStillEarns is the other half: the guard must not
// break the case the fill exists for. A client that sends nothing at all is a
// player standing still, and that is the whole game.
func TestStandingPerfectlyStillStillEarns(t *testing.T) {
	now := time.Unix(1700000000, 0)
	o := NewOffice()
	if err := o.Join("a", "shift-a", now); err != nil {
		t.Fatalf("join: %v", err)
	}
	for i := 0; i < 20; i++ {
		now = now.Add(SimStep)
		o.Advance(SimStep.Seconds(), now)
	}
	if got := o.salaryOf(t, "a"); got <= 0 {
		t.Fatalf("a player who stood perfectly still earned %.2f — the fill is not running", got)
	}
}

// dashLeftOf and salaryOf read one occupant's simulated state. Test-only, and
// deliberately here rather than as methods on Office: nothing in production has
// any reason to reach inside an occupant.
func (o *Office) dashLeftOf(t *testing.T, accountID string) float64 {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok {
		t.Fatalf("no occupant %q", accountID)
	}
	return occ.State.DashLeft
}

func (o *Office) salaryOf(t *testing.T, accountID string) float64 {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok {
		t.Fatalf("no occupant %q", accountID)
	}
	return occ.State.Salary
}
