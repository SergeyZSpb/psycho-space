package gamevanyagotchi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeTransport stands in for the hub. Every property of the broadcast worth
// asserting is reachable without a socket, which is what keeps these tests
// deterministic — the tick is a channel the test fires, so there is never a
// "wait for the next broadcast" sleep anywhere in this file.
type fakeTransport struct {
	mu         sync.Mutex
	members    []realtime.Member
	published  [][]byte
	unicast    []unicast
	membersErr error
	publishErr error
}

// unicast is one PublishTo call: which connection, and what it was sent.
type unicast struct {
	connID string
	msg    []byte
}

// epoch is the arbitrary instant the broadcast clock starts at in these tests.
// Fixed rather than time.Now() so a failure reads the same on every run, and far
// enough from the zero time that subtracting a grace period from it is still a
// sane timestamp.
var epoch = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

// at is the timestamp a broadcast is driven with, `mins` minutes after the
// epoch. Every test that does not care about the grace period passes at(0) and
// the position map simply never expires anything.
func at(mins float64) time.Time {
	return epoch.Add(time.Duration(mins * float64(time.Minute)))
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

func (f *fakeTransport) PublishTo(_ context.Context, connID string, msg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.unicast = append(f.unicast, unicast{connID: connID, msg: append([]byte(nil), msg...)})
	return nil
}

// unicasts returns every PublishTo the service has made.
func (f *fakeTransport) unicasts() []unicast {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]unicast(nil), f.unicast...)
}

// youFor decodes the single "you" frame sent to one connection, and reports
// whether exactly one was sent to it.
func youFor(t *testing.T, tr *fakeTransport, connID string) (You, bool) {
	t.Helper()
	var got You
	n := 0
	for _, u := range tr.unicasts() {
		if u.connID != connID {
			continue
		}
		var y You
		if err := json.Unmarshal(u.msg, &y); err != nil || y.T != TypeYou {
			continue
		}
		got, n = y, n+1
	}
	return got, n == 1
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

// member is one connection belonging to an account of its own — the common case
// of one person on one device.
func member(id string) realtime.Member {
	return realtime.Member{ConnID: id, AccountID: accountOf(id)}
}

// accountOf is the account id "member" invents for a connection id.
func accountOf(connID string) string { return "acct-" + connID }

// conn is a further connection of an account that may already be present: the
// second device, which the roster has to collapse into one entity.
func conn(connID, accountID string) realtime.Member {
	return realtime.Member{ConnID: connID, AccountID: accountID}
}

// peerByID finds one entity in a frame by the id that was actually published.
func peerByID(r Roster, id string) (Peer, bool) {
	for _, p := range r.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return Peer{}, false
}

// peerOf finds the entity an account is drawn as. Nothing in a frame says
// "acct-a" — that is the whole point of the pseudonym — so a test has to derive
// the published handle the same way the server does.
func peerOf(svc *Service, r Roster, accountID string) (Peer, bool) {
	return peerByID(r, svc.pseudonym(accountID))
}

const testRoom = "yard"

// TestBroadcastPlacesANewConnectionAtTheSpawn covers the first frame somebody
// appears in: they have never sent a move, and they still have to be somewhere,
// or a peer's client would have to invent a position for them.
func TestBroadcastPlacesANewConnectionAtTheSpawn(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	frames := tr.frames()
	if len(frames) != 1 {
		t.Fatalf("published %d frames; want 1", len(frames))
	}
	if frames[0].T != TypeRoster {
		t.Fatalf("frame type = %q; want %q", frames[0].T, TypeRoster)
	}
	p, ok := peerOf(svc, frames[0], accountOf("a"))
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
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.1,"y":0.9}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	f := tr.frames()[0]
	if len(f.Peers) != 2 {
		t.Fatalf("frame carries %d peers; want both", len(f.Peers))
	}
	moved, _ := peerOf(svc, f, accountOf("a"))
	if moved.X != 0.1 || moved.Y != 0.9 {
		t.Fatalf("mover at (%v,%v); want (0.1,0.9)", moved.X, moved.Y)
	}
	still, _ := peerOf(svc, f, accountOf("b"))
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
	svc := NewService(tr, testRoom, nil, nil)

	for i := 0; i < 3; i++ {
		if err := svc.broadcast(context.Background(), at(0)); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
	}

	for i, f := range tr.frames() {
		if len(f.Peers) != 2 {
			t.Fatalf("frame %d carries %d peers; every frame must carry all of them", i, len(f.Peers))
		}
	}
}

// TestLeavingRemovesAPeerAndItsPosition covers both halves of a disconnect for
// somebody who had only one device. The peer must vanish from the frame, and the
// position must not be kept for an account that is no longer here — a map that
// only ever grows is a leak on a long-running process, and the account would
// otherwise reappear tomorrow standing where it left off, which is not what
// presence means. TestAnEntityOutlivesOneOfItsConnections is the multi-device
// case, where one socket closing must NOT remove anything.
func TestLeavingRemovesAPeerFromTheFrameAtOnce(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.3}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	tr.setMembers(member("b"))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast after leave: %v", err)
	}

	f := tr.frames()[1]
	if _, ok := peerOf(svc, f, accountOf("a")); ok {
		t.Fatalf("a disconnected peer is still in the frame: %+v", f)
	}

	// The position itself is REMEMBERED for a while — that is what makes a
	// reload keep your place — but only for a while. Whoever is looking sees
	// them gone either way, which is the property that matters here.
	if err := svc.broadcast(context.Background(), at(0).Add(PositionGrace)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}
	svc.mu.Lock()
	_, kept := svc.pos[accountOf("a")]
	svc.mu.Unlock()
	if kept {
		t.Fatal("the position of a disconnected peer outlived the grace period")
	}
}

