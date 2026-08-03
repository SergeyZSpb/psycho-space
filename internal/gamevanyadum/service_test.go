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
	// And which place in the building he has been given, which is the only thing
	// that ever tells him: a snapshot names everybody EXCEPT its own reader, so
	// without this he could read the whole standings and not know which row was
	// his.
	if got, ok := ready[0]["slot"]; !ok || got != float64(0) {
		t.Fatalf("the first arrival was told his place is %v", ready[0]["slot"])
	}
}

func TestAReconnectIsGivenBackTheSamePlace(t *testing.T) {
	// A page reload, a tunnel or a phone waking up produces a second hello, and
	// the man is still standing where he was — so he is still in the same place in
	// the building. A reconnect that moved him would tell every other client that
	// one figure had vanished and an unrelated one appeared, which is the one
	// thing a slot is not allowed to do while its holder is here.
	acc := uuid.New().String()
	m := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(m)
	s := NewService(tr, Room, nil, newFakeRepo())

	// Somebody else is in the building first, so the place under test is not
	// zero — which is the value an omitted field would read back as.
	sayHello(s, realtime.Member{ConnID: "c0", AccountID: uuid.New().String()})
	sayHello(s, m)
	sayHello(s, m)

	ready := tr.framesOfType(TypeReady)
	if len(ready) != 3 {
		t.Fatalf("three hellos were answered %d times", len(ready))
	}
	if ready[1]["slot"] == float64(0) {
		t.Fatal("the second arrival was given the first arrival's place")
	}
	if ready[1]["slot"] != ready[2]["slot"] {
		t.Fatalf("a reconnect moved him from place %v to %v", ready[1]["slot"], ready[2]["slot"])
	}
}

