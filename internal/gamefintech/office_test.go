package gamefintech

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
	join(t, o, "a", "s1")
	cmds := make([]Command, 0, 10)
	for i := 1; i <= 10; i++ {
		cmds = append(cmds, Command{Seq: uint32(i), Dt: SimStep.Seconds(), MX: 1})
	}
	o.Enqueue("a", Input{Cmds: cmds}, epoch)

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
	o := newTestOffice()
	join(t, o, "a", "s1")
	c := Command{Seq: 1, Dt: SimStep.Seconds(), MX: 1}
	o.Enqueue("a", Input{Cmds: []Command{c}}, epoch)
	advance(o, 1)
	moved := snapOf(t, o, "a").X

	// The same command again, five times over, exactly as a lossy client would
	// resend it.
	o.Enqueue("a", Input{Cmds: []Command{c, c, c, c, c}}, epoch)
	advance(o, 5)
	if got := snapOf(t, o, "a").X; got != moved {
		t.Fatalf("a resent command moved him again: %d → %d", moved, got)
	}
	// A zero sequence is "unset" and is dropped for the same reason.
	o.Enqueue("a", Input{Cmds: []Command{{Dt: SimStep.Seconds(), MX: 1}}}, epoch)
	advance(o, 2)
	if got := snapOf(t, o, "a").X; got != moved {
		t.Fatalf("an unsequenced command was applied: %d → %d", moved, got)
	}
}

func TestARedundantCommandStillWaitingInTheQueueIsDroppedToo(t *testing.T) {
	// THE DEFECT THIS GAME SHIPPED, and the one the test above does not catch
	// because it drains the queue between the two sends.
	//
	// A frame carries four fresh sub-steps where a tick affords two, so at any
	// moment about half the queue is accepted-but-not-yet-simulated. Deduplicating
	// on the ACK — the last sequence applied — drops the repeats of the simulated
	// half and accepts the repeats of the waiting half, so the player walks twice
	// for input they gave once: dragged forward while the stick is down, and still
	// walking after it is released while the office works through the surplus.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", Vec2{X: 1, Y: OfficeH / 2})
	start := snapOf(t, o, "a").X

	frame := func(from, to uint32) []Command {
		var out []Command
		for i := from; i <= to; i++ {
			out = append(out, Command{Seq: i, Dt: 0.025, MX: 1})
		}
		return out
	}

	// Frame one: four sub-steps, 100 ms of walking. One tick affords two of them,
	// so three and four are still in the queue when the next frame lands.
	o.Enqueue("a", Input{Cmds: frame(1, 4)}, epoch)
	advance(o, 1)
	// Frame two, exactly as buildInputFrame produces it: the unacknowledged tail
	// repeated, then the fresh sub-steps.
	o.Enqueue("a", Input{Cmds: frame(1, 8)}, epoch)
	advance(o, 20)

	moved := float64(snapOf(t, o, "a").X-start) / 100
	want := 8 * 0.025 * WalkSpeed
	if math.Abs(moved-want) > 1e-6 {
		t.Fatalf("moved %.4f m where %.4f m was asked for — a queued command was applied twice", moved, want)
	}
}

func TestOnlyWhatWasActuallySimulatedIsAcknowledged(t *testing.T) {
	// The ack is ONE sequence number and the client drops everything at or below
	// it, so there is no way to acknowledge half a command. Simulating part of one
	// and acknowledging the whole leaves the client holding movement the office
	// never ran — a permanent divergence, and in the direction that costs most
	// here: the client believes it is further from the лысый than the office does,
	// so the shift ends while he is still drawn a metre away.
	o := newTestOffice()
	join(t, o, "a", "s1")
	start := snapOf(t, o, "a").X

	// One command four ticks long. A single tick can afford none of it.
	o.Enqueue("a", Input{Cmds: []Command{{Seq: 1, Dt: MaxStepSeconds, MX: 1}}}, epoch)
	advance(o, 1)
	if got := snapOf(t, o, "a").Ack; got != 0 {
		t.Fatalf("acknowledged seq %d after a tick that could not afford it", got)
	}
	if got := snapOf(t, o, "a").X; got != start {
		t.Fatalf("simulated part of a command it could not afford: %d → %d", start, got)
	}

	// Three more ticks buy it, and THEN it is acknowledged — in full, having
	// moved its full distance. That it is ever acknowledged at all is the other
	// half of the claim: the idle fill must not eat the budget it is waiting for.
	advance(o, 3)
	if got := snapOf(t, o, "a").Ack; got != 1 {
		t.Fatalf("four ticks' budget did not run a command worth four ticks: ack %d", got)
	}
	moved := float64(snapOf(t, o, "a").X-start) / 100
	if want := MaxStepSeconds * WalkSpeed; math.Abs(moved-want) > 1e-6 {
		t.Fatalf("acknowledged after moving %.4f m of the %.4f m it described", moved, want)
	}
}

func TestTheTimeBudgetCapsBankedSimulatedTime(t *testing.T) {
	// THE SPEED HACK. A client that fills every frame with the largest legal dt
	// is asking to run faster than everybody else, with no single field out of
	// range anywhere. The answer is that simulated time is bought at real time:
	// a tick may spend at most TimeBudgetCap seconds of client-claimed movement,
	// however much is queued and however long the tick itself claims to be.
	o := newTestOffice()
	join(t, o, "a", "s1")
	var cmds []Command
	for i := 1; i <= maxPending; i++ {
		cmds = append(cmds, Command{Seq: uint32(i), Dt: MaxStepSeconds, MX: 1})
	}
	o.Enqueue("a", Input{Cmds: cmds}, epoch)
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
	o := newTestOffice()
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
	o.Enqueue("a", Input{Cmds: cmds}, epoch)
	advance(o, 1)

	moved := float64(snapOf(t, o, "a").X-start) / 100
	if limit := WalkSpeed*SimStep.Seconds() + 1e-6; moved > limit {
		t.Fatalf("five quiet seconds bought %v m of movement in one tick, a tick is worth %v m", moved, limit)
	}
}

// --- lag compensation -------------------------------------------------------
//
// Your own Карен is predicted, so he is drawn in the present. The лысый cannot
// be, so he is drawn from an interpolation buffer in the recent past. Resolving
// the catch against the office's present compares two different instants, and
// the two errors ADD while you run away — which is how a shift ended while he
// was still drawn a couple of metres off.

func TestTheRoundTripIsDerivedFromTheTickTheClientSaysItDrew(t *testing.T) {
	// DERIVED, NEVER REPORTED. The tick rate is fixed, so the gap between the
	// tick a client says it has and the tick the office is on IS the loop — and
	// a client cannot inflate it without also claiming to be looking at a frame
	// it has not received.
	o := newTestOffice()
	join(t, o, "a", "s1")
	advance(o, 5)

	// Claims to have last drawn tick 3 while the office is on 5: two ticks, a
	// tenth of a second.
	o.Enqueue("a", Input{Seen: 3}, epoch)
	if got := o.rttOf(t, "a"); math.Abs(got-0.1) > 1e-9 {
		t.Fatalf("derived a round trip of %v, want 0.1", got)
	}

	// A frame carrying no commands at all still measures the loop: a player
	// standing perfectly still is the most present person in the game.
	advance(o, 1)
	o.Enqueue("a", Input{Seen: 3}, epoch)
	if got := o.rttOf(t, "a"); got <= 0.1 {
		t.Fatalf("a command-free frame did not update the round trip: %v", got)
	}
}

