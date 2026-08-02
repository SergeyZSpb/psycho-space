package gamevanyadum

import (
	"math"
	"math/rand"
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
	// what puts PeerFired on everybody else's frame for exactly that tick.
	//
	// A TICK NUMBER RATHER THAN A FLAG THAT IS CLEARED WHEN READ, and the
	// difference is the whole correctness of it: SnapshotFor runs once per
	// VIEWER, so a flag consumed by the first reader would show the shot to one
	// of the three people watching and hide it from the other two. Events get
	// away with being consumed because an event belongs to one person; this
	// belongs to the building. Comparing against World.Tick is idempotent for
	// every viewer and needs no reset — the tick moves on by itself.
	//
	// NOT ON Player, by ADR-058's test: Step never reads it, so the client never
	// has to simulate it, so it costs no port, no golden vector and no reconcile
	// spread. The client draws its OWN muzzle flash from the barrel count falling
	// (web/src/lib/vanyadumPredict.ts, `raw`), which is a value it already has.
	firedOn int64

	// hitOn is the tick on which this occupant was last hurt — by a barrel or by a
	// нейрослоп arriving, which are the two things in the building that can do it
	// — and it is the same mechanism firedOn is for the same reason: read per
	// viewer against World.Tick rather than consumed, so all three people watching
	// a man get hit are told about it and not merely whichever one was rendered
	// first.
	//
	// IT IS THE ONLY ACKNOWLEDGEMENT A HIT GETS, and it does two jobs at once.
	// The room learns somebody was hit, which is a thing nothing else on the
	// frame can say — neither a shot nor a touch moves the man it lands on, so
	// there is no value already there to derive it from. And the SHOOTER learns
	// he connected, because he knows he fired on this tick (his own barrel count
	// fell) and the man he was aiming at is marked on the very same frame. That
	// is why there is no second field addressed to the shooter.
	//
	// The one case that derivation misses is a victim who has walked out of the
	// shooter's visible set in the time being rewound over: he was hit where the
	// shooter saw him and is no longer on the shooter's frame to be marked. It
	// costs a hit marker and never a hit, the hold keeps it rare (visibleHold),
	// and the alternative is a field on a payload that repeats twenty times a
	// second to say nothing almost every time.
	hitOn int64

	// downUntil is the tick this occupant gets up on, and it is meaningful only
	// while his health is zero.
	//
	// A TICK DEADLINE RATHER THAN A COUNTDOWN IN SECONDS, and deliberately the
	// opposite choice from the gun's timers a few fields up. This one is the
	// WORLD's: the world owns the tick, an integer tick is exact where
	// subtracting dt from a float accumulates the error of SimStep's binary
	// expansion, and no client has to reproduce it — a dead man is dead in Step
	// because his health is zero, and nothing about when he gets up is his to
	// predict. It is the same argument, and the same shape, as the pickup respawn
	// (World.ready).
	downUntil int64

	// protectedUntil is the tick this occupant's spawn protection expires on, and
	// it is the WORLD's authority on that window.
	//
	// THERE ARE TWO OF THESE AND BOTH ARE NECESSARY — this and
	// Player.ProtectedLeft — so before deleting either, the two reasons:
	//
	//   - THE CLIENT NEEDS SECONDS, which is why the countdown is on Player. The
	//     browser decides whether to draw a muzzle flash the instant a thumb
	//     lands, so it has to run the same trigger refusal the server is about to
	//     (sim.go, stepGun). A rule it cannot predict is a rule it renders wrong
	//     for a round trip, every time.
	//   - THE WORLD NEEDS A DEADLINE, which is why this exists as well.
	//     ProtectedLeft is drained by the command's own dt, and the idle fill that
	//     would advance it for a silent player is gated on the client having
	//     claimed none of the tick (Advance) — so a client sending one sliver of a
	//     command per tick suppresses the fill and runs its own protection at a
	//     few percent of real time. Measured with this field taken out: a client
	//     claiming one millisecond of every tick still held 1.88s of a 2s window
	//     after six seconds of wall clock, and could not be shot for any of it
	//     (world_test.go, TestProtectionExpiresOnRealTimeAndNotOnWhatTheClientSends).
	//     The catalogue states protection as an expiry, so something the client
	//     does not touch has to enforce one.
	//
	// The deadline is therefore the authority and the countdown is a prediction of
	// it: Advance clamps ProtectedLeft to whatever this still allows, so the two
	// cannot drift apart in the direction that matters. It is the same shape, and
	// the same argument, as downUntil.
	protectedUntil int64

	// deaths is how often this occupant has been put on the floor, kills is how
	// many нейрослопы he has put down, and betrayals is how many friends he has.
	// All three ride the standings frame at 1 Hz (message.go, StandingsRow) and
	// none goes anywhere near a snapshot.
	//
	// A KILL AND A BETRAYAL ARE DIFFERENT NUMBERS AND NEITHER IS A SUBTOTAL OF THE
	// OTHER. Friendly fire is on, so both a слоп and a friend can be at the end of
	// the same barrel — and shooting the friend adds to nothing at all, which is
	// the whole joke now that there is something else in the building to shoot.
	// Until the слопы arrived there was only the confession, because every kill
	// there was was a friend's.
	//
	// NOT PERSISTED. The visit row records what somebody found (migrations/015,
	// immutable), and adding a column for these is a migration this iteration does
	// not need: the standings is a readout of the building rather than a career,
	// and nothing here ends.
	deaths    int
	kills     int
	betrayals int

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

// hitThisTick reports whether anything hurt this occupant on the tick the world
// is currently standing on — a barrel or a нейрослоп arriving, which the room is
// told about identically because neither one moves him. Zero is "never", for
// exactly the reason it is on firedThisTick.
func (o *Occupant) hitThisTick(tick int64) bool {
	return o.hitOn != 0 && o.hitOn == tick
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

	// routes is the walk from any room to any other, precomputed from the same
	// sector graph and read twice a tick by the нейрослопы. See buildRoutes
	// (slop.go).
	routes [][]route

	// slops[i] is the нейрослоп holding place i, and nil is a place nothing
	// occupies.
	//
	// THE SAME SHAPE AS THE SLOT TABLE, and for the same three reasons: it is the
	// wire's addressing (message.go, Foe), it is the capacity — the building is
	// full of слопы exactly when there is no nil left, so nothing counts them to
	// decide whether another may appear — and a fixed-width array walked in index
	// order is a stable iteration that allocates nothing on a tick.
	//
	// A PLACE IS REUSED once its holder is dead, which is what obliges killSlop to
	// wipe the two memories keyed by it: the visibility hold below and the rewind
	// ring. Getting that wrong is a слоп shot where the last one stood.
	slops [SlopPopulation]*Slop

	// slopSpawnAt is the earliest tick the next нейрослоп may appear.
	//
	// IT STARTS FULL rather than at zero, so a заброшка somebody has just walked
	// into is quiet for SlopSpawnInterval — see that constant for why nothing
	// materialises on the tick a socket says hello.
	slopSpawnAt int64

	// rng draws where a нейрослоп appears. Seeded from the same number the level
	// was generated from, so the same building spawns them in the same rooms in
	// the same order — the replay property the whole simulation is tested against
	// covers the spawner too, or a transcript would stop reproducing the world the
	// moment anything walked into it.
	rng *rand.Rand

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
	// who can see somebody who cannot see him — and the hit test now rests on
	// exactly that property (targetsFor): you may shoot what you were sent, so an
	// asymmetric hold would be a man shot by somebody his own client never drew.
	//
	// CLEARED WHEN A PLACE IS FREED (release), so a newcomer never inherits the
	// last holder's memory. Keyed by slot for the same reason the wire is: it is
	// a fixed-width array rather than a map, so nothing allocates on a tick.
	lastVisible [MaxOccupants][MaxOccupants]int64

	// lastVisibleSlop[a][i] is the same memory for the нейрослоп holding place i,
	// as seen by the occupant holding place a. Same table, same hysteresis, same
	// reasoning — a слоп walks through doorways too, and without the hold it would
	// strobe at the tick rate for everybody adjacent to one of the two rooms.
	//
	// NOT SYMMETRIC, AND IT DOES NOT NEED TO BE. The occupant table is symmetric
	// because a peer you were sent is a peer you may shoot, so an asymmetric hold
	// would be a man killed by somebody his own client never drew. A слоп has no
	// gun and no opinion about what it can see: the only thing it can do to you is
	// arrive, and arriving means standing SlopReach away, which is the same room or
	// a doorway between two — so anything that can touch you was drawn.
	//
	// CLEARED IN BOTH DIRECTIONS, by release for a row and by killSlop for a
	// column, because both numbers are handed on.
	lastVisibleSlop [MaxOccupants][SlopPopulation]int64

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

// NewWorld generates a заброшка with nobody in it — and, for the same reason,
// nothing in it: the first нейрослоп is SlopSpawnInterval away, exactly as every
// one after it is.
func NewWorld(id uuid.UUID, seed int64) *World {
	l := Generate(seed)
	return &World{
		ID:        id,
		Level:     l,
		occupants: make(map[string]*Occupant, MaxOccupants),
		ready:     make([]int64, len(l.Pickups)),
		history:   newHistory(),
		visible:   buildVisibility(l),
		routes:    buildRoutes(l),
		// A second stream from the same seed. Generate has consumed its own by the
		// time this is drawn, so the two never interleave, and where a слоп appears
		// is as reproducible from one number as where the rooms are.
		rng:         rand.New(rand.NewSource(seed)), //nolint:gosec // where a слоп appears, not security: crypto/rand would make a building unreproducible from its seed, which is the whole point.
		slopSpawnAt: slopSpawnTicks,
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
	// He materialises at the one spawn everybody in the building knows about, so
	// he arrives protected for exactly as long as a man who has just got up does
	// (content.go, SpawnProtectSeconds). NewPlayer does not grant it, deliberately:
	// Step is pure and knows no tick, so the world is the only thing that can open
	// a window it will also be held to.
	//
	// A RECONNECT CANNOT REFRESH IT, because a second hello returned above without
	// reaching this line. Otherwise the way to be permanently untouchable would be
	// to reload the page every two seconds.
	w.protect(o)
	w.roster++
	return o, true
}

// protect opens one spawn-protection window, in both of the places it is kept:
// the seconds the client predicts and the tick deadline the world holds him to.
// Occupant.protectedUntil is where the two are argued.
func (w *World) protect(o *Occupant) {
	o.State.ProtectedLeft = SpawnProtectSeconds
	o.protectedUntil = w.Tick + protectTicks
}

// protected reports whether this occupant is inside his window, BY THE WORLD'S
// DEADLINE and never by the countdown a client can slow down.
func (w *World) protected(o *Occupant) bool { return w.Tick < o.protectedUntil }

// protectLeft is how much of the window the deadline still allows, in the
// seconds Player.ProtectedLeft is expressed in. It is the ceiling Advance clamps
// the predicted countdown to.
func (w *World) protectLeft(o *Occupant) float64 {
	if !w.protected(o) {
		return 0
	}
	return float64(o.protectedUntil-w.Tick) * SimStep.Seconds()
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
	// And what this place remembered about the нейрослопы, which is one direction
	// rather than two: that table records what an occupant could see, and a слоп
	// has no memory of anybody.
	for i := range w.lastVisibleSlop[o.Slot] {
		w.lastVisibleSlop[o.Slot][i] = 0
	}
	// And this place's PAST, for the same reason one step further back: the
	// rewind buffer is keyed by slot too, so a shot resolved in the next
	// RewindMax would otherwise place the next holder of this place wherever the
	// man who has just left it was standing. See history.forget.
	w.history.forget(ref{N: o.Slot})
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
		for i, s := range w.slops {
			if s == nil {
				continue
			}
			if w.canSee(from, s.Sector) {
				w.lastVisibleSlop[a][i] = w.Tick
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

// heldVisibleSlop is heldVisible for a нейрослоп: whether the слоп holding place
// `id` was inside the visible set of the occupant holding place `from` recently
// enough to still belong on his frame. Same hold, same window, same reasoning.
func (w *World) heldVisibleSlop(from, id int) bool {
	if from < 0 || from >= MaxOccupants || id < 0 || id >= SlopPopulation {
		return false
	}
	last := w.lastVisibleSlop[from][id]
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
		// BEFORE HIS COMMANDS AND NOT AFTER THEM, so that the tick a man gets up
		// on is a tick he can move in. The other way round he would spend one step
		// standing at the spawn with his own input already spent on a corpse.
		w.rise(o)
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
				// RESOLVED HERE, ON THE SUB-STEP THAT FIRED IT, rather than once
				// at the end of the tick: a frame can carry four sub-steps, so the
				// player who pulls the trigger on the first of them and walks
				// through the other three aimed somewhere the tick's final
				// position is not.
				w.resolveShot(o, now)
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

		// THE DEADLINE HAS THE LAST WORD ON THE PROTECTION, after everything above
		// has had its say about it. The drain and the idle fill both run the
		// countdown down by a dt the CLIENT chooses the shape of, and a client that
		// claims a sliver of every tick suppresses the fill and advances its own
		// protection at a fraction of real time. Clamping here, once, to what the
		// world's own tick deadline still allows costs an honest client nothing —
		// he has been draining at real time, so the two agree to the float's last
		// digit — and ends the window on time for everybody else.
		//
		// A CLAMP RATHER THAN AN ASSIGNMENT, because the countdown is allowed to be
		// SHORTER than the deadline and never longer. Anything that ends a window
		// early ends it in both places at once — being shot clears the countdown and
		// the deadline together (wound) — and an assignment here would make this a
		// second thing deciding how much protection a man has, rather than a ceiling
		// on what he already has.
		o.State.ProtectedLeft = math.Min(o.State.ProtectedLeft, w.protectLeft(o))

		w.collect(o)
	}

	// AND THEN THE НЕЙРОСЛОПЫ, after every man has moved and never before.
	//
	// THE ORDER IS THE FAIRNESS. A слоп walks at where somebody IS, so stepping it
	// against last tick's positions would make it permanently 50 ms behind — which
	// at WalkSpeed is a quarter of a metre of free ground, every tick, for the
	// whole chase. Contact is then tested against the instant both of them have
	// actually reached, which is the same instant the snapshot about to go out
	// describes.
	//
	// The spawner goes first so that a слоп arriving on this tick is stepped and
	// can be reached on it, rather than standing still for one frame for no reason
	// a player could see.
	w.spawnSlop(ids)
	w.stepSlops(dt, ids)

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
//
// A MAN ON THE FLOOR PICKS UP NOTHING, which is the same rule Step already
// applies to everything else he might do (sim.go: a dead man does not walk and
// does not shoot). It needs saying separately because this runs from Advance and
// not from Step: he is killed standing on a bottle, the respawn comes back
// during his three seconds down, and without this he drinks it lying there and
// is sent the event for it.
func (w *World) collect(o *Occupant) {
	if o.State.Health <= 0 {
		return
	}
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

// spawnSlop keeps the building's population of нейрослопы up.
//
// A POPULATION-KEEPING SPAWNER RATHER THAN A GENERATOR THAT SCATTERS THEM ONCE,
// which is the difference between a building and a game: nothing here ends, so a
// заброшка stocked at generation time is one that is permanently cleared the
// first time somebody with a full gun tours it. The rate is the whole of the
// difficulty — see SlopSpawnInterval.
//
// ONE AT A TIME, so clearing both buys two intervals rather than one and the
// building never refills in a burst.
//
// NOTHING SPAWNS INTO AN EMPTY BUILDING, and that is what makes "no leak when
// everybody leaves" true by construction rather than by the service happening to
// stop ticking. A слоп with nobody to walk at is a creature standing in a room
// nobody is in, holding one of SlopPopulation places, in a world that is about to
// be torn down anyway.
func (w *World) spawnSlop(ids []string) {
	if len(ids) == 0 {
		return
	}
	free := -1
	for i, s := range w.slops {
		if s == nil {
			free = i
			break
		}
	}
	if free < 0 {
		// Full. The clock for the next one runs from NOW, so killing one is
		// followed by a whole interval of quiet rather than by whatever was left of
		// a timer that had been counting down under a building that needed nothing.
		w.slopSpawnAt = w.Tick + slopSpawnTicks
		return
	}
	if w.Tick < w.slopSpawnAt {
		return
	}
	sector := w.spawnSlopSector(ids)
	if sector < 0 {
		return
	}
	w.slops[free] = &Slop{
		ID:     free,
		Pos:    randomSpot(w.rng, w.Level.Sectors[sector]),
		Sector: sector,
		Health: SlopHealth,
	}
	w.slopSpawnAt = w.Tick + slopSpawnTicks
}

// spawnSlopSector picks the room a нейрослоп appears in: one that nobody in the
// building can see into.
//
// OUT OF SIGHT, BECAUSE APPEARING IS NOT A MOVE. Everything else in this world
// arrives by walking through a doorway, and a creature that materialises in the
// room you are standing in is one you could not have avoided and cannot explain.
// The visible set is already the answer to "which rooms is somebody looking at"
// (level.go, buildVisibility), so this is a table lookup per room per occupant on
// the rare tick something spawns.
//
// A BUILDING WITH NOWHERE OUT OF SIGHT FALLS BACK TO ANY ROOM rather than
// refusing to spawn. It cannot happen in a generated заброшка — seven rooms at
// least, and the visible set is one doorway deep — but a small fixture can leave
// every room visible, and a spawner that gave up there would be one that quietly
// stopped working on exactly the levels a test builds by hand.
func (w *World) spawnSlopSector(ids []string) int {
	if len(w.Level.Sectors) == 0 {
		return -1
	}
	hidden := make([]int, 0, len(w.Level.Sectors))
	for i := range w.Level.Sectors {
		seen := false
		for _, id := range ids {
			o := w.occupants[id]
			// A man on the floor is looking at nothing, and something appearing
			// beside a corpse is out of sight of everybody who matters.
			if o.State.Health <= 0 {
				continue
			}
			if w.canSee(o.State.Sector, i) {
				seen = true
				break
			}
		}
		if !seen {
			hidden = append(hidden, i)
		}
	}
	if len(hidden) == 0 {
		return w.rng.Intn(len(w.Level.Sectors))
	}
	return hidden[w.rng.Intn(len(hidden))]
}

// stepSlops walks every нейрослоп one step and applies whatever it reached.
//
// THE TARGET LIST IS BUILT ONCE AND IN keys' ORDER, which is what settles a tie
// between two men standing exactly the same distance away (slop.go, nearestPrey).
// Never map order: a tie decided by a coin the runtime tosses is a world that
// stops replaying from its transcript, which is the property everything else in
// this package is arranged to keep.
//
// A MAN ON THE FLOOR AND A MAN INSIDE HIS PROTECTION ARE NOT TARGETS. The floor
// is obvious. The protection is the same rule the hit test already applies
// (targetsFor) read the only way it can be: a window that stops a barrel and not
// a слоп would make getting up at the one spawn everybody knows about survivable
// against friends and fatal against the building — and being unable to shoot for
// two seconds is exactly when a creature walking at you is worst.
//
// TWO PASSES, DELIBERATELY. Everything moves, and only then does anything land:
// contact resolved inside the movement loop would test the first слоп against a
// position the second is about to be pushed out of, so which of two touching
// creatures hurt you would depend on the order they were stepped in.
func (w *World) stepSlops(dt float64, ids []string) {
	// Nothing to walk and nothing to be reached by, so nothing is built. It is a
	// guard on ALLOCATION rather than on correctness: the two slices below are per
	// tick, and a simulation loop that allocates twenty times a second to describe
	// an empty building is a self-inflicted garbage problem.
	empty := true
	for _, s := range w.slops {
		if s != nil {
			empty = false
			break
		}
	}
	if empty {
		return
	}

	targets := make([]prey, 0, len(ids))
	victims := make([]*Occupant, 0, len(ids))
	for _, id := range ids {
		o := w.occupants[id]
		if o.State.Health <= 0 || w.protected(o) {
			continue
		}
		targets = append(targets, prey{Pos: o.State.Pos, Sector: o.State.Sector})
		victims = append(victims, o)
	}

	for i := range w.slops {
		s := w.slops[i]
		if s == nil {
			continue
		}
		if at, ok := nearestPrey(s.Pos, targets); ok {
			*s = stepSlop(w.Level, w.routes, *s, at, dt)
		}
		// Clear of everything that has already moved, which is everything with a
		// lower id — so the lowest never yields and the order is the id order
		// rather than whatever the loop happened to be. See separate.
		for j := 0; j < i; j++ {
			if other := w.slops[j]; other != nil {
				*s = separate(w.Level, *s, *other)
			}
		}
	}

	for _, s := range w.slops {
		if s != nil {
			w.touch(s, victims)
		}
	}
}

// touch is one нейрослоп reaching somebody, and it is the second thing in this
// building that can put a man on the floor.
//
// THE DEATH LOOP IS THE ONE THAT ALREADY EXISTED. A слоп does not get a way of
// killing you — it gets a way of taking health off you, and everything about
// being on the floor, getting up, the spawn and the protection is whatever the
// обрез already made happen (hurt). What is different is only what it scores:
// nobody betrayed you, so nothing is added to anybody.
//
// THE ROOM IS TOLD, on the same field a shot uses (Occupant.hitOn). Being hurt by
// something is exactly as invisible as being shot — neither moves the man it
// happens to — so a слоп landing on somebody has to mark him for the same reason
// and, since the field is read per viewer against the tick, everybody watching is
// told rather than whoever was rendered first.
//
// ONE MAN PER TOUCH, because the cooldown belongs to the слоп: a creature
// standing between two people is not hitting both of them twenty times a second.
//
// AND IT IS THE NEAREST OF THEM, which is a fairness rule before it is an
// intuitive one. Walking the list in its own order and taking the first man in
// reach hands every touch to the lexicographically smaller uuid for the life of
// both accounts — the thing turnOrder exists to prevent, and which it describes as
// a coin that never gets tossed rather than one landing badly. Nearest needs no
// rotation to fix that: it is a property of where the two of them are standing
// rather than of what they are called, and it is what somebody watching a creature
// arrive would expect of it. An exact tie keeps the earlier of the list, which is
// keys' order and therefore the same on every process replaying the transcript.
//
// The list is re-checked as it is walked, because an earlier слоп on this same
// tick may already have put its man down.
func (w *World) touch(s *Slop, victims []*Occupant) {
	if w.Tick < s.touchAt {
		return
	}
	var nearest *Occupant
	shortest := math.Inf(1)
	for _, o := range victims {
		if o.State.Health <= 0 || w.protected(o) {
			continue
		}
		// Reach AND a clear line to him: concrete between the two of them refuses
		// it, whichever of them is on which side (slop.go, touches).
		dist, ok := touches(w.Level, s.Pos, o.State.Pos)
		// Strictly nearer, so an exact tie keeps the earlier man.
		if !ok || dist >= shortest {
			continue
		}
		nearest, shortest = o, dist
	}
	if nearest == nil {
		return
	}
	s.touchAt = w.Tick + slopTouchTicks
	nearest.hitOn = w.Tick
	w.hurt(nearest, SlopDamage)
}

// hitSlop applies one barrel to a нейрослоп, and takes it out of the building if
// that was the last of it.
//
// ONE BARREL IS ALWAYS THE LAST OF IT (content.go, SlopHealth), so the subtraction
// below is arithmetic rather than a branch anybody reaches — and it is written as
// arithmetic anyway, because the constant is what makes it true and a constant is
// a thing somebody retunes. The test that pins the two together is where the
// reasoning lives.
func (w *World) hitSlop(shooter *Occupant, id int) {
	s := w.slops[id]
	if s == nil {
		return
	}
	s.Health -= BarrelDamage
	if s.Health > 0 {
		return
	}
	w.killSlop(id)
	shooter.kills++
}

// killSlop takes a нейрослоп out of the building and frees the place it held. It
// is the ONLY way one leaves, which is what keeps the pool, the visibility memory
// and the rewind ring unable to disagree — the same guarantee, and the same three
// clears, that release gives for a man.
func (w *World) killSlop(id int) {
	w.slops[id] = nil
	for a := range w.lastVisibleSlop {
		w.lastVisibleSlop[a][id] = 0
	}
	// The number is handed to the next слоп, and the ring holds RewindMax of the
	// last one's walk. Without this the newcomer is shot where its predecessor
	// died, for half a second, in a room it has never been in. See history.forget.
	w.history.forget(ref{Slop: true, N: id})
}

// rise puts somebody who has been on the floor long enough back on his feet, at
// the spawn, with a full bar and a full gun.
//
// ONE SPAWN POINT, WHICH IS THE ONLY ONE THE LEVEL HAS. A set of them scattered
// through the building would be better play and is a generator change; what
// makes one survivable today is the protection it comes with — a man appearing
// where everybody knows he will appear is only a problem if he can be shot
// standing there, and for SpawnProtectSeconds he cannot. If that stops being
// enough, spawn points are the thing to add and this is where they would be
// read from.
//
// THE WINDOW IS NOT THIS FUNCTION'S IDEA and is not granted only here: Join
// hands out the same one, because walking in and getting up put a man in exactly
// the same place with exactly the same problem.
//
// HE KEEPS HIS BAG AND HIS ANGLES. The beer because a death already costs
// DownTime and the walk back, and taking the building's beer off him as well
// would compound whoever is winning (content.go). The angles because the camera
// belongs to the client, and snapping his view somewhere he did not point it is
// the one thing a first-person game may never do to somebody.
func (w *World) rise(o *Occupant) {
	if o.State.Health > 0 || w.Tick < o.downUntil {
		return
	}
	o.State.Pos = w.Level.Spawn
	o.State.Sector = w.Level.SpawnSector
	o.State.Health = StartHealth
	o.State.Loaded = Barrels
	o.State.CooldownLeft, o.State.ReloadLeft = 0, 0
	w.protect(o)
	o.downUntil = 0
}

// resolveShot fires one barrel: the ray, the rewind, and whatever it lands on.
//
// THE SHOOTER IS NOT REWOUND AND NEITHER IS HIS AIM. His position is the one his
// own client predicted and has just been acknowledged, and his yaw and pitch are
// client-owned inputs that arrived with this very command — both are already
// current, and rewinding either would resolve the shot from a place he was not
// standing, pointing somewhere he was not looking. Everybody ELSE is drawn
// interpolated in the past, so everybody else is where the rewind puts them.
// ADR-059 makes the same split for the sibling game's catch and gives the
// reasoning; it transfers whole even though none of its code does.
//
// The composition of the rewind — this occupant's smoothed round trip plus the
// served interpolation delay, clamped to RewindMax — belongs to history.go and
// is not re-derived here.
func (w *World) resolveShot(o *Occupant, now time.Time) {
	past := w.RewindTo(now, o.rtt)
	targets := w.targetsFor(o, past)
	if len(targets) == 0 {
		return
	}
	at, ok := shoot(w.Level, o.State.Pos, EyeZ(w.Level, o.State), o.State.Yaw, o.State.Pitch, targets)
	if !ok {
		return
	}
	// ONE RAY, TWO CONSEQUENCES, AND THE SPLIT IS HERE AND NOWHERE ELSE. A слоп
	// and a man are the same body to the geometry (hit.go), so the only thing that
	// differs is what happens afterwards: one is worth a kill, the other is worth a
	// confession. A second hit path for the second kind of target would be two ray
	// tests to keep in agreement.
	if at.Slop {
		w.hitSlop(o, at.N)
		return
	}
	w.wound(o, w.occupants[w.slots[at.N]])
}

// targetsFor is everybody this shot is allowed to land on, standing where the
// shooter saw them.
//
// YOU CAN HIT EXACTLY WHAT YOU WERE SENT, which is the rule that makes the
// visibility filter load-bearing rather than merely economical. The geometry
// would happily carry a shot through two lined-up doorways into a room the
// approximation does not consider visible (level.go, buildVisibility) — and the
// man standing there was never on the shooter's screen, so it would be a kill
// nobody could see coming and nobody could see happen. The filter is symmetric,
// so this is also what stops anybody being killed by somebody his own client was
// never told about.
//
// THE PAST DECIDES WHERE, THE PRESENT DECIDES WHETHER. A target is placed by the
// rewound frame and disqualified by the world as it is NOW: somebody who has
// since died is not killed twice, and somebody who has since got up is untouchable
// for as long as his protection lasts. history.go states that division for the
// buffer; this is the one place it is applied.
func (w *World) targetsFor(shooter *Occupant, past map[ref]Spot) []body {
	var out []body
	for slot, id := range w.slots {
		if id == "" || slot == shooter.Slot {
			continue
		}
		t := w.occupants[id]
		at := ref{N: slot}
		// Protection is read from the WORLD's deadline rather than from the
		// countdown on his Player, because the countdown is drained by commands he
		// sends and this is the half of the rule he must not be able to extend
		// (Occupant.protectedUntil).
		if t.State.Health <= 0 || w.protected(t) {
			continue
		}
		pos, sector := t.State.Pos, t.State.Sector
		if spot, ok := past[at]; ok {
			if !spot.Alive {
				// He was a corpse at the instant being shot at. Where he is
				// standing now is not where the shooter was aiming.
				continue
			}
			pos, sector = spot.Pos, spot.Sector
		}
		if !w.canSee(shooter.State.Sector, sector) && !w.heldVisible(shooter.Slot, slot) {
			continue
		}
		out = append(out, body{at: at, pos: pos, floorZ: w.floorOf(sector)})
	}

	// And the нейрослопы, through the same rewind and the same filter. There is
	// nothing to disqualify them by in the present beyond existing: a слоп is never
	// on the floor — one barrel is the whole of it, so it dies rather than falling
	// — and nothing protects it.
	for i, s := range w.slops {
		if s == nil {
			continue
		}
		at := ref{Slop: true, N: i}
		pos, sector := s.Pos, s.Sector
		if spot, ok := past[at]; ok {
			pos, sector = spot.Pos, spot.Sector
		}
		if !w.canSee(shooter.State.Sector, sector) && !w.heldVisibleSlop(shooter.Slot, i) {
			continue
		}
		out = append(out, body{at: at, pos: pos, floorZ: w.floorOf(sector)})
	}
	return out
}

// floorOf is how high the floor of one room is, or zero for a room that does not
// exist — which nothing in a generated level produces, and which is answered
// rather than trusted because the hit test would otherwise index a slice with a
// sector some rewound frame recorded before the building was replaced.
func (w *World) floorOf(sector int) float64 {
	if sector < 0 || sector >= len(w.Level.Sectors) {
		return 0
	}
	return w.Level.Sectors[sector].FloorZ
}

// wound applies one barrel to whoever it landed on, and puts him on the floor if
// that was the last of him.
//
// THE KILL IS A BETRAYAL AND STILL SCORES NOTHING, and now that there is
// something else in the заброшка to shoot, that is a statement rather than an
// accounting of what exists. A friend goes on his own counter, everybody reads it
// on the standings, and no total anywhere grows by it — where a нейрослоп at the
// end of the same barrel is a kill (hitSlop). Same gun, same ray, same damage;
// the building simply has an opinion about which one you pointed it at.
//
// WHICH OF TWO MEN WHO SHOT EACH OTHER ON THE SAME TICK DIES is decided by the
// step order, and that order rotates by the tick (turnOrder) precisely so it is
// not the same account every time. The loser's own trigger is then refused by
// Step, because a man with no health does nothing at all.
func (w *World) wound(shooter, victim *Occupant) {
	victim.hitOn = w.Tick
	if w.hurt(victim, BarrelDamage) {
		shooter.betrayals++
	}
}

// hurt takes health off somebody and puts him on the floor if that was the last
// of him, reporting whether it was.
//
// TWO THINGS IN THIS BUILDING CAN DO IT — a barrel and a нейрослоп — and this is
// the whole of what they share. What they do NOT share is the bookkeeping either
// side of it: only a shot marks a shooter, and only a shot at a friend is a
// betrayal, so both of those stay with their callers. A слоп arriving is nobody's
// fault.
//
// It reports the kill rather than leaving the caller to compare health before and
// after, because "did that finish him" is exactly the question both callers ask
// and there is one right way to answer it.
func (w *World) hurt(victim *Occupant, damage int) bool {
	victim.State.Health -= damage
	if victim.State.Health > 0 {
		return false
	}
	victim.State.Health = 0
	// The gun stops with him. Leaving a reload running would finish it under a
	// corpse, and a man who got up mid-cadence would be holding a weapon that was
	// busy for something he did three seconds and one death ago.
	victim.State.CooldownLeft, victim.State.ReloadLeft, victim.State.ProtectedLeft = 0, 0, 0
	// And the deadline behind the protection, or the clamp in Advance would put
	// the seconds cleared on the line above straight back on him.
	victim.protectedUntil = 0
	victim.downUntil = w.Tick + downTicks
	victim.deaths++
	return true
}

// spots is every entity's position this instant, in the shape the rewind buffer
// stores. Keyed by SLOT rather than by account, so a rewound world and a
// published one name the same things — which is what lets the hit test take the
// slot it shot at straight back to the wire (resolveShot), and a snapshot has
// nothing but a slot to name anybody with.
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
func (w *World) spots(ids []string) map[ref]Spot {
	out := make(map[ref]Spot, len(ids)+SlopPopulation)
	for _, id := range ids {
		o := w.occupants[id]
		out[ref{N: o.Slot}] = Spot{Pos: o.State.Pos, Sector: o.State.Sector, Alive: o.State.Health > 0}
	}
	// The нейрослопы are recorded on exactly the same terms and for exactly the
	// same reason: they are drawn interpolated in the past like everything else the
	// viewer does not predict, so a shot at one is only honest if it is resolved
	// where the shooter saw it. Always alive, because one killed has already left
	// the building (killSlop) and is not in the array to record.
	for i, s := range w.slops {
		if s == nil {
			continue
		}
		out[ref{Slop: true, N: i}] = Spot{Pos: s.Pos, Sector: s.Sector, Alive: true}
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
		row := StandingsRow{
			Slot: slot, Name: o.Pseudonym, Seconds: secs,
			Deaths: o.deaths, Kills: o.kills, Betrayals: o.betrayals,
		}
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
	// IT IS CUT TO THE ROOMS THE READER CAN SEE INTO, and that is not a rendering
	// nicety — it closes a position leak that was accepted only while nothing in
	// this game could shoot.
	//
	// THE LEAK, because the fix only makes sense next to it: a bit clearing names
	// the POSITION of the thing that was taken, since the client holds the level
	// and an index is a place; the next standings frame says whose bag grew; and
	// the two together put a man the reader was never sent at a known spot at a
	// known instant. That was a stranger drinking a beer in another room while
	// nothing could be done about it. It is now a target, which is exactly what
	// interest management exists to deny, and the exemption expired the day the
	// обрез started landing.
	//
	// The same filter the peers get, minus the hold: canSee against the reader's
	// own sector, which is a table lookup per pickup per viewer per tick against
	// a mask that can hold MaxWirePickups of them. No hold, because a place does
	// not walk — the hysteresis on visibleHold exists for a man jittering between
	// two rooms in a doorway, and a bottle stays where it was put. The cost of
	// leaving it out is that the reader's own jitter flickers the bits of a third
	// room, which is a mesh appearing and disappearing behind a wall he cannot see
	// through.
	//
	// WHAT IT COSTS THE CLIENT is that the mask no longer means "what is on the
	// floor of the building" but "what is on the floor NEAR YOU": a bit going
	// clear→set is still how a respawn travels, and it is now also how walking
	// into a room travels. Anything the client marks on that transition has to
	// know the difference — it has the level and its own sector, so it can.
	var left uint32
	for i, p := range w.Level.Pickups {
		if w.available(i) && w.canSee(me.State.Sector, p.Sector) {
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
		Down:     w.downMS(me),
		Protect:  ms(me.State.ProtectedLeft),
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
			St:     w.peerState(o),
		})
	}
	// And the нейрослопы, in id order and through the same filter — the viewer's
	// own room, the rooms through its doorways, and anything that was in one of
	// those recently enough to still be held. A слоп that has walked out of view is
	// simply absent, exactly as a peer is, and so is one that has just been shot:
	// this array is idempotent full state and dying is the same thing as not being
	// in it (message.go, Snapshot.Slops).
	for i, sl := range w.slops {
		if sl == nil {
			continue
		}
		if !w.canSee(me.State.Sector, sl.Sector) && !w.heldVisibleSlop(me.Slot, i) {
			continue
		}
		s.Slops = append(s.Slops, Foe{
			ID:     sl.ID,
			X:      cm(sl.Pos.X),
			Y:      cm(sl.Pos.Y),
			Sector: sl.Sector,
		})
	}
	me.events = nil
	return s, true
}

// downMS is how long this occupant has left on the floor, in the milliseconds
// the wire carries, or zero for anybody standing up.
//
// A deadline in ticks turned into a duration at the edge, which is the same
// direction of travel every other quantity on this frame makes: exact where it
// is simulated, small where it is sent.
func (w *World) downMS(o *Occupant) int {
	if o.State.Health > 0 || o.downUntil <= w.Tick {
		return 0
	}
	return int(time.Duration(o.downUntil-w.Tick) * SimStep / time.Millisecond)
}

// peerState is what one peer's frame says about him beyond where he is standing.
// See Peer.St for the four values and why they are one field.
//
// THE ORDER IS THE PRECEDENCE, and it is a precedence rather than a sequence of
// independent checks because the field holds one value. The two STATES come
// first because they last, and because a viewer whose shots are bouncing off a
// protected man needs telling that more than he needs a muzzle flash. Between
// the two INSTANTS, being hit beats firing: a man who pulled the trigger and was
// shot on the same tick has had the more interesting fifty milliseconds, and it
// is the thing that changed the building.
func (w *World) peerState(o *Occupant) int {
	switch {
	case o.State.Health <= 0:
		return PeerDown
	// The world's deadline again, and not his own countdown: what a viewer is
	// told about a peer has to be the rule his shots will actually be resolved
	// against (targetsFor).
	case w.protected(o):
		return PeerProtected
	case o.hitThisTick(w.Tick):
		return PeerHit
	case o.firedThisTick(w.Tick):
		return PeerFired
	}
	return 0
}
