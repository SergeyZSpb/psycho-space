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
 * which wall a pane is on, where a spawn is marked, what the status card says,
 * and — now that the plan is a constructor — where a desk ended up after
 * somebody dragged it.
 *
 * THE READOUT IS WHAT MAKES A DRAG ASSERTABLE. A gesture on a plan produces a
 * position, and a position on a plan is pixels; this project does not compare
 * pixels, so the editor states the selection in metres in real DOM and the drag
 * tests read that. It is the reason the readout exists, not a convenience it
 * happens to offer.
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

/**
 * The floor «СЛУЧАЙНЫЙ» draws, and it installs nothing.
 *
 * A DIFFERENT COUNT OF EVERYTHING AGAIN, so «the draft was filled from the
 * proposal» is distinguishable from «the draft was left alone» and from «the
 * page rebuilt the office», which is the one thing this button must never do.
 */
const PROPOSED = {
  solids: [
    { x: 2, y: 6, w: 2, h: 1, kind: 'desk' },
    { x: 6, y: 6, w: 2, h: 1, kind: 'desk' },
    { x: 2, y: 12, w: 1, h: 1, kind: 'tree' },
  ],
  windows: [{ wall: 'left' as const, at: 3, len: 4 }],
};

/** One thing the validator says is wrong, and what it is about. */
interface Problem {
  problem: string;
  index: number;
}

/** A bare layout, which is what both editor endpoints take. */
interface Draft {
  solids: { x: number; y: number; w: number; h: number; kind: string }[];
  windows: { wall: string; at: number; len: number }[];
}

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
  /** Answer every check with exactly this, whatever the draft actually is. */
  checkOverride?: Problem[];
  /** Refuse the SAVE with 422 and these problems, as an illegal floor is refused. */
  refuseSave?: Problem[];
  /** Refuse the save with a plain failure instead — a 500, a 403. */
  saveStatus?: number;
  /** The machine code that plain failure carries. */
  saveCode?: string;
}

/** What the test can ask about the stub after the fact. */
interface FloorStub {
  /** How many rebuilds have actually reached the server. */
  rerolls: () => number;
  /** How many drafts have been put to the validator. */
  checks: () => number;
  /** How many floors have been drawn without being installed. */
  proposals: () => number;
  /** The body of the last `PUT …/layout`, or null when nothing was ever saved. */
  saved: () => Draft | null;
}

/**
 * The stub's own opinion of a draft.
 *
 * DELIBERATELY THE SERVER'S RULES AND NOT CONVENIENT ONES, as far as two of them
 * go: every solid keeps `min_gap` from every wall and from every other solid,
 * measured PER AXIS — which is the shape the collision resolver cares about and
 * not the euclidean distance — and an unsound rectangle suppresses the pairwise
 * pass exactly as `ValidateLayout` does. A `too_close` is reported against the
 * SECOND of the pair, which is the one somebody has just dragged.
 *
 * The rest of the validator (the spot rule, the connectivity flood fill, the
 * glazing) is not here, and does not need to be: what this suite asserts is that
 * the page ASKS, marks whatever comes back, and refuses to save until the answer
 * is empty and current. Which rules produced the answer is the server's business
 * and is tested where the validator lives.
 */
