package gamefintech

import "math"

// The simulation: one player, one command, one fixed step.
//
// EVERYTHING HERE IS PURE. Step takes the furniture, a player and a command and
// returns the next player. It reads no clock, holds no state, draws no
// randomness, iterates no map and allocates nothing that outlives it — which is
// what makes the whole of the game's feel table-testable without a socket,
// without a goroutine and without a sleep.
//
// It is also written this way because it EXISTS TWICE. Client-side prediction
// means the browser runs this same function (web/src/lib/fintechStep.ts), and the
// two are pinned together by the golden vectors in testdata/step_vectors.json,
// emitted by golden_test.go. A second implementation of one rule is normally the
// thing this project refuses to build; it is accepted here only because
// prediction is impossible without it, and it is only SAFE because a divergence
// is a red test in the ordinary gate rather than a player being dragged
// backwards on somebody else's screen. Do not give this function a clock, a
// random source or a map iteration, and do not change the ORDER of what it does
// without changing the port in the same commit.

// Command is one sub-step of player input. Several arrive in a frame, because
// the socket allows InputHz frames a second and the client samples at SimHz — so
// a frame carries the steps that happened between sends rather than throwing
// them away.
//
// It is INTENT ONLY. There is no position here, no salary and no claim to have
// dodged anything: a direction, a duration, and a request to dash.
type Command struct {
	// Seq is the client's own monotonic per-command counter, 1-based. The
	// server echoes the last one it applied so the client can drop acknowledged
	// commands from its pending list and replay the rest — the whole of
	// reconciliation. Zero means "unset" and is dropped rather than applied.
	Seq uint32 `json:"q"`
	// Dt is how long this sub-step lasted, in seconds. Clamped by Sanitise.
	Dt float64 `json:"dt"`
	// MX and MY are the movement axes in WORLD space, each in −1..1, +Y down.
	// There is no yaw in this game — the plane is drawn from above and the stick
	// points where you go — so the client's stick vector arrives unrotated.
	MX float64 `json:"mx"`
	MY float64 `json:"my"`
	// Dash requests the dash. It is a REQUEST: the simulation grants it only if
	// the cooldown has expired and no dash is already running, and a refused
	// request is silent.
	Dash bool `json:"d,omitempty"`
}

// Player is one person's whole state inside the office.
//
// Salary, Streak and MoveGrace are as much of the game as Pos is: the position
// is what you watch, and these three are what you are playing for.
type Player struct {
	Pos Vec2
	// Salary is rubles earned this shift. A float because it accrues
	// continuously; quantised to whole rubles on the wire.
	Salary float64
	// Streak is seconds of accumulated stillness, and it is what the multiplier
	// is computed from. It grows only while standing still and only outside a
	// dash.
	Streak float64
	// MoveGrace is seconds spent moving inside the grace window. It is a
	// separate counter from Streak because the grace is a REPRIEVE rather than a
	// decay: move briefly and nothing happens at all, move past the window and
	// the streak is gone in one step.
	MoveGrace float64
	// DashLeft is seconds of dash remaining; DashCooldown is seconds until the
	// next one is available.
	DashLeft     float64
	DashCooldown float64
	// DashDX and DashDY are the direction the current dash committed to, unit
	// length, captured when it started.
	//
	// A DASH IS A COMMITTED MOVEMENT, NOT "MOVE FAST WHILE HOLDING THE STICK",
	// and that is the whole reason these exist. Without them a dash only moved on
	// the ticks that happened to carry input — and the commonest dash in this
	// game is tapped from a standstill, where exactly ONE command carries a
	// direction and the rest of the burst has no input at all. Measured: 0.50 m
	// of a 5.20 m dash. Worse than short, it was NON-DETERMINISTIC: how far you
	// went depended on which ticks input landed on, the client and the server
	// disagreed about that, and at dash speed the gap cleared the snap threshold
	// and yanked the player back and forth.
	DashDX float64
	DashDY float64
	// SlowLeft is seconds of Claude Code's slow remaining.
	//
	// ON Player RATHER THAN ON THE OCCUPANT, and it is the only one of this
	// iteration's new values that has to be. The cloud, the persona and the
	// redirect all live on the Occupant precisely because `Step` never reads them —
	// but this one MULTIPLIES THE WALK, so a client that did not know about it
	// would predict 6.4 m/s while the office moved him at 5.12. That is 1.28 m/s of
	// divergence: past `SILENT_CORRECTION` immediately and past the snap threshold
	// in about 1.6 s of walking, which is a figure yanked backwards ten times a
	// second while a thumb is held down.
	//
	// So it is here, it is in the TypeScript port, and it is in the golden vectors —
	// the three things that have to agree, and the reason this iteration is the only
	// one in the batch that regenerates them.
	SlowLeft float64
	// Alive is false once the bald man has reached this player. A dead occupant
	// is not a target for him and is not stepped again.
	Alive bool
}

