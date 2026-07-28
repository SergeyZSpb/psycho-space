package gamevanyadum

import (
	"math"
	"time"

	"github.com/google/uuid"
)

// An arena is one run's world, it lives entirely in memory, and it holds
// SEVERAL OCCUPANTS.
//
// Today every arena has exactly one, because nothing yet puts two people in a
// заброшка together. It is written this way anyway, and that is a decision
// rather than speculation: multiplayer then means *filling a map that already
// exists* rather than turning a field into a collection — which would change
// every method's shape, the snapshot's shape and the tests' shape at exactly
// the moment there is finally something at stake. See ADR-052 on what "ready
// for multiplayer" is claimed to mean.
//
// Nothing here is ever written to Postgres except the summary, once, when the
// run ends. That is what keeps the simulation tick clear of the rule that
// nothing in this project ticks durable state (ADR-038, and the package doc).
// An arena is lost on restart, exactly as the hub's presence is, and that is
// accepted: a run is a few minutes long and a lost one costs a replay.

// TimeBudgetCap is how much simulated time a player may bank.
//
// THIS IS THE SPEED-HACK GUARD, and it is per occupant. The socket allows ten
// frames a second and each may carry several sub-steps of up to MaxStepSeconds,
// so a client that filled every frame could ask for seconds of simulation per
// real second — running faster than everybody else, with no single field out of
// range anywhere.
//
// So an occupant accrues a budget at exactly real time and spends it on the
// commands they send. The cap exists because a phone that was backgrounded, or
// a wifi hiccup, delivers a burst that is honest and must be allowed to catch
// up; beyond half a second the burst stops being catch-up and starts being an
// advantage.
const TimeBudgetCap = 0.5

// Occupant is one player inside an arena, together with everything about their
// CONNECTION rather than about the world: what they have sent, how much
// simulated time they have paid for, and how stale their view of us is.
type Occupant struct {
	// AccountID identifies them to the server; Pseudonym is what other players
	// are told. The two are deliberately different — a snapshot is addressed to
	// one player and names another, and an account id would be a durable handle
	// on a person handed to everybody who shares an arena with them.
	AccountID string
	Pseudonym string

	State Player

	// budget is the unspent simulated time described on TimeBudgetCap.
	budget float64
	// pending are commands received but not yet stepped. Drained on the tick
	// rather than applied on arrival, so the simulation advances on its own
	// clock and never on a client's read pump.
	pending []Command
	// rtt is the smoothed round trip derived from the snapshot ticks this
	// client echoes back. Lag compensation rewinds by it.
	rtt time.Duration
	// events accumulate between ticks and ride out on this occupant's next
	// snapshot. Per occupant, because "you picked up a beer" is addressed to
	// the person who picked it up.
	events []Event
}

// Arena is one live run.
type Arena struct {
	RunID uuid.UUID
	// AccountID is whose run this is — the owner, and the account the summary
	// row is written against. With more than one occupant the others are
	// guests; who a shared run is *recorded* for is a question for the
	// iteration that introduces one.
	AccountID string
	Level     *Level
	StartedAt time.Time

	occupants map[string]*Occupant

	// Taken is which pickups are gone. Held beside the level rather than in it,
	// because the level is a pure function of the seed and stays that way. It
	// is arena-wide: the world is shared even when the players are not.
	Taken map[int]bool

	// history is the rewind buffer lag compensation resolves against. Recorded
	// every tick, consumed by the first thing that shoots — see history.go for
	// why the recorder cannot wait for the consumer.
	history *history

	Tick    int64
	Ended   bool
	Success bool
}

// NewArena starts a run with one occupant at the level's spawn.
func NewArena(runID uuid.UUID, accountID, pseudonym string, seed int64, now time.Time) *Arena {
	l := Generate(seed)
	a := &Arena{
		RunID:     runID,
		AccountID: accountID,
		Level:     l,
		StartedAt: now,
		occupants: make(map[string]*Occupant, 1),
		Taken:     map[int]bool{},
		history:   newHistory(),
	}
	a.Join(accountID, pseudonym)
	return a
}

