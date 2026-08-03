import { expect, test } from '@playwright/test';
import type { Page, WebSocketRoute } from '@playwright/test';
import { seedClient } from './fixtures';

/**
 * «СИМУЛЯТОР ФИНТЕХА» — the layout suite, at 360 px with `/api` stubbed in the
 * browser and the socket faked, so **the test is the server**.
 *
 * WHAT THIS SUITE IS FOR. The office is DOM rather than a canvas, so unlike
 * «ВАНЯДУМ» this file really can see the world — where a desk is, where the
 * bald man is standing, how pleased he is. What it deliberately does NOT assert
 * on is the shape of an INPUT frame: those are emitted from the render loop, a
 * browser pauses `requestAnimationFrame` outright for a backgrounded page, and
 * with parallel workers only one page is ever visible. That claim lives in
 * `fintechPredict.spec.ts`, where it is deterministic.
 *
 * THE STUB'S NUMBERS ARE DELIBERATELY NOT PRODUCTION'S. The splash's cheatsheet
 * is generated from the served catalogue, and these numbers are how that is
 * proved: a hand-typed rules line cannot pass the assertions below.
 */

/**
 * A balloon line LONGER THAN THE OLD ONE-ROW BOUND, which was 32 runes, and
 * inside the current two-row one, which is 48 (`content_test.go`,
 * `TestNobodySaysMoreThanFitsOnAPhone`). It exists so the wrap has something to
 * wrap: every other line in this stub fits on one row, so without it the whole
 * two-line change would be invisible to this suite and `.fintech-say` could go
 * back to `nowrap` with every assertion still green.
 *
 * It is the shape of the co-op redirect line the pools are growing towards,
 * carrying the stub's «СТЕНД» marker like everything else here so a client that
 * hardcoded a balloon cannot pass by accident.
 */
const LONG_SAY = 'ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО КОЛЛЕГИ, СТЕНД';

/**
 * The redirect announcement, as the catalogue publishes it in TWO places — the
 * verb's `say` and a line of `player_lines`. The client joins them by string to
 * learn which index means "somebody just pointed him at a colleague", so this
 * stub has to agree with itself the way the server does.
 */
const REDIRECT_SAY = 'ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО, СТЕНД';

/**
 * The router announcement, as the catalogue publishes it in TWO places — the
 * verb's `say` and a line of `player_lines`. Unlike the redirect the client does
 * NOT join them by string (the mark comes off the `ca` edge instead), but the
 * stub still has to agree with itself the way the server does, or the balloon
 * assertion below would be checking a line the catalogue never served.
 */
const ROUTER_SAY = 'РОУТЕР УПАЛ, СТЕНД';

const CONFIG = {
  // `karen` rather than `fintech`, mirroring production: a game_key VALUE is data
  // and did not move when the game was renamed (migrations/014). Nothing in the
  // client keys off it, so the stub could say anything — which is exactly why it
  // says the true thing, rather than teaching the next reader that it moved.
  game_key: 'karen',
  title: 'СИМУЛЯТОР ФИНТЕХА',
  office: {
    w: 12,
    h: 18,
    desks: [
      { x: 2, y: 3, w: 3, h: 1.2 },
      { x: 7, y: 3, w: 3, h: 1.2 },
      { x: 2, y: 9, w: 3, h: 1.2 },
    ],
    player_radius: 0.35,
    boss_radius: 0.4,
  },
  // None of these are the production values. See the note above.
  money: { base_per_second: 777, ramp_seconds: 9, max_multiplier: 4, grace_ms: 450 },
  move: {
    walk_speed: 4.4,
    dash_speed: 11.5,
    dash_ms: 330,
    dash_cooldown_ms: 5500,
    input_hz: 10,
    max_commands: 4,
  },
  boss: { speed: 2.9, catch_radius: 1.25, grin_range: 7 },
  sim: { hz: 20, snapshot_hz: 20, render_delay_ms: 75 },
  // Marked, so a client that hardcoded the endings instead of reading them
  // cannot pass the ending assertions either.
  endings: [
    { key: 'promoted', title: 'ТЕБЯ ПОВЫСИЛИ, СТЕНД', sub: 'теперь ты за это отвечаешь.' },
    { key: 'left', title: 'ТЫ ПРОСТО УШЁЛ, СТЕНД', sub: 'никто не заметил.' },
  ],
  // Marked like everything else here, so a client that hardcoded a balloon
  // instead of reading the catalogue cannot pass the assertions below.
  // Index 2 carries the name placeholder the client substitutes — appended rather
  // than replacing a line, so the assertions about index 1 keep meaning what they did.
  boss_lines: ['Я ЛЫСЫЙ, СТЕНД', 'А ГДЕ, СТЕНД?', 'ЧЕ ЗА ХЕРЬ, ГДЕ {} — СТЕНД'],
  player_lines: [
    'Я КАРЕН, СТЕНД',
    'ВОДЫ, СТЕНД',
    'НА ВСТРЕЧУ, СТЕНД',
    LONG_SAY,
    REDIRECT_SAY,
    ROUTER_SAY,
  ],
  // Marked like everything else here, so a client that hardcoded the cast instead
  // of reading it cannot pass the assertions below.
  personas: ['КАРЕН-СТЕНД', 'АНДРЮХА-СТЕНД', 'САНЯ-СТЕНД', 'ДАША-СТЕНД'],
  max_occupants: 3,
  // Marked like everything else in this stub, so a client that hardcoded the
  // label or the timers cannot pass the assertions below.
  bottle: {
    // THREE SPOTS, because the office stands up one prop per person and holds
    // three people — a stub with two could not describe a full floor, and the mask
    // assertions below would be testing a room that cannot happen.
    spots: [
      { x: 3, y: 9 },
      { x: 9, y: 4 },
      { x: 6, y: 16 },
    ],
    reach: 0.9,
    drunk_ms: 8500,
    return_ms: 9500,
    slow_pct: 45,
  },
  // Marked like everything else here.
  claude_lines: ['Я КЛОД, СТЕНД', 'УВИЖУ КОДЕКС — СТЕНД'],
  // Two non-players, marked like everything else here — the frame carries them in
  // THIS order and nothing else about them.
  npcs: [
    { key: 'serega', name: 'СЕРЕГА-СТЕНД', lines: ['Я СЕРЕГА, СТЕНД', 'ХУЙНЯ, СТЕНД'] },
    { key: 'tema', name: 'ТЁМА-СТЕНД', lines: ['Я ТЁМА, СТЕНД', 'Я В ПОЛЁТЕ, СТЕНД'] },
  ],
  claude: { speed: 2.9, reach: 0.6, slow_pct: 75, slow_ms: 4500 },
  // Marked like everything else in this stub.
  hookah: {
    spots: [
      { x: 6, y: 14 },
      { x: 6, y: 4 },
      { x: 2, y: 11 },
    ],
    reach: 0.9,
    invincible_ms: 11500,
    return_ms: 19500,
  },
  redirect: {
    label: 'ЭТО К НЕМУ, СТЕНД',
    say: REDIRECT_SAY,
    seconds_ms: 7000,
    cooldown_ms: 21000,
  },
  // Marked like every other number here: 25 s and 15 % are not production's 20 and
  // 10, so a HUD or a cheatsheet that hardcoded the ramp cannot pass below. At
  // 20 Hz that is 500 ticks a level, which is what the tempo assertions drive.
  tempo: { every_ms: 25000, step_pct: 15 },
  // Three days rather than production's seven, and for the usual reason: a
  // caption or a cheatsheet line that hardcoded «7 дней» cannot pass here. It also
  // exercises the other declension — «3 дня», not «дней».
  board: { window_days: 3 },
  // «РОУТЕР УПАЛ», marked like everything else in this stub — 9 s and 45 s are not
  // production's 12 and 30, and the label carries the СТЕНД marker so a client
  // that hardcoded the button's words cannot pass.
  router: {
    label: 'РОУТЕР УПАЛ, СТЕНД',
    say: ROUTER_SAY,
    seconds_ms: 9000,
    cooldown_ms: 45000,
  },
};

const SHIFT = { shift_id: 'shift-e2e', room: 'fintech', persona: 2 };

/**
 * A DELIBERATELY WIDE PNG — 4 × 1 — as a `data:` URI, used as the session's own
 * avatar and by the colleague redirector stub.
 *
 * WIDE RATHER THAN SQUARE, and that is the whole reason it exists. A replaced
 * element given an explicit `width` and no `height` derives its height from the
 * image's intrinsic ratio — so with a 1 × 1 fixture a badge with `height` deleted,
 * or with `aspect-ratio: 1` restored in its place, still measures exactly square
 * and the «it is a circle» assertion passes through the very regression it was
 * written to catch. A 4 : 1 source turns that into a visible strip. The yard
 * reasons the same way: «a VK avatar is not necessarily square, and `cover` is what
 * stops a wide one being drawn as a wide face».
 *
 * It also has to actually LOAD. The view latches an `@error` and removes the face
 * for the rest of the shift — correctly, since a broken glyph over the office is
 * worse than no face — so an unreachable URL like `example.invalid` makes the
 * element vanish and the assertion measure the wrong thing. `data:` is in the CSP's
 * `img-src` and needs no network at all.
 */
const WIDE_PNG = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAQAAAABCAIAAAB2XpiaAAAADUlEQVR4nGM4ERAARwAmfQWhqUwtbAAAAABJRU5ErkJggg==';

interface StubOptions {
  /** Answer `shifts/current` with a shift, as if the player had reloaded. */
  resume?: boolean;
  /** The session's own avatar, which is where your figure's face comes from. */
  avatar?: string;
  mine?: { cause: string; salary: number; seconds: number; created_at: string }[];
  /** The money board, and — where a test cares — the length board beside it. */
  top?: { name: string; salary: number; seconds: number; cause: string }[];
  topSeconds?: { name: string; salary: number; seconds: number; cause: string }[];
}

async function stubBackend(page: Page, opts: StubOptions = {}): Promise<void> {
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    const json = (status: number, body: unknown) =>
      route.fulfill({
        status,
        contentType: 'application/json; charset=utf-8',
        headers: { 'X-Trace-Id': 'e2e-trace-id' },
        body: JSON.stringify(body),
      });

    if (path === '/api/auth/me') {
      return json(200, {
        account: {
          id: 'a1',
          display_name: 'Тестер',
          role: 'user',
          status: 'approved',
          avatar_url: opts.avatar ?? '',
        },
      });
    }
    // A COLLEAGUE'S FACE, and it has to actually resolve. Left to the catch-all
    // this 404s, the `img` errors, and the client correctly gives up on it — so
    // any assertion about the badge would be racing that. A one-pixel PNG makes
    // it deterministic.
    if (path.startsWith('/api/game-fintech/avatar/')) {
      return route.fulfill({
        status: 200,
        contentType: 'image/png',
        // Deliberately 4 × 1 — see WIDE_PNG for why a square fixture makes the
        // badge's shape assertions unfalsifiable.
        body: Buffer.from(WIDE_PNG.split(',')[1], 'base64'),
      });
    }
    if (path === '/api/game-fintech/config') return json(200, CONFIG);
    if (path === '/api/game-fintech/shifts/me') return json(200, { shifts: opts.mine ?? [] });
    if (path === '/api/game-fintech/shifts/top') {
      // TWO BOARDS IN ONE RESPONSE, keyed by the metric each is scored on — the
      // splash draws them side by side and must not need a second request to
      // render one screen.
      return json(200, { salary: opts.top ?? [], seconds: opts.topSeconds ?? opts.top ?? [] });
    }
    if (path === '/api/game-fintech/shifts/current' && method === 'GET') {
      return opts.resume
        ? json(200, SHIFT)
        : json(404, { error: 'no_shift', trace_id: 'e2e-trace-id' });
    }
    if (path === '/api/game-fintech/shifts/current' && method === 'DELETE') {
      return route.fulfill({ status: 204, body: '' });
    }
    if (path === '/api/game-fintech/shifts' && method === 'POST') return json(201, SHIFT);
    return json(404, { error: 'not_found', trace_id: 'e2e-trace-id' });
  });
}

/**
 * Stands in for the office. Returns a handle the test drives, so every assertion
 * about the HUD and the plane is pushed rather than waited for.
 */
async function stubSocket(page: Page): Promise<{
  ready: (fields?: Record<string, unknown>) => Promise<void>;
  snapshot: (fields?: Record<string, unknown>) => Promise<void>;
  over: (fields?: Record<string, unknown>) => Promise<void>;
  sent: () => string[];
}> {
  const sent: string[] = [];
  let ws: WebSocketRoute | null = null;
  // THE TICK ADVANCES, and it has to. The interpolation buffer is keyed on the
  // office's tick rather than on when a frame turned up (fintechInterp), so a
  // stub that sent the same `k` twice would have its second frame dropped as a
  // duplicate and the figure would never move — which is a stub bug that reads
  // exactly like a broken renderer. A test that wants a specific tick still
  // passes one in `fields`.
  let tick = 12;

  await page.routeWebSocket('**/api/realtime*', (route) => {
    ws = route;
    route.onMessage((message) => {
      sent.push(typeof message === 'string' ? message : '');
    });
  });

  const send = async (payload: Record<string, unknown>) => {
    await expect.poll(() => ws !== null).toBe(true);
    ws?.send(JSON.stringify(payload));
  };

  return {
    ready: (fields = {}) => send({ t: 'fintech_ready', shift_id: SHIFT.shift_id, ...fields }),
    snapshot: (fields = {}) =>
      send({
        t: 'fintech_snap',
        k: (tick += 1),
        ack: 0,
        x: 600,
        y: 900,
        pay: 42800,
        m: 275,
        st: 4500,
        dc: 1800,
        b: { x: 300, y: 1500, g: 40 },
        // ONE OF EACH STANDING, on the catalogue's first spot. `bs` and `hs` are
        // MASKS — a bit per spot, because the office keeps one prop per person —
        // so the default frame has to say so: absent means «not one of them is on
        // the floor», which is a real state and not the resting one.
        bs: 1,
        hs: 1,
        // Claude, as a nested object — a test that wants him GONE passes
        // `cl: undefined`, which is exactly what the office does while the router
        // is down.
        cl: { x: 400, y: 1400, c: 30 },
        np: [
          { x: 200, y: 300 },
          { x: 1000, y: 300 },
        ],
        ...fields,
      }),
    over: (fields = {}) => send({ t: 'fintech_over', cause: 'promoted', pay: 42800, secs: 73, ...fields }),
    sent: () => sent,
  };
}