function faults(draft: Draft, room: Floor['office']): Problem[] {
  const gap = room.min_gap;
  const wall = (s: Draft['solids'][number]) =>
    Math.min(Math.min(s.x, s.y), Math.min(room.w - (s.x + s.w), room.h - (s.y + s.h)));
  const unsound = draft.solids.flatMap((s, i) => (wall(s) < gap ? [{ problem: 'off_floor', index: i }] : []));
  if (unsound.length > 0) return unsound;

  const sep = (a: Draft['solids'][number], b: Draft['solids'][number]) =>
    Math.max(
      Math.max(0, Math.max(b.x - (a.x + a.w), a.x - (b.x + b.w))),
      Math.max(0, Math.max(b.y - (a.y + a.h), a.y - (b.y + b.h))),
    );
  const out: Problem[] = [];
  for (let i = 0; i < draft.solids.length; i++) {
    for (let j = i + 1; j < draft.solids.length; j++) {
      if (sep(draft.solids[i], draft.solids[j]) < gap) out.push({ problem: 'too_close', index: j });
    }
  }
  return out;
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
  const served = opts.floor ?? FLOOR;
  let rerolls = 0;
  let checks = 0;
  let proposals = 0;
  let saved: Draft | null = null;
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
  await page.route('**/api/game-fintech/admin/layout/proposal', async (route) => {
    proposals += 1;
    return json(route, 200, { layout: { id: 'proposed000000', ...PROPOSED } });
  });
  await page.route('**/api/game-fintech/admin/layout/check', async (route) => {
    checks += 1;
    const draft = route.request().postDataJSON() as Draft;
    return json(route, 200, { problems: opts.checkOverride ?? faults(draft, served.office) });
  });
  await page.route('**/api/game-fintech/admin/layout', async (route) => {
    // ONE PATTERN, TWO VERBS, because that is what the server does: the read and
    // the install are the same resource. Splitting them into two Playwright
    // routes would let a page that used the wrong method still pass.
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON() as Draft;
      saved = body;
      if (opts.refuseSave) {
        // A REFUSED INSTALL CHANGES NOTHING, so the stub does not move either:
        // `saved` records what was asked for, and no floor is handed back.
        return json(route, 422, { error: 'layout_invalid', problems: opts.refuseSave });
      }
      if (opts.saveStatus) {
        return json(route, opts.saveStatus, {
          error: opts.saveCode ?? 'internal',
          trace_id: 'e2e-trace-id',
        });
      }
      return json(route, 200, {
        ...served,
        layout: { id: 'saved00000000ab', solids: body.solids, windows: body.windows },
        source: 'edited',
        installed_at: '2026-08-04T19:30:00.000000Z',
        occupants: 0,
        ended: 1,
      });
    }
    if (opts.status) {
      return json(route, opts.status, { error: opts.code ?? 'forbidden', trace_id: 'e2e-trace-id' });
    }
    return json(route, 200, served);
  });
  await page.route('**/api/**', (route) => route.fallback());
  return {
    rerolls: () => rerolls,
    checks: () => checks,
    proposals: () => proposals,
    saved: () => saved,
  };
}

/**
 * The lattice, mirrored here so every expectation below is DERIVED from the
 * pixels rather than typed out.
 *
 * A hardcoded «the desk ends at 9,50» would pass on a plan of the wrong size, a
 * plan with a border, and a plan whose pixel-to-metre map was inverted. Deriving
 * it from the plan's own bounding box is what makes one drag test prove the
 * mapping, the snap and the clamp at once.
 */
const GRID = 0.25;
const snap = (v: number): number => Math.round(v / GRID) * GRID;
const m = (v: number): string => v.toFixed(2).replace('.', ',');

/**
 * The plan's box and what one metre is worth on it, in pixels.
 *
 * IT SCROLLS THE PLAN INTO VIEW FIRST, and that is not tidiness: `page.mouse`
 * takes VIEWPORT coordinates and does no scrolling of its own, so on a 360 × 800
 * phone — where the status card alone pushes the plan most of the way down the
 * fold — every gesture below would otherwise be aimed at whatever happens to be
 * on screen at that coordinate, or at nothing at all.
 */
async function planScale(page: Page, room: Floor['office'] = FLOOR.office) {
  // AND IT WAITS FOR THE DRAWER TO STOP PEEKING — the SCRIM specifically, not the
  // drawer. The shell slides the nav open by itself on load and closes it again
  // about a second later, and the scrim behind it covers the WHOLE viewport while
  // it fades. So a raw mouse press aimed at a desk lands on the scrim, selects
  // nothing, and produces a failure that says «ничего не выбрано» with no hint of
  // why — and waiting on `v-navigation-drawer--active` is not enough, because
  // that class comes off the instant the close begins while the scrim is still
  // there for the length of its fade. The scrim is removed from the DOM when it
  // has finished, so its absence is the honest signal. Bounded by the
  // assertion's own deadline rather than by a sleep of the transition's length.
  await expect(page.locator('.v-navigation-drawer__scrim')).toHaveCount(0);
  const plan = page.getByTestId('fintech-admin-plan');
  await plan.scrollIntoViewIfNeeded();
  const box = await plan.boundingBox();
  expect(box, 'the plan has no box at all').not.toBeNull();
  return { box: box!, perX: box!.width / room.w, perY: box!.height / room.h };
}

/** Where a point in office metres lands on the screen, to the nearest pixel. */
function atMetres(
  box: { x: number; y: number },
  scale: { perX: number; perY: number },
  x: number,
  y: number,
): { x: number; y: number } {
  return { x: Math.round(box.x + x * scale.perX), y: Math.round(box.y + y * scale.perY) };
}

/** The line the editor writes about whatever is selected. */
const readout = (page: Page): Locator => page.getByTestId('fintech-admin-readout');

