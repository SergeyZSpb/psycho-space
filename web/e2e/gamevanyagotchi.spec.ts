import { expect, test, type Locator, type Page } from '@playwright/test';

// «Ванягоччи» — the shared plane, at phone widths, with the socket faked.
//
// Everything is stubbed inside this file rather than in e2e/fixtures.ts, and the
// helpers below are copies rather than imports, so the game keeps its own
// fixtures and this spec stands alone. Deleting this game means deleting its
// files and nothing else (ARCHITECTURE ADR-028) — which is only true if no other
// spec has to be edited on the way out.
//
// The socket is intercepted with page.routeWebSocket, so the test *is* the
// server: it sends roster frames and reads the moves the client sends back.
// Nothing here talks to Go.

/** Mirrored from internal/gamevanyagotchi/message.go. Duplicated on purpose: a
 *  wire-format change should fail this test rather than silently follow along. */
const TYPE_ROSTER = 'vanyagotchi_roster';
const TYPE_MOVE = 'vanyagotchi_move';
const TYPE_HELLO = 'vanyagotchi_hello';
const TYPE_YOU = 'vanyagotchi_you';

/** Mirrored from src/lib/vanyagotchiPlane.ts, for the same reason. */
const X_PROPERTY = '--x';
const Y_PROPERTY = '--y';

/**
 * The longest name a dot will show, in code points. Mirrored from
 * src/lib/vanyagotchiPlane.ts, for the same reason as everything else here: a
 * cap that quietly moved should fail this test rather than be followed.
 */
const LABEL_MAX = 16;

/**
 * One entry of a roster frame.
 *
 * A frame says two kinds of thing about an entity, and the split is the whole
 * design: WHERE it stands changes five times a second and is written straight to
 * CSS, WHAT it looks like changes a few times an hour and goes through Vue.
 *
 * `art`, `label` and `pose` are optional here rather than required because they
 * are optional on the WIRE too — `label` is omitted entirely for a Ваня who has
 * no name, and a server halfway through a deploy may send none of the three.
 * Every one of those has to draw somebody.
 */
interface Peer {
  id: string;
  x: number;
  y: number;
  /** A catalogue skin key, never a picture — see the appearance suite below. */
  art?: string;
  /** The pet's name. Left out, never empty, when it has none. */
  label?: string;
  /** 'fine' | 'poorly' | 'dead', as the SERVER decided it. */
  pose?: string;
}

/**
 * A roster frame.
 *
 * `JSON.stringify` drops an undefined property rather than writing `null`, which
 * is exactly what the Go `omitempty` on `Label` does — so a peer built without a
 * name here reaches the client in the same shape a nameless one really does.
 */
function roster(...peers: Peer[]): string {
  return JSON.stringify({ t: TYPE_ROSTER, peers });
}

/** Everything the socket handler hands back to the test. */
interface SocketHarness {
  /** Frames the page sent us, parsed. */
  sent: Record<string, unknown>[];
  /** Pushes a frame to the page. Resolves once a socket exists to push it to. */
  push: (payload: string) => Promise<void>;
  /** Drops the connection, optionally saying why first. */
  drop: (bye?: { code: number; reason: string }) => Promise<void>;
  /** How many times the page has opened a socket — reconnects included. */
  connections: () => number;
  /**
   * Refuse every further connection until released, so the client genuinely
   * stays down. Without this a reconnect can complete inside a single Vue flush
   * and the disconnected state never reaches the DOM at all — which is lovely in
   * production and untestable in a browser.
   */
  holdDown: (down: boolean) => void;
  /** Waits until the page has opened at least n sockets. */
  waitForConnections: (n: number) => Promise<void>;
}

/**
 * Intercepts the WebSocket. Must be installed before `goto`, and the pattern
 * needs the trailing `*` — the client appends `?room=yard`, and a glob without
 * it does not match a query string.
 */
