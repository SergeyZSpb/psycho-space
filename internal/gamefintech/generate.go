package gamefintech

import (
	"errors"
	"math"
	"math/rand"
)

// WHERE AN OFFICE COMES FROM WHEN NOBODY HAS DRAWN ONE.
//
// The floor is data now (layout.go), which makes «what is standing on it» a
// question with more than one answer: the checked-in floor the game shipped on, a
// drawing somebody dragged into shape by hand, or this — a number, and the office
// that falls out of it. All three produce the same type and are held to the same
// predicate, so nothing downstream can tell which of them it is walking round.
//
// THE SEED IS THE OFFICE, FOR EVER. A seeded math/rand v1 source, exactly as
// «ВАНЯДУМ» generates a заброшка (gamevanyadum/level.go) and for exactly its
// reason: crypto/rand would produce a floor that cannot be reproduced from the
// number that produced it, and reproducing it is the whole point — it is what lets
// an office be stored as one int64, rebuilt identically on every machine and after
// every deploy, and swept by the hundred in a test. The v1 stream for a given seed
// is fixed by the Go 1 compatibility promise, which is what makes «for ever» a
// guarantee rather than a hope, and is why this is not math/rand/v2.
//
// VALIDITY IS ValidateLayout'S ANSWER AND NOBODY ELSE'S. There is no second
// definition of playable in this file. The floor is built one object at a time and
// a candidate is kept only when the WHOLE layout still validates with it standing
// there — which is a strictly stronger thing than checking the object, because
// cutting the floor in two is a property of the arrangement rather than of any one
// desk in it. An empty floor is legal (TestAnEmptyFloorIsLegal), so the worst a
// hostile seed can do is a thin office: never an invalid one, and never a search
// that does not end.
//
// EVERY LOOP HAS A FLOOR, because this runs on the simulation goroutine and A LOOP
// WITH NO FLOOR IS A LOOP THAT CAN HANG A 20 Hz TICK — the rule office.go's spawn
// sampler is written to, applied to a bigger search. There is a fixed budget per
// object and a fixed number of objects, so the cost has a ceiling that does not
// depend on the seed: BenchmarkGenerate measures 0.58 ms for a whole office, and
// the slowest of two thousand seeds took 1.39 ms — under 3 % of the 50 ms a tick
// has to spend.
//
// EVERY NUMBER LANDS ON A QUARTER-METRE LATTICE, sizes and positions alike, and
// that buys two things. The validator's rules are comparisons against 1.5 m and
// 0.5 m; on a lattice of exactly-representable quarters every distance it measures
// is an exact multiple of 0.25, so whether a floor is accepted never turns on a
// rounding error. And it keeps the served numbers short — the layout rides the
// catalogue every client fetches, so `"x":3.25` rather than `"x":3.2500000000004`
// is bytes off that fetch for nothing.

// ErrThinFloor is returned when a seed produced too little furniture to call the
// result an office.
//
// A REFUSAL AND NOT A HALF-BUILT FLOOR, so that a caller has exactly one thing to
// do about it: keep the office it already has. The thinnest floor in a
// thousand-seed sweep carried ten solids against a minimum of eight, so this is a
// guard rather than a state — but it is the honest answer to a search that placed
// nothing, and it is a great deal cheaper than a caller discovering an empty room
// in production.
var ErrThinFloor = errors.New("gamefintech: the seed produced too thin a floor")

