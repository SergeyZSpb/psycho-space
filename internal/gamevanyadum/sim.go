package gamevanyadum

import "math"

// The simulation: one player, one command, one fixed step.
//
// EVERYTHING HERE IS PURE. Step takes a level, a player and a command and
// returns the next player. It reads no clock, holds no state, allocates nothing
// that outlives it, and never touches the database — which is what makes the
// whole of the game's movement table-testable without a socket, without a
// goroutine and without a sleep.
//
// It is also written this way for a reason that has not arrived yet. If the feel
// gate in iteration 1 fails, the netcode climbs to client-side prediction, and
// prediction means this exact function running in the browser as well. When that
// day comes, the port is pinned by golden vectors generated from the Go tests —
// which is only possible while Step depends on nothing ambient. Do not give it a
// clock, a random source or a map iteration.

// collisionPasses is how many times the resolver sweeps the wall list. One pass
// handles a flat wall; a second is what un-wedges an inside corner, where being
// pushed off one wall pushes you into the other. Three is one more than has ever
// been needed and still costs nothing at this level size.
const collisionPasses = 3

// PickupReach is how close the player's centre has to come to a thing on the
// floor before he picks it up. It is generous on purpose: there is no use button
// (see content.go), so the only way to fail to collect something is to walk past
// it, and a tight radius on a phone reads as the game ignoring you.
const PickupReach = 0.9

// Command is one sub-step of player input. Several arrive in a frame, because
// the socket allows ten frames a second and the client samples at forty — so a
// frame carries the steps that happened between sends rather than throwing them
// away. See MaxCommandsPerFrame.
//
// MX and MY are the movement axes in the player's own frame: MX strafes right,
// MY walks forward, each in −1..1. Yaw and Pitch are absolute view angles in
// radians, not deltas — the client owns where it is looking and the server
// merely clamps it, because aim is an input rather than a simulated quantity.
type Command struct {
	// Seq is the client's own monotonic per-command counter. The server echoes
	// the last one it applied so the client can drop acknowledged commands from
	// its pending list and replay the rest — the whole of reconciliation.
	Seq   int64
	Dt    float64
	MX    float64
	MY    float64
	Yaw   float64
	Pitch float64
	// Fire is a REQUEST to pull the trigger, and it is the first thing on this
	// struct that is not a continuous quantity: the axes and the angles describe
	// a state the client is in, where this describes a moment it wants.
	//
	// A REQUEST AND NEVER A CLAIM, exactly like the rest of this type. It says
	// "I pulled the trigger", not "I fired" and certainly not "I hit somebody" —
	// whether anything comes of it is Step's to decide, against a gun state the
	// server owns.
	//
	// A BOOL AND NOT A BUTTON BITFIELD, which is what the comment on wireCommand
	// used to anticipate. There is one button today; a bitfield with one bit set
	// in it is an abstraction bought against a second button nobody can name, and
	// this project asks for the second use before the seam (CLAUDE.md). Adding a
	// second bool later costs one field on two structs and one line in the port —
	// less than the bit-shuffling on both ends would have cost all along.
	Fire bool
}

