package gamevanyadum

import (
	"context"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/google/uuid"
)

// MaxArenas is how many runs this process will simulate at once.
//
// One arena is one twenty-hertz simulation of a small level, which is cheap —
// but this game shares a single box with everything else, and an unbounded map
// of them is an unbounded amount of work per tick. The number is deliberately
// far above the size of the audience: it is a backstop against a bug that leaks
// arenas, not a queue anybody is expected to reach.
const MaxArenas = 32

// AbandonGrace is how long an arena survives with nobody connected to it.
//
// It is not a disconnect timeout: a page reload, a tunnel, a phone locking and
// unlocking all take a few seconds, and dropping the run for any of them would
// make the game unplayable on a bus. What it protects against is the arena
// nobody ever comes back to, which would otherwise simulate an empty level until
// the process restarted.
//
// An abandoned run is dropped WITHOUT being written. A run somebody walked away
// from is not a result, and recording it would put rows nobody made into the
// only table this game has.
const AbandonGrace = 2 * time.Minute

// RecentRunsLimit bounds the "твои забеги" list on the splash screen.
const RecentRunsLimit = 5

// Transport is the narrow slice of the realtime hub this game uses. It is an
// interface so the tests drive the whole service without a socket, and it is
// deliberately two methods wide: this game unicasts to its own players and never
// broadcasts, because an arena is not a room.
type Transport interface {
	// PublishTo sends to a single connection. An unknown connection id is a
	// no-op rather than an error — a socket may go away between the tick that
	// named it and the write.
	PublishTo(ctx context.Context, connID string, msg []byte) error
	// Members reports who is connected to a room.
	Members(ctx context.Context, room string) ([]realtime.Member, error)
}

// Service owns every live arena and the simulation loop that advances them.
type Service struct {
	transport Transport
	room      string
	q         db.DBTX
	repo      Repository

	mu     sync.Mutex
	arenas map[string]*Arena
	// lastSeen is when each account last had a connection in the room. It is
	// what AbandonGrace is measured against, and it is kept beside the arena
	// rather than on it because it is a fact about the socket rather than about
	// the world.
	lastSeen map[string]time.Time

	// saves carries finished runs to the writer goroutine, so the tick never
	// waits on Postgres. A full channel drops the run and says so in the log:
	// stalling twenty-hertz simulation for everybody to record one summary is
	// the wrong trade, and the log line is what stops the loss being silent.
	saves chan Run
	done  chan struct{}

	// pseudonymKey turns an account into the handle other players see. Minted
	// from crypto/rand at startup and held only in memory, so a pseudonym is
	// stable for one process and meaningless after a restart — a snapshot is
	// fanned out to everybody in an arena, so anything in that field is a
	// handle every other player could record.
	pseudonymKey []byte
}

// pseudonymBytes is how much of the HMAC is published. Twelve base64url
// characters is more than enough to be unique among the handful of people who
// will ever share an arena, and short enough to cost nothing on a frame.
const pseudonymBytes = 9

// pseudonym is what other players are told an account is called.
func (s *Service) pseudonym(accountID string) string {
	mac := hmac.New(sha256.New, s.pseudonymKey)
	_, _ = mac.Write([]byte(accountID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:pseudonymBytes])
}

const savesBuffer = 64

// NewService builds the game.
func NewService(transport Transport, room string, q db.DBTX, repo Repository) *Service {
	return &Service{
		transport: transport,
		room:      room,
		q:         q,
		repo:      repo,
		arenas:    make(map[string]*Arena),
		lastSeen:  make(map[string]time.Time),
		saves:     make(chan Run, savesBuffer),
		done:      make(chan struct{}),
		// Since Go 1.24 crypto/rand.Read cannot fail — it panics internally
		// rather than returning an error — so there is no error path here to
		// thread back to main.
		pseudonymKey: randomKey(),
	}
}

func randomKey() []byte {
	k := make([]byte, 32)
	_, _ = crand.Read(k)
	return k
}

// Done is closed once Run has returned and the last write has been attempted, so
// the composition root can wait for it during shutdown.
func (s *Service) Done() <-chan struct{} { return s.done }

