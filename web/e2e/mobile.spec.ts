import { test, expect, type Page, type Locator } from '@playwright/test';
import { stubBackend, seedClient } from './fixtures';
import { DRAWER_PEEK_MS } from '../src/lib/drawerPeek';

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

// --- the nav drawer -----------------------------------------------------------

// Vuetify's own open/closed signal. A closed drawer stays in the DOM, merely
// slid off-screen, so `toBeVisible()` cannot tell the two states apart — the
// modifier class can.
const DRAWER_OPEN = /v-navigation-drawer--active/;
const drawer = (page: Page) => page.locator('.v-navigation-drawer');
// The scrim exists only while a temporary drawer is open: proof that the drawer
// is really over the page rather than just flagged open.
const drawerScrim = (page: Page) => page.locator('.v-navigation-drawer__scrim');

declare global {
  interface Window {
    __drawerStates?: boolean[];
  }
}

// Record every open/closed transition of the drawer from before the app boots.
//
// The shell peeks the drawer for well under a second, which is easily short
// enough for a busy test runner to miss between two polled assertions. Observing
// from the page removes the race entirely: the transitions are captured as they
// happen and asserted afterwards, so the test cannot pass by arriving late.
async function recordDrawerStates(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const states: boolean[] = [];
    window.__drawerStates = states;
    const record = () => {
      const open = !!document.querySelector('.v-navigation-drawer.v-navigation-drawer--active');
      if (states.length === 0 || states[states.length - 1] !== open) states.push(open);
    };
    const start = () => {
      record();
      new MutationObserver(record).observe(document.body, {
        subtree: true,
        childList: true,
        attributes: true,
        attributeFilter: ['class'],
      });
    };
    if (document.body) start();
    else document.addEventListener('DOMContentLoaded', start, { once: true });
  });
}

const drawerStates = (page: Page): Promise<boolean[]> =>
  page.evaluate(() => window.__drawerStates ?? []);

