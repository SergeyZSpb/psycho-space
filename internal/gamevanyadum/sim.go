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
	// "I pulled the trigger", not "I fired" and certainly not "I hit somebody".
	// Whether a barrel is spent is Step's to decide, against a gun state the
	// server owns; where the ray then goes and who it lands on is the world's
	// (world.go, resolveShot), and no part of it is ever the client's.
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
// THE BAG IS NOT HERE, AND USED TO BE. It sat on this struct for exactly one
// reason — the snapshot carried it, so the client held a copy — and that reason
// is gone: what somebody is carrying rides the standings at 1 Hz and nothing
// else (message.go, StandingsRow.Bag). Step never read it or wrote it even
// then, so it now lives beside the other numbers a viewer only ever sees once a
// second (world.go, Occupant.bag), which is where this package already puts a
// man's deaths, his kills and his betrayals. It also makes this struct a plain
// value with no reference field in it, so copying one is `=` and stepping a
// copy cannot move the original.
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
	Pos     Vec2
	Yaw     float64
	Pitch   float64
	Sector  int
	Health  int
	LastSeq int64

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

	// ProtectedLeft is seconds of spawn protection remaining: he cannot be hurt
	// and he cannot fire (content.go, SpawnProtectSeconds).
	//
	// ON Player RATHER THAN ON THE OCCUPANT, and it is the same argument the gun
	// made rather than a second one. Being UNHURTABLE is the world's business and
	// the client could be told about it in any shape at all; being unable to FIRE
	// is a refusal the browser has to run for itself, because it decides whether
	// to draw a muzzle flash the instant a thumb lands and it can only do that by
	// running the same rule the server is about to. So it goes here, into the
	// port, into the golden vectors and into the reconcile spread — the five
	// coordinated edits ADR-058 prices, paid for the same reason.
	//
	// A COUNTDOWN IN SECONDS AND NOT A TICK DEADLINE, like the gun's two timers
	// and unlike the world's respawn: Step is pure, reads no clock and knows no
	// tick, so seconds counted down by the command's own dt is the only
	// representation both ends can agree on.
	ProtectedLeft float64

	// InjectLeft is seconds until the ampoule in his forearm is empty — zero when
	// there is none. While it runs he cannot walk and cannot fire (content.go,
	// the шприц), and Health climbs as it empties.
	//
	// HERE FOR THE SAME REASON THE GUN'S TIMERS ARE, and it is the same test:
	// does the CLIENT have to simulate it? It does, twice over. The browser
	// decides whether to draw a muzzle flash the instant a thumb lands and the
	// trigger is refused while this runs; and it predicts its own position, which
	// while this runs does not move however hard the stick is held. A rule the
	// client cannot predict is a rule it renders wrong for a whole round trip,
	// and here that is a man drawn walking away from a room he is rooted in. So
	// this pays the five coordinated edits ADR-058 prices, exactly as the gun and
	// the protection did.
	//
	// IT IS ALSO WHY Health IS NOW A PREDICTED QUANTITY. Every other change to it
	// is the world's — a barrel, a нейрослоп — and the client simply reads the
	// frame; this one happens INSIDE Step, so the browser produces it too and the
	// golden vectors carry it. What the client still cannot predict is damage,
	// which is a correction like any other.
	//
	// A COUNTDOWN IN SECONDS AND NOT A WORLD DEADLINE, unlike the respawn, the
	// down window, and the SPAWN PROTECTION directly above — and that last one is
	// the comparison that has to be made, because it is the same shape of timer and
	// it did get a deadline (world.go, Occupant.protectedUntil). The two directions
	// a client can bend this in are not symmetric, and only one of them is worth
	// anything:
	//
	//   - STRETCHING IT IS A PUNISHMENT rather than an exploit. A client that
	//     under-claims its ticks lengthens its own helplessness and slows its own
	//     healing, so the direction the protection had to be clamped in is one
	//     nobody would ever take here (TestStretchingAnInjectionOnlyLengthensIt).
	//   - COMPRESSING IT IS THE EXPLOITABLE ONE, and it is CONCEDED with the bound
	//     named. The time budget banks up to TimeBudgetCap on ticks a client claims
	//     nothing on while nothing is counting down (world.go, Advance), so standing
	//     perfectly still with a cold gun beside an ampoule fills it — and the first
	//     tick of the injection can then spend the whole bank at once. The floor on
	//     a SyringeSeconds window is therefore SyringeSeconds − TimeBudgetCap +
	//     SimStep of wall clock — 2.05 s against an honest 2.5 as the constants
	//     stand, so up to 0.45 s off the only thing the ampoule charges. IT CANNOT
	//     COMPOUND, and the reason is arithmetic rather than a guard: the budget
	//     accrues exactly dt a tick and is capped, so the total time a client can
	//     claim over any window is that window plus one bankful, ever. Rebuilding
	//     the bank mid-injection means under-claiming, which gives back precisely
	//     what the burst will buy. It is also exactly the head start the gun's
	//     cooldown and reload already concede from the same cap. Pinned by
	//     TestABurstBudgetTakesAtMostTheBudgetCapOffTheInjection.
	//
	// WHY THE PROTECTION'S DEADLINE IS NOT SIMPLY COPIED, since that is the obvious
	// answer and it does not fit. That clamp only ever SHORTENS a countdown nothing
	// is derived from. Ending this one on time is the opposite operation — raising
	// InjectLeft back to whatever the world's clock still allows — and the health IS
	// derived from that number: stepInject reads injectDelivered afresh on both
	// sides of every decrement, so restoring half a second of ampoule hands the same
	// half second out a second time, and a fifty-point ampoule would deliver sixty.
	// Taking the health back with it means unwinding the MaxHealth cap too, which is
	// the world computing a second time what Step has already computed once — a
	// second path to the same number, which this project does not keep. A bounded,
	// pinned, one-off head start is the cheaper of the two mistakes.
	InjectLeft float64
}

