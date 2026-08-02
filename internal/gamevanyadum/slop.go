package gamevanyadum

import "math"

// The нейрослоп: what it is, how it finds you, and what happens when it arrives.
//
// EVERYTHING HERE IS PURE, exactly as sim.go and hit.go are, and for the same
// reason: a creature whose next position is a function of its arguments alone is
// one a table test can state, and one that produces the same building on every
// process replaying the same transcript. The world owns the clock, the pool and
// the randomness; this file owns the walking.
//
// IT IS NOT IN THE PORT AND NEVER WILL BE. The client predicts its OWN movement
// because a camera that waits for a round trip is unusable; it does not predict a
// слоп, because a слоп's intent is not the player's to know. It is drawn
// interpolated in the recent past exactly as a peer is, which is also what makes
// the ordinary rewind the right thing to resolve a shot at one against
// (world.go, resolveShot).
//
// # The model, stated once
//
//   - A СЛОП IS THE SAME DISC AS A MAN. It walks with the player's own collision
//     resolver at PlayerRadius and is shot with the player's own body model, so
//     what fits through a doorway is what can be shot through it and what can
//     follow you through it. One radius, one meaning (content.go, SlopReach).
//   - IT WALKS AT THE NEAREST MAN, room by room, through the doorways. Straight
//     at him when they share a room — rooms are rectangles with nothing inside
//     them — and at the next doorway on the way otherwise. Re-planned every tick,
//     because the thing it is walking at moves.
//   - IT HAS NO OTHER BEHAVIOUR. No line of sight, no aggression radius, no
//     memory, no retreat, no ranged attack. This is a Doom joke, not a
//     pathfinding paper, and every rule not written here is a rule the player
//     would have to be told about on the splash screen.
//
// The sibling office's antagonists are the same SHAPE — walk at the nearest, get
// round the furniture, never stop — and none of their code is here. ADR-028: two
// games share patterns by re-implementing them, and the two navigators are not
// even the same algorithm, because a заброшка is a graph of rooms where an office
// is one open floor.

// Slop is one нейрослоп in the building.
//
// WHO IT IS WALKING AT IS NOT ON IT, deliberately. That is recomputed from the
// same list on every tick — it is a pure function of where everybody is standing,
// not a fact about the creature — and a stored copy would be a second answer to a
// question already answered, kept in step by hand. Nothing outside the step needs
// it: the wire does not carry it, the hit test does not read it, and contact
// damage lands on whoever it actually reaches rather than on whoever it set out
// for.
type Slop struct {
	// ID is the place in the building this слоп holds, and it is what the WIRE
	// calls it (message.go, Foe). Stable while it is alive and handed to the next
	// one once it is not — the same discipline, for the same reason, as an
	// occupant's slot, including the memory that has to be wiped with it (world.go,
	// killSlop).
	ID     int
	Pos    Vec2
	Sector int

	// Health is what is left of it. It starts at SlopHealth, which is exactly one
	// barrel, because nothing on the wire could acknowledge a hit that did not
	// kill — see that constant.
	Health int

	// touchAt is the tick this слоп may hurt somebody again, and zero is "right
	// now". A TICK DEADLINE rather than a countdown in seconds, for the reason the
	// respawn is one (world.go, World.ready): the world owns the tick, an integer
	// tick is exact where subtracting dt from a float accumulates the error of
	// SimStep's binary expansion, and no client has to reproduce it.
	touchAt int64
}

// prey is one man a слоп may walk at, in the shape the walking needs him: where
// he is standing and which room that is.
//
// A VALUE RATHER THAN A POINTER TO AN Occupant, so nothing in this file can reach
// the world, mutate a player or read a field that is not part of the geometry —
// the same rule, for the same reason, that keeps hit.go's body a value.
type prey struct {
	Pos    Vec2
	Sector int
}

