package gamevanyadum

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Lag compensation's rewind buffer.
//
// The consumer — hitscan — arrives with the shotgun, so what is tested here is
// the recorder and the rewind itself. That asymmetry is deliberate and is the
// whole argument for the file existing now: a history of the past is the one
// piece of a netcode stack that cannot be retrofitted, because on the day you
// want it the past has already happened (ADR-052).

func spot(x, y float64) Spot { return Spot{Pos: Vec2{X: x, Y: y}, Alive: true} }

func TestHistoryInterpolatesBetweenRecordedFrames(t *testing.T) {
	// Interpolated rather than snapped to the nearest tick, because the shooter
	// was themselves looking at an interpolated world — their client draws peers
	// between two snapshots — so rewinding to a single recorded instant would
	// reconstruct a world nobody ever saw.
	h := newHistory()
	base := time.Unix(100, 0)
	h.record(1, base, map[int]Spot{0: spot(0, 0)})
	h.record(2, base.Add(100*time.Millisecond), map[int]Spot{0: spot(10, 20)})

	got := h.at(base.Add(50 * time.Millisecond))
	if math.Abs(got[0].Pos.X-5) > 1e-9 || math.Abs(got[0].Pos.Y-10) > 1e-9 {
		t.Fatalf("halfway between the two frames is %+v", got[0].Pos)
	}
}

func TestHistoryClampsRatherThanFabricating(t *testing.T) {
	// Both ends of the window clamp to a frame that really happened. The
	// alternative is inventing a position, and a hit registered against
	// something that was never there is worse than a hit that misses.
	h := newHistory()
	base := time.Unix(100, 0)
	h.record(1, base, map[int]Spot{0: spot(1, 1)})
	h.record(2, base.Add(100*time.Millisecond), map[int]Spot{0: spot(9, 9)})

	if got := h.at(base.Add(-time.Hour)); got[0].Pos.X != 1 {
		t.Fatalf("before the window should clamp to the oldest frame, got %+v", got[0].Pos)
	}
	if got := h.at(base.Add(time.Hour)); got[0].Pos.X != 9 {
		t.Fatalf("after the window should clamp to the newest frame, got %+v", got[0].Pos)
	}
}

func TestHistoryIsEmptyBeforeAnythingHappened(t *testing.T) {
	if got := newHistory().at(time.Unix(0, 0)); got != nil {
		t.Fatalf("an unrecorded history answered %+v", got)
	}
}

func TestHistoryDoesNotGrow(t *testing.T) {
	// A fixed ring rather than a slice that grows: the window is bounded by
	// wall-clock and the tick rate is fixed, so the capacity is known exactly
	// and the whole structure allocates once. A simulation loop that allocates
	// twenty times a second per arena is a self-inflicted garbage problem.
	h := newHistory()
	base := time.Unix(0, 0)
	for i := 0; i < historyCapacity()*5; i++ {
		h.record(int64(i), base.Add(time.Duration(i)*SimStep), map[int]Spot{0: spot(float64(i), 0)})
	}
	if len(h.frames) != historyCapacity() {
		t.Fatalf("ring grew to %d frames", len(h.frames))
	}
	if h.count != historyCapacity() {
		t.Fatalf("count is %d, capacity is %d", h.count, historyCapacity())
	}
}

func TestHistoryKeepsWorkingAfterItWraps(t *testing.T) {
	// The bug a ring invites: reading in insertion order rather than in time
	// order once the buffer has wrapped, which silently returns a world from a
	// second ago as if it were the newest.
	h := newHistory()
	base := time.Unix(0, 0)
	n := historyCapacity() + historyCapacity()/2
	for i := 0; i < n; i++ {
		h.record(int64(i), base.Add(time.Duration(i)*SimStep), map[int]Spot{0: spot(float64(i), 0)})
	}
	newest := h.at(base.Add(time.Duration(n) * SimStep))
	if want := float64(n - 1); newest[0].Pos.X != want {
		t.Fatalf("newest frame is %v, expected %v", newest[0].Pos.X, want)
	}
	// And a point midway through the surviving window is still interpolated
	// rather than clamped.
	mid := h.at(base.Add(time.Duration(n-3)*SimStep + SimStep/2))
	if mid[0].Pos.X <= float64(n-4) || mid[0].Pos.X >= float64(n-2) {
		t.Fatalf("midway lookup after wrap returned %v", mid[0].Pos.X)
	}
}

