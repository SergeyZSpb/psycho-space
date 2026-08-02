package gamevanyadum

import (
	"math"
	"math/rand"
)

// The level, and the seeded generator that produces one.
//
// THE MODEL IS DOOM'S, NOT QUAKE'S. A level is a set of rectangular sectors on
// the floor plane, each with its own floor and ceiling height, joined by portals
// cut through the walls they share. That is enough for everything this game
// needs — rooms at different heights, steps you walk up, doorways, and later a
// door that a key opens — while collision stays a circle against a list of
// axis-aligned segments, exact and testable, with no physics engine anywhere.
//
// A BSP tree, arbitrary convex sectors and true 3D brushes would all be more
// faithful to Quake and all cost far more than the game can currently spend.
// Rectangles are the simplest thing that satisfies the requirement; the sector
// graph below is the seam that would let something richer replace them.
//
// THE LEVEL IS GENERATED HERE AND NOWHERE ELSE. The client is sent the finished
// graph once, over HTTP, and builds meshes from it. The alternative
// — shipping the seed and generating it a second time in TypeScript — was
// refused: two implementations of one generator diverge on a floating-point edge
// and the first symptom is one player walking through a wall another can see.

// Vec2 is a point on the floor plane, in metres. Height is not a coordinate
// here: it belongs to the sector you are standing in.
type Vec2 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Sector is one rectangular room.
type Sector struct {
	ID     int     `json:"id"`
	MinX   float64 `json:"x0"`
	MinY   float64 `json:"y0"`
	MaxX   float64 `json:"x1"`
	MaxY   float64 `json:"y1"`
	FloorZ float64 `json:"fz"`
	CeilZ  float64 `json:"cz"`
	// Surface keys into the served catalogue; the client generates the texture.
	Wall    string `json:"w"`
	Floor   string `json:"f"`
	Ceiling string `json:"c"`
	// Light is 0..1 and is the whole of this game's lighting model in iteration
	// one: a per-sector level the renderer multiplies into the material.
	Light float64 `json:"l"`
}

// Contains reports whether p is inside this sector, boundary included.
//
// Boundaries are INCLUSIVE and rooms share them exactly — walls in this model
// have no thickness, exactly as Doom's did. A point on a shared edge is
// therefore in both rooms, and SectorAt resolves it deterministically by taking
// the first.
func (s Sector) Contains(p Vec2) bool {
	return p.X >= s.MinX && p.X <= s.MaxX && p.Y >= s.MinY && p.Y <= s.MaxY
}

// Center is the middle of the room, which is where things are placed when
// nothing more interesting decides.
func (s Sector) Center() Vec2 {
	return Vec2{X: (s.MinX + s.MaxX) / 2, Y: (s.MinY + s.MaxY) / 2}
}

// Wall is a solid axis-aligned segment the player cannot cross.
//
// Walls are DERIVED from the sectors and portals, not authored: every sector
// edge contributes a wall except where a portal has been cut out of it. That
// derivation happens once, in buildWalls, so the simulation never has to reason
// about doorways — it only ever pushes a circle out of a list of segments.
type Wall struct {
	// Vertical means the segment lies along a line X = At, spanning Lo..Hi in Y.
	// Otherwise it lies along Y = At, spanning Lo..Hi in X.
	Vertical bool    `json:"v"`
	At       float64 `json:"a"`
	Lo       float64 `json:"lo"`
	Hi       float64 `json:"hi"`
	// Sector is whose edge this is. The simulation never reads it — a wall is a
	// wall from either side — but the CLIENT cannot build the geometry without
	// it: how tall a wall is, and what it is made of, are facts about the room
	// it belongs to. Two rooms sharing a boundary each contribute their own
	// segment, which is also what draws the step riser between floors at
	// different heights.
	Sector int `json:"s"`
}

// Portal is an opening in the wall two sectors share.
type Portal struct {
	A int `json:"a"`
	B int `json:"b"`
	// Vertical, At, Lo and Hi describe the opening exactly as a Wall describes a
	// solid, so the two can be subtracted from one another without a conversion.
	Vertical bool    `json:"v"`
	At       float64 `json:"at"`
	Lo       float64 `json:"lo"`
	Hi       float64 `json:"hi"`
}

// Pickup is a thing lying on the floor of a sector.
type Pickup struct {
	ID     int    `json:"id"`
	Kind   string `json:"k"`
	Sector int    `json:"s"`
	Pos    Vec2   `json:"p"`
}

