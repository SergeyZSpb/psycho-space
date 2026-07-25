// The reply box is deliberately short — the play screen must never scroll on a
// phone — but дядя Ваня raps up to eight bars, which will not fit. Rather than
// clip the verse (the old behaviour: three lines and the rest silently gone) or
// demand a scroll gesture mid-game, a too-tall verse ROTATES: it rolls upward
// continuously and loops, like post-credits.
//
// The loop is seamless because the markup renders the verse twice and the reel is
// translated by exactly half its height, so the second copy arrives where the
// first began. These helpers own the arithmetic.
//
// PACING. The first cut rolled at 22 px/s and started the instant the reply
// landed, which was unreadable: the opening bar had already begun sliding away
// before the eye reached it, and the rest went past at roughly 440 words per
// minute. Three changes fix that, and the constants below are the whole tuning
// surface:
//
//   1. The verse HOLDS STILL for CREDITS_START_DELAY_SECONDS before it moves, so
//      the first bar can be read from a standstill.
//   2. It then rolls at CREDITS_SPEED_PX_PER_SEC, chosen for a comfortable
//      reading rate rather than for reaching the end quickly.
//   3. It comes to rest after CREDITS_CYCLES passes instead of rotating forever,
//      because permanent motion beside the answer buttons keeps pulling the eye
//      back once the verse has been read. The full text stays reachable by tap
//      after it settles.

/**
 * Scroll speed of the roll, in CSS pixels per second.
 *
 * Derived from a reading rate rather than picked by eye. A verse line is about
 * 21 px tall (0.875 rem at line-height 1.5) and a rap bar runs six to eight
 * words, so 9 px/s surfaces a line every ~2.3 s — on the order of 180 words per
 * minute, a relaxed pace for text that moves while you read it. The previous
 * 22 px/s worked out at roughly 440 wpm, which is why the verse was illegible.
 */
export const CREDITS_SPEED_PX_PER_SEC = 9;

/**
 * How long the verse stands still before the roll starts, in seconds.
 *
 * Without it the reader is behind from the first frame: the opening bar is the
 * one line guaranteed to be on screen at t=0, and it used to start moving
 * immediately. Long enough to read a bar from a standstill, short enough not to
 * strand a player who has already read it — and they can tap to skip regardless.
 */
export const CREDITS_START_DELAY_SECONDS = 2.5;

/** Shortest roll we will play, so a barely-overflowing verse doesn't race. */
export const CREDITS_MIN_SECONDS = 8;

/**
 * Longest roll we will play. A safety net rather than a normal case: at 9 px/s it
 * only binds on a verse over ~800 px tall (forty-odd lines), which the judge is
 * not supposed to produce. Capping it keeps a runaway reply bounded instead of
 * creeping for minutes; tap-to-reveal is the escape hatch if that ever rushes.
 */
export const CREDITS_MAX_SECONDS = 90;

/**
 * How many full passes the verse makes before it settles. Two: one to read, one
 * to catch what was missed. After that the motion stops competing with the
 * options below it.
 */
export const CREDITS_CYCLES = 2;

/**
 * Grace added when computing the settle moment, in seconds, so the state flip
 * lands just AFTER the animation's last frame rather than a frame or two before
 * it — settling early would snap the reel back mid-roll.
 */
const CREDITS_SETTLE_GRACE_SECONDS = 0.25;

/**
 * Seconds for one full cycle, given the height of ONE copy of the verse. The
 * animation translates the two-copy reel by -50%, i.e. by exactly this height, so
 * duration is that distance over the speed, clamped to the floor and the ceiling.
 */
export function creditsDuration(oneCopyPx: number): number {
  if (!Number.isFinite(oneCopyPx) || oneCopyPx <= 0) return CREDITS_MIN_SECONDS;
  const seconds = oneCopyPx / CREDITS_SPEED_PX_PER_SEC;
  const rounded = Math.round(seconds * 10) / 10;
  return Math.min(CREDITS_MAX_SECONDS, Math.max(CREDITS_MIN_SECONDS, rounded));
}

/**
 * Milliseconds from the moment the roll is armed to the moment its last pass is
 * over: the opening hold, then every cycle, plus the settle grace.
 */
export function creditsSettleMs(cycleSeconds: number): number {
  const total =
    CREDITS_START_DELAY_SECONDS + CREDITS_CYCLES * cycleSeconds + CREDITS_SETTLE_GRACE_SECONDS;
  return Math.round(total * 1000);
}

/**
 * Whether a verse needs to roll at all. A couple of pixels of overflow is
 * measurement noise (sub-pixel line heights), not a reason to animate.
 */
export function shouldRoll(contentPx: number, viewportPx: number): boolean {
  return contentPx - viewportPx > 2;
}

/**
 * Whether the reader has asked the platform for reduced motion. Such a reader
 * gets the whole verse at once, in a box they scroll themselves, and nothing on
 * the screen ever moves.
 *
 * Defensive about `matchMedia`: it is missing in some test environments and in
 * very old browsers, and missing means "no preference expressed", i.e. animate.
 */
export function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}
