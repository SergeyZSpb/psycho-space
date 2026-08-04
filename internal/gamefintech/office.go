package gamefintech

import (
	"crypto/rand"
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"sync"
	"time"
)

// The office: one shared world, in memory, with a slot per account.
//
// THERE IS ONE OF THESE FOR THE WHOLE PROCESS, not one per shift. That is the
// load-bearing difference from «ВАНЯДУМ», where an arena is a freshly generated
// заброшка and therefore has to be per run. The floor here is data rather than a
// constant — it is generated, stored and editable (layout.go) — but it is the
// SAME floor for everybody at any instant, and it is chosen by the service rather
// than by whoever clocked in. So there is one office, everybody who is working
// shares it, and it is dropped entirely when the last person leaves; the next
// shift builds a fresh one, on whatever floor is current by then, with the bald
// man back at the far wall.
//
// Nothing here is ever written to Postgres except the summary, once, when a
// shift ends. That is what keeps the 20 Hz tick clear of the rule that nothing
// in this project ticks durable state (ADR-038, and the package doc). The office
// is lost on restart, exactly as the hub's presence is, and that is accepted: a
// shift is a few minutes long and a lost one costs a replay.
//
// EVERY METHOD TAKES THE LOCK AND NONE OF THEM PUBLISH. Advance and SnapshotFor
// build bytes; the service sends them after unlocking. Holding this mutex across
// a hub write would couple the whole simulation to the hub's queue.

// trailLen is how many ticks of both men's positions the office remembers, so a
// catch can be resolved against the world the victim was looking at.
//
// Sized from CatchRewindMax rather than picked, plus two ticks of slack: the
// rewind is capped there, so nothing can ever ask for an older frame than this
// holds, and retuning the cap resizes the ring rather than silently outrunning
// it.
const trailLen = int(CatchRewindMax*SimHz) + 2

// trailPoint is where both men were on one tick. Only positions: the grin and
// the drink are display, and the client interpolates them alongside the position
// they belong to, so nothing here needs them.
type trailPoint struct {
	boss   Vec2
	claude Vec2
}

// maxPending bounds one occupant's command queue. Four frames' worth: enough
// that a burst after a hiccup is not thrown away, small enough that a client
// which floods cannot make the office hold an unbounded slice. What bounds how
// much of it is SIMULATED is the time budget, not this.
const maxPending = 4 * MaxInboundCommands

// Occupant is one person in the office: their simulated state, plus everything
// about their CONNECTION rather than about the world.
type Occupant struct {
	AccountID string
	ShiftID   string
	// Pseudonym is the handle the OTHER occupants know this one by (ADR-037).
	// Stamped once at Join and never derived again, so it cannot drift; an
	// account id never reaches the wire, and a pseudonym means nothing once the
	// process that minted it has restarted.
	Pseudonym string
	// Invincible is how long this occupant is behind a cloud of hookah smoke, in
	// seconds. While it runs he is not a target the лысый can see and not somebody
	// the лысый can catch.
	//
	// ON THE OCCUPANT RATHER THAN ON Player, like the persona and for the same
	// reason: `Step` never reads it, so putting it there would force the golden
	// vectors regenerated for a value that changes nothing about movement. It
	// changes who the BOSS walks at, and he is the office's business.
	Invincible float64

	// Persona is which employee this occupant is — an index into Personas, drawn
	// once when the shift starts.
	//
	// ON THE OCCUPANT AND NEVER ON Player, and that is a rule rather than a
	// preference: `Step` is pinned to its TypeScript port by golden vectors, so a
	// field the simulation never reads would force the whole 193 kB artefact to be
	// regenerated for a value that changes nothing about movement. A persona
	// decides what a figure SAYS, and the line it says is already chosen here.
	Persona int

	// Avatar is the picture the OTHER occupants draw on this one, read once when
	// the shift starts and never again.
	//
	// Held here rather than looked up per frame because it is a constant for the
	// life of a shift, and read once rather than per snapshot because it is a
	// database column and this is a 20 Hz loop. It never reaches the wire — see
	// PeerFrame — only AvatarFor, by pseudonym.
	Avatar string
	State  Player
	// Pending are commands received but not yet stepped. Drained on the tick
	// rather than applied on arrival, so the simulation advances on its own
	// clock and never on a client's read pump.
	Pending []Command
	// LastSeq is the last command sequence actually folded in, echoed to the
	// client as the snapshot's `ack`.
	LastSeq uint32
	// HighSeq is the highest sequence ever ACCEPTED INTO THE QUEUE, which is a
	// different number from LastSeq and is what redundancy is deduplicated
	// against.
	//
	// THE DIFFERENCE BETWEEN THE TWO IS A BUG THIS GAME SHIPPED. A client repeats
	// its unacknowledged tail in every frame so one lost packet costs no input,
	// and that is only free while a repeat is dropped. Deduplicating on LastSeq
	// alone drops the repeats of commands already SIMULATED and accepts the
	// repeats of commands still WAITING — and a frame carries four sub-steps
	// where a tick affords two, so at any moment about half the queue is in
	// exactly that state. Measured: eight sub-steps of walking sent, 1.45 m
	// travelled where 1.28 m was asked for.
	//
	// Worse, it compounds. Duplicates double the demand on a budget that accrues
	// at real time, so the queue grows, so MORE of it is unapplied when the next
	// frame lands, so the whole redundancy window duplicates rather than half of
	// it. The player is dragged forward while walking and keeps walking after the
	// stick is released, because the office is still working through a backlog of
	// movement they only asked for once.
	HighSeq uint32
	// RTT is this occupant's smoothed round trip, in seconds, derived from the
	// snapshot tick their frames say they had drawn.
	//
	// DERIVED AND NEVER REPORTED. The tick rate is fixed, so the gap between the
	// tick a client says it has and the tick the office is on IS the loop, and a
	// client cannot inflate it without also claiming to be looking at a frame it
	// has not received. Smoothed with a slow exponential average, because one
	// late frame is not a slower connection and rewinding by a spike would resolve
	// a catch against a world nobody was ever looking at.
	RTT float64
	// Budget is the unspent client-claimed simulated time described on
	// TimeBudgetCap.
	Budget    float64
	StartedAt time.Time
	// LastSeen is when this account last had a connection in the room. It is
	// what AbandonGrace is measured against, and it is updated by the service
	// once a tick for everybody who is CONNECTED — not by Enqueue alone, because
	// a player standing perfectly still sends nothing at all and is the most
	// present person in the game.
	LastSeen time.Time
	// AnnounceLine is WHICH line the announcement above is showing — the redirect's
	// or the router's.
	//
	// A FIELD RATHER THAN A HARDCODE, because there are two announcements now. It
	// was `lineFor` returning RedirectLine outright, which was right while there was
	// one verb and becomes a bug the moment there are two: taking the router down
	// would have put «ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО» over your head.
	AnnounceLine int
	// RedirectCool is seconds until this occupant may point the bald man at
	// somebody again, and Announce is seconds of still saying so.
	//
	// BOTH ON THE OCCUPANT AND NEITHER ON Player, deliberately. Player is pinned
	// to its TypeScript port by the golden vectors, and neither of these is
	// something Step reads or the client predicts — they are facts about the
	// office, so they live where the office keeps its facts.
	RedirectCool float64
	Announce     float64
	// Ended and Cause are set by the tick that finishes this shift, in the
	// instant between the office deciding it is over and the service writing the
	// row.
	Ended bool
	Cause string
	// StartTick is the office tick this occupant clocked in on, and it is how the
	// client draws «how long have I been standing here».
	//
	// A TICK RATHER THAN A CLOCK, and it never reaches a snapshot. The shift's age
	// is `tick − StartTick`, the tick is on every frame already, and this number is
	// constant for the life of the shift — so it rides the READY frame, once per
	// socket attach, and the browser does the subtraction. A field on the snapshot
	// would be bytes ten times a second per viewer to re-state something derivable
	// from what is already there (ADR-037's rule, applied to a duration).
	//
	// It is not the same number as StartedAt, which is a wall clock and is what the
	// recorded row's `seconds` is measured with. The two can differ by whatever the
	// tick has drifted; nobody reads a shift length to the millisecond.
	StartTick uint64
}

