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
	// Walked is seconds spent walking at the current target. Past NPCGiveUpSeconds he
	// picks somewhere else — see that constant for why he has to.
	Walked float64
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
func StepNPC(rects []Rect, n NPC, dt float64) NPC {
	if n.Pause > 0 {
		n.Pause = math.Max(0, n.Pause-dt)
		return n
	}

	dx, dy := n.To.X-n.Pos.X, n.To.Y-n.Pos.Y
	dist := math.Hypot(dx, dy)
	// ARRIVED, OR GIVEN UP. The second branch is what stops him being stuck: he has
	// no navigation, so a target inside a desk or behind one is one he would otherwise
	// walk at for the rest of the shift while the resolver held him off it.
	if dist <= NPCArrive || n.Walked >= NPCGiveUpSeconds {
		n.Pause = NPCPauseSeconds
		n.To = drawNPCTarget(rects)
		n.Walked = 0
		return n
	}
	n.Walked += dt

	if step := NPCSpeed * dt; step < dist {
		n.Pos.X += dx / dist * step
		n.Pos.Y += dy / dist * step
	} else {
		n.Pos = n.To
	}

	// The same resolver everybody else gets. He has no navigation — he ambles, and
	// bumping into a desk and sliding along it is exactly what an amble looks like.
	n.Pos = clampToFloor(n.Pos, PlayerRadius)
	for _, d := range rects {
		n.Pos = pushOut(d, n.Pos, PlayerRadius)
	}
	return n
}

// drawNPCTarget picks somewhere to amble to that he can actually stand on.
//
// Anywhere on the floor, inset by a radius, and REJECTED IF IT IS INSIDE A DESK —
// which the first version did not do, on the theory that the resolver pushing him
// off it "reads as somebody changing his mind". It does not: it reads as a man
// walking into a desk forever, because he never arrives and so never chooses again.
//
// Bounded draws with a floor fallback rather than a loop, for `spawnPoint`'s reason:
// a rejection sampler with no bound is a rejection sampler that can hang the tick.
func drawNPCTarget(rects []Rect) Vec2 {
	for i := 0; i < 16; i++ {
		at := Vec2{
			X: PlayerRadius + unitRand()*(OfficeW-2*PlayerRadius),
			Y: PlayerRadius + unitRand()*(OfficeH-2*PlayerRadius),
		}
		clear := true
		for _, d := range rects {
			if insideRect(d, at, PlayerRadius) {
				clear = false
				break
			}
		}
		if clear {
			return at
		}
	}
	// Sixteen draws all landed on furniture, which in this room is vanishingly
	// unlikely. The central lane is always clear — content_test pins it — so it is
	// somewhere he can certainly stand.
	return Vec2{X: OfficeW / 2, Y: OfficeH / 2}
}
