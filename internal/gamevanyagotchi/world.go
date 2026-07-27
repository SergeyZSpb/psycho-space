package gamevanyagotchi

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
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

// worldLimitPerLocation is how many objects each location will draw at once.
//
// PER LOCATION RATHER THAN WORLD-WIDE, and that is the change the four locations
// forced. A single world-wide cap is a budget five places compete for, so one
// busy evening in the yard would fill it and лес would quietly hold nothing at
// all — including, on a bad day, the key, which would end the hunt with nothing
// failing anywhere. A cap per location cannot starve a neighbour.
//
// AND IT IS STILL A HARD BOUND ON THE WIRE, which is why it fell from 24 as it
// became per-location rather than staying put. Every live object is an entity in
// a frame sent five times a second to every viewer — the frame carries all five
// locations, because they are not rooms — so the cost is bytes × rate × objects
// × viewers. At roughly 60 bytes each, six per location across five locations is
// about 1.8 KB a frame and 9 KB/s to each phone, against 1.4 KB and 7 KB/s
// before; keeping 24 would have been 7.2 KB a frame and 36 KB/s, which is a
// bandwidth incident on somebody's mobile data rather than a cap.
//
// Six is also enough to play against: the only kind that accumulates is the
// relief deposit, it lasts ten minutes, and it needs a bladder at fifteen, so
// six live at once in ONE place is already a busy evening for a group of five.
// What is over the cap is the OLDEST, and the world's own fixtures are never
// evictable — see LiveWorldObjects, which orders singletons first precisely so a
// pile of deposits cannot push the beer crate out of the cache and leave a yard
// where nobody can drink.
const worldLimitPerLocation = 6

// WorldObject is one durable thing on the plane, as the game reads it back.
type WorldObject struct {
	ID   string
	Kind string
	// LocationKey is which of the five places it is standing in.
	//
	// READ BACK RATHER THAN ASSUMED, which it did not have to be while the cache
	// held one location: every caller knew the answer because it had asked for
	// that location. Now the cache holds all five, so an object has to carry its
	// own, and two rules turn on it — the store block tells a client where the
	// shop is, and a search is refused unless the key is hidden in the searcher's
	// OWN location. Without it a key hidden in лес at coordinates that happened to
	// be nearest двор's bench would have been claimable from the bench.
	LocationKey string
	At          Point
	// ExpiresAt is nil for a kind that lasts forever.
	ExpiresAt *time.Time
	// Remaining is how many draws are left in it, and nil for a kind nobody
	// draws down — which is every kind but the crate.
	//
	// It is READ BACK, unlike everything else about a contest, because it is the
	// one contested value a player has to be able to see BEFORE he acts: the
	// button that takes the last beer has to look different from the one that
	// takes the sixth. What DECIDES the draw is still the conditional UPDATE and
	// never this number — see Service.Do — so a stale cached count costs a
	// refusal at worst and can never oversell.
	Remaining *int
}

// live reports whether this object is still standing at now.
func (o WorldObject) live(now time.Time) bool {
	return o.ExpiresAt == nil || o.ExpiresAt.After(now)
}

// worldNow is a copy of the world cache, taken under the lock and read outside
// it.
//
// Three callers on the broadcast path take this same copy every tick — the props
// in the frame, the hunt's id, and the store block — and a copy of a slice
// capped at worldLimit is a couple of hundred bytes. The alternative is holding
// the lock across the whole of building a frame, which is the one thing the
// position map's own comment rules out.
func (s *Service) worldNow() []WorldObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]WorldObject(nil), s.world...)
}

// objectOf finds the live object of a singleton kind in the cache.
//
// Only meaningful for a SINGLETON kind, which is why there is no "all of them"
// version: for a kind the world holds many of — a deposit — "the one of this
// kind" is not a question with an answer, and every caller here is asking about
// the key or the crate.
//
// IT TAKES NO LOCATION, deliberately, and that is the "one key world-wide"
// ruling written down as a function signature. A singleton is unique across the
// whole world — the partial unique index is on `kind` alone — so "the key" is a
// question with one answer wherever it happens to be hidden, and the caller that
// cares WHERE compares the LocationKey it gets back. Taking a location here
// would have quietly turned a world-wide invariant into a per-location one and
// made "is there a hunt running" answerable five different ways.
func (s *Service) objectOf(kind string, now time.Time) (WorldObject, bool) {
	for _, o := range s.worldNow() {
		if o.Kind == kind && o.live(now) {
			return o, true
		}
	}
	return WorldObject{}, false
}

