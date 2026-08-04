package gamefintech

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
)

// THE OFFICE IS A LEVEL NOW.
//
// It used to be a constant — eight identical desks in two symmetric columns,
// written out in content.go, with the whole design resting on «this game's
// simplification against «ВАНЯДУМ» is that the level never changes». It changes
// now: the floor is data, held by the office that is standing on it, and the same
// data can be generated, stored, served and edited by hand.
//
// WHAT THE SIMULATION SEES IS []Rect AND NOTHING ELSE. A Solid carries a kind so
// the browser can draw a flowerpot differently from a desk, and that kind must
// never reach the collision code — because the browser runs its own copy of that
// code (web/src/lib/fintechStep.ts) and the two must agree exactly. Here that is a
// TYPE rather than a rule to remember: Plan hands out []Rect, Step takes []Rect,
// and there is no path by which a kind could change what a body collides with.
//
// VALIDITY IS A PREDICATE, NOT A CONVENTION — see validate.go. The generator uses
// it to accept a candidate and the admin save path uses it to accept a drawing, so
// a hand-made office is held to exactly the rules a generated one is.

// Kind is what a solid is drawn as. A LOOK, NEVER A RULE: every solid collides
// identically.
type Kind string

const (
	// KindDesk is what the office has always had: a workstation, in varied
	// lengths and both orientations.
	KindDesk Kind = "desk"
	// KindFlower is a pot of something green. It exists to be walked round.
	KindFlower Kind = "flower"
	// KindTree is the artificial ficus every office of this kind owns. A bigger
	// flower, and nothing else.
	KindTree Kind = "tree"
)

// Kinds is every kind that may stand on the floor, in the order a palette offers
// them. ValidateLayout rejects anything else, so this is the whole taxonomy.
var Kinds = []Kind{KindDesk, KindFlower, KindTree}

// Solid is one thing on the floor: a rectangle, and what it looks like.
type Solid struct {
	Rect
	// Kind is `kind` on the wire and never `k` — the snapshot already spends that
	// key twice (the tick, and an input's seen-tick), and a served field colliding
	// with a frame field is the kind of thing a substring test trips over.
	Kind Kind `json:"kind"`
}

// Wall names one of the three walls that can be glazed.
type Wall string

const (
	// WallTop is the far wall, the one the client draws as a band above the room.
	WallTop Wall = "top"
	// WallLeft and WallRight are the side bands.
	WallLeft  Wall = "left"
	WallRight Wall = "right"
)

// Walls is every wall that may carry glazing. The bottom edge is missing on
// purpose: it is where the лысый spawns and where the client's controls sit, and
// there is no band there to draw glass in.
var Walls = []Wall{WallTop, WallLeft, WallRight}

// Window is a pane of glazing on a wall, and it is not on the floor at all.
//
// A SPAN RATHER THAN A RECTANGLE, because that is honestly what it is: a stretch
// of one wall, measured along it. It has no thickness, no collision and no
// presence in the simulation — which is exactly why it cannot desynchronise
// anything. `At` is the distance along the wall from its start (left to right on
// the top wall, top to bottom on the sides) and `Len` is how much of it is glass.
type Window struct {
	Wall Wall    `json:"wall"`
	At   float64 `json:"at"`
	Len  float64 `json:"len"`
}

// Layout is one office: what is standing on the floor, what is glazed, and the
// identity of the two together.
type Layout struct {
	// ID is a content hash — the first sixteen hex digits of a SHA-256 over the
	// canonical JSON of the solids and the windows.
	//
	// A HASH AND NOT A ROW ID, which is the difference between a client that
	// refetches when the office changed and one that refetches whenever the
	// process restarted. Two identical floors have the same id on every machine
	// and after every deploy, so «is my cached geometry still the office?» is
	// answered by the geometry itself rather than by who last wrote a row.
	ID      string   `json:"id"`
	Solids  []Solid  `json:"solids"`
	Windows []Window `json:"windows"`
}

