package gamevanyagotchi

import (
	"context"
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
		out = append(out, Peer{
			// TRUNCATED, deliberately. A uuid is 36 characters and this id
			// travels in every frame; twelve is the same length a player's
			// pseudonym uses, is unique enough among a capped two dozen objects,
			// and saves most of a kilobyte a second per viewer at the cap.
			ID:    "obj-" + shortID(o.ID),
			X:     o.At.X,
			Y:     o.At.Y,
			Art:   kind.Art,
			Label: kind.Label,
			Pose:  PoseFine,
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