/** Phone width, where the primary pointer is a thumb rather than a mouse. */
function isMobile(page: Page): boolean {
  const vp = page.viewportSize();
  return !!vp && vp.width <= 600;
}

async function openSplash(page: Page, opts: StubOptions = {}): Promise<void> {
  await stubBackend(page, opts);
  await seedClient(page, 'dark');
  await page.goto('/app/game-fintech');
}

/** Starts a shift and waits for the office. Every play test opens this way. */
async function enterOffice(page: Page, opts: StubOptions = {}) {
  const socket = await stubSocket(page);
  await openSplash(page, opts);
  await expect(page.getByTestId('fintech-splash')).toBeVisible();
  await page.getByTestId('fintech-start').click();
  await expect(page.getByTestId('fintech-play')).toBeVisible();
  return socket;
}

/**
 * The shape the complaint arrived on, and the one that discriminates.
 *
 * This suite's own project is 360 × 800, where a portrait office is full width
 * whichever way it is laid out — so a full-bleed assertion made at 360 asserts
 * nothing. 412 × 746 is a common Android viewport and is tall enough to play on
 * but not tall enough to hide a control band, which is exactly the case that was
 * broken. Any test that claims something about how big the office is has to set
 * this first.
 */
const PHONE = { width: 412, height: 746 };

const overflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

