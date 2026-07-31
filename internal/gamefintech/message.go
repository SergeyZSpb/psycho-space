package gamefintech

import (
	"encoding/json"
	"math"
)

// The wire contract, in both directions.
//
// Everything is a JSON text frame with a string `t` discriminator, and both ends
// ignore an unknown `t` — which is what lets either side learn a message type
// without a coordinated deploy. That is the platform's rule, not this game's.
//
// THE CLIENT SENDS INTENT AND NEVER A FACT. There is no position, no salary, no
// streak and no account field anywhere inbound: the account is bound at the
// upgrade and travels as a realtime.Member, so a payload cannot claim to be
// somebody else, and everything else is a request the simulation judges.
//
// BYTES ARE A DESIGN CONSTRAINT (CLAUDE.md). A snapshot goes out ten times a
// second, forever, to a phone on mobile data, and once per VIEWER — so its size
// is multiplied by the tick rate and by the number of people in the office. Every
// field on it is therefore either quantised to an integer or absent:
//
//   - Positions are CENTIMETRES as int, never float64. A float64 metre value
//     serialises to seventeen characters of noise nobody can see at a
//     centimetre; the int is three or four.
//   - Money is WHOLE RUBLES. Nobody has ever wanted to see a kopeck.
//   - The multiplier is HUNDREDTHS and the grin is a BYTE — both are read by the
//     eye off a bar and a face, so more precision is more bytes for nothing.
//   - Durations are MILLISECONDS as int.
//   - Keys are one to three characters. This is the one payload in the game that
//     repeats, which is the only place that unreadability is worth paying for.
//   - Anything empty is omitted rather than sent as a zero.
//
// The OFFICE is deliberately not here and is never on a frame: it is a constant
// the client already fetched with the catalogue (see content.go), so a snapshot
// carries only what moves.
const (
	// TypeHello attaches a connection to whatever shift the account already
	// started over HTTP. It carries no fields at all — identity is the
	// connection, so there is nothing to forge and nothing to validate.
	//
	// The client sends it from the socket's status callback on EVERY open, not
	// once at mount: a hello written before the socket is open is dropped rather
	// than queued, and a reconnect that never re-announces gets no snapshots.
	TypeHello = "fintech_hello"
	// TypeInput is the batch of sub-steps described on Command.
	TypeInput = "fintech_input"
	// TypeDo is a verb: something that happens once rather than continuously.
	//
	// SEPARATE FROM INPUT because it is a different shape of thing. Input is a
	// stream of sub-steps with sequences and redundancy, judged by the
	// simulation; a verb is one event with a target, judged by the office. It
	// gets no reply — the caller learns the outcome from the next snapshot, the
	// same way everybody else does, which is what stops a client believing a
	// verb the server refused.
	TypeDo = "fintech_do"

	// TypeReady confirms which shift this socket is now attached to.
	TypeReady = "fintech_ready"
	// TypeSnapshot is the idempotent full-state frame.
	TypeSnapshot = "fintech_snap"
	// TypeOver ends a shift.
	TypeOver = "fintech_over"
)

// MaxInboundCommands is how many sub-steps one frame may carry: the sampling
// ratio plus the redundancy window. A frame with more keeps this many AND IS
// STILL ACCEPTED — refusing a frame for being generous is how a lossy client
// gets stuck, and the surplus buys nothing anyway because the office drops every
// command whose sequence it has already applied.
const MaxInboundCommands = MaxCommandsPerFrame + RedundantCommands

// envelope reads only the discriminator, so an unknown message costs one small
// allocation and is then dropped in silence.
type envelope struct {
	T string `json:"t"`
}

// Input is one client→server input message, decoded.
//
// `Seen` is the last snapshot tick the client had received when it built this
// frame. IT IS HOW A ROUND TRIP IS DERIVED RATHER THAN REPORTED: the tick rate
// is fixed, so the gap between that tick and the office's current one is the
// whole loop — downlink, the client's own send window, uplink — and a client
// cannot claim a latency it does not have without also claiming to be drawing a
// frame it has not got.
//
// It was accepted and discarded until the catch was lag-compensated. The reason
// given for discarding it was that nothing here is shot at, and that was wrong:
// BEING CAUGHT IS A HIT TEST, resolved against a man the victim only ever sees
// in the past.
type Input struct {
	Seen uint64    `json:"k"`
	Cmds []Command `json:"cmds"`
}

