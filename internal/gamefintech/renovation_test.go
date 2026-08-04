package gamefintech

import (
	"context"
	"testing"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/google/uuid"
)

// THE DAILY REBUILD.
//
// Two halves, tested separately because they fail differently. The ARITHMETIC —
// which day an instant belongs to, and what office that day is entitled to — is
// pure and is swept; the TRIGGER — one tick observing the day turn over — is the
// destructive half, and every one of its claims is about what happens to people
// who were standing in the room.
//
// Nothing here waits for a clock. The tick is a parameter (ADR-034) and the day
// is a parameter of the arithmetic, so «it is now one second past nine in the
// evening» is a thing a test says rather than something it waits twenty-four
// hours for.

// evening is an instant on a named date, UTC, for the boundary tests below.
func evening(y int, m time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}

func TestTheRenovationDayTurnsOverAtNineInTheEvening(t *testing.T) {
	// The boundary is 21:00 UTC and NOT midnight UTC, so the two things that can
	// go wrong are bucketing on the wrong hour and bucketing on the wrong day.
	// Both are asserted as RELATIONS between instants rather than against a
	// literal day number, because the number is an epoch offset nobody should
	// have to compute by hand to read this test.
	base := evening(2026, time.August, 4, 20, 59, 59)
	after := evening(2026, time.August, 4, 21, 0, 0)

	if renovationDay(after) != renovationDay(base)+1 {
		t.Fatalf("20:59:59 is day %d and 21:00:00 is day %d — the boundary is not where it should be",
			renovationDay(base), renovationDay(after))
	}
	// One second either side of the same boundary, from below and from above.
	if got := renovationDay(base.Add(-time.Second)); got != renovationDay(base) {
		t.Fatalf("20:59:58 and 20:59:59 landed on different days (%d, %d)", got, renovationDay(base))
	}
	if got := renovationDay(after.Add(time.Second)); got != renovationDay(after) {
		t.Fatalf("21:00:00 and 21:00:01 landed on different days (%d, %d)", got, renovationDay(after))
	}

	// A UTC DAY EDGE IS NOT A RENOVATION DAY EDGE, which is the whole point of
	// the shift: 23:00 and the 05:00 after it are the same office.
	late := evening(2026, time.August, 4, 23, 0, 0)
	early := evening(2026, time.August, 5, 5, 0, 0)
	if renovationDay(late) != renovationDay(early) {
		t.Fatalf("the office changed over UTC midnight: %d then %d", renovationDay(late), renovationDay(early))
	}

	// And a day apart is a day apart, at any hour.
	for _, at := range []time.Time{base, after, late, early} {
		if got := renovationDay(at.Add(24 * time.Hour)); got != renovationDay(at)+1 {
			t.Fatalf("%v plus a day is day %d, not %d", at, got, renovationDay(at)+1)
		}
	}

	// THE ANSWER DOES NOT DEPEND ON THE Time'S LOCATION, because an instant is an
	// instant: midnight in Moscow is 21:00 UTC and belongs to the day that has
	// just begun.
	moscow := time.FixedZone("MSK", 3*60*60)
	if got := renovationDay(time.Date(2026, time.August, 5, 0, 0, 0, 0, moscow)); got != renovationDay(after) {
		t.Fatalf("midnight in Moscow is day %d and 21:00 UTC is day %d — they are the same instant", got, renovationDay(after))
	}
}

func TestADayStartsAtTheHourItsOfficeWasInstalled(t *testing.T) {
	// dayStart is the inverse of renovationDay on the boundary, and it is what a
	// derived floor reports as its installation time — so a boot at 22:00 and a
	// process that rotated at 21:00 say the same thing about the same floor.
	day := renovationDay(evening(2026, time.August, 4, 22, 30, 0))
	start := dayStart(day)
	if start.Hour() != RenovationHourUTC || start.Minute() != 0 || start.Second() != 0 {
		t.Fatalf("a day starts at %v, want %02d:00:00 UTC", start, RenovationHourUTC)
	}
	if renovationDay(start) != day {
		t.Fatalf("the instant a day starts belongs to day %d, not %d", renovationDay(start), day)
	}
	if renovationDay(start.Add(-time.Nanosecond)) != day-1 {
		t.Fatal("the nanosecond before a day starts belongs to that day")
	}
}

