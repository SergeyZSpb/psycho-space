import { expect, test } from '@playwright/test';
import type { Page, WebSocketRoute } from '@playwright/test';
import { seedClient } from './fixtures';

/**
 * «ВАНЯДУМ» — the layout suite, at 360 px with `/api` stubbed in the browser.
 *
 * WHAT THIS SUITE CAN AND CANNOT SEE. The world is a `<canvas>`, and nothing
 * inside one can be asserted on without pixel comparison — so this file asserts
 * on everything that is deliberately NOT in it: the splash, the rules cheatsheet
 * generated from the catalogue, the HUD, the touch targets, the result screen,
 * and the fact that the page never scrolls while somebody is turning round.
 *
 * That division is the whole reason the view keeps its readouts and controls in
 * real DOM (ADR-046). If a future change moves the HUD onto the canvas because
 * it would look nicer, it deletes this file's ability to check any of it.
 *
 * The socket is intercepted with `page.routeWebSocket`, so the test IS the
 * server: it decides what a snapshot says and when the run ends.
 */

const CONFIG = {
  player: {
    radius: 0.35,
    eye_height: 1.65,
    // Deliberately NOT the production numbers. If the cheatsheet were
    // hand-written rather than derived from the catalogue, these would not show
    // up on screen and the assertion below would fail.
    walk_speed: 7.5,
    max_step: 0.9,
    max_pitch: 1.5,
    max_health: 80,
    start_health: 61,
  },
  pickups: [
    {
      key: 'beer',
      title: 'пиво',
      icon: '🍺',
      grants: 'beer',
      amount: 1,
      max: 9,
      tint: '#c8892f',
      blurb: 'Заливаешь — и панчи сами идут.',
    },
  ],
  surfaces: [
    {
      key: 'concrete',
      base: '#5b5f5e',
      accent: '#3f4443',
      noise: 0.5,
      roughness: 0.35,
      pattern: 'concrete',
    },
    { key: 'floor', base: '#4a4d4b', accent: '#2e3130', noise: 0.7, roughness: 0.5, pattern: 'concrete' },
    { key: 'ceiling', base: '#3a3d3c', accent: '#242626', noise: 0.3, roughness: 0.6, pattern: 'concrete' },
  ],
  sim: {
    hz: 20,
    snapshot_hz: 20,
    input_hz: 10,
    max_commands: 4,
    max_step_seconds: 0.2,
    redundant: 6,
    interp_delay_ms: 120,
    collision_passes: 3,
  },
};

/** Two rooms and a doorway — enough geometry to be a level, small enough to read. */
const LEVEL = {
  seed: 4242,
  sectors: [
    { id: 0, x0: 0, y0: 0, x1: 10, y1: 10, fz: 0, cz: 3.2, w: 'concrete', f: 'floor', c: 'ceiling', l: 0.8 },
    { id: 1, x0: 10, y0: 0, x1: 20, y1: 10, fz: 0.3, cz: 3.5, w: 'concrete', f: 'floor', c: 'ceiling', l: 0.6 },
  ],
  portals: [{ a: 0, b: 1, v: true, at: 10, lo: 4, hi: 6 }],
  walls: [
    { v: true, a: 0, lo: 0, hi: 10, s: 0 },
    { v: false, a: 0, lo: 0, hi: 10, s: 0 },
    { v: false, a: 10, lo: 0, hi: 10, s: 0 },
    { v: true, a: 20, lo: 0, hi: 10, s: 1 },
    { v: false, a: 0, lo: 10, hi: 20, s: 1 },
    { v: false, a: 10, lo: 10, hi: 20, s: 1 },
  ],
  pickups: [
    { id: 0, k: 'beer', s: 1, p: { x: 14, y: 4 } },
    { id: 1, k: 'beer', s: 1, p: { x: 17, y: 7 } },
  ],
  spawn: { x: 5, y: 5 },
  spawn_sector: 0,
  spawn_yaw: 0,
};

const RUN = { run_id: 'run-e2e', level: LEVEL, room: 'vanyadum' };

