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
	inserted chan Visit
}

func newFakeRepo() *fakeRepo { return &fakeRepo{inserted: make(chan Visit, 8)} }

func (f *fakeRepo) InsertVisit(_ context.Context, _ db.DBTX, v Visit) error {
	f.inserted <- v
	return nil
}

func (f *fakeRepo) RecentVisits(context.Context, db.DBTX, uuid.UUID, int) ([]Visit, error) {
	return nil, nil
}

// clock hands out the timestamps this file's ticks carry.
//
// BASED AT time.Now() AND NEVER AT A FIXED EPOCH, which is not tidiness: the
// hello is the join and it stamps JoinedAt from the wall clock, exactly as
// production does, where the injected tick IS a time.Ticker reading the same
// clock. Ticking from 1970 would hand every occupant a visit that started fifty
// years after it ended, and would expire the abandon grace on the first tick
// after one that did not.
type clock struct {
	base time.Time
	n    int
}

func newClock() *clock { return &clock{base: time.Now()} }

// at is a fixed offset from the start, for a test that wants to place an event
// somewhere in particular.
func (c *clock) at(d time.Duration) time.Time { return c.base.Add(d) }

// next is the following simulation step. Read outside a select's send case,
// because a send case evaluates its value even when another case wins — which
// would run the clock forward on every timeout instead of every tick.
func (c *clock) next() time.Time {
	c.n++
	return c.base.Add(time.Duration(c.n) * SimStep)
}

// tickAndSettle fires one tick and returns only once the loop has finished
// stepping it.
//
// IT IS A BARRIER, AND EVERY TEST THAT CHANGES WHO IS CONNECTED NEEDS ONE. step
// reads the room's membership BEFORE it takes the lock, so a test that dropped a
// connection immediately after an ordinary send could have that read land on
// either side of it — and a tick that misses somebody who was still connected
// leaves their LastSeen behind, which comes out as a visit of zero seconds.
//
// The tick channel is unbuffered and the loop is sequential, so a SECOND send
// completes only once the FIRST step has returned. That is the whole mechanism.
// The second tick then advances the simulation one more step at the same
// instant, which costs one step of whatever the occupants had queued and changes
// nothing any assertion here reads.
func tickAndSettle(tick chan time.Time, at time.Time) {
	tick <- at
	tick <- at
}