func TestAClaimedLatencyIsCappedAndAFutureOneIsIgnored(t *testing.T) {
	// The client controls the number, so it needs a ceiling — otherwise claiming
	// an absurd latency would leave somebody permanently further from the лысый
	// than they really are.
	o := newTestOffice()
	join(t, o, "a", "s1")
	advance(o, 5)
	o.Enqueue("a", Input{Seen: 1}, epoch)
	if got := o.rttOf(t, "a"); got > CatchRewindMax+1e-9 {
		t.Fatalf("a claim of %v was accepted past the %v cap", got, CatchRewindMax)
	}

	// A tick in the FUTURE is discarded rather than clamped: it is a client
	// guessing, or the office having been rebuilt under it, and neither is a
	// measurement.
	o2 := newTestOffice()
	join(t, o2, "b", "s2")
	advance(o2, 2)
	o2.Enqueue("b", Input{Seen: 9999}, epoch)
	if got := o2.rttOf(t, "b"); got != 0 {
		t.Fatalf("a tick from the future measured %v", got)
	}
}

func TestTheCatchIsResolvedAgainstTheWorldTheVictimSaw(t *testing.T) {
	// He arrives on somebody whose screen is a third of a second behind. The
	// shift must not end on the tick he arrives — on that occupant's screen he is
	// still the better part of a metre away — but it must end once their screen
	// has caught up. Being uncatchable is not the fix; being caught where you saw
	// it happen is.
	o := newTestOffice()
	join(t, o, "a", "s1")
	advance(o, 8)
	// The measured loop, through the production path. Capped at CatchRewindMax,
	// which is six ticks.
	o.Enqueue("a", Input{Seen: 1}, epoch)

	o.mu.Lock()
	o.boss.Pos = o.occupants["a"].State.Pos
	o.mu.Unlock()

	// He is standing on them NOW, and was nowhere near them on the tick they are
	// looking at.
	if ended := advance(o, 1); len(ended) != 0 {
		t.Fatal("the shift ended on the tick he arrived, against a screen that had not seen him arrive")
	}
	// Six more ticks and the tick he arrived on is the one being drawn.
	ended := advance(o, 6)
	if len(ended) != 1 || ended[0].Cause != CausePromoted {
		t.Fatalf("the rewind never expired: %+v", ended)
	}
}

func TestAClientWithNoMeasuredLatencyStillGetsTheRenderDelay(t *testing.T) {
	// The floor, and it is not zero. EVERY client draws the лысый
	// RenderDelaySeconds in the past — that is the interpolation buffer's whole
	// mechanism — so a brand-new occupant with no round trip yet is still two
	// ticks behind, and resolving their catch in the present would be the same
	// defect in miniature.
	o := newTestOffice()
	join(t, o, "a", "s1")
	advance(o, 4)
	o.mu.Lock()
	o.boss.Pos = o.occupants["a"].State.Pos
	o.mu.Unlock()

	if ended := advance(o, 1); len(ended) != 0 {
		t.Fatal("caught in the present, with no allowance for the render delay at all")
	}
	if ended := advance(o, 2); len(ended) != 1 {
		t.Fatalf("still not caught two ticks later: %+v", ended)
	}
}

// rttOf reads one occupant's derived round trip. Test-only, like the two beside
// it: nothing in production reaches inside an occupant.
func (o *Office) rttOf(t *testing.T, accountID string) float64 {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok {
		t.Fatalf("no occupant %q", accountID)
	}
	return occ.RTT
}

func TestBeingCaughtEndsTheShiftAsAPromotion(t *testing.T) {
	// End to end, with nothing reached into: somebody stands still at the spawn
	// earning money, and the bald man crosses the office to congratulate them.
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	// Move one of them and not the other.
	o.Enqueue("a", Input{Cmds: []Command{{Seq: 1, Dt: SimStep.Seconds(), MX: 1}}}, epoch)
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
	o := newTestOffice()
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
		o := newTestOffice()
		join(t, o, "a", "s1")
		join(t, o, "b", "s2")
		// Both start on the same known point (join pins it), so the split below
		// is symmetric and his choice is a pure tie — which is the whole claim.
		o.Enqueue("a", Input{Cmds: []Command{{Seq: 1, Dt: MaxStepSeconds, MX: -1}}}, epoch)
		o.Enqueue("b", Input{Cmds: []Command{{Seq: 1, Dt: MaxStepSeconds, MX: 1}}}, epoch)
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
	o := newTestOffice()
	join(t, o, "a", "s1")
	for frame := 0; frame < 100; frame++ {
		cmds := make([]Command, 0, MaxInboundCommands)
		for i := 0; i < MaxInboundCommands; i++ {
			cmds = append(cmds, Command{Seq: uint32(frame*MaxInboundCommands + i + 1), Dt: MaxStepSeconds, MX: 1})
		}
		o.Enqueue("a", Input{Cmds: cmds}, epoch)
	}
	// The newest maxPending commands survive; everything older was dropped
	// before it could be simulated, so the position is bounded by REAL TIME and
	// not by how much was sent.
	//
	// FOUR TICKS RATHER THAN ONE, because a command is now simulated whole or it
	// waits: each of these claims MaxStepSeconds, which is four ticks' worth of
	// budget, so one tick affords none of it and the fourth affords exactly one.
	// That is the point of the change — the alternative was truncating a command
	// to the budget and acknowledging it as though it had run in full.
	const ticks = 4
	advance(o, ticks)
	x := float64(snapOf(t, o, "a").X) / 100
	if limit := PlayerSpawnX + ticks*SimStep.Seconds()*WalkSpeed + 1e-6; x > limit {
		t.Fatalf("a thousand queued commands moved him to %v, %d ticks of real time allow %v", x, ticks, limit)
	}
	if math.Abs(x-PlayerSpawnX) < 1e-9 {
		t.Fatal("none of the queue was simulated at all")
	}
}