interface StubOptions {
  /** Answer `runs/current` with a run, as if the player had reloaded mid-game. */
  resume?: boolean;
  /** Previously finished runs, for the splash's list. */
  runs?: { success: boolean; seconds: number; beer: number; created_at: string }[];
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
    if (path === '/api/game-vanyadum/config') return json(200, CONFIG);
    if (path === '/api/game-vanyadum/runs/me') return json(200, { runs: opts.runs ?? [] });
    if (path === '/api/game-vanyadum/runs/current' && method === 'GET') {
      return opts.resume
        ? json(200, RUN)
        : json(404, { error: 'no_run', trace_id: 'e2e-trace-id' });
    }
    if (path === '/api/game-vanyadum/runs/current' && method === 'DELETE') {
      return route.fulfill({ status: 204, body: '' });
    }
    if (path === '/api/game-vanyadum/runs' && method === 'POST') return json(201, RUN);
    return json(404, { error: 'not_found', trace_id: 'e2e-trace-id' });
  });
}

/**
 * Stands in for the simulation. Returns a handle the test uses to push frames,
 * so every assertion about the HUD is driven rather than waited for.
 */
async function stubSocket(page: Page): Promise<{
  snapshot: (fields: Record<string, unknown>) => Promise<void>;
  over: (fields: Record<string, unknown>) => Promise<void>;
  sent: () => Promise<string[]>;
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
    snapshot: (fields) =>
      send({ t: 'vanyadum_snap', k: 1, ack: 1, x: 500, y: 500, z: 165, yaw: 0, s: 0, hp: 61, pk: [0, 1], ...fields }),
    over: (fields) => send({ t: 'vanyadum_over', success: true, secs: 42, c: { beer: 2 }, ...fields }),
    sent: async () => sent,
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
  await page.goto('/app/game-vanyadum');
  await expect(page.getByTestId('vanyadum-root')).toBeVisible();
}

test.describe('«ВАНЯДУМ» — the browser running this suite', () => {
  // THIS TEST IS ABOUT THE HARNESS, NOT THE GAME, and it exists because the
  // failure it names is unrecognisable without it.
  //
  // Every test below that reaches `vanyadum-start` needs a real WebGL context:
  // the view probes for one before it builds anything, and a browser without it
  // is shown the «твой браузер не умеет 3D» screen instead of a start button —
  // correctly, that is a production path (ADR-047). So when the browser loses
  // 3D, fifteen specs fail with `waiting for getByTestId('vanyadum-start')` and
  // point at the game, which is innocent.
  //
  // It happens for one environmental reason: Chromium's ANGLE picks its EGL
  // backend from `$DISPLAY`, and a DISPLAY it cannot connect to makes it choose
  // Vulkan/XCB and then exit the GPU process rather than fall back to
  // SwiftShader. `dev.sh`'s `playwright_` unsets DISPLAY for exactly this, and
  // this test is what says so out loud when something defeats that again.
  //
  // Asserting the EFFECT rather than the presence of the fix, which is the
  // lesson of the three CSS rules that were written and never landed.
  test('can actually do 3D, or every test that starts a run below is a lie', async ({ page }) => {
    await openSplash(page);
    const gl = await page.evaluate(() => {
      const probe = document.createElement('canvas');
      return !!(probe.getContext('webgl2') ?? probe.getContext('webgl'));
    });
    expect(
      gl,
      'no WebGL in this browser: ANGLE could not initialise EGL. Run the suite through ' +
        '`./dev.sh e2e` (it unsets DISPLAY) rather than `npx playwright test` from a ' +
        'shell whose $DISPLAY is set but unreachable.',
    ).toBe(true);
    // And the view agrees, so this is the same judgement the game makes.
    await expect(page.getByTestId('vanyadum-nogl')).toHaveCount(0);
    await expect(page.getByTestId('vanyadum-start')).toBeVisible();
  });
});

