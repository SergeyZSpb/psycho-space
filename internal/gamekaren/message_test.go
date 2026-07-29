package gamekaren

import (
	"encoding/json"
	"fmt"
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
		`{"t":"vanyadum_input","cmds":[]}`, // another game's frame
		`{"t":"karen_input"`,               // truncated
		`{"t":"karen_snap","x":1}`,         // one of ours, but outbound
		`[1,2,3]`,
		`null`,
	} {
		if got, cmds := ParseInbound([]byte(payload)); got != "" || cmds != nil {
			t.Fatalf("payload %q produced %q / %+v", payload, got, cmds)
		}
	}
}

func TestParseInboundReadsHello(t *testing.T) {
	// Deliberately empty: identity is the connection, so there is nothing in a
	// hello to forge and nothing to validate.
	got, cmds := ParseInbound([]byte(`{"t":"karen_hello"}`))
	if got != TypeHello || cmds != nil {
		t.Fatalf("got %q / %+v", got, cmds)
	}
}

func TestParseInboundReadsAnInputFrame(t *testing.T) {
	got, cmds := ParseInbound([]byte(
		`{"t":"karen_input","k":812,"cmds":[{"q":41,"dt":0.05,"mx":0,"my":-1,"d":true}]}`))
	if got != TypeInput {
		t.Fatalf("type %q", got)
	}
	if len(cmds) != 1 {
		t.Fatalf("got %d commands", len(cmds))
	}
	c := cmds[0]
	if c.Seq != 41 || c.Dt != 0.05 || c.MX != 0 || c.MY != -1 || !c.Dash {
		t.Fatalf("the frame decoded to %+v", c)
	}
	// UNSANITISED on purpose: Office.Enqueue is the single place that clamps, so
	// Step can stay a pure function of valid input and the golden vectors can
	// describe the simulation rather than the clamp.
	_, hostile := ParseInbound([]byte(`{"t":"karen_input","cmds":[{"q":1,"dt":1000,"mx":50}]}`))
	if hostile[0].Dt != 1000 {
		t.Fatalf("parsing clamped a field it should have left alone: %+v", hostile[0])
	}
	if got := Sanitise(hostile[0]); got.Dt != MaxStepSeconds || got.MX > 1 {
		t.Fatalf("Sanitise did not clamp it either: %+v", got)
	}
}

func TestAFloatTooLargeForJSONDropsTheWholeFrame(t *testing.T) {
	// encoding/json refuses a float outside float64's range before this package
	// sees it, so the frame is malformed rather than hostile — and malformed gets
	// the same silence everything else does. Pinned because it is the one
	// hostile value that never reaches Sanitise.
	if got, cmds := ParseInbound([]byte(`{"t":"karen_input","cmds":[{"dt":1e309}]}`)); got != "" || cmds != nil {
		t.Fatalf("got %q / %+v", got, cmds)
	}
}

func TestASurplusOfCommandsIsTrimmedAndTheFrameIsStillAccepted(t *testing.T) {
	// REFUSING A FRAME FOR BEING GENEROUS IS HOW A LOSSY CLIENT GETS STUCK. The
	// surplus buys nothing anyway: the office drops every command whose sequence
	// it has already applied, and the time budget is what bounds simulation.
	var b strings.Builder
	b.WriteString(`{"t":"karen_input","k":1,"cmds":[`)
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"q":%d,"dt":0.05,"my":1}`, i+1)
	}
	b.WriteString(`]}`)

	got, cmds := ParseInbound([]byte(b.String()))
	if got != TypeInput {
		t.Fatalf("a forty-command frame was refused outright: %q", got)
	}
	if len(cmds) != MaxInboundCommands {
		t.Fatalf("kept %d commands, cap is %d", len(cmds), MaxInboundCommands)
	}
	if MaxInboundCommands != 10 {
		t.Fatalf("the cap is %d — the client and the layout suite are written against 10", MaxInboundCommands)
	}
	// The FIRST ten, not the last: sequence order is what reconciliation runs
	// on, and dropping the oldest would leave a hole the client never fills.
	if cmds[0].Seq != 1 {
		t.Fatalf("the surplus was trimmed from the wrong end: first seq is %d", cmds[0].Seq)
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
	if got := ms(0.22); got != 220 {
		t.Fatalf("ms(0.22) = %d", got)
	}
	if got := hundredths(2.75); got != 275 {
		t.Fatalf("hundredths(2.75) = %d", got)
	}
	if got := grinByte(1); got != 255 {
		t.Fatalf("grinByte(1) = %d", got)
	}
	if got := grinByte(-4); got != 0 {
		t.Fatalf("grinByte clamps below zero to %d", got)
	}
	// Money TRUNCATES rather than rounds: a counter that only goes up must never
	// show a ruble that has not been earned.
	if got := rub(4299.99); got != 4299 {
		t.Fatalf("rub(4299.99) = %d", got)
	}
}

// The dash cooldown is the one duration the CLIENT SIMULATES FROM rather than
// merely displays — it is folded straight into the browser's own predicted
// player, because a still player emits no commands and so never runs the timer
// down locally. So zero on the wire has to mean zero here: `ms`'s round-to-
// nearest would report a sliver of cooldown as ready, the client would grant a
// dash the server is about to refuse, and the two would disagree by a whole
// burst — 5.5 m, nearly three times the snap threshold.
func TestADashCooldownIsNeverRoundedDownToReady(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float64
		want int
	}{
		{"exactly ready is ready", 0, 0},
		{"a negative sliver is ready", -1e-9, 0},
		{"floating-point residue is NOT ready", 4e-16, 1},
		{"a fifth of a millisecond is NOT ready", 0.0002, 1},
		{"half a millisecond is NOT ready", 0.0005, 1},
		{"a whole cooldown", 2.2, 2200},
		{"a value between milliseconds rounds up", 1.2341, 1235},
	} {
		if got := msUp(tc.in); got != tc.want {
			t.Fatalf("%s: msUp(%v) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
	// And the snapshot uses it, so the guarantee reaches the wire rather than
	// stopping at the helper.
	o := NewOffice()
	if err := o.Join("a", "s", time.Now()); err != nil {
		t.Fatalf("join: %v", err)
	}
	occ := o.occupants["a"]
	occ.State.DashCooldown = 4e-16
	raw, ok := o.SnapshotFor("a")
	if !ok {
		t.Fatal("no snapshot")
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.Dc != 1 {
		t.Fatalf("a cooldown of 4e-16 s went on the wire as dc=%d, which the client reads as ready", snap.Dc)
	}
}

func TestSnapshotOmitsWhatIsEmpty(t *testing.T) {
	// Prefer omitting to sending empty (CLAUDE.md). The dash is ready for most
	// of a shift, so `dc` is absent from most frames — ten times a second, per
	// viewer, forever.
	raw, err := json.Marshal(Snapshot{T: TypeSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"dc"`) {
		t.Fatalf("a ready dash was serialised: %s", raw)
	}
	// The streak IS always sent, including at zero: a bar reading empty is
	// information, and a client that had to distinguish absent from zero would
	// be a client with a bug waiting to happen.
	if !strings.Contains(string(raw), `"st"`) {
		t.Fatalf("the streak went missing at zero: %s", raw)
	}
}

