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
	// diedAt is the RECORDED death. A pet can be at its floor without this being
	// set — recording it is a write, and writes belong to the read path — so the
	// pose below checks both.
	diedAt *time.Time
	stats  map[string]StatRow
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
	entry := display{skinKey: pet.SkinKey, name: name, diedAt: pet.DiedAt, stats: stats}

	s.mu.Lock()
	defer s.mu.Unlock()
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
			held.at = at
			held.provisional = false
			s.pos[accountID] = held
		}
		s.mu.Unlock()
	}
}