test.describe('«ВАНЯДУМ» splash', () => {
  test('the rules cheatsheet is generated from the served catalogue', async ({ page }) => {
    // The point of the whole derived-cheatsheet arrangement: these numbers are
    // the STUB's, not production's, so a hand-written cheatsheet fails here.
    // `CLAUDE.md` makes stating a game's current rules on its splash a gate.
    await openSplash(page);
    const rules = page.getByTestId('vanyadum-rules');
    await expect(rules).toBeVisible();
    await expect(rules).toContainText('7,5 м/с');
    await expect(rules).toContainText('90 см');
    await expect(rules).toContainText('61 из 80');
    await expect(rules).toContainText('пиво');
    await expect(rules).toContainText('максимум 9');
  });

  test('the controls are explained, because nothing else says how to move', async ({ page }) => {
    await openSplash(page);
    const rules = page.getByTestId('vanyadum-rules');
    await expect(rules).toContainText('слева');
    await expect(rules).toContainText('справа');
    await expect(rules).toContainText('WASD');
  });

  test('previous runs are listed, so persistence is visible without a database', async ({ page }) => {
    await openSplash(page, {
      runs: [{ success: true, seconds: 91, beer: 3, created_at: '2026-07-28T10:00:00Z' }],
    });
    const runs = page.getByTestId('vanyadum-runs');
    await expect(runs).toBeVisible();
    await expect(runs).toContainText('91 с');
    await expect(runs).toContainText('3');
  });

  test('the list is absent rather than empty when there is nothing in it', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('vanyadum-runs')).toHaveCount(0);
  });

  test('the start button is a real tap target', async ({ page }) => {
    await openSplash(page);
    const box = await page.getByTestId('vanyadum-start').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  });

  test('nothing overflows a 360 px phone', async ({ page }) => {
    await openSplash(page, {
      runs: [{ success: false, seconds: 12, beer: 0, created_at: '2026-07-28T10:00:00Z' }],
    });
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });
});

