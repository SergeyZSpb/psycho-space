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
import { authApi } from '../api/endpoints';
import { useAuthStore } from '../stores/auth';
import { CONSENT_VERSION, SS_PKCE_VERIFIER, SS_VK_STATE, VK_APP_ID } from '../constants';

// Minimal shape we read off the OneTap LOGIN_SUCCESS payload (VKID.RedirectPayload).
interface OneTapSuccessPayload {
  code: string;
  device_id: string;
}

function firstQueryValue(v: LocationQuery[string]): string {
  if (Array.isArray(v)) return v[0] ?? '';
  return v ?? '';
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
      await router.push({ name: 'wishlist' });
    } else {
      await router.push({ name: 'pending' });
    }
  }

  // Mount the OneTap widget into `container`. onError receives VK widget errors
  // and any failure from the backend exchange. Returns a cleanup function.
  async function mountOneTap(
    container: HTMLElement,
    onError: (err: unknown) => void,
  ): Promise<() => void> {
    const { codeVerifier, codeChallenge } = await createPkce();
    // Persist for the redirect-mode fallback (survives a full-page round trip).
    sessionStorage.setItem(SS_PKCE_VERIFIER, codeVerifier);

    // Obtain + set the backend CSRF state cookie; echo THIS state to VK.
    const { state } = await authApi.vkState();
    sessionStorage.setItem(SS_VK_STATE, state);

    VKID.Config.init({
      app: VK_APP_ID,
      redirectUrl: `${window.location.origin}/api/auth/vk/callback`,
      responseMode: VKID.ConfigResponseMode.Callback,
      source: VKID.ConfigSource.LOWCODE,
      scope: '',
      // PKCE: SDK computes code_challenge_method=S256 from this challenge.
      codeChallenge,
      state,
    });

    const oneTap = new VKID.OneTap();
    oneTap.render({ container, showAlternativeLogin: true });

    oneTap.on(VKID.WidgetEvents.ERROR, (err: unknown) => onError(err));
    oneTap.on(VKID.OneTapInternalEvents.LOGIN_SUCCESS, (payload: OneTapSuccessPayload) => {
      exchange(payload.code, payload.device_id, state, codeVerifier).catch(onError);
    });

    return () => {
      try {
        oneTap.close();
      } catch {
        /* widget already torn down */
      }
    };
  }

  // Redirect-mode fallback: VK bounced back with the code in the URL query.
  // The verifier + state were stashed in sessionStorage when we started the flow.
  async function completeRedirect(query: LocationQuery): Promise<void> {
    const code = firstQueryValue(query.code);
    const deviceId = firstQueryValue(query.device_id);
    const state = firstQueryValue(query.state) || sessionStorage.getItem(SS_VK_STATE) || '';
    const codeVerifier = sessionStorage.getItem(SS_PKCE_VERIFIER) ?? '';
    await exchange(code, deviceId, state, codeVerifier);
  }

  return { mountOneTap, exchange, completeRedirect };
}