// loadWorld refreshes the cache of what is standing in the world.
//
// EVERY LOCATION, IN ONE READ, and both halves matter. Every location, because
// the frame carries all five and a cache holding only the reader's own would
// draw an empty лес for anybody standing in it. In one read, because the
// alternative — a query per location, driven by a loop over the catalogue — is
// five round trips whose failure modes are partial: a cache half refreshed
// against a database that went away mid-loop is a world nobody was ever in. The
// per-location cap therefore lives in the statement rather than in this loop; see
// LiveWorldObjects.
//
// Called from the same human-paced moments as the display cache — a hello, and
// a verb that changed the world — never from the tick. A failure is not fatal:
// the plane keeps drawing whatever it last knew, and the next hello tries again.
func (s *Service) loadWorld(ctx context.Context) {
	if s.repo == nil || s.q == nil {
		return
	}
	objects, err := s.repo.LiveWorldObjects(ctx, s.q, worldLimitPerLocation)
	if err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: world load failed", "err", err)
		return
	}
	// NORMALISED ONCE, HERE, which is the same thing `display.location` does for a
	// pet and for the same reason: "empty means the yard" is a rule about stored
	// data, so it is applied where stored data enters the process rather than at
	// each of the four places that go on to compare a location. The column is NOT
	// NULL, so this is a belt on a pair of braces — but the comparison it feeds
	// decides whether a search is refused, and a rule stated in four places is a
	// rule that will one day be stated three times.
	for i := range objects {
		objects[i].LocationKey = locationOr(objects[i].LocationKey)
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
	objects := s.worldNow()

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
		if kind.Hidden || kind.OffFrame {
			// TWO KINDS NEVER BECOME ENTITIES, for two unrelated reasons, and the
			// flags are kept apart so that stays true. `OffFrame` is the beer
			// crate: perfectly visible, but carried by the `store` block instead —
			// its place and its count together, which is what lets the client draw
			// a shop with a number on it rather than one more dot with a face. It
			// used to be published BOTH ways, which is the second path to one
			// outcome CLAUDE.md forbids.
			//
			// THE KEY IS NOT ON THE WIRE, and this one line is the whole of it.
			// It stays in the cache — the hunt's id comes out of it and so does
			// the answer a search is judged against — but it never becomes an
			// entity, so its coordinates do not leave this process and no client
			// can draw, infer or record where it is. A search is only a search
			// because of this.
			//
			// It also makes the yard one entity SMALLER on the wire, about 60
			// bytes a frame per viewer, which is the rare case of the secure
			// thing also being the cheap one.
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
			ID: propPrefix + shortID(o.ID),
			X:  o.At.X,
			Y:  o.At.Y,
			// Where it is lying, so a deposit left in кусты is drawn in кусты and
			// nowhere else. Omitted for the yard, which is where most of them are.
			Loc:     onWire(o.LocationKey),
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

// contests reports the kind a verb races other players for, if any.
//
// READ OFF THE CATALOGUE, in two hops: the verb names a kind and the kind names
// a discipline, so the service routes on `Contest` alone. It was `if verb ==
// ActionClaim { return KindKey }` while the key was the only contested thing in
// the world, which was the right shape for one case and stopped being it the
// moment the crate arrived — a second `if` there would have been the point at
// which "which verb contests what" started living in Go instead of in content.
//
// A verb naming a kind the catalogue has since dropped contests nothing rather
// than failing: it is the same unrenderable-content case a retired stat key and
// a retired object kind already get.
func contests(verb string) (ObjectKind, bool) {
	action, ok := ActionByKey(verb)
	if !ok || action.Contests == "" {
		return ObjectKind{}, false
	}
	return ObjectKindByKey(action.Contests)
}

// ensureWorld puts back whatever the yard is supposed to always have and does
// not.
//
// EVERY SINGLETON KIND, iterated off the catalogue rather than named here, which
// is what makes "the world always holds one of these" a content property: the
// crate arrived as an entry and this function did not change. There is exactly
// one active key and exactly one active crate, and either can be missing after a
// cold start, after a restart nobody won, or on the first day of the game.
//
// LAZY, IDEMPOTENT, AND ON A HUMAN-PACED PATH. It runs at a hello, never on the
// tick, and each insert is the identical `INSERT … ON CONFLICT DO NOTHING` the
// exhausting write uses for the replacement — the same statement against the
// same partial index, so the two paths cannot disagree about how many of a thing
// exist. Cold start and a lost restart are one mechanism rather than two.
func (s *Service) ensureWorld(ctx context.Context) {
	if s.repo == nil || s.q == nil {
		return
	}
	for _, def := range catalogue.ObjectKinds {
		if !def.Singleton {
			continue
		}
		s.ensureSingleton(ctx, def)
	}
}

// ensureSingleton spawns the one active object of a kind if the world has none.
//
// The ASK is what makes this cheap enough to run on every hello: without it,
// every visitor would write a row the partial index refuses, which is a table
// full of nothing and a wasted write per arrival rather than a wasted read.
//
// AND THE ASK IS WORLD-WIDE, which is the whole of "one key, not one per
// location". It names no location, so it answers "is there an active key
// ANYWHERE" — which is exactly what the partial unique index on `kind` alone
// enforces, and exactly what the insert below would be refused by. Asking per
// location would have been a question the index does not answer: four locations
// would each have reported no key, each would have tried to spawn one, three
// would have been silently refused by the index, and the yard would have looked
// as though it worked.
func (s *Service) ensureSingleton(ctx context.Context, def ObjectKind) {
	if _, ok, err := s.repo.ActiveSingleton(ctx, s.q, def.Key); err != nil || ok {
		return
	}
	where := locationFor(def)
	if err := s.repo.InsertWorldObject(ctx, s.q, def.Key, where,
		s.placeFor(def, where), "", def.Singleton, def.stock(), nil); err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: could not spawn a world object", "kind", def.Key, "err", err)
	}
}

// locationFor decides which of the five places a freshly spawned object goes in.
//
// The catalogue's own pitch for a kind that names one, and a location drawn at
// random for a kind that does not — the same shape `placeFor` has one level
// down, and read in the same two places: the hello that stands a missing
// singleton up, and the write that replaces one that has just been used up.
//
// THE RANDOM ONE IS THE KEY, and it is what makes the four locations a game
// rather than four backdrops. Nobody is told which place holds it, so the only
// way to find out is to go and look — and because the choice is made afresh
// every time a key is claimed, watching where the last one was tells you nothing
// about the next.
//
// crypto/rand rather than math/rand, for the reason `hideIn` gives about the
// hotspot one level down: this is half of the answer to the game, so a
// predictable sequence would let somebody who watched a few rounds know where
// the next key is before it is hidden.
func locationFor(def ObjectKind) string {
	if def.Location != "" {
		return def.Location
	}
	locations := catalogue.Locations
	if len(locations) == 0 {
		// A catalogue with no locations at all cannot spawn anything anywhere. The
		// default is the honest answer and an invariant test forbids the state.
		return catalogue.DefaultLocation
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(locations))))
	if err != nil {
		// Unreachable, exactly as it is in hideIn: crypto/rand's reader does not
		// fail on any platform this runs on. Falling back to the first location
		// keeps a spawn from being skipped, which is the failure that would end
		// the hunt for ever.
		return locations[0].Key
	}
	return locations[n.Int64()].Key
}

