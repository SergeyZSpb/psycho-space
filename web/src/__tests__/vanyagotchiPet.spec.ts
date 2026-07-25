import { describe, expect, it } from 'vitest';
import { clampStat, decayedValue, inTrouble, skewMs, statFraction } from '../lib/vanyagotchiPet';
import type { VanyagotchiStat, VanyagotchiStatValue } from '../api/types';

// These tests exist because this file is a SECOND implementation of arithmetic
// the server already owns, written so a bar creeps between fetches instead of
// jumping. A second implementation of anything is a chance for the two to
// disagree, so the properties pinned here are exactly the ones that would make
// the screen lie: the direction of a fill, the bounds, whose clock is being
// measured against — and, since health became a consequence rather than a chore,
// WHOSE RATE is being drawn.

// The three shipped stats, mirrored from internal/gamevanyagotchi/content.go.
// Copies rather than anything imported, for the same reason the e2e fixtures
// are: a change to the catalogue should have to be noticed here.
//
// hp barely rots on its own. What kills him is the two penalties — an empty beer
// and a full bladder, six an hour each — and every one of them is invisible to
// this file. It never evaluates a penalty; it is TOLD the resulting rate.
const HP: VanyagotchiStat = {
  key: 'hp',
  label: 'здоровье',
  emoji: '❤️',
  min: 0,
  max: 100,
  start: 65,
  decay_per_hour: 1,
  good_high: true,
  warn_at: 30,
  fatal: true,
  penalties: [
    { when_key: 'beer', threshold: 20, above: false, rate_per_hour: 6 },
    { when_key: 'bladder', threshold: 80, above: true, rate_per_hour: 6 },
  ],
};

const BEER: VanyagotchiStat = {
  key: 'beer',
  label: 'пиво',
  emoji: '🍺',
  min: 0,
  max: 100,
  start: 60,
  decay_per_hour: 4,
  good_high: true,
  warn_at: 20,
  fatal: false,
};

// The bladder is the reason the rate is signed rather than a magnitude plus a
// direction: it FILLS. If a negative rate were ever treated as a drain, this is
// the stat that would silently run the wrong way.
const BLADDER: VanyagotchiStat = {
  key: 'bladder',
  label: 'мочевой пузырь',
  emoji: '🚽',
  min: 0,
  max: 100,
  start: 0,
  decay_per_hour: -5,
  good_high: false,
  warn_at: 80,
  fatal: false,
};

const T0 = Date.parse('2026-07-25T12:00:00Z');
const hours = (n: number) => T0 + n * 3_600_000;

/**
 * A stat as the server sends it. `rate` is the drain in force at the moment of
 * the read — penalties folded in — and is left out entirely when a test is
 * about the fallback, because that is exactly how a response without the field
 * arrives.
 */
function stored(value: number, asOfMs = T0, rate?: number): VanyagotchiStatValue {
  const out: VanyagotchiStatValue = { key: 'hp', value, as_of: new Date(asOfMs).toISOString() };
  if (rate !== undefined) out.rate_per_hour = rate;
  return out;
}

describe('decayedValue', () => {
  // THE property of the coupled model: the line drawn between fetches is the
  // one the SERVER said it was on. hp's catalogue rate is 1 an hour; a Ваня
  // with an empty beer is losing 7, and a screen that drew the catalogue rate
  // would show him comfortable for the entire time he was dying.
  it('draws the rate the server sent rather than the catalogue rate', () => {
    expect(decayedValue(HP, stored(100, T0, 3), hours(2))).toBeCloseTo(94, 10);
    expect(decayedValue(HP, stored(100, T0, 3), hours(10))).toBeCloseTo(70, 10);
    // And the catalogue rate is genuinely different, so the numbers above could
    // only have come from the wire.
    expect(HP.decay_per_hour).toBe(1);
  });

  // A response with no rate is an older server or a fixture that predates the
  // field. Drawing the uncoupled slope is wrong slowly; drawing nothing at all
  // would be wrong immediately.
  it('falls back to the catalogue rate when the server sent none', () => {
    expect(decayedValue(HP, stored(100), hours(10))).toBeCloseTo(90, 10);
    expect(decayedValue(BEER, { ...stored(60), key: 'beer' }, hours(5))).toBeCloseTo(40, 10);
  });

  // Zero is a rate, not a missing one — a stat only actions move. `||` here
  // instead of `??` would quietly hand it the catalogue's drain and make a
  // lifetime counter drift.
  it('treats a rate of zero as a rate rather than as an absent one', () => {
    expect(decayedValue(HP, stored(42, T0, 0), hours(50))).toBe(42);
  });

  it('fills a stat whose rate is negative', () => {
    const bladder = { ...stored(0, T0, -5), key: 'bladder' };
    expect(decayedValue(BLADDER, bladder, hours(4))).toBeCloseTo(20, 10);
  });

  // Splitting an interval must change nothing, because that is the property the
  // whole lazy model rests on: a client that redraws once a second and one that
  // redraws once an hour have to agree with each other and with the server.
  it('gives the same answer however the interval is split', () => {
    const whole = decayedValue(HP, stored(100, T0, 3), hours(4));
    const partial = decayedValue(HP, stored(100, T0, 3), hours(1));
    const rest = decayedValue(HP, stored(partial, hours(1), 3), hours(4));
    expect(rest).toBeCloseTo(whole, 10);
  });

  // The penalties are the whole reason the rate is on the wire, and this is what
  // it buys: with both needs unmet hp falls at 13 an hour, so a Ваня who would
  // have had two and a half days at the catalogue rate is gone in five hours.
  // A client drawing `decay_per_hour` would still be showing him at 59.
  it('reaches the floor far sooner at a penalised rate than the catalogue predicts', () => {
    const penalised = 1 + 6 + 6;
    expect(decayedValue(HP, stored(65, T0, penalised), hours(5))).toBe(0);
    expect(decayedValue(HP, stored(65, T0, penalised), hours(6))).toBe(0);
    // Same pair, no rate on the wire: the uncoupled slope has barely moved.
    expect(decayedValue(HP, stored(65), hours(6))).toBeCloseTo(59, 10);
  });

  it('clamps at the floor rather than going negative', () => {
    expect(decayedValue(HP, stored(100, T0, 3), hours(1000))).toBe(0);
  });

  it('clamps at the ceiling when filling', () => {
    const bladder = { ...stored(0, T0, -5), key: 'bladder' };
    expect(decayedValue(BLADDER, bladder, hours(1000))).toBe(100);
  });

  // A device clock that is behind the server's produces a negative interval.
  // Winding the value back up would be inventing state the server never had.
  it('never runs backwards', () => {
    expect(decayedValue(HP, stored(80, T0, 3), hours(-3))).toBe(80);
    expect(decayedValue(HP, stored(80, T0, 3), T0)).toBe(80);
  });

  it('falls back to the stored value when as_of is unparseable', () => {
    expect(
      decayedValue(HP, { key: 'hp', value: 61, as_of: 'not a date', rate_per_hour: 13 }, hours(5)),
    ).toBe(61);
  });
});