func TestEnqueueIgnoresAnAccountThatIsNotWorking(t *testing.T) {
	o := newTestOffice()
	o.Enqueue("nobody", Input{Cmds: []Command{{Seq: 1, Dt: 0.05, MX: 1}}}, epoch)
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
	o := newTestOffice()
	if err := o.Join("a", "shift-a", "p-a", "", 0, now); err != nil {
		t.Fatalf("join: %v", err)
	}

	// 40 ms claimed out of every 50 ms tick — a browser timer does not tile a
	// tick evenly, and this is an ordinary amount of drift rather than a bad one.
	const claimed = 0.04
	o.Enqueue("a", Input{Cmds: []Command{{Seq: 1, Dt: claimed, MX: 0, MY: -1, Dash: true}}}, now)
	o.Advance(SimStep.Seconds(), now)

	seq := uint32(2)
	for i := 0; i < 3; i++ {
		o.Enqueue("a", Input{Cmds: []Command{{Seq: seq, Dt: claimed, MX: 0, MY: -1}}}, now)
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
	o := newTestOffice()
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
		o := newTestOffice()
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
		for d, desk := range testRects {
			if insideRect(desk, at, PlayerRadius) {
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
		o := newTestOffice()
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
		o := newTestOffice()
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
	o := newTestOffice()
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
	for d, desk := range testRects {
		if insideRect(desk, at, PlayerRadius) {
			t.Fatalf("the crowded fallback stood somebody inside desk %d at %+v", d, at)
		}
	}
}

// --- peers on the frame ----------------------------------------------------

func TestYourFrameCarriesTheOtherPeopleInTheOffice(t *testing.T) {
	o := newTestOffice()
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
	o := newTestOffice()
	join(t, o, "a", "s1")
	if s := snapOf(t, o, "a"); len(s.Pr) != 0 {
		t.Fatalf("a solo occupant sees %d peers: %+v", len(s.Pr), s.Pr)
	}
}

func TestAPeerIsAPseudonymAndNeverAnAccountID(t *testing.T) {
	// ADR-037. An account id is a durable identifier for a person, and it must
	// not reach somebody else's browser — the handle is minted per process from
	// a key held only in memory.
	svc := NewService(nil, Room, nil, nil, nil, testLayout)
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
	if NewService(nil, Room, nil, nil, nil, testLayout).pseudonym(account) == handle {
		t.Fatal("the handle survived a restart, so it is a durable identifier")
	}
}

func TestTheFrameCarriesTheHandleTheServiceMinted(t *testing.T) {
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	// Nothing on the floor to pick up: with two people there are two кальяны, one
	// of which can be drawn onto the very tile this test parks `a` on — and a man
	// behind a cloud is a man the лысый cannot see, which would answer the question
	// this test is asking with a different mechanic.
	//
	// AFTER A TICK, and that ordering is the whole of why this works. An office
	// opens with one prop of each kind and grows to one per person on its first
	// tick, so clearing the floor before that tick clears one bottle and one
	// кальян and then has a fresh, STANDING кальян appended underneath the player.
	// CI found it and a local run did not, because where a prop lands is a draw.
	advance(o, 1)
	noProps(o)
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	o := newTestOffice()
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
	// And the frame stops carrying its spot, so the plane stops drawing it: the
	// mask is what says what is on the floor, and a bottle somebody has taken is
	// simply not in it.
	if snapOf(t, o, "a").Bs != 0 {
		t.Fatal("the frame still shows a bottle standing where one was just taken")
	}
}

func TestABottleNobodyHasReachedIsStillThere(t *testing.T) {
	// The mask carries a bit for it, which is what the plane draws one from.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", Vec2{X: OfficeW - 1, Y: 1})
	advance(o, 2)
	if got := snapOf(t, o, "a").Bs; got != 1<<spotOf(o) {
		t.Fatalf("the mask is %b, want a single bit for spot %d", got, spotOf(o))
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

// spotOf is which spot the FIRST bottle is on.
//
// The office keeps one per occupant now, so «the bottle» is only a thing in a
// one-player test — which is what nearly every test in this file is, and why this
// helper is still the right shape for them. A test about several says so by
// reading `o.bottles` itself.
func spotOf(o *Office) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.bottles[0].spot
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
	o := newTestOffice()
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
		//
		// AND «AWAY» HAS TO MEAN AWAY FROM EVERY SPOT, not just from this one. The
		// old park was the middle of the top wall, which is within arm's reach of
		// the spot at (8, 2) — harmless while the frame carried an INDEX (the index
		// was right whether or not it had just been taken again) and visible the
		// moment it carried a mask, which says what is actually standing there.
		for i := 0; i < int(BottleReturn*SimHz)+2; i++ {
			parkBoss(o)
			place(t, o, "a", awayFromEveryProp())
			advance(o, 1)
		}
		if now := spotOf(o); now == was {
			t.Fatalf("round %d: it came back on the same spot (%d)", round, was)
		}
		// And the frame names the spot by a BIT, never by a coordinate — one player
		// means one bottle, so the mask is exactly one bit wide here.
		s := snapOf(t, o, "a")
		if want := 1 << spotOf(o); s.Bs != want {
			t.Fatalf("the frame's mask is %b, the office's bottle is on spot %d", s.Bs, spotOf(o))
		}
	}
	if len(seen) < 3 {
		t.Fatalf("twelve bottles only ever used %d spots", len(seen))
	}
}

// awayFromEveryProp is a place on the floor out of reach of every bottle and
// кальян spot, and away from the bald man's own spawn.
//
// IT IS COMPUTED RATHER THAN TYPED OUT, because the catalogue moves: a spot added
// next to a hand-picked corner would make a test about something else start
// picking things up, which is the failure this replaced. Fatal if the floor has
// no such place — that would mean the props cover the room, and every test that
// parks somebody would be lying.
func awayFromEveryProp() Vec2 {
	best, bestGap := Vec2{X: PlayerRadius, Y: PlayerRadius}, -1.0
	for x := 1.0; x < OfficeW-1; x += 0.5 {
		for y := 1.0; y < OfficeH-1; y += 0.5 {
			at := Vec2{X: x, Y: y}
			gap := math.Hypot(at.X-BossSpawnX, at.Y-BossSpawnY)
			for _, s := range append(append([]Vec2(nil), BottleSpots...), HookahSpots...) {
				if d := math.Hypot(at.X-s.X, at.Y-s.Y); d < gap {
					gap = d
				}
			}
			if gap > bestGap {
				best, bestGap = at, gap
			}
		}
	}
	return best
}

// noProps takes every bottle and кальян off the floor for the rest of a test.
//
// THE OFFICE KEEPS ONE OF EACH PER PERSON NOW, so a two-player test has four
// props scattered over a floor sixteen metres wide, and a fixture that parks
// somebody on a chosen coordinate can find itself standing on one — which turns
// a test about the bald man into a test about being invisible to him. Tests that
// are not ABOUT the props say so with this.
func noProps(o *Office) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.bottles {
		o.bottles[i].gone = 1e6
	}
	for i := range o.hookahs {
		o.hookahs[i].gone = 1e6
	}
}

// hookahSpotOf is which spot the FIRST кальян is on — see spotOf.
func hookahSpotOf(o *Office) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hookahs[0].spot
}

func cloudOf(o *Office, accountID string) float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if occ, ok := o.occupants[accountID]; ok {
		return occ.Invincible
	}
	return 0
}

func TestWalkingToTheHookahPutsYouBehindACloud(t *testing.T) {
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", HookahSpots[hookahSpotOf(o)])
	advance(o, 1)

	if cloudOf(o, "a") <= 0 {
		t.Fatal("standing on the кальян did not put a cloud over him")
	}
	// And the frame says so, on YOUR own field — the client needs it for the row
	// above the office and for the cloud on the figure.
	if got := snapOf(t, o, "a").Iv; got <= 0 {
		t.Fatalf("the frame does not carry the cloud: %d", got)
	}
}

func TestHeCannotCatchSomebodyBehindACloud(t *testing.T) {
	// THE WHOLE MECHANIC. He is placed ON the player, which without the cloud ends
	// the shift on the next tick.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", HookahSpots[hookahSpotOf(o)])
	advance(o, 1)
	if cloudOf(o, "a") <= 0 {
		t.Fatal("no cloud, so this test proves nothing")
	}
	o.mu.Lock()
	o.boss.Pos = o.occupants["a"].State.Pos
	o.mu.Unlock()

	if ended := advance(o, 2); len(ended) != 0 {
		t.Fatalf("he caught somebody who was behind a cloud: %+v", ended[0])
	}
}

func TestBeingUncatchableIsNotBeingImmortal(t *testing.T) {
	// THE GUARD IS ON THE CAUGHT CASE ALONE, and this is why. If it were on the
	// whole switch, an invincible occupant who closed the tab would hold a slot in
	// a three-person office until the process restarted, because the abandon branch
	// shares that switch.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", HookahSpots[hookahSpotOf(o)])
	advance(o, 1)
	if cloudOf(o, "a") <= 0 {
		t.Fatal("no cloud, so this test proves nothing")
	}
	// Nobody has been connected for well past the grace.
	o.mu.Lock()
	o.occupants["a"].LastSeen = epoch.Add(-AbandonGrace - time.Minute)
	o.mu.Unlock()

	ended := advance(o, 1)
	if len(ended) != 1 || ended[0].Cause != CauseLeft {
		t.Fatalf("an abandoned shift behind a cloud was not recorded: %+v", ended)
	}
}

