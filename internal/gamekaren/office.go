package gamekaren

import (
	"encoding/json"
	"math"
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
	State     Player
	// Pending are commands received but not yet stepped. Drained on the tick
	// rather than applied on arrival, so the simulation advances on its own
	// clock and never on a client's read pump.
	Pending []Command
	// LastSeq is the last command sequence actually folded in, echoed to the
	// client as the snapshot's `ack`.
	LastSeq uint32
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
}

// NewOffice opens the floor with the bald man at the far wall and nobody in it.
func NewOffice() *Office {
	return &Office{occupants: make(map[string]*Occupant, MaxOccupants), boss: NewBoss()}
}

// Join puts an account to work.
//
// A second shift for the same account is REFUSED rather than replacing the
// first: dropping the running one would throw away a shift somebody is in the
// middle of on their other tab, and nothing here can tell which one they meant.
// The client's answer is the quit button.
func (o *Office) Join(accountID, shiftID string, now time.Time) error {
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
		State:     NewPlayer(),
		StartedAt: now,
		LastSeen:  now,
	}
	return nil
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
// COMMANDS ALREADY APPLIED ARE DROPPED HERE, by sequence number. That single
// rule is what makes input redundancy free: a client may resend the tail of its
// unacknowledged commands in every frame, so one lost packet costs no input at
// all, and a client that resends everything forever gains nothing because the
// second copy never reaches the queue. Without it a replayed command would be
// movement that happens twice on the server and once on the client, which the
// player feels as being dragged.
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
		// everything already applied.
		if c.Seq <= occ.LastSeq {
			continue
		}
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
		for len(occ.Pending) > 0 && occ.Budget > 0 {
			c := occ.Pending[0]
			occ.Pending = occ.Pending[1:]
			// Spend no more than has actually elapsed. A command longer than the
			// remaining budget is simulated for as much of itself as the player
			// has paid for rather than being dropped — dropping it would make a
			// laggy client stutter, where truncating merely makes it slower for
			// one step.
			if c.Dt > occ.Budget {
				c.Dt = occ.Budget
			}
			occ.Budget -= c.Dt
			spent += c.Dt
			occ.State = Step(Desks, occ.State, c)
			// Acknowledged only once it has ACTUALLY been folded in, so a
			// command truncated by the budget is not reported as applied — the
			// client would drop it from its pending list and its prediction
			// would drift permanently by whatever the server declined to
			// simulate.
			if c.Seq > occ.LastSeq {
				occ.LastSeq = c.Seq
			}
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
		// `due` in web/src/lib/karenPredict.ts), so the fill's job during a dash
		// is now only to cover a stalled or lagging one — which it does at
		// exactly the right speed, and which is why it is not guarded on
		// DashLeft as well.
		//
		// Nothing is lost by skipping it: money accrues only while standing
		// still, so an unclaimed sliver during movement was never worth
		// anything, and the budget carries it to the next tick regardless.
		if idle := dt - spent; idle > 0 && spent == 0 {
			occ.State = Step(Desks, occ.State, Command{Dt: idle})
			occ.Budget = math.Max(0, occ.Budget-idle)
		}
	}

	// Built in ascending account order, which is what makes his choice of victim
	// deterministic when two people are equally close — see StepBoss.
	targets := make([]Vec2, 0, len(keys))
	for _, k := range keys {
		if occ := o.occupants[k]; occ.State.Alive {
			targets = append(targets, occ.State.Pos)
		}
	}
	o.boss = StepBoss(Desks, o.boss, targets, dt)

	var ended []*Occupant
	for _, k := range keys {
		occ := o.occupants[k]
		switch {
		case Caught(o.boss, occ.State.Pos):
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
		P: KarenLine(occ.State),
		B: BossFrame{
			X: cm(o.boss.Pos.X),
			Y: cm(o.boss.Pos.Y),
			G: grinByte(o.boss.Grin),
			P: BossLine(o.boss.Grin, o.tick),
		},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// ShiftOf is which shift an account is working, if any.
func (o *Office) ShiftOf(accountID string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if occ, ok := o.occupants[accountID]; ok {
		return occ.ShiftID, true
	}
	return "", false
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