// Elapsed is how long this shift has lasted.
func (o *Occupant) Elapsed(now time.Time) float64 {
	d := now.Sub(o.StartedAt).Seconds()
	if d < 0 {
		return 0
	}
	return d
}

// Office is the shared world.
type Office struct {
	mu        sync.Mutex
	occupants map[string]*Occupant
	boss      Boss
	tick      uint64
	// plan is the office's furniture: the layout it was opened with, the
	// collision list, and the navigation grid built from it.
	//
	// HELD BY THE OFFICE RATHER THAN BY THE PACKAGE, which is the whole of the
	// difference between a level and a constant. Everything that collides or paths
	// reads this — under the mutex, like everything else here — so an office is
	// entirely described by its occupants and its plan, and there is no global
	// state left for two of them to disagree about.
	plan *Plan
	// redirectTo is the account the bald man has been POINTED at, overriding his
	// standing rule of walking at whoever is nearest, and redirectLeft is how
	// much of that override is left.
	//
	// ON THE OFFICE RATHER THAN ON THE BOSS: who he is chasing is a fact about
	// the room, not a property of the man, and Boss is a pure value that StepBoss
	// takes and returns. Keeping it here means the override is expressed by
	// giving StepBoss a shorter list of targets — no second pursuit rule, and
	// nothing about the boss changes at all.
	redirectTo   string
	redirectLeft float64
	// hookahs is every кальян on the floor — see `bottles` below for the whole
	// arrangement, which the two share exactly.
	hookahs []prop
	// claude is Claude Code, the second man on the floor. He is a separate value
	// with a separate step for the reason chaser.go states: what happens when he
	// arrives is different, and an interface over two implementations with one
	// common method would be a seam nobody asked to use.
	claude Chaser
	// trail is the recent past of both men, indexed by tick modulo its length.
	//
	// LAG COMPENSATION, AND THE REASON IT EXISTS HERE AT ALL. Your own Карен is
	// predicted, so he is drawn in the present; the лысый cannot be, so he is
	// drawn from an interpolation buffer in the recent past. Resolving the catch
	// against the office's present compares two different instants, and the two
	// errors ADD while you are running away — measured at 1.4–1.8 m against a
	// catch radius of 1.2 m, which is how a shift ended while he was still drawn
	// a couple of metres off.
	//
	// So the catch is resolved against where he WAS, by however far behind this
	// occupant's screen is. It is the fourth Gambetta rung, and the claim that
	// this game did not need one — «nothing here shoots» — was simply wrong about
	// what a hit test is.
	trail [trailLen]trailPoint
	// npcs are Серега and Тёма. Deliberately NOT in `occupants`: the moment
	// anything joins that map it becomes a chase target, a snapshot addressee and a
	// slot against MaxOccupants — so a lazy player would be saved by a colleague
	// who is not playing.
	npcs []NPC
	// lostTarget is true when the лысый had somebody to walk at and now has
	// nobody, because everybody left standing is behind a cloud. It decides which
	// run he speaks from and is recomputed every tick, so it never goes stale.
	lostTarget bool
	// routerAway is how long Claude Code stays off the floor, in seconds, and
	// routerCool is how long until the router may fall again.
	//
	// BOTH ON THE OFFICE AND NEITHER ON A PLAYER, and the cooldown's placement is
	// the design rather than an implementation detail: anybody may press the
	// button, and it then cannot be pressed by ANYBODY until the wait is over. A
	// per-caller cooldown — the shape the redirect uses — would let a full floor of
	// three keep Claude away almost permanently, which is a deletion rather than a
	// reprieve.
	routerAway float64
	routerCool float64
	// bottles is every bottle on the floor, one slot each.
	//
	// A LIST RATHER THAN A SINGLE PROP, AND AS LONG AS THERE ARE PEOPLE. One
	// bottle in a room of three is not a third of a mechanic, it is a race: the
	// nearest man gets it every time and the other two stop walking to it at all.
	// So the office keeps one per occupant, which makes the prop worth crossing
	// the floor for whoever you are — and, in a full office, makes two people
	// arriving at different bottles the ordinary case rather than a collision.
	//
	// The count is reconciled on the tick and only ever GROWS immediately: a slot
	// past the target is dropped once somebody has taken it, never snatched off
	// the floor from under whoever was walking towards it.
	bottles []prop
}

// prop is one bottle or one кальян: which catalogue spot it stands on, and how
// long until it is back if somebody has taken it.
//
// `gone` of zero means it is standing there right now, which is the common case
// and therefore the one that costs nothing to say — the wire sends a MASK of the
// spots that have one, and nothing at all about the ones that do not.
type prop struct {
	spot int
	gone float64
}

// NewOffice opens the floor on a plan, with both men at the far wall and nobody
// in it.
//
// The plan is a PARAMETER rather than something this reads, because the office is
// no longer the only thing that decides what the room looks like: it is opened on
// whatever floor is current, and the service that holds that decision hands it
// over — already built, so opening an office costs no flood fill.
func NewOffice(plan *Plan) *Office {
	o := &Office{
		occupants: make(map[string]*Occupant, MaxOccupants),
		plan:      plan,
		boss:      NewBoss(),
		claude:    NewChaser(),
		npcs:      NewNPCs(),
		// One of each to open with, because an office is opened by one person
		// joining. The tick reconciles the count from there.
		bottles: []prop{{spot: 0}},
		hookahs: []prop{{spot: 0}},
	}
	// Tick zero, seeded, so a rewind on the first few ticks reads where they
	// actually were rather than the ring's zero value — which is the top-left
	// corner, and would make both men unable to reach anybody for the first two
	// ticks of every shift.
	o.trail[0] = trailPoint{boss: o.boss.Pos, claude: o.claude.Pos}
	return o
}

