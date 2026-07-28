package gamevanyadum

import (
	"math"
	"testing"
)

// The level generator is tested as a set of INVARIANTS swept over many seeds,
// not as a handful of examples. A generator is exactly the kind of code where a
// hand-picked case proves nothing: it produces a different level every time, and
// the failures that matter — a room inside another room, a doorway too narrow to
// walk through, a beer nobody can reach — are the ones that show up on the seed
// nobody thought to write down.
//
// seeds is how many are swept. Large enough to catch a one-in-a-hundred
// placement bug, small enough that the whole file runs in well under a second.
const seeds = 300

func TestGenerateIsDeterministic(t *testing.T) {
	// The whole design rests on this: a level is a pure function of its seed, so
	// eight bytes in a database row is the entire durable record of a run's
	// geometry, and a replay is possible without storing anything.
	for _, seed := range []int64{0, 1, -1, 42, 1 << 40} {
		a, b := Generate(seed), Generate(seed)
		if len(a.Sectors) != len(b.Sectors) || len(a.Portals) != len(b.Portals) || len(a.Pickups) != len(b.Pickups) {
			t.Fatalf("seed %d: two generations differ in size", seed)
		}
		for i := range a.Sectors {
			if a.Sectors[i] != b.Sectors[i] {
				t.Fatalf("seed %d: sector %d differs: %+v vs %+v", seed, i, a.Sectors[i], b.Sectors[i])
			}
		}
		if a.Spawn != b.Spawn || a.SpawnSector != b.SpawnSector {
			t.Fatalf("seed %d: spawn differs", seed)
		}
	}
}

func TestGenerateProducesDifferentLevelsForDifferentSeeds(t *testing.T) {
	// Not a strong claim, just the one that catches a generator accidentally
	// ignoring its seed — which is a bug that every other test in this file
	// would happily pass.
	first := Generate(1)
	same := 0
	for s := int64(2); s < 20; s++ {
		if len(Generate(s).Sectors) == len(first.Sectors) {
			same++
		}
	}
	if same == 18 {
		t.Fatal("every seed produced the same number of rooms; is the seed being used?")
	}
}

func TestSectorsNeverOverlap(t *testing.T) {
	// Two rooms sharing interior area is the worst failure this generator has,
	// because nothing downstream notices: SectorAt picks one of them, the walls
	// of the other are still solid, and the player is stuck inside geometry that
	// looks fine from the outside.
	const eps = 1e-9
	for seed := int64(0); seed < seeds; seed++ {
		l := Generate(seed)
		for i := range l.Sectors {
			for j := i + 1; j < len(l.Sectors); j++ {
				a, b := l.Sectors[i], l.Sectors[j]
				if a.MinX < b.MaxX-eps && b.MinX < a.MaxX-eps &&
					a.MinY < b.MaxY-eps && b.MinY < a.MaxY-eps {
					t.Fatalf("seed %d: sectors %d and %d overlap: %+v %+v", seed, i, j, a, b)
				}
			}
		}
	}
}

func TestEveryRoomIsReachableFromSpawn(t *testing.T) {
	// Connectivity is meant to be true BY CONSTRUCTION — every room is attached
	// to one that already exists, so the portal graph is a tree. This test is
	// what stops that reasoning quietly becoming false when the generator grows
	// a second way of placing a room.
	for seed := int64(0); seed < seeds; seed++ {
		l := Generate(seed)
		adj := make(map[int][]int, len(l.Sectors))
		for _, p := range l.Portals {
			adj[p.A] = append(adj[p.A], p.B)
			adj[p.B] = append(adj[p.B], p.A)
		}
		seen := map[int]bool{l.SpawnSector: true}
		queue := []int{l.SpawnSector}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, n := range adj[cur] {
				if !seen[n] {
					seen[n] = true
					queue = append(queue, n)
				}
			}
		}
		if len(seen) != len(l.Sectors) {
			t.Fatalf("seed %d: %d of %d rooms reachable from spawn", seed, len(seen), len(l.Sectors))
		}
	}
}

func TestEveryDoorwayCanBeWalkedThrough(t *testing.T) {
	// Two separate ways a doorway can be generated that the player cannot use:
	// too narrow for his own diameter, or with a floor change taller than he can
	// step up. Both would look like an invisible wall, which is the single most
	// confusing bug a level generator can ship.
	for seed := int64(0); seed < seeds; seed++ {
		l := Generate(seed)
		for _, p := range l.Portals {
			if w := p.Hi - p.Lo; w < 2*PlayerRadius {
				t.Fatalf("seed %d: doorway %.2f m wide, player is %.2f m across", seed, w, 2*PlayerRadius)
			}
			rise := math.Abs(l.Sectors[p.A].FloorZ - l.Sectors[p.B].FloorZ)
			if rise > MaxStep+1e-9 {
				t.Fatalf("seed %d: doorway between %d and %d rises %.2f m, MaxStep is %.2f",
					seed, p.A, p.B, rise, MaxStep)
			}
		}
	}
}

