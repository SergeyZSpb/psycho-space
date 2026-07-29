package gamefintech

import (
	"math"
	"testing"
)

// The money ramp, which is the game. Every test here is one sentence of the
// splash-screen cheatsheet, checked against the simulation that generates it.

// stand advances a player through n steps of standing perfectly still.
func stand(p Player, n int) Player {
	for i := 0; i < n; i++ {
		p = Step(Desks, p, Command{Dt: testDt})
	}
	return p
}

// walk advances a player through n steps of walking, without dashing.
func walk(p Player, n int) Player {
	for i := 0; i < n; i++ {
		p = Step(Desks, p, Command{Dt: testDt, MX: 1})
	}
	return p
}

func TestTheRampReachesTheCapAtRampSeconds(t *testing.T) {
	if got := Multiplier(0); got != 1 {
		t.Fatalf("a cold start pays ×%v", got)
	}
	if got := Multiplier(RampSeconds / 2); math.Abs(got-(1+(MaxMultiplier-1)/2)) > 1e-9 {
		t.Fatalf("half the ramp pays ×%v", got)
	}
	if got := Multiplier(RampSeconds); math.Abs(got-MaxMultiplier) > 1e-9 {
		t.Fatalf("a full ramp pays ×%v, want ×%v", got, MaxMultiplier)
	}
	// And it stops there. An unbounded ramp would make the whole game "stand
	// still for as long as you can bear", which is a worse game than "protect
	// the three".
	if got := Multiplier(RampSeconds * 100); got != MaxMultiplier {
		t.Fatalf("a very long streak pays ×%v", got)
	}
}

func TestStandingStillAccruesTheBaseRateAtTheCurrentMultiplier(t *testing.T) {
	// One step of stillness from cold: the streak grows first, then the money is
	// paid at the multiplier that streak has just earned. The order is the
	// specification, so the number below is what pins it.
	p := stand(atSpawn(), 1)
	want := BasePerSecond * Multiplier(testDt) * testDt
	if math.Abs(p.Salary-want) > 1e-9 {
		t.Fatalf("one still step paid %v, want %v", p.Salary, want)
	}

	// A full ramp, then a second at the cap.
	p = stand(atSpawn(), int(RampSeconds*SimHz))
	before := p.Salary
	p = stand(p, SimHz)
	if got := p.Salary - before; math.Abs(got-BasePerSecond*MaxMultiplier) > 1e-6 {
		t.Fatalf("a second at the cap paid %v, want %v", got, BasePerSecond*MaxMultiplier)
	}
}

func TestMovingResetsTheStreakButOnlyAfterTheGraceWindow(t *testing.T) {
	full := stand(atSpawn(), int(RampSeconds*SimHz))
	if math.Abs(Multiplier(full.Streak)-MaxMultiplier) > 1e-9 {
		t.Fatalf("the setup never reached the cap: streak %v", full.Streak)
	}

	// Inside the window: a twitch costs nothing at all.
	//
	// Floor, not a plain conversion: the grace need not land on a whole tick —
	// it did at 0.3 s and does not at 0.18 — and a test that assumes it does
	// stops compiling the first time somebody retunes the feel, which is exactly
	// when it should be telling them whether the rule still holds.
	graceTicks := int(math.Floor(GraceSeconds * SimHz))
	brief := walk(full, graceTicks-1)
	if brief.Streak != full.Streak {
		t.Fatalf("a %v-second twitch cost the streak: %v → %v", GraceSeconds, full.Streak, brief.Streak)
	}

	// Past it: the streak is gone in one step, not decayed.
	past := walk(full, graceTicks+2)
	if past.Streak != 0 {
		t.Fatalf("walking past the grace window left a streak of %v", past.Streak)
	}
}

func TestTheGraceWindowIsClampedRatherThanGrowing(t *testing.T) {
	// A player who walks for a minute must not owe a minute of stillness before
	// the grace is available again. It is a reprieve, not a debt.
	long := walk(atSpawn(), 20*SimHz)
	if long.MoveGrace != GraceSeconds {
		t.Fatalf("twenty seconds of walking banked %v of grace, cap is %v", long.MoveGrace, GraceSeconds)
	}
	// One still step gives it straight back.
	if got := stand(long, 1); got.MoveGrace != 0 {
		t.Fatalf("standing still left %v of grace behind", got.MoveGrace)
	}
}