// TestAnEmptyRoomPublishesNothing keeps the common case free. Nobody is
// connected for most of the day, and publishing to an empty room five times a
// second would be pure waste — and would hide a genuine "why is this room
// empty" question behind traffic.
func TestAnEmptyRoomPublishesNothing(t *testing.T) {
	tr := &fakeTransport{}
	svc := NewService(tr, testRoom, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if n := len(tr.frames()); n != 0 {
		t.Fatalf("published %d frames into an empty room; want none", n)
	}
}

// TestAReloadKeepsYourPlace is the reason PositionGrace exists.
//
// Reloading the page closes the socket and opens a new one, so for a moment the
// account has no connections at all. The map used to be rebuilt from the live
// membership every tick, which dropped the position in that gap and put the
// player back in the middle of the yard — for a refresh, a tunnel, a lock
// screen, and every reconnect. Absence is not departure.
func TestAReloadKeepsYourPlace(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.4,"y":0.4}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// The socket goes away, and a couple of ticks pass with the yard empty.
	tr.setMembers()
	for _, when := range []time.Time{at(0.2), at(0.4)} {
		if err := svc.broadcast(context.Background(), when); err != nil {
			t.Fatalf("broadcast into an empty room: %v", err)
		}
	}

	// The new socket arrives, well inside the grace.
	tr.setMembers(member("a"))
	if err := svc.broadcast(context.Background(), at(0.6)); err != nil {
		t.Fatalf("broadcast after the reload: %v", err)
	}

	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if !ok {
		t.Fatal("the reloaded player is missing from the roster")
	}
	if p.X != 0.4 || p.Y != 0.4 {
		t.Fatalf("peer at (%v,%v) after a reload; want where they were standing (0.4,0.4)", p.X, p.Y)
	}
}

// TestAnEmptyRoomForgetsPositionsEventually guards the leak the grace period
// could otherwise introduce: a position held for a reload must not be held
// forever, or every account that ever connected stays in the map for the life of
// the process.
func TestAnEmptyRoomForgetsPositionsEventually(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.4,"y":0.4}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	tr.setMembers()
	// One tick inside the grace: still remembered.
	if err := svc.broadcast(context.Background(), at(1)); err != nil {
		t.Fatalf("broadcast inside the grace: %v", err)
	}
	svc.mu.Lock()
	held := len(svc.pos)
	svc.mu.Unlock()
	if held != 1 {
		t.Fatalf("%d positions held one minute after leaving; want 1", held)
	}

	// One tick past it: forgotten. The grace is measured from the last tick the
	// account was actually connected, which is at(0).
	if err := svc.broadcast(context.Background(), at(0).Add(PositionGrace)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}
	svc.mu.Lock()
	left := len(svc.pos)
	svc.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d positions survived the grace period; want 0", left)
	}
}

// TestComingBackAfterTheGraceStartsAtTheSpawn is the other half of the rule: a
// position that has genuinely gone stale must not be resurrected, or a player
// who was away all day would reappear wherever they happened to be standing at
// breakfast.
func TestComingBackAfterTheGraceStartsAtTheSpawn(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.9,"y":0.1}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	tr.setMembers()
	if err := svc.broadcast(context.Background(), at(0).Add(PositionGrace)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}

	tr.setMembers(member("a"))
	if err := svc.broadcast(context.Background(), at(0).Add(2*PositionGrace)); err != nil {
		t.Fatalf("broadcast on the return: %v", err)
	}

	frames := tr.frames()
	p, _ := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if p.X != spawn.X || p.Y != spawn.Y {
		t.Fatalf("peer at (%v,%v) after a long absence; want the spawn (%v,%v)", p.X, p.Y, spawn.X, spawn.Y)
	}
}