// Config is the served catalogue.
func (s *Service) Config() Config { return BuildConfig() }

// Run advances every arena on the injected tick.
//
// The tick is a parameter rather than a ticker built here (ADR-034's rule,
// applied again): main passes a time.Ticker, and every test passes a channel it
// fires by hand — which is what keeps the simulation tests free of sleeps and
// makes "advance exactly one hundred steps" a thing a test can say.
func (s *Service) Run(ctx context.Context, tick <-chan time.Time) {
	defer close(s.done)
	go s.persistRuns(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick:
			s.step(ctx, now)
		}
	}
}

// step is one simulation tick across every arena.
//
// The publishing is deliberately done AFTER the lock is released. PublishTo hands
// the message to the hub's own goroutine, and holding this mutex across that
// would couple every arena's simulation to the hub's queue — so the locked
// section only ever builds bytes.
func (s *Service) step(ctx context.Context, now time.Time) {
	conns := s.connsByAccount(ctx)

	type outbound struct {
		connID string
		msg    []byte
	}
	var out []outbound

	s.mu.Lock()
	for account, a := range s.arenas {
		here := conns[account]
		if len(here) > 0 {
			s.lastSeen[account] = now
		}

		a.Advance(SimStep.Seconds(), now)

		if a.Ended {
			owner := a.Owner()
			bag := map[string]int{}
			if owner != nil {
				bag = owner.State.Counters
			}
			over, err := json.Marshal(Over{
				T:       TypeOver,
				Success: a.Success,
				Seconds: a.Elapsed(now),
				Bag:     bag,
			})
			if err == nil {
				for _, c := range here {
					out = append(out, outbound{c, over})
				}
			}
			s.finish(a, now)
			delete(s.arenas, account)
			delete(s.lastSeen, account)
			continue
		}

		if len(here) == 0 {
			if seen, ok := s.lastSeen[account]; ok && now.Sub(seen) > AbandonGrace {
				// Dropped, never written — see AbandonGrace.
				delete(s.arenas, account)
				delete(s.lastSeen, account)
			}
			continue
		}

		// One snapshot PER OCCUPANT, not one per arena: a frame names everybody
		// except its reader, so two people in one arena get two different
		// frames. With one occupant this is the same single marshal it always
		// was; with two it is already correct.
		frame, ok := a.SnapshotFor(account)
		if !ok {
			continue
		}
		msg, err := json.Marshal(frame)
		if err != nil {
			continue
		}
		for _, c := range here {
			out = append(out, outbound{c, msg})
		}
	}
	s.mu.Unlock()

	for _, o := range out {
		if err := s.transport.PublishTo(ctx, o.connID, o.msg); err != nil {
			if errors.Is(err, realtime.ErrHubClosed) || errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}

// connsByAccount groups the room's members by the account behind them, so a
// player with the game open on two devices sees the same arena on both.
func (s *Service) connsByAccount(ctx context.Context) map[string][]string {
	members, err := s.transport.Members(ctx, s.room)
	if err != nil {
		return nil
	}
	out := make(map[string][]string, len(members))
	for _, m := range members {
		out[m.AccountID] = append(out[m.AccountID], m.ConnID)
	}
	return out
}

// finish queues a completed run for writing. It is called with the lock held.
func (s *Service) finish(a *Arena, now time.Time) {
	accountID, err := uuid.Parse(a.AccountID)
	if err != nil {
		return
	}
	beer := 0
	if owner := a.Owner(); owner != nil {
		beer = owner.State.Counters["beer"]
	}
	r := Run{
		ID:        a.RunID,
		AccountID: accountID,
		Seed:      a.Level.Seed,
		Success:   a.Success,
		Seconds:   a.Elapsed(now),
		Beer:      beer,
	}
	select {
	case s.saves <- r:
	default:
		slog.Warn("gamevanyadum: run dropped, save queue full", "run_id", r.ID)
	}
}

// persistRuns is the one writer. It owns every INSERT this game makes, which is
// what lets the simulation loop above promise never to touch Postgres.
func (s *Service) persistRuns(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Drain what is already queued before going away — a deploy in the
			// middle of somebody's last beer should still record the run. The
			// context is gone, so the writes get a fresh short-lived one.
			for {
				select {
				case r := <-s.saves:
					s.writeRun(context.WithoutCancel(ctx), r)
				default:
					return
				}
			}
		case r := <-s.saves:
			s.writeRun(ctx, r)
		}
	}
}