// placeFor decides where a freshly spawned object of a kind stands.
//
// The catalogue's own pitch for a kind that has one, and one of the location's
// hotspots for a kind that does not. That nil is the entire difference between a
// shop and a lost key, and reading it here is what keeps the difference out of
// the two places that spawn things — the hello above and the exhausting write in
// Do — so neither of them has to know which kinds stand still.
//
// IT TAKES THE LOCATION, which is the shape the four locations after the yard
// reuse: a thing is hidden among the places in the location it is spawned into,
// and nothing here names one.
func (s *Service) placeFor(def ObjectKind, locationKey string) Point {
	if def.At != nil {
		return *def.At
	}
	return hideIn(locationKey)
}

// hideIn picks one of a location's hotspots to lose something in.
//
// WHERE THE KEY IS, IS STORED RATHER THAN DERIVED, and that is a decision worth
// the paragraph because the obvious alternative was tried on paper first: derive
// the hiding place from the row's id under a process-lifetime secret, the way the
// roster's pseudonyms already are. Storing it wins on three counts. The row
// already has the columns — `x` and `y` — so this costs nothing and invents no
// second secret with a second lifetime to reason about. It is not derivable AT
// ALL, rather than derivable by anybody who obtains the secret, which meets the
// requirement more strongly and not less. And it SURVIVES A RESTART: a
// per-process secret would silently move the key out from under somebody who was
// halfway through searching for it — invisible, unfair, and impossible to write a
// test for.
//
// crypto/rand rather than math/rand, and here it genuinely is a secret rather
// than a habit: this is the answer to the game, so a predictable sequence would
// let a player who watched a few rounds know the next one before it started.
// `rand.Int` rather than a byte and a modulo, because a modulo over 256 favours
// the low indices by about 2% at five hotspots — small, but a bias in the one
// number the whole mechanic rests on is the wrong place to save three lines.
//
// A location with no hotspots cannot hide anything and falls back to its entry
// point. That is a content bug rather than a state — the invariant test in
// content_test.go forbids it — and the fallback exists only so a spawn cannot
// produce a coordinate the column's own CHECK would refuse.
func hideIn(locationKey string) Point {
	loc, ok := LocationByKey(locationKey)
	if !ok {
		return spawn
	}
	if len(loc.Hotspots) == 0 {
		return loc.Entry
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(loc.Hotspots))))
	if err != nil {
		// Unreachable on any platform this runs on — crypto/rand's reader does
		// not fail, and modern Go panics rather than reporting it here. Falling
		// back to the first hotspot keeps a spawn from being skipped, which is
		// the failure that would end the hunt for ever.
		return loc.Hotspots[0].At
	}
	return loc.Hotspots[n.Int64()].At
}