func TestWhileHiddenHeLosesInterestAndSaysSo(t *testing.T) {
	// Excluded from the target list rather than merely un-catchable, which is what
	// makes the reprieve buy DISTANCE: he stops at the catch radius, so a guard
	// alone would leave him standing on you when the cloud cleared.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", HookahSpots[hookahSpotOf(o)])
	advance(o, 2)

	o.mu.Lock()
	state := o.bossState()
	o.mu.Unlock()
	if state != BossLost {
		t.Fatalf("the office says he is in state %v, want BossLost", state)
	}
	// And the line over his head comes from the lost run.
	said := BossLines[snapOf(t, o, "a").B.P]
	if !strings.Contains(strings.Join(bossLostLines, "|"), said) {
		t.Fatalf("he says %q, which is not one of his lost lines", said)
	}
}

func TestAColleagueSeesTheCloudToo(t *testing.T) {
	// A buff only its owner can see is unfinished — and which colleague the лысый
	// can no longer walk at is the most useful thing to know about somebody else in
	// the room.
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	place(t, o, "a", HookahSpots[hookahSpotOf(o)])
	advance(o, 1)
	if cloudOf(o, "a") <= 0 {
		t.Fatal("no cloud, so this test proves nothing")
	}

	peers := snapOf(t, o, "b").Pr
	if len(peers) != 1 {
		t.Fatalf("b sees %d peers, want 1", len(peers))
	}
	if peers[0].Iv <= 0 {
		t.Fatalf("b cannot see that a is behind a cloud: %+v", peers[0])
	}
}

func TestTheHookahIsSpentAndComesBackSomewhereElse(t *testing.T) {
	o := newTestOffice()
	join(t, o, "a", "s1")
	first := hookahSpotOf(o)
	place(t, o, "a", HookahSpots[first])
	advance(o, 1)

	// Spent: standing on it a second time gives nothing until it returns.
	o.mu.Lock()
	o.occupants["a"].Invincible = 0
	o.mu.Unlock()
	advance(o, 1)
	if cloudOf(o, "a") > 0 {
		t.Fatal("the кальян was still there after somebody took it")
	}

	// And it comes back somewhere else, so the walk is a different walk.
	advance(o, int(HookahReturn*SimHz)+2)
	if got := hookahSpotOf(o); got == first {
		t.Fatalf("it came back on the same spot %d", got)
	}
}

func slowOf(o *Office, accountID string) float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if occ, ok := o.occupants[accountID]; ok {
		return occ.State.SlowLeft
	}
	return 0
}

func TestClaudeSlowsYouDownRatherThanEndingTheShift(t *testing.T) {
	// THE WHOLE DIFFERENCE BETWEEN THE TWO MEN. He is placed on the player, which
	// for the лысый is the end of the shift; for this one it is four seconds of
	// walking at 5.12 instead of 6.4.
	o := newTestOffice()
	join(t, o, "a", "s1")
	o.mu.Lock()
	o.claude.Pos = o.occupants["a"].State.Pos
	o.mu.Unlock()

	// THREE TICKS RATHER THAN ONE, because a landing is resolved against where he
	// was on the tick this occupant's screen is showing — RenderDelaySeconds
	// behind, which is two ticks at the published rate. Placing him and testing
	// immediately asks whether he landed in a past he had not yet occupied.
	ended := advance(o, 3)
	if len(ended) != 0 {
		t.Fatalf("Claude ended a shift: %+v", ended[0])
	}
	if got := slowOf(o, "a"); got <= 0 {
		t.Fatalf("landing on somebody did not slow them: %v", got)
	}
	// And the frame says so, on the field the client PREDICTS from.
	if got := snapOf(t, o, "a").Sl; got <= 0 {
		t.Fatalf("the frame does not carry the slow: %d", got)
	}
}

func TestTheSlowDoesNotStack(t *testing.T) {
	// Assigned, never accumulated — the лысый's drink arrangement. Two applications
	// would leave a walk at 4.096 m/s against their 4.0, which is not an escape.
	o := newTestOffice()
	join(t, o, "a", "s1")
	o.mu.Lock()
	o.claude.Pos = o.occupants["a"].State.Pos
	o.mu.Unlock()

	// Three, for the reason the test above spends three: the landing is resolved
	// against the rewound world.
	advance(o, 3)
	first := slowOf(o, "a")
	advance(o, 1)
	if second := slowOf(o, "a"); second > first {
		t.Fatalf("a second landing extended the slow from %v to %v", first, second)
	}
}

func TestACloudHidesYouFromBothOfThem(t *testing.T) {
	// Invincibility is expressed as a shorter target list, and Claude is stepped
	// against the SAME list — which is the answer a player expects from something
	// called being uncatchable.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", HookahSpots[hookahSpotOf(o)])
	advance(o, 1)
	if cloudOf(o, "a") <= 0 {
		t.Fatal("no cloud, so this test proves nothing")
	}
	o.mu.Lock()
	o.claude.Pos = o.occupants["a"].State.Pos
	o.mu.Unlock()

	advance(o, 1)
	if got := slowOf(o, "a"); got > 0 {
		t.Fatalf("Claude landed on somebody behind a cloud: %v", got)
	}
}

func TestAColleagueSeesTheSlowToo(t *testing.T) {
	// A debuff is as public as a buff: «he is slowed» is who the лысый will reach
	// first, which is exactly the sort of thing every screen has to show.
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	o.mu.Lock()
	o.claude.Pos = o.occupants["a"].State.Pos
	o.mu.Unlock()
	// Three, so the rewound world has caught up with where he was put — see
	// TestClaudeSlowsYouDownRatherThanEndingTheShift.
	advance(o, 3)

	peers := snapOf(t, o, "b").Pr
	if len(peers) != 1 {
		t.Fatalf("b sees %d peers, want 1", len(peers))
	}
	if peers[0].Sl <= 0 {
		t.Fatalf("b cannot see that a was slowed: %+v", peers[0])
	}
}

func TestTheTwoOfThemNeverStandInTheSamePlace(t *testing.T) {
	// THEY CONVERGE BY CONSTRUCTION. Both men walk at the nearest of the same
	// target list, through the same navigator, at the same speed — ChaserSpeed IS
	// BossSpeed — so from the moment their paths meet they compute an identical
	// heading and cover an identical distance every tick, for ever. The floor then
	// shows one figure where there are two, and a player is slowed by something
	// that is not there.
	o := newTestOffice()
	join(t, o, "a", "s1")
	// Put them on exactly the same tile, which is the state they lock into, and
	// let them both walk at the same person for a while.
	o.mu.Lock()
	o.claude.Pos = o.boss.Pos
	o.mu.Unlock()

	const gap = BossRadius + ChaserRadius
	for i := 0; i < 40; i++ {
		advance(o, 1)
		o.mu.Lock()
		d := math.Hypot(o.boss.Pos.X-o.claude.Pos.X, o.boss.Pos.Y-o.claude.Pos.Y)
		alive := len(o.occupants) > 0
		o.mu.Unlock()
		if d < gap-1e-6 {
			t.Fatalf("tick %d: they are %.3f m apart, two bodies need %.3f", i+1, d, gap)
		}
		if !alive {
			break // the лысый arrived, which is a different test's business
		}
	}
}

func TestOnlyClaudeGivesWay(t *testing.T) {
	// The лысый's position must not become a function of Claude's. If they split
	// the overlap between them, how long a player has before the shift ends would
	// depend on where a second man happened to be — and the catch, its rewind ring
	// and every test of the chase would move underneath.
	a := newTestOffice()
	join(t, a, "x", "s1")
	b := newTestOffice()
	join(t, b, "x", "s2")
	// Same office twice, except that in one of them Claude has walked into him.
	b.mu.Lock()
	b.claude.Pos = b.boss.Pos
	b.mu.Unlock()
	place(t, a, "x", Vec2{X: PlayerSpawnX, Y: PlayerSpawnY})
	place(t, b, "x", Vec2{X: PlayerSpawnX, Y: PlayerSpawnY})

	for i := 0; i < 20; i++ {
		advance(a, 1)
		advance(b, 1)
	}
	a.mu.Lock()
	b.mu.Lock()
	defer a.mu.Unlock()
	defer b.mu.Unlock()
	if a.boss.Pos != b.boss.Pos {
		t.Fatalf("Claude moved the лысый: %+v against %+v", a.boss.Pos, b.boss.Pos)
	}
}

