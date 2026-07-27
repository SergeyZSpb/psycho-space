package gamevanyagotchi

import (
	"encoding/json"
	"fmt"
)

// This game's wire types. They live here and not in internal/realtime: the hub
// carries bytes for whoever is publishing and must not learn that a game exists
// (ADR-028). The only type the transport owns is its own "bye".
//
// Every frame is a JSON object with a "t" discriminator, and both sides ignore a
// type they do not recognise — so teaching one end a new message never breaks
// the other. The names are game-scoped rather than bare ("move"), because the
// room is a transport-level namespace that a second realtime feature could one
// day share, and a collision there would be silent.
const (
	// TypeRoster is the server's periodic snapshot of who is on the plane and
	// where. See Roster for why it is a snapshot rather than a diff.
	TypeRoster = "vanyagotchi_roster"
	// TypeMove is the client asking to stand somewhere. It is a request, not a
	// statement: the server validates it, clamps it, and the position only
	// becomes real when it appears in the next roster.
	TypeMove = "vanyagotchi_move"
	// TypeHello is a client asking which entity in the roster is itself. It
	// carries no fields, and could not usefully carry any: the answer depends on
	// the connection the frame arrived on, and nothing a payload claimed about
	// identity would be trusted.
	TypeHello = "vanyagotchi_hello"
	// TypeYou is the unicast answer to TypeHello — see You.
	TypeYou = "vanyagotchi_you"
	// TypeDo is the client asking for one or more verbs to be applied to its own
	// pet. It carries a LIST because a batch is the general case and a single
	// press is a batch of one — the same shape the fold takes, so nothing has to
	// special-case the common path.
	//
	// It carries no instant. The server stamps every event, because a client
	// time would be forgeable and everything in this game is integrated against
	// timestamps — a drink backdated six hours would rewrite the decay after it.
	TypeDo = "vanyagotchi_do"
	// TypeStateFrame is the owner's own pet, pushed after it changes.
	//
	// NOT an acknowledgement, and the distinction is the whole design: it carries
	// no correlation to a request, it is sent for any change including one made
	// on the player's other device, and nothing waits for it. It is the same
	// "state, never a reply" rule the roster follows, applied to the half of the
	// game that is per-player rather than shared.
	TypeStateFrame = "vanyagotchi_state"
)

// Roster is the whole plane, every tick.
//
// Deliberately full state rather than a diff of what changed. The hub drops
// frames for a client that has fallen behind, and evicts one that keeps falling
// behind — so a diff stream would leave a slow phone quietly rendering a world
// that no longer exists, with nothing able to detect it. A snapshot makes a lost
// frame cost exactly nothing: the next one is the truth again. It is also why
// this can be published from a plain ticker with no bookkeeping.
type Roster struct {
	T     string `json:"t"`
	Peers []Peer `json:"peers"`
	// Here is how many PEOPLE are in the yard — not how many entities the frame
	// carries, which also includes the NPCs and the sleepers.
	//
	// Counted by the server and published, so the client can say «во дворе: 3»
	// without having to learn what an NPC is. Deriving it client-side would mean
	// teaching the browser to tell a person from a character, which is exactly
	// the content knowledge the entity frame exists to keep out of it.
	Here int `json:"here"`
	// Hunt is the id of the key hunt currently running, or empty.
	//
	// STATE, NEVER AN ANNOUNCEMENT, and that distinction has teeth: a one-shot
	// «ключи снова потерялись» is exactly what somebody who opened the app
	// thirty seconds later misses. Riding the idempotent full-state frame means
	// joining at any instant shows the world as it is, and a late joiner can
	// take part in a hunt already in progress.
	//
	// The FLAVOUR LINE IS A TRANSITION rather than a message: a client that sees
	// this id change knows a fresh hunt started and says so; one that has just
	// connected simply finds a hunt already running and says nothing, which is
	// correct.
	//
	// Twelve characters, the same truncation everything else on this frame uses —
	// about 20 bytes a frame, 100 B/s to each phone, for a field that changes a
	// few times an evening.
	Hunt string `json:"hunt,omitempty"`
	// Store is the beer store: where it stands and how much is left in it.
	// Absent when the yard has no crate at all, which is a state the next hello
	// ends.
	//
	// ONE SHARED FIELD, NOT A FIELD PER PEER, and the arithmetic is the reason.
	// `"store":{"x":0.82,"y":0.22,"left":6},` is about 36 bytes; on the frame it
	// costs 36 × 5 = 180 B/s to each phone however busy the yard is. Hung off
	// every peer instead it would be 36 × entities × 5 per viewer — at the
	// twenty-four the world cap allows, 4.3 KB/s to each phone to say the same
	// number twenty-four times. It is one fact about the world, so it is carried
	// once.
	//
	// It is also why this is not a per-recipient field either: the frame is fanned
	// out to the whole room, so anything that differed per player would have to be
	// rendered per player — the identical reason Peer carries no "you" flag. The
	// client works out its own distance from its own entity, which it already
	// knows from the hello.
	//
	// AND WHY IT IS A STRUCTURE RATHER THAN A KIND KEY. The client is deliberately
	// kind-agnostic: an object arrives as an ordinary entity and the browser has
	// no way to tell which `obj-` id is a crate, which is exactly the property
	// that makes a new kind a backend change. So it is told "here is the store"
	// rather than "find the entity whose kind is beer_crate", and it can grey the
	// drink button — too far, or nothing left — while still holding no content key
	// at all.
	Store *Store `json:"store,omitempty"`
}