// searched judges a claim against the place the player says he looked in.
//
// THE SECOND USER OF THE ARRIVAL GATE I9 BUILT, and it deliberately reuses the
// shape rather than the function: `beside` measures the distance to the one live
// object of a kind, which is exactly the question a search must not answer — "am
// I near the key" is the thing the player is not allowed to be told. This
// measures the distance to the spot HE NAMED, which he already knows, and only
// then asks whether the key is in it.
//
// THE ORDER OF THE THREE CHECKS IS THE SECURITY PROPERTY, not a matter of taste.
// Reach is decided BEFORE the answer is looked at, so a wrong spot and a right
// spot he has not reached are indistinguishable: standing anywhere far away is
// «далековато» whether or not the key is there, and only somebody actually
// standing in a place is ever told whether it was empty. Reverse the two and the
// refusal becomes an oracle — tap every hotspot from the middle of the yard and
// the one that answers differently is the answer.
//
// WHAT A HOSTILE CLIENT CAN SEND, weighed. The spot is an arbitrary string from
// the wire: it is resolved against the catalogue for the pet's OWN location
// (so a spot from another location, an invented one, or none at all is
// ErrNoSpot), and then against the yard's own in-memory placement at the instant
// the batch is folded (so a client announcing an arrival it has not made is
// ErrTooFar). Both are decided server-side from data the client cannot influence
// — the catalogue is compiled in and the placement is what the plane broadcast
// to everybody. The client announcing arrival is a REQUEST, never a fact. The
// most an arbitrary payload buys is a refusal, and the refusal it buys carries no
// information about where the key is.
//
// It is on the LIVE PATH ONLY, next to the arrival gate and never inside `apply`,
// for the reason ADR-043 gives: `apply` is what a replay folds over a history,
// and a rule about where somebody was standing cannot be one of its inputs. The
// log records that a search happened, never where from.
func (s *Service) searched(accountID, locationKey, spotKey string, def ObjectKind, now time.Time) error {
	spot, ok := hotspotIn(locationKey, spotKey)
	if !ok {
		return fmt.Errorf("%w: %q is not a place in %q", ErrNoSpot, spotKey, locationKey)
	}
	if distance(s.standing(accountID, now), spot.At) > arriveWithin {
		return fmt.Errorf("%w: %q is not standing at %q", ErrTooFar, accountID, spot.Key)
	}
	object, ok := s.objectOf(def.Key, now)
	if !ok || object.LocationKey != locationKey {
		// No hunt is running at all, or it is running somewhere else. ONE ANSWER
		// FOR BOTH, and the same answer a wrong hotspot gets: there is genuinely
		// nothing in the place he looked in, and any sentence that distinguished
		// the cases would be an oracle. «пусто здесь, но не там» narrows five
		// locations to four for the price of one tap, which is the entire game
		// given away by a refusal.
		//
		// THE COMPARISON IS WHY WorldObject CARRIES ITS LOCATION. Without it the
		// check below would measure the key's coordinates against THIS location's
		// hotspots — and coordinates are normalised, so a key hidden at (0.14,0.30)
		// in лес would have been nearest двор's куст and claimable from it.
		return fmt.Errorf("%w: no %q is hidden in %q", ErrNothingHere, def.Key, locationKey)
	}
	where, ok := nearestHotspot(locationKey, object.At)
	if !ok || where.Key != spot.Key {
		return fmt.Errorf("%w: %q is not in %q", ErrNothingHere, def.Key, spot.Key)
	}
	return nil
}

