package gamevanyadum

import (
	"math"
	"testing"
)

// The ray, tested without a world: shoot takes geometry and bodies and returns
// the one it landed on, so every rule it has can be stated as one call and one
// assertion.
//
// The world's half — who is a legal target, where he was when the shooter saw
// him, and what a hit costs him — is in world_test.go. This file is only about
// where the shot goes.

// The fixtures are sim_test.go's, deliberately: `room` and `twoRooms` are the
// same hand-built geometry the movement tests are written against, and a hit
// test that invented its own would be proving things about a building the
// simulation has never walked in. `eastward` — the yaw that looks along +X,
// since yaw zero looks along +Y — comes from world_test.go for the same reason.
// One package, one set of rooms.

// openRoom is a 20×20 room with nothing in it but its own four walls.
func openRoom() *Level { return room(0, 0, 20, 20, 0) }

// at is one target standing on the ground floor, which is every fixture below
// except the two about height.
func at(slot int, x, y float64) body {
	return body{at: ref{N: slot}, pos: Vec2{X: x, Y: y}}
}

// fire aims from (2,5) due east at eye height, which is where every shooter in
// this file stands unless the case is about standing somewhere else.
func fire(l *Level, targets ...body) (ref, bool) {
	return shoot(l, Vec2{X: 2, Y: 5}, EyeHeight, eastward, 0, targets)
}

func TestTheShotLandsOnWhoeverIsInFrontOfIt(t *testing.T) {
	hit, ok := fire(openRoom(), at(3, 6, 5))
	if !ok || hit != (ref{N: 3}) {
		t.Fatalf("a man four metres in front was answered with (%+v, %v)", hit, ok)
	}
}

func TestAShotBesideSomebodyIsAMiss(t *testing.T) {
	// A metre off the line, which on a 0.7 m body is a clean miss. Nothing in
	// this game bends a bullet towards a target, and nothing should: the whole
	// contract of a hitscan is that the shot went where the crosshair was.
	if hit, ok := fire(openRoom(), at(3, 6, 6)); ok {
		t.Fatalf("a shot a metre wide of him hit %+v", hit)
	}
}

func TestTheEdgeOfABodyIsStillABody(t *testing.T) {
	// The disc is PlayerRadius, the SAME disc the collision resolver pushes out
	// of walls — so what you can be hit through is exactly what you can walk
	// through. Both sides of the boundary, a centimetre either way.
	if _, ok := fire(openRoom(), at(1, 6, 5+PlayerRadius-0.01)); !ok {
		t.Fatal("a shot inside his own radius missed him")
	}
	if _, ok := fire(openRoom(), at(1, 6, 5+PlayerRadius+0.01)); ok {
		t.Fatal("a shot outside his radius hit him anyway")
	}
}

func TestConcreteStopsTheShot(t *testing.T) {
	// The order that matters most: the wall is resolved BEFORE the bodies, so a
	// man standing behind one is not a target however well he is lined up. He is
	// two rooms' worth of distance away along Y = 8, which the doorway does not
	// reach.
	l := twoRooms(0)
	if hit, ok := shoot(l, Vec2{X: 2, Y: 8}, EyeHeight, eastward, 0, []body{at(1, 15, 8)}); ok {
		t.Fatalf("a shot through a solid wall hit %+v", hit)
	}
	// And the same shot with the wall taken out of the picture does land, so the
	// miss above is the concrete rather than the arithmetic.
	if _, ok := shoot(openRoom(), Vec2{X: 2, Y: 8}, EyeHeight, eastward, 0, []body{at(1, 15, 8)}); !ok {
		t.Fatal("the same shot in an open room hit nobody, so the fixture proves nothing")
	}
	// AND IT IS THE SAME WALL FROM THE OTHER SIDE. Symmetry holds by construction
	// — one wall list, swept the same way whichever end the ray starts from — but
	// a hit test that only ever fired east would not notice the day it stopped
	// holding, and a shot that is blocked one way and lands the other is the
	// unfairest bug this file could ship.
	if hit, ok := shoot(l, Vec2{X: 15, Y: 8}, EyeHeight, eastward+math.Pi, 0, []body{at(1, 2, 8)}); ok {
		t.Fatalf("the same wall let a shot back through it westward, onto %+v", hit)
	}
}

func TestAShotGoesThroughTheDoorway(t *testing.T) {
	// The other half of the same rule. A doorway is a hole in the wall LIST
	// (level.go, subtractPortals) rather than a special case anywhere here, so
	// the shot simply passes the ends of both jambs and carries on.
	l := twoRooms(0)
	if _, ok := shoot(l, Vec2{X: 2, Y: 5}, EyeHeight, eastward, 0, []body{at(2, 15, 5)}); !ok {
		t.Fatal("a shot straight through the doorway hit nobody")
	}
	// A hand's width off the opening and it is a jamb, which is what stops
	// somebody rounding a corner through the wall beside a door.
	if hit, ok := shoot(l, Vec2{X: 2, Y: 6.4}, EyeHeight, eastward, 0, []body{at(2, 15, 6.4)}); ok {
		t.Fatalf("a shot past the edge of the opening hit %+v through the jamb", hit)
	}
}