test.describe('«СИМУЛЯТОР ФИНТЕХА» splash', () => {
  test('the rules cheatsheet is generated from the served catalogue', async ({ page }) => {
    // The point of the whole derived-cheatsheet arrangement: these numbers are
    // the STUB's, not production's, so a hand-written cheatsheet fails here.
    // `CLAUDE.md` makes stating a game's current rules on its splash a gate.
    await openSplash(page);
    const rules = page.getByTestId('fintech-rules');
    await expect(rules).toBeVisible();
    await expect(rules).toContainText('777 ₽');
    await expect(rules).toContainText('9 с');
    await expect(rules).toContainText('×4');
    await expect(rules).toContainText('0,45 с');
    await expect(rules).toContainText('4,4 м/с');
    await expect(rules).toContainText('11,5 м/с');
    // The marker this iteration added, explained where a player reads it before
    // starting rather than discovered mid-shift. Hardcoded prose, so it is asserted
    // by its words rather than by a served number.
    await expect(rules).toContainText('в белом круге');
    // And how to play at a desk, which is a rule a player needs before starting and
    // was invisible until the keyboard existed.
    await expect(rules).toContainText('WASD');
    await expect(rules).toContainText('пробел');
    await expect(rules).toContainText('5,5 с');
    await expect(rules).toContainText('2,9 м/с');
    await expect(rules).toContainText('1,25 м');
    // The ramp, from the stub's own numbers rather than production's 20 s / 10 %.
    await expect(rules).toContainText('25 с');
    await expect(rules).toContainText('15 %');
  });

  test('and it names both ways the shift can end, in the catalogue’s words', async ({ page }) => {
    await openSplash(page);
    const rules = page.getByTestId('fintech-rules');
    await expect(rules).toContainText('ТЕБЯ ПОВЫСИЛИ, СТЕНД');
    await expect(rules).toContainText('ТЫ ПРОСТО УШЁЛ, СТЕНД');
  });

  test('the controls are explained, because nothing else says how to move', async ({ page }) => {
    // The one block that is NOT derived: the server has no opinion about thumbs.
    await openSplash(page);
    const rules = page.getByTestId('fintech-rules');
    await expect(rules).toContainText('Стик слева');
    await expect(rules).toContainText('Стоишь — капает');
    await expect(rules).toContainText('Лысый подходит и здоровается');
  });

  test('and it says the cast is invented, which is the one line that is not a joke', async ({
    page,
  }) => {
    // The game is named after somebody and the лысый is recognisably somebody,
    // and it is played by the handful of people who would know both. Asserted on
    // the splash rather than trusted to a constant, and asserted BEFORE the
    // catalogue lands too — it sits outside the `v-else`, so a slow or failing
    // `/config` cannot be what takes it off the screen.
    await stubBackend(page);
    await seedClient(page, 'dark');
    await page.goto('/app/game-fintech');
    const note = page.getByTestId('fintech-disclaimer');
    await expect(note).toBeVisible();
    await expect(note).toHaveText('Все персонажи вымышлены, любые совпадения случайны.');
    await expect(page.getByTestId('fintech-rules')).toBeVisible();
    await expect(note).toBeVisible();
  });

  test('the cheatsheet is laid out as blocks, so a rule can be found', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('fintech-rule-block').first()).toBeVisible();
    expect(await page.getByTestId('fintech-rule-block').count()).toBeGreaterThan(2);
  });

  test('previous shifts are listed, so persistence is visible without a database', async ({
    page,
  }) => {
    await openSplash(page, {
      mine: [{ cause: 'promoted', salary: 51300, seconds: 91, created_at: '2026-07-29T10:00:00Z' }],
      top: [{ name: 'Карен', salary: 99900, seconds: 210, cause: 'left' }],
      topSeconds: [{ name: 'Даша', salary: 4200, seconds: 640, cause: 'promoted' }],
    });
    const mine = page.getByTestId('fintech-runs');
    await expect(mine).toBeVisible();
    await expect(mine).toContainText('51 300');
    // A length is a CLOCK everywhere it appears now — the strip counts it up this
    // way, the boards rank by it, and the ending repeats it.
    await expect(mine).toContainText('1:31');

    // TWO BOARDS, ONE PER SCORED DIMENSION, and each row carries both numbers so
    // the two read as one scoreboard rather than as two lists of strangers.
    const top = page.getByTestId('fintech-top');
    await expect(top).toBeVisible();
    const byMoney = page.getByTestId('fintech-top-salary');
    await expect(byMoney).toContainText('Карен');
    await expect(byMoney).toContainText('99 900');
    await expect(byMoney).toContainText('3:30');

    const byTime = page.getByTestId('fintech-top-seconds');
    await expect(byTime).toContainText('Даша');
    await expect(byTime).toContainText('10:40');
    await expect(byTime).toContainText('4 200');
    // The length board is a DIFFERENT board rather than the same one relabelled.
    await expect(byTime).not.toContainText('Карен');

    // AND THE BOARDS SAY HOW FAR BACK THEY LOOK, from the served window rather
    // than typed: a record that has aged off is otherwise indistinguishable from
    // one the game lost. The stub says three days, so «7» here would be a client
    // that hardcoded production's number.
    const window = page.getByTestId('fintech-board-window');
    await expect(window).toBeVisible();
    await expect(window).toContainText('за последние 3 дня');
    await expect(window).not.toContainText('7');
  });

  test('the splash is ordered button, boards, guide, your own shifts', async ({ page }) => {
    // THE ORDER SOMEBODY ACTUALLY USES THIS SCREEN IN. A returning player wants to
    // play, so nothing stands between them and the office; the boards are the
    // reason to play again; the guide is read once; your own shifts are the
    // natural bottom of the page. Asserted as document order rather than as pixel
    // positions, so it survives a layout change that keeps the meaning.
    await openSplash(page, {
      mine: [{ cause: 'left', salary: 100, seconds: 30, created_at: '2026-07-29T10:00:00Z' }],
      top: [{ name: 'Карен', salary: 99900, seconds: 210, cause: 'left' }],
    });
    await expect(page.getByTestId('fintech-runs')).toBeVisible();
    const order = await page.evaluate(() => {
      const ids = ['fintech-start', 'fintech-top', 'fintech-rules', 'fintech-runs'];
      const nodes = ids.map((id) => document.querySelector(`[data-testid="${id}"]`)!);
      return nodes.map((n, i) =>
        i === 0
          ? 0
          : // 4 is DOCUMENT_POSITION_FOLLOWING: the later element comes after.
            (nodes[i - 1].compareDocumentPosition(n) & 4) === 4
            ? 0
            : 1,
      );
    });
    expect(order, 'the splash is out of order').toEqual([0, 0, 0, 0]);
  });

  test('the lists are absent rather than empty when there is nothing in them', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    await expect(page.getByTestId('fintech-runs')).toHaveCount(0);
    await expect(page.getByTestId('fintech-top')).toHaveCount(0);
    // Including the window caption, which is a footnote TO the boards: with no
    // board on the screen it would be a sentence about nothing.
    await expect(page.getByTestId('fintech-board-window')).toHaveCount(0);
  });

  test('the start button is a real tap target', async ({ page }) => {
    await openSplash(page);
    const box = await page.getByTestId('fintech-start').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  });

  test('nothing overflows a 360 px phone', { tag: '@wide' }, async ({ page }) => {
    await openSplash(page, {
      mine: [{ cause: 'left', salary: 1234567, seconds: 12, created_at: '2026-07-29T10:00:00Z' }],
      top: [{ name: 'Человекснеприличнодлиннымименем', salary: 1234567, seconds: 9, cause: 'left' }],
      topSeconds: [
        { name: 'ЕщёОдинЧеловекСОченьДлиннымИменем', salary: 7, seconds: 98765, cause: 'promoted' },
      ],
    });
    await expect(page.getByTestId('fintech-runs')).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» play', () => {
  test('starting a shift replaces the splash with the office and a real HUD', async ({ page }) => {
    await enterOffice(page);
    await expect(page.getByTestId('fintech-plane')).toBeVisible();
    await expect(page.getByTestId('fintech-me')).toBeVisible();
    await expect(page.getByTestId('fintech-boss')).toBeVisible();
    // The furniture comes off the catalogue, one element each.
    await expect(page.getByTestId('fintech-desk')).toHaveCount(CONFIG.office.desks.length);
  });

  test('the HUD follows the snapshot, not the client', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ pay: 42800, m: 275, st: 4500, dc: 1800 });

    // Every one of these is the server's number, read off the wire and formatted
    // — the money is deliberately NOT predicted, because it is the score.
    await expect(page.getByTestId('fintech-hud-money')).toContainText('42 800 ₽');
    await expect(page.getByTestId('fintech-hud-mult')).toContainText('×2,75');
    await expect(page.getByTestId('fintech-hud-streak')).toBeVisible();
    await expect(page.getByTestId('fintech-play')).toContainText('РЫВОК 1,8 с.');
  });

  test('the clock counts from the tick the ready frame named, not from the office’s age', async ({
    page,
  }) => {
    // THE SECOND SCORED DIMENSION, and the whole of how it reaches the screen: the
    // ready frame carries `k0` once, every snapshot carries `k`, and the readout is
    // the difference. Nothing about elapsed time is on a repeating frame.
    //
    // The distinction this drives is the one that matters: the office is on tick
    // 1200 and the shift began on tick 1000, so the answer is 200 ticks — ten
    // seconds at the served 20 Hz — and NOT the whole minute the office has been
    // running. A client that read `k` alone would say 1:00.
    const socket = await enterOffice(page);
    await socket.ready({ persona: 2, k0: 1000 });
    await socket.snapshot({ k: 1200 });
    await expect(page.getByTestId('fintech-hud-alive')).toContainText('0:10');

    await socket.snapshot({ k: 2500 });
    await expect(page.getByTestId('fintech-hud-alive')).toContainText('1:15');
  });

  test('and reads 0:00 until the office says when the shift began', async ({ page }) => {
    // `k0` of zero is a real answer — the first person into a fresh office — so
    // «we have not been told yet» cannot be spelled zero. Until the ready frame
    // lands the clock shows nothing rather than the age of the whole office.
    const socket = await enterOffice(page);
    await socket.snapshot({ k: 4000 });
    await expect(page.getByTestId('fintech-hud-alive')).toContainText('0:00');
  });

  test('the tempo is derived from the office tick and the served ramp', async ({ page }) => {
    // The ramp is NOT on the wire. The client computes it from `k` against the
    // catalogue's `tempo`, which is why the stub's deliberately non-production
    // numbers are what these assertions are written against: 25 s a level at 20 Hz
    // is 500 ticks, and a step is 15 %.
    const socket = await enterOffice(page);
    const tempo = page.getByTestId('fintech-hud-tempo');

    await socket.snapshot({ k: 10 });
    await expect(tempo).toContainText('×1');
    await socket.snapshot({ k: 499 });
    await expect(tempo).toContainText('×1');

    await socket.snapshot({ k: 500 });
    await expect(tempo).toContainText('×1,15');
    // And it steps rather than gliding: a tick well inside the second level is
    // still exactly one step up.
    await socket.snapshot({ k: 900 });
    await expect(tempo).toContainText('×1,15');
    await socket.snapshot({ k: 1000 });
    await expect(tempo).toContainText('×1,3');
  });

  test('and the step is marked, because nobody can see two men walk 15 % faster', async ({
    page,
  }) => {
    // A level-up is an event with no visible cause: both men simply speed up by
    // less than the eye can read off a moving figure. The mark is one cell for
    // under a second — no shake, no flash of the plane, no sound — and it lands on
    // the EDGE rather than on the level, or it would flash for the whole level.
    // RECORDED RATHER THAN SAMPLED, and that is not fussiness — the mark lasts
    // well under a second by design, and a loaded runner can spend that long
    // between the frame that raises it and the assertion that looks. Sampling a
    // short-lived thing is the flake this repository has already paid for once
    // (`docs/RUNBOOK.md` → «A test that passes on its own and fails in CI»), so a
    // MutationObserver installed BEFORE the frame collects every value the
    // attribute ever took and the assertions read that log afterwards.
    const socket = await enterOffice(page);
    const tempo = page.getByTestId('fintech-hud-tempo');

    await socket.snapshot({ k: 100 });
    await page.evaluate(() => {
      const el = document.querySelector('[data-testid="fintech-hud-tempo"]')!;
      const seen: (string | null)[] = [el.getAttribute('data-bump')];
      (window as unknown as { __bumps: (string | null)[] }).__bumps = seen;
      new MutationObserver(() => seen.push(el.getAttribute('data-bump'))).observe(el, {
        attributes: true,
        attributeFilter: ['data-bump'],
      });
    });

    // Still inside the first level: nothing to mark.
    await socket.snapshot({ k: 300 });
    // And then over the line, which is the edge the mark is on.
    await socket.snapshot({ k: 500 });
    await expect(tempo).toContainText('×1,15');

    const bumps = () =>
      page.evaluate(() => (window as unknown as { __bumps: (string | null)[] }).__bumps);
    await expect.poll(bumps).toContain('1');
    // It was NOT raised by the frame that stayed inside the level — the mark is on
    // the step, not on the level, or it would flash for the whole of one.
    expect((await bumps())[0]).toBeNull();
    // And it goes away on its own, on a TIMER rather than on `animationend` — which
    // never fires when the animation is switched off under prefers-reduced-motion.
    await expect.poll(async () => (await bumps()).at(-1), { timeout: 5000 }).toBeNull();
  });

  test('and says the dash is ready when the snapshot omits the cooldown', async ({ page }) => {
    // `dc` is omitted rather than sent as zero, so an absent field has to mean
    // ready — read any other way the button would be dead forever.
    const socket = await enterOffice(page);
    await socket.snapshot({ dc: undefined });
    await expect(page.getByTestId('fintech-play')).toContainText('РЫВОК ГОТОВ');
    await expect(page.getByTestId('fintech-dash')).toBeEnabled();
  });

  test('the bald man is placed where the snapshot says, and drawn how pleased he is', async ({
    page,
  }) => {
    // Placed straight from the snapshot rather than from the render loop, which
    // is why this is assertable here at all: he is not predicted — his intent is
    // not ours to guess — so his position is written where it arrives.
    const socket = await enterOffice(page);
    await socket.snapshot({ b: { x: 300, y: 900, g: 200 } });

    const boss = page.getByTestId('fintech-boss');
    // 3 m across a 12 m office, 9 m down an 18 m one.
    await expect
      .poll(() => boss.evaluate((el) => getComputedStyle(el).getPropertyValue('--x').trim()))
      .toBe('0.25');
    await expect(boss).toHaveAttribute('data-grin', 'here');

    await socket.snapshot({ b: { x: 1100, y: 200, g: 10 } });
    await expect(boss).toHaveAttribute('data-grin', 'far');
  });

  test('and the man is what changes colour, never a box around him', async ({ page }) => {
    // A FIGURE'S BOX IS A COORDINATE, NOT A SURFACE. `.fintech-boss` is the
    // positioning element — a bare `--unit` × `--unit * 1.6` rectangle that the
    // head and body are painted on top of — so a `background` on it draws a
    // filled rectangle behind the man. It did, and the closer he got the more
    // visible it was: at `here` a solid orange box appeared around him, which
    // reads as a selection outline or a broken sprite rather than as somebody
    // arriving. The step is real and stays; it just belongs on `--skin` and
    // `--body`, which is what he is drawn with.
    const socket = await enterOffice(page);
    const boss = page.getByTestId('fintech-boss');
    const head = boss.locator('.fintech-fig-head');
    const painted = (l: typeof boss) =>
      l.evaluate((el) => {
        const s = getComputedStyle(el);
        return `${s.backgroundColor}|${s.backgroundImage}`;
      });

    const seen: string[] = [];
    for (const g of [10, 120, 250]) {
      await socket.snapshot({ b: { x: 600, y: 900, g } });
      // The state really did step, so this is not asserting an inert element.
      await expect(boss).toHaveAttribute(
        'data-grin',
        g === 10 ? 'far' : g === 120 ? 'closing' : 'here',
      );
      // Nothing is painted on the positioning box, in any state.
      expect(await painted(boss)).toBe('rgba(0, 0, 0, 0)|none');
      seen.push(await painted(head));
    }
    // And the man himself is what moved: three states, three different heads.
    expect(new Set(seen).size).toBe(3);
  });

  test('both figures always have something over their head, from the catalogue', async ({
    page,
  }) => {
    // SERVER-OWNED AND ALWAYS THERE. The frame carries an INDEX and the words
    // come from the catalogue the client fetched once (ADR-037), which is what
    // makes two people in one office read the same line — and what keeps a
    // Cyrillic sentence off a payload that repeats ten times a second forever.
    // These strings are the STUB's, so a hardcoded balloon fails here.
    const socket = await enterOffice(page);
    await socket.snapshot();
    // Nothing said means index 0, the default line for each — never an empty
    // balloon and never a stale one.
    await expect(page.getByTestId('fintech-me-say')).toHaveText('Я КАРЕН, СТЕНД');
    await expect(page.getByTestId('fintech-boss-say')).toHaveText('Я ЛЫСЫЙ, СТЕНД');
  });

  test('and what they say follows the snapshot, index by index', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ p: 2, b: { x: 300, y: 900, g: 200, p: 1 } });
    await expect(page.getByTestId('fintech-me-say')).toHaveText('НА ВСТРЕЧУ, СТЕНД');
    await expect(page.getByTestId('fintech-boss-say')).toHaveText('А ГДЕ, СТЕНД?');
    // AND BACK. Absent means index 0, never "unchanged" — read the other way a
    // figure sticks on the last interesting thing it said for the whole shift.
    await socket.snapshot({ p: undefined, b: { x: 300, y: 900, g: 200, p: undefined } });
    await expect(page.getByTestId('fintech-me-say')).toHaveText('Я КАРЕН, СТЕНД');
    await expect(page.getByTestId('fintech-boss-say')).toHaveText('Я ЛЫСЫЙ, СТЕНД');
  });

  test('a line past the old one-row bound wraps to two rows and keeps all of itself', async ({
    page,
  }) => {
    // THE CLAIM THE TWO-LINE BALLOON EXISTS FOR. `.fintech-say` was a single
    // `white-space: nowrap` row, and that row is what held the Go pools down to
    // 32 runes — under a short Russian sentence. Everything asserted here is a
    // number that the old CSS fails: 43 runes on one row would be ~255 px wide
    // and one line tall.
    //
    // 412 × 746 rather than this suite's 360 × 800, per the lesson two tests
    // below: assert at a shape that can discriminate. Both shapes happen to
    // work here, but the balloon's width bound is about a PHONE and the phone
    // that the complaints arrive on is this one.
    if (!isMobile(page)) test.skip();
    await page.setViewportSize(PHONE);
    const socket = await enterOffice(page);
    await socket.snapshot({ p: 3 });
    const say = page.getByTestId('fintech-me-say');
    await expect(say).toHaveText(LONG_SAY);
    expect(
      LONG_SAY.length,
      'the long stub line stopped being longer than the one-row bound it exists to exceed',
    ).toBeGreaterThan(32);

    const box = await say.evaluate((el) => {
      const cs = getComputedStyle(el);
      return {
        height: el.getBoundingClientRect().height,
        // TWO WIDTHS, and they are different numbers on purpose. `offsetWidth`
        // is what the balloon was LAID OUT at, so it is what `max-width` bounds;
        // the client rect is what it is DRAWN at, which the figure's depth scale
        // multiplies on the way up. Asserting the rect against 160 fails for a
        // reason that has nothing to do with this change.
        laid: (el as HTMLElement).offsetWidth,
        drawn: el.getBoundingClientRect().width,
        line: parseFloat(cs.lineHeight),
        font: parseFloat(cs.fontSize),
        // What the clamp WOULD have hidden. Equal means nothing was cut.
        scroll: el.scrollHeight,
        client: el.clientHeight,
      };
    });

    // MEASURE THE ROW AGAINST THE BALLOON NEXT TO IT rather than dividing a
    // height by a line height. The лысый is saying his 14-rune default, which is
    // one row, and both balloons are the same element with the same padding and
    // the same font — so the DIFFERENCE between the two heights is exactly the
    // extra row and nothing else. Dividing instead means guessing at padding, at
    // `-webkit-box`, and at whether the font's natural line box is really
    // `line-height`; a subtraction between two things styled identically has
    // none of those unknowns in it.
    const oneRow = (await page.getByTestId('fintech-boss-say').boundingBox())!.height;
    const extra = box.height - oneRow;
    // One extra row: not zero (the wrap never happened and `nowrap` is back) and
    // not two (the clamp is not holding and the balloon covers the office it is
    // standing in).
    expect(extra, 'the balloon did not wrap — it is still one row').toBeGreaterThan(box.line * 0.7);
    expect(extra, 'the balloon grew past two rows').toBeLessThan(box.line * 1.6);
    // NOT CLIPPED. `line-clamp` is the backstop for a pool that outgrew its
    // test, not the budget being spent — a line inside the Go bound must render
    // whole, and this is the assertion that would catch the bound and the CSS
    // drifting apart.
    expect(box.scroll, 'the clamp is eating a line that is inside the bound').toBeLessThanOrEqual(
      box.client + 1,
    );
    // Inside its stated max-width — `box-sizing` is border-box here, so 160 is
    // the whole box and the padding is already in it.
    expect(box.laid, 'the balloon is wider than the max-width it declares').toBeLessThanOrEqual(161);
    // And inside the phone once the depth scale has had it. The figure ramp tops
    // out at ×1.4, so the widest a full-width balloon can draw is 224 px — 62 %
    // of a 360 px screen and 54 % of this 412 px one. 70 % is the bound, which
    // catches a balloon that got wider or a depth ramp that got steeper without
    // failing on the arithmetic that is already true.
    expect(box.drawn).toBeLessThanOrEqual(PHONE.width * 0.7);
    // Smaller than the one-row size it replaced: 0.62rem was 9.92 px.
    expect(box.font, 'the balloon text did not get smaller').toBeLessThan(9.9);
  });

  test('the other people in the office are on the plane, each in his own shirt', async ({
    page,
  }) => {
    // THE WHOLE POINT OF CO-OP VISIBILITY, and the stub is what makes it
    // testable without a second browser: the office is multi-occupant on the
    // server from day one, so this suite can simply say there are two of them.
    const socket = await enterOffice(page);
    await expect(page.getByTestId('fintech-peer')).toHaveCount(0);

    await socket.snapshot({
      pr: [
        { i: 'AbCdEfGhIjKl', x: 300, y: 600, p: 2 },
        { i: 'MnOpQrStUvWx', x: 900, y: 1500 },
      ],
    });
    const peers = page.getByTestId('fintech-peer');
    await expect(peers).toHaveCount(2);

    // Placed where the frame said, in the same 0..1 plane coordinates every
    // other figure uses — centimetres over the office's metres.
    const at = async (handle: string) =>
      // Scoped to the FIGURE: `data-peer` is on his button too now, and an
      // unscoped selector matches both.
      page.locator(`[data-testid="fintech-peer"][data-peer="${handle}"]`).evaluate((el) => ({
        x: (el as HTMLElement).style.getPropertyValue('--x'),
        y: (el as HTMLElement).style.getPropertyValue('--y'),
        body: (el as HTMLElement).style.getPropertyValue('--body'),
      }));
    await expect
      .poll(async () => (await at('AbCdEfGhIjKl')).x, {
        message: 'the first colleague was never placed',
      })
      .toBe(String(3 / CONFIG.office.w));

    const one = await at('AbCdEfGhIjKl');
    const two = await at('MnOpQrStUvWx');
    expect(one.y).toBe(String(6 / CONFIG.office.h));
    expect(two.x).toBe(String(9 / CONFIG.office.w));
    // Different shirts, so two colleagues are told apart with no name on the
    // plane and nothing extra on the wire.
    expect(one.body).not.toBe('');
    expect(one.body).not.toBe(two.body);

    // AND HE IS ACTUALLY STYLED. Three CSS blocks for the peer — his colours,
    // his face and his dash aura — were written and silently did not land, so
    // avatars shipped as unstyled `img` elements at their natural size over the
    // office. Nothing noticed, because every test asked about POSITION. A rule
    // that exists only in a diff is a rule that does not exist.
    const badge = page.getByTestId('fintech-peer-avatar').first();
    await expect(badge).toHaveCount(1);
    // AS A FRACTION OF THE FIGURE, not "smaller than 60 px". The loose bound was
    // written when the only failure in view was an unstyled image at its natural
    // size; it stopped discriminating once the badge was sized off `--unit`, and it
    // would have gone on passing through a 25 % world shrink either way.
    const shape = await page.evaluate(() => {
      const img = document.querySelector('[data-testid="fintech-peer-avatar"]') as HTMLImageElement;
      const b = img.getBoundingClientRect();
      const fig = document
        .querySelector('[data-testid="fintech-peer"]')!
        .getBoundingClientRect();
      return { w: b.width, h: b.height, figW: fig.width, natural: img.naturalWidth > 0 };
    });
    // DECODED, not merely present: the box is two explicit `calc()` lengths and does
    // not depend on the bytes, so every shape claim below would hold for a broken
    // image too.
    expect(shape.natural, 'the badge never decoded, so its shape proves nothing').toBe(true);
    expect(shape.w / shape.figW, 'the badge is not 0.76 of the figure').toBeCloseTo(0.76, 2);
    // AND IT IS A CIRCLE. Two explicit lengths rather than a percentage pair on a
    // 1 : 1.6 box, which can never be one — and rather than `aspect-ratio` with no
    // height, which degrades to a strip on an engine that lacks it.
    expect(shape.h).toBeCloseTo(shape.w, 1);
    await expect(page.locator('[data-testid="fintech-peer"]').first()).toHaveCSS('opacity', '0.88');

    // The line over his head comes from the catalogue by INDEX, exactly as
    // yours and his do — these are the stub's words, so a hardcoded balloon
    // fails here.
    await expect(page.getByTestId('fintech-peer-say').first()).toHaveText('НА ВСТРЕЧУ, СТЕНД');

    // And somebody who leaves the office leaves the plane.
    await socket.snapshot({ pr: [{ i: 'MnOpQrStUvWx', x: 900, y: 1500 }] });
    await expect(peers).toHaveCount(1);
    await socket.snapshot({});
    await expect(peers).toHaveCount(0);
  });

  test('you are never drawn twice — the peer array is everybody else', async ({ page }) => {
    // The frame already says where YOU are at the top level and the client
    // PREDICTS that, so a self-entry would draw a second, laggier copy of you
    // standing on yourself. The server omits it; this is the client half.
    const socket = await enterOffice(page);
    await socket.snapshot({ pr: [] });
    await expect(page.getByTestId('fintech-peer')).toHaveCount(0);
    await expect(page.getByTestId('fintech-me')).toHaveCount(1);
  });

  test('there is a redirect control per colleague, and none at all when you are alone', async ({
    page,
  }) => {
    // SOLO IS A FIRST-CLASS CASE, NOT A DEGRADED ONE. The catalogue publishes
    // the verb whatever the office holds, and the client hides the control when
    // there is nobody to point him at — so the server needs no second code path
    // and the HUD carries no greyed-out button explaining that you have no
    // friends.
    const socket = await enterOffice(page);
    await socket.snapshot();
    await expect(page.getByTestId('fintech-redirect')).toHaveCount(0);

    await socket.snapshot({
      pr: [
        { i: 'AbCdEfGhIjKl', x: 300, y: 600 },
        { i: 'MnOpQrStUvWx', x: 900, y: 1500 },
      ],
    });
    const buttons = page.getByTestId('fintech-redirect');
    await expect(buttons).toHaveCount(2);
    // The LABEL comes from the catalogue, so a hardcoded one fails here.
    await expect(buttons.first()).toContainText('ЭТО К НЕМУ, СТЕНД');
    // And every control on this plane is a real tap target.
    for (const box of await buttons.all()) {
      const b = (await box.boundingBox())!;
      expect(b.height).toBeGreaterThanOrEqual(44);
    }

    // Pressing one sends the verb over the SOCKET, naming the colleague by the
    // pseudonym his frame carried — never an account, which this client has
    // never been told.
    await buttons.first().dispatchEvent('pointerdown');
    await expect
      .poll(() => socket.sent().some((m) => m.includes('"fintech_do"')))
      .toBe(true);
    const sent = socket.sent().find((m) => m.includes('"fintech_do"'))!;
    expect(JSON.parse(sent)).toMatchObject({ t: 'fintech_do', v: 'redirect', tg: 'AbCdEfGhIjKl' });

    // A colleague who leaves takes his button with him.
    await socket.snapshot({ pr: [{ i: 'MnOpQrStUvWx', x: 900, y: 1500 }] });
    await expect(buttons).toHaveCount(1);
  });

  test('the redirect button is dead while the office says it is cooling down', async ({ page }) => {
    // The office judges the verb and the frame carries the cooldown, so the
    // button follows the SERVER rather than running its own timer — a client
    // that decided for itself would offer a verb the office is about to refuse.
    const socket = await enterOffice(page);
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }], rc: 12000 });
    const button = page.getByTestId('fintech-redirect').first();
    await expect(button).toBeDisabled();

    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }] });
    await expect(button).toBeEnabled();
  });

  test('the bottle is on the floor until somebody drinks it, and then he turns green', async ({
    page,
  }) => {
    // «Набухать лысого». `bt` is omitted while a bottle is standing there, which
    // is the common case — so an ABSENT field means present, and the plane draws
    // it on the strength of the catalogue's own coordinates.
    const socket = await enterOffice(page);
    await socket.snapshot();
    const bottle = page.getByTestId('fintech-bottle');
    await expect(bottle).toHaveCount(1);
    await expect(bottle).toHaveCSS('--x', String(CONFIG.bottle.spots[0].x / CONFIG.office.w));

    // It MOVES: the frame names WHICH spot by index, and the plane looks the
    // coordinates up in the catalogue it already fetched. A position on a frame
    // that repeats ten times a second would be twenty bytes forever to say
    // something that changes once every ten seconds.
    await socket.snapshot({ bs: 1 << 1 });
    await expect(bottle).toHaveCSS('--x', String(CONFIG.bottle.spots[1].x / CONFIG.office.w));

    // Somebody drank it: it is gone, and he is drunk.
    await socket.snapshot({ bs: 0, b: { x: 300, y: 1500, g: 40, d: 8500 } });
    await expect(bottle).toHaveCount(0);
    await expect(page.getByTestId('fintech-boss')).toHaveAttribute('data-drunk', '1');

    // GREEN IS A STATE OF THE FIGURE, not a box around it — the positioning
    // element must stay transparent, which is the defect §17.5 caught once.
    const boxed = await page
      .getByTestId('fintech-boss')
      .evaluate((el) => getComputedStyle(el).backgroundImage + '|' + getComputedStyle(el).backgroundColor);
    expect(boxed).toMatch(/none\|rgba\(0, 0, 0, 0\)/);

    // And he sobers up.
    await socket.snapshot({ bs: 0, b: { x: 300, y: 1500, g: 40 } });
    await expect(page.getByTestId('fintech-boss')).not.toHaveAttribute('data-drunk', '1');
  });

  test('a verb that is not movement says so on the plane', async ({ page }) => {
    // THE RULE (CLAUDE.md → «A verb announces itself on the plane»): anything
    // that is not ordinary movement gets a brief mark where it happened.
    // Standing, walking and dashing do not need one — you can see those. The
    // bottle and the redirect are otherwise invisible: the office simply behaves
    // differently a moment later, which reads as the game misbehaving.
    //
    // It costs NOTHING on the wire: every mark is an existing field crossing
    // from zero to non-zero.
    const socket = await enterOffice(page);
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }] });
    await expect(page.getByTestId('fintech-pop')).toHaveCount(0);

    // Somebody drank it — its bit leaves the mask, which is both the edge and the
    // place: the mark lands on the spot that lost its bottle.
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }], bs: 0 });
    await expect(page.getByTestId('fintech-pop')).toHaveAttribute('data-kind', 'bottle');

    // He turned green — `d` starts running. A different kind, so two things in
    // the same second are still two things.
    await socket.snapshot({
      pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }],
      bs: 0,
      b: { x: 300, y: 1500, g: 40, d: 8500 },
    });
    await expect(page.getByTestId('fintech-pop')).toHaveAttribute('data-kind', 'drunk');

    // Your own verb landing — YOUR balloon becomes the announcement.
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }], p: 4 });
    await expect(page.getByTestId('fintech-pop')).toHaveAttribute('data-kind', 'redirect');

    // A LEVEL IS NOT AN EVENT. A cooldown still running is not a verb being
    // used again, so a frame that merely repeats it marks nothing — without
    // this the plane would flash ten times a second for eight seconds.
    await expect
      .poll(async () => page.getByTestId('fintech-pop').count(), { message: 'the mark never cleared' })
      .toBe(0);
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }], p: 4 });
    await expect(page.getByTestId('fintech-pop')).toHaveCount(0);
  });

  test('and a colleague’s verb is marked too, where HE is standing', async ({ page }) => {
    // EVERY SCREEN SEES EVERY ACTION. It used to be marked off your own cooldown
    // starting, so only the person who pressed it saw anything — a colleague
    // pointing the bald man at YOU was silent on your screen, which is the one
    // time it matters most.
    //
    // Derived locally from what the frame already carries: his balloon index,
    // joined against the catalogue this client fetched once. No extra byte.
    const socket = await enterOffice(page);
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }] });
    await expect(page.getByTestId('fintech-pop')).toHaveCount(0);

    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600, p: 4 }] });
    const pop = page.getByTestId('fintech-pop');
    await expect(pop).toHaveAttribute('data-kind', 'redirect');
    // AT HIM, not at you: 3 m across a 12 m office.
    await expect(pop).toHaveCSS('--x', String(3 / CONFIG.office.w));
  });

  test('a balloon rides the man rather than being placed beside him', async ({ page }) => {
    // It is a CHILD of the figure, so the transform that moves him every
    // animation frame carries it too — no second write, and no chance of the
    // words being a frame behind the man saying them.
    const socket = await enterOffice(page);
    await socket.snapshot({ b: { x: 300, y: 900, g: 40 } });
    const inside = await page
      .getByTestId('fintech-boss-say')
      .evaluate((el) => !!el.closest('[data-testid="fintech-boss"]'));
    expect(inside).toBe(true);
    const boss = (await page.getByTestId('fintech-boss').boundingBox())!;
    const say = (await page.getByTestId('fintech-boss-say').boundingBox())!;
    // Above his head, not over his face.
    expect(say.y + say.height).toBeLessThanOrEqual(boss.y + 1);
  });

  test('the client says hello the moment the socket opens', async ({ page }) => {
    // It goes out on every OPEN, including reconnects, because the office
    // outlives a dropped socket and a returning client has to be re-attached to
    // the shift it is already in. `send` drops rather than queues, so a hello
    // written before the handshake finished would simply vanish.
    const socket = await enterOffice(page);
    await expect
      .poll(() => socket.sent().filter((m) => m.includes('fintech_hello')).length)
      .toBeGreaterThan(0);
    // And it carries nothing: identity is the connection, so there is nothing in
    // a hello to forge and nothing to validate.
    const hello = JSON.parse(socket.sent().find((m) => m.includes('fintech_hello'))!);
    expect(Object.keys(hello)).toEqual(['t']);
  });

  test('standing still sends nothing at all', async ({ page }) => {
    // The rule this whole game is built on. Standing perfectly still is the
    // point, and it must cost the network nothing — the salary climbs because
    // the SERVER advances the shift, never because the client keeps talking.
    const socket = await enterOffice(page);
    await socket.snapshot();
    await page.waitForTimeout(700);
    expect(socket.sent().filter((m) => m.includes('fintech_input'))).toHaveLength(0);
  });

  test('the link is reported until the office answers', async ({ page }) => {
    const socket = await enterOffice(page);
    await expect(page.getByTestId('fintech-link')).toBeVisible();
    await socket.ready();
    await expect(page.getByTestId('fintech-link')).toHaveCount(0);
  });

  test('every control clears 44 px and sits inside the screen', async ({ page }) => {
    if (!isMobile(page)) test.skip();
    await enterOffice(page);
    const viewport = page.viewportSize()!;
    for (const id of ['fintech-stick', 'fintech-dash', 'fintech-quit']) {
      const box = await page.getByTestId(id).boundingBox();
      expect(box, id).not.toBeNull();
      expect(box!.width, id).toBeGreaterThanOrEqual(44);
      expect(box!.height, id).toBeGreaterThanOrEqual(44);
      expect(box!.x, id).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width, id).toBeLessThanOrEqual(viewport.width);
      // THE OTHER AXIS, which did not exist while the controls were a band in a
      // flex column — a column cannot put its own children off the screen. They
      // are absolutely positioned over the office now, so `bottom: -20px` is one
      // typo away and nothing else in this file would notice.
      expect(box!.y, id).toBeGreaterThanOrEqual(0);
      expect(box!.y + box!.height, id).toBeLessThanOrEqual(viewport.height);
    }
  });

  test('the stick stands clear of the left edge, further in than the dash is from the right', async ({
    page,
  }) => {
    // The stick used to start 16 px from the glass, which puts the half of it a
    // thumb reaches FIRST inside the phone's own edge-swipe strip — a drag begun
    // there argues with a system gesture instead of steering, and on a curved
    // screen it is partly on the bezel.
    //
    // The assertion is deliberately RELATIVE as well as absolute. An absolute
    // floor alone is passed by padding both sides equally, which would push the
    // dash inward for no reason; the claim is that the two edges are treated
    // DIFFERENTLY, because a drag and a tap do not want the same thing from an
    // edge. So: the stick clears a real gesture strip, and it clears more of one
    // than the dash does.
    if (!isMobile(page)) test.skip();
    await enterOffice(page);
    const viewport = page.viewportSize()!;
    const stick = (await page.getByTestId('fintech-stick').boundingBox())!;
    const dash = (await page.getByTestId('fintech-dash').boundingBox())!;
    const fromLeft = stick.x;
    const fromRight = viewport.width - (dash.x + dash.width);
    expect(fromLeft, 'the stick is back in the edge-swipe strip').toBeGreaterThanOrEqual(24);
    expect(fromLeft, 'the stick is inset far enough to be worth moving').toBeGreaterThan(fromRight);
    // And it did not buy that room by getting smaller — the size a thumb wants
    // is the reason the padding moved instead of the circle.
    expect(stick.width).toBeGreaterThanOrEqual(96);
  });

  test('the office is the whole screen, edge to edge', async ({ page }) => {
    // THE POINT OF THE OVERLAY, and it needs A REAL PHONE'S SHAPE rather than
    // this project's to be a claim at all.
    //
    // This suite runs at 360 × 800, and at that shape the office was ALREADY
    // full width before the controls became overlays — 800 is tall enough that
    // the plane's WIDTH bound wins either way, so nothing asserted at 360 could
    // tell the two layouts apart. 412 × 746 is the shape the complaint arrived
    // on: 746 − 72 of shell − 56 of readouts − 132 of control band left 486 px
    // of stage, and a portrait room bounded by 486 is only 324 px wide — 79 % of
    // the phone, with dead space down both sides and above and below. Give the
    // stage the whole box and the width bound wins instead.
    //
    // NOT @wide, deliberately: at 1440 × 900 the plane is bounded by HEIGHT and
    // is correctly NOT full width, so this claim is about a phone and skips
    // itself above 600 px — exactly the condition `playwright.config.ts` says
    // earns no tag.
    if (!isMobile(page)) test.skip();
    await page.setViewportSize(PHONE);
    await enterOffice(page);
    const plane = (await page.getByTestId('fintech-plane').boundingBox())!;
    expect(plane.x).toBeLessThanOrEqual(1);
    expect(plane.width).toBeGreaterThanOrEqual(PHONE.width - 1);
  });

  test('and both thumbs rest ON it, not in a band beneath it', async ({ page }) => {
    // The inverse of "no control stands where another takes the tap": the
    // controls SHOULD overlap the office, and without this the whole change is
    // invisible to the suite — the plane could quietly go back to being one
    // child of a column and every other test here would still pass.
    if (!isMobile(page)) test.skip();
    await enterOffice(page);
    const plane = (await page.getByTestId('fintech-plane').boundingBox())!;
    for (const id of ['fintech-stick', 'fintech-dash']) {
      const box = (await page.getByTestId(id).boundingBox())!;
      const overlaps =
        box.x < plane.x + plane.width &&
        box.x + box.width > plane.x &&
        box.y < plane.y + plane.height &&
        box.y + box.height > plane.y;
      expect(overlaps, `${id} is not over the office`).toBe(true);
    }
  });

  test('a thumb on an overlaid control reaches the control, not the office', async ({ page }) => {
    // `pointer-events` is the whole of this, and it is the one thing an overlay
    // gets wrong in both directions. A real hit test rather than a dispatched
    // event: `tap()` fails outright if something else is on top, which is the
    // failure this is here to catch.
    if (!isMobile(page)) test.skip();
    await enterOffice(page);
    await page.getByTestId('fintech-dash').tap();
    // And the gap BETWEEN the two thumbs is still office — the wrapper that
    // lays them out must not be swallowing the middle of the screen.
    const viewport = page.viewportSize()!;
    const stick = (await page.getByTestId('fintech-stick').boundingBox())!;
    // Whatever is under that point, it must not be the controls' own wrapper.
    // The office, a desk, or the stage behind the office are all fine answers —
    // the claim is only that a box which exists to lay two thumbs out does not
    // also eat the whole width of the screen between them.
    const under = await page.evaluate(
      ([x, y]) => {
        const el = document.elementFromPoint(x, y);
        return {
          inControls: !!el?.closest('.fintech-controls'),
          what: el?.getAttribute('data-testid') ?? el?.className ?? '(nothing)',
        };
      },
      [viewport.width / 2, stick.y + stick.height / 2],
    );
    expect(under.inControls, `the gap between the thumbs was eaten by ${under.what}`).toBe(false);
  });

  test('and no control stands where another one takes the tap', async ({ page }) => {
    // The yard shipped that bug once. The stick is bottom-left, the dash is
    // bottom-right, and the way out is at the top where neither thumb lives.
    await enterOffice(page);
    const boxes = await Promise.all(
      ['fintech-stick', 'fintech-dash', 'fintech-quit'].map((id) =>
        page.getByTestId(id).boundingBox(),
      ),
    );
    for (let i = 0; i < boxes.length; i++) {
      for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i]!;
        const b = boxes[j]!;
        const overlaps =
          a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
        expect(overlaps, `controls ${i} and ${j} overlap`).toBe(false);
      }
    }
  });

  test('the stick swallows gestures, so walking cannot scroll the page', async ({ page }) => {
    // `touch-action: none` is the difference between walking and pull-to-refresh
    // reloading the game mid-shift.
    await enterOffice(page);
    const touchAction = await page
      .getByTestId('fintech-stick')
      .evaluate((el) => getComputedStyle(el).touchAction);
    expect(touchAction).toBe('none');
  });

  test('the stick answers a thumb', async ({ page }) => {
    await enterOffice(page);
    const stick = page.getByTestId('fintech-stick');
    const box = (await stick.boundingBox())!;
    await stick.dispatchEvent('pointerdown', {
      pointerId: 1,
      pointerType: 'touch',
      clientX: box.x + box.width,
      clientY: box.y + box.height / 2,
      isPrimary: true,
    });
    // The knob follows the thumb, which is the only feedback that the control is
    // live before anything on the plane moves.
    const knob = page.locator('.fintech-stick-knob');
    await expect
      .poll(() => knob.evaluate((el) => getComputedStyle(el).transform))
      .not.toBe('matrix(1, 0, 0, 1, 0, 0)');
  });

  test('the office never scrolls the shell', { tag: '@wide' }, async ({ page }) => {
    // A page that scrolls under a thumb resting on a stick is a page that moves
    // the stick. The play phase is a fixed-height layout, not a document.
    const socket = await enterOffice(page);
    await socket.snapshot();
    const scroll = await page.evaluate(() => ({
      h: document.documentElement.scrollHeight - document.documentElement.clientHeight,
      w: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    }));
    expect(scroll.h).toBeLessThanOrEqual(1);
    expect(scroll.w).toBeLessThanOrEqual(0);
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» ending', () => {
  test('being caught shows the catalogue’s ending, not one this client invented', async ({
    page,
  }) => {
    const socket = await enterOffice(page);
    await socket.snapshot();
    await socket.over({ cause: 'promoted', pay: 42800, secs: 73 });

    const over = page.getByTestId('fintech-over');
    await expect(over).toBeVisible();
    await expect(page.getByTestId('fintech-over-title')).toHaveText('ТЕБЯ ПОВЫСИЛИ, СТЕНД');
    await expect(over).toContainText('теперь ты за это отвечаешь.');
    await expect(page.getByTestId('fintech-over-salary')).toContainText('42 800 ₽');
    // The SAME clock the strip counted up and both boards rank by, rather than the
    // raw «73 с» this screen used to print: the number a player just watched is
    // the number they are scored on.
    await expect(page.getByTestId('fintech-over-secs')).toContainText('1:13');
    await expect(page.getByTestId('fintech-retry')).toBeVisible();
  });

  test('walking out is an ending too, and the catalogue names that one as well', async ({
    page,
  }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ pay: 3100 });
    await page.getByTestId('fintech-quit').click();

    await expect(page.getByTestId('fintech-over-title')).toHaveText('ТЫ ПРОСТО УШЁЛ, СТЕНД');
    await expect(page.getByTestId('fintech-over-salary')).toContainText('3 100 ₽');
  });

  test('НАЗАД goes back to the splash', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.over();
    await expect(page.getByTestId('fintech-over')).toBeVisible();
    await page.getByRole('button', { name: 'НАЗАД' }).click();
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
  });

  test('and the ending screen fits a phone', { tag: '@wide' }, async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.over({ cause: 'promoted', pay: 1234567, secs: 4000 });
    await expect(page.getByTestId('fintech-over')).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» resuming', () => {
  test('a shift already in progress is picked up rather than restarted', async ({ page }) => {
    // The office outlives a disconnect by design, so a reload must pick the
    // shift back up instead of stranding the player behind a button that
    // answers 409.
    await stubSocket(page);
    await openSplash(page, { resume: true });
    await expect(page.getByTestId('fintech-play')).toBeVisible();
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» in the nav', () => {
  test('is offered in the drawer, and is never the front door', { tag: '@wide' }, async ({
    page,
  }) => {
    // Tagged because the drawer is PERMANENT above 960px and temporary below,
    // and only the wide project can see the permanent branch. The claim itself
    // holds at both widths: the first entry stays «Ванягоччи», because that is
    // HOME_ROUTE_NAME and e2e/appHome.spec.ts asserts the two agree.
    await openSplash(page);
    const items = page.locator('.v-navigation-drawer .v-list-item-title');
    await expect(items.first()).toHaveText('Ванягоччи');
    const entry = page.locator('.v-navigation-drawer').getByRole('link', {
      name: 'СИМУЛЯТОР ФИНТЕХА',
    });
    await expect(entry).toHaveCount(1);
    // Visibility is only a claim where the drawer is permanently on screen;
    // below 960px it is slid off by default and asserting it would be a race
    // against the shell's one-off peek rather than a claim about the nav.
    if ((page.viewportSize()?.width ?? 0) >= 960) await expect(entry).toBeVisible();
  });
});




test.describe('«СИМУЛЯТОР ФИНТЕХА» — the top of the room', () => {
  // THE BUG THIS FIXES WAS THE FIRST THING A SHIFT SHOWED. A figure is
  // feet-anchored, the simulation clamps only to PlayerRadius (0.35 m of a 22 m
  // room), and the plane clipped everything above its top edge — so a man at the
  // top wall was a sliver of body with no head and no words. The spawn sampler
  // draws the first point far enough from the лысый, and he starts at the bottom,
  // so most shifts OPENED in exactly that band.
  //
  // Every assertion here drives the BOSS rather than your own figure, on purpose:
  // he is written straight from the snapshot, while `fintech-me` is predicted from
  // a render loop that a browser pauses outright for a backgrounded tab — and with
  // several workers only one page is ever visible.

  test('a figure against the top wall is drawn whole, head and all', { tag: '@wide' }, async ({
    page,
  }) => {
    const socket = await enterOffice(page);
    // 0.4 m from the top wall — nearer than a player can actually stand.
    await socket.snapshot({ b: { x: 600, y: 40, g: 10 } });
    // WAIT FOR HIM TO GET THERE BEFORE MEASURING. He is interpolated, so his
    // position arrives a frame or two after the snapshot carrying it — and until
    // it does he is still standing where he was first placed, which is in the
    // middle of the room where he has always fitted. Measured without this poll
    // the test passes for the wrong reason and fails at random; bounded by a
    // deadline rather than an attempt count, per docs/RUNBOOK.md.
    await expect
      .poll(() =>
        page
          .getByTestId('fintech-boss')
          .evaluate((el) => getComputedStyle(el).getPropertyValue('--y').trim()),
      )
      .toBe(String(0.4 / CONFIG.office.h));

    // ONE evaluate for the geometry, not four round trips: several of these boxes
    // are written by a render loop, and reading them one at a time lets them move
    // between the reads.
    const seen = await page.evaluate(() => {
      const boss = document.querySelector('[data-testid="fintech-boss"]')!;
      const head = boss.querySelector('.fintech-fig-head')!.getBoundingClientRect();
      const plane = document.querySelector('[data-testid="fintech-plane"]')!.getBoundingClientRect();
      const office = document.querySelector('[data-testid="fintech-office"]')!.getBoundingClientRect();
      return {
        head: { top: head.top, height: head.height },
        planeTop: plane.top,
        officeTop: office.top,
      };
    });

    // His head is inside the clipping box — which is the first claim.
    expect(seen.head.height).toBeGreaterThan(0);
    expect(seen.head.top).toBeGreaterThanOrEqual(seen.planeTop - 1);
    // And it is genuinely ABOVE the room, standing on the wall: if this ever
    // passed with the head below the office's top edge, the test would be
    // asserting nothing, because the room is where he always fitted.
    expect(seen.head.top).toBeLessThan(seen.officeTop);
  });

  test('his words go under his feet when there is no room over his head', async ({ page }) => {
    // The wall is one figure deep, which makes the MAN visible; covering his
    // WORDS too would need it half again as deep, and that is floor space taken
    // from the room for two lines of text. So the balloon moves instead.
    const socket = await enterOffice(page);

    const boss = page.getByTestId('fintech-boss');
    const flag = () => boss.evaluate((el) => getComputedStyle(el).getPropertyValue('--say-below').trim());
    // Polled on a DEADLINE, never an attempt count: he is interpolated, so his
    // first position arrives a frame or two after the snapshot that carries it.
    // The flag is durable state once written, so reading it and then measuring is
    // not the two-round-trip race the RUNBOOK warns about.
    const geometry = () =>
      boss.evaluate((el) => {
        const say = el.querySelector('[data-testid="fintech-boss-say"]')!.getBoundingClientRect();
        return { sayTop: say.top, figTop: el.getBoundingClientRect().top };
      });

    await socket.snapshot({ b: { x: 600, y: 40, g: 10 } });
    await expect.poll(flag).toBe('1');
    const atWall = await geometry();
    // Below his feet, so below the top of his own box.
    expect(atWall.sayTop).toBeGreaterThan(atWall.figTop);

    // And in the middle of the room it is over his head, as it always was — which
    // is what makes the assertion above discriminate.
    await socket.snapshot({ b: { x: 600, y: 900, g: 10 } });
    await expect.poll(flag).toBe('0');
    const midRoom = await geometry();
    expect(midRoom.sayTop).toBeLessThan(midRoom.figTop);
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — the scale of the world', () => {
  test('a figure is the same fraction of the room on a phone and on a desktop', { tag: '@wide' }, async ({
    page,
  }) => {
    // THE OWNER'S REQUIREMENT, and the reason the basis is `cqw` rather than `vw`:
    // «i want other mobile phones to have same scale as i have». A figure is a
    // fixed fraction of the ROOM, so two players see the same distances whatever
    // they are holding — which a viewport-relative unit would break, drawing a man
    // three and a half office metres wide in a desktop window.
    //
    // This is exactly the cross-width claim the desktop project exists for: it
    // cannot fail at one width alone, so it earns the @wide tag.
    const socket = await enterOffice(page);
    await socket.snapshot({ b: { x: 600, y: 900, g: 10 } });
    await expect
      .poll(() =>
        page
          .getByTestId('fintech-boss')
          .evaluate((el) => getComputedStyle(el).getPropertyValue('--y').trim()),
      )
      .toBe(String(9 / CONFIG.office.h));

    const ratio = await page.evaluate(() => {
      const boss = document.querySelector('[data-testid="fintech-boss"]')!.getBoundingClientRect();
      const office = document.querySelector('[data-testid="fintech-office"]')!.getBoundingClientRect();
      return boss.height / office.width;
    });
    // 1.6 × --unit × the depth scale at this y, and --unit is 8.25 % of the room's
    // width: 1.6 × 0.0825 × 1.16 = 0.1531. Asserted to three places rather than as
    // a range, so it discriminates — before the world was cut by a quarter this
    // was 0.204, and a `vw` basis would give two different answers here.
    expect(ratio).toBeCloseTo(0.1531, 3);
  });

  test('the words over the office did not shrink with it', async ({ page }) => {
    // PINNING A DELIBERATE EXCLUSION. Everything in the room is a fraction of
    // `--unit` and got a quarter smaller; the balloon did not, and must not. Its
    // font is 0.54rem — 8.64 px — and three quarters of that is 6.5 px, which is
    // not text on a phone held at arm's length. `content_test.go`'s 48-rune bound
    // is a MEASUREMENT of this font against this max-width, so shrinking either
    // would make a Go test assert something false and start clamping lines that
    // are inside the bound.
    const socket = await enterOffice(page);
    await socket.snapshot();
    const say = page.getByTestId('fintech-boss-say');
    await expect(say).toHaveCSS('font-size', '8.64px');
    const laid = await say.evaluate((el) => (el as HTMLElement).offsetWidth);
    expect(laid).toBeLessThanOrEqual(161);
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — whose figure is whose', () => {
  test('you wear your own face, taken from the session rather than the wire', async ({ page }) => {
    // ZERO NEW BYTES AND ZERO NEW REQUESTS. The server never sends your own
    // handle, so the peer redirector is not even reachable for you — and it does
    // not need to be: `avatar_url` is in the auth store before this view can
    // mount. The stub's `/api/auth/me` carries one, so a client reading the wire
    // instead of the store cannot pass this.
    const socket = await enterOffice(page, { avatar: WIDE_PNG });
    await socket.snapshot();
    const face = page.getByTestId('fintech-me-avatar');
    await expect(face).toHaveCount(1);
    await expect(face).toHaveAttribute('src', WIDE_PNG);
    // Same rule as a colleague's, so it is the same size and the same circle.
    const shape = await page.evaluate(() => {
      const img = document.querySelector('[data-testid="fintech-me-avatar"]') as HTMLImageElement;
      const b = img.getBoundingClientRect();
      const fig = document.querySelector('[data-testid="fintech-me"]')!.getBoundingClientRect();
      return { w: b.width, h: b.height, figW: fig.width, natural: img.naturalWidth > 0 };
    });
    expect(shape.natural).toBe(true);
    // TWICE what it was, owner-directed: at 0.38 it was a colour cue rather than a
    // face, and the point of a badge is telling which of your friends is standing
    // there.
    expect(shape.w / shape.figW).toBeCloseTo(0.76, 2);
    expect(shape.h).toBeCloseTo(shape.w, 1);
  });

  test('and no face at all rather than a broken one when there is no picture', async ({ page }) => {
    // Every Яндекс account and every forgotten one carries the empty string, which
    // must be absent rather than an `img` with no src painting a broken glyph over
    // the office.
    const socket = await enterOffice(page);
    await socket.snapshot();
    await expect(page.getByTestId('fintech-me-avatar')).toHaveCount(0);
    // AND NOT «rendered, then withdrawn», which `toHaveCount(0)` cannot tell apart:
    // the `@error` latch removes the very element the count is looking for, so an
    // `img` with `src=""` would come out green. `|| undefined` in the computed is
    // what prevents it; this is the assertion that says so.
    const emptySrc = await page.evaluate(
      () => document.querySelectorAll('[data-testid="fintech-plane"] img:not([src]), [data-testid="fintech-plane"] img[src=""]').length,
    );
    expect(emptySrc).toBe(0);
  });

  test('your own figure is ringed on the floor, and nobody else’s is', async ({ page }) => {
    // WITH THREE COLLEAGUES IN ONE OPEN PLAN, all built the same way and all
    // wearing a face, `opacity: 0.88` on everybody else is not enough to find
    // yourself while something is walking at you.
    const socket = await enterOffice(page);
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600, p: 2 }] });
    await expect(page.getByTestId('fintech-peer')).toHaveCount(1);

    const rings = await page.evaluate(() => {
      const ring = (sel: string) => {
        const el = document.querySelector(sel)!;
        const st = getComputedStyle(el, '::after');
        return { content: st.content, borderWidth: st.borderTopWidth, width: st.width };
      };
      return { me: ring('[data-testid="fintech-me"]'), peer: ring('[data-testid="fintech-peer"]') };
    });
    // A real ::after with a real border on you...
    expect(rings.me.content).not.toBe('none');
    expect(parseFloat(rings.me.borderWidth)).toBeGreaterThan(0);
    expect(parseFloat(rings.me.width)).toBeGreaterThan(0);
    // ...and NO pseudo-element at all on a colleague, which is what makes it a
    // marker. Asserted as `content: none` rather than as an either/or: a computed
    // style is reported for a pseudo-element that does not generate, so a
    // disjunction over its border width would pass whatever the rule said.
    expect(rings.peer.content).toBe('none');
  });

  test('the ring is no wider than the ground he is standing on', async ({ page }) => {
    // THE OUTCOME, NOT THE MECHANISM. `z-index: -1` orders the ring behind its own
    // body and head and nothing more — the figure carries `z-index: var(--band)`
    // while a desk is positioned at `auto`, so the whole figure including this
    // pseudo-element always paints above the furniture and no value could change
    // that. What makes that harmless is the ring's SIZE: it is the collision disc,
    // the ground `PlayerRadius` guarantees nothing else occupies, so it can never
    // claim floor the player is not standing on.
    //
    // Derived from the served radius rather than from the number in the stylesheet,
    // so retuning either one has to keep them consistent.
    const socket = await enterOffice(page);
    await socket.snapshot();
    const seen = await page.evaluate(() => {
      const me = document.querySelector('[data-testid="fintech-me"]')!;
      const office = document.querySelector('[data-testid="fintech-office"]')!.getBoundingClientRect();
      return {
        ringZ: getComputedStyle(me, '::after').zIndex,
        ringW: parseFloat(getComputedStyle(me, '::after').width),
        officeW: office.width,
      };
    });
    expect(seen.ringZ).toBe('-1');
    const discFraction = (2 * CONFIG.office.player_radius) / CONFIG.office.w;
    expect(seen.ringW / seen.officeW).toBeLessThanOrEqual(discFraction);
    // And not vanishingly small either, or the marker is not a marker.
    expect(seen.ringW / seen.officeW).toBeGreaterThan(discFraction * 0.5);
  });
});

test('a colleague’s face survives the top wall, because it is inside his own box', {
  tag: '@wide',
}, async ({ page }) => {
  // THE OTHER HALF OF THE BADGE CHANGE, which shipped with no assertion at all.
  // The badge used to sit at `top: -6%`, outboard of the figure's box; the wall
  // above the room is a whole figure deep, so that cleared it at the tested sizes
  // — but `--unit` has a `clamp()` floor, and where it engages the figure is taller
  // than the wall and anything outboard is the first thing lost. Inside the box it
  // cannot happen at any width, and this is what says so.
  const socket = await enterOffice(page);
  await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 40, p: 1 }] });
  const peer = page.locator('[data-testid="fintech-peer"][data-peer="AbCdEfGhIjKl"]');
  await expect
    .poll(() => peer.evaluate((el) => getComputedStyle(el).getPropertyValue('--y').trim()))
    .toBe(String(0.4 / CONFIG.office.h));

  const seen = await page.evaluate(() => {
    const fig = document.querySelector('[data-testid="fintech-peer"]')!.getBoundingClientRect();
    const badge = document.querySelector('[data-testid="fintech-peer-avatar"]')!.getBoundingClientRect();
    const plane = document.querySelector('[data-testid="fintech-plane"]')!.getBoundingClientRect();
    return { figTop: fig.top, badgeTop: badge.top, badgeH: badge.height, planeTop: plane.top };
  });
  // Inside his own box, so the wall can never reach it...
  expect(seen.badgeTop).toBeGreaterThanOrEqual(seen.figTop - 1);
  // ...and therefore inside the clipping box, whole.
  expect(seen.badgeTop).toBeGreaterThanOrEqual(seen.planeTop - 1);
  expect(seen.badgeH).toBeGreaterThan(0);
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — who you are this shift', () => {
  test('the ending says who was working, from the served cast', async ({ page }) => {
    // NOT «Карен», and that is the whole reframe: the office is a fintech and the
    // person standing still is whoever clocked in. The index comes from the shift
    // response and the NAME from the catalogue, so a client that hardcoded either
    // one fails here — the stub's cast is marked, and its index is 2.
    //
    // THE ENDING IS THE ONLY PLACE IT IS SHOWN. It was on the play HUD too, and it
    // was dropped: the persona changes nothing about the game, and it was the
    // widest cell in a strip that has to hold the money, the clock, the tempo and
    // the way out on a 360 px phone.
    const socket = await enterOffice(page);
    await socket.over({ cause: 'left' });
    await expect(page.getByTestId('fintech-over')).toBeVisible();
    await expect(page.getByTestId('fintech-over-who')).toContainText(CONFIG.personas[2]);
  });

  test('a socket that attaches to somebody else’s shift is told who it is', async ({ page }) => {
    // A SECOND DEVICE, or a reconnect after the tab slept. The shift was not
    // started here, so the HTTP response never carried a persona to this client —
    // the ready frame is what tells it, and this is the only place that is proved.
    // A RESUMED shift enters play on mount, so there is no start button to click
    // and `enterOffice` is the wrong door — the same shape the resume test uses.
    const socket = await stubSocket(page);
    await openSplash(page, { resume: true });
    await expect(page.getByTestId('fintech-play')).toBeVisible();
    await socket.ready({ persona: 3 });
    // Read where the persona is now shown: the ending. The claim is about the
    // READY frame carrying it, not about which screen draws it.
    await socket.over({ cause: 'left' });
    await expect(page.getByTestId('fintech-over-who')).toContainText(CONFIG.personas[3]);
  });

  test('and the play HUD does not spend a cell on it', async ({ page }) => {
    // The removal, pinned. A readout that says nothing a player can act on is a
    // readout competing for width with three that they can.
    await enterOffice(page);
    await expect(page.getByTestId('fintech-who')).toHaveCount(0);
  });

  test('the cheatsheet names the whole cast, derived rather than typed', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    const rules = page.getByTestId('fintech-rules');
    for (const name of CONFIG.personas) await expect(rules).toContainText(name);
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — the кальян and the cloud', () => {
  test('the кальян stands where the catalogue put it, and goes when it is taken', async ({
    page,
  }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ hs: 1 << 1 });
    const hookah = page.getByTestId('fintech-hookah');
    await expect(hookah).toHaveCount(1);
    // From the catalogue by index, never from a coordinate on the frame.
    await expect(hookah).toHaveCSS('--x', String(CONFIG.hookah.spots[1].x / CONFIG.office.w));
    await expect(hookah).toHaveCSS('--y', String(CONFIG.hookah.spots[1].y / CONFIG.office.h));

    // Gone the moment its bit leaves the mask, which is the whole of what «it is
    // not on the floor» is on this wire.
    await socket.snapshot({ hs: 0 });
    await expect(hookah).toHaveCount(0);
  });

  test('a full office has one of each per person, and the plane draws all of them', async ({
    page,
  }) => {
    // THE COUNT IS THE MECHANIC. One bottle in a room of three is a race the
    // nearest man wins every time; one per person makes the walk worth taking
    // whoever you are. On the wire that is a MASK — a bit per catalogue spot — so
    // three props cost exactly what one did, and the plane draws a figure per bit.
    const socket = await enterOffice(page);
    await socket.snapshot({ bs: 0b101, hs: 0b11 });

    const bottles = page.getByTestId('fintech-bottle');
    await expect(bottles).toHaveCount(2);
    await expect(page.getByTestId('fintech-hookah')).toHaveCount(2);

    // AND EACH ON ITS OWN SPOT, from the catalogue by the index its bit names —
    // two props drawn on one tile would be one prop drawn twice.
    const xs = await bottles.evaluateAll((els) =>
      els.map((el) => getComputedStyle(el).getPropertyValue('--x').trim()),
    );
    expect(new Set(xs).size, `both bottles are on ${xs[0]}`).toBe(2);
    expect(xs).toContain(String(CONFIG.bottle.spots[0].x / CONFIG.office.w));
    expect(xs).toContain(String(CONFIG.bottle.spots[2].x / CONFIG.office.w));

    // One of them goes: the others stay, and the mark lands on the one that went.
    await socket.snapshot({ bs: 0b001, hs: 0b11 });
    await expect(bottles).toHaveCount(1);
    await expect(page.getByTestId('fintech-pop')).toHaveAttribute('data-kind', 'bottle');
  });

  test('a cloud is drawn on you and on a colleague alike', async ({ page }) => {
    // A buff only its owner can see is unfinished, and which colleague the лысый
    // can no longer walk at is the most useful thing to know about somebody else.
    const socket = await enterOffice(page);
    await socket.snapshot({
      iv: 9000,
      pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600, iv: 4000 }],
    });
    const clouded = await page.evaluate(() => ({
      me: document.querySelector('[data-testid="fintech-me"]')!.getAttribute('data-cloud'),
      peer: document.querySelector('[data-testid="fintech-peer"]')!.getAttribute('data-cloud'),
      mePaints: getComputedStyle(
        document.querySelector('[data-testid="fintech-me"]')!,
        '::before',
      ).content,
    }));
    expect(clouded.me).toBe('1');
    expect(clouded.peer).toBe('1');
    // And it actually paints, rather than only setting an attribute — the lesson of
    // the three CSS rules that were written and never landed.
    expect(clouded.mePaints).not.toBe('none');
  });

  test('and it clears when the cloud does', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ iv: 9000 });
    await expect(page.getByTestId('fintech-me')).toHaveAttribute('data-cloud', '1');
    await socket.snapshot({ iv: undefined });
    await expect(page.getByTestId('fintech-me')).not.toHaveAttribute('data-cloud', '1');
  });

  test('the row over the office says what is running and for how long', async ({ page }) => {
    // A verb is marked where it happened and a buff is drawn on the figure — neither
    // says HOW LONG, and ten seconds of being uncatchable is worth crossing the
    // floor for while two seconds is not.
    const socket = await enterOffice(page);
    await expect(page.getByTestId('fintech-hud-buffs')).toHaveCount(0);

    await socket.snapshot({ iv: 9200, b: { x: 300, y: 1500, g: 40, d: 3100 } });
    const row = page.getByTestId('fintech-hud-buffs');
    await expect(row).toHaveCount(1);
    // Rounded UP, so a running timer never reads zero; longest first, so the row
    // does not reorder itself as the timers run down past each other.
    await expect(row).toContainText('10');
    await expect(row).toContainText('4');
    const order = await row.evaluate((el) =>
      [...el.querySelectorAll('[data-buff]')].map((n) => n.getAttribute('data-buff')),
    );
    expect(order).toEqual(['cloud', 'drunk']);

    // And gone when nothing is running, so the office keeps the pixels.
    await socket.snapshot({ iv: undefined, b: { x: 300, y: 1500, g: 40 } });
    await expect(row).toHaveCount(0);
  });

  test('he names the man who vanished, and only on that man’s screen', async ({ page }) => {
    // The office knows who vanished; a name on a frame that repeats ten times a
    // second to say something that changes once a shift is what ADR-037 refused. So
    // the server sends the templated line to that occupant alone and the client
    // fills it in from a persona it already knows.
    const socket = await enterOffice(page);
    await socket.snapshot({ iv: 9000, b: { x: 300, y: 1500, g: 40, p: 2 } });
    const said = page.getByTestId('fintech-boss-say');
    await expect(said).toContainText(CONFIG.personas[2].toUpperCase());
    // And never as a raw placeholder, whatever the pool says.
    await expect(said).not.toContainText('{}');
  });

  test('the cheatsheet states the rule, from the served numbers', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    const rules = page.getByTestId('fintech-rules');
    // The stub's numbers, not production's — 11,5 s and 19,5 s.
    await expect(rules).toContainText('11,5 с');
    await expect(rules).toContainText('19,5 с');
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — «РОУТЕР УПАЛ»', () => {
  test('the button sits on top of the dash and says what the catalogue says', async ({ page }) => {
    // It is a real tap target on a phone like every other control here, and its
    // words come from the served verb rather than from this client — the stub's
    // label carries the СТЕНД marker, so a hardcoded button fails.
    await enterOffice(page);
    const router = page.getByTestId('fintech-router');
    await expect(router).toBeVisible();
    await expect(router).toContainText(CONFIG.router.label);
    await expect(router).toBeEnabled();

    const [box, dash, stick] = await Promise.all([
      router.boundingBox(),
      page.getByTestId('fintech-dash').boundingBox(),
      page.getByTestId('fintech-stick').boundingBox(),
    ]);
    expect(box!.height, 'a control under 44 px is not a tap target').toBeGreaterThanOrEqual(44);
    // DIRECTLY ABOVE THE DASH, in one column: the same thumb reaches both without
    // crossing the glass, and the middle of the band is left to the colleagues.
    expect(box!.y + box!.height, 'the router is not above the dash').toBeLessThanOrEqual(dash!.y + 1);
    expect(Math.abs(box!.x + box!.width / 2 - (dash!.x + dash!.width / 2)),
      'the two are not one column').toBeLessThan(4);
    // And it is nowhere near the stick, which keeps its clearance from the edge.
    expect(box!.x).toBeGreaterThan(stick!.x + stick!.width);
  });

  test('and it never changes size, however it is pressed', async ({ page }) => {
    // A CONTROL UNDER A THUMB MUST NOT MOVE. The label used to be REPLACED by the
    // countdown, so the button was as wide as whichever string was showing and the
    // whole column jumped on every press. The label stays and the state line under
    // it changes instead — same box, different text.
    const socket = await enterOffice(page);
    const router = page.getByTestId('fintech-router');
    const size = async () => {
      const b = (await router.boundingBox())!;
      return { w: Math.round(b.width), h: Math.round(b.height), x: Math.round(b.x), y: Math.round(b.y) };
    };
    const ready = await size();

    // On cooldown, with Claude still away — the two states that change the text.
    await socket.snapshot({ cl: undefined, ca: 9000, rd: 45000 });
    await expect(router).toBeDisabled();
    expect(await size(), 'it resized while Claude was away').toEqual(ready);

    await socket.snapshot({ cl: { x: 400, y: 1400, c: 30 }, rd: 45000 });
    expect(await size(), 'it resized while counting down').toEqual(ready);

    // And a four-figure countdown, which is the widest the state line ever gets.
    await socket.snapshot({ cl: { x: 400, y: 1400, c: 30 }, rd: 9900 });
    expect(await size(), 'it resized as the digits fell').toEqual(ready);
  });

  test('the colleagues keep the middle of the band, as they always had', async ({ page }) => {
    // A REDIRECT LIVES BETWEEN THE STICK AND THE THUMB'S COLUMN. The router was
    // put there first and pushed them about; the middle is theirs, and there is
    // one per colleague, so it has to be the part of the band that grows.
    const socket = await enterOffice(page);
    await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 500, y: 500 }] });
    const redirect = page.getByTestId('fintech-redirect');
    await expect(redirect).toHaveCount(1);

    const [verb, stick, router] = await Promise.all([
      redirect.boundingBox(),
      page.getByTestId('fintech-stick').boundingBox(),
      page.getByTestId('fintech-router').boundingBox(),
    ]);
    expect(verb!.x, 'a colleague is to the left of the stick').toBeGreaterThan(stick!.x);
    expect(verb!.x + verb!.width, 'a colleague overlaps the thumb’s column')
      .toBeLessThanOrEqual(router!.x + 1);
  });

  test('pressing it sends the verb, with no target because there is nothing to aim', async ({
    page,
  }) => {
    const socket = await enterOffice(page);
    await page.getByTestId('fintech-router').click();
    await expect
      .poll(() => socket.sent().filter((m) => m.includes('"v":"router"')).length)
      .toBeGreaterThan(0);
    const sent = JSON.parse(socket.sent().find((m) => m.includes('"v":"router"'))!);
    expect(sent.t).toBe('fintech_do');
    expect(sent.v).toBe('router');
    expect(sent.tg, 'the router verb carries a target it has no use for').toBeUndefined();
  });

  test('Claude leaves the floor entirely while the router is down', async ({ page }) => {
    // AN ABSENT `cl` IS WHAT SAYS HE IS GONE — the office stops sending him rather
    // than sending a stale position with a flag beside it — and `ca` is how long
    // for. So the figure has to leave the DOM, not merely be moved somewhere.
    const socket = await enterOffice(page);
    await socket.snapshot();
    await expect(page.getByTestId('fintech-claude')).toHaveCount(1);

    await socket.snapshot({ cl: undefined, ca: 9000, rd: 45000 });
    await expect(page.getByTestId('fintech-claude')).toHaveCount(0);
    // And the row says how long, because a state with a duration you cannot read
    // is a state you cannot decide against.
    await expect(page.getByTestId('fintech-hud-buffs')).toContainText('9');
    await expect(page.getByTestId('fintech-hud-buffs')).toContainText('клод');
    // The button is spent, and visibly so.
    await expect(page.getByTestId('fintech-router')).toBeDisabled();

    // And he is back the moment the office sends him again.
    await socket.snapshot({ cl: { x: 400, y: 1400, c: 30 }, rd: 30000 });
    await expect(page.getByTestId('fintech-claude')).toHaveCount(1);
    // Still on cooldown, though — the wait outlasts the absence on purpose.
    await expect(page.getByTestId('fintech-router')).toBeDisabled();

    await socket.snapshot({ cl: { x: 400, y: 1400, c: 30 } });
    await expect(page.getByTestId('fintech-router')).toBeEnabled();
  });

  test('the caller says so, and every screen reads it off the same pool', async ({ page }) => {
    // WHO DID IT is the balloon's job and it rides `p` for you and `pr[].p` for a
    // colleague, so a second screen sees the announcement with no extra byte.
    const socket = await enterOffice(page);
    const routerIndex = CONFIG.player_lines.indexOf(ROUTER_SAY);
    await socket.snapshot({
      p: routerIndex,
      pr: [{ i: 'AbCdEfGhIjKl', x: 500, y: 500, p: routerIndex }],
    });
    await expect(page.getByTestId('fintech-me-say')).toContainText(ROUTER_SAY);
    await expect(page.getByTestId('fintech-peer-say')).toContainText(ROUTER_SAY);
  });

  test('the cheatsheet states the rule, from the served numbers', async ({ page }) => {
    await openSplash(page);
    const rules = page.getByTestId('fintech-rules');
    await expect(rules).toContainText('9 с');
    await expect(rules).toContainText('45 с');
    // The half a hardcoded cheatsheet would get wrong: whose timer it is.
    await expect(rules).toContainText('таймер офиса');
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — Claude Code', () => {
  test('he is on the plane, placed where the frame put him', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ cl: { x: 600, y: 900, c: 120, p: 1 } });
    const claude = page.getByTestId('fintech-claude');
    await expect(claude).toHaveCount(1);
    await expect
      .poll(() => claude.evaluate((el) => getComputedStyle(el).getPropertyValue('--x').trim()))
      .toBe(String(6 / CONFIG.office.w));
    // His words come from his OWN served pool, by index — not the лысый's.
    await expect(page.getByTestId('fintech-claude-say')).toHaveText(CONFIG.claude_lines[1]);
  });

  test('he is built like the others and marked by what you can see at thirty pixels', async ({
    page,
  }) => {
    // No logo and no likeness: colour and silhouette. The stubble and the cigarette
    // have to actually paint, which is the lesson of the three CSS rules that were
    // written and never landed.
    const socket = await enterOffice(page);
    await socket.snapshot({ cl: { x: 600, y: 900, c: 200 } });
    const shape = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="fintech-claude"]')!;
      const box = (sel: string) => {
        const n = el.querySelector(sel);
        const r = n?.getBoundingClientRect();
        return r ? r.width * r.height : 0;
      };
      return {
        head: box('.fintech-fig-head'),
        body: box('.fintech-fig-body'),
        stubble: box('.fintech-claude-stubble'),
        cig: box('.fintech-claude-cig'),
      };
    });
    expect(shape.head).toBeGreaterThan(0);
    expect(shape.body).toBeGreaterThan(0);
    expect(shape.stubble, 'the stubble does not paint').toBeGreaterThan(0);
    expect(shape.cig, 'the cigarette does not paint').toBeGreaterThan(0);
  });

  test('the slow shows on you and on a colleague, and in the row', async ({ page }) => {
    // A debuff is as public as a buff — who the лысый reaches first is exactly the
    // sort of thing every screen has to show.
    const socket = await enterOffice(page);
    await socket.snapshot({ sl: 4500, pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600, sl: 2000 }] });
    const marks = await page.evaluate(() => ({
      me: document.querySelector('[data-testid="fintech-me"]')!.getAttribute('data-slow'),
      peer: document.querySelector('[data-testid="fintech-peer"]')!.getAttribute('data-slow'),
      skin: getComputedStyle(document.querySelector('[data-testid="fintech-me"]')!).getPropertyValue(
        '--skin',
      ),
    }));
    expect(marks.me).toBe('1');
    expect(marks.peer).toBe('1');
    // On the SKIN rather than on the positioning box, which is the rule the лысый's
    // green already follows — a background on a figure's box paints the coordinate.
    expect(marks.skin.trim()).not.toBe('');

    const row = page.getByTestId('fintech-hud-buffs');
    await expect(row).toContainText('5');
    const bad = await row.evaluate((el) =>
      el.querySelector('[data-buff="slow"]')!.getAttribute('data-bad'),
    );
    expect(bad, 'the slow is not marked as working against you').toBe('1');
  });

  test('the cheatsheet says what he costs, from the served numbers', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    const rules = page.getByTestId('fintech-rules');
    // The stub's numbers: 75 % of the walk for 4,5 s, at 2,9 m/s.
    await expect(rules).toContainText('75 %');
    await expect(rules).toContainText('4,5 с');
    await expect(rules).toContainText('2,9 м/с');
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — Серега and Тёма', () => {
  test('both of them are on the plane, each saying his own lines', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({
      np: [
        { x: 400, y: 600, p: 1 },
        { x: 1200, y: 900, p: 1, c: 1 },
      ],
    });
    const said = page.getByTestId('fintech-npc-say');
    await expect(page.getByTestId('fintech-npc')).toHaveCount(2);
    // HIS OWN pool, by index — the frame's ORDER is which of them it is, so a client
    // that read the wrong array would show one man the other's line.
    await expect(said.nth(0)).toHaveText(CONFIG.npcs[0].lines[1]);
    await expect(said.nth(1)).toHaveText(CONFIG.npcs[1].lines[1]);
  });

  test('the caption and the paraglider both actually paint', async ({ page }) => {
    // What tells them apart at thirty pixels. A rule that exists only in a diff does
    // not exist, so this measures the effect rather than the presence of the CSS.
    const socket = await enterOffice(page);
    await socket.snapshot({ np: [{ x: 400, y: 600 }, { x: 1200, y: 900 }] });
    const marks = await page.evaluate(() => {
      const one = (key: string) => {
        const el = document.querySelector(`[data-npc="${key}"] .fintech-npc-mark`)!;
        const r = el.getBoundingClientRect();
        const st = getComputedStyle(el);
        const after = getComputedStyle(el, '::after');
        return { area: r.width * r.height, background: st.backgroundImage, caption: after.content };
      };
      return { serega: one('serega'), tema: one('tema') };
    });
    // Серега wears a caption, which is real text so it is legible at any size.
    expect(marks.serega.caption).toContain('ХУЙ');
    // Тёма wears a canopy, which is a painted shape.
    expect(marks.tema.area).toBeGreaterThan(0);
    expect(marks.tema.background).not.toBe('none');
  });

  test('both carry their own кальян and stand in a cloud that never goes out', async ({ page }) => {
    // They hold their own now and never touch the office's one, so there is no state
    // to it and no flag on the frame — the cloud is simply part of what they look
    // like. A player's cloud means uncatchable; theirs means nothing at all, so it
    // must be smaller and dimmer or the two read as the same thing.
    const socket = await enterOffice(page);
    await socket.snapshot({ iv: 9000, np: [{ x: 400, y: 600 }, { x: 1200, y: 900 }] });
    const seen = await page.evaluate(() => {
      const cloud = (sel: string) => {
        const st = getComputedStyle(document.querySelector(sel)!, '::before');
        return { content: st.content, w: parseFloat(st.width) };
      };
      const pipe = (sel: string) => {
        const el = document.querySelector(`${sel} .fintech-npc-pipe`)!;
        const r = el.getBoundingClientRect();
        // FOUR PARTS, like the floor one: the box is the glass base, `::before` the
        // stem and bowl, `::after` the hose. A single blob would pass an area check
        // and read as a bottle, which is what the floor кальян already had to be
        // fixed for.
        return {
          area: r.width * r.height,
          base: getComputedStyle(el).backgroundImage,
          stem: getComputedStyle(el, '::before').content,
          bowl: getComputedStyle(el, '::after').content,
          hose: getComputedStyle(el, '::after').boxShadow,
        };
      };
      return {
        me: cloud('[data-testid="fintech-me"]'),
        serega: cloud('[data-npc="serega"]'),
        tema: cloud('[data-npc="tema"]'),
        seregaPipe: pipe('[data-npc="serega"]'),
        temaPipe: pipe('[data-npc="tema"]'),
        npcOpacity: getComputedStyle(document.querySelector('[data-npc="serega"]')!).opacity,
      };
    });
    // BOTH of them, unconditionally — no frame said so.
    expect(seen.serega.content).not.toBe('none');
    expect(seen.tema.content).not.toBe('none');
    // And each is holding the thing the smoke comes from.
    for (const [who, p] of [
      ['Серега', seen.seregaPipe],
      ['Тёма', seen.temaPipe],
    ] as const) {
      expect(p.area, `${who} has no кальян in his hand`).toBeGreaterThan(0);
      expect(p.base, `${who}'s кальян has no glass base`).not.toBe('none');
      expect(p.stem, `${who}'s кальян has no stem`).not.toBe('none');
      expect(p.bowl, `${who}'s кальян has no bowl`).not.toBe('none');
      expect(p.hose, `${who}'s кальян has no hose`).not.toBe('none');
    }
    // Smaller than a player's, whose cloud means something.
    expect(seen.serega.w).toBeLessThan(seen.me.w);
    // And recessed, so a glance never mistakes one for somebody who matters.
    expect(Number(seen.npcOpacity)).toBeLessThan(1);
  });

  test('the cheatsheet names them and says they do not matter', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('fintech-splash')).toBeVisible();
    const rules = page.getByTestId('fintech-rules');
    for (const n of CONFIG.npcs) await expect(rules).toContainText(n.name);
  });
});

