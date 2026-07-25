import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  CREDITS_CYCLES,
  CREDITS_MAX_SECONDS,
  CREDITS_MIN_SECONDS,
  CREDITS_SPEED_PX_PER_SEC,
  CREDITS_START_DELAY_SECONDS,
  creditsDuration,
  creditsSettleMs,
  prefersReducedMotion,
  shouldRoll,
} from '../lib/gameKhimkiCredits';

describe('post-credits roll for an over-tall verse', () => {
  it('rolls only when the verse genuinely overflows', () => {
    expect(shouldRoll(200, 120)).toBe(true);
    expect(shouldRoll(120, 120)).toBe(false);
    // Sub-pixel line heights leave a pixel or two of slack; that is noise, not a
    // reason to start animating.
    expect(shouldRoll(121, 120)).toBe(false);
    expect(shouldRoll(123, 120)).toBe(true);
  });

  it('keeps a constant reading speed regardless of verse length', () => {
    // A cycle travels exactly one copy's height, so duration scales with it.
    const tall = creditsDuration(CREDITS_SPEED_PX_PER_SEC * 20);
    const taller = creditsDuration(CREDITS_SPEED_PX_PER_SEC * 40);
    expect(tall).toBeCloseTo(20, 1);
    expect(taller).toBeCloseTo(40, 1);
  });

  it('never races through a barely-overflowing verse', () => {
    expect(creditsDuration(10)).toBe(CREDITS_MIN_SECONDS);
    expect(creditsDuration(0)).toBe(CREDITS_MIN_SECONDS);
  });

  it('survives a measurement that never happened', () => {
    // A hidden or not-yet-laid-out element measures 0 or NaN; it must not produce
    // an animation-duration of 0s, which would spin the verse illegibly.
    expect(creditsDuration(Number.NaN)).toBe(CREDITS_MIN_SECONDS);
    expect(creditsDuration(-50)).toBe(CREDITS_MIN_SECONDS);
  });
});

// The complaint that produced this pacing: "it rotates too fast too quickly (not
// able to read 1st line + have to really read quickly)". These lock in the two
// halves of the answer — a standstill at the start, and a readable speed after.
describe('verse pacing is slow enough to read', () => {
  it('holds the verse still before it starts to move', () => {
    // The opening bar is the one line guaranteed to be on screen at t=0. Rolling
    // from the first frame is exactly what made it unreadable.
    expect(CREDITS_START_DELAY_SECONDS).toBeGreaterThanOrEqual(1.5);
  });

  it('rolls at a human reading rate, not a credits-crawl sprint', () => {
    // A verse line is ~21px tall; at this speed a line must last well over a
    // second. The original 22px/s gave 0.95s a line, i.e. roughly 440 wpm.
    const secondsPerLine = 21 / CREDITS_SPEED_PX_PER_SEC;
    expect(secondsPerLine).toBeGreaterThan(2);
  });

  it('gives a taller verse proportionally more time than a shorter one', () => {
    // Both above the floor and below the ceiling, so the clamps are not what is
    // being measured here — the proportionality is.
    const shorter = creditsDuration(CREDITS_SPEED_PX_PER_SEC * 15);
    const taller = creditsDuration(CREDITS_SPEED_PX_PER_SEC * 30);
    expect(taller).toBeGreaterThan(shorter);
    expect(taller).toBeCloseTo(shorter * 2, 1);
  });

  it('clamps a runaway verse to the ceiling', () => {
    // Forty-odd lines is not a verse the judge should produce, but if it does,
    // the roll has to terminate in a bounded time rather than creep for minutes.
    expect(creditsDuration(CREDITS_SPEED_PX_PER_SEC * 10_000)).toBe(CREDITS_MAX_SECONDS);
    expect(creditsDuration(Number.POSITIVE_INFINITY)).toBe(CREDITS_MIN_SECONDS); // not finite
  });

  it('settles only after the hold and every pass have elapsed', () => {
    const cycle = 12;
    const settle = creditsSettleMs(cycle);
    // Strictly later than the hold plus all the passes, so the state flip lands
    // after the animation's last frame and never snaps the reel back mid-roll.
    expect(settle).toBeGreaterThan((CREDITS_START_DELAY_SECONDS + CREDITS_CYCLES * cycle) * 1000);
    // ...but not so much later that the verse sits frozen mid-roll for a beat.
    expect(settle).toBeLessThan((CREDITS_START_DELAY_SECONDS + CREDITS_CYCLES * cycle + 1) * 1000);
  });

  it('makes a longer verse take longer to settle', () => {
    expect(creditsSettleMs(30)).toBeGreaterThan(creditsSettleMs(10));
  });
});

describe('reduced-motion probe', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const withMatchMedia = (matches: boolean) => {
    const matchMedia = vi.fn(() => ({ matches }) as MediaQueryList);
    vi.stubGlobal('window', { matchMedia });
    return matchMedia;
  };

  it('reports the preference when the platform expresses one', () => {
    const matchMedia = withMatchMedia(true);
    expect(prefersReducedMotion()).toBe(true);
    expect(matchMedia).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)');
  });

  it('reports no preference when the platform expresses none', () => {
    withMatchMedia(false);
    expect(prefersReducedMotion()).toBe(false);
  });

  it('treats a missing matchMedia as no preference rather than throwing', () => {
    // Some environments (and very old browsers) have no matchMedia at all; the
    // CSS media query is the backstop there, so the probe must not blow up.
    vi.stubGlobal('window', {});
    expect(prefersReducedMotion()).toBe(false);
  });
});