// Level is a whole generated floor: what it is made of, what is lying in it, and
// where the player starts.
type Level struct {
	Seed        int64    `json:"seed"`
	Sectors     []Sector `json:"sectors"`
	Portals     []Portal `json:"portals"`
	Walls       []Wall   `json:"walls"`
	Pickups     []Pickup `json:"pickups"`
	Spawn       Vec2     `json:"spawn"`
	SpawnSector int      `json:"spawn_sector"`
	SpawnYaw    float64  `json:"spawn_yaw"`
}

// SectorAt returns the id of the sector containing p, or -1 when p is outside
// every room. Shared boundaries belong to both rooms and the lowest id wins,
// which makes the answer a deterministic function of the level rather than of
// iteration order.
func (l *Level) SectorAt(p Vec2) int {
	for i := range l.Sectors {
		if l.Sectors[i].Contains(p) {
			return l.Sectors[i].ID
		}
	}
	return -1
}

// FloorAt returns the floor height of the sector containing p, and whether there
// was one.
func (l *Level) FloorAt(p Vec2) (float64, bool) {
	id := l.SectorAt(p)
	if id < 0 {
		return 0, false
	}
	return l.Sectors[id].FloorZ, true
}

// Generate builds a level from a seed. The same seed always produces the same
// level, which is what lets a building be reproduced from one number and never
// stored, and what makes the invariant tests able to sweep hundreds of seeds.
//
// The shape is a random walk over a grid of rooms: start with one, then
// repeatedly attach another to a wall of an existing room, rejecting any
// placement that overlaps something already there. Because every room is
// attached to one that already exists, the connectivity graph is a tree and
// everything is reachable from the spawn by construction — which is asserted
// rather than assumed, in level_test.go.
func Generate(seed int64) *Level {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // level shape, not security: crypto/rand would make a level unreproducible from its seed, which is the whole point.

	target := RoomsMin + rng.Intn(RoomsMax-RoomsMin+1)

	first := Sector{
		ID:     0,
		MinX:   0,
		MinY:   0,
		MaxX:   roomSize(rng),
		MaxY:   roomSize(rng),
		FloorZ: 0,
	}
	l := &Level{Seed: seed, Sectors: []Sector{first}}

	for attempts := 0; attempts < GenerationAttempts && len(l.Sectors) < target; attempts++ {
		host := l.Sectors[rng.Intn(len(l.Sectors))]
		side := rng.Intn(4)
		cand, portal, ok := placeAdjacent(rng, host, side, len(l.Sectors))
		if !ok || l.overlapsAny(cand) {
			continue
		}
		l.Sectors = append(l.Sectors, cand)
		l.Portals = append(l.Portals, portal)
	}

	// Surfaces and light are decided after the shape, so a room's appearance can
	// never influence where it was allowed to go.
	for i := range l.Sectors {
		l.Sectors[i].CeilZ = l.Sectors[i].FloorZ + CeilingHeight
		l.Sectors[i].Wall = "concrete"
		if rng.Intn(3) == 0 {
			l.Sectors[i].Wall = "brick"
		}
		l.Sectors[i].Floor = "floor"
		l.Sectors[i].Ceiling = "ceiling"
		// Enough variation that rooms read as different places, never so dark
		// that a phone in daylight shows a black rectangle.
		l.Sectors[i].Light = 0.55 + rng.Float64()*0.4
	}

	l.Walls = buildWalls(l)
	l.placePickups(rng)

	l.SpawnSector = 0
	l.Spawn = l.Sectors[0].Center()
	l.SpawnYaw = float64(rng.Intn(4)) * (math.Pi / 2)
	return l
}

// roomSize draws one axis of a room.
func roomSize(rng *rand.Rand) float64 {
	return RoomMin + rng.Float64()*(RoomMax-RoomMin)
}