func TestAReconnectIsToldHowFarItsInputHadCounted(t *testing.T) {
	// An occupant outlives its socket for AbandonGrace, and its high-water mark
	// goes on outliving it too — so a page reload comes back to a world that has
	// already accepted sequences the rebuilt client is about to start counting
	// from zero again, and Enqueue drops every one of them as already seen. The
	// ready frame carries the mark so the client can resume from it instead of
	// spending a round trip discovering it from the first ack, and what that round
	// trip swallows is a trigger pull as readily as a step.
	acc := uuid.New().String()
	m := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(m)
	s := NewService(tr, Room, nil, newFakeRepo())

	sayHello(s, m)
	// A fresh arrival has counted nothing, and the field is omitted rather than
	// sent as a zero.
	ready := tr.framesOfType(TypeReady)
	if _, present := ready[0]["seq"]; present {
		t.Fatalf("a first arrival was sent a resume hint: %v", ready[0])
	}

	// He plays for a while. Enqueue is what advances the mark, so the input goes
	// through the real door rather than being written onto the occupant.
	s.HandleInbound(context.Background(), m, Room, []byte(
		`{"t":"`+TypeInput+`","k":0,"cmds":[{"q":1,"dt":0.025,"my":1},{"q":2,"dt":0.025,"my":1},{"q":3,"dt":0.025,"my":1}]}`))

	// The tab reloads: a new connection, the same account, well inside the grace.
	back := realtime.Member{ConnID: "c2", AccountID: acc}
	tr.setMembers([]realtime.Member{back})
	sayHello(s, back)

	ready = tr.framesOfType(TypeReady)
	if len(ready) != 2 {
		t.Fatalf("two hellos were answered %d times", len(ready))
	}
	if got, ok := ready[1]["seq"].(float64); !ok || int64(got) != 3 {
		t.Fatalf("the reload was told to resume from %v, the world has accepted 3", ready[1]["seq"])
	}
	// And it is the world's own number rather than a count of frames or of
	// anything else this test could have arranged by accident.
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := s.world.Occupant(acc).highSeq; got != 3 {
		t.Fatalf("the occupant's mark is %d", got)
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
	// two occupants, and a frame per occupant naming the OTHER one — by the slot
	// he holds, which the standings frame turns into a handle.
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
	saw := map[string]int{}
	for _, m := range tr.sent {
		var f Snapshot
		if json.Unmarshal(m.body, &f) != nil || f.T != TypeSnapshot || len(f.Peers) == 0 {
			continue
		}
		saw[m.connID] = f.Peers[0].Slot
	}
	if len(saw) != 2 {
		t.Fatalf("only these connections were told about anybody else: %v", saw)
	}
	if saw["ca"] == saw["cb"] {
		t.Fatalf("both frames name slot %d — somebody is looking at himself", saw["ca"])
	}

	// And the name that goes with each slot is on the standings, where it is sent
	// once a second instead of twenty times. A HANDLE AND NEVER AN ACCOUNT
	// (ADR-037): an account id is a UUID, far longer than the twelve base64url
	// characters a pseudonym is.
	named := map[int]string{}
	for _, m := range tr.sent {
		var board Standings
		if json.Unmarshal(m.body, &board) != nil || board.T != TypeStandings {
			continue
		}
		for _, r := range board.Rows {
			named[r.Slot] = r.Name
		}
	}
	for conn, slot := range saw {
		name, ok := named[slot]
		if !ok {
			t.Fatalf("%s was shown slot %d and never told whose it is: %v", conn, slot, named)
		}
		if len(name) != 12 {
			t.Fatalf("%s's neighbour is called %q, which is not a handle", conn, name)
		}
	}
}

func TestTheStandingsGoOutOnceASecondAndNotOnEveryTick(t *testing.T) {
	// The rate is the whole reason this is its own frame. Twenty a second would
	// cost twenty times as much to restate numbers that move a few times a minute
	// — which is exactly what putting them on the snapshot would have done.
	acc := uuid.New().String()
	member := realtime.Member{ConnID: "c1", AccountID: acc}
	tr := newFakeTransport(member)
	s, tick := startService(t, tr, newFakeRepo())
	clk := newClock()

	sayHello(s, member)
	// Three seconds of ticks, driven on the tick's own clock. ONE SEND PER TICK
	// and a barrier at the end, rather than tickAndSettle throughout: that helper
	// sends twice, so a loop built on it would advance the world twice as far as
	// the seconds it is counting.
	const seconds = 3
	for i := 0; i < seconds*SimHz; i++ {
		tick <- clk.next()
	}
	tickAndSettle(tick, clk.next())

	snapshots := len(tr.framesOfType(TypeSnapshot))
	boards := len(tr.framesOfType(TypeStandings))
	if snapshots < seconds*SimHz {
		t.Fatalf("only %d snapshots in %d seconds of ticks", snapshots, seconds)
	}
	// AN EXACT COUNT, BECAUSE THE CADENCE IS EXACT. A range wide enough to be
	// comfortable is a range that lets twice the intended rate through, which is
	// the one thing this frame's whole existence rests on not happening. Two
	// terms, both of them deliberate:
	//
	//	the first tick    1   joining moved the roster, and a snapshot may not name
	//	                      a slot the reader has never been given a name for —
	//	                      which is also the tick that boards this connection
	//	the seconds       3   ticks 20, 40 and 60 of the 60 driven here
	//
	// It is not a race with the tick loop either: the barrier above guarantees
	// steps 1 to seconds*SimHz+1 have completed, and the one step that may still
	// be running is past the last multiple of standingsEvery, moves no roster, and
	// has no unboarded connection to serve — so it cannot publish anything.
	want := 1 + seconds*SimHz/int(standingsEvery)
	if boards != want {
		t.Fatalf("%d standings frames in %d seconds of ticks, expected exactly %d, alongside %d snapshots",
			boards, seconds, want, snapshots)
	}
}

func TestASnapshotNeverNamesASlotTheStandingsHaveNot(t *testing.T) {
	// The invariant that makes a slot safe to be the only name on a repeating
	// frame. A peer arrives as a bare number, so a client that has not been told
	// whose number it is can neither label the figure nor tell that the place has
	// changed hands since it last drew somebody there — and interpolating a
	// newcomer from where the last holder stood draws a man sliding across the
	// building.
	//
	// Once a second is not enough on its own, which is why the roster CHANGING
	// publishes one too, ahead of the snapshot on the same connection. Here the
	// second player joins mid-second, precisely where a naive once-a-second
	// cadence would leave the first player looking at a stranger.
	//
	// AND THE ROSTER IS NOT ENOUGH EITHER, which is what the third act is for. A
	// new CONNECTION belonging to somebody already in the building changes no
	// roster at all — Join hands back the occupant he already is — so nothing was
	// published for it, while the tick sent it snapshots naming everybody. It is
	// not the "a frame was dropped" case: a page reload, a tunnel coming back and a
	// second device all produce one, and a client that kept its old directory
	// across the outage labels a place that changed hands with the previous
	// holder's handle and interpolates him from the previous holder's positions —
	// exactly the mislabelling a slot is not allowed to cause.
	a, b := uuid.New().String(), uuid.New().String()
	ma := realtime.Member{ConnID: "ca", AccountID: a}
	mb := realtime.Member{ConnID: "cb", AccountID: b}
	tr := newFakeTransport(ma)
	s, tick := startService(t, tr, newFakeRepo())
	clk := newClock()

	sayHello(s, ma)
	// Off the second boundary on purpose.
	for i := 0; i < 7; i++ {
		tickAndSettle(tick, clk.next())
	}
	tr.setMembers([]realtime.Member{ma, mb})
	sayHello(s, mb)
	for i := 0; i < 5; i++ {
		tickAndSettle(tick, clk.next())
	}

	// A second device for a, and a rebuilt socket for b. Both are new connections
	// for occupants who never left, and the ticks that follow stay clear of the
	// next second boundary so nothing rescues them by cadence.
	//
	// THE SECOND DEVICE IS TICKED BEFORE IT SAYS HELLO, which is not an odd case
	// but the ordinary one: a socket joins the room at the upgrade and is a
	// snapshot's destination from the very next tick, a whole round trip before
	// its hello can arrive. Answering the hello is therefore too late by
	// construction, and a fix that only answered it would leave exactly this
	// window mislabelling a reconnect.
	ma2 := realtime.Member{ConnID: "ca2", AccountID: a}
	mb2 := realtime.Member{ConnID: "cb2", AccountID: b}
	tr.setMembers([]realtime.Member{ma, ma2, mb2})
	for i := 0; i < 3; i++ {
		tickAndSettle(tick, clk.next())
	}
	sayHello(s, ma2)
	sayHello(s, mb2)
	for i := 0; i < 3; i++ {
		tickAndSettle(tick, clk.next())
	}

	// Replayed in the order the connection received them, which is what the
	// invariant is actually about: it is not enough for a standings to exist
	// somewhere, it has to have arrived FIRST.
	tr.mu.Lock()
	defer tr.mu.Unlock()
	known := map[string]map[int]bool{}
	for _, m := range tr.sent {
		var env struct {
			T string `json:"t"`
		}
		if json.Unmarshal(m.body, &env) != nil {
			continue
		}
		switch env.T {
		case TypeStandings:
			var board Standings
			if json.Unmarshal(m.body, &board) != nil {
				continue
			}
			seen := map[int]bool{}
			for _, r := range board.Rows {
				seen[r.Slot] = true
			}
			known[m.connID] = seen
		case TypeSnapshot:
			var f Snapshot
			if json.Unmarshal(m.body, &f) != nil {
				continue
			}
			for _, p := range f.Peers {
				if !known[m.connID][p.Slot] {
					t.Fatalf("%s was sent a peer in slot %d before any standings named it (knew: %v)",
						m.connID, p.Slot, known[m.connID])
				}
			}
		}
	}
	// And the invariant is not vacuously true because nobody was ever a peer — per
	// connection, not in total, since the reconnects are the whole point and a
	// count over the lot would be carried by the first two sockets alone.
	peers := map[string]int{}
	for _, m := range tr.sent {
		var f Snapshot
		if json.Unmarshal(m.body, &f) == nil && f.T == TypeSnapshot {
			peers[m.connID] += len(f.Peers)
		}
	}
	for _, conn := range []string{"ca", "cb", "ca2", "cb2"} {
		if peers[conn] == 0 {
			t.Fatalf("no snapshot sent to %s ever carried a peer, so this proves nothing about it: %v",
				conn, peers)
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

	// He found two bottles and put one of them in the gun, which is what a player
	// does with beer now that it is the ammunition. Set directly rather than
	// walked to, because what is being tested here is what gets WRITTEN.
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		o := s.world.Occupant(acc)
		o.collected = map[string]int{"beer": 2}
		o.State.Counters["beer"] = 1
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
	// And what he FOUND rather than what he still had. The two were the same
	// number until the gun started spending beer; the column means the former and
	// the migration that says so is immutable, so a visit that read the bag would
	// quietly record a nought for everybody who actually played.
	if written.Beer != 2 {
		t.Fatalf("the visit records %d bottles; he found two and reloaded with one", written.Beer)
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
