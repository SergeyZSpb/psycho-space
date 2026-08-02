package gamevanyadum

import "math"

// The hit test: one ray, the building's geometry, and the bodies standing in it.
//
// EVERYTHING HERE IS PURE, exactly as sim.go is and for a related reason. Step
// answers "where is one player", this answers "what is in front of him", and
// both are functions of their arguments alone — no clock, no world, no map
// iteration — so a shot is table-testable, deterministic, and cannot depend on
// which occupant happened to be ranged over first.
//
// It is NOT in the port and never will be. The client predicts its own movement
// because a camera that waits for a round trip is unusable; it does not predict
// whether it hit anybody, because that is a question about somebody else and the
// only honest answer comes from the server. The browser draws the muzzle flash
// and learns the rest a round trip later (message.go, Peer.St).
//
// # The model, stated once
//
//   - A BODY IS A CYLINDER: the disc of PlayerRadius the collision resolver
//     already uses, standing on the floor of the sector he is in, BodyHeight
//     tall. The same disc in both directions — what you can be hit through is
//     what you can walk through.
//   - THE RAY STARTS AT THE EYE and runs along the yaw and pitch the client sent.
//     Yaw zero looks along +Y (the same convention Step's movement uses) and
//     positive pitch looks UP, which is what the browser's camera does.
//   - OCCLUSION IS MEASURED IN PLAN, and that is exact here rather than a
//     simplification worth apologising for: a wall spans its room from floor to
//     ceiling, and a doorway is a full-height gap cut out of the wall list
//     itself (level.go, subtractPortals). So there is nothing to shoot over and
//     nothing to shoot under — if the segment crosses a wall before it reaches
//     you, the concrete stopped it.
//   - THE NEAREST BODY WINS, and only one. A single ray rather than a cone of
//     pellets: a shotgun spread is several rays and a damage split, which is
//     more rules than "the shot went where you aimed it" needs today.
//
// There is no range limit, deliberately. What bounds a shot is the wall it hits
// and the set of people the shooter was actually SENT (world.go, resolveShot) —
// a leash shorter than any number typed here, and one that already exists.

// body is one target the ray can land on: where he stands, and how high the
// floor under him is.
//
// A VALUE RATHER THAN A POINTER TO AN Occupant, so nothing in here can reach the
// world, mutate a player, or read a field that is not part of the geometry. The
// caller does the rewinding and hands over positions; this file has no opinion
// about which instant they belong to.
type body struct {
	slot   int
	pos    Vec2
	floorZ float64
}

// shoot resolves one shot, reporting the slot it landed on.
//
// The eye height is passed rather than derived because the caller knows which
// sector the shooter is standing in and this file deliberately does not read
// players.
func shoot(l *Level, from Vec2, eyeZ, yaw, pitch float64, targets []body) (int, bool) {
	sin, cos := math.Sincos(yaw)
	dir := Vec2{X: sin, Y: cos}

	// Metres of height per metre travelled ON THE FLOOR PLANE, which is what
	// makes the whole test two-dimensional plus a slope: the parameter below is
	// horizontal distance, so a wall crossing and a body entry are measured in
	// the same unit and can be compared directly. MaxPitch is just short of
	// straight up, so this is large but always finite.
	rise := math.Tan(pitch)

	// THE WALL FIRST, because a body behind concrete is not a target and the
	// cheapest way to say so is to know where the shot stopped before asking who
	// is standing in it.
	stop := wallDistance(l, from, dir)

	best, nearest := -1, math.Inf(1)
	for _, b := range targets {
		at, ok := bodyDistance(from, dir, eyeZ, rise, b)
		if !ok || at >= stop || at >= nearest {
			continue
		}
		best, nearest = b.slot, at
	}
	return best, best >= 0
}

// wallDistance is how far the shot travels before the building stops it, in
// metres along the floor plane, or +Inf if it never does.
//
// Every wall in the level, every shot. That is a few dozen segments against a
// gun that fires at most three times a second per player, so it is nothing —
// and it is the same list the collision resolver sweeps, which is what keeps
// "you cannot shoot through what you cannot walk through" true by construction
// rather than by two lists agreeing.
func wallDistance(l *Level, from, dir Vec2) float64 {
	nearest := math.Inf(1)
	for _, w := range l.Walls {
		if at, ok := wallCrossing(w, from, dir); ok && at < nearest {
			nearest = at
		}
	}
	return nearest
}

// wallCrossing is where a ray crosses one axis-aligned segment, if it does.
//
// A crossing at or behind the origin does not count: the shooter is standing a
// PlayerRadius clear of every wall (sim.go, pushOut), so anything at zero is the
// degenerate case of a man on the line, and answering "you are blocked" there
// would mean the wall he is leaning against stops his own shot.
func wallCrossing(w Wall, from, dir Vec2) (float64, bool) {
	if w.Vertical {
		if dir.X == 0 {
			return 0, false // parallel to it, so it is never crossed
		}
		at := (w.At - from.X) / dir.X
		if at <= 0 {
			return 0, false
		}
		y := from.Y + at*dir.Y
		if y < w.Lo || y > w.Hi {
			return 0, false // it passes the segment's end — through the doorway
		}
		return at, true
	}
	if dir.Y == 0 {
		return 0, false
	}
	at := (w.At - from.Y) / dir.Y
	if at <= 0 {
		return 0, false
	}
	x := from.X + at*dir.X
	if x < w.Lo || x > w.Hi {
		return 0, false
	}
	return at, true
}

// bodyDistance is how far along the floor plane the ray enters one body, if it
// enters it at all.
//
// Two halves, and they are separate because the parameter is horizontal:
//
//   - IN PLAN it is a line against a circle, which is the ordinary quadratic. Its
//     two roots are where the ray enters and leaves the disc.
//   - IN HEIGHT the ray rises linearly across that span, so the shot is inside
//     the cylinder exactly when the interval it sweeps between those two roots
//     overlaps the body's own [floor, floor+BodyHeight]. That is exact rather
//     than a sample at one end: a steep shot can enter the disc above a man's
//     head and leave it below his feet, and it went through him on the way.
//
// A NEGATIVE ENTRY IS CLAMPED TO ZERO RATHER THAN REFUSED, which is the point
// blank case and it happens: nothing in this game pushes players apart, so two
// men can stand in the same half metre and one of them is inside the other's
// disc. Refusing it would make the обрез useless at exactly the range it is for.
func bodyDistance(from, dir Vec2, eyeZ, rise float64, b body) (float64, bool) {
	ox, oy := from.X-b.pos.X, from.Y-b.pos.Y
	// dir is a unit vector, so the quadratic is s² + 2(o·d)s + (|o|² − r²) and
	// half is −(o·d): the parameter of the disc's centre projected onto the ray.
	half := -(ox*dir.X + oy*dir.Y)
	disc := half*half - (ox*ox + oy*oy - PlayerRadius*PlayerRadius)
	if disc < 0 {
		return 0, false // the line misses the circle entirely
	}
	root := math.Sqrt(disc)
	enter, leave := half-root, half+root
	if leave <= 0 {
		return 0, false // he is behind the shooter
	}
	if enter < 0 {
		enter = 0
	}

	lo, hi := eyeZ+enter*rise, eyeZ+leave*rise
	if lo > hi {
		lo, hi = hi, lo
	}
	if hi < b.floorZ || lo > b.floorZ+BodyHeight {
		return 0, false // over his head, or into the floor in front of him
	}
	return enter, true
}
