package gamevanyadum

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseInboundIgnoresEverythingItDoesNotUnderstand(t *testing.T) {
	// Silence is the platform's policy for a bad frame: no reply, no error, no
	// log line. A log per bad frame at the permitted ten a second is a flood
	// lever handed to any client, so "returns nothing" is the behaviour to pin.
	for _, payload := range []string{
		``,
		`not json at all`,
		`{}`,
		`{"t":"vanyagotchi_do","verbs":["drink"]}`, // another game's frame
		`{"t":"vanyadum_input"`,                    // truncated
		`[1,2,3]`,
		`null`,
	} {
		if got, in := ParseInbound([]byte(payload)); got != "" || in != nil {
			t.Fatalf("payload %q produced %q / %+v", payload, got, in)
		}
	}
}

func TestParseInboundReadsHello(t *testing.T) {
	// Deliberately empty: identity is the connection, so there is nothing in a
	// hello to forge and nothing to validate.
	got, in := ParseInbound([]byte(`{"t":"vanyadum_hello"}`))
	if got != TypeHello || in != nil {
		t.Fatalf("got %q / %+v", got, in)
	}
}

func TestParseInboundSanitisesEveryCommand(t *testing.T) {
	// The edge does not have to remember to sanitise, because it cannot forget:
	// parsing produces sanitised commands and Step sanitises again. Both, on
	// purpose — this is the boundary a hostile frame crosses.
	payload := `{"t":"vanyadum_input","cmds":[{"q":7,"dt":1000,"mx":50,"my":-50,"yaw":1e18,"pitch":9}]}`
	got, in := ParseInbound([]byte(payload))
	if got != TypeInput || in == nil {
		t.Fatalf("got %q / %+v", got, in)
	}
	// The sequence is per COMMAND now, not per frame: reconciliation has to be
	// able to hear "I applied three of your four", and a frame-level number
	// cannot say that.
	if in.Cmds[0].Seq != 7 {
		t.Fatalf("seq %d", in.Cmds[0].Seq)
	}
	c := in.Cmds[0]
	if c.Dt != MaxStepSeconds || c.MX != 1 || c.MY != -1 || c.Pitch != MaxPitch {
		t.Fatalf("not clamped: %+v", c)
	}
	if math.IsNaN(c.Yaw) || math.IsInf(c.Yaw, 0) {
		t.Fatalf("yaw survived as %v", c.Yaw)
	}
	// The wrap is what keeps a huge-but-legal yaw from reaching trigonometry as
	// a number with no precision left in it.
	if math.Abs(c.Yaw) > 2*math.Pi {
		t.Fatalf("yaw was not wrapped: %v", c.Yaw)
	}
}

func TestAFloatTooLargeForJSONDropsTheWholeFrame(t *testing.T) {
	// encoding/json refuses a float outside float64's range before this package
	// sees it, so the frame is malformed rather than hostile — and malformed
	// gets the same silence everything else does. Pinned because it is the one
	// hostile value that never reaches Sanitise, and a reader of that function
	// would reasonably assume otherwise.
	if got, in := ParseInbound([]byte(`{"t":"vanyadum_input","cmds":[{"dt":1e309}]}`)); got != "" || in != nil {
		t.Fatalf("got %q / %+v", got, in)
	}
}

