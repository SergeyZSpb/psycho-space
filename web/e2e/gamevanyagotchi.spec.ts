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

interface Peer {
  id: string;
  x: number;
  y: number;
}

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

  await page.routeWebSocket('**/api/realtime*', (route) => {
    ws = route;
    count += 1;
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

const plane = (page: Page) => page.locator('[data-test="plane"]');
const dots = (page: Page) => page.locator('[data-test="peer"]');

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
    await expect(page.getByText(/переподключаемся/)).toBeVisible();

    // And it really does come back, without anybody clicking anything.
    await socket.waitForConnections(2);
    await socket.push(roster({ id: 'peer-a', x: 0.6, y: 0.6 }));
    await expect(dots(page)).toHaveCount(1);
    await expect(page.getByText('на связи')).toBeVisible();
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
