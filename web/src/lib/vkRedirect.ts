// Reading VK's redirect-mode return trip.
//
// VK ID normally hands the authorization code to JavaScript (Callback response
// mode) and no navigation happens at all. When it cannot — an in-app WebView, a
// blocked popup, partitioned third-party storage, or the "войти другим способом"
// path — it navigates the whole browser to the registered redirect URL with the
// result in the query string instead. That landing page is a page of this SPA,
// so everything below is about turning that query string into either the four
// values the exchange needs or a sentence a person can act on.
//
// Pure on purpose: the composable does the I/O, this decides what the trip said.

/** The query shape vue-router hands us, without importing vue-router. */
export type QueryLike = Record<string, string | null | (string | null)[] | undefined>;

/** What was stashed in sessionStorage when the flow started. */
export interface StoredPkce {
  state: string | null;
  codeVerifier: string | null;
}

/** Everything `POST /api/auth/vk/callback` needs, or why we cannot call it. */
export type VkRedirectOutcome =
  | { kind: 'ready'; code: string; deviceId: string; state: string; codeVerifier: string }
  | { kind: 'failed'; reason: 'vk_error' | 'incomplete' | 'lost_verifier'; message: string };

/** First value of a possibly-repeated query parameter, '' when absent. */
export function firstQueryValue(v: QueryLike[string]): string {
  if (Array.isArray(v)) return v[0] ?? '';
  return v ?? '';
}

export function parseVkRedirect(query: QueryLike, stored: StoredPkce): VkRedirectOutcome {
  const error = firstQueryValue(query.error);
  if (error) {
    // access_denied is the ordinary "user pressed cancel", not a fault.
    return {
      kind: 'failed',
      reason: 'vk_error',
      message:
        error === 'access_denied'
          ? 'вход через ВК отменён'
          : 'ВК не пустил — попробуй войти ещё раз',
    };
  }

  const code = firstQueryValue(query.code);
  const deviceId = firstQueryValue(query.device_id);
  const state = firstQueryValue(query.state) || stored.state || '';
  if (!code || !deviceId || !state) {
    return {
      kind: 'failed',
      reason: 'incomplete',
      message: 'ссылка возврата из ВК неполная — начни вход заново',
    };
  }

  // The verifier never leaves the browser and lives in sessionStorage, which is
  // per-tab: if VK finished the login in a NEW tab, this tab has it and that one
  // does not. Say so, because "начни заново" in the same tab actually works.
  if (!stored.codeVerifier) {
    return {
      kind: 'failed',
      reason: 'lost_verifier',
      message: 'вход начался в другой вкладке — вернись на главную и войди заново',
    };
  }

  return { kind: 'ready', code, deviceId, state, codeVerifier: stored.codeVerifier };
}
