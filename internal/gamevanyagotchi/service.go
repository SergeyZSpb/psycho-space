package gamevanyagotchi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/crypto"
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
	// PublishTo sends a message to a single connection. An unknown connection
	// id is a no-op rather than an error — the socket may have gone away between
	// the frame that named it and the reply.
	PublishTo(ctx context.Context, connID string, msg []byte) error
	// Members reports who is connected to a room.
	Members(ctx context.Context, room string) ([]realtime.Member, error)
}

// How an account is named on the wire. See (*Service).pseudonym.
const (
	// pseudonymKeyBytes is the size of the per-process HMAC key — 32, matching
	// every other HMAC key in this project.
	pseudonymKeyBytes = 32
	// pseudonymChars is how much of the digest is published. Twelve base64url
	// characters is 72 bits: far more than enough to keep a yard's worth of
	// entities distinct, and short enough to stay readable in a log line or a
	// devtools frame list.
	pseudonymChars = 12
)

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

	// pseudonymKey turns an account id into the handle that goes on the wire.
	// Read-only after construction, so it needs no lock. See pseudonym.
	pseudonymKey []byte

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
	//
	// Keyed by ACCOUNT id, not by connection id. One account is one Ваня however
	// many devices it is signed in on: keying by connection made a second device
	// a second dot, standing somewhere else, moving on its own.
	mu  sync.Mutex
	pos map[string]Point
}

// NewService builds the game's realtime service. room is the transport room it
// publishes to and accepts frames from; the caller supplies it so the game does
// not hardcode a name the platform's allowlist also owns.
func NewService(transport Transport, room string) *Service {
	key := make([]byte, pseudonymKeyBytes)
	// crypto/rand, never math/rand: this key is the only thing standing between
	// a broadcast handle and the account id behind it, so a guessable one would
	// defeat the pseudonym entirely. Since Go 1.24 crypto/rand.Read cannot fail
	// — it panics internally rather than returning an error — so there is no
	// error path here to thread back to main.
	_, _ = rand.Read(key)
	return &Service{
		transport:    transport,
		room:         room,
		pseudonymKey: key,
		pos:          make(map[string]Point),
	}
}

// pseudonym is the handle an account is known by on the wire:
// HMAC-SHA256(processKey, accountID), base64url, truncated to pseudonymChars.
//
// The key is pseudonymKeyBytes of crypto/rand minted once in NewService, held
// only in memory and never written anywhere — so a pseudonym is stable for the
// life of the process, identical across every connection of that account, and
// meaningless outside that process.
//
// That lifetime is chosen rather than inherited. Presence in this game is
// already in-memory-only and already meaningless after a restart: positions are
// dropped when the last connection goes, and every one of them is wrong the
// moment the binary is replaced. A key with exactly that lifetime is therefore
// the honest one — it needs no configuration, cannot be rotated wrongly, cannot
// be lost, and leaks nothing across restarts, because there is nothing on either
// side of a restart to correlate. A key from config would quietly reintroduce
// what putting accounts.id on the wire would have done outright: a durable
// per-person identifier broadcast to every other player.
//
// Not memoised on purpose. This is one HMAC over a UUID per entity per tick, at
// five ticks a second, for a yard holding a few dozen people at the very most. A
// cache would be a second map to prune in step with pos — new state to get wrong
// — bought for a cost that does not register.
func (s *Service) pseudonym(accountID string) string {
	sum := crypto.HMACSHA256(s.pseudonymKey, []byte(accountID))
	return base64.RawURLEncoding.EncodeToString(sum)[:pseudonymChars]
}

// HandleInbound implements realtime.Handler.
//
// A move never gets a reply: the sender learns the outcome the same way everyone
// else does, from the next roster, which is what stops a client from believing a
// move the server rejected. A hello is the one exception, and it is a question
// rather than a claim — see replyWhoAmI.
//
// Identity comes from the connection the hub bound at upgrade, so a frame cannot
// claim to be somebody else; nothing in the payload is trusted beyond two
// numbers.
func (s *Service) HandleInbound(ctx context.Context, m realtime.Member, room string, payload []byte) {
	if room != s.room {
		return
	}
	// The discriminator is read here and then again inside parseInbound. That is
	// deliberate: it keeps parseInbound a pure function of a whole frame, so
	// every rejection case stays a table row in a unit test instead of something
	// that needs a socket to reach. The second decode is a few hundred bytes at
	// a rate the read pump has already capped at 10/s.
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return // dropped in silence — see below
	}

	switch env.T {
	case TypeHello:
		s.replyWhoAmI(ctx, m)
	case TypeMove:
		p, err := parseInbound(payload)
		if err != nil {
			// Dropped in silence on purpose. There is no reply channel for a bad
			// frame, and logging one would hand any client a log-flood lever at
			// its full 10 messages a second. The read pump's rate limit is the
			// control that matters here.
			return
		}
		s.mu.Lock()
		// By account, so all of that account's devices drive the one Ваня.
		s.pos[m.AccountID] = p
		s.mu.Unlock()
	}
	// Anything else is a type this server does not know: a client newer than it,
	// or another feature sharing the room. Both ends ignore what they do not
	// recognise, which is what lets either end learn a message first.
}

// replyWhoAmI answers a client's hello with the pseudonym of its own account, on
// that connection alone.
//
// Note what this does NOT need: a join or leave hook on the transport. The hello
// arrives through the ordinary inbound path, which already carries the Member
// the hub bound at upgrade — so the answer is derivable from the question, and
// realtime keeps the two seams it has (ADR-033) instead of growing a lifecycle
// callback whose bookkeeping would have to be kept in step with the hub's own.
// Pull-not-push for presence, and question-not-notification for identity, are
// the same choice made twice.
//
// It is also why the answer cannot be spoofed: it is addressed to the connection
// the frame arrived on, describes that connection's account, and reads nothing
// from the payload at all.
func (s *Service) replyWhoAmI(ctx context.Context, m realtime.Member) {
	frame, err := json.Marshal(You{T: TypeYou, ID: s.pseudonym(m.AccountID)})
	if err != nil {
		return
	}
	// A failure here means the hub is shutting down, or the socket went away
	// between the hello arriving and this reply being composed. Neither is worth
	// a log line on a path a client can drive ten times a second, and the
	// client's own remedy — ask again on its next connection — already covers it.
	_ = s.transport.PublishTo(ctx, m.ConnID, frame)
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
	// Rebuilt from the hub's member list rather than trimmed in place, so the
	// position of an account with no connections left is dropped by
	// construction. There is no leave event to miss and no bookkeeping that can
	// drift from the hub.
	//
	// Keyed by account, and that keying IS the deduplication: the hub allows an
	// account three connections and reports each of them as its own Member, but
	// all three describe one Ваня standing in one place. The `seen` skip below is
	// what stops a second device from arriving as a second dot — and, because the
	// map is rebuilt from members every tick, an account keeps its position for
	// as long as ANY of its connections survives and loses it when the last one
	// goes.
	next := make(map[string]Point, len(members))
	for _, m := range members {
		if _, seen := next[m.AccountID]; seen {
			continue
		}
		p, ok := s.pos[m.AccountID]
		if !ok {
			p = spawn
		}
		next[m.AccountID] = p
		peers = append(peers, Peer{ID: s.pseudonym(m.AccountID), X: p.X, Y: p.Y})
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
