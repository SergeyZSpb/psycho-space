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
type Peer struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
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
