package gamefintech

import (
	"math"
	"math/rand"
	"testing"
)

// The floor, as data. These are the tests that used to be about a constant and
// are now about the predicate that constant satisfied — which is a strictly
// better thing to assert, because it holds of every office this game will ever
// accept rather than of the one somebody typed out.

func TestTheStartingLayoutIsALegalOffice(t *testing.T) {
	// The floor a fresh database opens with. If this ever fails, the game boots
	// onto geometry the resolver has no answer for.
	if issues := ValidateLayout(StartingLayout); len(issues) > 0 {
		t.Fatalf("the starting office is not playable: %+v", issues)
	}
	// And it has one of everything the client draws, so a browser opening the
	// game on a fresh database exercises every kind rather than only the desks.
	seen := map[Kind]bool{}
	for _, s := range StartingLayout.Solids {
		seen[s.Kind] = true
	}
	for _, k := range Kinds {
		if !seen[k] {
			t.Fatalf("the starting office has no %q in it", k)
		}
	}
	if len(StartingLayout.Windows) == 0 {
		t.Fatal("the starting office has no windows, so nothing exercises the wall band")
	}
}

func TestTheBaldManIsStillTheWidestBody(t *testing.T) {
	// Clearance is defined as HIS radius, and every separation rule is derived
	// from it. The day the player is the wider of the two, every one of those
	// rules is quietly measuring the wrong body.
	if Clearance != math.Max(PlayerRadius, BossRadius) {
		t.Fatalf("Clearance is %v, the widest body is %v", Clearance, math.Max(PlayerRadius, BossRadius))
	}
	if ChaserRadius > Clearance {
		t.Fatalf("Claude is %v wide against a clearance of %v — he is pathing on a grid built for somebody thinner",
			ChaserRadius, Clearance)
	}
}

func TestAnEmptyFloorIsLegal(t *testing.T) {
	// It is the base case the generator builds from, so if it were illegal the
	// generator could never place a first object.
	if issues := ValidateLayout(Layout{}); len(issues) > 0 {
		t.Fatalf("an empty room is not playable: %+v", issues)
	}
}

func TestValidateNamesWhatIsWrongAndWhere(t *testing.T) {
	// One layout per problem, each built to trip exactly one rule, and each
	// asserting the INDEX as well as the code — the admin editor highlights what
	// the index names, so a right code against the wrong object is a red square
	// drawn on an innocent desk.
	desk := func(x, y, w, h float64) Solid {
		return Solid{Rect: Rect{X: x, Y: y, W: w, H: h}, Kind: KindDesk}
	}
	for _, tc := range []struct {
		name  string
		l     Layout
		want  LayoutProblem
		index int
	}{
		{
			name:  "a kind nothing can draw",
			l:     Layout{Solids: []Solid{{Rect: Rect{X: 6, Y: 6, W: 2, H: 1}, Kind: "aquarium"}}},
			want:  ProblemBadKind,
			index: 0,
		},
		{
			name:  "smaller than anything may be",
			l:     Layout{Solids: []Solid{desk(6, 6, 0.2, 1)}},
			want:  ProblemBadSize,
			index: 0,
		},
		{
			name:  "long enough to span the room",
			l:     Layout{Solids: []Solid{desk(2, 6, 12, 1)}},
			want:  ProblemBadSize,
			index: 0,
		},
		{
			name:  "not a number at all",
			l:     Layout{Solids: []Solid{desk(math.NaN(), 6, 2, 1)}},
			want:  ProblemBadSize,
			index: 0,
		},
		{
			name:  "against the wall",
			l:     Layout{Solids: []Solid{desk(0.5, 6, 2, 1)}},
			want:  ProblemOffFloor,
			index: 0,
		},
		{
			// THE DIAGONAL NEAR-MISS, and the reason separation is measured per
			// axis. These two are 0.85 m apart as the crow flies — comfortably
			// past ResolverFloor — and their expanded boxes still overlap, which
			// is the one arrangement the single-pass resolver cannot survive.
			name:  "diagonally too close, which a euclidean rule would pass",
			l:     Layout{Solids: []Solid{desk(6, 6, 1, 1), desk(7.6, 7.6, 1, 1)}},
			want:  ProblemTooClose,
			index: 1,
		},
		{
			name:  "standing on a bottle",
			l:     Layout{Solids: []Solid{desk(BottleSpots[0].X-0.5, BottleSpots[0].Y-0.5, 1, 1)}},
			want:  ProblemSpotBlocked,
			index: 0,
		},
		{
			name:  "glazing hanging off the end of the wall",
			l:     Layout{Windows: []Window{{Wall: WallTop, At: OfficeW - 1, Len: 3}}},
			want:  ProblemBadWindow,
			index: 0,
		},
		{
			name:  "glazing on a wall that is not glazed",
			l:     Layout{Windows: []Window{{Wall: "bottom", At: 4, Len: 3}}},
			want:  ProblemBadWindow,
			index: 0,
		},
		{
			name: "two panes in the same stretch of wall",
			l: Layout{Windows: []Window{
				{Wall: WallLeft, At: 4, Len: 4},
				{Wall: WallLeft, At: 6, Len: 4},
			}},
			want:  ProblemBadWindow,
			index: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateLayout(tc.l)
			if len(issues) == 0 {
				t.Fatalf("no problem reported, wanted %q at %d", tc.want, tc.index)
			}
			found := false
			for _, is := range issues {
				if is.Problem == tc.want && is.Index == tc.index {
					found = true
				}
			}
			if !found {
				t.Fatalf("wanted %q at %d, got %+v", tc.want, tc.index, issues)
			}
		})
	}
}

