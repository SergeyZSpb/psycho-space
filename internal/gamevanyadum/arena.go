package gamevanyadum

import (
	"math"
	"time"

	"github.com/google/uuid"
)

// An arena is one run's world, and it lives entirely in memory.
//
// Nothing here is ever written to Postgres except the summary, once, when the
// run ends. That is what keeps the simulation tick clear of the rule that
// nothing in this project ticks durable state (ADR-038, and the package doc).
// An arena is lost on restart, exactly as the hub's presence is, and that is
// accepted: a run is a few minutes long and a lost one costs a replay.
//
// The arena is written to hold SEVERAL players from the first day even though
// this iteration never puts two people in one. Multiplayer is then more
// occupants of an arena rather than a different kind of object, which is the
// difference between an iteration and a rewrite.

// TimeBudgetCap is how much simulated time a player may bank.
//
// THIS IS THE SPEED-HACK GUARD, and it is the reason the arena counts seconds
// rather than commands. The socket allows ten frames a second and each may carry
// four sub-steps of up to MaxStepSeconds, so a client that fills every frame
// could ask for eight seconds of simulation per real second — running eight
// times faster than everybody else, with no single field out of range anywhere.
//
// So a player accrues a budget at exactly real time and spends it on the
// commands he sends. The cap exists because a phone that was backgrounded, or a
// wifi hiccup, delivers a burst that is honest and must be allowed to catch up;
// beyond half a second the burst stops being catch-up and starts being an
// advantage.
const TimeBudgetCap = 0.5

// Arena is one live run.
type Arena struct {
	RunID     uuid.UUID
	AccountID string
	Level     *Level
	StartedAt time.Time

	Player Player
	// budget is the unspent simulated time described on TimeBudgetCap.
	budget float64
	// pending are commands received but not yet stepped. They are drained on the
	// tick rather than applied on arrival, so the simulation advances on its own
	// clock and never on the client's read pump.
	pending []Command

	// Taken is which pickups are gone. Held beside the level rather than in it,
	// because the level is a pure function of the seed and stays that way.
	Taken map[int]bool

	Tick    int64
	Ended   bool
	Success bool

	// events accumulate between ticks and ride out on the next snapshot.
	events []Event
}

// NewArena starts a run: a level from the seed, a player at its spawn.
func NewArena(runID uuid.UUID, accountID string, seed int64, now time.Time) *Arena {
	l := Generate(seed)
	return &Arena{
		RunID:     runID,
		AccountID: accountID,
		Level:     l,
		StartedAt: now,
		Player:    NewPlayer(l),
		Taken:     map[int]bool{},
	}
}

// Enqueue accepts input from a client. It runs on the connection's read pump, so
// it does the least possible work: appending to a slice the tick will drain.
//
// Seq is remembered rather than checked. An out-of-order or duplicated frame is
// simply more input, and the budget above is what stops that mattering — which
// is a better guard than a sequence check, because a sequence check is trivially
// satisfied by a client that lies about it.
func (a *Arena) Enqueue(in *ParsedInput) {
	if a.Ended || in == nil {
		return
	}
	if in.Seq > a.Player.LastSeq {
		a.Player.LastSeq = in.Seq
	}
	// A hard ceiling on the queue, so a client that sends while the tick is
	// stalled cannot grow this without bound. Dropping the oldest is right:
	// stale input is the input least worth simulating.
	const maxPending = 4 * MaxCommandsPerFrame
	a.pending = append(a.pending, in.Cmds...)
	if len(a.pending) > maxPending {
		a.pending = a.pending[len(a.pending)-maxPending:]
	}
}