test.describe('«СИМУЛЯТОР ФИНТЕХА» — the three things that had to look right', () => {
  test('Claude wears a burst on his shirt', async ({ page }) => {
    // Whose tool he is, said by the shape rather than by a wordmark. It has to
    // actually paint AND actually be masked into spokes — a solid square would pass
    // a bare "is it there" check and read as a badge.
    const socket = await enterOffice(page);
    await socket.snapshot({ cl: { x: 600, y: 900, c: 60 } });
    const mark = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="fintech-claude"] .fintech-claude-mark')!;
      const r = el.getBoundingClientRect();
      const st = getComputedStyle(el);
      return {
        area: r.width * r.height,
        square: Math.abs(r.width - r.height),
        mask: st.maskImage || st.webkitMaskImage,
      };
    });
    expect(mark.area).toBeGreaterThan(0);
    // Square, so the spokes are evenly spaced rather than an ellipse of them.
    expect(mark.square).toBeLessThan(1.5);
    expect(mark.mask, 'the burst is a solid block rather than spokes').toContain('conic');
  });

  test('the кальян is a кальян and not a bottle', async ({ page }) => {
    // It shipped drawn as a bottle and read as one. A кальян is a bowl on a stem on a
    // wide base with a hose off the side: taller than it is wide, and both halves
    // have to paint.
    const socket = await enterOffice(page);
    await socket.snapshot({ hs: 1 });
    const shape = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="fintech-hookah"]')!;
      const r = el.getBoundingClientRect();
      const bg = (pseudo: string) => getComputedStyle(el, pseudo).backgroundImage;
      return { w: r.width, h: r.height, base: bg('::before'), top: bg('::after') };
    });
    expect(shape.h / shape.w, 'it is not taller than it is wide').toBeGreaterThan(1.5);
    expect(shape.base, 'the glass base does not paint').not.toBe('none');
    expect(shape.top, 'the bowl, stem and hose do not paint').not.toBe('none');
    // Three gradients in the upper half: the bowl, the stem and the hose.
    expect(shape.top.split('gradient').length - 1).toBeGreaterThanOrEqual(3);
  });

  test('the cloud is bigger than the man behind it', async ({ page }) => {
    // A cloud that fits inside the figure reads as a puff of breath. This one is
    // something you are hiding behind, which is what it mechanically is.
    const socket = await enterOffice(page);
    await socket.snapshot({ iv: 9000 });
    const sizes = await page.evaluate(() => {
      const me = document.querySelector('[data-testid="fintech-me"]')!;
      const cloud = getComputedStyle(me, '::before');
      return {
        cloudW: parseFloat(cloud.width),
        cloudH: parseFloat(cloud.height),
        figW: me.getBoundingClientRect().width,
      };
    });
    expect(sizes.cloudW).toBeGreaterThan(sizes.figW);
    expect(sizes.cloudH).toBeGreaterThan(sizes.figW);
  });
});

