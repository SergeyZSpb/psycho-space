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
