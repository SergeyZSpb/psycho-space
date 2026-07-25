package realtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"
)

// Per-connection bounds. The global 1 MiB body limit does NOT apply here: it
// wraps r.Body, which the hijack bypasses, so SetReadLimit is the only control
// over frame size.
const (
	// MaxFrameBytes caps an inbound frame. A presence message is a few hundred
	// bytes; the library's default is 32 KiB.
	MaxFrameBytes = 4096
	// msgPerSecond and msgBurst bound the inbound message rate. The HTTP rate
	// limiter fires once, at the handshake — after the upgrade there is no
	// request left for it to count.
	msgPerSecond = 10
	msgBurst     = 20
	// pingInterval detects half-open TCP on mobile networks. nginx's read
	// timeout is raised well past this; the ping is for the network, not nginx.
	pingInterval = 25 * time.Second
	// writeTimeout bounds one message write, so a reader that has stopped
	// reading cannot pin a goroutine and a send buffer indefinitely.
	writeTimeout = 5 * time.Second
	// byeGrace is how long the write pump waits for a close reason after its
	// context is cancelled. Shutdown cancels the hub context and only then walks
	// its clients calling Close, so without this window the pump would exit on
	// the cancellation and the client would never learn why. Small enough that
	// every connection still drains well inside the 5 s budget in main.
	byeGrace = 250 * time.Millisecond
)

// closeReason is a pending close request handed from Close to the write pump.
type closeReason struct {
	code   int
	reason string
}

// Conn adapts a WebSocket to the hub's Sink and runs the two pumps that feed
// it. One Conn owns exactly one socket.
type Conn struct {
	id        string
	accountID string
	room      string
	handler   Handler // nil: inbound frames are read for their bounds and discarded
	ws        *websocket.Conn
	send      chan []byte

	// closing carries at most one close request to the write pump. Capacity 1
	// plus closeOnce is what makes Close non-blocking without a lossy default.
	closing   chan closeReason
	closeOnce sync.Once
	hardOnce  sync.Once
}

// NewConn wraps an accepted WebSocket. The room is the one this connection is
// about to be registered into, carried here so an inbound frame can say where it
// came from. A nil handler is valid and means nobody is listening: the frames
// are still read, so the read limit and the rate limit still apply, and the
// payload is discarded.
func NewConn(id, accountID, room string, ws *websocket.Conn, handler Handler) *Conn {
	ws.SetReadLimit(MaxFrameBytes)
	return &Conn{
		id:        id,
		accountID: accountID,
		room:      room,
		handler:   handler,
		ws:        ws,
		send:      make(chan []byte, defaultSendBuffer),
		closing:   make(chan closeReason, 1),
	}
}

// Room is the room this connection was opened against.
func (c *Conn) Room() string { return c.room }

// ID implements Sink.
func (c *Conn) ID() string { return c.id }

// AccountID implements Sink.
func (c *Conn) AccountID() string { return c.accountID }

// TrySend implements Sink: it queues without blocking and reports false when
// the client is behind.
func (c *Conn) TrySend(msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// Close implements Sink. It asks the write pump to tell the client why the
// socket is going away and then tear it down. Repeated calls are no-ops, so the
// hub and the pumps may all call it, and it never blocks — the channel has
// capacity 1 and closeOnce guarantees a single send.
//
// The reason travels as an ordinary text frame (a Bye), not as a WebSocket close
// code, and that is a deliberate trade rather than an oversight. The library's
// Conn.Close is the only public way to emit a close code, and it runs a full
// close handshake: a 5 s write, then a 5 s wait for the peer's reply which needs
// the read lock our own read pump already holds while blocked in Read, then a
// join with a 15 s timer. That is seconds of stall on paths that must never
// stall — the single hub goroutine, and a shutdown drain budgeted at 5 s for
// every connection at once. The unexported writeClose, which would emit the code
// without waiting, is not reachable from here.
//
// So the transport close stays abrupt (a browser sees 1006) and the *reason*
// arrives in the frame immediately before it. Nothing safety-critical rests on
// that frame being delivered: blocking an account also revokes its sessions, so
// its reconnect is refused by requireAuth with a 401/403 before any upgrade, and
// that HTTP status — not this frame — is the client's terminal signal.
func (c *Conn) Close(code int, reason string) {
	c.closeOnce.Do(func() {
		c.closing <- closeReason{code: code, reason: reason}
	})
}

// hardClose drops the socket without a handshake. Idempotent, and safe to call
// from any goroutine: every caller is on a path that must not block.
func (c *Conn) hardClose() {
	c.hardOnce.Do(func() {
		_ = c.ws.CloseNow()
	})
}

// writeBye makes a best-effort attempt to deliver the reason before the socket
// drops. It deliberately does not use the pump's context: by the time shutdown
// asks for a close that context is already cancelled, and a write on it would
// fail instantly, which is the bug this exists to prevent.
func (c *Conn) writeBye(ctx context.Context, cr closeReason) {
	frame := encodeBye(cr.code, cr.reason)
	if frame == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	// A failure here means the peer is already gone — there is nobody left to
	// tell, and hardClose is about to run regardless.
	_ = c.ws.Write(writeCtx, websocket.MessageText, frame)
}

// Serve runs the read and write pumps until either ends, then unregisters from
// the hub. It blocks, and it returns only once both pumps have exited — so the
// HTTP handler returning implies no goroutine is left behind.
//
// The context must NOT be the request's: coder/websocket documents the request
// context as undefined after Accept, and it is cancelled as soon as the handler
// returns, which is immediately after the upgrade.
func (c *Conn) Serve(ctx context.Context, hub *Hub) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer hub.Unregister(c)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.readPump(ctx)
		// The peer is gone, or misbehaved. Ask the write pump to wind up rather
		// than cancelling its context: cancellation makes it pay the bye grace
		// window for a socket that has nobody left to tell, and the request path
		// is a no-op if the hub already asked first.
		c.Close(CloseGoingAway, "peer closed")
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		c.writePump(ctx)
	}()
	wg.Wait()
	// Both pumps are gone, so nothing can write a Bye any more; just make sure
	// the socket is not left open.
	c.hardClose()
}

