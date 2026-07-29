package gamekaren

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
	TypeHello = "karen_hello"
	// TypeInput is the batch of sub-steps described on Command.
	TypeInput = "karen_input"

	// TypeReady confirms which shift this socket is now attached to.
	TypeReady = "karen_ready"
	// TypeSnapshot is the idempotent full-state frame.
	TypeSnapshot = "karen_snap"
	// TypeOver ends a shift.
	TypeOver = "karen_over"
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

// inputFrame is one client→server input message.
//
// The `k` field §5 documents — the last snapshot tick the client had drawn — is
// accepted and IGNORED in iteration 1. It is how a round trip would be derived
// rather than reported, and the thing that would consume it is lag compensation,
// which this game does not have: nothing here is rewound, because nothing here
// is shot at. Decoding it into a field nothing reads would be a parameter set by
// nobody, so it is simply not decoded; encoding/json ignores it, the client's
// frames are unchanged, and the day a rewind buffer arrives this is where it
// starts.
type inputFrame struct {
	Cmds []Command `json:"cmds"`
}

// ParseInbound decodes a frame, returning its type and — for an input frame —
// the commands it carried.
//
// A malformed, unknown or unparseable frame produces ("", nil): no reply, no
// error and no log line. Silence is the platform's policy for a bad frame,
// because a log line per bad frame at the permitted ten a second is a flood
// lever handed to any client.
//
// The commands come back UNSANITISED. Office.Enqueue is the single place that
// clamps them, so there is exactly one such place and Step can stay pure.
func ParseInbound(payload []byte) (string, []Command) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", nil
	}
	switch env.T {
	case TypeHello:
		return TypeHello, nil
	case TypeInput:
		var f inputFrame
		if err := json.Unmarshal(payload, &f); err != nil {
			return "", nil
		}
		if len(f.Cmds) > MaxInboundCommands {
			f.Cmds = f.Cmds[:MaxInboundCommands]
		}
		return TypeInput, f.Cmds
	default:
		return "", nil
	}
}

// Ready tells a freshly-attached socket which shift it is now watching.
type Ready struct {
	T       string `json:"t"`
	ShiftID string `json:"shift_id"`
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
	// The bald man.
	B BossFrame `json:"b"`
}

// BossFrame is him, quantised: centimetres and a grin in 0..255.
type BossFrame struct {
	X int `json:"x"`
	Y int `json:"y"`
	G int `json:"g"`
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
// it into its own simulated player (`reconcile` in web/src/lib/karenPredict.ts),
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
