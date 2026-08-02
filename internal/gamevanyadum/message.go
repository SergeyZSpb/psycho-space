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
	// Fire is the trigger, and it is OMITTED IN ITS RESTING STATE because almost
	// no command is a trigger pull: a client emits forty sub-steps a second and
	// even a player firing as fast as the gun allows pulls three times in that
	// second. `"f":true` is nine bytes on the handful of commands that carry it
	// and nothing at all on the rest, where a field always present would be nine
	// bytes forty times a second, uplink, on mobile data — the direction of the
	// connection that is worst.
	Fire bool `json:"f,omitempty"`
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
		// form is allowed to grow a field the simulation has no opinion about
		// yet — and the day they diverge, this line stops compiling rather
		// than silently dropping something. The trigger was the first field
		// added since, and it went on BOTH: an input the simulation reads is
		// exactly what this conversion is for.
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
	// Health is what is left of you, and ZERO IS THE WHOLE OF BEING DEAD. There
	// is no "down" flag for yourself: your own health is already on every frame,
	// and hp falling IS the acknowledgement that you were hit — the same
	// per-frame comparison the client already makes for the barrel count and the
	// pickup mask. A field to say "you were shot" would be bytes on a payload
	// that repeats twenty times a second to restate something the payload
	// carries.
	Health int `json:"hp"`
	// Down is milliseconds until you get up, and Protect is milliseconds of
	// spawn protection left. Both are omitted at rest, which for a player who is
	// alive and unprotected is every frame.
	//
	// DURATIONS RATHER THAN FLAGS, which is the shape this project asks for: a
	// mark that flashes once says nothing about the seconds that follow, and the
	// two things a man on the floor wants to know are how long and how long
	// after that. They cannot both be set — protection begins when the down
	// window ends — so the widest frame carries one.
	//
	// PROTECTION MUST BE A NUMBER THE CLIENT CAN SIMULATE, not merely one it can
	// draw: it is on Player and the browser counts it down through the same Step
	// (sim.go), because the trigger is refused while it runs and prediction has
	// to know that before it draws a muzzle flash. It is therefore also the
	// reconcile base for that timer, exactly as `d` and `r` are for the gun's.
	Down    int `json:"dn,omitempty"`
	Protect int `json:"pr,omitempty"`
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
	Left uint32 `json:"pk"`
	// Loaded is how many barrels are ready to fire, and it is the shell count on
	// the HUD — the SERVER's, so a client that mispredicted a refused shot is
	// corrected within one frame rather than showing a number it made up.
	//
	// NOT `omitempty`, unlike the two timers below. An empty gun is exactly the
	// state a player most needs to see, and a resting gun is full rather than
	// zero, so omitting at zero would save nothing while making the reader
	// responsible for remembering that an absent field means the worst case.
	Loaded int `json:"b"`
	// Cooldown is milliseconds until the gun will fire again and Reload is
	// milliseconds until a reload finishes. Both omitted at rest, which is nearly
	// always: a gun is idle for most of every second even in a firefight.
	//
	// MILLISECONDS AS AN INT, quantised exactly as everything else on this frame
	// is. A float64 second serialises to seventeen characters of a precision no
	// screen can show; the int is three or four, and half a millisecond of error
	// against a 350 ms cadence is a thousandth of a frame.
	//
	// THEY CANNOT BOTH BE SET (sim.go, Player), and the wire budget is measured on
	// that: the widest frame this game can actually send carries the barrels and
	// one timer, never two.
	//
	// YOUR OWN SHOT NEEDS NO EVENT, and this is why. `b` falling by one between
	// two frames IS the shot, and `d` going from absent to set is the same
	// instant — both are per-frame comparisons the client already makes for the
	// pickup mask. An "I fired" event addressed to the man who fired would be
	// bytes on a payload that repeats twenty times a second to say nothing at all
	// almost every time it was sent.
	//
	// A PEER'S SHOT IS THE CASE THIS DOES NOT COVER, because a peer carries
	// neither of these two fields and giving it one would cost more than the
	// small integer that replaced them: see Peer.St for the measurement and for
	// why the cheap derivation available here is not available there.
	Cooldown int            `json:"d,omitempty"`
	Reload   int            `json:"r,omitempty"`
	Bag      map[string]int `json:"c,omitempty"`
	Events   []Event        `json:"ev,omitempty"`
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
	// rate, per viewer — and a peer is a MEASURED 56 bytes at the widest
	// quantisation the wire can carry, of which 7 are the state field this
	// iteration added. See that constant for the arithmetic, for the 8 kB/s
	// ceiling it is derived from, and for why the answer today is four.
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
// 71 bytes to 49, which took MaxOccupants from four to five — and the state
// field below has since given the place back, which is what a peer costing 56
// again means. The pose enum went because nothing could yet reduce anybody's
// health, so it described a state the simulation could not reach; `St` is that
// field returning, with the four states the обрез actually produces.
//
// THE SECTOR RATHER THAN THE HEIGHT, and being smaller is the lesser reason. The
// client holds the level, so a sector index is a floor height it can look up and
// a light level it could not have derived at all. Deriving the height from the
// POSITION instead would have cost nothing on the wire and been wrong at every
// doorway: a shared boundary belongs to both rooms, so two ends resolving it
// independently can disagree about which room a man in a doorway is in, and he
// would bob by up to MaxStep while standing still.
//
// NONE OF THE FIVE POSITION FIELDS IS `omitempty`, deliberately. Slot 0 is a real
// place and the first one handed out, and x and y are genuinely zero somewhere in
// every building — so omitting at zero would make the reader responsible for
// remembering the default on four separate fields. It would not help the case
// that matters in any event: the capacity is derived from the worst frame, and
// nothing is zero in that one. `St` is the exception and the reason is on it.
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
	// St is everything a viewer has to be told about this peer beyond where he is
	// standing: PeerFired, PeerHit, PeerDown or PeerProtected. Omitted for a man
	// who is alive, unprotected and did nothing on this tick, which is almost
	// every peer on almost every frame.
	//
	// ONE FIELD FOR FOUR STATES, because the rules make them mutually exclusive
	// and a field that can only hold one value at a time is one field. A man on
	// the floor cannot fire and cannot be hit again; a protected man can do
	// neither either (content.go, SpawnProtectSeconds). The single genuine
	// collision is firing and being shot on the same tick, and being shot wins —
	// the viewer is told the thing that changed the building.
	//
	// IT SAYS WHERE, NOT WHAT (CLAUDE.md). Four values the client tells apart by
	// colour and shape; no numbers, no names, no damage. It is the acknowledgement
	// half of every verb this iteration adds, and it is the SAME field for
	// everybody watching — an effect only its author can see is a bug rather than
	// a smaller feature.
	//
	// WHAT IT REPLACED, AND WHAT THAT COST. This was `,"f":true`, a bool that rode
	// the tick a trigger was pulled: 9 bytes at the gun's own cadence, 108 B/s at
	// the old capacity. Two of the four values above are DURATIONS rather than
	// actions — a man is down for DownTime and protected for SpawnProtectSeconds,
	// and both are non-zero on every tick they run, so they are priced at the
	// snapshot rate exactly as the peer's position is. That is the shape this
	// field's predecessor rejected at 640 B/s, and the reason the answer is
	// different now is that there is no cheaper one: a hit and a death cannot be
	// derived from any value the frame already carries, because neither moves the
	// man they happen to. `,"st":1` is 7 bytes — the floor for a JSON field — and
	// at 20 Hz on every peer it is what took MaxOccupants from five to four. See
	// that constant for the arithmetic, and note which way the trade went: a
	// smaller building, never a bigger ceiling.
	St int `json:"st,omitempty"`
}

