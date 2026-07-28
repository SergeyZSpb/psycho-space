// App-wide constants.

/**
 * The login providers. Two, and the list is closed: a provider is a route, a
 * pair of endpoints, a pair of sessionStorage keys and a Russian name, and all
 * four are spelled out per provider rather than derived.
 */
export type OAuthProvider = 'vk' | 'yandex';

// VK ID application id (public). Matches the backend's PSYCHOSPACE_VK_APP_ID.
//
// THE YANDEX CLIENT ID IS DELIBERATELY NOT HERE. VK's SDK builds the authorize
// URL in the browser, so the browser has to know the app id; Yandex needs no
// SDK, so the backend builds that URL instead and the SPA never learns either
// the client id or the redirect URI. Two copies of a value that must agree
// beats three — see internal/httpapi/auth_yandex.go's handleYandexState.
export const VK_APP_ID = 54691267;

// Where VK sends the browser back to. Three copies of this string must agree or
// no login works at all: this one (sent at authorize), the redirect URL
// registered on the VK app, and PSYCHOSPACE_VK_REDIRECT_URI (echoed by the
// backend at the token exchange, which VK matches exactly). It is a SPA route —
// see the header of composables/useVkLogin.ts for why it must never be an API
// endpoint.
export const VK_REDIRECT_PATH = '/auth/redirect';

// Where Yandex sends the browser back to. Its own page rather than VK's,
// because the two return trips carry different things (VK brings a device_id,
// Yandex does not) and because the provider a landing page is finishing must
// come from the ROUTE: a `?provider=` query parameter is attacker-controlled,
// and the whole job of this page is to post an authorization code somewhere.
//
// VK's path cannot move — it is registered on the VK app — so the new provider
// is the one that gets the explicit path.
export const YANDEX_REDIRECT_PATH = '/auth/yandex/redirect';

// The app's front door: where an approved user lands with no route of their
// own. Named once because five places send people there — the /app index, the
// three guard redirects in the router, and the post-login push — and a default
// they disagree about is a bug nobody notices until someone lands in the wrong
// section. Changing this changes all of them.
export const HOME_ROUTE_NAME = 'game-vanyagotchi';

// Consent version recorded on login (152-ФЗ). Bump when the consent text changes.
// v2: disclose the full vkid.personal_info set (added пол + дата рождения).
// v3: a second login provider — the text now names VK ID *or* Яндекс ID, states
//     each provider's granted rights, and says the email address is neither
//     requested nor stored.
export const CONSENT_VERSION = 'v3';

// Brand accent (teal). Kept in sync with the Vuetify dark-theme primary color.
export const BRAND_ACCENT = '#2dd4bf';

// localStorage keys.
export const LS_THEME = 'ps-theme';
export const LS_COOKIE_CONSENT = 'ps-cookie-consent';

/**
 * sessionStorage keys used by a login flow — they survive the full-page round
 * trip a redirect-mode login makes, which is the whole reason they exist.
 *
 * PER PROVIDER, and that is the point of the function. One shared pair meant a
 * half-finished VK login in one tab overwriting the verifier and state of a
 * half-finished Yandex login in another; the second trip back would then find
 * the wrong verifier, and the backend would answer `bad_state` for a reason
 * nobody could see. The backend already scopes its state COOKIE per provider
 * for exactly this reason (`psycho_oauth_state_<provider>`); this is the
 * browser-side half of the same rule.
 *
 * Renaming these strings strands any login already in flight at deploy time —
 * that tab lands on the redirect page, finds no verifier, and is told to start
 * again in this tab, which works. That is the accepted cost of the split.
 */
export const ssVerifierKey = (p: OAuthProvider): string => `ps-pkce-verifier-${p}`;
export const ssStateKey = (p: OAuthProvider): string => `ps-oauth-state-${p}`;

// Operator (152-ФЗ) — shown on the privacy/consent pages.
export const OPERATOR_NAME = 'Зобнин Сергей Сергеевич';
export const OPERATOR_EMAIL = 'sck.spb@yandex.ru';
