import { expect, test } from '@playwright/test';
import type { Locator, Page, Route } from '@playwright/test';
import { seedClient, stubBackend } from './fixtures';

/**
 * «АДМИН ФИНТЕХА» — the office's control room, at 360 px with `/api` stubbed in
 * the browser, so **the test is the server**.
 *
 * WHAT THIS SUITE IS FOR. The plan is real DOM — no canvas, deliberately, because
 * nothing inside a canvas can be asserted on without pixel comparison — so every
 * claim this page makes is a claim a locator can check: how many desks are drawn,
 * which wall a pane is on, where a spawn is marked, what the status card says.
 * The page reads one endpoint and writes nothing, so there is no state to arrange
 * and no socket to fake; the whole suite is «serve this floor, draw exactly it».
 *
 * THE STUB'S FLOOR IS DELIBERATELY NOT PRODUCTION'S — a different room size, a
 * different number of everything, and a kind this build has never heard of. A
 * page that hardcoded the office it shipped with cannot pass below.
 */

/**
 * The floor the tests are served.
 *
 * COUNTS ARE ALL DIFFERENT FROM EACH OTHER on purpose: four solids, three panes
 * and five spots, so an assertion that counted the wrong list would fail rather
 * than pass by coincidence.
 *
 * `printer` is a kind this client has never heard of, and it is here because a
 * kind is a plain string on the wire precisely so the server can learn a fourth
 * one without breaking a deployed browser. It still collides in the game, so the
 * plan has to draw it — an invisible solid is furniture an admin cannot see they
 * are working around.
 */
const FLOOR = {
  office: { w: 12, h: 18, player_radius: 0.35, boss_radius: 0.4, min_gap: 1.5 },
  layout: {
    id: '0123456789abcdef',
    solids: [
      { x: 2, y: 3, w: 3, h: 1.2, kind: 'desk' },
      { x: 7, y: 3, w: 3, h: 1.2, kind: 'desk' },
      { x: 8, y: 12, w: 0.8, h: 0.8, kind: 'flower' },
      { x: 3, y: 9, w: 1.1, h: 1.1, kind: 'printer' },
    ],
    // ONE PANE PER GLAZED WALL, so all three placements are exercised: they are
    // measured along different axes and from different corners, which is the
    // whole failure mode the band arithmetic has.
    windows: [
      { wall: 'top', at: 1, len: 3 },
      { wall: 'left', at: 4, len: 6 },
      { wall: 'right', at: 2, len: 5 },
    ],
  },
  // Not production's `starting`, so a page that hardcoded the one source the
  // server sends today cannot pass the status assertion.
  source: 'edited',
  installed_at: '2026-08-04T15:37:53.439338Z',
  // Not zero, and not one: an occupant count is the number that decides whether
  // rebuilding this floor throws somebody out, so a default cannot pass for it.
  occupants: 3,
  // Marked, so a legend that typed the taxonomy out instead of reading it cannot
  // pass either — the labels are the server's, not this build's.
  kinds: [
    { key: 'desk', label: 'стол-стенд' },
    { key: 'flower', label: 'цветок-стенд' },
    { key: 'tree', label: 'фикус-стенд' },
  ],
  spots: [
    { x: 6, y: 4, what: 'player' },
    { x: 6, y: 16, what: 'boss' },
    { x: 2, y: 15, what: 'chaser' },
    { x: 1, y: 1, what: 'bottle' },
    { x: 11, y: 1, what: 'bottle' },
  ],
};

type Floor = typeof FLOOR;

/**
 * The floor the REBUILD answers with, and every visible number in it differs
 * from `FLOOR`'s.
 *
 * A DIFFERENT COUNT OF EVERYTHING, a different id and a different source, because
 * that is what tells «the page redrew from the response» apart from «the page
 * left the old floor on the screen». The office is empty afterwards — a rebuild
 * throws everybody out — and `ended` is the two shifts it threw.
 */
const REBUILT: Floor & { ended: number } = {
  ...FLOOR,
  layout: {
    id: 'fedcba9876543210',
    solids: [
      { x: 4, y: 6, w: 2, h: 1, kind: 'desk' },
      { x: 8, y: 13, w: 0.9, h: 0.9, kind: 'tree' },
    ],
    windows: [{ wall: 'top', at: 2, len: 4 }],
  },
  source: 'generated',
  installed_at: '2026-08-04T18:02:11.000000Z',
  occupants: 0,
  ended: 2,
};

