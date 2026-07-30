package gamefintech

import "math"

// СЕРЕГА AND ТЁМА — the two people in this office who are not playing.
//
// They wander, they walk to the кальян, they stand in a cloud for a while, and they
// say what they think of your branch. What they do NOT do is touch the game: they are
// not targets for either man on the floor, they take no slot in the occupancy cap,
// they cannot be caught, and they never buff or debuff a player. That is the whole
// specification, and every clause of it is a test.
//
// THEY DO NOT CONSUME THE КАЛЬЯН, which is the one place the specification had to be
// read carefully. «They get to hookah, get cloud» is decoration; taking the prop away
// from a player would be interference, and interference is what «they do not buff or
// debuff the players» rules out. So they smoke where it stands and it stays standing.
//
// WHY THEY ARE STEPPED ON THE SERVER rather than evaluated closed-form on the client
// like the yard's regulars (ADR-042): they walk to a place the SERVER chooses — the
// кальян moves every twenty seconds — so a client-side evaluation would need the spot
// draw published and the wander reimplemented in TypeScript. That is a second
// unpinned port of a moving thing, and this project's answer to those is golden
// vectors, which decoration does not earn. The bytes are paid instead, and stated:
// two NPCs at about thirty bytes a frame is ~600 B/s per viewer.

// NPC is one of them: where he is, where he is going, and what is over his head.
type NPC struct {
	Pos Vec2
	// To is where he is walking. Reached, he pauses and picks somewhere else.
	To Vec2
	// Pause is seconds of standing still remaining.
	Pause float64
	// Cloud is seconds of smoke over him remaining. Cosmetic — it hides him from
	// nobody, because nobody is looking for him.
	Cloud float64
	// Smoking is true while `To` is the кальян, so arriving there lights one and
	// arriving anywhere else does not.
	Smoking bool
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
func StepNPC(desks []Rect, n NPC, hookah Vec2, dt float64) NPC {
	if n.Cloud > 0 {
		n.Cloud = math.Max(0, n.Cloud-dt)
	}
	if n.Pause > 0 {
		n.Pause = math.Max(0, n.Pause-dt)
		return n
	}

	dx, dy := n.To.X-n.Pos.X, n.To.Y-n.Pos.Y
	dist := math.Hypot(dx, dy)
	if dist <= NPCArrive {
		// Arrived. A cigarette if this was the кальян, a breather either way, and
		// then somewhere else — which is sometimes the кальян and mostly not.
		if n.Smoking {
			n.Cloud = NPCCloudSeconds
		}
		n.Pause = NPCPauseSeconds
		n.To, n.Smoking = drawNPCTarget(hookah)
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

// drawNPCTarget picks somewhere to amble to, and whether it is the кальян.
func drawNPCTarget(hookah Vec2) (Vec2, bool) {
	if unitRand() < NPCHookahChance {
		return hookah, true
	}
	// Anywhere on the floor, inset by a radius so the draw is always somewhere he
	// can stand. The resolver handles a spot inside a desk by pushing him off it,
	// which reads as somebody changing their mind.
	return Vec2{
		X: PlayerRadius + unitRand()*(OfficeW-2*PlayerRadius),
		Y: PlayerRadius + unitRand()*(OfficeH-2*PlayerRadius),
	}, false
}
