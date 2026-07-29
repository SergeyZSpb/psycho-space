package gamekaren

import (
	"math"
	"sync"
)

// Getting round a desk.
//
// WHY THIS EXISTS. StepBoss used to be pure pursuit: head at the target, then be
// pushed out of whatever you ended up inside. That is a perfectly good rule in an
// open room and a broken one in a room with furniture — a desk between him and a
// player is not something he goes around, it is a wall he grinds along, and which
// way the push sends him has nothing to do with where he is trying to get.
// Measured on the real simulation against a player standing perfectly still: from
// a point in a desk's shadow he took as long as NINETY SECONDS to arrive, against
// under five from a point he could see. In a game whose optimal play is standing
// still, that is not cover — it is a corner of the room where the antagonist
// cannot reach you, which is the one thing this game cannot have.
//
// WHAT IT IS. A coarse uniform grid over the static office, a breadth-first
// search from the TARGET each tick, and a boss who walks downhill through the
// resulting distance field. That is the whole of it. Deliberately not A*: the
// grid is 1400 cells and there is one boss, so a full flood fill costs less than
// the priority queue would, and BFS has no heuristic to get subtly wrong.
//
// WHY FROM THE TARGET RATHER THAN FROM HIM. One flood fill answers "how far is
// every cell from the player", which is exactly what a pursuer needs and is
// reusable for every pursuer that ever wants it. From the boss it would answer a
// question nobody asked and would have to stop early, which is where an
// off-by-one becomes a boss that walks into a wall.
//
// WHAT IT DOES NOT CHANGE. He is still slower than you, he still has no idea
// what you are about to do, and he still walks into desks and is pushed out of
// them — the resolver in StepBoss is untouched. Furniture is still worth
// standing behind, because going around it takes him time. What it stops being
// is a place he never arrives at at all.

// The navigation grid. Cell size is a compromise stated rather than guessed: it
// has to be small enough that the gaps between the desks are several cells wide
// — the narrowest passage in the catalogue is comfortably over a metre, so half
// a metre gives at least two cells of it — and large enough that a flood fill is
// nothing. 32 x 44 = 1408 cells.
const (
	navCell = 0.5
	navCols = int(OfficeW / navCell)
	navRows = int(OfficeH / navCell)
)

// navFree reports, per cell, whether the bald man's disc fits with its centre at
// that cell's middle.
//
// Built ONCE from the catalogue's own desks, not from the argument StepBoss
// takes. That is deliberate and it is not an inconsistency: the office in this
// game is STATIC and lives in the catalogue (unlike «ВАНЯДУМ», where a level is
// generated per run), so the navigable space is a constant of the content rather
// than a function of a parameter. The `desks` argument still drives the collision
// resolver, which is per-call and unchanged.
var navFree = sync.OnceValue(func() []bool {
	free := make([]bool, navCols*navRows)
	for r := 0; r < navRows; r++ {
		for c := 0; c < navCols; c++ {
			at := navCentre(c, r)
			ok := at.X >= BossRadius && at.X <= OfficeW-BossRadius &&
				at.Y >= BossRadius && at.Y <= OfficeH-BossRadius
			for _, d := range Desks {
				if insideDesk(d, at, BossRadius) {
					ok = false
					break
				}
			}
			free[r*navCols+c] = ok
		}
	}
	return free
})

// navCentre is the middle of a cell, in metres.
func navCentre(col, row int) Vec2 {
	return Vec2{X: (float64(col) + 0.5) * navCell, Y: (float64(row) + 0.5) * navCell}
}

// navCellOf is which cell a point falls in, clamped to the grid.
func navCellOf(p Vec2) (col, row int) {
	col = int(p.X / navCell)
	row = int(p.Y / navCell)
	return clampInt(col, 0, navCols-1), clampInt(row, 0, navRows-1)
}

