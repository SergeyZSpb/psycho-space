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

	// Slot is the place in the building this occupant holds, and it is what the
	// WIRE calls them: a snapshot addresses a peer by this number rather than by
	// the pseudonym, which is 19 constant bytes a repeating frame has no business
	// carrying. The standings frame publishes the pairing.
	//
	// Stable for as long as they are in the building, and handed to somebody else
	// once they are not. See World.slots.
	Slot int

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

	// firedOn is the tick on which this occupant's gun last went off, and it is
	// what puts Peer.Fired on everybody else's frame for exactly that tick.
	//
	// A TICK NUMBER RATHER THAN A FLAG THAT IS CLEARED WHEN READ, and the
	// difference is the whole correctness of it: SnapshotFor runs once per
	// VIEWER, so a flag consumed by the first reader would show the shot to one
	// of the four people watching and hide it from the other three. Events get
	// away with being consumed because an event belongs to one person; this
	// belongs to the building. Comparing against World.Tick is idempotent for
	// every viewer and needs no reset — the tick moves on by itself.
	//
	// NOT ON Player, by ADR-058's test: Step never reads it, so the client never
	// has to simulate it, so it costs no port, no golden vector and no reconcile
	// spread. The client draws its OWN muzzle flash from the barrel count falling
	// (web/src/lib/vanyadumPredict.ts, `raw`), which is a value it already has.
	firedOn int64

	// collected is everything this occupant has picked up during this visit,
	// keyed exactly as the bag is, and it is what the visit row records.
	//
	// IT IS NOT THE BAG, and it stopped being the same number the day the gun
	// arrived. Until then nothing was ever spent, so what somebody was carrying
	// when they left was what they had found — and the visit row simply read the
	// bag. Now a reload takes a bottle out of it, so a player who actually used
	// the building's beer would have his visit recorded as zero, which is the
	// opposite of what the column means (migrations/015, and that file is
	// immutable, so the code is what has to agree with it).
	//
	// ON THE OCCUPANT AND NOT ON Player, by ADR-058's test read the easy way:
	// Step never looks at it, so the client never has to simulate it, so it costs
	// no port, no golden vector and no reconcile spread.
	//
	// Keyed rather than counted, because the loop that fills it is generic over
	// the catalogue — `collected[kind.Grants] += n` is exactly as general as the
	// line above it, where a plain int would put the word "beer" inside the
	// world's collection loop.
	collected map[string]int
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