// TestRejectedFramesLeaveThePositionAlone is the substance of "the server owns
// truth". A malformed or out-of-protocol frame must not move anybody, and above
// all must not reset them to the spawn — a client could otherwise teleport a
// rival home by sending nonsense.
func TestRejectedFramesLeaveThePositionAlone(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := NewService(tr, testRoom, nil, nil)

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

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	p, _ := peerOf(svc, tr.frames()[0], accountOf("a"))
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
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), member("a"), "somewhere-else",
		[]byte(`{"t":"vanyagotchi_move","x":0.9,"y":0.9}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	p, _ := peerOf(svc, tr.frames()[0], accountOf("a"))
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
	svc := NewService(tr, testRoom, nil, nil)

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
	svc := NewService(tr, testRoom, nil, nil)

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
	svc := NewService(tr, testRoom, nil, nil)

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
	svc := NewService(tr, testRoom, nil, nil)

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
			if err := svc.broadcast(context.Background(), at(0)); err != nil {
				t.Errorf("broadcast: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

// TestTwoConnectionsOfOneAccountAreOneEntity is the bug this keying fixes:
// signing in on a second device used to put a second Ваня in the yard, because
// the roster was built per connection and the hub reports one Member per socket.
func TestTwoConnectionsOfOneAccountAreOneEntity(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(conn("phone", "acct-1"), conn("laptop", "acct-1"))
	svc := NewService(tr, testRoom, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	f := tr.frames()[0]
	if len(f.Peers) != 1 {
		t.Fatalf("two devices of one account produced %d entities; want 1: %+v", len(f.Peers), f)
	}
	if _, ok := peerOf(svc, f, "acct-1"); !ok {
		t.Fatalf("the one entity is not the account's: %+v", f)
	}
}

// TestAMoveFromEitherDeviceMovesTheSameEntity is the other half of it. One Ваня
// means one position: whichever socket the tap arrives on, the same entity has
// to move, and there must still be only one of it afterwards.
func TestAMoveFromEitherDeviceMovesTheSameEntity(t *testing.T) {
	phone, laptop := conn("phone", "acct-1"), conn("laptop", "acct-1")
	for _, tc := range []struct {
		name   string
		sender realtime.Member
		x, y   float64
	}{
		{name: "from the phone", sender: phone, x: 0.1, y: 0.2},
		{name: "from the laptop", sender: laptop, x: 0.7, y: 0.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &fakeTransport{}
			tr.setMembers(phone, laptop)
			svc := NewService(tr, testRoom, nil, nil)

			svc.HandleInbound(context.Background(), tc.sender, testRoom,
				fmt.Appendf(nil, `{"t":"vanyagotchi_move","x":%v,"y":%v}`, tc.x, tc.y))
			if err := svc.broadcast(context.Background(), at(0)); err != nil {
				t.Fatalf("broadcast: %v", err)
			}

			f := tr.frames()[0]
			if len(f.Peers) != 1 {
				t.Fatalf("frame carries %d entities; want 1: %+v", len(f.Peers), f)
			}
			p, ok := peerOf(svc, f, "acct-1")
			if !ok {
				t.Fatalf("the account is not on the plane: %+v", f)
			}
			if p.X != tc.x || p.Y != tc.y {
				t.Fatalf("entity at (%v,%v); want (%v,%v)", p.X, p.Y, tc.x, tc.y)
			}
		})
	}
}

// TestAnEntityOutlivesOneOfItsConnections is the pruning property restated per
// account. Closing a laptop must not remove the Ваня the phone is still holding
// open — but closing the last socket must, and must drop its position with it,
// or the map grows for the life of the process.
func TestAnEntityOutlivesOneOfItsConnections(t *testing.T) {
	tr := &fakeTransport{}
	phone, laptop := conn("phone", "acct-1"), conn("laptop", "acct-1")
	tr.setMembers(phone, laptop)
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), laptop, testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.25,"y":0.5}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// The laptop goes; the phone is still connected.
	tr.setMembers(phone)
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast after one connection left: %v", err)
	}
	p, ok := peerOf(svc, tr.frames()[1], "acct-1")
	if !ok {
		t.Fatalf("the account vanished while it still had a connection: %+v", tr.frames()[1])
	}
	// And it kept standing where the departed device had put it — the position
	// belongs to the account, not to the socket that last moved it.
	if p.X != 0.25 || p.Y != 0.5 {
		t.Fatalf("entity at (%v,%v) after one device left; want (0.25,0.5)", p.X, p.Y)
	}

	// The phone goes too, and now there is nothing left to keep.
	tr.setMembers(conn("someone-else", "acct-2"))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast after the last connection left: %v", err)
	}
	if _, ok := peerOf(svc, tr.frames()[2], "acct-1"); ok {
		t.Fatalf("an account with no connections is still on the plane: %+v", tr.frames()[2])
	}
	// Off the plane at once; out of memory once the grace has run out.
	if err := svc.broadcast(context.Background(), at(0).Add(PositionGrace)); err != nil {
		t.Fatalf("broadcast past the grace: %v", err)
	}
	svc.mu.Lock()
	_, kept := svc.pos["acct-1"]
	svc.mu.Unlock()
	if kept {
		t.Fatal("the position of a fully disconnected account outlived the grace period")
	}
}

// TestTwoAccountsAreStillTwoEntities is the guard against over-correcting. The
// fix collapses one account's connections; it must not collapse two people who
// happen to be in the room together.
func TestTwoAccountsAreStillTwoEntities(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(
		conn("a-phone", "acct-1"), conn("a-laptop", "acct-1"),
		conn("b-phone", "acct-2"),
	)
	svc := NewService(tr, testRoom, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	f := tr.frames()[0]
	if len(f.Peers) != 2 {
		t.Fatalf("three connections across two accounts produced %d entities; want 2: %+v", len(f.Peers), f)
	}
	for _, acct := range []string{"acct-1", "acct-2"} {
		if _, ok := peerOf(svc, f, acct); !ok {
			t.Fatalf("%s is missing from the plane: %+v", acct, f)
		}
	}
}

// TestThePseudonymIsStableAndIsNotTheAccountID pins the identifier's contract in
// both directions. It has to be usable as an identity — the same for one account
// across its devices and across ticks, different between accounts — and it has
// to not be the account id, which is the durable cross-session handle this
// project declines to broadcast to everybody in a room.
func TestThePseudonymIsStableAndIsNotTheAccountID(t *testing.T) {
	svc := NewService(&fakeTransport{}, testRoom, nil, nil)
	const acct = "11111111-2222-3333-4444-555555555555"

	first := svc.pseudonym(acct)
	if first != svc.pseudonym(acct) {
		t.Fatal("the pseudonym changed between two calls for the same account")
	}
	if first == acct {
		t.Fatal("the pseudonym IS the account id")
	}
	if strings.Contains(first, acct) || strings.Contains(acct, first) {
		t.Fatalf("the pseudonym %q shares text with the account id", first)
	}
	if len(first) != pseudonymChars {
		t.Fatalf("pseudonym %q is %d chars; want %d", first, len(first), pseudonymChars)
	}
	if other := svc.pseudonym("66666666-7777-8888-9999-000000000000"); other == first {
		t.Fatal("two accounts share one pseudonym")
	}

	// A second process is a second key, so the same account is a different
	// entity there. That is the point of a per-process key: nothing published
	// before a restart can be correlated with anything published after it.
	if second := NewService(&fakeTransport{}, testRoom, nil, nil).pseudonym(acct); second == first {
		t.Fatal("two services produced the same pseudonym; the key is not per-process")
	}
}

// TestTheRosterNeverCarriesAnAccountID is the same rule enforced where it
// actually matters — on the bytes that leave the process. Deriving a pseudonym
// is no use if the frame also carries the thing it was derived from.
func TestTheRosterNeverCarriesAnAccountID(t *testing.T) {
	tr := &fakeTransport{}
	const acct = "11111111-2222-3333-4444-555555555555"
	tr.setMembers(conn("c-1", acct))
	svc := NewService(tr, testRoom, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	tr.mu.Lock()
	raw := string(tr.published[0])
	tr.mu.Unlock()

	if strings.Contains(raw, acct) {
		t.Fatalf("the account id is on the wire: %s", raw)
	}
	if !strings.Contains(raw, svc.pseudonym(acct)) {
		t.Fatalf("the frame does not carry the account's pseudonym: %s", raw)
	}
}

// TestHelloIsAnsweredOnTheAskingConnectionOnly covers the handshake. The client
// cannot pick itself out of a roster that names everybody by an opaque handle,
// so it asks — and the answer is a unicast, because the pseudonym of one player
// is of no use to the rest of the room.
func TestHelloIsAnsweredOnTheAskingConnectionOnly(t *testing.T) {
	tr := &fakeTransport{}
	asker, bystander := conn("asker", "acct-1"), conn("bystander", "acct-2")
	tr.setMembers(asker, bystander)
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), asker, testRoom, []byte(`{"t":"vanyagotchi_hello"}`))

	got, ok := youFor(t, tr, "asker")
	if !ok {
		t.Fatalf("the asking connection got no single you frame: %+v", tr.unicasts())
	}
	if got.ID != svc.pseudonym("acct-1") {
		t.Fatalf("you.id = %q; want the asker's own pseudonym %q", got.ID, svc.pseudonym("acct-1"))
	}
	// And the identity it was told is the identity it is drawn under.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if _, found := peerByID(tr.frames()[0], got.ID); !found {
		t.Fatalf("the id the client was given is in no roster: %+v", tr.frames()[0])
	}
	// Nobody else hears about it.
	for _, u := range tr.unicasts() {
		if u.connID != "asker" {
			t.Fatalf("a you frame was sent to %q as well", u.connID)
		}
	}
}

// TestHelloFromASecondDeviceGetsTheSameID is what makes "highlight me" work on
// both devices at once: they are one entity, so they must be told the same id.
func TestHelloFromASecondDeviceGetsTheSameID(t *testing.T) {
	tr := &fakeTransport{}
	phone, laptop := conn("phone", "acct-1"), conn("laptop", "acct-1")
	tr.setMembers(phone, laptop)
	svc := NewService(tr, testRoom, nil, nil)

	svc.HandleInbound(context.Background(), phone, testRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	svc.HandleInbound(context.Background(), laptop, testRoom, []byte(`{"t":"vanyagotchi_hello"}`))

	onPhone, okP := youFor(t, tr, "phone")
	onLaptop, okL := youFor(t, tr, "laptop")
	if !okP || !okL {
		t.Fatalf("both devices must be answered exactly once: %+v", tr.unicasts())
	}
	if onPhone.ID != onLaptop.ID {
		t.Fatalf("the two devices were told different ids (%q, %q)", onPhone.ID, onLaptop.ID)
	}
}

// TestOnlyAHelloIsAnswered keeps the reply path narrow. Every other frame is
// silent by design — a move that drew a reply would let a client believe a
// position the server had not published yet, and an unknown frame that drew one
// would be an amplifier a client controls.
func TestOnlyAHelloIsAnswered(t *testing.T) {
	tr := &fakeTransport{}
	c := conn("c-1", "acct-1")
	tr.setMembers(c)
	svc := NewService(tr, testRoom, nil, nil)

	for _, payload := range []string{
		`{"t":"vanyagotchi_move","x":0.5,"y":0.5}`,
		`{"t":"vanyagotchi_you","id":"whatever"}`,
		`{"t":"vanyagotchi_shout"}`,
		`{"t":"bye","code":1001,"reason":"restart"}`,
		`not json`,
		``,
	} {
		svc.HandleInbound(context.Background(), c, testRoom, []byte(payload))
	}
	if n := len(tr.unicasts()); n != 0 {
		t.Fatalf("%d frames were sent in reply to messages that are not a hello: %+v", n, tr.unicasts())
	}

	// A hello from another room is somebody else's protocol, not ours.
	svc.HandleInbound(context.Background(), c, "somewhere-else", []byte(`{"t":"vanyagotchi_hello"}`))
	if n := len(tr.unicasts()); n != 0 {
		t.Fatalf("a hello from another room was answered: %+v", tr.unicasts())
	}
}

// ---------------------------------------------------------------------------
// Appearance: what stops the yard being a field of anonymous dots.
// ---------------------------------------------------------------------------

// TestEveryPeerCarriesItsArtAndPoseAndOnlyANamedPetALabel is the whole of the
// roster's appearance contract, in one frame.
//
// It goes to EVERYBODY, and that is the point: a world where each player can
// only see their own Ваня properly is two worlds rather than one shared one. The
// label is the one field that may be absent, because a pet is named in a dialog
// and is nil until then — publishing an empty string as a name would put a blank
// caption under an entity rather than none.
func TestEveryPeerCarriesItsArtAndPoseAndOnlyANamedPetALabel(t *testing.T) {
	hpDef := mustStat(t, StatHP)
	name := "Ваня"

	tr := &fakeTransport{}
	tr.setMembers(member("named"), member("nameless"))
	svc := NewService(tr, testRoom, nil, nil)

	// Cached exactly as the two human-paced events fill it — a hello, or its
	// owner acting over HTTP. Which of the two put it there is not this test's
	// subject; that they are what the frame is built from is.
	svc.remember(accountOf("named"), Pet{SkinKey: SkinVanya, Name: &name},
		[]StatRow{{Key: StatHP, Value: (hpDef.Min + hpDef.WarnAt) / 2, AsOf: at(0)}})
	svc.remember(accountOf("nameless"), Pet{SkinKey: SkinVanya},
		[]StatRow{{Key: StatHP, Value: (hpDef.WarnAt + hpDef.Max) / 2, AsOf: at(0)}})

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]

	named, ok := peerOf(svc, f, accountOf("named"))
	if !ok {
		t.Fatalf("the named account is not on the plane: %+v", f)
	}
	if named.Art != SkinVanya {
		t.Errorf("art = %q; want the pet's own skin %q", named.Art, SkinVanya)
	}
	if named.Label != name {
		t.Errorf("label = %q; want %q", named.Label, name)
	}
	if named.Pose != PosePoorly {
		t.Errorf("pose = %q; want %q — his health is inside the catalogue's warning range", named.Pose, PosePoorly)
	}

	nameless, ok := peerOf(svc, f, accountOf("nameless"))
	if !ok {
		t.Fatalf("the unnamed account is not on the plane: %+v", f)
	}
	if nameless.Label != "" {
		t.Errorf("an unnamed pet was labelled %q; want no label at all", nameless.Label)
	}
	if nameless.Art != SkinVanya || nameless.Pose != PoseFine {
		t.Errorf("an unnamed but healthy pet was drawn as %+v; want the skin it has and %q", nameless, PoseFine)
	}
}

// TestAPlayerTheCacheKnowsNothingAboutIsStillDrawn is the rule that a missing
// lookup must never cost somebody their presence.
//
// A client that has connected but not yet said hello, or one whose owner has
// never opened the game over HTTP, has nothing cached at all. Rendering nothing
// for them would make a real person invisible to everyone else in the yard for
// the sake of a name — so they are drawn with the catalogue's default skin, no
// label, and no trouble.
func TestAPlayerTheCacheKnowsNothingAboutIsStillDrawn(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("stranger"))
	svc := NewService(tr, testRoom, nil, nil)

	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	f := tr.frames()[0]
	p, ok := peerOf(svc, f, accountOf("stranger"))
	if !ok {
		t.Fatalf("an account with nothing cached was left out of the roster entirely: %+v", f)
	}
	if p.Art != Content().DefaultSkin {
		t.Errorf("art = %q; want the catalogue default %q", p.Art, Content().DefaultSkin)
	}
	if p.Label != "" {
		t.Errorf("label = %q; want none — nothing is known about this pet, not even that it has a name", p.Label)
	}
	if p.Pose != PoseFine {
		t.Errorf("pose = %q; want %q — an unknown pet must not be drawn as one that is dying", p.Pose, PoseFine)
	}
}

// ---------------------------------------------------------------------------
// Durable position: what makes a deploy leave the yard where it was.
// ---------------------------------------------------------------------------

// noPool stands in for the connection pool on the plane's durable path.
//
// The service does no durable work at all while its pool or its repository is
// nil — which is what lets every test above drive the plane with both of them
// unset — so a test of the durable half needs something non-nil to hand it. The
// fake repository ignores it entirely, and every method here panics rather than
// answering, so a query that somehow reached the pool fails loudly instead of
// quietly reading nothing.
type noPool struct{}

func (noPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("gamevanyagotchi: the plane reached the connection pool")
}

func (noPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("gamevanyagotchi: the plane reached the connection pool")
}

func (noPool) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("gamevanyagotchi: the plane reached the connection pool")
}

// planeService builds the plane with its durable half wired up.
//
// Run is deliberately never started by the tests below. Departures are queued by
// the broadcast and written by a goroutine of Run's, so driving broadcast
// directly and reading the queue makes every assertion here synchronous: no
// waiting, no polling, and no way for a passing test to be a race that happened
// to land the right way round.
func planeService(tr Transport, repo Repository) *Service {
	return NewService(tr, testRoom, noPool{}, repo)
}

// queued drains everything the plane has asked to have written down.
func queued(svc *Service) []positionSave {
	var out []positionSave
	for {
		select {
		case sv := <-svc.saves:
			out = append(out, sv)
		default:
			return out
		}
	}
}

// TestADepartureIsQueuedOnceHoweverLongItLasts is why placement carries a
// `saved` flag at all.
//
// Absence is not an event: there is no leave hook, so it is re-observed on every
// tick until the grace period runs out. Queueing the write each time would be
// five database writes a second for somebody who has already gone — the
// per-tick traffic this whole design exists to avoid — and every one after the
// first would say exactly what the first one said.
//
// Asserted on the queue rather than on the repository because the queue is the
// seam the broadcast itself touches. What is under test is what the TICK does;
// the writer behind it is a goroutine, and reaching past it would mean
// synchronising with something this assertion does not need.
func TestADepartureIsQueuedOnceHoweverLongItLasts(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.3}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// Gone, and noticed on every one of the next several ticks — all of them
	// well inside the grace, so the position is still being held.
	tr.setMembers()
	for i := 1; i <= 5; i++ {
		if err := svc.broadcast(context.Background(), at(float64(i)*0.1)); err != nil {
			t.Fatalf("broadcast %d after the departure: %v", i, err)
		}
	}

	saves := queued(svc)
	if len(saves) != 1 {
		t.Fatalf("%d writes queued for one departure observed over five ticks; want exactly 1", len(saves))
	}
	if saves[0].accountID != accountOf("a") {
		t.Errorf("queued a write for %q; want %q", saves[0].accountID, accountOf("a"))
	}
	if saves[0].at.X != 0.2 || saves[0].at.Y != 0.3 {
		t.Errorf("queued position (%v,%v); want where he was standing (0.2,0.3)", saves[0].at.X, saves[0].at.Y)
	}
	// The instant recorded is the last tick he was actually connected, not the
	// one that noticed he had gone — the first is when he was last seen and the
	// second is merely when somebody looked.
	if !saves[0].seen.Equal(at(0)) {
		t.Errorf("queued seen = %v; want the last tick he was connected, %v", saves[0].seen, at(0))
	}
}

// TestComingBackMakesTheNextDepartureANewOne is the other half of that flag. It
// suppresses repeats of ONE departure and must not suppress the next one, or a
// player who reloaded the page would keep his old resting place forever and
// every walk after the first reconnect would be lost on the way out.
func TestComingBackMakesTheNextDepartureANewOne(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.1,"y":0.1}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// Away — written down once.
	tr.setMembers()
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast while away: %v", err)
	}

	// Back inside the grace, and standing somewhere else by the time he goes
	// again.
	tr.setMembers(member("a"))
	if err := svc.broadcast(context.Background(), at(0.4)); err != nil {
		t.Fatalf("broadcast on the return: %v", err)
	}
	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.9,"y":0.8}`))
	if err := svc.broadcast(context.Background(), at(0.6)); err != nil {
		t.Fatalf("broadcast after the second move: %v", err)
	}

	tr.setMembers()
	if err := svc.broadcast(context.Background(), at(0.8)); err != nil {
		t.Fatalf("broadcast on the second departure: %v", err)
	}

	saves := queued(svc)
	if len(saves) != 2 {
		t.Fatalf("%d writes queued for two departures; want exactly 2: %+v", len(saves), saves)
	}
	second := saves[1]
	if second.at.X != 0.9 || second.at.Y != 0.8 {
		t.Errorf("the second departure was written at (%v,%v); want where he was standing by then (0.9,0.8)",
			second.at.X, second.at.Y)
	}
	if !second.seen.Equal(at(0.6)) {
		t.Errorf("the second departure carries seen = %v; want the last tick of his second visit, %v — it is a new departure, not a repeat of the first",
			second.seen, at(0.6))
	}
}

