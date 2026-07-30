package gamefintech

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
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
		t.Fatalf("no snapshot for %s — the occupant is gone, which almost always means "+
			"the bald man reached them. He crosses from his spawn in about %.1fs, so a test "+
			"that is not ABOUT the chase must finish well inside that.", accountID, (BossSpawnY-PlayerSpawnY-CatchRadius-PlayerRadius)/BossSpeed)
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// join puts an account to work ON THE CANONICAL SPAWN.
//
// Production DRAWS a spawn now (Office.spawnPoint), which is right for the game
// and wrong for almost every test in this file: they are about the idle fill, the
// time budget, the ramp or the wire, and every one of them reasons from how long
// the bald man takes to arrive — a number that is only knowable from a known
// starting point. So the tests that are not ABOUT spawning pin it, and the ones
// that are (TestASpawnIsDrawnSomewhereLegal and friends) call Join directly.
func join(t *testing.T, o *Office, accountID, shiftID string) {
	t.Helper()
	if err := o.Join(accountID, shiftID, "p-"+accountID, "", 0, epoch); err != nil {
		t.Fatal(err)
	}
	place(t, o, accountID, Vec2{X: PlayerSpawnX, Y: PlayerSpawnY})
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
	join(t, o, "a", "s1")
	if err := o.Join("a", "s2", "p-a", "", 0, epoch); !errors.Is(err, ErrShiftInProgress) {
		t.Fatalf("the second shift was allowed: %v", err)
	}
	// And leaving makes room for the next one.
	if _, ok := o.Leave("a"); !ok {
		t.Fatal("leaving found nobody")
	}
	if err := o.Join("a", "s3", "p-a", "", 0, epoch); err != nil {
		t.Fatalf("starting after leaving was refused: %v", err)
	}
}

func TestTheFloorIsCapped(t *testing.T) {
	o := NewOffice()
	for i := 0; i < MaxOccupants; i++ {
		if err := o.Join(string(rune('a'+i)), "s", "p-"+string(rune('a'+i)), "", 0, epoch); err != nil {
			t.Fatalf("occupant %d refused: %v", i, err)
		}
	}
	if err := o.Join("z", "s", "p-z", "", 0, epoch); !errors.Is(err, ErrOfficeFull) {
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
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
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
	join(t, o, "a", "s1")
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
	join(t, o, "a", "s1")
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
	join(t, o, "a", "s1")
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
	join(t, o, "a", "s1")
	var cmds []Command
	for i := 1; i <= maxPending; i++ {
		cmds = append(cmds, Command{Seq: uint32(i), Dt: MaxStepSeconds, MX: 1})
	}
	o.Enqueue("a", cmds, epoch)
	start := snapOf(t, o, "a").X

	// One tick claiming a whole second — a stalled loop, a suspended process, or
	// a test being unkind. The queue holds eight seconds of movement; the cap
	// says half a second of it may be spent.
	//
	// A second rather than five, and the reason is worth knowing: the BOSS is not
	// budget-capped, because in production Advance is only ever called with
	// SimStep. Hand it five seconds and he crosses the whole office in one step
	// and ends the shift, and this test — which is about the player's cap — dies
	// of something else entirely.
	o.Advance(1.0, epoch.Add(time.Second))

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
	join(t, o, "a", "s1")
	// Two seconds of standing perfectly still. NOT five: the bald man reaches the
	// spawn in about 3.8 s, and this test is about the budget rather than about
	// him — see snapOf.
	advance(o, 2*SimHz)
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
	join(t, o, "a", "s1")

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
	join(t, o, "a", "s1")

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
	join(t, o, "a", "s1")
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
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
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
	join(t, o, "a", "s1")
	advance(o, 7)
	if got := o.Tick(); got != 7 {
		t.Fatalf("seven ticks left the counter at %d", got)
	}
	if got := snapOf(t, o, "a").Tick; got != 7 {
		t.Fatalf("the frame says tick %d", got)
	}
}

// place stands an occupant on a known point.
//
// Production DRAWS a spawn (Office.spawnPoint), which is right for the game and
// useless to a test that has to know where somebody started. This writes the
// position directly — white-box setup, the same answer the repository gives for
// determinism everywhere else, and specifically NOT a production flag that turns
// the randomness off: that would be test-only machinery in a live path.
func place(t *testing.T, o *Office, accountID string, at Vec2) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok {
		t.Fatalf("cannot place %s — the occupant is gone, which almost always means "+
			"the bald man reached them mid-test", accountID)
	}
	occ.State.Pos = at
}

