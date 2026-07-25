package gamevanyagotchi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
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

// Service owns two halves of the game that keep deliberately different company.
//
// The shared plane — where everybody is standing, and telling everybody about it
// — lives in memory and nowhere else, and that is not a shortcut. A position is
// presence: it is meaningless once the socket is gone, and a stored one would
// keep asserting something untrue after a restart.
//
// The pet is the opposite. It outlives the process, so it is Postgres's, and it
// is read through the repository below. The two halves share a struct because
// they are one game and the plane will soon draw what the database knows, but
// they share no state: nothing on the realtime path touches the pool, and
// nothing on the pet path touches the position map.
type Service struct {
	transport Transport
	room      string

	// q and repo are the durable half. Both may be nil in a test that only
	// exercises the plane, which is safe because the two paths never meet — but
	// the composition root always supplies them.
	q    db.DBTX
	repo Repository

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
	pos map[string]placement
	// display is what the plane draws for each account — see display.go. Guarded
	// by the same lock as pos, because every tick reads both together and a
	// second lock would buy nothing but an ordering rule to get wrong.
	display map[string]display

	// done is closed when Run returns, so shutdown can WAIT for the flush.
	//
	// Without it the flush was racing process exit and losing: main cancels the
	// hub context, waits for the hub to drain, shuts the HTTP server down and
	// returns — and nothing anywhere waited for this game's loop, so the process
	// could be gone before the positions reached Postgres. It survived locally
	// and failed in CI, which is exactly the shape of bug that would otherwise
	// have been found by a deploy quietly not saving anybody's place.
	done chan struct{}

	// saves carries departures to the goroutine that writes them down. Buffered
	// and sent to WITHOUT blocking: the broadcast must never wait on Postgres,
	// and a dropped save costs a returning Ваня his last resting place, which is
	// the same thing a crash costs and is acceptable for a nap.
	saves chan positionSave
}

// positionSave is one account's last known position, on its way to being
// written down.
type positionSave struct {
	accountID string
	at        Point
	seen      time.Time
}

// savesBuffer is how many departures may be queued before the plane starts
// dropping them. A hundred is far beyond any plausible simultaneous exodus in a
// yard this size, and the alternative — an unbounded queue — would turn a
// database outage into memory growth.
const savesBuffer = 100

// flushTimeout bounds the write done on the way out of Run. Comfortably longer
// than a few dozen single-row updates against a database on the same box, and
// comfortably shorter than the shutdown budget the rest of the drain works to.
const flushTimeout = 2 * time.Second

// PositionGrace is how long an account keeps its place in the yard after its
// last connection goes away.
//
// Without it, reloading the page put you back in the middle of the yard: a
// refresh closes the socket, so for a moment the account has no connections at
// all, and a map rebuilt from the live membership dropped the position before
// the new socket arrived. The same thing happened on a tunnel, a lock screen and
// every reconnect — which is a lot of teleporting for something the player did
// not do.
//
// Two minutes because it has to outlast the longest ordinary absence the client
// itself creates: it deliberately closes the socket after sixty seconds hidden
// and reconnects on the way back. Beyond that the position is genuinely stale
// and the entry point is the honest answer.
//
// This is a hold, not durability: a deploy restarts the process and the map
// goes with it. Surviving that means writing the position down when the last
// connection leaves — which is what pets.x / pets.y / pets.last_seen_at exist
// for, and it is the slice that also makes an idle Ваня lie down and sleep
// where he stood. It is deliberately not done from the broadcast loop.
const PositionGrace = 2 * time.Minute

// placement is a position plus the last tick at which its account was actually
// connected, which is what the grace above is measured from.
type placement struct {
	at       Point
	lastSeen time.Time
	// saved records that this account's departure has already been written
	// down, so that being absent for the whole grace period costs one write
	// rather than one per tick.
	saved bool
	// provisional marks a position nobody chose: the spawn point the broadcast
	// puts a newly-seen account at so that it has somewhere to be drawn.
	//
	// It exists because of an ordering that is easy to miss. The hub registers a
	// connection at the upgrade, BEFORE the client has said hello — so a tick
	// can land in that gap and fill the map with a spawn point. Without this
	// flag, the stored position arriving a moment later would look like it was
	// racing a real one and be discarded, and a returning Ваня would silently
	// teleport to the middle exactly as he did before any of this was built.
	provisional bool
}

