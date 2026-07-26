package gamevanyagotchi

import (
	"context"
	"crypto/rand"
	"log/slog"
	"time"
)

// The world half of the game: durable things standing on the plane.
//
// A pet is per-account and a placement is presence; a world object is neither.
// It is a row somebody left behind — a deposit today, a lost key and a crate of
// beer later — that everybody in the yard can see and that outlives the socket
// which created it.
//
// THE TICK STILL READS NOTHING. That is the constraint everything here is
// shaped by (ADR-041): the 5 Hz broadcast renders from memory, so world objects
// are held in a cache filled at the two human-paced moments that already read
// the database — a client saying hello, and a verb that writes one. In between,
// the cache is enough, and expiry is arithmetic over what it already holds
// rather than a query.
//
// EXPIRY IS FILTERED, NEVER SWEPT, for the same reason `sessions.expires_at`
// already is: rows accumulate and are ignored, because a sweeper would be the
// background timer this design does not have. At a handful of deposits a day
// that is thousands of dead rows a year, which is nothing.

// worldLimit is how many objects the plane will draw at once.
//
// A HARD BOUND ON THE WIRE, and the reason it exists rather than trusting the
// TTL to keep the number small. Every live object is an entity in a frame sent
// five times a second to every viewer, so the cost is bytes x rate x objects x
// viewers: at roughly 60 bytes each this cap is about 1.4 KB a frame, 7 KB/s to
// each phone, and it cannot grow however enthusiastically the yard behaves. The
// alternative — an unbounded list — turns a busy evening into a bandwidth
// incident on somebody's mobile data.
const worldLimit = 24

// WorldObject is one durable thing on the plane, as the game reads it back.
type WorldObject struct {
	ID   string
	Kind string
	At   Point
	// ExpiresAt is nil for a kind that lasts forever.
	ExpiresAt *time.Time
}

// live reports whether this object is still standing at now.
func (o WorldObject) live(now time.Time) bool {
	return o.ExpiresAt == nil || o.ExpiresAt.After(now)
}

// loadWorld refreshes the cache of what is standing in the yard.
//
// Called from the same human-paced moments as the display cache — a hello, and
// a verb that changed the world — never from the tick. A failure is not fatal:
// the plane keeps drawing whatever it last knew, and the next hello tries again.
func (s *Service) loadWorld(ctx context.Context) {
	if s.repo == nil || s.q == nil {
		return
	}
	objects, err := s.repo.LiveWorldObjects(ctx, s.q, LocationYard, worldLimit)
	if err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: world load failed", "err", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.world = objects
}

// props places every world object the plane should draw at now.
//
// They arrive as ORDINARY ENTITIES, which is the whole point: the client
// resolves an object's art against the catalogue exactly as it does a pet's or
// an NPC's, so it holds no kind key, no `switch (kind)` and no notion that
// world objects exist at all. Adding a kind is a catalogue entry and stays a
// backend-only change (ADR-028).
//
// Appended AFTER the head count is taken, like the NPCs and for the same
// reason: a deposit is not somebody who is in the yard.
func (s *Service) props(now time.Time) []Peer {
	s.mu.Lock()
	objects := append([]WorldObject(nil), s.world...)
	s.mu.Unlock()

	out := make([]Peer, 0, len(objects))
	for _, o := range objects {
		// Expiry is decided here, from the instant the tick is rendering, so a
		// deposit disappears from every screen at the same moment without
		// anybody having asked the database whether it should.
		if !o.live(now) {
			continue
		}
		kind, ok := ObjectKindByKey(o.Kind)
		if !ok {
			// A row whose kind has left the catalogue cannot be drawn, and is
			// skipped rather than drawn as a placeholder: unlike a pet, there is
			// no person behind it who would otherwise vanish from the yard.
			continue
		}
		var expires int64
		if o.ExpiresAt != nil {
			expires = o.ExpiresAt.Unix()
		}
		out = append(out, Peer{
			// TRUNCATED, deliberately. A uuid is 36 characters and this id
			// travels in every frame; twelve is the same length a player's
			// pseudonym uses, is unique enough among a capped two dozen objects,
			// and saves most of a kilobyte a second per viewer at the cap.
			ID:      "obj-" + shortID(o.ID),
			X:       o.At.X,
			Y:       o.At.Y,
			Art:     kind.Art,
			Label:   kind.Label,
			Pose:    PoseFine,
			Expires: expires,
		})
	}
	return out
}

// shortID cuts an identifier down to what the wire needs.
func shortID(id string) string {
	if len(id) <= pseudonymChars {
		return id
	}
	return id[:pseudonymChars]
}

// worldLeavings is what a batch of verbs puts on the ground.
//
// Read off the CATALOGUE rather than switched on a verb key, so teaching a new
// verb to leave something behind is an entry in content.go and nothing else —
// the same property every other axis of this game has. Today exactly one verb
// carries it.
func worldLeavings(verbs []string) []string {
	var out []string
	for _, v := range verbs {
		action, ok := ActionByKey(v)
		if ok && action.Leaves != "" {
			out = append(out, action.Leaves)
		}
	}
	return out
}

