// Reading a provider's redirect-mode return trip.
//
// Both login providers can navigate the whole browser back to a page of this
// SPA with the result in the query string, and for Yandex that is the ONLY way
// a login ever finishes — there is no SDK and no in-page callback.
//
// VK ID normally hands the authorization code to JavaScript (Callback response
// mode) and no navigation happens at all. When it cannot — an in-app WebView, a
// blocked popup, partitioned third-party storage, or the "войти другим способом"
// path — it falls back to navigating, exactly as Yandex always does.
//
// Either way the landing page is a page of this SPA, so everything below is
// about turning that query string into either the values the exchange needs or
// a sentence a person can act on.
//
// Pure on purpose: the composable does the I/O, this decides what the trip said.

import type { OAuthProvider } from '../constants';

/** The query shape vue-router hands us, without importing vue-router. */
export type QueryLike = Record<string, string | null | (string | null)[] | undefined>;

/** What was stashed in sessionStorage when the flow started. */
export interface StoredPkce {
  state: string | null;
  codeVerifier: string | null;
}

/**
 * Everything `POST /api/auth/<provider>/callback` needs, or why we cannot call it.
 *
 * `deviceId` is VK's alone and is '' for Yandex — see `requiresDeviceId` below
 * for why that difference is a rule here rather than a tolerance.
 */
export type OAuthRedirectOutcome =
  | { kind: 'ready'; code: string; deviceId: string; state: string; codeVerifier: string }
  | { kind: 'failed'; reason: 'provider_error' | 'incomplete' | 'lost_verifier'; message: string };

/**
 * What a provider is called in a Russian sentence, nominative and genitive.
 *
 * Two forms because both are needed and «ВК» happens to be indeclinable while
 * «Яндекс» is not: "вход через ВК"/"через Яндекс" but "из ВК"/"из Яндекса".
 */
const NAMES: Record<OAuthProvider, { nom: string; gen: string }> = {
  vk: { nom: 'ВК', gen: 'ВК' },
  yandex: { nom: 'Яндекс', gen: 'Яндекса' },
};

/**
 * Does this provider's return trip carry a device id?
 *
 * VK's does and the exchange needs it, so a VK trip without one is incomplete
 * and must say so rather than posting a request the backend will refuse.
 * Yandex has no such concept at all, so requiring one there would reject every
 * single Yandex login. This is the one place the two flows genuinely differ.
 */
function requiresDeviceId(provider: OAuthProvider): boolean {
  return provider === 'vk';
}

/** First value of a possibly-repeated query parameter, '' when absent. */
export function firstQueryValue(v: QueryLike[string]): string {
  if (Array.isArray(v)) return v[0] ?? '';
  return v ?? '';
}

export function parseOAuthRedirect(
  provider: OAuthProvider,
  query: QueryLike,
  stored: StoredPkce,
): OAuthRedirectOutcome {
  const name = NAMES[provider];

  const error = firstQueryValue(query.error);
  if (error) {
    // access_denied is the ordinary "user pressed cancel", not a fault.
    return {
      kind: 'failed',
      reason: 'provider_error',
      message:
        error === 'access_denied'
          ? `вход через ${name.nom} отменён`
          : `${name.nom} не пустил — попробуй войти ещё раз`,
    };
  }

  const code = firstQueryValue(query.code);
  const deviceId = firstQueryValue(query.device_id);
  const state = firstQueryValue(query.state) || stored.state || '';
  if (!code || !state || (requiresDeviceId(provider) && !deviceId)) {
    return {
      kind: 'failed',
      reason: 'incomplete',
      message: `ссылка возврата из ${name.gen} неполная — начни вход заново`,
    };
  }

  // The verifier never leaves the browser and lives in sessionStorage, which is
  // per-tab: if the login finished in a NEW tab, this tab has it and that one
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
