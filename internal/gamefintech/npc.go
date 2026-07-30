package gamefintech

import "math"

// СЕРЕГА AND ТЁМА — the two people in this office who are not playing.
//
// They walk about at random, they carry their own кальян, and they say what they
// think of your branch. That is the whole of it. What they do NOT do is touch the
// game: they are not targets for either man on the floor, they take no slot in the
// occupancy cap, they cannot be caught, and they never buff or debuff a player.
//
// THEY DO NOT USE THE OFFICE'S КАЛЬЯН, and the first version of this file had them
// walking to it. That was wrong twice over. It was interference — the prop is a
// first-taker, so an NPC on his way to it was competing with the player for the
// strongest effect in the game — and it was logic in something that exists to be
// scenery. Now each of them holds his own, the cloud under him is permanent, and
// there is no seeking, no arrival, no smoking state and no shared prop. The
// simplest thing that produces the intended picture.

// NPC is one of them: where he is and where he is ambling to.
type NPC struct {
	Pos Vec2
	// To is where he is walking. Reached, he pauses and picks somewhere else.
	To Vec2
	// Pause is seconds of standing still remaining.
	Pause float64
}

// NewNPCs puts them on the floor at their own spawns.
func NewNPCs() []NPC {
	out := make([]NPC, len(NPCCast))
	for i := range NPCCast {
		out[i] = NPC{Pos: NPCCast[i].Spawn, To: NPCCast[i].Spawn}
	}
	return out
}

// StepNPC advances one of them.
//
// Not pure, unlike `Step` and `StepBoss`: it draws. That is allowed here for the
// reason it is not allowed there — nothing predicts an NPC, nothing reconciles one,
// and no golden vector pins one, so there is no second implementation for a random
// draw to disagree with. The office is the only authority on where Серега is, which
// is the cheapest possible arrangement for something nobody is racing.
func StepNPC(desks []Rect, n NPC, dt float64) NPC {
	if n.Pause > 0 {
		n.Pause = math.Max(0, n.Pause-dt)
		return n
	}

	dx, dy := n.To.X-n.Pos.X, n.To.Y-n.Pos.Y
	dist := math.Hypot(dx, dy)
	if dist <= NPCArrive {
		n.Pause = NPCPauseSeconds
		n.To = drawNPCTarget()
		return n
	}

	if step := NPCSpeed * dt; step < dist {
		n.Pos.X += dx / dist * step
		n.Pos.Y += dy / dist * step
	} else {
		n.Pos = n.To
	}

	// The same resolver everybody else gets. He has no navigation — he ambles, and
	// bumping into a desk and sliding along it is exactly what an amble looks like.
	n.Pos = clampToFloor(n.Pos, PlayerRadius)
	for _, d := range desks {
		n.Pos = pushOut(d, n.Pos, PlayerRadius)
	}
	return n
}

// drawNPCTarget picks somewhere to amble to.
//
// Anywhere on the floor, inset by a radius so the draw is always somewhere he can
// stand. A spot inside a desk is handled by the resolver pushing him off it, which
// reads as somebody changing their mind.
func drawNPCTarget() Vec2 {
	return Vec2{
		X: PlayerRadius + unitRand()*(OfficeW-2*PlayerRadius),
		Y: PlayerRadius + unitRand()*(OfficeH-2*PlayerRadius),
	}
}
