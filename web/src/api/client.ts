// Typed fetch wrapper. Every backend error carries a stable machine `error`
// code plus a `trace_id` (also echoed in the X-Trace-Id header); we surface both
// to the user so they can quote the trace id to the admin.

/**
 * Error codes the UI handles inline (near a form) rather than via the global modal.
 *
 * PROVIDER-NEUTRAL NOW, and short by design. The login failures a provider can
 * produce — `oauth_not_configured`, `oauth_exchange_failed`,
 * `oauth_userinfo_failed`, `oauth_no_user_id`, `oauth_identity_mismatch`,
 * `oauth_idtoken_invalid` (the old `vk_*` names, renamed when Yandex arrived
 * and the same handler started serving both) — are deliberately NOT here: there
 * is no form to show them next to, the user can do nothing about any of them,
 * and each carries a trace id worth quoting. They belong in the modal, which is
 * where anything not listed here goes. `bad_state` and `consent_required` stay
 * because they are about this browser's own half of the login.
 */
const KNOWN_ERROR_CODES = ['title_required', 'too_long', 'consent_required', 'bad_state'] as const;

export type KnownErrorCode = (typeof KNOWN_ERROR_CODES)[number];

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly traceId: string;

  constructor(code: string, status: number, traceId: string) {
    super(`api error ${code} (status ${status}, trace ${traceId || 'n/a'})`);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.traceId = traceId;
  }

  // True for the handful of validation codes worth showing inline near a form.
  // Reads the one list above rather than repeating it — the two used to be
  // separate copies, which is a way for a code to be in the type and not in the
  // check.
  isKnown(): boolean {
    return (KNOWN_ERROR_CODES as readonly string[]).includes(this.code);
  }
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  body?: unknown;
}

// apiFetch resolves to the parsed JSON body (or undefined for 204/no-content),
// and throws ApiError for any non-2xx response or a network failure.
export async function apiFetch<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const hasBody = opts.body !== undefined;

  let res: Response;
  try {
    res = await fetch(path, {
      method: opts.method ?? 'GET',
      credentials: 'include',
      headers: hasBody ? { 'Content-Type': 'application/json' } : undefined,
      body: hasBody ? JSON.stringify(opts.body) : undefined,
    });
  } catch {
    // DNS/offline/CORS — no HTTP response at all.
    throw new ApiError('network', 0, '');
  }

  const headerTrace = res.headers.get('X-Trace-Id') ?? '';

  // Read as text first so an empty body (204) doesn't blow up JSON parsing.
  const raw = await res.text();
  let parsed: unknown = null;
  if (raw) {
    try {
      parsed = JSON.parse(raw);
    } catch {
      parsed = null;
    }
  }

  if (!res.ok) {
    const body = (parsed ?? {}) as { error?: string; trace_id?: string };
    const code = body.error ?? 'http_error';
    const traceId = body.trace_id ?? headerTrace;
    throw new ApiError(code, res.status, traceId);
  }

  return parsed as T;
}