// The values St takes. They are wire constants rather than catalogue entries:
// the client switches on them to choose a colour, which is a rendering decision
// and not a rule a player is told.
//
// PeerHit AND PeerFired ARE INSTANTS and are true for exactly the tick they
// happened on. PeerDown and PeerProtected are STATES and are true for every tick
// they last, which is what makes them the expensive half of this field.
const (
	// PeerFired is his gun going off. The man who fired needs no telling — his
	// own barrel count is on his own frame — so this is how everybody else finds
	// out, which is the whole reason it exists.
	PeerFired = 1
	// PeerHit is a shot landing on him. It is what tells the ROOM that somebody
	// was hit, and it is also how the SHOOTER learns he connected: he knows he
	// fired this tick, because his own `b` fell, and the man he was pointing at
	// is marked on the same frame. That derivation is why there is no separate
	// "you hit him" field addressed to the shooter.
	PeerHit = 2
	// PeerDown is a man on the floor, waiting out DownTime.
	PeerDown = 3
	// PeerProtected is a man who has just got up: he cannot be hurt and cannot
	// shoot. Shown for as long as it lasts, because a state with a duration is
	// not an event — and because somebody whose shots are bouncing off needs to
	// be told why rather than left to conclude the game is broken.
	PeerProtected = 4
)