async function stubSocket(page: Page): Promise<SocketHarness> {
  const sent: Record<string, unknown>[] = [];
  let ws: { send: (m: string) => void; close: (o?: { code?: number }) => void } | undefined;
  let count = 0;
  let resolveReady: () => void;
  let ready = new Promise<void>((r) => {
    resolveReady = r;
  });

  let down = false;
  await page.routeWebSocket('**/api/realtime*', (route) => {
    count += 1;
    if (down) {
      route.close({ code: 1006 });
      return;
    }
    ws = route;
    route.onMessage((message) => {
      const text = typeof message === 'string' ? message : message.toString();
      try {
        sent.push(JSON.parse(text));
      } catch {
        sent.push({ unparsed: text });
      }
    });
    resolveReady();
  });

  return {
    sent,
    connections: () => count,
    holdDown(value: boolean) {
      down = value;
    },
    async waitForConnections(n: number) {
      const deadline = Date.now() + 15_000;
      while (count < n) {
        if (Date.now() > deadline) throw new Error(`only ${count} of ${n} connections opened`);
        await new Promise((r) => setTimeout(r, 50));
      }
    },
    async push(payload: string) {
      await ready;
      ws?.send(payload);
    },
    async drop(bye) {
      await ready;
      if (bye) ws?.send(JSON.stringify({ t: 'bye', ...bye }));
      const dying = ws;
      // The next connection gets its own readiness gate, so a push after a
      // reconnect waits for the NEW socket rather than racing the dead one.
      ready = new Promise<void>((r) => {
        resolveReady = r;
      });
      ws = undefined;
      dying?.close({ code: 1006 });
    },
  };
}

/** Stubs the HTTP the app shell needs to let an approved user through. */
async function stubBackend(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      localStorage.setItem('ps-theme', 'dark');
      localStorage.setItem('ps-cookie-consent', '1');
    } catch {
      /* ignore */
    }
  });
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const json = (body: unknown) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        headers: { 'X-Trace-Id': 'e2e-trace-id' },
        body: JSON.stringify(body),
      });
    if (path === '/api/auth/me') {
      return json({
        account: {
          id: 'acc-1',
          display_name: 'Тест Пользователь',
          avatar_url: '',
          role: 'user',
          status: 'approved',
        },
      });
    }
    return json({});
  });
}

/** Copied, not imported — see the header. */
async function expectNoOverflow(page: Page, label: string): Promise<void> {
  const diff = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(diff, `horizontal overflow on "${label}": ${diff}px`).toBeLessThanOrEqual(0);
}

/** The play screen must never scroll vertically either — that is the layout rule. */
async function expectNoVerticalScroll(page: Page, label: string): Promise<void> {
  const diff = await page.evaluate(
    () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
  );
  expect(diff, `vertical scroll on "${label}": ${diff}px`).toBeLessThanOrEqual(1);
}

function isMobile(page: Page): boolean {
  const vp = page.viewportSize();
  return !!vp && vp.width <= 600;
}

async function expectTapTarget(loc: Locator, label: string): Promise<void> {
  await expect(loc, `${label} should be visible`).toBeVisible();
  const box = await loc.boundingBox();
  expect(box, `${label} has no bounding box`).not.toBeNull();
  if (box) {
    const min = Math.round(Math.min(box.width, box.height));
    expect(
      min,
      `${label} tap target too small: ${Math.round(box.width)}x${Math.round(box.height)}`,
    ).toBeGreaterThanOrEqual(44);
  }
}

/** Reads a dot's resolved custom properties — the only place a position lives. */
async function peerPosition(page: Page, id: string): Promise<{ x: string; y: string }> {
  return page.evaluate(
    ([peerId, xProp, yProp]) => {
      const el = document.querySelector<HTMLElement>(`[data-peer="${peerId}"]`);
      if (!el) throw new Error(`no dot for ${peerId}`);
      const style = getComputedStyle(el);
      return {
        x: style.getPropertyValue(xProp).trim(),
        y: style.getPropertyValue(yProp).trim(),
      };
    },
    [id, X_PROPERTY, Y_PROPERTY] as const,
  );
}


/**
 * Records every stale/live transition of the plane, from before the app boots.
 *
 * A reconnect is over in well under a second, so the stale window is far too
 * short to catch with a polled assertion — under load the check simply arrives
 * after it has passed. A MutationObserver installed before boot cannot miss it,
 * so the test asserts the recorded SEQUENCE instead of trying to be quick. Same
 * technique, and the same reason, as the drawer-peek test in mobile.spec.ts.
 */
async function recordStale(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const log: boolean[] = [];
    (window as unknown as { __staleLog: boolean[] }).__staleLog = log;
    const read = () => !!document.querySelector('[data-test="plane"][data-stale="1"]');
    let last: boolean | undefined;
    new MutationObserver(() => {
      const now = read();
      if (now !== last) {
        last = now;
        log.push(now);
      }
    }).observe(document.documentElement, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ['data-stale'],
    });
  });
}