// Join adds an occupant at the spawn, or returns the one already here.
func (a *Arena) Join(accountID, pseudonym string) *Occupant {
	if o, ok := a.occupants[accountID]; ok {
		return o
	}
	o := &Occupant{AccountID: accountID, Pseudonym: pseudonym, State: NewPlayer(a.Level)}
	a.occupants[accountID] = o
	return o
}

// Occupant returns one player, or nil.
func (a *Arena) Occupant(accountID string) *Occupant { return a.occupants[accountID] }

// Occupants is how many people are in this arena.
func (a *Arena) Occupants() int { return len(a.occupants) }

// Owner is the arena's own occupant — the account the run belongs to, and the
// convenient handle for the single-occupant case the game has today.
func (a *Arena) Owner() *Occupant { return a.occupants[a.AccountID] }

// Enqueue accepts input from one occupant. It runs on that connection's read
// pump, so it does the least possible work: appending to a slice the tick will
// drain.
//
// COMMANDS ALREADY APPLIED ARE DROPPED HERE, by sequence number. That single
// rule is what makes input redundancy free — a client may resend the tail of
// its unacknowledged commands in every frame, so one lost packet costs no input
// at all, and a client that resends everything forever gains nothing because
// the second copy never reaches the queue.
//
// It is also why a sequence check is worth having here where the yard's
// equivalent deliberately has none: without prediction a duplicate is merely
// more input, but with it a replayed command is movement that happens twice on
// the server and once on the client, and the player feels it as being dragged.
func (a *Arena) Enqueue(accountID string, in *ParsedInput) {
	if a.Ended || in == nil {
		return
	}
	o := a.occupants[accountID]
	if o == nil {
		return
	}

	// Round trip, DERIVED rather than reported: the tick rate is fixed, so the
	// gap between the snapshot this client had drawn and where the simulation
	// is now is the whole loop. Smoothed with a slow exponential average — one
	// late frame is not a slower connection, and rewinding by a spike would
	// resolve a shot against a world nobody was ever looking at. Deriving it
	// also means a client cannot claim a latency it does not have, which
	// matters precisely because lag compensation rewinds by this number.
	if in.Seen > 0 && in.Seen <= a.Tick {
		sample := time.Duration(a.Tick-in.Seen) * SimStep
		if sample > HistoryWindow {
			sample = HistoryWindow
		}
		if o.rtt == 0 {
			o.rtt = sample
		} else {
			o.rtt = (o.rtt*3 + sample) / 4
		}
	}

	const maxPending = 4 * (MaxCommandsPerFrame + RedundantCommands)
	for _, c := range in.Cmds {
		if c.Seq <= o.State.LastSeq {
			continue // already applied, or a redundant copy of one
		}
		o.pending = append(o.pending, c)
	}
	if len(o.pending) > maxPending {
		o.pending = o.pending[len(o.pending)-maxPending:]
	}
}

// RTT is one occupant's smoothed round trip.
func (a *Arena) RTT(accountID string) time.Duration {
	if o := a.occupants[accountID]; o != nil {
		return o.rtt
	}
	return 0
}

// Advance runs one simulation step for every occupant. dt is the tick's own
// length, which is what the time budget accrues at — never a client's claim.
func (a *Arena) Advance(dt float64, now time.Time) {
	if a.Ended {
		return
	}
	a.Tick++

	for _, o := range a.occupants {
		o.budget = math.Min(o.budget+dt, TimeBudgetCap)
		for len(o.pending) > 0 && o.budget > 0 {
			c := o.pending[0]
			o.pending = o.pending[1:]
			// Spend no more than has actually elapsed. A command longer than
			// the remaining budget is simulated for as much of itself as the
			// player has paid for, rather than being dropped — dropping it
			// would make a laggy client stutter, where truncating merely makes
			// it slower for one step.
			if c.Dt > o.budget {
				c.Dt = o.budget
			}
			o.budget -= c.Dt
			o.State = Step(a.Level, o.State, c)
			// Acknowledged only once it has ACTUALLY been folded in, so a
			// command truncated by the budget is not reported as applied — the
			// client would drop it from its pending list and its prediction
			// would drift permanently by whatever the server declined to
			// simulate.
			if c.Seq > o.State.LastSeq {
				o.State.LastSeq = c.Seq
			}
		}
		a.collect(o)
	}

	// Recorded AFTER the step, so a frame describes the world as the snapshot
	// about to go out describes it. Recording before would leave the rewind
	// buffer a tick behind everything that reads it.
	a.history.record(a.Tick, now, a.spots())

	// The objective of this iteration, and deliberately the smallest one that
	// closes the loop: collect every beer in the заброшка. It exists so a run
	// can END — which is what exercises the only two database writes this game
	// makes — and it is replaced by the locked door and the exit as soon as
	// there are keys to find.
	if !a.Ended && a.remaining() == 0 && len(a.Level.Pickups) > 0 {
		a.Ended, a.Success = true, true
	}
}