func TestAFloorWithAPocketInItIsRefused(t *testing.T) {
	// A room the лысый cannot walk the whole of is a room with somewhere to be
	// immortal in — and the score that comes out of it ranks on a seven-day board,
	// so it outlives the office that produced it by a week.
	//
	// Sealing a corner takes solids closer together than MinGap, which is refused
	// on its own — so this walls off the corner with the tightest legal spacing and
	// checks the connectivity pass is what catches it. If MinGap alone were enough,
	// this would report only `too_close` and the pass would be untested.
	var solids []Solid
	for i := 0; i < 3; i++ {
		solids = append(solids, Solid{
			Rect: Rect{X: 2.0 + float64(i)*3.0, Y: 8.0, W: 1.0, H: 1.0},
			Kind: KindDesk,
		})
	}
	l := Layout{Solids: solids}
	// It is legal as it stands — a row of solids with two metres between them, in
	// a stretch of floor with no catalogue point on it — which is what makes this a
	// test of the flood fill rather than of the spacing rule.
	if issues := ValidateLayout(l); len(issues) > 0 {
		t.Fatalf("the fixture itself is illegal, so this proves nothing: %+v", issues)
	}
	if !floorIsOnePlace(l.Rects()) {
		t.Fatal("three solids in a row cut the floor in two")
	}
	// And a genuinely sealed corner is caught. Built by hand rather than through
	// ValidateLayout, because a wall tight enough to seal is a wall too tight to
	// be legal — the two rules overlap, and this is the one under test.
	sealed := []Rect{
		{X: 0.0, Y: 4.0, W: 6.0, H: 1.0},
		{X: 5.0, Y: 0.0, W: 1.0, H: 5.0},
	}
	if floorIsOnePlace(sealed) {
		t.Fatal("a corner walled off from the rest of the room counts as one place")
	}
}

func TestTheNavigationGridFollowsTheFloorItWasBuiltFor(t *testing.T) {
	// The grid used to be a process-wide constant, which is exactly wrong once the
	// floor can change: an office rebuilt underneath it would path around
	// furniture that is no longer there. Two different floors, two different grids.
	a := navGridFor(nil)
	b := navGridFor([]Rect{{X: 6, Y: 6, W: 4, H: 4}})
	freeA, freeB := 0, 0
	for i := range a {
		if a[i] {
			freeA++
		}
		if b[i] {
			freeB++
		}
	}
	if freeA <= freeB {
		t.Fatalf("an empty floor has %d free cells and one with a desk in it has %d", freeA, freeB)
	}
	// And the cell under the desk is blocked in one and not in the other, which is
	// the claim the counts only imply.
	c, r := navCellOf(Vec2{X: 8, Y: 8})
	if !a[r*navCols+c] || b[r*navCols+c] {
		t.Fatal("the grid does not describe the floor it was built from")
	}
}