// NewPlayerAt puts somebody down at a given point with nothing earned.
//
// It takes the position rather than reading a constant because WHERE somebody
// starts is a decision the office makes and this is not the office — a shift now
// spawns at a drawn point clear of the desks, of the bald man and of whoever is
// already working, and only the office knows any of that. See Office.spawnPoint.
func NewPlayerAt(at Vec2) Player {
	return Player{Pos: at, Alive: true}
}

// insideDesk reports whether a disc's centre is strictly inside a desk expanded
// by its radius — the condition the resolver exists to make false, and the
// condition a spawn has to avoid rather than resolve.
func insideDesk(d Rect, p Vec2, r float64) bool {
	const eps = 1e-9
	return p.X > d.X-r+eps && p.X < d.X+d.W+r-eps &&
		p.Y > d.Y-r+eps && p.Y < d.Y+d.H+r-eps
}

// clearLine reports whether a disc of radius r could travel straight from a to b
// without meeting a desk.
//
// IT IS THE FAST PATH OF THE NAVIGATION (navigate.go): when the straight line is
// free there is nothing to plan, and the bald man simply walks at you — which is
// both cheaper and smoother than following a grid across an empty floor.
//
// It was written for a different job. Before he could route around furniture, a
// clear line was the predicate a SPAWN was chosen by, because a shift opening in
// a desk's shadow measured up to ninety seconds of nothing happening. That defect
// is fixed rather than avoided now, so the spawn rule is gone and this is what
// survives of it.
//
// Liang–Barsky against each desk grown by r, which is the standard segment/box
// clip and is exact — no sampling along the segment, so a thin desk cannot be
// stepped over.
func clearLine(a, b Vec2, r float64) bool {
	for _, d := range Desks {
		if segmentHitsRect(a, b, d, r) {
			return false
		}
	}
	return true
}

// segmentHitsRect is Liang–Barsky: does the segment a..b meet the rectangle d
// grown by r on every side?
func segmentHitsRect(a, b Vec2, d Rect, r float64) bool {
	minX, minY := d.X-r, d.Y-r
	maxX, maxY := d.X+d.W+r, d.Y+d.H+r
	t0, t1 := 0.0, 1.0
	dx, dy := b.X-a.X, b.Y-a.Y
	for _, e := range [4][2]float64{{-dx, a.X - minX}, {dx, maxX - a.X}, {-dy, a.Y - minY}, {dy, maxY - a.Y}} {
		p, q := e[0], e[1]
		if p == 0 {
			// Parallel to this edge: outside it means the segment misses the box
			// entirely, and no later edge can bring it back.
			if q < 0 {
				return false
			}
			continue
		}
		t := q / p
		switch {
		case p < 0:
			if t > t1 {
				return false
			}
			if t > t0 {
				t0 = t
			}
		default:
			if t < t0 {
				return false
			}
			if t < t1 {
				t1 = t
			}
		}
	}
	return true
}