/** The recorded stale/live transitions so far. */
function staleLog(page: Page): Promise<boolean[]> {
  return page.evaluate(() => (window as unknown as { __staleLog: boolean[] }).__staleLog ?? []);
}

const plane = (page: Page) => page.locator('[data-test="plane"]');
const dots = (page: Page) => page.locator('[data-test="peer"]');
/** One entity's face. On EVERY dot now, not only on your own. */
const face = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-face"]`);
/** One entity's name, if it has one — the element is absent when it does not. */
const nameTag = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-label"]`);

/**
 * The stylesheet's cap on a name's WIDTH, mirrored: `min(80px, 30cqw)` against
 * the plane's own box. Both branches matter — the px ceiling stops a name
 * growing past legible on a tablet, the percentage keeps it the same fraction of
 * the world on every device — so both are asserted, and whichever is smaller on
 * the viewport under test is the one actually in force.
 */
const LABEL_MAX_PX = 80;
const LABEL_MAX_PLANE_FRACTION = 0.3;

/**
 * Asserts one dot's name is no wider than the cap allows.
 *
 * This is the assertion that has teeth. A name is centred on its dot, so an
 * uncapped one covers every neighbour it reaches — which is what a player would
 * actually see, and which no page-level scroll check can detect: `.stage` clips
 * its own overflow and the root stylesheet sets `overflow-x: hidden`, so the
 * page cannot grow a scrollbar however wide a label gets.
 *
 * A pixel of slack on each bound, because a fractional layout size rounds.
 */
async function expectLabelWithinCap(page: Page, id: string): Promise<void> {
  const label = await nameTag(page, id).boundingBox();
  const box = await plane(page).boundingBox();
  expect(label, `no label box for ${id}`).not.toBeNull();
  expect(box, 'the plane has no box').not.toBeNull();
  const width = label?.width ?? 0;
  const planeWidth = box?.width ?? 1;
  expect(width, `${id}'s name is ${Math.round(width)}px wide`).toBeLessThanOrEqual(
    LABEL_MAX_PX + 1,
  );
  expect(
    width / planeWidth,
    `${id}'s name is ${Math.round((width / planeWidth) * 100)}% of the yard wide`,
  ).toBeLessThanOrEqual(LABEL_MAX_PLANE_FRACTION + 0.01);
}

/** Loads the game and steps past the intro into the yard. */
async function enterYard(page: Page): Promise<void> {
  await page.goto('/app/game-vanyagotchi');
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(plane(page)).toBeVisible();
}

