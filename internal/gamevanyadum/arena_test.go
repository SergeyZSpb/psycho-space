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

func TestTimeBudgetStopsASpeedHack(t *testing.T) {
	// The attack this defends against needs no field out of range anywhere: the
	// socket allows ten frames a second, each may carry four sub-steps of up to
	// MaxStepSeconds, so a client that fills every frame asks for eight seconds
	// of simulation per real second. Every individual value is legal; the total
	// is eight times everybody else's speed.
	//
	// So the arena spends REAL time, not claimed time.
	a := newTestArena(t, 3)
	start := a.Owner().State.Pos

	seq := int64(0)
	for i := 0; i < 10; i++ {
		cmds := make([]Command, 0, 4)
		for j := 0; j < 4; j++ {
			seq++
			cmds = append(cmds, Command{Seq: seq, Dt: MaxStepSeconds, MY: 1, Yaw: 0})
		}
		a.Enqueue(a.AccountID, &ParsedInput{Cmds: cmds})
	}

	a.Advance(SimStep.Seconds(), time.Unix(0, 0))

	moved := math.Hypot(a.Owner().State.Pos.X-start.X, a.Owner().State.Pos.Y-start.Y)
	if budget := SimStep.Seconds() * WalkSpeed; moved > budget+1e-6 {
		t.Fatalf("one tick moved %.3f m; a tick is worth at most %.3f m", moved, budget)
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
