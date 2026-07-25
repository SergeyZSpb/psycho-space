// Package realtime carries the WebSocket layer: a hub that fans messages out to
// the clients in a room, and the per-connection pumps that feed it.
//
// Authority does not live here. The hub broadcasts what a domain service has
// already decided and written; it never decides anything itself and never
// accepts game state from a client. Presence — who is connected — is the one
// thing it does own, and that is in memory only: it is meaningless after a
// restart, so persisting it would only let it lie.
//
// There is one process, so an in-memory hub is not a compromise, it is the
// whole design: microsecond fanout, nothing to serialise, nothing to run.
//
// Nothing in this package may reach the LLM. It is the only paid dependency in
// the application, and its cost is bounded by a per-IP rate limit on a single
// HTTP endpoint. A socket message is not bounded the same way, and a broadcast
// or a timer could multiply one player's action into many calls. If a feature
// ever needs the judge, it goes through that HTTP endpoint.
package realtime

import (
	"context"
	"log/slog"
	"sync"
)

// Close codes the hub asks a connection to close with. Plain ints so this file
// stays free of the websocket library and can be unit-tested without sockets.
const (
	// CloseGoingAway (1001) means the process is shutting down. The client
	// treats it as a planned restart and reconnects quickly.
	CloseGoingAway = 1001
	// CloseTryAgainLater (1013) means the client was evicted for falling behind
	// or exceeding a cap. Reconnect after a delay.
	CloseTryAgainLater = 1013
	// CloseUnauthorized (4001) is application-defined and TERMINAL: the session
	// is gone or the account is no longer approved. A client that reconnects on
	// this would hammer the handshake forever.
	CloseUnauthorized = 4001
)

// Defaults chosen for a small allowlisted group, not for scale. Each has a
// stated trigger for revisiting in the design doc.
const (
	// defaultSendBuffer is how many messages may be queued for one client
	// before it is considered behind.
	defaultSendBuffer = 64
	// maxOverflowsBeforeEvict is how many times a client may overflow that
	// buffer before the hub drops it.
	maxOverflowsBeforeEvict = 3
	// defaultMaxPerAccount allows a phone, a laptop and one stale tab.
	defaultMaxPerAccount = 3
	// defaultMaxTotal bounds process memory: ~40 KiB of buffers per connection.
	defaultMaxTotal = 200
)

// Sink is the hub's view of one connection. Keeping it an interface is what
// lets every hub test run without a socket — including the backpressure test,
// which is impossible to drive reliably through a real one.
type Sink interface {
	// ID uniquely identifies this connection.
	ID() string
	// AccountID is the authenticated account, bound at upgrade. It is never
	// read from a message body.
	AccountID() string
	// TrySend queues a message without blocking. It reports false when the
	// client's buffer is full, which the hub treats as "this client is behind".
	TrySend(msg []byte) bool
	// Close asks the connection to close. It must be safe to call more than
	// once and must not block.
	Close(code int, reason string)
}

type client struct {
	sink      Sink
	room      string
	overflows int
}

type registerCmd struct {
	sink Sink
	room string
	err  chan<- error
}

type publishCmd struct {
	room string
	msg  []byte
}

type kickCmd struct {
	accountID string
	code      int
	reason    string
}

// Hub fans messages out to the connections in a room. All shared state is owned
// by the single goroutine running Run; everything else talks to it over
// channels, so there are no mutexes and no lock ordering to get wrong.
type Hub struct {
	register   chan registerCmd
	unregister chan Sink
	publish    chan publishCmd
	kick       chan kickCmd

	maxPerAccount int
	maxTotal      int

	// done is closed when Run returns, so shutdown can wait for the drain.
	done     chan struct{}
	doneOnce sync.Once
}

// NewHub builds a hub. Run must be called for it to do anything.
func NewHub() *Hub {
	return &Hub{
		register:      make(chan registerCmd),
		unregister:    make(chan Sink),
		publish:       make(chan publishCmd, 64),
		kick:          make(chan kickCmd, 8),
		maxPerAccount: defaultMaxPerAccount,
		maxTotal:      defaultMaxTotal,
		done:          make(chan struct{}),
	}
}

