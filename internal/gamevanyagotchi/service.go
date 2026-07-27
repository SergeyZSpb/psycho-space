package gamevanyagotchi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/jackc/pgx/v5"
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

// Profiles is what this game needs from the account service, and nothing more:
// the picture to draw on somebody's Ваня.
//
// One method rather than the whole account, because everything else on an
// account is personal data this package has no business holding — the narrowest
// thing that satisfies the need is the one least likely to end up somewhere it
// should not. Declared here so the dependency points at shared infrastructure
// and can be faked in a test, the same shape gamekhimki uses for the asset
// store (ADR-031).
//
// Nil is a supported state: a service constructed without one draws the
// catalogue art for everybody, which is exactly what the plane did before
// avatars existed.
type Profiles interface {
	// AvatarURL returns the account's avatar, or "" when it has none.
	AvatarURL(ctx context.Context, accountID string) (string, error)
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

	// profiles is where an avatar comes from. Read once per hello, never on the
	// tick — see display.go for why that boundary is the whole design.
	profiles Profiles

	// assets says which of this game's art keys have a picture uploaded. Read on
	// the CONFIG path only — never on the tick, and never per entity — because
	// which keys exist changes when somebody uploads one and not otherwise.
	assets AssetPresence

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
	// world is what is STANDING in the yard — see world.go. Under the same lock
	// for the same reason, and refreshed at the same human-paced moments: the
	// tick reads it and never fills it.
	world []WorldObject
	// npcSaid is the one thing about a regular that is remembered rather than
	// computed, and it is worth being uncomfortable about.
	//
	// The cast is otherwise catalogue plus arithmetic (ADR-042): no rows, no
	// placements, nothing stored, so every process draws the same characters in
	// the same places at the same instant with nothing to keep in step. Their
	// POSITIONS still are, and that is the property worth having. What could not
	// be closed-form is a reaction to something a player did: it has no formula,
	// because it has a cause outside the clock.
	//
	// Without it a place only recoils when a second person is standing in it, and
	// on the evening this is actually played that is one player and the one or two
	// regulars who live there saying nothing — the feature would be invisible in
	// its commonest case. So a regular gets a line with an expiry, in memory, under
	// the same lock as everything else here, keyed by his catalogue key. It
	// expires by comparison against the tick's clock, so nothing schedules its
	// removal, exactly as a player's balloon does.
	npcSaid map[string]spoken

	// lastTick is the instant of the most recent broadcast, and it is THE clock
	// this game measures walks against.
	//
	// One clock, not two. A walk is evaluated on the tick, so if a tap stamped
	// itself with time.Now() while the tick ran on its own injected clock, the
	// two would disagree — invisibly in production, where a ticker's timestamp is
	// the wall clock, and catastrophically in a test, where the tick is a channel
	// the test fires and a walk stamped "now" would look like it had not started
	// yet. Taking the last tick's time costs at most one frame of staleness, 200
	// ms, which nobody can perceive, and buys a game whose every moving part is
	// driven by a clock the tests already own.
	lastTick time.Time

	// sleepersLoaded records that the one-off read of who is lying in the yard
	// has succeeded. Guarded by mu with everything else it touches; a bool
	// rather than a sync.Once because a Once would burn its single chance on a
	// failed query and leave the yard permanently empty of sleepers.
	sleepersLoaded bool

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
	// walk is where he is going, which subsumes where he is: a finished walk is
	// simply standing somewhere. Position stopped being a point when it stopped
	// being instantaneous — see motion.go.
	walk     walk
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
	// mood is a COSMETIC pose to draw until `moodUntil` — the happy face of
	// somebody who just found the keys, or the sad one of everybody who did not.
	// In memory and expiring by arithmetic, because winning and losing move no
	// stat and there is therefore nothing to write down.
	mood      string
	moodUntil time.Time
	// says is a line to draw over this Ваня until `saysUntil`.
	//
	// STATE WITH AN EXPIRY, never a message. A verb owes the player an answer —
	// "too far", «пиво кончилось», «хорошо пошло» — and the socket deliberately
	// has no reply channel, so the answer is put in the world where the next
	// full-state frame carries it. It expires by comparison against the tick's
	// clock, so nothing schedules its removal and nothing has to be cleaned up.
	says      string
	saysUntil time.Time
	// lastVerb is when this account's last batch of verbs was accepted.
	//
	// The socket's own limit is ten frames a second, which is right for taps
	// because a tap writes nothing. A verb writes — a transaction each — so it
	// needs its own bound, and this is it.
	lastVerb time.Time
	// lastGoto is when this account last changed location.
	//
	// ITS OWN CLOCK RATHER THAN A SHARE OF lastVerb, which is the whole reason it
	// is a second field instead of a reuse. A goto writes — one UPDATE, so at the
	// socket's ten frames a second an alternating pair of locations would be ten
	// writes a second — and therefore needs the same one-a-second bound a verb
	// gets. But sharing the verb's budget would mean that walking into лес ate the
	// allowance for the drink you walked there to have, and a verb refused by the
	// rate limit is refused in silence, so the player would meet it as a button
	// that did nothing.
	lastGoto time.Time
}