describe('skewMs', () => {
  // The elapsed time that matters is measured on the SERVER's clock: as_of is a
  // server timestamp, so subtracting it from a phone that is three minutes fast
  // would draw three minutes of decay that never happened.
  it('reports how far the device clock is ahead', () => {
    expect(skewMs('2026-07-25T12:00:00Z', hours(0) + 180_000)).toBe(180_000);
    expect(skewMs('2026-07-25T12:00:00Z', hours(0) - 60_000)).toBe(-60_000);
  });

  it('assumes no skew rather than NaN when the server time is unreadable', () => {
    expect(skewMs('', 12_345)).toBe(0);
  });

  // The two together are the real contract: correcting for skew must produce the
  // same value a correctly-set clock would have produced.
  it('cancels the device clock out of the reading', () => {
    const skew = skewMs(new Date(T0).toISOString(), hours(0) + 600_000);
    const asIfOnServer = hours(2) + 600_000 - skew;
    expect(decayedValue(HP, stored(100, T0, 3), asIfOnServer)).toBeCloseTo(94, 10);
  });
});

describe('clampStat', () => {
  it('holds the bounds', () => {
    expect(clampStat(HP, -5)).toBe(0);
    expect(clampStat(HP, 140)).toBe(100);
    expect(clampStat(HP, 42)).toBe(42);
  });

  // A NaN reaching the DOM would render as "NaN" in the bar and as a zero-width
  // fill, which reads as a bug in the game rather than in one number.
  it('falls back to the starting value for a value that is not a number', () => {
    expect(clampStat(HP, Number.NaN)).toBe(HP.start);
    expect(clampStat(HP, Number.POSITIVE_INFINITY)).toBe(HP.start);
  });
});

describe('statFraction', () => {
  // Length always measures min → max, whichever end is the happy one: a bladder
  // filling up must visibly fill up rather than visibly empty.
  it('measures towards the maximum regardless of which end is good', () => {
    expect(statFraction(HP, 25)).toBeCloseTo(0.25, 10);
    expect(statFraction(BLADDER, 25)).toBeCloseTo(0.25, 10);
  });

  it('stays within 0..1', () => {
    expect(statFraction(HP, -50)).toBe(0);
    expect(statFraction(HP, 500)).toBe(1);
  });

  it('does not divide by a zero span', () => {
    expect(statFraction({ ...HP, min: 5, max: 5 }, 5)).toBe(0);
  });
});

describe('inTrouble', () => {
  // Which values count as trouble is catalogue data in both directions, so the
  // stylesheet never learns that 30 is a bad amount of health.
  it('reads low as trouble when high is good', () => {
    expect(inTrouble(HP, 29)).toBe(true);
    expect(inTrouble(HP, 31)).toBe(false);
  });

  it('reads high as trouble when low is good', () => {
    expect(inTrouble(BLADDER, 81)).toBe(true);
    expect(inTrouble(BLADDER, 79)).toBe(false);
  });

  // Each driver's warning threshold is the SAME number as the penalty it
  // triggers on hp, which is what makes an amber bar mean something: it turns at
  // exactly the moment it starts costing him health. Pinned here because the two
  // numbers live in different halves of the catalogue and could drift apart.
  it('turns amber exactly where the penalty on health begins', () => {
    for (const penalty of HP.penalties ?? []) {
      const driver = [BEER, BLADDER].find((s) => s.key === penalty.when_key);
      expect(driver, `no fixture for driver ${penalty.when_key}`).toBeDefined();
      expect(driver?.warn_at).toBe(penalty.threshold);
      // And the driver's warning direction is the direction the penalty fires
      // in: beer is trouble when LOW and penalises below its threshold, the
      // bladder is trouble when HIGH and penalises above.
      expect(driver ? inTrouble(driver, penalty.threshold + (penalty.above ? 1 : -1)) : false).toBe(
        true,
      );
    }
  });
});

// The `condition` block that stood here is gone with the function it tested:
// the pose every dot is drawn in now comes down the roster frame, decided once
// on the server, so the properties it pinned — dead beats every value, only the
// fatal stat decides — belong to display_test.go and to nothing in this bundle.