// doFrame is one client→server verb.
//
// `tg` is a PSEUDONYM, which is the only name this client has for anybody else —
// it never learns an account id (ADR-037), so it cannot name one here even by
// accident. The office resolves it, and an unknown handle is simply refused.
type doFrame struct {
	V  string `json:"v"`
	Tg string `json:"tg"`
}

// Verb names, as they arrive on a doFrame.
const (
	VerbRedirect = "redirect"
	// VerbRouter is «РОУТЕР УПАЛ» — it takes Claude Code off the floor. It carries
	// no `tg`: there is nothing to aim, because there is exactly one Claude and
	// the effect is the whole office's.
	VerbRouter = "router"
)

// ParseVerb decodes a verb frame into its name and target. It is separate from
// ParseInbound because a verb carries strings rather than commands, and giving
// ParseInbound a third return value that is nil for every other message type
// would be a parameter set by nobody on all but one path.
func ParseVerb(payload []byte) (verb, target string) {
	var f doFrame
	if err := json.Unmarshal(payload, &f); err != nil {
		return "", ""
	}
	// Bounded before anything looks at it: a target is a twelve-character
	// handle, and a megabyte of `tg` must not become a map key or a log line.
	if len(f.Tg) > 64 || len(f.V) > 32 {
		return "", ""
	}
	return f.V, f.Tg
}

// ParseInbound decodes a frame, returning its type and — for an input frame —
// what it carried.
//
// A malformed, unknown or unparseable frame produces ("", zero): no reply, no
// error and no log line. Silence is the platform's policy for a bad frame,
// because a log line per bad frame at the permitted ten a second is a flood
// lever handed to any client.
//
// It returns a STRUCT rather than a second and third value, unlike ParseVerb,
// and the distinction is worth stating because the two look alike. A verb's
// target is a different KIND of thing from a command and is nil on every other
// path, so it stays out; the seen tick and the commands are two halves of one
// input frame and travel together.
//
// The commands come back UNSANITISED. Office.Enqueue is the single place that
// clamps them, so there is exactly one such place and Step can stay pure.
func ParseInbound(payload []byte) (string, Input) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", Input{}
	}
	switch env.T {
	case TypeHello:
		return TypeHello, Input{}
	case TypeDo:
		// The verb and its target are read by ParseVerb, which the handler calls
		// on the same payload — this only has to say what kind of frame it is.
		return TypeDo, Input{}
	case TypeInput:
		var f Input
		if err := json.Unmarshal(payload, &f); err != nil {
			return "", Input{}
		}
		if len(f.Cmds) > MaxInboundCommands {
			f.Cmds = f.Cmds[:MaxInboundCommands]
		}
		return TypeInput, f
	default:
		return "", Input{}
	}
}

// Ready tells a freshly-attached socket which shift it is now watching.
type Ready struct {
	T       string `json:"t"`
	ShiftID string `json:"shift_id"`
	// Persona is which employee this shift is, as an index into the catalogue's
	// `personas`. It is here as well as on the two shift responses because a socket
	// can open against a shift this client did not start — a second device, or a
	// reconnect after the tab was asleep — and the over screen has to be able to
	// name who was working. Sent ONCE per attach, never on a frame.
	Persona int `json:"persona"`
	// K0 is the office tick this shift started on, so the client can draw how long
	// it has lasted as `k − k0`.
	//
	// HERE RATHER THAN ON THE SNAPSHOT, and that is the whole reason this game's
	// second scored dimension costs nothing on the wire. The shift's age changes
	// every tick, but the number it is derived FROM does not change at all — and a
	// snapshot already carries `k`. Sending the elapsed time instead would be ~9
	// bytes × 20 Hz × up to three viewers, for ever, to say something the frame and
	// this one attach-time integer already imply.
	//
	// Not `omitempty`: zero is the honest answer for the first person into a fresh
	// office, and an absent field would be indistinguishable from "we did not say".
	K0 uint64 `json:"k0"`
}