test('your own figure is outlined and no colleague is', async ({ page }) => {
  // The ground ring was not enough: three figures built identically, all wearing a
  // face, and a ring under the feet is something you look for rather than see. The
  // outline is on the HEAD and the BODY rather than on the box, because a figure's box
  // is a coordinate and not a surface — the лысый shipped once as a filled rectangle
  // for exactly that reason.
  const socket = await enterOffice(page);
  await socket.snapshot({ pr: [{ i: 'AbCdEfGhIjKl', x: 300, y: 600 }] });
  await expect(page.getByTestId('fintech-peer')).toHaveCount(1);

  const seen = await page.evaluate(() => {
    const part = (who: string, cls: string) =>
      getComputedStyle(document.querySelector(`${who} .${cls}`)!).boxShadow;
    const st = getComputedStyle(document.querySelector('[data-testid="fintech-me"]')!);
    return {
      meHead: part('[data-testid="fintech-me"]', 'fintech-fig-head'),
      meBody: part('[data-testid="fintech-me"]', 'fintech-fig-body'),
      peerHead: part('[data-testid="fintech-peer"]', 'fintech-fig-head'),
      boxShadow: st.boxShadow,
      boxBackground: st.backgroundImage,
      boxOutline: st.outlineStyle,
    };
  });
  // A bright ring on both of his shapes...
  expect(seen.meHead).toContain('255, 255, 255');
  expect(seen.meBody).toContain('255, 255, 255');
  // ...and not on a colleague's, which is what makes it a marker rather than a style.
  expect(seen.peerHead).not.toContain('255, 255, 255');
  // AND NOTHING ON THE BOX, which is the rule the лысый's filled rectangle bought.
  expect(seen.boxShadow).toBe('none');
  expect(seen.boxBackground).toBe('none');
  // `outlineStyle` rather than `outlineWidth`: an element with no outline still
  // computes a width of `medium`, so the width says nothing about whether one is
  // drawn.
  expect(seen.boxOutline).toBe('none');
});

