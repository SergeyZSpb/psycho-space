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
// заброшка and therefore has to be per run. This office is a constant in the
// catalogue: the same room, the same eight desks, the same two spawns, every
// time. So there is one of it, everybody who is working shares it, and it is
// dropped entirely when the last person leaves — the next shift builds a fresh
// one with the bald man back at the far wall.
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
	// hookahGone and hookahSpot are the кальян's half of the same arrangement as
	// the bottle's two fields below: zero `hookahGone` means one is standing on
	// `hookahSpot` right now.
	hookahGone float64
	hookahSpot int
	// claude is Claude Code, the second man on the floor. He is a separate value
	// with a separate step for the reason chaser.go states: what happens when he
	// arrives is different, and an interface over two implementations with one
	// common method would be a seam nobody asked to use.
	claude Chaser
	// npcs are Серега and Тёма. Deliberately NOT in `occupants`: the moment
	// anything joins that map it becomes a chase target, a snapshot addressee and a
	// slot against MaxOccupants — so a lazy player would be saved by a colleague
	// who is not playing.
	npcs []NPC
	// lostTarget is true when the лысый had somebody to walk at and now has
	// nobody, because everybody left standing is behind a cloud. It decides which
	// run he speaks from and is recomputed every tick, so it never goes stale.
	lostTarget bool
	// bottleGone is how long until another bottle appears, in seconds. Zero means
	// one is standing there now, which is the common case and therefore the one
	// that costs nothing on the wire.
	bottleGone float64
	// bottleSpot is which of BottleSpots it is standing on. It MOVES: a bottle
	// that always came back to the same place would be a lever you stand next
	// to, and the walk is supposed to be the price.
	bottleSpot int
}