// placeAdjacent proposes a room attached to one side of host, together with the
// portal joining them. It reports false when the two cannot share enough wall
// for a doorway — which is the common case and is not an error.
//
// side is 0:+X 1:-X 2:+Y 3:-Y.
func placeAdjacent(rng *rand.Rand, host Sector, side, id int) (Sector, Portal, bool) {
	w, h := roomSize(rng), roomSize(rng)
	vertical := side == 0 || side == 1

	// The new room shares a boundary LINE with the host and is offset along it
	// by a random amount, so corridors do not all line up on their centres.
	var s Sector
	s.ID = id
	if vertical {
		if side == 0 {
			s.MinX, s.MaxX = host.MaxX, host.MaxX+w
		} else {
			s.MinX, s.MaxX = host.MinX-w, host.MinX
		}
		span := host.MaxY - host.MinY
		off := (rng.Float64()*2 - 1) * span * 0.4
		s.MinY = host.MinY + off
		s.MaxY = s.MinY + h
	} else {
		if side == 2 {
			s.MinY, s.MaxY = host.MaxY, host.MaxY+h
		} else {
			s.MinY, s.MaxY = host.MinY-h, host.MinY
		}
		span := host.MaxX - host.MinX
		off := (rng.Float64()*2 - 1) * span * 0.4
		s.MinX = host.MinX + off
		s.MaxX = s.MinX + w
	}

	// The height change is a whole number of FloorStep and never exceeds
	// MaxStep, so every doorway this generator cuts is one the player can walk
	// through. Drops you cannot climb back out of are a later iteration and a
	// reachability problem, not a free bit of variety.
	maxSteps := int(MaxStep / FloorStep)
	s.FloorZ = host.FloorZ + float64(rng.Intn(2*maxSteps+1)-maxSteps)*FloorStep

	// The doorway goes in the middle of whatever wall the two rooms actually
	// share, which is not the middle of either of them.
	var lo, hi float64
	if vertical {
		lo, hi = math.Max(host.MinY, s.MinY), math.Min(host.MaxY, s.MaxY)
	} else {
		lo, hi = math.Max(host.MinX, s.MinX), math.Min(host.MaxX, s.MaxX)
	}
	if hi-lo < MinSharedWall {
		return Sector{}, Portal{}, false
	}
	mid := (lo + hi) / 2
	p := Portal{
		A: host.ID, B: id,
		Vertical: vertical,
		Lo:       mid - DoorWidth/2,
		Hi:       mid + DoorWidth/2,
	}
	if vertical {
		p.At = s.MinX
		if side == 1 {
			p.At = s.MaxX
		}
	} else {
		p.At = s.MinY
		if side == 3 {
			p.At = s.MaxY
		}
	}
	return s, p, true
}

// overlapsAny reports whether cand shares interior area with a room already
// placed. Touching is not overlapping — that is the whole point of the model,
// since two rooms that touch are two rooms a door can join.
func (l *Level) overlapsAny(cand Sector) bool {
	const eps = 1e-9
	for _, s := range l.Sectors {
		if cand.MinX < s.MaxX-eps && s.MinX < cand.MaxX-eps &&
			cand.MinY < s.MaxY-eps && s.MinY < cand.MaxY-eps {
			return true
		}
	}
	return false
}

// buildWalls turns sectors and portals into the flat list of solid segments the
// simulation collides against.
//
// Every sector contributes its four edges, and every portal is then SUBTRACTED
// from whichever edges it lies on — leaving the jambs either side of the opening
// as walls in their own right, which is what stops a player rounding a doorway
// through the wall beside it. Two rooms that share an edge each contribute it,
// so some segments appear twice; that is harmless, because pushing a circle out
// of the same line twice is the same as doing it once.
func buildWalls(l *Level) []Wall {
	var out []Wall
	for _, s := range l.Sectors {
		edges := []Wall{
			{Vertical: true, At: s.MinX, Lo: s.MinY, Hi: s.MaxY, Sector: s.ID},
			{Vertical: true, At: s.MaxX, Lo: s.MinY, Hi: s.MaxY, Sector: s.ID},
			{Vertical: false, At: s.MinY, Lo: s.MinX, Hi: s.MaxX, Sector: s.ID},
			{Vertical: false, At: s.MaxY, Lo: s.MinX, Hi: s.MaxX, Sector: s.ID},
		}
		for _, e := range edges {
			out = append(out, subtractPortals(e, l.Portals)...)
		}
	}
	return out
}