/**
 * Drags with a real mouse from one point to another.
 *
 * INTEGER PIXELS THROUGHOUT, which is what lets the expectation be computed from
 * the same delta the browser was given: a fractional coordinate is rounded
 * somewhere inside Chromium, and then the test and the page are working from two
 * slightly different numbers.
 */
async function drag(page: Page, from: { x: number; y: number }, to: { x: number; y: number }): Promise<void> {
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 8 });
  await page.mouse.up();
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

// --- the constructor ----------------------------------------------------------

test('nothing is selected until something is tapped', async ({ page }) => {
  const stub = await openPlan(page);

  await expect(page.getByTestId('fintech-admin-editor')).toBeVisible();
  await expect(readout(page)).toHaveText('ничего не выбрано');
  // The steppers and the bin act on the selection, so with none they are dead
  // controls rather than controls that quietly edit whatever was last touched.
  await expect(page.getByTestId('fintech-admin-delete')).toBeDisabled();
  await expect(page.getByTestId('fintech-admin-step-x-up')).toBeDisabled();
  // And nothing destructive is reachable on an untouched floor.
  await expect(page.getByTestId('fintech-admin-apply')).toBeDisabled();
  await expect(page.getByTestId('fintech-admin-revert')).toBeDisabled();

  // OPENING THE PAGE ASKS NOTHING. A draft identical to the floor in force needs
  // no judgement — the server has already judged that floor by installing it — so
  // a check on load would be a round trip spent to be told what is already known,
  // on every visit, for ever.
  //
  // A SLEEP, and it is the right shape here: the claim is that nothing happens
  // inside a window, and there is no condition whose becoming true would make
  // «nothing happened» knowable sooner. The window is comfortably longer than the
  // 400 ms debounce that would have fired.
  await page.waitForTimeout(1000);
  expect(stub.checks(), 'the page asked about a floor nobody had touched').toBe(0);
});

test('tapping a desk selects it and states where it is, in metres', async ({ page }) => {
  await openPlan(page);
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];

  // Dead centre of the second desk: (7, 3), 3 × 1.2 in a 12 × 18 room.
  const at = atMetres(scale.box, scale, desk.x + desk.w / 2, desk.y + desk.h / 2);
  await page.mouse.click(at.x, at.y);

  await expect(readout(page)).toHaveText(
    `стол-стенд · X ${m(desk.x)} · Y ${m(desk.y)} · Ш ${m(desk.w)} · В ${m(desk.h)}`,
  );
  // The name comes from the SERVER's taxonomy, so a page that typed «стол» out
  // cannot pass — the stub calls it «стол-стенд».
  await expect(page.getByTestId('fintech-admin-solid').nth(1)).toHaveAttribute('data-selected', '1');
});

test('a tap on bare floor selects nothing, and the small thing beside a desk is reachable', async ({
  page,
}) => {
  // THE TWO-PASS PICK, ASSERTED FROM BOTH SIDES. A single pass taking the nearest
  // object within a thumb's width would answer «the printer» for a tap in the
  // middle of the room — 22 px is over a metre on this plan — and would make the
  // flowerpot beside a desk unselectable for ever.
  await openPlan(page);
  const scale = await planScale(page);

  const empty = atMetres(scale.box, scale, 6.5, 7.5);
  await page.mouse.click(empty.x, empty.y);
  await expect(readout(page)).toHaveText('ничего не выбрано');

  const flower = FLOOR.layout.solids[2];
  const on = atMetres(scale.box, scale, flower.x + flower.w / 2, flower.y + flower.h / 2);
  await page.mouse.click(on.x, on.y);
  await expect(readout(page)).toContainText('цветок-стенд');
});

