package gamefintech

import "math"

// CLAUDE CODE, the second man on the floor.
//
// He is structurally the лысый — walk at the nearest person, round the furniture,
// never stop — and he is a different thing, because what happens when he arrives is
// different. The лысый ends your shift. He slows you down.
//
// THIS IS A SECOND IMPLEMENTATION AND NOT AN INTERFACE, deliberately. The two
// differ in consequence, in speed, in reach, in what they say and in whether a verb
// can redirect them; the only method they would share is a step, and an interface
// over two implementations with one common method buys a seam nobody has asked to
// use. The project's rule is to name the second use or write the direct version —
// there is no third chaser — so this file duplicates the SHAPE and shares the
// PRIMITIVES: `nearest`, `navAimAt`, `clampToFloor` and `pushOut` are all the
// лысый's, unchanged.

// Chaser is where he is and whether he has just landed on somebody.
type Chaser struct {
	Pos Vec2
	// Cig is how lit the cigarette is, 0..1, rising as he closes — the same idea
	// as the лысый's grin and quantised to a byte the same way. It is what lets a
	// player read how much trouble he is in without a meter, and it is derived
	// from distance rather than stored.
	Cig float64
}

// NewChaser puts him at his spawn.
func NewChaser() Chaser {
	return Chaser{Pos: Vec2{X: ChaserSpawnX, Y: ChaserSpawnY}}
}

// StepChaser advances him one step towards the nearest of the given targets.
//
// The лысый's own logic with three things removed: he does not get drunk, he does
// not wobble, and he has no spawn to walk home to — with nobody to chase he simply
// stands where he is, because unlike the лысый he is not the thing a shift is a race
// against and a man standing still reads as a man waiting for you to come back.
//
// `elapsed` is the OFFICE's age in seconds, and it is here for one reason: the
// tempo ramp (TempoAt) speeds both men up together. He takes it as a parameter
// rather than reading a clock for the reason everything in this file is pure — the
// office steps him and the tests state his next position.
func StepChaser(desks []Rect, c Chaser, targets []Vec2, dt float64, elapsed float64) Chaser {
	aim, ok := nearest(c.Pos, targets)
	if !ok {
		c.Cig = 0
		return c
	}

	head := navAimAt(c.Pos, aim)
	dx, dy := head.X-c.Pos.X, head.Y-c.Pos.Y
	dist := math.Hypot(dx, dy)
	// The same ramp the лысый is on, so «his speed is the лысый's, exactly» stays
	// true at every tempo rather than only at the start of a shift.
	if step := ChaserSpeed * TempoAt(elapsed) * dt; dist > 1e-9 {
		if step >= dist {
			c.Pos = head
		} else {
			c.Pos.X += dx / dist * step
			c.Pos.Y += dy / dist * step
		}
	}

	c.Pos = clampToFloor(c.Pos, ChaserRadius)
	for _, d := range desks {
		c.Pos = pushOut(d, c.Pos, ChaserRadius)
	}
	c.Cig = Grin(math.Hypot(aim.X-c.Pos.X, aim.Y-c.Pos.Y))
	return c
}