// Player is one occupant of the заброшка.
//
// Counters is the generic bag every pickup grants into, keyed by the catalogue's
// Grants field, so the HUD and the snapshot both iterate rather than naming
// anything — which is what makes the syringe and the keys catalogue entries
// rather than code.
//
// THE GUN IS HERE, AND THAT IS A DECISION ADR-058 DID NOT QUITE COVER. Its test
// is "does Step have to read this to produce the same POSITION?", and the gun
// fails it — a cooldown moves nobody. But the record's real subject is the
// question behind that test — does the CLIENT have to simulate this — and the
// answer for the trigger is yes, for a reason movement never raised: the client
// decides whether to draw a muzzle flash the instant a thumb lands, and it can
// only do that by running the same refusal the server is about to run. So the
// gun is on Player, in the port, in the golden vectors and in the reconcile
// spread, and this iteration pays the five coordinated edits that record prices.
//
// THESE ARE THE FIRST FIELDS HERE THAT ARE READ-MODIFY-WRITE. Everything above
// is REPLACED — by a snapshot, or by the client's own thumb — where a countdown
// is DECREMENTED, so applying one command to one state twice moves it twice.
// TestReplayingACommandDecrementsTheCooldownAgain states that property from the
// server's side, where it can be proved, and what it decides is the shape of the
// client's reconciliation.
//
// WHAT IT DECIDES IS THE REPLAY BASE, AND THE ANSWER IS THE SNAPSHOT. The
// predictor first drops every pending command the snapshot has acknowledged, so
// what is left begins exactly where the snapshot's gun stands — replaying that
// list on top of the frame applies each command once and no more. The hazard is
// a base taken from the client's OWN predicted player, which already contains
// those commands' effects: every reconcile would then take their dt off the
// clock a second time, so a walking client would burn its cadence at twice real
// speed and finish a reload early. That is ADR-058's rule read literally —
// every predicted duration is taken from the frame on every reconcile — and a
// reviewer who mutated exactly that base failed five of the port's specs.
//
// THE SNAPSHOT IS ALSO THE ONLY HONEST BASE, for a reason this file owns: the
// world advances the gun through ticks the client sent nothing for (world.go,
// Advance, the idle fill), because somebody who has fired and is standing
// perfectly still emits no commands at all. A clock the client kept for itself
// would stop the moment its owner stopped walking. The whole argument, and the
// object literal it turns into, is in web/src/lib/vanyadumPredict.ts.
type Player struct {
	Pos      Vec2
	Yaw      float64
	Pitch    float64
	Sector   int
	Health   int
	Counters map[string]int
	LastSeq  int64

	// Loaded is how many barrels are ready to fire, 0..Barrels.
	Loaded int
	// CooldownLeft is seconds until the gun will fire again, and ReloadLeft is
	// seconds until a reload finishes — zero when none is running.
	//
	// NEVER BOTH NON-ZERO. A trigger pull is refused while either is running and
	// nothing else sets either, so the gun is busy for one reason at a time.
	// That is not an incidental property: the wire budget is measured on it
	// (message_test.go), and TestTheGunIsOnlyEverBusyForOneReason pins it.
	CooldownLeft float64
	ReloadLeft   float64
}

// NewPlayer places somebody at the level's spawn with a full bar, a loaded gun
// and nothing in his pockets.
//
// LOADED BUT WITH NO BEER, deliberately: the first two shots are free and the
// third costs a walk. Spawning dry would make the first thing a new player does
// pulling a trigger that does nothing, and spawning with a bottle would make the
// bottles scenery.
func NewPlayer(l *Level) Player {
	return Player{
		Pos:      l.Spawn,
		Yaw:      l.SpawnYaw,
		Sector:   l.SpawnSector,
		Health:   StartHealth,
		Counters: map[string]int{},
		Loaded:   Barrels,
	}
}

// ticking reports whether anything on this player is counting down, and so
// whether a step that carries no input would change him at all.
//
// EVERY COUNTDOWN ADDED TO Player BELONGS IN HERE. It is what the world's idle
// fill is guarded on (world.go, Advance): a timer this does not name is a timer
// that stops running the moment its owner stands still and sends nothing, which
// is a bug that only appears when somebody stops moving — the state a player is
// in whenever he is aiming at something.
func (p Player) ticking() bool {
	return p.CooldownLeft > 0 || p.ReloadLeft > 0
}

// Clone copies a player deeply enough that the copy can be stepped without the
// original moving. Only Counters is shared by reference otherwise, and a
// snapshot that aliased it would report the future.
func (p Player) Clone() Player {
	c := p
	c.Counters = make(map[string]int, len(p.Counters))
	for k, v := range p.Counters {
		c.Counters[k] = v
	}
	return c
}

// Sanitise clamps a command into the range the server is willing to simulate.
//
// Every field here is attacker-controlled. A dt of a thousand seconds, a
// movement axis of a million, or a NaN yaw are all one JSON frame away, so each
// is clamped rather than rejected: a refusal would need reporting, and a clamp
// is silent, total and cannot be probed for information.
func (c Command) Sanitise() Command {
	c.Dt = clampFinite(c.Dt, 0, MaxStepSeconds)
	c.MX = clampFinite(c.MX, -1, 1)
	c.MY = clampFinite(c.MY, -1, 1)
	c.Pitch = clampFinite(c.Pitch, -MaxPitch, MaxPitch)
	if math.IsNaN(c.Yaw) || math.IsInf(c.Yaw, 0) {
		c.Yaw = 0
	}
	c.Yaw = math.Mod(c.Yaw, 2*math.Pi)
	return c
}

