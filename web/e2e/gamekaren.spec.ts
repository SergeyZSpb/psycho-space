import { expect, test } from '@playwright/test';
import type { Page, WebSocketRoute } from '@playwright/test';
import { seedClient } from './fixtures';

/**
 * «СИМУЛЯТОР КАРЕНА» — the layout suite, at 360 px with `/api` stubbed in the
 * browser and the socket faked, so **the test is the server**.
 *
 * WHAT THIS SUITE IS FOR. The office is DOM rather than a canvas, so unlike
 * «ВАНЯДУМ» this file really can see the world — where a desk is, where the
 * bald man is standing, how pleased he is. What it deliberately does NOT assert
 * on is the shape of an INPUT frame: those are emitted from the render loop, a
 * browser pauses `requestAnimationFrame` outright for a backgrounded page, and
 * with parallel workers only one page is ever visible. That claim lives in
 * `karenPredict.spec.ts`, where it is deterministic.
 *
 * THE STUB'S NUMBERS ARE DELIBERATELY NOT PRODUCTION'S. The splash's cheatsheet
 * is generated from the served catalogue, and these numbers are how that is
 * proved: a hand-typed rules line cannot pass the assertions below.
 */

const CONFIG = {
  game_key: 'karen',
  title: 'СИМУЛЯТОР КАРЕНА',
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
  sim: { hz: 20, snapshot_hz: 10 },
  // Marked, so a client that hardcoded the endings instead of reading them
  // cannot pass the ending assertions either.
  endings: [
    { key: 'promoted', title: 'ТЕБЯ ПОВЫСИЛИ, СТЕНД', sub: 'теперь ты за это отвечаешь.' },
    { key: 'left', title: 'ТЫ ПРОСТО УШЁЛ, СТЕНД', sub: 'никто не заметил.' },
  ],
  boss_lines: ['А ГДЕ?'],
  max_occupants: 3,
};

const SHIFT = { shift_id: 'shift-e2e', room: 'karen' };

interface StubOptions {
  /** Answer `shifts/current` with a shift, as if the player had reloaded. */
  resume?: boolean;
  mine?: { cause: string; salary: number; seconds: number; created_at: string }[];
  top?: { name: string; salary: number; seconds: number; cause: string }[];
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
          avatar_url: '',
        },
      });
    }
    if (path === '/api/game-karen/config') return json(200, CONFIG);
    if (path === '/api/game-karen/shifts/me') return json(200, { shifts: opts.mine ?? [] });
    if (path === '/api/game-karen/shifts/top') return json(200, { shifts: opts.top ?? [] });
    if (path === '/api/game-karen/shifts/current' && method === 'GET') {
      return opts.resume
        ? json(200, SHIFT)
        : json(404, { error: 'no_shift', trace_id: 'e2e-trace-id' });
    }
    if (path === '/api/game-karen/shifts/current' && method === 'DELETE') {
      return route.fulfill({ status: 204, body: '' });
    }
    if (path === '/api/game-karen/shifts' && method === 'POST') return json(201, SHIFT);
    return json(404, { error: 'not_found', trace_id: 'e2e-trace-id' });
  });
}

/**
 * Stands in for the office. Returns a handle the test drives, so every assertion
 * about the HUD and the plane is pushed rather than waited for.
 */
async function stubSocket(page: Page): Promise<{
  ready: () => Promise<void>;
  snapshot: (fields?: Record<string, unknown>) => Promise<void>;
  over: (fields?: Record<string, unknown>) => Promise<void>;
  sent: () => string[];
}> {
  const sent: string[] = [];
  let ws: WebSocketRoute | null = null;

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
    ready: () => send({ t: 'karen_ready', shift_id: SHIFT.shift_id }),
    snapshot: (fields = {}) =>
      send({
        t: 'karen_snap',
        k: 12,
        ack: 0,
        x: 600,
        y: 900,
        pay: 42800,
        m: 275,
        st: 4500,
        dc: 1800,
        b: { x: 300, y: 1500, g: 40 },
        ...fields,
      }),
    over: (fields = {}) => send({ t: 'karen_over', cause: 'promoted', pay: 42800, secs: 73, ...fields }),
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
  await page.goto('/app/game-karen');
}