// NewService builds the game. room is the transport room it publishes to and
// accepts frames from; the caller supplies it so the game does not hardcode a
// name the platform's allowlist also owns. q and repo are the durable half and
// may both be nil for a caller that only drives the plane.
func NewService(transport Transport, room string, q db.DBTX, repo Repository) *Service {
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
		q:            q,
		repo:         repo,
		pseudonymKey: key,
		pos:          make(map[string]placement),
		display:      make(map[string]display),
		saves:        make(chan positionSave, savesBuffer),
		done:         make(chan struct{}),
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
		// A hello is a fresh socket, which is the human-paced moment this game
		// gets to read the database on the plane's behalf. It happens on this
		// connection's own read pump, so a slow query delays one client's next
		// frame and never the yard's.
		s.load(ctx, m.AccountID)
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
		// By account, so all of that account's devices drive the one Ваня. Only
		// the point is written: lastSeen belongs to the broadcast, which is the
		// only thing here holding a clock, and a moving player is by definition
		// connected so the next tick refreshes it anyway.
		cur := s.pos[m.AccountID]
		cur.at = p
		// Chosen by a person, so no longer the spawn default that a stored
		// position is allowed to replace.
		cur.provisional = false
		s.pos[m.AccountID] = cur
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
	// Closed last, so a caller that waits on Done knows the flush below has
	// finished rather than merely started. Run is called once, by the
	// composition root; calling it twice would close this twice and panic, which
	// is the correct answer to doing that.
	defer close(s.done)

	// One writer, owned by this loop and gone with it. Departures are written
	// here rather than on the tick because the tick must never wait on Postgres
	// — it only notices that somebody has left and says so down a channel.
	go s.persistPositions(ctx)

	for {
		select {
		case <-ctx.Done():
			// Everybody is leaving at once, and there will be no further tick to
			// notice it. Without this a deploy would put the whole yard back in
			// the middle — the exact failure durable position exists to fix, and
			// one that only ever happens in production.
			s.flushPositions()
			return
		case now := <-tick:
			// The tick carries its own timestamp, and that is the clock the
			// grace period is measured against. A ticker sends the real time; a
			// test sends whatever it likes, so the grace can be driven to expiry
			// without a Clock seam and without ever sleeping.
			if err := s.broadcast(ctx, now); err != nil {
				if errors.Is(err, realtime.ErrHubClosed) || errors.Is(err, context.Canceled) {
					return
				}
				slog.WarnContext(ctx, "gamevanyagotchi: broadcast failed", "err", err)
			}
		}
	}
}

// Done is closed once Run has returned and everybody standing in the yard has
// been written down. Shutdown waits on it — see the field's comment for what
// went wrong when nothing did.
func (s *Service) Done() <-chan struct{} { return s.done }

// broadcast sends one snapshot of the plane, as of now.
func (s *Service) broadcast(ctx context.Context, now time.Time) error {
	members, err := s.transport.Members(ctx, s.room)
	if err != nil {
		return err
	}

	// Placed even when the room is empty, because that is also when the grace
	// period has to be able to run out. An earlier version cleared the map
	// wholesale here, which is exactly what teleported the last player in the
	// yard back to the middle when they reloaded the page.
	peers := s.place(members, now)
	if len(peers) == 0 {
		// Nobody to tell. Publishing into an empty room five times a second
		// would be pure waste, and would hide a genuine "why is this room empty"
		// question behind traffic.
		return nil
	}

	// Marshalled and published outside the lock: a slow publish must not hold up
	// the read pumps writing moves.
	frame, err := json.Marshal(Roster{T: TypeRoster, Peers: peers})
	if err != nil {
		return err
	}
	return s.transport.Publish(ctx, s.room, frame)
}