func TestTheSinglePassResolverNeverLeavesABodyInsideFurniture(t *testing.T) {
	// THE WHOLE POINT OF ResolverFloor, driven rather than argued. Step clamps and
	// then pushes out ONCE per solid; if two solids were closer than two body
	// widths, or one were closer than that to a wall, a push could hand a body from
	// one into another or out of the room — and the office would look fine until
	// somebody walked into that exact corner.
	//
	// Deterministic despite the draws: a fixed seed, because a flake here would be
	// a geometry bug nobody could reproduce.
	rng := rand.New(rand.NewSource(20260804)) //nolint:gosec // a fixture, not a secret
	for _, floor := range []struct {
		name  string
		rects []Rect
	}{
		{"the starting office", StartingLayout.Rects()},
		{"the golden torture fixture", goldenRects},
	} {
		t.Run(floor.name, func(t *testing.T) {
			for i := 0; i < 400; i++ {
				p := Player{
					Pos:   Vec2{X: rng.Float64() * OfficeW, Y: rng.Float64() * OfficeH},
					Alive: true,
				}
				p = Step(floor.rects, p, Sanitise(Command{
					Dt: rng.Float64() * MaxStepSeconds,
					MX: rng.Float64()*2 - 1,
					MY: rng.Float64()*2 - 1,
				}))
				if p.Pos.X < PlayerRadius-1e-9 || p.Pos.X > OfficeW-PlayerRadius+1e-9 ||
					p.Pos.Y < PlayerRadius-1e-9 || p.Pos.Y > OfficeH-PlayerRadius+1e-9 {
					t.Fatalf("a push-out put a body outside the room at %+v", p.Pos)
				}
				for j, d := range floor.rects {
					if insideRect(d, p.Pos, PlayerRadius) {
						t.Fatalf("a body ended a step inside solid %d (%+v) at %+v", j, d, p.Pos)
					}
				}
			}
		})
	}
}

func TestTheLayoutIdIsTheGeometryAndNothingElse(t *testing.T) {
	// The client refetches its catalogue when this changes, so it has to change
	// when the floor does and NOT when anything else does — a per-process id would
	// make every deploy a refetch for every open tab.
	a := StartingLayout.WithID()
	b := StartingLayout.WithID()
	if a.ID == "" || a.ID != b.ID {
		t.Fatalf("the same floor hashed to %q and %q", a.ID, b.ID)
	}
	moved := StartingLayout
	moved.Solids = append([]Solid(nil), moved.Solids...)
	moved.Solids[0].X += 0.5
	if moved.WithID().ID == a.ID {
		t.Fatal("moving a desk did not change the office id")
	}
	glazed := StartingLayout
	glazed.Windows = append(append([]Window(nil), glazed.Windows...), Window{Wall: WallTop, At: 5, Len: 1.2})
	if glazed.WithID().ID == a.ID {
		t.Fatal("adding a window did not change the office id")
	}
}

func TestAPlanHandsOutRectsAndNeverKinds(t *testing.T) {
	// The kind is a look and never a rule: the browser runs its own copy of the
	// resolver, and the moment a kind could change what a body collides with, the
	// two would have to agree about the taxonomy as well as the arithmetic. This
	// is that guarantee as a type — Rects is []Rect, so there is nothing to agree
	// about.
	p := NewPlan(StartingLayout)
	rects := p.Rects()
	if len(rects) != len(StartingLayout.Solids) {
		t.Fatalf("the plan carries %d rectangles for %d solids", len(rects), len(StartingLayout.Solids))
	}
	for i, r := range rects {
		if r != StartingLayout.Solids[i].Rect {
			t.Fatalf("rectangle %d is %+v, the solid is %+v", i, r, StartingLayout.Solids[i].Rect)
		}
	}
}
