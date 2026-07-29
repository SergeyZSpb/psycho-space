package gamekaren

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
	if err := o.Join(accountID, shiftID, "p-"+accountID, epoch); err != nil {
		t.Fatal(err)
	}
	place(o, accountID, Vec2{X: PlayerSpawnX, Y: PlayerSpawnY})
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
	if err := o.Join("a", "s2", "p-a", epoch); !errors.Is(err, ErrShiftInProgress) {
		t.Fatalf("the second shift was allowed: %v", err)
	}
	// And leaving makes room for the next one.
	if _, ok := o.Leave("a"); !ok {
		t.Fatal("leaving found nobody")
	}
	if err := o.Join("a", "s3", "p-a", epoch); err != nil {
		t.Fatalf("starting after leaving was refused: %v", err)
	}
}

func TestTheFloorIsCapped(t *testing.T) {
	o := NewOffice()
	for i := 0; i < MaxOccupants; i++ {
		if err := o.Join(string(rune('a'+i)), "s", "p-"+string(rune('a'+i)), epoch); err != nil {
			t.Fatalf("occupant %d refused: %v", i, err)
		}
	}
	if err := o.Join("z", "s", "p-z", epoch); !errors.Is(err, ErrOfficeFull) {
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
func place(o *Office, accountID string, at Vec2) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.occupants[accountID].State.Pos = at
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
	if err := o.Join("a", "shift-a", "p-a", now); err != nil {
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
	if err := o.Join("a", "shift-a", "p-a", now); err != nil {
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
		if err := o.Join("a", "s", "p-a", epoch); err != nil {
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

func TestEverySpawnIsSomewhereHeCanWalkStraightTo(t *testing.T) {
	// The measurement behind this is on clearLine: he has no way round a desk,
	// so a shift opening in one is a shift where nothing happens for a minute
	// and a half. Without this rule about a sixth of the floor was over ten
	// seconds and the worst point took ninety.
	boss := NewBoss()
	for i, at := range spawnsOf(t, 300) {
		if !clearLine(at, boss.Pos, PlayerRadius) {
			t.Fatalf("draw %d spawned in a desk's shadow at %+v", i, at)
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
		if err := o.Join("a", "s1", "p-a", epoch); err != nil {
			t.Fatal(err)
		}
		if err := o.Join("b", "s2", "p-b", epoch); err != nil {
			t.Fatal(err)
		}
		a, b := posOf(t, o, "a"), posOf(t, o, "b")
		if gap := math.Hypot(a.X-b.X, a.Y-b.Y); gap < spawnFromEachOther-0.02 {
			t.Fatalf("run %d put two people %.2f m apart: %+v %+v", i, gap, a, b)
		}
	}
}

func TestASpawnKeepsItsDistanceFromHimWhereItCan(t *testing.T) {
	// TWO CLAIMS, because spawnFromBoss is a PREFERENCE and not a filter — the
	// sampler short-circuits on the first draw that is comfortably clear and
	// otherwise keeps the best it saw, so a run of unlucky draws legitimately
	// lands inside it. Rejecting outright is the worse design: mid-shift he can
	// stand where almost nothing is 12 m away, and a hard filter would reject
	// every draw and fall through to a fixed point that could be beside him.
	//
	// Both numbers are measured over 3000 draws into an empty office: the
	// smallest gap seen was 8.02 m and 0.17 % were inside the preference. The
	// bars below are those with room — a hard floor at GrinRange, which is the
	// distance that decides whether the shift opens with him already smiling at
	// you, and 95 % against a measured 99.8 %.
	const draws = 400
	inside := 0
	for i, at := range spawnsOf(t, draws) {
		gap := math.Hypot(at.X-BossSpawnX, at.Y-BossSpawnY)
		if gap <= GrinRange {
			t.Fatalf("draw %d spawned %.2f m from him — inside the range he smiles from (%v)", i, gap, GrinRange)
		}
		if gap < spawnFromBoss {
			inside++
		}
	}
	if got := float64(draws-inside) / draws; got < 0.95 {
		t.Fatalf("only %.1f%% of draws kept %v m from him", 100*got, spawnFromBoss)
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
	place(o, "a", Vec2{X: PlayerSpawnX, Y: PlayerSpawnY})
	place(o, "b", Vec2{X: PlayerSpawnX + 0.1, Y: PlayerSpawnY})
	if err := o.Join("c", "s3", "p-c", epoch); err != nil {
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
	place(o, "b", Vec2{X: PlayerSpawnX + 3, Y: PlayerSpawnY + 1})

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
	svc := NewService(nil, Room, nil, nil)
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
	if NewService(nil, Room, nil, nil).pseudonym(account) == handle {
		t.Fatal("the handle survived a restart, so it is a durable identifier")
	}
}

func TestTheFrameCarriesTheHandleTheServiceMinted(t *testing.T) {
	o := NewOffice()
	if err := o.Join("a", "s1", "handle-a", epoch); err != nil {
		t.Fatal(err)
	}
	if err := o.Join("b", "s2", "handle-b", epoch); err != nil {
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