// Separate moves Claude clear of anybody he is standing inside — the лысый, or a
// player — and returns him.
//
// HE CONVERGES ON BOTH BY CONSTRUCTION, WHICH IS WHY NEITHER IS AN EDGE CASE, and
// the two cases have the same cure for different reasons.
//
// The лысый: both men walk at the nearest of the same target list, through the
// same `navAimAt`, at the same speed — `ChaserSpeed` IS `BossSpeed` — so from the
// moment their paths meet they compute an identical heading and cover an identical
// distance every tick, for ever. They do not merely brush past each other: they
// lock together and the floor shows one figure where there are two.
//
// A player: Claude walks at the man's CENTRE and arriving does not stop him,
// because landing is not catching — he applies a slow and keeps coming. So against
// somebody standing still, which is what this game pays you to do, he closes the
// last half-metre and parks exactly on top of him and stays there. The floor draws
// both figures at the same point, in the same depth band, so which one is visible
// is down to the order the elements happen to sit in the document: Claude
// disappears UNDER the player, and the player is being slowed by a man he cannot
// see. The лысый has no such state — his arrival ends the shift a metre out, at
// `CatchRadius` — so this is Claude's alone.
//
// CLAUDE YIELDS AND NOBODY ELSE MOVES, which is the whole design of this function
// rather than an implementation detail, and it holds for both. Splitting the
// overlap with the лысый would make his position a function of Claude's — so how
// long a player has before the shift ends would depend on where a second man
// happened to be, and the catch, its rewind ring and every test of the chase would
// shift underneath. Moving a PLAYER is worse still: his position is predicted in
// the browser from his own commands, so a server-side shove he never asked for is
// a correction that snaps him sideways. Only the man whose arrival is survivable
// gives way.
//
// He is moved SIDEWAYS rather than backwards: pushing him along the line between
// them keeps his distance to whoever he was walking at unchanged, so he still
// lands on the same tick he would have. The standoff is two bodies touching, which
// is well inside `ChaserReach` — being kept out of somebody costs him nothing, he
// simply stands next to them instead of in them.
//
// BODIES ARE BODIES, so an invincible player is separated from too. The cloud
// makes him uncatchable and unchaseable, not incorporeal, and a man hidden under a
// player is the same bug whether or not the player can be landed on.
//
// Pure, like everything else in this file, and deterministic in the degenerate
// case: two discs exactly coincident have no line between them, so the fallback is
// a fixed axis rather than a draw — this function is stepped twenty times a second
// and must produce the same office on every process. The bodies are resolved in
// the order given, which the office fixes as ascending account order for the same
// reason it fixes his choice of victim.
func Separate(bossPos Vec2, bodies []Vec2, c Chaser, desks []Rect) Chaser {
	// The лысый first and the players second, so the last word belongs to the
	// overlap a player is actually looking at. One pass rather than a settle: in a
	// crowd, yielding from one man can leave him touching another, and the tick
	// after resolves that — a loop here would be a repulsor with a fixed point
	// nobody has asked for.
	c.Pos = yieldFrom(c.Pos, bossPos, BossRadius+ChaserRadius)
	for _, b := range bodies {
		c.Pos = yieldFrom(c.Pos, b, PlayerRadius+ChaserRadius)
	}
	// The same treatment every move in this game gets, and it is needed here:
	// stepping aside can step into a desk or through a wall.
	c.Pos = clampToFloor(c.Pos, ChaserRadius)
	for _, d := range desks {
		c.Pos = pushOut(d, c.Pos, ChaserRadius)
	}
	return c
}

// yieldFrom pushes `p` out along the line from `other` until the two are `gap`
// apart, and leaves it alone when they already are.
//
// It is a resolver and not a repulsor — a man who is clear is not nudged — and the
// coincident case picks +X rather than a random direction, so two processes
// replaying the same office agree. The wall and desk resolution its caller applies
// afterwards is what stops it mattering that the axis is arbitrary.
func yieldFrom(p, other Vec2, gap float64) Vec2 {
	dx, dy := p.X-other.X, p.Y-other.Y
	dist := math.Hypot(dx, dy)
	if dist >= gap {
		return p
	}
	if dist < 1e-9 {
		dx, dy, dist = 1, 0, 1
	}
	return Vec2{X: other.X + dx/dist*gap, Y: other.Y + dy/dist*gap}
}

// Landed reports whether he has reached a player.
//
// A SHORTER REACH THAN THE ЛЫСЫЙ'S, and that is the one asymmetry worth having
// between them: being told your prompt is wrong needs him next to you, where being
// noticed by the лысый only needs him to arrive at your desk. It also means the two
// of them converging on one person do not land on the same tick, which reads as two
// separate things happening rather than one.
//
// It takes his POSITION and not him, for the reason Caught does: the office
// resolves this against where he was on the tick the victim's screen is showing.
func Landed(chaserPos, playerPos Vec2) bool {
	return math.Hypot(chaserPos.X-playerPos.X, chaserPos.Y-playerPos.Y) <= ChaserReach+PlayerRadius
}