test.describe('«Ванягоччи» — the shared plane', () => {
  test('the intro carries the fiction disclaimer before play begins', async ({ page }) => {
    // The game names real meme characters and parodies a real subculture, so the
    // disclaimer is a requirement rather than decoration — and it must be on the
    // surface that is read BEFORE play, not buried behind it.
    await stubBackend(page);
    await stubSocket(page);
    await page.goto('/app/game-vanyagotchi');

    await expect(
      page.getByText('Все персонажи вымышлены; любые совпадения с реальными людьми случайны.'),
    ).toBeVisible();
    await expectNoOverflow(page, 'vanyagotchi intro');

    if (isMobile(page)) {
      await expectTapTarget(page.getByRole('button', { name: 'Во двор' }), 'enter-yard CTA');
    }
  });

  test('the disclaimer is never the thing squeezed off a short screen', async ({ page }) => {
    // It sits in a fixed-size row, never in the one flexible child, so shrinking
    // the viewport takes height from the artwork and not from the legal line.
    await stubBackend(page);
    await stubSocket(page);
    await page.setViewportSize({ width: 320, height: 568 });
    await page.goto('/app/game-vanyagotchi');

    const disclaimer = page.getByText(
      'Все персонажи вымышлены; любые совпадения с реальными людьми случайны.',
    );
    await expect(disclaimer).toBeVisible();
    const box = await disclaimer.boundingBox();
    expect(box).not.toBeNull();
    expect((box?.y ?? 0) + (box?.height ?? 0)).toBeLessThanOrEqual(568);
    await expectNoOverflow(page, 'vanyagotchi intro at 320x568');
  });

  test('peers from a roster frame appear on the plane at their coordinates', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster({ id: 'peer-a', x: 0.25, y: 0.75 }, { id: 'peer-b', x: 0.5, y: 0.5 }),
    );

    await expect(dots(page)).toHaveCount(2);
    // The position lives in the custom properties and nowhere else — no inline
    // transform, no left/top. Reading them back is how we know the frame
    // actually reached the DOM.
    expect(await peerPosition(page, 'peer-a')).toEqual({ x: '0.25', y: '0.75' });
    expect(await peerPosition(page, 'peer-b')).toEqual({ x: '0.5', y: '0.5' });
    await expect(page.getByText('во дворе: 2')).toBeVisible();
  });

  test('a later frame moves a peer without re-creating it', async ({ page }) => {
    // The membership tier must not churn when only positions changed: the dot
    // has to be the same DOM node, or every frame would be re-keying the list.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(roster({ id: 'peer-a', x: 0.1, y: 0.1 }));
    await expect(dots(page)).toHaveCount(1);
    await page.evaluate(() => {
      document.querySelector('[data-peer="peer-a"]')?.setAttribute('data-marked', '1');
    });

    await socket.push(roster({ id: 'peer-a', x: 0.9, y: 0.2 }));
    await expect
      .poll(async () => (await peerPosition(page, 'peer-a')).x)
      .toBe('0.9');
    await expect(page.locator('[data-peer="peer-a"][data-marked="1"]')).toHaveCount(1);
  });

  test('a peer that leaves the frame leaves the plane', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(roster({ id: 'peer-a', x: 0.2, y: 0.2 }, { id: 'peer-b', x: 0.8, y: 0.8 }));
    await expect(dots(page)).toHaveCount(2);

    await socket.push(roster({ id: 'peer-a', x: 0.2, y: 0.2 }));
    await expect(dots(page)).toHaveCount(1);
    await expect(page.getByText('во дворе: 1')).toBeVisible();
  });

  test('tapping the plane sends one normalised move, and nothing moves locally', async ({
    page,
  }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'me', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    const box = await plane(page).boundingBox();
    expect(box).not.toBeNull();
    // A quarter across, three quarters down.
    await page.mouse.click(
      (box?.x ?? 0) + (box?.width ?? 0) * 0.25,
      (box?.y ?? 0) + (box?.height ?? 0) * 0.75,
    );

    // Exactly one move — the hello sent on open is not one of them.
    await expect.poll(() => socket.sent.filter((m) => m.t === TYPE_MOVE).length).toBe(1);
    const move = socket.sent.filter((m) => m.t === TYPE_MOVE)[0];
    expect(move.x as number).toBeCloseTo(0.25, 1);
    expect(move.y as number).toBeCloseTo(0.75, 1);

    // The server owns the position: the dot must NOT have moved optimistically.
    expect(await peerPosition(page, 'me')).toEqual({ x: '0.5', y: '0.5' });
  });

  test('the plane never scrolls, and the status row stays on screen', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(
      roster(
        ...Array.from({ length: 12 }, (_, i) => ({
          id: `peer-${i}`,
          x: (i % 4) / 3,
          y: Math.floor(i / 4) / 2,
        })),
      ),
    );
    await expect(dots(page)).toHaveCount(12);

    await expectNoOverflow(page, 'vanyagotchi yard');
    const scrolls = await page.evaluate(
      () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
    );
    expect(scrolls, 'the play screen must never scroll vertically either').toBeLessThanOrEqual(1);

    // The status row is the fixed-size child; a crowded plane must not push it off.
    const hud = page.getByText(/во дворе:/);
    await expect(hud).toBeVisible();
    const hudBox = await hud.boundingBox();
    const viewportHeight = page.viewportSize()?.height ?? 0;
    expect((hudBox?.y ?? 0) + (hudBox?.height ?? 0)).toBeLessThanOrEqual(viewportHeight);
  });

  test('a dot is a full-size tap target even though the plane takes the tap', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    if (isMobile(page)) {
      // The 44 px rule binds the sprite size, which is what Phase 2's depth
      // scaling has to stay above. Checked now so the constraint is already
      // pinned when scaling arrives.
      const box = await dots(page).first().boundingBox();
      expect(box).not.toBeNull();
      expect(Math.round(Math.min(box?.width ?? 0, box?.height ?? 0))).toBeGreaterThanOrEqual(44);
    }
  });

  test('a close tells the player which of the three things happened', async ({ page }) => {
    // The reason arrives as a bye FRAME, not a close code — a browser reports
    // 1006 for every disconnect. Showing "доступ отозван" rather than a generic
    // failure is the entire payoff of that frame existing.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 4001, reason: 'unauthorized' });

    await expect(page.getByText('доступ отозван')).toBeVisible();
    // And the world empties rather than freezing on a snapshot that is no
    // longer being updated.
    await expect(dots(page)).toHaveCount(0);
  });

  test('a plain drop says so without inventing a reason', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.drop();
    await expect(page.getByText('связь потеряна')).toBeVisible();
  });

  test('the nav offers the yard', async ({ page }) => {
    await stubBackend(page);
    await stubSocket(page);
    await page.goto('/app/wishlist');

    const link = page.getByRole('link', { name: 'Ванягоччи' });
    await expect(link).toBeVisible();
    if (isMobile(page)) {
      await expectTapTarget(link, 'Ванягоччи nav link');
    }
  });
});