// firedThisTick reports whether this occupant's gun went off on the tick the
// world is currently standing on.
//
// THE ZERO VALUE IS "NEVER", and that has to be checked rather than assumed
// because the world's own counter also starts at zero. SnapshotFor is callable
// on a world nothing has advanced yet — a client that attaches between two ticks
// on a building that has just been generated — and a bare equality would put a
// muzzle flash on every peer in it for that one frame. A shot is only ever
// recorded from inside Advance, which increments the tick before it steps
// anybody, so a real firedOn is always at least 1.
func (o *Occupant) firedThisTick(tick int64) bool {
	return o.firedOn != 0 && o.firedOn == tick
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

	// slots[i] is the account holding place i in the building, and an empty
	// string is a place nobody is standing in.
	//
	// IT IS THE WIRE'S ADDRESSING. A snapshot names a peer by their index here,
	// because a place in a building of MaxOccupants is one digit where the
	// pseudonym it replaced was twelve characters — 13 bytes of every entry, on a
	// frame that repeats twenty times a second (message.go, Peer). The standings
	// frame publishes which handle currently answers to which index.
	//
	// IT IS ALSO THE CAPACITY, which is why nothing counts occupants to decide
	// whether somebody may come in: the заброшка is full exactly when there is no
	// empty string left, so there is no second number to fall out of step with
	// this one.
	//
	// THIS TABLE AND THE OCCUPANT MAP ARE WRITTEN TOGETHER OR NOT AT ALL — Join
	// fills both, release empties both, and nothing else touches either. Every
	// read below relies on that: a slot holding an account the map does not have
	// would be a nil dereference on the tick, and it is kept impossible rather
	// than guarded against in the half-dozen places that would each have to.
	//
	// A SLOT IS REUSED once its holder has gone, which is what makes it small
	// enough to be worth sending — and what obliges the standings to say whose it
	// is now. See RosterVersion for how a client is stopped from interpolating
	// the newcomer from where the last holder was standing.
	slots [MaxOccupants]string

	// roster advances whenever somebody joins or leaves. See RosterVersion.
	roster int64

	// visible is the potentially-visible set, precomputed from the sector graph
	// when the building was generated: visible[a][b] is whether somebody in
	// sector b is drawn by somebody in sector a. See buildVisibility (level.go)
	// for what it approximates, and canSee for how it is read.
	visible [][]bool

	// lastVisible[a][b] is the last TICK on which the occupant holding place b
	// was inside the potentially-visible set of the occupant holding place a, and
	// it is the whole of the hysteresis described on visibleHold: a peer is sent
	// while he is visible now or was visible recently enough.
	//
	// A TICK RATHER THAN A COUNTDOWN, for the reason the respawn deadline is one:
	// an integer tick is exact, is the same number on every process replaying the
	// same world, and needs no per-tick decrement. Zero is "never" and cannot
	// collide with a real recording, because Advance increments before it records
	// and so the first tick anything is written on is one.
	//
	// SYMMETRIC BY CONSTRUCTION, because canSee is and both entries of a pair are
	// written on the same tick. That is what stops the hold from creating a man
	// who can see somebody who cannot see him — the property a hit test will rest
	// on the day something shoots.
	//
	// CLEARED WHEN A PLACE IS FREED (release), so a newcomer never inherits the
	// last holder's memory. Keyed by slot for the same reason the wire is: it is
	// a fixed-width array rather than a map, so nothing allocates on a tick.
	lastVisible [MaxOccupants][MaxOccupants]int64

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
		visible:   buildVisibility(l),
	}
}

// Join puts somebody in the building, or hands back the occupant already here.
// It reports false when the заброшка is full, which is a refusal the player is
// told about rather than a silent no-op — see the hello.
//
// A SECOND HELLO IS A RECONNECT AND NOT A SECOND VISIT. A page reload, a tunnel
// or a phone waking up all produce one, and the occupant is still standing where
// he was: the only thing that changes is LastSeen, which is what stops the grace
// from expiring under a socket that has just come back. He keeps his slot too,
// so nobody watching him sees him vanish and reappear as somebody else.
//
// THE LOWEST FREE PLACE, which is a decision only because a slot is reused: the
// alternative — cycling through the places so a freed one is the last to be
// handed out again — buys nothing here, because it stops helping the moment the
// заброшка is nearly full, and the standings frame answers reuse properly for
// every case rather than for the common one.
func (w *World) Join(accountID, pseudonym string, now time.Time) (*Occupant, bool) {
	if o, ok := w.occupants[accountID]; ok {
		o.LastSeen = now
		return o, true
	}
	slot := -1
	for i, held := range w.slots {
		if held == "" {
			slot = i
			break
		}
	}
	if slot < 0 {
		return nil, false
	}
	o := &Occupant{
		AccountID: accountID,
		Pseudonym: pseudonym,
		Slot:      slot,
		State:     NewPlayer(w.Level),
		JoinedAt:  now,
		LastSeen:  now,
	}
	w.occupants[accountID] = o
	w.slots[slot] = accountID
	w.roster++
	return o, true
}

// release takes somebody out of the building and frees the place they were
// holding. It is the ONLY way an occupant leaves, which is what keeps the slot
// table, the occupant map and the roster version unable to disagree.
func (w *World) release(o *Occupant) {
	delete(w.occupants, o.AccountID)
	w.slots[o.Slot] = ""
	// Both directions of this place's visibility memory, so the next person to
	// stand here starts with none of it. Holding a peer visible for a fifth of a
	// second after he leaves is the point (visibleHold); holding a STRANGER
	// visible because the man whose place he took was, is not.
	for i := range w.lastVisible {
		w.lastVisible[o.Slot][i] = 0
		w.lastVisible[i][o.Slot] = 0
	}
	w.roster++
}