func (s *Service) writeRun(ctx context.Context, r Run) {
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.repo.InsertRun(wctx, s.q, r); err != nil {
		slog.ErrorContext(ctx, "gamevanyadum: insert run", "err", err, "run_id", r.ID)
	}
}

// StartRun begins a run for an account and returns its arena.
//
// The seed comes from crypto/rand rather than from the clock or from a counter.
// It is not a security value, but a clock-seeded level means two people starting
// in the same second play the same заброшка, and a counter means the levels
// arrive in a fixed order — both of which are worse than free randomness.
func (s *Service) StartRun(accountID uuid.UUID, now time.Time) (*Arena, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := accountID.String()
	if _, ok := s.arenas[key]; ok {
		return nil, ErrRunInProgress
	}
	if len(s.arenas) >= MaxArenas {
		return nil, ErrTooManyArenas
	}

	var b [8]byte
	// Since Go 1.24 crypto/rand.Read cannot fail — it panics internally rather
	// than returning an error — so there is no error path to thread out.
	_, _ = crand.Read(b[:])
	// Masked to sixty-three bits so the seed is always non-negative. It loses one
	// bit of a value that only has to be unlikely to repeat, and it buys a seed
	// that reads sensibly in a database row and in a URL somebody pastes into a
	// chat to say "play this level".
	seed := int64(binary.LittleEndian.Uint64(b[:]) & math.MaxInt64)

	a := NewArena(uuid.New(), key, s.pseudonym(key), seed, now)
	s.arenas[key] = a
	s.lastSeen[key] = now
	return a, nil
}

// AbandonRun throws away an account's run without recording it. It is what the
// «сдаться» button does, and it is the only way to start a second run before the
// first one has ended.
func (s *Service) AbandonRun(accountID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := accountID.String()
	delete(s.arenas, key)
	delete(s.lastSeen, key)
}

// CurrentRun returns the arena an account is playing, if any.
func (s *Service) CurrentRun(accountID uuid.UUID) (*Arena, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.arenas[accountID.String()]
	return a, ok
}

// RecentRuns reads an account's own last runs.
func (s *Service) RecentRuns(ctx context.Context, accountID uuid.UUID) ([]Run, error) {
	return s.repo.RecentRuns(ctx, s.q, accountID, RecentRunsLimit)
}

// HandleInbound implements realtime.Handler. It runs on the connection's own
// read pump, after the socket's rate check, so it must not block — which is why
// input is queued for the tick rather than simulated here.
func (s *Service) HandleInbound(ctx context.Context, m realtime.Member, room string, payload []byte) {
	if room != s.room {
		return
	}
	t, in := ParseInbound(payload)
	switch t {
	case TypeHello:
		s.hello(ctx, m)
	case TypeInput:
		s.mu.Lock()
		if a, ok := s.arenas[m.AccountID]; ok {
			a.Enqueue(m.AccountID, in)
		}
		s.mu.Unlock()
	default:
		// Malformed, unknown or invalid: no reply and no log line. A log per bad
		// frame at the permitted ten a second is a flood lever handed to any
		// client.
	}
}

// hello attaches a connection to whatever run the account already started over
// HTTP, and tells it which one that is.
//
// A hello with no run gets silence. It is not an error state: it is a socket
// that opened before the run did, or one that outlived it, and the client's own
// next move (starting a run) is what fixes it.
func (s *Service) hello(ctx context.Context, m realtime.Member) {
	s.mu.Lock()
	a, ok := s.arenas[m.AccountID]
	var runID string
	if ok {
		runID = a.RunID.String()
		s.lastSeen[m.AccountID] = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	msg, err := json.Marshal(Ready{T: TypeReady, RunID: runID})
	if err != nil {
		return
	}
	_ = s.transport.PublishTo(ctx, m.ConnID, msg)
}
