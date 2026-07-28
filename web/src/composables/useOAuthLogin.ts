// The half of a login that is the same whichever provider started it.
//
// Two providers, two very different beginnings — VK mounts an SDK widget in the
// page, Yandex navigates the whole browser to an authorize URL the backend
// built — and one identical ending: post the authorization code to our own
// confidential backend, take the account it answers with, and route by status.
//
// That ending is what lives here. It differs between the two by exactly two
// things: which endpoint to post to, and whether a `device_id` rides along. The
// rest — the consent version, setting the pinia account, the approved/pending
// split — is a property of this application rather than of VK or Yandex, and
// the backend draws the same line in the same place (internal/httpapi/auth.go's
// handleOAuthCallback takes a provider and shares everything else).

import { useRouter } from 'vue-router';
import type { LocationQuery } from 'vue-router';
import { parseOAuthRedirect } from '../lib/oauthRedirect';
import { authApi } from '../api/endpoints';
import type { OAuthCallbackBody } from '../api/endpoints';
import { useAuthStore } from '../stores/auth';
import {
  CONSENT_VERSION,
  HOME_ROUTE_NAME,
  ssStateKey,
  ssVerifierKey,
  type OAuthProvider,
} from '../constants';

export function useOAuthLogin(provider: OAuthProvider) {
  const auth = useAuthStore();
  const router = useRouter();

  /**
   * POST the authorization code to the confidential backend and route on the
   * result.
   *
   * `deviceId` is VK's alone: it is sent for VK and the field is simply absent
   * from a Yandex body, because Yandex has no such concept and a field that
   * means nothing to a provider should not appear on its request.
   */
  async function finishLogin(
    code: string,
    deviceId: string,
    state: string,
    codeVerifier: string,
  ): Promise<void> {
    const body: OAuthCallbackBody = {
      code,
      state,
      code_verifier: codeVerifier,
      consent_version: CONSENT_VERSION,
    };
    if (provider === 'vk') body.device_id = deviceId;

    const result =
      provider === 'vk' ? await authApi.vkCallback(body) : await authApi.yandexCallback(body);

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
   * Redirect-mode return trip: the provider navigated the browser to this
   * provider's landing page with the result in the query. The verifier + state
   * were stashed in per-provider sessionStorage when the flow started, and
   * survive the full-page round trip in that tab.
   *
   * Returns null once the exchange has happened and the router has moved on, or
   * the sentence to show when the trip cannot be completed at all — a cancelled
   * login and a verifier left in another tab are both ordinary outcomes, not
   * errors worth a trace id.
   */
  async function completeRedirect(query: LocationQuery): Promise<string | null> {
    const trip = parseOAuthRedirect(provider, query, {
      state: sessionStorage.getItem(ssStateKey(provider)),
      codeVerifier: sessionStorage.getItem(ssVerifierKey(provider)),
    });
    if (trip.kind === 'failed') return trip.message;
    await finishLogin(trip.code, trip.deviceId, trip.state, trip.codeVerifier);
    return null;
  }

  return { finishLogin, completeRedirect };
}