test.describe('«Ванягоччи» — surviving a restart', () => {
  test('the player can tell which Ваня is theirs', async ({ page }) => {
    // The id on the wire is a per-account pseudonym the server derives, not
    // anything the client already knows, so the only way to find yourself is to
    // ask. The client says hello on every open and the server answers.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await expect.poll(() => socket.sent.some((m) => m.t === TYPE_HELLO)).toBe(true);

    await socket.push(JSON.stringify({ t: TYPE_YOU, id: 'me' }));
    await socket.push(roster({ id: 'me', x: 0.3, y: 0.3 }, { id: 'other', x: 0.7, y: 0.7 }));

    await expect(dots(page)).toHaveCount(2);
    await expect(page.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);
    await expect(page.locator('[data-peer="me"][data-you="1"]')).toHaveCount(1);
  });

  test('a reconnect does not throw the player back to the intro', async ({ page }) => {
    await recordStale(page);
    // Losing the socket is the normal case — the service restarts several times
    // a day. Being bounced back to a splash screen with a "Во двор" button every
    // time would be worse than the disconnect.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.4, y: 0.4 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 1001, reason: 'restart' });

    // Still in the yard, with the plane on screen and the intro CTA gone.
    await expect(page.locator('[data-test="plane"]')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Во двор' })).toHaveCount(0);

    // And it really does come back, without anybody clicking anything.
    await socket.waitForConnections(2);
    await socket.push(roster({ id: 'peer-a', x: 0.6, y: 0.6 }));
    await expect(dots(page)).toHaveCount(1);
    await expect(page.getByText('на связи')).toBeVisible();

    // Whether the stale state was ever painted depends on how fast the
    // reconnect was — a quick one is coalesced away inside a single Vue flush,
    // which is a feature. What must hold either way is that it ended live and
    // never went stale-and-stayed-there.
    const log = await staleLog(page);
    expect(log.at(-1) ?? false, `stale transitions: ${JSON.stringify(log)}`).toBe(false);
  });

  test('it says hello again after reconnecting, because the pseudonym changed', async ({
    page,
  }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await expect.poll(() => socket.sent.filter((m) => m.t === TYPE_HELLO).length).toBe(1);

    await socket.drop({ code: 1001, reason: 'restart' });
    await socket.waitForConnections(2);

    await expect
      .poll(() => socket.sent.filter((m) => m.t === TYPE_HELLO).length, { timeout: 15_000 })
      .toBe(2);
  });

  test('a revoked session stops for good instead of hammering the handshake', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 4001, reason: 'unauthorized' });

    await expect(page.getByText('доступ отозван')).toBeVisible();
    await expect(dots(page)).toHaveCount(0);
    // The whole point: no retry. A blocked account must not sit there
    // reconnecting until the tab is closed.
    await page.waitForTimeout(3_000);
    expect(socket.connections()).toBe(1);
  });
});