// TestTheShutdownFlushWritesDownEverybodyStillStanding is the leg that only a
// deploy exercises.
//
// A restart is everybody leaving at once with no further tick to notice it, so
// without this the whole yard would come back standing in the middle — which is
// the exact failure durable position exists to fix, and the one that shows up
// nowhere but production.
func TestTheShutdownFlushWritesDownEverybodyStillStanding(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("a"), member("b"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.4}`))
	svc.HandleInbound(context.Background(), member("b"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.7,"y":0.6}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	svc.flushPositions()

	saved := repo.saved()
	if len(saved) != 2 {
		t.Fatalf("the shutdown flush wrote %d positions; want one for each of the two players: %+v", len(saved), saved)
	}
	want := map[string]Point{
		accountOf("a"): {X: 0.2, Y: 0.4},
		accountOf("b"): {X: 0.7, Y: 0.6},
	}
	for _, sv := range saved {
		w, ok := want[sv.accountID]
		if !ok {
			t.Fatalf("the flush wrote a position for %q, who is not in the yard", sv.accountID)
		}
		if sv.at != w {
			t.Errorf("%q was written down at (%v,%v); want (%v,%v)", sv.accountID, sv.at.X, sv.at.Y, w.X, w.Y)
		}
		if !sv.seen.Equal(at(0)) {
			t.Errorf("%q was written down as last seen at %v; want the last tick, %v", sv.accountID, sv.seen, at(0))
		}
		delete(want, sv.accountID)
	}
	if len(want) != 0 {
		t.Fatalf("the flush left %v out entirely", want)
	}
}

// TestTheShutdownFlushDoesNotWriteADepartureDownTwice is the same flag seen from
// the other end.
//
// Somebody who left a moment before the restart is STILL held in memory — the
// grace period is what makes a reload keep your place — so the flush can see
// him. Writing him again would be a second write saying exactly what the queued
// one said, and it is the flush, arriving later, that would win: he would be
// recorded as last seen at the shutdown rather than at the moment he actually
// left.
func TestTheShutdownFlushDoesNotWriteADepartureDownTwice(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("staying"), member("leaving"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	// Both of them go and stand somewhere first, which is what makes their
	// positions worth writing down at all: a spawn point nobody chose is
	// provisional and is deliberately NOT persisted — see the test below.
	for _, who := range []string{"staying", "leaving"} {
		svc.HandleInbound(context.Background(), member(who), testRoom,
			[]byte(`{"t":"vanyagotchi_move","x":0.3,"y":0.7}`))
	}
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// One socket goes, and the tick queues that departure.
	tr.setMembers(member("staying"))
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast after the departure: %v", err)
	}
	if n := len(queued(svc)); n != 1 {
		t.Fatalf("%d writes queued for one departure; want 1", n)
	}
	svc.mu.Lock()
	_, held := svc.pos[accountOf("leaving")]
	svc.mu.Unlock()
	if !held {
		t.Fatal("the departed account was forgotten immediately; this test needs it still held, which is what makes the flush able to see it")
	}

	svc.flushPositions()

	saved := repo.saved()
	if len(saved) != 1 {
		t.Fatalf("the flush wrote %d positions; want 1 — only the player who is still standing there: %+v", len(saved), saved)
	}
	if saved[0].accountID != accountOf("staying") {
		t.Fatalf("the flush wrote down %q; want %q, since the other one's departure had already been written",
			saved[0].accountID, accountOf("staying"))
	}
}

// TestAHelloPutsAPetBackWhereItLastStood is the reconnect a deploy causes: the
// position map died with the old process, so the only thing that knows where
// anybody was standing is the pets table.
//
// The second half is the case the guard in load exists for. A reconnect INSIDE
// the grace is also a hello, and there the in-memory position is the newer
// truth — winding him back to the last one written down would undo the very
// thing the grace period is for.
func TestAHelloPutsAPetBackWhereItLastStood(t *testing.T) {
	stood := Point{X: 0.8, Y: 0.15}
	if stood == spawn {
		t.Fatal("the stored position is the spawn, so this test could not tell a restore from a default")
	}
	name := "Ваня"
	saved := time.Now().UTC().Add(-time.Hour)
	repo := &fakeRepo{
		pet: &Pet{
			ID: "pet-a", AccountID: accountOf("a"), Name: &name, SkinKey: SkinVanya,
			LocationKey: LocationYard, X: &stood.X, Y: &stood.Y, LastSeenAt: &saved,
		},
		rows: []StatRow{{Key: StatHP, Value: mustStat(t, StatHP).Max, AsOf: at(0)}},
	}
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("a"), testRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	p, ok := peerOf(svc, tr.frames()[0], accountOf("a"))
	if !ok {
		t.Fatalf("the account that said hello is not on the plane: %+v", tr.frames()[0])
	}
	if p.X != stood.X || p.Y != stood.Y {
		t.Fatalf("peer at (%v,%v) on a fresh process; want where he was standing when he last left (%v,%v)",
			p.X, p.Y, stood.X, stood.Y)
	}
	// The same hello is what fills the cache, so the appearance arrives with the
	// position rather than a frame later.
	if p.Art != SkinVanya || p.Label != name {
		t.Errorf("peer drawn as %+v; want the skin and name the pet actually has", p)
	}

	// He walks somewhere else, and his socket drops and comes back inside the
	// grace. The hello must leave him where he is.
	svc.HandleInbound(context.Background(), member("a"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.35,"y":0.45}`))
	tr.setMembers()
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast while the socket is away: %v", err)
	}
	tr.setMembers(member("a"))
	svc.HandleInbound(context.Background(), member("a"), testRoom, []byte(`{"t":"vanyagotchi_hello"}`))
	if err := svc.broadcast(context.Background(), at(0.4)); err != nil {
		t.Fatalf("broadcast after the reconnect: %v", err)
	}

	frames := tr.frames()
	back, ok := peerOf(svc, frames[len(frames)-1], accountOf("a"))
	if !ok {
		t.Fatalf("the reconnected account is missing from the roster: %+v", frames[len(frames)-1])
	}
	if back.X != 0.35 || back.Y != 0.45 {
		t.Fatalf("peer at (%v,%v) after reconnecting inside the grace; want where he was standing (0.35,0.45) — the stored position is older news",
			back.X, back.Y)
	}
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