// Standings is who is in the building, how long they have each been in it, and
// what they are carrying.
//
// IT IS THIS GAME'S WHOLE NOTION OF A SCORE, and it exists because nothing here
// ends. A match with no result has nothing to show at the end of it, so what it
// needs instead is something to look at in the middle: a readout that says how
// everybody is doing, updated while you play. The metric is deliberately what
// the game actually has today — time in the building, what everybody is
// carrying, how often they have been put on the floor and how many friends they
// have put there — rather than a shape with empty columns in it. It grew the
// last two on the iteration that made a player killable, which is the rule: a
// column appears with the thing it counts.
//
// WHAT IS CARRIED RATHER THAN WHAT WAS FOUND, and since the gun started spending
// beer the two are different numbers. Carried is the one worth publishing: it
// says who is out of ammunition, which is a thing to act on, where a lifetime
// total is trivia. The lifetime total is what the visit row keeps
// (world.go, Occupant.collected).
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
	// Bag is what they are CARRYING, keyed by the catalogue's Grants exactly as
	// the snapshot's own is. Omitted entirely for somebody carrying nothing,
	// which is everybody for their first minute — and everybody again once the
	// gun has drunk what they found.
	Bag map[string]int `json:"c,omitempty"`
	// Deaths is how many times the building has put them on the floor, and
	// Betrayals is how many friends they have put there.
	//
	// THERE IS NO KILL COLUMN, AND THAT IS THE JOKE RATHER THAN AN OMISSION.
	// Friendly fire is on and the нейрослопы have not arrived, so every kill in
	// this заброшка is a friend's — it scores nothing, it is not added to any
	// total, and it is published on its own line under the name the catalogue
	// gives it (content.go, BetrayalsTitle). A kill total appears on the day
	// there is something here worth killing.
	//
	// ON THIS FRAME AND NOT ON THE SNAPSHOT, which is the rule that made this
	// frame exist: both numbers move a few times a MINUTE, and restating them
	// twenty times a second per viewer is exactly what a once-a-second readout is
	// for. Omitted at zero, so a building where nobody has shot anybody carries
	// neither.
	Deaths    int `json:"d,omitempty"`
	Betrayals int `json:"br,omitempty"`
}

// cm quantises metres to centimetres for the wire.
func cm(v float64) int { return int(math.Round(v * 100)) }

// mrad quantises radians to thousandths for the wire.
func mrad(v float64) int { return int(math.Round(v * 1000)) }

// ms quantises seconds to milliseconds for the wire. The same arithmetic as
// mrad and deliberately a separate function: the two carry different units, and
// one of them existing does not make the other's rounding somebody else's
// decision.
func ms(v float64) int { return int(math.Round(v * 1000)) }