/** Starts a shift and waits for the office. Every play test opens this way. */
async function enterOffice(page: Page, opts: StubOptions = {}) {
  const socket = await stubSocket(page);
  await openSplash(page, opts);
  await expect(page.getByTestId('karen-splash')).toBeVisible();
  await page.getByTestId('karen-start').click();
  await expect(page.getByTestId('karen-play')).toBeVisible();
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

test.describe('«СИМУЛЯТОР КАРЕНА» splash', () => {
  test('the rules cheatsheet is generated from the served catalogue', async ({ page }) => {
    // The point of the whole derived-cheatsheet arrangement: these numbers are
    // the STUB's, not production's, so a hand-written cheatsheet fails here.
    // `CLAUDE.md` makes stating a game's current rules on its splash a gate.
    await openSplash(page);
    const rules = page.getByTestId('karen-rules');
    await expect(rules).toBeVisible();
    await expect(rules).toContainText('777 ₽');
    await expect(rules).toContainText('9 с');
    await expect(rules).toContainText('×4');
    await expect(rules).toContainText('0,45 с');
    await expect(rules).toContainText('4,4 м/с');
    await expect(rules).toContainText('11,5 м/с');
    await expect(rules).toContainText('5,5 с');
    await expect(rules).toContainText('2,9 м/с');
    await expect(rules).toContainText('1,25 м');
  });

  test('and it names both ways the shift can end, in the catalogue’s words', async ({ page }) => {
    await openSplash(page);
    const rules = page.getByTestId('karen-rules');
    await expect(rules).toContainText('ТЕБЯ ПОВЫСИЛИ, СТЕНД');
    await expect(rules).toContainText('ТЫ ПРОСТО УШЁЛ, СТЕНД');
  });

  test('the controls are explained, because nothing else says how to move', async ({ page }) => {
    // The one block that is NOT derived: the server has no opinion about thumbs.
    await openSplash(page);
    const rules = page.getByTestId('karen-rules');
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
    await page.goto('/app/game-karen');
    const note = page.getByTestId('karen-disclaimer');
    await expect(note).toBeVisible();
    await expect(note).toHaveText('Все персонажи вымышлены, любые совпадения случайны.');
    await expect(page.getByTestId('karen-rules')).toBeVisible();
    await expect(note).toBeVisible();
  });

  test('the cheatsheet is laid out as blocks, so a rule can be found', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('karen-rule-block').first()).toBeVisible();
    expect(await page.getByTestId('karen-rule-block').count()).toBeGreaterThan(2);
  });

  test('previous shifts are listed, so persistence is visible without a database', async ({
    page,
  }) => {
    await openSplash(page, {
      mine: [{ cause: 'promoted', salary: 51300, seconds: 91, created_at: '2026-07-29T10:00:00Z' }],
      top: [{ name: 'Карен', salary: 99900, seconds: 210, cause: 'left' }],
    });
    const mine = page.getByTestId('karen-runs');
    await expect(mine).toBeVisible();
    await expect(mine).toContainText('51 300');
    await expect(mine).toContainText('91 с');

    const top = page.getByTestId('karen-top');
    await expect(top).toBeVisible();
    await expect(top).toContainText('Карен');
    await expect(top).toContainText('99 900');
  });

  test('the lists are absent rather than empty when there is nothing in them', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('karen-splash')).toBeVisible();
    await expect(page.getByTestId('karen-runs')).toHaveCount(0);
    await expect(page.getByTestId('karen-top')).toHaveCount(0);
  });

  test('the start button is a real tap target', async ({ page }) => {
    await openSplash(page);
    const box = await page.getByTestId('karen-start').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  });

  test('nothing overflows a 360 px phone', { tag: '@wide' }, async ({ page }) => {
    await openSplash(page, {
      mine: [{ cause: 'left', salary: 1234567, seconds: 12, created_at: '2026-07-29T10:00:00Z' }],
      top: [{ name: 'Человекснеприличнодлиннымименем', salary: 1234567, seconds: 9, cause: 'left' }],
    });
    await expect(page.getByTestId('karen-runs')).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });
});