test('a drag moves the desk by exactly the metres those pixels were worth', async ({ page }) => {
  // THE ONE TEST THAT PROVES THE WHOLE MAPPING. It derives the expected metres
  // from the plan's own bounding box and the served room, so it checks three
  // things at once that would otherwise need three tests and a lot of trust: that
  // pixels become metres against the right rectangle, that the result lands on
  // the quarter-metre lattice, and that the readout is telling the truth about
  // where the box actually is.
  await openPlan(page);
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];

  // Grabbed near the desk's left end, well clear of the corner handle — which
  // only appears once something is selected, but this same gesture is what
  // selects it.
  const from = atMetres(scale.box, scale, desk.x + 0.5, desk.y + desk.h / 2);

  // A DELIBERATELY AWKWARD DISTANCE: 2.5 m across and 2 m down expressed in whole
  // pixels is not a whole number of quarter-metres once it is converted back, so
  // the snap has something to do.
  //
  // LEFT AND DOWN, and the direction matters: the room is only 12 m across and
  // this desk is 3 m of it standing at 7, so the same distance to the RIGHT would
  // hit the wall and the clamp would be what decided the answer. Clamping has its
  // own test; this one is about the mapping, so it stays well inside the room and
  // says so.
  const dx = -Math.round(2.5 * scale.perX);
  const dy = Math.round(2 * scale.perY);
  const raw = { x: desk.x + dx / scale.perX, y: desk.y + dy / scale.perY };
  expect(raw.x % GRID, 'pick a delta the lattice actually has to move').not.toBe(0);
  expect(raw.x, 'the drag must not reach a wall').toBeGreaterThan(0);
  expect(raw.y + desk.h, 'the drag must not reach a wall').toBeLessThan(FLOOR.office.h);

  await drag(page, from, { x: from.x + dx, y: from.y + dy });

  const want = { x: snap(raw.x), y: snap(raw.y) };
  await expect(readout(page)).toHaveText(
    `стол-стенд · X ${m(want.x)} · Y ${m(want.y)} · Ш ${m(desk.w)} · В ${m(desk.h)}`,
  );

  // AND THE BOX ITSELF WENT THERE, which is what stops the readout being a
  // number the page merely computed and never drew.
  const drawn = await page.getByTestId('fintech-admin-solid').nth(1).boundingBox();
  expect(drawn).not.toBeNull();
  expect(drawn!.x - scale.box.x).toBeCloseTo(want.x * scale.perX, 0);
  expect(drawn!.y - scale.box.y).toBeCloseTo(want.y * scale.perY, 0);
});

test('a drag stops at the wall instead of leaving the room', async ({ page }) => {
  // The one judgement this client is allowed to make. Everything past «stays in
  // the box, lands on the grid» is the server's word — but a control that lets
  // you drag a desk into the car park is simply broken.
  await openPlan(page);
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];
  const from = atMetres(scale.box, scale, desk.x + 0.5, desk.y + desk.h / 2);

  await drag(page, from, { x: Math.round(scale.box.x) - 400, y: Math.round(scale.box.y) - 400 });
  await expect(readout(page)).toContainText(`X ${m(0)} · Y ${m(0)}`);

  await drag(
    page,
    atMetres(scale.box, scale, 0.5, desk.h / 2),
    { x: Math.round(scale.box.x + scale.box.width) + 400, y: Math.round(scale.box.y + scale.box.height) + 400 },
  );
  // The FAR edge is what stops at the wall, so a 3 × 1.2 desk in a 12 × 18 room
  // ends with its origin at (9, 16.8) rather than at (12, 18).
  await expect(readout(page)).toContainText(
    `X ${m(FLOOR.office.w - desk.w)} · Y ${m(FLOOR.office.h - desk.h)}`,
  );
});

test('the corner handle is a real tap target and is not clipped by the plan', async ({ page }) => {
  // A SOLID MAY STAND AT THE EDGE OF THE ROOM, and its handle hangs off the
  // corner — so a plan with `overflow: hidden` collapses the handle to nothing,
  // leaving a control a thumb cannot find and `boundingBox()` cannot measure.
  // `elementFromPoint` is what proves it is actually hittable there rather than
  // merely laid out.
  await openPlan(page);
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];

  // Push it hard against the RIGHT wall first, which is the case that used to
  // disappear. Sideways rather than into the corner on purpose: `overflow:
  // hidden` clips in both directions, so one axis proves it is not set — and the
  // bottom corner of this plan sits at the very bottom of an 800 px phone, where
  // `elementFromPoint` has nothing to answer with.
  await drag(
    page,
    atMetres(scale.box, scale, desk.x + 0.5, desk.y + desk.h / 2),
    { x: Math.round(scale.box.x + scale.box.width) + 400, y: atMetres(scale.box, scale, 0, desk.y + desk.h / 2).y },
  );

  const handle = page.getByTestId('fintech-admin-handle');
  await expect(handle).toBeVisible();
  const box = await handle.boundingBox();
  expect(box, 'the handle has no box at all').not.toBeNull();
  expect(Math.round(Math.min(box!.width, box!.height))).toBeGreaterThanOrEqual(44);

  const hit = await page.evaluate(
    ([x, y]) => document.elementFromPoint(x as number, y as number)?.getAttribute('data-testid') ?? '',
    [box!.x + box!.width / 2, box!.y + box!.height / 2],
  );
  expect(hit, 'something is painted over the handle at its own centre').toBe('fintech-admin-handle');
});