// Join puts an account to work.
//
// A second shift for the same account is REFUSED rather than replacing the
// first: dropping the running one would throw away a shift somebody is in the
// middle of on their other tab, and nothing here can tell which one they meant.
// The client's answer is the quit button.
func (o *Office) Join(accountID, shiftID, pseudonym, avatar string, persona int, now time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.occupants[accountID]; ok {
		return ErrShiftInProgress
	}
	if len(o.occupants) >= MaxOccupants {
		return ErrOfficeFull
	}
	o.occupants[accountID] = &Occupant{
		AccountID: accountID,
		ShiftID:   shiftID,
		Pseudonym: pseudonym,
		Avatar:    avatar,
		Persona:   persona,
		State:     NewPlayerAt(o.spawnPoint()),
		StartedAt: now,
		LastSeen:  now,
		StartTick: o.tick,
	}
	return nil
}

// How a spawn is drawn. Called with the lock held.
const (
	// spawnTries bounds the rejection sampler. A LOOP WITH NO FLOOR IS A LOOP
	// THAT CAN HANG A 20 Hz TICK, and Join runs under the same mutex the
	// simulation does — so this terminates in a bounded number of draws and
	// falls back to a point that is legal by construction.
	//
	// The count is sized against a MEASUREMENT rather than picked: 26.9 % of the
	// legal floor clears the spawnFromBoss floor below, so 64 draws miss about
	// once in three hundred million and take the fallback — which is the
	// near-the-boss spawn the floor exists to prevent. They are arithmetic on a
	// join, which happens once a shift and never on a tick.
	//
	// It was 240 while a spawn also had to have a clear line to him, which left
	// only 8.4 % qualifying. That rule is gone: he can walk round a desk now
	// (navigate.go), so a spawn in a desk's shadow is no longer a spawn he cannot
	// reach — and dropping it took the usable floor from 8.4 % to 26.9 %, which
	// is most of the variety a drawn spawn was asked for in the first place.
	spawnTries = 64
	// spawnFromEachOther is how much room two Карена get. Two player radii is
	// touching; this is enough that a joiner is visibly beside somebody rather
	// than inside them.
	spawnFromEachOther = 1.5
	// spawnHeadStart is the shortest chase a shift may OPEN with, and it is tied
	// to MinShiftSeconds on purpose: a shift shorter than that is dropped rather
	// than written, so a spawn that lets him arrive sooner can produce a shift
	// that ended and left no trace of itself. Half a second of margin over it,
	// because he does not have to walk in a straight line to get there.
	spawnHeadStart = MinShiftSeconds + 0.5
	// spawnFromBoss is that head start expressed as a distance, which is what a
	// spawn can actually be tested against.
	//
	// DERIVED RATHER THAN PICKED. Every number in it is a constant this game
	// already tunes — his speed, the radius at which he catches you, yours — so
	// retuning any of them keeps this true instead of silently invalidating it.
	// At today's values it is 15.2 m, against the old fixed spawn's 16.5.
	//
	// It is a HARD filter with a fallback, not a preference, and that distinction
	// is what CI caught: as a preference the sampler kept the best of its draws
	// and the measured worst was 8 m — a 1.7 s head start, shorter than
	// MinShiftSeconds, so an unlucky shift ended before it was worth recording.
	spawnFromBoss = spawnHeadStart*BossSpeed + CatchRadius + PlayerRadius
)

// spawnPoint draws where somebody joining stands.
//
// ITERATION 1 SPAWNED EVERYBODY ON ONE FIXED TILE, which is correct for one
// player and wrong in two ways the moment there are two. Join while somebody is
// playing and you materialise INSIDE them, on the one spot the bald man is
// already walking towards. And dying became a free teleport: die, rejoin, and
// you are the length of the room from a man who is now busy with your colleague.
// Neither is a rendering problem, so neither could be fixed by drawing peers.
//
// The rule a drawn point has to satisfy: inside the walls, out of the furniture,
// clear of everybody already working, and far enough from the лысый to be worth
// calling a shift — see spawnFromBoss, which is derived from MinShiftSeconds
// rather than picked.
//
// It used to require a CLEAR LINE to him as well, because he could not walk round
// a desk and a shift opening in one measured up to ninety seconds of nothing
// happening. He can now (navigate.go), so that rule went with the defect it was
// working around.
//
// The randomness is crypto/rand, matching the yard's habit rather than because a
// spawn is a secret: this package has one reader, and a second generator here
// would be a second thing to reason about for nothing. It lives HERE and not in
// Step — Step is pure, draws no randomness and is pinned to its TypeScript port
// by golden vectors, and a spawn the client could compute is a spawn the client
// could choose.
func (o *Office) spawnPoint() Vec2 {
	free := func(at Vec2) bool {
		for _, d := range o.plan.rects {
			if insideRect(d, at, PlayerRadius) {
				return false
			}
		}
		// Map iteration is fine here and only here: this reads every occupant to
		// answer one boolean, so the ORDER cannot reach the result. Anything that
		// produces a value walks keys() instead.
		for _, occ := range o.occupants {
			if math.Hypot(occ.State.Pos.X-at.X, occ.State.Pos.Y-at.Y) < spawnFromEachOther {
				return false
			}
		}
		return true
	}
	// A HARD FLOOR, WITH THE BEST LEGAL POINT AS THE FALLBACK. Taking the first
	// draw that clears spawnFromBoss is what gives the head start a floor rather
	// than a distribution — and the floor is what stops a shift ending before
	// MinShiftSeconds. But the filter cannot be the whole rule: mid-shift he is
	// wherever he has chased somebody to, and from the middle of the room almost
	// nothing is 12 m away, so rejecting everything would fall through to a fixed
	// point that could be right beside him. Keeping the farthest legal point seen
	// degrades to "as far as we could find" instead, which is the best answer
	// available when the good one does not exist.
	best, bestGap := Vec2{}, -1.0
	for i := 0; i < spawnTries; i++ {
		at := Vec2{
			X: PlayerRadius + unitRand()*(OfficeW-2*PlayerRadius),
			Y: PlayerRadius + unitRand()*(OfficeH-2*PlayerRadius),
		}
		if !free(at) {
			continue
		}
		gap := math.Hypot(o.boss.Pos.X-at.X, o.boss.Pos.Y-at.Y)
		if gap >= spawnFromBoss {
			return at
		}
		if gap > bestGap {
			best, bestGap = at, gap
		}
	}
	if bestGap >= 0 {
		return best
	}
	// Not one draw was legal — reachable only with the floor unusually crowded.
	// The catalogue's own spawn is legal by construction and pinned by
	// TestBothSpawnsAreOnTheFloorAndOutOfTheFurniture, so it is the one answer
	// that cannot be wrong about the geometry. It may put two people together,
	// which is the lesser fault: overlapping is untidy, off the floor is broken.
	return Vec2{X: PlayerSpawnX, Y: PlayerSpawnY}
}

