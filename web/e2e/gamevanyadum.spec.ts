import { expect, test } from '@playwright/test';
import type { Page, WebSocketRoute } from '@playwright/test';
import { seedClient } from './fixtures';

/**
 * «ВАНЯДУМ» — the layout suite, at 360 px with `/api` stubbed in the browser.
 *
 * WHAT THIS SUITE CAN AND CANNOT SEE. The world is a `<canvas>`, and nothing
 * inside one can be asserted on without pixel comparison — so this file asserts
 * on everything that is deliberately NOT in it: the splash, the rules cheatsheet
 * generated from the catalogue, the HUD, the touch targets, the refusal a full
 * заброшка sends back, and the fact that the page never scrolls while somebody
 * is turning round.
 *
 * That division is the whole reason the view keeps its readouts and controls in
 * real DOM (ADR-046). If a future change moves the HUD onto the canvas because
 * it would look nicer, it deletes this file's ability to check any of it.
 *
 * The socket is intercepted with `page.routeWebSocket`, so the test IS the
 * server: it decides what a snapshot says, which building a ready frame names,
 * and whether the building has room.
 *
 * WHAT IS NOT ASSERTED HERE, AND WHY. Anything needing the render loop. Input is
 * emitted from `requestAnimationFrame`, a browser pauses rAF outright for a
 * backgrounded page, and with several parallel workers only one page is ever
 * visible — so such an assertion fails about one run in three for reasons that
 * have nothing to do with its claim. Those claims live in unit tests over the
 * pure functions instead.
 */