// Done is closed once Run has returned and every client has been asked to
// close. Shutdown waits on it before closing the HTTP server, because
// http.Server.Shutdown does not close or wait for hijacked connections.
func (h *Hub) Done() <-chan struct{} { return h.done }

// Run owns the hub's state until ctx is cancelled, then closes every client
// with CloseGoingAway and returns. It blocks; call it in its own goroutine.
func (h *Hub) Run(ctx context.Context) {
	defer h.doneOnce.Do(func() { close(h.done) })

	clients := make(map[Sink]*client)
	rooms := make(map[string]map[Sink]*client)
	perAccount := make(map[string]int)

	remove := func(s Sink) {
		c, ok := clients[s]
		if !ok {
			return
		}
		delete(clients, s)
		if r := rooms[c.room]; r != nil {
			delete(r, s)
			if len(r) == 0 {
				delete(rooms, c.room)
			}
		}
		if n := perAccount[s.AccountID()] - 1; n > 0 {
			perAccount[s.AccountID()] = n
		} else {
			delete(perAccount, s.AccountID())
		}
	}

	for {
		select {
		case <-ctx.Done():
			for s := range clients {
				s.Close(CloseGoingAway, "restart")
			}
			slog.InfoContext(ctx, "realtime hub stopped", "clients", len(clients))
			return

		case cmd := <-h.register:
			switch {
			case len(clients) >= h.maxTotal:
				cmd.err <- ErrTooManyConnections
			case perAccount[cmd.sink.AccountID()] >= h.maxPerAccount:
				cmd.err <- ErrTooManyConnections
			default:
				c := &client{sink: cmd.sink, room: cmd.room}
				clients[cmd.sink] = c
				if rooms[cmd.room] == nil {
					rooms[cmd.room] = make(map[Sink]*client)
				}
				rooms[cmd.room][cmd.sink] = c
				perAccount[cmd.sink.AccountID()]++
				cmd.err <- nil
			}

		case s := <-h.unregister:
			remove(s)

		case cmd := <-h.publish:
			for s, c := range rooms[cmd.room] {
				if c.sink.TrySend(cmd.msg) {
					c.overflows = 0
					continue
				}
				// Never block on a slow client: one phone on a bad connection
				// must not freeze the room for everyone. Presence is broadcast
				// as idempotent full state, so a dropped frame followed by the
				// next one leaves the client correct rather than corrupt.
				c.overflows++
				if c.overflows >= maxOverflowsBeforeEvict {
					remove(s)
					s.Close(CloseTryAgainLater, "slow consumer")
				}
			}

		case cmd := <-h.kick:
			for s := range clients {
				if s.AccountID() == cmd.accountID {
					remove(s)
					s.Close(cmd.code, cmd.reason)
				}
			}
		}
	}
}

// Register adds a connection to a room. It returns ErrTooManyConnections when a
// cap is hit and ErrHubClosed once the hub has stopped.
func (h *Hub) Register(ctx context.Context, s Sink, room string) error {
	errc := make(chan error, 1)
	select {
	case h.register <- registerCmd{sink: s, room: room, err: errc}:
	case <-h.done:
		return ErrHubClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-errc:
		return err
	case <-h.done:
		return ErrHubClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unregister removes a connection. It is safe to call for a connection that was
// never registered, and never blocks for long.
func (h *Hub) Unregister(s Sink) {
	select {
	case h.unregister <- s:
	case <-h.done:
	}
}

// Publish fans a message out to a room. It does not block on slow clients.
func (h *Hub) Publish(ctx context.Context, room string, msg []byte) error {
	select {
	case h.publish <- publishCmd{room: room, msg: msg}:
		return nil
	case <-h.done:
		return ErrHubClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// KickAccount closes every connection belonging to an account. It is called
// when an admin blocks someone: the app revokes sessions immediately, and a
// live socket must not outlive that. The periodic revalidation sweep is the
// backstop for the cases this cannot see — a session expiring on its own, or a
// block applied directly in the database.
func (h *Hub) KickAccount(accountID string) {
	select {
	case h.kick <- kickCmd{accountID: accountID, code: CloseUnauthorized, reason: "unauthorized"}:
	case <-h.done:
	}
}