// place reconciles the position map against who is actually connected and
// returns the roster to publish.
//
// Keyed by account, and that keying IS the deduplication: the hub allows an
// account three connections and reports each as its own Member, but all three
// describe one Ваня standing in one place. The `seen` skip is what stops a
// second device from arriving as a second dot.
//
// Membership decides who is IN the roster; the grace period decides how long a
// position is REMEMBERED. Those were the same thing until a reload proved they
// should not be — an absent account is not in the frame either way, so nobody
// sees a ghost, and coming back inside the window puts you where you left off
// instead of in the middle of the yard.
func (s *Service) place(members []realtime.Member, now time.Time) []Peer {
	s.mu.Lock()
	defer s.mu.Unlock()

	peers := make([]Peer, 0, len(members))
	present := make(map[string]bool, len(members))
	for _, m := range members {
		if present[m.AccountID] {
			continue
		}
		present[m.AccountID] = true

		p, ok := s.pos[m.AccountID]
		if !ok {
			// Somewhere to be drawn until we know better. Marked provisional so
			// the stored position, which arrives on this connection's hello a
			// moment later, is allowed to replace it.
			p.at = spawn
			p.provisional = true
		}
		p.lastSeen = now
		// Back in the yard, so a later departure is a new one to write down.
		p.saved = false
		s.pos[m.AccountID] = p

		// Appearance comes from the cache, never from a query — see display.go.
		// An account with nothing cached yet (a client that has not said hello,
		// or one whose pet has never been read) still draws, as the catalogue's
		// default skin with no name and no trouble. Rendering nothing at all
		// would make a player invisible to the yard for the sake of a label.
		d := s.display[m.AccountID]
		peers = append(peers, Peer{
			ID:    s.pseudonym(m.AccountID),
			X:     p.at.X,
			Y:     p.at.Y,
			Art:   d.skin(),
			Label: d.name,
			Pose:  d.pose(now),
		})
	}

	// Whoever has just gone gets written down, once. `saved` is what makes it
	// once: absence is observed on every tick until the grace expires, and
	// queueing a write five times a second for somebody who has left would be
	// the per-tick database traffic this design does not have.
	for id, p := range s.pos {
		if present[id] {
			continue
		}
		if !p.saved && !p.provisional {
			p.saved = true
			s.pos[id] = p
			s.enqueueSave(positionSave{accountID: id, at: p.at, seen: p.lastSeen})
		}
		// Forget whoever has been gone longer than the grace. Deleting during a
		// range over a map is defined behaviour in Go, and doing it here rather
		// than by rebuilding the map is what lets an absent account keep its
		// entry: there is still no leave event to miss, because absence is
		// inferred from the membership the hub reports rather than from a
		// notification.
		if now.Sub(p.lastSeen) >= PositionGrace {
			delete(s.pos, id)
			delete(s.display, id)
		}
	}
	return peers
}

// enqueueSave hands a departure to the writer without ever blocking.
//
// A full queue means Postgres is not keeping up with people leaving, which at
// this scale means Postgres is in trouble generally. Dropping is the right
// answer: the plane must keep running, and the cost is that one Ваня reappears
// where he was last written rather than where he last stood.
func (s *Service) enqueueSave(sv positionSave) {
	if s.repo == nil || s.q == nil {
		return
	}
	select {
	case s.saves <- sv:
	default:
		slog.Warn("gamevanyagotchi: position save dropped, queue full")
	}
}

// persistPositions writes departures down until the game stops.
func (s *Service) persistPositions(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case sv := <-s.saves:
			s.savePosition(ctx, sv)
		}
	}
}

// savePosition writes one position, logging rather than propagating a failure —
// there is nobody to propagate it to, and losing a resting place is not worth
// stopping the game for.
func (s *Service) savePosition(ctx context.Context, sv positionSave) {
	if err := s.repo.SavePosition(ctx, s.q, sv.accountID, sv.at, sv.seen); err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: saving a position failed", "err", err)
	}
}

// flushPositions writes down where everybody currently is, on the way out.
//
// Its own context, because the one that just ended is the reason we are here —
// using it would cancel every write immediately. Bounded, because shutdown is
// already racing the service manager's patience, and a hung database must not
// turn a deploy into a kill.
func (s *Service) flushPositions() {
	if s.repo == nil || s.q == nil {
		return
	}
	s.mu.Lock()
	pending := make([]positionSave, 0, len(s.pos))
	for id, p := range s.pos {
		if p.saved || p.provisional {
			continue
		}
		pending = append(pending, positionSave{accountID: id, at: p.at, seen: p.lastSeen})
	}
	s.mu.Unlock()
	if len(pending) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	for _, sv := range pending {
		s.savePosition(ctx, sv)
	}
}

// ---------------------------------------------------------------------------
// The pet. Everything below outlives the process; everything above dies with it.
// ---------------------------------------------------------------------------