// unitRand is a number in 0..1 from crypto/rand.
//
// Quantised to a thousand steps — far finer than a plane sixteen metres wide can
// resolve on a phone — which keeps the draw one bounded integer rather than a
// float assembled out of bytes. A read error is unreachable in practice (Go's
// crypto/rand panics internally rather than returning one) and answers with the
// middle of the range, which is a legal number rather than a NaN.
func unitRand() float64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1000))
	if err != nil {
		return 0.5
	}
	return float64(n.Int64()) / 1000
}

// drawPersona picks which employee you are this shift.
//
// `crypto/rand` rather than `math/rand`, because it is the only source this project
// uses — not because a persona is a secret. A failed draw answers Карен, which is
// index 0 and the one persona every other contract already assumes as its default.
func drawPersona() int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(Personas))))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

// Leave ends a shift on purpose and takes the occupant out of the world,
// returning them so the caller can decide whether the shift is worth recording.
//
// It takes no clock: the occupant carries StartedAt, and how long the shift
// lasted is the caller's arithmetic — which matters because the same method
// serves both walking out (recorded) and being forgotten (never recorded).
func (o *Office) Leave(accountID string) (*Occupant, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok {
		return nil, false
	}
	occ.Ended, occ.Cause = true, CauseLeft
	occ.State.Alive = false
	delete(o.occupants, accountID)
	return occ, true
}

// EvictAll ends every shift in the office and returns everybody it ended.
//
// THE OFFICE IS BEING REBUILT UNDERNEATH THEM, which is the one ending nobody
// chose and nobody earned — so it is recorded exactly as walking out is, and the
// salary counts. The alternative, discarding the shift, punishes the player for
// something the building did.
//
// IN keys() ORDER, like everything here that produces a value: a map's iteration
// order is randomised, and the caller turns this into one frame per occupant. A
// test that could not predict the order of those frames would have to sort them,
// which is a test working around a determinism this game otherwise has.
//
// It leaves the office EMPTY rather than usable. The caller drops it entirely —
// the лысый, Claude, the two colleagues and one prop per spot were all placed
// against the floor that is going away, so an office retrofitted with a new plan
// would have its whole cast standing in the wrong room.
func (o *Office) EvictAll() []*Occupant {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]*Occupant, 0, len(o.occupants))
	for _, k := range o.keys() {
		occ := o.occupants[k]
		occ.Ended, occ.Cause = true, CauseRenovated
		occ.State.Alive = false
		delete(o.occupants, k)
		out = append(out, occ)
	}
	return out
}

// Seen records that an account still has a connection. It is what AbandonGrace
// is measured against.
func (o *Office) Seen(accountID string, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if occ, ok := o.occupants[accountID]; ok {
		occ.LastSeen = now
	}
}

// Enqueue accepts input from one occupant. It runs on that connection's read
// pump, so it does the least possible work: sanitising and appending to a slice
// the tick will drain.
//
// THIS IS THE ONE PLACE COMMANDS ARE SANITISED, which is what lets Step stay a
// pure function of valid input and lets the golden vectors describe the
// simulation rather than the clamp.
//
// COMMANDS ALREADY SEEN ARE DROPPED HERE, by sequence number. That single rule
// is what makes input redundancy free: a client may resend the tail of its
// unacknowledged commands in every frame, so one lost packet costs no input at
// all, and a client that resends everything forever gains nothing because the
// second copy never reaches the queue. Without it a replayed command would be
// movement that happens twice on the server and once on the client, which the
// player feels as being dragged.
//
// "SEEN" IS HighSeq AND NOT LastSeq, and the difference is the whole of the
// rule's correctness — see the field. A command that is queued but not yet
// simulated has been seen; deduplicating against what has been APPLIED lets
// every one of those through a second time.
func (o *Office) Enqueue(accountID string, in Input, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok || occ.Ended {
		return
	}
	occ.LastSeen = now

	// THE ROUND TRIP, DERIVED FROM WHAT THE CLIENT SAYS IT HAS DRAWN. This runs
	// even for a frame carrying no commands, because a frame is evidence of the
	// loop's length whatever else is in it.
	//
	// A tick in the FUTURE is discarded rather than clamped: it is either a client
	// guessing or the office having been rebuilt under it, and neither is a
	// latency measurement.
	if in.Seen > 0 && in.Seen <= o.tick {
		sample := float64(o.tick-in.Seen) * SimStep.Seconds()
		if sample > CatchRewindMax {
			sample = CatchRewindMax
		}
		if occ.RTT == 0 {
			occ.RTT = sample
		} else {
			occ.RTT = (occ.RTT*3 + sample) / 4
		}
	}

	if len(in.Cmds) == 0 {
		return
	}
	for _, c := range in.Cmds {
		// Sequences are 1-based, so a zero is "unset" and is dropped along with
		// everything already seen.
		if c.Seq <= occ.HighSeq {
			continue
		}
		occ.HighSeq = c.Seq
		occ.Pending = append(occ.Pending, Sanitise(c))
	}
	if len(occ.Pending) > maxPending {
		occ.Pending = occ.Pending[len(occ.Pending)-maxPending:]
	}
}

