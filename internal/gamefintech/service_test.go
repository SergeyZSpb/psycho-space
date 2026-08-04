package gamefintech

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/google/uuid"
)

// The service is tested with a fake transport and a fake repository, and its
// tick driven by a channel the test fires. NOTHING HERE SLEEPS: the tick is a
// parameter in production for exactly this reason, so "advance one step and then
// read the frame it caused" is a thing a test can say.
//
// The tick times are derived from time.Now() rather than from a fixed epoch,
// because how long a shift LASTED is measured against the real clock the handler
// started it on — the tick carries the office's idea of the present, and the two
// have to be the same present.

type fakeTransport struct {
	mu      sync.Mutex
	members []realtime.Member
	sent    []sentMsg
}

type sentMsg struct {
	connID string
	body   []byte
}

func newFakeTransport(members ...realtime.Member) *fakeTransport {
	return &fakeTransport{members: members}
}

func (f *fakeTransport) PublishTo(_ context.Context, connID string, msg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMsg{connID, append([]byte(nil), msg...)})
	return nil
}

func (f *fakeTransport) Members(context.Context, string) ([]realtime.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]realtime.Member(nil), f.members...), nil
}

func (f *fakeTransport) setMembers(m []realtime.Member) {
	f.mu.Lock()
	f.members = m
	f.mu.Unlock()
}

// framesOfType returns every published frame whose discriminator matches, with
// the connection it went to.
func (f *fakeTransport) framesOfType(t string) []struct {
	Conn  string
	Frame map[string]any
} {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []struct {
		Conn  string
		Frame map[string]any
	}
	for _, m := range f.sent {
		var v map[string]any
		if json.Unmarshal(m.body, &v) == nil && v["t"] == t {
			out = append(out, struct {
				Conn  string
				Frame map[string]any
			}{m.connID, v})
		}
	}
	return out
}

type fakeRepo struct {
	inserted chan Shift
	// boardSince is the cutoff the last board read was made with. Recorded rather
	// than asserted here, because what the service owes the repository is a
	// window — and the only way to see it is to look at the argument.
	mu         sync.Mutex
	boardSince time.Time
	// layouts and sources are what InsertLayout was called with, in order.
	layouts []Layout
	sources []string
}

func newFakeRepo() *fakeRepo { return &fakeRepo{inserted: make(chan Shift, 16)} }

// lastBoardSince is the cutoff of the most recent board read, and the zero time
// when no board has been read.
func (f *fakeRepo) lastBoardSince() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.boardSince
}

func (f *fakeRepo) recordBoardSince(since time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.boardSince = since
}

func (f *fakeRepo) InsertShift(_ context.Context, _ db.DBTX, s Shift) error {
	f.inserted <- s
	return nil
}

// The floor. The service reads it once at boot through EnsureLayout rather than
// through this fake — every test here builds a service on an explicit layout —
// so what these two owe is a plausible answer and a record of the write.
func (f *fakeRepo) CurrentLayout(context.Context, db.DBTX) (Layout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.layouts) == 0 {
		return Layout{}, ErrNoLayout
	}
	return f.layouts[len(f.layouts)-1], nil
}

func (f *fakeRepo) InsertLayout(_ context.Context, _ db.DBTX, l Layout, source string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.layouts = append(f.layouts, l)
	f.sources = append(f.sources, source)
	return nil
}

func (f *fakeRepo) RecentShifts(context.Context, db.DBTX, uuid.UUID, int) ([]Shift, error) {
	return nil, nil
}

func (f *fakeRepo) TopShifts(_ context.Context, _ db.DBTX, since time.Time, _ int) ([]Shift, error) {
	f.recordBoardSince(since)
	return nil, nil
}

func (f *fakeRepo) TopShiftsBySeconds(_ context.Context, _ db.DBTX, since time.Time, _ int) ([]Shift, error) {
	f.recordBoardSince(since)
	return nil, nil
}

// harness is a running service with a tick channel the test fires by hand.
// fakeProfiles is the account service, reduced to the one thing this game asks
// of it. Every account has the same picture, which is enough for "the peer got a
// face" and keeps the test from caring which one.
type fakeProfiles struct{ err error }

