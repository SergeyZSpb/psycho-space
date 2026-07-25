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
)

// Conn adapts a WebSocket to the hub's Sink and runs the two pumps that feed
// it. One Conn owns exactly one socket.
type Conn struct {
	id        string
	accountID string
	ws        *websocket.Conn
	send      chan []byte

	closeOnce sync.Once
	closeCode int
	closeText string
}

// NewConn wraps an accepted WebSocket.
func NewConn(id, accountID string, ws *websocket.Conn) *Conn {
	ws.SetReadLimit(MaxFrameBytes)
	return &Conn{
		id:        id,
		accountID: accountID,
		ws:        ws,
		send:      make(chan []byte, defaultSendBuffer),
	}
}

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

// Close implements Sink. It records the reason and closes the socket; repeated
// calls are no-ops, so the hub and the pumps may both call it.
func (c *Conn) Close(code int, reason string) {
	c.closeOnce.Do(func() {
		c.closeCode, c.closeText = code, reason
		// CloseNow rather than Close: a graceful close handshake can block on a
		// peer that has stopped reading, and every caller here is on a path
		// that must not block (the hub loop, or shutdown).
		_ = c.ws.CloseNow()
	})
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
		defer cancel()
		c.readPump(ctx)
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		c.writePump(ctx)
	}()
	wg.Wait()
	c.Close(CloseGoingAway, "closed")
}

// readPump drains inbound frames. Nothing acts on them yet — the first slice
// proves transport, auth and lifetime. What it does do is enforce the bounds:
// oversized frames are rejected by SetReadLimit, and a client that exceeds the
// rate limit is disconnected.
func (c *Conn) readPump(ctx context.Context) {
	limiter := rate.NewLimiter(rate.Limit(msgPerSecond), msgBurst)
	for {
		_, _, err := c.ws.Read(ctx)
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
	}
}

// writePump owns every write to the socket. Nothing else may write, which is
// what makes concurrent broadcast safe without a per-connection mutex.
func (c *Conn) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
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
