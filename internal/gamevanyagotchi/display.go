package gamevanyagotchi

import (
	"context"
	"log/slog"
	"time"
)

// What the plane needs to know about a pet, held in memory so that the
// broadcast tick never touches Postgres.
//
// THE RULE THIS FILE EXISTS TO KEEP: the 5 Hz tick is a RENDER step. It reads no
// database, writes nothing and owns nothing, which is what makes a late, early,
// skipped or duplicated tick harmless (ADR-034, ADR-038). A query per tick would
// be thirty players × five a second against a box that also serves the site, to
// re-fetch a name and a skin key that change roughly never.
//
// So the durable half is read on the two occasions a human causes: when a client
// says hello on a fresh socket, and whenever that account acts over HTTP. In
// between, the cache is enough.
//
// The subtle part is WHAT is cached. Not the pose — a pose changes with the
// clock, so a cached one would be quietly wrong an hour later, showing a healthy
// Ваня who has been dying since lunchtime. What is cached is the raw
// `(value, as_of)` pairs, and the pose is DERIVED from them on every tick. That
// costs a subtraction per stat and stays correct indefinitely without anybody
// refreshing anything, for exactly the reason the whole decay model works: the
// value is a function of the pair and the clock, not an accumulation.
type display struct {
	skinKey string
	name    string
	// locationKey is which of the five places this pet is in.
	//
	// Cached here for exactly the reason the skin is: the tick has to stamp `loc`
	// on every entity it draws, and it may not ask Postgres which location
	// anybody is in five times a second. It is refreshed by the same human-paced
	// moments — a hello, any HTTP read, any verb — plus one more that is peculiar
	// to it: `goto` writes `pets.location_key` and must update this in the same
	// operation, or the plane goes on drawing him in the place he just left with
	// nothing anywhere reporting a problem.
	//
	// Empty means the default, which is what an account with nothing cached yet
	// gets — see `location`.
	locationKey string
	// diedAt is the RECORDED death. A pet can be at its floor without this being
	// set — recording it is a write, and writes belong to the read path — so the
	// pose below checks both.
	diedAt *time.Time
	stats  map[string]StatRow
	// avatar is the OWNER's picture, not the pet's. It has a different lifecycle
	// from everything else here — it belongs to the account, changes when the
	// person changes it on VK, and is re-read when a client says hello — which is
	// exactly why `remember` carries it over rather than rebuilding it: a verb
	// that rewrites the pet's stats must not blank the face on the plane.
	avatar string
}

// pose works out how this pet should be drawn at now.
//
// Derived rather than stored: there is no mood column to fall out of step with
// the numbers it claims to summarise, and adding a pose needs no migration. It
// reads the fatal stat because "how he looks" and "what is killing him" should
// not be two different opinions — and it uses the catalogue's own warning
// threshold, so an amber bar and a rough-looking Ваня are the same moment rather
// than two numbers that drift apart.
func (d display) pose(now time.Time) string {
	if d.diedAt != nil {
		return PoseDead
	}
	worst := PoseFine
	for _, def := range catalogue.Stats {
		if !def.Fatal {
			continue
		}
		row, ok := d.stats[def.Key]
		if !ok {
			continue
		}
		switch v := def.AtWith(row.Value, row.AsOf, now, d.stats); {
		case v <= def.Min:
			// At the floor but not yet written down: the next HTTP read is what
			// records the death. Drawing him dead before then is right — the
			// plane should not wait for a database row to tell the truth.
			return PoseDead
		case def.Troubled(v):
			worst = PosePoorly
		}
	}
	return worst
}

// skin is the art key to publish, falling back to the catalogue default so an
// account whose pet has not been read yet still draws as something.
func (d display) skin() string {
	if d.skinKey == "" {
		return catalogue.DefaultSkin
	}
	return d.skinKey
}

// location is where to draw this pet, falling back to the catalogue default for
// the same reason `skin` does: an account whose pet has not been read yet has to
// be somewhere, and the yard is where a pet is created.
func (d display) location() string { return locationOr(d.locationKey) }