interface StubOptions {
  /** Serve this floor instead of `FLOOR`. */
  floor?: Floor;
  /** Refuse the read, as the server does for an approved non-admin. */
  status?: number;
  /** The machine code the refusal carries. */
  code?: string;
  /** Refuse the REBUILD with this status instead of answering `REBUILT`. */
  rerollStatus?: number;
  /** The machine code that refusal carries. */
  rerollCode?: string;
}

/** What the test can ask about the stub after the fact. */
interface FloorStub {
  /** How many rebuilds have actually reached the server. */
  rerolls: () => number;
}

/**
 * The two endpoints this page has.
 *
 * Registered AFTER the shared fixture's catch-all and therefore ahead of it —
 * Playwright matches handlers in reverse registration order — so everything else
 * (`/api/auth/me`, above all) keeps answering exactly as it does for every other
 * suite, and only the floor is this file's business. `fallback()` is what hands
 * the rest back.
 *
 * THE READ AND THE REBUILD ARE SEPARATE PATTERNS, and the read's does not match
 * the rebuild's path: `**` ends the glob, so `…/layout` matches only a URL that
 * ends there. Counting the rebuilds is what lets a test say «cancelling sent
 * nothing», which is a claim about the network rather than about the screen.
 */
async function stubFloor(page: Page, opts: StubOptions = {}): Promise<FloorStub> {
  let rerolls = 0;
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({
      status,
      contentType: 'application/json; charset=utf-8',
      headers: { 'X-Trace-Id': 'e2e-trace-id' },
      body: JSON.stringify(body),
    });

  await page.route('**/api/game-fintech/admin/layout/reroll', async (route) => {
    rerolls += 1;
    if (opts.rerollStatus) {
      return json(route, opts.rerollStatus, {
        error: opts.rerollCode ?? 'internal',
        trace_id: 'e2e-trace-id',
      });
    }
    return json(route, 200, REBUILT);
  });
  await page.route('**/api/game-fintech/admin/layout', async (route) => {
    if (opts.status) {
      return json(route, opts.status, { error: opts.code ?? 'forbidden', trace_id: 'e2e-trace-id' });
    }
    return json(route, 200, opts.floor ?? FLOOR);
  });
  await page.route('**/api/**', (route) => route.fallback());
  return { rerolls: () => rerolls };
}

/** Opens the control room as an admin, with the plan drawn. */
async function openPlan(page: Page, opts: StubOptions = {}): Promise<FloorStub> {
  await stubBackend(page, 'admin');
  const stub = await stubFloor(page, opts);
  await seedClient(page, 'dark');
  await page.goto('/app/game-fintech-admin');
  await expect(page.getByTestId('fintech-admin-plan')).toBeVisible();
  return stub;
}

/** Phone width, where the primary pointer is a thumb rather than a mouse. */
function isMobile(page: Page): boolean {
  const vp = page.viewportSize();
  return !!vp && vp.width <= 600;
}

/**
 * Vuetify's own open/closed signal for the drawer.
 *
 * A closed temporary drawer stays in the DOM, merely slid off-screen, so
 * `toBeVisible()` cannot tell the two states apart — the modifier class can. The
 * layout suite keys off the same class for the same reason.
 */
const DRAWER_OPEN = /v-navigation-drawer--active/;

const drawerEntry = (page: Page): Locator =>
  page.locator('.v-navigation-drawer .v-list-item-title', { hasText: 'АДМИН ФИНТЕХА' });

// --- who can see it -----------------------------------------------------------

test('an admin gets the section in the nav', async ({ page }) => {
  await stubBackend(page, 'admin');
  await stubFloor(page);
  await seedClient(page, 'dark');

  await page.goto('/app');

  await expect(drawerEntry(page)).toHaveCount(1);
});

test('a superadmin gets it too', async ({ page }) => {
  // Gated on `isAdmin` rather than on `isSuperadmin` at the owner's direction, so
  // the higher role has to see it as well — a check that only ever ran as one of
  // the two could not tell those apart.
  await stubBackend(page, 'superadmin');
  await stubFloor(page);
  await seedClient(page, 'dark');

  await page.goto('/app');

  await expect(drawerEntry(page)).toHaveCount(1);
});

test('an ordinary user never sees it', async ({ page }) => {
  await stubBackend(page, 'user');
  await stubFloor(page);
  await seedClient(page, 'dark');

  await page.goto('/app');

  await expect(drawerEntry(page)).toHaveCount(0);
  // And the nav is still the nav: hiding one entry must not have hidden the rest.
  await expect(page.locator('.v-navigation-drawer .v-list-item-title').first()).toHaveText('Ванягоччи');
});