func TestTwoOccupantsAreSteppedInADeterministicOrder(t *testing.T) {
	// Nothing in this game iterates a map to produce a result, because map order
	// is randomised in Go and the bald man's choice of victim would otherwise
	// differ between processes. Two people at exactly the same distance is not a
	// hypothetical in a room this size.
	run := func() (Vec2, Vec2) {
		o := NewOffice()
		join(t, o, "a", "s1")
		join(t, o, "b", "s2")
		// Both start on the same known point (join pins it), so the split below
		// is symmetric and his choice is a pure tie — which is the whole claim.
		o.Enqueue("a", []Command{{Seq: 1, Dt: MaxStepSeconds, MX: -1}}, epoch)
		o.Enqueue("b", []Command{{Seq: 1, Dt: MaxStepSeconds, MX: 1}}, epoch)
		// Comfortably inside the chase: determinism shows up in the first second
		// and the shift ending mid-run would prove nothing — see snapOf.
		advance(o, 40)
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
	join(t, o, "a", "s1")
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
	if err := o.Join("a", "shift-a", "p-a", "", 0, now); err != nil {
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
	if err := o.Join("a", "shift-a", "p-a", "", 0, now); err != nil {
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

// --- the drawn spawn -------------------------------------------------------
//
// These are the ones that are ABOUT spawning, so they call Join directly rather
// than the pinning helper above. None of them asserts a POSITION: the draw is
// random by design and no production flag turns it off (that would be test-only
// machinery in a live path), so what is pinned is the INVARIANTS every draw has
// to satisfy, over enough draws that a rule which held by luck would show.

// spawnsOf draws n spawns into an otherwise empty office.
func spawnsOf(t *testing.T, n int) []Vec2 {
	t.Helper()
	out := make([]Vec2, 0, n)
	for i := 0; i < n; i++ {
		o := NewOffice()
		if err := o.Join("a", "s", "p-a", "", 0, epoch); err != nil {
			t.Fatal(err)
		}
		out = append(out, posOf(t, o, "a"))
	}
	return out
}

// posOf reads an occupant's simulated position, unrounded.
//
// Unrounded on purpose: the wire quantises to centimetres, which is nothing to a
// figure on a phone and everything to "is this point outside that rectangle", so
// the GEOMETRIC claims below read the simulation and the rest read the frame.
func posOf(t *testing.T, o *Office, accountID string) Vec2 {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok {
		t.Fatalf("no occupant %s", accountID)
	}
	return occ.State.Pos
}

func TestEverySpawnIsSomewhereYouCouldStand(t *testing.T) {
	for i, at := range spawnsOf(t, 300) {
		if at.X < PlayerRadius || at.X > OfficeW-PlayerRadius ||
			at.Y < PlayerRadius || at.Y > OfficeH-PlayerRadius {
			t.Fatalf("draw %d spawned outside the floor at %+v", i, at)
		}
		for d, desk := range Desks {
			if insideDesk(desk, at, PlayerRadius) {
				t.Fatalf("draw %d spawned inside desk %d at %+v", i, d, at)
			}
		}
	}
}

func TestEverySpawnIsSomewhereHeCanActuallyGetTo(t *testing.T) {
	// THE CLAIM CHANGED WITH THE PURSUIT. It used to be "every spawn has a clear
	// straight line to him", because he could not walk round a desk and a shift
	// opening in one measured up to ninety seconds. He can now, so the claim is
	// the one that actually matters: wherever the draw puts you, he arrives.
	//
	// Simulated rather than asserted geometrically, against a player standing
	// perfectly still — which is how this game is played and therefore the case
	// that matters. Bounded generously: the measured worst over the whole floor
	// is 10.35 s, and this is here to catch "never", not to pin a number.
	const patience = 30 * SimHz
	for i, at := range spawnsOf(t, 40) {
		o := NewOffice()
		join(t, o, "a", "s1")
		place(t, o, "a", at)
		caught := false
		for tick := 0; tick < patience && !caught; tick++ {
			place(t, o, "a", at)
			advance(o, 1)
			o.mu.Lock()
			_, still := o.occupants["a"]
			o.mu.Unlock()
			caught = !still
		}
		if !caught {
			t.Fatalf("draw %d spawned at %+v, where he never arrived in %v seconds",
				i, at, patience/SimHz)
		}
	}
}

func TestASpawnIsNotTheSamePlaceEveryTime(t *testing.T) {
	// The point of the change. A fixed spawn put two occupants inside each other
	// and made death-and-rejoin a free teleport to the safe end of the room.
	//
	// Twenty distinct points out of 300 draws is a deliberately loose bar: it is
	// here to catch the draw being switched off or collapsing to the fallback,
	// not to assert a distribution.
	seen := map[Vec2]bool{}
	for _, at := range spawnsOf(t, 300) {
		seen[at] = true
	}
	if len(seen) < 20 {
		t.Fatalf("300 draws produced only %d distinct spawns", len(seen))
	}
}

func TestNobodySpawnsOnTopOfSomebodyAlreadyWorking(t *testing.T) {
	// The first of the two faults a fixed spawn had: join while somebody is
	// playing and you used to materialise INSIDE them.
	for i := 0; i < 200; i++ {
		o := NewOffice()
		if err := o.Join("a", "s1", "p-a", "", 0, epoch); err != nil {
			t.Fatal(err)
		}
		if err := o.Join("b", "s2", "p-b", "", 0, epoch); err != nil {
			t.Fatal(err)
		}
		a, b := posOf(t, o, "a"), posOf(t, o, "b")
		if gap := math.Hypot(a.X-b.X, a.Y-b.Y); gap < spawnFromEachOther-0.02 {
			t.Fatalf("run %d put two people %.2f m apart: %+v %+v", i, gap, a, b)
		}
	}
}

func TestASpawnKeepsItsDistanceFromHimWhereItCan(t *testing.T) {
	// A FLOOR, NOT A DISTRIBUTION, and the difference is what CI caught.
	//
	// While spawnFromBoss was a preference the sampler kept the best of its
	// draws, and the worst seen over 3000 was 8.02 m — a 1.7 s head start, which
	// is SHORTER THAN MinShiftSeconds. So an unlucky shift could be over before
	// it was long enough to be written down, and the integration test that walks
	// out after MinShiftSeconds raced him and lost.
	//
	// It is a hard filter now, so in an empty office — where he is at his own
	// spawn and most of the floor qualifies — every draw clears it. The fallback
	// exists for the case this test cannot make: him standing mid-room, with
	// nothing 12 m from anywhere. See TestACrowdedFloorStillProducesALegalSpawn.
	for i, at := range spawnsOf(t, 400) {
		gap := math.Hypot(at.X-BossSpawnX, at.Y-BossSpawnY)
		if gap < spawnFromBoss {
			t.Fatalf("draw %d spawned %.2f m from him, inside the %v m floor", i, gap, spawnFromBoss)
		}
		// And the floor is worth what it claims: long enough to be a shift.
		if head := (gap - CatchRadius - PlayerRadius) / BossSpeed; head < MinShiftSeconds {
			t.Fatalf("draw %d gives a %.2f s head start, shorter than MinShiftSeconds (%v)",
				i, head, MinShiftSeconds)
		}
	}
}

func TestACrowdedFloorStillProducesALegalSpawn(t *testing.T) {
	// The fallback path, reached by making every draw fail: everybody alive is
	// standing in the one lane, so nothing is spawnFromEachOther clear of them.
	// It must still answer with a point on the floor and out of the furniture —
	// overlapping is untidy, off the floor is broken.
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	// Fill the room: pin the two of them either side of the canonical spawn so
	// the sampler is fighting for room.
	place(t, o, "a", Vec2{X: PlayerSpawnX, Y: PlayerSpawnY})
	place(t, o, "b", Vec2{X: PlayerSpawnX + 0.1, Y: PlayerSpawnY})
	if err := o.Join("c", "s3", "p-c", "", 0, epoch); err != nil {
		t.Fatal(err)
	}
	at := posOf(t, o, "c")
	if at.X < PlayerRadius || at.X > OfficeW-PlayerRadius ||
		at.Y < PlayerRadius || at.Y > OfficeH-PlayerRadius {
		t.Fatalf("the crowded fallback stood somebody off the floor at %+v", at)
	}
	for d, desk := range Desks {
		if insideDesk(desk, at, PlayerRadius) {
			t.Fatalf("the crowded fallback stood somebody inside desk %d at %+v", d, at)
		}
	}
}

// --- peers on the frame ----------------------------------------------------

func TestYourFrameCarriesTheOtherPeopleInTheOffice(t *testing.T) {
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	// Somewhere else, so the two of them are distinguishable on the wire.
	place(t, o, "b", Vec2{X: PlayerSpawnX + 3, Y: PlayerSpawnY + 1})

	sa := snapOf(t, o, "a")
	if len(sa.Pr) != 1 {
		t.Fatalf("a's frame carries %d peers: %+v", len(sa.Pr), sa.Pr)
	}
	if sa.Pr[0].X != cm(PlayerSpawnX+3) || sa.Pr[0].Y != cm(PlayerSpawnY+1) {
		t.Fatalf("the peer is drawn at %+v, b is at %v,%v", sa.Pr[0], PlayerSpawnX+3, PlayerSpawnY+1)
	}
	// And it is symmetric: b sees a, in a's place.
	sb := snapOf(t, o, "b")
	if len(sb.Pr) != 1 || sb.Pr[0].X != cm(PlayerSpawnX) {
		t.Fatalf("b's frame does not carry a: %+v", sb.Pr)
	}
}

func TestYouAreNeverYourOwnPeer(t *testing.T) {
	// The frame already says where YOU are at the top level, and the client
	// predicts that position rather than interpolating it. A self-entry in the
	// array would draw a second, laggier copy of you standing on yourself.
	o := NewOffice()
	join(t, o, "a", "s1")
	if s := snapOf(t, o, "a"); len(s.Pr) != 0 {
		t.Fatalf("a solo occupant sees %d peers: %+v", len(s.Pr), s.Pr)
	}
}

func TestAPeerIsAPseudonymAndNeverAnAccountID(t *testing.T) {
	// ADR-037. An account id is a durable identifier for a person, and it must
	// not reach somebody else's browser — the handle is minted per process from
	// a key held only in memory.
	svc := NewService(nil, Room, nil, nil, nil)
	account := "0195f0c2-1111-2222-3333-444455556666"
	handle := svc.pseudonym(account)
	if handle == account || strings.Contains(handle, account) {
		t.Fatalf("the wire handle is the account id: %q", handle)
	}
	if handle != svc.pseudonym(account) {
		t.Fatal("the same account got two different handles from one process")
	}
	if svc.pseudonym("someone-else") == handle {
		t.Fatal("two accounts share a handle")
	}
	// A second process means a second key, so the handle is meaningless once
	// this office is gone — which is the property that makes it not an identity.
	if NewService(nil, Room, nil, nil, nil).pseudonym(account) == handle {
		t.Fatal("the handle survived a restart, so it is a durable identifier")
	}
}

func TestTheFrameCarriesTheHandleTheServiceMinted(t *testing.T) {
	o := NewOffice()
	if err := o.Join("a", "s1", "handle-a", "", 0, epoch); err != nil {
		t.Fatal(err)
	}
	if err := o.Join("b", "s2", "handle-b", "", 0, epoch); err != nil {
		t.Fatal(err)
	}
	s := snapOf(t, o, "a")
	if len(s.Pr) != 1 || s.Pr[0].I != "handle-b" {
		t.Fatalf("a's peer is %+v, expected handle-b", s.Pr)
	}
	if s.Pr[0].I == "b" {
		t.Fatal("the account id reached the wire")
	}
}

func TestADeadColleagueIsNotDrawn(t *testing.T) {
	// The tick that catches somebody deletes them, so this is reachable only in
	// the instant between the two — but drawing a figure the simulation has
	// stopped stepping is worse than drawing nothing.
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	o.mu.Lock()
	o.occupants["b"].State.Alive = false
	o.mu.Unlock()
	if s := snapOf(t, o, "a"); len(s.Pr) != 0 {
		t.Fatalf("a promoted colleague is still on the plane: %+v", s.Pr)
	}
}

func TestTheOrderOfPeersDoesNotWander(t *testing.T) {
	// A slice's order is part of its value, so a randomised one is a diff on the
	// wire and a re-render on the client ten times a second for nothing. Map
	// iteration is what would cause it, which is why peersFor walks keys().
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	join(t, o, "c", "s3")
	first := snapOf(t, o, "a").Pr
	for i := 0; i < 30; i++ {
		got := snapOf(t, o, "a").Pr
		if len(got) != len(first) {
			t.Fatalf("read %d saw %d peers, first saw %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].I != first[j].I {
				t.Fatalf("read %d reordered the peers: %+v vs %+v", i, got, first)
			}
		}
	}
}

// --- the face on a colleague ------------------------------------------------

func TestAColleaguesFaceIsFetchedByHandleAndNeverSentOnAFrame(t *testing.T) {
	// ADR-037's whole point. A ~250-character URL beside each peer would repeat
	// ten times a second per viewer to say something that cannot change during a
	// shift, so the frame carries the handle and the picture is one cached GET.
	o := NewOffice()
	if err := o.Join("a", "s1", "handle-a", "https://cdn.example/a.jpg", 0, epoch); err != nil {
		t.Fatal(err)
	}
	if err := o.Join("b", "s2", "handle-b", "https://cdn.example/b.jpg", 0, epoch); err != nil {
		t.Fatal(err)
	}

	raw, ok := o.SnapshotFor("a")
	if !ok {
		t.Fatal("no snapshot")
	}
	if strings.Contains(string(raw), "cdn.example") || strings.Contains(string(raw), "http") {
		t.Fatalf("an avatar URL rode a snapshot: %s", raw)
	}

	// And it is reachable by the handle that frame DID carry.
	got, ok := o.AvatarFor("handle-b")
	if !ok || got != "https://cdn.example/b.jpg" {
		t.Fatalf("AvatarFor(handle-b) = %q, %v", got, ok)
	}
	if _, ok := o.AvatarFor("nobody"); ok {
		t.Fatal("an unknown handle resolved to a face")
	}
}

func TestAnAccountWithNoPictureIsSimplyAPlainFigure(t *testing.T) {
	// Not an error and not a placeholder URL: the plane draws the figure it was
	// already drawing, which is what the office looked like before avatars.
	o := NewOffice()
	if err := o.Join("a", "s1", "handle-a", "", 0, epoch); err != nil {
		t.Fatal(err)
	}
	if _, ok := o.AvatarFor("handle-a"); ok {
		t.Fatal("an account with no avatar resolved to one")
	}
}

func TestAFaceGoesWhenItsOwnerDoes(t *testing.T) {
	// The handle is per-process and the office is the only thing that holds the
	// mapping, so walking out has to take the picture with it — otherwise a
	// handle that came back would serve a face nobody in the office has.
	o := NewOffice()
	if err := o.Join("a", "s1", "handle-a", "https://cdn.example/a.jpg", 0, epoch); err != nil {
		t.Fatal(err)
	}
	if _, ok := o.AvatarFor("handle-a"); !ok {
		t.Fatal("a working occupant has no face")
	}
	if _, ok := o.Leave("a"); !ok {
		t.Fatal("leave failed")
	}
	if _, ok := o.AvatarFor("handle-a"); ok {
		t.Fatal("a face outlived the shift it belonged to")
	}
}

// --- «ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО» -----------------------------------------

// redirected reports who the bald man is currently walking at, by seeing which
// occupant he closes on over a few ticks. Read off behaviour rather than off the
// field, because the field is the mechanism and the behaviour is the claim.
func closesOn(t *testing.T, o *Office, a, b string) string {
	t.Helper()
	da0 := math.Hypot(posOf(t, o, a).X-bossOf(o).X, posOf(t, o, a).Y-bossOf(o).Y)
	db0 := math.Hypot(posOf(t, o, b).X-bossOf(o).X, posOf(t, o, b).Y-bossOf(o).Y)
	advance(o, 10)
	da1 := math.Hypot(posOf(t, o, a).X-bossOf(o).X, posOf(t, o, a).Y-bossOf(o).Y)
	db1 := math.Hypot(posOf(t, o, b).X-bossOf(o).X, posOf(t, o, b).Y-bossOf(o).Y)
	if da0-da1 > db0-db1 {
		return a
	}
	return b
}

func bossOf(o *Office) Vec2 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.boss.Pos
}

func TestPointingHimAtSomebodyElseOverridesWhoIsNearest(t *testing.T) {
	// The whole verb, and the whole of co-op's betrayal half: he walks at the
	// NEAREST person, and this says otherwise for a few seconds.
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	// `a` is much closer to him, so without the verb he is `a`'s problem.
	place(t, o, "a", Vec2{X: BossSpawnX, Y: BossSpawnY - GrinRange - 1})
	place(t, o, "b", Vec2{X: 2, Y: 2})
	if got := closesOn(t, o, "a", "b"); got != "a" {
		t.Fatalf("without the verb he closed on %s, not the nearer one", got)
	}

	if !o.Redirect("a", "p-b") {
		t.Fatal("the verb was refused")
	}
	if got := closesOn(t, o, "a", "b"); got != "b" {
		t.Fatalf("after the redirect he is still closing on %s", got)
	}
}

func TestTheRedirectWearsOffAndHeComesBack(t *testing.T) {
	// A reprieve rather than an answer — and he is nearer than he was.
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	// `a` parks out of the way; `b` RUNS, and has to.
	//
	// Both used to be pinned, which was safe only because the bald man could not
	// get round the furniture. Now that he can, standing still in front of a man
	// who has been pointed at you for six seconds is exactly as fatal as it
	// sounds — and a caught `b` would end the redirect for the wrong reason,
	// proving nothing about the timer. So he keeps to whichever corner is
	// furthest from wherever the chase has got to.
	aAt := Vec2{X: OfficeW - 1.2, Y: 1.2}
	place(t, o, "a", aAt)
	place(t, o, "b", Vec2{X: 1.2, Y: 1.2})
	if !o.Redirect("a", "p-b") {
		t.Fatal("the verb was refused")
	}
	flee := func() {
		him := bossOf(o)
		far, best := Vec2{}, -1.0
		for _, c := range []Vec2{{X: 1.2, Y: 1.2}, {X: OfficeW - 1.2, Y: OfficeH - 1.2}, {X: 1.2, Y: OfficeH - 1.2}} {
			if d := math.Hypot(c.X-him.X, c.Y-him.Y); d > best {
				far, best = c, d
			}
		}
		place(t, o, "b", far)
	}
	for i := 0; i < int(RedirectSeconds*SimHz)+2; i++ {
		place(t, o, "a", aAt)
		flee()
		advance(o, 1)
	}

	// The window has closed, so he is back on the NEAREST — and the chase has
	// left him somewhere this test did not choose, so both of them are placed
	// relative to where he actually is before the question is asked. `a` a few
	// metres off him, `b` across the room: anything less deliberate and the
	// answer is about wherever the flee loop happened to end.
	him := bossOf(o)
	near := clampToFloor(Vec2{X: him.X, Y: him.Y - GrinRange}, PlayerRadius)
	far := Vec2{X: OfficeW - him.X, Y: OfficeH - him.Y}
	place(t, o, "a", near)
	place(t, o, "b", clampToFloor(far, PlayerRadius))
	if got := closesOn(t, o, "a", "b"); got != "a" {
		t.Fatalf("the redirect never wore off — he is still on %s", got)
	}
}

func TestTheRedirectIsRefusedWhenItWouldBeFree(t *testing.T) {
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")

	if o.Redirect("a", "p-a") {
		t.Fatal("somebody pointed the bald man at himself")
	}
	if o.Redirect("a", "nobody") {
		t.Fatal("a handle nobody has resolved to a target")
	}
	if o.Redirect("not-working", "p-b") {
		t.Fatal("somebody who is not on a shift used a verb")
	}
	if !o.Redirect("a", "p-b") {
		t.Fatal("the first, legitimate use was refused")
	}
	// AND IT COSTS SOMETHING. The cooldown is the whole price today — the
	// design's +ПОДОЗРЕНИЕ arrives with Claude — so without this the verb is
	// free and there is no reason not to hold the button down.
	if o.Redirect("a", "p-b") {
		t.Fatal("it fired twice with no cooldown")
	}
}

func TestTheCallerSaysWhatHeDidAndTheFrameCarriesTheCooldown(t *testing.T) {
	// A colleague has to be able to see who did it to him, so the announcement
	// outranks the ordinary two-second rotation for a few seconds.
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	if !o.Redirect("a", "p-b") {
		t.Fatal("the verb was refused")
	}

	sa := snapOf(t, o, "a")
	if sa.P != RedirectLine {
		t.Fatalf("the caller says line %d, want the redirect line %d", sa.P, RedirectLine)
	}
	if PlayerLines[sa.P] != "ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО" {
		t.Fatalf("the redirect line is %q", PlayerLines[sa.P])
	}
	if sa.Rc <= 0 {
		t.Fatal("the frame does not carry the cooldown, so no client can disable the button")
	}
	// And his colleague SEES it — the peer entry carries the same index.
	sb := snapOf(t, o, "b")
	if len(sb.Pr) != 1 || sb.Pr[0].P != RedirectLine {
		t.Fatalf("the colleague cannot see who did it: %+v", sb.Pr)
	}
	// It is an ANNOUNCEMENT, not a permanent state: it wears off.
	for i := 0; i < int(RedirectSaySeconds*SimHz)+2; i++ {
		advance(o, 1)
	}
	if got := snapOf(t, o, "a").P; got == RedirectLine {
		t.Fatal("the caller is still announcing it a shift later")
	}
}

func TestARedirectedColleagueWhoLeavesGivesHimBack(t *testing.T) {
	// He must not keep walking at somebody who is no longer in the office —
	// StepBoss with a target list of one that has gone would leave him homing on
	// a corpse's last position.
	o := NewOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	place(t, o, "a", Vec2{X: BossSpawnX, Y: BossSpawnY - GrinRange - 1})
	place(t, o, "b", Vec2{X: 2, Y: 2})
	if !o.Redirect("a", "p-b") {
		t.Fatal("the verb was refused")
	}
	if _, ok := o.Leave("b"); !ok {
		t.Fatal("leave failed")
	}
	advance(o, 5)
	before := math.Hypot(posOf(t, o, "a").X-bossOf(o).X, posOf(t, o, "a").Y-bossOf(o).Y)
	advance(o, 10)
	after := math.Hypot(posOf(t, o, "a").X-bossOf(o).X, posOf(t, o, "a").Y-bossOf(o).Y)
	if after >= before {
		t.Fatalf("he did not come back to the only person left: %.2f then %.2f", before, after)
	}
}

// --- «набухать лысого» ------------------------------------------------------

func TestWalkingIntoTheBottleBuysHimARound(t *testing.T) {
	// The one mechanic that acts on HIM rather than on the space between you, so
	// it costs a walk — which means leaving wherever you were standing, and the
	// streak with it.
	o := NewOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", BottleSpots[spotOf(o)])
	advance(o, 1)

	if drunkOf(o) <= 0 {
		t.Fatal("standing on the bottle did not get him drunk")
	}
	// And the frame says so, to EVERYBODY — being drunk is a fact about the
	// office, so one Карен buys the round and both of them watch him wobble.
	if got := snapOf(t, o, "a").B.D; got <= 0 {
		t.Fatalf("the frame does not carry his state: %d", got)
	}
}

func TestTheBottleIsNotAButtonYouCanHold(t *testing.T) {
	// It is spent, and another one takes a long time to arrive — the effect is
	// the strongest in the game, so the cooldown is the whole balance of it.
	o := NewOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", BottleSpots[spotOf(o)])
	advance(o, 1)
	first := drunkOf(o)

	// Stand on the spot for a while: no second round, and he sobers up on
	// schedule rather than being topped up.
	for i := 0; i < int(DrunkSeconds*SimHz)+4; i++ {
		place(t, o, "a", BottleSpots[spotOf(o)])
		advance(o, 1)
	}
	if drunkOf(o) > 0 {
		t.Fatalf("he never sobered up — the bottle is refilling under him (%v, was %v)", drunkOf(o), first)
	}
	// The frame says when another one is due, so the plane can stop drawing it.
	if snapOf(t, o, "a").Bt <= 0 {
		t.Fatal("the frame does not say the bottle is gone, so a client would draw one that is not there")
	}
}

func TestABottleNobodyHasReachedIsStillThere(t *testing.T) {
	// Absent `bt` means "it is standing there", which is the common case and is
	// why it costs nothing to say.
	o := NewOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", Vec2{X: OfficeW - 1, Y: 1})
	advance(o, 2)
	if got := snapOf(t, o, "a").Bt; got != 0 {
		t.Fatalf("an untouched bottle reports %d ms until it returns", got)
	}
	if drunkOf(o) != 0 {
		t.Fatal("he got drunk with nobody near the bottle")
	}
}

// parkBoss stands the bald man back at his spawn.
//
// For tests that need a long run and are not about the chase. Since he learned
// to route around the furniture there is nowhere on this floor a pinned player
// survives two minutes, so a test that wants two minutes has to stop him rather
// than hide from him.
func parkBoss(o *Office) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.boss.Pos = Vec2{X: BossSpawnX, Y: BossSpawnY}
}

// spotOf is which spot the bottle is currently on.
func spotOf(o *Office) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.bottleSpot
}

func drunkOf(o *Office) float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.boss.Drunk
}