test('the handle resizes down to the smallest thing the server will take', async ({ page }) => {
  await openPlan(page);
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];

  const on = atMetres(scale.box, scale, desk.x + 0.5, desk.y + desk.h / 2);
  await page.mouse.click(on.x, on.y);
  const handle = await page.getByTestId('fintech-admin-handle').boundingBox();
  expect(handle).not.toBeNull();

  // Dragged hard back towards the origin: the far corner stays, so the box
  // shrinks until it hits the floor the validator sets and stops there.
  await drag(
    page,
    { x: Math.round(handle!.x + handle!.width / 2), y: Math.round(handle!.y + handle!.height / 2) },
    { x: Math.round(scale.box.x) - 400, y: Math.round(scale.box.y) - 400 },
  );

  // 0.75 rather than the server's own 0.6: the smallest side this editor can
  // produce is the first lattice step at or above it, so a drag can never make a
  // box the validator refuses outright.
  await expect(readout(page)).toContainText(`Ш ${m(0.75)} · В ${m(0.75)}`);
  // And the corner it was not holding did not move.
  await expect(readout(page)).toContainText(`X ${m(desk.x)} · Y ${m(desk.y)}`);
});

test('the palette adds what the server named, somewhere you can see it', async ({ page }) => {
  // The kinds are the SERVER's, exactly as the legend's are: a fourth kind of
  // thing on the floor is a backend change and no deploy here.
  await openPlan(page);
  const solids = page.getByTestId('fintech-admin-solid');
  await expect(solids).toHaveCount(FLOOR.layout.solids.length);

  await page.getByTestId('fintech-admin-add-tree').click();

  await expect(solids).toHaveCount(FLOOR.layout.solids.length + 1);
  // Selected on arrival, so the readout names it and the steppers act on it.
  await expect(readout(page)).toContainText('фикус-стенд');
  await expect(solids.last()).toHaveAttribute('data-kind', 'tree');

  // AND THE SECOND ONE IS NOT INSIDE THE FIRST, which is the whole usefulness of
  // the placement search: a palette that drops everything on one square makes
  // the admin's first act always «drag it off again».
  const first = await readout(page).textContent();
  await page.getByTestId('fintech-admin-add-tree').click();
  await expect(readout(page)).not.toHaveText(first ?? '');
});

test('the palette glazes a wall, and a pane is edited by its steppers', async ({ page }) => {
  // A PANE IS FIVE PIXELS OF WALL, which is a third of the smallest thing a thumb
  // can hit — so it is placed by a button, selected by that button or by Tab, and
  // moved by the steppers. There is no honest way to grab one.
  await openPlan(page);
  const panes = page.getByTestId('fintech-admin-window');
  await expect(panes).toHaveCount(FLOOR.layout.windows.length);

  await page.getByTestId('fintech-admin-add-window-right').click();

  await expect(panes).toHaveCount(FLOOR.layout.windows.length + 1);
  await expect(readout(page)).toContainText('окно · право');
  // The steppers change shape with the selection: a pane has an offset and a
  // length rather than four corners.
  await expect(page.getByTestId('fintech-admin-step-at-up')).toBeVisible();
  await expect(page.getByTestId('fintech-admin-step-x-up')).toHaveCount(0);

  const before = await readout(page).textContent();
  await page.getByTestId('fintech-admin-step-at-up').click();
  await expect(readout(page)).not.toHaveText(before ?? '');
  await expect(readout(page)).toContainText('окно · право');
});

test('the bin removes exactly what is selected and then has nothing to remove', async ({ page }) => {
  await openPlan(page);
  const scale = await planScale(page);
  const solids = page.getByTestId('fintech-admin-solid');
  const flower = FLOOR.layout.solids[2];

  const on = atMetres(scale.box, scale, flower.x + flower.w / 2, flower.y + flower.h / 2);
  await page.mouse.click(on.x, on.y);
  await expect(readout(page)).toContainText('цветок-стенд');

  await page.getByTestId('fintech-admin-delete').click();

  await expect(solids).toHaveCount(FLOOR.layout.solids.length - 1);
  // The kinds that are left, in order — a deletion that removed the wrong one
  // would keep the count and fail here.
  expect(await solids.evaluateAll((els) => els.map((el) => el.getAttribute('data-kind')))).toEqual([
    'desk',
    'desk',
    'printer',
  ]);
  // Cleared rather than moved to a neighbour: the array has just renumbered, so
  // any index kept would be pointing at whatever slid into the gap.
  await expect(readout(page)).toHaveText('ничего не выбрано');
  await expect(page.getByTestId('fintech-admin-delete')).toBeDisabled();
});