test('the front door is still the first thing in the nav for an admin', async ({ page }) => {
  // The new entry sits after «Админка» and before the divider. This is the
  // assertion that stops it drifting to the top, where it would displace the
  // section every approved user actually lands in (`appHome.spec.ts`).
  await stubBackend(page, 'admin');
  await stubFloor(page);
  await seedClient(page, 'dark');

  await page.goto('/app');

  const titles = page.locator('.v-navigation-drawer .v-list-item-title');
  await expect(titles.first()).toHaveText('Ванягоччи');
  const all = await titles.allTextContents();
  expect(all.indexOf('АДМИН ФИНТЕХА')).toBe(all.indexOf('Админка') + 1);
});

test('a non-admin aiming at the control room is turned back to the front door', async ({ page }) => {
  // The same shape as `appHome.spec.ts`'s admin-page redirect, and for the same
  // reason: the guard is the first lock, and the 403 behind it is the second.
  await stubBackend(page, 'user');
  await stubFloor(page);
  await seedClient(page, 'dark');

  await page.goto('/app/game-fintech-admin');

  await expect(page).toHaveURL(/\/app\/game-vanyagotchi$/);
});

// --- the plan -----------------------------------------------------------------

test('the plan draws exactly the floor it was served', async ({ page }) => {
  await openPlan(page);

  const solids = page.getByTestId('fintech-admin-solid');
  await expect(solids).toHaveCount(FLOOR.layout.solids.length);
  // In the layout's own order, and each carrying what it IS — which is the only
  // thing that decides how it is drawn.
  expect(await solids.evaluateAll((els) => els.map((el) => el.getAttribute('data-kind')))).toEqual([
    'desk',
    'desk',
    'flower',
    'printer',
  ]);

  const panes = page.getByTestId('fintech-admin-window');
  await expect(panes).toHaveCount(FLOOR.layout.windows.length);
  expect(await panes.evaluateAll((els) => els.map((el) => el.getAttribute('data-wall')))).toEqual([
    'top',
    'left',
    'right',
  ]);

  const spots = page.getByTestId('fintech-admin-spot');
  await expect(spots).toHaveCount(FLOOR.spots.length);
  expect(await spots.evaluateAll((els) => els.map((el) => el.getAttribute('data-what')))).toEqual([
    'player',
    'boss',
    'chaser',
    'bottle',
    'bottle',
  ]);
});

test('a solid is placed where the served room puts it', async ({ page }) => {
  await openPlan(page);

  // The second desk: (7, 3) 3 × 1.2 in a 12 × 18 room. Measured against the plan
  // itself rather than against a pixel count, so the claim survives any width.
  const plan = await page.getByTestId('fintech-admin-plan').boundingBox();
  const desk = await page.getByTestId('fintech-admin-solid').nth(1).boundingBox();
  expect(plan).not.toBeNull();
  expect(desk).not.toBeNull();
  expect((desk!.x - plan!.x) / plan!.width).toBeCloseTo(7 / 12, 2);
  expect((desk!.y - plan!.y) / plan!.height).toBeCloseTo(3 / 18, 2);
  expect(desk!.width / plan!.width).toBeCloseTo(3 / 12, 2);
  expect(desk!.height / plan!.height).toBeCloseTo(1.2 / 18, 2);
});

test('a spot is marked where the catalogue keeps furniture off', async ({ page }) => {
  await openPlan(page);

  // The лысый's spawn: (6, 16) in a 12 × 18 room — halfway across and most of the
  // way down. A marker drawn at its own size rather than to scale, so the
  // assertion is about its CENTRE.
  const plan = await page.getByTestId('fintech-admin-plan').boundingBox();
  const boss = await page.getByTestId('fintech-admin-spot').nth(1).boundingBox();
  expect(plan).not.toBeNull();
  expect(boss).not.toBeNull();
  expect((boss!.x + boss!.width / 2 - plan!.x) / plan!.width).toBeCloseTo(6 / 12, 2);
  expect((boss!.y + boss!.height / 2 - plan!.y) / plan!.height).toBeCloseTo(16 / 18, 2);
});

test('a floor with no glazing says so instead of drawing nothing', async ({ page }) => {
  await openPlan(page, { floor: { ...FLOOR, layout: { ...FLOOR.layout, windows: [] } } });

  await expect(page.getByTestId('fintech-admin-window')).toHaveCount(0);
  await expect(page.getByTestId('fintech-admin-no-windows')).toBeVisible();
  // The rest of the plan is unaffected: no windows is a floor, not a failure.
  await expect(page.getByTestId('fintech-admin-solid')).toHaveCount(FLOOR.layout.solids.length);
});

