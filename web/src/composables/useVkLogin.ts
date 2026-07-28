// VK ID login (confidential backend exchange with PKCE).
//
// Flow:
//   1. Generate a PKCE pair in the browser (Web Crypto).
//   2. GET /api/auth/vk/state — the backend sets its state cookie and returns
//      the value we must echo to VK (it validates the cookie against the echo).
//   3. Init the VK SDK in Callback response mode and render the OneTap widget.
//   4. On LOGIN_SUCCESS we get { code, device_id }. We DO NOT call
//      VKID.Auth.exchangeCode (that's the public/frontend flow). Instead we POST
//      the code + verifier to our confidential backend, which does the exchange.
//   5. Route on the result: approved -> /app, pending|blocked -> /pending.
//
// THE REDIRECT URL IS A PAGE, NOT THE API ENDPOINT — and it has to be.
//   `redirectUrl` below is what VK validates at authorize and what the backend
//   must echo, byte for byte, at the token exchange (PSYCHOSPACE_VK_REDIRECT_URI
//   — the two must always be changed together, or every login fails). In Callback
//   mode nothing navigates to it, which is how it went unnoticed that it used to
//   point at POST-only /api/auth/vk/callback: any browser that fell back to
//   redirect mode — VK's own in-app WebView, a blocked popup, partitioned
//   third-party storage, "войти другим способом" — was navigated there with GET
//   and got a bare 405. A redirect URL is by definition somewhere a browser is
//   sent, so it points at the SPA page /auth/redirect, which reads the query and
//   POSTs the exchange the same way the widget does. The API endpoint stays a
//   POST-only API and is never a navigation target.
//
// !!! LIVE-VERIFICATION NOTE (only testable against the real VK app + domain) !!!
//   - The SDK's Config type accepts `codeChallenge` and derives
//     code_challenge_method=S256 itself; it does NOT accept a `codeChallengeMethod`
//     field (passing one is a type error), so we omit it. If VK ever needs the
//     method passed explicitly, that must be confirmed on the live app.
//   - The exact OneTap success-payload field names (`code`, `device_id`) and the
//     redirectUrl matching rules can only be confirmed during a real login on the
//     production domain (VK rejects unregistered redirect URLs / origins).

import * as VKID from '@vkid/sdk';
import { useRouter } from 'vue-router';
import type { LocationQuery } from 'vue-router';
import { createPkce } from '../lib/pkce';
import { parseVkRedirect } from '../lib/vkRedirect';
import { authApi } from '../api/endpoints';
import { useAuthStore } from '../stores/auth';
import {
  CONSENT_VERSION,
  HOME_ROUTE_NAME,
  SS_PKCE_VERIFIER,
  SS_VK_STATE,
  VK_APP_ID,
  VK_REDIRECT_PATH,
} from '../constants';

// Minimal shape we read off the OneTap LOGIN_SUCCESS payload (VKID.RedirectPayload).
interface OneTapSuccessPayload {
  code: string;
  device_id: string;
}

export function useVkLogin() {
  const auth = useAuthStore();
  const router = useRouter();

  // POST the authorization code to the confidential backend and route on the result.
  async function exchange(
    code: string,
    deviceId: string,
    state: string,
    codeVerifier: string,
  ): Promise<void> {
    const result = await authApi.vkCallback({
      code,
      device_id: deviceId,
      state,
      code_verifier: codeVerifier,
      consent_version: CONSENT_VERSION,
    });

    // The backend always returns the account + sets a session cookie now; route
    // by status. Pending/blocked users have a session and read their handle from
    // /me on the pending screen.
    auth.setAccount(result.account);
    if (result.account.status === 'approved') {
      await router.push({ name: HOME_ROUTE_NAME });
    } else {
      await router.push({ name: 'pending' });
    }
  }

  /**
   * Mount the OneTap widget into `container`. Returns a cleanup function.
   *
   * THE TWO FAILURE CALLBACKS ARE SEPARATE ON PURPOSE, because the two failures
   * are nothing alike and used to share a modal.
   *
   * `onWidgetError` is the VK widget saying it could not do something — most
   * often that it could not personalise its button, because it reads your VK
   * session from an iframe on VK's origin and the browser would not hand that
   * iframe VK's cookies. **Firefox's Total Cookie Protection partitions
   * third-party storage per top-level site**, so on psycho-space.ru that iframe
   * gets an empty cookie jar and sees no session; Chrome still passes the real
   * ones through, which is the entire difference between the two browsers here.
   * Nothing is broken when this fires: the button still works, because clicking
   * it opens VK top-level in a new tab where VK is first-party. There is
   * nothing for the user to do and nothing to investigate, so it must not
   * produce an error dialog.
   *
   * `onExchangeError` is our own backend refusing the code exchange. That is a
   * real ApiError carrying a real trace id, the user genuinely cannot log in,
   * and it belongs in the modal.
   */
  async function mountOneTap(
    container: HTMLElement,
    handlers: {
      onWidgetError: (err: unknown) => void;
      onExchangeError: (err: unknown) => void;
    },
  ): Promise<() => void> {
    const { codeVerifier, codeChallenge } = await createPkce();
    // Persist for the redirect-mode fallback (survives a full-page round trip).
    sessionStorage.setItem(SS_PKCE_VERIFIER, codeVerifier);

    // Obtain + set the backend CSRF state cookie; echo THIS state to VK.
    const { state } = await authApi.vkState();
    sessionStorage.setItem(SS_VK_STATE, state);

    VKID.Config.init({
      app: VK_APP_ID,
      redirectUrl: `${window.location.origin}${VK_REDIRECT_PATH}`,
      responseMode: VKID.ConfigResponseMode.Callback,
      source: VKID.ConfigSource.LOWCODE,
      scope: '',
      // PKCE: SDK computes code_challenge_method=S256 from this challenge.
      codeChallenge,
      state,
    });

    const oneTap = new VKID.OneTap();
    oneTap.render({ container, showAlternativeLogin: true });

    oneTap.on(VKID.WidgetEvents.ERROR, (err: unknown) => handlers.onWidgetError(err));
    oneTap.on(VKID.OneTapInternalEvents.LOGIN_SUCCESS, (payload: OneTapSuccessPayload) => {
      exchange(payload.code, payload.device_id, state, codeVerifier).catch(handlers.onExchangeError);
    });

    return () => {
      try {
        oneTap.close();
      } catch {
        /* widget already torn down */
      }
    };
  }

  // Redirect-mode return trip: VK navigated the browser to /auth/redirect with
  // the result in the query. The verifier + state were stashed in sessionStorage
  // when the flow started, and survive the full-page round trip in that tab.
  //
  // Returns null once the exchange has happened and the router has moved on, or
  // the sentence to show when the trip cannot be completed at all — a cancelled
  // login and a verifier left in another tab are both ordinary outcomes, not
  // errors worth a trace id.
  async function completeRedirect(query: LocationQuery): Promise<string | null> {
    const trip = parseVkRedirect(query, {
      state: sessionStorage.getItem(SS_VK_STATE),
      codeVerifier: sessionStorage.getItem(SS_PKCE_VERIFIER),
    });
    if (trip.kind === 'failed') return trip.message;
    await exchange(trip.code, trip.deviceId, trip.state, trip.codeVerifier);
    return null;
  }

  return { mountOneTap, exchange, completeRedirect };
}