test.describe('«ВАНЯДУМ» play', () => {
  test('starting a run replaces the splash with the world and a real HUD', async ({ page }) => {
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();

    await expect(page.getByTestId('vanyadum-play')).toBeVisible();
    await expect(page.getByTestId('vanyadum-canvas')).toBeVisible();
    // The HUD is DOM and its numbers are readable as text — which is the whole
    // reason it is not painted into the canvas.
    await expect(page.getByTestId('vanyadum-hud')).toContainText('61');
    await expect(page.getByTestId('vanyadum-count-beer')).toBeVisible();
  });

  test('movement is predicted, so the camera does not wait for a snapshot', async ({ page }) => {
    // The complaint this whole netcode change exists to fix: iteration 1 only
    // moved the camera when a snapshot landed, so walking looked like twenty
    // frames a second. Asserted through the DOM rather than the canvas — the
    // client sends input the instant a key is held, and it does so without ever
    // having been told where it is.
    const socket = await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    // Deliberately never send a snapshot. A client that could not predict would
    // have nothing to say.
    await page.keyboard.down('KeyW');
    await expect
      // Generous, because this waits on a requestAnimationFrame loop and the
      // whole suite runs in parallel — a loaded machine throttles the frames
      // this depends on. The claim is "a frame is eventually sent", not "within
      // four seconds".
      .poll(async () => (await socket.sent()).filter((m) => m.includes('vanyadum_input')).length, {
        timeout: 15_000,
      })
      .toBeGreaterThan(0);
    await page.keyboard.up('KeyW');
  });

  test('the HUD follows the snapshot, not the client', async ({ page }) => {
    const socket = await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    await socket.snapshot({ hp: 37, c: { beer: 2 }, pk: [1] });

    await expect(page.getByTestId('vanyadum-hud')).toContainText('37');
    await expect(page.getByTestId('vanyadum-count-beer')).toContainText('2');
    await expect(page.getByTestId('vanyadum-hud')).toContainText('осталось 1');
  });

  test('the client says hello the moment the socket opens', async ({ page }) => {
    // What this suite can honestly prove. The INPUT frame's shape is asserted in
    // a unit test over buildInputFrame instead: input is emitted from the render
    // loop, a browser pauses requestAnimationFrame outright for a backgrounded
    // page, and with several parallel workers only one page is ever visible — so
    // a Playwright assertion about input frames failed about one run in three
    // for reasons that had nothing to do with the payload.
    //
    // The hello needs no render loop, and it is the part that matters here: it
    // goes out on every OPEN, including reconnects, because an arena outlives a
    // dropped socket and a returning client has to be re-attached to the run it
    // is already in.
    const socket = await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    await expect
      .poll(async () => (await socket.sent()).filter((m) => m.includes('vanyadum_hello')).length)
      .toBeGreaterThan(0);
    // And it carries nothing: identity is the connection, so there is nothing in
    // a hello to forge and nothing to validate.
    const hello = JSON.parse((await socket.sent()).find((m) => m.includes('vanyadum_hello'))!);
    expect(Object.keys(hello)).toEqual(['t']);
  });

  test('standing still sends nothing at all', async ({ page }) => {
    const socket = await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    await page.waitForTimeout(700);
    const inputs = (await socket.sent()).filter((m) => m.includes('vanyadum_input'));
    expect(inputs).toHaveLength(0);
  });

  test('the stick appears where the thumb lands, not where a circle is painted', async ({ page }) => {
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    const pad = page.getByTestId('vanyadum-pad');
    await expect(pad).toBeVisible();

    // Left half — the movement side.
    await pad.dispatchEvent('pointerdown', { pointerId: 1, clientX: 70, clientY: 400, isPrimary: true });
    const stick = page.locator('.dum-stick');
    await expect(stick).toBeVisible();
    const box = await stick.boundingBox();
    expect(box).not.toBeNull();
    // Centred on the touch, within the ring's own radius.
    expect(Math.abs(box!.x + box!.width / 2 - 70)).toBeLessThan(4);
  });

  test('the fire and quit controls clear 44 px and sit inside the screen', async ({ page }) => {
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();

    const viewport = page.viewportSize()!;
    for (const id of ['vanyadum-fire', 'vanyadum-quit']) {
      const box = await page.getByTestId(id).boundingBox();
      expect(box, id).not.toBeNull();
      expect(box!.width, id).toBeGreaterThanOrEqual(44);
      expect(box!.height, id).toBeGreaterThanOrEqual(44);
      expect(box!.x + box!.width, id).toBeLessThanOrEqual(viewport.width);
    }
  });

  test('the play surface swallows gestures, so a firefight cannot scroll the page', async ({
    page,
  }) => {
    // `touch-action: none` is the difference between turning round and
    // pull-to-refresh reloading the game mid-run.
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    const touchAction = await page
      .getByTestId('vanyadum-pad')
      .evaluate((el) => getComputedStyle(el).touchAction);
    expect(touchAction).toBe('none');
  });

  test('giving up goes back to the splash', async ({ page }) => {
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    await page.getByTestId('vanyadum-quit').click();
    await expect(page.getByTestId('vanyadum-splash')).toBeVisible();
  });

  test('a run already in progress is resumed rather than restarted', async ({ page }) => {
    // The arena outlives a disconnect by design, so a reload must pick the run
    // back up instead of stranding the player behind a button that answers 409.
    await stubSocket(page);
    await openSplash(page, { resume: true });
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();
  });
});

/**
 * Counts calls to `requestPointerLock` instead of letting them happen.
 *
 * Deliberately a stub rather than the real thing. Whether a headless browser
 * under N parallel workers actually grants a capture depends on which page has
 * focus — precisely the sort of thing that made an earlier assertion in this
 * file fail one run in three for reasons unrelated to what it claimed. What the
 * view is responsible for is ASKING; whether the browser says yes is the
 * browser's business, so that is what this measures.
 */
async function spyOnPointerLock(page: Page): Promise<() => Promise<number>> {
  await page.addInitScript(() => {
    (window as unknown as { __locks: number }).__locks = 0;
    Element.prototype.requestPointerLock = function () {
      (window as unknown as { __locks: number }).__locks += 1;
    } as typeof Element.prototype.requestPointerLock;
  });
  return () => page.evaluate(() => (window as unknown as { __locks: number }).__locks);
}

