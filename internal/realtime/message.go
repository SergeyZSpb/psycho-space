package realtime

import "encoding/json"

// Every frame on the wire is a JSON object carrying a "t" discriminator. A
// client dispatches on it and must ignore types it does not recognise, so
// teaching the server a new message type never breaks an older client.
const (
	// TypeBye is the server's last frame before it closes a socket. It carries
	// the reason that the transport-level close cannot — see Conn.Close.
	TypeBye = "bye"
)

// The reasons that travel with CloseTryAgainLater. Three distinct situations
// share that one code, so the reason string is the only thing separating them —
// and they call for different behaviour: a slow consumer should reconnect and
// expect the same treatment if its network has not improved, a rate-limited
// client should send less, and a client over the per-account cap should say so
// to the person rather than retry, because retrying cannot succeed until a tab
// somewhere else is closed.
//
// They are constants because a client branches on the exact text; changing one
// is a wire-protocol change, not a wording change.
const (
	// ReasonSlowConsumer: the client fell too far behind the broadcast and was
	// evicted rather than allowed to stall the room.
	ReasonSlowConsumer = "slow consumer"
	// ReasonRateLimited: the client sent frames faster than the socket allows.
	ReasonRateLimited = "rate limited"
	// ReasonTooManyConnections: the account, or the process, is already at its
	// connection cap. This one is delivered before the connection is ever
	// registered — the caps are checked after the 101, so there is no HTTP
	// status left to carry it (see Conn.Refuse).
	ReasonTooManyConnections = "too many connections"
)

// ReasonUnauthorized travels with CloseUnauthorized, and is the only reason that
// does. Both revocation paths use it — the in-process kick when an admin blocks
// someone, and the periodic sweep that catches an expired session or a block
// applied straight in the database — because the client's response is the same
// either way: stop, clear the session, and do not reconnect. The sweep could not
// distinguish them in any case; all it learns is that the credential no longer
// holds.
const ReasonUnauthorized = "unauthorized"

// Bye is the final frame a server-initiated close sends before the socket goes
// away.
//
// Code reuses the CloseGoingAway / CloseTryAgainLater / CloseUnauthorized values
// so the semantics are the ones already documented on those constants, even
// though they never travel as an actual WebSocket close code (Conn.Close
// explains why). A client branches on Code: 1001 reconnect promptly, 1013 back
// off, 4001 stop and clear its session.
type Bye struct {
	T      string `json:"t"`
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// encodeBye renders a Bye frame. Marshalling a struct of a string and an int
// cannot fail, so a nil return means "skip the frame" rather than an error worth
// propagating onto a path that is already closing.
func encodeBye(code int, reason string) []byte {
	b, err := json.Marshal(Bye{T: TypeBye, Code: code, Reason: reason})
	if err != nil {
		return nil
	}
	return b
}
