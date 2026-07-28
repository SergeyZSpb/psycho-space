package gamevanyadum

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/google/uuid"
)

// The service is tested with a fake transport and a fake repository, and its
// tick driven by a channel the test fires. Nothing here sleeps: the tick is a
// parameter in production for exactly this reason, so "advance one step and then
// read the frame it caused" is a thing a test can say.

type fakeTransport struct {
	mu      sync.Mutex
	members []realtime.Member
	sent    []sentMsg
	// delivered is signalled after every publish, so a test can wait for a
	// specific frame instead of guessing how long a goroutine needs.
	delivered chan struct{}
}

type sentMsg struct {
	connID string
	body   []byte
}

func newFakeTransport(members ...realtime.Member) *fakeTransport {
	return &fakeTransport{members: members, delivered: make(chan struct{}, 256)}
}

func (f *fakeTransport) PublishTo(_ context.Context, connID string, msg []byte) error {
	f.mu.Lock()
	f.sent = append(f.sent, sentMsg{connID, append([]byte(nil), msg...)})
	f.mu.Unlock()
	select {
	case f.delivered <- struct{}{}:
	default:
	}
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

// framesOfType returns every published frame whose discriminator matches.
func (f *fakeTransport) framesOfType(t string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, m := range f.sent {
		var v map[string]any
		if json.Unmarshal(m.body, &v) == nil && v["t"] == t {
			out = append(out, v)
		}
	}
	return out
}

type fakeRepo struct {
	inserted chan Run
}

func newFakeRepo() *fakeRepo { return &fakeRepo{inserted: make(chan Run, 8)} }

func (f *fakeRepo) InsertRun(_ context.Context, _ db.DBTX, r Run) error {
	f.inserted <- r
	return nil
}

func (f *fakeRepo) RecentRuns(context.Context, db.DBTX, uuid.UUID, int) ([]Run, error) {
	return nil, nil
}

// waitFor pumps the tick until cond holds or the attempts run out. It is the
// synchronisation primitive this file uses instead of a sleep: the loop makes
// progress happen rather than waiting for it.
func waitFor(t *testing.T, tick chan time.Time, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		select {
		case tick <- time.Unix(int64(i), 0):
		case <-time.After(2 * time.Second):
			t.Fatal("service stopped consuming ticks")
		}
	}
	if !cond() {
		t.Fatal("condition never became true")
	}
}

func startService(t *testing.T, tr Transport, repo Repository) (*Service, chan time.Time) {
	t.Helper()
	s := NewService(tr, Room, nil, repo)
	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	go s.Run(ctx, tick)
	t.Cleanup(func() {
		cancel()
		<-s.Done()
	})
	return s, tick
}