func TestTheNearestBodyTakesIt(t *testing.T) {
	// One ray, one victim. Two men in a line and the near one is shot — the far
	// one is behind a body exactly as he would be behind a wall, and the order
	// the targets happen to arrive in must not decide it.
	l := openRoom()
	near, far := at(1, 6, 5), at(2, 9, 5)
	for _, order := range [][]body{{near, far}, {far, near}} {
		hit, ok := fire(l, order...)
		if !ok || hit != near.at {
			t.Fatalf("targets in order %v answered (%+v, %v), expected the near man %+v",
				[]ref{order[0].at, order[1].at}, hit, ok, near.at)
		}
	}
}

func TestNobodyBehindTheShooterIsHit(t *testing.T) {
	if hit, ok := fire(openRoom(), at(1, -3, 5)); ok {
		t.Fatalf("a man behind him was shot in the back by his own barrel: %+v", hit)
	}
}

func TestPointBlankIsStillAHit(t *testing.T) {
	// NOTHING PUSHES PLAYERS APART in this game — the resolver only ever pushes a
	// disc out of a WALL — so two men really can stand in the same half metre,
	// and the обрез is exactly the weapon for it. The shooter is inside the
	// target's own disc here, which is the case a naive quadratic answers with a
	// negative distance and throws away.
	if _, ok := fire(openRoom(), at(1, 2, 5)); !ok {
		t.Fatal("the muzzle was touching him and the shot missed")
	}
}

func TestLookingOverHisHeadMisses(t *testing.T) {
	// Pitch is not decoration: it decides whether the shot clears him. Ten metres
	// away, a body is 1.8 m of a ray that starts at 1.65 — so a very small angle
	// upwards is the difference between a kill and a hole in the far wall.
	l := openRoom()
	target := at(1, 12, 5)
	if _, ok := shoot(l, Vec2{X: 2, Y: 5}, EyeHeight, eastward, 0, []body{target}); !ok {
		t.Fatal("a level shot at a man ten metres away missed")
	}
	if _, ok := shoot(l, Vec2{X: 2, Y: 5}, EyeHeight, eastward, 0.05, []body{target}); ok {
		t.Fatal("a shot aimed over his head hit him")
	}
	// And into the floor in front of him, which is the same rule the other way up.
	if _, ok := shoot(l, Vec2{X: 2, Y: 5}, EyeHeight, eastward, -0.3, []body{target}); ok {
		t.Fatal("a shot into the floor hit a man standing beyond it")
	}
}

func TestABodyStandsOnItsOwnFloorAndNotOnTheShootersOne(t *testing.T) {
	// Rooms are at different heights (level.go, FloorStep), so where a man's feet
	// are is a fact about the room he is in. A step up puts his whole body inside
	// a level shot; a pit deep enough puts all of him under it.
	l := openRoom()
	raised := body{at: ref{N: 1}, pos: Vec2{X: 8, Y: 5}, floorZ: 0.6}
	if _, ok := fire(l, raised); !ok {
		t.Fatal("a man standing a step higher was not hit by a level shot")
	}
	sunken := body{at: ref{N: 1}, pos: Vec2{X: 8, Y: 5}, floorZ: -2}
	if _, ok := fire(l, sunken); ok {
		t.Fatal("a man whose head is below the muzzle was hit by a level shot")
	}
}

func TestTheRayIsTheSameGeometryInEveryDirection(t *testing.T) {
	// Nothing here may be true only along +X. The same shot is swept through a
	// full turn, with the target placed by the same trigonometry the simulation
	// walks by (sim.go: yaw zero looks along +Y), and it must land every time.
	l := openRoom()
	from := Vec2{X: 10, Y: 10}
	for i := 0; i < 16; i++ {
		yaw := float64(i) * math.Pi / 8
		sin, cos := math.Sincos(yaw)
		const away = 4.0
		target := at(1, from.X+sin*away, from.Y+cos*away)
		if _, ok := shoot(l, from, EyeHeight, yaw, 0, []body{target}); !ok {
			t.Fatalf("a shot at yaw %.2f missed a man standing directly along it", yaw)
		}
		// And the man diametrically behind that one is not also hit.
		behind := at(2, from.X-sin*away, from.Y-cos*away)
		if hit, ok := shoot(l, from, EyeHeight, yaw, 0, []body{behind}); ok {
			t.Fatalf("a shot at yaw %.2f hit %+v standing behind him", yaw, hit)
		}
	}
}

func TestAShotWithNobodyInItHitsNobody(t *testing.T) {
	if hit, ok := shoot(openRoom(), Vec2{X: 2, Y: 5}, EyeHeight, eastward, 0, nil); ok {
		t.Fatalf("an empty building answered %+v", hit)
	}
}
