// The reply box is deliberately short — the play screen must never scroll on a
// phone — but дядя Ваня raps up to eight bars, which will not fit. Rather than
// clip the verse (the old behaviour: three lines and the rest silently gone) or
// demand a scroll gesture mid-game, a too-tall verse ROTATES: it rolls upward
// continuously and loops, like post-credits.
//
// The loop is seamless because the markup renders the verse twice and the reel is
// translated by exactly half its height, so the second copy arrives where the
// first began. These helpers own the arithmetic.

/** Scroll speed of the roll, in CSS pixels per second. Slow enough to read. */
export const CREDITS_SPEED_PX_PER_SEC = 22;

/** Shortest roll we will play, so a barely-overflowing verse doesn't race. */
export const CREDITS_MIN_SECONDS = 6;

/**
 * Seconds for one full cycle, given the height of ONE copy of the verse. The
 * animation translates the two-copy reel by -50%, i.e. by exactly this height, so
 * duration is that distance over the speed.
 */
export function creditsDuration(oneCopyPx: number): number {
  if (!Number.isFinite(oneCopyPx) || oneCopyPx <= 0) return CREDITS_MIN_SECONDS;
  const seconds = oneCopyPx / CREDITS_SPEED_PX_PER_SEC;
  return Math.max(CREDITS_MIN_SECONDS, Math.round(seconds * 10) / 10);
}

/**
 * Whether a verse needs to roll at all. A couple of pixels of overflow is
 * measurement noise (sub-pixel line heights), not a reason to animate.
 */
export function shouldRoll(contentPx: number, viewportPx: number): boolean {
  return contentPx - viewportPx > 2;
}