// --- what the plan is of ------------------------------------------------------

test('the status card says where the floor came from, who is on it and which one it is', async ({ page }) => {
  await openPlan(page);

  await expect(page.getByTestId('fintech-admin-source')).toHaveText('изменённый вручную');
  await expect(page.getByTestId('fintech-admin-occupants')).toHaveText(String(FLOOR.occupants));
  await expect(page.getByTestId('fintech-admin-id')).toHaveText(FLOOR.layout.id);
  // The room is the served one rather than the office this build shipped with.
  await expect(page.getByTestId('fintech-admin-size')).toHaveText('12 × 18 м');
  // 15:37 UTC is 18:37 in Moscow, and the readout says which clock it is on.
  const installed = page.getByTestId('fintech-admin-installed');
  await expect(installed).toContainText('04.08.2026');
  await expect(installed).toContainText('18:37');
  await expect(installed).toContainText('МСК');
});

test('the legend names the kinds the server named, not the ones this build knows', async ({ page }) => {
  await openPlan(page);

  const kinds = page.getByTestId('fintech-admin-legend-kind');
  await expect(kinds).toHaveCount(FLOOR.kinds.length);
  await expect(kinds.nth(0)).toContainText('стол-стенд');
  await expect(kinds.nth(1)).toContainText('цветок-стенд');
  await expect(kinds.nth(2)).toContainText('фикус-стенд');

  // The spots are rolled up by marker, with the count beside each — two bottles
  // are one row saying two, not two rows.
  const spots = page.getByTestId('fintech-admin-legend-spot');
  await expect(spots).toHaveCount(4);
  await expect(spots.filter({ hasText: 'бутылка' })).toContainText('2');
  await expect(spots.filter({ hasText: 'старт игрока' })).toHaveCount(1);
});

test('a refusal is a sentence rather than an empty room', async ({ page }) => {
  // The guard turns a non-admin back at the door, so a 403 here means the two
  // locks disagree — a role that changed under a tab that was already open.
  await stubBackend(page, 'admin');
  await stubFloor(page, { status: 403, code: 'forbidden' });
  await seedClient(page, 'dark');

  await page.goto('/app/game-fintech-admin');

  await expect(page.getByTestId('fintech-admin-error')).toContainText('администратора');
  await expect(page.getByTestId('fintech-admin-plan')).toHaveCount(0);
});

test('an unwired game says so, and a failure quotes its code', async ({ page }) => {
  await stubBackend(page, 'admin');
  await stubFloor(page, { status: 503, code: 'game_unavailable' });
  await seedClient(page, 'dark');

  await page.goto('/app/game-fintech-admin');

  await expect(page.getByTestId('fintech-admin-error')).toContainText('не подключена');
});

// --- rebuilding it ------------------------------------------------------------

test('the rebuild is behind a confirmation that names who is in the office', async ({ page }) => {
  // THE COUNT IS THE WHOLE POINT OF THE DIALOG. «Пересобрать офис?» on its own is
  // a question about geometry; the same question carrying the live occupant count
  // is a question about three people's shifts. The stub says three, so a page that
  // typed a number or dropped it cannot pass.
  const stub = await openPlan(page);

  await expect(page.getByTestId('fintech-admin-reroll-dialog')).toHaveCount(0);
  await page.getByTestId('fintech-admin-reroll').click();

  const warning = page.getByTestId('fintech-admin-reroll-warning');
  await expect(warning).toBeVisible();
  await expect(warning).toContainText('3 человека');
  await expect(warning).toContainText('их смены закончатся');
  // Asking is not doing: nothing has reached the server yet.
  expect(stub.rerolls()).toBe(0);
});

test('cancelling the confirmation sends nothing and changes nothing', async ({ page }) => {
  const stub = await openPlan(page);

  await page.getByTestId('fintech-admin-reroll').click();
  await expect(page.getByTestId('fintech-admin-reroll-dialog')).toBeVisible();
  await page.getByTestId('fintech-admin-reroll-cancel').click();

  await expect(page.getByTestId('fintech-admin-reroll-dialog')).toBeHidden();
  expect(stub.rerolls(), 'cancelling rebuilt the office').toBe(0);
  // And the floor on the screen is the one that was always there.
  await expect(page.getByTestId('fintech-admin-id')).toHaveText(FLOOR.layout.id);
  await expect(page.getByTestId('fintech-admin-solid')).toHaveCount(FLOOR.layout.solids.length);
  await expect(page.getByTestId('fintech-admin-rebuild-result')).toHaveCount(0);
});