// Advance runs one simulation step for everybody and returns the occupants whose
// shift ended on it — caught, or gone long enough to count as walked out. Those
// are removed from the world before this returns, so the caller only has to
// decide whether to record them.
//
// dt is the tick's own length, which is what the time budget accrues at — never
// a client's claim.
func (o *Office) Advance(dt float64, now time.Time) []*Occupant {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tick++

	keys := o.keys()

	for _, k := range keys {
		occ := o.occupants[k]
		occ.Budget = math.Min(occ.Budget+dt, TimeBudgetCap)

		spent := 0.0
		// A COMMAND IS SIMULATED WHOLE OR IT WAITS. It is never simulated in part,
		// because there is no way to acknowledge a part: the ack is one sequence
		// number, the client drops everything at or below it, and a client that
		// dropped a command the office had only half-run would keep the whole of
		// it in its own prediction for ever. That is a permanent divergence in the
		// direction that matters most here — the client believes it is further
		// from the лысый than the office does, so it is eaten while he is still
		// drawn a metre away.
		//
		// The earlier version truncated the command to the remaining budget and
		// acknowledged it anyway, against a comment saying it did not. Waiting
		// costs one tick of stutter for a client that is already behind; the
		// truncation cost 0.96 m of silent, unrecoverable drift per occurrence.
		for len(occ.Pending) > 0 && occ.Pending[0].Dt <= occ.Budget {
			c := occ.Pending[0]
			occ.Pending = occ.Pending[1:]
			occ.Budget -= c.Dt
			spent += c.Dt
			occ.State = Step(o.plan.rects, occ.State, c)
			// The queue is strictly ascending in Seq — Enqueue drops anything at
			// or below HighSeq and the overflow trim takes from the front — so
			// this is the last command actually folded in, which is what the
			// snapshot's `ack` promises.
			occ.LastSeq = c.Seq
		}

		// THE IDLE FILL, and the game does not work without it.
		//
		// The money ramp is time-based and the client sends NOTHING while
		// standing still (a frame per tick of a player doing nothing is the
		// exact mistake «ВАНЯДУМ» shipped once). So any part of this tick that
		// no command claimed is simulated as standing perfectly still: that is
		// what accrues the salary, grows the streak and makes the whole game
		// happen. Without it a player who stood still would earn nothing, which
		// is the one outcome the design cannot have.
		//
		// It consumes the budget too, so quiet time cannot be BANKED and then
		// spent on movement — earning the ramp and hoarding simulated seconds to
		// dodge with would be having it both ways.
		//
		// ONLY WHEN THE CLIENT CLAIMED NOTHING AT ALL, and the guard is the
		// whole correctness of it. A client that is standing still sends no
		// frame whatsoever, so `spent == 0` is exactly the state this exists
		// for. A client that IS sending has merely under-filled the tick by a
		// millisecond or two of ordinary clock drift — a browser timer does not
		// tile a 50 ms tick evenly — and fabricating stillness in that gap is
		// inventing input the player did not give.
		//
		// It was doing precisely that, and the dash is where it showed. A still
		// step is NOT a still step while a dash is running: `Step` reads
		// DashLeft, and since the dash became a committed movement it carries
		// the player along DashDX/DashDY at the full 20 m/s. A browser timer
		// does not tile a 50 ms tick evenly, so an unguarded fill fired on most
		// ticks and added a sliver of dash the client had not predicted.
		//
		// Which makes the guard load-bearing in BOTH directions, and worth
		// stating plainly because it is not obvious: this fill is also the only
		// thing that moves a dashing player whose client has gone quiet. It ran
		// the whole burst by itself for one build, while the browser predicted a
		// single sub-step of it — 5.5 m against 0.5 m, snapped back and forth
		// two metres at a time. The client no longer goes quiet mid-dash (see
		// `due` in web/src/lib/fintechPredict.ts), so the fill's job during a dash
		// is now only to cover a stalled or lagging one — which it does at
		// exactly the right speed, and which is why it is not guarded on
		// DashLeft as well.
		//
		// Nothing is lost by skipping it: money accrues only while standing
		// still, so an unclaimed sliver during movement was never worth
		// anything, and the budget carries it to the next tick regardless.
		//
		// AND ONLY WHILE THE QUEUE IS EMPTY, which is the guard that lets the
		// drain above wait rather than truncate. The fill consumes the budget, so
		// a fill that ran while a command sat waiting for budget would consume
		// exactly the budget that command was waiting for — and the two would
		// deadlock: the command is never affordable, so nothing is ever spent, so
		// the fill runs again, for ever. The fill exists for a client that sent
		// NOTHING; a client whose command is waiting has sent something.
		if idle := dt - spent; idle > 0 && spent == 0 && len(occ.Pending) == 0 {
			occ.State = Step(o.plan.rects, occ.State, Command{Dt: idle})
			occ.Budget = math.Max(0, occ.Budget-idle)
		}
	}

	// Built in ascending account order, which is what makes his choice of victim
	// deterministic when two people are equally close — see StepBoss.
	// A CLOUD IS EXPRESSED AS A SHORTER TARGET LIST, which is the arrangement the
	// redirect already established: StepBoss walks at the nearest of whatever it is
	// given, so leaving somebody out IS "he cannot see you" — the boss needs no
	// knowledge of the hookah and there is still one pursuit implementation.
	//
	// EXCLUDING rather than only refusing to catch is what makes the reprieve buy
	// DISTANCE. He stops on arrival at the catch radius, so a guard alone would
	// leave him standing on you, and the tick after the cloud cleared would end the
	// shift. Excluded, he loses interest and walks at somebody else — or goes home.
	targets := make([]Vec2, 0, len(keys))
	// EVERY BODY ON THE FLOOR, which is a different list from the targets and is
	// why it is built rather than reused: a cloud takes a man out of the pursuit,
	// not out of the room. Claude is kept out of these (see Separate); nobody is
	// kept out of anybody else, because the players are the only things here whose
	// positions are predicted in a browser.
	bodies := make([]Vec2, 0, len(keys))
	standing := 0
	for _, k := range keys {
		occ := o.occupants[k]
		if !occ.State.Alive {
			continue
		}
		standing++
		bodies = append(bodies, occ.State.Pos)
		if occ.Invincible > 0 {
			continue
		}
		targets = append(targets, occ.State.Pos)
	}
	// He had somebody and now has nobody, which is a thing he says rather than a
	// thing he does. Recomputed every tick, so it cannot go stale.
	o.lostTarget = standing > 0 && len(targets) == 0

	// THE REDIRECT IS EXPRESSED AS A SHORTER TARGET LIST, not as a second
	// pursuit rule. StepBoss walks at the nearest of whatever it is given, so
	// handing it exactly one target IS "walk at him regardless of who is nearer"
	// — the boss needs no knowledge of the verb, and there is one pursuit
	// implementation rather than two that have to agree.
	if o.redirectLeft > 0 {
		o.redirectLeft -= dt
		if occ, ok := o.occupants[o.redirectTo]; ok && occ.State.Alive && o.redirectLeft > 0 {
			targets = []Vec2{occ.State.Pos}
		} else {
			// He has been promoted, walked out, or the window closed. Back to
			// whoever is nearest, immediately.
			o.redirectTo, o.redirectLeft = "", 0
		}
	}

	// The verb's timers, which are the office's rather than the simulation's —
	// see the fields on Occupant.
	for _, k := range keys {
		occ := o.occupants[k]
		if occ.RedirectCool > 0 {
			occ.RedirectCool = math.Max(0, occ.RedirectCool-dt)
		}
		if occ.Announce > 0 {
			occ.Announce = math.Max(0, occ.Announce-dt)
		}
		if occ.Invincible > 0 {
			occ.Invincible = math.Max(0, occ.Invincible-dt)
		}
	}

	// THE ROUTER, which is the office's rather than anybody's. Ticked before Claude
	// is stepped so an absence that ends on this tick puts him back on the floor on
	// this tick, exactly as a round bought this tick slows the лысый on this tick.
	if o.routerCool > 0 {
		o.routerCool = math.Max(0, o.routerCool-dt)
	}
	if o.routerAway > 0 {
		o.routerAway = math.Max(0, o.routerAway-dt)
		if o.routerAway == 0 {
			// HE COMES BACK THROUGH THE DOOR RATHER THAN REAPPEARING WHERE HE STOOD.
			// A man who vanishes at your desk and rematerialises at your desk twelve
			// seconds later has not been anywhere, and the reprieve would end with
			// him already on top of you. His spawn is the far corner, which is the
			// same head start a shift opens with.
			o.claude = NewChaser()
		}
	}

	// HOW MANY OF EACH PROP THERE ARE, which is one per person on the floor. A
	// single bottle in a room of three is a race the nearest man wins every time,
	// and the other two stop walking to it at all.
	want := standing * PropsPerPlayer
	if want < 1 {
		want = 1
	}
	o.bottles = reconcileProps(o.bottles, BottleSpots, want)
	o.hookahs = reconcileProps(o.hookahs, HookahSpots, want)

	// THE BOTTLES, checked before he steps so a round bought this tick slows him
	// on this tick rather than the next. Whoever reaches one gets it — in ascending
	// account order, like every other decision here, so two people arriving on the
	// same tick is settled the same way his choice of victim is.
	for i := range o.bottles {
		if taken := o.stepProp(&o.bottles[i], BottleSpots, o.bottles, BottleReturn, BottleReach, dt, keys); taken != nil {
			// HIS state rather than the taker's: one Карен buys the round and
			// everybody watches him wobble. Assigned rather than accumulated, so a
			// second bottle in a full office extends the wobble rather than
			// stacking it into something the game cannot be played against.
			o.boss.Drunk = DrunkSeconds
		}
	}

	// THE KALYANS, in the same place and for the same reason as the bottles: a
	// cloud taken this tick hides you on this tick rather than the next. First
	// taker only — one cloud per кальян.
	for i := range o.hookahs {
		if taken := o.stepProp(&o.hookahs[i], HookahSpots, o.hookahs, HookahReturn, HookahReach, dt, keys); taken != nil {
			taken.Invincible = InvincibleSeconds
		}
	}

	// Elapsed simulated time, which is what the wobble and the TEMPO are functions
	// of. Derived from the tick rather than from a clock, so it is the same number
	// on every process that replays the same office — and so the browser can
	// compute the tempo from the `k` its snapshot already carries.
	elapsed := float64(o.tick) * SimStep.Seconds()
	o.boss = StepBoss(o.plan, o.boss, targets, dt, elapsed)

	// AND CLAUDE, stepped against the same target list — so a cloud hides you from
	// both of them, which is the answer a player expects from something called
	// invincibility. He is deliberately NOT redirectable: the verb is «уточните у
	// другого», which is a thing you say to a manager and not to a colleague with
	// an opinion about your tooling. Same elapsed, so the two of them ramp together.
	//
	// UNLESS THE ROUTER IS DOWN, in which case he is not on the floor at all: not
	// stepped, not separated, not tested against anybody, and not on the wire. He
	// keeps his last position and it is never sent, because he is about to be put
	// back at his spawn when the absence ends.
	if o.routerAway <= 0 {
		o.claude = StepChaser(o.plan, o.claude, targets, dt, elapsed)
	}

	// AND THEN HE STEPS ASIDE IF HE HAS WALKED INTO SOMEBODY — the лысый, whose
	// pursuit rule, navigator and speed are his, so their paths do not cross but
	// MERGE; or a player, whose centre he walks at and does not stop on arrival,
	// so against somebody standing still he ends up inside him and invisible under
	// him. Claude is the one who gives way in both cases, so neither the лысый's
	// position nor a player's is a function of his. See Separate.
	if o.routerAway <= 0 {
		o.claude = Separate(o.boss.Pos, bodies, o.claude, o.plan.rects)
	}

	// AND THE TWO WHO ARE NOT PLAYING. Stepped after both men and against neither of
	// them: they are not in `targets`, so nobody walks at them and they walk at
	// nobody. They carry their own кальян and never touch the office's one.
	for i := range o.npcs {
		o.npcs[i] = StepNPC(o.plan.rects, o.npcs[i], dt)
	}

	// RECORDED AFTER BOTH MEN HAVE STEPPED and before either is tested against
	// anybody, so index `tick` is the world the snapshot about to go out
	// describes. Recording before would leave the ring a tick behind everything
	// that reads it.
	o.trail[o.tick%uint64(trailLen)] = trailPoint{boss: o.boss.Pos, claude: o.claude.Pos}

	// He LANDS rather than catches, and the difference is the whole point of him.
	// Assigned rather than accumulated, exactly as the лысый's drink is: two
	// applications would leave a walk at 4.096 m/s against his own 4.0, which is
	// not an escape, and three would make the game unwinnable.
	//
	// REWOUND LIKE THE CATCH, and for the same reason rather than for consistency
	// alone: he is drawn from the same interpolation buffer, so landing on
	// somebody the player watched him miss is the identical complaint with a
	// smaller consequence.
	if o.routerAway <= 0 {
		for _, k := range keys {
			occ := o.occupants[k]
			if !occ.State.Alive || occ.Invincible > 0 {
				continue
			}
			if Landed(o.seenBy(occ).claude, occ.State.Pos) {
				occ.State.SlowLeft = SlowSeconds
			}
		}
	}

	var ended []*Occupant
	for _, k := range keys {
		occ := o.occupants[k]
		switch {
		// THE GUARD IS ON THIS CASE ALONE, deliberately. Put it on the switch and an
		// invincible occupant who closed the tab would hold a slot in a
		// three-person office until the process restarted, because the abandon
		// branch below shares it. Being uncatchable is not being immortal.
		case occ.Invincible <= 0 && Caught(o.seenBy(occ).boss, occ.State.Pos):
			occ.Ended, occ.Cause = true, CausePromoted
			occ.State.Alive = false
		case now.Sub(occ.LastSeen) > AbandonGrace:
			// Nobody has been connected for a minute and a half. Unlike
			// «ВАНЯДУМ», this one IS recorded: a забег somebody walked away from
			// is not a result, but a SHIFT somebody walked away from is exactly
			// what this game is about.
			occ.Ended, occ.Cause = true, CauseLeft
			occ.State.Alive = false
		default:
			continue
		}
		ended = append(ended, occ)
		delete(o.occupants, k)
	}
	return ended
}