// Snapshot is the idempotent full-state frame, sent at SimHz/SnapshotEvery.
//
// Self is flattened into the top level rather than nested: there is exactly one
// "you" on a frame, and a nested object is punctuation ten times a second for
// nothing. The bald man is nested because he is one thing with three numbers,
// and because peers will arrive beside him as an array.
type Snapshot struct {
	T string `json:"t"`
	// Tick is the simulation step this frame describes. With a fixed rate it is
	// a TIMELINE — two snapshots and their tick numbers are all a client needs
	// to place the boss between them, which is what interpolation runs on.
	Tick uint64 `json:"k"`
	// Ack is the last COMMAND sequence this player had folded in. The client
	// drops everything at or below it from its pending list and replays the rest
	// on top of the authoritative position below. It is the whole of
	// reconciliation.
	Ack uint32 `json:"ack"`
	// Centimetres.
	X int `json:"x"`
	Y int `json:"y"`
	// Whole rubles.
	Pay int `json:"pay"`
	// The multiplier, ×100. Sent rather than derived from the streak so the HUD
	// and the payout can never disagree, and so a retune of the ramp needs no
	// client deploy.
	M int `json:"m"`
	// Streak, milliseconds. Always present: a bar reading zero is information.
	St int `json:"st"`
	// Dash cooldown remaining, milliseconds, ROUNDED UP — see msUp. OMITTED WHEN
	// READY, which is the common case — the button spends most of the shift
	// available.
	Dc int `json:"dc,omitempty"`
	// P is which line is over your head: an INDEX into the catalogue's
	// PlayerLines, never the words themselves.
	//
	// This is ADR-037 applied to speech. A Cyrillic sentence is two bytes a
	// character, this frame repeats ten times a second per viewer for the whole
	// of a shift, and the client already fetched every line once with the
	// catalogue — so what crosses the wire is one digit. OMITTED AT ZERO, which
	// is «Я КАРЕН» and is what somebody playing this game properly says almost
	// all of the time, so the common case costs nothing at all.
	//
	// Absent therefore means INDEX 0, never "unchanged" — the same rule `dc`
	// documents above, and for the same reason: read as "unchanged" a client
	// would stick on the last interesting thing it was told for ever.
	P int `json:"p,omitempty"`
	// Rc is the redirect verb's cooldown, milliseconds, rounded up and omitted
	// when it is ready — the same rule and the same reason as `dc`.
	Rc int `json:"rc,omitempty"`
	// Bs is WHICH of the catalogue's BottleSpots have a bottle standing on them —
	// a BIT PER SPOT, never a coordinate and no longer a single index.
	//
	// The office keeps one bottle per person on the floor, so «which spot» stopped
	// being a question with one answer. A mask is the encoding that did not grow
	// with it: eight bytes whatever it describes, where an array of indexes grows
	// with the office and an array of positions would be twenty bytes a prop,
	// twenty times a second, per viewer, to say something that changes once every
	// ten seconds (ADR-037's rule, applied to a prop).
	//
	// Omitted at zero like every other index here, and zero means «not one of them
	// is standing» — a state a busy office really reaches, and never «unchanged».
	//
	// THERE IS NO LONGER A `bt`. It said how long until the bottle came back, and
	// with several of them it could only have meant «the soonest», which nothing
	// draws: the mask already says what is on the floor, and what is not on the
	// floor is not drawn.
	Bs int `json:"bs,omitempty"`
	// Np is Серега and Тёма, in catalogue order. Never omitted — they are always on
	// the floor — and their positions come from the server because they amble towards
	// the кальян's current spot, which only the server knows (see npc.go).
	Np []NPCFrame `json:"np"`
	// Cl is Claude Code — where he is, and how lit his cigarette is.
	//
	// A POINTER, AND OMITTED WHILE HE IS AWAY. It used to be the one field on this
	// frame that was never omitted, on the reasoning that he is always on the floor
	// and an absent field would mean he was not. «РОУТЕР УПАЛ» made that exactly
	// what an absent field has to mean: he walks off to see about the router, and
	// sending a stale position for twelve seconds would be forty bytes a frame per
	// viewer to draw a man who is not there.
	Cl *ClaudeFrame `json:"cl,omitempty"`
	// Ca is how long until Claude is back, in milliseconds, and it is the field
	// that says he is gone at all — `cl` being absent is the consequence, not the
	// announcement. Omitted while he is on the floor, which is most of a shift.
	Ca int `json:"ca,omitempty"`
	// Rd is the router verb's cooldown, milliseconds, rounded up and omitted when
	// it is ready — the same rule as `rc` and `dc`.
	//
	// IT IS THE OFFICE'S AND NOT YOURS, so every occupant is told the same number:
	// the router can only fall once every RouterCooldown however many people are in
	// the room. That is a fact everybody's button has to know, or two players would
	// both offer a press and one of them would be silently refused.
	Rd int `json:"rd,omitempty"`
	// Sl is how long Claude Code's slow has left on YOU, in milliseconds.
	//
	// THE CLIENT PREDICTS FROM THIS ONE, unlike `iv`: it multiplies the walk, so a
	// client that did not fold it in would predict 6.4 m/s against the office's
	// 5.12 — 1.28 m/s of divergence, which clears the snap threshold in about 1.6 s
	// of walking. Rounded UP for the dash cooldown's reason: zero on the wire has
	// to mean exactly zero on the server, or the client would predict a full-speed
	// walk the office is about to refuse.
	Sl int `json:"sl,omitempty"`
	//
	// The client needs it for the buff row and for the cloud over your own figure,
	// and it does NOT predict from it: unlike the dash cooldown, nothing about
	// movement depends on it, so a client that is a frame behind on it draws a
	// cloud a frame late and never disagrees with the office about a position.
	// Rounded UP for the same reason the dash cooldown is — a readout that says 0
	// while the server still has milliseconds left is a readout that lies at the
	// exact moment it matters.
	Iv int `json:"iv,omitempty"`
	// Hs is which of the catalogue's hookah spots have one standing on them — the
	// bottle's mask exactly, and one кальян per person on the floor for the same
	// reason. `hk` went with `bt`, and for the same reason.
	Hs int `json:"hs,omitempty"`
	// The bald man.
	B BossFrame `json:"b"`
	// Pr is everybody else in the office — at most two, because MaxOccupants is
	// three and one of them is you.
	//
	// `pr` rather than `p`, which the balloon index above already spent. Absent
	// when you are alone, which is the common case and costs nothing: this game
	// is playable solo by design, and an empty array would be two bytes ten
	// times a second forever to say "there is nobody here".
	Pr []PeerFrame `json:"pr,omitempty"`
}