// TestAPositionNobodyChoseIsNotWrittenDown guards a data-loss bug that would be
// invisible until somebody complained.
//
// The hub registers a connection at the upgrade, BEFORE its client has said
// hello — so a tick can land in that gap and put a spawn point in the map for
// somebody whose stored position has not been read yet. If that provisional
// placement were persisted on departure, a player whose hello never arrived (an
// old cached client, a slow query, a dropped frame) would have the real position
// he left behind quietly overwritten with the middle of the yard. Nothing would
// error; he would just stop being where he was.
func TestAPositionNobodyChoseIsNotWrittenDown(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("drifter"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	// Seen by the plane, never moved, never said hello: placed at the spawn by
	// the broadcast alone.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	tr.setMembers()
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast after the departure: %v", err)
	}
	if n := len(queued(svc)); n != 0 {
		t.Fatalf("%d writes queued for a position nobody chose; want none", n)
	}

	// And the shutdown path agrees with the tick, which is the half that only
	// ever runs in production.
	svc.flushPositions()
	if saved := repo.saved(); len(saved) != 0 {
		t.Fatalf("the flush wrote %d provisional positions; want none: %+v", len(saved), saved)
	}
}

// TestAStoredPositionReplacesTheSpawnTheTickInvented is the other half of the
// same ordering problem, from the arriving side.
//
// A tick between the upgrade and the hello leaves a spawn point in the map. The
// stored position then arrives and MUST be allowed to replace it — treating the
// map as already-decided is what would make a returning Ваня teleport to the
// middle, which is the entire failure durable position exists to prevent.
func TestAStoredPositionReplacesTheSpawnTheTickInvented(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("returning"))
	acct := accountOf("returning")
	stood := Point{X: 0.15, Y: 0.85}
	repo := &fakeRepo{pet: &Pet{
		ID: "pet-1", AccountID: acct, SkinKey: SkinVanya, LocationKey: LocationYard,
		X: &stood.X, Y: &stood.Y,
	}}
	svc := planeService(tr, repo)

	// The gap: a frame is published before the client has said anything.
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast before the hello: %v", err)
	}
	svc.HandleInbound(context.Background(), member("returning"), testRoom,
		[]byte(`{"t":"vanyagotchi_hello"}`))
	if err := svc.broadcast(context.Background(), at(0.2)); err != nil {
		t.Fatalf("broadcast after the hello: %v", err)
	}

	frames := tr.frames()
	p, ok := peerOf(svc, frames[len(frames)-1], acct)
	if !ok {
		t.Fatal("the returning player is missing from the roster")
	}
	if p.X != stood.X || p.Y != stood.Y {
		t.Fatalf("returning peer at (%v,%v); want where he left off (%v,%v)", p.X, p.Y, stood.X, stood.Y)
	}
}

