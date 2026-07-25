import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { effectScope } from 'vue';
import { useVersePacing } from '../composables/useVersePacing';
import type { VersePacing } from '../composables/useVersePacing';
import {
  CREDITS_CYCLES,
  CREDITS_START_DELAY_SECONDS,
  creditsDuration,
  creditsSettleMs,
} from '../lib/gameKhimkiCredits';

// Heights for a verse that overflows its box, and for one that fits. The box is
// the short window the play screen can spare; the tall verse is roughly eight
// rapped bars.
const BOX_PX = 120;
const TALL_PX = 300;
const SHORT_PX = 90;

describe('verse pacing sequence', () => {
  let pacing: VersePacing;
  let scope: ReturnType<typeof effectScope>;
  let reduced: boolean;

  beforeEach(() => {
    vi.useFakeTimers();
    reduced = false;
    scope = effectScope();
    // `useVersePacing` registers a scope-disposal hook, so it has to be created
    // inside a scope for the unmount behaviour to be exercised at all.
    pacing = scope.run(() => useVersePacing({ reducedMotion: () => reduced }))!;
  });

  afterEach(() => {
    scope.stop();
    vi.useRealTimers();
  });

  it('does nothing at all for a verse that fits its box', () => {
    pacing.start(SHORT_PX, BOX_PX);
    expect(pacing.overflows.value).toBe(false);
    expect(pacing.rolling.value).toBe(false);
    // Nothing hidden means nothing to skip to, so no control is offered.
    expect(pacing.skippable.value).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('holds the verse still before the roll starts', () => {
    pacing.start(TALL_PX, BOX_PX);
    expect(pacing.rolling.value).toBe(true);
    // The hold is an animation-delay rather than a timer: the reel is armed but
    // the browser will not move it until the delay has passed.
    expect(pacing.reelStyle.value?.animationDelay).toBe(`${CREDITS_START_DELAY_SECONDS}s`);
    expect(CREDITS_START_DELAY_SECONDS).toBeGreaterThan(0);
  });

  it('binds the measured duration and the pass count onto the reel', () => {
    pacing.start(TALL_PX, BOX_PX);
    expect(pacing.seconds.value).toBe(creditsDuration(TALL_PX));
    expect(pacing.reelStyle.value?.animationDuration).toBe(`${creditsDuration(TALL_PX)}s`);
    expect(pacing.reelStyle.value?.animationIterationCount).toBe(`${CREDITS_CYCLES}`);
  });

  it('gives a taller verse a longer roll than a shorter one', () => {
    pacing.start(200, BOX_PX);
    const shorter = pacing.seconds.value;
    pacing.start(600, BOX_PX);
    expect(pacing.seconds.value).toBeGreaterThan(shorter);
  });

  it('keeps rolling until the hold and every pass are done, then settles', () => {
    pacing.start(TALL_PX, BOX_PX);
    const settleMs = creditsSettleMs(pacing.seconds.value);

    vi.advanceTimersByTime(settleMs - 1);
    expect(pacing.rolling.value).toBe(true);

    vi.advanceTimersByTime(1);
    expect(pacing.rolling.value).toBe(false);
    // Settled is not the same as read: the verse is still too tall for the box,
    // so the reader can still ask for the whole thing.
    expect(pacing.skippable.value).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('skips straight to the full text on demand, mid-roll', () => {
    pacing.start(TALL_PX, BOX_PX);
    vi.advanceTimersByTime(1000);

    pacing.reveal();
    expect(pacing.revealed.value).toBe(true);
    expect(pacing.rolling.value).toBe(false);
    // Everything is on screen now, so the control stops being offered.
    expect(pacing.skippable.value).toBe(false);
    // ...and the settle timer went with it.
    expect(vi.getTimerCount()).toBe(0);

    vi.advanceTimersByTime(creditsSettleMs(creditsDuration(TALL_PX)) * 2);
    expect(pacing.revealed.value).toBe(true);
    expect(pacing.rolling.value).toBe(false);
  });

  it('renders immediately and never animates under reduced motion', () => {
    reduced = true;
    pacing.start(TALL_PX, BOX_PX);

    expect(pacing.rolling.value).toBe(false);
    expect(pacing.reelStyle.value).toBeUndefined();
    // The whole verse is available at once, in a box the reader scrolls.
    expect(pacing.revealed.value).toBe(true);
    expect(pacing.overflows.value).toBe(true);
    // No motion means no sequence, so nothing was scheduled to end one.
    expect(vi.getTimerCount()).toBe(0);
  });

  it('reveals nothing for a reduced-motion reader whose verse already fits', () => {
    reduced = true;
    pacing.start(SHORT_PX, BOX_PX);
    expect(pacing.revealed.value).toBe(false);
    expect(pacing.overflows.value).toBe(false);
  });
});

describe('verse pacing never leaks or interleaves timers', () => {
  let reduced: boolean;

  beforeEach(() => {
    vi.useFakeTimers();
    reduced = false;
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const create = () => {
    const scope = effectScope();
    const pacing = scope.run(() => useVersePacing({ reducedMotion: () => reduced }))!;
    return { scope, pacing };
  };

  it('replaces the previous turn rather than running two sequences at once', () => {
    const { scope, pacing } = create();
    pacing.start(600, BOX_PX); // a long verse, long settle
    expect(vi.getTimerCount()).toBe(1);

    const firstSettle = creditsSettleMs(pacing.seconds.value);
    vi.advanceTimersByTime(1000);

    // Дядя Ваня answers again before the first verse finished rolling.
    pacing.start(TALL_PX, BOX_PX);
    expect(vi.getTimerCount()).toBe(1); // the old one was cleared, not stacked
    const secondSettle = creditsSettleMs(pacing.seconds.value);
    expect(secondSettle).toBeLessThan(firstSettle);

    // The first verse's settle moment must not stop the second verse's roll.
    vi.advanceTimersByTime(secondSettle - 1);
    expect(pacing.rolling.value).toBe(true);
    vi.advanceTimersByTime(1);
    expect(pacing.rolling.value).toBe(false);

    scope.stop();
  });

  it('clears a revealed state when the next turn arrives', () => {
    const { scope, pacing } = create();
    pacing.start(TALL_PX, BOX_PX);
    pacing.reveal();
    expect(pacing.revealed.value).toBe(true);

    // A new reply is a new verse: it must not inherit the last one's expanded box.
    pacing.start(TALL_PX, BOX_PX);
    expect(pacing.revealed.value).toBe(false);
    expect(pacing.rolling.value).toBe(true);

    scope.stop();
  });

  it('cancel() abandons the sequence and leaves nothing pending', () => {
    const { scope, pacing } = create();
    pacing.start(TALL_PX, BOX_PX);
    expect(vi.getTimerCount()).toBe(1);

    pacing.cancel();
    expect(vi.getTimerCount()).toBe(0);
    expect(pacing.rolling.value).toBe(false);
    expect(pacing.overflows.value).toBe(false);
    expect(pacing.skippable.value).toBe(false);

    scope.stop();
  });

  it('drops the pending timer when the view goes away', () => {
    const { scope, pacing } = create();
    pacing.start(TALL_PX, BOX_PX);
    expect(vi.getTimerCount()).toBe(1);

    // Leaving /app/game-khimki mid-roll must not leave a timer behind holding a
    // reference to a component that no longer exists.
    scope.stop();
    expect(vi.getTimerCount()).toBe(0);
  });
});
