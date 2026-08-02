package gamevanyadum

import "time"

// The rewind buffer — the server's memory of what the world used to look like.
//
// WHY IT EXISTS BEFORE ANYTHING SHOOTS. Lag compensation resolves a shot
// against the world **as the shooter actually saw it**, which is
// `now − RTT − InterpolationDelay` ago. That is the only way a player who
// aimed at a moving target and was right does not miss; without it everybody
// learns to lead by a body width, which is a game teaching you to play around
// its netcode.
//
// THE CONSUMER IS NOW HERE: World.resolveShot rewinds by RewindTo on every
// barrel that goes off. The buffer was built and left unwired for two
// iterations, deliberately — the recorder CANNOT arrive with the shotgun,
// because a ring buffer of the past is the one piece of a netcode stack that
// genuinely cannot be retrofitted: on the day you want it the past has already
// happened. See ADR-052.
//
// It is memory-only and belongs to the one world, like everything else the
// simulation owns, so it neither persists nor ticks anything durable.

// historyFrame is one recorded instant.
//
// Positions only, and deliberately: rewinding a hit test needs where things
// were, not what they were carrying. Health, counters and pickups are resolved
// against the PRESENT even for a compensated shot — a bullet fired at somebody
// who has since died should not kill them twice.
type historyFrame struct {
	tick int64
	at   time.Time
	// spots is every entity's position at that instant, keyed by the same
	// identity the snapshot publishes — the SLOT for a man and the ID for a слоп,
	// because those two numbers are all a client has to name what it was aiming at
	// with.
	spots map[ref]Spot
}

// ref names one thing the rewind ring and the hit test can talk about.
//
// TWO NUMBERING SCHEMES SHARE ONE MAP, and this type is what keeps them apart. A
// place in the building and a нейрослоп are both counted from zero, both are
// small, and both are reused the moment their holder is gone — so a bare integer
// key would silently resolve a shot at слоп 1 against whoever holds place 1, and
// would do it only when both happened to exist. A two-field key costs nothing at
// runtime and makes the collision unrepresentable rather than unlikely.
//
// It is also what the hit test hands back (hit.go, shoot), so the answer to "what
// did this ray land on" arrives already saying which kind of thing it was.
type ref struct {
	// Slop tells the two kinds apart.
	Slop bool
	// N is the slot for a man and the id for a слоп.
	N int
}

// Spot is one entity's place in the world at one instant.
type Spot struct {
	Pos    Vec2
	Sector int
	// Alive is recorded so a rewind cannot resurrect a corpse to be hit. A слоп is
	// never recorded dead — one leaves the building on the tick it is killed
	// (world.go, killSlop) — so this is only ever false for a man on the floor.
	Alive bool
}

// history is a fixed-size ring of recent frames.
//
// A ring rather than a slice that grows: the capacity is bounded by wall-clock
// (HistoryWindow) and the tick rate is fixed, so it is known exactly and the
// whole structure allocates once, when the world is generated, and never again.
// A simulation loop that allocates twenty times a second is a self-inflicted
// garbage problem.
//
// The past it can actually answer for is one step shorter than HistoryWindow,
// because the frames are fenceposts; see that constant for why the margin over
// RewindMax is sized the way it is.
type history struct {
	frames []historyFrame
	// next is where the following record goes; the ring is full once it has
	// wrapped, which `count` reports.
	next  int
	count int
}

// historyCapacity is how many frames the ring holds at the simulation rate.
func historyCapacity() int {
	n := int(HistoryWindow / SimStep)
	if n < 2 {
		return 2
	}
	return n
}

func newHistory() *history {
	return &history{frames: make([]historyFrame, historyCapacity())}
}

// record stores one instant. The map is reused rather than reallocated when the
// ring wraps, for the same reason the ring is fixed-size.
func (h *history) record(tick int64, at time.Time, spots map[ref]Spot) {
	f := &h.frames[h.next]
	if f.spots == nil {
		f.spots = make(map[ref]Spot, len(spots)+2)
	} else {
		clear(f.spots)
	}
	for id, s := range spots {
		f.spots[id] = s
	}
	f.tick, f.at = tick, at
	h.next = (h.next + 1) % len(h.frames)
	if h.count < len(h.frames) {
		h.count++
	}
}