func TestParseInboundDropsSurplusCommands(t *testing.T) {
	// A frame carrying more sub-steps than the sampling ratio allows is a client
	// asking for extra simulation time. The surplus is dropped rather than the
	// frame refused: an honest client that drifted by one step keeps playing,
	// and a dishonest one gains nothing.
	var b strings.Builder
	b.WriteString(`{"t":"vanyadum_input","seq":1,"cmds":[`)
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"q":%d,"dt":0.05,"my":1}`, i+1)
	}
	b.WriteString(`]}`)

	_, in := ParseInbound([]byte(b.String()))
	if in == nil {
		t.Fatal("frame refused outright")
	}
	// The cap is the sampling ratio PLUS the redundancy window, because a frame
	// legally repeats commands the server has not acknowledged. What bounds
	// simulation is the arena's time budget, not this number.
	if want := MaxCommandsPerFrame + RedundantCommands; len(in.Cmds) != want {
		t.Fatalf("kept %d commands, cap is %d", len(in.Cmds), want)
	}
}

func TestQuantisation(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want int
	}{{0, 0}, {1, 100}, {1.004, 100}, {1.006, 101}, {-2.5, -250}} {
		if got := cm(tc.in); got != tc.want {
			t.Fatalf("cm(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if got := mrad(math.Pi); got != 3142 {
		t.Fatalf("mrad(pi) = %d", got)
	}
}

func TestSnapshotOmitsWhatIsEmpty(t *testing.T) {
	// Prefer omitting to sending empty (CLAUDE.md). At twenty frames a second
	// the difference between an absent field and `"c":{}` is real money on
	// somebody's mobile data.
	raw, err := json.Marshal(Snapshot{T: TypeSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"c"`, `"ev"`, `"p"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("empty field %s was serialised: %s", key, raw)
		}
	}
}

func TestTheRemainingPickupMaskRoundTrips(t *testing.T) {
	// It is one word on the wire and it has to come back as the same word. The
	// interesting values are the ends: an empty level, a full one, and the top
	// bit, which is the one a signed or narrower field would mangle.
	for _, want := range []uint32{0, 1, 0b1010, 1<<MaxWirePickups - 1, 1 << (MaxWirePickups - 1)} {
		raw, err := json.Marshal(Snapshot{T: TypeSnapshot, Left: want})
		if err != nil {
			t.Fatal(err)
		}
		var back Snapshot
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("mask %b did not survive its own encoding: %v (%s)", want, err, raw)
		}
		if back.Left != want {
			t.Fatalf("mask %b came back as %b: %s", want, back.Left, raw)
		}
	}
}

func TestTheMaskIsNarrowEnoughForABrowserToParse(t *testing.T) {
	// WHY THE FIELD IS 32 BITS AND NOT 64. A JSON number is an IEEE754 double on
	// the other end, so a mask wider than a double's 53-bit mantissa loses its
	// high bits in the PARSE — silently, and only on the levels large enough to
	// reach them. This pins the reason rather than the choice: if it ever stops
	// being true, the constant is worth revisiting.
	var widest float64
	if err := json.Unmarshal([]byte("4294967295"), &widest); err != nil {
		t.Fatal(err)
	}
	if uint32(widest) != 1<<MaxWirePickups-1 {
		t.Fatalf("the widest %d-bit mask does not survive a double: %v", MaxWirePickups, widest)
	}

	// And the counter-example that makes the width a real limit rather than a
	// superstition: 2^53+1 does not survive, so a 64-bit mask genuinely could not
	// be read back. A level that outgrows 32 pickups gets a second word.
	var lost float64
	if err := json.Unmarshal([]byte("9007199254740993"), &lost); err != nil {
		t.Fatal(err)
	}
	if uint64(lost) == 9007199254740993 {
		t.Fatal("a double now parses 2^53+1 exactly; the reason Left is not a uint64 has changed")
	}
}

func TestNoGeneratedLevelOverflowsTheMask(t *testing.T) {
	// The guard on the generator. Go evaluates a shift at or past a word's width
	// as zero, so a level with more than MaxWirePickups pickups would publish the
	// surplus as ALREADY TAKEN: the client would never draw them, nobody would
	// ever walk to them, and the run's objective could never be met. A silently
	// unwinnable game rather than an error — which is why the bound is asserted
	// here and not discovered by a player.
	//
	// A guard against a future change rather than a regression test: the
	// generator scatters two or three today, and this is where raising that past
	// the wire's width gets caught.
	for seed := int64(0); seed < 500; seed++ {
		if n := len(Generate(seed).Pickups); n > MaxWirePickups {
			t.Fatalf("seed %d generated %d pickups; the wire carries %d", seed, n, MaxWirePickups)
		}
	}
}

func TestSnapshotStaysSmall(t *testing.T) {
	// The bandwidth budget, as a test rather than as a comment. A snapshot goes
	// out twenty times a second forever, so its size is the game's recurring
	// cost — and the design doc's mitigations (integers, short keys, the level
	// never on a frame) are only worth anything if something notices when they
	// stop being applied.
	//
	// THE BUDGET MOVED FROM 200 TO 160 WITH THE PICKUP MASK, and here is the
	// arithmetic rather than a shrug. Measured: this frame was 139 bytes with the
	// old six-id list (`"pk":[0,1,2,3,4,5]`, 18 of them) and is 136 with the mask
	// at its widest (`"pk":4294967295`, 15) — three bytes, which on its own would
	// not be worth a comment.
	//
	// What is worth it is WHY the old ceiling was 61 bytes above the measurement:
	// the list grew with the level's contents, so the slack was standing in for a
	// field nobody could bound. The mask cannot grow — it is one word whatever
	// the заброшка holds, and the value below is the widest it can ever take — so
	// that slack has nothing left to insure against and comes off. 160 leaves 24
	// bytes for a field the next iteration adds, and 160 at 20 Hz is 3.2 KB/s per
	// player against the 4 KB/s the design doc budgets.
	s := Snapshot{
		T: TypeSnapshot, Tick: 999999, Ack: 999999,
		X: 123456, Y: -123456, Z: 12345, Yaw: 3142, Sector: 12, Health: 100,
		Left: math.MaxUint32,
		Bag:  map[string]int{"beer": 9},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// If a field is added that pushes past this, the right response is to measure
	// and move the budget deliberately — with the arithmetic written down, as
	// above — and never to raise the number until the test passes.
	const budget = 160
	if len(raw) > budget {
		t.Fatalf("a full snapshot is %d bytes, budget is %d: %s", len(raw), budget, raw)
	}
}

// wireCeiling is what one viewer's socket may cost, in bytes per second.
//
// It is the number this game's design named as the point at which interest
// management stops being optional, and it is the budget for everything that
// REPEATS — because a phone does not care which frame type the bytes belonged
// to, and what it can afford is a steady rate rather than an instant.
//
// TWO THINGS ARE OUTSIDE THE ARITHMETIC BELOW AND NEITHER IS UNBOUNDED. Saying
// so is the point: a test that counted one board a second while the server sends
// more than that would be a ceiling that agrees with itself rather than with the
// wire.
//
//   - STANDINGS FRAMES OUT OF TURN. One goes to everybody on the tick the roster
//     changes, and one goes to a single connection the first time it is served
//     and again on the tick after each accepted hello (service.go, the ledger).
//     The roster moves only when somebody genuinely joins or is taken out past
//     AbandonGrace, so at MaxOccupants that is a handful of extra frames a minute
//     — noise against 20 snapshots a second. The hello is the larger term and it
//     is self-inflicted: the socket allows ten inbound frames a second
//     (internal/realtime/conn.go) and a tick sends a connection at most one
//     board, so a client that sent nothing but hellos could pull ten a second,
//     ~2.9 kB/s, to its OWN socket and to nobody else's. It is bounded by a rate
//     limit that already exists, it costs an honest client one frame per attach,
//     and the headroom below absorbs it.
//   - Snapshot.Events. They ride the snapshot, are delivered ONCE and cleared, and
//     the only one that exists is a pickup at ~28 bytes. A thing can be collected
//     again only after PickupRespawn, so a player standing on the densest heap a
//     level can hold sustains at most MaxWirePickups/PickupRespawn ≈ 1 event a
//     second, ~28 B/s. The instantaneous burst — several things collected on one
//     tick — is bounded by the same 32.
//
// Folding either in would mean inventing a worst case for a term two orders of
// magnitude under the headroom, so they are stated and left out. If a future
// event fires per tick rather than per action, it belongs INSIDE the arithmetic
// and not in this list.
const wireCeiling = 8000

// worstSnapshot is the widest frame the wire can carry for a building of n
// people — deliberately larger than a generated level produces, because the
// capacity is budgeted on the worst case and a phone on bad mobile data is
// precisely when the worst case is the case.
//
// Yaw is at the far end of the wrapped range (five characters where an ordinary
// one is four), positions are ±1234 m where a заброшка is tens of metres across,
// and the pickup mask is every bit set.
func worstSnapshot(n int) Snapshot {
	s := Snapshot{
		T: TypeSnapshot, Tick: 999999, Ack: 999999,
		X: 123456, Y: -123456, Z: 12345, Yaw: -6283, Sector: 12, Health: 100,
		Left: math.MaxUint32,
		Bag:  map[string]int{"beer": 9},
	}
	// Everybody else, and NOT a peer fewer because some of them were filtered
	// out: interest management makes the typical frame smaller and does nothing
	// at all for the worst case, which is everybody standing in one room.
	for i := 0; i < n-1; i++ {
		s.Peers = append(s.Peers, Peer{Slot: 9, X: 123456, Y: -123456, Sector: 12, Yaw: -6283})
	}
	return s
}

// worstStandings is the widest standings frame for a building of n people: a
// twelve-character pseudonym each, a bag, and a stay of eleven days.
func worstStandings(n int) Standings {
	b := Standings{T: TypeStandings}
	for i := 0; i < n; i++ {
		b.Rows = append(b.Rows, StandingsRow{
			Slot: 9, Name: "K3jf9sLm2QpZ", Seconds: 999999, Bag: map[string]int{"beer": 9},
		})
	}
	return b
}

func TestEverythingAFullBuildingSendsAViewerFitsTheCeiling(t *testing.T) {
	// THE SOLO FRAME IS THE FLOOR, NOT THE COST, and MaxOccupants is what this
	// test exists to derive. A viewer pays two things: a snapshot built for them
	// SnapshotHz times a second carrying everybody else, and a standings frame
	// once a second carrying everybody including them. Both are counted, because
	// leaving the cheaper one out would be moving the ceiling rather than meeting
	// it — which is how the constant came to be six before anybody measured a
	// peer.
	//
	// Measured at the widest quantisation the wire can carry: 137 bytes of self,
	// +56 for the first peer (the entry plus the `p` array around it), +50 for
	// each one after; a standings frame is 81 bytes with one row and +53 a row
	// after that. So a full house of five is 343 × 20 + 293 = 7153 B/s, where six
	// would be 393 × 20 + 346 = 8206 and over. That is the whole derivation of
	// MaxOccupants, and this is what turns it into a gate: raising the constant,
	// or growing either frame, fails here rather than on somebody's mobile data.
	//
	// WHAT IS NOT COUNTED, AND WHY IT DOES NOT HAVE TO BE, is on wireCeiling: the
	// out-of-turn standings frames and a snapshot's events, both bounded and both
	// far under the ~850 B/s of headroom this leaves. Nor is the peer array taken
	// filtered — interest management and the hold that keeps a peer on the frame a
	// moment after he leaves the visible set (visibleHold) both act on a set that
	// is already whole here, because the worst case is everybody in one room.
	snap, err := json.Marshal(worstSnapshot(MaxOccupants))
	if err != nil {
		t.Fatal(err)
	}
	board, err := json.Marshal(worstStandings(MaxOccupants))
	if err != nil {
		t.Fatal(err)
	}
	perSecond := len(snap)*SimHz + len(board)*int(time.Second/StandingsInterval)
	if perSecond > wireCeiling {
		t.Fatalf("a full building costs a viewer %d B/s, the ceiling is %d — %d-byte snapshot × %d Hz plus a %d-byte standings\nsnapshot: %s\nstandings: %s",
			perSecond, wireCeiling, len(snap), SimHz, len(board), snap, board)
	}

	// And the step this constant is one below really is over the line, so the
	// test cannot pass by the arithmetic having drifted somewhere generous.
	next, err := json.Marshal(worstSnapshot(MaxOccupants + 1))
	if err != nil {
		t.Fatal(err)
	}
	nextBoard, err := json.Marshal(worstStandings(MaxOccupants + 1))
	if err != nil {
		t.Fatal(err)
	}
	if over := len(next)*SimHz + len(nextBoard)*int(time.Second/StandingsInterval); over <= wireCeiling {
		t.Fatalf("%d people would cost %d B/s, inside the %d ceiling — MaxOccupants is a place too low",
			MaxOccupants+1, over, wireCeiling)
	}
}

func TestNoNameAndNoScoreRidesTheRepeatingFrame(t *testing.T) {
	// The rule the standings frame exists to keep. Two kinds of thing must never
	// be on a snapshot: a value that is CONSTANT for the life of an occupant (a
	// pseudonym), and one that changes a few times a MINUTE (a score). Both would
	// be paid for twenty times a second, per occupant, per viewer, to restate
	// something nobody reads at frame rate.
	//
	// Asserted over the serialised bytes rather than over the struct, because
	// what this forbids is a field REACHING THE WIRE — a helpfully-named Go field
	// with the wrong json tag would pass any check made against the type.
	raw, err := json.Marshal(worstSnapshot(MaxOccupants))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "K3jf9sLm2QpZ") {
		t.Fatalf("a pseudonym is riding the snapshot: %s", raw)
	}
	var back struct {
		Peers []map[string]any `json:"p"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Peers) != MaxOccupants-1 {
		t.Fatalf("a full house published %d peers", len(back.Peers))
	}
	// Five numbers and nothing else — where, which room, which way round, and
	// which place in the building. A sixth is not forbidden for ever (the pose
	// enum comes back with whatever first does damage) but it costs 20 × N × V
	// bytes a second, so it fails here first and gets argued for in the commit.
	for _, p := range back.Peers {
		if len(p) != 5 {
			t.Fatalf("a peer entry carries %d fields, not the five it is budgeted for: %v", len(p), p)
		}
		for _, key := range []string{"n", "x", "y", "s", "yaw"} {
			if _, ok := p[key]; !ok {
				t.Fatalf("a peer entry has no %q: %v", key, p)
			}
		}
	}
}

func TestTheStandingsSayNothingAboutAnEmptyBag(t *testing.T) {
	// Prefer omitting to sending empty, at 1 Hz as much as at 20: everybody's
	// first minute in the building is spent carrying nothing, and `"c":{}` a
	// second per occupant per viewer is bytes spent to say so.
	raw, err := json.Marshal(Standings{
		T:    TypeStandings,
		Rows: []StandingsRow{{Slot: 0, Name: "K3jf9sLm2QpZ", Seconds: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"c"`) {
		t.Fatalf("an empty bag was serialised: %s", raw)
	}
	// And the slot IS sent at zero, on both frames. It is the first place handed
	// out, so omitting it would put the commonest occupant in nobody's table.
	if !strings.Contains(string(raw), `"n":0`) {
		t.Fatalf("slot zero was omitted: %s", raw)
	}
	peer, err := json.Marshal(Peer{Slot: 0, X: 0, Y: 0, Sector: 0, Yaw: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(peer); got != `{"n":0,"x":0,"y":0,"s":0,"yaw":0}` {
		t.Fatalf("a peer standing at the origin of slot zero serialised as %s", got)
	}
}
