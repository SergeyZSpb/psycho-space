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
	// TypeStandings is the once-a-second readout of who is in the building. The
	// same bytes for everybody, unlike a snapshot.
	TypeStandings = "vanyadum_board"
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

// Ready tells a freshly-attached socket which building it is now standing in,
// and which place in it the reader has been given.
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
	// Slot is the place in the building this reader now holds, and it is how
	// they find THEMSELVES in the standings.
	//
	// Nothing else ever tells them: a snapshot names everybody EXCEPT its own
	// reader, so a client that was not told this could read the whole board and
	// not know which row was its own. It belongs here for the same reason the
	// world id does — it is constant for as long as the occupant is in the
	// building, so it is sent once per attach rather than on anything that
	// repeats. A reconnect is answered with the same number, because a second
	// hello is the same person walking back to the place he was holding.
	Slot int `json:"slot"`
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
	// Peers is everything in the building that is not you AND THAT YOU COULD
	// PLAUSIBLY SEE — the other people in the заброшка today, the нейрослопы when
	// they arrive. Omitted entirely when there is nobody, which is the common
	// case and the one that should cost nothing.
	//
	// FILTERED TO THE VIEWER'S OWN ROOM AND THE ROOMS THROUGH ITS DOORWAYS
	// (level.go, buildVisibility), plus anybody who was in one of those within the
	// last visibleHold. That makes the array a function of what is visible rather
	// than of how many people are in the building, which is what a phone on mobile
	// data actually experiences — though it does nothing for the worst case, where
	// everybody is standing in one room, and it is the worst case MaxOccupants is
	// derived from.
	//
	// ABSENCE IS THE WHOLE OF LEAVING THE SET, exactly as a bit going clear is
	// the whole of a pickup being taken: this array is idempotent full state, so
	// a peer who has walked out of view simply stops being in it. There is no
	// "he went away" event, and the client draws whoever the newest frames name
	// and nobody else — a figure kept alive because nothing said to remove it is
	// a ghost standing where somebody used to be.
	//
	// THIS IS WHAT BOUNDS MaxOccupants. The array is per viewer and holds
	// everybody else, so its cost is (occupants − 1) × a peer × the snapshot
	// rate, per viewer — and a peer is a MEASURED 49 bytes at the widest
	// quantisation the wire can carry. See that constant for the arithmetic, for
	// the 8 kB/s ceiling it is derived from, and for why the answer today is
	// five.
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
// addressed by a SLOT: a small integer naming a place in the building, published
// against a pseudonym on the standings frame, and reused once its holder leaves.
// An account id is still nowhere near this struct and never will be, because a
// frame addressed to one player names another and an account id would be a
// durable handle on a person handed to everybody who shares the building with
// them (ADR-037).
//
// THE PSEUDONYM ITSELF USED TO BE HERE, and it was 19 of the entry's 71 bytes,
// constant for the life of an occupant, on a payload that repeats twenty times a
// second — precisely what this project's bytes-on-the-wire rule forbids. What
// went, and what each removal saved at the widest quantisation the wire can
// carry:
//
//	the pseudonym, for the slot      −13   `"i":"K3jf9sLm2QpZ"` → `"n":9`
//	the eye height, for the sector    −3   `"z":12345` → `"s":12`
//	the pose enum                     −6   it was 0 in every frame ever sent
//
// 71 bytes to 49, which is the whole of what took MaxOccupants from four to
// five. The pose enum went because nothing in this game can yet reduce anybody's
// health, so it was a field describing a state the simulation cannot reach; it
// comes back with whatever first does damage.
//
// THE SECTOR RATHER THAN THE HEIGHT, and being smaller is the lesser reason. The
// client holds the level, so a sector index is a floor height it can look up and
// a light level it could not have derived at all. Deriving the height from the
// POSITION instead would have cost nothing on the wire and been wrong at every
// doorway: a shared boundary belongs to both rooms, so two ends resolving it
// independently can disagree about which room a man in a doorway is in, and he
// would bob by up to MaxStep while standing still.
//
// NOTHING HERE IS `omitempty`, deliberately. Slot 0 is a real place and the first
// one handed out, and x and y are genuinely zero somewhere in every building — so
// omitting at zero would make the reader responsible for remembering the default
// on four separate fields. It would not help the case that matters in any event:
// the capacity is derived from the worst frame, and nothing is zero in that one.
type Peer struct {
	// Slot is the place in the building this entity holds. The standings frame
	// says whose it currently is.
	Slot int `json:"n"`
	X    int `json:"x"`
	Y    int `json:"y"`
	// Sector is the room he is standing in: the client's source for how high to
	// draw him (that sector's floor plus the eye height) and how dark the room he
	// is in should make him.
	Sector int `json:"s"`
	Yaw    int `json:"yaw"`
}