// collect picks up everything one occupant is standing on. There is no use
// button by design (content.go), so this is the whole of the interaction.
func (a *Arena) collect(o *Occupant) {
	pf, ok := a.Level.FloorAt(o.State.Pos)
	if !ok {
		return
	}
	for _, p := range a.Level.Pickups {
		if a.Taken[p.ID] {
			continue
		}
		if math.Hypot(p.Pos.X-o.State.Pos.X, p.Pos.Y-o.State.Pos.Y) > PickupReach {
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
		if o.State.Counters == nil {
			o.State.Counters = map[string]int{}
		}
		v := o.State.Counters[kind.Grants] + kind.Amount
		if kind.Max > 0 && v > kind.Max {
			v = kind.Max
		}
		o.State.Counters[kind.Grants] = v
		o.events = append(o.events, Event{E: EventPickup, K: p.Kind, ID: p.ID})
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

// spots is every entity's position this instant, in the shape the rewind buffer
// stores. Keyed by PSEUDONYM rather than by account, so a rewound world and a
// published one name the same things — which is what lets a future hit test
// take the id it was shot at straight from the client's aim.
func (a *Arena) spots() map[string]Spot {
	out := make(map[string]Spot, len(a.occupants))
	for _, o := range a.occupants {
		out[o.Pseudonym] = Spot{Pos: o.State.Pos, Sector: o.State.Sector, Alive: o.State.Health > 0}
	}
	return out
}

// SnapshotFor renders the arena from one occupant's point of view and clears
// the events they have been waiting for.
//
// Not a pure read, and deliberately: an event is delivered ONCE, on the next
// frame. A frame that re-sent it would replay the same sound forever, which is
// the failure that makes people mute a game.
//
// Everybody else in the arena arrives as a PEER, to be drawn interpolated in
// the recent past — their intent cannot be predicted the way the reader's own
// can. The array is empty in every arena today and is on the wire anyway; see
// its comment on Snapshot.
func (a *Arena) SnapshotFor(accountID string) (Snapshot, bool) {
	me := a.occupants[accountID]
	if me == nil {
		return Snapshot{}, false
	}

	left := make([]int, 0, len(a.Level.Pickups))
	for _, p := range a.Level.Pickups {
		if !a.Taken[p.ID] {
			left = append(left, p.ID)
		}
	}

	s := Snapshot{
		T:      TypeSnapshot,
		Tick:   a.Tick,
		Ack:    me.State.LastSeq,
		X:      cm(me.State.Pos.X),
		Y:      cm(me.State.Pos.Y),
		Z:      cm(EyeZ(a.Level, me.State)),
		Yaw:    mrad(me.State.Yaw),
		Sector: me.State.Sector,
		Health: me.State.Health,
		Left:   left,
		Events: me.events,
	}
	if len(me.State.Counters) > 0 {
		s.Bag = make(map[string]int, len(me.State.Counters))
		for k, v := range me.State.Counters {
			s.Bag[k] = v
		}
	}
	for id, o := range a.occupants {
		if id == accountID {
			continue
		}
		state := 0
		if o.State.Health <= 0 {
			state = 2
		}
		s.Peers = append(s.Peers, Peer{
			ID:    o.Pseudonym,
			X:     cm(o.State.Pos.X),
			Y:     cm(o.State.Pos.Y),
			Z:     cm(EyeZ(a.Level, o.State)),
			Yaw:   mrad(o.State.Yaw),
			State: state,
		})
	}
	me.events = nil
	return s, true
}

// Elapsed is how long the run has been going, in whole seconds.
func (a *Arena) Elapsed(now time.Time) int {
	d := now.Sub(a.StartedAt)
	if d < 0 {
		return 0
	}
	return int(d / time.Second)
}