// buildVisibility precomputes the potentially-visible set: visible[a][b] reports
// whether somebody standing in sector b is drawn by somebody standing in sector
// a.
//
// THE SECTOR GRAPH IS ALREADY THE ANSWER, which is what makes filtering peers
// nearly free. Rooms are rectangles joined by portals cut through the walls they
// share, so from inside one room the only things there are to see are what is in
// it and what is through one of its doorways — adjacency in the portal graph IS
// the visible set. It is a table of a hundred-odd booleans built once per
// building and read twice per peer per frame, against a line-of-sight test that
// would sweep every wall in the заброшка for every pair of people on every tick.
//
// IT IS AN APPROXIMATION IN BOTH DIRECTIONS, and both are accepted rather than
// overlooked. It OVER-sends: a peer in the next room may be standing behind that
// room's far wall with the doorway nowhere near him, and he is sent anyway. It
// also UNDER-sends: two doorways that happen to line up genuinely do show you
// the room beyond the next one, and somebody standing in that line is not sent —
// so he appears at the doorway rather than being visible through it. Both errors
// are bounded by a room, which is the trade every adjacency-based PVS makes.
//
// SYMMETRIC BY CONSTRUCTION, because adjacency is. If you are on my frame I am
// on yours, and that is what stops a player being hit by somebody his own client
// was never told about — the hit test is confined to this set (world.go,
// targetsFor), so the symmetry is load-bearing rather than merely tidy.
//
// IT SAYS NOTHING ABOUT TIME, WHICH IS WHY IT IS NOT READ ON ITS OWN. A sector is
// derived from a position, so somebody standing in a doorway belongs to whichever
// room the last sub-centimetre of movement put him in, and this table answers a
// different question either side of that line — which strobes everybody who is
// adjacent to one of the two rooms and not the other. The set is therefore held
// for a moment after it changes rather than read raw: world.go, heldVisible, and
// visibleHold for the argument.
func buildVisibility(l *Level) [][]bool {
	out := make([][]bool, len(l.Sectors))
	for i := range out {
		out[i] = make([]bool, len(l.Sectors))
		// Your own room, which no portal reports.
		out[i][i] = true
	}
	for _, p := range l.Portals {
		out[p.A][p.B], out[p.B][p.A] = true, true
	}
	return out
}

// subtractPortals cuts every opening that lies on this edge out of it, returning
// what is left solid. An edge with no opening comes back unchanged; one whose
// opening spans it entirely comes back as nothing at all.
func subtractPortals(e Wall, portals []Portal) []Wall {
	segs := []Wall{e}
	const eps = 1e-9
	for _, p := range portals {
		if p.Vertical != e.Vertical || math.Abs(p.At-e.At) > eps {
			continue
		}
		var next []Wall
		for _, s := range segs {
			if p.Hi <= s.Lo+eps || p.Lo >= s.Hi-eps {
				next = append(next, s) // the opening misses this piece entirely
				continue
			}
			if p.Lo > s.Lo+eps {
				next = append(next, Wall{Vertical: s.Vertical, At: s.At, Lo: s.Lo, Hi: p.Lo, Sector: s.Sector})
			}
			if p.Hi < s.Hi-eps {
				next = append(next, Wall{Vertical: s.Vertical, At: s.At, Lo: p.Hi, Hi: s.Hi, Sector: s.Sector})
			}
		}
		segs = next
	}
	return segs
}

// placePickups scatters beer through the level, never in the room the player
// starts in — walking somewhere to find it is the entire loop this iteration
// exists to prove.
func (l *Level) placePickups(rng *rand.Rand) {
	if len(l.Sectors) < 2 {
		return
	}
	count := 2 + rng.Intn(2)
	for i := 0; i < count; i++ {
		s := l.Sectors[1+rng.Intn(len(l.Sectors)-1)]
		l.Pickups = append(l.Pickups, Pickup{
			ID:     i,
			Kind:   Pickups[rng.Intn(len(Pickups))].Key,
			Sector: s.ID,
			Pos:    randomSpot(rng, s),
		})
	}
}

// randomSpot draws a point inside a room, kept a clear player-radius off the
// walls so that nothing lands half-buried in the concrete or out of reach behind
// a jamb.
//
// TWO CALLERS, WHICH IS WHY IT IS A FUNCTION: the generator scattering beer, and
// the spawner deciding where a нейрослоп appears (world.go, spawnSlop). The
// second one is what made it worth extracting — the inset argument is the same
// argument in both cases, and a слоп generated inside a wall would be pushed out
// by the resolver on its first step in a direction nobody chose.
//
// It draws X and then Y, in that order, because a level is reproducible from its
// seed and the order the numbers are drawn in IS part of that reproduction.
func randomSpot(rng *rand.Rand, s Sector) Vec2 {
	inset := PlayerRadius * 2
	return Vec2{
		X: s.MinX + inset + rng.Float64()*math.Max(0, (s.MaxX-s.MinX)-2*inset),
		Y: s.MinY + inset + rng.Float64()*math.Max(0, (s.MaxY-s.MinY)-2*inset),
	}
}
