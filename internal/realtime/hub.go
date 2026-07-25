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
// stated trigger for revisiting in the decision records (docs/ARCHITECTURE.md
// section 8).
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

// Member is what the hub will tell a caller about one connection: an identity
// and nothing else. It is deliberately a value type rather than the Sink, so a
// domain service can ask who is present without acquiring the ability to write
// to, or close, somebody's socket.
//
// The identity is the CONNECTION, not the account. Three tabs are three members,
// and ConnID is a per-connection UUID that means nothing after the socket goes
// away — the right lifetime for presence, and the id to use for anything
// addressed at one socket (Hub.PublishTo).
//
// AccountID is the durable one, and what a service does with it is that
// service's decision: collapsing an account's connections into one entity needs
// it, and putting it on a frame that is broadcast to other people would hand out
// a cross-session identifier. Nothing here forces either — the hub carries bytes
// and decides nothing.
type Member struct {
	ConnID    string
	AccountID string
}

// Handler receives frames that clients sent. It is how a domain service reads a
// socket: the hub itself never interprets a payload, it only decides who is
// connected and where a message goes.
//
// It stays free of any game's vocabulary — no message names, no verbs — because
// this package must not learn that a game exists. What the bytes mean is the
// handler's business.
//
// HandleInbound is called on the connection's own read-pump goroutine, so it
// runs concurrently across connections and MUST NOT BLOCK: a handler that waits
// stalls that client's reads, and the socket is already rate-limited on the
// assumption that reading is cheap. It is deliberately not called on the hub
// goroutine, which owns every room and must never wait for anything.
type Handler interface {
	HandleInbound(ctx context.Context, m Member, room string, payload []byte)
}

type client struct {
	sink      Sink
	room      string
	overflows int
}

type membersCmd struct {
	room  string
	reply chan<- []Member
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

type publishToCmd struct {
	connID string
	msg    []byte
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
	publishTo  chan publishToCmd
	kick       chan kickCmd
	members    chan membersCmd

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
		publishTo:     make(chan publishToCmd), // unbuffered on purpose — see PublishTo
		kick:          make(chan kickCmd, 8),
		members:       make(chan membersCmd),
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

	// deliver hands one message to one client without ever blocking, and applies
	// the backpressure policy when the client will not take it. Both the
	// broadcast and the unicast path go through it, so "this client is behind"
	// has one meaning in this hub rather than two that can drift apart.
	deliver := func(s Sink, c *client, msg []byte) {
		if c.sink.TrySend(msg) {
			c.overflows = 0
			return
		}
		// Never block on a slow client: one phone on a bad connection must not
		// freeze the room for everyone. Presence is broadcast as idempotent full
		// state, so a dropped frame followed by the next one leaves the client
		// correct rather than corrupt.
		c.overflows++
		if c.overflows >= maxOverflowsBeforeEvict {
			remove(s)
			s.Close(CloseTryAgainLater, ReasonSlowConsumer)
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
				deliver(s, c, cmd.msg)
			}

		case cmd := <-h.publishTo:
			// A linear scan rather than a fourth map keyed by connection id. The
			// client set is bounded by maxTotal, a unicast is rare — a client
			// sends one handshake per socket, not one per tick — and every extra
			// index is one more thing `remove` has to keep in step, which is the
			// class of bug this goroutine's sole ownership exists to rule out.
			// An id nobody holds simply matches nothing.
			for s, c := range clients {
				if s.ID() == cmd.connID {
					deliver(s, c, cmd.msg)
					break
				}
			}

		case cmd := <-h.kick:
			for s := range clients {
				if s.AccountID() == cmd.accountID {
					remove(s)
					s.Close(cmd.code, cmd.reason)
				}
			}

		case cmd := <-h.members:
			// A fresh slice every time: the maps above belong to this goroutine
			// and handing out anything that aliases them would be a data race
			// the caller could not see.
			out := make([]Member, 0, len(rooms[cmd.room]))
			for s := range rooms[cmd.room] {
				out = append(out, Member{ConnID: s.ID(), AccountID: s.AccountID()})
			}
			cmd.reply <- out
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

// PublishTo sends a message to one connection and to nobody else. It does not
// block on a slow client, exactly as Publish does not.
//
// An unknown connection id is a silent no-op rather than an error. The only
// caller that can name a connection is one holding a Member, and between the
// frame that produced that Member and the reply being composed the socket may
// already have gone — a race that is ordinary rather than a failure worth
// reporting, and one no caller could do anything useful about.
//
// It stays game-agnostic like the rest of this package: it carries bytes to a
// connection the caller already knows about and never looks inside them.
//
// The command channel is deliberately UNBUFFERED, unlike publish. A buffered one
// would go on accepting sends after shutdown — select would see both a writable
// channel and a closed done channel and pick between them at random — so a
// caller could not reliably tell "queued" from "the hub is gone". The cost is
// waiting for the hub goroutine, which never blocks on anything.
func (h *Hub) PublishTo(ctx context.Context, connID string, msg []byte) error {
	select {
	case h.publishTo <- publishToCmd{connID: connID, msg: msg}:
		return nil
	case <-h.done:
		return ErrHubClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Members reports who is currently connected to a room, in no particular order.
// An unknown or empty room yields an empty slice, not an error.
//
// This is a pull, and that is the point: a domain service that rebuilds its view
// from this on every broadcast cannot drift out of step with the hub, because it
// holds no join/leave bookkeeping of its own to get wrong. The alternative —
// pushing join and leave events at a service — would make presence a thing two
// components each believe they know, which is the bug this avoids. It also
// composes with the backpressure design: a roster built from the current member
// set is idempotent full state, so dropping a frame costs nothing.
func (h *Hub) Members(ctx context.Context, room string) ([]Member, error) {
	reply := make(chan []Member, 1)
	select {
	case h.members <- membersCmd{room: room, reply: reply}:
	case <-h.done:
		return nil, ErrHubClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case out := <-reply:
		return out, nil
	case <-h.done:
		return nil, ErrHubClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// KickAccount closes every connection belonging to an account. It is called
// when an admin blocks someone: the app revokes sessions immediately, and a
// live socket must not outlive that.
//
// This is the only revocation path that exists today. There is NO periodic
// revalidation sweep, so the two cases it cannot see — a session expiring on its
// own, and a block applied straight in the database — currently leave a socket
// alive until the peer or the process goes away. Adding that sweep is tracked as
// its own work item and belongs before anything durable rides on this transport.
func (h *Hub) KickAccount(accountID string) {
	select {
	case h.kick <- kickCmd{accountID: accountID, code: CloseUnauthorized, reason: "unauthorized"}:
	case <-h.done:
	}
}