// nearestPrey is the man a слоп walks at: the closest of them.
//
// DETERMINISM: the nearest is the FIRST of the closest, so the caller decides
// ties by the order it builds the slice in — World.stepSlops builds it in keys'
// order, which is sorted by account id, and never by ranging a map. Two people at
// exactly the same distance is not a hypothetical in a building this size, and a
// tie settled by map order would be a world that stopped replaying.
func nearestPrey(from Vec2, targets []prey) (prey, bool) {
	best, bestDist, found := prey{}, math.Inf(1), false
	for _, t := range targets {
		// Squared, because the comparison does not need the square root.
		d := (t.Pos.X-from.X)*(t.Pos.X-from.X) + (t.Pos.Y-from.Y)*(t.Pos.Y-from.Y)
		// Strictly less, so an exact tie keeps the earlier target.
		if d < bestDist {
			best, bestDist, found = t, d, true
		}
	}
	return best, found
}

// route is the first step of the walk from one room to another: the point to
// head for, and whether there is a way at all.
//
// ONE STEP AND NOT A PATH, because the thing being walked at moves. A route is
// recomputed for free every tick — it is a table lookup — where a stored path
// would have to be invalidated the moment its destination changed rooms, which is
// most of the time.
type route struct {
	// To is the middle of the doorway to leave by, nudged into the room beyond it
	// (content.go, doorwayStep).
	To Vec2
	// OK is false when the two rooms are not joined by any sequence of doorways.
	// It cannot happen in a generated level — the generator attaches every room to
	// one that already exists, so the portal graph is a tree — and it is answered
	// rather than assumed because a fixture level need not be.
	OK bool
}

// buildRoutes precomputes, for every ordered pair of rooms, where something
// walking from the first to the second should head next.
//
// ALL PAIRS, ONCE, WHEN THE BUILDING IS GENERATED, and it is the same shape and
// the same argument as the visibility table beside it (level.go,
// buildVisibility): a заброшка is a dozen rooms, so the whole table is a
// hundred-odd entries built once and read twice a tick, against a search that
// would flood the graph for every слоп on every tick and allocate to do it. A
// simulation loop that allocates twenty times a second is a self-inflicted
// garbage problem.
//
// A BREADTH-FIRST FLOOD FROM THE DESTINATION, exactly as the sibling office's
// grid does it (internal/gamefintech/navigate.go) and for the reason stated
// there: one flood answers "how far is every room from here", which is what a
// pursuer needs, and reading it downhill needs no path reconstruction and no
// off-by-one. Deliberately not A*: the graph is a dozen nodes, so a heuristic
// would cost more to get right than the whole search costs to run.
//
// Ties are broken by the LOWEST PORTAL INDEX rather than by anything that varies,
// so two processes generating the same level walk the same слоп through the same
// doorways.
func buildRoutes(l *Level) [][]route {
	n := len(l.Sectors)
	out := make([][]route, n)
	for i := range out {
		out[i] = make([]route, n)
	}

	// The portal graph, as a neighbour list. A portal naming a room that does not
	// exist is skipped rather than trusted: the generator cannot produce one, and
	// a fixture that did would otherwise take the whole table out with an index
	// panic on a tick.
	type link struct{ to, portal int }
	adj := make([][]link, n)
	for i, p := range l.Portals {
		if p.A < 0 || p.A >= n || p.B < 0 || p.B >= n {
			continue
		}
		adj[p.A] = append(adj[p.A], link{to: p.B, portal: i})
		adj[p.B] = append(adj[p.B], link{to: p.A, portal: i})
	}

	dist := make([]int, n)
	queue := make([]int, 0, n)
	for to := 0; to < n; to++ {
		for i := range dist {
			dist[i] = -1
		}
		dist[to] = 0
		queue = append(queue[:0], to)
		for head := 0; head < len(queue); head++ {
			cur := queue[head]
			for _, e := range adj[cur] {
				if dist[e.to] >= 0 {
					continue
				}
				dist[e.to] = dist[cur] + 1
				queue = append(queue, e.to)
			}
		}

		// Downhill from every other room: the neighbour one doorway closer.
		for from := 0; from < n; from++ {
			if from == to || dist[from] < 0 {
				continue
			}
			best, bestDist := -1, dist[from]
			for _, e := range adj[from] {
				if dist[e.to] >= 0 && dist[e.to] < bestDist {
					best, bestDist = e.portal, dist[e.to]
				}
			}
			if best < 0 {
				continue
			}
			out[from][to] = route{To: doorwayInto(l, l.Portals[best], from), OK: true}
		}
	}
	return out
}