func TestASecondRunIsRefusedRatherThanReplacing(t *testing.T) {
	// Silently dropping the existing arena would throw away a run somebody is in
	// the middle of on their other tab, and nothing here can tell which one they
	// meant. So it is a refusal, and the client offers «сдаться».
	s := NewService(newFakeTransport(), Room, nil, newFakeRepo())
	acc := uuid.New()
	if _, err := s.StartRun(acc, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRun(acc, time.Now()); err != ErrRunInProgress {
		t.Fatalf("second start returned %v", err)
	}
	// And giving up is what makes room for the next one.
	s.AbandonRun(acc)
	if _, err := s.StartRun(acc, time.Now()); err != nil {
		t.Fatalf("start after abandoning returned %v", err)
	}
}

func TestArenasAreCapped(t *testing.T) {
	// A capacity limit rather than a rate limit: one arena is one twenty-hertz
	// simulation, and this game shares a box with everything else.
	s := NewService(newFakeTransport(), Room, nil, newFakeRepo())
	for i := 0; i < MaxArenas; i++ {
		if _, err := s.StartRun(uuid.New(), time.Now()); err != nil {
			t.Fatalf("run %d refused: %v", i, err)
		}
	}
	if _, err := s.StartRun(uuid.New(), time.Now()); err != ErrTooManyArenas {
		t.Fatalf("expected the cap to bite, got %v", err)
	}
}

func TestHelloAnswersWithTheRunYouAreIn(t *testing.T) {
	acc := uuid.New()
	tr := newFakeTransport(realtime.Member{ConnID: "c1", AccountID: acc.String()})
	s := NewService(tr, Room, nil, newFakeRepo())
	a, err := s.StartRun(acc, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	s.HandleInbound(context.Background(), realtime.Member{ConnID: "c1", AccountID: acc.String()},
		Room, []byte(`{"t":"vanyadum_hello"}`))

	ready := tr.framesOfType(TypeReady)
	if len(ready) != 1 {
		t.Fatalf("expected one ready frame, got %d", len(ready))
	}
	if ready[0]["run_id"] != a.RunID.String() {
		t.Fatalf("ready names run %v, arena is %v", ready[0]["run_id"], a.RunID)
	}
}

func TestHelloWithNoRunIsSilent(t *testing.T) {
	// Not an error state: a socket that opened before the run did, or outlived
	// it. The client's own next move is what fixes it.
	tr := newFakeTransport()
	s := NewService(tr, Room, nil, newFakeRepo())
	s.HandleInbound(context.Background(),
		realtime.Member{ConnID: "c1", AccountID: uuid.New().String()},
		Room, []byte(`{"t":"vanyadum_hello"}`))
	if got := len(tr.framesOfType(TypeReady)); got != 0 {
		t.Fatalf("answered a hello with no run: %d frames", got)
	}
}

func TestAFrameForAnotherRoomIsIgnored(t *testing.T) {
	// The handler is registered per room by the composition root, but a service
	// that trusted that entirely would misbehave the day two games shared one.
	acc := uuid.New()
	tr := newFakeTransport()
	s := NewService(tr, Room, nil, newFakeRepo())
	if _, err := s.StartRun(acc, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.HandleInbound(context.Background(), realtime.Member{ConnID: "c1", AccountID: acc.String()},
		"yard", []byte(`{"t":"vanyadum_hello"}`))
	if got := len(tr.framesOfType(TypeReady)); got != 0 {
		t.Fatalf("answered a frame addressed to another room: %d", got)
	}
}

func TestInputMovesThePlayerAndTheSnapshotSaysSo(t *testing.T) {
	acc := uuid.New()
	member := realtime.Member{ConnID: "c1", AccountID: acc.String()}
	tr := newFakeTransport(member)
	repo := newFakeRepo()
	s, tick := startService(t, tr, repo)

	a, err := s.StartRun(acc, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	start := a.Player.Pos

	// Walk in every direction in turn, so the test does not depend on which way
	// the spawn happens to face or where the nearest wall is.
	for i := 0; i < 12; i++ {
		yaw := float64(i) * 0.5
		s.HandleInbound(context.Background(), member, Room, mustInput(t, int64(i), yaw))
		tick <- time.Unix(int64(i), 0)
	}

	frames := tr.framesOfType(TypeSnapshot)
	if len(frames) == 0 {
		t.Fatal("no snapshots were published")
	}
	last := frames[len(frames)-1]
	if last["ack"].(float64) < 1 {
		t.Fatalf("ack never advanced: %v", last["ack"])
	}
	if got, want := int(last["x"].(float64)), cm(start.X); got == want {
		if int(last["y"].(float64)) == cm(start.Y) {
			t.Fatal("twelve steps of walking moved nobody")
		}
	}
}

func mustInput(t *testing.T, seq int64, yaw float64) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"t":   TypeInput,
		"seq": seq,
		"cmds": []map[string]any{
			{"dt": 0.05, "my": 1, "yaw": yaw},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBothOfAPlayersDevicesGetTheSnapshot(t *testing.T) {
	// An arena belongs to an ACCOUNT, not to a connection. Two tabs are two
	// views of one run, which is the same rule «Ванягоччи» learned the
	// expensive way when a second device produced a second Ваня.
	acc := uuid.New()
	tr := newFakeTransport(
		realtime.Member{ConnID: "c1", AccountID: acc.String()},
		realtime.Member{ConnID: "c2", AccountID: acc.String()},
	)
	s, tick := startService(t, tr, newFakeRepo())
	if _, err := s.StartRun(acc, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	tick <- time.Unix(1, 0)
	waitFor(t, tick, func() bool { return len(tr.framesOfType(TypeSnapshot)) >= 2 })

	tr.mu.Lock()
	defer tr.mu.Unlock()
	seen := map[string]bool{}
	for _, m := range tr.sent {
		seen[m.connID] = true
	}
	if !seen["c1"] || !seen["c2"] {
		t.Fatalf("only these connections were written to: %v", seen)
	}
}

func TestAFinishedRunIsAnnouncedAndWritten(t *testing.T) {
	acc := uuid.New()
	member := realtime.Member{ConnID: "c1", AccountID: acc.String()}
	tr := newFakeTransport(member)
	repo := newFakeRepo()
	s, tick := startService(t, tr, repo)

	a, err := s.StartRun(acc, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Put the run one beer from finished, BEFORE the first tick is sent.
	//
	// Reaching into the arena is legitimate here — this test is about what the
	// service does when a run ends, and walking there is what the simulation
	// tests already cover — but it is only SAFE before the first tick: the loop
	// touches no arena until a tick arrives, so the channel send is the
	// happens-before edge that makes this ordinary sequential code rather than a
	// race. Mutating between ticks would be neither.
	last := a.Level.Pickups[len(a.Level.Pickups)-1]
	for _, p := range a.Level.Pickups[:len(a.Level.Pickups)-1] {
		a.Taken[p.ID] = true
		a.Player.Counters["beer"]++
	}
	a.Player.Pos, a.Player.Sector = last.Pos, last.Sector

	tick <- time.Unix(1, 0)

	var written Run
	select {
	case written = <-repo.inserted:
	case <-time.After(2 * time.Second):
		t.Fatal("the finished run was never written")
	}
	if !written.Success || written.Seed != a.Level.Seed || written.Beer != len(a.Level.Pickups) {
		t.Fatalf("written run does not match the arena: %+v", written)
	}
	if got := len(tr.framesOfType(TypeOver)); got == 0 {
		t.Fatal("the player was never told the run ended")
	}
	if _, ok := s.CurrentRun(acc); ok {
		t.Fatal("the arena outlived the run")
	}
}

func TestAnAbandonedArenaIsDroppedAndNotWritten(t *testing.T) {
	// A run somebody walked away from is not a result. Recording it would put
	// rows nobody made into the only table this game has — and simulating it
	// forever would be worse.
	acc := uuid.New()
	tr := newFakeTransport() // nobody connected, ever
	repo := newFakeRepo()
	s, tick := startService(t, tr, repo)

	if _, err := s.StartRun(acc, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}

	// The tick carries its own clock, so the grace period is driven to expiry
	// without waiting for it — the same trick the yard's tests use.
	tick <- time.Unix(0, 0)
	tick <- time.Unix(int64(AbandonGrace.Seconds())+1, 0)
	waitFor(t, tick, func() bool { _, ok := s.CurrentRun(acc); return !ok })

	select {
	case r := <-repo.inserted:
		t.Fatalf("an abandoned run was written: %+v", r)
	default:
	}
}

func TestAReconnectingPlayerKeepsHisRun(t *testing.T) {
	// The other side of the same rule, and the reason AbandonGrace is minutes
	// rather than seconds: a page reload, a tunnel, or a phone locking all take
	// a few seconds, and losing the run for any of them would make the game
	// unplayable on a bus.
	acc := uuid.New()
	member := realtime.Member{ConnID: "c1", AccountID: acc.String()}
	tr := newFakeTransport(member)
	s, tick := startService(t, tr, newFakeRepo())
	if _, err := s.StartRun(acc, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}

	tick <- time.Unix(0, 0)
	tr.setMembers(nil) // the socket drops
	tick <- time.Unix(30, 0)
	tr.setMembers([]realtime.Member{member}) // and comes back
	tick <- time.Unix(60, 0)

	if _, ok := s.CurrentRun(acc); !ok {
		t.Fatal("a thirty-second absence lost the run")
	}
}
