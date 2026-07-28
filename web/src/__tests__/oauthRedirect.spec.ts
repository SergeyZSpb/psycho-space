import { describe, expect, it } from 'vitest';
import { firstQueryValue, parseOAuthRedirect } from '../lib/oauthRedirect';

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

describe('parseOAuthRedirect — VK', () => {
  it('returns the four exchange values when VK came back complete', () => {
    const out = parseOAuthRedirect('vk', { code: 'c1', device_id: 'd1', state: 'from-vk' }, stored);
    expect(out).toEqual({
      kind: 'ready',
      code: 'c1',
      deviceId: 'd1',
      state: 'from-vk',
      codeVerifier: 'verifier-123',
    });
  });

  it('falls back to the stored state when VK did not echo one', () => {
    const out = parseOAuthRedirect('vk', { code: 'c1', device_id: 'd1' }, stored);
    expect(out).toMatchObject({ kind: 'ready', state: 'stored-state' });
  });

  it('reads a cancelled login as such, not as a fault', () => {
    const out = parseOAuthRedirect('vk', { error: 'access_denied' }, stored);
    expect(out).toMatchObject({ kind: 'failed', reason: 'provider_error' });
    expect(out).toHaveProperty('message', 'вход через ВК отменён');
  });

  it('reports any other VK error', () => {
    const out = parseOAuthRedirect('vk', { error: 'server_error' }, stored);
    expect(out).toMatchObject({ kind: 'failed', reason: 'provider_error' });
    expect(out).toHaveProperty('message', 'ВК не пустил — попробуй войти ещё раз');
  });

  it('prefers the VK error over a code that is also present', () => {
    const out = parseOAuthRedirect(
      'vk',
      { error: 'invalid_request', code: 'c1', device_id: 'd1' },
      stored,
    );
    expect(out).toMatchObject({ kind: 'failed', reason: 'provider_error' });
  });

  it.each([
    ['no code', { device_id: 'd1', state: 's' }],
    ['no device_id', { code: 'c1', state: 's' }],
    ['no state anywhere', { code: 'c1', device_id: 'd1' }],
  ])('calls the trip incomplete when there is %s', (_name, query) => {
    const withoutState = { state: null, codeVerifier: 'verifier-123' };
    const out = parseOAuthRedirect('vk', query, withoutState);
    expect(out).toMatchObject({ kind: 'failed', reason: 'incomplete' });
    expect(out).toHaveProperty('message', 'ссылка возврата из ВК неполная — начни вход заново');
  });

  it('names the lost verifier separately — it means another tab started the flow', () => {
    const out = parseOAuthRedirect(
      'vk',
      { code: 'c1', device_id: 'd1', state: 's' },
      { state: 's', codeVerifier: null },
    );
    expect(out).toMatchObject({ kind: 'failed', reason: 'lost_verifier' });
  });
});

describe('parseOAuthRedirect — Yandex', () => {
  it('is ready without a device_id — Yandex has no such concept', () => {
    // The whole reason this module took a provider argument. Requiring a device
    // id here would refuse every Yandex login there will ever be.
    const out = parseOAuthRedirect('yandex', { code: 'c1', state: 'from-yandex' }, stored);
    expect(out).toEqual({
      kind: 'ready',
      code: 'c1',
      deviceId: '',
      state: 'from-yandex',
      codeVerifier: 'verifier-123',
    });
  });

  it('ignores a device_id if one somehow shows up', () => {
    // Nothing sends one, and the callback body drops it regardless — this pins
    // that a stray parameter cannot change the outcome.
    const out = parseOAuthRedirect('yandex', { code: 'c1', state: 's', device_id: 'd1' }, stored);
    expect(out).toMatchObject({ kind: 'ready', code: 'c1' });
  });

  it('falls back to the stored state when Yandex did not echo one', () => {
    const out = parseOAuthRedirect('yandex', { code: 'c1' }, stored);
    expect(out).toMatchObject({ kind: 'ready', state: 'stored-state' });
  });

  it('says «Яндекс» rather than «ВК» when the user cancelled', () => {
    const out = parseOAuthRedirect('yandex', { error: 'access_denied' }, stored);
    expect(out).toMatchObject({ kind: 'failed', reason: 'provider_error' });
    expect(out).toHaveProperty('message', 'вход через Яндекс отменён');
  });

  it('reports any other Yandex error in its own name', () => {
    const out = parseOAuthRedirect('yandex', { error: 'server_error' }, stored);
    expect(out).toHaveProperty('message', 'Яндекс не пустил — попробуй войти ещё раз');
  });

  it.each([
    ['no code', { state: 's' }],
    ['no state anywhere', { code: 'c1' }],
  ])('calls the trip incomplete when there is %s, in the genitive', (_name, query) => {
    const withoutState = { state: null, codeVerifier: 'verifier-123' };
    const out = parseOAuthRedirect('yandex', query, withoutState);
    expect(out).toMatchObject({ kind: 'failed', reason: 'incomplete' });
    expect(out).toHaveProperty(
      'message',
      'ссылка возврата из Яндекса неполная — начни вход заново',
    );
  });

  it('names the lost verifier with the same provider-neutral sentence', () => {
    // Nothing about this failure is the provider's doing — it is a tab that
    // never started the flow — so the sentence deliberately names neither.
    const out = parseOAuthRedirect(
      'yandex',
      { code: 'c1', state: 's' },
      { state: 's', codeVerifier: null },
    );
    expect(out).toMatchObject({ kind: 'failed', reason: 'lost_verifier' });
    expect(out).toHaveProperty(
      'message',
      'вход начался в другой вкладке — вернись на главную и войди заново',
    );
  });
});