func TestTheBottleComesBackSomewhereElse(t *testing.T) {
	// It MOVES, and that is what stops fetch-and-spend being a button you stand
	// next to. Ten seconds is short enough to be a thing you watch for; a bottle
	// that came back where it was would just be a lever with a timer.
	o := NewOffice()
	join(t, o, "a", "s1")
	seen := map[int]bool{}
	for round := 0; round < 12; round++ {
		was := spotOf(o)
		seen[was] = true
		parkBoss(o)
		place(t, o, "a", BottleSpots[was])
		advance(o, 1)
		if drunkOf(o) <= 0 {
			t.Fatalf("round %d: standing on the bottle did not get him drunk", round)
		}
		// Walk away and wait it out, so nothing is picked up the instant it lands.
		place(t, o, "a", Vec2{X: OfficeW / 2, Y: 1})
		for i := 0; i < int(BottleReturn*SimHz)+2; i++ {
			parkBoss(o)
			place(t, o, "a", Vec2{X: OfficeW / 2, Y: 1})
			advance(o, 1)
		}
		if now := spotOf(o); now == was {
			t.Fatalf("round %d: it came back on the same spot (%d)", round, was)
		}
		// And the frame names the spot by INDEX, never by a coordinate.
		s := snapOf(t, o, "a")
		if s.Bs != spotOf(o) {
			t.Fatalf("the frame says spot %d, the office says %d", s.Bs, spotOf(o))
		}
	}
	if len(seen) < 3 {
		t.Fatalf("twelve bottles only ever used %d spots", len(seen))
	}
}
