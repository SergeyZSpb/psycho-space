// Typed fetch wrapper. Every backend error carries a stable machine `error`
// code plus a `trace_id` (also echoed in the X-Trace-Id header); we surface both
// to the user so they can quote the trace id to the admin.

// Error codes the UI handles inline (near a form) rather than via the global modal.
export type KnownErrorCode =
  | 'title_required'
  | 'too_long'
  | 'consent_required'
  | 'bad_state';

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
  isKnown(): boolean {
    return (
      this.code === 'title_required' ||
      this.code === 'too_long' ||
      this.code === 'consent_required' ||
      this.code === 'bad_state'
    );
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