test('an illegal floor is named, marked and unsavable until it is fixed', async ({ page }) => {
  // THE WHOLE POINT OF ASKING THE SERVER. This client never decides whether a
  // floor is playable — it drags a box into the room, asks, and shows the answer.
  const stub = await openPlan(page);
  const scale = await planScale(page);
  const flower = FLOOR.layout.solids[2];

  // The flowerpot dragged eight metres straight up, which parks it under the
  // second desk with no gap at all — well inside the 1.5 m the validator keeps
  // between two things on the floor.
  const middle = { x: flower.x + flower.w / 2, y: flower.y + flower.h / 2 };
  await drag(
    page,
    atMetres(scale.box, scale, middle.x, middle.y),
    atMetres(scale.box, scale, middle.x, middle.y - 8),
  );
  await expect(readout(page)).toContainText(`X ${m(flower.x)} · Y ${m(4)}`);

  const problems = page.getByTestId('fintech-admin-problem');
  await expect(problems).toHaveCount(1);
  await expect(problems.first()).toContainText('слишком близко');
  // NAMED AND MARKED, both: the panel says which object and the plan shows it,
  // because a list of complaints about a room you are looking at is no use if it
  // does not point. The validator reports a pair against its SECOND member,
  // which is the one that has just been dragged.
  await expect(problems.first()).toContainText('цветок-стенд 3');
  await expect(page.getByTestId('fintech-admin-solid').nth(2)).toHaveAttribute('data-problem', '1');
  await expect(page.getByTestId('fintech-admin-apply')).toBeDisabled();

  // Pulled four metres back down into open floor, and the same round trip clears
  // it — the page never decides this for itself, it asks again.
  await drag(
    page,
    atMetres(scale.box, scale, middle.x, 4 + flower.h / 2),
    atMetres(scale.box, scale, middle.x, 4 + flower.h / 2 + 4),
  );
  await expect(readout(page)).toContainText(`X ${m(flower.x)} · Y ${m(8)}`);

  await expect(page.getByTestId('fintech-admin-check')).toHaveAttribute('data-state', 'ok');
  await expect(page.getByTestId('fintech-admin-problem')).toHaveCount(0);
  await expect(page.getByTestId('fintech-admin-solid').nth(2)).not.toHaveAttribute('data-problem', '1');
  await expect(page.getByTestId('fintech-admin-apply')).toBeEnabled();
  // Every one of those was a question actually put to the server.
  expect(stub.checks()).toBeGreaterThan(0);
});

test('«СЛУЧАЙНЫЙ» fills the draft and installs nothing at all', async ({ page }) => {
  // PRESSING IT HAS TO BE FREE, which is why it is a different endpoint from the
  // rebuild: trying three offices before keeping one must not throw three rooms
  // full of people out on the way.
  const stub = await openPlan(page);

  await page.getByTestId('fintech-admin-random').click();

  await expect(page.getByTestId('fintech-admin-solid')).toHaveCount(PROPOSED.solids.length);
  await expect(page.getByTestId('fintech-admin-window')).toHaveCount(PROPOSED.windows.length);
  expect(stub.proposals()).toBe(1);
  // Nothing was installed: the floor in force is the one it always was.
  expect(stub.rerolls()).toBe(0);
  expect(stub.saved()).toBeNull();
  await expect(page.getByTestId('fintech-admin-id')).toHaveText(FLOOR.layout.id);
  await expect(page.getByTestId('fintech-admin-source')).toHaveText('изменённый вручную');

  // And «ОТМЕНИТЬ» puts the draft back to the floor people are standing on.
  await page.getByTestId('fintech-admin-revert').click();
  await expect(page.getByTestId('fintech-admin-solid')).toHaveCount(FLOOR.layout.solids.length);
  await expect(page.getByTestId('fintech-admin-revert')).toBeDisabled();
});