func clampInt(v, lo, hi int) int {
	switch {
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

// navAimAt is where he should head in order to get to `to`, given that he is at
// `from` and cannot walk through the furniture.
//
// It returns `to` itself whenever there is nothing in the way, which is the
// common case and the one worth keeping smooth: a boss who followed grid cells
// across an empty floor would move in visible steps for no reason. Only when the
// straight line is blocked does the path decide, and then it returns the centre
// of the NEXT cell along rather than a whole route — he is re-planned every tick
// anyway, because the thing he is chasing moves.
//
// It also returns `to` when there is no path at all. That cannot happen in the
// catalogue office, whose free space is one connected region, and if a later
// layout breaks that the honest failure is the old behaviour — walking at you and
// grinding on a desk — rather than standing still.
func navAimAt(from, to Vec2) Vec2 {
	// Nothing in the way: go straight at him. The disc that has to fit is HIS,
	// so the line is tested at BossRadius.
	if clearLine(from, to, BossRadius) {
		return to
	}

	free := navFree()
	tc, tr := navCellOf(to)
	fc, fr := navCellOf(from)
	if fc == tc && fr == tr {
		return to
	}

	// The flood fill, from the target. A blocked target cell — he is standing in
	// a doorway the grid rounds into a desk — is nudged to the nearest free
	// neighbour rather than abandoning the search, because "close enough to walk
	// at directly" is exactly the state the caller is in by then.
	start := tr*navCols + tc
	if !free[start] {
		if n, ok := navNearestFree(free, tc, tr); ok {
			start = n
		} else {
			return to
		}
	}

	dist := make([]int32, navCols*navRows)
	for i := range dist {
		dist[i] = -1
	}
	dist[start] = 0
	queue := make([]int32, 1, navCols*navRows)
	queue[0] = int32(start) //nolint:gosec // bounded by the grid, which is 1408 cells

	//nolint:gosec // bounded by the grid: fr < navRows and fc < navCols, both
	// clamped by navCellOf, so the product is under 1408 and cannot overflow.
	goal := int32(fr*navCols + fc)
	found := false
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if cur == goal {
			found = true
			break
		}
		cc, cr := int(cur)%navCols, int(cur)/navCols
		// FOUR-WAY, not eight. A diagonal step between two desk corners would
		// squeeze him through a gap his disc does not fit in, and the resolver
		// would then push him back out — which looks exactly like the sticking
		// this exists to remove.
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nc, nr := cc+d[0], cr+d[1]
			if nc < 0 || nc >= navCols || nr < 0 || nr >= navRows {
				continue
			}
			n := int32(nr*navCols + nc) //nolint:gosec // bounded by the grid
			if dist[n] >= 0 || !free[n] {
				continue
			}
			dist[n] = dist[cur] + 1
			queue = append(queue, n)
		}
	}
	if !found {
		return to
	}

	// Downhill from his own cell: the neighbour one step closer to the target.
	// Ties are broken by the fixed order above rather than by anything that
	// varies, so two identical offices step him identically — the same rule that
	// keeps the rest of this game deterministic.
	best, bestD := -1, dist[goal]
	for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nc, nr := fc+d[0], fr+d[1]
		if nc < 0 || nc >= navCols || nr < 0 || nr >= navRows {
			continue
		}
		n := nr*navCols + nc
		if !free[n] || dist[n] < 0 {
			continue
		}
		if dist[n] < bestD {
			best, bestD = n, dist[n]
		}
	}
	if best < 0 {
		return to
	}
	return navCentre(best%navCols, best/navCols)
}

// navNearestFree is the closest free cell to a blocked one, searched in rings so
// the answer does not depend on iteration order. Bounded, because an unbounded
// ring search over a grid with no free cells at all would walk the whole thing.
func navNearestFree(free []bool, col, row int) (int, bool) {
	const rings = 4
	for k := 1; k <= rings; k++ {
		for dr := -k; dr <= k; dr++ {
			for dc := -k; dc <= k; dc++ {
				if int(math.Abs(float64(dr))) != k && int(math.Abs(float64(dc))) != k {
					continue
				}
				c, r := col+dc, row+dr
				if c < 0 || c >= navCols || r < 0 || r >= navRows {
					continue
				}
				if n := r*navCols + c; free[n] {
					return n, true
				}
			}
		}
	}
	return 0, false
}
