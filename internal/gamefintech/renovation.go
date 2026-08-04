package gamefintech

import (
	"context"
	"log/slog"
	"time"
)

// THE OFFICE IS REBUILT EVERY NIGHT, AND THE DAY IT BELONGS TO IS ARITHMETIC.
//
// At 21:00 UTC — midnight in Moscow, where everybody who plays this lives — the
// floor is redrawn and everybody standing on it is thrown out with the «РЕМОНТ»
// ending. That is a rule of the game rather than a maintenance job, so it is
// stated on the splash screen (RenovationConfig on the served catalogue) and
// nothing about it is scheduled.
//
// THE DAY'S OFFICE IS A PURE FUNCTION OF THE DAY, NEVER A STORED FACT. A
// renovation day is a number — how many whole days have passed since the epoch,
// bucketed on 21:00 rather than on midnight — and the floor is Generate() over a
// seed derived from that number. NO FLOOR IS WRITTEN BECAUSE TIME PASSED,
// nothing is read to find out what today's office is, and two processes running
// the same binary on the same day are standing in the same room without having
// agreed on anything.
//
// Said exactly, because the loose version of it is not true: a rotation DOES
// write, once per occupant, because it ENDS THEIR SHIFTS — and an ended shift is
// recorded here exactly as one the лысый ended is, on the same buffered channel
// and the same single writer. What never happens is a row appearing to say what
// the office looks like today. That distinction is the one this file rests on.
//
// It keeps two claims this project makes elsewhere LITERALLY true rather than
// nearly true:
//
//   - ADR-038 — time-varying state is computed on read and never ticked. The
//     floor for a given day is a closed form over the clock, exactly as a pet's
//     hunger is; what the tick below does is OBSERVE it, not maintain it.
//   - «no Redis, no cron, no worker and no queue» (docs/ARCHITECTURE.md). There
//     is still no scheduler anywhere in the system, and this added no goroutine:
//     it is two integers compared on a loop that was already running.
//
// And it borrows one: ADR-034's tick is a PARAMETER rather than a ticker built
// where it is used, which is what lets «it is now one second past nine in the
// evening» be a thing a test says instead of something it waits a day for.
//
// WHY THE TICK IS THE READER, AND NOT A TIMER OR A LAZY READ. A rotation is
// DESTRUCTIVE — it ends other people's shifts — so it cannot be materialised by
// whoever happens to read next, the way a pet's death is: «the first person to
// open the page ends everybody else's shift» is a rule with a person's identity
// in it, and a shift would go on earning money for as long as nobody looked. A
// goroutine with a timer would work and would be the fourth thing in this
// process with its own clock, with its own shutdown, its own test seam and its
// own failure mode when it dies quietly. The simulation loop is already running
// at 20 Hz, already holds the mutex that owns the floor, and already knows how
// to evict a room and tell everybody why — so it observes the day turning over
// as its first act on every tick, and the whole feature is a comparison of two
// integers.
//
// WHAT A RESTART, A DEPLOY AND A MISSED 21:00 ALL PRODUCE: the same room. The
// boot path (EnsureLayout) runs the identical arithmetic, so a process that
// starts at 22:00 computes exactly what a process that ran through 21:00
// computed, and a deploy that overlaps the boundary leaves the two processes
// agreeing rather than racing. There is no catch-up: a process that was down for
// three days installs today's office and not four of them in a row, because
// «today's office» was never a queue of events.

// RenovationHourUTC is when the building does it: 21:00 UTC, which is midnight
// in Moscow.
//
// UTC ON THE WIRE AND MOSCOW IN THE HEAD. The server owns one clock and it is
// UTC (ADR-038's rule about `now`), so the constant is UTC and the splash screen
// converts — Moscow has been a permanent UTC+3 since 2014, so the conversion is
// an addition rather than a timezone database. Midnight local is the hour with
// the fewest people standing in the office and the one a player can predict
// without being told a number.
const RenovationHourUTC = 21

// secondsPerDay is a day, for the bucketing below. Written out rather than
// derived from time.Duration so the arithmetic stays integer seconds throughout,
// which is what makes the boundary exact rather than nearly exact.
const secondsPerDay = 24 * 60 * 60

// renovationDay is which office day an instant belongs to: how many whole
// renovation days have passed since the Unix epoch.
//
// The shift by RenovationHourUTC is the whole of it. Subtracting the hour moves
// the boundary the division falls on, so 20:59:59 and 21:00:00 on the same date
// land either side of it, and a UTC midnight in between changes nothing.
//
// IT IS INDEPENDENT OF THE Time'S LOCATION, because Unix() is an absolute
// instant — the same moment expressed in Moscow and in UTC answers the same day,
// which is one fewer thing for a caller to get wrong.
func renovationDay(t time.Time) int64 {
	return t.Add(-RenovationHourUTC*time.Hour).Unix() / secondsPerDay
}

// dayStart is the instant a renovation day began: 21:00 UTC on its own date.
//
// It is what a derived floor reports as its installation time, so the admin
// section says «поставлен вчера в 21:00» — when the floor actually came into
// force — rather than whenever this process happened to start. That also makes a
// restart invisible: two processes that derived the same day's floor agree about
// its id AND about when it arrived.
func dayStart(day int64) time.Time {
	return time.Unix(day*secondsPerDay+RenovationHourUTC*60*60, 0).UTC()
}