func TestSnapshotStaysSmall(t *testing.T) {
	// THE BANDWIDTH BUDGET, AS A TEST RATHER THAN AS A COMMENT.
	//
	// The arithmetic, from the design's §5: a snapshot goes out at
	// SimHz/SnapshotEvery = 10 Hz, to every viewer, for as long as the shift
	// lasts. At the 152-byte budget that is 1.5 kB/s per viewer, and with the
	// office's three occupants 4.6 kB/s out of this box — an order inside the
	// platform's 4 KiB frame limit and nowhere near the ten-messages-a-second
	// inbound bound this design fits inside rather than loosening.
	//
	// The mitigations that buy it are the quantisation (centimetres, whole
	// rubles, hundredths, a byte of grin, milliseconds), the one-to-three
	// character keys, and the office never riding a frame. This test is what
	// notices when one of them stops being applied. If a field is added that
	// pushes past the budget, the right response is to measure and change the
	// budget deliberately — not to raise the number until the test passes.
	//
	// The values below are a deliberately expensive frame: half an hour into a
	// shift (36 000 ticks), a five-figure command sequence, a position in the
	// far corner, six figures of salary, the multiplier capped, a thirty-second
	// streak, the dash on cooldown so `dc` is present rather than omitted, and
	// BOTH balloons off their default so both `p` fields are present too.
	s := Snapshot{
		T: TypeSnapshot, Tick: 36000, Ack: 71999,
		X: 1565, Y: 2165, Pay: 648000, M: 300, St: 30000, Dc: 4000, P: 2,
		B: BossFrame{X: 1560, Y: 2160, G: 255, P: 8},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// 140 -> 152. The two balloon indexes cost `,"p":2` and `,"p":8` — six bytes
	// each, twelve in total, and only on a frame where somebody is not saying
	// their default line. MEASURED AND RAISED DELIBERATELY, per the paragraph
	// above: the alternative shape, putting the words themselves on the frame,
	// is what this pays for — `,"p":8` against `,"say":"ПРОСТО ПОСМОТРИ,
	// НИЧЕГО НЕ ДЕЛАЙ"` is 6 bytes against 72, ten times a second, per viewer.
	const budget = 152
	if len(raw) > budget {
		t.Fatalf("a full snapshot is %d bytes, budget is %d: %s", len(raw), budget, raw)
	}
	t.Logf("a full snapshot is %d of the %d byte budget: %s", len(raw), budget, raw)
}

func TestTheOfficeIsNeverOnAFrame(t *testing.T) {
	// It is a constant the client already fetched with the catalogue. Putting
	// eight desks on a payload that repeats ten times a second would be the
	// single most expensive mistake available in this game.
	raw, err := json.Marshal(Snapshot{T: TypeSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"desks"`, `"office"`, `"w"`, `"h"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("the office leaked onto a snapshot (%s): %s", key, raw)
		}
	}
}