func TestForgettingAPlaceTakesItOutOfTheWholeRing(t *testing.T) {
	// The ring is keyed by slot, and a slot outlives its holder — so freeing one
	// has to erase its past as well as its present, or the next man to stand in
	// it inherits somebody else's positions for the whole window (history.forget,
	// and world.go, release). Every frame, including the ones the ring has not
	// wrapped past yet, and only the place being freed.
	h := newHistory()
	base := time.Unix(0, 0)
	for i := 0; i < historyCapacity(); i++ {
		h.record(int64(i), base.Add(time.Duration(i)*SimStep), map[int]Spot{
			0: spot(float64(i), 0),
			1: spot(0, float64(i)),
		})
	}
	h.forget(0)

	for i := range h.frames {
		if _, still := h.frames[i].spots[0]; still {
			t.Fatalf("frame %d still remembers the place that was freed", i)
		}
		if _, gone := h.frames[i].spots[1]; !gone {
			t.Fatalf("frame %d lost the place nobody freed", i)
		}
	}
	// And a lookup answers about the man who is still here, rather than nothing
	// at all: forgetting one place does not empty the buffer.
	if got := h.at(base.Add(time.Duration(historyCapacity()) * SimStep)); len(got) != 1 {
		t.Fatalf("a rewind after one place was freed returned %d entries, expected the one man left", len(got))
	}
}

func TestHistoryDoesNotResurrectTheDead(t *testing.T) {
	// A rewind must not make a corpse shootable again. Liveness is discrete, so
	// it is not interpolated — and a span in which somebody died counts as dead.
	h := newHistory()
	base := time.Unix(0, 0)
	h.record(1, base, map[int]Spot{0: {Pos: Vec2{}, Alive: true}})
	h.record(2, base.Add(100*time.Millisecond), map[int]Spot{0: {Pos: Vec2{}, Alive: false}})
	if h.at(base.Add(50 * time.Millisecond))[0].Alive {
		t.Fatal("rewound into a span where the target died and found them alive")
	}
}

func TestTheWorldRecordsEveryTick(t *testing.T) {
	w, acc := newTestWorld(t, 5)
	base := time.Unix(0, 0)
	for i := 0; i < 10; i++ {
		w.Advance(SimStep.Seconds(), base.Add(time.Duration(i)*SimStep))
	}
	if w.history.count != 10 {
		t.Fatalf("ten ticks recorded %d frames", w.history.count)
	}
	// Keyed by SLOT, so a rewound world and a published one name the same things
	// — which is what lets a future hit test take the id it was shot at straight
	// from the client's aim, and a slot is the only name a client has for
	// anybody.
	got := w.history.at(base.Add(5 * SimStep))
	if _, ok := got[w.Occupant(acc).Slot]; !ok {
		t.Fatalf("history is keyed by something other than the slot: %+v", got)
	}
}

// tickedHistory fills the world's buffer with `frames` recorded instants, one
// per simulation step, in which the occupant in slot zero stands at X == the
// frame's own index. A rewound X therefore reads back directly as a tick number,
// which is what lets the assertions below be hand-computed arithmetic rather
// than a tolerance.
//
// It returns the instant of the newest frame, which is the `now` a rewind is
// measured back from.
func tickedHistory(t *testing.T, w *World, frames int) time.Time {
	t.Helper()
	base := time.Unix(0, 0)
	for i := 0; i < frames; i++ {
		at := base.Add(time.Duration(i) * SimStep)
		w.history.record(int64(i), at, map[int]Spot{0: spot(float64(i), 0)})
	}
	return base.Add(time.Duration(frames-1) * SimStep)
}

// rewoundTick is where slot zero was, in frame indices, for a player this far
// behind.
func rewoundTick(t *testing.T, w *World, now time.Time, rtt time.Duration) float64 {
	t.Helper()
	got := w.RewindTo(now, rtt)
	s, ok := got[0]
	if !ok {
		t.Fatalf("rewind returned no spot for the only entity there is: %+v", got)
	}
	return s.Pos.X
}

