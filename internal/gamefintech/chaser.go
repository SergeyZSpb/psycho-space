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

// Landed reports whether he has reached a player.
//
// A SHORTER REACH THAN THE ЛЫСЫЙ'S, and that is the one asymmetry worth having
// between them: being told your prompt is wrong needs him next to you, where being
// noticed by the лысый only needs him to arrive at your desk. It also means the two
// of them converging on one person do not land on the same tick, which reads as two
// separate things happening rather than one.
func Landed(c Chaser, p Vec2) bool {
	return math.Hypot(c.Pos.X-p.X, c.Pos.Y-p.Y) <= ChaserReach+PlayerRadius
}
