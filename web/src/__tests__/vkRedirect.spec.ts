import { describe, expect, it } from 'vitest';
import { firstQueryValue, parseVkRedirect } from '../lib/vkRedirect';

const stored = { state: 'stored-state', codeVerifier: 'verifier-123' };

describe('firstQueryValue', () => {
  it('takes the first value of a repeated parameter', () => {
    expect(firstQueryValue(['a', 'b'])).toBe('a');
  });

  it('returns empty string for null, undefined and an empty array', () => {
    expect(firstQueryValue(null)).toBe('');
    expect(firstQueryValue(undefined)).toBe('');
    expect(firstQueryValue([])).toBe('');
  });
});

describe('parseVkRedirect', () => {
  it('returns the four exchange values when VK came back complete', () => {
    const out = parseVkRedirect({ code: 'c1', device_id: 'd1', state: 'from-vk' }, stored);
    expect(out).toEqual({
      kind: 'ready',
      code: 'c1',
      deviceId: 'd1',
      state: 'from-vk',
      codeVerifier: 'verifier-123',
    });
  });

  it('falls back to the stored state when VK did not echo one', () => {
    const out = parseVkRedirect({ code: 'c1', device_id: 'd1' }, stored);
    expect(out).toMatchObject({ kind: 'ready', state: 'stored-state' });
  });

  it('reads a cancelled login as such, not as a fault', () => {
    const out = parseVkRedirect({ error: 'access_denied' }, stored);
    expect(out).toMatchObject({ kind: 'failed', reason: 'vk_error' });
    expect(out).toHaveProperty('message', 'вход через ВК отменён');
  });

  it('reports any other VK error', () => {
    const out = parseVkRedirect({ error: 'server_error' }, stored);
    expect(out).toMatchObject({ kind: 'failed', reason: 'vk_error' });
  });

  it('prefers the VK error over a code that is also present', () => {
    const out = parseVkRedirect({ error: 'invalid_request', code: 'c1', device_id: 'd1' }, stored);
    expect(out).toMatchObject({ kind: 'failed', reason: 'vk_error' });
  });

  it.each([
    ['no code', { device_id: 'd1', state: 's' }],
    ['no device_id', { code: 'c1', state: 's' }],
    ['no state anywhere', { code: 'c1', device_id: 'd1' }],
  ])('calls the trip incomplete when there is %s', (_name, query) => {
    const withoutState = { state: null, codeVerifier: 'verifier-123' };
    const out = parseVkRedirect(query, withoutState);
    expect(out).toMatchObject({ kind: 'failed', reason: 'incomplete' });
  });

  it('names the lost verifier separately — it means another tab started the flow', () => {
    const out = parseVkRedirect(
      { code: 'c1', device_id: 'd1', state: 's' },
      { state: 's', codeVerifier: null },
    );
    expect(out).toMatchObject({ kind: 'failed', reason: 'lost_verifier' });
  });
});