// remember caches what the plane draws for an account.
func (s *Service) remember(accountID string, pet Pet, rows []StatRow) {
	stats := make(map[string]StatRow, len(rows))
	for _, r := range rows {
		stats[r.Key] = r
	}
	name := ""
	if pet.Name != nil {
		name = *pet.Name
	}
	entry := display{
		skinKey:     pet.SkinKey,
		name:        name,
		locationKey: pet.LocationKey,
		diedAt:      pet.DiedAt,
		stats:       stats,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// The avatar survives, because it is not part of what a pet read tells us.
	// This is called by every verb and every HTTP read, and none of them fetches
	// an account — so rebuilding the entry from the pet alone would drop the
	// picture the moment its owner did anything, and put it back only on their
	// next hello. Read under the same lock that writes it, so there is no window
	// where a concurrent setAvatar is lost.
	entry.avatar = s.display[accountID].avatar
	s.display[accountID] = entry
}

// setAvatar records the picture to draw on one account's Ваня.
//
// Separate from remember because the two have different sources and different
// lifecycles: a pet comes from this game's own tables, an avatar from the
// account service. Called only at hello, which is a fresh socket and therefore
// human-paced — the tick never reaches this, and that is the rule display.go
// exists to keep.
func (s *Service) setAvatar(accountID, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.display[accountID]
	entry.avatar = url
	s.display[accountID] = entry
}

// load fetches an account's pet into the cache, and — if the yard does not
// already know where that account is standing — puts it back where it was when
// it last left.
//
// Called when a client says hello, which is a fresh socket every time, so this
// is once per connection rather than once per frame.
//
// The position is applied only over NOTHING or over a PROVISIONAL spawn, never
// over one somebody chose. Both halves matter:
//
// A reconnect after a dropped socket is also a hello, and the in-memory position
// is the newer truth there — overwriting it with the last persisted one would
// undo the grace period that exists precisely so a reload keeps your place.
//
// But "nothing in memory" is not the same as "not yet placed", and assuming so
// was a real bug. The hub registers a connection at the upgrade, before the
// client's hello gets here, so a broadcast tick can land in that gap and write a
// spawn point into the map — after which a stored position would look like it
// was arriving late and be dropped, and a returning Ваня would teleport to the
// middle exactly as he did before any of this existed. The tick marks what it
// invents as provisional, and this is what that flag is for.
func (s *Service) load(ctx context.Context, accountID string) {
	if s.repo == nil || s.q == nil {
		return
	}
	// The yard's furniture comes first, so the arriving player sees everybody
	// who is asleep in it rather than an empty field followed by a pop-in.
	s.ensureSleepers(ctx)
	// And whatever is lying about in it, for the same reason: a hello is a fresh
	// socket and therefore human-paced, which is the only kind of moment allowed
	// to read the world.
	// A hunt is always running and there is always a crate, so somebody arriving
	// to an empty world stands both back up. Lazy and idempotent — the same
	// insert the exhausting write uses for the replacement, against the same
	// partial index — so cold start and a lost restart are one mechanism rather
	// than two, and neither is a timer.
	s.ensureWorld(ctx)
	s.loadWorld(ctx)

	// The picture, before the pet, and deliberately not fatal. A hello is the one
	// human-paced moment this is read — the tick must never reach the account
	// service any more than it reaches Postgres — and an account service that is
	// slow or unhappy costs this player his face for one connection rather than
	// costing the yard its frame.
	if s.profiles != nil {
		if url, err := s.profiles.AvatarURL(ctx, accountID); err != nil {
			slog.WarnContext(ctx, "gamevanyagotchi: avatar load failed", "err", err)
		} else {
			s.setAvatar(accountID, url)
		}
	}

	pet, ok, err := s.repo.FindPet(ctx, s.q, accountID)
	if err != nil {
		// Not fatal to anything: the plane draws catalogue defaults and the next
		// hello tries again. Logged because a persistent failure here is a real
		// problem that is otherwise completely silent.
		slog.WarnContext(ctx, "gamevanyagotchi: display load failed", "err", err)
		return
	}
	if !ok {
		return // no pet yet; the first HTTP read creates one
	}
	rows, err := s.repo.Stats(ctx, s.q, pet.ID)
	if err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: display stats load failed", "err", err)
		return
	}
	s.remember(accountID, pet, rows)

	if at, known := pet.Standing(); known {
		s.mu.Lock()
		if held, ok := s.pos[accountID]; !ok || held.provisional {
			// Standing, not walking: he was put down here by the database, and
			// arriving mid-stride from wherever the map happened to have him
			// would be inventing a journey nobody took.
			held.walk = standing(at)
			held.provisional = false
			s.pos[accountID] = held
		}
		s.mu.Unlock()
	}
}

// ensureSleepers reads, once, who is lying about in the yard.
//
// Without it the plane would only know about people who had connected since the
// last deploy, so the first visitor after a restart would find an empty field —
// and an empty field is exactly what the sleepers exist to prevent. With five to
// thirty friends the yard is almost never occupied by two people at once, but it
// is never empty either, because everybody else is asleep in it.
//
// ONE QUERY PER PROCESS, triggered by a human saying hello — not a timer, not
// the broadcast. After that the cache is maintained by the events that actually
// change it: somebody connects, somebody acts, somebody leaves. A pet created
// later joins the yard the first time its owner does anything, which is the
// moment it becomes true that they are in it.
//
// Retried on failure rather than being a sync.Once, because a Once would spend
// its single chance on a database blip and leave the yard bare until the next
// deploy.
func (s *Service) ensureSleepers(ctx context.Context) {
	if s.repo == nil || s.q == nil {
		return
	}
	s.mu.Lock()
	done := s.sleepersLoaded
	s.mu.Unlock()
	if done {
		return
	}

	pets, err := s.repo.SleepingPets(ctx, s.q, sleeperLimit)
	if err != nil {
		slog.WarnContext(ctx, "gamevanyagotchi: sleepers load failed", "err", err)
		return
	}

	for _, pet := range pets {
		at, known := pet.Standing()
		if !known {
			continue
		}
		// No stats fetched, and none needed: a sleeper is drawn asleep whatever
		// his numbers say, and the one thing that outranks sleep — being dead —
		// is on the pet row itself. Fetching stats for thirty pets to decide
		// something nobody can see would be a query per sleeper for nothing.
		s.remember(pet.AccountID, pet, nil)

		s.mu.Lock()
		if _, held := s.pos[pet.AccountID]; !held {
			// lastSeen is left at its zero value on purpose: it means "long ago",
			// which puts him past the grace immediately and therefore asleep
			// rather than briefly-disconnected. `saved` is set because this
			// position came OUT of the database — writing it straight back on
			// the next departure would be a write to say nothing changed.
			s.pos[pet.AccountID] = placement{walk: standing(at), saved: true}
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	s.sleepersLoaded = true
	s.mu.Unlock()
}
