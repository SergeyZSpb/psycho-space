import { test, expect, type Page, type Locator } from '@playwright/test';
import { stubBackend, seedClient } from './fixtures';

const THEMES = ['light', 'dark'] as const;

// --- assertions ---------------------------------------------------------------

async function expectNoOverflow(page: Page, label: string): Promise<void> {
  const diff = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(diff, `horizontal overflow on "${label}": scrollWidth exceeds innerWidth by ${diff}px`).toBeLessThanOrEqual(1);
}

function isMobile(page: Page): boolean {
  const vp = page.viewportSize();
  return !!vp && vp.width <= 600;
}

// Tap targets must be >= 44px on their smaller dimension (only enforced on mobile).
async function expectTapTarget(loc: Locator, label: string): Promise<void> {
  await expect(loc, `${label} should be visible`).toBeVisible();
  const box = await loc.boundingBox();
  expect(box, `${label} has no bounding box`).not.toBeNull();
  if (box) {
    const min = Math.round(Math.min(box.width, box.height));
    expect(min, `${label} tap target too small: ${Math.round(box.width)}x${Math.round(box.height)}`).toBeGreaterThanOrEqual(44);
  }
}

const themeToggle = (page: Page) =>
  page.locator('button[aria-label="Тёмная тема"], button[aria-label="Светлая тема"]').first();

// --- tests --------------------------------------------------------------------

for (const theme of THEMES) {
  test.describe(`theme=${theme}`, () => {
    test('landing (hero + consent + cookie banner)', async ({ page }) => {
      await seedClient(page, theme, /* dismissCookie */ false);
      await stubBackend(page, 'anon');
      await page.goto('/');

      await expect(page.getByRole('heading', { name: 'психоспасе' })).toBeVisible();
      await expect(page.getByRole('checkbox')).toBeVisible();
      // cookie banner present (not dismissed) — its width must fit the viewport
      await expect(page.getByText(/Мы используем куки/)).toBeVisible();

      await expectNoOverflow(page, `landing ${theme}`);
      if (isMobile(page)) {
        await expectTapTarget(themeToggle(page), 'landing theme toggle');
      }
    });

    test('pending + blocked', async ({ page }) => {
      await seedClient(page, theme);
      await stubBackend(page, 'anon');

      await page.goto('/pending?status=pending&handle=ab12cd34');
      await expect(page.getByText(/Попроси Сергея/)).toBeVisible();
      await expect(page.getByText('ab12cd34')).toBeVisible();
      await expectNoOverflow(page, `pending ${theme}`);

      await page.goto('/pending?status=blocked');
      await expect(page.getByRole('heading', { name: /Доступ отозван/ })).toBeVisible();
      await expectNoOverflow(page, `blocked ${theme}`);
    });

    test('privacy + consent (long policy text)', async ({ page }) => {
      await seedClient(page, theme);
      await stubBackend(page, 'anon');

      await page.goto('/privacy');
      await expect(page.getByRole('heading', { name: /Политика обработки/ })).toBeVisible();
      await expectNoOverflow(page, `privacy ${theme}`);

      await page.goto('/consent');
      await expect(page.getByRole('heading', { name: /Согласие на обработку/ })).toBeVisible();
      await expectNoOverflow(page, `consent ${theme}`);
    });

    test('wishlist: cards, upvote, add-idea, comments expanded', async ({ page }) => {
      await seedClient(page, theme);
      await stubBackend(page, 'user');
      await page.goto('/app/wishlist');

      await expect(page.getByRole('heading', { name: 'Вишлист', exact: true })).toBeVisible();
      await expect(page.getByText('Тёмная тема для всего')).toBeVisible();
      await expectNoOverflow(page, `wishlist ${theme}`);

      if (isMobile(page)) {
        await expectTapTarget(page.getByRole('button', { name: 'Голос' }).first(), 'item upvote');
        await expectTapTarget(page.getByRole('button', { name: 'Добавить', exact: true }), 'add-idea submit');
      }

      // expand comments on the first item (lazy-loaded)
      await page.getByRole('button', { name: /Комментарии/ }).first().click();
      await expect(page.getByText(/Полностью поддерживаю/)).toBeVisible();
      await expect(page.getByRole('heading', { name: 'Комментарии' })).toBeVisible();
      await expectNoOverflow(page, `wishlist + comments ${theme}`);

      if (isMobile(page)) {
        const section = page.locator('.comment-section').first();
        await expectTapTarget(section.getByRole('button', { name: 'Голос' }).first(), 'comment upvote');
        await expectTapTarget(page.getByRole('button', { name: 'Добавить комментарий' }), 'add-comment submit');
      }
    });

    test('admin: list, actions, superadmin settings switch, tabs', async ({ page }) => {
      await seedClient(page, theme);
      await stubBackend(page, 'superadmin');
      await page.goto('/app/admin');

      await expect(page.getByRole('heading', { name: 'Админка' })).toBeVisible();
      await expect(page.getByText('Обычный Юзер')).toBeVisible();
      await expect(page.locator('.v-switch')).toBeVisible(); // superadmin-only
      await expectNoOverflow(page, `admin ${theme}`);

      if (isMobile(page)) {
        await expectTapTarget(page.getByRole('button', { name: 'принять' }).first(), 'admin approve');
        await expectTapTarget(page.getByRole('button', { name: /отозвать доступ/ }).first(), 'admin block');
      }

      await page.getByRole('tab', { name: 'Одобрены' }).click();
      await expect(page.getByText('Обычный Юзер')).toBeVisible();
      await expectNoOverflow(page, `admin approved tab ${theme}`);
    });

    test('app shell: nav drawer + app-bar actions', async ({ page }) => {
      await seedClient(page, theme);
      await stubBackend(page, 'superadmin');
      await page.goto('/app/wishlist');
      await expect(page.getByRole('heading', { name: 'Вишлист', exact: true })).toBeVisible();

      if (isMobile(page)) {
        await expectTapTarget(page.locator('button[aria-label="Выйти"]'), 'logout');
        await expectTapTarget(themeToggle(page), 'app theme toggle');

        // Nav is a drawer, not a squished row — open it via the app-bar icon.
        await page.locator('.v-app-bar-nav-icon').click();
        const nav = page.locator('.v-navigation-drawer');
        await expect(nav).toBeVisible();
        await expectTapTarget(nav.getByRole('link', { name: 'Вишлист' }), 'nav item: wishlist');
        await expectTapTarget(nav.getByRole('link', { name: 'Админка' }), 'nav item: admin');
        await expectNoOverflow(page, `app nav drawer open ${theme}`);
      } else {
        await expectNoOverflow(page, `app shell ${theme}`);
      }
    });
  });
}
