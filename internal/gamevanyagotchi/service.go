package gamevanyagotchi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
)

// BroadcastInterval is how often the plane is published: 5 Hz.
//
// Chosen against the interpolation, not against a frame rate. The client walks
// each entity to its new position with a CSS transition of about this length, so
// a slower rate would read as stepping and a faster one would spend bandwidth on
// motion the transition is already inventing. It is a package constant rather
// than a knob because it is half of a two-part decision — the other half is a
// duration in a stylesheet, and letting the two drift is the bug.
const BroadcastInterval = 200 * time.Millisecond

// Transport is what this game needs from the realtime hub, and nothing more.
// Declared here rather than importing the hub concretely so the dependency
// points at infrastructure and the service can be tested with a fake — the same
// shape gamekhimki uses for the shared asset store.
type Transport interface {
	// Publish fans a message out to everyone in a room, without blocking on a
	// slow client.
	Publish(ctx context.Context, room string, msg []byte) error
	// Members reports who is connected to a room.
	Members(ctx context.Context, room string) ([]realtime.Member, error)
}

// Service owns the shared plane: where everybody is standing, and telling
// everybody about it.
//
// Positions live in memory and nowhere else, which is not a shortcut. A position
// is presence — it is meaningless once the socket is gone, and a stored one
// would keep asserting something untrue after a restart. Anything durable in
// this game (a pet, a stat, a claimed key) is Postgres's job and arrives in
// Phase 2.
type Service struct {
	transport Transport
	room      string

	// mu guards pos, which is written from every connection's read pump and
	// read by the broadcast loop.
	//
	// A mutex rather than the hub's own owner-goroutine pattern, deliberately.
	// The hub needs that pattern because it fans out to every client and must
	// never wait; this critical section is two map operations over at most a few
	// hundred entries, and no I/O ever happens under the lock — the frame is
	// marshalled and published after it is released. A channel and a goroutine
	// here would add a queue that can fill, to remove a lock that is never
	// contended for long enough to measure.
	mu  sync.Mutex
	pos map[string]Point
}

// NewService builds the game's realtime service. room is the transport room it
// publishes to and accepts frames from; the caller supplies it so the game does
// not hardcode a name the platform's allowlist also owns.
func NewService(transport Transport, room string) *Service {
	return &Service{
		transport: transport,
		room:      room,
		pos:       make(map[string]Point),
	}
}

// HandleInbound implements realtime.Handler.
//
// It never replies. The sender learns the outcome the same way everyone else
// does — from the next roster — which is what stops the client from believing a
// move the server rejected. Identity comes from the connection the hub bound at
// upgrade, so a frame cannot claim to be somebody else; nothing in the payload
// is trusted beyond two numbers.
func (s *Service) HandleInbound(ctx context.Context, m realtime.Member, room string, payload []byte) {
	if room != s.room {
		return
	}
	p, err := parseInbound(payload)
	if err != nil {
		// Dropped in silence on purpose. There is no reply channel for a bad
		// frame, and logging one would hand any client a log-flood lever at its
		// full 10 messages a second. The read pump's rate limit is the control
		// that matters here.
		return
	}
	s.mu.Lock()
	s.pos[m.ConnID] = p
	s.mu.Unlock()
}

// Run publishes the roster on every tick until ctx is cancelled. It blocks; call
// it in its own goroutine.
//
// The tick is injected rather than owned. In production it is a ticker created
// by main; in a test it is a plain channel the test fires, which is what removes
// every "wait for the next broadcast" sleep from the suite. It is also why the
// rate is not a constant in here.
//
// This is a RENDER tick, and it is not the timer the design rules out. It writes
// nothing, owns nothing and decides nothing: it reads the hub's current members
// and sends a snapshot. A tick that is late, early, skipped or duplicated
// produces the same correct frame, because the frame is full state rather than a
// step forward from the last one.
func (s *Service) Run(ctx context.Context, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			if err := s.broadcast(ctx); err != nil {
				if errors.Is(err, realtime.ErrHubClosed) || errors.Is(err, context.Canceled) {
					return
				}
				slog.WarnContext(ctx, "gamevanyagotchi: broadcast failed", "err", err)
			}
		}
	}
}

// broadcast sends one snapshot of the plane.
func (s *Service) broadcast(ctx context.Context) error {
	members, err := s.transport.Members(ctx, s.room)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		// Nobody to tell. Also the only moment stale positions are cleared
		// wholesale, which is fine: the rebuild below prunes continuously.
		s.mu.Lock()
		clear(s.pos)
		s.mu.Unlock()
		return nil
	}

	peers := make([]Peer, 0, len(members))
	s.mu.Lock()
	// Rebuilt from the hub's member list rather than trimmed in place, so a
	// position whose connection has gone is dropped by construction. There is no
	// leave event to miss and no bookkeeping that can drift from the hub.
	next := make(map[string]Point, len(members))
	for _, m := range members {
		p, ok := s.pos[m.ConnID]
		if !ok {
			p = spawn
		}
		next[m.ConnID] = p
		peers = append(peers, Peer{ID: m.ConnID, X: p.X, Y: p.Y})
	}
	s.pos = next
	s.mu.Unlock()

	// Marshalled and published outside the lock: a slow publish must not hold up
	// the read pumps writing moves.
	frame, err := json.Marshal(Roster{T: TypeRoster, Peers: peers})
	if err != nil {
		return err
	}
	return s.transport.Publish(ctx, s.room, frame)
}