func TestSteppingAsideKeepsHimOnTheFloorAndOutOfTheFurniture(t *testing.T) {
	// Stepping sideways can step into a desk or through a wall, so the same
	// resolution every other move in this game gets applies to it too. Driven over
	// every desk corner and both spawn ends rather than one convenient spot.
	for _, at := range append([]Vec2{
		{X: ChaserRadius, Y: ChaserRadius},
		{X: OfficeW - ChaserRadius, Y: OfficeH - ChaserRadius},
		{X: BossSpawnX, Y: BossSpawnY},
	}, deskCorners()...) {
		c := Separate(at, []Vec2{at}, Chaser{Pos: at}, testRects)
		if c.Pos.X < ChaserRadius-1e-9 || c.Pos.X > OfficeW-ChaserRadius+1e-9 ||
			c.Pos.Y < ChaserRadius-1e-9 || c.Pos.Y > OfficeH-ChaserRadius+1e-9 {
			t.Fatalf("stepping aside from %+v put him off the floor at %+v", at, c.Pos)
		}
		for _, d := range testRects {
			if insideRect(d, c.Pos, ChaserRadius) {
				t.Fatalf("stepping aside from %+v put him inside a desk at %+v", at, c.Pos)
			}
		}
	}
}

func TestSteppingAsideDoesNothingWhenThereIsRoom(t *testing.T) {
	// It is a resolver and not a repulsor: two men a metre apart are two men a
	// metre apart, and nudging them every tick would be a permanent wobble on a
	// figure the player is reading for direction.
	far := Chaser{Pos: Vec2{X: 8, Y: 8}}
	bodies := []Vec2{{X: 4, Y: 8}, {X: 8, Y: 4}}
	if got := Separate(Vec2{X: 8, Y: 12}, bodies, far, testRects); got.Pos != far.Pos {
		t.Fatalf("a man four metres away was moved: %+v → %+v", far.Pos, got.Pos)
	}
}

func TestClaudeStepsOutOfAPlayerRatherThanStandingInHim(t *testing.T) {
	// He walks at the man's centre and landing does not stop him, so against
	// somebody standing still — which is what this game pays you to do — he closed
	// the last half-metre and parked on top of him. Same point, same depth band, so
	// the document order decided which figure was visible and Claude vanished under
	// the player who was being slowed by him.
	const gap = PlayerRadius + ChaserRadius
	at := Vec2{X: 8, Y: 11}
	for _, from := range []Vec2{
		at,                        // exactly on top: the state with no direction in it
		{X: at.X + 0.05, Y: at.Y}, // and a hair off it, from each side
		{X: at.X - 0.05, Y: at.Y},
		{X: at.X, Y: at.Y + 0.05},
		{X: at.X, Y: at.Y - 0.05},
	} {
		c := Separate(Vec2{X: 2, Y: 2}, []Vec2{at}, Chaser{Pos: from}, testRects)
		if d := math.Hypot(c.Pos.X-at.X, c.Pos.Y-at.Y); d < gap-1e-6 {
			t.Fatalf("from %+v he is %.3f m into the player, two bodies need %.3f", from, d, gap)
		}
		// And being kept out of him must not put him out of reach: the standoff is
		// two bodies touching, which is well inside the reach he lands from.
		if !Landed(c.Pos, at) {
			t.Fatalf("from %+v stepping out of the player put him out of landing reach at %+v", from, c.Pos)
		}
	}
}

func TestClaudeNeverEndsATickInsideAStandingPlayer(t *testing.T) {
	// The office-level version of the above: a player who stands perfectly still,
	// which is the whole game, and a man walking at him for as long as it takes.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", Vec2{X: 8, Y: 11})
	o.mu.Lock()
	// Next to him already, so this is about the ticks after he arrives rather than
	// about the walk across the office — and the лысый is parked in a far corner so
	// he is not the one ending the test.
	o.claude.Pos = Vec2{X: 8.4, Y: 11}
	o.boss.Pos = Vec2{X: OfficeW - BossRadius, Y: BossRadius}
	o.mu.Unlock()

	const gap = PlayerRadius + ChaserRadius
	landed := false
	for i := 0; i < 60; i++ {
		advance(o, 1)
		o.mu.Lock()
		occ, ok := o.occupants["a"]
		if !ok {
			o.mu.Unlock()
			t.Fatal("the shift ended, which is a different test's business")
		}
		d := math.Hypot(o.claude.Pos.X-occ.State.Pos.X, o.claude.Pos.Y-occ.State.Pos.Y)
		landed = landed || occ.State.SlowLeft > 0
		o.mu.Unlock()
		if d < gap-1e-6 {
			t.Fatalf("tick %d: he is %.3f m into the player, two bodies need %.3f", i+1, d, gap)
		}
	}
	if !landed {
		t.Fatal("he never landed, so this proved nothing about a man who has arrived")
	}
}

func TestOnlyClaudeGivesWayToAPlayerToo(t *testing.T) {
	// A player's position is predicted in his own browser, so a server-side shove he
	// never asked for is a correction that snaps him sideways. He must be exactly
	// where he steered himself, with a man standing in him or without one.
	a := newTestOffice()
	join(t, a, "x", "s1")
	b := newTestOffice()
	join(t, b, "x", "s2")
	place(t, a, "x", Vec2{X: 8, Y: 11})
	place(t, b, "x", Vec2{X: 8, Y: 11})
	// Same office twice, except that in one of them Claude is standing in him.
	b.mu.Lock()
	b.claude.Pos = Vec2{X: 8, Y: 11}
	b.mu.Unlock()

	for i := 0; i < 10; i++ {
		advance(a, 1)
		advance(b, 1)
	}
	a.mu.Lock()
	b.mu.Lock()
	defer a.mu.Unlock()
	defer b.mu.Unlock()
	if a.occupants["x"].State.Pos != b.occupants["x"].State.Pos {
		t.Fatalf("Claude moved the player: %+v against %+v",
			a.occupants["x"].State.Pos, b.occupants["x"].State.Pos)
	}
}

func TestACloudDoesNotMakeAPlayerIncorporeal(t *testing.T) {
	// A кальян takes a man out of the pursuit, not out of the room. Claude walks at
	// somebody else — or nobody — but a hidden player is still a body, and a man
	// standing inside one is the same bug whether or not he can land on him.
	const gap = PlayerRadius + ChaserRadius
	at := Vec2{X: 8, Y: 11}
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", at)
	o.mu.Lock()
	o.occupants["a"].Invincible = InvincibleSeconds
	o.claude.Pos = at
	o.mu.Unlock()

	advance(o, 1)
	o.mu.Lock()
	defer o.mu.Unlock()
	if d := math.Hypot(o.claude.Pos.X-at.X, o.claude.Pos.Y-at.Y); d < gap-1e-6 {
		t.Fatalf("he is %.3f m inside a clouded player, two bodies need %.3f", d, gap)
	}
}

// deskCorners is every desk's four corners, for the resolution tests above.
func deskCorners() []Vec2 {
	out := make([]Vec2, 0, len(testRects)*4)
	for _, d := range testRects {
		out = append(out,
			Vec2{X: d.X, Y: d.Y},
			Vec2{X: d.X + d.W, Y: d.Y},
			Vec2{X: d.X, Y: d.Y + d.H},
			Vec2{X: d.X + d.W, Y: d.Y + d.H},
		)
	}
	return out
}