// Standings is who is in the building, how long they have each been in it, and
// what they are carrying.
//
// IT IS THIS GAME'S WHOLE NOTION OF A SCORE, and it exists because nothing here
// ends. A match with no result has nothing to show at the end of it, so what it
// needs instead is something to look at in the middle: a readout that says how
// everybody is doing, updated while you play. The metric is deliberately what
// the game actually has today — time in the building and what has been collected
// — rather than a shape with empty columns in it. Kills and streaks are fields
// this frame grows on the day something can be killed, and not before.
//
// A FRAME OF ITS OWN AT ONE HERTZ, AND NOT A FIELD ON THE SNAPSHOT. The
// arithmetic is the whole argument. A seconds-and-bag pair on each peer is about
// 25 bytes (`"s":999999,"c":{"beer":9}`), and the snapshot is built per occupant
// and carries everybody, so at MaxOccupants that is 25 × 20 × 5 ≈ 2.5 kB/s per
// viewer — nearly a third of the whole budget — to restate numbers that change a
// few times a minute, twenty times a second. The same rows on their own frame at
// StandingsInterval cost 293 B/s. Twenty times cheaper, for a readout nobody
// reads at frame rate.
//
// IT IS NOT FILTERED, and that is the point of it rather than an oversight. A
// snapshot is what you can SEE and is therefore cut to your own room and the
// rooms through its doorways; this is what is TRUE OF THE BUILDING, so it lists
// everybody — including the person reading it, and including the man two rooms
// away that reader has no idea is there. Which also makes it identical for every
// reader, so the service marshals it once and writes the same bytes to every
// connection.
//
// IT IS ALSO THE SLOT DIRECTORY, and that is not a second job bolted on. A
// snapshot addresses a peer by a slot, and a slot is reused after its holder
// leaves, so the mapping from slot to pseudonym has to be published somewhere —
// and a per-occupant roster that already exists at 1 Hz is exactly where it
// costs nothing extra. It is why the roster CHANGING publishes one too, on the
// tick it changed: see World.RosterVersion.
//
// NO TICK ON IT. A frame that has to be placed on a timeline carries one (see
// Snapshot, and the interpolation that runs on it); this is a readout with no
// history, the socket delivers in order, and the newest one to arrive is simply
// the truth.
type Standings struct {
	T    string         `json:"t"`
	Rows []StandingsRow `json:"b,omitempty"`
}

// StandingsRow is one occupant on that readout.
type StandingsRow struct {
	// Slot is the place in the building, and it is the same number the snapshot
	// addresses this person's peer entry by. That correspondence is the entire
	// reason both frames exist in the shape they do.
	Slot int `json:"n"`
	// Name is the per-process pseudonym other players are told (ADR-037) — never
	// an account id, and meaningless after a restart.
	Name string `json:"i"`
	// Seconds is how long they have been in the building.
	Seconds int `json:"s"`
	// Bag is what they have collected, keyed by the catalogue's Grants exactly as
	// the snapshot's own is. Omitted entirely for somebody carrying nothing,
	// which is everybody for their first minute.
	Bag map[string]int `json:"c,omitempty"`
}

// cm quantises metres to centimetres for the wire.
func cm(v float64) int { return int(math.Round(v * 100)) }

// mrad quantises radians to thousandths for the wire.
func mrad(v float64) int { return int(math.Round(v * 1000)) }