// forget takes one entity out of every frame in the ring, so nothing recorded
// while it existed can be read back for whatever takes its number next.
//
// IT IS THE PRICE OF KEYING BY THE WIRE'S OWN NAMES, and it is worth paying
// rather than avoiding. `spots` is keyed by slot and by слоп id deliberately —
// the rewind and the wire then name the same things, which is what lets the hit
// test hand what it landed on straight back to the world (world.go, resolveShot)
// — but BOTH of those numbers are REUSED the moment their holder is gone, and the
// ring does not expire for RewindMax after that. Without this purge whoever takes
// a freed number is placed, for the whole of that window, wherever its last
// holder was standing: shot on a line it was never on, at the far end of a
// building it has just walked into.
//
// IT IS REACHABLE ON THE ORDINARY PATH FOR BOTH KINDS, rather than in theory.
// Advance records the frame and THEN releases whoever has been abandoned, so the
// last thing the ring learns about a slot is where its previous holder stood on
// the tick he left it — and a слоп is killed mid-tick by a shot, so the ring is
// already carrying half a second of a creature that no longer exists when its id
// is handed to the next one.
//
// A DELETION FROM EVERY FRAME RATHER THAN A RE-KEYING OF THE RING. Keying the
// past by account would survive reuse by itself, and would cost the whole
// correspondence between the rewound world and the published one — every read
// would have to translate an account back into the slot the wire names. This is
// one pass over a fixed-size ring, on the rare tick something leaves.
func (h *history) forget(r ref) {
	for i := range h.frames {
		// A frame the ring has not reached yet has no map at all, and deleting
		// from a nil map is a no-op rather than a panic, so there is nothing to
		// guard.
		delete(h.frames[i].spots, r)
	}
}

// at returns the world as it was at an instant, interpolated between the two
// recorded frames that bracket it.
//
// Interpolated rather than snapped to the nearest frame, because the shooter
// was themselves looking at an interpolated world — the client draws peers
// between two snapshots (see the client's interpolation buffer), so rewinding
// to a single recorded tick would reconstruct a world nobody ever saw. The
// error that introduces is small and always in the same direction, which is
// worse than the arithmetic that removes it.
//
// An instant older than the window clamps to the oldest frame, and one in the
// future clamps to the newest. Both are honest answers: the alternative is
// fabricating a position, and a fabricated position is a hit registered against
// something that was never there.
func (h *history) at(instant time.Time) map[ref]Spot {
	if h.count == 0 {
		return nil
	}
	// Oldest first, in ring order.
	idx := func(i int) int {
		start := 0
		if h.count == len(h.frames) {
			start = h.next
		}
		return (start + i) % len(h.frames)
	}

	oldest := h.frames[idx(0)]
	newest := h.frames[idx(h.count-1)]
	if !instant.After(oldest.at) {
		return oldest.spots
	}
	if !instant.Before(newest.at) {
		return newest.spots
	}

	for i := 1; i < h.count; i++ {
		b := h.frames[idx(i)]
		if b.at.Before(instant) {
			continue
		}
		a := h.frames[idx(i-1)]
		span := b.at.Sub(a.at)
		if span <= 0 {
			return b.spots
		}
		t := float64(instant.Sub(a.at)) / float64(span)
		out := make(map[ref]Spot, len(b.spots))
		for id, bs := range b.spots {
			as, ok := a.spots[id]
			if !ok {
				// Present in the later frame only — it appeared during the gap,
				// so there is nothing to interpolate from and its later
				// position is the only one that was ever true.
				out[id] = bs
				continue
			}
			out[id] = Spot{
				Pos: Vec2{
					X: as.Pos.X + (bs.Pos.X-as.Pos.X)*t,
					Y: as.Pos.Y + (bs.Pos.Y-as.Pos.Y)*t,
				},
				// The sector and liveness are not interpolated — they are
				// discrete, and halfway between two rooms is not a room.
				Sector: bs.Sector,
				Alive:  as.Alive && bs.Alive,
			}
		}
		return out
	}
	return newest.spots
}

// RewindTo is the public face of the buffer: the world as a player with this
// much latency saw it.
//
// TWO TERMS, AND THEY ARE DIFFERENT THINGS. `rtt` is the WHOLE loop — how stale
// the newest frame in that player's browser already is by the time their answer
// reaches us — and InterpolationDelay is how much further into the past that
// browser deliberately draws everything it does not predict. Add them and you
// have the instant that was on their screen when they aimed.
//
// NEITHER IS HALVED, and the halving that used to be here was a real error
// rather than a conservative choice. World.Enqueue derives `rtt` from the
// snapshot tick the client echoes back, so it already counts the frame's journey
// out, the client's own think time and the answer's journey back — it is not a
// ping. Halving it rewound an honest shooter to a moment between what he saw and
// what we see, a world nobody was ever looking at: at a 150 ms loop that is
// 75 ms, or 0.37 m at WalkSpeed, about half a body width of "I hit that and it
// did not count".
//
// Clamped to RewindMax last, because `rtt` is ultimately derived from a number
// the client chooses, and that ceiling is what stops a liar being resolved
// against a world seconds old. The clamp is on the composition and not only on
// the sample: the two terms are added here, so this is the only place their sum
// exists.
func (w *World) RewindTo(now time.Time, rtt time.Duration) map[ref]Spot {
	if rtt < 0 {
		rtt = 0
	}
	rewind := rtt + InterpolationDelay
	if rewind > RewindMax {
		rewind = RewindMax
	}
	return w.history.at(now.Add(-rewind))
}
