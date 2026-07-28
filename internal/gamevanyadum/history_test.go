package gamevanyadum

import (
	"math"
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
	h.record(1, base, map[string]Spot{"a": spot(0, 0)})
	h.record(2, base.Add(100*time.Millisecond), map[string]Spot{"a": spot(10, 20)})

	got := h.at(base.Add(50 * time.Millisecond))
	if math.Abs(got["a"].Pos.X-5) > 1e-9 || math.Abs(got["a"].Pos.Y-10) > 1e-9 {
		t.Fatalf("halfway between the two frames is %+v", got["a"].Pos)
	}
}

func TestHistoryClampsRatherThanFabricating(t *testing.T) {
	// Both ends of the window clamp to a frame that really happened. The
	// alternative is inventing a position, and a hit registered against
	// something that was never there is worse than a hit that misses.
	h := newHistory()
	base := time.Unix(100, 0)
	h.record(1, base, map[string]Spot{"a": spot(1, 1)})
	h.record(2, base.Add(100*time.Millisecond), map[string]Spot{"a": spot(9, 9)})

	if got := h.at(base.Add(-time.Hour)); got["a"].Pos.X != 1 {
		t.Fatalf("before the window should clamp to the oldest frame, got %+v", got["a"].Pos)
	}
	if got := h.at(base.Add(time.Hour)); got["a"].Pos.X != 9 {
		t.Fatalf("after the window should clamp to the newest frame, got %+v", got["a"].Pos)
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
		h.record(int64(i), base.Add(time.Duration(i)*SimStep), map[string]Spot{"a": spot(float64(i), 0)})
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
		h.record(int64(i), base.Add(time.Duration(i)*SimStep), map[string]Spot{"a": spot(float64(i), 0)})
	}
	newest := h.at(base.Add(time.Duration(n) * SimStep))
	if want := float64(n - 1); newest["a"].Pos.X != want {
		t.Fatalf("newest frame is %v, expected %v", newest["a"].Pos.X, want)
	}
	// And a point midway through the surviving window is still interpolated
	// rather than clamped.
	mid := h.at(base.Add(time.Duration(n-3)*SimStep + SimStep/2))
	if mid["a"].Pos.X <= float64(n-4) || mid["a"].Pos.X >= float64(n-2) {
		t.Fatalf("midway lookup after wrap returned %v", mid["a"].Pos.X)
	}
}

func TestHistoryDoesNotResurrectTheDead(t *testing.T) {
	// A rewind must not make a corpse shootable again. Liveness is discrete, so
	// it is not interpolated — and a span in which somebody died counts as dead.
	h := newHistory()
	base := time.Unix(0, 0)
	h.record(1, base, map[string]Spot{"a": {Pos: Vec2{}, Alive: true}})
	h.record(2, base.Add(100*time.Millisecond), map[string]Spot{"a": {Pos: Vec2{}, Alive: false}})
	if h.at(base.Add(50 * time.Millisecond))["a"].Alive {
		t.Fatal("rewound into a span where the target died and found them alive")
	}
}

func TestArenaRecordsEveryTick(t *testing.T) {
	a := newTestArena(t, 5)
	base := time.Unix(0, 0)
	for i := 0; i < 10; i++ {
		a.Advance(SimStep.Seconds(), base.Add(time.Duration(i)*SimStep))
	}
	if a.history.count != 10 {
		t.Fatalf("ten ticks recorded %d frames", a.history.count)
	}
	// Keyed by PSEUDONYM, so a rewound world and a published one name the same
	// things — which is what lets a future hit test take the id it was shot at
	// straight from the client's aim.
	got := a.history.at(base.Add(5 * SimStep))
	if _, ok := got[a.Owner().Pseudonym]; !ok {
		t.Fatalf("history is keyed by something other than the pseudonym: %+v", got)
	}
}

func TestRewindGoesBackByLatencyPlusInterpolation(t *testing.T) {
	// The formula that makes lag compensation correct: a shooter's picture is
	// one-way latency behind the server, and their client deliberately draws
	// peers InterpolationDelay behind that. Rewinding by only the round trip
	// would still miss by the interpolation window.
	a := NewArena(uuid.New(), "acct", "pseudo", 5, time.Unix(0, 0))
	base := time.Unix(0, 0)
	for i := 0; i < historyCapacity()-1; i++ {
		at := base.Add(time.Duration(i) * SimStep)
		a.history.record(int64(i), at, map[string]Spot{"pseudo": spot(float64(i), 0)})
	}
	now := base.Add(time.Duration(historyCapacity()-2) * SimStep)

	const rtt = 200 * time.Millisecond
	got := a.RewindTo(now, rtt)
	behind := rtt/2 + InterpolationDelay
	wantX := float64(historyCapacity()-2) - behind.Seconds()/SimStep.Seconds()

	if math.Abs(got["pseudo"].Pos.X-wantX) > 0.5 {
		t.Fatalf("rewound to %v, expected about %v", got["pseudo"].Pos.X, wantX)
	}
	// And it is genuinely in the past — the commonest way to get this wrong is
	// to rewind by nothing at all and not notice.
	if got["pseudo"].Pos.X >= float64(historyCapacity()-2) {
		t.Fatal("rewind returned the present")
	}
}