func TestClaudeIsAlwaysOnTheFrame(t *testing.T) {
	// He is never omitted, unlike the props: an absent field would mean «he is not
	// there», which is never true. This is what makes the budget raise honest.
	o := newTestOffice()
	join(t, o, "a", "s1")
	advance(o, 1)
	raw, ok := o.SnapshotFor("a")
	if !ok {
		t.Fatal("no snapshot")
	}
	if !strings.Contains(string(raw), `"cl":{`) {
		t.Fatalf("Claude is not on the frame: %s", raw)
	}
}

func TestClaudeIsNotRedirectable(t *testing.T) {
	// The verb is «уточните у другого» — a thing you say to a manager, not to a
	// colleague with an opinion about your tooling. He walks at the nearest person
	// whatever anybody says, so a redirect that pointed the лысый at somebody else
	// leaves Claude exactly where he was heading.
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	place(t, o, "a", Vec2{X: 4, Y: 4})
	place(t, o, "b", Vec2{X: 12, Y: 4})
	o.mu.Lock()
	o.claude.Pos = Vec2{X: 4.6, Y: 4}
	o.mu.Unlock()

	if !o.Redirect("a", "p-b") {
		t.Fatal("the redirect was refused")
	}
	advance(o, 2)
	// He landed on the man nearest HIM, not on the one the verb named.
	if slowOf(o, "a") <= 0 {
		t.Fatal("Claude followed the redirect instead of walking at the nearest person")
	}
}

func npcsOf(o *Office) []NPC {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]NPC(nil), o.npcs...)
}

func TestTheNonPlayersAreNeverTargets(t *testing.T) {
	// THE ONE THING THAT MUST NEVER BE TRUE. The moment anything enters the occupant
	// map it becomes a chase target, a snapshot addressee and a slot against the
	// occupancy cap — so a lazy player would be saved by a colleague who is not
	// playing, which is the whole game undone by scenery.
	o := newTestOffice()
	join(t, o, "a", "s1")
	advance(o, 20)

	o.mu.Lock()
	occupants := len(o.occupants)
	npcs := len(o.npcs)
	o.mu.Unlock()
	if occupants != 1 {
		t.Fatalf("the office holds %d occupants, want 1 — an NPC has joined the map", occupants)
	}
	if npcs == 0 {
		t.Fatal("there are no NPCs at all, so this test proves nothing")
	}
	if got := o.Occupants(); got != 1 {
		t.Fatalf("Occupants() reports %d, want 1", got)
	}
}

func TestTheBaldManWalksPastThemToGetToYou(t *testing.T) {
	// He is stepped against the occupants' positions and they are not in that list,
	// so however close one of them is standing he keeps coming for the player. Put
	// one right next to him and check he still closes on the man at the other end.
	o := newTestOffice()
	join(t, o, "a", "s1")
	place(t, o, "a", Vec2{X: 8, Y: 4})
	o.mu.Lock()
	o.boss.Pos = Vec2{X: 8, Y: 16}
	o.npcs[0].Pos = Vec2{X: 8, Y: 17}
	o.npcs[0].To = Vec2{X: 8, Y: 17}
	o.npcs[0].Pause = 60
	before := math.Hypot(o.boss.Pos.X-8, o.boss.Pos.Y-4)
	o.mu.Unlock()

	advance(o, 10)

	o.mu.Lock()
	after := math.Hypot(o.boss.Pos.X-8, o.boss.Pos.Y-4)
	o.mu.Unlock()
	if after >= before {
		t.Fatalf("he was %v from the player and is now %v — an NPC distracted him", before, after)
	}
}

func TestTheyNeverTouchYourShift(t *testing.T) {
	// They buff nobody and debuff nobody. Parked on top of the player for a full
	// second of simulation, nothing about him may change but his own arithmetic.
	o := newTestOffice()
	join(t, o, "a", "s1")
	o.mu.Lock()
	at := o.occupants["a"].State.Pos
	for i := range o.npcs {
		o.npcs[i].Pos = at
		o.npcs[i].To = at
		o.npcs[i].Pause = 60
	}
	o.mu.Unlock()

	advance(o, int(SimHz))

	if got := slowOf(o, "a"); got != 0 {
		t.Fatalf("an NPC slowed the player: %v", got)
	}
	if got := cloudOf(o, "a"); got != 0 {
		t.Fatalf("an NPC's smoke made the player invincible: %v", got)
	}
	o.mu.Lock()
	alive := o.occupants["a"].State.Alive
	o.mu.Unlock()
	if !alive {
		t.Fatal("an NPC ended the shift")
	}
}

func TestTheyAmbleAndSmokeAndSaySomething(t *testing.T) {
	// They have to actually MOVE, or they are furniture with opinions.
	o := newTestOffice()
	join(t, o, "a", "s1")
	start := npcsOf(o)
	moved := false
	for i := 0; i < int(SimHz)*6 && !moved; i++ {
		advance(o, 1)
		for j, n := range npcsOf(o) {
			if math.Hypot(n.Pos.X-start[j].Pos.X, n.Pos.Y-start[j].Pos.Y) > 0.5 {
				moved = true
			}
		}
	}
	if !moved {
		t.Fatal("neither of them went anywhere")
	}

	// And they are on the frame, each with his own line rather than a shared pool.
	np := snapOf(t, o, "a").Np
	if len(np) != len(NPCCast) {
		t.Fatalf("the frame carries %d NPCs, want %d", len(np), len(NPCCast))
	}
	for i, f := range np {
		if f.P < 0 || f.P >= len(NPCCast[i].Lines) {
			t.Fatalf("NPC %d says index %d, outside his own pool of %d", i, f.P, len(NPCCast[i].Lines))
		}
	}
}

func TestTheTwoOfThemDoNotSpeakInUnison(t *testing.T) {
	// On a plane holding both of them, the same line at the same instant reads as one
	// man duplicated rather than as two colleagues — which is why NPCSays is salted
	// by which of them it is.
	same, total := 0, 0
	for tick := uint64(0); tick < NPCSlot*40; tick += NPCSlot {
		a, b := NPCSays(0, tick), NPCSays(1, tick)
		total++
		if NPCCast[0].Lines[a] == NPCCast[1].Lines[b] {
			same++
		}
	}
	if same*2 > total {
		t.Fatalf("they said the same thing in %d of %d slots", same, total)
	}
}

func TestTheNonPlayersAreAlwaysOnTheFrame(t *testing.T) {
	// The owner logged in and could not see them. This is the assertion that was
	// missing: `np` is never omitted, so a real office has to put both of them on
	// every snapshot from the first tick.
	o := newTestOffice()
	join(t, o, "a", "s1")
	raw, ok := o.SnapshotFor("a")
	if !ok {
		t.Fatal("no snapshot")
	}
	if !strings.Contains(string(raw), `"np":[`) {
		t.Fatalf("the non-players are not on the frame at all: %s", raw)
	}
	s := snapOf(t, o, "a")
	if len(s.Np) != len(NPCCast) {
		t.Fatalf("the frame carries %d non-players, want %d: %s", len(s.Np), len(NPCCast), raw)
	}
	// And at a real position rather than at the origin, which the client would draw
	// in the room's top-left corner.
	for i, f := range s.Np {
		if f.X == 0 && f.Y == 0 {
			t.Fatalf("non-player %d is at the origin: %+v", i, f)
		}
	}
}