// doorwayInto is the middle of a doorway, nudged out of the room being left and
// into the one being entered. See doorwayStep for why the nudge is not optional.
func doorwayInto(l *Level, p Portal, from int) Vec2 {
	mid := Vec2{X: p.At, Y: (p.Lo + p.Hi) / 2}
	if !p.Vertical {
		mid = Vec2{X: (p.Lo + p.Hi) / 2, Y: p.At}
	}
	other := p.A
	if from == p.A {
		other = p.B
	}
	// Towards the middle of the room on the other side, which is the only thing
	// in the level that says which way "through" is for this pair.
	c := l.Sectors[other].Center()
	if p.Vertical {
		if c.X > mid.X {
			mid.X += doorwayStep
		} else {
			mid.X -= doorwayStep
		}
		return mid
	}
	if c.Y > mid.Y {
		mid.Y += doorwayStep
	} else {
		mid.Y -= doorwayStep
	}
	return mid
}

// slopAim is where a слоп should head this tick.
//
// STRAIGHT AT HIM IN THE SAME ROOM, and the doorway otherwise. A room in this
// game is a rectangle with nothing standing in it, so inside one there is nothing
// to go around — which is what lets this be a table lookup and a comparison
// rather than the grid flood the sibling office needs for its furniture.
//
// A ROOM THE TABLE CANNOT REACH, or a sector index out of range, is answered by
// walking straight at him. Neither can happen in a generated level, and the
// honest failure if one ever did is a слоп grinding on a wall — a creature the
// player can see is stuck — rather than one standing still, which reads as the
// game having stopped.
func slopAim(routes [][]route, fromSector int, at prey) Vec2 {
	if fromSector == at.Sector {
		return at.Pos
	}
	if fromSector < 0 || fromSector >= len(routes) || at.Sector < 0 || at.Sector >= len(routes) {
		return at.Pos
	}
	r := routes[fromSector][at.Sector]
	if !r.OK {
		return at.Pos
	}
	return r.To
}

// stepSlop advances one слоп one step towards the man it is walking at.
//
// THE PLAYER'S OWN RESOLVER MOVES IT, which is what makes "a слоп cannot leave
// the building" true by construction rather than by a second rule that has to
// agree with the first: resolve refuses a move that lands outside every room and
// refuses a rise taller than MaxStep, so the worst a слоп can do is grind on a
// wall (sim.go, resolve). Pinned over hundreds of seeds by
// TestNoSlopEverLeavesTheBuilding.
func stepSlop(l *Level, routes [][]route, s Slop, at prey, dt float64) Slop {
	aim := slopAim(routes, s.Sector, at)
	dx, dy := aim.X-s.Pos.X, aim.Y-s.Pos.Y
	dist := math.Hypot(dx, dy)
	if dist < 1e-9 {
		return s
	}
	// Arriving exactly rather than overshooting and oscillating around him. On a
	// doorway that is simply the next waypoint next tick.
	to := aim
	if step := SlopSpeed * dt; step < dist {
		to = Vec2{X: s.Pos.X + dx/dist*step, Y: s.Pos.Y + dy/dist*step}
	}
	s.Pos, s.Sector = resolve(l, s.Pos, s.Sector, to)
	return s
}

