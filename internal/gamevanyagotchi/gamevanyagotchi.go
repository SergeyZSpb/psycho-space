// Package gamevanyagotchi is «Ванягоччи», the second game: a shared plane that
// several players stand on at once, backed by the realtime transport.
//
// It is a self-contained module. It shares no table and no service code with any
// other game, and it owns its own wire types rather than adding them to
// internal/realtime — the hub is game-agnostic and this game publishes *through*
// it. Deleting this game must be deleting this package, its migration, its routes
// and its views, and nothing else. See docs/ARCHITECTURE.md ADR-028 and ADR-030.
//
// NO MESSAGE HERE MAY EVER REACH THE LLM. The judge is the only paid dependency
// in the application, and its cost is bounded by human turn-taking behind a
// 5/min per-IP limit on one HTTP endpoint in the *other* game. A broadcast or a
// tick multiplies one player's action into many calls, which is unbounded in a
// way that limit cannot see. This game makes no model call on any path, and if
// one is ever wanted it goes through that HTTP endpoint. Written here because it
// is the sort of rule that erodes silently (ADR-016).
//
// What exists so far is the wire protocol only: presence on a shared plane, and
// a move. There is no pet, no database table and no decay yet — Phase 1 proves
// the transport end to end before anything durable rides on it.
package gamevanyagotchi

import "math"

// Point is a position on the plane in normalised coordinates: 0..1 on each axis,
// origin top-left. Normalised rather than pixels because the client maps them in
// CSS (`--x`/`--y` against the container's own size), so the server never needs
// to know a viewport and a phone rotating changes nothing on the wire.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// spawn is where a connection is drawn before it has ever moved: the middle of
// the plane. Phase 2 replaces this with the current location's entry point from
// the content catalogue.
var spawn = Point{X: 0.5, Y: 0.5}

// clampUnit forces v into 0..1 and reports whether it was a usable number at
// all. A NaN or an infinity is rejected rather than clamped: they are not
// out-of-range positions, they are a client that is broken or probing, and
// silently mapping them onto an edge of the plane would hide both.
func clampUnit(v float64) (float64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return math.Min(1, math.Max(0, v)), true
}