func TestTheNonPlayersDoNotGetStuck(t *testing.T) {
	// THE OWNER REPORTED BOTH OF THEM PERMANENTLY STUCK. They have no navigation, so
	// a target inside a desk — or behind one — is one they walk at forever: the
	// resolver pushes them off the furniture, they never get within NPCArrive of it,
	// and the arrival branch that would draw somewhere else is never reached.
	//
	// Driven over a long stretch of simulated time and asserted on DISTINCT PLACES
	// VISITED rather than on movement, because a man grinding along a desk edge is
	// moving and still stuck.
	o := newTestOffice()
	join(t, o, "a", "s1")

	seen := make([]map[string]bool, len(NPCCast))
	for i := range seen {
		seen[i] = map[string]bool{}
	}
	for i := 0; i < int(SimHz)*90; i++ {
		advance(o, 1)
		for j, n := range npcsOf(o) {
			seen[j][fmt.Sprintf("%.0f,%.0f", n.Pos.X, n.Pos.Y)] = true
		}
	}
	for j, places := range seen {
		if len(places) < 8 {
			t.Fatalf("%s visited only %d places in 90 s — he is stuck", NPCCast[j].Name, len(places))
		}
	}
}

func TestAnUnreachableTargetIsGivenUpOn(t *testing.T) {
	// The specific trap: a target inside a desk. He cannot stand there, so he can
	// never arrive, and without a give-up he walks into it for the rest of the shift.
	o := newTestOffice()
	join(t, o, "a", "s1")
	inside := Vec2{X: testRects[0].X + testRects[0].W/2, Y: testRects[0].Y + testRects[0].H/2}
	o.mu.Lock()
	o.npcs[0].Pos = Vec2{X: testRects[0].X - 2, Y: inside.Y}
	o.npcs[0].To = inside
	o.npcs[0].Pause = 0
	o.mu.Unlock()

	advance(o, int(SimHz*NPCGiveUpSeconds)+int(SimHz*NPCPauseSeconds)+int(SimHz))

	o.mu.Lock()
	to := o.npcs[0].To
	o.mu.Unlock()
	if to == inside {
		t.Fatal("he is still walking at a spot inside a desk")
	}
}

func TestEveryShiftDrawsItsOwnPersona(t *testing.T) {
	// «Make each player receive random Карен / Андрюха / Саня / Даша, not fixed
	// assignment.» It always was a draw — but nothing asserted it, and the draw was
	// invisible for a deploy because the served `personas` array was nil, so the
	// readout had no name to show and every shift looked identical. This is the test
	// that says the draw reaches all four.
	seen := map[int]int{}
	for i := 0; i < 400; i++ {
		seen[drawPersona()]++
	}
	if len(seen) != len(Personas) {
		t.Fatalf("400 draws produced %d of %d personas: %v", len(seen), len(Personas), seen)
	}
	// And roughly evenly, so a draw that is technically random but practically Карен
	// nine times in ten is caught too. A tenth of the fair share is a floor no fair
	// draw fails and a stuck one cannot pass.
	floor := 400 / len(Personas) / 10
	for p, n := range seen {
		if n < floor {
			t.Fatalf("persona %d (%s) came up %d times in 400, want at least %d", p, Personas[p], n, floor)
		}
	}
}

// --- «РОУТЕР УПАЛ» ----------------------------------------------------------

func TestTheRouterTakesClaudeOffTheFloorAndOffTheWire(t *testing.T) {
	// THE WHOLE VERB. He is not stepped, not tested against anybody, and — the
	// part a client depends on — not on the frame at all: an absent `cl` is what
	// says he is not there, and `ca` is how long for.
	o := newTestOffice()
	join(t, o, "a", "s1")
	// Standing where he is about to be landed on, so "he stopped chasing" is a
	// claim this test can actually make.
	place(t, o, "a", Vec2{X: ChaserSpawnX, Y: ChaserSpawnY - 1})
	advance(o, 1)
	if snapOf(t, o, "a").Cl == nil {
		t.Fatal("Claude is missing from a frame before anything happened to him")
	}

	if !o.RouterDown("a") {
		t.Fatal("the router refused to fall")
	}
	advance(o, 1)
	got := snapOf(t, o, "a")
	if got.Cl != nil {
		t.Fatalf("Claude is still on the frame while the router is down: %+v", got.Cl)
	}
	if got.Ca <= 0 {
		t.Fatalf("nothing says how long he is gone for: ca=%d", got.Ca)
	}
	if got.Rd <= 0 {
		t.Fatalf("the button has no cooldown to disable itself with: rd=%d", got.Rd)
	}
	// AND HE DOES NOTHING WHILE HE IS AWAY. Standing on the spot he was walking
	// to would be slowed within a tick or two if he were still being stepped.
	place(t, o, "a", claudeOf(o))
	advance(o, 4)
	if slowOf(o, "a") > 0 {
		t.Fatal("a man who is not in the office landed on somebody")
	}
}

func TestTheRouterComesBackUpAndClaudeReturnsThroughTheDoor(t *testing.T) {
	// A REPRIEVE, NOT A DELETION. He is back after RouterSeconds — and back at his
	// SPAWN, because a man who vanishes at your desk and rematerialises at your
	// desk has not been anywhere and the reprieve would end with him on top of you.
	o := newTestOffice()
	join(t, o, "a", "s1")
	// LET HIM WALK FIRST, or the claim is vacuous: he starts at his spawn, so a
	// test that presses the button immediately proves nothing about where he
	// reappears. Two seconds of chasing takes him most of the way across the room.
	place(t, o, "a", Vec2{X: OfficeW - 1.5, Y: 1.5})
	advance(o, 2*SimHz)
	left := claudeOf(o)
	if math.Hypot(left.X-ChaserSpawnX, left.Y-ChaserSpawnY) < 2 {
		t.Fatalf("he never left his spawn, so this test asserts nothing: %+v", left)
	}

	if !o.RouterDown("a") {
		t.Fatal("the router refused to fall")
	}
	// One tick past the absence, so the tick that restores him has run and he has
	// barely stepped since — fleeing throughout, because twelve seconds is three
	// times what the лысый needs to cross the floor and a caught occupant has no
	// snapshot to read.
	advanceFleeing(t, o, int(RouterSeconds*SimHz)+1, "a")

	got := snapOf(t, o, "a")
	if got.Cl == nil {
		t.Fatal("Claude never came back")
	}
	if got.Ca != 0 {
		t.Fatalf("he is back and the frame still counts him away: ca=%d", got.Ca)
	}
	// At his spawn, within the step or two he has taken since being put back —
	// and nowhere near where he vanished, which is the whole point.
	back := claudeOf(o)
	if d := math.Hypot(back.X-ChaserSpawnX, back.Y-ChaserSpawnY); d > 3*ChaserSpeed*SimStep.Seconds() {
		t.Fatalf("he came back at %+v, %.2f m from his spawn", back, d)
	}
	if d := math.Hypot(back.X-left.X, back.Y-left.Y); d < 2 {
		t.Fatalf("he reappeared %.2f m from where he vanished — that is not going anywhere", d)
	}
	// And the cooldown outlasts the absence, or the verb would be holdable.
	if got.Rd <= 0 {
		t.Fatalf("the router was pressable again the instant he returned: rd=%d", got.Rd)
	}
}

func TestTheRoutersCooldownBelongsToTheOfficeAndNotToTheCaller(t *testing.T) {
	// «ANYBODY CAN PRESS IT» IS WHAT MAKES THIS LOAD-BEARING. With a per-caller
	// cooldown a full floor of three would cover 36 s of absence in every 30, and
	// Claude would simply never be on the floor again — which is a deletion rather
	// than the reprieve this verb is meant to be.
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")

	if !o.RouterDown("a") {
		t.Fatal("the first, legitimate press was refused")
	}
	if o.RouterDown("a") {
		t.Fatal("the same caller pressed it twice")
	}
	if o.RouterDown("b") {
		t.Fatal("a colleague pressed it while the router was already down")
	}
	// Past the absence but still inside the wait: he is on the floor and it is
	// still refused, which is the difference between the two timers.
	advanceFleeing(t, o, int(RouterSeconds*SimHz)+2, "a", "b")
	if snapOf(t, o, "a").Cl == nil {
		t.Fatal("Claude should be back by now")
	}
	if o.RouterDown("b") {
		t.Fatal("the wait ended with the absence — the two timers are the same one")
	}
	// And past the wait, anybody may take it down again.
	advanceFleeing(t, o, int((RouterCooldown-RouterSeconds)*SimHz)+2, "a", "b")
	if !o.RouterDown("b") {
		t.Fatal("the router never came back up for the office")
	}
}