test.describe('«Ванягоччи» — the shape of the world, and losing it briefly', () => {
  test('a blip does not empty the yard, it marks it stale', async ({ page }) => {
    await recordStale(page);
    // The bug this fixes, reported from a phone: "all disappeared". A mobile
    // socket drops constantly — a tunnel, a lock screen, a cell handover — and
    // clearing the plane the instant it did made every one of those look like
    // everybody had left. The dots stay, visibly stale, across a reconnect.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.3, y: 0.3 }, { id: 'peer-b', x: 0.7, y: 0.7 }));
    await expect(dots(page)).toHaveCount(2);

    // Keep it down, so the disconnected state is something a browser can be
    // asked about rather than a moment that has already passed.
    socket.holdDown(true);
    await socket.drop({ code: 1001, reason: 'restart' });

    // The world is still on screen — this is the whole bug — and it says so.
    await expect(page.locator('[data-test="plane"][data-stale="1"]')).toHaveCount(1);
    await expect(dots(page)).toHaveCount(2);
    await expect(page.getByText(/переподключаемся/)).toBeVisible();

    // And the staleness lifts by itself when the socket comes back. `push`
    // already waits for a socket to exist, so it is the readiness gate here —
    // counting connections would be counting the refusals too.
    socket.holdDown(false);
    await socket.push(roster({ id: 'peer-a', x: 0.3, y: 0.3 }, { id: 'peer-b', x: 0.7, y: 0.7 }));
    await expect(page.locator('[data-test="plane"][data-stale="1"]')).toHaveCount(0);
    await expect(dots(page)).toHaveCount(2);
    await expect(page.getByText('на связи')).toBeVisible();
  });

  test('a revoked session empties the yard at once, because nothing is coming back', async ({
    page,
  }) => {
    // The other side of the rule above: holding a world is only honest while a
    // reconnect is still possible.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.3, y: 0.3 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 4001, reason: 'unauthorized' });

    await expect(dots(page)).toHaveCount(0);
    await expect(page.locator('[data-test="plane"][data-stale="1"]')).toHaveCount(0);
  });

  test('the yard is the same shape on every device', async ({ page }) => {
    // Coordinates are normalised per axis, so a plane that took whatever space
    // was left would give a phone a tall world and a tablet a wide one — the
    // same coordinates, different distances between them. Distance becomes a
    // mechanic in Phase 2 (the beer delivery is a race to arrive), so the shape
    // has to be the same for everybody.
    await stubBackend(page);
    await stubSocket(page);
    await enterYard(page);

    const box = await plane(page).boundingBox();
    expect(box).not.toBeNull();
    const ratio = (box?.width ?? 0) / (box?.height ?? 1);
    expect(ratio, `plane is ${box?.width}x${box?.height}, ratio ${ratio}`).toBeCloseTo(3 / 4, 1);

    // And it still fits: no scrolling, at any of the viewports this suite runs.
    await expectNoOverflow(page, 'vanyagotchi yard shape');
    const vScroll = await page.evaluate(
      () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
    );
    expect(vScroll).toBeLessThanOrEqual(1);
  });
});

