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
// The level itself is deliberately NOT here: it is fetched once over HTTP and
// referenced by index thereafter.
const (
	// TypeHello walks this connection's account into the заброшка. It carries no
	// fields at all — identity is the connection, so there is nothing to forge
	// and nothing to validate — and it is the ONLY way in: there is no start
	// endpoint and no lobby.
	TypeHello = "vanyadum_hello"
	// TypeInput is the batch of sub-steps described on Command.
	TypeInput = "vanyadum_input"

	// TypeReady confirms which building this socket has been let into.
	TypeReady = "vanyadum_ready"
	// TypeSnapshot is the idempotent full-state frame.
	TypeSnapshot = "vanyadum_snap"
	// TypeFull refuses a hello because the заброшка already holds MaxOccupants.
	TypeFull = "vanyadum_full"
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
	// Seen is the last snapshot tick this client had drawn when it sent this
	// frame. It is how the server MEASURES ROUND TRIP without a ping: the tick
	// rate is fixed, so the difference between the current tick and this one is
	// the whole loop — the trip out, the client's own frame, and the trip back.
	// Lag compensation rewinds by exactly that, so it has to come from
	// somewhere, and deriving it beats trusting a client-reported number.
	Seen int64         `json:"k"`
	Cmds []wireCommand `json:"cmds"`
}

// wireCommand is one sub-step.
//
// Seq is PER COMMAND, not per frame. That granularity is what reconciliation
// needs: the server has to be able to say "I applied three of the four you
// sent", and a frame-level sequence cannot express it. A frame therefore
// carries commands with consecutive sequence numbers and the server
// acknowledges the last one it actually folded in.
//
// It is also what makes INPUT REDUNDANCY free: a client may resend commands it
// has not seen acknowledged, and the server drops any whose seq it has already
// ACCEPTED — which includes the ones still waiting in its queue and not only
// the ones already stepped (Occupant.highSeq). One lost packet then costs
// nothing at all.
//
// Sequence numbers are 1-BASED. Zero means "unset", and an unset command is
// dropped rather than applied — the world deduplicates against the highest
// sequence it has ACCEPTED, which starts at zero, so accepting a zero would
// make the very first command indistinguishable from one already seen.
type wireCommand struct {
	Seq   int64   `json:"q"`
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
	Seen int64
	Cmds []Command
}

func parseInput(f InputFrame) *ParsedInput {
	// A frame carrying more sub-steps than the sampling ratio allows is a client
	// asking for extra simulation time. The surplus is dropped rather than the
	// frame refused: the honest client that drifted by one step keeps playing,
	// and the dishonest one gains nothing.
	//
	// Redundant commands ride along with the fresh ones, so a frame may legally
	// be larger than the sampling ratio. The bound is on how much SIMULATION a
	// frame can ask for, and a re-sent command asks for none because
	// World.Enqueue drops every sequence it has already ACCEPTED — the ones
	// still sitting in the queue as well as the ones already stepped. That
	// dedupe is the whole of what this cap rests on, which is why it is the
	// ratio plus the redundancy window rather than the ratio alone: deduplicate
	// on what has been APPLIED instead and the surplus this cap permits becomes
	// real simulation a client did not pay for.
	n := len(f.Cmds)
	if n > MaxCommandsPerFrame+RedundantCommands {
		n = MaxCommandsPerFrame + RedundantCommands
	}
	out := &ParsedInput{Seen: f.Seen, Cmds: make([]Command, 0, n)}
	for i := 0; i < n; i++ {
		// A direct conversion, which the two types being field-for-field
		// identical is what permits. They stay separate types because the wire
		// form is allowed to grow a field (a button bitfield, next iteration)
		// that the simulation has no opinion about yet — and the day they
		// diverge, this line stops compiling rather than silently dropping
		// something.
		//
		// This clamp is the WIRE boundary's own, and it is one of three —
		// World.Enqueue and Step each clamp again, and each carries a guarantee
		// the other two do not. The duplication is deliberate; the arrangement is
		// written out in full at the Enqueue site (world.go).
		out.Cmds = append(out.Cmds, Command(f.Cmds[i]).Sanitise())
	}
	return out
}

// Ready tells a freshly-attached socket which building it is now standing in.
//
// WorldID IS THE CACHE KEY FOR THE GEOMETRY, and that is the whole reason it is
// on this frame. The заброшка is regenerated whenever it empties, so a client
// holding a level it fetched earlier has no other way to know whether that level
// is still the one everybody is walking around in. It compares, and re-fetches
// GET /api/game-vanyadum/world when the two disagree. Sent once per attach, so
// a UUID's thirty-six characters cost nothing that repeats.
type Ready struct {
	T       string `json:"t"`
	WorldID string `json:"world_id"`
}