func TestTheRouterIsRefusedForSomebodyWhoIsNotWorking(t *testing.T) {
	o := newTestOffice()
	join(t, o, "a", "s1")
	if o.RouterDown("not-working") {
		t.Fatal("somebody who is not on a shift used a verb")
	}
	if !o.RouterDown("a") {
		t.Fatal("the legitimate press was refused")
	}
}

func TestTheCallerSaysTheRouterFellAndNotTheOtherVerbsLine(t *testing.T) {
	// TWO ANNOUNCEMENTS NOW, and which one is showing lives on the occupant. It
	// was a hardcoded RedirectLine, which was right with one verb and would have
	// put «ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО» over the head of somebody who pressed
	// this one.
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	if !o.RouterDown("a") {
		t.Fatal("the router refused to fall")
	}
	advance(o, 1)
	if got := snapOf(t, o, "a").P; got != RouterLine {
		t.Fatalf("the caller is saying line %d, want the router's %d", got, RouterLine)
	}
	// AND EVERY SCREEN SEES IT, not just his own: a verb only its author can see
	// is an unfinished verb.
	peers := snapOf(t, o, "b").Pr
	if len(peers) != 1 || peers[0].P != RouterLine {
		t.Fatalf("a colleague was not told who did it: %+v", peers)
	}
}

// advanceFleeing runs n ticks while keeping the named occupants in whichever
// corners are furthest from the лысый — one corner each, so they do not stack.
//
// EVERY LONG TEST IN THIS FILE NEEDS SOMETHING LIKE THIS. He crosses the floor in
// under four seconds, so anything measuring a twelve- or thirty-second timer ends
// with its occupants caught and their snapshots gone — which reads as the feature
// being broken rather than as the chase working. Fleeing is not what is under test
// here; surviving long enough to read the timer is.
func advanceFleeing(t *testing.T, o *Office, n int, accounts ...string) {
	t.Helper()
	corners := []Vec2{
		{X: 1.2, Y: 1.2},
		{X: OfficeW - 1.2, Y: 1.2},
		{X: 1.2, Y: OfficeH - 1.2},
		{X: OfficeW - 1.2, Y: OfficeH - 1.2},
	}
	for i := 0; i < n; i++ {
		him := bossOf(o)
		ranked := append([]Vec2(nil), corners...)
		sort.Slice(ranked, func(a, b int) bool {
			return math.Hypot(ranked[a].X-him.X, ranked[a].Y-him.Y) >
				math.Hypot(ranked[b].X-him.X, ranked[b].Y-him.Y)
		})
		for j, acc := range accounts {
			place(t, o, acc, ranked[j%len(ranked)])
		}
		advance(o, 1)
	}
}

// claudeOf is where Claude is standing.
func claudeOf(o *Office) Vec2 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.claude.Pos
}

// --- one prop per person ----------------------------------------------------

func propsOf(o *Office) (bottles, hookahs []prop) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]prop(nil), o.bottles...), append([]prop(nil), o.hookahs...)
}

func TestThereIsABottleAndAKalyanForEverybodyOnTheFloor(t *testing.T) {
	// THE COUNT IS THE MECHANIC. One bottle in a room of three is a race the
	// nearest man wins every time, and the other two stop walking to it — which
	// hands the strongest effects in the game to whoever spawned near them.
	o := newTestOffice()
	join(t, o, "a", "s1")
	advance(o, 1)
	if b, h := propsOf(o); len(b) != 1 || len(h) != 1 {
		t.Fatalf("one player got %d bottles and %d кальянов", len(b), len(h))
	}

	join(t, o, "b", "s2")
	join(t, o, "c", "s3")
	advance(o, 1)
	b, h := propsOf(o)
	if len(b) != 3*PropsPerPlayer || len(h) != 3*PropsPerPlayer {
		t.Fatalf("three players got %d bottles and %d кальянов", len(b), len(h))
	}
	// AND NO TWO OF THEM ON ONE TILE, or three bottles would be one bottle drawn
	// three times.
	for _, list := range [][]prop{b, h} {
		seen := map[int]bool{}
		for _, p := range list {
			if seen[p.spot] {
				t.Fatalf("two props are standing on spot %d: %+v", p.spot, list)
			}
			seen[p.spot] = true
		}
	}
	// And the frame says so: one bit per standing prop, so a full office draws
	// three of each.
	if got := bits(snapOf(t, o, "a").Bs); got != 3 {
		t.Fatalf("the mask says %d bottles are standing, want 3", got)
	}
	if got := bits(snapOf(t, o, "a").Hs); got != 3 {
		t.Fatalf("the mask says %d кальянов are standing, want 3", got)
	}
}

func TestAPropIsNeverSnatchedOffTheFloorWhenSomebodyLeaves(t *testing.T) {
	// GROWTH IS IMMEDIATE, SHRINKING IS NOT. A joiner should find a prop of their
	// own at once; one already standing must never vanish from under whoever is
	// walking towards it. So an extra is dropped only once somebody has taken it.
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	advance(o, 1)
	if b, _ := propsOf(o); len(b) != 2 {
		t.Fatalf("two players got %d bottles", len(b))
	}

	o.Leave("b")
	advance(o, 1)
	if b, _ := propsOf(o); len(b) != 2 {
		t.Fatalf("a bottle vanished off the floor the moment somebody left: %d left", len(b))
	}

	// Take one, and the office comes back to one bottle rather than standing a
	// replacement up for a person who is not there.
	b, _ := propsOf(o)
	place(t, o, "a", BottleSpots[b[0].spot])
	advance(o, 1)
	for i := 0; i < int(BottleReturn*SimHz)+4; i++ {
		parkBoss(o)
		place(t, o, "a", awayFromEveryProp())
		advance(o, 1)
	}
	if got, _ := propsOf(o); len(got) != 1 {
		t.Fatalf("the spare never went: %d bottles for one player", len(got))
	}
}

func TestTwoPeopleCanDrinkFromTwoDifferentBottles(t *testing.T) {
	// The whole point of the count, end to end: both of them reach a bottle of
	// their own on the same tick, and both bottles go.
	o := newTestOffice()
	join(t, o, "a", "s1")
	join(t, o, "b", "s2")
	advance(o, 1)
	b, _ := propsOf(o)
	if len(b) != 2 {
		t.Fatalf("two players got %d bottles", len(b))
	}
	place(t, o, "a", BottleSpots[b[0].spot])
	place(t, o, "b", BottleSpots[b[1].spot])
	advance(o, 1)

	after, _ := propsOf(o)
	for i, p := range after {
		if p.gone <= 0 {
			t.Fatalf("bottle %d is still standing after somebody reached it: %+v", i, after)
		}
	}
	if drunkOf(o) <= 0 {
		t.Fatal("nobody got him drunk")
	}
	if got := snapOf(t, o, "a").Bs; got != 0 {
		t.Fatalf("the frame still shows a bottle standing: mask %b", got)
	}
}

// bits is how many spots a mask names.
func bits(mask int) int {
	n := 0
	for ; mask > 0; mask >>= 1 {
		n += mask & 1
	}
	return n
}