// seedForDay turns a day into the seed its office is drawn from.
//
// THE MULTIPLICATION IS DECORRELATION AND NOTHING ELSE. Consecutive days are
// consecutive integers, and handing a generator a sequence of adjacent seeds is
// a habit that depends on how well that particular generator scrambles its seed
// — a thing this package should not have to know or to keep knowing. Multiplying
// by the 64-bit golden-ratio constant costs one instruction a day and makes
// adjacent days as unrelated as any other pair of seeds.
//
// THERE IS NO MASK, deliberately. `&^ (1 << 63)` does not compile against an
// int64 and would have to be spelled through a uint64 anyway, and there is
// nothing to fix: rand.NewSource takes a negative seed perfectly well and
// produces exactly as good a stream. cryptoSeed masks because a seed drawn at
// random ends up in a log line and a negative one reads like a bug; nothing logs
// this one, because the DAY is the interesting number and the seed is derived
// from it.
func seedForDay(day int64) int64 {
	return int64(uint64(day) * 0x9E3779B97F4A7C15) //nolint:gosec // the wrap is the hash: any 64-bit pattern is a valid seed, and rand.NewSource takes a negative one.
}

// dailyLayout is the office a day is entitled to.
//
// PURE WITH RESPECT TO A FIXED BINARY, and that limitation is worth stating
// plainly because it is easy to overclaim: the same day computed twice by the
// same build gives the same floor, on every machine and after every deploy — but
// EDITING Generate CHANGES EVERY DAY'S OFFICE, retroactively, including today's.
// A deploy that touches the generator therefore swaps the geometry under
// whoever is standing on it with no eviction and no «РЕМОНТ», until the next
// 21:00 puts everybody on the new build's version of the same day. That is
// tolerable — a floor is furniture and the geometry is re-served on the
// catalogue — but it is not reproducibility, and the seed in a log line will not
// bring back what an older binary drew.
func dailyLayout(day int64) (Layout, error) {
	return Generate(seedForDay(day))
}

// dailyFloor is the day's office as a StoredLayout that was never stored: the
// geometry, SourceDaily, and the 21:00 it came into force at.
//
// A DRAW THAT FAILS FALLS BACK TO THE STARTING FLOOR rather than to nothing.
// Generate refuses a seed that produced too thin a floor (ErrThinFloor), and
// while no day in a five-year sweep does that (TestEveryDailyOfficeForFiveYears
// IsPlayable), the honest answer to one that did is the hand-made floor this
// game shipped with — which always exists and always validates. Refusing to boot
// over a game's furniture would take the landing page, the wishlist and three
// other games down with it.
func dailyFloor(now time.Time) StoredLayout {
	day := renovationDay(now)
	l, err := dailyLayout(day)
	if err != nil {
		slog.Error("gamefintech: the day's office could not be drawn, standing up on the starting floor",
			"err", err, "day", day)
		return StoredLayout{Layout: StartingLayout.WithID(), Source: SourceStarting, InstalledAt: dayStart(day)}
	}
	return StoredLayout{Layout: l, Source: SourceDaily, InstalledAt: dayStart(day)}
}

// rotateIfDue rebuilds the office when the tick has carried the clock into a new
// renovation day. It is the FIRST statement of step, for two reasons: an idle
// process has to rotate too — the catalogue a splash screen reads comes from the
// same plan, so a floor that only changed when somebody was inside would hand a
// stale office to the next person to clock in — and a tick that is going to
// empty the room should do it before it simulates a further fiftieth of a second
// in a room that is about to be taken away.
//
// THE GUARD IS `>` AND NOT `!=`, WHICH IS THE BACKWARDS-CLOCK GUARD. An NTP
// correction or a resumed virtual machine can hand the loop an instant EARLIER
// than the one before it; an inequality would read that as a new day, evict
// everybody onto yesterday's floor, and then evict them again when the clock
// caught up. Comparing for «later than the day in force» makes a clock that goes
// backwards a no-op — the office keeps the newer floor, which is the one
// everybody's client is already drawing.
//
// THE GENERATION HAPPENS WITH NO LOCK HELD. It is 0.58 ms of search against a
// 50 ms tick, and this package's whole design is that the mutex the simulation
// takes twenty times a second is never held across anything slower than a slice
// append. The consequence is a window in which an admin's own Install could land
// between the decision and the swap; it loses to the day, which is the same
// outcome as having pressed the button a second earlier, and both floors are
// valid.
func (s *Service) rotateIfDue(ctx context.Context, now time.Time) {
	day := renovationDay(now)
	s.mu.Lock()
	due := day > s.installedDay
	s.mu.Unlock()
	if !due {
		return
	}

	l, err := dailyLayout(day)
	if err != nil {
		// THE DAY IS RECORDED ANYWAY, AND THAT IS THE WHOLE OF THE RETRY POLICY.
		// dailyLayout is a pure function of the day, so an attempt that failed
		// cannot succeed on the next tick — retrying would be the same refusal
		// twenty times a second and a log line with it, for the rest of the day.
		// The office keeps the floor it has, which is strictly better than being
		// evicted onto nothing, and the next day draws its own.
		s.mu.Lock()
		s.installedDay = day
		s.mu.Unlock()
		slog.ErrorContext(ctx, "gamefintech: the day's office could not be drawn, keeping the floor in force",
			"err", err, "day", day)
		return
	}

	s.putInForce(ctx, l, SourceDaily, dayStart(day))
}