// Full is the answer to a hello the заброшка cannot honour, because it already
// holds MaxOccupants.
//
// It carries no fields: the capacity is in the catalogue the client fetched
// once, and there is nothing else honest to say — nothing in this game ends, so
// there is no time at which a place is known to come free. The client's move is
// to say so in Russian and offer to try again.
type Full struct {
	T string `json:"t"`
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
// Self is flattened into the top level rather than nested; everybody else in the
// building is in Peers. A nested "me" object would be eight bytes of punctuation
// twenty times a second to say something the frame's own address already says.
type Snapshot struct {
	T string `json:"t"`
	// Tick is the simulation step this frame describes. With a fixed rate it is
	// a TIMELINE — two snapshots and their tick numbers are all the client
	// needs to place an entity between them, which is what entity
	// interpolation runs on. Without it a client can only guess how far apart
	// two frames were, and jitter makes that guess wrong.
	Tick int64 `json:"k"`
	// Ack is the last COMMAND sequence this player had folded in. The client
	// drops everything at or below it from its pending list and replays the
	// rest on top of the authoritative position below.
	Ack int64 `json:"ack"`
	// Centimetres.
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
	// Thousandths of a radian.
	Yaw int `json:"yaw"`
	// Sector index, so the client can pick the right light level without
	// working out where it is standing.
	Sector int `json:"s"`
	Health int `json:"hp"`
	// Left is what is lying on the floor right now, as a BITMASK: bit i is set
	// when the pickup at INDEX i of the level's Pickups is there to be walked
	// over. The index and not the id — an index is dense by construction, an id
	// need not be, and a mask can only be as wide as the thing it indexes.
	//
	// It was the list of remaining ids, recomputed and re-sent twenty times a
	// second in order to say "nothing was taken". That was the one field on an
	// otherwise disciplined frame whose size grew with the level's contents. A
	// word costs the same whatever it holds.
	//
	// A RESPAWN TRAVELS ON THIS AND NOTHING ELSE. Things come back (content.go,
	// PickupRespawn), and a bit going from clear to set IS that having happened —
	// so a client that wants to mark the moment compares this word against the
	// previous frame's, per bit. An "it respawned" event would be bytes on a
	// payload that repeats twenty times a second, per viewer, to say nothing at
	// all almost every time it was sent.
	//
	// UINT32 AND NOT UINT64, DELIBERATELY. A JSON number is an IEEE754 double in
	// a browser, so a 64-bit mask would lose its high bits in the PARSE rather
	// than in transit — silently, and only on the levels big enough to reach
	// them. 32 is the widest word both ends read back exactly, and it is far more
	// than any level has ever generated; MaxWirePickups is that bound, pinned by
	// a test over the generator. If a level ever genuinely needs more, the answer
	// is a SECOND WORD — an array of two — and never a wider integer.
	//
	// Still idempotent full state, exactly as the list was: a dropped frame costs
	// nothing, because the next one restates the whole world.
	Left   uint32         `json:"pk"`
	Bag    map[string]int `json:"c,omitempty"`
	Events []Event        `json:"ev,omitempty"`
	// Peers is everything in the building that is not you — the other people in
	// the заброшка today, the нейрослопы when they arrive. Omitted entirely when
	// you are alone in there, which is the common case and the one that should
	// cost nothing.
	//
	// THIS IS WHAT BOUNDS MaxOccupants. The array is per viewer and holds
	// everybody else, so its cost is (occupants − 1) × a peer × the snapshot
	// rate, per viewer — and a peer is a MEASURED 72 bytes at the widest
	// quantisation the wire can carry, about 60 at the magnitudes a generated
	// level produces. See that constant for the arithmetic, for the 8 kB/s
	// ceiling it is derived from, and for why the answer today is four.
	//
	// ID IS THE PART OF A PEER WORTH SHRINKING FIRST, and it is called out here
	// because this struct is where it would be done: the pseudonym is 19 bytes
	// of an entry's 71 and it does not change for the life of an occupant, which
	// is exactly what a repeating frame is not supposed to carry.
	//
	// A peer is drawn INTERPOLATED, about a hundred milliseconds in the past,
	// because its intent cannot be predicted the way your own is.
	Peers []Peer `json:"p,omitempty"`
}

// MaxWirePickups is how many pickups a level may contain, because it is the
// width of Snapshot.Left.
//
// A generator that produced more would publish the surplus as PERMANENTLY GONE —
// Go evaluates a shift at or past a word's width as zero — so the client would
// never draw them and nobody would ever walk to them, however long they waited
// for a respawn that had already happened. A part of the building quietly
// missing rather than an error, which is exactly the kind of thing that has to
// be caught by a test rather than by a player.
//
// Pinned over the GENERATOR and not checked on a frame: it is a property of what
// levels are allowed to be, and a frame is far too late to discover it.
const MaxWirePickups = 32

// Peer is one entity that is not the player receiving this frame.
//
// Quantised exactly as self is — centimetres and thousandths of a radian — and
// identified by a PER-PROCESS PSEUDONYM rather than by an account id. A frame
// is addressed to one player, but it names another, and an account id would be
// a durable handle on a person handed to everybody who shares the building with
// them. The same rule «Ванягоччи» settled on for its roster (ADR-037).
type Peer struct {
	ID  string `json:"i"`
	X   int    `json:"x"`
	Y   int    `json:"y"`
	Z   int    `json:"z"`
	Yaw int    `json:"yaw"`
	// State is a small enum for the pose: 0 idle, 1 moving, 2 dead. Everything
	// a peer's appearance needs that is not its position.
	State int `json:"s,omitempty"`
}

// cm quantises metres to centimetres for the wire.
func cm(v float64) int { return int(math.Round(v * 100)) }

// mrad quantises radians to thousandths for the wire.
func mrad(v float64) int { return int(math.Round(v * 1000)) }