test('saving sends exactly the floor on the screen, and only after a confirmation', async ({
  page,
}) => {
  const stub = await openPlan(page);
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];

  const on = atMetres(scale.box, scale, desk.x + desk.w / 2, desk.y + desk.h / 2);
  await page.mouse.click(on.x, on.y);
  // Two lattice steps to the right, which stays legal — the point of this test is
  // the payload, not the validator.
  await page.getByTestId('fintech-admin-step-x-up').click();
  await page.getByTestId('fintech-admin-step-x-up').click();
  await expect(readout(page)).toContainText(`X ${m(desk.x + 2 * GRID)}`);

  await expect(page.getByTestId('fintech-admin-apply')).toBeEnabled();
  await page.getByTestId('fintech-admin-apply').click();
  // THE COST IS NAMED IN PEOPLE, exactly as the rebuild's is: installing a
  // hand-drawn floor ends every shift in progress too, so the careful path must
  // not read as the cheap one.
  await expect(page.getByTestId('fintech-admin-apply-warning')).toContainText('3 человека');
  expect(stub.saved(), 'asking is not doing').toBeNull();

  await page.getByTestId('fintech-admin-apply-confirm').click();

  await expect(page.getByTestId('fintech-admin-save-result')).toContainText('Этаж поставлен');
  const sent = stub.saved();
  expect(sent).not.toBeNull();
  // THE BODY IS THE DRAFT AND NOTHING ELSE — geometry, no id. The id is a content
  // hash the server computes from exactly these two lists, so a client that sent
  // one would be sending a claim about a value it does not own.
  expect(sent).toEqual({
    solids: FLOOR.layout.solids.map((s, i) => (i === 1 ? { ...s, x: s.x + 2 * GRID } : { ...s })),
    windows: FLOOR.layout.windows.map((w) => ({ ...w })),
  });

  // And the page redrew from the answer: a new id, a new source, an empty office.
  await expect(page.getByTestId('fintech-admin-id')).toHaveText('saved00000000ab');
  await expect(page.getByTestId('fintech-admin-occupants')).toHaveText('0');
  await expect(page.getByTestId('fintech-admin-apply')).toBeDisabled();
});

test('a refused floor is explained rather than swallowed, and nothing moves', async ({ page }) => {
  // A 422 CARRIES THE PROBLEMS IN ITS BODY, which is the one failure in this
  // client where the payload is worth more than the code: «what is wrong with the
  // office I just tried to save» is the entire answer.
  const stub = await openPlan(page, {
    checkOverride: [],
    refuseSave: [
      { problem: 'split_floor', index: -1 },
      { problem: 'spot_blocked', index: 0 },
    ],
  });
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];

  const on = atMetres(scale.box, scale, desk.x + desk.w / 2, desk.y + desk.h / 2);
  await page.mouse.click(on.x, on.y);
  await page.getByTestId('fintech-admin-step-x-up').click();
  await expect(page.getByTestId('fintech-admin-apply')).toBeEnabled();

  await page.getByTestId('fintech-admin-apply').click();
  await page.getByTestId('fintech-admin-apply-confirm').click();

  await expect(page.getByTestId('fintech-admin-save-error')).toContainText('Так поставить нельзя');
  const problems = page.getByTestId('fintech-admin-problem');
  await expect(problems).toHaveCount(2);
  await expect(problems.nth(0)).toContainText('весь этаж');
  await expect(problems.nth(1)).toContainText('стол-стенд 1');
  await expect(page.getByTestId('fintech-admin-solid').nth(0)).toHaveAttribute('data-problem', '1');

  // NOTHING MOVED ON THE SERVER, so nothing moves here: the floor in force is
  // the one it was, the draft is still the one somebody was working on, and the
  // button is shut until they change something.
  expect(stub.saved()).not.toBeNull();
  await expect(page.getByTestId('fintech-admin-id')).toHaveText(FLOOR.layout.id);
  await expect(page.getByTestId('fintech-admin-occupants')).toHaveText(String(FLOOR.occupants));
  await expect(readout(page)).toContainText(`X ${m(desk.x + GRID)}`);
  await expect(page.getByTestId('fintech-admin-apply')).toBeDisabled();
  await expect(page.getByTestId('fintech-admin-save-result')).toHaveCount(0);
});

