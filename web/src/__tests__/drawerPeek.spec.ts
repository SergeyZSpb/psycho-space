import { afterEach, describe, expect, it, vi } from 'vitest';
import { DRAWER_PEEK_MS, prefersReducedMotion, shouldPeekDrawer } from '../lib/drawerPeek';

// Replace window.matchMedia with a stub that answers `matches` for every query.
function stubMatchMedia(matches: boolean) {
  const stub = vi.fn().mockReturnValue({ matches } as MediaQueryList);
  vi.stubGlobal('matchMedia', stub);
  return stub;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('shouldPeekDrawer (the app shell reveals the nav once per page load)', () => {
  it('peeks when the drawer is an overlay and motion is welcome', () => {
    expect(shouldPeekDrawer({ temporary: true, reducedMotion: false })).toBe(true);
  });

  it('does not peek a permanent drawer — the nav is already on screen', () => {
    expect(shouldPeekDrawer({ temporary: false, reducedMotion: false })).toBe(false);
  });

  it('does not peek under prefers-reduced-motion', () => {
    expect(shouldPeekDrawer({ temporary: true, reducedMotion: true })).toBe(false);
  });

  it('does not peek when both reasons to skip apply', () => {
    expect(shouldPeekDrawer({ temporary: false, reducedMotion: true })).toBe(false);
  });
});

describe('DRAWER_PEEK_MS', () => {
  // Not an assertion that the constant equals itself: it guards the order of
  // magnitude, so a stray zero cannot leave the drawer parked over the content.
  it('holds the drawer open long enough to notice and short enough to forgive', () => {
    expect(DRAWER_PEEK_MS).toBeGreaterThanOrEqual(500);
    expect(DRAWER_PEEK_MS).toBeLessThanOrEqual(1500);
  });
});

describe('prefersReducedMotion', () => {
  it('asks the browser for prefers-reduced-motion: reduce', () => {
    const stub = stubMatchMedia(true);
    expect(prefersReducedMotion()).toBe(true);
    expect(stub).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)');
  });

  it('reports no preference when the browser reports none', () => {
    stubMatchMedia(false);
    expect(prefersReducedMotion()).toBe(false);
  });

  it('treats an environment without matchMedia as no preference', () => {
    vi.stubGlobal('matchMedia', undefined);
    expect(prefersReducedMotion()).toBe(false);
  });
});