func TestRewindLandsOnTheInstantTheShooterSaw(t *testing.T) {
	// The whole formula, pinned against arithmetic done by hand. A shooter's
	// picture is the WHOLE round trip behind the server — Enqueue derives that
	// number from the snapshot tick he echoes back, so it already counts the trip
	// out, his own frame and the trip home — and his client deliberately draws
	// everything it does not predict InterpolationDelay behind that.
	//
	// 200 ms of loop plus 120 ms of render delay is 320 ms, and at a 50 ms step
	// that is 6.4 frames. From frame 9 the answer is therefore 2.6, exactly.
	// Halving the loop would answer 4.6 — a whole 100 ms, or half a metre at
	// WalkSpeed, in the wrong place.
	w := NewWorld(uuid.New(), 5)
	now := tickedHistory(t, w, 10)

	const rtt = 200 * time.Millisecond
	wantX := 9 - (rtt+InterpolationDelay).Seconds()/SimStep.Seconds()
	if math.Abs(wantX-2.6) > 1e-9 {
		t.Fatalf("the tuning moved: this test's hand-computed 2.6 is now %v", wantX)
	}
	if got := rewoundTick(t, w, now, rtt); math.Abs(got-wantX) > 1e-9 {
		t.Fatalf("rewound to frame %v, expected %v", got, wantX)
	}
}

func TestTheRewindIsTheWholeRoundTripAndNotHalfOfIt(t *testing.T) {
	// The same rule stated so that no arithmetic about the render delay can hide
	// it: adding R to a player's round trip must move his rewind back by R, not
	// by R/2. Measured as a difference, so the interpolation term cancels and the
	// only thing under test is what the loop is worth.
	w := NewWorld(uuid.New(), 5)
	now := tickedHistory(t, w, 10)

	// Small enough that neither end of the comparison is anywhere near the
	// ceiling, which would flatten the difference and pass for the wrong reason.
	const r = 100 * time.Millisecond
	at0 := rewoundTick(t, w, now, 0)
	atR := rewoundTick(t, w, now, r)

	want := r.Seconds() / SimStep.Seconds()
	if got := at0 - atR; math.Abs(got-want) > 1e-9 {
		t.Fatalf("%v of extra round trip moved the rewind %v frames back, expected %v (half of it is %v)",
			r, got, want, want/2)
	}
}

func TestTheRingIsLongEnoughToServeTheDeepestLegalRewind(t *testing.T) {
	// HistoryWindow is the ring's CAPACITY and RewindMax is the bound on the
	// rewind. They are two numbers now, and this is the relationship between
	// them: the oldest frame a full ring holds is (capacity − 1) steps old, and a
	// rewind of exactly RewindMax has to fall strictly INSIDE that span. Land on
	// or past the oldest frame and `at` clamps, which quietly collapses every
	// deep rewind onto the same answer and makes the ceiling look like it is
	// working when it is the ring that ran out.
	//
	// A guard against a future retune rather than a regression test: shrink
	// HistoryWindow or raise RewindMax past one another and nothing else in the
	// package notices.
	oldest := time.Duration(historyCapacity()-1) * SimStep
	if oldest <= RewindMax {
		t.Fatalf("the ring reaches %v into the past and the deepest legal rewind is %v", oldest, RewindMax)
	}
}

func TestRewindMaxStillClearsAnHonestPhone(t *testing.T) {
	// The other side of the ceiling, and the one that is easy to lose sight of
	// while arguing about what a liar buys: a bound that clips HONEST players is
	// a bound that reintroduces the defect it was raised against, because a shot
	// resolved short of where the shooter was aiming misses either way.
	//
	// WHAT HAS TO FIT UNDER IT IS NOT A NETWORK ROUND TRIP. Enqueue derives its
	// sample from the last snapshot tick the client received, so the sample
	// already contains the client's whole send window — input is batched onto a
	// timer at InputHz — and RewindTo then adds InterpolationDelay on top before
	// clamping. Only the remainder is the network.
	window := time.Second / InputHz
	allowance := RewindMax - InterpolationDelay - window

	// RU mobile data is routinely 200 ms and worse outdoors. This is the figure
	// ADR-059 costs the same budget against for «СИМУЛЯТОР ФИНТЕХА».
	const badMobileRoundTrip = 250 * time.Millisecond
	if allowance < badMobileRoundTrip {
		t.Fatalf("a ceiling of %v leaves %v for the network once %v of render delay and %v of send window "+
			"are paid; a bad mobile round trip is %v, so honest shots are being clipped",
			RewindMax, allowance, InterpolationDelay, window, badMobileRoundTrip)
	}
}