// waitFor pumps the tick until cond holds or the attempts run out. It is the
// synchronisation primitive this file uses instead of a sleep: the loop makes
// progress happen rather than waiting for it.
func waitFor(t *testing.T, tick chan time.Time, clk *clock, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		at := clk.next()
		select {
		case tick <- at:
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

// sayHello is one socket walking into the building.
func sayHello(s *Service, m realtime.Member) {
	s.HandleInbound(context.Background(), m, Room, []byte(`{"t":"`+TypeHello+`"}`))
}

// members turns account ids into one connection each, which is the ordinary
// shape: one person, one tab.
func members(accounts ...string) []realtime.Member {
	out := make([]realtime.Member, 0, len(accounts))
	for i, a := range accounts {
		out = append(out, realtime.Member{ConnID: "c" + string(rune('1'+i)), AccountID: a})
	}
	return out
}

func TestTheWorldIsGeneratedOnDemandAndIsTheSameOneForEverybody(t *testing.T) {
	// One заброшка for the whole process. Two people asking for it get the same
	// building, and asking twice does not regenerate it under the first one.
	s := NewService(newFakeTransport(), Room, nil, newFakeRepo())
	first, level := s.World()
	second, again := s.World()
	if first != second {
		t.Fatalf("two reads produced two buildings: %v and %v", first, second)
	}
	if level != again {
		t.Fatal("the second read generated a fresh level")
	}
	if len(level.Sectors) < 2 {
		t.Fatalf("a заброшка with %d rooms is not one", len(level.Sectors))
	}
}

func TestHelloIsTheJoinAndNamesTheBuilding(t *testing.T) {
	// There is no start endpoint. The room already carries an authenticated
	// account, so being in the room is being in the building — and the ready
	// frame names which building, because that is the client's only way to tell
	// whether the level it cached is still the current one.
	acc := uuid.New().String()
	tr := newFakeTransport(members(acc)...)
	s := NewService(tr, Room, nil, newFakeRepo())

	sayHello(s, realtime.Member{ConnID: "c1", AccountID: acc})

	ready := tr.framesOfType(TypeReady)
	if len(ready) != 1 {
		t.Fatalf("expected one ready frame, got %d", len(ready))
	}
	id, _ := s.World()
	if ready[0]["world_id"] != id.String() {
		t.Fatalf("ready names world %v, the building is %v", ready[0]["world_id"], id)
	}
}

func TestASecondHelloIsAReconnectAndNotASecondPersonInTheRoom(t *testing.T) {
	// A page reload, a tunnel and a phone waking up all produce a second hello,
	// and every one of them is the same person walking back to where he was.
	acc := uuid.New().String()
	m := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(m)
	s := NewService(tr, Room, nil, newFakeRepo())

	sayHello(s, m)
	sayHello(s, m)

	if got := len(tr.framesOfType(TypeReady)); got != 2 {
		t.Fatalf("two hellos were answered %d times", got)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := s.world.Occupants(); got != 1 {
		t.Fatalf("two hellos from one account put %d people in the building", got)
	}
}

func TestAFullBuildingSaysSoRatherThanSayingNothing(t *testing.T) {
	// A refusal a player can READ. Silence is this game's answer to a frame it
	// cannot parse; a hello it parsed perfectly well and cannot honour is
	// answered, because somebody told nothing would sit watching an empty screen
	// deciding the game is broken.
	tr := newFakeTransport()
	s := NewService(tr, Room, nil, newFakeRepo())
	for i := 0; i < MaxOccupants; i++ {
		sayHello(s, realtime.Member{ConnID: "c" + uuid.New().String(), AccountID: uuid.New().String()})
	}
	if got := len(tr.framesOfType(TypeFull)); got != 0 {
		t.Fatalf("%d of the first %d arrivals were turned away", got, MaxOccupants)
	}

	sayHello(s, realtime.Member{ConnID: "c-late", AccountID: uuid.New().String()})

	full := tr.framesOfType(TypeFull)
	if len(full) != 1 {
		t.Fatalf("the %dth arrival got %d refusals", MaxOccupants+1, len(full))
	}
	if got := len(tr.framesOfType(TypeReady)); got != MaxOccupants {
		t.Fatalf("%d ready frames for %d places", got, MaxOccupants)
	}
}

func TestAFrameForAnotherRoomIsIgnored(t *testing.T) {
	// The handler is registered per room by the composition root, but a service
	// that trusted that entirely would misbehave the day two games shared one.
	tr := newFakeTransport()
	s := NewService(tr, Room, nil, newFakeRepo())
	s.HandleInbound(context.Background(), realtime.Member{ConnID: "c1", AccountID: uuid.New().String()},
		"yard", []byte(`{"t":"vanyadum_hello"}`))
	if got := len(tr.framesOfType(TypeReady)); got != 0 {
		t.Fatalf("answered a frame addressed to another room: %d", got)
	}
}

func TestInputMovesThePlayerAndTheSnapshotSaysSo(t *testing.T) {
	acc := uuid.New().String()
	member := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(member)
	s, tick := startService(t, tr, newFakeRepo())
	clk := newClock()

	sayHello(s, member)
	s.mu.Lock()
	start := s.world.Occupant(acc).State.Pos
	s.mu.Unlock()

	// Walk in every direction in turn, so the test does not depend on which way
	// the spawn happens to face or where the nearest wall is.
	for i := 0; i < 12; i++ {
		yaw := float64(i) * 0.5
		s.HandleInbound(context.Background(), member, Room, mustInput(t, int64(i), yaw))
		tick <- clk.next()
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
		"t": TypeInput,
		"cmds": []map[string]any{
			{"q": seq + 1, "dt": 0.05, "my": 1, "yaw": yaw},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBothOfAPlayersDevicesGetTheSnapshot(t *testing.T) {
	// An occupant is an ACCOUNT, not a connection. Two tabs are two views of one
	// person standing in one place, which is the same rule «Ванягоччи» learned
	// the expensive way when a second device produced a second Ваня.
	acc := uuid.New().String()
	tr := newFakeTransport(
		realtime.Member{ConnID: "c1", AccountID: acc},
		realtime.Member{ConnID: "c2", AccountID: acc},
	)
	s, tick := startService(t, tr, newFakeRepo())
	sayHello(s, realtime.Member{ConnID: "c1", AccountID: acc})

	waitFor(t, tick, newClock(), func() bool { return len(tr.framesOfType(TypeSnapshot)) >= 2 })

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

func TestTwoPeopleInTheBuildingSeeEachOther(t *testing.T) {
	// The whole point of the iteration, at the service's own level: one world,
	// two occupants, and a frame per occupant that names the OTHER one.
	a, b := uuid.New().String(), uuid.New().String()
	ma := realtime.Member{ConnID: "ca", AccountID: a}
	mb := realtime.Member{ConnID: "cb", AccountID: b}
	tr := newFakeTransport(ma, mb)
	s, tick := startService(t, tr, newFakeRepo())

	sayHello(s, ma)
	sayHello(s, mb)

	waitFor(t, tick, newClock(), func() bool { return len(tr.framesOfType(TypeSnapshot)) >= 2 })

	tr.mu.Lock()
	defer tr.mu.Unlock()
	saw := map[string]string{}
	for _, m := range tr.sent {
		var f Snapshot
		if json.Unmarshal(m.body, &f) != nil || f.T != TypeSnapshot || len(f.Peers) == 0 {
			continue
		}
		saw[m.connID] = f.Peers[0].ID
	}
	if len(saw) != 2 {
		t.Fatalf("only these connections were told about anybody else: %v", saw)
	}
	if saw["ca"] == saw["cb"] {
		t.Fatalf("both frames name the peer %q — somebody is looking at himself", saw["ca"])
	}
	// A HANDLE AND NEVER AN ACCOUNT (ADR-037): an account id is a UUID, which is
	// far longer than the twelve base64url characters a pseudonym is.
	for conn, id := range saw {
		if len(id) != 12 {
			t.Fatalf("%s was told its neighbour is %q, which is not a handle", conn, id)
		}
	}
}

func TestSomebodyWhoStopsComingBackHasHisVisitWritten(t *testing.T) {
	// The only database write this game makes, and the only way out of the
	// building. There is no quit button and nothing that ends, so leaving is
	// exactly "my connections went away and did not come back".
	acc := uuid.New().String()
	member := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(member)
	repo := newFakeRepo()
	s, tick := startService(t, tr, repo)

	sayHello(s, member)
	// The clock starts AFTER the join, so the arithmetic below is exact: the
	// hello stamps JoinedAt from the wall clock (production's tick reads the same
	// one), and a base captured before it would leave every offset a hair short
	// and floor to a second less.
	clk := newClock()

	// A couple of ticks with the connection present, so the visit has a length.
	// The tick carries its own clock, so twenty seconds of standing there and the
	// whole grace after it are driven without waiting for either.
	tickAndSettle(tick, clk.at(0))
	tickAndSettle(tick, clk.at(20*time.Second))
	seed := func() int64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.world.Level.Seed
	}()

	tr.setMembers(nil) // the socket goes, and does not come back
	tick <- clk.at(20*time.Second + AbandonGrace + time.Second)

	var written Visit
	select {
	case written = <-repo.inserted:
	case <-time.After(2 * time.Second):
		t.Fatal("the visit was never written")
	}
	if written.Seed != seed {
		t.Fatalf("the visit records building %d, the world was %d", written.Seed, seed)
	}
	if written.AccountID.String() != acc {
		t.Fatalf("the visit is recorded against %v", written.AccountID)
	}
	// Twenty seconds of connection, and never the two minutes of grace on top.
	if written.Seconds != 20 {
		t.Fatalf("the visit records %d seconds; the grace was counted as time in the building", written.Seconds)
	}
}

func TestTheBuildingIsTornDownWhenTheLastPersonLeavesAndTheNextOneIsFresh(t *testing.T) {
	// Regeneration, and the rule that makes it safe: it happens ONLY when the
	// заброшка is empty, so nothing ever changes under somebody's feet, and the
	// next arrival is handed a different world id — which is exactly what tells
	// their client to fetch the geometry again.
	acc := uuid.New().String()
	member := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(member)
	s, tick := startService(t, tr, newFakeRepo())
	clk := newClock()

	sayHello(s, member)
	first, _ := s.World()

	// While he is in it, the building does not move.
	tickAndSettle(tick, clk.at(0))
	tickAndSettle(tick, clk.at(30*time.Second))
	if again, _ := s.World(); again != first {
		t.Fatal("the building was regenerated under somebody standing in it")
	}

	tr.setMembers(nil)
	tick <- clk.at(30*time.Second + AbandonGrace + time.Second)
	waitFor(t, tick, clk, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.world == nil
	})

	second, level := s.World()
	if second == first {
		t.Fatal("the next arrival walked into the building the last person emptied")
	}
	if level == nil || len(level.Sectors) < 2 {
		t.Fatal("the regenerated building is not one")
	}
}

func TestAnEmptyBuildingNobodyHasJoinedYetIsNotTornDown(t *testing.T) {
	// The gap between fetching the level over HTTP and the socket saying hello.
	// Tearing the world down in it would hand the client a building whose id no
	// longer matches the geometry it has just downloaded — for ever, because the
	// next fetch would open the same gap again.
	tr := newFakeTransport()
	s, tick := startService(t, tr, newFakeRepo())
	clk := newClock()

	before, _ := s.World()
	for i := 0; i < 20; i++ {
		tick <- clk.next()
	}
	after, _ := s.World()
	if after != before {
		t.Fatalf("a building nobody had joined was replaced: %v became %v", before, after)
	}
}

func TestForgettingSomebodyWritesNothing(t *testing.T) {
	// The admin «забыть» path: a visit belonging to somebody who is being erased
	// is not a result. It is called twice around the anonymising statement, so it
	// has to be idempotent, and for accounts that never played, so an unknown one
	// is a no-op.
	acc := uuid.New().String()
	member := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(member)
	repo := newFakeRepo()
	s, tick := startService(t, tr, repo)
	clk := newClock()

	sayHello(s, member)
	tickAndSettle(tick, clk.next())

	s.PurgeAccount(acc)
	s.PurgeAccount(acc)
	s.PurgeAccount(uuid.New().String())

	// Give the loop a few ticks to have written something, if it were going to.
	for i := 0; i < 5; i++ {
		tick <- clk.next()
	}
	select {
	case v := <-repo.inserted:
		t.Fatalf("erasing an account wrote a visit: %+v", v)
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.world != nil {
		t.Fatal("the building outlived the only person in it")
	}
}

func TestAReconnectingPlayerKeepsHisPlace(t *testing.T) {
	// The other side of the grace, and the reason it is minutes rather than
	// seconds: a page reload, a tunnel, or a phone locking all take a few
	// seconds, and losing somebody's place for any of them would make the game
	// unplayable on a bus.
	acc := uuid.New().String()
	member := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(member)
	repo := newFakeRepo()
	s, tick := startService(t, tr, repo)
	clk := newClock()

	sayHello(s, member)
	tickAndSettle(tick, clk.at(0))
	tr.setMembers(nil) // the socket drops
	tickAndSettle(tick, clk.at(30*time.Second))
	tr.setMembers([]realtime.Member{member}) // and comes back
	tickAndSettle(tick, clk.at(60*time.Second))

	s.mu.Lock()
	occupied := s.world != nil && s.world.Occupant(acc) != nil
	s.mu.Unlock()
	if !occupied {
		t.Fatal("a thirty-second absence took him out of the building")
	}
	select {
	case v := <-repo.inserted:
		t.Fatalf("a reconnect wrote a visit: %+v", v)
	default:
	}
}
