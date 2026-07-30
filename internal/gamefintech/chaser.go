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
func StepChaser(desks []Rect, c Chaser, targets []Vec2, dt float64) Chaser {
	aim, ok := nearest(c.Pos, targets)
	if !ok {
		c.Cig = 0
		return c
	}

	head := navAimAt(c.Pos, aim)
	dx, dy := head.X-c.Pos.X, head.Y-c.Pos.Y
	dist := math.Hypot(dx, dy)
	if step := ChaserSpeed * dt; dist > 1e-9 {
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

// Separate moves Claude clear of the лысый when the two are standing in the same
// place, and returns him.
//
// THEY CONVERGE BY CONSTRUCTION, WHICH IS WHY THIS IS NOT AN EDGE CASE. Both men
// walk at the nearest of the same target list, through the same `navAimAt`, at
// the same speed — `ChaserSpeed` IS `BossSpeed` — so from the moment their paths
// meet they compute an identical heading and cover an identical distance every
// tick, for ever. They do not merely brush past each other: they lock together
// and the floor shows one figure where there are two, with the лысый's sprite
// drawn over Claude's or the other way about depending on the depth band. A
// player then reads one man walking at him and is slowed by something that is not
// there.
//
// CLAUDE YIELDS AND THE ЛЫСЫЙ NEVER MOVES, which is the whole design of this
// function rather than an implementation detail. Splitting the overlap between
// them would make the лысый's position a function of Claude's — so how long a
// player has before the shift ends would depend on where a second man happened to
// be, and the catch, its rewind ring and every test of the chase would all shift
// underneath. Only the man whose arrival is survivable gives way.
//
// He is moved SIDEWAYS rather than backwards: pushing him along the line between
// them keeps his distance to the target unchanged, so he still lands on the same
// tick he would have. Backwards would make him permanently the лысый's shadow and
// he would never arrive at all.
//
// Pure, like everything else in this file, and deterministic in the degenerate
// case: two discs exactly coincident have no line between them, so the fallback
// is a fixed axis rather than a draw — this function is stepped twenty times a
// second and must produce the same office on every process.
func Separate(bossPos Vec2, c Chaser, desks []Rect) Chaser {
	const gap = BossRadius + ChaserRadius
	dx, dy := c.Pos.X-bossPos.X, c.Pos.Y-bossPos.Y
	dist := math.Hypot(dx, dy)
	if dist >= gap {
		return c
	}
	if dist < 1e-9 {
		// Exactly on top of him, which is the state this exists for and the one
		// with no direction in it. +X, always, so two processes replaying the same
		// office agree — and the wall and desk resolution below is what stops it
		// mattering that the axis is arbitrary.
		dx, dy, dist = 1, 0, 1
	}
	c.Pos = Vec2{
		X: bossPos.X + dx/dist*gap,
		Y: bossPos.Y + dy/dist*gap,
	}
	// The same treatment every move in this game gets, and it is needed here:
	// stepping aside can step into a desk or through a wall.
	c.Pos = clampToFloor(c.Pos, ChaserRadius)
	for _, d := range desks {
		c.Pos = pushOut(d, c.Pos, ChaserRadius)
	}
	return c
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