func (f fakeProfiles) AvatarURL(context.Context, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "https://cdn.example/face.jpg", nil
}

type harness struct {
	svc  *Service
	tr   *fakeTransport
	repo *fakeRepo
	tick chan time.Time
	base time.Time
	n    int
}

func start(t *testing.T, members ...realtime.Member) *harness {
	t.Helper()
	h := &harness{
		tr:   newFakeTransport(members...),
		repo: newFakeRepo(),
		tick: make(chan time.Time),
		base: time.Now(),
	}
	h.svc = NewService(h.tr, Room, nil, h.repo, fakeProfiles{}, testLayout)
	ctx, cancel := context.WithCancel(context.Background())
	go h.svc.Run(ctx, h.tick)
	t.Cleanup(func() {
		cancel()
		<-h.svc.Done()
	})
	return h
}

// step fires one tick, advancing the office's clock by one simulation step.
func (h *harness) step(t *testing.T) {
	t.Helper()
	h.stepAt(t, 0)
}

// stepAt fires one tick, additionally jumping the office's clock forward by
// extra — which is how a ninety-second grace or a three-second shift is reached
// without anything waiting for it.
func (h *harness) stepAt(t *testing.T, extra time.Duration) {
	t.Helper()
	h.n++
	h.base = h.base.Add(extra)
	at := h.base.Add(time.Duration(h.n) * SimStep)
	select {
	case h.tick <- at:
	case <-time.After(5 * time.Second):
		t.Fatal("the service stopped consuming ticks")
	}
}

// pump fires ticks until cond holds or the deadline passes. BOUNDED BY A
// DEADLINE AND NEVER BY AN ATTEMPT COUNT: a loaded CI runner changes how long
// each tick takes, not how many are needed, so a count runs out mid-convergence
// and the test lies about why.
func (h *harness) pump(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		h.step(t)
	}
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestHelloIsAnsweredWithTheShiftYouAreOn(t *testing.T) {
	acc := uuid.New().String()
	m := realtime.Member{ConnID: "c1", AccountID: acc}
	h := start(t, m)

	working, err := h.svc.StartShift(context.Background(), acc)
	if err != nil {
		t.Fatal(err)
	}
	h.svc.HandleInbound(context.Background(), m, Room, []byte(`{"t":"fintech_hello"}`))

	ready := h.tr.framesOfType(TypeReady)
	if len(ready) != 1 {
		t.Fatalf("expected one ready frame, got %d", len(ready))
	}
	if ready[0].Frame["shift_id"] != working.ShiftID {
		t.Fatalf("ready names shift %v, StartShift returned %v", ready[0].Frame["shift_id"], working.ShiftID)
	}
}