func TestSpawnAndPickupsAreInsideRoomsAndClearOfWalls(t *testing.T) {
	// A pickup generated inside a wall is a run that cannot be finished, because
	// finishing this iteration's run means collecting all of them.
	for seed := int64(0); seed < seeds; seed++ {
		l := Generate(seed)
		if id := l.SectorAt(l.Spawn); id != l.SpawnSector {
			t.Fatalf("seed %d: spawn is in sector %d, not the declared %d", seed, id, l.SpawnSector)
		}
		for _, p := range l.Pickups {
			id := l.SectorAt(p.Pos)
			if id < 0 {
				t.Fatalf("seed %d: pickup %d at %+v is outside every room", seed, p.ID, p.Pos)
			}
			if id != p.Sector {
				t.Fatalf("seed %d: pickup %d says sector %d, geometry says %d", seed, p.ID, p.Sector, id)
			}
			s := l.Sectors[id]
			if p.Pos.X-s.MinX < PlayerRadius || s.MaxX-p.Pos.X < PlayerRadius ||
				p.Pos.Y-s.MinY < PlayerRadius || s.MaxY-p.Pos.Y < PlayerRadius {
				t.Fatalf("seed %d: pickup %d is inside the wall of sector %d", seed, p.ID, id)
			}
		}
	}
}

func TestPickupsAreNeverInTheSpawnRoom(t *testing.T) {
	// Walking somewhere to find the beer is the entire loop this iteration
	// exists to prove; a run that is complete before the player moves proves
	// nothing.
	for seed := int64(0); seed < seeds; seed++ {
		l := Generate(seed)
		for _, p := range l.Pickups {
			if p.Sector == l.SpawnSector {
				t.Fatalf("seed %d: pickup %d generated in the spawn room", seed, p.ID)
			}
		}
	}
}

func TestLevelAlwaysHasSomethingToCollect(t *testing.T) {
	// The run's end condition is "no pickups left". A level generated with none
	// would be a run that can never finish rather than one that finishes
	// instantly — Advance guards on len(Pickups) — and either way it is a level
	// nobody can complete.
	for seed := int64(0); seed < seeds; seed++ {
		if l := Generate(seed); len(l.Pickups) == 0 {
			t.Fatalf("seed %d: nothing to collect", seed)
		}
	}
}

func TestPortalsAreCutOutOfTheWalls(t *testing.T) {
	// The walls are DERIVED from sectors minus portals, and this is what pins
	// that derivation: the middle of every doorway must be free of solid
	// segments on the line it lies on.
	const eps = 1e-9
	for seed := int64(0); seed < seeds; seed++ {
		l := Generate(seed)
		for _, p := range l.Portals {
			mid := (p.Lo + p.Hi) / 2
			for _, w := range l.Walls {
				if w.Vertical != p.Vertical || math.Abs(w.At-p.At) > eps {
					continue
				}
				if mid > w.Lo+eps && mid < w.Hi-eps {
					t.Fatalf("seed %d: wall %+v still spans the middle of doorway %+v", seed, w, p)
				}
			}
		}
	}
}

func TestSubtractPortalsLeavesTheJambs(t *testing.T) {
	// The one piece of the wall derivation worth testing directly, because
	// getting it wrong in the OTHER direction — leaving no jambs — is what would
	// let a player walk through the wall beside a door rather than through the
	// door.
	edge := Wall{Vertical: true, At: 5, Lo: 0, Hi: 10}
	got := subtractPortals(edge, []Portal{{Vertical: true, At: 5, Lo: 4, Hi: 6}})
	if len(got) != 2 {
		t.Fatalf("expected two jambs, got %d: %+v", len(got), got)
	}
	if got[0].Lo != 0 || got[0].Hi != 4 || got[1].Lo != 6 || got[1].Hi != 10 {
		t.Fatalf("jambs are in the wrong place: %+v", got)
	}

	// An opening that swallows the whole edge leaves nothing solid at all.
	if got := subtractPortals(edge, []Portal{{Vertical: true, At: 5, Lo: -1, Hi: 11}}); len(got) != 0 {
		t.Fatalf("a doorway spanning the edge should leave no wall, got %+v", got)
	}

	// An opening on a different line does not touch this edge.
	if got := subtractPortals(edge, []Portal{{Vertical: true, At: 9, Lo: 4, Hi: 6}}); len(got) != 1 || got[0] != edge {
		t.Fatalf("an unrelated doorway changed the edge: %+v", got)
	}
}