const CONFIG = {
  player: {
    radius: 0.35,
    eye_height: 1.65,
    body_height: 1.8,
    // Deliberately NOT the production numbers. If the cheatsheet were
    // hand-written rather than derived from the catalogue, these would not show
    // up on screen and the assertion below would fail.
    walk_speed: 7.5,
    max_step: 0.9,
    max_pitch: 1.5,
    max_health: 80,
    start_health: 61,
  },
  // Not production's two barrels, 0.35 s and 1.5 s either, and for the same
  // reason: the splash states the gun's rules and it must state THESE. A
  // cheatsheet with a «2» typed into it would pass against the real catalogue
  // and fail here, which is what makes the numbers wrong on purpose worth having.
  gun: {
    barrels: 3,
    fire_cooldown_seconds: 0.65,
    reload_seconds: 2.5,
    reload_cost: 2,
    ammo: 'beer',
    damage: 40,
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
  // Also deliberately none of production's numbers: the splash states the
  // building's own rules, and it must state THESE. Production's values are not
  // named here on purpose — the capacity has already moved once, and a comment
  // that quotes it is a comment that goes stale the next time it does.
  //
  // The capacity is load-bearing twice over, so it is read from here rather than
  // typed out anywhere below. It proves the cheatsheet and the refusal notice
  // are derived — a hand-written capacity would pass against production and fail
  // here, which is the point of the number being wrong on purpose. And it is
  // what «a full house» means to the layout test: seven rows of standings, more
  // than production ever shows, because a readout tuned to exactly one capacity
  // is a readout that breaks the afternoon somebody retunes it.
  //
  // The death rules are wrong on purpose for the same reason: how long you lie
  // there, how long you are untouchable afterwards, and what the standings call
  // a friendly kill are all a player has to be told, and all three are served.
  world: {
    max_occupants: 7,
    respawn_seconds: 45,
    down_seconds: 6,
    protect_seconds: 5,
    betrayals_title: 'подставы',
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

const WORLD_ID = 'world-e2e';
const WORLD = { world_id: WORLD_ID, seed: LEVEL.seed, level: LEVEL, room: 'vanyadum' };

interface StubOptions {
  /** Visits already recorded, for the splash's list. */
  visits?: { seed: number; seconds: number; beer: number; joined_at: string }[];
}

/**
 * Stubs the three reads this game has. Returns a counter for `/world`, which is
 * how the "the building was regenerated" path is observed: re-fetching is the
 * whole of the client's response to a ready frame naming a building it is not
 * standing in, and it needs no render loop to see.
 */
async function stubBackend(page: Page, opts: StubOptions = {}): Promise<() => number> {
  let worldFetches = 0;
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
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
    if (path === '/api/game-vanyadum/visits/me') return json(200, { visits: opts.visits ?? [] });
    if (path === '/api/game-vanyadum/world') {
      worldFetches += 1;
      return json(200, WORLD);
    }
    return json(404, { error: 'not_found', trace_id: 'e2e-trace-id' });
  });
  return () => worldFetches;
}

/**
 * Stands in for the simulation. Returns a handle the test uses to push frames,
 * so every assertion about the HUD is driven rather than waited for.
 */
async function stubSocket(page: Page): Promise<{
  ready: (worldID?: string, slot?: number) => Promise<void>;
  snapshot: (fields: Record<string, unknown>) => Promise<void>;
  board: (rows: Record<string, unknown>[]) => Promise<void>;
  full: () => Promise<void>;
  drop: () => void;
  revoke: () => void;
  sent: () => Promise<string[]>;
}> {
  const sent: string[] = [];
  let ws: WebSocketRoute | null = null;

  await page.routeWebSocket('**/api/realtime*', (route) => {
    // Every connection, not only the first: a client that lost its socket opens
    // another one, and the handle has to follow it there or the test would go on
    // talking to a socket nobody is listening to.
    ws = route;
    route.onMessage((message) => {
      sent.push(typeof message === 'string' ? message : '');
    });
  });

  const send = async (payload: Record<string, unknown>) => {
    // Waiting for a socket to exist is usually instant — but after a `drop` it
    // is waiting out the client's reconnect backoff, which is a fully jittered
    // draw from the first second and doubles if an attempt fails. A deadline
    // rather than a count of tries, and generous, because what varies under a
    // loaded runner is how long each step TAKES: the claim is "the client comes
    // back", not "within a second".
    await expect.poll(() => ws !== null, { timeout: 15_000 }).toBe(true);
    ws?.send(JSON.stringify(payload));
  };

  return {
    // Which building this socket was let into, and which place in it the reader
    // is holding. An id other than the one the client fetched means the заброшка
    // emptied and was regenerated while it was away — the only invalidation
    // signal this game has. The slot is the only thing that ever tells a client
    // which standings row is its own, because a snapshot names everybody except
    // its reader.
    ready: (worldID = WORLD_ID, slot = 0) =>
      send({ t: 'vanyadum_ready', world_id: worldID, slot }),
    // `pk` is a BITMASK over the index into the level's pickup list — bit i set
    // means the i-th is lying on the floor — so the resting frame is 0b11: both
    // of LEVEL's two bottles are there. Sending a list of ids here would still
    // be valid JSON and would decode as the number zero, which reads as an empty
    // floor and is exactly the kind of quiet wrongness this stub exists to
    // avoid. The mask moves in BOTH directions: things come back.
    // `b` is the shell count and is ALWAYS sent, because a resting gun is full
    // rather than empty — the resting frame here therefore carries the
    // catalogue's own barrel count, and `d`/`r` are absent, which is what a gun
    // that is not busy looks like on the wire.
    snapshot: (fields) =>
      send({
        t: 'vanyadum_snap',
        k: 1,
        ack: 1,
        x: 500,
        y: 500,
        z: 165,
        yaw: 0,
        s: 0,
        hp: 61,
        pk: 0b11,
        b: CONFIG.gun.barrels,
        ...fields,
      }),
    // The standings: who is in the BUILDING, which is a different question from
    // who is in the snapshot. Unfiltered, identical for every reader, and
    // including the reader himself — so it is the only honest source for how
    // many people are in here.
    board: (rows) => send({ t: 'vanyadum_board', b: rows }),
    // The building is at capacity. It carries no fields — the number is in the
    // catalogue, and nothing here ends, so there is no honest "try again at T".
    full: () => send({ t: 'vanyadum_full' }),
    // The link goes, and the client comes back for another one. An ordinary
    // network drop: no `bye` frame, so the client cannot know why and treats it
    // as one to retry. The handle is dropped first, so anything sent afterwards
    // waits for the NEW socket rather than being written into the dead one.
    drop: () => {
      const dying = ws;
      ws = null;
      dying?.close();
    },
    // The link goes for good. A `bye` naming a revoked session is the one close
    // the client must not retry — it settles on `terminal` and stays there,
    // which is what makes the state stable enough to measure a layout in. (A
    // plain drop is reconnected within a second, so anything on screen only
    // while the link is down is gone before it can be looked at.)
    revoke: () => {
      const dying = ws;
      ws = null;
      dying?.send(JSON.stringify({ t: 'bye', code: 4001, reason: 'unauthorized' }));
      dying?.close();
    },
    sent: async () => sent,
  };
}

/**
 * A board frame with the building as full as the served catalogue allows.
 *
 * A full-width pseudonym, an hour on the clock and a bag at its ceiling — the
 * widest row this readout can ever be asked to draw, which is what makes it the
 * layout case rather than a plausible one.
 */
function fullHouse(): Record<string, unknown>[] {
  return Array.from({ length: CONFIG.world.max_occupants }, (_, n) => ({
    n,
    i: `Wwwwwwwwww${n}m`,
    s: 3671 + n,
    c: { beer: 9 },
    // Two digits of each, which is wider than a заброшка of this size will ever
    // really produce — the layout case is the widest row that can be asked for,
    // not a plausible one.
    d: 12,
    br: 34,
  }));
}

/**
 * How much of the screen's width an overlay is drawn in, and how much it is
 * ALLOWED to take.
 *
 * The second number is the one a layout budget is made of. Both of the top
 * overlays are absolutely positioned and shrink to fit their text, so what they
 * happen to draw today says nothing about whether a longer sentence would
 * collide — `reserved` is the cap plus the inset it is pushed off its edge by,
 * which is the width that is genuinely spoken for.
 */
async function reservation(
  locator: ReturnType<Page['getByTestId']>,
  edge: 'left' | 'right',
): Promise<{ box: { x: number; width: number }; reserved: number }> {
  const box = await locator.boundingBox();
  expect(box).not.toBeNull();
  const reserved = await locator.evaluate((el, side) => {
    const style = getComputedStyle(el);
    return parseFloat(style.maxWidth) + parseFloat(style[side as 'left' | 'right']);
  }, edge);
  return { box: { x: box!.x, width: box!.width }, reserved };
}

/** Phone width, where the primary pointer is a thumb rather than a mouse. */
function isMobile(page: Page): boolean {
  const vp = page.viewportSize();
  return !!vp && vp.width <= 600;
}

async function openSplash(page: Page, opts: StubOptions = {}): Promise<() => number> {
  const worldFetches = await stubBackend(page, opts);
  await seedClient(page, 'dark');
  await page.goto('/app/game-vanyadum');
  await expect(page.getByTestId('vanyadum-root')).toBeVisible();
  return worldFetches;
}

/** Walks in. Every play test starts here, and none of them starts a run. */
async function walkIn(page: Page): Promise<void> {
  await page.getByTestId('vanyadum-enter').click();
  await expect(page.getByTestId('vanyadum-play')).toBeVisible();
}

test.describe('«ВАНЯДУМ» — the browser running this suite', () => {
  // THIS TEST IS ABOUT THE HARNESS, NOT THE GAME, and it exists because the
  // failure it names is unrecognisable without it.
  //
  // Every test below that reaches `vanyadum-enter` needs a real WebGL context:
  // the view probes for one before it builds anything, and a browser without it
  // is shown the «твой браузер не умеет 3D» screen instead of the door —
  // correctly, that is a production path (ADR-047). So when the browser loses
  // 3D, fifteen specs fail with `waiting for getByTestId('vanyadum-enter')` and
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
  test('can actually do 3D, or every test that walks in below is a lie', async ({ page }) => {
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
    await expect(page.getByTestId('vanyadum-enter')).toBeVisible();
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

  test('and it states what the game now IS: one shared building that never ends', async ({
    page,
  }) => {
    // The rules changed completely in W1a, and a cheatsheet describing the
    // previous version of a game is worse than none, because it is believed.
    // Capacity and the respawn interval are the stub's numbers, so these two
    // assertions also prove that half is derived rather than typed.
    await openSplash(page);
    const rules = page.getByTestId('vanyadum-rules');
    await expect(rules).toContainText('Больше 7 человек');
    await expect(rules).toContainText('45 с');
    await expect(rules).toContainText('Заброшка одна');
    await expect(rules).toContainText('цели нет');
    await expect(rules).toContainText('закрой вкладку');
    // And the objective it used to have is gone from the screen entirely.
    await expect(rules).not.toContainText('Собрать всё пиво');
  });

  test('and it states the обрез’s rules, every number of them derived', async ({ page }) => {
    // C1a's rules change, and the reason the splash is a gate rather than a
    // nicety: a player who does not know the gun holds three, has a cadence, and
    // spends beer to reload will empty it, stand there, and conclude the game is
    // broken. Every number here is the stub's rather than production's, so a
    // hand-typed cheatsheet fails.
    await openSplash(page);
    const rules = page.getByTestId('vanyadum-rules');
    await expect(rules).toContainText('0,65 с');
    await expect(rules).toContainText('2,5 с');
    // The ammunition is NAMED BY A JOIN — the gun publishes a counter and the
    // pickup that grants it carries the word — so this is also what says the two
    // agree.
    await expect(rules).toContainText('🍺 пиво');
  });

  test('and it states what a barrel does, and what happens when one lands', async ({ page }) => {
    // C1b's rules change, and the biggest one this game has had: the обрез
    // finally reaches people, the people it reaches are friends, and nothing
    // about killing one is worth anything. Every number is the stub's rather
    // than production's, so a hand-typed cheatsheet fails — including the WORD
    // for the standings column, which the server chooses.
    await openSplash(page);
    const rules = page.getByTestId('vanyadum-rules');
    // Damage against the player's own health, joined from two halves of the
    // catalogue.
    await expect(rules).toContainText('61 из 80');
    await expect(rules).toContainText('40');
    // How long you lie there, and how long the shield lasts — the shield's own
    // number, the stub's rather than production's.
    await expect(rules).toContainText('6 с');
    await expect(rules).toContainText('5 с');
    // And that the shield covers BOTH ways of standing on the spawn. The server
    // grants the same window to a man who has just got up and to one who has
    // just walked in, so a splash naming only the respawn sends a newcomer into
    // the building to discover a dead trigger on his own.
    await expect(rules).toContainText('когда только зашёл');
    await expect(rules).toContainText('когда встал после смерти');
    // That your friends are the targets, and that it scores nothing.
    await expect(rules).toContainText('Огонь по своим включён');
    await expect(rules).toContainText('подставы');
    // What a hit looks like — a rendering decision, so it is prose, and the only
    // thing on screen that distinguishes a shot that connected from a miss.
    await expect(rules).toContainText('красным');

    // And the claim that replaced is gone. It was true for exactly one
    // iteration, its own comment predicted this, and believed it would now be a
    // lie about the central rule of the game.
    await expect(rules).not.toContainText('нейрослопов');
    await expect(rules).not.toContainText('Пока отнять его некому');
  });

  test('and it says you cannot see everybody, and where the rest are listed', async ({ page }) => {
    // W1b's rules change. A snapshot is now cut to the room you are in and the
    // rooms through its doorways, so somebody two rooms away is genuinely not on
    // your screen — and without being told, a player reads that as the game
    // losing him. The standings are the answer, so the screen has to name them.
    await openSplash(page);
    const rules = page.getByTestId('vanyadum-rules');
    await expect(rules).toContainText('в соседней через проём');
    await expect(rules).toContainText('табло');
    await expect(rules).toContainText('даже те, кого не видно');
    // And the claim that replaced is gone: it was true for exactly one iteration.
    await expect(rules).not.toContainText('кто зашёл, того и видно');
  });

  test('the controls are explained, because nothing else says how to move', async ({ page }) => {
    await openSplash(page);
    const rules = page.getByTestId('vanyadum-rules');
    await expect(rules).toContainText('слева');
    await expect(rules).toContainText('справа');
    await expect(rules).toContainText('WASD');
  });

  test('previous visits are listed, so persistence is visible without a database', async ({
    page,
  }) => {
    await openSplash(page, {
      visits: [{ seed: 4242, seconds: 91, beer: 3, joined_at: '2026-07-28T10:00:00Z' }],
    });
    const list = page.getByTestId('vanyadum-visits');
    await expect(list).toBeVisible();
    await expect(list).toContainText('91 с');
    await expect(list).toContainText('3');
    // No verdict on a visit: nothing was won or lost, so there is no ✅ or 💀.
    await expect(list).not.toContainText('✅');
    await expect(list).not.toContainText('💀');
  });

  test('the list is absent rather than empty when there is nothing in it', async ({ page }) => {
    await openSplash(page);
    await expect(page.getByTestId('vanyadum-visits')).toHaveCount(0);
  });

  test('the door is a real tap target', async ({ page }) => {
    await openSplash(page);
    const box = await page.getByTestId('vanyadum-enter').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  });

  test('nothing overflows a 360 px phone', async ({ page }) => {
    await openSplash(page, {
      visits: [{ seed: 4242, seconds: 12, beer: 0, joined_at: '2026-07-28T10:00:00Z' }],
    });
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('nothing is entered just by opening the page', async ({ page }) => {
    // A hello creates an occupant and therefore, eventually, a recorded visit —
    // so it may only be sent once somebody has said he wants to be inside.
    // Walking a player in because he opened the page would write a visit for
    // reading the rules.
    const socket = await stubSocket(page);
    const worldFetches = await openSplash(page);
    await expect(page.getByTestId('vanyadum-rules')).toBeVisible();
    expect(await socket.sent()).toHaveLength(0);
    expect(worldFetches()).toBe(0);
  });
});

test.describe('«ВАНЯДУМ» play', () => {
  test('walking in replaces the splash with the world and a real HUD', async ({ page }) => {
    await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await expect(page.getByTestId('vanyadum-canvas')).toBeVisible();
    // The HUD is DOM and its numbers are readable as text — which is the whole
    // reason it is not painted into the canvas.
    await expect(page.getByTestId('vanyadum-hud')).toContainText('61');
    await expect(page.getByTestId('vanyadum-count-beer')).toBeVisible();
  });

  test('movement is predicted, so the camera does not wait for a snapshot', async ({ page }) => {
    // The complaint the netcode change exists to fix: iteration 1 only moved the
    // camera when a snapshot landed, so walking looked like twenty frames a
    // second. Asserted through the DOM rather than the canvas — the client sends
    // input the instant a key is held, and it does so without ever having been
    // told where it is.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

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
    await walkIn(page);

    // One bit set: the bottle at index 0 is lying there, the other was taken.
    await socket.snapshot({ hp: 37, c: { beer: 2 }, pk: 0b01 });

    await expect(page.getByTestId('vanyadum-hud')).toContainText('37');
    await expect(page.getByTestId('vanyadum-count-beer')).toContainText('2');
    await expect(page.getByTestId('vanyadum-floor')).toContainText('на полу 1');
  });

  test('a bottle that comes back is shown as back, not only as taken', async ({ page }) => {
    // The mask now moves in BOTH directions, and this is the DOM-visible half of
    // that: the mesh going back into the scene cannot be asserted on, but the
    // readout driven by the same number can. A client that only ever removed
    // things would sit at «на полу 1» for the rest of the session.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.snapshot({ pk: 0b11 });
    await expect(page.getByTestId('vanyadum-floor')).toContainText('на полу 2');
    await socket.snapshot({ pk: 0b01 });
    await expect(page.getByTestId('vanyadum-floor')).toContainText('на полу 1');
    // Thirty seconds later, on the server's clock.
    await socket.snapshot({ pk: 0b11 });
    await expect(page.getByTestId('vanyadum-floor')).toContainText('на полу 2');
  });

  test('the standings say who is in the building, and what they have', async ({ page }) => {
    // Real DOM and not the canvas (ADR-047), which is the whole reason any of it
    // can be asserted on. Its numbers are the board frame's, and its icons come
    // from the catalogue exactly as the HUD's do.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.board([
      { n: 0, i: 'K3jf9sLm2QpZ', s: 137, c: { beer: 4 } },
      { n: 1, i: 'Qq1Ww2Ee3Rr4', s: 7 },
    ]);

    const board = page.getByTestId('vanyadum-board');
    await expect(board).toBeVisible();
    await expect(board).toContainText('K3jf9sLm2QpZ');
    await expect(board).toContainText('2:17');
    await expect(board).toContainText('🍺4');
    // Somebody carrying nothing is a zero rather than a blank, so the columns
    // stay aligned down the list.
    await expect(board).toContainText('0:07');
    await expect(board).toContainText('🍺0');
  });

  test('the count is of the building, not of what you can see', async ({ page }) => {
    // THE CORRECTION W1b FORCED. The occupancy used to be the peer array's
    // length plus one, and the peer array is now FILTERED to the room you are in
    // and the rooms through its doorways — so counting it would report how many
    // are visible and tick up and down as somebody walked through a door. The
    // standings are unfiltered by design, and they are the only honest source.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    // Three in the building, and exactly one of them close enough to be drawn.
    await socket.board([
      { n: 0, i: 'K3jf9sLm2QpZ', s: 10 },
      { n: 1, i: 'Qq1Ww2Ee3Rr4', s: 20 },
      { n: 2, i: 'Zz9Xx8Cc7Vv6', s: 30 },
    ]);
    await socket.snapshot({ p: [{ n: 1, x: 700, y: 500, s: 0, yaw: 0 }] });

    const board = page.getByTestId('vanyadum-board');
    await expect(board).toContainText('👥 3');
    await expect(page.getByTestId('vanyadum-board-row')).toHaveCount(3);
    // Including the man nobody can see, named in full.
    await expect(board).toContainText('Zz9Xx8Cc7Vv6');
  });

  test('you can find yourself in a list that names everybody', async ({ page }) => {
    // A snapshot names everybody EXCEPT its own reader, so the ready frame's
    // slot is the only thing that ever says which row is ours. Marked by an
    // arrow in the text rather than by colour alone — a readout told apart by
    // being a slightly different grey is one nobody can read.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.ready(WORLD_ID, 1);
    await socket.board([
      { n: 0, i: 'K3jf9sLm2QpZ', s: 10 },
      { n: 1, i: 'Qq1Ww2Ee3Rr4', s: 20 },
    ]);

    const rows = page.getByTestId('vanyadum-board-row');
    await expect(rows.nth(1)).toContainText('▸');
    await expect(rows.nth(0)).not.toContainText('▸');
  });

  test('a peer the standings have not named yet is still drawn, not dropped', async ({ page }) => {
    // The one ordering hazard: the server publishes the board ahead of the
    // snapshot that first carries a new holder, so this only happens when that
    // board frame was dropped for a slow consumer. Being unlabelled for a second
    // is better than being invisible, and both are better than being given
    // somebody else's name.
    //
    // The figure itself is inside the canvas and cannot be looked at — what is
    // observable here is that the client did not fall over and the readout did
    // not invent a row for him.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.snapshot({ hp: 42, p: [{ n: 3, x: 700, y: 500, s: 0, yaw: 0 }] });
    await expect(page.getByTestId('vanyadum-hud')).toContainText('42');
    // Full state, so it is short by a row rather than carrying an invented one.
    await expect(page.getByTestId('vanyadum-board')).toHaveCount(0);

    // And the board catching up a moment later is what puts him on the list.
    await socket.board([{ n: 3, i: 'K3jf9sLm2QpZ', s: 1 }]);
    await expect(page.getByTestId('vanyadum-board')).toContainText('K3jf9sLm2QpZ');
  });

  test('a socket that came back does not bring the old standings with it', async ({ page }) => {
    // THE OUTAGE IS THE ONE WINDOW IN WHICH THE DIRECTORY CAN GO WRONG QUIETLY.
    // A slot is a place, not a person: it is handed back when its holder leaves
    // and given to the next arrival, and the only thing that ever announces that
    // is a standings frame — which is exactly what a dead socket does not carry.
    // So a client that kept its board across a reconnect can put the man who
    // LEFT at the top of a list, with the newcomer's time in the building beside
    // his name.
    //
    // Held out of the canvas on purpose (ADR-047), which is the only reason a
    // test can read a mislabelled human being at all.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.ready(WORLD_ID, 1);
    await socket.board([
      { n: 0, i: 'K3jf9sLm2QpZ', s: 40 },
      { n: 1, i: 'Qq1Ww2Ee3Rr4', s: 30 },
    ]);
    const rows = page.getByTestId('vanyadum-board-row');
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(1)).toContainText('▸');

    // The link goes. Everything on that list was true of a moment nothing is
    // keeping true any more, so none of it survives.
    socket.drop();
    await expect(page.getByTestId('vanyadum-board')).toHaveCount(0);

    // It comes back, and slot 1 changed hands while it was away — our own place
    // among them, because an occupant only outlives his socket for the grace
    // period. The readout is whatever the newest frame says and nothing else.
    await socket.board([{ n: 1, i: 'Zz9Xx8Cc7Vv6', s: 3 }]);
    await expect(rows).toHaveCount(1);
    const board = page.getByTestId('vanyadum-board');
    await expect(board).toContainText('Zz9Xx8Cc7Vv6');
    await expect(board).not.toContainText('Qq1Ww2Ee3Rr4');
    await expect(board).not.toContainText('K3jf9sLm2QpZ');
    // And no arrow anywhere: which place is ours is a ready frame's answer, and
    // the one we held before the outage is the one Zz9Xx8Cc7Vv6 is standing in.
    await expect(rows.nth(0)).not.toContainText('▸');

    // The ready frame the reconnect earns is what says where we are now.
    await socket.ready(WORLD_ID, 4);
    await socket.board([
      { n: 1, i: 'Zz9Xx8Cc7Vv6', s: 4 },
      { n: 4, i: 'Mm5Nn6Bb7Vv8', s: 1 },
    ]);
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).not.toContainText('▸');
    await expect(rows.nth(1)).toContainText('▸');
  });

  test('a full house of standings fits a 360 px phone', async ({ page }) => {
    // A building's worth of twelve-character pseudonyms, each with a clock and a
    // bag, on top of a game. The name column is what gives — it ellipsises
    // rather than pushing the numbers off the side.
    //
    // The house is as full as the CATALOGUE says, not as full as production is:
    // the stub deliberately serves a capacity nobody runs, and a layout that
    // only fits five is a layout that breaks the day the number moves.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.board(fullHouse());
    await expect(page.getByTestId('vanyadum-board-row')).toHaveCount(CONFIG.world.max_occupants);

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);

    // And it stays inside the viewport rather than merely failing to scroll it.
    const viewport = page.viewportSize()!;
    const box = await page.getByTestId('vanyadum-board').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width);
  });

  test('the standings and the connection notice cannot reach each other', async ({ page }) => {
    // TWO OVERLAYS, ONE STRIP OF SCREEN. The standings hug the right edge and
    // the notice hugs the left, at nearly the same height, so their widths are
    // one sum rather than two independent choices — and chosen by eye they came
    // to 358.4 px of a 360 px phone, correct by a pixel and a half and by
    // accident. The link's cap is now derived from what the board reserves, and
    // this is what says so.
    //
    // MEASURED IN THE TWO STATES THEY ACTUALLY OCCUR IN, because they no longer
    // occur together: the link leaving `open` empties the standings, since a
    // directory nothing is refreshing stops being true. The budget still has to
    // hold — the notice appears when something has already gone wrong, which is
    // the last moment to depend on the other overlay being absent — so the
    // boxes are taken one at a time and checked against each other.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.board(fullHouse());
    await expect(page.getByTestId('vanyadum-board-row')).toHaveCount(CONFIG.world.max_occupants);
    const board = await reservation(page.getByTestId('vanyadum-board'), 'right');

    // A close the client will not retry, so the notice stays up long enough to
    // be measured. An ordinary drop is reconnected inside a second.
    socket.revoke();
    const notice = page.getByTestId('vanyadum-link');
    await expect(notice).toBeVisible();
    const link = await reservation(notice, 'left');

    // Neither runs off the phone as drawn...
    const viewport = page.viewportSize()!;
    expect(link.box.x).toBeGreaterThanOrEqual(0);
    expect(link.box.x + link.box.width).toBeLessThanOrEqual(viewport.width);
    expect(board.box.x).toBeGreaterThanOrEqual(0);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);

    // ...and the notice ends before the standings begin, as drawn.
    expect(board.box.x).toBeGreaterThanOrEqual(link.box.x + link.box.width);

    // THE ASSERTION THAT MATTERS, and the one the drawn boxes cannot make. Both
    // overlays are shrink-to-fit, so today's short Russian sentence is narrower
    // than its cap and would clear the standings even with a budget that does
    // not add up. What has to hold is the CAP: the widest either is ALLOWED to
    // become, plus its inset, plus the other's, has to leave the phone with room
    // to spare — otherwise a longer message, a bigger font or a wider name
    // silently reintroduces the collision. Chosen by eye these summed to 358.4
    // of 360 px; the link's cap is now derived from what the board reserves.
    const spare = viewport.width - (board.reserved + link.reserved);
    expect(spare).toBeGreaterThanOrEqual(8);
  });

  test('the standings never swallow a tap meant for the game', async ({ page }) => {
    // It is a readout sitting on top of the play surface. A touch that lands on
    // it belongs to whatever is underneath — the pad that turns you round.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
    await socket.board([{ n: 0, i: 'K3jf9sLm2QpZ', s: 5 }]);

    const events = await page
      .getByTestId('vanyadum-board')
      .evaluate((el) => getComputedStyle(el).pointerEvents);
    expect(events).toBe('none');
  });

  test('the client says hello the moment the socket opens', async ({ page }) => {
    // The hello IS the join: there is no start endpoint, so this frame is the
    // only door. It needs no render loop, which is why it can be asserted here
    // when the input frame's shape cannot — that claim lives in a unit test over
    // buildInputFrame instead.
    //
    // It goes out on every OPEN, including reconnects: an occupant outlives a
    // dropped socket for the length of the grace period, so a returning client
    // has to say hello again to keep the place it already had.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

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
    await walkIn(page);

    await page.waitForTimeout(700);
    const inputs = (await socket.sent()).filter((m) => m.includes('vanyadum_input'));
    expect(inputs).toHaveLength(0);
  });

  test('the stick appears where the thumb lands, not where a circle is painted', async ({
    page,
  }) => {
    await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
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

  test('the fire control clears 44 px and sits inside the screen', async ({ page }) => {
    // It finally does something, which makes its reachability a rule rather than
    // a formality: a trigger a thumb cannot land on squarely is a gun that eats
    // shots. «сдаться» went with the runs it used to end — leaving is leaving
    // the page.
    await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    const viewport = page.viewportSize()!;
    const box = await page.getByTestId('vanyadum-fire').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.width).toBeGreaterThanOrEqual(44);
    expect(box!.height).toBeGreaterThanOrEqual(44);
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width);
  });

  test('the mute is reachable too, and does not overlap the trigger', async ({ page }) => {
    // The only sound this game makes is a shotgun, so the person who wants it
    // off wants it off NOW — which means on the play surface and not behind a
    // settings screen. Two round controls side by side at the bottom of a 360 px
    // phone is exactly the arrangement that goes wrong by a few pixels, so the
    // gap between them is measured rather than eyeballed.
    await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    const mute = page.getByTestId('vanyadum-mute');
    await expect(mute).toBeVisible();
    const box = (await mute.boundingBox())!;
    const fire = (await page.getByTestId('vanyadum-fire').boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(44);
    expect(box.height).toBeGreaterThanOrEqual(44);
    expect(box.x).toBeGreaterThanOrEqual(0);
    // Left of the trigger with daylight between them, so neither is pressed by
    // accident by a thumb aiming at the other.
    expect(box.x + box.width).toBeLessThanOrEqual(fire.x - 8);
  });

  test('the mute says which state it is in, in the DOM', async ({ page }) => {
    // Real DOM like every other control (ADR-047), which is what makes it
    // assertable at all — and `aria-pressed` is what makes it answerable to a
    // screen reader rather than only to somebody who can see an emoji change.
    await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    const mute = page.getByTestId('vanyadum-mute');
    await expect(mute).toHaveAttribute('aria-pressed', 'false');
    await mute.click();
    await expect(mute).toHaveAttribute('aria-pressed', 'true');
    await mute.click();
    await expect(mute).toHaveAttribute('aria-pressed', 'false');
  });

  test('the shell count is the server’s, and it is real text', async ({ page }) => {
    // THE HALF OF THE GUN THIS SUITE CAN SEE. The muzzle flash and the kick are
    // inside the canvas and cannot be asserted on without pixel comparison —
    // their claims live in the unit tests over the predictor. What is DOM is the
    // number a player reads, and it is the snapshot's rather than the
    // prediction's precisely so that it can be trusted.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    // A full gun, from the catalogue's own barrel count.
    await socket.snapshot({});
    await expect(page.getByTestId('vanyadum-shells')).toContainText(
      `${CONFIG.gun.barrels}/${CONFIG.gun.barrels}`,
    );

    // One barrel gone and the cadence running.
    await socket.snapshot({ b: CONFIG.gun.barrels - 1, d: 350 });
    await expect(page.getByTestId('vanyadum-shells')).toContainText(
      `${CONFIG.gun.barrels - 1}/${CONFIG.gun.barrels}`,
    );

    // Empty, and reloading — `r` and `d` are never both set, and a reload is the
    // one state where the count is not the useful thing to show.
    await socket.snapshot({ b: 0, r: 2500 });
    await expect(page.getByTestId('vanyadum-shells')).toContainText('жду');

    // And back, which no event announces: the snapshot simply says so again.
    await socket.snapshot({ b: CONFIG.gun.barrels });
    await expect(page.getByTestId('vanyadum-shells')).toContainText(
      `${CONFIG.gun.barrels}/${CONFIG.gun.barrels}`,
    );
  });

  test('being shot is marked on the readout that changed, and only for a moment', async ({
    page,
  }) => {
    // DERIVED RATHER THAN SENT: `hp` falling between two frames IS the hit, so
    // nothing on the wire says one happened. The mark is on the health cell
    // because that is where being shot is legible, and it is over well inside
    // the half second this project caps an acknowledgement at — a mark big
    // enough to watch takes the eye off whoever is still shooting at you.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    const hp = page.getByTestId('vanyadum-health');
    await socket.snapshot({ hp: 61 });
    await expect(hp).not.toHaveClass(/is-hurt/);

    await socket.snapshot({ hp: 21 });
    await expect(hp).toHaveClass(/is-hurt/);
    await expect(hp).toContainText('21');
    // Cleared on a timer rather than on `animationend`, which never fires when
    // the animation is switched off — so this holds under reduced motion too.
    await expect(hp).not.toHaveClass(/is-hurt/, { timeout: 2000 });

    // Coming back is a RISE, and a rise marks nothing: a respawn is not a hit.
    await socket.snapshot({ hp: 80 });
    await expect(hp).toContainText('80');
    await expect(hp).not.toHaveClass(/is-hurt/);
  });

  test('being killed says so in DOM, with the countdown that ends it', async ({ page }) => {
    // Real DOM over the canvas (ADR-047), which is the only reason either half
    // of this can be read at all. A player who is told neither that he is down
    // nor that it ends by itself reads several seconds of refused input as the
    // game having crashed.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    // Alive: nothing on screen.
    await socket.snapshot({ hp: 61 });
    await expect(page.getByTestId('vanyadum-down')).toHaveCount(0);

    // Zero health, and the milliseconds until he gets up.
    await socket.snapshot({ hp: 0, dn: 2400 });
    const card = page.getByTestId('vanyadum-down');
    await expect(card).toBeVisible();
    await expect(card).toContainText('ТЕБЯ ПОЛОЖИЛИ');
    // Rounded UP, so it never reads zero while it is still running.
    await expect(card).toContainText('3');
    await socket.snapshot({ hp: 0, dn: 1200 });
    await expect(card).toContainText('2');

    // Up again — the server simply stops sending the countdown.
    await socket.snapshot({ hp: 80 });
    await expect(page.getByTestId('vanyadum-down')).toHaveCount(0);
  });

  test('the death card never swallows the gesture that looks around', async ({ page }) => {
    // The camera still turns while you are down, so you can watch whoever did it
    // walk away — which a card sitting on the play surface would take away.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
    await socket.snapshot({ hp: 0, dn: 3000 });

    const events = await page
      .getByTestId('vanyadum-down')
      .evaluate((el) => getComputedStyle(el).pointerEvents);
    expect(events).toBe('none');
  });

  test('spawn protection is shown for the whole time it lasts, not flashed', async ({ page }) => {
    // A BUFF IS A PROPERTY, NOT AN EVENT. A mark that appears once says nothing
    // about the seconds that follow — and this one has a second half a player
    // would otherwise discover by pulling a trigger that does nothing, which
    // reads as the game being broken rather than as a rule.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.snapshot({ hp: 80 });
    await expect(page.getByTestId('vanyadum-protect')).toHaveCount(0);

    await socket.snapshot({ hp: 80, pr: 1400 });
    const badge = page.getByTestId('vanyadum-protect');
    await expect(badge).toBeVisible();
    await expect(badge).toContainText('1,4');
    // Both halves of the rule, because only one of them is discoverable.
    await expect(badge).toContainText('не убить');
    await expect(badge).toContainText('не стреляешь');

    await socket.snapshot({ hp: 80, pr: 300 });
    await expect(badge).toContainText('0,3');
    await socket.snapshot({ hp: 80 });
    await expect(page.getByTestId('vanyadum-protect')).toHaveCount(0);
  });

  test('neither overlay runs off a 360 px phone', async ({ page }) => {
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
    const viewport = page.viewportSize()!;

    await socket.snapshot({ hp: 0, dn: 6000 });
    const card = (await page.getByTestId('vanyadum-down').boundingBox())!;
    expect(card.x).toBeGreaterThanOrEqual(0);
    expect(card.x + card.width).toBeLessThanOrEqual(viewport.width);

    await socket.snapshot({ hp: 80, pr: 5000 });
    const badge = (await page.getByTestId('vanyadum-protect').boundingBox())!;
    expect(badge.x).toBeGreaterThanOrEqual(0);
    expect(badge.x + badge.width).toBeLessThanOrEqual(viewport.width);
    // And clear of the two controls at the bottom, so neither is covered by a
    // notice that appears while somebody is trying to press one.
    const fire = (await page.getByTestId('vanyadum-fire').boundingBox())!;
    const mute = (await page.getByTestId('vanyadum-mute').boundingBox())!;
    expect(badge.y + badge.height).toBeLessThanOrEqual(fire.y);
    expect(badge.y + badge.height).toBeLessThanOrEqual(mute.y);

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('the standings count deaths and betrayals, and nothing else', async ({ page }) => {
    // THERE IS NO KILL COLUMN, and that is the joke rather than an omission:
    // friendly fire is on and everybody in the building is a friend, so every
    // kill is the second number. Both are omitted at zero on the wire and drawn
    // as a zero here, so the columns stay aligned down the list.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.board([
      { n: 0, i: 'K3jf9sLm2QpZ', s: 137, c: { beer: 4 }, d: 2, br: 3 },
      { n: 1, i: 'Qq1Ww2Ee3Rr4', s: 7 },
    ]);

    const rows = page.getByTestId('vanyadum-board-row');
    await expect(rows.nth(0)).toContainText('💀2');
    await expect(rows.nth(0)).toContainText('🔪3');
    await expect(rows.nth(1)).toContainText('💀0');
    await expect(rows.nth(1)).toContainText('🔪0');
  });

  test('holding the trigger reaches the server', async ({ page }) => {
    // The end-to-end claim this suite CAN make about firing: a thumb on the
    // button produces an input frame carrying the trigger. Deliberately never
    // sends a snapshot first — a client that could not predict its own gun would
    // have nothing to say until one arrived.
    //
    // Generous, because the frame is built inside a requestAnimationFrame loop
    // and the whole suite runs in parallel on a machine that throttles the
    // frames it depends on. The claim is "a pull is eventually sent", not
    // "within four seconds".
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    const fire = page.getByTestId('vanyadum-fire');
    await fire.dispatchEvent('pointerdown', { pointerId: 3, isPrimary: true });
    await expect
      .poll(
        async () =>
          (await socket.sent()).filter((m) => m.includes('vanyadum_input') && m.includes('"f":true'))
            .length,
        { timeout: 15_000 },
      )
      .toBeGreaterThan(0);
    await fire.dispatchEvent('pointerup', { pointerId: 3, isPrimary: true });
  });

  test('and letting go stops it, without holding it down forever', async ({ page }) => {
    // A trigger left stuck down is a gun that empties itself and a socket that
    // never goes quiet. Measured by what stops arriving, so it needs the release
    // to have actually been heard rather than merely dispatched.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    const fire = page.getByTestId('vanyadum-fire');
    await fire.dispatchEvent('pointerdown', { pointerId: 4, isPrimary: true });
    await expect
      .poll(async () => (await socket.sent()).filter((m) => m.includes('"f":true')).length, {
        timeout: 15_000,
      })
      .toBeGreaterThan(0);
    await fire.dispatchEvent('pointerup', { pointerId: 4, isPrimary: true });

    // Everything already in flight has landed by now; nothing new may.
    await page.waitForTimeout(500);
    const settled = (await socket.sent()).filter((m) => m.includes('"f":true')).length;
    await page.waitForTimeout(700);
    expect((await socket.sent()).filter((m) => m.includes('"f":true')).length).toBe(settled);
  });

  test('the play surface swallows gestures, so a firefight cannot scroll the page', async ({
    page,
  }) => {
    // `touch-action: none` is the difference between turning round and
    // pull-to-refresh reloading the game mid-visit.
    await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
    const touchAction = await page
      .getByTestId('vanyadum-pad')
      .evaluate((el) => getComputedStyle(el).touchAction);
    expect(touchAction).toBe('none');
  });
});

