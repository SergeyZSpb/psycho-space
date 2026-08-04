package gamefintech

import "math"

// IS THIS OFFICE PLAYABLE? — one predicate, three callers.
//
// The generator asks it whether a candidate object may stand where it was drawn,
// the admin save path asks it whether a hand-drawn floor may be installed, and the
// seed sweep asks it of a few hundred generated offices at once. That is
// deliberate rather than convenient: a level somebody dragged into shape has to be
// held to exactly the rules a generated one is, and two implementations of
// «playable» would drift the moment one of them was retuned.
//
// IT ANSWERS IN CODES, NOT PROSE. An issue is a stable snake_case string plus the
// index of the thing it is about, because the editor has to point at what is
// wrong, and because this project never returns an error's text to a client. The
// Russian for each code is written once, in the browser, where the rest of this
// game's Russian lives.

// LayoutProblem is one way an office can be unplayable.
type LayoutProblem string

const (
	// ProblemBadKind is a solid whose kind is not in Kinds.
	ProblemBadKind LayoutProblem = "bad_kind"
	// ProblemBadSize is a rectangle that is not finite, or is smaller than
	// MinSolidSide, or longer than MaxSolidSide.
	ProblemBadSize LayoutProblem = "bad_size"
	// ProblemOffFloor is a solid closer to a wall than MinGap — which is both a
	// resolver hazard below ResolverFloor and a slot nobody can walk down above it.
	ProblemOffFloor LayoutProblem = "off_floor"
	// ProblemTooClose is two solids closer to each other than MinGap, measured per
	// axis. Reported against the second of the pair, which is the one the editor
	// should highlight when somebody has just dragged it.
	ProblemTooClose LayoutProblem = "too_close"
	// ProblemSpotBlocked is a catalogue point with furniture standing on it: a
	// spawn nobody can use, or a bottle nobody can pick up.
	ProblemSpotBlocked LayoutProblem = "spot_blocked"
	// ProblemSplitFloor is free space that is not one region — a pocket a player
	// can be dropped into and never leave, or that the лысый can never walk into.
	ProblemSplitFloor LayoutProblem = "split_floor"
	// ProblemTooMany is more solids or more windows than may be saved at all.
	ProblemTooMany LayoutProblem = "too_many"
	// ProblemBadWindow is glazing on no wall, hanging off the end of one, shorter
	// than a pane, or overlapping its neighbour.
	ProblemBadWindow LayoutProblem = "bad_window"
)

// LayoutIssue is one problem and what it is about.
//
// Index addresses the solids, or the windows when the problem is ProblemBadWindow,
// or nothing at all when it is WholeLayout. Two lists and one index is
// unambiguous because exactly one problem is about a window.
type LayoutIssue struct {
	Problem LayoutProblem `json:"problem"`
	Index   int           `json:"index"`
}

// WholeLayout is the index of a problem that belongs to no single object.
const WholeLayout = -1

// maxIssues bounds what the validator reports. An editor cannot show forty
// problems usefully, and a hostile payload should not be able to make the answer
// large.
const maxIssues = 12