// Advance runs one simulation step. dt is the tick's own length, which is what
// the time budget accrues at — never the client's claim.
func (a *Arena) Advance(dt float64, now time.Time) {
	if a.Ended {
		return
	}
	a.Tick++
	a.budget = math.Min(a.budget+dt, TimeBudgetCap)

	for len(a.pending) > 0 && a.budget > 0 {
		c := a.pending[0]
		a.pending = a.pending[1:]
		// Spend no more than has actually elapsed. A command longer than the
		// remaining budget is simulated for as much of itself as the player has
		// paid for, rather than being dropped — dropping it would make a
		// laggy client's movement stutter, where truncating it merely makes it
		// slower for one step.
		if c.Dt > a.budget {
			c.Dt = a.budget
		}
		a.budget -= c.Dt
		a.Player = Step(a.Level, a.Player, c)
	}

	a.collect()

	// The objective of this iteration, and deliberately the smallest one that
	// closes the loop: collect every beer in the заброшка. It exists so that a
	// run can END — which is what exercises the only two database writes this
	// game has — and it is replaced by the locked door and the exit as soon as
	// there are keys to find.
	if !a.Ended && a.remaining() == 0 && len(a.Level.Pickups) > 0 {
		a.Ended, a.Success = true, true
	}
	_ = now
}

// collect picks up everything the player is standing on. There is no use button
// by design (content.go), so this is the whole of the interaction.
func (a *Arena) collect() {
	pf, ok := a.Level.FloorAt(a.Player.Pos)
	if !ok {
		return
	}
	for _, p := range a.Level.Pickups {
		if a.Taken[p.ID] {
			continue
		}
		if math.Hypot(p.Pos.X-a.Player.Pos.X, p.Pos.Y-a.Player.Pos.Y) > PickupReach {
			continue
		}
		// Reach is measured on the floor plane, so without this a player could
		// collect something through a floor from the room below it.
		if math.Abs(a.Level.Sectors[p.Sector].FloorZ-pf) > MaxStep+1e-9 {
			continue
		}
		kind, known := PickupByKey(p.Kind)
		if !known {
			continue
		}
		a.Taken[p.ID] = true
		if a.Player.Counters == nil {
			a.Player.Counters = map[string]int{}
		}
		v := a.Player.Counters[kind.Grants] + kind.Amount
		if kind.Max > 0 && v > kind.Max {
			v = kind.Max
		}
		a.Player.Counters[kind.Grants] = v
		a.events = append(a.events, Event{E: EventPickup, K: p.Kind, ID: p.ID})
	}
}

// remaining counts the pickups still lying about.
func (a *Arena) remaining() int {
	n := 0
	for _, p := range a.Level.Pickups {
		if !a.Taken[p.ID] {
			n++
		}
	}
	return n
}

// SnapshotFrame renders the arena for its player and clears the pending events,
// which is why it is not a pure read: an event is delivered once, on the next
// frame, and a frame that re-sent it would replay every sound forever.
func (a *Arena) SnapshotFrame() Snapshot {
	left := make([]int, 0, len(a.Level.Pickups))
	for _, p := range a.Level.Pickups {
		if !a.Taken[p.ID] {
			left = append(left, p.ID)
		}
	}
	s := Snapshot{
		T:      TypeSnapshot,
		Tick:   a.Tick,
		Ack:    a.Player.LastSeq,
		X:      cm(a.Player.Pos.X),
		Y:      cm(a.Player.Pos.Y),
		Z:      cm(EyeZ(a.Level, a.Player)),
		Yaw:    mrad(a.Player.Yaw),
		Sector: a.Player.Sector,
		Health: a.Player.Health,
		Left:   left,
		Events: a.events,
	}
	if len(a.Player.Counters) > 0 {
		s.Bag = make(map[string]int, len(a.Player.Counters))
		for k, v := range a.Player.Counters {
			s.Bag[k] = v
		}
	}
	a.events = nil
	return s
}

// Elapsed is how long the run has been going, in whole seconds.
func (a *Arena) Elapsed(now time.Time) int {
	d := now.Sub(a.StartedAt)
	if d < 0 {
		return 0
	}
	return int(d / time.Second)
}