// TestRunWritesEverybodyDownOnTheWayOut is the shutdown path, driven through Run
// rather than by calling the flush directly — because calling it directly is
// exactly the thing that cannot fail.
//
// This is the half of durable position that ONLY happens in production. A deploy
// cancels the context the tick loop, the writer and every socket share; there is
// no further tick to notice that everybody has left, so without an explicit
// flush on the way out a restart would write nothing at all and the whole yard
// would come back standing in the middle. A test that only ever called
// flushPositions() would keep passing with that flush deleted from Run.
func TestRunWritesEverybodyDownOnTheWayOut(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(member("sleeper"))
	repo := &fakeRepo{}
	svc := planeService(tr, repo)

	svc.HandleInbound(context.Background(), member("sleeper"), testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.2,"y":0.8}`))

	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		svc.Run(ctx, tick)
		close(done)
	}()

	// One frame, so the position is held and its owner is present.
	tick <- at(0)

	// The deploy.
	cancel()
	<-done

	saved := repo.saved()
	if len(saved) != 1 {
		t.Fatalf("shutdown wrote %d positions; want the one player standing in the yard: %+v", len(saved), saved)
	}
	if saved[0].accountID != accountOf("sleeper") {
		t.Fatalf("shutdown wrote down %q; want %q", saved[0].accountID, accountOf("sleeper"))
	}
	if saved[0].at.X != 0.2 || saved[0].at.Y != 0.8 {
		t.Fatalf("shutdown wrote (%v,%v); want where he was standing (0.2,0.8)", saved[0].at.X, saved[0].at.Y)
	}
}
