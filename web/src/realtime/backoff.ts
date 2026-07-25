// Reconnect timing, kept apart from the socket so the policy can be reasoned
// about — and tested — without a transport.
//
// Everything here is a pure function of (why we were disconnected, how many
// times we have failed in a row). No timers, no state.

/** Close codes the server can put in a `bye` frame. Mirrors internal/realtime. */
export const CLOSE_GOING_AWAY = 1001;
export const CLOSE_TRY_AGAIN_LATER = 1013;
export const CLOSE_UNAUTHORIZED = 4001;

/**
 * How the client should react to losing the socket.
 *
 * `terminal` means stop for good: reconnecting would hammer a handshake that is
 * going to keep refusing us, which is the failure mode a revoked account would
 * otherwise inflict on the server for as long as the tab stays open.
 */
export type ReconnectPolicy = 'prompt' | 'normal' | 'slow' | 'terminal';

/**
 * The shortest delay for each policy, in milliseconds.
 *
 * `prompt` is for a planned restart: the server told us it is going away, so it
 * is coming back in a second or two and there is no point pretending otherwise.
 * It is not zero because every connected client got that same frame at the same
 * instant, and a thundering herd against a process that is still booting is how
 * a clean restart turns into a slow one.
 */
export const BASE_DELAY_MS: Record<Exclude<ReconnectPolicy, 'terminal'>, number> = {
  prompt: 400,
  normal: 1_000,
  slow: 5_000,
};

/** Never wait longer than this between attempts. */
export const MAX_DELAY_MS = 30_000;

/** How long the tab may be hidden before the socket is closed deliberately. */
export const HIDDEN_CLOSE_MS = 60_000;

/**
 * What to do about a disconnect.
 *
 * `code` is the one from the server's `bye` frame, not `CloseEvent.code` — a
 * browser reports 1006 for every disconnect, so the frame is the only place the
 * real reason can come from (ADR-018). No frame means we never heard why, which
 * is an ordinary network drop.
 */
export function policyForClose(code: number | undefined): ReconnectPolicy {
  switch (code) {
    case CLOSE_UNAUTHORIZED:
      return 'terminal';
    case CLOSE_GOING_AWAY:
      return 'prompt';
    case CLOSE_TRY_AGAIN_LATER:
      // Either we were evicted for falling behind or we are over a cap. Both
      // mean the server would rather we came back later, and coming back
      // immediately makes both worse.
      return 'slow';
    default:
      return 'normal';
  }
}

/**
 * How long to wait before attempt number `attempt` (0 = the first retry).
 *
 * Exponential, capped, and then **fully jittered**: the returned delay is a
 * uniform draw from `[0, computed]` rather than `computed ± a bit`. That matters
 * here more than it usually would, because the disconnects this client sees are
 * overwhelmingly correlated — a deploy drops every socket in the same
 * millisecond — and partial jitter leaves the herd almost as synchronised as no
 * jitter at all.
 *
 * `random` is injected so a test can pin the draw.
 */
export function reconnectDelay(
  policy: Exclude<ReconnectPolicy, 'terminal'>,
  attempt: number,
  random: () => number,
): number {
  const base = BASE_DELAY_MS[policy];
  const uncapped = base * 2 ** Math.max(0, attempt);
  const ceiling = Math.min(MAX_DELAY_MS, uncapped);
  return Math.round(ceiling * random());
}