func TestMovingEarnsNothing(t *testing.T) {
	p := walk(stand(atSpawn(), int(RampSeconds*SimHz)), SimHz)
	before := stand(atSpawn(), int(RampSeconds*SimHz)).Salary
	if p.Salary != before {
		t.Fatalf("a second of walking paid %v", p.Salary-before)
	}
}

func TestADashNeverResetsTheStreakAndNeverEarns(t *testing.T) {
	// THE ASYMMETRY THE WHOLE SKILL CEILING RESTS ON. A dash is not working, so
	// it pays nothing; but it is not slacking either, so it costs nothing.
	full := stand(atSpawn(), int(RampSeconds*SimHz))
	pay, streak := full.Salary, full.Streak

	// Request it, then hold the stick down for exactly as long as the dash runs.
	p := Step(Desks, full, Command{Dt: testDt, MX: 1, Dash: true})
	if p.DashLeft <= 0 {
		t.Fatal("the dash was refused from a cold cooldown")
	}
	for p.DashLeft > 0 {
		p = Step(Desks, p, Command{Dt: testDt, MX: 1})
	}
	if p.Streak != streak {
		t.Fatalf("the dash cost the streak: %v → %v", streak, p.Streak)
	}
	if p.Salary != pay {
		t.Fatalf("the dash earned %v", p.Salary-pay)
	}
	if p.MoveGrace != 0 {
		t.Fatalf("the dash spent %v of the grace window", p.MoveGrace)
	}

	// And the exemption is the DASH rather than the grace window happening to be
	// longer than it: one walking step spends grace, the identical dashing step
	// spends none.
	walked := Step(Desks, full, Command{Dt: testDt, MX: 1})
	dashed := Step(Desks, full, Command{Dt: testDt, MX: 1, Dash: true})
	if walked.MoveGrace != testDt {
		t.Fatalf("one walking step spent %v of grace, want %v", walked.MoveGrace, testDt)
	}
	if dashed.MoveGrace != 0 {
		t.Fatalf("one dashing step spent %v of grace", dashed.MoveGrace)
	}
}

func TestADashMovesAtDashSpeedOnTheStepItIsRequested(t *testing.T) {
	// The button must not feel like it missed. A dash granted on this step is a
	// dash that moves on this step.
	p := atSpawn()
	p.Pos = Vec2{X: 8, Y: 8}
	got := Step(nil, p, Command{Dt: testDt, MX: 1, Dash: true})
	if want := 8 + DashSpeed*testDt; math.Abs(got.Pos.X-want) > 1e-9 {
		t.Fatalf("the first dash step moved to %v, want %v (walk would be %v)",
			got.Pos.X, want, 8+WalkSpeed*testDt)
	}
}

func TestTheDashCannotBeRetriggeredInsideItsCooldown(t *testing.T) {
	p := atSpawn()
	p.Pos = Vec2{X: 8, Y: 8}
	p = Step(Desks, p, Command{Dt: testDt, MX: 1, Dash: true})
	if math.Abs(p.DashCooldown-(DashCooldown-testDt)) > 1e-9 {
		t.Fatalf("the cooldown started at %v", p.DashCooldown)
	}

	// Mash it for a second: nothing happens, and the cooldown keeps running down
	// rather than being reset by the attempt.
	for i := 0; i < SimHz; i++ {
		p = Step(Desks, p, Command{Dt: testDt, MX: 1, Dash: true})
		if p.DashLeft > 0 && float64(i)*testDt > DashSeconds {
			t.Fatalf("step %d: a second dash started inside the cooldown", i)
		}
	}
	if want := DashCooldown - float64(SimHz+1)*testDt; math.Abs(p.DashCooldown-want) > 1e-9 {
		t.Fatalf("mashing the button held the cooldown at %v, want %v", p.DashCooldown, want)
	}

	// And once it expires, it works again.
	for p.DashCooldown > 0 {
		p = Step(Desks, p, Command{Dt: testDt})
	}
	p = Step(Desks, p, Command{Dt: testDt, MX: 1, Dash: true})
	if p.DashLeft <= 0 {
		t.Fatal("the dash never came back after its cooldown expired")
	}
}

