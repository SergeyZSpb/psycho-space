import { describe, expect, it } from 'vitest';
import {
  clampStat,
  condition,
  decayedValue,
  inTrouble,
  skewMs,
  statFraction,
} from '../lib/vanyagotchiPet';
import type { VanyagotchiStat, VanyagotchiStatValue } from '../api/types';

// These tests exist because this file is a SECOND implementation of arithmetic
// the server already owns, written so a bar creeps between fetches instead of
// jumping. A second implementation of anything is a chance for the two to
// disagree, so the properties pinned here are exactly the ones that would make
// the screen lie: the direction of a fill, the bounds, and whose clock is being
// measured against.

const HP: VanyagotchiStat = {
  key: 'hp',
  label: 'здоровье',
  emoji: '❤️',
  min: 0,
  max: 100,
  start: 100,
  decay_per_hour: 3,
  good_high: true,
  warn_at: 30,
  fatal: true,
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
  warn_at: 70,
  fatal: false,
};

const T0 = Date.parse('2026-07-25T12:00:00Z');
const hours = (n: number) => T0 + n * 3_600_000;

function stored(value: number, asOfMs = T0): VanyagotchiStatValue {
  return { key: 'hp', value, as_of: new Date(asOfMs).toISOString() };
}

describe('decayedValue', () => {
  it('drains at exactly the catalogue rate', () => {
    expect(decayedValue(HP, stored(100), hours(2))).toBeCloseTo(94, 10);
    expect(decayedValue(HP, stored(100), hours(10))).toBeCloseTo(70, 10);
  });

  it('fills a stat whose rate is negative', () => {
    const bladder = { ...stored(0), key: 'bladder' };
    expect(decayedValue(BLADDER, bladder, hours(4))).toBeCloseTo(20, 10);
  });

  // Splitting an interval must change nothing, because that is the property the
  // whole lazy model rests on: a client that redraws once a second and one that
  // redraws once an hour have to agree with each other and with the server.
  it('gives the same answer however the interval is split', () => {
    const whole = decayedValue(HP, stored(100), hours(4));
    const partial = decayedValue(HP, stored(100), hours(1));
    const rest = decayedValue(HP, stored(partial, hours(1)), hours(4));
    expect(rest).toBeCloseTo(whole, 10);
  });

  it('clamps at the floor rather than going negative', () => {
    expect(decayedValue(HP, stored(100), hours(1000))).toBe(0);
  });

  it('clamps at the ceiling when filling', () => {
    const bladder = { ...stored(0), key: 'bladder' };
    expect(decayedValue(BLADDER, bladder, hours(1000))).toBe(100);
  });

  // A device clock that is behind the server's produces a negative interval.
  // Winding the value back up would be inventing state the server never had.
  it('never runs backwards', () => {
    expect(decayedValue(HP, stored(80), hours(-3))).toBe(80);
    expect(decayedValue(HP, stored(80), T0)).toBe(80);
  });

  it('falls back to the stored value when as_of is unparseable', () => {
    expect(decayedValue(HP, { key: 'hp', value: 61, as_of: 'not a date' }, hours(5))).toBe(61);
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
    expect(decayedValue(HP, stored(100), asIfOnServer)).toBeCloseTo(94, 10);
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
    expect(clampStat(HP, Number.NaN)).toBe(100);
    expect(clampStat(HP, Number.POSITIVE_INFINITY)).toBe(100);
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
    expect(inTrouble(BLADDER, 71)).toBe(true);
    expect(inTrouble(BLADDER, 69)).toBe(false);
  });
});

describe('condition', () => {
  const defs = [HP, BLADDER];

  // Death is a recorded fact the server sends, not "hp happens to be zero now",
  // which is why `alive` wins over every value.
  it('is dead whenever the server says so', () => {
    expect(condition(defs, new Map([['hp', 100]]), false)).toBe('dead');
  });

  it('is poorly when the fatal stat is in trouble', () => {
    expect(condition(defs, new Map([['hp', 12]]), true)).toBe('poorly');
  });

  // A worrying bladder is not a health problem: only the fatal stat decides how
  // he looks, or every stat added later would silently change his face.
  it('ignores a non-fatal stat in trouble', () => {
    expect(
      condition(
        defs,
        new Map([
          ['hp', 90],
          ['bladder', 95],
        ]),
        true,
      ),
    ).toBe('fine');
  });

  it('is fine when there is nothing to go on', () => {
    expect(condition(defs, new Map(), true)).toBe('fine');
  });
});