// Config returns the content catalogue as the SPA receives it.
//
// No art decoration yet: no sprite has been uploaded, so every skin resolves to
// its emoji-over-gradient placeholder, which is what lets this game ship
// playable with zero images. Pointing a skin at an uploaded blob is one lookup
// against the shared asset store, added when there is a blob to point at.
func (s *Service) Config() Config { return Content() }

// State returns the caller's pet with every stat decayed to this instant,
// creating the pet on first sight and recording a death if this is the read that
// observes one.
func (s *Service) State(ctx context.Context, accountID string) (State, error) {
	return s.state(ctx, accountID, time.Now().UTC())
}

// Act applies one catalogue action to the caller's pet and answers with the
// server's own recomputed state.
//
// The client sends a verb and never a value — it does not say "set hp to 80",
// it says "heal" — so there is nothing in the request to forge. That differs
// deliberately from the first game, which carries its tension counter
// client-side: a run there is ephemeral and unrecorded until it ends, and a pet
// here is persistent.
func (s *Service) Act(ctx context.Context, accountID, actionKey string) (State, error) {
	action, ok := ActionByKey(actionKey)
	if !ok {
		return State{}, fmt.Errorf("%w: %q", ErrUnknownAction, actionKey)
	}
	for _, e := range action.Effects {
		if _, ok := StatByKey(e.StatKey); !ok {
			// A catalogue that disagrees with itself — an action moving a stat
			// that was removed. Caught here rather than silently doing nothing,
			// because the alternative is a button that appears to work and does
			// not.
			return State{}, fmt.Errorf("%w: action %q moves %q", ErrUnknownStat, action.Key, e.StatKey)
		}
	}

	now := time.Now().UTC()
	before, err := s.state(ctx, accountID, now)
	if err != nil {
		return State{}, err
	}
	if !before.Alive && !action.RevivesFatal {
		return State{}, ErrPetDead
	}

	// EVERY stat is written, not only the ones this action moves, and all of
	// them carry the same instant. That is the invariant the coupled decay
	// rests on: hp's drain is a function of the other stats' trajectories, so a
	// window whose start predates a driver's own as_of has a stretch of history
	// nobody can reconstruct. Emptying the bladder and writing only the bladder
	// would erase the morning's damage — the value would be re-derived later
	// from a pair that says it was never full.
	next := make([]StatRow, 0, len(before.Stats))
	for _, cur := range before.Stats {
		def, ok := StatByKey(cur.Key)
		if !ok {
			continue
		}
		value := cur.Value
		for _, e := range action.Effects {
			if e.StatKey == cur.Key {
				value = def.Clamp(value + e.Delta)
			}
		}
		next = append(next, StatRow{Key: cur.Key, Value: value, AsOf: now})
	}
	if err := s.repo.WriteStats(ctx, s.q, before.Pet.ID, next); err != nil {
		return State{}, err
	}

	// Bringing him round.
	//
	// Death is recoverable in this game, and that is a design decision rather
	// than an unfinished one: an irreversible loss in a fifteen-person friend
	// group is how you lose a player permanently, whereas a Ваня who has to be
	// revived is a story somebody tells. What death costs is the fright and
	// whatever decayed while nobody was looking — the moment of it stays
	// recorded until he is actually back on his feet.
	//
	// Only if the action actually lifted the fatal stat off its floor: an action
	// allowed on a corpse that failed to move the thing that killed him has not
	// revived anybody.
	if !before.Alive && action.RevivesFatal && revives(next) {
		if err := s.repo.Revive(ctx, s.q, before.Pet.ID); err != nil {
			return State{}, err
		}
	}

	return s.state(ctx, accountID, now)
}

// revives reports whether the rows about to be written leave every fatal stat
// above its floor.
func revives(rows []StatRow) bool {
	for _, r := range rows {
		def, ok := StatByKey(r.Key)
		if ok && def.Fatal && r.Value <= def.Min {
			return false
		}
	}
	return true
}