test.describe('«ВАНЯДУМ» with a mouse', () => {
  test('a phone is never asked to capture a pointer it has not got', async ({ page }) => {
    // `(pointer: fine)` is the whole gate. Without it a phone gets a prompt in
    // the middle of the screen that does nothing when tapped, sitting on top of
    // the half of the pad that walks.
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    await expect(page.getByTestId('vanyadum-lock')).toHaveCount(0);
  });

  test('the stick still answers a finger', async ({ page }) => {
    // The mouse path skips the on-screen stick, and it must do so by looking at
    // the EVENT's pointer type rather than at the device — otherwise a laptop
    // with a touchscreen would lose its touch controls entirely.
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    const pad = page.getByTestId('vanyadum-pad');
    await pad.dispatchEvent('pointerdown', {
      pointerId: 1,
      pointerType: 'touch',
      clientX: 70,
      clientY: 400,
      isPrimary: true,
    });
    await expect(page.locator('.dum-stick')).toBeVisible();
  });

  test('a desktop is offered the capture and asks for it when clicked', {
    tag: '@wide',
  }, async ({ page }) => {
    // Only meaningful where the primary pointer is a mouse, which at 360 px it
    // is not — this project exists for exactly that reason.
    if (isMobile(page)) test.skip();
    const locks = await spyOnPointerLock(page);
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    const prompt = page.getByTestId('vanyadum-lock');
    await expect(prompt).toBeVisible();
    await expect(prompt).toContainText('захватить мышь');
    // Says how to get back out. A capture that hides the cursor with no visible
    // way to undo it is the thing people find alarming about pointer lock.
    await expect(prompt).toContainText('Esc');

    expect(await locks()).toBe(0);
    await prompt.click();
    expect(await locks()).toBe(1);
  });

  test('and clicking anywhere on the world asks too, not only the prompt', {
    tag: '@wide',
  }, async ({ page }) => {
    // Reaching for the mouse and clicking the game is what a player does; making
    // them find a button first would be a worse version of the same thing.
    if (isMobile(page)) test.skip();
    const locks = await spyOnPointerLock(page);
    await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    await page.getByTestId('vanyadum-pad').click({ position: { x: 300, y: 300 } });
    expect(await locks()).toBe(1);
  });
});

test.describe('«ВАНЯДУМ» ending', () => {
  test('the run-over frame shows the result and the way back in', async ({ page }) => {
    const socket = await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();

    await socket.over({ success: true, secs: 42, c: { beer: 2 } });

    const over = page.getByTestId('vanyadum-over');
    await expect(over).toBeVisible();
    await expect(over).toContainText('42');
    await expect(over).toContainText('2');
    await expect(page.getByTestId('vanyadum-again')).toBeVisible();
  });

  test('and the ending screen fits a phone', async ({ page }) => {
    const socket = await stubSocket(page);
    await openSplash(page);
    await page.getByTestId('vanyadum-start').click();
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();
    await socket.over({ success: false, secs: 8, c: {} });

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });
});

test.describe('«ВАНЯДУМ» without 3D', () => {
  test('a browser with no WebGL is told, and the rest of the app still works', async ({ page }) => {
    // A REAL path — WebGL can be off by policy, by a blocklisted driver, or by a
    // phone saving power — so the player gets a sentence instead of a black
    // rectangle. Forced here by refusing the context, which is exactly what such
    // a browser does.
    await page.addInitScript(() => {
      const original = HTMLCanvasElement.prototype.getContext;
      HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, type: string, ...rest: unknown[]) {
        if (typeof type === 'string' && type.includes('webgl')) return null;
        return (original as (...a: unknown[]) => unknown).call(this, type, ...rest);
      } as typeof HTMLCanvasElement.prototype.getContext;
    });
    await openSplash(page);

    await expect(page.getByTestId('vanyadum-nogl')).toBeVisible();
    await expect(page.getByTestId('vanyadum-start')).toHaveCount(0);
  });
});
