package gamevanyadum

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
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

func TestAnOverFullFrameDropsTheStaleHalfAndKeepsTheFreshest(t *testing.T) {
	// WHICH END IS DROPPED IS THE WHOLE POINT OF THIS TEST, because the cap alone
	// does not say and the two answers are not equally good. A client composes a
	// frame as [redundancy tail…, fresh commands…], so the prefix is the half the
	// server has almost always already accepted and the suffix is the input that
	// has just happened — a trigger pull among it. Keeping the prefix used to
	// throw the newest commands away, which at a 250 ms round trip is 30 % of
	// frames and about one tap in a hundred arriving a frame late, resolved
	// against a world the player never aimed at.
	//
	// It is pinned rather than left to the parser's loop bounds because the
	// inversion is a one-character edit that breaks nothing else: every count in
	// every other test is identical either way, and the symptom is a shot that
	// silently misses.
	const n = MaxCommandsPerFrame + RedundantCommands
	const sent = 40

	var b strings.Builder
	b.WriteString(`{"t":"vanyadum_input","k":0,"cmds":[`)
	for i := 1; i <= sent; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		// The newest command is the one that pulls the trigger, which is the shape
		// this is really about: the player tapped, and the tap is at the end.
		fmt.Fprintf(&b, `{"q":%d,"dt":0.025,"my":1`, i)
		if i == sent {
			b.WriteString(`,"f":true`)
		}
		b.WriteString("}")
	}
	b.WriteString(`]}`)

	_, in := ParseInbound([]byte(b.String()))
	if in == nil || len(in.Cmds) != n {
		t.Fatalf("an over-full frame parsed to %+v", in)
	}
	for i, c := range in.Cmds {
		if want := int64(sent - n + 1 + i); c.Seq != want {
			t.Fatalf("kept command %d is q=%d, the freshest %d are q=%d…%d",
				i, c.Seq, n, sent-n+1, sent)
		}
	}
	if !in.Cmds[len(in.Cmds)-1].Fire {
		t.Fatal("the trigger pull at the end of an over-full frame was truncated away")
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
	// The gun's timers, which are seconds on the simulation and milliseconds on
	// the wire. Half a millisecond of rounding against a 350 ms cadence is a
	// thousandth of a frame, and it is corrected by the very next snapshot.
	for _, tc := range []struct {
		in   float64
		want int
	}{{0, 0}, {FireCooldownSeconds, 350}, {ReloadSeconds, 1500}, {0.0004, 0}, {0.0006, 1}} {
		if got := ms(tc.in); got != tc.want {
			t.Fatalf("ms(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestTheTriggerIsOmittedWhenItWasNotPulled(t *testing.T) {
	// A client emits forty sub-steps a second and pulls the trigger perhaps
	// three times in a busy one, so `"f":false` on every command would be nine
	// bytes forty times a second on the UPLINK — the worse half of a mobile
	// connection. Absent means "not pulled", which is the only reading an
	// idempotent per-command field can have.
	resting, err := json.Marshal(wireCommand{Seq: 9, Dt: 0.025, MY: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resting), `"f"`) {
		t.Fatalf("a command that pulled nothing carries a trigger: %s", resting)
	}

	// And it survives the trip, because the whole point is that the simulation
	// reads it. ParseInbound is the only door in.
	frame, err := json.Marshal(map[string]any{
		"t": TypeInput, "k": 0,
		"cmds": []map[string]any{
			{"q": 1, "dt": 0.025, "my": 1},
			{"q": 2, "dt": 0.025, "my": 1, "f": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	typ, in := ParseInbound(frame)
	if typ != TypeInput || in == nil || len(in.Cmds) != 2 {
		t.Fatalf("the frame did not parse: %v %+v", typ, in)
	}
	if in.Cmds[0].Fire {
		t.Fatal("a command with no trigger in it came back pulled")
	}
	if !in.Cmds[1].Fire {
		t.Fatal("a trigger pull was dropped between the wire and the simulation")
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
	for _, key := range []string{`"c"`, `"ev"`, `"p"`, `"d"`, `"r"`, `"dn"`, `"pr"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("empty field %s was serialised: %s", key, raw)
		}
	}
	// And the peer's state, which is absent for a man who is alive, unprotected
	// and did nothing on this tick — that being almost every peer on almost every
	// frame, and the whole reason the field can afford to exist at all.
	quiet, err := json.Marshal(Peer{Slot: 1, X: 100, Y: 200, Sector: 3, Yaw: 42})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(quiet), `"st"`) {
		t.Fatalf("a peer doing nothing carries a state: %s", quiet)
	}
	// The barrel count is the exception, and deliberately: an empty gun is
	// exactly the state a player most needs to see, and a resting one is FULL
	// rather than zero — so omitting at zero would save nothing while making an
	// absent field mean the worst case.
	if !strings.Contains(string(raw), `"b":0`) {
		t.Fatalf("the barrel count was omitted at zero: %s", raw)
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
	// that slack has nothing left to insure against and comes off. 180 at 20 Hz is
	// 3.6 KB/s per player against the 4 KB/s the design doc budgets for a solo
	// frame — and the peers are budgeted separately, against the ceiling.
	//
	// THE GUN SPENT MOST OF THOSE 24, and the arithmetic is here rather than in a
	// commit message because this is where the next person will look. Measured on
	// this fixture: `"b":2` is 6 bytes and always present, `"d":350` is 8 and
	// `"r":1500` is 9, both absent at rest. So 136 → 151 for the widest frame the
	// game can ACTUALLY send, which is the barrels and one timer — the two timers
	// cannot both be running (sim.go, Player, and the test named there).
	//
	// The fixture below sets both anyway, because a budget that leaned on an
	// invariant proved in another file would be a budget one refactor away from
	// being wrong.
	//
	// AND DYING ADDED TWO MORE TIMERS, `dn` and `pr`, on the same terms. Those two
	// cannot both be set — protection begins exactly when the down window ends —
	// and neither can be set alongside the gun's while it means a man on the
	// floor, since death clears the gun and a protected man may not pull the
	// trigger (sim.go, stepGun). The fixture carries all four: 19 bytes of
	// deliberate pessimism, bought for the same reason as before. The budget moves
	// from 160 to 180 to hold it.
	//
	// AND THE ШПРИЦ ADDED NO TIMER AT ALL, which is the whole of what it cost this
	// frame. An injection RIDES `dn` and is told apart from a respawn by `hp`
	// (message.go, Snapshot.Down), so the field above is unchanged and this
	// measurement did not move. What it does change is the pessimism: `dn` meaning
	// an injection CAN run alongside a gun timer — a man who has just fired walks
	// onto an ampoule with the cadence still on the clock — so the widest frame
	// the game can really send is now the barrels and TWO timers rather than one.
	// Four in the fixture is still a bound rather than a claim; it is just a
	// slightly less generous one than it was.
	//
	// AND THEN THE BAG CAME OFF, WHICH IS THE FIRST TIME THIS BUDGET HAS FALLEN.
	// Measured: `,"c":{"beer":9}` was 15 of the 179 bytes this fixture used to
	// produce, so the frame is 164 and the budget moves from 180 to 165. Its
	// reader — the predictor reconciling a gun that spent a bottle — was deleted
	// when ammunition became infinite, and what was left was a HUD cell fed twenty
	// times a second with a number that changes a few times a minute (message.go,
	// Snapshot.Events). It rides the standings now, where the same map already
	// went out unfiltered.
	//
	// THE SAVING IS BIGGER THAN THE FIFTEEN BYTES, because the field would not
	// have stayed fifteen: the ceiling on the counter went with it (content.go,
	// PickupKind), so nothing bounds the digits any more and the widest honest
	// `"c"` is `{"beer":999999}` — 21 bytes, 420 B/s per viewer, on a wire that
	// had 32 B/s left.
	s := Snapshot{
		T: TypeSnapshot, Tick: 999999, Ack: 999999,
		X: 123456, Y: -123456, Z: 12345, Yaw: 3142, Sector: 12, Health: 100,
		Left:     math.MaxUint32,
		Loaded:   Barrels,
		Cooldown: ms(FireCooldownSeconds),
		Reload:   ms(ReloadSeconds),
		Down:     int(DownTime / time.Millisecond),
		Protect:  ms(SpawnProtectSeconds),
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// If a field is added that pushes past this, the right response is to measure
	// and move the budget deliberately — with the arithmetic written down, as
	// above — and never to raise the number until the test passes.
	const budget = 165
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
// EVERYTHING SUSTAINED IS COUNTED IN THE ARITHMETIC BELOW, and the нейрослопы are
// why it now has to be. They took the headroom from 528 B/s to 71, at which point
// "far under the ceiling" stopped being something anybody can claim by eye:
// Snapshot.Events was stated here and left out at ~28 B/s on the grounds of being
// two orders of magnitude under the headroom, and 28 against 71 is not two orders
// under anything — it is 39% of what was left. So it is a term of the sum now
// (worstEventCost), priced at its own rate rather than at the frame rate.
//
// THE HEADROOM IS 317 B/s, AND IT WAS 32 UNTIL THE BAG CAME OFF THE SNAPSHOT.
// That is the first time any of these three terms has gone DOWN, and it is worth
// recording which way the trade went: 15 bytes of `c` × SnapshotHz = 300 B/s
// came off every viewer's frame, and 5 bytes a row went ON to the standings —
// once a second, because removing the counter's ceiling took its digits from one
// to six (worstStandings). 300 saved against 15 spent, for a number that lags a
// pickup by up to StandingsInterval and buys nothing when it arrives.
//
// ONE THING IS STILL OUTSIDE THE SUM, and it is bounded rather than negligible:
// STANDINGS FRAMES OUT OF TURN. One goes to everybody on the tick the roster
// changes, and one goes to a single connection the first time it is served and
// again on the tick after each accepted hello (service.go, the ledger). The
// roster moves only when somebody genuinely joins or is taken out past
// AbandonGrace — a handful of frames a minute at MaxOccupants, against twenty
// snapshots a second.
//
// The hello is the large one, and the honest statement of it is not that there is
// room for it. The socket allows ten inbound frames a second
// (internal/realtime/conn.go) and a tick sends a connection at most one board, so
// a client sending nothing but hellos pulls ten boards a second — about 2.9 kB/s,
// ninety times the headroom below, and nothing in this budget absorbs it. What
// bounds it is that it is SELF-INFLICTED AND SELF-DIRECTED: those frames go to
// that client's own socket and to nobody else's, the inbound rate limit is what
// caps them, and an honest client pays exactly one extra board per attach. A phone
// that floods its own connection gets the connection it asked for; every other
// phone in the building is untouched, and it is the per-viewer cost of an ordinary
// viewer that MaxOccupants is derived from.
//
// If a future event fires per tick rather than per action, it belongs in
// worstSnapshot rather than in a term of its own.
const wireCeiling = 8000

// worstSnapshot is the widest frame the wire can carry for a building of n
// people and f нейрослопы — deliberately larger than a generated level produces,
// because the capacity is budgeted on the worst case and a phone on bad mobile
// data is precisely when the worst case is the case.
//
// Yaw is at the far end of the wrapped range (five characters where an ordinary
// one is four), positions are ±1234 m where a заброшка is tens of metres across,
// and the pickup mask is every bit set.
//
// The gun is at its widest too, which means BOTH of its timers running — a state
// the simulation cannot actually reach (sim.go, Player). That is deliberate here
// and nowhere else: the capacity of the building is derived from this number, so
// it is the one measurement that must never be optimistic. It costs 8 bytes ×
// 20 Hz = 160 B/s of headroom against a state that cannot occur, and the honest
// figure is in the test below.
//
// EVERY PEER CARRIES A STATE, and that is the field that took a place out of the
// building. `st` is priced at SnapshotHz like the position beside it, because
// three of its five values are DURATIONS: a man is down for DownTime, protected
// for SpawnProtectSeconds and rooted by an ampoule for SyringeSeconds, and all
// three are true on every tick they last. There is no honest duty cycle to
// discount by either — he cannot be hurt while down or protected, so a player
// killed the instant his protection expires is flagged essentially all the time,
// and a capacity derived from anything softer would fail the first time somebody
// was being spawn-camped. The ampoule made it three and changed no arithmetic
// here: the field was already charged at the full rate for the other two.
//
// EVERY СЛОП IS COUNTED, ALL OF THEM, and that is the same rule read for the
// second kind of entity. They all walk at the nearest man, so the room he is
// standing in is where they end up — the population IS the number visible at
// once, and a budget taken on a filtered set would be a budget that fails at the
// exact moment the game is worth playing.
func worstSnapshot(n, f int) Snapshot {
	s := Snapshot{
		T: TypeSnapshot, Tick: 999999, Ack: 999999,
		X: 123456, Y: -123456, Z: 12345, Yaw: -6283, Sector: 12, Health: 100,
		Left:     math.MaxUint32,
		Loaded:   Barrels,
		Cooldown: ms(FireCooldownSeconds),
		Reload:   ms(ReloadSeconds),
		Down:     int(DownTime / time.Millisecond),
		Protect:  ms(SpawnProtectSeconds),
	}
	// Everybody else, and NOT a peer fewer because some of them were filtered
	// out: interest management makes the typical frame smaller and does nothing
	// at all for the worst case, which is everybody standing in one room.
	for i := 0; i < n-1; i++ {
		s.Peers = append(s.Peers, Peer{Slot: 9, X: 123456, Y: -123456, Sector: 12, Yaw: -6283, St: PeerProtected})
	}
	for i := 0; i < f; i++ {
		s.Slops = append(s.Slops, Foe{ID: 9, X: 123456, Y: -123456, Sector: 12})
	}
	return s
}

// worstEventCost is what Snapshot.Events costs one viewer per second, sustained,
// and it is a term of the ceiling rather than a note beside it.
//
// PRICED AT ITS OWN RATE AND NOT AT SnapshotHz, which is the whole reason it is
// not simply a field on worstSnapshot: an event is delivered ONCE and cleared on
// the next frame, so multiplying it by twenty would be a twenty-fold pessimism —
// and this budget is what decides how many people fit in the building, so a lie in
// either direction costs somebody a place.
//
// THE RATE IS THE DENSEST HEAP A LEVEL CAN HOLD. A thing comes back only after
// PickupRespawn, so the most a player can be handed is every pickup in the
// building once per respawn — MaxWirePickups of them, that being the width of the
// mask the client reads them from (message.go, Snapshot.Left). The instantaneous
// burst, several things collected on one tick, is bounded by the same number and
// is not what a ceiling measured in bytes per second is about.
func worstEventCost(t *testing.T) int {
	t.Helper()
	// The widest event this game can emit: the LONGEST KEY THE CATALOGUE HOLDS,
	// and the highest index the mask can carry.
	//
	// Taken from the catalogue rather than typed here, which stopped being a
	// refinement the day a second kind arrived: this used to be the one key the
	// gun named, true only while "beer" was the only key there was, and a
	// catalogue entry with a longer one would have made the ceiling arithmetic
	// quietly optimistic — the one direction it may never be, because it is what
	// decides how many people fit in the building.
	widest := ""
	for _, k := range Pickups {
		if len(k.Key) > len(widest) {
			widest = k.Key
		}
	}
	raw, err := json.Marshal([]Event{{E: EventPickup, K: widest, ID: MaxWirePickups - 1}})
	if err != nil {
		t.Fatal(err)
	}
	// The array does not travel alone — it costs its own key and the comma joining
	// it to the frame as well.
	return (len(raw) + len(`,"ev":`)) * MaxWirePickups / int(PickupRespawn/time.Second)
}

// worstStandings is the widest standings frame for a building of n people: a
// twelve-character pseudonym each, a stay of eleven days, and four six-figure
// numbers — a career of bottles, a career of deaths, a career of слопы and a
// career of friends shot.
//
// THE BAG IS SIX FIGURES HERE AND USED TO BE ONE, which is what removing the
// ceiling cost this frame and the only place it cost anything. Beer capped at
// nine while a reload drank a bottle; nothing spends a counter now, so the
// number only ever grows — and over the eleven-day stay the rest of this row is
// measured at, a building whose pickups come back every PickupRespawn hands out
// six figures of them. Four bytes a row, once a second, is the whole price of
// the counter never stopping.
func worstStandings(n int) Standings {
	b := Standings{T: TypeStandings}
	for i := 0; i < n; i++ {
		b.Rows = append(b.Rows, StandingsRow{
			Slot: 9, Name: "K3jf9sLm2QpZ", Seconds: 999999, Bag: map[string]int{"beer": 999999},
			Deaths: 999999, Kills: 999999, Betrayals: 999999,
		})
	}
	return b
}

func TestEverythingAFullBuildingSendsAViewerFitsTheCeiling(t *testing.T) {
	// THE SOLO FRAME IS THE FLOOR, NOT THE COST, and MaxOccupants and
	// SlopPopulation are what this test exists to derive. A viewer pays three
	// things: a snapshot built for them SnapshotHz times a second carrying
	// everybody else AND every нейрослоп they can see, a standings frame once a
	// second carrying everybody including them, and the events he is handed as he
	// collects things. All three are counted, because leaving one out would be
	// moving the ceiling rather than meeting it — which is how the capacity came to
	// be six before anybody measured a peer.
	//
	// Measured at the widest quantisation the wire can carry: 165 bytes of self,
	// +63 for the first peer and +57 for each one after, +44 for the first слоп
	// and +38 for each one after; a standings frame is 120 bytes with one row and
	// +92 a row after that; and events sustain 39 B/s at the densest heap a level
	// can hold (worstEventCost). So three people and two слопы is 367 × 20 + 304 +
	// 39 = 7683 B/s, where a third слоп would be 8443 and a fourth person 8915 —
	// both over, and both by more than the whole of the headroom. That is the
	// derivation of the two constants, and this is what turns it into a gate:
	// raising either, or growing any of the three frames, fails here rather than
	// on somebody's mobile data.
	//
	// THE SNAPSHOT SHRANK BY 15 AND THE STANDINGS GREW BY 5 A ROW, which is the
	// whole of what taking the bag off the frame did to this sum: 7968 B/s became
	// 7683, and the headroom 32 became 317. Neither constant moves on it. A place
	// bought back would need 1232 B/s for the fourth man and 760 for the third
	// слоп — the saving is a fifth of the cheaper of the two, and the answer to
	// wanting either is still the binary codec.
	//
	// THE НЕЙРОСЛОПЫ COST THE FOURTH PLACE, and not by a margin worth arguing
	// about: four people and ONE слоп is 8155 B/s, so the building was over the
	// ceiling before the antagonist arrived. A слоп is already the cheapest entity
	// this wire can carry — 37 bytes against a peer's 56, with no yaw and no state
	// field, because it has nothing to say with either (message.go, Foe). The
	// arithmetic above is what says three people rather than four; the answer to
	// wanting the fourth back is the binary codec.
	//
	// 19 of that 165 assumes a gun reloading AND on its firing cadence while its
	// owner is simultaneously freshly protected and either dead or being injected,
	// which is four timers at once and no reachable state carries more than two of
	// them. The pessimism is on purpose (see worstSnapshot) and it is worth about
	// 200 B/s — which is to say most of the headroom this leaves is pessimism
	// rather than room.
	//
	// THE ШПРИЦ IS IN THIS ARITHMETIC AND ADDED NOTHING TO IT, which is not an
	// omission. Everything it needs on the wire it borrowed: the injection's own
	// countdown rides `dn` against `hp` (message.go, Snapshot.Down) and what the
	// room sees is a FIFTH VALUE on a state field every peer was already carrying
	// (Peer.St). A field of either kind would have been 120 B/s at the JSON floor,
	// and the headroom when that was decided was 32 B/s — so the alternatives were
	// a smaller building or the binary codec, and the reuse is what made a third
	// occupant survive the iteration. It would fit inside today's 317, which is
	// worth saying plainly rather than leaving for somebody to rediscover: the
	// bag coming off this frame is what bought that room, and spending it on a
	// field the шприц demonstrably does not need would be spending it twice.
	//
	// WHAT IS STILL NOT COUNTED IS ON wireCeiling: the out-of-turn standings
	// frames, bounded by an inbound rate limit and delivered to the socket that
	// asked for them. Nor is any of the three arrays taken filtered — interest
	// management and the hold that keeps something on the frame a moment after it
	// leaves the visible set (visibleHold) both act on a set that is already whole
	// here, because the worst case is everybody and everything in one room.
	snap, err := json.Marshal(worstSnapshot(MaxOccupants, SlopPopulation))
	if err != nil {
		t.Fatal(err)
	}
	board, err := json.Marshal(worstStandings(MaxOccupants))
	if err != nil {
		t.Fatal(err)
	}
	events := worstEventCost(t)
	perSecond := len(snap)*SimHz + len(board)*int(time.Second/StandingsInterval) + events
	t.Logf("MaxOccupants=%d SlopPopulation=%d: snapshot %d B × %d Hz + standings %d B + events %d B/s = %d B/s, ceiling %d (headroom %d)",
		MaxOccupants, SlopPopulation, len(snap), SimHz, len(board), events, perSecond, wireCeiling, wireCeiling-perSecond)
	if perSecond > wireCeiling {
		t.Fatalf("a full building costs a viewer %d B/s, the ceiling is %d — %d-byte snapshot × %d Hz plus a %d-byte standings plus %d B/s of events\nsnapshot: %s\nstandings: %s",
			perSecond, wireCeiling, len(snap), SimHz, len(board), events, snap, board)
	}

	// And BOTH of the steps these constants are one below really are over the
	// line, so the test cannot pass by the arithmetic having drifted somewhere
	// generous — and so that a future change cannot quietly buy a place back by
	// shrinking a frame without saying so.
	for _, over := range []struct {
		what        string
		people, slo int
	}{
		{"people", MaxOccupants + 1, SlopPopulation},
		{"слопы", MaxOccupants, SlopPopulation + 1},
	} {
		nextSnap, err := json.Marshal(worstSnapshot(over.people, over.slo))
		if err != nil {
			t.Fatal(err)
		}
		nextBoard, err := json.Marshal(worstStandings(over.people))
		if err != nil {
			t.Fatal(err)
		}
		// The same three terms as above: an extra place in the building would be
		// paid for out of the same viewer's second.
		cost := len(nextSnap)*SimHz + len(nextBoard)*int(time.Second/StandingsInterval) + events
		t.Logf("one more of the %s (%d people, %d слопы) would be %d B/s", over.what, over.people, over.slo, cost)
		if cost <= wireCeiling {
			t.Fatalf("%d people and %d слопы would cost %d B/s, inside the %d ceiling — the %s constant is one too low",
				over.people, over.slo, cost, wireCeiling, over.what)
		}
	}
}

func TestTheAmpouleRidesTheDownTimerWithoutWideningIt(t *testing.T) {
	// The шприц reached the wire by REUSING `dn` rather than by adding a field
	// (message.go, Snapshot.Down), and a reuse is only free while the borrowed
	// field is wide enough to hold what was put in it. The budget above measures
	// `dn` at DownTime; an injection longer than that would add a digit to a frame
	// that goes out twenty times a second, which is 20 B/s against 317 B/s of
	// headroom — and it would do so without any test noticing, because nothing
	// else in this file knows the field has two meanings.
	//
	// The relationship is stated as digits rather than as milliseconds because
	// digits are what JSON actually charges for.
	down := int(DownTime / time.Millisecond)
	inject := ms(SyringeSeconds)
	if inject > down {
		t.Fatalf("an injection is %d ms and the down window is %d ms, so `dn` is now wider than the wire budget measured it: "+
			"either shorten SyringeSeconds, or re-measure worstSnapshot deliberately", inject, down)
	}

	// And the two really are exclusive, which is what makes one number honest for
	// both. Proved on the wire's own terms: a frame carrying a positive `dn` says
	// which meaning it has through `hp`, so there is no state in which the reader
	// has to guess.
	//
	// The simulation's half of that exclusion — a dead man collects nothing, and
	// being hurt clears the ampoule — is pinned in world_test.go, where the world
	// that enforces it lives.
	for _, c := range []struct {
		what   string
		health int
		down   int
	}{
		{"a man on the floor", 0, down},
		{"a man with a needle in his arm", MaxHealth - SyringeHeal, inject},
	} {
		raw, err := json.Marshal(Snapshot{T: TypeSnapshot, Health: c.health, Down: c.down})
		if err != nil {
			t.Fatal(err)
		}
		var back Snapshot
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if back.Down != c.down || back.Health != c.health {
			t.Fatalf("%s: %s does not round-trip to hp=%d dn=%d", c.what, raw, c.health, c.down)
		}
		if (back.Health > 0) != (c.health > 0) {
			t.Fatalf("%s: the frame %s no longer says which of the two `dn` means", c.what, raw)
		}
	}
}

func TestNoTwoFieldsOnAFrameShareAWireKey(t *testing.T) {
	// A KEY COLLISION IS SILENT, AND IT DELETES BOTH FIELDS. encoding/json
	// resolves two fields at the same level with the same tag by emitting NEITHER
	// — no error, no panic, no log line. The frame simply comes out short.
	//
	// It is not a hypothetical. The слоп array shipped as `"z"` for exactly as
	// long as it took to measure it, and `z` was already the eye height: the
	// snapshot lost both, so every player would have been drawn standing on the
	// floor of sector zero and no слоп would ever have appeared. Nothing in the
	// type system says a word about it, and the only reason it was caught is that
	// the byte count refused to move.
	//
	// Keys are one and two characters on these frames deliberately (see the file
	// header), which is exactly the regime in which a collision is easy — so this
	// is asserted over the struct tags of every type that reaches the wire rather
	// than left to whoever adds the next field noticing.
	for _, frame := range []any{Snapshot{}, Peer{}, Foe{}, Standings{}, StandingsRow{}, Ready{}, Full{}, Event{}, InputFrame{}, wireCommand{}} {
		typ := reflect.TypeOf(frame)
		seen := make(map[string]string, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if key == "" || key == "-" {
				continue
			}
			if first, dup := seen[key]; dup {
				t.Fatalf("%s.%s and %s.%s both serialise as %q — encoding/json emits neither",
					typ.Name(), first, typ.Name(), f.Name, key)
			}
			seen[key] = f.Name
		}
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
	raw, err := json.Marshal(worstSnapshot(MaxOccupants, SlopPopulation))
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
	// Five numbers on a RESTING peer and nothing else — where, which room, which
	// way round, and which place in the building. A sixth field that rides every
	// tick is not forbidden (`st` is one, and it is argued for on Peer.St and
	// priced in the ceiling test) but it costs SnapshotHz × N × V bytes a second,
	// so it fails here first and gets argued for in the commit.
	//
	// READ OFF A RESTING FRAME AND NOT OFF worstSnapshot, because that fixture is
	// now deliberately the worst case: every peer in it is flagged, since the
	// state field's three expensive values are durations that really can be true
	// on every tick. What is being pinned here is the OTHER end — that a man who
	// is alive, unprotected, carrying no needle and doing nothing costs five
	// numbers.
	var resting struct {
		Peers []map[string]any `json:"p"`
	}
	calm := worstSnapshot(MaxOccupants, SlopPopulation)
	for i := range calm.Peers {
		calm.Peers[i].St = 0
	}
	quiet, err := json.Marshal(calm)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(quiet, &resting); err != nil {
		t.Fatal(err)
	}
	for _, p := range resting.Peers {
		if len(p) != 5 {
			t.Fatalf("a peer entry carries %d fields, not the five a resting one is budgeted for: %v", len(p), p)
		}
		for _, key := range []string{"n", "x", "y", "s", "yaw"} {
			if _, ok := p[key]; !ok {
				t.Fatalf("a peer entry has no %q: %v", key, p)
			}
		}
	}

	// And the state really is on the entry when it is set, so the assertion above
	// is proving that it was omitted rather than that it does not exist. One
	// digit, whichever of the five it is: a value that needed two would be a
	// different field's arithmetic, which is what makes the fifth one worth
	// listing here rather than trusting to the four that came before it.
	for _, st := range []int{PeerFired, PeerHit, PeerDown, PeerProtected, PeerInjecting} {
		marked, err := json.Marshal(Peer{Slot: 9, X: 123456, Y: -123456, Sector: 12, Yaw: -6283, St: st})
		if err != nil {
			t.Fatal(err)
		}
		if want := `"st":` + strconv.Itoa(st); !strings.Contains(string(marked), want) {
			t.Fatalf("a peer in state %d says nothing about it: %s", st, marked)
		}
		if st > 9 {
			t.Fatalf("state %d is two digits; the wire budget is priced on one", st)
		}
	}
}

func TestTheBagRidesTheStandingsAloneAndNeverEmpty(t *testing.T) {
	// TWO CLAIMS, AND THE FIRST ONE IS THE EXPENSIVE ONE. `c` was on the snapshot
	// for the reader's own bag until the predictor stopped reading it, at 15 bytes
	// × SnapshotHz × every viewer to restate a tally that moves a few times a
	// minute (message.go, Snapshot.Events). Nothing serialises it there now, and
	// this is what says so — a field re-added by reflex would cost 300 B/s of a
	// budget with 317 in it and no test below would notice, because the ceiling
	// arithmetic measures whatever the struct happens to hold.
	snap, err := json.Marshal(Snapshot{
		T: TypeSnapshot, Tick: 7, Loaded: Barrels, Left: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(snap), `"c"`) {
		t.Fatalf("the snapshot carries a bag again: %s", snap)
	}

	// And the second: prefer omitting to sending empty, at 1 Hz as much as at 20.
	// Everybody's first minute in the building is spent carrying nothing, and
	// `"c":{}` a second per occupant per viewer is bytes spent to say so.
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