func clampFinite(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(lo, math.Min(hi, v))
}

// Step advances one player by one command. The command is sanitised here rather
// than at the edge, so there is no path into the simulation that skips it.
func Step(l *Level, p Player, c Command) Player {
	c = c.Sanitise()
	p.Yaw, p.Pitch = c.Yaw, c.Pitch
	// BEFORE the standing-still return below, and that ordering is the whole of
	// whether the gun works: firing while standing perfectly still is not an edge
	// case, it is how anybody shoots at anything. A gun folded in after that
	// return would cool down only while its owner was walking.
	p = stepGun(p, c)

	// The movement axes are in the player's own frame, so yaw turns them into
	// the world. Yaw zero looks along +Y; the client's camera uses the same
	// convention and a test pins it, because the two disagreeing is the sort of
	// bug that looks like broken controls rather than like a broken transform.
	sin, cos := math.Sincos(p.Yaw)
	dx := c.MY*sin + c.MX*cos
	dy := c.MY*cos - c.MX*sin

	// A diagonal must not be faster than a straight line. Normalising only when
	// the vector is longer than one keeps a half-pressed stick analogue.
	if mag := math.Hypot(dx, dy); mag > 1 {
		dx, dy = dx/mag, dy/mag
	}
	if dx == 0 && dy == 0 {
		return p
	}

	dist := WalkSpeed * c.Dt
	target := Vec2{X: p.Pos.X + dx*dist, Y: p.Pos.Y + dy*dist}
	p.Pos, p.Sector = resolve(l, p.Pos, p.Sector, target)
	return p
}

// stepGun advances the обрез by one command: the timers by the command's own dt,
// and then the trigger.
//
// TIMERS FIRST AND THE TRIGGER SECOND. A shot asked for on the very step a
// cooldown runs out is honoured, rather than being refused by a state the gun has
// just left — which at 20 Hz is the difference between a weapon that fires when
// you ask and one that occasionally eats a tap for no reason a player can see.
//
// THE TRIGGER IS THE ONLY THING THAT STARTS A RELOAD, and nothing reloads by
// itself. Pulling on an empty gun is therefore always answered — with a shot, or
// with a reload, or with nothing at all when there is no beer — which is what
// makes an empty gun teach the player about the bottles instead of feeling
// broken. It also means the whole gun is ONE rule read top to bottom, rather
// than a rule plus an "and also, when the gun happens to empty" clause.
//
// NOTHING HERE HITS ANYTHING. A granted shot spends a barrel and starts a
// cooldown, and that is the complete list. The ray, the damage, the death and
// the respawn are the next iteration; splitting them off is what keeps the
// input protocol and the first read-modify-write state on Player debuggable
// without a hit test in the picture.
func stepGun(p Player, c Command) Player {
	if p.ReloadLeft > 0 {
		p.ReloadLeft = math.Max(0, p.ReloadLeft-c.Dt)
		if p.ReloadLeft == 0 {
			p.Loaded = Barrels
		}
	}
	p.CooldownLeft = math.Max(0, p.CooldownLeft-c.Dt)

	if !c.Fire || p.CooldownLeft > 0 || p.ReloadLeft > 0 {
		return p
	}
	if p.Loaded > 0 {
		p.Loaded--
		p.CooldownLeft = FireCooldownSeconds
		return p
	}
	// An empty gun. The beer is spent NOW rather than when the reload finishes,
	// so that a reload interrupted by anything at all — and in the next iteration
	// that is being killed halfway through one — cannot be a free one.
	if p.Counters[AmmoCounter] >= ReloadCost {
		p.Counters = spendCounter(p.Counters, AmmoCounter, ReloadCost)
		p.ReloadLeft = ReloadSeconds
	}
	return p
}