// SnapshotFor renders the office from one occupant's point of view, already
// marshalled, because the caller is holding no lock by the time it publishes.
func (o *Office) SnapshotFor(accountID string) ([]byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok {
		return nil, false
	}
	s := Snapshot{
		T:    TypeSnapshot,
		Tick: o.tick,
		Ack:  occ.LastSeq,
		X:    cm(occ.State.Pos.X),
		Y:    cm(occ.State.Pos.Y),
		Pay:  rub(occ.State.Salary),
		M:    hundredths(Multiplier(occ.State.Streak)),
		St:   ms(occ.State.Streak),
		Dc:   msUp(occ.State.DashCooldown),
		// The balloons, as indexes. Both are derived here rather than stored,
		// because both are pure functions of state this frame already carries —
		// so nothing has to be kept in sync, nothing expires, and two people
		// looking at the same office are told the same thing by construction.
		P:  o.lineFor(occ),
		Rc: msUp(occ.RedirectCool),
		B: BossFrame{
			X: cm(o.boss.Pos.X),
			Y: cm(o.boss.Pos.Y),
			G: grinByte(o.boss.Grin),
			P: BossSays(o.bossState(), o.boss.Grin, o.tick, occ.Invincible > 0),
			D: msUp(o.boss.Drunk),
		},
		Bs: propMask(o.bottles),
		Iv: msUp(occ.Invincible),
		Sl: msUp(occ.State.SlowLeft),
		// CLAUDE, OR NOTHING AT ALL. While the router is down he is off the floor,
		// so the frame says so by leaving him out rather than by sending a stale
		// position with a flag beside it — `ca` is what carries how long he is gone
		// for, and it is the only field either of them costs in that state.
		Cl: o.claudeFrame(),
		Ca: msUp(o.routerAway),
		Rd: msUp(o.routerCool),
		Hs: propMask(o.hookahs),
		Np: o.npcsFor(),
		Pr: o.peersFor(accountID),
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// claudeFrame is Claude Code as the wire carries him, or nil while the router is
// down and he is not on the floor. Called with the lock held.
func (o *Office) claudeFrame() *ClaudeFrame {
	if o.routerAway > 0 {
		return nil
	}
	return &ClaudeFrame{
		X: cm(o.claude.Pos.X),
		Y: cm(o.claude.Pos.Y),
		C: grinByte(o.claude.Cig),
		P: ClaudeSays(o.tick),
	}
}

// lineFor is which line is over an occupant's head.
//
// The announcement OUTRANKS the rotation: for a few seconds after a verb, that is
// what you are saying, because the whole point of a verb is that your colleagues
// can see who did it. WHICH announcement is on the occupant — there are two of
// them now, and a hardcoded RedirectLine here would have put «ЭТО НУЖНО УТОЧНИТЬ
// У ДРУГОГО» over the head of somebody who took the router down. Everything else
// is the ordinary two-second rotation. Called with the lock held.
func (o *Office) lineFor(occ *Occupant) int {
	if occ.Announce > 0 {
		return occ.AnnounceLine
	}
	return PlayerLine(occ.State, occ.Persona, o.tick)
}

// peersFor is everybody in the office except the account being addressed.
// Called with the lock held.
//
// Built per addressee rather than once per tick, which is O(occupants²) at 10 Hz
// — nine cheap comparisons a second at MaxOccupants, and the alternative is a
// shared slice that would have to be filtered per recipient anyway.
//
// A DEAD OCCUPANT IS OMITTED. The tick that catches somebody deletes them, so
// this is only reachable in the instant between the two, and drawing a figure
// that is no longer in the simulation is worse than drawing nothing.
// npcsFor is the two non-players, as the plane needs them.
//
// NEVER OMITTED, like Claude: they are always on the floor. That is about sixty
// bytes on every frame for two figures, which is the price of stepping them on the
// server — see npc.go for why that is the arrangement.
func (o *Office) npcsFor() []NPCFrame {
	if len(o.npcs) == 0 {
		return nil
	}
	out := make([]NPCFrame, 0, len(o.npcs))
	for i, n := range o.npcs {
		out = append(out, NPCFrame{
			X: cm(n.Pos.X),
			Y: cm(n.Pos.Y),
			P: NPCSays(i, o.tick),
		})
	}
	return out
}

func (o *Office) peersFor(accountID string) []PeerFrame {
	if len(o.occupants) < 2 {
		return nil
	}
	peers := make([]PeerFrame, 0, len(o.occupants)-1)
	// keys() rather than the map: a slice's ORDER is part of the value, and a
	// randomised one would make two consecutive frames differ for no reason —
	// which is a diff on the wire, a re-render on the client, and a test that
	// passes four times in five.
	for _, k := range o.keys() {
		if k == accountID {
			continue
		}
		occ := o.occupants[k]
		if !occ.State.Alive {
			continue
		}
		peers = append(peers, PeerFrame{
			I:  occ.Pseudonym,
			X:  cm(occ.State.Pos.X),
			Y:  cm(occ.State.Pos.Y),
			P:  o.lineFor(occ),
			Iv: msUp(occ.Invincible),
			Sl: msUp(occ.State.SlowLeft),
		})
	}
	if len(peers) == 0 {
		return nil
	}
	return peers
}

// Redirect points the bald man at somebody else for RedirectSeconds.
//
// Returns whether it fired. It is refused — silently, like every other bad
// frame — when the caller is not working, is on cooldown, names somebody who is
// not in the office, or names himself. A refusal is not an error: the client
// disables the button from the cooldown the snapshot carries, so a refused verb
// means the two disagreed for a frame, which is not worth a reply.
func (o *Office) Redirect(accountID, targetHandle string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	caller, ok := o.occupants[accountID]
	if !ok || !caller.State.Alive || caller.RedirectCool > 0 {
		return false
	}
	for _, k := range o.keys() {
		occ := o.occupants[k]
		if occ.Pseudonym != targetHandle || k == accountID || !occ.State.Alive {
			continue
		}
		o.redirectTo, o.redirectLeft = k, RedirectSeconds
		caller.RedirectCool = RedirectCooldown
		caller.Announce, caller.AnnounceLine = RedirectSaySeconds, RedirectLine
		return true
	}
	return false
}

// RouterDown takes Claude Code off the floor for RouterSeconds.
//
// Returns whether it fired, and is refused — silently, like every other bad frame
// — when the caller is not working, when the router is already down, or when it
// has not come back up yet. A refusal is not an error: the client disables the
// button from the cooldown the snapshot carries, so a refused verb means the two
// disagreed for a frame.
//
// THE COOLDOWN IS THE OFFICE'S, NOT THE CALLER'S, which is what «anybody can
// press it» actually costs. Three occupants each holding their own thirty-second
// timer would cover thirty-six seconds of absence in every thirty, and Claude
// would simply never be on the floor again.
func (o *Office) RouterDown(accountID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	caller, ok := o.occupants[accountID]
	if !ok || !caller.State.Alive {
		return false
	}
	if o.routerCool > 0 || o.routerAway > 0 {
		return false
	}
	o.routerAway = RouterSeconds
	o.routerCool = RouterCooldown
	caller.Announce, caller.AnnounceLine = RouterSaySeconds, RouterLine
	return true
}

// bossState is what has most recently happened to him. Called with the lock held.
//
// Drunk outranks redirected, because being drunk is the louder thing to be and
// because a man who is both is funnier saying the drink lines.
func (o *Office) bossState() BossState {
	switch {
	case o.boss.Drunk > 0:
		return BossDrunk
	// LOST OUTRANKS THE REDIRECT, because it outranks it mechanically too: a verb
	// that points him at somebody who is behind a cloud has pointed him at nobody,
	// and «тогда я к нему» would be a lie while he is standing in an empty room
	// looking for a man who is not there.
	case o.lostTarget:
		return BossLost
	case o.redirectLeft > 0:
		return BossRedirected
	default:
		return BossIdle
	}
}

// stepProp advances one prop and returns the occupant who just took it, or nil.
//
// It is the whole of a prop's life in one place, and both kinds share it because
// they differ only in WHAT taking one does — the caller applies that. Called with
// the lock held.
//
// The order inside matters: a prop that is away is only counted down, so it
// cannot be taken on the tick it returns, and the taker is the first occupant in
// ascending account order within reach.
func (o *Office) stepProp(p *prop, spots []Vec2, siblings []prop, back, reach, dt float64, keys []string) *Occupant {
	if p.gone > 0 {
		p.gone = math.Max(0, p.gone-dt)
		if p.gone == 0 {
			// It comes back SOMEWHERE ELSE. Drawn rather than cycled, because a
			// rotation would be a pattern to learn and then to stand in front of.
			p.spot = drawSpot(spots, siblings, p.spot)
		}
		return nil
	}
	at := spots[clampInt(p.spot, 0, len(spots)-1)]
	for _, k := range keys {
		occ := o.occupants[k]
		if !occ.State.Alive {
			continue
		}
		if math.Hypot(occ.State.Pos.X-at.X, occ.State.Pos.Y-at.Y) <= reach+PlayerRadius {
			p.gone = back
			return occ
		}
	}
	return nil
}

// reconcileProps grows a kind's list to one per person and shrinks it only by
// dropping something nobody can reach.
//
// GROWTH IS IMMEDIATE, SHRINKING IS NOT, and the asymmetry is deliberate: a
// joiner should find a prop of their own at once, but a prop already standing on
// the floor must never vanish from under whoever is walking towards it. So an
// extra one is dropped only while it is away — which is to say, after somebody
// has taken it — and until then a half-empty office simply has a spare.
func reconcileProps(props []prop, spots []Vec2, want int) []prop {
	if want > len(spots) {
		want = len(spots)
	}
	for len(props) < want {
		props = append(props, prop{spot: drawSpot(spots, props, -1)})
	}
	for len(props) > want {
		dropped := false
		for i := range props {
			if props[i].gone > 0 {
				props = append(props[:i], props[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			break
		}
	}
	return props
}

// drawSpot picks where a prop stands: somewhere none of its siblings is, and
// never where this one just was.
//
// «Never where it just was» is the rule that makes fetch-and-spend a walk rather
// than a button — a prop reappearing under your feet would hand you a second
// round for standing still, which is the one thing this game already pays for.
// «Nowhere a sibling is» is its extension to a room with several: two bottles on
// one tile is one bottle drawn twice.
//
// Both are preferences with a floor. If every spot is taken the avoid rule is
// dropped first and the sibling rule second, because a prop somewhere is always
// better than a prop nowhere. With three occupants against six bottle spots and
// four hookah spots that floor is unreachable today; it is here so that shrinking
// either catalogue cannot wedge the tick.
func drawSpot(spots []Vec2, siblings []prop, avoid int) int {
	if len(spots) == 0 {
		return 0
	}
	used := make(map[int]bool, len(siblings))
	for _, s := range siblings {
		if s.spot != avoid {
			used[s.spot] = true
		}
	}
	free := make([]int, 0, len(spots))
	for i := range spots {
		if !used[i] && i != avoid {
			free = append(free, i)
		}
	}
	if len(free) == 0 {
		for i := range spots {
			if !used[i] {
				free = append(free, i)
			}
		}
	}
	if len(free) == 0 {
		return clampInt(avoid, 0, len(spots)-1)
	}
	//nolint:gosec // bounded by len(free), which is a handful
	return free[int(unitRand()*float64(len(free)))%len(free)]
}

// propMask is which of a catalogue's spots have one standing on them right now,
// as a bit per spot.
//
// A MASK RATHER THAN A LIST, and it is what keeps several props as cheap on the
// wire as one was: `"bs":21` is eight bytes whatever it describes, where an array
// of indexes grows with the office and an array of positions would be twenty
// bytes a prop, twenty times a second, per viewer, to say something that changes
// once every ten seconds. Omitted at zero like every other index here — and zero
// means «none of them is standing», which is a state the office really has.
func propMask(props []prop) int {
	mask := 0
	for _, p := range props {
		if p.gone <= 0 {
			mask |= 1 << p.spot
		}
	}
	return mask
}

// AvatarFor is the picture to draw on the peer a frame calls handle, and whether
// there is one.
//
// THE PSEUDONYM IS THE LOOKUP KEY, which is the whole of why a URL never rides a
// frame (ADR-037): a couple of hundred characters re-sent ten times a second per
// viewer, to say something that cannot change during a shift. Asked for by the
// handle the client already has, it costs one cached GET per face instead.
//
// A linear scan over at most three occupants, comparing the pseudonym each one
// already carries — so unlike the yard there is no HMAC to re-derive and no
// second map to fall out of step with the first.
//
// An unknown handle is not an error: it is the ordinary answer for somebody who
// has just walked out, and for anybody whose account has no picture.
func (o *Office) AvatarFor(handle string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, k := range o.keys() {
		if occ := o.occupants[k]; occ.Pseudonym == handle && occ.Avatar != "" {
			return occ.Avatar, true
		}
	}
	return "", false
}

// ShiftOf is which shift an account is working, if any: its id, which employee
// they are, and the office tick they clocked in on.
//
// Four returns rather than a struct, because every one of them is a scalar that
// goes straight onto one wire message (Ready) and a struct here would be a type
// declared for one call site. The start tick is last because only the socket's
// hello needs it — the HTTP reload path drops it.
func (o *Office) ShiftOf(accountID string) (string, int, uint64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if occ, ok := o.occupants[accountID]; ok {
		return occ.ShiftID, occ.Persona, occ.StartTick, true
	}
	return "", 0, 0, false
}

// Occupants is how many people are on the floor.
func (o *Office) Occupants() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.occupants)
}

// Empty reports that everybody has gone home, which is when the service drops
// the office entirely.
func (o *Office) Empty() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.occupants) == 0
}

// Tick is the simulation step the office is on. The service reads it to decide
// which ticks publish a snapshot, and it is on every frame as the client's
// timeline.
func (o *Office) Tick() uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tick
}