func TestTheReadyFrameSaysWhichTickTheShiftStartedOn(t *testing.T) {
	// HOW LONG HAVE I BEEN STANDING HERE, and why it is not on the snapshot.
	//
	// The client draws the shift's age as `k − k0`. `k` is on every frame already
	// and `k0` is constant for the life of the shift, so it is sent once, at
	// attach, and the browser does the subtraction — where an elapsed field on the
	// snapshot would be bytes at twenty hertz per viewer for ever.
	//
	// The office is built by the first StartShift at tick zero, so a k0 that meant
	// anything had to be measured on somebody who clocked in LATE: this drives a
	// first shift for a while, then joins a second account and reads its ready.
	first, late := uuid.New().String(), uuid.New().String()
	m1 := realtime.Member{ConnID: "c1", AccountID: first}
	m2 := realtime.Member{ConnID: "c2", AccountID: late}
	h := start(t, m1)

	if _, err := h.svc.StartShift(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	h.pump(t, "the office to be a few ticks old", func() bool {
		return len(h.tr.framesOfType(TypeSnapshot)) >= 5
	})

	h.tr.setMembers([]realtime.Member{m1, m2})
	if _, err := h.svc.StartShift(context.Background(), late); err != nil {
		t.Fatal(err)
	}
	h.svc.HandleInbound(context.Background(), m2, Room, []byte(`{"t":"fintech_hello"}`))

	var got float64 = -1
	for _, f := range h.tr.framesOfType(TypeReady) {
		if f.Conn == m2.ConnID {
			k0, ok := f.Frame["k0"].(float64)
			if !ok {
				t.Fatalf("the ready frame carries no k0: %+v", f.Frame)
			}
			got = k0
		}
	}
	if got <= 0 {
		t.Fatalf("a shift that started five ticks in was told it began on tick %v", got)
	}
	// And it is the office's own tick, not a count of anything else: the next
	// snapshot addressed to him is at or after it, and the difference is his age.
	for _, f := range h.tr.framesOfType(TypeSnapshot) {
		if f.Conn != m2.ConnID {
			continue
		}
		if k := f.Frame["k"].(float64); k < got {
			t.Fatalf("a snapshot at tick %v is older than the k0 %v it was told", k, got)
		}
	}
}

func TestHelloWithNoShiftIsSilent(t *testing.T) {
	// Not an error state: a socket that opened before the shift did, or one that
	// outlived it. The client's own next move — pressing НАЧАТЬ СМЕНУ — is what
	// fixes it.
	h := start(t)
	h.svc.HandleInbound(context.Background(),
		realtime.Member{ConnID: "c1", AccountID: uuid.New().String()},
		Room, []byte(`{"t":"fintech_hello"}`))
	if got := len(h.tr.framesOfType(TypeReady)); got != 0 {
		t.Fatalf("answered a hello with no shift: %d frames", got)
	}
}

func TestAFrameForAnotherRoomIsIgnored(t *testing.T) {
	// The handler is registered per room by the composition root, but a service
	// that trusted that entirely would misbehave the day two games shared one.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	h.svc.HandleInbound(context.Background(), realtime.Member{ConnID: "c1", AccountID: acc},
		"yard", []byte(`{"t":"fintech_hello"}`))
	if got := len(h.tr.framesOfType(TypeReady)); got != 0 {
		t.Fatalf("answered a frame addressed to another room: %d", got)
	}
}

func TestSnapshotsGoOutOnEverySecondTick(t *testing.T) {
	// The wire runs at half the simulation rate. The client predicts its own
	// position between frames and interpolates his, so nobody can see the
	// difference — and it halves a cost that is per viewer, per tick, forever.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	h.pump(t, "four snapshots", func() bool { return len(h.tr.framesOfType(TypeSnapshot)) >= 4 })

	var last float64 = -1
	for i, f := range h.tr.framesOfType(TypeSnapshot) {
		k, ok := f.Frame["k"].(float64)
		if !ok {
			t.Fatalf("frame %d has no tick: %+v", i, f.Frame)
		}
		if int(k)%SnapshotEvery != 0 {
			t.Fatalf("frame %d describes tick %v, which is not a publishing tick", i, k)
		}
		if last >= 0 && k-last != SnapshotEvery {
			t.Fatalf("frame %d jumped from tick %v to %v", i, last, k)
		}
		last = k
	}
}

func TestBothOfAnAccountsDevicesGetTheSnapshot(t *testing.T) {
	// A shift belongs to an ACCOUNT, not to a connection. Two tabs are two views
	// of one office, which is the rule «Ванягоччи» learned the expensive way
	// when a second device produced a second Ваня.
	acc := uuid.New().String()
	h := start(t,
		realtime.Member{ConnID: "c1", AccountID: acc},
		realtime.Member{ConnID: "c2", AccountID: acc},
	)
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	h.pump(t, "both connections", func() bool {
		seen := map[string]bool{}
		for _, f := range h.tr.framesOfType(TypeSnapshot) {
			seen[f.Conn] = true
		}
		return seen["c1"] && seen["c2"]
	})
}