func TestRewindIsBoundedByRewindMax(t *testing.T) {
	// The ceiling, and it is stated in metres for a reason: `rtt` is derived from
	// the tick a client chooses to echo, so a client that echoes an ancient one
	// claims to be arbitrarily far behind. RewindMax is 0.5 s, or 2.5 m at
	// WalkSpeed — the worst a liar buys is being resolved against where you stood
	// three and a half body-widths ago. Under the old bound of 1.2 s it was 6 m,
	// which is enough to be shot around a corner by somebody who was nowhere near
	// you.
	w := NewWorld(uuid.New(), 5)
	// Deep enough that the ceiling and not the ring's end is what answers.
	frames := historyCapacity() + 2
	now := tickedHistory(t, w, frames)

	newest := float64(frames - 1)
	wantX := newest - RewindMax.Seconds()/SimStep.Seconds()
	if got := rewoundTick(t, w, now, time.Hour); math.Abs(got-wantX) > 1e-9 {
		t.Fatalf("an hour of claimed latency rewound to frame %v; the ceiling is frame %v", got, wantX)
	}

	// And the floor: no latency at all still draws the render delay, because
	// every client draws at least that far behind.
	wantX = newest - InterpolationDelay.Seconds()/SimStep.Seconds()
	if got := rewoundTick(t, w, now, -time.Hour); math.Abs(got-wantX) > 1e-9 {
		t.Fatalf("a negative latency rewound to frame %v, expected the render delay alone at %v", got, wantX)
	}
}