// How the search is bounded, and what it is aiming at. Every number below was
// MEASURED over a thousand-seed sweep rather than argued — the sweep is
// TestEveryGeneratedOfficeIsPlayable and the properties around it, and the figures
// quoted here are what it reports.
const (
	// placeStep is the lattice every generated number lands on. A quarter of a
	// metre is exact in binary, half the navigation grid's cell, and finer than
	// anything a player can perceive through a plane that draws a metre as 22 px.
	placeStep = 0.25

	// placeTries is the budget for ONE object. Twenty-four draws is generous on an
	// open floor and hopeless on a full one, which is the shape wanted: an object
	// that lands takes a mean of 4.9 draws, and the 1.9 objects a floor cannot fit
	// spend the whole budget discovering it. The product with the wish list is the
	// ceiling on the whole generation — at most 19 × 24 candidates, however hostile
	// the seed — and it is that product, not this number, that has to stay inside a
	// tick.
	placeTries = 24

	// minSolids is the fewest objects worth calling an office rather than a room,
	// and the line below which Generate refuses rather than returns. Measured: a
	// mean of 14.1 solids and a worst seed of 10, so this sits two under anything
	// the sweep has produced.
	minSolids = 8

	// tailMin and tailMax bound what is drawn after the four guaranteed objects,
	// and they are where the floor's DENSITY is decided.
	//
	// THE THING THEY TRADE AGAINST IS THE EDITOR'S HEADROOM, measured as «is there
	// anywhere left on this floor to put one more 1 × 1 object» — the same
	// measurement that killed a 2.3 m separation rule (see MinGap). At 9..15 the
	// floor holds a mean of 14.1 solids and every one of a thousand seeds still had
	// somewhere to add one. At 12..20 it holds 15.9 and 1 % of floors have nowhere;
	// at 20..30, 18.0 and 6.3 %. The extra two desks are not worth an office nobody
	// can edit, so the density stops where the headroom is still total.
	tailMin = 9
	tailMax = 15

	// aisleMax is the widest aisle an adjacent placement leaves. Added to MinGap,
	// so a neighbour stands between 1.5 m and 2.5 m away — a bank of desks with a
	// walkable lane beside it, rather than two objects that merely both exist.
	aisleMax = 1.0

	// slideFraction is how far along a host's face a neighbour may slide, as a
	// fraction of that face. «ВАНЯДУМ» attaches its rooms with the same ±40 %
	// offset and for the same reason: at zero every neighbour is centred on the one
	// before it and the office comes out a lattice, and at one they stop reading as
	// related to each other at all.
	slideFraction = 0.4

	// adjacentShare is how often a candidate is proposed beside something already
	// standing rather than anywhere at all. It is what makes the result an office —
	// banks of desks with lanes between them — instead of a scatter, and it is not
	// 1.0 because a floor built purely by accretion grows one clump and leaves the
	// rest of the room empty. Measured at 0.6: the four quadrants of the floor hold
	// 3.6, 3.4, 3.6 and 3.5 solids, and the emptiest of the four holds 2.5.
	adjacentShare = 0.6
)

// The glazing. Every number here is one of the validator's own bounds moved up to
// the lattice, which is what makes the panes correct BY CONSTRUCTION rather than
// by rejection: a wall is cut into n cells, one pane is centred in each, and there
// is no draw below that ValidateLayout can refuse.
const (
	// windowInset is how much wall is kept at each end — WindowEdge (0.6), rounded
	// up onto the lattice.
	windowInset = 0.75
	// windowGap is the wall left between two panes — WindowMullion (0.6), likewise.
	// The validator's overlap test is strict, so a gap of exactly the mullion would
	// pass; the extra 0.15 m is so that it passes with room rather than exactly.
	windowGap = 0.75
	// paneMin and paneMax bound one pane. The floor of WindowMin (1.2) is lifted to
	// 1.5, because a 1.2 m pane on a 22 m wall reads as a slot rather than a window.
	paneMin = 1.5
	paneMax = 4.0
	// panesMin and panesMax are how many panes a wall gets — measured across the
	// three walls as a mean of 9.0 panes, between 6 and 12. Three walls at four
	// panes is exactly MaxWindows, the cap the validator refuses to exceed, so
	// panesMax cannot go up without that going up first.
	panesMin = 2
	panesMax = 4
)

// class is what a generated object is: a kind, and the size it is drawn at.
//
// A CLASS RATHER THAN A KIND, because two of these draw the same Kind at very
// different sizes and that difference is most of what stops the office being a
// field of identical desks. A run is a bank long enough to have to be walked
// round; a desk is one workstation.
type class uint8

const (
	classRun class = iota
	classDesk
	classFlower
	classTree
)

// classes is how big each class is drawn, in metres: the long side, the short
// side, and whether it has only one side at all — the short side is not read for
// a square class. Every bound is on the lattice and inside
// MinSolidSide..MaxSolidSide, so a drawn size is never a size the validator would
// refuse.
var classes = [...]struct {
	kind        Kind
	long, short [2]float64
	square      bool
}{
	classRun:    {kind: KindDesk, long: [2]float64{4.0, 6.0}, short: [2]float64{0.75, 1.0}},
	classDesk:   {kind: KindDesk, long: [2]float64{1.5, 3.0}, short: [2]float64{0.75, 1.0}},
	classFlower: {kind: KindFlower, long: [2]float64{0.75, 1.0}, square: true},
	classTree:   {kind: KindTree, long: [2]float64{1.25, 1.75}, square: true},
}

// tailPool is what the floor is filled with after the guaranteed four. THE
// REPEATS ARE THE WEIGHTING — a uniform draw over a slice naming the desk four
// times is a fifty per cent desk, and that is a great deal easier to read and to
// retune than a cumulative probability table. Greenery is a quarter of it because
// an office is furniture with plants in it rather than the other way about;
// measured, a floor comes out with 9.4 desks, 2.4 pots and 2.3 ficuses.
var tailPool = []class{
	classRun, classRun,
	classDesk, classDesk, classDesk, classDesk,
	classFlower, classTree,
}