// beside reports whether an account is standing close enough to the one live
// object of a kind to use a verb gated on it.
//
// THE MOVEMENT GATE ADR-043 RESERVED A PLACE FOR, and three things about it are
// deliberate. It reads the IN-MEMORY placement, because the question is about
// where he is now and the answer is stored nowhere — a position written down on
// departure is not what "he is at the crate" means. It reads the object out of
// the same world cache the frame is built from, so the client greying a button
// and the server refusing a verb are looking at one number rather than two that
// can disagree. And a kind the world does not currently hold is not something
// anybody can be beside, which is the honest answer rather than a fail-open one:
// no crate means the frame carries no store either, so both ends say the same
// thing.
//
// AND A THING IN ANOTHER LOCATION IS NOT SOMETHING YOU ARE BESIDE, however close
// the two coordinates happen to be. Coordinates are normalised per location, so
// without this a Ваня standing at (0.82, 0.22) in лес would be at the crate's
// exact pitch and could drink from a shop four places away. The client is told
// the same thing by Store.Loc — it draws no store at all when it is looking at
// somewhere else — so the greyed button and the refusal agree.
func (s *Service) beside(accountID, kind, locationKey string, now time.Time) bool {
	object, ok := s.objectOf(kind, now)
	if !ok || object.LocationKey != locationKey {
		return false
	}
	return distance(s.standing(accountID, now), object.At) <= arriveWithin
}