test('Тёма’s words clear his paraglider instead of hiding under it', async ({ page }) => {
  // His canopy reaches above the figure's box and a balloon sits on that box's top
  // edge, so the two shared a strip of air — and the canopy is a later sibling, so it
  // painted over his own words. A balloon is a READOUT; what a figure is wearing is
  // not, so the words win on both counts.
  const socket = await enterOffice(page);
  await socket.snapshot({
    np: [
      { x: 400, y: 900, p: 1 },
      { x: 1000, y: 900, p: 1 },
    ],
  });
  const seen = await page.evaluate(() => {
    const tema = document.querySelector('[data-npc="tema"]')!;
    const say = tema.querySelector('[data-testid="fintech-npc-say"]')!.getBoundingClientRect();
    const mark = tema.querySelector('.fintech-npc-mark')!.getBoundingClientRect();
    const figBox = tema.getBoundingClientRect();
    return {
      sayBottom: say.bottom,
      sayHeight: say.height,
      markTop: mark.top,
      markHeight: mark.height,
      figTop: figBox.top,
      figHeight: figBox.height,
      unit: getComputedStyle(tema).fontSize,
      markArea: mark.width * mark.height,
      sayZ: getComputedStyle(tema.querySelector('[data-testid="fintech-npc-say"]')!).zIndex,
    };
  });
  expect(seen.sayHeight, 'he is not saying anything, so this proves nothing').toBeGreaterThan(0);
  expect(seen.markArea, 'he has no paraglider, so this proves nothing').toBeGreaterThan(0);
  // The balloon's bottom edge is above the canopy's top edge — they no longer overlap.
  expect(seen.sayBottom).toBeLessThanOrEqual(seen.markTop + 1);
  // And it would win the paint order anyway, which is the belt to that brace.
  expect(Number(seen.sayZ)).toBeGreaterThan(0);
});