// seenBy is where both men were on the office's last tick that this occupant
// can actually have DRAWN. Called with the lock held.
//
// Two terms, and they are different things. The round trip is how stale the
// newest frame in their browser is by the time their answer gets back here; the
// render delay is how much further into the past that browser deliberately draws
// everything it does not predict, which is the interpolation buffer's whole
// mechanism. Add them and you have the instant on their screen.
//
// A brand-new occupant has no round trip yet and gets the render delay alone,
// which is the right floor: every client draws at least that far behind, and
// nobody's first frames should be resolved as though their connection were
// perfect.
func (o *Office) seenBy(occ *Occupant) trailPoint {
	rewind := occ.RTT + RenderDelaySeconds
	if rewind > CatchRewindMax {
		rewind = CatchRewindMax
	}
	back := uint64(math.Round(rewind / SimStep.Seconds()))
	if back >= uint64(trailLen) {
		back = uint64(trailLen) - 1
	}
	// Early in a shift the ring is not full yet, so a rewind past the first tick
	// reads a zero value — which would put both men in the corner and catch
	// nobody. Clamped to the office's own age instead.
	if back > o.tick {
		back = o.tick
	}
	return o.trail[(o.tick-back)%uint64(trailLen)]
}

// keys is every occupant, sorted. Called with the lock held.
//
// NOTHING IN THIS GAME ITERATES A MAP TO PRODUCE A RESULT. Map order is
// randomised in Go, so a boss choosing between two equally distant targets, or
// two players' steps interleaving, would differ between processes and between
// runs of the same test. Three keys is nothing to sort.
func (o *Office) keys() []string {
	out := make([]string, 0, len(o.occupants))
	for k := range o.occupants {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