func TestRewindClampsAHostileLatency(t *testing.T) {
	// The round trip is derived rather than reported for exactly this reason,
	// but the clamp is here as well: a player who could claim a huge latency
	// could shoot at a world seconds old, which is a way to kill somebody who
	// has been behind cover the whole time.
	a := NewArena(uuid.New(), "acct", "pseudo", 5, time.Unix(0, 0))
	base := time.Unix(0, 0)
	for i := 0; i < 10; i++ {
		a.history.record(int64(i), base.Add(time.Duration(i)*SimStep), map[string]Spot{"pseudo": spot(float64(i), 0)})
	}
	now := base.Add(9 * SimStep)
	if got := a.RewindTo(now, time.Hour); got["pseudo"].Pos.X != 0 {
		t.Fatalf("an hour of claimed latency should clamp to the oldest frame, got %v", got["pseudo"].Pos.X)
	}
	if got := a.RewindTo(now, -time.Hour); len(got) == 0 {
		t.Fatal("a negative latency should be treated as none, not panic or empty")
	}
}

func TestRoundTripIsDerivedFromTheEchoedTick(t *testing.T) {
	// Derived rather than trusted: the tick rate is fixed, so the gap between
	// the snapshot a client had drawn and where the simulation is now is the
	// whole loop. A client cannot claim a latency it does not have.
	a := newTestArena(t, 5)
	for i := 0; i < 20; i++ {
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}
	// Claims to have last drawn tick 10 while the simulation is at 20 — ten
	// ticks, half a second at 20 Hz.
	a.Enqueue(a.AccountID, &ParsedInput{Seen: 10, Cmds: []Command{{Seq: 1, Dt: 0.05, MY: 1}}})
	if got := a.RTT(a.AccountID); got < 400*time.Millisecond || got > 600*time.Millisecond {
		t.Fatalf("derived round trip %v, expected about 500ms", got)
	}
}

func TestRedundantCommandsAreAppliedOnlyOnce(t *testing.T) {
	// Input redundancy: a client resends unacknowledged commands so one lost
	// packet costs no input. That is only free because a duplicate is dropped —
	// otherwise a resend is movement applied twice on the server and once on
	// the client, which the player feels as being dragged.
	a := newTestArena(t, 5)
	start := a.Owner().State.Pos
	cmd := Command{Seq: 1, Dt: 0.05, MY: 1, Yaw: 0}

	a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{cmd}})
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	once := a.Owner().State.Pos

	// The same command again, three times over, exactly as a redundant frame
	// would carry it.
	for i := 0; i < 3; i++ {
		a.Enqueue(a.AccountID, &ParsedInput{Cmds: []Command{cmd}})
		a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	}

	if once == start {
		t.Fatal("the first command did nothing, so this test proves nothing")
	}
	if a.Owner().State.Pos != once {
		t.Fatalf("a resent command moved him again: %+v then %+v", once, a.Owner().State.Pos)
	}
}

func TestAcknowledgementFollowsWhatWasActuallySimulated(t *testing.T) {
	// A command truncated by the time budget must NOT be acknowledged as
	// applied — the client would drop it from its pending list and its
	// prediction would drift permanently by whatever the server declined to
	// simulate.
	a := newTestArena(t, 5)
	cmds := make([]Command, 0, 8)
	for i := 1; i <= 8; i++ {
		cmds = append(cmds, Command{Seq: int64(i), Dt: MaxStepSeconds, MY: 1})
	}
	a.Enqueue(a.AccountID, &ParsedInput{Cmds: cmds})
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))

	// One tick buys one tick of simulated time, which is a fraction of the
	// first command — so at most the first can have been acknowledged.
	if got := a.Owner().State.LastSeq; got > 1 {
		t.Fatalf("acknowledged up to %d after a single tick's budget", got)
	}
}

func TestSnapshotCarriesPeersAndATimeline(t *testing.T) {
	// Both are on the wire before anything fills them, because adding a field
	// to a live protocol is a coordinated deploy and an empty array is two
	// bytes. The timeline is what entity interpolation runs on.
	a := newTestArena(t, 5)
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	s, ok := a.SnapshotFor(a.AccountID)
	if !ok {
		t.Fatal("no snapshot for the arena's own owner")
	}
	if s.Tick == 0 {
		t.Fatal("a snapshot with no tick on it cannot be interpolated between")
	}
	if len(s.Peers) != 0 {
		t.Fatalf("a lone player has peers: %+v", s.Peers)
	}

	// And a second occupant appears as a peer to the first, with a pseudonym
	// rather than an account id.
	a.Join("other-account", "other-pseudonym")
	a.Advance(SimStep.Seconds(), time.Unix(0, 0))
	s, _ = a.SnapshotFor(a.AccountID)
	if len(s.Peers) != 1 {
		t.Fatalf("expected one peer, got %+v", s.Peers)
	}
	if s.Peers[0].ID != "other-pseudonym" {
		t.Fatalf("peer is named %q", s.Peers[0].ID)
	}
	if s.Peers[0].ID == "other-account" {
		t.Fatal("an account id reached the wire")
	}
}