// NewService builds the game. room is the transport room it publishes to and
// accepts frames from; the caller supplies it so the game does not hardcode a
// name the platform's allowlist also owns. q and repo are the durable half and
// may both be nil for a caller that only drives the plane.
func NewService(transport Transport, room string, q db.DBTX, repo Repository, profiles Profiles, assets AssetPresence) *Service {
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
		profiles:     profiles,
		assets:       assets,
		pseudonymKey: key,
		pos:          make(map[string]placement),
		display:      make(map[string]display),
		npcSaid:      make(map[string]spoken),
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

// AvatarFor returns the picture to draw on the entity the roster calls handle,
// and reports whether there is one.
//
// THE PSEUDONYM IS THE LOOKUP KEY, and that is what keeps a durable identifier
// off the broadcast entirely. A URL on the frame would be a couple of hundred
// characters re-sent for every player five times a second, and — worse — the one
// thing on an ephemeral frame that survives a restart. Asking for it by the
// handle the client already has costs nothing on the wire and puts the picture
// behind exactly the same per-process pseudonym everything else about a person
// is behind.
//
// A linear scan rather than a reverse map, for the same reason the hub does one
// per id: a yard is tens of entities, the derivation is one HMAC each, and a
// second map would be a second thing to keep in step with the first — with a
// stale entry serving one person's face for another, which is the worst bug this
// code could have.
//
// An unknown handle is not an error. It is the ordinary answer for every NPC —
// none of them is a person and none has an account — and for anybody whose hello
// has not landed yet.
func (s *Service) AvatarFor(handle string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for accountID, d := range s.display {
		if d.avatar != "" && s.pseudonym(accountID) == handle {
			return d.avatar, true
		}
	}
	return "", false
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
	case TypeDo:
		s.handleVerbs(ctx, m, payload)
	case TypeGoto:
		s.handleGoto(ctx, m, payload)
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
		// The tick's clock, not time.Now — see the lastTick field.
		// By account, so all of that account's devices drive the one Ваня. Only
		// the point is written: lastSeen belongs to the broadcast, which is the
		// only thing here holding a clock, and a moving player is by definition
		// connected so the next tick refreshes it anyway.
		cur, known := s.pos[m.AccountID]
		if !known {
			// Never been placed, so he is at the spawn — not at the origin, which
			// is the corner of the plane and is what a zero-valued walk would
			// quietly claim.
			cur.walk = standing(spawn)
		}
		if now := s.lastTick; now.IsZero() {
			// No frame has been published yet, so nobody has seen him anywhere
			// and there is no journey to show. Put him where he asked to be.
			// This also avoids the one case where the clock is genuinely unknown:
			// there is no tick to borrow an instant from, and stamping the walk
			// with the wall clock would make it unrelatable to whatever clock the
			// first tick turns out to carry.
			cur.walk = standing(p)
		} else {
			// Retargeted from where he ACTUALLY IS, not from where he was going:
			// a tap mid-journey should feel like changing your mind, not like
			// queueing a second errand behind the first.
			cur.walk = s.planWalk(m.AccountID, cur.walk.at(now), p, now)
		}
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
	peers, here := s.place(members, now)
	if len(here) == 0 {
		// Nobody to tell, in any location. The map carries only the places
		// somebody is actually standing in, so an empty one is nobody anywhere —
		// and publishing into an empty room five times a second would be pure
		// waste, and would hide a genuine "why is this room empty" question behind
		// traffic.
		return nil
	}

	// Marshalled and published outside the lock: a slow publish must not hold up
	// the read pumps writing moves.
	// The NPCs, evaluated right here on the render tick and stored nowhere.
	// Appended AFTER the people so that the roster reads as "the yard, then its
	// furniture", and so the count above cannot accidentally include them.
	peers = append(peers, s.cast(now)...)
	// And whatever is lying about. After the count for the same reason the cast
	// is: a deposit is not somebody who is in the yard.
	peers = append(peers, s.props(now)...)

	frame, err := json.Marshal(Roster{
		T: TypeRoster, Peers: peers, Here: here,
		Hunt: s.hunt(now), Store: s.store(now),
	})
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
func (s *Service) place(members []realtime.Member, now time.Time) ([]Peer, map[string]int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastTick = now

	peers := make([]Peer, 0, len(members))
	// The head count, per location. Filled inside the loop below rather than taken
	// as a length afterwards, which is what it was while there was one number to
	// take: a map has to be accumulated as each person is placed. It still counts
	// PEOPLE only — the sleepers, the cast and the props are appended after this
	// function has returned, so none of them can reach it.
	here := make(map[string]int, 1)
	present := make(map[string]bool, len(members))
	var sleepers []sleeper
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
			p.walk = standing(spawn)
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
		at := p.walk.at(now)
		pose := d.pose(now)
		// A cosmetic mood outranks the derived pose, but NOT death: a corpse
		// grinning because he happened to be in the yard when somebody found the
		// keys would be funny once and wrong every time after.
		if pose != PoseDead && p.mood != "" && now.Before(p.moodUntil) {
			pose = p.mood
		}
		say := ""
		switch {
		case p.says != "" && now.Before(p.saysUntil):
			// Something the SERVER decided to tell him — a refusal, or what the
			// verb he just pressed did. It outranks all three of the others,
			// INCLUDING death, and that ordering is the point rather than an
			// accident: the moment a player most needs to be told «он не встаёт»
			// is exactly the moment his Ваня is dead, and a dead-first branch
			// swallowed the only answer a refused verb ever gets. He is not
			// speaking — the server is telling his owner something — so being
			// dead does not disqualify him from carrying it.
			say = p.says
		case pose == PoseDead:
			// Nothing of his own to add, though.
		case p.walk.gaveUp(now):
			// Decided once, at accept time, and broadcast — so everybody watches
			// him give up in the same spot at the same moment, saying the same
			// thing. A client-side roll would desynchronise the world, and a
			// client-side anything here would be forgeable.
			say = p.walk.say
		case p.walk.arrived(now):
			// Standing about, so he is entitled to mutter. Only when he is
			// STILL: a Ваня narrating his own walk would read as a status
			// message, and the joke is that this is not one — he is just
			// somebody who has been in a yard too long.
			say = s.idleSay(m.AccountID, now)
		}
		// Where he is drawn, and where he is counted. Both come from the display
		// cache rather than from a query, which is the rule this whole file exists
		// to keep — and it is why `goto` refreshes that cache in the same operation
		// as it writes the row.
		where := d.location()
		here[where]++
		peers = append(peers, Peer{
			ID:    s.pseudonym(m.AccountID),
			X:     at.X,
			Y:     at.Y,
			Loc:   onWire(where),
			Art:   d.skin(),
			Label: d.name,
			Pose:  pose,
			Say:   say,
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
			s.enqueueSave(positionSave{accountID: id, at: p.walk.at(now), seen: p.lastSeen})
		}
		// Past the grace he is not coming straight back, so he stops being
		// somebody who has briefly dropped out and becomes somebody who is
		// ASLEEP where he stood. That is the whole payoff of writing positions
		// down: with five to thirty friends the yard is almost never occupied by
		// two people at once, but it is never empty either, because everybody
		// else is lying about in it.
		//
		// Note what is NOT deleted any more. The entry is the sleeper, and the
		// map is bounded by the number of pets rather than by traffic. A
		// PROVISIONAL entry is dropped, because somebody who never stood
		// anywhere has nowhere to lie down.
		if now.Sub(p.lastSeen) < PositionGrace {
			continue
		}
		if p.provisional {
			delete(s.pos, id)
			delete(s.display, id)
			continue
		}
		sleepers = append(sleepers, sleeper{id: id, at: p.walk.at(now), since: p.lastSeen})
	}

	// Newest first, then capped: the yard shows who was here recently rather
	// than an ever-growing pile of everybody who ever played.
	sort.Slice(sleepers, func(i, j int) bool { return sleepers[i].since.After(sleepers[j].since) })
	if len(sleepers) > sleeperLimit {
		sleepers = sleepers[:sleeperLimit]
	}
	for _, sl := range sleepers {
		d := s.display[sl.id]
		pose := PoseAsleep
		if d.pose(now) == PoseDead {
			// Dead outranks asleep. He is not going to wake up, and pretending
			// otherwise would hide the one thing his owner needs to come back for.
			pose = PoseDead
		}
		peers = append(peers, Peer{
			ID: s.pseudonym(sl.id),
			X:  sl.at.X,
			Y:  sl.at.Y,
			// He is asleep where he stood, which includes WHICH PLACE he stood in:
			// somebody who went home from заброшка is lying in заброшка, and drawing
			// him in the yard would put a body in a place he was not. He is still not
			// counted — the map above is people with a socket open.
			Loc:   onWire(d.location()),
			Art:   d.skin(),
			Label: d.name,
			Pose:  pose,
		})
	}

	return peers, here
}

// sleeper is an absent account still lying in the yard.
type sleeper struct {
	id    string
	at    Point
	since time.Time
}

// spoken is a line and the instant it stops being drawn.
//
// The same shape as a placement's `says`/`saysUntil` pair, given a name because
// the cast needs one too and a map of two parallel maps would be a second thing
// to keep in step. It is state with an expiry and never a message: the tick
// compares against its own clock, so nothing has to remove it.
type spoken struct {
	text  string
	until time.Time
}

// cast places every NPC, closed-form, at this instant.
//
// No rows, no accounts, no placements: their POSITIONS are catalogue plus
// arithmetic, so they cannot acquire one, cannot be written to pets, and are not
// counted among the people in the yard. Roughly one sine per axis per character
// per tick, which does not register.
//
// The one thing that is looked up rather than computed is what a regular is
// SAYING, because a reaction to a player's deed has a cause outside the clock and
// therefore no formula — see `npcSaid`. The lock is taken once for the whole
// loop rather than per character: it is three map reads, and `broadcast` still
// marshals and publishes the frame outside it.
func (s *Service) cast(now time.Time) []Peer {
	elapsed := now.Sub(worldEpoch)
	out := make([]Peer, 0, len(catalogue.NPCs))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, npc := range catalogue.NPCs {
		at := evaluate(npc.Pattern, npc.Params, elapsed)
		out = append(out, Peer{
			ID: npcPrefix + npc.Key,
			X:  at.X,
			Y:  at.Y,
			// Where the catalogue says he hangs about, so a character stays in his
			// own place instead of appearing in all five at once.
			Loc:   onWire(npc.Location),
			Art:   npc.Art,
			Label: npc.Label,
			Pose:  PoseFine,
			Say:   s.npcSaying(npc.Key, now),
		})
	}
	return out
}

// npcSaying is the line over a regular's head right now, and forgets it once it
// has run out. Assumes s.mu is held.
//
// The read is what prunes, which is why there is no cleanup anywhere else: a
// regular who has stopped talking is one nobody asks about again until the next
// tick, and that tick is what drops the entry. The map therefore holds at most
// one line per character in the catalogue, and only for the seconds it is up.
func (s *Service) npcSaying(key string, now time.Time) string {
	said, ok := s.npcSaid[key]
	if !ok {
		return ""
	}
	if !now.Before(said.until) {
		delete(s.npcSaid, key)
		return ""
	}
	return said.text
}

// planWalk decides the journey a tap asks for, including whether he manages it.
//
// The tiredness roll happens HERE, once, on the server — not on each tick and
// not on the client. Everybody then watches the same Ваня sit down in the same
// place at the same moment, because the outcome is part of the walk rather than
// something each viewer re-rolls.
//
// Derived rather than randomised, so it needs no seed, no injection and no
// stored state, and a test can assert an exact outcome: the roll is a hash of
// the account, the destination and the instant, which is unpredictable in
// practice and reproducible in a test. A client could in principle predict its
// own roll and gains precisely nothing by it — the server still decides.
func (s *Service) planWalk(accountID string, from, to Point, now time.Time) walk {
	w := walk{from: from, to: to, startedAt: now, stopAt: 1}
	dist := distance(from, to)
	if dist <= tiredFrom {
		// A short hop always works, so nothing is ever unrecoverable: re-tapping
		// starts a new walk from where he sat down.
		return w
	}
	// Probability rises with how much is being asked of him, from nothing at
	// tiredFrom to tiredChance across the whole yard.
	reach := (dist - tiredFrom) / (maxDistance - tiredFrom)
	roll, pick, phrase := s.rolls(accountID, to, now)
	if roll >= tiredChance*reach {
		return w
	}
	w.stopAt = tiredEarliest + pick*(tiredLatest-tiredEarliest)
	w.say = pickPhrase(tiredSays, phrase)
	return w
}

// shy decides whether a verb fails for nerves this time, and what he says if it
// does.
//
// One hash gives both, exactly as the tiredness roll gives the fraction and the
// complaint together: the sentence belongs to this particular loss of nerve
// rather than being chosen after the fact. Keyed on (account, verb, instant)
// under the per-process key, so nobody can predict their own roll and wait for a
// favourable moment — which is the whole reason it is keyed at all, since a
// player who could see the answer would simply press at the instants that work
// and the mechanic would evaporate.
//
// The namespace prefix keeps it from colliding with the other two rolls: the
// walk and the muttering hash the same key at the same instant, and without it a
// giving-up and a loss of nerve would be the same draw.
func (s *Service) shy(accountID, verb string, chance float64, now time.Time) (bool, string) {
	if chance <= 0 {
		return false, ""
	}
	seed := fmt.Sprintf("shy|%s|%s|%d", accountID, verb, now.UnixNano())
	roll, phrase, _ := unitTriple(crypto.HMACSHA256(s.pseudonymKey, []byte(seed)))
	if roll >= chance {
		return false, ""
	}
	return true, pickPhrase(shySays, phrase)
}

// witnessLine is what one bystander says about one thing somebody else did.
//
// TWO CALLERS AND THAT IS WHAT EARNED IT: the yard objecting to a deposit
// (`recoil`) and the yard envying a found key (`settleHunt`). They differ in the
// pool they draw from and in nothing else, so the pool is the argument.
//
// Hashed PER WITNESS so that four people react in four different voices: one
// shared roll would be a chorus, which reads as a scripted cutscene rather than
// as a yard full of individuals. Keyed on the deed as well — the actor and the
// instant — so the same person standing through two of them does not repeat
// himself.
//
// The namespace prefix is what keeps each roll clear of the others, for the
// reason `shy` gives: the walk, the muttering and both of these hash the same key,
// and without it a loss of nerve and a bystander's disgust at the same instant
// would be the same draw.
func (s *Service) witnessLine(pool []string, namespace, witness, actor string, now time.Time) string {
	seed := fmt.Sprintf("%s|%s|%s|%d", namespace, witness, actor, now.UnixNano())
	_, phrase, _ := unitTriple(crypto.HMACSHA256(s.pseudonymKey, []byte(seed)))
	return pickPhrase(pool, phrase)
}

// rolls turns (who, where, when) into three independent numbers in 0..1.
func (s *Service) rolls(accountID string, to Point, now time.Time) (float64, float64, float64) {
	seed := fmt.Sprintf("%s|%v|%v|%d", accountID, to.X, to.Y, now.UnixNano())
	return unitTriple(crypto.HMACSHA256(s.pseudonymKey, []byte(seed)))
}

// idleSay is what he is muttering to himself right now, if anything.
//
// Closed-form, like everything else that moves here (ADR-042): time is cut into
// fixed slots from worldEpoch, hashing (account, slot) decides whether this one
// produces a remark and which, and it is shown for the first idleSayFor of the
// slot. So there is no timer, nothing stored, nothing to expire and nothing to
// clean up — and every process computes the same answer, which is what makes
// two people watching the same Ваня see the same words at the same moment.
//
// The account is the ONLY identity in the hash. Not the pseudonym, which is the
// same thing one HMAC later, and not the connection: a second tab must not put
// a second, differently-worded balloon over the same head.
func (s *Service) idleSay(accountID string, now time.Time) string {
	if len(idleSays) == 0 {
		return ""
	}
	since := now.Sub(worldEpoch)
	if since < 0 {
		// Before the world began. Reachable only from a test with a clock in the
		// past, and silence is the honest answer rather than a negative slot.
		return ""
	}
	slot := int64(since / idlePeriod)
	if since-time.Duration(slot)*idlePeriod >= idleSayFor {
		// Past the talking part of this slot. He has said his piece, or was
		// never going to.
		return ""
	}
	seed := fmt.Sprintf("idle|%s|%d", accountID, slot)
	roll, phrase, _ := unitTriple(crypto.HMACSHA256(s.pseudonymKey, []byte(seed)))
	if roll >= idleChance {
		return ""
	}
	return pickPhrase(idleSays, phrase)
}

// unitTriple reads three independent numbers in 0..1 out of one HMAC.
//
// One hash rather than three: the sum is 32 bytes and each draw needs four, so
// taking disjoint windows of it is as independent as hashing three times and
// costs a third as much.
func unitTriple(sum []byte) (float64, float64, float64) {
	a := binary.BigEndian.Uint32(sum[0:4])
	b := binary.BigEndian.Uint32(sum[4:8])
	c := binary.BigEndian.Uint32(sum[8:12])
	const max = float64(math.MaxUint32)
	return float64(a) / max, float64(b) / max, float64(c) / max
}

// pickPhrase chooses a line from a pool by a number in 0..1.
//
// The modulo is not decoration: a draw of exactly 1.0 is possible, and without
// it the index would be one past the end.
func pickPhrase(pool []string, at float64) string {
	if len(pool) == 0 {
		return ""
	}
	return pool[int(at*float64(len(pool)))%len(pool)]
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
	// One instant for the whole flush: everybody is stopping at the same moment,
	// and evaluating each walk against a slightly different `now` would write
	// down a yard nobody was ever standing in.
	flushAt := time.Now().UTC()
	s.mu.Lock()
	pending := make([]positionSave, 0, len(s.pos))
	for id, p := range s.pos {
		if p.saved || p.provisional {
			continue
		}
		pending = append(pending, positionSave{accountID: id, at: p.walk.at(flushAt), seen: p.lastSeen})
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

// Do applies a batch of verbs to one account's pet and answers with the server's
// own recomputed state.
//
// It is the single funnel every verb passes through, whatever delivered it. One
// place interprets a verb, so a rule — refused while dead, too far from the
// vendor, the stock is empty — is written once and cannot be enforced on one
// path and forgotten on another. An inbound socket frame is a thin adapter onto
// this and is currently the only one; a replay reads back the very log this
// appends.
//
// The client sends a verb and never a value — it does not say "set hp to 80",
// it says "heal" — so there is nothing in the request to forge. That differs
// deliberately from the first game, which carries its tension counter
// client-side: a run there is ephemeral and unrecorded until it ends, and a pet
// here is persistent.
//
// A BATCH IS THE GENERAL CASE AND A SINGLE ACTION IS A BATCH OF ONE. The verbs
// are folded in order against one snapshot, so drink-then-relieve is a
// different pet from relieve-then-drink, and the whole batch is written in one
// transaction with the events that produced it. `at` is supplied rather than
// taken so that the read, the fold, the write and the answer all happen at a
// single instant — and so that the caller can hand over the broadcast tick's
// clock, which is the one clock the rest of this game measures against.
//
// The whole batch is refused if ANY verb in it is refused, and nothing is
// written. Partially applying a list is the behaviour nobody can reason about:
// the player sees some of what they pressed take effect with no way to tell
// which, and a replay of the log would not reproduce it.
// `spot` is where the player says he searched, and it is empty for every verb
// but a search. It is a parameter of THIS function rather than of a second
// entry point, because one funnel stays one funnel: a rule about a search
// belongs where every other rule about a verb already is. It never reaches
// `apply`, which has to stay a pure function of (Snapshot, Event) — a spot is a
// fact about NOW, and a replay of last March has no idea where anybody stood.
func (s *Service) Do(ctx context.Context, accountID string, verbs []string, spot string, at time.Time) (State, error) {
	if len(verbs) == 0 {
		return s.state(ctx, accountID, at)
	}
	if len(verbs) > maxBatch {
		return State{}, ErrBatchTooLong
	}

	// Every verb is checked against the catalogue BEFORE any storage is touched.
	// `apply` would reject an unknown one too, but only after the read path had
	// already created the pet and seeded its stats — so a nonsense verb from a
	// hostile client would conjure rows for an account that has never opened the
	// game. The check is a pure catalogue lookup, and doing it here keeps the
	// property that a rejected batch leaves nothing behind at all.
	for _, verb := range verbs {
		action, ok := ActionByKey(verb)
		if !ok {
			return State{}, fmt.Errorf("%w: %q", ErrUnknownAction, verb)
		}
		for _, effect := range action.Effects {
			if _, ok := StatByKey(effect.StatKey); !ok {
				return State{}, fmt.Errorf("%w: action %q moves %q", ErrUnknownStat, action.Key, effect.StatKey)
			}
		}
	}

	// The read path runs FIRST, and not merely to fetch. It creates the pet,
	// seeds any stat the catalogue has gained, and — the part that matters here
	// — MATERIALISES a death that has already happened. A Ваня who ran out of
	// health while nobody was looking must be recorded dead before a verb is
	// judged against him, or reviving him would be clearing a death that was
	// never written down.
	before, err := s.state(ctx, accountID, at)
	if err != nil {
		return State{}, err
	}
	stored, err := s.storedStats(ctx, before.Pet.ID, at)
	if err != nil {
		return State{}, err
	}

	// Folded BEFORE anything is written, so a refused batch costs no rows.
	after := snapshotOf(stored, before.Pet.DiedAt)
	// ONE SEARCH PER FRAME, and the flag is what enforces it. The gate below
	// reads the world CACHE, which is not refreshed inside the batch, so without
	// this a frame carrying `{"verbs":["claim","claim",…],"spot":"bush"}` would
	// pass the same cached key eight times and win eight keys off one correct
	// search — the second claim taking the replacement the first one stood up,
	// which is hidden somewhere he has not looked. It is refused as an empty
	// place because that is exactly what it is: he already took what was there.
	searchedHere := false
	for i, verb := range verbs {
		next, err := apply(after, Event{Seq: int64(i + 1), Verb: verb, At: at})
		if err != nil {
			return State{}, err
		}
		// THE MOVEMENT GATE, and this is the line ADR-043 reserved for it.
		//
		// It is HERE RATHER THAN IN `apply` for a reason that is not stylistic.
		// `apply` is replayed, and it has to stay a pure function of (Snapshot,
		// Event): a rule about where somebody was standing cannot be one of its
		// inputs, because a replay of last March's drinks has no idea where
		// anybody was and finding out would put a query inside the fold. So the
		// rule about NOW lives on the live path alone, and the log records that
		// the drink happened rather than that it was permitted.
		//
		// Immediately AFTER apply, per verb and in batch order, which is what
		// puts it in the right place in the queue of refusals: being dead
		// outranks being far away, exactly as it already outranks an empty
		// bladder, because «он не встаёт» is the answer that tells the player
		// which button to press instead and «далековато» would send him walking
		// with a corpse. It reads the placement at `at` — the instant the batch is
		// folded — so a walk in progress is judged by where he has actually got
		// to rather than by where he set off from.
		// Looked up ONCE for the whole body. Both gates below read it, and an
		// unknown verb cannot reach here anyway — the catalogue check above the
		// read path rejected it before a single row was touched.
		// It is judged in the pet's OWN LOCATION as well as at its own distance:
		// coordinates are normalised per location, so a Ваня standing at the
		// crate's exact pitch in лес is not at the crate.
		action, known := ActionByKey(verb)
		if known && action.NeedsNear != "" && !s.beside(accountID, action.NeedsNear, before.Pet.LocationKey, at) {
			return State{}, fmt.Errorf("%w: %q needs to be at %q", ErrTooFar, verb, action.NeedsNear)
		}
		// THE SEARCH GATE, and it is the same rule one layer along: a verb that
		// contests a HIDDEN kind is a search, so it is judged against the place
		// the player named rather than against the object he cannot see. Read off
		// the catalogue in the same two hops the contest itself is — the verb
		// names a kind and the kind says it is hidden — so a second hidden kind
		// would inherit the whole mechanic without a line here.
		// `needsSpot` is the SAME expression the served `Action.NeedsSpot` is derived
		// from, deliberately: the gate the server enforces here and the flag the
		// browser draws its hiding places from are ONE rule, so a kind that stops
		// being hidden stops needing a spot on both ends in a single edit.
		if def, ok := contests(verb); ok && known && action.needsSpot() {
			if searchedHere {
				return State{}, fmt.Errorf("%w: %q has already been searched in this batch", ErrNothingHere, spot)
			}
			if err := s.searched(accountID, before.Pet.LocationKey, spot, def, at); err != nil {
				return State{}, err
			}
			searchedHere = true
		}
		// AND THEN HE MAY SIMPLY NOT GO THROUGH WITH IT.
		//
		// LAST of the gates, and the order is the point: every refusal above tells
		// the player something true about the world he can act on — he is dead, he
		// is not full enough, he is too far, he looked in the wrong place — and a
		// random one announced ahead of any of them would send him away with the
		// wrong idea about why the button did nothing. Nerves are what is left when
		// nothing else was in the way.
		//
		// Here rather than in `apply` for the reason the movement gate is: `apply`
		// has to stay a pure function of (Snapshot, Event), and a coin flip inside
		// it would make a replay of the same history produce a different pet every
		// time it ran (ADR-044). So the log records that he WENT, never that he was
		// willing to — and returning here, before the transaction is opened, is
		// what makes the refusal cost nothing at all: no stat, no event, no
		// deposit, and no tick of the tally that counts them.
		if known {
			if lost, line := s.shy(accountID, verb, action.FailChance, at); lost {
				return State{}, ShyRefusal{Line: line}
			}
		}
		after = next
	}

	// The snapshot and the events that produced it are written TOGETHER or not
	// at all. They are two statements, and a crash between them is the one
	// failure this model cannot absorb: a snapshot with no matching events is a
	// pet whose history silently disagrees with its state, and no read would
	// ever notice. Repository methods take a db.DBTX precisely so they compose
	// this way — this is the first caller that needed it.
	//
	// A fake in a unit test is not a pool and cannot begin anything, so the
	// transaction is taken when the handle supports one and skipped when it does
	// not. That is not a silent downgrade in production: the composition root
	// always passes the pool.
	// What the verbs LEAVE BEHIND in the world, decided before the transaction
	// so the statement below stays a list of writes rather than a place where
	// rules live. Nothing here reads the world: a deposit is unconditional, and
	// the one verb that is conditional on the world — arriving at the crate — did
	// its checking above, beside the catalogue checks, where a refusal costs no
	// rows.
	leavings := worldLeavings(verbs)
	// won records a SingleWinner outcome, which is the only one that paints the
	// yard; touched records that anything at all moved in the world, which is
	// what the cache has to be refreshed for. They are separate because drinking
	// changes the world and gives nobody a face: nothing is taken from anybody,
	// so there is nobody to look sad.
	won, touched := false, false

	if err := s.inTx(ctx, func(q db.DBTX) error {
		if err := s.repo.WriteStats(ctx, q, before.Pet.ID, rowsAt(after)); err != nil {
			return err
		}
		if _, err := s.repo.AppendEvents(ctx, q, before.Pet.ID, verbs, at); err != nil {
			return err
		}
		// In the SAME transaction as the stats and the events. A deposit that
		// survived a rolled-back batch would be a thing in the world that no
		// event explains and no snapshot accounts for — and this is already the
		// one place in the game where a multi-statement atomic write exists, so
		// it costs nothing to be honest here.
		// THE CONTEST, and it is inside the transaction on purpose: a player who
		// loses must not have his stats written either, and returning an error
		// here rolls the whole batch back. That is also how "losing costs
		// nothing" falls out for free rather than as a special case somebody has
		// to remember to write — a refused batch writes nothing at all.
		//
		// TWO DISCIPLINES, ROUTED BY THE CATALOGUE. The verb names a kind and the
		// kind names its discipline, so this is a switch over `Contest` rather
		// than over a verb or a kind key, and a third contested verb is two
		// catalogue entries and nothing here. What the two share is the shape:
		// one conditional UPDATE decides it, zero rows affected means you lost,
		// and whichever of them EXHAUSTS the thing stands its replacement up in
		// the same transaction.
		for _, verb := range verbs {
			def, ok := contests(verb)
			if !ok {
				continue
			}
			exhausted := false
			switch def.Contest {
			case ContestSingleWinner:
				got, err := s.repo.ClaimSingleton(ctx, q, def.Key, before.Pet.LocationKey, accountID, at)
				if err != nil {
					return err
				}
				if !got {
					return ErrClaimLost
				}
				// All or nothing: winning it is the same event as using it up.
				won, exhausted = true, true
			case ContestStock:
				left, got, err := s.repo.DrawFromStock(ctx, q, def.Key, before.Pet.LocationKey, at)
				if err != nil {
					return err
				}
				if !got {
					return ErrOutOfStock
				}
				// A crate is used up gradually, so only the draw that reaches
				// nought exhausts it — and the statement that decremented it has
				// already written `exhausted_at`, which is what lets the insert
				// below succeed against the partial index.
				exhausted = left <= 0
			default:
				// A kind that names no discipline is not contested by anybody,
				// which makes a verb pointing at one a content bug rather than a
				// refusal: nothing was taken, so nothing is owed.
				continue
			}
			touched = true
			if !exhausted {
				continue
			}
			// The replacement, in the SAME transaction as the thing it replaces,
			// so the very next frame already carries a fresh key or a fresh crate
			// rather than an empty yard somebody has to be told about.
			//
			// IT GOES WHERE THE CATALOGUE SAYS, NOT WHERE THE LAST ONE WAS, and
			// since neither singleton names a place that means a location drawn at
			// random and a hotspot within it — for the fresh key and, now, for the
			// fresh crate alike. Using the finder's own location here would have
			// been the quiet bug, and it would have been the worse one of the two:
			// every key after the first would appear wherever the previous one was
			// found and the hunt would settle into one place, and every crate would
			// be replaced under the man who emptied it, so the beer would never move
			// again once one player had drunk in his favourite corner.
			where := locationFor(def)
			if err := s.repo.InsertWorldObject(ctx, q, def.Key, where,
				s.placeFor(def, where), "", def.Singleton, def.stock(), nil); err != nil {
				return err
			}
		}

		for _, kind := range leavings {
			def, ok := ObjectKindByKey(kind)
			if !ok {
				continue
			}
			var expires *time.Time
			if def.Lifetime > 0 {
				until := at.Add(def.Lifetime)
				expires = &until
			}
			// AT THE POSITION THE SERVER ALREADY BELIEVES. The client sends a
			// verb and never a coordinate — there is nothing in the frame to
			// forge — so where he is standing is the yard's opinion rather than
			// his own.
			if err := s.repo.InsertWorldObject(ctx, q, kind, before.Pet.LocationKey,
				s.standing(accountID, at), accountID, def.Singleton, def.stock(), expires); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return State{}, err
	}
	if won {
		// Cosmetic, and only now that the write has committed: one happy face
		// and everybody else's sad, for a few seconds. Only the all-or-nothing
		// discipline does this — a drink takes nothing from anybody, so there is
		// nobody to look sad about it.
		//
		// IN THE PLACE IT HAPPENED. The room is the whole world, so an unfiltered
		// reaction would drain the face of somebody standing in лифт because of
		// something that happened in заброшка — a cosmetic effect with no local
		// cause, which reads as a broken game rather than as a lost race.
		s.settleHunt(ctx, accountID, before.Pet.LocationKey, at)
	}
	if len(leavings) > 0 || touched {
		// The world changed, so the cache the tick renders from is refreshed —
		// here, on the verb's own goroutine, which is human-paced. The tick
		// still reads nothing.
		s.loadWorld(ctx)
	}
	for _, kind := range leavings {
		if kind != KindRelief {
			continue
		}
		// And everybody who watched him do it says so. AFTER THE COMMIT, like
		// the moods above and for the same reason: everything before this point
		// can still be refused — by his nerve, by a lost claim, by the write
		// failing — and a yard that gags at a deed that did not happen is worse
		// than one that says nothing.
		s.recoil(ctx, accountID, before.Pet.LocationKey, at)
		break
	}
	// Only the CLEARING belongs here: a death is recorded by the read path, and
	// only a verb can undo one.
	if !before.Alive && after.Alive() {
		if err := s.repo.Revive(ctx, s.q, before.Pet.ID); err != nil {
			return State{}, err
		}
		// AND HE WAKES UP SOMEWHERE ELSE. Death costs him his place — the walk he
		// had done — and nothing else.
		//
		// HERE RATHER THAN IN `apply`, like every other rule about NOW: a replay
		// of last March's revivals must not move anybody, and a draw inside the
		// fold would make one history produce a different pet every time it ran.
		// After the transaction rather than inside it, for the same reason the
		// death clear is: it is a consequence of the batch having succeeded, not
		// a part of what makes it atomic — a revival that committed and then
		// failed to relocate is a live Ваня standing where he died, which is the
		// old behaviour and costs nobody anything.
		if loc, ok := somewhereElse(); ok {
			if err := s.repo.SetLocation(ctx, s.q, accountID, loc.Key); err != nil {
				slog.WarnContext(ctx, "gamevanyagotchi: could not move a revived pet", "err", err)
			} else {
				s.arrive(accountID, loc)
			}
		}
	}
	return s.state(ctx, accountID, at)
}

// state is the one read path, shared by State and Do so a verb can never answer
// with a differently-computed world than a plain read would have.
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

// beginner is a handle that can start a transaction. pgxpool.Pool satisfies it;
// a fake in a unit test does not, which is the whole reason it is an assertion
// rather than a field on Service.
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// inTx runs fn inside a transaction when the handle can provide one, and
// directly otherwise.
//
// The rollback is deferred unconditionally: a commit makes it a no-op, and
// every early return is then covered without a naked-return dance around each
// one.
func (s *Service) inTx(ctx context.Context, fn func(q db.DBTX) error) error {
	b, ok := s.q.(beginner)
	if !ok {
		return fn(s.q)
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Replay recomputes a pet from its history, ignoring the stored snapshot.
//
// This is what the event log is FOR, and the reason a verb is recorded rather
// than only folded into a number. Two uses, both real: reproducing a pet's
// exact state to diagnose it, and retro-tuning — change a decay rate in the
// catalogue and a replay produces what every pet WOULD have been under the new
// number, instead of carrying its old history forward for ever.
//
// It reads nothing but the pet's creation instant and its events, and folds
// them over `genesis` with the CURRENT catalogue. The result is deliberately
// not written anywhere: deciding to adopt a replay is a separate act from
// computing one, and a function that silently rewrote every pet would be a
// migration wearing the clothes of a query.
//
// `to` is the instant to bring the result up to, so a caller can ask what a pet
// looked like at any point rather than only now.
func (s *Service) Replay(ctx context.Context, accountID string, to time.Time) (Snapshot, error) {
	pet, ok, err := s.repo.FindPet(ctx, s.q, accountID)
	if err != nil || !ok {
		return Snapshot{}, err
	}
	events, err := s.repo.Events(ctx, s.q, pet.ID)
	if err != nil {
		return Snapshot{}, err
	}
	return advance(fold(genesis(pet.CreatedAt), events), to), nil
}

// verbInterval is the shortest gap between two accepted batches from one
// account.
//
// The socket already bounds frames at ten a second, and that is right for taps
// because a tap writes nothing at all. A verb writes — one transaction, a
// snapshot and an append — so it needs a bound of its own, and pressing a button
// once a second is far past what a person does deliberately.
const verbInterval = time.Second

// sayFor is how long a line stays over a Ваня's head.
const sayFor = 4 * time.Second

// handleVerbs applies a batch of verbs arriving over the socket.
//
// THE WIRE OWES NO REPLY, and that is the design rather than a shortcut. The
// 5 Hz full-state frame is the reconciliation channel — exactly as it already is
// for movement, where a tap is answered by the next roster and not by an
// acknowledgement — so a verb that is dropped is simply re-pressed. What the
// player IS owed is knowing what happened, and that arrives as state: the line
// over their own Ваня, which every other player can see too, and a push of their
// own pet so the bars move.
func (s *Service) handleVerbs(ctx context.Context, m realtime.Member, payload []byte) {
	verbs, spot, err := parseVerbs(payload)
	if err != nil {
		// Malformed shape, dropped in silence like every other bad frame: at ten
		// frames a second a log line per bad one is a flood lever, and there is
		// nobody to reply to.
		return
	}
	if s.repo == nil || s.q == nil {
		return
	}

	now := s.tickClock()
	if !s.allowVerb(m.AccountID, now) {
		// Deliberately silent. A client hammering the button is not owed an
		// explanation, and a balloon would make the flood visible to the whole
		// yard rather than costing only the sender.
		return
	}

	state, err := s.Do(ctx, m.AccountID, verbs, spot, now)
	if err != nil {
		s.Say(m.AccountID, refusalLine(ctx, err), sayFor)
		return
	}
	if line := doneLine(verbs); line != "" {
		s.Say(m.AccountID, line, sayFor)
	}
	s.pushState(ctx, m.AccountID, state)
}

// gotoInterval is the shortest gap between two accepted location changes from
// one account.
//
// The same one-a-second bound a batch of verbs gets, for the same reason and on
// its own clock. A tap writes nothing, so the socket's ten frames a second is
// the right limit for one; a goto writes a row, so at that rate a client
// alternating between two places would be ten UPDATEs a second for as long as it
// cared to. Changing where you are standing once a second is far past what a
// person does deliberately.
const gotoInterval = time.Second

// handleGoto moves a pet to another location.
//
// IT IS NOT A VERB AND DOES NOT GO THROUGH Do, which is the decision this
// function is: nothing here moves a stat, nothing is appended to the event log
// and there is nothing for `apply` to fold. Routing it through the verb funnel
// would have meant either a payload column on the log — which ADR-044 forbids,
// because a frozen copy of a verb's effects is what makes retro-tuning
// impossible — or one verb key per location, which is a content key per
// destination. It is a movement message, and it sits beside `vanyagotchi_move`
// rather than beside `vanyagotchi_do`.
//
// THREE THINGS ARE WRITTEN AND THEY MUST BE WRITTEN TOGETHER: the row, so he is
// still there after a restart; the display cache, so the tick draws him in the
// right place without asking Postgres (ADR-041); and the placement, so he is
// standing at the new location's entry rather than at whatever coordinates he
// happened to hold in the old one. Skipping the second is the interesting bug —
// the row would be right, every read would agree, and the plane would go on
// drawing him in the place he left with nothing anywhere reporting a problem.
//
// AND THEN THE PET GOES BACK TO HIM, which is not a write but is the step this
// function shipped without. A goto writes `pets.location_key`, and
// `vanyagotchi_state` is precisely what this game sends when a pet changes — so
// omitting it here was a breach of the wire contract rather than a missing
// courtesy, and «answered by nothing at all, exactly as a tap is» was the wrong
// analogy: a tap changes no row.
//
// The roster moves his DOT and that is all it does. The browser reads which
// place it is LOOKING at off the pet, so a journey that pushed nothing left the
// yard drawing the place he had left — filtering his own Ваня out of it as
// somebody standing elsewhere, reporting «здесь никого», and marking the old
// place as the one he was in. That last one is what made it unrecoverable rather
// than merely wrong: the place you are in is the row the travel sheet refuses to
// send you to, because it means "stay". He could not get back.
//
// A frame this server does not believe — bad JSON, no location, a location that
// is not in the catalogue — is still dropped in silence like every other one.
func (s *Service) handleGoto(ctx context.Context, m realtime.Member, payload []byte) {
	key, err := parseGoto(payload)
	if err != nil {
		return
	}
	// Resolved against the CATALOGUE, which is what makes an arbitrary string from
	// the wire harmless: an invented location does not resolve, so the worst a
	// hostile payload buys is a frame that did nothing. It is also what stops a
	// client writing a `location_key` the rest of the game cannot render — the
	// column is plain text, so nothing in the database would have objected.
	loc, ok := LocationByKey(key)
	if !ok {
		return
	}
	if s.repo == nil || s.q == nil {
		return
	}

	now := s.tickClock()
	if !s.allowGoto(m.AccountID, now) {
		return
	}
	// The row FIRST, and the caches only if it succeeded. The other order would
	// leave the plane drawing him somewhere the database has never heard of, and
	// the next hello would silently put him back.
	if err := s.repo.SetLocation(ctx, s.q, m.AccountID, loc.Key); err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: could not move a pet to another location", "err", err)
		return
	}
	s.arrive(m.AccountID, loc)

	// Read back rather than patched onto a struct we already hold: `state` is the
	// one place a pet is assembled for a client, and building a second, thinner
	// one here would be the second path to one outcome that costs a bug the day
	// the shape grows a field. It is one indexed read per journey, on a path
	// already bounded to one a second and already writing a row.
	state, err := s.state(ctx, m.AccountID, now)
	if err != nil {
		// He has moved and been told nothing, which is exactly the failure this
		// push exists to prevent — so it is worth a line, unlike a bad frame. The
		// next HTTP read answers with the same thing, so it self-heals on a
		// reload or a tab wake.
		slog.WarnContext(ctx, "gamevanyagotchi: could not read a pet back after a move", "err", err)
		return
	}
	s.pushState(ctx, m.AccountID, state)
}

// allowGoto reports whether this account may change location now, and records it
// if so.
func (s *Service) allowGoto(accountID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.pos[accountID]
	if !held.lastGoto.IsZero() && now.Sub(held.lastGoto) < gotoInterval {
		return false
	}
	held.lastGoto = now
	s.pos[accountID] = held
	return true
}

// arrive puts a Ваня down at a location's entry point and records that he is in
// it, in memory.
//
// STANDING, NOT WALKING, for the reason `load` gives about a stored position: he
// did not cross anything to get here, he left one place and turned up in
// another, and inventing a journey across the new location would draw him
// sliding in from wherever he happened to be standing in the old one.
//
// The display entry is READ, MODIFIED AND WRITTEN BACK rather than replaced,
// exactly as `remember` treats the avatar and for the identical reason: this
// knows one thing about a pet, and building a fresh entry from it would blank
// the skin, the name, the stats and the face of anybody whose cache was already
// loaded.
func (s *Service) arrive(accountID string, loc Location) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.pos[accountID]
	held.walk = standing(loc.Entry)
	// Chosen by a person, so no longer the spawn default a stored position is
	// allowed to replace — the same flag a tap clears, for the same reason.
	held.provisional = false
	s.pos[accountID] = held

	entry := s.display[accountID]
	entry.locationKey = loc.Key
	s.display[accountID] = entry
}

// tickClock is the instant this game measures everything against.
//
// The broadcast tick's, borrowed — the same clock a tap is stamped with, for the
// same reason: two clocks in one game is a bug that is invisible in production
// and nonsense in a test. Before the first tick there is nothing to borrow, so a
// verb arriving that early takes the wall clock.
func (s *Service) tickClock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastTick.IsZero() {
		return time.Now().UTC()
	}
	return s.lastTick
}

// allowVerb reports whether this account may act now, and records it if so.
func (s *Service) allowVerb(accountID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.pos[accountID]
	if !held.lastVerb.IsZero() && now.Sub(held.lastVerb) < verbInterval {
		return false
	}
	held.lastVerb = now
	s.pos[accountID] = held
	return true
}

// Say puts a line over an account's Ваня for a while.
//
// Exported because it is the game's one way of telling a player something: a
// refusal, a confirmation, and later the winner of a key. State with an expiry
// rather than a message, so a late joiner sees whatever is currently true and
// nothing has to be replayed to them.
//
// Capped to the same length the client caps at, so the server never sends a line
// that would be silently truncated on the way in — a message the player only
// half receives is worse than a shorter one written to fit.
func (s *Service) Say(accountID, text string, forHow time.Duration) {
	if text == "" {
		return
	}
	if r := []rune(text); len(r) > sayMax {
		text = string(r[:sayMax])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	held, ok := s.pos[accountID]
	if !ok {
		// Nobody to draw it over. A verb can arrive from a client that has an
		// HTTP session but no place in the yard, and inventing a placement to
		// hang a balloon on would put a Ваня on the plane who never walked in.
		return
	}
	held.says = text
	held.saysUntil = s.lastTick.Add(forHow)
	if s.lastTick.IsZero() {
		held.saysUntil = time.Now().UTC().Add(forHow)
	}
	s.pos[accountID] = held
}

// pushState sends an account its own pet, on every connection it has open.
//
// Not a reply: it carries no correlation, and a second device gets it too — which
// is the point, because that device's bars would otherwise sit stale until it
// happened to re-read. A failed send is ignored; the socket may have gone away,
// and the next HTTP read will answer with the same thing anyway.
func (s *Service) pushState(ctx context.Context, accountID string, state State) {
	members, err := s.transport.Members(ctx, s.room)
	if err != nil {
		return
	}
	frame, err := json.Marshal(StateFrame{T: TypeStateFrame, State: state})
	if err != nil {
		return
	}
	for _, m := range members {
		if m.AccountID != accountID {
			continue
		}
		_ = s.transport.PublishTo(ctx, m.ConnID, frame)
	}
}

// refusalLine turns a refusal into something a player can read over their own
// Ваня's head.
//
// The catalogue does not own these because they are not content about a verb —
// they are what the SERVER decided about this attempt, and the same sentence
// serves whichever verb was refused.
func refusalLine(ctx context.Context, err error) string {
	// The one refusal whose sentence is not fixed here. It is drawn from the
	// catalogue's pool by the same hash that took the roll, so the error carries
	// it and this reads it out with errors.As rather than looking one up — which
	// is what keeps the lines content, editable with no client deploy.
	var shy ShyRefusal
	switch {
	case errors.As(err, &shy):
		return shy.Line
	case errors.Is(err, ErrPetDead):
		return "он не встаёт"
	case errors.Is(err, ErrNotYet):
		return "рано ещё"
	case errors.Is(err, ErrClaimLost):
		return "кто-то успел раньше"
	case errors.Is(err, ErrNoSpot):
		// He pressed «искать ключи» without saying where. THREE REFUSALS SERVE A
		// SEARCH and they are distinct because they ask for three different
		// things: name a place, walk to it, or try somewhere else. A single line
		// covering them would tell the player to do none of the three, which is
		// the same reasoning that keeps «далековато» and «пиво кончилось» apart.
		return "где искать-то"
	case errors.Is(err, ErrTooFar):
		// Deliberately NOT the same line as the empty crate below. The two
		// refusals want opposite things from the player — walk over, or wait —
		// and one sentence covering both would tell him to do neither.
		return "далековато"
	case errors.Is(err, ErrNothingHere):
		// He looked, and it was not there. The one refusal in this game that is a
		// move in it rather than a correction — it costs him the search, and
		// telling him so is the only feedback the mechanic has.
		return "тут пусто"
	case errors.Is(err, ErrOutOfStock):
		return "пиво кончилось"
	case errors.Is(err, ErrBatchTooLong):
		return "не части"
	case errors.Is(err, ErrUnknownAction):
		// A verb the catalogue does not have can only come from a client that
		// invented one, so there is nothing useful to say and nothing to fix.
		return ""
	case errors.Is(err, ErrUnknownStat):
		// Silent to the PLAYER for the same reason — nothing he could press
		// would help — but not silent to the operator, and that asymmetry is the
		// point. An unknown verb is somebody else's stale client; an unknown
		// stat is OUR catalogue disagreeing with itself, which is a bug in the
		// content we shipped. A unit test makes it unreachable in a released
		// binary, so if it ever fires the log line is the only thing that would
		// say so.
		slog.WarnContext(ctx, "gamevanyagotchi: the catalogue disagrees with itself", "err", err)
		return ""
	default:
		// A database failure or anything else unexpected. The player gets
		// something honest and vague; the operator gets the real reason in the
		// log, with a trace id, which is where an error of this kind belongs.
		slog.WarnContext(ctx, "gamevanyagotchi: a verb failed", "err", err)
		return "чёт не вышло"
	}
}

// doneLine is what the catalogue says the last verb of a batch announces.
//
// The last one, because that is what just happened as far as the player is
// concerned — showing all of them would need a queue of balloons, and a batch is
// one press-and-a-bit in practice rather than a programme of work.
func doneLine(verbs []string) string {
	if len(verbs) == 0 {
		return ""
	}
	action, ok := ActionByKey(verbs[len(verbs)-1])
	if !ok {
		return ""
	}
	return action.Done
}

// PresentArtKeys reports which of this game's art keys have an uploaded image.
//
// The config advertises an image URL only for those, and every other key falls
// back to the emoji-over-gradient placeholder the catalogue already carries — so
// ART IS NEVER A HARD DEPENDENCY. The yard is playable with none uploaded, which
// is what makes commissioning it a separate errand from shipping the mechanic.
//
// A nil `assets` answers nil rather than failing, because that is a legitimate
// wiring: every unit test in this package builds a Service without one, and a
// deployment that has not been given the blob store should draw emoji rather
// than 503 a screen.
//
// Read on the config path alone. The blobs live in shared infrastructure
// (internal/gameassets) rather than here: storing and serving bytes is a
// mechanism any game needs, whereas which keys this game expects is a rule of
// this game (ADR-031).
func (s *Service) PresentArtKeys(ctx context.Context, gameKey string) (map[string]bool, error) {
	if s.assets == nil {
		return nil, nil
	}
	return s.assets.PresentKeys(ctx, gameKey)
}
