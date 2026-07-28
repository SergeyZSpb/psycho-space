package gamekaren

import "math"

// The bald man.
//
// He is the whole antagonist and he has one behaviour: walk at the nearest
// person, slightly slower than they can walk, and smile more the closer he gets.
// He does not path-find, he does not throw anything, he does not speak yet, and
// he never stops. Everything here is pure for the same reason Step is — the
// client draws him from a snapshot and interpolates between frames, and a
// deterministic boss is one whose next position a test can state.

// Boss is where he is and how pleased he is about it.
type Boss struct {
	Pos Vec2
	// Grin is 0..1: 0 at GrinRange or further, 1 on contact. The client picks a
	// face from it; the server sends it quantised to a byte.
	Grin float64
}

// NewBoss puts him at his spawn, at the far end of the floor.
func NewBoss() Boss {
	return Boss{Pos: Vec2{X: BossSpawnX, Y: BossSpawnY}}
}

// Grin is how wide he is smiling at a given distance: 1 − dist/GrinRange,
// clamped to 0..1.
func Grin(dist float64) float64 {
	return clamp(1-dist/GrinRange, 0, 1)
}

// StepBoss advances him one step towards the nearest of the given targets.
//
// DETERMINISM: the nearest target is the FIRST of the closest, so the caller
// decides ties by the order it builds the slice in — Office.Advance builds it in
// ascending occupant-key order, which is why nothing in this game iterates a map
// to produce a result. Two people standing at exactly the same distance is not a
// hypothetical in a room this size.
//
// WITH NO TARGETS he walks back towards his spawn and stops there. The office is
// never empty of a boss; an office empty of PEOPLE is torn down entirely, so
// this is what he does on the last tick before that happens and while somebody
// is between shifts.
func StepBoss(desks []Rect, b Boss, targets []Vec2, dt float64) Boss {
	aim, ok := nearest(b.Pos, targets)
	if !ok {
		aim = Vec2{X: BossSpawnX, Y: BossSpawnY}
	}

	dx, dy := aim.X-b.Pos.X, aim.Y-b.Pos.Y
	dist := math.Hypot(dx, dy)
	if step := BossSpeed * dt; dist > 1e-9 {
		if step >= dist {
			// Arriving exactly rather than overshooting and oscillating. On a
			// target this is the tick he catches you; on his spawn it is where
			// he stands and waits.
			b.Pos = aim
		} else {
			b.Pos.X += dx / dist * step
			b.Pos.Y += dy / dist * step
		}
	}

	// The same treatment the player gets: clamped to the floor, then pushed out
	// of every desk in catalogue order. He does NOT route around them — bumping
	// into a desk and sliding along it is what makes furniture tactically
	// useful, and it is the only thing in the game that is on your side.
	b.Pos = clampToFloor(b.Pos, BossRadius)
	for _, d := range desks {
		b.Pos = pushOut(d, b.Pos, BossRadius)
	}

	// Recomputed from where he ENDED UP, against the target he was walking at,
	// so the grin and the position on a snapshot describe the same instant. With
	// nobody to walk at he is not smiling at anybody.
	if !ok {
		b.Grin = 0
		return b
	}
	b.Grin = Grin(math.Hypot(aim.X-b.Pos.X, aim.Y-b.Pos.Y))
	return b
}

// Caught reports whether he has reached a player.
//
// The two discs touching, not their centres coinciding — which is about a metre
// apart, the distance at which somebody has visibly arrived at your desk and it
// is already too late.
func Caught(b Boss, p Vec2) bool {
	return math.Hypot(b.Pos.X-p.X, b.Pos.Y-p.Y) <= CatchRadius+PlayerRadius
}

// nearest returns the first of the closest targets, or false if there are none.
func nearest(from Vec2, targets []Vec2) (Vec2, bool) {
	best, bestDist, found := Vec2{}, math.Inf(1), false
	for _, t := range targets {
		// Squared, because the comparison does not need the square root and the
		// port is one operation shorter for it.
		d := (t.X-from.X)*(t.X-from.X) + (t.Y-from.Y)*(t.Y-from.Y)
		// Strictly less, so an exact tie keeps the earlier target and the
		// caller's ordering is what decides.
		if d < bestDist {
			best, bestDist, found = t, d, true
		}
	}
	return best, found
}
