package gamefintech

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
		`{"t":"fintech_input"`,             // truncated
		`{"t":"fintech_snap","x":1}`,       // one of ours, but outbound
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
	got, cmds := ParseInbound([]byte(`{"t":"fintech_hello"}`))
	if got != TypeHello || cmds != nil {
		t.Fatalf("got %q / %+v", got, cmds)
	}
}

func TestParseInboundReadsAnInputFrame(t *testing.T) {
	got, cmds := ParseInbound([]byte(
		`{"t":"fintech_input","k":812,"cmds":[{"q":41,"dt":0.05,"mx":0,"my":-1,"d":true}]}`))
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
	_, hostile := ParseInbound([]byte(`{"t":"fintech_input","cmds":[{"q":1,"dt":1000,"mx":50}]}`))
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
	if got, cmds := ParseInbound([]byte(`{"t":"fintech_input","cmds":[{"dt":1e309}]}`)); got != "" || cmds != nil {
		t.Fatalf("got %q / %+v", got, cmds)
	}
}

func TestASurplusOfCommandsIsTrimmedAndTheFrameIsStillAccepted(t *testing.T) {
	// REFUSING A FRAME FOR BEING GENEROUS IS HOW A LOSSY CLIENT GETS STUCK. The
	// surplus buys nothing anyway: the office drops every command whose sequence
	// it has already applied, and the time budget is what bounds simulation.
	var b strings.Builder
	b.WriteString(`{"t":"fintech_input","k":1,"cmds":[`)
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
	if err := o.Join("a", "s", "p-a", "", 0, time.Now()); err != nil {
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
	// both balloons on the LAST line of their pool — which is the widest either
	// index can be, and stays the widest as the pools grow, so this bound does
	// not quietly stop being the worst case the day somebody adds a joke.
	s := Snapshot{
		T: TypeSnapshot, Tick: 36000, Ack: 71999,
		X: 1565, Y: 2165, Pay: 648000, M: 300, St: 30000, Dc: 4000, Rc: 20000,
		P: len(PlayerLines) - 1,
		B: BossFrame{X: 1560, Y: 2160, G: 255, P: len(BossLines) - 1, D: 8000},
		// Somebody bought him a round moments ago: he is still wobbling AND the
		// bottle has not come back. Both present at once is the real worst case,
		// even though it only lasts as long as he stays drunk.
		Bt: 10000, Bs: len(BottleSpots) - 1,
		// AND A CLOUD, on you and on both colleagues at once, with the кальян also
		// gone and its next spot the widest index. Four fields that are all
		// omitempty and are all present together only in the few seconds after
		// somebody smokes — which is exactly what a worst case is for.
		Iv: 10000, Hk: 20000, Hs: len(HookahSpots) - 1,
		// AND CLAUDE, who is ALWAYS on the floor — the one unconditional addition in
		// this batch — with his cigarette lit and his widest line index, plus the
		// slow he leaves behind on you and on both colleagues.
		Cl: ClaudeFrame{X: 1560, Y: 2160, C: 255, P: len(ClaudeLines) - 1},
		Sl: 4000,
		// A FULL OFFICE, which is the worst case and is now most of the frame.
		// MaxOccupants is three, so two peers; twelve-character handles, both in
		// the far corner, both on the widest line index in the pool.
		Pr: []PeerFrame{
			{I: "AbCdEfGhIjKl", X: 1565, Y: 2165, P: len(PlayerLines) - 1, Iv: 10000, Sl: 4000},
			{I: "MnOpQrStUvWx", X: 1565, Y: 2165, P: len(PlayerLines) - 1, Iv: 10000, Sl: 4000},
		},
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
	//
	// 152 -> 248, FOR PEERS, and measured the same way. A full office is two of
	// them at 42 bytes each — `,{"i":"AbCdEfGhIjKl","x":1565,"y":2165,"p":24}` —
	// plus 8 for the `,"pr":[…]` that carries them, so 93 in total and the frame
	// lands at 245. At 10 Hz that is 2.5 kB/s per viewer and 7.4 kB/s out of this
	// box with all three watching: still an order inside the platform's 4 KiB
	// frame limit, and the reason a design for three people can send full state
	// rather than deltas.
	//
	// What the pseudonym buys is most of that number back. The obvious shape —
	// a display name and an avatar URL beside each peer — is about 280 bytes a
	// peer, so the same office would be ~800 bytes of frame and 24 kB/s out of
	// the box, to re-send a face and a name that never change for the whole of a
	// shift (ADR-037). Twelve characters and one cached GET instead.
	//
	// SOLO IS UNCHANGED: `pr` is omitempty and a lone occupant has no peers, so
	// the commonest frame in this game did not grow by a byte.
	//
	// 248 -> 260, for the redirect verb's cooldown. `,"rc":20000` is eleven
	// bytes and lands on this frame only while somebody is actually cooling
	// down, which is twenty seconds in every shift with two people in it and
	// never at all in a solo one — it is omitempty like `dc`, for the same
	// reason. Measured at 256, so the bound keeps its few bytes of headroom.
	//
	// It buys the alternative shapes being avoided: a reply frame to say the
	// verb was accepted (a second message type, and a client that could believe
	// a verb the office refused), or a client-side timer (which would offer the
	// button back before the office would honour it).
	//
	// 260 -> 280, for «набухать лысого». `,"bt":45000` is twelve bytes and rides
	// only while the bottle is gone; `,"d":8000` is nine and rides only while he
	// is actually drunk, which is eight seconds of a forty-five second cycle.
	// Both at once is the eleven-byte overlap measured above. Neither is on a
	// frame in the state this game spends most of its time in, which is a sober
	// bald man and a bottle standing on the floor.
	//
	// 280 -> 288, for the bottle MOVING. `,"bs":5` is seven bytes, and it is
	// seven rather than twenty because the frame names which of the catalogue's
	// spots it is on rather than where the spot is — the same trick a balloon
	// plays with a sentence (ADR-037). The alternative, two quantised positions,
	// is about twenty bytes ten times a second per viewer, forever, to describe
	// something that changes once every ten seconds. Omitted on spot zero, like
	// every other index here.
	// 288 -> 336, for «кальян». Four new fields, all omitempty, and the arithmetic
	// is worth writing out because they are not present together for long:
	// `,"iv":10000` is twelve on your own frame, `,"hk":20000` is twelve, `,"hs":3`
	// is seven, and `,"iv":10000` on each of two peers is twelve each. That is 55
	// on a frame where all three occupants are simultaneously behind a cloud AND
	// the hookah has not come back — a state that lasts ten seconds at most and
	// requires everybody to have smoked at once.
	//
	// The state this game actually spends its time in has none of them: nobody
	// invincible and a hookah standing on the floor is the resting frame, and it is
	// the same size it was before this iteration. The peers' `iv` is the one that
	// could have been avoided and deliberately was not — an effect only its owner
	// can see is unfinished, and knowing which colleague the лысый can no longer
	// walk at is the single most useful thing to know about somebody else in the
	// room.
	//
	// 344 -> 424, for CLAUDE CODE, and this raise is different from every one before
	// it: most of it is UNCONDITIONAL. He is always on the floor, so
	// `,"cl":{"x":1560,"y":2160,"c":255,"p":11}` — about 40 bytes — rides every
	// single frame rather than only an interesting one. At 10 Hz that is 400 B/s per
	// viewer added to the resting state, which is the real cost of a second
	// antagonist and is stated here rather than buried.
	//
	// The rest is omitempty like everything else: `,"sl":4000` is eleven on your own
	// frame and eleven on each of two peers, present only during the four seconds
	// after he lands. A debuff is as public as a buff — «he is slowed» is who the
	// лысый will reach first — so the peers' copy is deliberate.
	//
	// Measured at 406 and budgeted at 424, keeping the headroom this bound has
	// always carried: the widest line index grows a character the day a pool passes
	// a hundred entries, and a bound with no slack fails on a commit that added a
	// joke rather than a field.
	const budget = 424
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