// RosterVersion changes whenever somebody joins or leaves, and never otherwise.
//
// IT IS WHAT PUBLISHES A STANDINGS FRAME OUT OF TURN. A snapshot names slots and
// nothing else, so a slot the client has not yet been given a name for is a
// figure it cannot label — and a slot that has been REUSED is worse than
// unlabelled, because a client that did not know would interpolate the newcomer
// from wherever the last holder was standing, and draw a man sliding across the
// building. Publishing the roster on the tick it changed, ahead of the snapshot
// that first names the new occupant, removes both.
//
// IT IS NOT THE ONLY TRIGGER, and it cannot be. A new CONNECTION for somebody
// already in the building — a reload, a tunnel coming back, a second device —
// moves nothing here, because Join hands back the occupant he already is, and
// that socket is being sent snapshots naming everybody from its first tick. This
// counter is for everybody ALREADY watching; a connection that has never been
// told anything is boarded by its own ledger (service.go, Service.boarded).
//
// A counter rather than a comparison of the published bytes, which differ every
// second anyway because the seconds on it advance; and rather than a comparison
// of the slot table, which is only unambiguous while a place cannot be freed and
// refilled between two publishes — that holds today, but it is a property of
// where the lock is taken rather than of the data, and this is one integer.
func (w *World) RosterVersion() int64 { return w.roster }

// canSee reports whether somebody standing in sector `at` is drawn by somebody
// standing in sector `from`.
//
// A sector index out of range is answered YES rather than no. It cannot happen —
// resolve only ever moves a player between real sectors — but if it ever did,
// being drawn from too far away is a smaller failure than being invisible in a
// building where the other people in it will eventually be shooting.
func (w *World) canSee(from, at int) bool {
	if from < 0 || from >= len(w.visible) || at < 0 || at >= len(w.visible) {
		return true
	}
	return w.visible[from][at]
}

// rememberVisibility records, for every pair of people in the building, whether
// one was inside the other's potentially-visible set on this tick. Advance is its
// only caller, and it runs AFTER the step, so what is recorded is where everybody
// actually ended up.
//
// It is what heldVisible reads, and between them they are the whole of the
// doorway hysteresis argued on visibleHold. At most MaxOccupants² pairs, a table
// lookup each and one array write for the pairs that can see each other, nothing
// allocated.
func (w *World) rememberVisibility() {
	for a, ida := range w.slots {
		if ida == "" {
			continue
		}
		from := w.occupants[ida].State.Sector
		for b, idb := range w.slots {
			if idb == "" || b == a {
				continue
			}
			if w.canSee(from, w.occupants[idb].State.Sector) {
				w.lastVisible[a][b] = w.Tick
			}
		}
	}
}