// NewOffice opens the floor with both men at the far wall and nobody in it.
func NewOffice() *Office {
	return &Office{
		occupants: make(map[string]*Occupant, MaxOccupants),
		boss:      NewBoss(),
		claude:    NewChaser(),
		npcs:      NewNPCs(),
	}
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
		for _, d := range Desks {
			if insideDesk(d, at, PlayerRadius) {
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
func (o *Office) Enqueue(accountID string, cmds []Command, now time.Time) {
	if len(cmds) == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	occ, ok := o.occupants[accountID]
	if !ok || occ.Ended {
		return
	}
	occ.LastSeen = now
	for _, c := range cmds {
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
			occ.State = Step(Desks, occ.State, c)
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
			occ.State = Step(Desks, occ.State, Command{Dt: idle})
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
	standing := 0
	for _, k := range keys {
		occ := o.occupants[k]
		if !occ.State.Alive {
			continue
		}
		standing++
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

	// THE BOTTLE, and it is checked before he steps so a round bought this tick
	// slows him on this tick rather than the next. Whoever reaches it gets it —
	// in ascending account order, like every other decision here, so two people
	// arriving on the same tick is settled the same way his choice of victim is.
	if o.bottleGone > 0 {
		o.bottleGone = math.Max(0, o.bottleGone-dt)
		if o.bottleGone == 0 {
			// It comes back SOMEWHERE ELSE. Drawn rather than cycled, because a
			// rotation would be a pattern to learn and then to stand in front of.
			o.bottleSpot = o.drawBottleSpot()
		}
	} else {
		bottle := BottleSpots[o.bottleSpot]
		for _, k := range keys {
			occ := o.occupants[k]
			if !occ.State.Alive {
				continue
			}
			if math.Hypot(occ.State.Pos.X-bottle.X, occ.State.Pos.Y-bottle.Y) <= BottleReach+PlayerRadius {
				o.boss.Drunk = DrunkSeconds
				o.bottleGone = BottleReturn
				break
			}
		}
	}

	// THE KALYAN, checked in the same place and for the same reason as the bottle:
	// a cloud taken this tick hides you on this tick rather than the next, and two
	// people arriving together is settled in ascending account order like every
	// other decision here. First taker only — one cloud per hookah.
	if o.hookahGone > 0 {
		o.hookahGone = math.Max(0, o.hookahGone-dt)
		if o.hookahGone == 0 {
			o.hookahSpot = o.drawHookahSpot()
		}
	} else {
		spot := HookahSpots[o.hookahSpot]
		for _, k := range keys {
			occ := o.occupants[k]
			if !occ.State.Alive {
				continue
			}
			if math.Hypot(occ.State.Pos.X-spot.X, occ.State.Pos.Y-spot.Y) <= HookahReach+PlayerRadius {
				occ.Invincible = InvincibleSeconds
				o.hookahGone = HookahReturn
				break
			}
		}
	}

	// Elapsed simulated time, which is what the wobble is a function of. Derived
	// from the tick rather than from a clock, so it is the same number on every
	// process that replays the same office.
	o.boss = StepBoss(Desks, o.boss, targets, dt, float64(o.tick)*SimStep.Seconds())

	// AND CLAUDE, stepped against the same target list — so a cloud hides you from
	// both of them, which is the answer a player expects from something called
	// invincibility. He is deliberately NOT redirectable: the verb is «уточните у
	// другого», which is a thing you say to a manager and not to a colleague with
	// an opinion about your tooling.
	o.claude = StepChaser(Desks, o.claude, targets, dt)

	// AND THE TWO WHO ARE NOT PLAYING. Stepped after both men and against neither of
	// them: they are not in `targets`, so nobody walks at them and they walk at
	// nobody. They carry their own кальян and never touch the office's one.
	for i := range o.npcs {
		o.npcs[i] = StepNPC(Desks, o.npcs[i], dt)
	}

	// He LANDS rather than catches, and the difference is the whole point of him.
	// Assigned rather than accumulated, exactly as the лысый's drink is: two
	// applications would leave a walk at 4.096 m/s against his own 4.0, which is
	// not an escape, and three would make the game unwinnable.
	for _, k := range keys {
		occ := o.occupants[k]
		if !occ.State.Alive || occ.Invincible > 0 {
			continue
		}
		if Landed(o.claude, occ.State.Pos) {
			occ.State.SlowLeft = SlowSeconds
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
		case occ.Invincible <= 0 && Caught(o.boss, occ.State.Pos):
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
		Bt: msUp(o.bottleGone),
		Bs: o.bottleSpot,
		Iv: msUp(occ.Invincible),
		Sl: msUp(occ.State.SlowLeft),
		Cl: ClaudeFrame{
			X: cm(o.claude.Pos.X),
			Y: cm(o.claude.Pos.Y),
			C: grinByte(o.claude.Cig),
			P: ClaudeSays(o.tick),
		},
		Hk: msUp(o.hookahGone),
		Hs: o.hookahSpot,
		Np: o.npcsFor(),
		Pr: o.peersFor(accountID),
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// lineFor is which line is over an occupant's head.
//
// The announcement OUTRANKS the rotation: for RedirectSaySeconds after pointing
// the bald man at somebody, that is what you are saying, because the whole point
// of the verb is that your colleague can see who did it to him. Everything else
// is the ordinary two-second rotation. Called with the lock held.
func (o *Office) lineFor(occ *Occupant) int {
	if occ.Announce > 0 {
		return RedirectLine
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
		caller.Announce = RedirectSaySeconds
		return true
	}
	return false
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

// drawHookahSpot picks where the next кальян stands. Called with the lock held.
//
// Never the one it was just on, for the bottle's reason: a prop that reappeared
// under your feet would hand you a second cloud for standing still, which is the
// one thing this game already pays you for.
func (o *Office) drawHookahSpot() int {
	if len(HookahSpots) < 2 {
		return 0
	}
	//nolint:gosec // bounded by len(HookahSpots)-1, which is a handful
	next := int(unitRand() * float64(len(HookahSpots)-1))
	if next >= o.hookahSpot {
		next++
	}
	return clampInt(next, 0, len(HookahSpots)-1)
}

// drawBottleSpot picks where the next bottle stands. Called with the lock held.
//
// Never the one it was just on: a bottle that reappeared under your feet would
// make the whole mechanic a button rather than a walk, and "somewhere else" is
// the entire point of it moving.
func (o *Office) drawBottleSpot() int {
	if len(BottleSpots) < 2 {
		return 0
	}
	//nolint:gosec // bounded by len(BottleSpots)-1, which is a handful
	next := int(unitRand() * float64(len(BottleSpots)-1))
	if next >= o.bottleSpot {
		next++
	}
	return clampInt(next, 0, len(BottleSpots)-1)
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

// ShiftOf is which shift an account is working, if any.
func (o *Office) ShiftOf(accountID string) (string, int, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if occ, ok := o.occupants[accountID]; ok {
		return occ.ShiftID, occ.Persona, true
	}
	return "", 0, false
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