func TestAClientCannotEchoItsWayPastTheCeiling(t *testing.T) {
	// The other half of the same ceiling. RewindTo clamps the COMPOSITION, and
	// this clamps the SAMPLE the composition is built from — without it a client
	// that simply echoed tick 1 forever would drive the smoothed round trip up to
	// the ring's whole capacity, and the composition's clamp would then be doing
	// all the work with no margin left for the render delay.
	w, acc := newTestWorld(t, 5)
	for i := 0; i < 200; i++ {
		w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	// Claims, repeatedly, to have last drawn the very first tick — ten seconds of
	// staleness against a simulation that is 200 ticks in.
	for i := 0; i < 50; i++ {
		w.Enqueue(acc, &ParsedInput{Seen: 1})
	}
	if got := w.RTT(acc); got > RewindMax {
		t.Fatalf("smoothed round trip settled at %v, the ceiling is %v", got, RewindMax)
	}
	// And it did converge on the ceiling rather than staying near zero, or the
	// assertion above would pass over a clamp that never fired.
	if got := w.RTT(acc); got < RewindMax/2 {
		t.Fatalf("smoothed round trip is only %v, so this test never exercised the clamp", got)
	}
}

func TestRoundTripIsDerivedFromTheEchoedTick(t *testing.T) {
	// Derived rather than trusted: the tick rate is fixed, so the gap between
	// the snapshot a client had drawn and where the simulation is now is the
	// whole loop. A client cannot claim a latency it does not have.
	w, acc := newTestWorld(t, 5)
	for i := 0; i < 20; i++ {
		w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	// Claims to have last drawn tick 10 while the simulation is at 20 — ten
	// ticks, half a second at 20 Hz.
	w.Enqueue(acc, &ParsedInput{Seen: 10, Cmds: []Command{{Seq: 1, Dt: 0.05, MY: 1}}})
	if got := w.RTT(acc); got < 400*time.Millisecond || got > 600*time.Millisecond {
		t.Fatalf("derived round trip %v, expected about 500ms", got)
	}
}

func TestRedundantCommandsAreAppliedOnlyOnce(t *testing.T) {
	// Input redundancy: a client resends unacknowledged commands so one lost
	// packet costs no input. That is only free because a duplicate is dropped —
	// otherwise a resend is movement applied twice on the server and once on
	// the client, which the player feels as being dragged.
	w, acc := newTestWorld(t, 5)
	start := w.Occupant(acc).State.Pos
	cmd := Command{Seq: 1, Dt: 0.05, MY: 1, Yaw: 0}

	w.Enqueue(acc, &ParsedInput{Cmds: []Command{cmd}})
	w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	once := w.Occupant(acc).State.Pos

	// The same command again, three times over, exactly as a redundant frame
	// would carry it.
	for i := 0; i < 3; i++ {
		w.Enqueue(acc, &ParsedInput{Cmds: []Command{cmd}})
		w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}

	if once == start {
		t.Fatal("the first command did nothing, so this test proves nothing")
	}
	if w.Occupant(acc).State.Pos != once {
		t.Fatalf("a resent command moved him again: %+v then %+v", once, w.Occupant(acc).State.Pos)
	}
}

func TestAcknowledgementFollowsWhatWasActuallySimulated(t *testing.T) {
	// The ack is EXACTLY the last sequence folded in whole. A command the budget
	// could not afford must not be acknowledged, because the client drops
	// everything at or below the ack and would keep the whole of a half-run
	// command in its own prediction for ever.
	//
	// This assertion is written as an equality on purpose. It used to read
	// `LastSeq > 1`, which the truncating drain satisfied — that drain
	// acknowledged sequence 1 after simulating a quarter of it, so the test
	// passed green over the exact defect its own comment named.
	w, acc := newTestWorld(t, 5)
	cmds := make([]Command, 0, 8)
	for i := 1; i <= 8; i++ {
		cmds = append(cmds, Command{Seq: int64(i), Dt: MaxStepSeconds, MY: 1})
	}
	w.Enqueue(acc, &ParsedInput{Cmds: cmds})

	// One tick buys 50 ms and every one of these asks for 200, so not one of
	// them is affordable and the ack has to still be zero.
	w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	if got := w.Occupant(acc).State.LastSeq; got != 0 {
		t.Fatalf("acknowledged up to %d before a single command was affordable", got)
	}

	// Four ticks buy the first command exactly, and nothing towards the second.
	// Exact IEEE754 arithmetic rather than a tolerance, so a retune of SimHz or
	// MaxStepSeconds that accumulates one ULP short fails here over the tuning
	// and not over the rule — the full caveat is at
	// TestACommandLargerThanTheBudgetWaitsWholeRatherThanBeingTruncated in
	// world_test.go.
	for i := 0; i < 3; i++ {
		w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	if got := w.Occupant(acc).State.LastSeq; got != 1 {
		t.Fatalf("acknowledged up to %d after a budget worth exactly one command", got)
	}
}

func TestSnapshotCarriesPeersAndATimeline(t *testing.T) {
	// The peers array is omitted entirely while you are alone in the building,
	// which is the common case and the one that should cost nothing. The timeline
	// is what entity interpolation runs on.
	w, acc := newTestWorld(t, 5)
	w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	s, ok := w.SnapshotFor(acc)
	if !ok {
		t.Fatal("no snapshot for the only person in the building")
	}
	if s.Tick == 0 {
		t.Fatal("a snapshot with no tick on it cannot be interpolated between")
	}
	if len(s.Peers) != 0 {
		t.Fatalf("a lone player has peers: %+v", s.Peers)
	}

	// And a second occupant appears as a peer to the first, addressed by the slot
	// he was given — the pseudonym is the standings frame's job and the account
	// id is nobody's.
	other, joined := w.Join("other-account", "other-pseudonym", epoch)
	if !joined {
		t.Fatal("the second occupant was refused")
	}
	w.Advance(SimStep.Seconds(), time.Unix(0, 0))
	s, _ = w.SnapshotFor(acc)
	if len(s.Peers) != 1 {
		t.Fatalf("expected one peer, got %+v", s.Peers)
	}
	if s.Peers[0].Slot != other.Slot {
		t.Fatalf("peer is in slot %d, he was given %d", s.Peers[0].Slot, other.Slot)
	}
	// Nothing on a peer is a name of any kind, which is the whole of what makes
	// the entry 49 bytes rather than 71.
	raw, err := json.Marshal(s.Peers[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"other-pseudonym", "other-account"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("a peer entry carries %q: %s", leaked, raw)
		}
	}
}
