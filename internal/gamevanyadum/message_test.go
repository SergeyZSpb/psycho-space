package gamevanyadum

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
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

func TestAFullBuildingsFrameStaysInsideItsBudget(t *testing.T) {
	// THE FRAME ABOVE IS THE FLOOR, NOT THE COST. A snapshot is built per
	// occupant and carries everybody else, so what a viewer actually pays is that
	// frame plus MaxOccupants−1 peers — and that product, not the solo frame, is
	// what decides how many people fit in the заброшка.
	//
	// THE BUDGET IS THE CEILING, CONVERTED. 8 kB/s per viewer is the number this
	// game's design named as the point at which interest management stops being
	// optional, and at SnapshotHz it affords exactly 400 bytes a frame. That is
	// where the 400 comes from — it is not slack chosen to fit a measurement.
	//
	// Measured at the widest quantisation the wire can carry: 137 bytes of self,
	// then +78 for the first peer (the entry plus the `p` array around it) and
	// +72 for each one after, so a full house of four is 359 bytes — 7.2 kB/s per
	// viewer. Five would be 431 and 8.6 kB/s, which is over. That is the whole
	// derivation of MaxOccupants, and this test is what turns it into a gate:
	// raising the constant, or growing a peer, fails here rather than on
	// somebody's mobile data.
	//
	// THE BUDGET WAS 512 AND COULD NOT FIRE. It was set against a design estimate
	// of ~25 bytes a peer that measurement did not survive, so it sat 28 % past
	// the ceiling it existed to protect: a six-person house measured 503 bytes,
	// over the line and still comfortably inside the test.
	//
	// The values are deliberately larger than a generated level produces — ±1234 m
	// where a заброшка is tens of metres across, and a yaw at the far end of the
	// wrapped range, which is five characters where an ordinary one is four — so
	// this is an upper bound rather than a typical case. It is one byte wider than
	// the frame measured above for exactly that reason: that test asks what an
	// ordinary frame costs, this one asks what the worst one costs, and the worst
	// case is the right one to budget on because a phone on bad mobile data is
	// precisely when it is the case. A realistic peer is about 59 bytes.
	s := Snapshot{
		T: TypeSnapshot, Tick: 999999, Ack: 999999,
		X: 123456, Y: -123456, Z: 12345, Yaw: -6283, Sector: 12, Health: 100,
		Left: math.MaxUint32,
		Bag:  map[string]int{"beer": 9},
	}
	for i := 0; i < MaxOccupants-1; i++ {
		s.Peers = append(s.Peers, Peer{
			ID: "K3jf9sLm2QpZ", X: 123456, Y: -123456, Z: 12345, Yaw: -6283, State: 2,
		})
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	const budget = 400
	if len(raw) > budget {
		t.Fatalf("a full building's frame is %d bytes (%.1f kB/s at %d Hz), budget is %d: %s",
			len(raw), float64(len(raw))*SimHz/1000, SimHz, budget, raw)
	}
}