// Wait for the automatic peek to run its course: closed (no drawer yet), open,
// closed again. Every assertion after this one is about a settled drawer.
async function expectPeekCompleted(page: Page, label: string): Promise<void> {
  await expect
    .poll(() => drawerStates(page), {
      message: `${label}: the drawer should open by itself on load and then close by itself`,
    })
    .toEqual([false, true, false]);
}

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

    test('pending + blocked (handle from /me, auto-refresh + Проверить)', async ({ page }) => {
      // Pending users now have a session; handle/status come from /api/auth/me.
      await seedClient(page, theme);
      await stubBackend(page, 'pending');

      await page.goto('/pending');
      await expect(page.getByText(/Попроси Сергея/)).toBeVisible();
      await expect(page.getByText('ab12cd34')).toBeVisible();
      await expect(page.getByText(/Страница обновляется автоматически/)).toBeVisible();
      await expectNoOverflow(page, `pending ${theme}`);
      if (isMobile(page)) {
        await expectTapTarget(page.getByRole('button', { name: 'Проверить' }), 'pending refresh');
      }
      // Manual re-check keeps a still-pending user on the page.
      await page.getByRole('button', { name: 'Проверить' }).click();
      await expect(page.getByText(/Попроси Сергея/)).toBeVisible();

      // Blocked variant.
      await stubBackend(page, 'blocked');
      await page.goto('/pending');
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

    test('wishlist: cards, upvote, add-idea, comments expanded by default, delete, toggle', async ({ page }) => {
      await seedClient(page, theme);
      await stubBackend(page, 'user');
      await page.goto('/app/wishlist');

      await expect(page.getByRole('heading', { name: 'Вишлист', exact: true })).toBeVisible();
      await expect(page.getByText('Тёмная тема для всего')).toBeVisible();

      // Comments show by default now (no click). Same stubbed comments per item.
      await expect(page.getByText(/Полностью поддерживаю/).first()).toBeVisible();
      await expect(page.getByRole('heading', { name: 'Комментарии' }).first()).toBeVisible();
      await expectNoOverflow(page, `wishlist (comments expanded) ${theme}`);

      // A standard user sees delete only on their own idea (i3) + own comment (c2).
      await expect(page.getByRole('button', { name: 'Удалить идею' })).toHaveCount(1);
      await expect(page.getByRole('button', { name: 'Удалить комментарий' }).first()).toBeVisible();

      if (isMobile(page)) {
        await expectTapTarget(page.getByRole('button', { name: 'Голос' }).first(), 'item upvote');
        await expectTapTarget(page.getByRole('button', { name: 'Добавить', exact: true }), 'add-idea submit');
        await expectTapTarget(page.getByRole('button', { name: 'Удалить идею' }), 'item delete');
        const section = page.locator('.comment-section').first();
        await expectTapTarget(section.getByRole('button', { name: 'Голос' }).first(), 'comment upvote');
        await expectTapTarget(page.getByRole('button', { name: 'Добавить комментарий' }).first(), 'add-comment submit');
      }

      // Click-to-toggle: collapsing the first item removes its comments section.
      const before = await page.locator('.comment-section').count();
      await page.getByRole('button', { name: /Комментарии/ }).first().click();
      await expect(page.locator('.comment-section')).toHaveCount(before - 1);
    });

    test('admin: list, actions, settings switch, role controls, tabs', async ({ page }) => {
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

      // Approved tab → superadmin role controls: promote / demote / superadmin label.
      await page.getByRole('tab', { name: 'Одобрены' }).click();
      await expect(page.getByText('Обычный Юзер')).toBeVisible();
      await expect(page.getByRole('button', { name: 'Сделать админом' }).first()).toBeVisible();
      await expect(page.getByRole('button', { name: 'Разжаловать' }).first()).toBeVisible();
      await expect(page.getByText('суперадмин').first()).toBeVisible();
      await expectNoOverflow(page, `admin approved tab ${theme}`);
      if (isMobile(page)) {
        await expectTapTarget(page.getByRole('button', { name: 'Сделать админом' }).first(), 'promote button');
      }
    });

    test('app shell: nav drawer + app-bar actions', async ({ page }) => {
      await recordDrawerStates(page);
      await seedClient(page, theme);
      await stubBackend(page, 'superadmin');
      await page.goto('/app/wishlist');
      await expect(page.getByRole('heading', { name: 'Вишлист', exact: true })).toBeVisible();

      // The shell peeks the drawer open on load. Let it finish before driving the
      // nav by hand, so the click below is a real open and not a cancelled peek.
      await expectPeekCompleted(page, `app shell ${theme}`);

      if (isMobile(page)) {
        await expectTapTarget(page.locator('button[aria-label="Выйти"]'), 'logout');
        await expectTapTarget(themeToggle(page), 'app theme toggle');

        // Nav is a drawer, not a squished row — open it via the app-bar icon.
        await page.locator('.v-app-bar-nav-icon').click();
        const nav = drawer(page);
        await expect(nav).toBeVisible();
        await expect(nav).toHaveClass(DRAWER_OPEN);
        await expectTapTarget(nav.getByRole('link', { name: 'Вишлист' }), 'nav item: wishlist');
        await expectTapTarget(nav.getByRole('link', { name: 'Админка' }), 'nav item: admin');
        await expectNoOverflow(page, `app nav drawer open ${theme}`);
      } else {
        await expectNoOverflow(page, `app shell ${theme}`);
      }
    });

    test('app shell: the nav drawer peeks itself open on load, then closes', async ({ page }) => {
      await recordDrawerStates(page);
      await seedClient(page, theme);
      await stubBackend(page, 'superadmin');

      // A deep link straight into the app — the case the peek exists for.
      await page.goto('/app/wishlist');
      await expect(page.getByRole('heading', { name: 'Вишлист', exact: true })).toBeVisible();

      const nav = drawer(page);

      // Nothing in this test clicks, taps, or types: the drawer opens and closes
      // entirely on its own.
      await expectPeekCompleted(page, `peek ${theme}`);
      await expect(nav).not.toHaveClass(DRAWER_OPEN);
      await expect(drawerScrim(page)).toHaveCount(0);

      // The layout invariants hold on both sides of the peek.
      await expectNoOverflow(page, `app shell after peek ${theme}`);
      await page.locator('.v-app-bar-nav-icon').click();
      await expect(nav).toHaveClass(DRAWER_OPEN);
      await expectNoOverflow(page, `app shell peek reopened ${theme}`);
      if (isMobile(page)) {
        await expectTapTarget(nav.getByRole('link', { name: 'Вишлист' }), 'peeked nav item: wishlist');
        await expectTapTarget(
          nav.getByRole('link', { name: 'Смолтолк в Химках' }),
          'peeked nav item: game-khimki',
        );
      }

      // Once per page load: a client-side route change must not replay the peek.
      // Vuetify closes a temporary drawer when the route changes, so the whole
      // visit should read as exactly two open/close pairs — the peek, then the
      // one the click above opened.
      await nav.getByRole('link', { name: 'Админка' }).click();
      await expect(page).toHaveURL(/\/app\/admin$/);
      await expect(page.getByRole('heading', { name: 'Админка' })).toBeVisible();
      await expect(nav).not.toHaveClass(DRAWER_OPEN);
      await expect.poll(() => drawerStates(page)).toEqual([false, true, false, true, false]);
    });
    // A user who has asked for less motion gets no unrequested slide-in at all.
    // The preference is emulated per page rather than via `test.use`, which this
    // Playwright version does not honour for `reducedMotion`.
    test('app shell: no drawer peek under prefers-reduced-motion', async ({ page }) => {
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await recordDrawerStates(page);
      await seedClient(page, theme);
      await stubBackend(page, 'superadmin');
      await page.goto('/app/wishlist');
      await expect(page.getByRole('heading', { name: 'Вишлист', exact: true })).toBeVisible();

      // Proving a non-event needs elapsed time rather than a condition to wait
      // on: sit out the whole window in which a peek would have happened.
      await page.waitForTimeout(DRAWER_PEEK_MS * 2);

      expect(await drawerStates(page), 'the drawer must never open by itself').toEqual([false]);
      await expect(drawer(page)).not.toHaveClass(DRAWER_OPEN);
      await expect(drawerScrim(page)).toHaveCount(0);
      await expectNoOverflow(page, `app shell reduced-motion ${theme}`);

      // The nav still works, it just waits to be asked.
      await page.locator('.v-app-bar-nav-icon').click();
      await expect(drawer(page)).toHaveClass(DRAWER_OPEN);
      await expectNoOverflow(page, `app shell reduced-motion drawer open ${theme}`);
    });
  });
}