// heldVisible reports whether the occupant holding place `at` was inside the
// potentially-visible set of the one holding place `from` recently enough to
// still belong on his frame. See visibleHold for why a peer is held at all.
//
// A place outside the table is answered NO, which is the opposite of canSee's
// out-of-range answer and right for the same reason: canSee is the live test and
// errs towards being seen, where this is a memory and there is nothing to
// remember about a place that does not exist.
func (w *World) heldVisible(from, at int) bool {
	if from < 0 || from >= MaxOccupants || at < 0 || at >= MaxOccupants {
		return false
	}
	last := w.lastVisible[from][at]
	return last > 0 && w.Tick-last < visibleHoldTicks
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
	o, ok := w.occupants[accountID]
	if !ok {
		return false
	}
	w.release(o)
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
		var spent float64
		for len(o.pending) > 0 && o.pending[0].Dt <= o.budget {
			c := o.pending[0]
			o.pending = o.pending[1:]
			o.budget -= c.Dt
			spent += c.Dt
			// A BARREL COUNT FALLING IS THE SHOT, which is the same reading the
			// client makes of its own frame rather than a second definition kept
			// in step with it by hand. Nothing else in the simulation lowers it:
			// a reload only ever raises it, to Barrels. Recorded here so that
			// everybody watching gets told (Occupant.firedOn); the man who fired
			// needs no telling, because his own `b` is on his own snapshot.
			loaded := o.State.Loaded
			o.State = Step(w.Level, o.State, c)
			if o.State.Loaded < loaded {
				o.firedOn = w.Tick
			}
			// The queue is strictly ascending in Seq — Enqueue drops everything
			// at or below highSeq and the overflow trim takes from the front —
			// so this is the last command actually folded in, which is exactly
			// what the snapshot's Ack promises.
			o.State.LastSeq = c.Seq
		}

		// THE IDLE FILL: whatever part of this tick no command claimed is
		// simulated as standing perfectly still.
		//
		// IT ARRIVED WITH THE GUN, and the gun is what needs it. Everything the
		// simulation held before was a POSITION, and a player who sends nothing is
		// a player who is not moving, so a tick nobody claimed had nothing to
		// advance. A cooldown is not like that: it runs down because time passed,
		// and the client sends NOTHING while standing still with the screen
		// untouched (web/src/lib/vanyadumInput.ts, `due`). Without this, firing and
		// then standing still would stop the cooldown where it was and hang the
		// reload half-finished — so the gun would work only while you were walking,
		// which is the state you are least often in when you shoot at something.
		//
		// IT CHARGES THE BUDGET, and that is what stops the same second being
		// spent twice. Without the charge a player could stand still for half a
		// second — his gun cooling at real time — bank that half second, and then
		// burst-send it as still commands to cool the gun by another half. Half
		// again on top of real time, repeatable for as long as he alternated:
		// close to double the fire rate, and a third off every reload, with no
		// field out of range anywhere. Charged, the arithmetic closes: every
		// second granted to the gun is a second the budget paid for, and the
		// budget is bought at real time.
		//
		// ONLY WHEN THE CLIENT CLAIMED NOTHING AT ALL. A client that is sending has
		// merely under-filled the tick by a millisecond or two of ordinary browser
		// timer drift, and fabricating stillness in that gap is inventing input the
		// player did not give — which the sibling game learned the expensive way,
		// with a fill that added slivers of dash nobody had predicted
		// (internal/gamefintech/office.go). AND ONLY WHILE THE QUEUE IS EMPTY: the
		// fill consumes the budget, so one that ran while a command sat waiting for
		// budget would eat exactly the budget that command was waiting for, and the
		// two would deadlock for ever.
		//
		// AND ONLY WHILE SOMETHING IS ACTUALLY COUNTING DOWN, which is the guard
		// that keeps the charge from being a tax on standing still. A still step
		// against a cold gun provably changes nothing at all: the axes are zero so
		// no position, sector or angle can move, and both timers are already at
		// rest — so paying budget for it would buy the state it started from. That
		// costs something real, because the same budget is the honest client's
		// catch-up cushion after a stall (TestTimeBudgetLetsAStutteringClientCatchUp),
		// and it would be spent on nothing.
		//
		// It also leaves the exploit closed rather than merely smaller, and the
		// argument is worth following once: the only ticks that now bank are ticks
		// on which no gun time was granted, so banked seconds are seconds the gun
		// stood still for. Spending them later moves WHEN the gun advanced without
		// creating any, and the one-off head start is TimeBudgetCap — the same
		// bounded cushion movement has always had, from the same cap.
		//
		// THE ANGLES ARE THE PLAYER'S OWN AND NOT THE ZERO VALUE. Step assigns
		// c.Yaw and c.Pitch unconditionally, so a bare Command{Dt: idle} would
		// snap the view to due north and level every time a player stopped moving.
		if idle := dt - spent; idle > 0 && spent == 0 && len(o.pending) == 0 && o.State.ticking() {
			o.State = Step(w.Level, o.State, Command{
				Dt:    idle,
				Yaw:   o.State.Yaw,
				Pitch: o.State.Pitch,
			})
			o.budget = math.Max(0, o.budget-idle)
		}
		w.collect(o)
	}

	// Both of these are recorded AFTER the step, so they describe the world as the
	// snapshot about to go out describes it. Recording before would leave the
	// rewind buffer a tick behind everything that reads it, and would hold peers
	// against where everybody was standing rather than where they are.
	w.rememberVisibility()
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
		w.release(o)
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
		// The tally counts what the bag actually GAINED rather than what the
		// pickup offers, so somebody standing on a bottle with a full bag is
		// recorded as having gained nothing — which he did. It keeps "collected"
		// an upper bound on "carrying" for the whole visit, which is the one
		// relationship between the two numbers anybody would rely on.
		if gained := v - o.State.Counters[kind.Grants]; gained > 0 {
			if o.collected == nil {
				o.collected = map[string]int{}
			}
			o.collected[kind.Grants] += gained
		}
		o.State.Counters[kind.Grants] = v
		o.events = append(o.events, Event{E: EventPickup, K: p.Kind, ID: p.ID})
	}
}

