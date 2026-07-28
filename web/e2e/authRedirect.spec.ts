import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { seedClient, stubBackend } from './fixtures';

// The two OAuth landing pages.
//
// /auth/redirect is where VK lands the browser when the login could not finish
// in place (its own in-app WebView, a blocked popup, "войти другим способом").
// This is the leg that used to answer 405, because the redirect URL pointed at
// the POST-only API endpoint. The suite that would have caught it is this one:
// only a browser can be navigated to a URL the way VK navigates it.
//
// /auth/yandex/redirect is not a fallback at all — it is how EVERY Яндекс login
// finishes, because there is no in-page SDK for one to fall back from. Which
// makes these three cases the ONLY browser-level coverage that path has.
//
// The provider comes from the route, never from a query parameter, and the two
// paths are separate records for exactly that reason.

// sessionStorage keys from src/constants.ts. Duplicated deliberately: renaming a
// key breaks a redirect login already in flight, so this should fail and be read
// rather than quietly follow the rename.
//
// Per provider since Яндекс arrived, so that a half-finished login in one tab
// cannot clobber the other's verifier.
const SS_KEYS = {
  vk: { verifier: 'ps-pkce-verifier-vk', state: 'ps-oauth-state-vk' },
  yandex: { verifier: 'ps-pkce-verifier-yandex', state: 'ps-oauth-state-yandex' },
} as const;

async function seedPkce(page: Page, provider: 'vk' | 'yandex'): Promise<void> {
  await page.addInitScript(
    ([verifierKey, stateKey]) => {
      try {
        sessionStorage.setItem(verifierKey as string, 'e2e-verifier');
        sessionStorage.setItem(stateKey as string, 'e2e-state');
      } catch {
        /* ignore */
      }
    },
    [SS_KEYS[provider].verifier, SS_KEYS[provider].state] as const,
  );
}

test('a redirect-mode return finishes the login and enters the app', async ({ page }) => {
  await stubBackend(page, 'user');
  await seedClient(page, 'dark');
  await seedPkce(page, 'vk');

  await page.goto('/auth/redirect?code=vk-code&device_id=vk-device&state=e2e-state');

  await expect(page).toHaveURL(/\/app\/game-vanyagotchi$/);
});

test('a cancelled login says so and offers the way back', async ({ page }) => {
  await stubBackend(page, 'anon');
  await seedClient(page, 'dark');
  await seedPkce(page, 'vk');

  await page.goto('/auth/redirect?error=access_denied');

  await expect(page.getByTestId('auth-redirect-message')).toHaveText('вход через ВК отменён');
  await expect(page.getByRole('link', { name: 'на главную' })).toBeVisible();
});

test('a verifier left in another tab is reported as that, not as a failure', async ({ page }) => {
  await stubBackend(page, 'anon');
  await seedClient(page, 'dark');
  // No seedPkce: this tab never started the flow.

  await page.goto('/auth/redirect?code=vk-code&device_id=vk-device&state=from-vk');

  await expect(page.getByTestId('auth-redirect-message')).toContainText('в другой вкладке');
});

// --- Яндекс -------------------------------------------------------------------
// The same three cases, and the first one is the interesting one: there is no
// device_id in that URL, because Яндекс has no such concept. On VK's route the
// identical query would be refused as incomplete.

test('a Яндекс return finishes the login without a device_id and enters the app', async ({
  page,
}) => {
  await stubBackend(page, 'user');
  await seedClient(page, 'dark');
  await seedPkce(page, 'yandex');

  await page.goto('/auth/yandex/redirect?code=yx-code&state=e2e-state');

  await expect(page).toHaveURL(/\/app\/game-vanyagotchi$/);
});

test('a cancelled Яндекс login says so, in Яндекс’s name, and offers the way back', async ({
  page,
}) => {
  await stubBackend(page, 'anon');
  await seedClient(page, 'dark');
  await seedPkce(page, 'yandex');

  await page.goto('/auth/yandex/redirect?error=access_denied');

  await expect(page.getByTestId('auth-redirect-message')).toHaveText('вход через Яндекс отменён');
  await expect(page.getByRole('link', { name: 'на главную' })).toBeVisible();
});

test('a Яндекс verifier left in another tab is reported as that, not as a failure', async ({
  page,
}) => {
  await stubBackend(page, 'anon');
  await seedClient(page, 'dark');
  // No seedPkce: this tab never started the flow. Seeding VK's keys instead
  // would not help it either — that is what per-provider keys mean.

  await page.goto('/auth/yandex/redirect?code=yx-code&state=from-yandex');

  await expect(page.getByTestId('auth-redirect-message')).toContainText('в другой вкладке');
});
