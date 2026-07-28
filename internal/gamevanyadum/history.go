package gamevanyadum

import "time"

// The rewind buffer — the server's memory of what the world used to look like.
//
// WHY IT EXISTS BEFORE ANYTHING SHOOTS. Lag compensation resolves a shot
// against the world **as the shooter actually saw it**, which is roughly
// `now − RTT/2 − InterpolationDelay` ago. That is the only way a player who
// aimed at a moving target and was right does not miss; without it everybody
// learns to lead by a body width, which is a game teaching you to play around
// its netcode.
//
// The consumer — hitscan — arrives with the shotgun. **The recorder cannot**,
// and that asymmetry is the whole reason this file is here now: a ring buffer
// of the past is the one piece of a netcode stack that genuinely cannot be
// retrofitted, because on the day you want it the past has already happened.
// See ADR-052.
//
// It is memory-only and per-arena, like everything else the simulation owns, so
// it neither persists nor ticks anything durable.

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
	// identity the snapshot publishes.
	spots map[string]Spot
}

// Spot is one entity's place in the world at one instant.
type Spot struct {
	Pos    Vec2
	Sector int
	// Alive is recorded so a rewind cannot resurrect a corpse to be hit.
	Alive bool
}

// history is a fixed-size ring of recent frames.
//
// A ring rather than a slice that grows: the window is bounded by wall-clock
// (HistoryWindow) and the tick rate is fixed, so the capacity is known exactly
// and the whole structure allocates once, at arena creation, and never again.
// A simulation loop that allocates twenty times a second per arena is a
// self-inflicted garbage problem.
type history struct {
	frames []historyFrame
	// next is where the following record goes; the ring is full once it has
	// wrapped, which `count` reports.
	next  int
	count int
}

// historyCapacity is how many frames the window holds at the simulation rate.
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
func (h *history) record(tick int64, at time.Time, spots map[string]Spot) {
	f := &h.frames[h.next]
	if f.spots == nil {
		f.spots = make(map[string]Spot, len(spots)+2)
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
func (h *history) at(instant time.Time) map[string]Spot {
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
		out := make(map[string]Spot, len(b.spots))
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
// `rtt` is that player's measured round trip. Half of it is the one-way trip
// their view is behind by, and InterpolationDelay is how far behind that their
// client deliberately draws peers — so together they are exactly how stale the
// picture they aimed at was.
func (a *Arena) RewindTo(now time.Time, rtt time.Duration) map[string]Spot {
	if rtt < 0 {
		rtt = 0
	}
	if rtt > HistoryWindow {
		rtt = HistoryWindow
	}
	return a.history.at(now.Add(-(rtt/2 + InterpolationDelay)))
}