// Rects is the collision list: every solid's rectangle, in layout order.
//
// THE ORDER IS THE CONTRACT. Step pushes a body out of each rectangle in turn,
// once, in this order, and so does the TypeScript port — so the same rectangles
// in a different order are a different office to anybody standing between two of
// them.
func (l Layout) Rects() []Rect {
	out := make([]Rect, len(l.Solids))
	for i, s := range l.Solids {
		out[i] = s.Rect
	}
	return out
}

// WithID returns the layout with its content hash filled in. Called by whoever
// produces a layout — the generator, the loader, the admin save path — so that an
// id is never something a caller can forget to keep in step with the geometry.
func (l Layout) WithID() Layout {
	// Over the geometry alone, never over the whole value: hashing a struct that
	// contains its own hash is a fixed point nobody wants to reason about, and the
	// id has to be a function of the floor rather than of what the floor was
	// previously called.
	body, err := json.Marshal(struct {
		Solids  []Solid  `json:"solids"`
		Windows []Window `json:"windows"`
	}{l.Solids, l.Windows})
	if err != nil {
		// Unreachable: the fields are plain floats and strings. An id of «unknown»
		// is a layout the client will refetch rather than a panic on a 20 Hz loop.
		return l
	}
	sum := sha256.Sum256(body)
	l.ID = hex.EncodeToString(sum[:8])
	return l
}

// Plan is a layout as the SIMULATION uses it: the collision list and the
// navigation grid, both derived once, when the layout is installed.
//
// THE DERIVATION IS WHY THIS TYPE EXISTS. The grid used to be a package-level
// sync.OnceValue, which was correct while the office was a constant and is a bug
// the moment it is not — a rebuilt office would have gone on pathing round the
// furniture of the one before it, which presents as the лысый grinding against a
// desk that is not there. Rebuilding per tick is not the answer either: it is 1408
// cells against a 20 Hz loop. So it is built once per layout, by whoever installs
// it, and every pursuer reads it.
//
// IMMUTABLE ONCE BUILT. An office swaps its whole plan for another rather than
// editing the one it holds, which makes «the layout changed» a single pointer
// write under the office's own mutex instead of a half-updated world.
type Plan struct {
	layout Layout
	rects  []Rect
	nav    []bool
}

// NewPlan installs a layout: the collision list in layout order, and the grid.
func NewPlan(l Layout) *Plan {
	rects := l.Rects()
	return &Plan{layout: l, rects: rects, nav: navGridFor(rects)}
}

// Layout is the plan's own layout, for whoever has to serve or store it.
func (p *Plan) Layout() Layout { return p.layout }

// Rects is the collision list, in layout order.
func (p *Plan) Rects() []Rect { return p.rects }

// The rules a layout has to satisfy. Every one is derived from something the
// simulation already does rather than picked, which is what makes them arguable
// with a reader rather than magic numbers.
const (
	// Clearance is the widest body the resolver has to push out of furniture —
	// the лысый's, since he is wider than you. TestTheBaldManIsStillTheWidestBody
	// pins that it is still the maximum of the two.
	Clearance = BossRadius

	// ResolverFloor is the HARD correctness bound, and it is why a slot in this
	// office can never be narrow.
	//
	// Step clamps you to the floor and then pushes you out of each rectangle
	// ONCE, in order. Two solids closer than two body widths can therefore hand
	// a body from one into the other and leave it there; a solid closer than that
	// to a wall can push a body clean out of the room, because the clamp has
	// already run and nothing puts it back. Lifting this means an iterative
	// resolver — a function that exists twice, in two languages, pinned by 232 kB
	// of golden vectors — so the bound is bought with a layout rule instead.
	ResolverFloor = 2 * Clearance

	// MinGap is the PLAYABILITY separation, and it is the number to retune when
	// the office feels wrong. Every solid keeps this much clear floor from every
	// other solid and from every wall, measured per axis (see sepAxis).
	//
	// 1.5 m leaves 0.7 m of daylight once the widest body is subtracted — enough
	// to steer through at a walk, tight enough that the floor can be dense. It
	// was 2.3 m for one afternoon (the widest body plus the 1.5 m of daylight the
	// old symmetric aisles were designed against) and that was measured: a mean
	// of 7 solids, no greenery at all on 46 % of floors, and no legal position
	// left to add a single 1 × 1 object on 59 % of them — which makes an editor
	// that cannot place anything and an office with nothing in it.
	MinGap = 1.5

	// SpotClear is how much room a fixed catalogue point keeps from furniture —
	// both spawns, Claude's, the colleagues', and every bottle and кальян spot.
	//
	// A PLAYER'S RADIUS AND A LITTLE, not the widest body's: these are places a
	// PLAYER stands, and the лысый does not have to be able to stand on a bottle.
	// The existing catalogue is what sets the ceiling here — the bottle spot at
	// (2.2, 15.5) sits 0.6 m from a desk in the office this game shipped with —
	// so anything above 0.6 would declare the shipped office illegal.
	SpotClear = PlayerRadius + 0.15

	// MinSolidSide and MaxSolidSide bound one object. The floor is under a
	// rectangle this small, and nothing may be long enough to span the room: the
	// placement band is OfficeW - 2*MinGap = 13 m wide, so 6 m always leaves a way
	// round both ends.
	MinSolidSide = 0.6
	MaxSolidSide = 6.0

	// MaxSolids and MaxWindows bound what a generator produces and what the admin
	// save path accepts. The floor cannot hold anything like this many at MinGap;
	// the numbers exist so that a malformed or hostile payload cannot make the
	// validator's pairwise pass expensive.
	MaxSolids  = 40
	MaxWindows = 12
)