test('confirming rebuilds the floor and redraws the page from the answer', async ({ page }) => {
  // THE POINT OF THE IDENTICAL PAYLOAD. The rebuild replies with the read's own
  // shape plus the count, so the page must draw the NEW floor without a second
  // request — this stub answers the read exactly once, and the plan below is the
  // rebuilt one.
  const stub = await openPlan(page);

  await page.getByTestId('fintech-admin-reroll').click();
  await page.getByTestId('fintech-admin-reroll-confirm').click();

  await expect(page.getByTestId('fintech-admin-reroll-dialog')).toBeHidden();
  expect(stub.rerolls()).toBe(1);

  // Every visible fact about the floor moved, and each of them differs from
  // `FLOOR`'s, so a page that redrew half of it fails here.
  await expect(page.getByTestId('fintech-admin-id')).toHaveText(REBUILT.layout.id);
  await expect(page.getByTestId('fintech-admin-solid')).toHaveCount(REBUILT.layout.solids.length);
  await expect(page.getByTestId('fintech-admin-window')).toHaveCount(REBUILT.layout.windows.length);
  await expect(page.getByTestId('fintech-admin-source')).toHaveText('сгенерированный');
  await expect(page.getByTestId('fintech-admin-occupants')).toHaveText('0');

  // AND IT SAYS WHAT IT COST, which is the half nobody can see: the new floor is
  // on the screen and the people thrown off the old one are somewhere else.
  const result = page.getByTestId('fintech-admin-rebuild-result');
  await expect(result).toBeVisible();
  await expect(result).toContainText('Закончились 2 смены');
  await expect(page.getByTestId('fintech-admin-rebuild-error')).toHaveCount(0);

  // The confirmation now names the office it has just emptied, not the old one.
  await page.getByTestId('fintech-admin-reroll').click();
  await expect(page.getByTestId('fintech-admin-reroll-warning')).toContainText('никого');
});

test('a refused rebuild says so and leaves the floor exactly as it was', async ({ page }) => {
  // A REFUSED INSTALL CHANGES NOTHING ON THE SERVER — the people working carry on,
  // on the floor they are standing on — so the page must not redraw, and the
  // message has to say that rather than leaving somebody wondering.
  const stub = await openPlan(page, { rerollStatus: 500, rerollCode: 'internal' });

  await page.getByTestId('fintech-admin-reroll').click();
  await page.getByTestId('fintech-admin-reroll-confirm').click();

  const err = page.getByTestId('fintech-admin-rebuild-error');
  await expect(err).toBeVisible();
  await expect(err).toContainText('internal');
  await expect(err).toContainText('прежним');

  expect(stub.rerolls()).toBe(1);
  await expect(page.getByTestId('fintech-admin-id')).toHaveText(FLOOR.layout.id);
  await expect(page.getByTestId('fintech-admin-solid')).toHaveCount(FLOOR.layout.solids.length);
  await expect(page.getByTestId('fintech-admin-occupants')).toHaveText(String(FLOOR.occupants));
  await expect(page.getByTestId('fintech-admin-rebuild-result')).toHaveCount(0);
  // The button is usable again: a failure is not a dead end.
  await expect(page.getByTestId('fintech-admin-reroll')).toBeEnabled();
});

// --- the phone ----------------------------------------------------------------

test('the whole page fits a 360 px screen', async ({ page }) => {
  await openPlan(page);

  // Horizontal overflow, measured the way the layout suite measures it.
  const diff = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(diff, `horizontal overflow: scrollWidth exceeds innerWidth by ${diff}px`).toBeLessThanOrEqual(1);

  // The plan itself is inside the screen, not merely un-scrolled.
  const plan = await page.getByTestId('fintech-admin-plan').boundingBox();
  expect(plan).not.toBeNull();
  const vp = page.viewportSize()!;
  expect(plan!.x).toBeGreaterThanOrEqual(0);
  expect(plan!.x + plan!.width).toBeLessThanOrEqual(vp.width + 1);
});

