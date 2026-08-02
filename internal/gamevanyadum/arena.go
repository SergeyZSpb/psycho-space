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
	// highSeq is the highest sequence ever ACCEPTED INTO THE QUEUE. It is a
	// different number from State.LastSeq — the last one actually SIMULATED —
	// and it is what input redundancy is deduplicated against.
	//
	// THE DIFFERENCE BETWEEN THE TWO IS THE WHOLE CORRECTNESS OF THE REDUNDANCY
	// WINDOW. A client repeats the tail of its unacknowledged commands in every
	// frame so that one lost packet costs no input, and that is only free while
	// a repeat is dropped. Deduplicating on LastSeq alone drops the repeats of
	// commands already simulated and accepts the repeats of commands still
	// WAITING — and a frame carries four sub-steps of 25 ms where one 50 ms tick
	// affords two, so a queue that has fallen behind at all holds a tail the
	// client is entitled to resend and the arena would apply twice.
	//
	// It also compounds, because the client's demand is exactly what the budget
	// accrues: forty sub-steps of 25 ms a second against a budget that fills at
	// real time. A duplicate doubles the demand for that instant, so the queue
	// grows, so more of it is unapplied when the next frame lands, so the whole
	// redundancy window duplicates rather than the tail of it — and nothing
	// clears the backlog until the player stands still and stops sending. The
	// player is dragged forward while walking and keeps walking after he lets
	// go.
	//
	// «СИМУЛЯТОР ФИНТЕХА» is where this defect was first found. The number worth
	// quoting here is this game's own, measured by running a control against the
	// pre-change code and pinned by TestEightSubStepsWalkExactlyEightSubSteps:
	// 1.25 m walked where 1.00 m was asked for, a 25 % overshoot. How far too far
	// a player walks depends on how deep the queue had grown when the repeat
	// landed, which is why no two measurements of it agree.
	//
	// The overflow trim in Enqueue takes from the FRONT, so a trimmed sequence
	// stays below highSeq for ever: the queue is strictly ascending in Seq, and
	// a command dropped for being stale can never be re-accepted by a resend.
	// That is a real cost — trimmed input is now lost permanently, where
	// deduplicating on LastSeq would have let the client's next repeat restore it
	// — and it is the right trade, because the trim fires only after roughly a
	// second in which nothing drained, and re-admitting movement that stale
	// teleports a player sideways in front of everyone watching him. Pinned by
	// TestTrimmedInputIsNotRestoredByAResend.
	highSeq int64
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
// COMMANDS ALREADY SEEN ARE DROPPED HERE, by sequence number. That single rule
// is what makes input redundancy free — a client may resend the tail of its
// unacknowledged commands in every frame, so one lost packet costs no input at
// all, and a client that resends everything forever gains nothing because the
// second copy never reaches the queue.
//
// "SEEN" IS highSeq AND NOT State.LastSeq, and the difference is the whole of
// the rule's correctness — see the field. A command sitting in the queue has
// been seen and not applied; deduplicating against what has been APPLIED lets
// every one of those through a second time, which is the one thing redundancy
// is not allowed to cost.
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

	// Four frames' worth of the largest frame the parser will pass through:
	// enough that a burst after a hiccup is not thrown away, small enough that a
	// client which floods cannot make the arena hold an unbounded slice. It is a
	// bound on MEMORY and not on simulation — what bounds how much of the queue
	// is actually stepped is the time budget. The oldest go first, because stale
	// input is the input least worth simulating.
	const maxPending = 4 * (MaxCommandsPerFrame + RedundantCommands)
	for _, c := range in.Cmds {
		if c.Seq <= o.highSeq {
			continue // already applied, still queued, or trimmed for being stale
		}
		o.highSeq = c.Seq
		// ENQUEUE IS THE BOUNDARY AT WHICH A COMMAND'S SIMULATED FIELDS BECOME
		// TRUSTWORTHY — Dt, the movement axes and the look angles, which is
		// exactly what Sanitise clamps and no more. Step clamps them too, but
		// that guards the SIMULATION and does nothing for this queue: the drain
		// loop tests affordability on Dt directly, and NaN <= x is false for
		// every x, so one NaN — or a +Inf, or a dt of a thousand seconds — at the
		// head would wait for a budget that can never arrive and take every
		// command behind it with it, silently and for the rest of the run. An
		// unsanitised Dt here is a LIVENESS bug and not merely a wrong distance,
		// which is why the clamp belongs at the queue's own edge rather than
		// being left to whatever happens to call Enqueue. «СИМУЛЯТОР ФИНТЕХА»
		// sanitises at exactly this boundary for the same reason
		// (internal/gamefintech/office.go).
		//
		// SEQ IS THE ONE ATTACKER-CONTROLLED FIELD NOTHING CLAMPS — not Sanitise,
		// not this loop, not anywhere — and that is conceded rather than
		// overlooked. A frame carrying q = MaxInt64 puts highSeq at MaxInt64,
		// after which every command that client sends is dropped as stale for the
		// rest of the run.
		//
		// No bound is added because none would be honest. The counter advances
		// forty times a second and the arena is not obliged to have seen every
		// step of it: a lost frame, a socket that dropped and came back, and the
		// surplus parseInput trims off an over-long frame all leave gaps, so a
		// legitimate distance of hundreds between the client's counter and the
		// arena's mark is ordinary. A window tight enough to catch the attack is
		// therefore a number picked out of the air, and this project does not buy
		// those (CLAUDE.md, on complexity without a current requirement).
		//
		// What makes that affordable is that the damage is self-inflicted and
		// goes no further: highSeq lives on the Occupant, so the only input that
		// stops flowing is the sender's own — and he can already stop it by
		// sending nothing at all. An HONEST client that ends up behind a
		// high-water mark (a reload, a rebuilt socket, a run resumed in progress)
		// is recovered by the client's own resume path: reconcile takes the
		// server's ack as a floor on its counter and counts on from there
		// (web/src/lib/vanyadumPredict.ts).
		//
		// THREE PLACES SANITISE AND NONE OF THEM IS REDUNDANT, because each owes
		// a guarantee the other two cannot. parseInput clamps because it is the
		// WIRE boundary and hands out a ParsedInput its callers are entitled to
		// treat as in range. This loop clamps because Enqueue is EXPORTED and
		// takes a ParsedInput anybody may construct, so the liveness argument
		// above has to hold for whatever built it and not for the parser alone.
		// Step clamps because it is PURE and is called directly — by this
		// package's own simulation tests and by the golden conformance vectors —
		// with commands that passed through neither of the other two. Sanitise is
		// idempotent and costs a handful of comparisons, so three independent
		// guarantees are had for nothing.
		o.pending = append(o.pending, c.Sanitise())
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
		// A COMMAND IS SIMULATED WHOLE OR IT WAITS. It is never simulated in
		// part, because there is no way to acknowledge a part: the ack is one
		// sequence number, the client drops everything at or below it, and a
		// client that dropped a command the arena had only half-run would keep
		// the whole of it in its own prediction for ever. That is a permanent
		// divergence, and in a shooter it is the expensive kind — the player is
		// drawn further down the corridor than the simulation has him, so he
		// slides along a wall the server says he already cleared and every step
		// afterwards is resolved against different geometry.
		//
		// The earlier version truncated the boundary command to the remaining
		// budget and acknowledged it in full anyway, against a comment saying it
		// did not. Waiting costs one tick of stutter to a client that is already
		// behind; truncating cost silent, unrecoverable drift every time it
		// happened.
		//
		// It cannot starve, and exactly one thing guarantees that: Enqueue clamps
		// every command to MaxStepSeconds on the way INTO the queue. The clamp
		// inside Step carries none of this argument — a command that fails the
		// test below never reaches Step at all — so the queue's own boundary is
		// the whole of it. Given that clamp, the largest Dt that can ever be
		// waiting is MaxStepSeconds, the budget banks up to the strictly larger
		// TimeBudgetCap, and so the head of the queue becomes affordable within a
		// bounded number of ticks. A test pins that relationship, because
		// retuning one past the other would freeze an occupant for ever rather
		// than fail anything.
		for len(o.pending) > 0 && o.pending[0].Dt <= o.budget {
			c := o.pending[0]
			o.pending = o.pending[1:]
			o.budget -= c.Dt
			o.State = Step(a.Level, o.State, c)
			// The queue is strictly ascending in Seq — Enqueue drops everything
			// at or below highSeq and the overflow trim takes from the front —
			// so this is the last command actually folded in, which is exactly
			// what the snapshot's Ack promises.
			o.State.LastSeq = c.Seq
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
