import { describe, expect, it } from 'vitest';
import {
  CREDITS_MIN_SECONDS,
  CREDITS_SPEED_PX_PER_SEC,
  creditsDuration,
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