test('the whole editor works from the keyboard alone', async ({ page }) => {
  // TAP AND KEYBOARD BOTH WORK is a rule of this project, and here it is load-
  // bearing rather than a courtesy: SELECTION has no other keyboard route, so
  // without the cycle the bin and every stepper are unreachable without a
  // pointer. Escape is what stops the plan being a trap — it drops the selection
  // and the focus, and Tab carries on out of the widget.
  await openPlan(page);
  const solids = page.getByTestId('fintech-admin-solid');

  // Add: focused and pressed, not clicked.
  await page.getByTestId('fintech-admin-add-flower').focus();
  await page.keyboard.press('Enter');
  await expect(solids).toHaveCount(FLOOR.layout.solids.length + 1);
  await expect(readout(page)).toContainText('цветок-стенд');
  const placed = await readout(page).textContent();

  // Nudge: one lattice step per press, and the readout is how anybody knows.
  await page.keyboard.press('ArrowRight');
  await expect(readout(page)).not.toHaveText(placed ?? '');
  await page.keyboard.press('ArrowLeft');
  await expect(readout(page)).toHaveText(placed ?? '');

  // Cycle: forwards onto the next object and back again.
  await page.keyboard.press('Tab');
  await expect(readout(page)).not.toHaveText(placed ?? '');
  await page.keyboard.press('Shift+Tab');
  await expect(readout(page)).toHaveText(placed ?? '');

  // Remove, and then leave.
  await page.keyboard.press('Delete');
  await expect(solids).toHaveCount(FLOOR.layout.solids.length);
  await expect(readout(page)).toHaveText('ничего не выбрано');

  await page.keyboard.press('Tab');
  await expect(readout(page)).not.toHaveText('ничего не выбрано');
  await page.keyboard.press('Escape');
  await expect(readout(page)).toHaveText('ничего не выбрано');
  const stillOnPlan = await page.getByTestId('fintech-admin-plan').evaluate(
    (el) => document.activeElement === el,
  );
  expect(stillOnPlan, 'Escape has to let a keyboard out of the plan').toBe(false);
});

test('a drag under a thumb is a drag rather than a scroll', async ({ page }) => {
  // `touch-action: none` is the difference between moving a desk and the page
  // sliding out from under the finger that was moving it — the same rule the
  // game's own stick is held to.
  await openPlan(page);
  const touchAction = await page
    .getByTestId('fintech-admin-plan')
    .evaluate((el) => getComputedStyle(el).touchAction);
  expect(touchAction).toBe('none');
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

test('a handle hanging off the corner of the room does not push the page sideways', async ({
  page,
}) => {
  // THE PRICE OF NOT CLIPPING THE PLAN. The handle is 48 px centred on a solid's
  // far corner, so a solid shoved into the bottom-right corner puts 24 px of it
  // outside the room — and this is the check that the container's own margin
  // absorbs that rather than the document growing a scrollbar.
  await openPlan(page);
  const scale = await planScale(page);
  const desk = FLOOR.layout.solids[1];

  await drag(
    page,
    atMetres(scale.box, scale, desk.x + 0.5, desk.y + desk.h / 2),
    { x: Math.round(scale.box.x + scale.box.width) + 400, y: Math.round(scale.box.y + scale.box.height) + 400 },
  );
  await expect(page.getByTestId('fintech-admin-handle')).toBeVisible();

  const diff = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(diff, `the handle at the wall overflows by ${diff}px`).toBeLessThanOrEqual(1);
});

test('everything you can tap clears 44 px', async ({ page }) => {
  test.skip(!isMobile(page), 'a tap-target floor is a rule about thumbs');
  await openPlan(page);

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

  // AND EVERY CONTROL THE CONSTRUCTOR ADDED. There are now a dozen of them —
  // four steppers, a bin, six palette buttons, three actions — and they are
  // exactly the kind of small, dense strip that ends up at Vuetify's 36 px
  // default without anybody noticing.
  const controls = page.locator('main button, main a[href], main [role="button"]');
  expect(await controls.count(), 'the editor should have grown the control strip').toBeGreaterThan(10);
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

  // POLLED RATHER THAN MEASURED ONCE, because a dialog ARRIVES: Vuetify scales it
  // in over about a fifth of a second, and `toBeVisible()` is satisfied the
  // instant it starts. A single measurement therefore reads whatever fraction of
  // the final size the transition happened to be at — 43 px of an eventual 48,
  // which is a failure about timing wearing the costume of a failure about
  // layout. Bounded by the assertion's own deadline, and it still fails for good
  // if the button is genuinely too small.
  for (const id of ['fintech-admin-reroll-confirm', 'fintech-admin-reroll-cancel']) {
    await expect
      .poll(
        async () => {
          const box = await page.getByTestId(id).boundingBox();
          return box ? Math.round(Math.min(box.width, box.height)) : 0;
        },
        { message: `${id} never reached the 44 px floor` },
      )
      .toBeGreaterThanOrEqual(44);
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