func TestNobodyElsesConnectionGetsYourFrames(t *testing.T) {
	a, b := uuid.New().String(), uuid.New().String()
	h := start(t,
		realtime.Member{ConnID: "ca", AccountID: a},
		realtime.Member{ConnID: "cb", AccountID: b},
	)
	if _, err := h.svc.StartShift(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	h.pump(t, "a snapshot", func() bool { return len(h.tr.framesOfType(TypeSnapshot)) >= 2 })
	for _, f := range h.tr.framesOfType(TypeSnapshot) {
		if f.Conn == "cb" {
			t.Fatal("somebody who is not working got a snapshot")
		}
	}
}

func TestASecondShiftIsRefusedAndTheFloorIsCapped(t *testing.T) {
	h := start(t)
	ctx := context.Background()
	acc := uuid.New().String()
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.StartShift(ctx, acc); !errors.Is(err, ErrShiftInProgress) {
		t.Fatalf("the second shift returned %v", err)
	}
	for i := 1; i < MaxOccupants; i++ {
		if _, err := h.svc.StartShift(ctx, uuid.New().String()); err != nil {
			t.Fatalf("occupant %d refused: %v", i, err)
		}
	}
	if _, err := h.svc.StartShift(ctx, uuid.New().String()); !errors.Is(err, ErrOfficeFull) {
		t.Fatalf("the cap did not bite: %v", err)
	}
}

func TestARefusedFirstShiftDoesNotLeaveAnEmptyOfficeBehind(t *testing.T) {
	// The office is built by the first StartShift and dropped when the last
	// person leaves, which is what puts the bald man back at the far wall. A
	// refusal that left an empty one behind would leave him wherever the last
	// shift ended.
	h := start(t)
	ctx := context.Background()
	acc := uuid.New().String()
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.LeaveShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	h.svc.mu.Lock()
	office := h.svc.office
	h.svc.mu.Unlock()
	if office != nil {
		t.Fatal("the office outlived its last occupant")
	}
}

func TestCurrentShiftIsTheReloadPath(t *testing.T) {
	h := start(t)
	ctx := context.Background()
	acc := uuid.New().String()
	if _, ok := h.svc.CurrentShift(acc); ok {
		t.Fatal("an account that has never played is working")
	}
	started, err := h.svc.StartShift(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := h.svc.CurrentShift(acc)
	if !ok || got.ShiftID != started.ShiftID {
		t.Fatalf("CurrentShift = %q, %v; want %q", got.ShiftID, ok, started.ShiftID)
	}
	// And it names the floor the shift is being played on, which is what a
	// reloaded tab compares its cached catalogue against.
	if got.OfficeID == "" || got.OfficeID != testLayout.ID {
		t.Fatalf("CurrentShift says the office is %q, the service was built on %q", got.OfficeID, testLayout.ID)
	}
	if err := h.svc.LeaveShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.svc.CurrentShift(acc); ok {
		t.Fatal("the shift survived being walked out of")
	}
}

func TestWalkingOutWritesTheShiftAndTellsEveryDevice(t *testing.T) {
	acc := uuid.New().String()
	h := start(t,
		realtime.Member{ConnID: "c1", AccountID: acc},
		realtime.Member{ConnID: "c2", AccountID: acc},
	)
	ctx := context.Background()
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	// Long enough to be worth recording. The clock is the one the handler reads,
	// so this is the one place a test has to reach real time — and it does it by
	// backdating the start rather than by waiting.
	h.svc.mu.Lock()
	h.svc.office.mu.Lock()
	h.svc.office.occupants[acc].StartedAt = time.Now().Add(-time.Minute)
	h.svc.office.occupants[acc].State.Salary = 4242
	h.svc.office.mu.Unlock()
	h.svc.mu.Unlock()

	if err := h.svc.LeaveShift(ctx, acc); err != nil {
		t.Fatal(err)
	}

	select {
	case written := <-h.repo.inserted:
		if written.Cause != CauseLeft {
			t.Fatalf("written with cause %q", written.Cause)
		}
		if written.Salary != 4242 {
			t.Fatalf("written salary %v", written.Salary)
		}
		if written.Seconds < 59 {
			t.Fatalf("written seconds %v", written.Seconds)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("walking out never wrote the shift")
	}

	over := h.tr.framesOfType(TypeOver)
	if len(over) != 2 {
		t.Fatalf("%d over frames for two devices", len(over))
	}
	for _, f := range over {
		if f.Frame["cause"] != CauseLeft {
			t.Fatalf("the over frame says %v", f.Frame["cause"])
		}
		if f.Frame["pay"].(float64) != 4242 {
			t.Fatalf("the over frame pays %v", f.Frame["pay"])
		}
	}
}

func TestAShiftShorterThanTheMinimumIsNotWritten(t *testing.T) {
	// An accidental tap that starts and ends a shift in a second is not a
	// result, and a leaderboard full of them is noise. The player is still told
	// it ended — the over frame does not depend on the row.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	ctx := context.Background()
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.LeaveShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	select {
	case written := <-h.repo.inserted:
		t.Fatalf("a shift lasting %v seconds was recorded: %+v", written.Seconds, written)
	default:
	}
	if got := len(h.tr.framesOfType(TypeOver)); got != 1 {
		t.Fatalf("%d over frames — the player was not told", got)
	}
}

func TestBothBoardsAreReadWithTheWeekWindow(t *testing.T) {
	// THE BOARD AGES BY ITSELF, and this is where that is asserted: the service
	// hands the repository a cutoff of BoardWindow ago on every read, so a record
	// leaves the board a week after it was set with nothing scheduled and nothing
	// swept. Both boards, because two nearly-identical methods are exactly where
	// one of them gets to keep the old behaviour unnoticed.
	h := start(t)
	ctx := context.Background()
	for _, board := range []struct {
		name string
		read func() ([]Shift, error)
	}{
		{"salary", func() ([]Shift, error) { return h.svc.TopShifts(ctx, 5) }},
		{"seconds", func() ([]Shift, error) { return h.svc.TopShiftsBySeconds(ctx, 5) }},
	} {
		before := time.Now()
		if _, err := board.read(); err != nil {
			t.Fatalf("%s board: %v", board.name, err)
		}
		since := h.repo.lastBoardSince()
		// Bounded by the two clock readings around the call rather than by an
		// equality against a third: `time.Now()` moves between them, so the only
		// true statement is that the cutoff is BoardWindow before some instant
		// inside the call.
		if since.Before(before.Add(-BoardWindow)) || since.After(time.Now().Add(-BoardWindow)) {
			t.Fatalf("the %s board was read from %v, which is not %v ago", board.name, since, BoardWindow)
		}
	}
}

func TestTheBoardWindowIsAWeekAndIsPublished(t *testing.T) {
	// The number the SQL filters on and the number the splash screen states are
	// the same number, and the cheatsheet is generated from the served config —
	// so a window retuned in one place and not the other would be a screen that
	// lies about the rule.
	if BoardWindow != time.Duration(BoardWindowDays)*24*time.Hour {
		t.Fatalf("BoardWindow is %v, which is not %d days", BoardWindow, BoardWindowDays)
	}
	if got := BuildConfig(testLayout).Board.WindowDays; got != BoardWindowDays {
		t.Fatalf("the served board window is %d days, want %d", got, BoardWindowDays)
	}
}

func TestLeavingAShiftThatIsNotThere(t *testing.T) {
	h := start(t)
	ctx := context.Background()
	if err := h.svc.LeaveShift(ctx, uuid.New().String()); !errors.Is(err, ErrNoShift) {
		t.Fatalf("leaving an empty office returned %v", err)
	}
	acc := uuid.New().String()
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.LeaveShift(ctx, uuid.New().String()); !errors.Is(err, ErrNoShift) {
		t.Fatal("leaving somebody else's shift succeeded")
	}
}

func TestBeingCaughtEndsTheShiftAndWritesIt(t *testing.T) {
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	// Backdate the start so the shift is worth recording when he arrives, which
	// he does after about six and a half seconds of simulated time.
	h.svc.mu.Lock()
	h.svc.office.mu.Lock()
	h.svc.office.occupants[acc].StartedAt = time.Now().Add(-time.Minute)
	h.svc.office.mu.Unlock()
	h.svc.mu.Unlock()

	h.pump(t, "the over frame", func() bool { return len(h.tr.framesOfType(TypeOver)) > 0 })

	over := h.tr.framesOfType(TypeOver)[0].Frame
	if over["cause"] != CausePromoted {
		t.Fatalf("the shift ended as %v", over["cause"])
	}
	if over["pay"].(float64) <= 0 {
		t.Fatalf("standing still until he arrived paid %v", over["pay"])
	}
	select {
	case written := <-h.repo.inserted:
		if written.Cause != CausePromoted {
			t.Fatalf("written with cause %q", written.Cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("being caught never wrote the shift")
	}
	if _, ok := h.svc.CurrentShift(acc); ok {
		t.Fatal("the shift outlived the promotion")
	}
}

func TestAnOccupantWhoseConnectionNeverComesBackIsEndedAndWritten(t *testing.T) {
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	h.svc.mu.Lock()
	h.svc.office.mu.Lock()
	h.svc.office.occupants[acc].StartedAt = time.Now().Add(-time.Minute)
	h.svc.office.mu.Unlock()
	h.svc.mu.Unlock()

	h.step(t)
	h.tr.setMembers(nil) // the socket drops and never comes back

	// The tick carries its own clock, so the grace is driven to expiry without
	// anything waiting for it.
	h.stepAt(t, AbandonGrace+time.Second)

	select {
	case written := <-h.repo.inserted:
		if written.Cause != CauseLeft {
			t.Fatalf("an abandoned shift was written as %q", written.Cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned shift was never written")
	}
}

func TestAReconnectingPlayerKeepsTheirShift(t *testing.T) {
	// A page reload, a tunnel, or a phone locking all take a few seconds, and
	// ending the shift for any of them would make the game unplayable on a bus.
	acc := uuid.New().String()
	m := realtime.Member{ConnID: "c1", AccountID: acc}
	h := start(t, m)
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	h.tr.setMembers(nil)
	h.stepAt(t, AbandonGrace/2)
	h.tr.setMembers([]realtime.Member{m})
	h.stepAt(t, AbandonGrace/2)
	h.step(t)

	if _, ok := h.svc.CurrentShift(acc); !ok {
		t.Fatal("a gap shorter than the grace lost the shift")
	}
}

func TestInputMovesThePlayerAndTheSnapshotSaysSo(t *testing.T) {
	acc := uuid.New().String()
	m := realtime.Member{ConnID: "c1", AccountID: acc}
	h := start(t, m)
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	h.pump(t, "a first snapshot", func() bool { return len(h.tr.framesOfType(TypeSnapshot)) > 0 })
	start := h.tr.framesOfType(TypeSnapshot)[0].Frame["x"].(float64)

	for i := 1; i <= 8; i++ {
		h.svc.HandleInbound(context.Background(), m, Room,
			[]byte(`{"t":"fintech_input","k":0,"cmds":[{"q":`+itoa(i)+`,"dt":0.05,"mx":1}]}`))
		h.step(t)
	}
	h.pump(t, "the player to move", func() bool {
		frames := h.tr.framesOfType(TypeSnapshot)
		last := frames[len(frames)-1].Frame
		return last["x"].(float64) > start && last["ack"].(float64) > 0
	})
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestPurgeAccountIsIdempotentAndWritesNothing(t *testing.T) {
	// The admin «забыть» path calls it TWICE on purpose — before and after the
	// anonymising statement — so it must be idempotent, safe for an account it
	// has never seen, and non-blocking. A shift belonging to somebody who is
	// being erased is not a result, so nothing is recorded.
	h := start(t)
	ctx := context.Background()
	acc := uuid.New().String()
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	h.svc.mu.Lock()
	h.svc.office.mu.Lock()
	h.svc.office.occupants[acc].StartedAt = time.Now().Add(-time.Hour)
	h.svc.office.occupants[acc].State.Salary = 99999
	h.svc.office.mu.Unlock()
	h.svc.mu.Unlock()

	h.svc.PurgeAccount(acc)
	h.svc.PurgeAccount(acc)
	h.svc.PurgeAccount(uuid.New().String())
	h.svc.PurgeAccount("not even a uuid")

	if _, ok := h.svc.CurrentShift(acc); ok {
		t.Fatal("the purged account is still working")
	}
	if got := len(h.svc.saves); got != 0 {
		t.Fatalf("%d shifts were queued for writing by a purge", got)
	}
	select {
	case written := <-h.repo.inserted:
		t.Fatalf("a purged shift was recorded: %+v", written)
	default:
	}
	// And the account can play again afterwards, which is what "idempotent"
	// has to mean for something called around a mutation.
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatalf("a purged account cannot start again: %v", err)
	}
}

func TestAFullSaveQueueDropsRatherThanBlocking(t *testing.T) {
	// Stalling a twenty-hertz simulation for everybody in order to record one
	// summary row is the wrong trade. The log line is what stops the loss being
	// silent.
	s := NewService(newFakeTransport(), Room, nil, newFakeRepo(), fakeProfiles{}, testLayout)
	for i := 0; i < savesBuffer; i++ {
		s.saves <- Shift{ID: uuid.New()}
	}
	occ := &Occupant{
		AccountID: uuid.New().String(),
		ShiftID:   uuid.New().String(),
		StartedAt: time.Now().Add(-time.Minute),
	}

	done := make(chan struct{})
	go func() {
		s.finish(occ, time.Now())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("finish blocked on a full save queue")
	}
	if got := len(s.saves); got != savesBuffer {
		t.Fatalf("the queue grew past its buffer: %d", got)
	}
}

func TestTheWriterDrainsOnShutdown(t *testing.T) {
	// A deploy in the middle of somebody's best shift should still record it.
	// The context is gone by then, so the writes get a fresh short-lived one.
	repo := newFakeRepo()
	s := NewService(newFakeTransport(), Room, nil, repo, fakeProfiles{}, testLayout)
	queued := Shift{ID: uuid.New(), AccountID: uuid.New(), Cause: CauseLeft, Salary: 1234, Seconds: 42}
	s.saves <- queued

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.persistShifts(ctx) // returns only once the queue is empty

	select {
	case got := <-repo.inserted:
		if got.ID != queued.ID {
			t.Fatalf("drained %+v, queued %+v", got, queued)
		}
	default:
		t.Fatal("the queued shift was thrown away on shutdown")
	}
}

func TestConfigIsTheCatalogue(t *testing.T) {
	h := start(t)
	if got := h.svc.Config(); got.GameKey != GameKey || len(got.Office.Solids) != len(testLayout.Solids) {
		t.Fatalf("the service serves something other than the catalogue: %+v", got)
	}
}

func TestListLimitsAreClamped(t *testing.T) {
	// The limit is a query parameter, so it is a client-controlled bound on a
	// query. Nothing on the wire says these numbers; they only stop a `?limit=`
	// of a million.
	for _, tc := range []struct{ in, want int }{
		{0, DefaultShiftLimit}, {-3, DefaultShiftLimit},
		{5, 5}, {MaxShiftLimit, MaxShiftLimit}, {1000, MaxShiftLimit},
	} {
		if got := clampLimit(tc.in); got != tc.want {
			t.Fatalf("clampLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestRecentShiftsRefusesAnAccountThatIsNotAUUID(t *testing.T) {
	h := start(t)
	if _, err := h.svc.RecentShifts(context.Background(), "not a uuid", 10); err == nil {
		t.Fatal("a malformed account id reached the database")
	}
}

func TestAShiftStartsEvenWhenTheFaceWillNotLoad(t *testing.T) {
	// The account service is a database round trip and this is a game. A person
	// must not be refused work because their picture would not load — the office
	// simply draws a plain figure, which is the state it shipped in.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	h.svc.profiles = fakeProfiles{err: errors.New("the account service is having a moment")}
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatalf("a failing avatar lookup stopped the shift: %v", err)
	}
	if _, ok := h.svc.CurrentShift(acc); !ok {
		t.Fatal("no shift after a failed avatar load")
	}
}

func TestTheFaceIsReadOnceAtTheStartAndNotOnEveryTick(t *testing.T) {
	// It is constant for the life of a shift and it is a query. Reading it per
	// snapshot would be a database round trip ten times a second per player.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	counting := &countingProfiles{}
	h.svc.profiles = counting
	if _, err := h.svc.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	h.pump(t, "a snapshot", func() bool { return len(h.tr.framesOfType(TypeSnapshot)) >= 5 })
	if n := counting.n.Load(); n != 1 {
		t.Fatalf("the avatar was read %d times for one shift", n)
	}
}

type countingProfiles struct{ n atomic.Int64 }

func (c *countingProfiles) AvatarURL(context.Context, string) (string, error) {
	c.n.Add(1)
	return "https://cdn.example/face.jpg", nil
}
