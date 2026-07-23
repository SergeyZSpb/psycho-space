import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, apiFetch } from '../api/client';

// A minimal Response-like stub (apiFetch only touches ok/status/headers.get/text).
function fakeResponse(opts: {
  ok: boolean;
  status: number;
  body?: string;
  traceHeader?: string;
}): Response {
  return {
    ok: opts.ok,
    status: opts.status,
    headers: { get: (name: string) => (name === 'X-Trace-Id' ? (opts.traceHeader ?? null) : null) },
    text: async () => opts.body ?? '',
  } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('apiFetch error parsing', () => {
  it('throws an ApiError carrying code + traceId from the JSON body', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        fakeResponse({
          ok: false,
          status: 422,
          body: JSON.stringify({ error: 'title_required', trace_id: 'abc123trace' }),
        }),
      ),
    );

    await expect(apiFetch('/api/wishlist/items', { method: 'POST', body: {} })).rejects.toMatchObject({
      code: 'title_required',
      status: 422,
      traceId: 'abc123trace',
    });

    try {
      await apiFetch('/api/wishlist/items');
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError);
      expect((e as ApiError).isKnown()).toBe(true);
    }
  });

  it('falls back to the X-Trace-Id header when the body has no trace_id', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        fakeResponse({
          ok: false,
          status: 500,
          body: JSON.stringify({ error: 'internal' }),
          traceHeader: 'header-trace-999',
        }),
      ),
    );

    const err = await apiFetch('/api/auth/me').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe('internal');
    expect((err as ApiError).status).toBe(500);
    expect((err as ApiError).traceId).toBe('header-trace-999');
    expect((err as ApiError).isKnown()).toBe(false);
  });

  it('reports a network failure as ApiError(code=network, status=0)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      }),
    );

    const err = await apiFetch('/api/auth/me').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe('network');
    expect((err as ApiError).status).toBe(0);
  });

  it('returns parsed JSON on success', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        fakeResponse({ ok: true, status: 200, body: JSON.stringify({ state: 'xyz' }) }),
      ),
    );

    const res = await apiFetch<{ state: string }>('/api/auth/vk/state');
    expect(res.state).toBe('xyz');
  });
});
