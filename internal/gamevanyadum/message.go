package gamevanyadum

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
// THE CLIENT SENDS INTENT AND NEVER A FACT. There is no position, no health, no
// hit claim and no account field anywhere inbound: the account is bound at the
// upgrade and travels as a realtime.Member, so a payload cannot claim to be
// somebody else, and everything else is a request the simulation judges.
//
// BYTES ARE A DESIGN CONSTRAINT (CLAUDE.md). A snapshot goes out twenty times a
// second, forever, to a phone on mobile data, so every field on it is either
// quantised to an integer or absent:
//
//   - Positions are CENTIMETRES as int, never float64. A float64 metre value
//     serialises to seventeen characters of noise nobody can see at a
//     centimetre; the int is four.
//   - Angles are THOUSANDTHS OF A RADIAN, which is far finer than a phone can
//     display and a third of the characters.
//   - Keys are one or two characters. This is the one file in the game where
//     that is worth doing, because it is the only payload that repeats.
//   - Anything empty is omitted rather than sent as a zero or an empty array.
//
// The level itself is deliberately NOT here: it is sent once over HTTP when a
// run starts, and referenced by index thereafter.
const (
	// TypeHello attaches a connection to whatever run the account already
	// started over HTTP. It carries no fields at all — identity is the
	// connection, so there is nothing to forge and nothing to validate.
	TypeHello = "vanyadum_hello"
	// TypeInput is the batch of sub-steps described on Command.
	TypeInput = "vanyadum_input"

	// TypeReady confirms which run this socket is now attached to.
	TypeReady = "vanyadum_ready"
	// TypeSnapshot is the idempotent full-state frame.
	TypeSnapshot = "vanyadum_snap"
	// TypeOver ends a run.
	TypeOver = "vanyadum_over"
)

// envelope reads only the discriminator, so an unknown message costs one small
// allocation and is then dropped in silence.
type envelope struct {
	T string `json:"t"`
}

// InputFrame is one client→server input message.
//
// Seq is the client's own monotonic counter and is echoed back as Ack. Nothing
// in this iteration uses it: it is on the wire from the first day because adding
// a field to a live protocol later is a coordinated deploy, and reserving one
// costs four bytes a frame. If the netcode ever climbs to client-side
// prediction, this pair is the whole hook reconciliation needs.
type InputFrame struct {
	Seq  int64         `json:"seq"`
	Cmds []wireCommand `json:"cmds"`
}

type wireCommand struct {
	Dt    float64 `json:"dt"`
	MX    float64 `json:"mx"`
	MY    float64 `json:"my"`
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
}

// ParseInbound decodes a frame, returning its type and — for an input frame —
// the sanitised commands it carried.
//
// A malformed, unknown or over-long frame produces ("", nil): no reply, no error
// and no log line. Silence is the platform's policy for bad frames, because a
// log line per bad frame at the permitted ten a second is a flood lever handed
// to any client.
func ParseInbound(payload []byte) (string, *ParsedInput) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", nil
	}
	switch env.T {
	case TypeHello:
		return TypeHello, nil
	case TypeInput:
		var f struct {
			InputFrame
		}
		if err := json.Unmarshal(payload, &f); err != nil {
			return "", nil
		}
		return TypeInput, parseInput(f.InputFrame)
	default:
		return "", nil
	}
}

// ParsedInput is an input frame after validation.
type ParsedInput struct {
	Seq  int64
	Cmds []Command
}

func parseInput(f InputFrame) *ParsedInput {
	// A frame carrying more sub-steps than the sampling ratio allows is a client
	// asking for extra simulation time. The surplus is dropped rather than the
	// frame refused: the honest client that drifted by one step keeps playing,
	// and the dishonest one gains nothing.
	n := len(f.Cmds)
	if n > MaxCommandsPerFrame {
		n = MaxCommandsPerFrame
	}
	out := &ParsedInput{Seq: f.Seq, Cmds: make([]Command, 0, n)}
	for i := 0; i < n; i++ {
		// A direct conversion, which the two types being field-for-field
		// identical is what permits. They are separate types anyway, because the
		// wire form is allowed to grow a field (a button bitfield, next
		// iteration) that the simulation has no opinion about yet — and the day
		// they diverge, this line stops compiling rather than silently dropping
		// something.
		out.Cmds = append(out.Cmds, Command(f.Cmds[i]).Sanitise())
	}
	return out
}

// Ready tells a freshly-attached socket which run it is now watching.
type Ready struct {
	T     string `json:"t"`
	RunID string `json:"run_id"`
}

// Event is something that HAPPENED, as opposed to something that is true.
//
// A snapshot is state and a dropped one costs nothing, because the next one is
// the truth again. An event cannot be expressed that way — "a beer was picked
// up" is an instant, and the thing it drives is a sound and a flash. They ride
// the snapshot rather than travelling as their own frames, so a missed one costs
// a sound effect and never a divergence in state.
type Event struct {
	E  string `json:"e"`
	K  string `json:"k,omitempty"`
	ID int    `json:"id,omitempty"`
}

// EventPickup is emitted the instant a thing is collected.
const EventPickup = "pk"

// Snapshot is the idempotent full-state frame.
//
// Self is flattened into the top level rather than nested, because in this
// iteration there is exactly one player in an arena and a nested object is eight
// bytes of punctuation twenty times a second for nothing. Peers arrive with
// multiplayer as their own array beside these fields.
type Snapshot struct {
	T    string `json:"t"`
	Tick int64  `json:"k"`
	Ack  int64  `json:"ack"`
	// Centimetres.
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
	// Thousandths of a radian.
	Yaw int `json:"yaw"`
	// Sector index, so the client can pick the right light level without
	// working out where it is standing.
	Sector int            `json:"s"`
	Health int            `json:"hp"`
	Left   []int          `json:"pk"`
	Bag    map[string]int `json:"c,omitempty"`
	Events []Event        `json:"ev,omitempty"`
}

// Over ends a run. It is sent once, and the client stops sending input.
type Over struct {
	T       string         `json:"t"`
	Success bool           `json:"success"`
	Seconds int            `json:"secs"`
	Bag     map[string]int `json:"c,omitempty"`
}

// cm quantises metres to centimetres for the wire.
func cm(v float64) int { return int(math.Round(v * 100)) }

// mrad quantises radians to thousandths for the wire.
func mrad(v float64) int { return int(math.Round(v * 1000)) }