func TestEveryDailyOfficeForFiveYearsIsPlayable(t *testing.T) {
	// THE SWEEP THAT MAKES THE DERIVATION SAFE TO SHIP. Nothing chooses the day's
	// seed, so nothing can refuse a bad one: whatever the arithmetic produces is
	// what everybody plays on that date. Generate can answer ErrThinFloor and
	// ValidateLayout can refuse geometry the single-pass resolver cannot survive,
	// and either of those on a Tuesday in 2029 would be a defect nobody could see
	// coming.
	//
	// Five years is 1826 days at ~0.6 ms each — a second of test for every date
	// this game is plausibly still up.
	const days = 5*365 + 1
	first := renovationDay(time.Now())
	ids := make(map[string]int64, days)
	var prev string
	for d := first; d < first+days; d++ {
		l, err := dailyLayout(d)
		if err != nil {
			t.Fatalf("day %d (%v) could not be drawn: %v", d, dayStart(d).Format(time.DateOnly), err)
		}
		if issues := ValidateLayout(l); len(issues) > 0 {
			t.Fatalf("day %d (%v) is not playable: %s at %d",
				d, dayStart(d).Format(time.DateOnly), issues[0].Problem, issues[0].Index)
		}
		// AND IT IS A DIFFERENT OFFICE EVERY DAY, which is the point of the whole
		// exercise — a rebuild that produced yesterday's room would be an eviction
		// for nothing. Consecutive days are consecutive integers, so this is what
		// the decorrelating multiply in seedForDay is actually for.
		if l.ID == prev {
			t.Fatalf("day %d drew the same office as the day before it (%s)", d, l.ID)
		}
		if was, seen := ids[l.ID]; seen {
			t.Fatalf("day %d drew the same office as day %d (%s)", d, was, l.ID)
		}
		ids[l.ID], prev = d, l.ID
	}
}

