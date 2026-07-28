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
}

// Player is one occupant of an arena.
//
// Counters is the generic bag every pickup grants into, keyed by the catalogue's
// Grants field, so the HUD and the snapshot both iterate rather than naming
// anything — which is what makes the syringe and the keys catalogue entries
// rather than code.
type Player struct {
	Pos      Vec2
	Yaw      float64
	Pitch    float64
	Sector   int
	Health   int
	Counters map[string]int
	LastSeq  int64
}

// NewPlayer places somebody at the level's spawn with a full bar and nothing in
// his pockets.
func NewPlayer(l *Level) Player {
	return Player{
		Pos:      l.Spawn,
		Yaw:      l.SpawnYaw,
		Sector:   l.SpawnSector,
		Health:   StartHealth,
		Counters: map[string]int{},
	}
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