test('everything you can tap clears 44 px', async ({ page }) => {
  test.skip(!isMobile(page), 'a tap-target floor is a rule about thumbs');
  await openPlan(page);

  // The way IN is a tap target, and it is the only one this read-only page owns
  // today: nothing on the plan is pressable, deliberately — the editor is a
  // later iteration and a disabled control for it would be scaffolding.
  //
  // THE SHELL PEEKS THE DRAWER OPEN ON LOAD, so the click below has to wait for
  // that to finish or it would CLOSE the drawer rather than open it. Bounded by
  // the assertion's own deadline rather than by a sleep of the peek's length.
  const drawer = page.locator('.v-navigation-drawer');
  await expect(drawer).not.toHaveClass(DRAWER_OPEN);
  await page.locator('.v-app-bar-nav-icon').click();
  await expect(drawer).toHaveClass(DRAWER_OPEN);

  const entry = drawer.getByRole('link', { name: 'АДМИН ФИНТЕХА' });
  await expect(entry).toBeVisible();
  const box = await entry.boundingBox();
  expect(box).not.toBeNull();
  expect(Math.round(Math.min(box!.width, box!.height))).toBeGreaterThanOrEqual(44);

  // And anything the page itself grows later is held to the same floor.
  const controls = page.locator('main button, main a[href], main [role="button"]');
  for (const control of await controls.all()) {
    if (!(await control.isVisible())) continue;
    const b = await control.boundingBox();
    if (!b) continue;
    expect(Math.round(Math.min(b.width, b.height))).toBeGreaterThanOrEqual(44);
  }
});

test('the confirmation is a thumb-sized decision and fits a 360 px screen', async ({ page }) => {
  test.skip(!isMobile(page), 'a tap-target floor is a rule about thumbs');
  // THE DIALOG IS TELEPORTED OUT OF `main`, so the sweep above cannot see either
  // of its buttons — and they are the two that matter most: one of them is
  // destructive and the other is the way out of it.
  await openPlan(page);
  await page.getByTestId('fintech-admin-reroll').click();
  await expect(page.getByTestId('fintech-admin-reroll-dialog')).toBeVisible();

  for (const id of ['fintech-admin-reroll-confirm', 'fintech-admin-reroll-cancel']) {
    const box = await page.getByTestId(id).boundingBox();
    expect(box, `${id} has no box at all`).not.toBeNull();
    expect(Math.round(Math.min(box!.width, box!.height)), id).toBeGreaterThanOrEqual(44);
  }

  const diff = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(diff, `the open dialog overflows by ${diff}px`).toBeLessThanOrEqual(1);
});

test('the plan is drawn in the page’s own ink, so both themes work', async ({ page }) => {
  // THE PLAN HAS NO PALETTE OF ITS OWN for the floor and the grid: they are the
  // page's ink at a few per cent, so the room is pale on a light theme and dark
  // on a dark one with nothing to keep in step. This is the assertion that stops
  // somebody replacing that with a fixed slab, which would look right in exactly
  // one theme — and it drives the real toggle rather than reloading with a
  // different seed, because that is how a person changes theme.
  await openPlan(page);
  const floorColour = () =>
    page
      .getByTestId('fintech-admin-plan')
      .evaluate((el) => getComputedStyle(el).backgroundColor);
  const ink = (colour: string) =>
    (colour.match(/\d+(\.\d+)?/g) ?? []).slice(0, 3).reduce((sum, part) => sum + Number(part), 0);

  const dark = await floorColour();
  await page.locator('button[aria-label="Светлая тема"]').click();
  await expect(page.locator('button[aria-label="Тёмная тема"]')).toBeVisible();
  const light = await floorColour();

  expect(light).not.toBe(dark);
  // A dark theme paints the floor in LIGHT ink and a light theme in dark ink, so
  // the plan is legible either way rather than merely different.
  expect(ink(dark)).toBeGreaterThan(ink(light));
});

test('the plan keeps the served ratio on a desktop', { tag: '@wide' }, async ({ page }) => {
  // THE CLAIM IS ABOUT WIDTH, which is what earns the tag: the plan is capped by
  // the viewport's HEIGHT as well as by its column, and only above phone width is
  // the height cap the binding one. A plan drawn at any other shape lies about
  // how far apart two desks are, which is the one thing it exists to show.
  await openPlan(page);

  const plan = await page.getByTestId('fintech-admin-plan').boundingBox();
  expect(plan).not.toBeNull();
  expect(plan!.width / plan!.height).toBeCloseTo(FLOOR.office.w / FLOOR.office.h, 2);

  // And it is really bounded by the screen rather than running off the bottom.
  const vp = page.viewportSize()!;
  expect(plan!.height).toBeLessThanOrEqual(vp.height);
});
