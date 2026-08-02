package gamevanyadum

import (
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// The world is ONE заброшка, shared by everybody, and it lives entirely in
// memory.
//
// THERE IS ONE OF THESE FOR THE WHOLE PROCESS, not one per player. That is the
// load-bearing reversal of what this game shipped first: an arena per run meant
// two people were never in the same building, which is a single-player game with
// multiplayer netcode bolted to the side of it. The заброшка is now generated
// once, from one seed, and opening a socket is walking into it — the shape the
// sibling game arrived at independently and recorded as ADR-056.
//
// NOTHING ENDS, AND THERE IS NO OBJECTIVE. An occupant appears when their socket
// says hello and is taken out when their last connection has been gone past
// AbandonGrace; the pickups respawn; the match is infinite. The building itself
// is torn down only when it EMPTIES, and the next arrival generates a fresh
// seed — so nothing regenerates under anybody's feet and a level is never
// re-sent mid-session.
//
// Postgres is touched ONCE PER VISIT — one summary row, written when somebody's
// last connection has been gone past the grace — and never on a tick. That is
// what keeps the simulation clear of the rule that nothing in this project ticks
// durable state (ADR-038, and the package doc). The world is lost on restart,
// exactly as the hub's presence is, and that is accepted: what a restart costs
// is the building everybody was standing in, and the next hello builds another.

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

// AbandonGrace is how long an occupant stays in the building after their last
// connection has gone.
//
// It is not a disconnect timeout: a page reload, a tunnel, a phone locking and
// unlocking all take a few seconds, and emptying somebody's slot for any of them
// would make the game unplayable on a bus. What it protects against is the
// occupant nobody comes back to, who would otherwise stand in the заброшка for
// ever holding one of MaxOccupants places.
//
// Expiring it is what WRITES THE VISIT, and it is the only thing that does.
// There is no quit button and no ending, so leaving is exactly "my connections
// went away and did not come back". The length recorded is measured to the
// occupant's last seen connection rather than to this moment — the two minutes
// spent waiting for somebody who never returned are not two minutes they were
// here. See Occupant.Stayed.
const AbandonGrace = 2 * time.Minute

// Occupant is one player inside the world, together with everything about their
// CONNECTION rather than about the world: what they have sent, how much
// simulated time they have paid for, and how stale their view of us is.
type Occupant struct {
	// AccountID identifies them to the server; Pseudonym is what other players
	// are told. The two are deliberately different — a snapshot is addressed to
	// one player and names another, and an account id would be a durable handle
	// on a person handed to everybody who shares the building with them.
	AccountID string
	Pseudonym string

	State Player

	// JoinedAt is when this occupant walked in, and LastSeen is the last tick on
	// which the account still had a connection in the room.
	//
	// LastSeen is updated once a tick for everybody who is CONNECTED rather than
	// by Enqueue alone, because a player standing perfectly still sends nothing
	// at all and is still very much in the building.
	JoinedAt time.Time
	LastSeen time.Time

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
	// client is entitled to resend and the world would apply twice.
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

// Stayed is how long this occupant actually had a connection, in whole seconds,
// and it is the number a visit row records.
//
// Measured to LastSeen and never to the moment the grace expired: AbandonGrace
// is a tolerance for a flaky connection rather than time spent in the building,
// and counting it would add two minutes to every visit that ended the ordinary
// way — by somebody closing the tab.
func (o *Occupant) Stayed() int {
	d := o.LastSeen.Sub(o.JoinedAt)
	if d < 0 {
		return 0
	}
	return int(d / time.Second)
}

// World is the one заброшка everybody is standing in.
type World struct {
	// ID changes every time the building is regenerated, and it is the whole
	// mechanism that makes regenerating one safe: a client caches the level it
	// fetched over HTTP, the ready frame names the world it has been let into,
	// and a mismatch tells the client its geometry is of a building that no
	// longer exists. Nothing else has to be invalidated, because nothing else
	// about the world is cached.
	ID    uuid.UUID
	Level *Level

	occupants map[string]*Occupant

	// ready[i] is the TICK at which the pickup at index i is back on the floor,
	// and the whole of what "taken" means here. Zero is the resting state — the
	// world starts at tick zero, so `Tick >= ready[i]` is "it is lying there"
	// without a sentinel and without a countdown to drift.
	//
	// A DEADLINE RATHER THAN A COUNTDOWN, deliberately. Subtracting dt from a
	// float every tick accumulates the error of SimStep's binary expansion, so a
	// thing due back in exactly PickupRespawn would return a tick early or late
	// depending on how the rounding fell, and no test could say which. An
	// integer tick is exact, is the same number on every process replaying the
	// same world, and is one comparison instead of a loop.
	//
	// Indexed by POSITION in Level.Pickups, which is the same key the wire's
	// remaining-mask uses (Snapshot.Left). One keying, so the two cannot
	// disagree.
	ready []int64

	// history is the rewind buffer lag compensation resolves against. Recorded
	// every tick, consumed by the first thing that shoots — see history.go for
	// why the recorder cannot wait for the consumer.
	history *history

	Tick int64
}

// NewWorld generates a заброшка with nobody in it.
func NewWorld(id uuid.UUID, seed int64) *World {
	l := Generate(seed)
	return &World{
		ID:        id,
		Level:     l,
		occupants: make(map[string]*Occupant, MaxOccupants),
		ready:     make([]int64, len(l.Pickups)),
		history:   newHistory(),
	}
}

// Join puts somebody in the building, or hands back the occupant already here.
// It reports false when the заброшка is full, which is a refusal the player is
// told about rather than a silent no-op — see the hello.
//
// A SECOND HELLO IS A RECONNECT AND NOT A SECOND VISIT. A page reload, a tunnel
// or a phone waking up all produce one, and the occupant is still standing where
// he was: the only thing that changes is LastSeen, which is what stops the grace
// from expiring under a socket that has just come back.
func (w *World) Join(accountID, pseudonym string, now time.Time) (*Occupant, bool) {
	if o, ok := w.occupants[accountID]; ok {
		o.LastSeen = now
		return o, true
	}
	if len(w.occupants) >= MaxOccupants {
		return nil, false
	}
	o := &Occupant{
		AccountID: accountID,
		Pseudonym: pseudonym,
		State:     NewPlayer(w.Level),
		JoinedAt:  now,
		LastSeen:  now,
	}
	w.occupants[accountID] = o
	return o, true
}

// Occupant returns one player, or nil.
func (w *World) Occupant(accountID string) *Occupant { return w.occupants[accountID] }

// Occupants is how many people are in the building.
func (w *World) Occupants() int { return len(w.occupants) }

// Seen records that an account still has a connection. It is what AbandonGrace
// is measured against, and the service calls it once a tick for everybody who is
// connected — including the player standing perfectly still, who sends nothing
// at all.
func (w *World) Seen(accountID string, now time.Time) {
	if o, ok := w.occupants[accountID]; ok {
		o.LastSeen = now
	}
}

// Remove takes somebody out of the building WITHOUT producing a visit, and
// reports whether they were in it. It is the admin «забыть» path: a visit
// belonging to somebody who is being erased is not a result.
func (w *World) Remove(accountID string) bool {
	if _, ok := w.occupants[accountID]; !ok {
		return false
	}
	delete(w.occupants, accountID)
	return true
}

// keys is every occupant, sorted by account id. It is the STABLE order — the one
// the wire and the rewind buffer are filled from — and deliberately NOT the
// order they are simulated in; see turnOrder.
//
// NOTHING IN THIS WORLD LETS MAP ORDER DECIDE ANYTHING. Go randomises map order
// on every range, so an array built by ranging the occupants would come out
// reshuffled between two renders of an unchanged world: a golden test over the
// wire shape that flaps for no reason, and a client asked to re-key its
// bookkeeping every frame. The same coin flip inside the simulation would be
// worse still, because the same seed and the same input transcript would stop
// producing the same world — and that determinism is the property the whole
// simulation is tested against, Step being pure precisely so it can be.
func (w *World) keys() []string {
	out := make([]string, 0, len(w.occupants))
	for k := range w.occupants {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// turnOrder is the same occupants as keys, ROTATED BY THE TICK. Advance's step
// loop is its only caller, and the only thing in the package that sees an order
// other than keys'.
//
// TWO ORDERS, BECAUSE THEY ANSWER DIFFERENT QUESTIONS, and collapsing them back
// into one is the mistake this comment exists to prevent. The wire wants an
// order that does not MOVE, for the reasons on keys. The simulation wants an
// order that is not a PRIORITY: collect mutates world-wide state, so whoever
// steps first takes a contested bottle, and any one fixed order hands that to
// the same account for the life of both accounts. Sorted, it is whoever drew the
// lexicographically smaller UUID — every beer, and every hit test once something
// shoots. That is not a coin flip landing badly, it is a coin that never gets
// tossed.
//
// DETERMINISM DOES NOT REQUIRE A FIXED ORDER, only a derived one. The tick is
// part of the world's state, so rotating by it keeps the replay property intact
// — the same seed and the same transcript still produce the same world, frame
// for frame — while the advantage moves on by one occupant every 50 ms. Nothing
// fairer is available without inventing a notion of who deserves the bottle,
// which is a rule this game does not have.
//
// A building with one person in it returns the caller's own slice: there is
// nothing to rotate and no reason to allocate for it.
func (w *World) turnOrder(ids []string) []string {
	if len(ids) < 2 {
		return ids
	}
	off := int(w.Tick % int64(len(ids)))
	out := make([]string, len(ids))
	for i := range ids {
		out[i] = ids[(i+off)%len(ids)]
	}
	return out
}

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
func (w *World) Enqueue(accountID string, in *ParsedInput) {
	if in == nil {
		return
	}
	o := w.occupants[accountID]
	if o == nil {
		return
	}

	// Round trip, DERIVED rather than reported: the tick rate is fixed, so the
	// gap between the snapshot this client had drawn and where the simulation
	// is now is the whole loop. Smoothed with a slow exponential average — one
	// late frame is not a slower connection, and rewinding by a spike would
	// resolve a shot against a world nobody was ever looking at.
	//
	// Deriving it narrows the lie without ending it: a client cannot claim a
	// latency out of thin air, but it chooses which tick to echo, so it can
	// claim to be further behind than it is. Hence the ceiling, and hence
	// RewindMax rather than the ring's capacity — the ring is merely how much
	// past exists, where RewindMax is how much of it a shot may reach, and the
	// smoothed value here is one of the two terms that composition is built from
	// (history.go, RewindTo).
	if in.Seen > 0 && in.Seen <= w.Tick {
		sample := time.Duration(w.Tick-in.Seen) * SimStep
		if sample > RewindMax {
			sample = RewindMax
		}
		if o.rtt == 0 {
			o.rtt = sample
		} else {
			o.rtt = (o.rtt*3 + sample) / 4
		}
	}

	// Four frames' worth of the largest frame the parser will pass through:
	// enough that a burst after a hiccup is not thrown away, small enough that a
	// client which floods cannot make the world hold an unbounded slice. It is a
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
		// command behind it with it, silently and for ever. An unsanitised Dt
		// here is a LIVENESS bug and not merely a wrong distance, which is why
		// the clamp belongs at the queue's own edge rather than being left to
		// whatever happens to call Enqueue. «СИМУЛЯТОР ФИНТЕХА» sanitises at
		// exactly this boundary for the same reason
		// (internal/gamefintech/office.go).
		//
		// SEQ IS THE ONE ATTACKER-CONTROLLED FIELD NOTHING CLAMPS — not Sanitise,
		// not this loop, not anywhere — and that is conceded rather than
		// overlooked. A frame carrying q = MaxInt64 puts highSeq at MaxInt64,
		// after which every command that client sends is dropped as stale for as
		// long as they stay in the building.
		//
		// No bound is added because none would be honest. The counter advances
		// forty times a second and the world is not obliged to have seen every
		// step of it: a lost frame, a socket that dropped and came back, and the
		// surplus parseInput trims off an over-long frame all leave gaps, so a
		// legitimate distance of hundreds between the client's counter and the
		// world's mark is ordinary. A window tight enough to catch the attack is
		// therefore a number picked out of the air, and this project does not buy
		// those (CLAUDE.md, on complexity without a current requirement).
		//
		// What makes that affordable is that the damage is self-inflicted and
		// goes no further: highSeq lives on the Occupant, so the only input that
		// stops flowing is the sender's own — and he can already stop it by
		// sending nothing at all. An HONEST client that ends up behind a
		// high-water mark (a reload, a rebuilt socket) is recovered by the
		// client's own resume path: reconcile takes the server's ack as a floor
		// on its counter and counts on from there
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
func (w *World) RTT(accountID string) time.Duration {
	if o := w.occupants[accountID]; o != nil {
		return o.rtt
	}
	return 0
}

// Advance runs one simulation step for every occupant and returns whoever has
// been gone long enough to have left. Those are taken out of the world before it
// returns, so the caller only has to decide what to do with them — which is
// write the visit down, or, on the admin path, nothing at all.
//
// dt is the tick's own length, which is what the time budget accrues at — never
// a client's claim.
func (w *World) Advance(dt float64, now time.Time) []*Occupant {
	w.Tick++

	// The stable order, for the rewind buffer below, and the rotated one for the
	// step loop — never map order for either. collect mutates world-wide state,
	// so which occupant steps first decides who takes a contested pickup, and
	// that is exactly the decision that must not settle on one account for ever.
	// See keys and turnOrder.
	ids := w.keys()

	for _, id := range w.turnOrder(ids) {
		o := w.occupants[id]
		o.budget = math.Min(o.budget+dt, TimeBudgetCap)
		// A COMMAND IS SIMULATED WHOLE OR IT WAITS. It is never simulated in
		// part, because there is no way to acknowledge a part: the ack is one
		// sequence number, the client drops everything at or below it, and a
		// client that dropped a command the world had only half-run would keep
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
			o.State = Step(w.Level, o.State, c)
			// The queue is strictly ascending in Seq — Enqueue drops everything
			// at or below highSeq and the overflow trim takes from the front —
			// so this is the last command actually folded in, which is exactly
			// what the snapshot's Ack promises.
			o.State.LastSeq = c.Seq
		}
		w.collect(o)
	}

	// Recorded AFTER the step, so a frame describes the world as the snapshot
	// about to go out describes it. Recording before would leave the rewind
	// buffer a tick behind everything that reads it.
	w.history.record(w.Tick, now, w.spots(ids))

	// And whoever has stopped being here. In keys' order rather than the rotated
	// one: which occupants leave on a tick is not a contest, so the stable order
	// is the one that makes the visits queue in a defined sequence.
	var left []*Occupant
	for _, id := range ids {
		o := w.occupants[id]
		if now.Sub(o.LastSeen) <= AbandonGrace {
			continue
		}
		delete(w.occupants, id)
		left = append(left, o)
	}
	return left
}

// available reports whether the pickup at index i is lying on the floor right
// now, which is the one place the respawn deadline is interpreted.
func (w *World) available(i int) bool { return w.Tick >= w.ready[i] }

// collect picks up everything one occupant is standing on. There is no use
// button by design (content.go), so this is the whole of the interaction.
//
// Taking something does not remove it — it sets the tick it comes back on. In an
// infinite match with no objective, a заброшка that empties permanently is a
// building with nothing left to walk to.
func (w *World) collect(o *Occupant) {
	pf, ok := w.Level.FloorAt(o.State.Pos)
	if !ok {
		return
	}
	for i, p := range w.Level.Pickups {
		if !w.available(i) {
			continue
		}
		if math.Hypot(p.Pos.X-o.State.Pos.X, p.Pos.Y-o.State.Pos.Y) > PickupReach {
			continue
		}
		// Reach is measured on the floor plane, so without this a player could
		// collect something through a floor from the room below it.
		if math.Abs(w.Level.Sectors[p.Sector].FloorZ-pf) > MaxStep+1e-9 {
			continue
		}
		kind, known := PickupByKey(p.Kind)
		if !known {
			continue
		}
		w.ready[i] = w.Tick + pickupRespawnTicks
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

// spots is every entity's position this instant, in the shape the rewind buffer
// stores. Keyed by PSEUDONYM rather than by account, so a rewound world and a
// published one name the same things — which is what lets a future hit test
// take the id it was shot at straight from the client's aim.
//
// The occupant list arrives as an argument rather than being derived here, and
// it is keys' order rather than the rotated one — but neither choice changes the
// ANSWER, because a map does not remember what order it was filled in. It takes
// the caller's list so that the frame recorded for a tick is of exactly the
// occupants that tick stepped, rather than of a second reading of the roster.
func (w *World) spots(ids []string) map[string]Spot {
	out := make(map[string]Spot, len(ids))
	for _, id := range ids {
		o := w.occupants[id]
		out[o.Pseudonym] = Spot{Pos: o.State.Pos, Sector: o.State.Sector, Alive: o.State.Health > 0}
	}
	return out
}

// SnapshotFor renders the world from one occupant's point of view and clears
// the events they have been waiting for.
//
// Not a pure read, and deliberately: an event is delivered ONCE, on the next
// frame. A frame that re-sent it would replay the same sound forever, which is
// the failure that makes people mute a game.
//
// Everybody else in the building arrives as a PEER, to be drawn interpolated in
// the recent past — their intent cannot be predicted the way the reader's own
// can.
func (w *World) SnapshotFor(accountID string) (Snapshot, bool) {
	me := w.occupants[accountID]
	if me == nil {
		return Snapshot{}, false
	}

	// Bit i is set when the pickup at INDEX i is lying on the floor. The index
	// and not the id, because the mask's width is the level's length and an index
	// is dense by construction where an id need not be — see Snapshot.Left for
	// why the field is one 32-bit word rather than a list.
	//
	// A RESPAWN RIDES THIS AND NOTHING ELSE. The mask is idempotent full state,
	// so a bit coming back IS the announcement that a thing has returned, and a
	// client that wants to mark the moment compares it against the previous
	// frame. An event saying "something respawned" would be bytes on a payload
	// that repeats twenty times a second, per viewer, to say nothing at all
	// almost every time it was sent.
	var left uint32
	for i := range w.Level.Pickups {
		if w.available(i) {
			left |= 1 << uint(i)
		}
	}

	s := Snapshot{
		T:      TypeSnapshot,
		Tick:   w.Tick,
		Ack:    me.State.LastSeq,
		X:      cm(me.State.Pos.X),
		Y:      cm(me.State.Pos.Y),
		Z:      cm(EyeZ(w.Level, me.State)),
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
	// In account order, so two renders of an unchanged world produce the same
	// array. Map order here would make the peers array shuffle between frames,
	// which is a golden test that flaps and a client asked to re-key its
	// bookkeeping for no reason. See keys.
	for _, id := range w.keys() {
		if id == accountID {
			continue
		}
		o := w.occupants[id]
		state := 0
		if o.State.Health <= 0 {
			state = 2
		}
		s.Peers = append(s.Peers, Peer{
			ID:    o.Pseudonym,
			X:     cm(o.State.Pos.X),
			Y:     cm(o.State.Pos.Y),
			Z:     cm(EyeZ(w.Level, o.State)),
			Yaw:   mrad(o.State.Yaw),
			State: state,
		})
	}
	me.events = nil
	return s, true
}
