import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * Starting a Яндекс login.
 *
 * WHAT THIS PINS, and why it is worth a test of its own: the browser must never
 * learn the Yandex client id or redirect URI. The backend builds the whole
 * authorize URL and hands it back, so those two values live in ONE place
 * (the server's environment) plus the provider's dashboard — two copies that
 * must agree, rather than the three VK needs. If somebody ever "simplifies"
 * this by composing the URL in the SPA, the client id lands in a constant, in
 * the bundle, and in a third place to forget to change; the assertions below
 * are what fail first.
 *
 * The second half is just as load-bearing: whatever URL the server returned is
 * where the browser goes, byte for byte. Rewriting it here would be a second
 * implementation of URL building, which is the thing being avoided.
 *
 * The real api/endpoints + api/client are used deliberately — the request URL
 * is part of what is under test, so mocking the layer that builds it would test
 * nothing.
 */

vi.mock('../lib/pkce', () => ({
  createPkce: vi.fn(async () => ({
    codeVerifier: 'test-verifier-value',
    codeChallenge: 'test-challenge-value',
  })),
}));

import { useYandexLogin } from '../composables/useYandexLogin';
import { ApiError } from '../api/client';

// What the backend answers with. The client id inside it is the SERVER's, and
// the fact that it appears only here — never in a constant, never in a query
// this code builds — is the point.
const AUTHORIZE_URL =
  'https://oauth.yandex.ru/authorize?client_id=server-side-client-id' +
  '&code_challenge=test-challenge-value&code_challenge_method=S256' +
  '&redirect_uri=https%3A%2F%2Fpsycho-space.ru%2Fauth%2Fyandex%2Fredirect' +
  '&response_type=code&state=server-state';

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: async () => JSON.stringify(body),
  } as unknown as Response;
}

let requests: { url: string; init: RequestInit | undefined }[] = [];

function stubFetch(status: number, body: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      requests.push({ url, init });
      return fakeResponse(status, body);
    }),
  );
}

beforeEach(() => {
  requests = [];
  sessionStorage.clear();
  // jsdom refuses a real cross-origin navigation, so stand a plain object in
  // its place and read what was assigned to it.
  Object.defineProperty(window, 'location', {
    value: { href: '' },
    writable: true,
    configurable: true,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe('useYandexLogin().start()', () => {
  it('asks the server for the authorize URL, sending only the PKCE challenge', async () => {
    stubFetch(200, { state: 'server-state', authorize_url: AUTHORIZE_URL });

    await useYandexLogin().start();

    expect(requests).toHaveLength(1);
    const url = new URL(requests[0].url, 'https://psycho-space.ru');
    expect(url.pathname).toBe('/api/auth/yandex/state');
    // Exactly one parameter. A client id or a redirect uri appearing here would
    // mean the SPA had started building the authorize URL itself.
    expect([...url.searchParams.keys()]).toEqual(['code_challenge']);
    expect(url.searchParams.get('code_challenge')).toBe('test-challenge-value');
    // And it is a GET with no body — nothing is smuggled past the query either.
    expect(requests[0].init?.method ?? 'GET').toBe('GET');
    expect(requests[0].init?.body).toBeUndefined();
  });

  it('never puts a client id on the wire', async () => {
    stubFetch(200, { state: 'server-state', authorize_url: AUTHORIZE_URL });

    await useYandexLogin().start();

    const sent = requests[0].url + String(requests[0].init?.body ?? '');
    expect(sent).not.toMatch(/client_id/i);
    expect(sent).not.toMatch(/redirect_uri/i);
  });

  it('navigates to exactly the authorize_url the server returned', async () => {
    stubFetch(200, { state: 'server-state', authorize_url: AUTHORIZE_URL });

    await useYandexLogin().start();

    // Byte for byte: the URL is the server's product, not something rebuilt or
    // patched up here.
    expect(window.location.href).toBe(AUTHORIZE_URL);
  });

  it('stashes the verifier and state under the Яндекс keys, leaving VK’s alone', async () => {
    // A half-finished VK login in another tab of the same session.
    sessionStorage.setItem('ps-pkce-verifier-vk', 'vk-verifier');
    sessionStorage.setItem('ps-oauth-state-vk', 'vk-state');
    stubFetch(200, { state: 'server-state', authorize_url: AUTHORIZE_URL });

    await useYandexLogin().start();

    expect(sessionStorage.getItem('ps-pkce-verifier-yandex')).toBe('test-verifier-value');
    expect(sessionStorage.getItem('ps-oauth-state-yandex')).toBe('server-state');
    // The whole reason the keys are per provider.
    expect(sessionStorage.getItem('ps-pkce-verifier-vk')).toBe('vk-verifier');
    expect(sessionStorage.getItem('ps-oauth-state-vk')).toBe('vk-state');
  });

  it('propagates an unconfigured provider instead of navigating anywhere', async () => {
    stubFetch(503, { error: 'oauth_not_configured', trace_id: 't-1' });

    const err = await useYandexLogin()
      .start()
      .catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe('oauth_not_configured');
    expect(window.location.href).toBe('');
  });
});