func TestAFreshServiceHasAlreadyDealtWithTodaysRotation(t *testing.T) {
	// The one invariant that makes a deploy safe: the first tick after boot
	// cannot evict anybody, because the day it carries is the day the service was
	// built in and the guard needs a LATER one.
	s := NewService(newFakeTransport(), Room, nil, newFakeRepo(), fakeProfiles{}, testFloor)
	if got, want := s.installedDay, renovationDay(time.Now()); got != want {
		t.Fatalf("a fresh service believes it is day %d, the clock says %d", got, want)
	}
	acc := uuid.New().String()
	if _, err := s.StartShift(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	s.step(context.Background(), time.Now())
	if _, ok := s.CurrentShift(acc); !ok {
		t.Fatal("the first tick after boot threw somebody out of the office")
	}
}

// renovating puts a harness one second short of a boundary, with `n` accounts
// working shifts old enough to be worth recording. It answers the instant just
// before the rebuild and the instant just after it.
func renovating(t *testing.T, h *harness, accounts []string) (before, after time.Time) {
	t.Helper()
	ctx := context.Background()
	for _, acc := range accounts {
		if _, err := h.svc.StartShift(ctx, acc); err != nil {
			t.Fatal(err)
		}
	}
	// Backdated rather than waited out: how long a shift LASTED is measured
	// against the real clock the handler started it on, exactly as the quit
	// button's tests do it.
	h.svc.mu.Lock()
	h.svc.office.mu.Lock()
	for _, acc := range accounts {
		h.svc.office.occupants[acc].StartedAt = time.Now().Add(-time.Minute)
		h.svc.office.occupants[acc].State.Salary = 1000
	}
	h.svc.office.mu.Unlock()
	h.svc.mu.Unlock()

	before = evening(2026, time.August, 4, 20, 59, 59)
	after = evening(2026, time.August, 4, 21, 0, 0)
	h.svc.mu.Lock()
	h.svc.installedDay = renovationDay(before)
	h.svc.mu.Unlock()
	return before, after
}

func TestTheDayTurningOverRebuildsTheOfficeOnOneTick(t *testing.T) {
	// THE WHOLE OF THE FEATURE, IN ONE STEP. Nine o'clock passes, and on the very
	// next tick the floor is a different floor, everybody standing on it has been
	// told why, their shifts are recorded, and the office is gone rather than
	// re-floored — the same contract Install has, reached without anybody pressing
	// anything.
	a, b := uuid.New().String(), uuid.New().String()
	h := start(t,
		realtime.Member{ConnID: "ca", AccountID: a},
		realtime.Member{ConnID: "cb", AccountID: b},
	)
	before, after := renovating(t, h, []string{a, b})
	was := h.svc.Floor()

	// A tick a second short of the hour changes nothing at all.
	h.tickAt(t, before)
	if got := h.svc.Floor(); got.Layout.ID != was.Layout.ID || got.Occupants != 2 {
		t.Fatalf("a tick before the hour rebuilt the office: %+v", got)
	}

	h.tickAt(t, after)

	now := h.svc.Floor()
	if now.Layout.ID == was.Layout.ID {
		t.Fatalf("the office is still %q after the day turned over", now.Layout.ID)
	}
	if now.Source != SourceDaily {
		t.Fatalf("the day's floor says it came from %q, want %q", now.Source, SourceDaily)
	}
	if !now.InstalledAt.Equal(dayStart(renovationDay(after))) {
		t.Fatalf("the day's floor says it arrived at %v, the day began at %v",
			now.InstalledAt, dayStart(renovationDay(after)))
	}
	if now.Occupants != 0 {
		t.Fatalf("%d people survived the rebuild", now.Occupants)
	}
	// The office is DROPPED rather than re-floored — the лысый, Claude, the
	// colleagues and the props were all placed against a floor that is gone.
	h.svc.mu.Lock()
	office := h.svc.office
	h.svc.mu.Unlock()
	if office != nil {
		t.Fatal("the office survived the floor it was standing on")
	}

	// EVERYBODY WAS TOLD, one frame per connection, and every one of them says
	// «РЕМОНТ» — an eviction that empties the map without publishing leaves each
	// browser watching an office that has stopped moving.
	over := h.tr.framesOfType(TypeOver)
	if len(over) != 2 {
		t.Fatalf("expected an over frame for each of the two connections, got %d", len(over))
	}
	for _, f := range over {
		if f.Frame["cause"] != CauseRenovated {
			t.Fatalf("a shift ended with cause %v, want %q", f.Frame["cause"], CauseRenovated)
		}
		// At least what they had: the tick a second short of the hour simulated a
		// fiftieth of a second of standing perfectly still, which is the game and
		// therefore pays. Asserting equality here would be asserting that the
		// office does nothing.
		if pay, _ := f.Frame["pay"].(float64); pay < 1000 {
			t.Fatalf("the ending says %v was earned, the shift had 1000", f.Frame["pay"])
		}
	}

	// And both are recorded, with the money that was earned on a floor the
	// building took away.
	for i := 0; i < 2; i++ {
		select {
		case written := <-h.repo.inserted:
			if written.Cause != CauseRenovated || written.Salary < 1000 {
				t.Fatalf("wrote %+v", written)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of the two evicted shifts were written", i)
		}
	}

	// NOTHING WAS WRITTEN TO POSTGRES BECAUSE TIME PASSED, which is the claim the
	// whole design rests on (ADR-038, ADR-062): the day's office is derived, so a
	// rotation stores no floor.
	h.repo.mu.Lock()
	wrote := len(h.repo.layouts)
	h.repo.mu.Unlock()
	if wrote != 0 {
		t.Fatalf("the rotation stored %d floors", wrote)
	}
}

func TestTheOfficeIsRebuiltOnceAndThenLeftAlone(t *testing.T) {
	// A hundred further ticks inside the same day must be inert. The guard is the
	// only thing standing between «rebuilt at midnight» and «rebuilt twenty times
	// a second for ever», and it is one integer comparison, so it is worth
	// pinning that it holds after the boundary rather than only across it.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	_, after := renovating(t, h, []string{acc})

	h.tickAt(t, after)
	rebuilt := h.svc.Floor()

	for i := 0; i < 100; i++ {
		h.tickAt(t, after.Add(time.Duration(i)*time.Minute))
	}

	if got := h.svc.Floor(); got.Layout.ID != rebuilt.Layout.ID || !got.InstalledAt.Equal(rebuilt.InstalledAt) {
		t.Fatalf("a hundred ticks inside one day moved the floor: %+v then %+v", rebuilt, got)
	}
	if got := len(h.tr.framesOfType(TypeOver)); got != 1 {
		t.Fatalf("%d endings for one rebuild", got)
	}
	h.repo.mu.Lock()
	wrote := len(h.repo.layouts)
	h.repo.mu.Unlock()
	if wrote != 0 {
		t.Fatalf("a hundred ticks stored %d floors", wrote)
	}
}

func TestAClockSteppedBackwardsKeepsTheNewerOffice(t *testing.T) {
	// THE GUARD IS `>` AND NOT `!=`, AND THIS IS WHY. An NTP correction or a
	// resumed virtual machine hands the loop an instant EARLIER than the one
	// before it; read as «a different day» that would throw everybody out onto
	// yesterday's floor, and then throw them out again when the clock caught up.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	before, after := renovating(t, h, []string{acc})

	h.svc.mu.Lock()
	h.svc.installedDay = renovationDay(after)
	h.svc.mu.Unlock()
	was := h.svc.Floor()

	h.tickAt(t, before)
	h.tickAt(t, before.Add(-48*time.Hour))

	if got := h.svc.Floor(); got.Layout.ID != was.Layout.ID {
		t.Fatalf("a clock that went backwards rebuilt the office: %q became %q", was.Layout.ID, got.Layout.ID)
	}
	if got := h.svc.Floor().Occupants; got != 1 {
		t.Fatalf("a clock that went backwards evicted somebody: %d left working", got)
	}
	if got := len(h.tr.framesOfType(TypeOver)); got != 0 {
		t.Fatalf("a clock that went backwards ended %d shifts", got)
	}
	h.svc.mu.Lock()
	day := h.svc.installedDay
	h.svc.mu.Unlock()
	if day != renovationDay(after) {
		t.Fatalf("the office now believes it is day %d, it was on %d", day, renovationDay(after))
	}
}

func TestAShiftTooShortToRecordIsStillToldTheOfficeWasRebuilt(t *testing.T) {
	// The MinShiftSeconds rule is about noise on a leaderboard, not about how a
	// shift ended — so a shift that started a moment ago gets no row and is still
	// told, because the over frame does not depend on the row and a browser left
	// watching a stopped office is the failure this whole path exists to avoid.
	acc := uuid.New().String()
	h := start(t, realtime.Member{ConnID: "c1", AccountID: acc})
	ctx := context.Background()
	if _, err := h.svc.StartShift(ctx, acc); err != nil {
		t.Fatal(err)
	}
	after := evening(2026, time.August, 4, 21, 0, 0)
	h.svc.mu.Lock()
	h.svc.installedDay = renovationDay(after) - 1
	h.svc.mu.Unlock()

	h.tickAt(t, after)

	if got := len(h.tr.framesOfType(TypeOver)); got != 1 {
		t.Fatalf("%d over frames — a short shift was thrown out in silence", got)
	}
	select {
	case written := <-h.repo.inserted:
		t.Fatalf("a shift lasting %v seconds was recorded: %+v", written.Seconds, written)
	default:
	}
}

func TestAnIdleProcessRebuildsTheOfficeToo(t *testing.T) {
	// The rotation is the FIRST statement of a tick, above the empty-office
	// return, and this is the claim that placement buys: the plan a splash screen
	// is served is the one that rotates, so somebody clocking in at ten past nine
	// opens the office on the floor their catalogue already describes rather than
	// on yesterday's.
	h := start(t)
	was := h.svc.Config().Office.ID
	after := evening(2026, time.August, 4, 21, 0, 0)
	h.svc.mu.Lock()
	h.svc.installedDay = renovationDay(after) - 1
	h.svc.mu.Unlock()

	h.tickAt(t, after)

	now := h.svc.Config().Office.ID
	if now == was {
		t.Fatalf("an empty office was left on yesterday's floor (%q)", was)
	}
	// And the next shift is joined on exactly that floor rather than on a third
	// one, which is what the client's cached catalogue is compared against.
	w, err := h.svc.StartShift(context.Background(), uuid.New().String())
	if err != nil {
		t.Fatal(err)
	}
	if w.OfficeID != now {
		t.Fatalf("the shift was joined on %q, the catalogue serves %q", w.OfficeID, now)
	}
}

func TestTheDaysOfficeIsTheOneTheBootPathWouldHaveDerived(t *testing.T) {
	// A ROTATION AND A RESTART PRODUCE THE SAME ROOM, which is the property that
	// makes a deploy across the boundary uninteresting: neither process is
	// carrying state the other lacks, because there is no state — both evaluate
	// the same function of the same day.
	after := evening(2026, time.August, 4, 21, 30, 0)
	h := start(t)
	h.svc.mu.Lock()
	h.svc.installedDay = renovationDay(after) - 1
	h.svc.mu.Unlock()
	h.tickAt(t, after)

	rotated := h.svc.Floor()
	booted := dailyFloor(after)
	if rotated.Layout.ID != booted.Layout.ID {
		t.Fatalf("the tick installed %q and a boot would have derived %q", rotated.Layout.ID, booted.Layout.ID)
	}
	if rotated.Source != booted.Source || !rotated.InstalledAt.Equal(booted.InstalledAt) {
		t.Fatalf("the two disagree about provenance: %+v against %+v", rotated, booted)
	}
}

func TestTheHourIsPublishedSoTheSplashScreenCanStateIt(t *testing.T) {
	// The cheatsheet's «Ремонт» block is generated from this number rather than
	// typed, so moving the hour is a backend deploy — the same rule the board
	// window and the tempo ramp already follow.
	if got := BuildConfig(testLayout).Renovation.HourUTC; got != RenovationHourUTC {
		t.Fatalf("the served renovation hour is %d, the office rebuilds at %d", got, RenovationHourUTC)
	}
	if RenovationHourUTC < 0 || RenovationHourUTC > 23 {
		t.Fatalf("%d is not an hour", RenovationHourUTC)
	}
}