// spots is every entity's position this instant, in the shape the rewind buffer
// stores. Keyed by SLOT rather than by account, so a rewound world and a
// published one name the same things — which is what lets a future hit test take
// the id it was shot at straight from the client's aim, and the client's aim has
// nothing but a slot to name it with.
//
// It was keyed by pseudonym, which was the same argument against the identity
// the wire published at the time. The key follows the wire; it is not an
// independent choice.
//
// EVERYBODY IS RECORDED, INCLUDING THE PEOPLE NOBODY COULD SEE. Interest
// management decides what is SENT, and this is the world as it actually was —
// a shot resolved against a filtered past would miss the man who stepped through
// the doorway in the interval being rewound over.
//
// The occupant list arrives as an argument rather than being derived here, and
// it is keys' order rather than the rotated one — but neither choice changes the
// ANSWER, because a map does not remember what order it was filled in. It takes
// the caller's list so that the frame recorded for a tick is of exactly the
// occupants that tick stepped, rather than of a second reading of the roster.
func (w *World) spots(ids []string) map[int]Spot {
	out := make(map[int]Spot, len(ids))
	for _, id := range ids {
		o := w.occupants[id]
		out[o.Slot] = Spot{Pos: o.State.Pos, Sector: o.State.Sector, Alive: o.State.Health > 0}
	}
	return out
}