// Window geometry, in metres along a wall.
const (
	// WindowMin is the shortest pane worth drawing, WindowEdge is how much wall
	// is kept at each end, and WindowMullion is the wall between two panes.
	WindowMin     = 1.2
	WindowEdge    = 0.6
	WindowMullion = 0.6
)

// wallLength is how long a wall is, in metres.
func wallLength(w Wall) float64 {
	if w == WallTop {
		return OfficeW
	}
	return OfficeH
}

// sepAxis is the separation between two rectangles, PER AXIS.
//
// NOT THE EUCLIDEAN DISTANCE, and the difference is a defect rather than a
// refinement. Two rectangles expanded by r are disjoint if and only if they are
// more than 2r apart along X or more than 2r apart along Y — so a pair offset
// diagonally by (0.6, 0.6) is 0.85 m apart as the crow flies and yet their
// expanded boxes overlap, which is exactly the state the resolver cannot handle.
// The office shipped with a euclidean version of this check in content_test.go; it
// never bit because the desks were five metres apart.
func sepAxis(a, b Rect) float64 {
	dx := math.Max(0, math.Max(b.X-(a.X+a.W), a.X-(b.X+b.W)))
	dy := math.Max(0, math.Max(b.Y-(a.Y+a.H), a.Y-(b.Y+b.H)))
	return math.Max(dx, dy)
}

// wallGap is how far a rectangle is from the nearest wall.
func wallGap(r Rect) float64 {
	return math.Min(
		math.Min(r.X, r.Y),
		math.Min(OfficeW-(r.X+r.W), OfficeH-(r.Y+r.H)),
	)
}

// pointGap is how far a point is from a rectangle, zero inside it.
func pointGap(r Rect, at Vec2) float64 {
	dx := math.Max(0, math.Max(r.X-at.X, at.X-(r.X+r.W)))
	dy := math.Max(0, math.Max(r.Y-at.Y, at.Y-(r.Y+r.H)))
	return math.Hypot(dx, dy)
}

// FixedPoints is every point in the catalogue that furniture must keep off: both
// spawns, Claude's, the two colleagues', and every bottle and кальян spot.
//
// Assembled per call rather than kept as a package var, because a slice built out
// of four others is a thing that can be mutated by accident, and this is read
// once per validation rather than per tick.
func FixedPoints() []Vec2 {
	out := make([]Vec2, 0, 5+len(BottleSpots)+len(HookahSpots))
	out = append(out,
		Vec2{X: PlayerSpawnX, Y: PlayerSpawnY},
		Vec2{X: BossSpawnX, Y: BossSpawnY},
		Vec2{X: ChaserSpawnX, Y: ChaserSpawnY},
	)
	for _, n := range NPCCast {
		out = append(out, n.Spawn)
	}
	out = append(out, BottleSpots...)
	out = append(out, HookahSpots...)
	return out
}