// lie is which way a desk lies: along X, along Y, or whichever the draw says.
type lie uint8

const (
	lieDrawn lie = iota
	lieAcross
	lieDown
)

// want is one object the generator is going to try to place.
type want struct {
	class class
	lie   lie
}

// Generate builds an office from a seed. The same seed produces the same office
// in every process and after every deploy, which is what lets a floor be stored
// as one number and what lets the tests sweep hundreds of them.
//
// It returns ErrThinFloor, and nothing half-built, when the search placed fewer
// than minSolids objects.
func Generate(seed int64) (Layout, error) {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // office shape, not security: crypto/rand would make a floor unreproducible from its seed, which is the whole point.

	// The glazing first, and never again: it is correct by construction, it
	// collides with nothing, and having it on the layout from the start means every
	// candidate below is validated against the office as it will actually be served.
	l := Layout{Windows: glazing(rng)}

	// Assembled once rather than per candidate. FixedPoints builds a fresh slice on
	// every call — which is right for a validator that runs once, and wasteful for a
	// loop asking the same question several hundred times.
	spots := FixedPoints()

	for _, w := range wishList(rng) {
		for try := 0; try < placeTries; try++ {
			cand := drawSolid(rng, w, l.Solids)
			// The cheap rules first. Most candidates die here, which is what keeps the
			// flood fill off the hot path.
			if !roomFor(l.Solids, spots, cand.Rect) {
				continue
			}
			// And then the predicate, over the whole floor, which is the only thing
			// that decides. Appended and popped rather than copied: a rejected
			// candidate leaves the slice exactly as it found it.
			//
			// Measured over a thousand offices: 14.1 of these per office — one per
			// object that landed — and not one candidate that had passed roomFor was
			// then refused here. That is the connectivity rule being the guard
			// validate.go says it is rather than a constraint that bites, and it is
			// why the flood fill costs nothing: it runs once per accepted object
			// instead of once per draw. It stays the acceptance test regardless,
			// because the day it does bite is the day an office has a pocket in it.
			l.Solids = append(l.Solids, cand)
			if len(ValidateLayout(l)) > 0 {
				l.Solids = l.Solids[:len(l.Solids)-1]
				continue
			}
			break
		}
		// And nothing at all when an object found nowhere to stand. A wish list is a
		// wish list: the floor fills up, the late objects miss, and that is the room
		// being full rather than the generator failing.
	}

	if len(l.Solids) < minSolids {
		return Layout{}, ErrThinFloor
	}
	return l.WithID(), nil
}

// wishList is what the generator is going to try to place, in order.
//
// THE FIRST FOUR ARE GUARANTEES, AND THEY ARE FIRST BECAUSE OF IT. They are asked
// for while the floor is still empty, which is the one moment each of them is
// certain to fit — and each is a property the office would otherwise be able to
// lose to a die. Two runs, one across and one down: an office of nothing but short
// desks has no shape to run round, and a floor whose long things all lie the same
// way has one kind of chase in it. Then the ficus and the pot, which are an eighth
// of the tail pool between them — leaving them to the draw alone would produce a
// floor with no greenery on it about one time in twenty, silently, because nothing
// about an office says what it failed to generate. «ВАНЯДУМ» learned that with its
// beer (level.go, placePickups) and this is the same lesson applied to a plant.
func wishList(rng *rand.Rand) []want {
	out := make([]want, 0, 4+tailMax)
	out = append(out,
		want{class: classRun, lie: lieAcross},
		want{class: classRun, lie: lieDown},
		want{class: classTree},
		want{class: classFlower},
	)
	for n := tailMin + rng.Intn(tailMax-tailMin+1); n > 0; n-- {
		out = append(out, want{class: tailPool[rng.Intn(len(tailPool))]})
	}
	return out
}

// drawSolid proposes one object: a size, a way round, and somewhere to stand. It
// PROPOSES rather than places — whether the office can have it is roomFor's
// question and then ValidateLayout's, and this function knows nothing about
// either.
func drawSolid(rng *rand.Rand, w want, placed []Solid) Solid {
	c := classes[w.class]
	long := drawSide(rng, c.long)
	short := long
	if !c.square {
		short = drawSide(rng, c.short)
	}
	width, height := long, short
	if w.lie == lieDown || (w.lie == lieDrawn && rng.Intn(2) == 0) {
		width, height = short, long
	}

	var x, y float64
	if len(placed) > 0 && rng.Float64() < adjacentShare {
		x, y = beside(rng, placed[rng.Intn(len(placed))].Rect, width, height)
	} else {
		x, y = anywhere(rng, width, height)
	}
	// Clamped rather than redrawn: a candidate nudged back onto the floor is
	// usually still a candidate worth testing, and on the occasions the clamp has
	// shoved it into its own host the acceptance test refuses it like any other
	// miss. Clamping cannot take a number off the lattice — both ends of the clamp
	// are on it.
	return Solid{
		Rect: Rect{X: onFloor(x, width, OfficeW), Y: onFloor(y, height, OfficeH), W: width, H: height},
		Kind: c.kind,
	}
}