// standing is where the yard currently believes an account is.
//
// Falls back to the catalogue's spawn point for somebody who has never been
// placed — a verb can arrive from a client with a session but no socket, and
// putting a deposit at the entrance is better than refusing the verb over a
// detail the player cannot see.
func (s *Service) standing(accountID string, now time.Time) Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	if held, ok := s.pos[accountID]; ok {
		return held.walk.at(now)
	}
	return spawn
}

// moodFor is how long a cosmetic outcome stays on a face.
//
// The same few seconds a balloon lasts, and for the same reason: long enough to
// notice across the yard, short enough that the world does not stay decorated
// with something that already happened.
const moodFor = 4 * time.Second

// contested reports the world-object kind a verb races for, if any.
//
// Read off the catalogue: a verb contests the singleton kind whose own key it
// carries, which today is exactly one verb and one kind.
func contested(verb string) (string, bool) {
	if verb == ActionClaim {
		return KindKey, true
	}
	return "", false
}

// ensureHunt starts a key hunt if there is not one running.
//
// LAZY, IDEMPOTENT, AND ON A HUMAN-PACED PATH. It runs at a hello, never on the
// tick, and it is the identical `INSERT … ON CONFLICT DO NOTHING` the winning
// claim uses to insert the replacement — the same statement against the same
// partial index, so the two paths cannot disagree about how many keys exist.
// Cold start and a lost restart are therefore one mechanism rather than two.
func (s *Service) ensureHunt(ctx context.Context) {
	if s.repo == nil || s.q == nil {
		return
	}
	if _, ok, err := s.repo.ActiveSingleton(ctx, s.q, KindKey, LocationYard); err != nil || ok {
		return
	}
	def, ok := ObjectKindByKey(KindKey)
	if !ok {
		return
	}
	if err := s.repo.InsertWorldObject(ctx, s.q, KindKey, LocationYard, s.hidingPlace(), "", def.Singleton, nil); err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: could not start a key hunt", "err", err)
	}
}

// hidingPlace picks somewhere on the plane to lose the keys.
//
// crypto/rand rather than math/rand, and not because a spawn point is a secret:
// it is the project's standing rule for anything random, and a predictable
// sequence here would let a player who watched a few rounds guess where the next
// key lands before it is drawn.
func (s *Service) hidingPlace() Point {
	var b [2]byte
	_, _ = rand.Read(b[:])
	// Kept off the very edge, where a dot is half clipped by the plane and
	// awkward to tap.
	const margin = 0.08
	span := 1 - 2*margin
	return Point{
		X: margin + span*float64(b[0])/255,
		Y: margin + span*float64(b[1])/255,
	}
}

// setMood puts a cosmetic face on somebody for a moment.
//
// In memory and nowhere else, because winning and losing are COSMETIC: no stat
// moves, so there is nothing to write down. It expires by arithmetic rather than
// by anything clearing it, the same shape a balloon has.
func (s *Service) setMood(accountID, pose string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held, ok := s.pos[accountID]
	if !ok {
		return
	}
	held.mood, held.moodUntil = pose, until
	s.pos[accountID] = held
}

// settleHunt paints the yard's reaction to a claim: one happy face, everybody
// else's sad.
//
// Everyone present, not merely the people who were looking for it. Losing costs
// nothing but the face, and that is the whole of the loser effect — there is no
// hook here and there never will be, because the cosmetic ruling removed its
// only use.
func (s *Service) settleHunt(ctx context.Context, winner string, now time.Time) {
	until := now.Add(moodFor)
	// THE PET PATH STAYS TRANSPORT-FREE, which is a property the durable half
	// documents about itself and which a won claim nearly took away: this is the
	// first thing reachable from Do that wants to know who else is present. A
	// service built without a transport — every test of the pet in isolation,
	// and the composition the integration suite uses for it — must not panic
	// here, and the honest fallback is that the winner still gets his face while
	// nobody else is told, because there is nobody to tell.
	if s.transport == nil {
		s.setMood(winner, PoseHappy, until)
		return
	}
	members, err := s.transport.Members(ctx, s.room)
	if err != nil {
		s.setMood(winner, PoseHappy, until)
		return
	}
	for _, m := range members {
		if m.AccountID == winner {
			s.setMood(m.AccountID, PoseHappy, until)
			continue
		}
		s.setMood(m.AccountID, PoseSad, until)
	}
}

// hunt is the id of the key currently lost, read from the cache.
//
// From memory like everything else the tick touches: the world cache already
// holds the active key, so naming it costs no query. Empty when no hunt is
// running, which is a state the yard should not stay in for long — the next
// hello starts one.
func (s *Service) hunt() string {
	s.mu.Lock()
	objects := append([]WorldObject(nil), s.world...)
	s.mu.Unlock()
	for _, o := range objects {
		if o.Kind == KindKey {
			return shortID(o.ID)
		}
	}
	return ""
}
