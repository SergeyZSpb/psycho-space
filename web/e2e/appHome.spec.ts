import { expect, test } from '@playwright/test';
import { seedClient, stubBackend } from './fixtures';

// The app's front door.
//
// `/app` itself renders nothing — it is a shell with an index redirect — so
// which section an approved user lands in is a routing decision, made in four
// places at once (the index, two guard redirects, the post-login push) and
// pointed at one constant, HOME_ROUTE_NAME. These tests are what stops those
// four drifting apart: each is a different way of arriving with no route of
// your own, and all of them must end in the same place.
//
// Stubbed rather than full-stack on purpose, and not only because it is cheaper:
// the full-stack suite is serial against one database, its «Ванягоччи» specs
// drive the seeded `user` account's pet through forgetPet/setStats, and merely
// *visiting* the yard creates and moves that pet. A routing assertion there
// would perturb the state those tests set up — which it did, once, before this
// comment existed. Where the router sends you is not a persistence claim.

test('/app sends an approved user to the front door', async ({ page }) => {
  await stubBackend(page, 'user');
  await seedClient(page, 'dark');

  await page.goto('/app');

  await expect(page).toHaveURL(/\/app\/game-vanyagotchi$/);
});

test('the landing sends an already-signed-in user to the same place', async ({ page }) => {
  await stubBackend(page, 'user');
  await seedClient(page, 'dark');

  await page.goto('/');

  await expect(page).toHaveURL(/\/app\/game-vanyagotchi$/);
});

test('a non-admin aiming at the admin page is turned back to the front door', async ({ page }) => {
  await stubBackend(page, 'user');
  await seedClient(page, 'dark');

  await page.goto('/app/admin');

  await expect(page).toHaveURL(/\/app\/game-vanyagotchi$/);
});

test('the front door is the first thing in the nav, so the two agree', async ({ page }) => {
  await stubBackend(page, 'user');
  await seedClient(page, 'dark');

  await page.goto('/app');

  const items = page.locator('.v-navigation-drawer .v-list-item-title');
  await expect(items.first()).toHaveText('Ванягоччи');
});
