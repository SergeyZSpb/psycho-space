package gamevanyagotchi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
)

// fakeTransport stands in for the hub. Every property of the broadcast worth
// asserting is reachable without a socket, which is what keeps these tests
// deterministic — the tick is a channel the test fires, so there is never a
// "wait for the next broadcast" sleep anywhere in this file.
type fakeTransport struct {
	mu         sync.Mutex
	members    []realtime.Member
	published  [][]byte
	membersErr error
	publishErr error
}

func (f *fakeTransport) setMembers(ms ...realtime.Member) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members = ms
}

func (f *fakeTransport) Members(_ context.Context, _ string) ([]realtime.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.membersErr != nil {
		return nil, f.membersErr
	}
	return append([]realtime.Member(nil), f.members...), nil
}

func (f *fakeTransport) Publish(_ context.Context, _ string, msg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, append([]byte(nil), msg...))
	return nil
}

func (f *fakeTransport) frames() []Roster {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Roster, 0, len(f.published))
	for _, raw := range f.published {
		var r Roster
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

func member(id string) realtime.Member {
	return realtime.Member{ConnID: id, AccountID: "acct-" + id}
}

// peerByID finds one entity in a frame.
func peerByID(r Roster, id string) (Peer, bool) {
	for _, p := range r.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return Peer{}, false
}

const testRoom = "yard"

// TestBroadcastPlacesANewConnectionAtTheSpawn covers the first frame somebody
// appears in: they have never sent a move, and they still have to be somewhere,
// or a peer's client would have to invent a position for them.
func TestBroadcastPlacesANewConnectionAtTheSpawn(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom)

	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	frames := tr.frames()
	if len(frames) != 1 {
		t.Fatalf("published %d frames; want 1", len(frames))
	}
	if frames[0].T != TypeRoster {
		t.Fatalf("frame type = %q; want %q", frames[0].T, TypeRoster)
	}
	p, ok := peerByID(frames[0], "a")
	if !ok {
		t.Fatalf("frame does not mention the connected peer: %+v", frames[0])
	}
	if p.X != spawn.X || p.Y != spawn.Y {
		t.Fatalf("peer at (%v,%v); want the spawn point (%v,%v)", p.X, p.Y, spawn.X, spawn.Y)
	}
}

// TestMoveShowsUpInTheNextFrame is the whole point of the iteration: what one
// player does has to become visible to the others.
func TestMoveShowsUpInTheNextFrame(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.1,"y":0.9}`))
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	f := tr.frames()[0]
	if len(f.Peers) != 2 {
		t.Fatalf("frame carries %d peers; want both", len(f.Peers))
	}
	moved, _ := peerByID(f, "a")
	if moved.X != 0.1 || moved.Y != 0.9 {
		t.Fatalf("mover at (%v,%v); want (0.1,0.9)", moved.X, moved.Y)
	}
	still, _ := peerByID(f, "b")
	if still.X != spawn.X || still.Y != spawn.Y {
		t.Fatalf("the other peer moved to (%v,%v) without asking", still.X, still.Y)
	}
}

// TestEveryFrameIsFullState pins the property the hub's backpressure depends on.
// The hub drops frames for a client that is behind, so a frame that described
// only what changed would leave that client rendering a world that no longer
// exists — undetectably. Nothing moves here, and the second frame must still
// describe everybody.
func TestEveryFrameIsFullState(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom)

	for i := 0; i < 3; i++ {
		if err := svc.broadcast(context.Background()); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
	}

	for i, f := range tr.frames() {
		if len(f.Peers) != 2 {
			t.Fatalf("frame %d carries %d peers; every frame must carry all of them", i, len(f.Peers))
		}
	}
}

// TestLeavingRemovesAPeerAndItsPosition covers both halves of a disconnect. The
// peer must vanish from the frame, and the position must not be kept for a
// connection id that will never come back — a map that only ever grows is a leak
// on a long-running process, and a reused id would inherit a stale position.
func TestLeavingRemovesAPeerAndItsPosition(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.3}`))
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	tr.setMembers(member("b"))
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast after leave: %v", err)
	}

	f := tr.frames()[1]
	if _, ok := peerByID(f, "a"); ok {
		t.Fatalf("a disconnected peer is still in the frame: %+v", f)
	}
	svc.mu.Lock()
	_, kept := svc.pos["a"]
	svc.mu.Unlock()
	if kept {
		t.Fatal("the position of a disconnected peer was kept")
	}
}

// TestAnEmptyRoomPublishesNothing keeps the common case free. Nobody is
// connected for most of the day, and publishing to an empty room five times a
// second would be pure waste — and would hide a genuine "why is this room
// empty" question behind traffic.
func TestAnEmptyRoomPublishesNothing(t *testing.T) {
	tr := &fakeTransport{}
	svc := NewService(tr, testRoom)

	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if n := len(tr.frames()); n != 0 {
		t.Fatalf("published %d frames into an empty room; want none", n)
	}
}