test('the readouts and the thumbs line up with the room, not the screen', {
  tag: '@wide',
}, async ({ page }) => {
  // ON A DESKTOP THE ROOM IS A COLUMN IN THE MIDDLE, and the overlays were laid out
  // against the whole play box — so the money sat in the far top-left corner with the
  // quit button in the far top-right, an armspan away from the office they describe.
  // At phone width this changes nothing, because the plane is full-bleed there, which
  // is why the claim is @wide.
  await enterOffice(page);
  const seen = await page.evaluate(() => {
    const box = (sel: string) => {
      const r = document.querySelector(sel)!.getBoundingClientRect();
      return { left: r.left, right: r.right, width: r.width };
    };
    return {
      plane: box('[data-testid="fintech-plane"]'),
      hud: box('.fintech-hud'),
      streak: box('[data-testid="fintech-hud-streak"]'),
      controls: box('.fintech-controls'),
      play: box('[data-testid="fintech-play"]'),
    };
  });
  // Each overlay is the room's width, within a pixel of rounding...
  for (const [name, b] of [
    ['the readouts', seen.hud],
    ['the streak bar', seen.streak],
    ['the controls', seen.controls],
  ] as const) {
    expect(Math.abs(b.width - seen.plane.width), `${name} is not the room's width`).toBeLessThan(2);
    expect(Math.abs(b.left - seen.plane.left), `${name} is not aligned with the room`).toBeLessThan(2);
  }
  // ...and on a wide screen that is genuinely narrower than the play box, so the test
  // discriminates rather than passing because everything happens to be full width.
  if (seen.play.width > 900) {
    expect(seen.plane.width).toBeLessThan(seen.play.width - 40);
  }
});