// Store is the beer store on the wire: a place and a count.
//
// Both halves are gates the client renders and the SERVER enforces regardless.
// `Left` is the one contested number published before the fact, which is safe
// because it decides nothing: the draw is a conditional UPDATE, so a client
// acting on a count that has just gone stale is refused rather than served, and
// no arrangement of stale frames can oversell the crate.
type Store struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// Left is how many drinks the crate still holds. Never nought on a published
	// frame — the draw that empties a crate exhausts it and stands a fresh one up
	// in the same transaction, so an empty store is absent rather than zero.
	Left int `json:"left"`
}

// Peer is one entity on the plane: one ACCOUNT, not one connection. Signing in
// on a phone and on a laptop puts one Ваня in the yard, standing in one place,
// and a move from either device moves that one.
//
// ID is a per-process PSEUDONYM of the account and never accounts.id. The roster
// is fanned out to everybody in the room, so whatever sits in this field is a
// handle every other player can record and correlate; the account id would make
// that handle durable and cross-session, which is precisely what this project's
// data posture declines to broadcast (CLAUDE.md → *Security & personal data*).
// (*Service).pseudonym carries the derivation and why its key lives and dies
// with the process. Nothing here is personal data, which is why the roster can
// be published with no redaction step.
//
// There is no "you" flag, and there should not be: this frame goes to everybody,
// so a per-recipient field would have to be rendered per recipient. A client
// learns which entity is its own by sending TypeHello once and keeping the id
// from the TypeYou reply.
//
// Art, Label and Pose are what stop the yard being a field of anonymous dots.
// They are DERIVED from the pet the account owns — its skin, its name, and its
// condition worked out from its own stats — and they are sent to everybody,
// because a world where each player can only see their own Ваня properly is two
// worlds rather than one shared one.
//
// The client stays kind-agnostic: it resolves Art against the catalogue it
// already fetched and renders the emoji-over-gradient placeholder for a key it
// has no image for. It contains no skin key, no pose name and no notion of what
// a Ваня looks like, which is what keeps "add a skin" a backend-only change —
// and it is why an NPC will be able to arrive later as just another entry in
// this list, with no client work at all.
type Peer struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	// Art is a catalogue skin key, resolved client-side.
	Art string `json:"art"`
	// Label is the pet's name, absent until it has been given one.
	Label string `json:"label,omitempty"`
	// Pose is how he is doing: see PoseFine and friends.
	Pose string `json:"pose"`
	// Say is a line over his head, absent almost always.
	//
	// Its first use is «устал» — the walk he did not finish. The cringe-phrase
	// pool arrives later on this same field, which is why it is a string rather
	// than a second pose: a pose is how he looks, and this is something he said.
	Say string `json:"say,omitempty"`
	// Expires is when this entity stops existing, as unix seconds, or absent for
	// one that does not.
	//
	// An ABSOLUTE instant rather than "seconds left", and that is the whole
	// reason it can be on the frame at all: an absolute expiry is CONSTANT for
	// the life of the object, so it does not churn the client's appearance guard
	// five times a second, whereas a countdown would change on every tick and
	// re-render every deposit in the yard. The client subtracts it from the
	// server clock it already tracks and fades the thing out itself — the same
	// division of labour the stat bars use, where the server sends a rate and
	// the browser does the interpolating.
	Expires int64 `json:"expires,omitempty"`
	// THERE IS NO AVATAR FIELD, and its absence is a decision rather than an
	// omission. A picture is fetched by ID over ordinary HTTP — see
	// Service.AvatarFor and GET /api/game-vanyagotchi/avatar/{peer} — for two
	// reasons that point the same way.
	//
	// A URL on this frame would be re-sent for every player five times a second
	// forever: a couple of hundred characters that never change, multiplied by
	// the number of people in the yard and again by the number watching. And it
	// would be the one DURABLE thing on an otherwise ephemeral frame — a VK URL
	// comes from Postgres and survives a restart, while ID beside it is a
	// per-process pseudonym that deliberately does not, so two frames from
	// either side of a deploy would be linkable by it.
	//
	// Fetching by ID costs nothing on the wire and puts the picture behind the
	// same pseudonym everything else about a person is behind (ADR-037).
}

