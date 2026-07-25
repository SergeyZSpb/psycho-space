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

// TestLeavingRemovesAPeerAndItsPosition covers both halves of a disconnect for
// somebody who had only one device. The peer must vanish from the frame, and the
// position must not be kept for an account that is no longer here — a map that
// only ever grows is a leak on a long-running process, and the account would
// otherwise reappear tomorrow standing where it left off, which is not what
// presence means. TestAnEntityOutlivesOneOfItsConnections is the multi-device
// case, where one socket closing must NOT remove anything.
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
	if _, ok := peerOf(svc, f, accountOf("a")); ok {
		t.Fatalf("a disconnected peer is still in the frame: %+v", f)
	}
	svc.mu.Lock()
	_, kept := svc.pos[accountOf("a")]
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
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), member("a"), "somewhere-else",
		[]byte(`{"t":"vanyagotchi_move","x":0.9,"y":0.9}`))
	if err := svc.broadcast(context.Background()); err != nil {
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

// TestTwoConnectionsOfOneAccountAreOneEntity is the bug this keying fixes:
// signing in on a second device used to put a second Ваня in the yard, because
// the roster was built per connection and the hub reports one Member per socket.
func TestTwoConnectionsOfOneAccountAreOneEntity(t *testing.T) {
	tr := &fakeTransport{}
	tr.setMembers(conn("phone", "acct-1"), conn("laptop", "acct-1"))
	svc := NewService(tr, testRoom)

	if err := svc.broadcast(context.Background()); err != nil {
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
			svc := NewService(tr, testRoom)

			svc.HandleInbound(context.Background(), tc.sender, testRoom,
				fmt.Appendf(nil, `{"t":"vanyagotchi_move","x":%v,"y":%v}`, tc.x, tc.y))
			if err := svc.broadcast(context.Background()); err != nil {
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
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), laptop, testRoom,
		[]byte(`{"t":"vanyagotchi_move","x":0.25,"y":0.5}`))
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	// The laptop goes; the phone is still connected.
	tr.setMembers(phone)
	if err := svc.broadcast(context.Background()); err != nil {
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
	if err := svc.broadcast(context.Background()); err != nil {
		t.Fatalf("broadcast after the last connection left: %v", err)
	}
	if _, ok := peerOf(svc, tr.frames()[2], "acct-1"); ok {
		t.Fatalf("an account with no connections is still on the plane: %+v", tr.frames()[2])
	}
	svc.mu.Lock()
	_, kept := svc.pos["acct-1"]
	svc.mu.Unlock()
	if kept {
		t.Fatal("the position of a fully disconnected account was kept")
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
	svc := NewService(tr, testRoom)

	if err := svc.broadcast(context.Background()); err != nil {
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
	svc := NewService(&fakeTransport{}, testRoom)
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
	if second := NewService(&fakeTransport{}, testRoom).pseudonym(acct); second == first {
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
	svc := NewService(tr, testRoom)

	if err := svc.broadcast(context.Background()); err != nil {
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
	svc := NewService(tr, testRoom)

	svc.HandleInbound(context.Background(), asker, testRoom, []byte(`{"t":"vanyagotchi_hello"}`))

	got, ok := youFor(t, tr, "asker")
	if !ok {
		t.Fatalf("the asking connection got no single you frame: %+v", tr.unicasts())
	}
	if got.ID != svc.pseudonym("acct-1") {
		t.Fatalf("you.id = %q; want the asker's own pseudonym %q", got.ID, svc.pseudonym("acct-1"))
	}
	// And the identity it was told is the identity it is drawn under.
	if err := svc.broadcast(context.Background()); err != nil {
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
	svc := NewService(tr, testRoom)

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
	svc := NewService(tr, testRoom)

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