// NewPlayer places somebody at the level's spawn with a full bar and a loaded
// gun.
//
// LOADED RATHER THAN EMPTY, deliberately: the first thing a new player does is
// pull the trigger, and a gun that answered that with ReloadSeconds of nothing
// happening reads as a game that has not started yet. Arriving with empty
// pockets costs him nothing now — the reload is free — so what he walks to the
// bottles for is the standings rather than the next two shots.
func NewPlayer(l *Level) Player {
	return Player{
		Pos:    l.Spawn,
		Yaw:    l.SpawnYaw,
		Sector: l.SpawnSector,
		Health: StartHealth,
		Loaded: Barrels,
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
	return p.CooldownLeft > 0 || p.ReloadLeft > 0 || p.ProtectedLeft > 0 || p.InjectLeft > 0
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

	// A MAN ON THE FLOOR DOES NOTHING BUT LOOK AROUND. He does not walk, he does
	// not shoot, and none of his timers runs — what gets him up again is the
	// world's own deadline (world.go, rise) rather than anything he sends.
	//
	// IT IS A RULE OF Step AND NOT OF THE WORLD, deliberately, because the client
	// predicts through this function: a server that quietly ignored the commands
	// of a dead player while the browser went on applying them would drag his
	// corpse down the corridor and correct it twenty times a second. Here, both
	// ends refuse the same input and there is nothing to reconcile.
	//
	// The angles are still assigned, above, because the camera belongs to the
	// client — a death you cannot look around from is a black screen with a
	// timer on it.
	if p.Health <= 0 {
		return p
	}

	// Spawn protection runs on the command's own dt, exactly as the gun's timers
	// do, and it is what stops the trigger below (content.go,
	// SpawnProtectSeconds).
	p.ProtectedLeft = math.Max(0, p.ProtectedLeft-c.Dt)

	// The ampoule first, because the gun below asks whether one is running. Both
	// obey the same ordering rule for the same reason: the timer is advanced and
	// THEN the trigger is read, so a shot asked for on the very step an injection
	// finishes is honoured rather than refused by a state the arm has just left.
	p = stepInject(p, c)

	// BEFORE the standing-still return below, and that ordering is the whole of
	// whether the gun works: firing while standing perfectly still is not an edge
	// case, it is how anybody shoots at anything. A gun folded in after that
	// return would cool down only while its owner was walking.
	p = stepGun(p, c)

	// A MAN WITH A NEEDLE IN HIS ARM DOES NOT WALK, and this is the whole of the
	// vulnerability the ampoule buys (content.go). It is refused HERE rather than
	// by zeroing the axes, so his angles still land — the camera belongs to the
	// client, and a first-person game that takes the view away from somebody is
	// worse than one that takes his feet.
	//
	// AFTER the gun and after the injection's own countdown, so that the step an
	// injection ends on is a step he can move in — the same rule the world
	// applies to a man getting up off the floor (world.go, rise).
	if p.InjectLeft > 0 {
		return p
	}

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

// stepInject advances the ampoule by one command: the countdown by the command's
// own dt, and the health that went in while it ran.
//
// THE HEALTH IS DERIVED FROM THE CLOCK AND NEVER ACCUMULATED. How much has gone
// in is a pure function of how much is left (injectDelivered), so the step grants
// the DIFFERENCE between the two readings either side of the decrement. The
// obvious alternative — a fractional accumulator carried on Player — would be a
// second piece of state to serialise, to reconcile and to keep in step with the
// timer it is derived from, and it would drift: the sum of Heals×dt/Seconds over
// commands a client chose the lengths of is not the same number twice.
//
// WHICH MATTERS BECAUSE THE CLIENT RUNS THIS TOO. Derived, the arithmetic is
// idempotent in the only sense prediction needs — a player replayed from a
// snapshot arrives at exactly the health the server has, because both ends
// computed it from the same remaining time rather than from a sum of their own
// histories.
//
// IT DOES NOT OVERHEAL. The cap is applied to the grant rather than to the
// ampoule, so a man who fills up halfway through is still rooted for the rest of
// it — the ampoule is spent on the clock, not on the health, and injecting at
// nearly full health is therefore a bad trade rather than a free top-up.
func stepInject(p Player, c Command) Player {
	if p.InjectLeft <= 0 {
		return p
	}
	before := injectDelivered(p.InjectLeft)
	p.InjectLeft = math.Max(0, p.InjectLeft-c.Dt)
	if gain := injectDelivered(p.InjectLeft) - before; gain > 0 {
		p.Health = int(math.Min(float64(p.Health+gain), MaxHealth))
	}
	return p
}

// injectDelivered is how much of one ampoule has gone in by the time `left`
// seconds of it remain: nothing at the start, the whole of SyringeHeal at the
// end, and a straight line between the two.
//
// ROUNDED RATHER THAN TRUNCATED, and that is the one line in this game where
// two languages could disagree. Go's math.Round and JavaScript's Math.round part
// company on exact halves of negative numbers, and nothing here is ever negative:
// `left` is clamped to zero at the low end and to SyringeSeconds at the high one,
// so the argument is bounded by 0 and SyringeHeal and the two agree bit for bit.
func injectDelivered(left float64) int {
	return int(math.Round(SyringeHeal * (SyringeSeconds - left) / SyringeSeconds))
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
// with a reload, and never with silence — so the whole gun is ONE rule read top
// to bottom, rather than a rule plus an "and also, when the gun happens to
// empty" clause. There is no third outcome left for the trigger to have: a
// reload costs nothing but the time it takes (content.go, ReloadSeconds).
//
// NOTHING HERE HITS ANYTHING, STILL. A granted shot spends a barrel and starts a
// cooldown; where the ray went and who it landed on is the WORLD's, because a
// hit test needs every other body in the building and a rewind buffer of where
// they used to be (world.go, resolveShot). Step stays a pure function of one
// player, which is what lets the browser run it.
//
// A SPENT BARREL IS THEREFORE THE WHOLE SIGNAL. The world watches the count fall
// and resolves the shot from that, so there is no second definition of "he
// fired" to keep in step with this one.
func stepGun(p Player, c Command) Player {
	if p.ReloadLeft > 0 {
		p.ReloadLeft = math.Max(0, p.ReloadLeft-c.Dt)
		if p.ReloadLeft == 0 {
			p.Loaded = Barrels
		}
	}
	p.CooldownLeft = math.Max(0, p.CooldownLeft-c.Dt)

	// PROTECTION IS PART OF THE REFUSAL AND NOT A SEPARATE CHECK, so a protected
	// man cannot start a reload either — otherwise the window would be spent
	// filling the gun he is about to be untouchable with.
	//
	// AND SO IS THE AMPOULE, WHICH IS HOW THE TRIGGER AND THE INJECTION AGREE
	// ABOUT WHO WINS: the injection does, always, for as long as it lasts. The
	// other way round — a trigger pull that cancels the needle — reads as the
	// generous rule and is in fact the rule that deletes the feature: a window
	// you can leave by tapping the thing you were already holding is not a window,
	// and nobody would ever be caught in one. Committed, the ampoule costs
	// SyringeSeconds of being unable to answer, which is the entire price it
	// charges. The same refusal keeps a reload from starting mid-injection, since
	// the trigger is the only thing that starts one.
	//
	// THE BROWSER SPELLS THIS SAME REFUSAL A SECOND TIME, IN ITS OWN LANGUAGE, and
	// they are two copies rather than one shared predicate. It is an inline
	// condition here and a named function there — `gunBusy`,
	// web/src/lib/vanyadumStep.ts — because the port has three callers for it
	// where this file has one: its own step, the uplink filter that declines to
	// send a pull the gun is going to refuse anyway, and the busy state the
	// trigger control wears. Nothing in either language compares the two, so what
	// binds them is the GOLDEN VECTORS (golden_test.go), whose transcripts hold
	// the trigger down through every one of these five terms — a cadence, a
	// reload, a man on the floor, a spawn window and an ampoule. Drop one from the
	// port and it fires where the Go trace says it did not: taking the ampoule out
	// parts company on the syringe cases, which is how that was confirmed. Drop
	// one from HERE and TestGoldenVectors fails as stale first, which is the same
	// gate read from the other end. Either way, a term added on one side is a term
	// added on the other, in the same change.
	if !c.Fire || p.CooldownLeft > 0 || p.ReloadLeft > 0 || p.ProtectedLeft > 0 || p.InjectLeft > 0 {
		return p
	}
	if p.Loaded > 0 {
		p.Loaded--
		p.CooldownLeft = FireCooldownSeconds
		return p
	}
	// An empty gun, which is the only state left for the trigger to answer. It
	// reloads itself: nothing is checked and nothing is spent, because nothing in
	// this building is ammunition any more (content.go, ReloadSeconds). What the
	// pull buys is the wait — and being shot halfway through it clears the timer
	// and puts the man on the floor, so the wait is still interruptible and still
	// the only thing missing costs (world.go, wound).
	p.ReloadLeft = ReloadSeconds
	return p
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