// spendCounter returns the bag with n taken off key, WITHOUT writing to the map
// it was given.
//
// STEP IS PURE AND A MAP IS NOT COPIED BY VALUE, which together make this the
// one place in the simulation where the obvious line is a bug. `p.Counters[k]--`
// on a Player taken by value writes through to the caller's map, and Step is
// deliberately called twice on the same Player: the client replays every pending
// command on top of each snapshot it reconciles against (ADR-058). An in-place
// decrement would land once per replay rather than once per command, so a
// player's beer would drain at the round-trip rate — and the golden vectors,
// which run one long transcript through a single Player, would never see it.
//
// The copy is paid only on the reload branch: at most one allocation of a
// one-entry map per ReloadSeconds per player, against a tick that otherwise
// allocates nothing.
//
// A KEY THAT REACHES ZERO IS DELETED rather than left sitting at zero. The bag
// is serialised onto the snapshot at 20 Hz and onto the standings at 1 Hz, both
// guarded by `len(Counters) > 0`, so `"c":{"beer":0}` would be eleven bytes per
// frame per viewer spent saying a man is carrying nothing.
func spendCounter(in map[string]int, key string, n int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	if left := out[key] - n; left > 0 {
		out[key] = left
	} else {
		delete(out, key)
	}
	return out
}

// resolve moves a disc from one point towards another, sliding along whatever it
// meets. It returns where the player ended up and which sector that is.
//
// The whole collision model is "push the circle out of every solid segment it
// overlaps, a few times". That is enough here because every wall is axis-aligned
// and the world is small, and it gives wall-sliding, doorway jambs and inside
// corners without any of them being special cases.
func resolve(l *Level, from Vec2, fromSector int, to Vec2) (Vec2, int) {
	pos := to
	for pass := 0; pass < collisionPasses; pass++ {
		moved := false
		for _, w := range l.Walls {
			if out, ok := pushOut(w, pos, from); ok {
				pos, moved = out, true
			}
		}
		if !moved {
			break
		}
	}

	// Two ways the move is refused outright rather than adjusted. Leaving the
	// level at all means the resolver has been defeated — by a doorway too
	// narrow for the passes above, or by geometry nobody anticipated — and
	// standing still is always a safe answer where teleporting into the void is
	// not. A rise taller than MaxStep is the ordinary rule, and it cannot fire
	// in this iteration because the generator clamps every doorway to it; it is
	// here because the first drop that cannot be climbed arrives with the lifts,
	// and a rule the simulation only learns later is a rule it learns wrong.
	id := l.SectorAt(pos)
	if id < 0 {
		return from, fromSector
	}
	if fromSector >= 0 && fromSector < len(l.Sectors) {
		if math.Abs(l.Sectors[id].FloorZ-l.Sectors[fromSector].FloorZ) > MaxStep+1e-9 {
			return from, fromSector
		}
	}
	return pos, id
}

// pushOut moves p clear of a wall if it is inside it, reporting whether it had
// to. `from` is where the player was before the step and is used only to break
// the degenerate tie when the centre lands exactly on the segment.
func pushOut(w Wall, p, from Vec2) (Vec2, bool) {
	var closest Vec2
	if w.Vertical {
		closest = Vec2{X: w.At, Y: clamp(p.Y, w.Lo, w.Hi)}
	} else {
		closest = Vec2{X: clamp(p.X, w.Lo, w.Hi), Y: w.At}
	}
	dx, dy := p.X-closest.X, p.Y-closest.Y
	d := math.Hypot(dx, dy)
	if d >= PlayerRadius {
		return p, false
	}
	if d < 1e-9 {
		// Dead on the line. Fall back to the side the player came from, and to
		// the wall's own normal if he came from nowhere either — anything but a
		// division by zero, which would put him at infinity.
		if w.Vertical {
			dx, dy = from.X-w.At, 0
			if dx == 0 {
				dx = 1
			}
		} else {
			dx, dy = 0, from.Y-w.At
			if dy == 0 {
				dy = 1
			}
		}
		d = math.Hypot(dx, dy)
	}
	scale := PlayerRadius / d
	return Vec2{X: closest.X + dx*scale, Y: closest.Y + dy*scale}, true
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// EyeZ is where the camera sits for a player standing in this level. It is
// server-computed rather than left to the client so that the floor a player sees
// himself standing on is the floor the simulation put him on.
func EyeZ(l *Level, p Player) float64 {
	if p.Sector < 0 || p.Sector >= len(l.Sectors) {
		return EyeHeight
	}
	return l.Sectors[p.Sector].FloorZ + EyeHeight
}