// You tells one client which entity in the roster is its own. It is the answer
// to TypeHello and goes to that connection alone.
//
// ID is the same pseudonym the roster uses for that account, so the client
// matches it against Peer.ID by equality and needs to understand nothing about
// how it was derived. It is stable for the life of the process and identical on
// every device that account is signed in on — which is what makes "highlight
// me" work on the second device without a second handshake protocol.
type You struct {
	T  string `json:"t"`
	ID string `json:"id"`
}

// move is the inbound frame. Pointers on the coordinates so that a payload which
// simply omits one is rejected rather than silently read as the origin — a
// missing field and a deliberate 0 must not look the same.
type move struct {
	T string   `json:"t"`
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
}

// envelope reads just the discriminator, so an unknown type costs one small
// decode and never has to satisfy any other type's shape.
type envelope struct {
	T string `json:"t"`
}

// parseInbound turns a raw frame into the position the sender is asking for.
//
// It is a pure function on purpose: every rejection case — malformed JSON, an
// unknown type, a missing coordinate, a NaN, an infinity, a value off the plane
// — is then a table row in a unit test rather than something that needs a socket
// to reach. Out-of-range coordinates are clamped rather than refused, because
// the honest reading of a tap slightly outside the plane is the edge of it.
func parseInbound(payload []byte) (Point, error) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return Point{}, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if env.T != TypeMove {
		return Point{}, fmt.Errorf("%w: %q", ErrUnknownMessage, env.T)
	}

	var m move
	if err := json.Unmarshal(payload, &m); err != nil {
		return Point{}, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if m.X == nil || m.Y == nil {
		return Point{}, fmt.Errorf("%w: x and y are both required", ErrInvalidPosition)
	}
	x, okX := clampUnit(*m.X)
	y, okY := clampUnit(*m.Y)
	if !okX || !okY {
		return Point{}, fmt.Errorf("%w: coordinates must be finite", ErrInvalidPosition)
	}
	return Point{X: x, Y: y}, nil
}

// do is the inbound verb frame: a list, applied in order.
type do struct {
	T     string   `json:"t"`
	Verbs []string `json:"verbs"`
}

// StateFrame carries a player their own pet after it changes.
//
// The same State the HTTP read answers with, so the client has one shape to
// render and cannot drift between the two paths. Sent only to the connections of
// the account it belongs to — a pet's numbers are its owner's, and the shared
// roster deliberately publishes only a derived pose.
type StateFrame struct {
	T     string `json:"t"`
	State State  `json:"state"`
}

// parseVerbs turns a raw frame into the verbs it asks for.
//
// Pure, like parseInbound, so every rejection is a table row rather than
// something needing a socket. It validates SHAPE only — that the frame is the
// right type and carries a plausible list. Whether a verb exists, and whether
// this pet may take it, is the service's business and is decided against the
// catalogue and the pet's own state.
func parseVerbs(payload []byte) ([]string, error) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if env.T != TypeDo {
		return nil, fmt.Errorf("%w: %q", ErrUnknownMessage, env.T)
	}

	var d do
	if err := json.Unmarshal(payload, &d); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if len(d.Verbs) == 0 {
		return nil, fmt.Errorf("%w: verbs is required and must not be empty", ErrNoVerbs)
	}
	// Checked here as well as in Do, because this is the edge: a frame asking
	// for a thousand verbs must be refused before anything reads a database,
	// not after.
	if len(d.Verbs) > maxBatch {
		return nil, fmt.Errorf("%w: %d verbs, limit %d", ErrBatchTooLong, len(d.Verbs), maxBatch)
	}
	for _, v := range d.Verbs {
		if v == "" {
			return nil, fmt.Errorf("%w: an empty verb", ErrNoVerbs)
		}
	}
	return d.Verbs, nil
}