// ValidateLayout reports everything wrong with an office, or nothing at all.
//
// ALL THE FAULTS AND NOT THE FIRST, because the editor highlights every offending
// object at once — which is the difference between one fix and five save attempts.
//
// The order is cheapest-first and it matters: the pairwise and grid passes only
// run over rectangles already known to be finite and on the floor, so a payload
// full of NaN can never reach the flood fill.
func ValidateLayout(l Layout) []LayoutIssue {
	var out []LayoutIssue
	add := func(p LayoutProblem, i int) {
		if len(out) < maxIssues {
			out = append(out, LayoutIssue{Problem: p, Index: i})
		}
	}

	if len(l.Solids) > MaxSolids || len(l.Windows) > MaxWindows {
		// Nothing below is worth running on a payload this size, and refusing to
		// walk it is the whole point of the bound.
		return []LayoutIssue{{Problem: ProblemTooMany, Index: WholeLayout}}
	}

	sane := true
	for i, s := range l.Solids {
		if !validKind(s.Kind) {
			add(ProblemBadKind, i)
			sane = false
			continue
		}
		if !finite(s.X) || !finite(s.Y) || !finite(s.W) || !finite(s.H) ||
			s.W < MinSolidSide || s.H < MinSolidSide ||
			s.W > MaxSolidSide || s.H > MaxSolidSide {
			add(ProblemBadSize, i)
			sane = false
			continue
		}
		// EVERY SOLID IS MinGap FROM EVERY WALL. Below ResolverFloor that is a
		// correctness bound — the push-out would put a body outside the room, and
		// the floor clamp has already run — and above it, it is what stops the
		// room having slots in it.
		if wallGap(s.Rect) < MinGap {
			add(ProblemOffFloor, i)
			sane = false
		}
	}
	if !sane {
		// The rectangles are not all trustworthy, so the pairwise and grid passes
		// would be reporting on nonsense.
		return out
	}

	// AND MinGap FROM EACH OTHER, per axis. See sepAxis: the euclidean version of
	// this test passes a diagonal near-miss that the single-pass resolver cannot
	// survive.
	for i := range l.Solids {
		for j := i + 1; j < len(l.Solids); j++ {
			if sepAxis(l.Solids[i].Rect, l.Solids[j].Rect) < MinGap {
				add(ProblemTooClose, j)
			}
		}
	}

	// Every catalogue point has to be somewhere a body can stand. The points are
	// fixed content — the spawns, the bottle spots, the кальян spots — so it is
	// the furniture that has to keep out of their way rather than the other way
	// about.
	for _, at := range FixedPoints() {
		for i, s := range l.Solids {
			if pointGap(s.Rect, at) < SpotClear {
				add(ProblemSpotBlocked, i)
			}
		}
	}

	// AND THE FLOOR IS ONE PLACE.
	//
	// Under the gap rule above this is nearly impossible to violate — the
	// narrowest passage the rules allow is 1.5 m, which is three cells of the
	// navigation grid — so this pass is not what keeps the office playable day to
	// day. It is here because it is the only check that would notice if the gap
	// rule were relaxed or given an exception, and because «the лысый cannot
	// reach that corner» is the one defect in this game that is invisible until
	// somebody has been standing in the corner for a minute. navigate.go exists
	// because of exactly that, measured at ninety seconds — and a player the лысый
	// cannot reach is immortal, which puts a score on a seven-day leaderboard that
	// outlives the office that produced it.
	if !floorIsOnePlace(l.Rects()) {
		add(ProblemSplitFloor, WholeLayout)
	}

	// The glazing, which collides with nothing and can therefore only be wrong
	// about itself.
	for i, w := range l.Windows {
		length := wallLength(w.Wall)
		if !validWall(w.Wall) || !finite(w.At) || !finite(w.Len) ||
			w.Len < WindowMin || w.At < WindowEdge || w.At+w.Len > length-WindowEdge {
			add(ProblemBadWindow, i)
			continue
		}
		for j := i + 1; j < len(l.Windows); j++ {
			o := l.Windows[j]
			if o.Wall != w.Wall || !finite(o.At) || !finite(o.Len) {
				continue
			}
			if w.At < o.At+o.Len+WindowMullion && o.At < w.At+w.Len+WindowMullion {
				add(ProblemBadWindow, j)
			}
		}
	}
	return out
}

func validKind(k Kind) bool {
	for _, want := range Kinds {
		if k == want {
			return true
		}
	}
	return false
}

func validWall(w Wall) bool {
	for _, want := range Walls {
		if w == want {
			return true
		}
	}
	return false
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// floorIsOnePlace reports whether every navigable cell can be reached from every
// other one — flood filled from the лысый's own spawn, because he is the widest
// body and his failure to arrive is the defect that matters.
func floorIsOnePlace(rects []Rect) bool {
	free := navGridFor(rects)
	start, ok := navStartCell(free, Vec2{X: BossSpawnX, Y: BossSpawnY})
	if !ok {
		return false
	}
	seen := make([]bool, len(free))
	seen[start] = true
	queue := []int{start}
	reached := 1
	for head := 0; head < len(queue); head++ {
		cc, cr := queue[head]%navCols, queue[head]/navCols
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nc, nr := cc+d[0], cr+d[1]
			if nc < 0 || nc >= navCols || nr < 0 || nr >= navRows {
				continue
			}
			n := nr*navCols + nc
			if seen[n] || !free[n] {
				continue
			}
			seen[n] = true
			reached++
			queue = append(queue, n)
		}
	}
	total := 0
	for _, f := range free {
		if f {
			total++
		}
	}
	return reached == total
}

// navStartCell is the cell a point falls in, or its nearest free neighbour.
func navStartCell(free []bool, at Vec2) (int, bool) {
	c, r := navCellOf(at)
	if n := r*navCols + c; free[n] {
		return n, true
	}
	return navNearestFree(free, c, r)
}