// NPCFrame is one of the two non-players, quantised exactly as everybody else is.
//
// No name and no key: the catalogue carries both and the frame's INDEX is which of
// them this is, so the wire says nothing that cannot change during a shift — the same
// rule the balloons and the peers follow (ADR-037).
type NPCFrame struct {
	X int `json:"x"`
	Y int `json:"y"`
	// P is which of HIS OWN lines is over his head. Omitted at zero, and zero is his
	// introduction.
	P int `json:"p,omitempty"`
}

// ClaudeFrame is Claude Code, quantised exactly as the лысый is.
//
// OMITTED ONLY WHILE HE IS AWAY, and an absent `cl` means exactly that — «he is
// not there» — which is now a state the game has. It costs the frame about thirty
// bytes on every snapshot he IS on the floor for, which is most of a shift;
// stated rather than hidden, and measured in the budget test.
type ClaudeFrame struct {
	X int `json:"x"`
	Y int `json:"y"`
	// C is how lit the cigarette is, 0..255 — the grin's arrangement exactly, so
	// the client reads how much trouble it is in without a meter.
	C int `json:"c"`
	// P is which line is over his head, an index into ClaudeLines. Omitted at
	// zero, and zero is «Я КЛОД».
	P int `json:"p,omitempty"`
}

// PeerFrame is another Карен, quantised exactly as you are.
//
// `I` IS A PSEUDONYM, NOT AN ACCOUNT ID, and there is no name and no picture on
// it (ADR-037). The handle is HMAC(process key, account) truncated, so it is
// stable for as long as the office is and meaningless after a restart; a face is
// fetched once by that handle over cached HTTP, because a ~250-character avatar
// URL riding a frame that repeats ten times a second per viewer is about a
// megabit a second at ten players and this frame is the wrong place for anything
// that never changes.
//
// What it deliberately does NOT carry: a display name, a salary, a multiplier or
// a streak. Another Карен's money is his own business, and none of it is
// drawable — the plane shows a figure and a balloon, so the frame carries a
// position and a balloon.
type PeerFrame struct {
	I string `json:"i"`
	// Centimetres, like everything else on this frame.
	X int `json:"x"`
	Y int `json:"y"`
	// Which line is over his head — an index into PlayerLines, omitted at zero,
	// and zero is «Я КАРЕН». The same rule as yours, so two people watching the
	// same office read the same words at the same instant.
	P int `json:"p,omitempty"`
	// Sl is how long Claude Code's slow has left on this colleague, milliseconds.
	//
	// A DEBUFF IS AS PUBLIC AS A BUFF. The office rule is that every screen shows
	// every player's state, and «he is slowed» is exactly the kind of thing that
	// changes what you do next — it is who the лысый will reach first. Omitted in
	// the resting state.
	Sl int `json:"sl,omitempty"`
	// Iv is how long this colleague is behind a cloud, in milliseconds.
	//
	// IT RIDES A PEER'S FRAME BECAUSE A BUFF IS NOT PRIVATE. The office rule is
	// that every screen shows every player's state — an effect only its owner can
	// see is unfinished — and this one changes who the лысый will walk at, which is
	// the single most useful thing to know about somebody else in the room.
	// Omitted in the resting state, which is almost always, so an office where
	// nobody has smoked costs nothing: four bytes of key and up to five of value,
	// on at most two peers, ten times a second, only while a cloud is running.
	Iv int `json:"iv,omitempty"`
}