test.describe('«ВАНЯДУМ» when the заброшка will not have you', () => {
  test('a full building says so in Russian rather than doing nothing', async ({ page }) => {
    // A hello the server parsed perfectly and cannot honour gets an answer, and
    // the player has to be able to read it. Silence would be indistinguishable
    // from the game being broken.
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);

    await socket.full();

    const notice = page.getByTestId('vanyadum-full');
    await expect(notice).toBeVisible();
    await expect(notice).toContainText('ЗАБРОШКА ПОЛНА');
    // The capacity comes from the catalogue, like every other number here.
    await expect(notice).toContainText('7');
    // Back on the splash, where the door is also the retry — a second button
    // saying "try again" would be a second path to one outcome.
    await expect(page.getByTestId('vanyadum-splash')).toBeVisible();
    await expect(page.getByTestId('vanyadum-enter')).toBeVisible();
  });

  test('and the refusal fits a phone without pushing anything off the side', async ({ page }) => {
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
    await socket.full();
    await expect(page.getByTestId('vanyadum-full')).toBeVisible();

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('pressing the door again is the retry, and it clears the notice', async ({ page }) => {
    const socket = await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
    await socket.full();
    await expect(page.getByTestId('vanyadum-full')).toBeVisible();

    await walkIn(page);
    await expect(page.getByTestId('vanyadum-full')).toHaveCount(0);
  });
});

test.describe('«ВАНЯДУМ» when the building has been regenerated', () => {
  test('a ready frame naming another building makes the client re-fetch it', async ({ page }) => {
    // The заброшка is torn down when the last person leaves, so a client that
    // was away long enough can come back holding geometry nobody is standing in.
    // The world id on the ready frame is the ONLY signal that says so, and
    // re-fetching is the whole of the response. Counted rather than looked at:
    // the meshes are inside the canvas, but the HTTP call is not.
    const socket = await stubSocket(page);
    const worldFetches = await openSplash(page);
    await walkIn(page);
    await expect.poll(worldFetches).toBe(1);

    // Same building — nothing to do. A reconnect into it is answered with a
    // ready frame and nothing else, and it carries back the SAME place.
    await socket.ready();
    await socket.board([{ n: 0, i: 'K3jf9sLm2QpZ', s: 12 }]);
    await expect(page.getByTestId('vanyadum-board-row')).toContainText('▸');
    expect(worldFetches()).toBe(1);

    // A different one. The level, the walls the predictor collides against and
    // the interpolation buffer all describe a building that no longer exists.
    await socket.ready('world-regenerated');
    await expect.poll(worldFetches).toBe(2);
    // And the player is still inside, not thrown back to the splash.
    await expect(page.getByTestId('vanyadum-play')).toBeVisible();
    // The standings went with the building they described: everybody on that
    // list was standing in a заброшка that no longer exists.
    await expect(page.getByTestId('vanyadum-board')).toHaveCount(0);
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
    await walkIn(page);

    await expect(page.getByTestId('vanyadum-lock')).toHaveCount(0);
  });

  test('the stick still answers a finger', async ({ page }) => {
    // The mouse path skips the on-screen stick, and it must do so by looking at
    // the EVENT's pointer type rather than at the device — otherwise a laptop
    // with a touchscreen would lose its touch controls entirely.
    await stubSocket(page);
    await openSplash(page);
    await walkIn(page);
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
    await walkIn(page);

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
    await walkIn(page);

    await page.getByTestId('vanyadum-pad').click({ position: { x: 300, y: 300 } });
    expect(await locks()).toBe(1);
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
    await expect(page.getByTestId('vanyadum-enter')).toHaveCount(0);
  });
});