// beside proposes a spot against one face of something already standing, offset
// along that face — which is what produces banks of desks with lanes between them
// rather than a scatter of unrelated objects.
func beside(rng *rand.Rand, host Rect, w, h float64) (float64, float64) {
	aisle := MinGap + snapDown(rng.Float64()*aisleMax)
	slide := func(span float64) float64 { return snapDown((rng.Float64()*2 - 1) * span * slideFraction) }
	switch rng.Intn(4) {
	case 0:
		return host.X + host.W + aisle, host.Y + slide(host.H)
	case 1:
		return host.X - aisle - w, host.Y + slide(host.H)
	case 2:
		return host.X + slide(host.W), host.Y + host.H + aisle
	default:
		return host.X + slide(host.W), host.Y - aisle - h
	}
}

// anywhere proposes a spot drawn flat over everywhere the object could legally
// stand: the floor inset by MinGap, which is the band the validator's wall rule
// leaves.
func anywhere(rng *rand.Rand, w, h float64) (float64, float64) {
	return MinGap + snapDown(rng.Float64()*(OfficeW-2*MinGap-w)),
		MinGap + snapDown(rng.Float64()*(OfficeH-2*MinGap-h))
}

// roomFor is the CHEAP HALF of ValidateLayout, asked of one candidate before the
// expensive half is asked of the whole floor.
//
// NOT A SECOND DEFINITION OF PLAYABLE, and that distinction is the whole of its
// justification. Every rule in it is one of ValidateLayout's, computed by
// ValidateLayout's own helpers against ValidateLayout's own constants, and a
// candidate that passes it is still put to ValidateLayout before it is kept. All
// it decides is whether the expensive question is worth asking — which matters
// because asking costs a 1408-cell navigation grid and a flood fill over it, while
// most candidates are refused by a subtraction.
func roomFor(placed []Solid, spots []Vec2, cand Rect) bool {
	if wallGap(cand) < MinGap {
		return false
	}
	for _, s := range placed {
		if sepAxis(s.Rect, cand) < MinGap {
			return false
		}
	}
	for _, at := range spots {
		if pointGap(cand, at) < SpotClear {
			return false
		}
	}
	return true
}

// glazing cuts every glazed wall into panes.
//
// BY CONSTRUCTION AND NOT BY REJECTION: n panes of one length with a mullion
// between each pair, the run centred on the wall, every number on the lattice and
// every bound already inside the validator's. There is nothing here for
// ValidateLayout to refuse, which is why no window ever appears in the placement
// loop above.
func glazing(rng *rand.Rand) []Window {
	out := make([]Window, 0, len(Walls)*panesMax)
	for _, wall := range Walls {
		usable := wallLength(wall) - 2*windowInset
		n := panesMin + rng.Intn(panesMax-panesMin+1)
		// The widest pane that n of them can be once the mullions are taken out. The
		// draw is CAPPED by it rather than rejected against it, so a wall that wanted
		// four four-metre panes gets four of whatever does fit rather than a redraw.
		widest := snapDown((usable - float64(n-1)*windowGap) / float64(n))
		pane := math.Min(drawSide(rng, [2]float64{paneMin, paneMax}), widest)
		run := float64(n)*pane + float64(n-1)*windowGap
		at := windowInset + snapDown((usable-run)/2)
		for i := 0; i < n; i++ {
			out = append(out, Window{Wall: wall, At: at + float64(i)*(pane+windowGap), Len: pane})
		}
	}
	return out
}

// drawSide draws one side of an object, on the lattice, inside the range given.
func drawSide(rng *rand.Rand, span [2]float64) float64 {
	return span[0] + snapDown(rng.Float64()*(span[1]-span[0]))
}

// onFloor clamps a coordinate so that an object of this size stands inside the
// band MinGap leaves it.
func onFloor(v, size, extent float64) float64 {
	return math.Min(math.Max(v, MinGap), extent-MinGap-size)
}

// snapDown puts a number on the lattice, downwards. Downwards rather than to the
// nearest, so that a value drawn inside a range stays inside it: rounding to the
// nearest would let a draw of «up to 2.0» come back as 2.25.
func snapDown(v float64) float64 {
	return math.Floor(v/placeStep) * placeStep
}
