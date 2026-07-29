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

/**
 * Refuse every request to a login provider's own hosts.
 *
 * Ticking consent mounts the real VK OneTap widget, which reaches out to VK to
 * personalise its button. A test that let it would be a test of somebody else's
 * uptime; aborting instead makes the run deterministic and offline. Nothing
 * breaks — a widget that cannot personalise itself is not an error, it fires
 * its ERROR event and the composable deliberately swallows it (commit b6d4632,
 * pinned by src/__tests__/vkLoginErrors.spec.ts).
 */
async function blockProviderOrigins(page: Page): Promise<void> {
  await page.route(/https?:\/\/([^/]*\.)?(vk\.com|vk\.ru|vkid\.ru|userapi\.com|yandex\.[a-z]+)\//, (route) =>
    route.abort(),
  );
}

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
    __drawerStateTimes?: number[];
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
    const at: number[] = [];
    window.__drawerStates = states;
    window.__drawerStateTimes = at;
    const t0 = performance.now();
    const record = () => {
      const open = !!document.querySelector('.v-navigation-drawer.v-navigation-drawer--active');
      if (states.length === 0 || states[states.length - 1] !== open) {
        states.push(open);
        at.push(performance.now() - t0);
      }
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

/**
 * How long after the drawer FIRST APPEARS a change still counts as mounting
 * rather than as motion.
 *
 * Anchored on the drawer's own arrival and not on page load, which is the whole
 * subtlety. Under load the shell can take most of a second to mount — measured
 * at 850 ms on a saturated machine — so any window measured from page start is
 * either too short to cover mounting or long enough to swallow a real peek.
 * Measured from the drawer appearing, mount churn is a frame or two and a peek
 * is DRAWER_PEEK_MS, and nothing in between exists.
 */
const SETTLE_MS = Math.min(200, DRAWER_PEEK_MS / 2);

const drawerStateTimes = (page: Page): Promise<number[]> =>
  page.evaluate(() => window.__drawerStateTimes ?? []);

const drawerStates = (page: Page): Promise<boolean[]> =>
  page.evaluate(() => window.__drawerStates ?? []);

/**
 * Is the drawer PERMANENT at this viewport rather than an overlay?
 *
 * `AppShell.vue` binds `:permanent="mdAndUp"`, and Vuetify's `md` starts at
 * 960px. Where the drawer is permanent the nav is already on screen, so there is
 * nothing to reveal and `shouldPeekDrawer` returns false — every peek assertion
 * below is therefore a claim about the temporary drawer and has to say so.
 *
 * This became load-bearing the day a desktop project was added to the config.
 * Before that every project was under 960px and the distinction could not be
 * observed, which is precisely why it went untested for so long.
 */
const hasPermanentDrawer = (page: Page): boolean => (page.viewportSize()?.width ?? 0) >= 960;

// Wait for the automatic peek to run its course: closed (no drawer yet), open,
// closed again. Every assertion after this one is about a settled drawer.
//
// Where the drawer is permanent there is no peek to wait for, and asserting one
// happened anyway would be asserting a bug. What is checked instead is the other
// half of the same rule: the nav is simply there, and it never moved on its own.
async function expectPeekCompleted(page: Page, label: string): Promise<void> {
  if (hasPermanentDrawer(page)) {
    await expect(drawer(page), `${label}: a permanent drawer should be on screen`).toBeVisible();
    await expect(drawer(page)).toHaveClass(DRAWER_OPEN);
    // Never an overlay, so never a scrim over the page behind it.
    await expect(drawerScrim(page)).toHaveCount(0);
    // Asserted as "opened once and never closed" rather than as an exact
    // history, because the recorder starts before the app does: whether a
    // leading `false` is captured depends on how much of the shell exists at
    // the first observation, and that is a race rather than a claim. What is a
    // claim is that once the drawer is there it stays — a permanent drawer that
    // closed by itself would have hidden the nav.
    const states = await drawerStates(page);
    const opened = states.indexOf(true);
    expect(opened, `${label}: the permanent drawer never appeared (${states.join(',')})`).toBeGreaterThanOrEqual(0);
    expect(
      states.slice(opened),
      `${label}: a permanent drawer must not peek — it is already open, and closing it would hide the nav`,
    ).toEqual([true]);
    return;
  }
  await expect
    .poll(() => drawerStates(page), {
      message: `${label}: the drawer should open by itself on load and then close by itself`,
    })
    .toEqual([false, true, false]);
}

// --- tests --------------------------------------------------------------------

for (const theme of THEMES) {
  // Tagged @wide: this file IS the layout suite — overflow, the never-scroll
  // shell and the permanent nav drawer (≥960px) are exactly the claims that can
  // fail above phone width, and the drawer branch runs nowhere else.
  test.describe(`theme=${theme}`, { tag: '@wide' }, () => {
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

    test('landing: BOTH login affordances are behind the consent gate', async ({ page }) => {
      // 152-ФЗ: consent precedes any processing of personal data, and both ways
      // in start that processing. So the gate is a legal requirement rather
      // than a nicety, and adding a second provider is exactly the change that
      // could have let one slip out from behind it.
      //
      // The layout claim rides along: two affordances plus a divider have to
      // fit 360px without overflowing, and the Яндекс button is a real tap
      // target rather than a text link.
      await blockProviderOrigins(page);
      await seedClient(page, theme);
      await stubBackend(page, 'anon');
      await page.goto('/');

      const vkLogin = page.getByTestId('login-vk');
      const yandexLogin = page.getByTestId('login-yandex');

      await expect(vkLogin).toBeHidden();
      await expect(yandexLogin).toBeHidden();
      await expect(page.getByText(/поставь галочку выше/)).toBeVisible();

      await page.getByRole('checkbox').check();

      await expect(vkLogin).toBeVisible();
      await expect(yandexLogin).toBeVisible();
      await expect(yandexLogin).toHaveText(/Войти с Яндекс ID/);
      // The «или» between them, so the two do not read as one stacked control.
      await expect(page.getByText('или', { exact: true })).toBeVisible();

      await expectNoOverflow(page, `landing both logins ${theme}`);
      if (isMobile(page)) {
        await expectTapTarget(yandexLogin, 'Яндекс login button');
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

    test('admin: forgetting a user is superadmin-only, confirmed, and says what stays', async ({
      page,
    }) => {
      // Irreversible, so the dialog has to be honest about BOTH halves — what
      // is destroyed and what survives. The half that survives is the one
      // people do not expect, and "забыть" sounds gentler than it is.
      await seedClient(page, theme);
      await stubBackend(page, 'superadmin');
      await page.goto('/app/admin');

      const forget = page.getByTestId('admin-forget').first();
      await expect(forget).toBeVisible();
      if (isMobile(page)) await expectTapTarget(forget, 'forget button');

      await forget.click();
      const dialog = page.getByTestId('admin-forget-dialog');
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('обезличен');
      await expect(dialog).toContainText('останется'); // the content is kept
      await expect(dialog).toContainText('новый аккаунт'); // and a re-login is a new one
      await expectNoOverflow(page, `admin forget dialog ${theme}`);
      if (isMobile(page)) {
        await expectTapTarget(page.getByTestId('admin-forget-confirm'), 'forget confirm');
        // The way OUT of an irreversible dialog deserves a real target too.
        await expectTapTarget(page.getByRole('button', { name: 'Отмена' }), 'forget cancel');
      }

      // Cancelling leaves everything alone, which is the whole point of asking.
      await page.getByRole('button', { name: 'Отмена' }).click();
      await expect(dialog).toBeHidden();

      await page.getByTestId('admin-forget').first().click();
      await page.getByTestId('admin-forget-confirm').click();
      await expect(page.getByTestId('admin-forget-dialog')).toBeHidden();
    });

    test('admin: an ordinary admin is never offered the forget button', async ({ page }) => {
      // The route is superadmin-only and answers 403, but a button that only
      // ever fails is a worse way to learn that than not having one.
      await seedClient(page, theme);
      await stubBackend(page, 'user');
      await page.goto('/app/admin');
      // A plain user is bounced out of the admin section entirely.
      await expect(page).not.toHaveURL(/\/app\/admin$/);
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
      if (hasPermanentDrawer(page)) {
        // Everything below this point is about a drawer that opens and closes.
        // A permanent one does neither — it IS the nav, always on screen — so
        // there is no peek to have happened, no icon to reopen it with, and no
        // route change that could replay it. `expectPeekCompleted` has already
        // asserted the whole of what this test can mean at this width.
        await expectNoOverflow(page, `app shell permanent drawer ${theme}`);
        return;
      }
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

      // A permanent drawer starts open and stays open; a temporary one must not
      // move at all. Either way the claim is that nothing MOVED on its own —
      // which for the permanent case means it never closed, and for the
      // temporary case means it never opened. The preference is about
      // unrequested motion, and a drawer that was already on screen has none to
      // make.
      const states = await drawerStates(page);
      if (hasPermanentDrawer(page)) {
        const opened = states.indexOf(true);
        expect(opened, `the permanent drawer never appeared (${states.join(',')})`).toBeGreaterThanOrEqual(0);
        // Every transition after the shell has SETTLED. The recorder starts at
        // DOMContentLoaded, before the SPA has mounted, so the first few
        // transitions are the app arriving rather than anything moving on its
        // own: Vuetify renders the drawer and then resolves the `mdAndUp`
        // breakpoint, and on a loaded machine that resolution can land after a
        // paint in which the permanent drawer was already active — which reads
        // as a close-and-reopen and is neither motion nor unrequested.
        //
        // Time is what tells the two apart, and it is what the preference is
        // actually about. A peek is a deliberate animation lasting
        // DRAWER_PEEK_MS; mount churn is over in a frame or two.
        const times = await drawerStateTimes(page);
        // Everything before the drawer exists is absence, not a close, and the
        // first moments after it appears are the shell settling.
        const closes = states
          .map((open, i) => ({ open, at: times[i] }))
          .filter((s, i) => i > opened && !s.open && s.at > times[opened] + SETTLE_MS);
        expect(
          closes,
          `the drawer closed by itself after settling (${states.join(',')} at ${times.map(Math.round).join(',')}ms)`,
        ).toEqual([]);
        await expect(drawer(page)).toHaveClass(DRAWER_OPEN);
      } else {
        expect(states, 'the drawer must never open by itself').toEqual([false]);
        await expect(drawer(page)).not.toHaveClass(DRAWER_OPEN);
      }
      await expect(drawerScrim(page)).toHaveCount(0);
      await expectNoOverflow(page, `app shell reduced-motion ${theme}`);

      if (hasPermanentDrawer(page)) {
        // There is no icon to press, and that is right rather than a gap: the
        // shell binds it `v-if="!mdAndUp"`, because a button that toggles a
        // drawer which cannot be closed is a control with nothing to do. The
        // nav is reachable without one — which is the actual thing worth
        // asserting, and the reason the missing button is not a regression.
        await expect(page.locator('.v-app-bar-nav-icon')).toHaveCount(0);
        await expect(drawer(page).getByRole('link', { name: 'Вишлист' })).toBeVisible();
        return;
      }
      // The nav still works, it just waits to be asked.
      await page.locator('.v-app-bar-nav-icon').click();
      await expect(drawer(page)).toHaveClass(DRAWER_OPEN);
      await expectNoOverflow(page, `app shell reduced-motion drawer open ${theme}`);
    });
  });
}