// separate moves one слоп clear of another standing in the same place.
//
// THEY CONVERGE BY CONSTRUCTION, WHICH IS WHY THIS IS NOT AN EDGE CASE. Every
// слоп walks at the nearest man through the same table at the same speed, so from
// the moment two of their paths meet they compute an identical heading and cover
// an identical distance every tick, for ever. They do not brush past each other:
// they lock together, and the building then shows one figure where there are two
// — while the player takes contact damage from both of them, on two independent
// cooldowns, from something he can only see one of. A ray through a pile of
// coincident bodies is undefined about which it hit for the same reason.
//
// THE LOWER ID NEVER MOVES, which is the whole design of this function rather
// than an implementation detail. Splitting the overlap between the two would make
// each one's position a function of the other's, so a shot resolved at one, the
// tick it lands on and everything the rewind ring recorded about it would all
// shift because a second creature happened to walk into it. One yields; the other
// does not know it happened. It is the same rule, and the same reasoning, as the
// sibling office's Separate (internal/gamefintech/chaser.go), re-implemented
// rather than shared.
//
// It is pushed out ALONG THE LINE JOINING THEM, which is the shortest move that
// ends the overlap and leaves it beside where it was rather than behind: it walks
// at the same man from the new place on the next tick, so it still arrives. A
// push backwards would make it permanently the other's shadow.
//
// The degenerate case is deterministic on purpose: two discs exactly coincident
// have no line between them, so the fallback is a FIXED AXIS rather than a
// division by a distance of zero. Which axis does not matter — the resolver below
// puts it wherever the geometry allows — but that every process picks the same
// one does.
func separate(l *Level, s, other Slop) Slop {
	dx, dy := s.Pos.X-other.Pos.X, s.Pos.Y-other.Pos.Y
	dist := math.Hypot(dx, dy)
	if dist >= SlopReach {
		return s
	}
	if dist < 1e-9 {
		dx, dy, dist = 1, 0, 1
	}
	to := Vec2{
		X: other.Pos.X + dx/dist*SlopReach,
		Y: other.Pos.Y + dy/dist*SlopReach,
	}
	// Stepping aside can step into concrete, so it gets the same treatment every
	// other move in this game gets.
	s.Pos, s.Sector = resolve(l, s.Pos, s.Sector, to)
	return s
}

// touches reports whether a слоп has reached somebody — the two discs meeting,
// with nothing solid between them — and how far away he is, so that a caller with
// more than one man in reach can pick the nearest.
//
// It takes the POSITION and not the whole слоп, for the reason hit.go's body is a
// value: nothing about being reached depends on how much of the creature is left.
//
// THE CONCRETE STOPS IT, and that is a correctness rule rather than a refinement.
// Reach on its own is a plain two-dimensional distance, so two discs flush against
// opposite sides of a shared wall — each of them pushed PlayerRadius clear of it
// (sim.go, pushOut), which is exactly SlopReach apart — are touching THROUGH it.
// The wall sweep is the hit test's own (hit.go, wallDistance), against the same
// list of walls the resolver uses, so what stops a barrel is what stops a слоп and
// there is no second answer to keep in agreement.
//
// THE VISIBLE SET IS NOT THE LEASH FOR THIS ONE, which is worth saying because it
// is the leash for everything a barrel can do (world.go, targetsFor) and the rule
// it exists to enforce — nothing may hurt a man that he could not have seen —
// binds contact just as hard. Two rooms sharing a wall are in each other's visible
// set, which is what the doorway between them is for, so the table that answers
// "could he be on your screen" says yes for exactly the pair this has to refuse.
// The wall is the STRICTER reading of that rule and not an exemption from it: over
// a distance of SlopReach a clear line means the same room or one step through a
// doorway, and the table already calls both of those visible.
func touches(l *Level, slopPos, playerPos Vec2) (float64, bool) {
	dx, dy := playerPos.X-slopPos.X, playerPos.Y-slopPos.Y
	dist := math.Hypot(dx, dy)
	if dist > SlopReach {
		return dist, false
	}
	// Standing inside him, which has no direction in it to sweep along and no room
	// for concrete either.
	if dist < 1e-9 {
		return dist, true
	}
	return dist, wallDistance(l, slopPos, Vec2{X: dx / dist, Y: dy / dist}) > dist
}
