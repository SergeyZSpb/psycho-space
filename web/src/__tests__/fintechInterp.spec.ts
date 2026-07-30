import { describe, expect, it } from 'vitest';
import { DELAY_PERIODS, createInterpolator } from '../lib/fintechInterp';

/**
 * Entity interpolation — the third Gambetta rung, and the one this game shipped
 * without. Every test here is a way the CSS transition it replaces went wrong.
 */

const PERIOD = 50; // the 20 Hz this game publishes at
const DELAY = PERIOD * DELAY_PERIODS;

const at = (x: number) => ({ x, y: 0, grin: 0 });

describe('the interpolator', () => {
  it('draws nothing at all before anything has arrived', () => {
    expect(createInterpolator(PERIOD).at(1000)).toBeNull();
  });

  it('draws between the two samples bracketing the delayed instant', () => {
    const i = createInterpolator(PERIOD);
    i.push(at(0), 1000);
    i.push(at(10), 1100);
    // Drawn at now-DELAY. At now = 1100+DELAY that is 1100 — the newer sample.
    expect(i.at(1100 + DELAY)!.x).toBeCloseTo(10, 6);
    // Half a period earlier is half way between them.
    expect(i.at(1050 + DELAY)!.x).toBeCloseTo(5, 6);
    expect(i.at(1025 + DELAY)!.x).toBeCloseTo(2.5, 6);
  });

  it('follows the served publish rate, so the office can tighten the delay without a client deploy', () => {
    // THE DELAY IS A MULTIPLE OF THE SERVED PERIOD AND NEVER A FIXED NUMBER OF
    // MILLISECONDS. It is what made the office's move from 10 Hz to 20 Hz
    // snapshots cost nothing on this side — the same 1.5 periods went from 150 ms
    // of staleness to 75 ms, which is the difference between spending half the
    // catch radius on being behind and spending a quarter of it.
    for (const period of [100, 50, 25]) {
      const i = createInterpolator(period);
      i.push(at(0), 1000);
      i.push(at(10), 1000 + period);
      // The newest sample is drawn exactly `period * DELAY_PERIODS` after it
      // arrived, and not one millisecond sooner.
      const delay = period * DELAY_PERIODS;
      expect(i.at(1000 + period + delay)!.x).toBeCloseTo(10, 6);
      expect(i.at(1000 + period + delay - period / 2)!.x).toBeCloseTo(5, 6);
    }
  });

  it('is unbothered by jitter, which is the whole point', () => {
    // Samples 100ms apart in intent, arriving at 80, 130, 90 — the pattern that
    // made a fixed 100ms CSS transition stop-start.
    const i = createInterpolator(PERIOD);
    i.push(at(0), 1000);
    i.push(at(10), 1080);
    i.push(at(20), 1210);
    i.push(at(30), 1300);
    // Sampled every 16ms across the whole span, the drawn position must never
    // go backwards and never stand still for a whole frame while a sample is
    // late — both of which a transition-per-arrival does.
    let prev = -Infinity;
    let stalls = 0;
    for (let t = 1000 + DELAY; t <= 1300 + DELAY; t += 16) {
      const x = i.at(t)!.x;
      expect(x).toBeGreaterThanOrEqual(prev - 1e-9);
      if (Math.abs(x - prev) < 1e-9) stalls++;
      prev = x;
    }
    expect(stalls).toBe(0);
  });

  it('rides straight over a dropped frame', () => {
    // The hub discards a slow client's backlog on purpose, so this happens.
    const i = createInterpolator(PERIOD);
    i.push(at(0), 1000);
    i.push(at(20), 1200); // the 1100 sample never arrived
    // Half way through the gap is half way through the distance — a smooth
    // glide, not a stall followed by a jump.
    expect(i.at(1100 + DELAY)!.x).toBeCloseTo(10, 6);
  });

  it('holds rather than extrapolating when the sender goes quiet', () => {
    // A boss who keeps walking on a guess and is then corrected is worse than
    // one who pauses: the guess is the thing being dodged.
    const i = createInterpolator(PERIOD);
    i.push(at(0), 1000);
    i.push(at(10), 1100);
    expect(i.at(5000)!.x).toBeCloseTo(10, 6);
  });

  it('interpolates the grin too, because it is a continuous quantity', () => {
    const i = createInterpolator(PERIOD);
    i.push({ x: 0, y: 0, grin: 0 }, 1000);
    i.push({ x: 0, y: 0, grin: 1 }, 1100);
    expect(i.at(1050 + DELAY)!.grin).toBeCloseTo(0.5, 6);
  });

  it('keeps a bounded buffer however long the shift runs', () => {
    const i = createInterpolator(PERIOD);
    for (let n = 0; n < 500; n++) i.push(at(n), 1000 + n * 100);
    expect(i.size()).toBeLessThanOrEqual(8);
    // And still draws correctly from what it kept.
    expect(i.at(1000 + 499 * 100 + DELAY)!.x).toBeCloseTo(499, 6);
  });

  it('drops an out-of-order arrival rather than sorting it in', () => {
    const i = createInterpolator(PERIOD);
    i.push(at(0), 1000);
    i.push(at(10), 1100);
    i.push(at(99), 1050); // late and out of order
    expect(i.at(1100 + DELAY)!.x).toBeCloseTo(10, 6);
  });

  it('resets between shifts', () => {
    const i = createInterpolator(PERIOD);
    i.push(at(5), 1000);
    i.reset();
    expect(i.at(2000)).toBeNull();
  });
});
