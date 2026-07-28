import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { seedClient, stubBackend } from './fixtures';

// /auth/redirect — where VK lands the browser when the login could not finish in
// place (its own in-app WebView, a blocked popup, "войти другим способом").
//
// This is the leg that used to answer 405, because the redirect URL pointed at
// the POST-only API endpoint. The suite that would have caught it is this one:
// only a browser can be navigated to a URL the way VK navigates it.

// sessionStorage keys from src/constants.ts. Duplicated deliberately: renaming a
// key breaks a redirect login already in flight, so this should fail and be read
// rather than quietly follow the rename.
const SS_PKCE_VERIFIER = 'ps-pkce-verifier';
const SS_VK_STATE = 'ps-vk-state';

async function seedPkce(page: Page): Promise<void> {
  await page.addInitScript(
    ([verifierKey, stateKey]) => {
      try {
        sessionStorage.setItem(verifierKey as string, 'e2e-verifier');
        sessionStorage.setItem(stateKey as string, 'e2e-state');
      } catch {
        /* ignore */
      }
    },
    [SS_PKCE_VERIFIER, SS_VK_STATE] as const,
  );
}

test('a redirect-mode return finishes the login and enters the app', async ({ page }) => {
  await stubBackend(page, 'user');
  await seedClient(page, 'dark');
  await seedPkce(page);

  await page.goto('/auth/redirect?code=vk-code&device_id=vk-device&state=e2e-state');

  await expect(page).toHaveURL(/\/app\/game-khimki$/);
});

test('a cancelled login says so and offers the way back', async ({ page }) => {
  await stubBackend(page, 'anon');
  await seedClient(page, 'dark');
  await seedPkce(page);

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