// store is the beer store as the frame publishes it, or nil when the world has
// none.
//
// Read from the cache like everything else the tick touches, so naming it costs
// no query.
//
// It carries WHERE the shop is as well as its place within that location, and
// the two are not the same question now that there are five: a client looking at
// заброшка is told the store is in двор and draws nothing. One field rather than
// a store block per location, because there is one crate in the world.
func (s *Service) store(now time.Time) *Store {
	crate, ok := s.objectOf(KindCrate, now)
	if !ok || crate.Remaining == nil {
		return nil
	}
	return &Store{
		X: crate.At.X, Y: crate.At.Y,
		Loc:  onWire(crate.LocationKey),
		Left: *crate.Remaining,
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

// settleHunt paints the reaction to a claim in the place it happened: one happy
// face, and everybody else standing there sad.
//
// Everyone PRESENT IN THAT LOCATION, not merely the people who were looking for
// it — and not the whole world, which is what it used to be and what the four
// locations turned into a bug. The room is the world, deliberately, because
// locations are not rooms; so an unfiltered loop drained the face of somebody
// standing in лифт because of something that happened in заброшка, which he
// could not see, had no part in, and would be given no explanation of. A
// cosmetic effect with no local cause is worse than none: it reads as the game
// being broken rather than as somebody having lost a race.
//
// The WINNER is unconditional. There is one of him, and the search gate proved
// he is standing in `where` before any of this ran, so no filter could tell him
// anything the caller does not already know.
//
// Losing still costs nothing but the face, and that is the whole of the loser
// effect — there is no hook here and there never will be, because the cosmetic
// ruling removed its only use.
func (s *Service) settleHunt(ctx context.Context, winner, where string, now time.Time) {
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
	standing := s.locations()
	for _, m := range members {
		if m.AccountID == winner {
			s.setMood(m.AccountID, PoseHappy, until)
			continue
		}
		// Somebody the display cache has never heard of is treated as being in the
		// default location, exactly as the frame draws him there — one rule for
		// "empty means the yard", applied here as well.
		// Somebody the display cache has never heard of is treated as being in the
		// default location, exactly as the frame draws him there — one rule for
		// "empty means the yard", applied here as well.
		if locationOr(standing[m.AccountID]) != where {
			continue
		}
		s.setMood(m.AccountID, PoseSad, until)
	}
}

// locations is a copy of where every account the plane knows about is standing,
// taken under the lock and read outside it.
//
// The same shape `worldNow` uses, and for the same reason its comment gives: the
// alternative is holding the lock across a loop that calls back into `setMood`,
// which takes it again. A map of a few dozen short strings is nothing; a
// re-entrant lock is a deadlock.
//
// A MISSING ENTRY IS NOT THE SAME AS AN EMPTY ONE. An account with nothing cached
// is absent from this map rather than present with "", and every caller resolves
// that through `locationOr` — the same rule `display.location` applies, so the
// place somebody is judged to be in and the place he is drawn in cannot differ.
func (s *Service) locations() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.display))
	for id, d := range s.display {
		out[id] = d.location()
	}
	return out
}

// hunt is the id of the key currently lost, read from the cache.
//
// From memory like everything else the tick touches: the world cache already
// holds the active key, so naming it costs no query. Empty when no hunt is
// running, which is a state the yard should not stay in for long — the next
// hello starts one.
func (s *Service) hunt(now time.Time) string {
	key, ok := s.objectOf(KindKey, now)
	if !ok {
		return ""
	}
	return shortID(key.ID)
}

// somewhereElse picks a location at random, for a Ваня who has just been raised
// from the dead.
//
// A REVIVAL RELOCATES HIM ENTIRELY, and that is a mechanic rather than a
// placement detail: dying costs you the walk you had done and nothing else,
// which is the shape the reference-game research settled on — mildly annoying
// and cheaply reversible is fine, irreversible is churn. It also gives the four
// places besides the yard something to do that is not the key hunt.
//
// SERVER-SIDE, and it has to be. A client-decided destination would be forgeable
// and would show two players different worlds; this one arrives the way every
// other fact about a pet does, on the `vanyagotchi_state` push that follows the
// verb.
//
// crypto/rand for consistency with the other draws in this file rather than
// because it is a secret — nobody gains anything by predicting where a corpse
// wakes up. Two randomisers with different guarantees in one package would be a
// distinction somebody has to keep straight, and there is no reason to introduce
// one.
func somewhereElse() (Location, bool) {
	locations := catalogue.Locations
	if len(locations) == 0 {
		return Location{}, false
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(locations))))
	if err != nil {
		// Unreachable, as it is everywhere else in this file. Waking up in the
		// first location beats not waking up at all.
		return locations[0], true
	}
	return locations[n.Int64()], true
}