func TestTheDashLastsExactlyDashSeconds(t *testing.T) {
	p := atSpawn()
	p.Pos = Vec2{X: 8, Y: 8}
	steps := 0
	c := Command{Dt: testDt, MX: 1, Dash: true}
	for {
		p = Step(nil, p, c)
		c = Command{Dt: testDt, MX: 1}
		if p.DashLeft <= 0 {
			break
		}
		steps++
		if steps > 100 {
			t.Fatal("the dash never ended")
		}
	}
	// DashSeconds is not a whole number of steps, so the count is the ceiling.
	if want := int(math.Ceil(DashSeconds/testDt)) - 1; steps != want {
		t.Fatalf("the dash ran for %d steps, want %d", steps, want)
	}
}

func TestAThumbRestingOnTheStickIsStandingStill(t *testing.T) {
	// An analogue stick never returns exactly zero, and a player whose thumb is
	// resting on it is standing still as far as anybody watching is concerned.
	p := stand(atSpawn(), 1)
	twitch := Step(Desks, p, Command{Dt: testDt, MX: IdleThreshold / 2})
	if twitch.Salary <= p.Salary {
		t.Fatal("a stick below the idle threshold stopped the money")
	}
	if twitch.MoveGrace != 0 {
		t.Fatalf("a stick below the idle threshold spent grace: %v", twitch.MoveGrace)
	}
}

// TestADashCoversItsWholeDistanceWithNoFurtherInput is the rule that makes a
// dash a dash.
//
// The commonest dash in this game is tapped from a standstill — that is the
// state the whole game is played in — so exactly ONE command carries a
// direction and the rest of the burst carries no input at all. A dash steered
// per-command covered 0.50 m of its 5.20 m, and covered a DIFFERENT fraction on
// the client, which is what threw the player back and forth.
func TestADashCoversItsWholeDistanceWithNoFurtherInput(t *testing.T) {
	p := atSpawn()
	from := p.Pos
	// One command carrying the dash and its direction, then silence. DOWN the
	// clear central lane: the spawn is only 3.65 m from the top wall and a dash
	// is 5.2 m, so dashing up would measure the wall rather than the dash.
	p = Step(Desks, p, Command{Seq: 1, Dt: 0.025, MX: 0, MY: 1, Dash: true})
	for p.DashLeft > 0 {
		p = Step(Desks, p, Command{Dt: 0.025})
	}
	got := p.Pos.Y - from.Y
	want := DashSpeed * DashSeconds
	// Within one sub-step. A dash is granted in whole sub-steps, and DashSeconds
	// need not be a multiple of one, so the last step of a dash runs its full
	// length even when less dash remained. That is quantisation rather than
	// drift: both ends run this same function over the same sub-steps, so they
	// overshoot identically and never disagree — which is the property that
	// matters, and the reason this is a tolerance and not a bug.
	if slack := DashSpeed * 0.025; math.Abs(got-want) > slack+1e-9 {
		t.Fatalf("a dash covered %.3f m with no further input, want %.3f m (±%.3f)", got, want, slack)
	}
	// And it is emphatically not the 0.50 m it used to be.
	if got < want*0.9 {
		t.Fatalf("a dash covered only %.3f m of its %.3f m", got, want)
	}
}

// TestADashIgnoresTheStickOnceCommitted — it goes where it committed, so the
// same dash covers the same ground however the thumb wanders during it.
func TestADashIgnoresTheStickOnceCommitted(t *testing.T) {
	run := func(during Command) Vec2 {
		p := atSpawn()
		p = Step(Desks, p, Command{Seq: 1, Dt: 0.025, MX: 0, MY: 1, Dash: true})
		for p.DashLeft > 0 {
			c := during
			c.Dt = 0.025
			p = Step(Desks, p, c)
		}
		return p.Pos
	}
	silent := run(Command{})
	fighting := run(Command{MX: 1, MY: 1}) // shoving the other way mid-dash
	if math.Abs(silent.X-fighting.X) > 1e-9 || math.Abs(silent.Y-fighting.Y) > 1e-9 {
		t.Fatalf("the stick steered a committed dash: %v vs %v", silent, fighting)
	}
}