test.describe('«Ванягоччи» — a yard of people rather than a field of dots', () => {
  // Until this iteration a roster entry was three fields — an id and a pair of
  // coordinates — and the plane drew everybody as an identical coloured circle.
  // The frame now also says what each entity LOOKS like, and it says it about
  // every entity rather than only about the caller's own, which is the
  // difference between one shared world and every player watching a private one.
  //
  // These tests deliberately drive appearance through the SOCKET and assert on a
  // peer that is NOT yours. Everything about a neighbour's Ваня — his skin, his
  // name, how he is doing — can only have come off the wire: this screen holds
  // no state about anybody else and could not derive it if it wanted to.

  test('every dot wears its own face and its own condition, not just yours', async ({ page }) => {
    // Three entities, three different poses, and the two that matter are the
    // ones that are not us. A screen that painted its own pet's condition onto
    // the yard — the obvious shortcut, and the one that makes two players see
    // two different worlds — would give all three the same face.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(JSON.stringify({ t: TYPE_YOU, id: 'me' }));
    await socket.push(
      roster(
        { id: 'me', x: 0.5, y: 0.5, art: 'vanya', label: 'я', pose: 'fine' },
        { id: 'neighbour', x: 0.2, y: 0.3, art: 'vanya', label: 'сосед', pose: 'poorly' },
        { id: 'goner', x: 0.8, y: 0.7, art: 'vanya', label: 'бедолага', pose: 'dead' },
      ),
    );

    await expect(dots(page)).toHaveCount(3);
    // A face per dot — the count is the assertion that this is no longer a
    // privilege of the caller's own entity.
    await expect(page.locator('[data-test="peer-face"]')).toHaveCount(3);

    await expect(face(page, 'me')).toHaveAttribute('data-condition', 'fine');
    await expect(face(page, 'neighbour')).toHaveAttribute('data-condition', 'poorly');
    await expect(face(page, 'goner')).toHaveAttribute('data-condition', 'dead');

    // And each name is on its own dot rather than all of them on one.
    await expect(nameTag(page, 'neighbour')).toHaveText('сосед');
    await expect(nameTag(page, 'goner')).toHaveText('бедолага');
    await expect(page.locator('[data-test="peer-label"]')).toHaveCount(3);
  });

  test('an unnamed Ваня gets no label element, and never the word "undefined"', async ({
    page,
  }) => {
    // Most Ваняs have no name — naming one is a dialog nobody has opened yet —
    // so "no name" is the common case rather than an edge one. It is one state
    // at every layer: the field is absent from the frame, `capLabel` answers
    // undefined, and the template's `v-if` renders no element at all. The bug
    // this forecloses is the classic one, where a missing name reaches the DOM
    // as the four-letter string a template interpolated out of nothing.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster(
        { id: 'named', x: 0.3, y: 0.3, art: 'vanya', label: 'Ваня', pose: 'fine' },
        { id: 'nameless', x: 0.7, y: 0.7, art: 'vanya', pose: 'fine' },
      ),
    );
    await expect(dots(page)).toHaveCount(2);

    await expect(nameTag(page, 'named')).toHaveText('Ваня');

    // Checked before the element count, so that this assertion is the one a
    // stringifying `capLabel` trips over rather than being shadowed by it.
    await expect(page.locator('[data-peer="nameless"]')).not.toContainText('undefined');
    await expect(plane(page)).not.toContainText('undefined');

    // Absent, not empty: nothing is rendered and nothing occupies the space.
    await expect(nameTag(page, 'nameless')).toHaveCount(0);
    await expect(page.locator('[data-test="peer-label"]')).toHaveCount(1);
    // He is still drawn, with a face — a nameless entity is not a broken one.
    await expect(face(page, 'nameless')).toBeVisible();
  });

  test('a change of pose lands on the dot that is already there', async ({ page }) => {
    // The same guarantee the position tier has, for the tier above it. Poses
    // arrive inside a frame that also carries coordinates, five times a second,
    // and re-keying the list on one would throw away every element on the plane
    // — taking the imperatively-written positions with it and restarting the
    // dot's CSS transition from wherever it happened to be. The marker is how a
    // browser can be asked whether this is literally the same node.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(roster({ id: 'peer-a', x: 0.4, y: 0.4, art: 'vanya', label: 'Ваня', pose: 'fine' }));
    await expect(face(page, 'peer-a')).toHaveAttribute('data-condition', 'fine');
    await page.evaluate(() => {
      document.querySelector('[data-peer="peer-a"]')?.setAttribute('data-marked', '1');
    });

    // Same entity, same place, worse day.
    await socket.push(roster({ id: 'peer-a', x: 0.4, y: 0.4, art: 'vanya', label: 'Ваня', pose: 'dead' }));

    await expect(face(page, 'peer-a')).toHaveAttribute('data-condition', 'dead');
    await expect(page.locator('[data-peer="peer-a"][data-marked="1"]')).toHaveCount(1);
    // The position survived with it, which is the thing a re-keyed list would
    // have silently dropped on the floor.
    expect(await peerPosition(page, 'peer-a')).toEqual({ x: '0.4', y: '0.4' });
  });

  test('long names cost the yard nothing, and never grow past their cap', async ({ page }) => {
    // A busy yard of long names is the case that breaks this screen. Three
    // independent caps stop it — `capLabel` bounds the STRING, the stylesheet
    // bounds the WIDTH, and the plane's `overflow: hidden` is the backstop — and
    // this asserts the two that are load-bearing rather than the backstop.
    //
    // The page-scroll checks below are kept as regression guards, but they are
    // deliberately not the point: `.stage` clips its own overflow and
    // styles/mobile.css puts `overflow-x: hidden` on the root, so a runaway name
    // would be swallowed twice over before it ever reached a scrollbar. What
    // WOULD be visible to a player is a name spilling across the neighbours, and
    // that is a measurement of the label's own box.
    //
    // Dots deliberately ON the edges: a name is centred on its dot, so one at
    // x=0 or x=1 hangs half its width past the plane. That is left clipped on
    // purpose (a drifted name reads as belonging to the dot next to it, which is
    // worse than a cut one), so what is asserted is the width, not containment.
    const LONG = 'Владислав-Афанасий Многобуквенный из Химок';
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    // The yard's geometry before a single name is in it, so the comparison
    // afterwards is against this screen rather than against a hardcoded size.
    const emptyPlane = await plane(page).boundingBox();
    expect(emptyPlane, 'the plane has no box').not.toBeNull();

    await socket.push(
      roster(
        { id: 'left', x: 0, y: 0.5, art: 'vanya', label: LONG, pose: 'fine' },
        { id: 'right', x: 1, y: 0.5, art: 'vanya', label: LONG, pose: 'poorly' },
        { id: 'top', x: 0.5, y: 0, art: 'vanya', label: LONG, pose: 'fine' },
        { id: 'bottom', x: 0.5, y: 1, art: 'vanya', label: LONG, pose: 'dead' },
        ...Array.from({ length: 4 }, (_, i) => ({
          id: `crowd-${i}`,
          x: 0.3 + i * 0.1,
          y: 0.4,
          art: 'vanya',
          label: LONG,
          pose: 'fine',
        })),
      ),
    );
    await expect(dots(page)).toHaveCount(8);

    // THE assertion: a name is a fraction of the world wide, not a third of the
    // screen. Without the width cap this label measures the whole 42 characters
    // and covers every neighbour it passes.
    await expectLabelWithinCap(page, 'left');
    await expectLabelWithinCap(page, 'right');

    // And the yard did not move to accommodate any of them — same box, to the
    // pixel. A label that could stretch its parent would show up here as a
    // plane that had shrunk or changed shape.
    const namedPlane = await plane(page).boundingBox();
    expect(namedPlane?.width, 'the plane changed width once names arrived').toBeCloseTo(
      emptyPlane?.width ?? 0,
      1,
    );
    expect(namedPlane?.height, 'the plane changed height once names arrived').toBeCloseTo(
      emptyPlane?.height ?? 0,
      1,
    );

    await expectNoOverflow(page, 'vanyagotchi yard full of long names');
    await expectNoVerticalScroll(page, 'vanyagotchi yard full of long names');
    // The status row is the fixed child; a plane stretched by a name would be
    // the thing that pushed it off.
    const hud = page.getByText(/во дворе:/);
    await expect(hud).toBeVisible();
    const hudBox = await hud.boundingBox();
    const viewportHeight = page.viewportSize()?.height ?? 0;
    expect((hudBox?.y ?? 0) + (hudBox?.height ?? 0)).toBeLessThanOrEqual(viewportHeight);

    // And the STRING was cut, not merely hidden by CSS — so nothing downstream
    // (an aria label, a tooltip, anything added later) has to re-derive the cap,
    // and a server that never validated a name cannot put a kilobyte of it in
    // the DOM. Counted in code points, because that is the unit the cap is in:
    // cutting UTF-16 in the middle of a surrogate pair is how the same field
    // grows a replacement character the first time somebody uses an emoji.
    const shown = (await nameTag(page, 'left').textContent()) ?? '';
    expect([...shown].length, `label rendered ${shown.length} units: ${shown}`).toBeLessThanOrEqual(
      LABEL_MAX,
    );
    expect([...LONG].length, 'the fixture is no longer past the cap').toBeGreaterThan(LABEL_MAX);
  });

  test('long names still fit the smallest screen we support', async ({ page }) => {
    // 320x568 is the floor, and it is the width at which the cap's OTHER branch
    // is the one in force: the plane is about 231px across there, so `30cqw` is
    // ~69px and wins the `min()` against the 80px ceiling. That is the whole
    // point of sizing a name in the world's units before the device's — a name
    // that is a third of the yard wide on a phone and a seventh on a tablet is
    // two different games — and this is the only viewport in the suite that
    // exercises it.
    //
    // Set before `goto` rather than resized afterwards, so the layout is built
    // for this size rather than reflowed into it.
    await page.setViewportSize({ width: 320, height: 568 });
    const LONG = 'Владислав-Афанасий Многобуквенный из Химок';
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster(
        ...Array.from({ length: 8 }, (_, i) => ({
          id: `peer-${i}`,
          x: (i % 4) / 3,
          y: Math.floor(i / 4),
          art: 'vanya',
          label: LONG,
          pose: (['fine', 'poorly', 'dead'] as const)[i % 3],
        })),
      ),
    );
    await expect(dots(page)).toHaveCount(8);

    await expectLabelWithinCap(page, 'peer-0');
    await expectLabelWithinCap(page, 'peer-3');
    await expectNoOverflow(page, 'vanyagotchi yard of long names at 320x568');
    await expectNoVerticalScroll(page, 'vanyagotchi yard of long names at 320x568');
    // The plane was not squeezed to nothing to make room for the names either.
    const box = await plane(page).boundingBox();
    expect(box?.height ?? 0, 'the plane collapsed under a crowd of names').toBeGreaterThan(120);
  });
});