// Sanitise clamps every attacker-controlled field into the range the server is
// willing to simulate.
//
// A dt of a thousand seconds, a movement axis of a million, or a NaN are all one
// JSON frame away, so each is clamped rather than rejected: a refusal would need
// reporting, and a clamp is silent, total and cannot be probed for information.
//
// IT IS CALLED BY Office.Enqueue AND NEVER BY Step. Step stays a pure function
// of already-valid input, so the golden vectors describe the simulation rather
// than the sanitiser, and the TypeScript port can predict from the commands it
// just sent without re-deriving what the server would have done to them.
func Sanitise(c Command) Command {
	c.Dt = clampFinite(c.Dt, 0, MaxStepSeconds)
	c.MX = clampFinite(c.MX, -1, 1)
	c.MY = clampFinite(c.MY, -1, 1)
	// A diagonal must not be faster than a straight line. Normalising only when
	// the vector is longer than one keeps a half-pressed stick analogue, which
	// matters on a phone where the stick is the only control there is.
	if mag := math.Hypot(c.MX, c.MY); mag > 1 {
		c.MX, c.MY = c.MX/mag, c.MY/mag
	}
	return c
}

func clampFinite(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(lo, math.Min(hi, v))
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Multiplier is the money multiplier a streak has earned: ×1 at nothing, rising
// linearly to MaxMultiplier at RampSeconds and staying there.
//
// Exported because the snapshot publishes it and the tests read it, and shared
// with Step so the number the HUD shows and the number that pays out cannot
// drift apart.
func Multiplier(streak float64) float64 {
	return 1 + (MaxMultiplier-1)*math.Min(streak/RampSeconds, 1)
}

// Step advances one player by one already-sanitised command.
//
// THE ORDER BELOW IS THE SPECIFICATION. The TypeScript port matches it statement
// for statement and the golden vectors pin the two together, so a reordering
// here is a change to the game that must be made in both places:
//
//  1. Dash start
//  2. Timers
//  3. Speed
//  4. Move, clamp, push out of furniture
//  5. Idle test
//  6. The streak
//  7. Money
//  8. Return
//
// Two consequences are worth stating so they are not "fixed" later by mistake.
// A DASH EARNS NOTHING — you are not standing still. And A DASH DOES NOT LOSE
// THE RAMP — you were never really working. That asymmetry is the entire skill
// ceiling: the correct play is to leave the dodge as late as you dare, because
// the seconds you spend walking are the only ones that cost you anything.
func Step(desks []Rect, p Player, c Command) Player {
	// 1. Dash start. A request is granted only off cooldown and only when no
	// dash is already running, so holding the button does nothing and mashing it
	// does nothing.
	if c.Dash && p.DashCooldown <= 0 && p.DashLeft <= 0 {
		p.DashLeft = DashSeconds
		p.DashCooldown = DashCooldown
		// The direction is captured HERE, once, and the whole dash runs on it.
		// A dash with no direction at all still burns — the client always sends
		// one (see dashAxes), and inventing one here would be the server making
		// up input.
		if mag := math.Hypot(c.MX, c.MY); mag > 0 {
			p.DashDX, p.DashDY = c.MX/mag, c.MY/mag
		} else {
			p.DashDX, p.DashDY = 0, 0
		}
	}

	// Whether the dash is running FOR THIS STEP, captured after it may have
	// started and before the timers are decremented. A dash therefore takes
	// effect on the very step it is requested — anything else would make the
	// button feel like it missed — and the same flag decides both the speed
	// (op 3) and the streak (op 6), so those two can never disagree about
	// whether this step was a dash.
	dashing := p.DashLeft > 0

	// 2. Timers.
	p.DashLeft = math.Max(0, p.DashLeft-c.Dt)
	p.DashCooldown = math.Max(0, p.DashCooldown-c.Dt)
	p.SlowLeft = math.Max(0, p.SlowLeft-c.Dt)

	// 3. Speed, and — while dashing — the DIRECTION too. The command's axes are
	// ignored for the duration: the dash goes where it committed, so it covers
	// the same ground whether input arrives on every tick, on one of them, or on
	// none at all. That is what makes it identical on both ends of the port.
	speed := WalkSpeed
	mx, my := c.MX, c.MY
	if dashing {
		speed = DashSpeed
		mx, my = p.DashDX, p.DashDY
	} else if p.SlowLeft > 0 {
		// THE SLOW MULTIPLIES THE WALK AND NOT THE DASH, deliberately. The dash is
		// the answer to being caught, and a slowed dash would be a punishment that
		// takes away the way out of the punishment — so it still covers its
		// documented 5.20 m and `TestADashCoversItsWholeDistanceWithNoFurtherInput`
		// is untouched. Slowed, walking is 5.12 m/s: a real cost, and still faster
		// than either man on the floor.
		speed *= SlowFactor
	}

	// 4. Move, then clamp to the floor, then push out of every desk in
	// catalogue order. One pass per desk is enough and is deterministic: the
	// desks are metres apart, so no push-out can put anybody inside another one,
	// and every desk is clear of the walls, so no push-out can put anybody
	// through one. content_test pins both of those invariants, which is what
	// lets this stay a single pass rather than the iterative resolver a
	// generated level needs.
	p.Pos.X += mx * speed * c.Dt
	p.Pos.Y += my * speed * c.Dt
	p.Pos = clampToFloor(p.Pos, PlayerRadius)
	for _, d := range desks {
		p.Pos = pushOut(d, p.Pos, PlayerRadius)
	}

	// 5. Idle test.
	moving := math.Hypot(c.MX, c.MY) > IdleThreshold

	// 6. The streak — the rule the whole game rests on.
	switch {
	case dashing:
		// The one movement that costs nothing: the streak is preserved and does
		// not grow, and the grace window is not touched. Dashing is neither
		// working nor slacking.
	case moving:
		p.MoveGrace += c.Dt
		if p.MoveGrace > GraceSeconds {
			p.Streak = 0
			// Clamped rather than left to grow, so a player who walks for a
			// minute does not have to stand still for a minute before the grace
			// is available again. It is a reprieve, not a debt.
			p.MoveGrace = GraceSeconds
		}
	default:
		p.MoveGrace = 0
		p.Streak += c.Dt
	}

	// 7. Money. Accrues ONLY while standing still and not dashing, at the
	// multiplier the streak has just earned.
	if !moving && !dashing {
		p.Salary += BasePerSecond * Multiplier(p.Streak) * c.Dt
	}

	// 8.
	return p
}

// clampToFloor keeps a disc of radius r inside the room.
func clampToFloor(p Vec2, r float64) Vec2 {
	return Vec2{
		X: clamp(p.X, r, OfficeW-r),
		Y: clamp(p.Y, r, OfficeH-r),
	}
}

// pushOut moves a disc of radius r clear of a desk if it is inside it, along the
// axis of least penetration.
//
// The desk is expanded by the radius and the disc treated as its centre point,
// which squares off the corners a little. That is deliberate: it is one
// comparison per side, it is trivially portable to TypeScript, and the
// difference between a squared and a rounded desk corner is four centimetres
// nobody has ever noticed while being chased.
func pushOut(d Rect, p Vec2, r float64) Vec2 {
	lo := Vec2{X: d.X - r, Y: d.Y - r}
	hi := Vec2{X: d.X + d.W + r, Y: d.Y + d.H + r}
	if p.X <= lo.X || p.X >= hi.X || p.Y <= lo.Y || p.Y >= hi.Y {
		return p
	}
	// Four penetrations, all positive because the point is inside.
	left, right := p.X-lo.X, hi.X-p.X
	up, down := p.Y-lo.Y, hi.Y-p.Y
	least := math.Min(math.Min(left, right), math.Min(up, down))
	// Compared in a fixed order so an exact tie resolves the same way on both
	// ends of the port. Ties happen: the centre of a desk is one.
	switch least {
	case left:
		p.X = lo.X
	case right:
		p.X = hi.X
	case up:
		p.Y = lo.Y
	default:
		p.Y = hi.Y
	}
	return p
}