// TestAnEmptyRoomForgetsPositions guards the leak the test above cannot see: if
// everybody leaves at once there is no member list left to rebuild against, so
// the positions have to be dropped explicitly or they stay forever.
func TestAnEmptyRoomForgetsPositions(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.4,"y":0.4}`))
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	tr.setMembers()
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast into an empty room: %v", err)
	}
	svc.mu.Lock()
	n := len(svc.pos)
	svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d positions survived an empty room; want 0", n)
	}
}

// TestRejectedFramesLeaveThePositionAlone is the substance of "the server owns
// truth". A malformed or out-of-protocol frame must not move anybody, and above
// all must not reset them to the spawn — a client could otherwise teleport a
// rival home by sending nonsense.
func TestRejectedFramesLeaveThePositionAlone(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.8,"y":0.2}`))
	for _, bad := range []string{
		`not json`,
		`{"t":"vanyagotchi_move"}`,
		`{"t":"vanyagotchi_move","x":0.1}`,
		`{"t":"something_else","x":0.1,"y":0.1}`,
		`{"t":"vanyagotchi_move","x":"0.1","y":0.1}`,
	} {
		svc.HandleInbound(context.Background(), member("a"), testRoom, []byte(bad))
	}

	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	p, _ := peerByID(tr.frames()[0], "a")
	if p.X != 0.8 || p.Y != 0.2 {
		t.Fatalf("peer at (%v,%v) after bad frames; want the last good move (0.8,0.2)", p.X, p.Y)
	}
}

// TestFramesFromAnotherRoomAreIgnored matters because the handler is registered
// on the transport, not on a room: it is handed everything that arrives. A game
// acting on a frame from a room it does not own would be one realtime feature
// reaching into another.
func TestFramesFromAnotherRoomAreIgnored(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), member("a"), "somewhere-else",
		[]byte(`{"t":"vanyagotchi_move","x":0.9,"y":0.9}`))
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	p, _ := peerByID(tr.frames()[0], "a")
	if p.X != spawn.X || p.Y != spawn.Y {
		t.Fatalf("a frame from another room moved the peer to (%v,%v)", p.X, p.Y)
	}
}

// TestRunPublishesOnEachTick proves the loop is driven by the injected channel
// and nothing else — no internal timer, so a test never waits on wall-clock time
// and a production tick that is late produces the same frame.
func TestRunPublishesOnEachTick(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx, tick) }()

	for i := 1; i <= 3; i++ {
		tick <- time.Time{}
		waitForCount(t, tr, i)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
}

// TestRunStopsWhenTheHubIsGone covers the deploy path. The hub drains before the
// HTTP server on every restart, so this loop outlives it by a moment; it must
// stop rather than spin logging a failure five times a second until the process
// dies.
func TestRunStopsWhenTheHubIsGone(t *testing.T) {
	tr := &fakeTransport{membersErr: realtime.ErrHubClosed}
	svc := NewService(tr, testRoom)

	tick := make(chan time.Time, 1)
	tick <- time.Time{}
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(context.Background(), tick) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going after the hub reported itself closed")
	}
}

// TestRunSurvivesATransientPublishFailure is the other side of that: an ordinary
// error must not kill the loop, or one bad moment would silently end the game
// for everybody until the next deploy.
func TestRunSurvivesATransientPublishFailure(t *testing.T) {
	tr := &fakeTransport{publishErr: errors.New("transient")}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx, tick) }()

	// Two ticks accepted means the loop is still running after the first failed.
	for i := 0; i < 2; i++ {
		select {
		case tick <- time.Time{}:
		case <-done:
			t.Fatal("Run exited on a transient publish failure")
		case <-time.After(2 * time.Second):
			t.Fatal("Run stopped consuming ticks")
		}
	}
}

// TestConcurrentMovesAreSafe drives the actual production shape: HandleInbound
// runs on each connection's own read pump, so it is called concurrently while
// the broadcast loop reads the same map. Meaningful under -race, which the gate
// runs.
func TestConcurrentMovesAreSafe(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"), member("c"))
	svc := NewService(tr, testRoom)

	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				svc.HandleInbound(context.Background(), member(id), testRoom,
					[]byte(`{"t":"vanyagotchi_move","x":0.5,"y":0.5}`))
			}
		}(id)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := svc.broadcast(context.Background()); err != nil {
				t.Errorf("broadcast: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

// waitForCount waits until the transport has seen n frames.
func waitForCount(t *testing.T, tr *fakeTransport, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(tr.frames()) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d published frames", n)
}