// Standings is the readout of who is in the building: everybody, in slot order,
// with how long they have been here and what they are carrying.
//
// NOT ADDRESSED TO ANYBODY, unlike a snapshot — it describes the building rather
// than one person's view of it, so it lists the reader as well as everybody
// else, and the same bytes go to every connection. See the Standings type for
// why it is its own frame at its own rate.
//
// THE TIME IS MEASURED FROM WHEN THEY WALKED IN, and deliberately not the way a
// visit row measures it (Occupant.Stayed, which stops at the last connection).
// Somebody inside the abandon grace is still holding a place and is still drawn
// standing where he stopped, so a board that had quietly stopped counting for him
// would be disagreeing with what everybody can see.
func (w *World) Standings(now time.Time) Standings {
	out := Standings{T: TypeStandings}
	for slot, id := range w.slots {
		if id == "" {
			continue
		}
		o := w.occupants[id]
		secs := int(now.Sub(o.JoinedAt) / time.Second)
		if secs < 0 {
			// A clock that went backwards is not a negative visit, and it is not
			// a negative score either.
			secs = 0
		}
		row := StandingsRow{Slot: slot, Name: o.Pseudonym, Seconds: secs}
		if len(o.State.Counters) > 0 {
			row.Bag = make(map[string]int, len(o.State.Counters))
			for k, v := range o.State.Counters {
				row.Bag[k] = v
			}
		}
		out.Rows = append(out.Rows, row)
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
// Everybody else THE READER COULD PLAUSIBLY SEE arrives as a PEER, to be drawn
// interpolated in the recent past — their intent cannot be predicted the way the
// reader's own can. Everybody he could not, and has not been able to for
// visibleHold, is simply absent — which is what interest management is: see
// canSee and heldVisible, and Standings for the frame that tells him about the
// rest of the building anyway.
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
	//
	// IT IS OF THE WHOLE BUILDING AND IS NOT FILTERED, and that leaks a position
	// interest management otherwise withholds. A bit clearing names the position
	// of the thing that was taken — the client holds the level, so an index is a
	// place — and the next standings frame says whose bag grew, so the two
	// together put a man the reader was never sent at a known spot at a known
	// instant. It is left as it is on purpose: nothing in this game shoots yet, so
	// what is being reconstructed is a stranger drinking a beer in another room,
	// and both frames earn their shape elsewhere (this one is one word rather than
	// a per-viewer list; the standings is one marshalling for everybody). THE DAY
	// SOMETHING SHOOTS THIS BECOMES A REAL CONCERN — knowing where somebody is
	// standing is exactly what the filter exists to deny — and closing it starts
	// with cutting the mask to the reader's own visible sectors, which is a
	// per-viewer computation this deliberately does not pay today.
	var left uint32
	for i := range w.Level.Pickups {
		if w.available(i) {
			left |= 1 << uint(i)
		}
	}

	s := Snapshot{
		T:        TypeSnapshot,
		Tick:     w.Tick,
		Ack:      me.State.LastSeq,
		X:        cm(me.State.Pos.X),
		Y:        cm(me.State.Pos.Y),
		Z:        cm(EyeZ(w.Level, me.State)),
		Yaw:      mrad(me.State.Yaw),
		Sector:   me.State.Sector,
		Health:   me.State.Health,
		Left:     left,
		Loaded:   me.State.Loaded,
		Cooldown: ms(me.State.CooldownLeft),
		Reload:   ms(me.State.ReloadLeft),
		Events:   me.events,
	}
	if len(me.State.Counters) > 0 {
		s.Bag = make(map[string]int, len(me.State.Counters))
		for k, v := range me.State.Counters {
			s.Bag[k] = v
		}
	}
	// In SLOT order, which is stable by construction — an occupant's slot does
	// not move while he is in the building — and is the order the standings lists
	// the same people in, so a client reading the two frames together never has
	// to sort either. Walking the slot table also avoids the sort keys() does,
	// on a path that runs once per occupant per tick.
	//
	// THE FILTER IS INTEREST MANAGEMENT: the viewer's own room, the rooms through
	// its doorways, and whoever was in one of those recently enough to still be
	// held. buildVisibility (level.go) says what the set approximates and in which
	// direction; visibleHold says why leaving it is not instant — a sector is
	// derived from a position, so a man in a doorway changes rooms without
	// walking, and without the hold everybody adjacent to one of the two rooms and
	// not the other would strobe at the tick rate. The array is full state either
	// way, so somebody who has walked out of view is simply absent from it rather
	// than announced as gone.
	for slot, id := range w.slots {
		if id == "" || id == accountID {
			continue
		}
		o := w.occupants[id]
		if !w.canSee(me.State.Sector, o.State.Sector) && !w.heldVisible(me.Slot, slot) {
			continue
		}
		s.Peers = append(s.Peers, Peer{
			Slot:   slot,
			X:      cm(o.State.Pos.X),
			Y:      cm(o.State.Pos.Y),
			Sector: o.State.Sector,
			Yaw:    mrad(o.State.Yaw),
			// On the tick it happened and on no other. Read rather than
			// consumed, so all four viewers of a full building are told about
			// the same shot — see Occupant.firedOn.
			Fired: o.firedThisTick(w.Tick),
		})
	}
	me.events = nil
	return s, true
}