// state is the one read path, shared by State and Act so an action can never
// answer with a differently-computed world than a plain read would have.
//
// now is passed in rather than taken, so that one action reads, writes and
// answers against a single instant instead of three slightly different ones.
func (s *Service) state(ctx context.Context, accountID string, now time.Time) (State, error) {
	pet, err := s.repo.EnsurePet(ctx, s.q, accountID, catalogue.DefaultSkin, catalogue.DefaultLocation)
	if err != nil {
		return State{}, err
	}

	stored, err := s.storedStats(ctx, pet.ID, now)
	if err != nil {
		return State{}, err
	}

	// Values are built in CATALOGUE order, not in whatever order the query
	// returned, because that order is the display order and is content too. A
	// stored row whose key has left the catalogue is skipped rather than sent:
	// the client resolves keys against the config, so a key the config does not
	// mention is unrenderable, and that is the correct failure for a value only
	// content can define.
	values := make([]StatValue, 0, len(catalogue.Stats))
	var deadAt time.Time
	dead := false
	for _, def := range catalogue.Stats {
		row, ok := stored[def.Key]
		if !ok {
			continue
		}
		// `stored` is passed as the drivers for every stat, which is safe
		// because the dependency graph is one layer deep: a stat with penalties
		// is never itself a driver, so nothing here can consult a value that is
		// still being computed.
		values = append(values, StatValue{
			Key:         def.Key,
			Value:       def.AtWith(row.Value, row.AsOf, now, stored),
			AsOf:        row.AsOf,
			RatePerHour: def.RateAt(row.AsOf, now, stored),
		})
		if !def.Dead(row.Value, row.AsOf, now, stored) {
			continue
		}
		at, ok := def.DeadAtWith(row.Value, row.AsOf, stored)
		if ok && (!dead || at.Before(deadAt)) {
			deadAt, dead = at, true
		}
	}

	// The other half of keeping Postgres off the broadcast tick: every HTTP read
	// and every action passes through here, so the plane's copy is refreshed by
	// the same human-paced events that change it.
	s.remember(accountID, pet, rowsOf(stored))

	if dead && pet.Alive() {
		// The instant is derived from (value, as_of) alone — never from when
		// somebody happened to look — so two readers observing the same death at
		// different moments compute the identical timestamp. That is what makes
		// losing the write race harmless: whoever wrote it wrote what we would
		// have, and we can report it without reading it back.
		if _, err := s.repo.MarkDied(ctx, s.q, pet.ID, deadAt); err != nil {
			return State{}, err
		}
		at := deadAt
		pet.DiedAt = &at
		// Cached again, so the plane and the response cannot disagree about
		// whether the death has been recorded.
		s.remember(accountID, pet, rowsOf(stored))
	}

	return State{Pet: pet, Stats: values, Alive: pet.Alive(), ServerNow: now}, nil
}

// rowsOf flattens the stored pairs back into a slice, for the cache.
func rowsOf(stored map[string]StatRow) []StatRow {
	rows := make([]StatRow, 0, len(stored))
	for _, r := range stored {
		rows = append(rows, r)
	}
	return rows
}

// storedStats reads a pet's stat rows, seeding any the catalogue defines and the
// pet does not have yet.
//
// That seeding is what makes "adding a stat is a catalogue entry" true for pets
// that already exist, rather than only for pets created afterwards. It is the
// same lazy-materialisation shape as the death write and as the pet itself: no
// migration backfills anything, and the first read that notices a gap fills it.
//
// The re-read after seeding is not belt-and-braces. SeedStats does nothing on
// conflict, so when two requests seed the same pet at once the loser's rows are
// discarded — and the loser would otherwise report the values it tried to write
// rather than the ones that are actually stored.
func (s *Service) storedStats(ctx context.Context, petID string, now time.Time) (map[string]StatRow, error) {
	stored, err := s.statsByKey(ctx, petID)
	if err != nil {
		return nil, err
	}

	var missing []StatRow
	for _, def := range catalogue.Stats {
		if _, ok := stored[def.Key]; !ok {
			missing = append(missing, StatRow{Key: def.Key, Value: def.Start, AsOf: now})
		}
	}
	if len(missing) == 0 {
		return stored, nil
	}
	if err := s.repo.SeedStats(ctx, s.q, petID, missing); err != nil {
		return nil, err
	}
	return s.statsByKey(ctx, petID)
}

// statsByKey reads a pet's stored stat rows into a map. Keyed rather than
// returned as a slice because every caller wants "the row for this catalogue
// stat", and the query's own order is not the display order.
func (s *Service) statsByKey(ctx context.Context, petID string) (map[string]StatRow, error) {
	rows, err := s.repo.Stats(ctx, s.q, petID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]StatRow, len(rows))
	for _, r := range rows {
		byKey[r.Key] = r
	}
	return byKey, nil
}