// readPump drains inbound frames, enforces the per-connection bounds, and hands
// what survives to the handler. Oversized frames are rejected by SetReadLimit,
// and a client that exceeds the rate limit is disconnected.
//
// The rate check runs BEFORE the handler, so a flood costs the handler nothing —
// the bound the socket already had is the bound the game inherits, and no game
// has to remember to rate-limit itself. Binary frames are dropped: every
// protocol here is JSON text, and accepting both would mean two decode paths
// where one is never exercised.
//
// Identity comes from the connection, never from the payload. A message cannot
// claim to be from another account because HandleInbound is not given anything
// the payload could influence.
//
// Reads deliberately run on a context that is never cancelled. The library
// installs a context.AfterFunc on the read context that closes the whole
// connection when it fires (setupReadTimeout), so handing it a context that
// shutdown cancels would tear the socket down before the write pump could send
// the Bye explaining why — the most common disconnect in production, silently
// degraded to a network error. The loop still always terminates: every exit path
// out of Serve runs hardClose, and that makes this Read fail.
func (c *Conn) readPump(ctx context.Context) {
	readCtx := context.WithoutCancel(ctx)
	limiter := rate.NewLimiter(rate.Limit(msgPerSecond), msgBurst)
	for {
		typ, payload, err := c.ws.Read(readCtx)
		if err != nil {
			// Peer closed, deadline, read limit exceeded, or ctx cancelled.
			// Every one of these means this connection is finished.
			return
		}
		if !limiter.Allow() {
			slog.WarnContext(ctx, "realtime rate limit exceeded",
				"conn_id", c.id, "account_id", c.accountID)
			c.Close(CloseTryAgainLater, "rate limited")
			return
		}
		if c.handler == nil || typ != websocket.MessageText {
			continue
		}
		// On this goroutine on purpose: one read pump per connection, so a
		// handler that is slow delays only the client that sent the frame, and
		// never the hub goroutine that every room depends on.
		c.handler.HandleInbound(readCtx, Member{ConnID: c.id, AccountID: c.accountID}, c.room, payload)
	}
}

// writePump owns every write to the socket. Nothing else may write, which is
// what makes concurrent broadcast safe without a per-connection mutex.
func (c *Conn) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	defer c.hardClose()

	for {
		// A pending close outranks queued messages. Without this the drain could
		// end up waiting behind as many as defaultSendBuffer writes, each bounded
		// only by writeTimeout — many times the whole shutdown budget.
		select {
		case cr := <-c.closing:
			c.writeBye(ctx, cr)
			return
		default:
		}

		select {
		case <-ctx.Done():
			// Shutdown cancels the hub context and only then walks its clients
			// calling Close, so wait a moment for that request rather than
			// exiting and leaving the client to guess.
			select {
			case cr := <-c.closing:
				c.writeBye(ctx, cr)
			case <-time.After(byeGrace):
			}
			return
		case cr := <-c.closing:
			c.writeBye(ctx, cr)
			return
		case msg := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