test('the pane is full-bleed even though the room inside it cannot be', async ({ page }) => {
  // THE ROOM IS A PORTRAIT 16 × 22 AND KEEPS THAT SHAPE EXACTLY, so the plane can
  // only ever fill one axis of the box it is given: measured, 360 of 360 wide but
  // 501 of 728 tall on a phone, and 595 of 1184 wide on a 1440 desktop. The
  // leftover is unavoidable — what is not is leaving it a flat slab of a different
  // colour, which reads as the game not covering its own pane.
  //
  // So the element that fills the play box carries the wall's own gradient, and
  // this asserts exactly that: it is the full box on BOTH axes, and it paints.
  await enterOffice(page);
  const seen = await page.evaluate(() => {
    const stage = document.querySelector('.fintech-stage')!;
    const play = document.querySelector('[data-testid="fintech-play"]')!;
    const s = stage.getBoundingClientRect();
    const p = play.getBoundingClientRect();
    return {
      dw: Math.abs(s.width - p.width),
      dh: Math.abs(s.height - p.height),
      paint: getComputedStyle(stage).backgroundImage,
      planeW: document.querySelector('[data-testid="fintech-plane"]')!.getBoundingClientRect().width,
      playW: p.width,
    };
  });
  expect(seen.dw, 'the pane does not span the play box').toBeLessThan(2);
  expect(seen.dh, 'the pane does not fill the play box').toBeLessThan(2);
  expect(seen.paint, 'the pane behind the room paints nothing').not.toBe('none');
  // And the room really is the smaller thing inside it, or this asserts nothing.
  expect(seen.planeW).toBeLessThanOrEqual(seen.playW);
});
