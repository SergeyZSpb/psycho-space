// Yandex ID login — the head of the flow. The tail it shares with VK (post the
// code, set the account, route by status) is in useOAuthLogin.
//
// It is a fraction of the size of its VK counterpart, and the reason is worth
// stating: Yandex ID is plain OAuth 2.0 with PKCE. There is no SDK, no widget,
// no iframe reading a session from another origin, no device id, no id_token —
// so there is nothing to mount, nothing to tear down, and no widget-error path
// to keep apart from a real one. One button, one navigation.
//
// THE CLIENT ID IS NOT IN THIS FILE, and must not arrive here. The authorize
// URL is built by the backend and returned whole by /api/auth/yandex/state,
// which is what keeps the client id and the redirect URI in ONE place instead
// of the three VK needs (SPA constant, backend env, provider dashboard) — a
// three-way agreement that has broken production once already. All this
// composable knows is that the server hands it somewhere to go.
//
// Flow:
//   1. Generate a PKCE pair in the browser (Web Crypto).
//   2. Stash the verifier under this provider's own sessionStorage key, so a
//      half-finished VK login in another tab cannot clobber it.
//   3. GET /api/auth/yandex/state?code_challenge=… — the backend sets its state
//      cookie and answers with the state plus the authorize URL.
//   4. Navigate. Yandex comes back to /auth/yandex/redirect, where
//      AuthRedirectView finishes the login via useOAuthLogin.

import { createPkce } from '../lib/pkce';
import { authApi } from '../api/endpoints';
import { ssStateKey, ssVerifierKey } from '../constants';

export function useYandexLogin() {
  /**
   * Begin a Yandex login by navigating the browser to Yandex.
   *
   * Resolves only if the navigation could not be started — on success the page
   * is on its way out, so callers keep any pending/loading state set rather
   * than clearing it.
   */
  async function start(): Promise<void> {
    const { codeVerifier, codeChallenge } = await createPkce();
    // Before the request, not after: the state endpoint sets a cookie, and a
    // verifier stashed only on the happy path would be missing in exactly the
    // case where the browser navigated anyway.
    sessionStorage.setItem(ssVerifierKey('yandex'), codeVerifier);

    const { state, authorize_url: authorizeUrl } = await authApi.yandexState(codeChallenge);
    sessionStorage.setItem(ssStateKey('yandex'), state);

    window.location.href = authorizeUrl;
  }

  return { start };
}
