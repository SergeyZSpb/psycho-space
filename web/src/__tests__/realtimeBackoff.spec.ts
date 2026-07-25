import { describe, expect, it } from 'vitest';
import {
  BASE_DELAY_MS,
  CLOSE_GOING_AWAY,
  CLOSE_TRY_AGAIN_LATER,
  CLOSE_UNAUTHORIZED,
  HIDDEN_CLOSE_MS,
  MAX_DELAY_MS,
  policyForClose,
  reconnectDelay,
} from '../realtime/backoff';

describe('policyForClose', () => {
  it('treats a revoked session as terminal', () => {
    // Retrying would mean a blocked account's browser hammering a handshake
    // that is going to keep refusing it, for as long as the tab stays open.
    expect(policyForClose(CLOSE_UNAUTHORIZED)).toBe('terminal');
  });

  it('comes back promptly after a planned restart', () => {
    // The server said it is going away, so it is coming back in seconds.
    expect(policyForClose(CLOSE_GOING_AWAY)).toBe('prompt');
  });

  it('backs off harder when the server asked us to', () => {
    // Evicted for falling behind, or over a cap. Returning at once makes both
    // of those worse rather than better.
    expect(policyForClose(CLOSE_TRY_AGAIN_LATER)).toBe('slow');
  });

  it('treats a silent drop as an ordinary network failure', () => {
    // No bye frame arrived, so we never heard why — which is what a lost
    // network looks like, and it is the common case.
    expect(policyForClose(undefined)).toBe('normal');
    expect(policyForClose(1006)).toBe('normal');
  });
});

describe('reconnectDelay', () => {
  it('grows exponentially with the attempt', () => {
    const noJitter = () => 1;
    expect(reconnectDelay('normal', 0, noJitter)).toBe(BASE_DELAY_MS.normal);
    expect(reconnectDelay('normal', 1, noJitter)).toBe(BASE_DELAY_MS.normal * 2);
    expect(reconnectDelay('normal', 3, noJitter)).toBe(BASE_DELAY_MS.normal * 8);
  });

  it('never waits longer than the cap', () => {
    expect(reconnectDelay('normal', 40, () => 1)).toBe(MAX_DELAY_MS);
    expect(reconnectDelay('slow', 40, () => 1)).toBe(MAX_DELAY_MS);
  });

  it('comes back sooner after a planned restart than after a drop', () => {
    const noJitter = () => 1;
    expect(reconnectDelay('prompt', 0, noJitter)).toBeLessThan(
      reconnectDelay('normal', 0, noJitter),
    );
    expect(reconnectDelay('slow', 0, noJitter)).toBeGreaterThan(
      reconnectDelay('normal', 0, noJitter),
    );
  });

  it('is FULLY jittered, not merely nudged', () => {
    // The disconnects this client sees are overwhelmingly correlated — a deploy
    // drops every socket in the same millisecond. Partial jitter would leave
    // that herd almost as synchronised as none at all, so the delay has to be
    // able to come out near zero as well as near the ceiling.
    expect(reconnectDelay('normal', 2, () => 0)).toBe(0);
    expect(reconnectDelay('normal', 2, () => 1)).toBe(BASE_DELAY_MS.normal * 4);
    expect(reconnectDelay('normal', 2, () => 0.5)).toBe((BASE_DELAY_MS.normal * 4) / 2);
  });

  it('does not go backwards on a negative attempt', () => {
    expect(reconnectDelay('normal', -3, () => 1)).toBe(BASE_DELAY_MS.normal);
  });

  it('waits about a minute before closing a hidden tab', () => {
    // iOS kills a backgrounded socket anyway; this makes it deliberate. Long
    // enough not to fire on a glance at another app, short enough that the
    // return is a fresh connection rather than the discovery of a zombie.
    expect(HIDDEN_CLOSE_MS).toBeGreaterThanOrEqual(30_000);
    expect(HIDDEN_CLOSE_MS).toBeLessThanOrEqual(120_000);
  });
});
