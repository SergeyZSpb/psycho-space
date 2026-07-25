// Pacing state for дядя Ваня's rapped reply — the "post-credits" roll described
// in lib/gameKhimkiCredits.ts.
//
// This owns the part that is a state machine rather than arithmetic: whether the
// reel is rolling, whether the reader has skipped to the full text, and the one
// timer that retires the roll after its last pass. It lives apart from
// GameKhimkiView.vue so the sequence can be driven directly under fake timers —
// the view itself is covered by Playwright, not by vitest (see vite.config.ts).
//
// Lifecycle, per reply:
//
//   start(contentPx, viewportPx)
//     ├─ verse fits            -> idle, nothing animates, nothing to skip
//     ├─ reduced motion        -> revealed immediately, static and scrollable
//     └─ verse overflows       -> rolling, then settled after CREDITS_CYCLES
//                                 passes; skippable throughout
//   reveal()   the reader tapped: stop everything, show the whole verse
//   cancel()   a new reply arrived, or the view went away
//
// Every entry point clears the pending timer before arming a new one, so two
// replies can never run overlapping sequences, and the scope disposal hook makes
// unmounting the view enough to guarantee nothing is left pending.

import { computed, getCurrentScope, onScopeDispose, ref } from 'vue';
import type { ComputedRef, CSSProperties, Ref } from 'vue';
import {
  CREDITS_CYCLES,
  CREDITS_START_DELAY_SECONDS,
  creditsDuration,
  creditsSettleMs,
  prefersReducedMotion,
  shouldRoll,
} from '../lib/gameKhimkiCredits';

/** Inline `animation-*` declarations for the reel, or nothing when it is still. */
export interface ReelStyle extends CSSProperties {
  animationDuration: string;
  animationDelay: string;
  animationIterationCount: string;
}

/** Reactive pacing state for one verse box, plus the controls that drive it. */
export interface VersePacing {
  /** True while the reel animates. Drives the rolling class and the second copy. */
  readonly rolling: Ref<boolean>;
  /** Duration of ONE pass, in seconds, bound to the CSS animation-duration. */
  readonly seconds: Ref<number>;
  /** True once the reader has asked for the whole verse at once. */
  readonly revealed: Ref<boolean>;
  /** True when this verse is too tall for its box, whether or not it is moving. */
  readonly overflows: Ref<boolean>;
  /** True while offering "show the whole thing" would actually do something. */
  readonly skippable: ComputedRef<boolean>;
  /**
   * Inline animation style for the reel, or `undefined` when it is not rolling.
   * The hold and the pass count are bound from the constants rather than written
   * into the stylesheet, so the pacing has exactly one source of truth.
   */
  readonly reelStyle: ComputedRef<ReelStyle | undefined>;
  /** Measure a freshly rendered verse and begin its sequence. */
  start(contentPx: number, viewportPx: number): void;
  /** Skip to the full text: stop the roll and hand the reader the lot. */
  reveal(): void;
  /** Abandon the current sequence and clear its timer. */
  cancel(): void;
}

/** Overridable seams, so a test can drive the sequence without a real viewport. */
export interface VersePacingOptions {
  /** Reduced-motion probe. Defaults to the `prefers-reduced-motion` media query. */
  reducedMotion?: () => boolean;
}

/**
 * Create the pacing state machine for a verse box. Call it from `setup()`: it
 * registers a scope-disposal hook so the settle timer cannot outlive the view.
 */
export function useVersePacing(options: VersePacingOptions = {}): VersePacing {
  const reducedMotion = options.reducedMotion ?? prefersReducedMotion;

  const rolling = ref(false);
  const seconds = ref(0);
  const revealed = ref(false);
  const overflows = ref(false);

  // Revealing or settling both leave the verse static, but only an overflowing
  // verse that has NOT been revealed still has text the reader cannot see.
  const skippable = computed(() => overflows.value && !revealed.value);

  const reelStyle = computed<ReelStyle | undefined>(() =>
    rolling.value
      ? {
          animationDuration: `${seconds.value}s`,
          animationDelay: `${CREDITS_START_DELAY_SECONDS}s`,
          animationIterationCount: `${CREDITS_CYCLES}`,
        }
      : undefined,
  );

  let settleTimer: ReturnType<typeof setTimeout> | null = null;

  function clearSettleTimer(): void {
    if (settleTimer !== null) {
      clearTimeout(settleTimer);
      settleTimer = null;
    }
  }

  function cancel(): void {
    clearSettleTimer();
    rolling.value = false;
    revealed.value = false;
    overflows.value = false;
    seconds.value = 0;
  }

  function start(contentPx: number, viewportPx: number): void {
    // Always from a clean slate: the previous reply's timer must not survive to
    // stop this one's roll partway through.
    cancel();
    if (!shouldRoll(contentPx, viewportPx)) return;
    overflows.value = true;

    if (reducedMotion()) {
      // No animation at all for this reader — the whole verse, immediately, in a
      // box they scroll at their own pace.
      revealed.value = true;
      return;
    }

    seconds.value = creditsDuration(contentPx);
    rolling.value = true;
    settleTimer = setTimeout(() => {
      settleTimer = null;
      rolling.value = false;
    }, creditsSettleMs(seconds.value));
  }

  function reveal(): void {
    clearSettleTimer();
    rolling.value = false;
    revealed.value = true;
  }

  // Unmounting the view is enough; there is no way to leave the timer running.
  if (getCurrentScope()) onScopeDispose(clearSettleTimer);

  return { rolling, seconds, revealed, overflows, skippable, reelStyle, start, reveal, cancel };
}
