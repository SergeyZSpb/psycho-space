package gamevanyadum

import (
	"encoding/json"
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
	payload := `{"t":"vanyadum_input","seq":7,"cmds":[{"dt":1000,"mx":50,"my":-50,"yaw":1e18,"pitch":9}]}`
	got, in := ParseInbound([]byte(payload))
	if got != TypeInput || in == nil {
		t.Fatalf("got %q / %+v", got, in)
	}
	if in.Seq != 7 {
		t.Fatalf("seq %d", in.Seq)
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
		b.WriteString(`{"dt":0.05,"my":1}`)
	}
	b.WriteString(`]}`)

	_, in := ParseInbound([]byte(b.String()))
	if in == nil {
		t.Fatal("frame refused outright")
	}
	if len(in.Cmds) != MaxCommandsPerFrame {
		t.Fatalf("kept %d commands, cap is %d", len(in.Cmds), MaxCommandsPerFrame)
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
	raw, err := json.Marshal(Snapshot{T: TypeSnapshot, Left: []int{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"c"`, `"ev"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("empty field %s was serialised: %s", key, raw)
		}
	}
}

func TestSnapshotStaysSmall(t *testing.T) {
	// The bandwidth budget, as a test rather than as a comment. A snapshot goes
	// out twenty times a second forever, so its size is the game's recurring
	// cost — and the design doc's mitigations (integers, short keys, the level
	// never on a frame) are only worth anything if something notices when they
	// stop being applied.
	s := Snapshot{
		T: TypeSnapshot, Tick: 999999, Ack: 999999,
		X: 123456, Y: -123456, Z: 12345, Yaw: 3142, Sector: 12, Health: 100,
		Left: []int{0, 1, 2, 3, 4, 5},
		Bag:  map[string]int{"beer": 9},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// 200 bytes at 20 Hz is 4 KB/s, which is the figure the design doc budgets
	// against. If a field is added that pushes past this, the right response is
	// to measure and update the budget deliberately — not to raise the number
	// until the test passes.
	const budget = 200
	if len(raw) > budget {
		t.Fatalf("a full snapshot is %d bytes, budget is %d: %s", len(raw), budget, raw)
	}
}