test.describe('«СИМУЛЯТОР КАРЕНА» play', () => {
  test('starting a shift replaces the splash with the office and a real HUD', async ({ page }) => {
    await enterOffice(page);
    await expect(page.getByTestId('karen-plane')).toBeVisible();
    await expect(page.getByTestId('karen-me')).toBeVisible();
    await expect(page.getByTestId('karen-boss')).toBeVisible();
    // The furniture comes off the catalogue, one element each.
    await expect(page.getByTestId('karen-desk')).toHaveCount(CONFIG.office.desks.length);
  });

  test('the HUD follows the snapshot, not the client', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ pay: 42800, m: 275, st: 4500, dc: 1800 });

    // Every one of these is the server's number, read off the wire and formatted
    // — the money is deliberately NOT predicted, because it is the score.
    await expect(page.getByTestId('karen-hud-money')).toContainText('42 800 ₽');
    await expect(page.getByTestId('karen-hud-mult')).toContainText('×2,75');
    await expect(page.getByTestId('karen-hud-streak')).toBeVisible();
    await expect(page.getByTestId('karen-play')).toContainText('РЫВОК 1,8 с.');
  });

  test('and says the dash is ready when the snapshot omits the cooldown', async ({ page }) => {
    // `dc` is omitted rather than sent as zero, so an absent field has to mean
    // ready — read any other way the button would be dead forever.
    const socket = await enterOffice(page);
    await socket.snapshot({ dc: undefined });
    await expect(page.getByTestId('karen-play')).toContainText('РЫВОК ГОТОВ');
    await expect(page.getByTestId('karen-dash')).toBeEnabled();
  });

  test('the bald man is placed where the snapshot says, and drawn how pleased he is', async ({
    page,
  }) => {
    // Placed straight from the snapshot rather than from the render loop, which
    // is why this is assertable here at all: he is not predicted — his intent is
    // not ours to guess — so his position is written where it arrives.
    const socket = await enterOffice(page);
    await socket.snapshot({ b: { x: 300, y: 900, g: 200 } });

    const boss = page.getByTestId('karen-boss');
    // 3 m across a 12 m office, 9 m down an 18 m one.
    await expect
      .poll(() => boss.evaluate((el) => getComputedStyle(el).getPropertyValue('--x').trim()))
      .toBe('0.25');
    await expect(boss).toHaveAttribute('data-grin', 'here');

    await socket.snapshot({ b: { x: 1100, y: 200, g: 10 } });
    await expect(boss).toHaveAttribute('data-grin', 'far');
  });

  test('and the man is what changes colour, never a box around him', async ({ page }) => {
    // A FIGURE'S BOX IS A COORDINATE, NOT A SURFACE. `.karen-boss` is the
    // positioning element — a bare `--unit` × `--unit * 1.6` rectangle that the
    // head and body are painted on top of — so a `background` on it draws a
    // filled rectangle behind the man. It did, and the closer he got the more
    // visible it was: at `here` a solid orange box appeared around him, which
    // reads as a selection outline or a broken sprite rather than as somebody
    // arriving. The step is real and stays; it just belongs on `--skin` and
    // `--body`, which is what he is drawn with.
    const socket = await enterOffice(page);
    const boss = page.getByTestId('karen-boss');
    const head = boss.locator('.karen-fig-head');
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

  test('the client says hello the moment the socket opens', async ({ page }) => {
    // It goes out on every OPEN, including reconnects, because the office
    // outlives a dropped socket and a returning client has to be re-attached to
    // the shift it is already in. `send` drops rather than queues, so a hello
    // written before the handshake finished would simply vanish.
    const socket = await enterOffice(page);
    await expect
      .poll(() => socket.sent().filter((m) => m.includes('karen_hello')).length)
      .toBeGreaterThan(0);
    // And it carries nothing: identity is the connection, so there is nothing in
    // a hello to forge and nothing to validate.
    const hello = JSON.parse(socket.sent().find((m) => m.includes('karen_hello'))!);
    expect(Object.keys(hello)).toEqual(['t']);
  });

  test('standing still sends nothing at all', async ({ page }) => {
    // The rule this whole game is built on. Standing perfectly still is the
    // point, and it must cost the network nothing — the salary climbs because
    // the SERVER advances the shift, never because the client keeps talking.
    const socket = await enterOffice(page);
    await socket.snapshot();
    await page.waitForTimeout(700);
    expect(socket.sent().filter((m) => m.includes('karen_input'))).toHaveLength(0);
  });

  test('the link is reported until the office answers', async ({ page }) => {
    const socket = await enterOffice(page);
    await expect(page.getByTestId('karen-link')).toBeVisible();
    await socket.ready();
    await expect(page.getByTestId('karen-link')).toHaveCount(0);
  });

  test('every control clears 44 px and sits inside the screen', async ({ page }) => {
    if (!isMobile(page)) test.skip();
    await enterOffice(page);
    const viewport = page.viewportSize()!;
    for (const id of ['karen-stick', 'karen-dash', 'karen-quit']) {
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
    const plane = (await page.getByTestId('karen-plane').boundingBox())!;
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
    const plane = (await page.getByTestId('karen-plane').boundingBox())!;
    for (const id of ['karen-stick', 'karen-dash']) {
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
    await page.getByTestId('karen-dash').tap();
    // And the gap BETWEEN the two thumbs is still office — the wrapper that
    // lays them out must not be swallowing the middle of the screen.
    const viewport = page.viewportSize()!;
    const stick = (await page.getByTestId('karen-stick').boundingBox())!;
    // Whatever is under that point, it must not be the controls' own wrapper.
    // The office, a desk, or the stage behind the office are all fine answers —
    // the claim is only that a box which exists to lay two thumbs out does not
    // also eat the whole width of the screen between them.
    const under = await page.evaluate(
      ([x, y]) => {
        const el = document.elementFromPoint(x, y);
        return {
          inControls: !!el?.closest('.karen-controls'),
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
      ['karen-stick', 'karen-dash', 'karen-quit'].map((id) =>
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
      .getByTestId('karen-stick')
      .evaluate((el) => getComputedStyle(el).touchAction);
    expect(touchAction).toBe('none');
  });

  test('the stick answers a thumb', async ({ page }) => {
    await enterOffice(page);
    const stick = page.getByTestId('karen-stick');
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
    const knob = page.locator('.karen-stick-knob');
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

test.describe('«СИМУЛЯТОР КАРЕНА» ending', () => {
  test('being caught shows the catalogue’s ending, not one this client invented', async ({
    page,
  }) => {
    const socket = await enterOffice(page);
    await socket.snapshot();
    await socket.over({ cause: 'promoted', pay: 42800, secs: 73 });

    const over = page.getByTestId('karen-over');
    await expect(over).toBeVisible();
    await expect(page.getByTestId('karen-over-title')).toHaveText('ТЕБЯ ПОВЫСИЛИ, СТЕНД');
    await expect(over).toContainText('теперь ты за это отвечаешь.');
    await expect(page.getByTestId('karen-over-salary')).toContainText('42 800 ₽');
    await expect(over).toContainText('73');
    await expect(page.getByTestId('karen-retry')).toBeVisible();
  });

  test('walking out is an ending too, and the catalogue names that one as well', async ({
    page,
  }) => {
    const socket = await enterOffice(page);
    await socket.snapshot({ pay: 3100 });
    await page.getByTestId('karen-quit').click();

    await expect(page.getByTestId('karen-over-title')).toHaveText('ТЫ ПРОСТО УШЁЛ, СТЕНД');
    await expect(page.getByTestId('karen-over-salary')).toContainText('3 100 ₽');
  });

  test('НАЗАД goes back to the splash', async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.over();
    await expect(page.getByTestId('karen-over')).toBeVisible();
    await page.getByRole('button', { name: 'НАЗАД' }).click();
    await expect(page.getByTestId('karen-splash')).toBeVisible();
  });

  test('and the ending screen fits a phone', { tag: '@wide' }, async ({ page }) => {
    const socket = await enterOffice(page);
    await socket.over({ cause: 'promoted', pay: 1234567, secs: 4000 });
    await expect(page.getByTestId('karen-over')).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });
});

test.describe('«СИМУЛЯТОР КАРЕНА» resuming', () => {
  test('a shift already in progress is picked up rather than restarted', async ({ page }) => {
    // The office outlives a disconnect by design, so a reload must pick the
    // shift back up instead of stranding the player behind a button that
    // answers 409.
    await stubSocket(page);
    await openSplash(page, { resume: true });
    await expect(page.getByTestId('karen-play')).toBeVisible();
  });
});

test.describe('«СИМУЛЯТОР КАРЕНА» in the nav', () => {
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
      name: 'СИМУЛЯТОР КАРЕНА',
    });
    await expect(entry).toHaveCount(1);
    // Visibility is only a claim where the drawer is permanently on screen;
    // below 960px it is slid off by default and asserting it would be a race
    // against the shell's one-off peek rather than a claim about the nav.
    if ((page.viewportSize()?.width ?? 0) >= 960) await expect(entry).toBeVisible();
  });
});



