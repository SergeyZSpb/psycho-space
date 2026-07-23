// App-wide constants.

// VK ID application id (public). Matches the backend's PSYCHOSPACE_VK_APP_ID.
export const VK_APP_ID = 54691267;

// Consent version recorded on login (152-ФЗ). Bump when the consent text changes.
export const CONSENT_VERSION = 'v1';

// Brand accent (purple-ish). Kept in sync with the Vuetify theme primary color.
export const BRAND_ACCENT = '#8a5cf6';

// localStorage keys.
export const LS_THEME = 'ps-theme';
export const LS_COOKIE_CONSENT = 'ps-cookie-consent';

// sessionStorage keys used by the VK login flow (survive a redirect-mode round trip).
export const SS_PKCE_VERIFIER = 'ps-pkce-verifier';
export const SS_VK_STATE = 'ps-vk-state';

// Operator (152-ФЗ) — shown on the privacy/consent pages.
export const OPERATOR_NAME = 'Зобнин Сергей Сергеевич';
export const OPERATOR_EMAIL = 'sck.spb@yandex.ru';