// BossFrame is him, quantised: centimetres, a grin in 0..255, and which of his
// lines he is on. `P` follows the same rule as the player's — an index into the
// catalogue's BossLines, omitted at zero, and zero is «Я ЛЫСЫЙ».
type BossFrame struct {
	X int `json:"x"`
	Y int `json:"y"`
	G int `json:"g"`
	P int `json:"p,omitempty"`
	// D is how long he stays drunk, milliseconds, omitted when he is sober.
	//
	// A DURATION RATHER THAN A BOOLEAN, because the client draws a countdown as
	// well as a colour — and because "how much longer" is the only question
	// worth asking about a state you are trying to outlast. Whether he is drunk
	// is a fact about the OFFICE, so it rides everybody's frame: one Карен buys
	// the round and both of them watch him wobble.
	D int `json:"d,omitempty"`
}

// Over ends a shift. It is sent once, and the client stops sending input.
type Over struct {
	T     string `json:"t"`
	Cause string `json:"cause"`
	Pay   int    `json:"pay"`
	Secs  int    `json:"secs"`
}

// cm quantises metres to centimetres for the wire.
func cm(v float64) int { return int(math.Round(v * 100)) }

// ms quantises seconds to milliseconds for the wire.
func ms(v float64) int { return int(math.Round(v * 1000)) }

// msUp quantises a countdown to milliseconds, ROUNDING UP, so that zero on the
// wire means exactly zero here.
//
// `ms` rounds to nearest, which is right for a number that is only ever read —
// and the streak is only ever read. The dash cooldown is not: the client folds
// it into its own simulated player (`reconcile` in web/src/lib/fintechPredict.ts),
// because a still player emits no commands and so never runs the timer down
// locally. That makes this a value the client PREDICTS FROM, and a prediction
// that says "ready" half a millisecond before the server does is a dash the
// client grants and the server refuses — five and a half metres of divergence,
// which is nearly three times the snap threshold.
//
// It is reachable rather than theoretical: floating-point residue can leave the
// cooldown at 4e-16 for the one tick before the next step clamps it, and a
// snapshot published in that tick would round it away. Rounding up costs a
// millisecond on a readout nobody reads to the millisecond.
func msUp(v float64) int {
	if v <= 0 {
		return 0
	}
	return int(math.Ceil(v * 1000))
}

// rub quantises money to whole rubles, TRUNCATING rather than rounding: a
// counter that only ever goes up should never show a ruble that has not been
// earned yet.
func rub(v float64) int { return int(v) }

// hundredths quantises the multiplier for the wire.
func hundredths(v float64) int { return int(math.Round(v * 100)) }

// grinByte quantises the grin to 0..255.
func grinByte(v float64) int { return int(math.Round(clamp(v, 0, 1) * 255)) }
